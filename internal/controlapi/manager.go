package controlapi

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const (
	defaultInputReadTimeout = 20 * time.Millisecond
	defaultReconnectDelay   = time.Second
	commandQueueSize        = 64
)

type hardwareDevice interface {
	Info() (streamdeck.DeviceInfo, error)
	ReadEvents(time.Duration) (streamdeck.InputRead, error)
	SetBrightness(int) error
	SetKeyImage(int, image.Image) error
	Close() error
}

type plusHardwareDevice interface {
	hardwareDevice
	SetLCDImage(image.Image) error
	SetTouchStripImage(image.Image) error
	SetPartialWindowImage(int, int, image.Image) error
}

type openDeviceFunc func() (hardwareDevice, error)

type commandRequest struct {
	requestID  string
	operations []operation
	reply      chan CommandAck
}

type desiredImage struct {
	image    *image.NRGBA
	revision string
}

type desiredHardwareState struct {
	brightness *int
	keys       map[int]desiredImage
	lcd        *desiredImage
	strip      *desiredImage
}

// Manager is the single lifecycle and output owner of the HID handle. HTTP
// handlers submit validated operations through its queue. A dedicated reader
// keeps physical input flowing while a large JPEG is being uploaded.
type Manager struct {
	model          streamdeck.Model
	open           openDeviceFunc
	reconnectDelay time.Duration
	commands       chan commandRequest
	broker         *eventBroker

	mu       sync.RWMutex
	snapshot Snapshot

	desired  desiredHardwareState
	sequence uint64
}

func NewManager(model streamdeck.Model) *Manager {
	return newManager(model, func() (hardwareDevice, error) {
		return streamdeck.OpenModel(model)
	})
}

func newManager(model streamdeck.Model, open openDeviceFunc) *Manager {
	return &Manager{
		model:          model,
		open:           open,
		reconnectDelay: defaultReconnectDelay,
		commands:       make(chan commandRequest, commandQueueSize),
		broker:         newEventBroker(),
		desired:        desiredHardwareState{keys: make(map[int]desiredImage)},
		snapshot: Snapshot{
			APIVersion:   APIVersion,
			Capabilities: capabilitiesFor(model),
			Desired:      DesiredState{Keys: make(map[int]string)},
		},
	}
}

func (m *Manager) Model() streamdeck.Model {
	return m.model
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.snapshot
	result.Desired.Brightness = cloneInt(m.snapshot.Desired.Brightness)
	result.Desired.Keys = cloneKeyRevisions(m.snapshot.Desired.Keys)
	if m.snapshot.Device != nil {
		device := *m.snapshot.Device
		result.Device = &device
	}
	return result
}

func (m *Manager) Subscribe() (<-chan Event, func()) {
	return m.broker.subscribe()
}

func (m *Manager) Submit(ctx context.Context, requestID string, operations []operation) (CommandAck, error) {
	reply := make(chan CommandAck, 1)
	request := commandRequest{requestID: requestID, operations: operations, reply: reply}
	select {
	case m.commands <- request:
	case <-ctx.Done():
		return CommandAck{}, ctx.Err()
	}

	select {
	case ack := <-reply:
		return ack, nil
	case <-ctx.Done():
		return CommandAck{}, ctx.Err()
	}
}

// Run opens the configured deck, restores accepted desired state after every
// reconnect, dispatches commands, and publishes normalized physical input.
func (m *Manager) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		deck, err := m.open()
		if err != nil {
			m.setDisconnected(err)
			if !m.waitDisconnected(ctx) {
				return nil
			}
			continue
		}

		info, err := deck.Info()
		if err == nil {
			err = m.restore(deck)
		}
		if err != nil {
			_ = deck.Close()
			m.setDisconnected(err)
			if !m.waitDisconnected(ctx) {
				return nil
			}
			continue
		}

		m.setConnected(info)
		err = m.runConnected(ctx, deck)
		closeErr := deck.Close()
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = errors.New("device connection ended")
		}
		m.setDisconnected(err)
		if !m.waitDisconnected(ctx) {
			return nil
		}
	}
}

func (m *Manager) waitDisconnected(ctx context.Context) bool {
	timer := time.NewTimer(m.reconnectDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case request := <-m.commands:
			m.accept(request, nil)
		case <-timer.C:
			return true
		}
	}
}

func (m *Manager) runConnected(ctx context.Context, deck hardwareDevice) error {
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputErrors := m.startInputReader(connectionCtx, deck)
	for {
		select {
		case <-ctx.Done():
			return nil
		case request := <-m.commands:
			if err := m.accept(request, deck); err != nil {
				return err
			}
		case err := <-inputErrors:
			return err
		}
	}
}

func (m *Manager) startInputReader(ctx context.Context, deck hardwareDevice) <-chan error {
	errorResults := make(chan error, 1)
	go func() {
		for {
			input, err := deck.ReadEvents(defaultInputReadTimeout)
			if errors.Is(err, streamdeck.ErrTimeout) {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if err != nil {
				select {
				case errorResults <- err:
				case <-ctx.Done():
				}
				return
			}
			m.publishInput(input)
		}
	}()
	return errorResults
}

func (m *Manager) publishInput(input streamdeck.InputRead) {
	for _, event := range input.Events {
		if wire, ok := m.wireEvent(event); ok {
			m.broker.publish(wire)
		}
	}
	for _, diagnostic := range input.Diagnostics {
		m.publish("diagnostic", func(event *Event) { event.Message = diagnostic })
	}
}

func (m *Manager) accept(request commandRequest, deck hardwareDevice) error {
	normalized := m.desired.accept(request.operations, m.nextGeneration())
	m.updateDesiredSnapshot()

	ack := CommandAck{
		APIVersion: APIVersion,
		RequestID:  request.requestID,
		Generation: m.generation(),
		Connected:  deck != nil,
	}
	if deck == nil {
		ack.Status = "queued"
		ack.Message = "device is disconnected; desired state will be restored on reconnect"
		request.reply <- ack
		return nil
	}

	for _, operation := range normalized {
		if err := applyOperation(deck, operation); err != nil {
			ack.Status = "queued"
			ack.Connected = false
			ack.Message = "hardware write failed; desired state retained for reconnect"
			request.reply <- ack
			return err
		}
	}
	ack.Status = "applied"
	request.reply <- ack
	return nil
}

func (m *Manager) restore(deck hardwareDevice) error {
	if m.desired.brightness != nil {
		if err := deck.SetBrightness(*m.desired.brightness); err != nil {
			return fmt.Errorf("restore brightness: %w", err)
		}
	}
	if m.desired.lcd != nil {
		plus, ok := deck.(plusHardwareDevice)
		if !ok {
			return fmt.Errorf("restore LCD: connected device does not support Plus output")
		}
		if err := plus.SetLCDImage(m.desired.lcd.image); err != nil {
			return fmt.Errorf("restore LCD: %w", err)
		}
	}
	for index := 0; index < m.model.KeyCount(); index++ {
		key, ok := m.desired.keys[index]
		if !ok {
			continue
		}
		if err := deck.SetKeyImage(index, key.image); err != nil {
			return fmt.Errorf("restore key %d: %w", index, err)
		}
	}
	if m.desired.strip != nil {
		plus, ok := deck.(plusHardwareDevice)
		if !ok {
			return fmt.Errorf("restore touch strip: connected device does not support Plus output")
		}
		if err := plus.SetTouchStripImage(m.desired.strip.image); err != nil {
			return fmt.Errorf("restore touch strip: %w", err)
		}
	}
	return nil
}

func applyOperation(deck hardwareDevice, operation operation) error {
	switch operation.typeName {
	case CommandSetBrightness:
		if err := deck.SetBrightness(operation.brightness); err != nil {
			return fmt.Errorf("set brightness: %w", err)
		}
	case CommandSetKeyImage:
		if err := deck.SetKeyImage(operation.index, operation.image); err != nil {
			return fmt.Errorf("set key %d image: %w", operation.index, err)
		}
	case CommandSetLCDImage:
		plus, ok := deck.(plusHardwareDevice)
		if !ok {
			return fmt.Errorf("connected device does not support full LCD output")
		}
		if err := plus.SetLCDImage(operation.image); err != nil {
			return fmt.Errorf("set LCD image: %w", err)
		}
	case CommandSetTouchStrip:
		plus, ok := deck.(plusHardwareDevice)
		if !ok {
			return fmt.Errorf("connected device does not support touch-strip output")
		}
		if err := plus.SetTouchStripImage(operation.image); err != nil {
			return fmt.Errorf("set touch-strip image: %w", err)
		}
	case CommandSetTouchRegion:
		plus, ok := deck.(plusHardwareDevice)
		if !ok {
			return fmt.Errorf("connected device does not support touch-strip output")
		}
		if err := plus.SetPartialWindowImage(operation.x, operation.y, operation.image); err != nil {
			return fmt.Errorf("set touch-strip region: %w", err)
		}
	default:
		return fmt.Errorf("unsupported operation %q", operation.typeName)
	}
	return nil
}

// accept updates the deterministic reconnect state and returns the hardware
// operations to perform now. A first partial strip update becomes one complete
// black-backed strip frame so current and reconnect state cannot diverge.
func (s *desiredHardwareState) accept(operations []operation, generation uint64) []operation {
	normalized := make([]operation, 0, len(operations))
	for _, current := range operations {
		switch current.typeName {
		case CommandSetBrightness:
			value := current.brightness
			s.brightness = &value
			normalized = append(normalized, current)
		case CommandSetKeyImage:
			s.keys[current.index] = desiredImage{image: cloneImage(current.image), revision: current.revision}
			normalized = append(normalized, current)
		case CommandSetLCDImage:
			frame := desiredImage{image: cloneImage(current.image), revision: current.revision}
			s.lcd = &frame
			// A complete LCD frame is authoritative for the upper display. Key
			// images sent after it remain explicit overlays.
			clear(s.keys)
			normalized = append(normalized, current)
		case CommandSetTouchStrip:
			frame := desiredImage{image: cloneImage(current.image), revision: current.revision}
			s.strip = &frame
			normalized = append(normalized, current)
		case CommandSetTouchRegion:
			first := s.strip == nil
			if first {
				base := image.NewNRGBA(image.Rect(0, 0, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight))
				draw.Draw(base, base.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
				s.strip = &desiredImage{image: base}
			}
			draw.Draw(s.strip.image, image.Rect(current.x, current.y, current.x+current.image.Bounds().Dx(), current.y+current.image.Bounds().Dy()), current.image, current.image.Bounds().Min, draw.Src)
			s.strip.revision = fmt.Sprintf("generation:%d", generation)
			if first {
				normalized = append(normalized, operation{typeName: CommandSetTouchStrip, image: cloneImage(s.strip.image), revision: s.strip.revision})
			} else {
				normalized = append(normalized, current)
			}
		}
	}
	return normalized
}

func (m *Manager) nextGeneration() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot.Generation++
	return m.snapshot.Generation
}

func (m *Manager) generation() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot.Generation
}

func (m *Manager) updateDesiredSnapshot() {
	desired := DesiredState{Keys: make(map[int]string, len(m.desired.keys))}
	if m.desired.brightness != nil {
		value := *m.desired.brightness
		desired.Brightness = &value
	}
	for index, key := range m.desired.keys {
		desired.Keys[index] = key.revision
	}
	if m.desired.lcd != nil {
		desired.LCDRevision = m.desired.lcd.revision
	}
	if m.desired.strip != nil {
		desired.TouchStripRevision = m.desired.strip.revision
	}
	m.mu.Lock()
	m.snapshot.Desired = desired
	m.mu.Unlock()
}

func (m *Manager) setConnected(info streamdeck.DeviceInfo) {
	device := connectedDevice(info)
	m.mu.Lock()
	m.snapshot.Connected = true
	m.snapshot.Device = &device
	m.snapshot.LastError = ""
	m.mu.Unlock()
	m.publish("device_connected", func(event *Event) { event.Device = &device })
}

func (m *Manager) setDisconnected(err error) {
	m.mu.Lock()
	wasConnected := m.snapshot.Connected
	m.snapshot.Connected = false
	m.snapshot.Device = nil
	if err != nil {
		m.snapshot.LastError = err.Error()
	}
	m.mu.Unlock()
	if wasConnected {
		m.publish("device_disconnected", func(event *Event) {
			if err != nil {
				event.Message = err.Error()
			}
		})
	}
}

func (m *Manager) wireEvent(input streamdeck.Event) (Event, bool) {
	var event Event
	switch input := input.(type) {
	case streamdeck.KeyEvent:
		event.Kind = "key"
		event.Index = intPointer(input.Key)
		event.Pressed = boolPointer(input.Pressed)
	case streamdeck.DialPressEvent:
		event.Kind = "dial_press"
		event.Index = intPointer(input.Dial)
		event.Pressed = boolPointer(input.Pressed)
	case streamdeck.DialRotateEvent:
		event.Kind = "dial_rotate"
		event.Index = intPointer(input.Dial)
		event.Delta = intPointer(input.Delta)
	case streamdeck.TouchEvent:
		switch input.Kind {
		case streamdeck.TouchTap:
			event.Kind = "touch_tap"
			event.X, event.Y = intPointer(input.X), intPointer(input.Y)
		case streamdeck.TouchPress:
			event.Kind = "touch_press"
			event.X, event.Y = intPointer(input.X), intPointer(input.Y)
		case streamdeck.TouchFlick:
			event.Kind = "touch_flick"
			event.StartX, event.StartY = intPointer(input.StartX), intPointer(input.StartY)
			event.EndX, event.EndY = intPointer(input.EndX), intPointer(input.EndY)
		default:
			return Event{}, false
		}
	default:
		return Event{}, false
	}
	m.stamp(&event)
	return event, true
}

func (m *Manager) publish(kind string, populate func(*Event)) {
	event := Event{Kind: kind}
	populate(&event)
	m.stamp(&event)
	m.broker.publish(event)
}

func (m *Manager) stamp(event *Event) {
	m.mu.Lock()
	m.sequence++
	event.Sequence = m.sequence
	m.mu.Unlock()
	event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
}

func connectedDevice(info streamdeck.DeviceInfo) ConnectedDevice {
	return ConnectedDevice{
		VendorID:     info.VendorID,
		ProductID:    info.ProductID,
		Manufacturer: info.Manufacturer,
		Product:      info.Product,
	}
}

func cloneImage(source *image.NRGBA) *image.NRGBA {
	if source == nil {
		return nil
	}
	result := image.NewNRGBA(source.Bounds())
	copy(result.Pix, source.Pix)
	return result
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneKeyRevisions(source map[int]string) map[int]string {
	result := make(map[int]string, len(source))
	for index, revision := range source {
		result[index] = revision
	}
	return result
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }
