package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const (
	inputReadTimeout  = 50 * time.Millisecond
	reconnectInterval = time.Second
)

var displayModes = [...]string{"INPUT", "KEY TEST", "TOUCH TEST"}

type demoState struct {
	keys        [streamdeck.KeyCount]bool
	counter     int
	brightness  int
	selectedKey int
	mode        int
	latestInput string
	latestTouch *streamdeck.TouchEvent

	remoteTheme      string
	remoteTitle      string
	remoteMessage    string
	remoteEventsSeen int
	remoteLast       string

	// revision-2 bridge mode: the controller owns all state and semantics. The deck is
	// a pure renderer - it paints exactly what the server sends (GET poll and
	// POST acks) and never interprets a key/dial/flick meaning locally.
	bridge      bool
	activeKey   *remoteKey
	activeStrip *remoteStrip
	background  []remoteKey
	pollMS      int
}

// pollResult carries one bridge-mode poll response from the background poller
// into the main event loop.
type pollResult struct {
	demo remoteDemo
	err  error
}

var (
	// bridgePaperBG is the quiet paper fill (#F6F5EE) for unlabeled keys and
	// the fallback background when the server's bg cannot be parsed.
	bridgePaperBG = color.NRGBA{R: 0xf6, G: 0xf5, B: 0xee, A: 255}
	// bridgePaperInk is the fallback foreground (#272C24) when the server's
	// fg cannot be parsed.
	bridgePaperInk = color.NRGBA{R: 0x27, G: 0x2c, B: 0x24, A: 255}
)

// parseHexColor parses "#RRGGBB" (the '#' is optional) into an opaque color.
func parseHexColor(s string) (color.Color, error) {
	hex := strings.TrimPrefix(s, "#")
	if len(hex) != 6 {
		return nil, fmt.Errorf("expected #RRGGBB, got %q", s)
	}
	var values [3]uint8
	for i := 0; i < 3; i++ {
		value, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", s, err)
		}
		values[i] = uint8(value)
	}
	return color.NRGBA{R: values[0], G: values[1], B: values[2], A: 255}, nil
}

// keyRenderColors resolves the wire bg/fg hex colors for a server key, falling
// back to quiet paper/ink on unparseable values.
func keyRenderColors(key *remoteKey) (bg, fg color.Color) {
	bg = bridgePaperBG
	fg = bridgePaperInk
	if key == nil {
		return
	}
	if key.BG != "" {
		if parsed, err := parseHexColor(key.BG); err == nil {
			bg = parsed
		} else {
			log.Printf("bridge key bg %q ignored: %v", key.BG, err)
		}
	}
	if key.FG != "" {
		if parsed, err := parseHexColor(key.FG); err == nil {
			fg = parsed
		} else {
			log.Printf("bridge key fg %q ignored: %v", key.FG, err)
		}
	}
	return
}

type demoDeck interface {
	Info() (streamdeck.DeviceInfo, error)
	ReadEvents(time.Duration) (streamdeck.InputRead, error)
	SetBrightness(int) error
	SetKeyImage(int, image.Image) error
	Close() error
}

type touchStripDeck interface {
	SetTouchStripImage(image.Image) error
}

type openDeckFunc func() (demoDeck, error)
type fetchDemoFunc func(context.Context) (remoteDemo, error)

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("deckdemo: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGINT (terminal) and SIGTERM (launchd bootout / logout / reboot) both
	// count as a clean shutdown so the server background can be persisted to
	// the device before exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := demoState{brightness: 70}
	return runDemo(
		ctx,
		&state,
		func() (demoDeck, error) { return streamdeck.OpenAny() },
		func(ctx context.Context) (remoteDemo, error) {
			return fetchRemoteDemo(ctx, demoHTTPClient, demoEndpointURL)
		},
		reconnectInterval,
	)
}

func runDemo(ctx context.Context, state *demoState, open openDeckFunc, fetchDemo fetchDemoFunc, retryInterval time.Duration) error {
	for {
		if ctx.Err() != nil {
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		deck, err := open()
		if err != nil {
			log.Printf("device unavailable retry=%s error=%q", retryInterval, err)
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		info, setupErr := deck.Info()
		var remote remoteDemo
		if setupErr == nil {
			var fetchErr error
			remote, fetchErr = fetchDemo(ctx)
			if fetchErr != nil {
				// Hardening: an absent or broken endpoint must never strand
				// the deck dark. Fall back to the classic local demo render.
				log.Printf("demo endpoint unavailable mode=rev1-local error=%q", fetchErr)
				state.bridge = false
				state.activeKey = nil
				state.activeStrip = nil
			} else {
				applyRemoteDemo(state, remote)
			}
		}
		if setupErr == nil {
			setupErr = restoreDemo(deck, state, info.Model)
		}
		if setupErr != nil {
			log.Printf("device setup failed retry=%s error=%q", retryInterval, setupErr)
			if closeErr := deck.Close(); closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		mode := "rev1"
		if state.bridge {
			mode = "bridge"
		}
		log.Printf("demo endpoint connected revision=%d mode=%s theme=%q events_seen=%d", remote.Revision, mode, remote.Presentation.Theme, remote.EventsSeen)
		log.Printf("device connected vid=%04x pid=%04x product=%q model=%q", info.VendorID, info.ProductID, info.Product, info.Model)
		if state.bridge && state.activeKey != nil && state.activeStrip != nil {
			log.Printf("bridge view key[%d]=%q state=%q strip=%q page=%d/%d poll_ms=%d background_keys=%d",
				state.activeKey.Index, state.activeKey.Label, state.activeKey.State,
				state.activeStrip.Title, state.activeStrip.Page, state.activeStrip.Pages, state.pollMS, len(state.background))
		}
		if info.Model.HasTouchStrip() {
			log.Printf("output restored keys=1-8 strip=800x100 brightness=%d", state.brightness)
		} else {
			width, height := info.Model.KeyImageSize()
			log.Printf("output restored keys=1-%d key-size=%dx%d brightness=%d", info.Model.KeyCount(), width, height, state.brightness)
		}

		connectionErr := runConnected(ctx, deck, state, info.Model, fetchDemo)
		if ctx.Err() != nil && state.bridge {
			// Best-effort: repaint every key from the server background so the
			// frames persisted in the device (shown at next power-on) are
			// controller-owned rather than whatever was last flashed.
			if persistErr := renderBackgroundKeys(deck, state, info.Model); persistErr != nil {
				log.Printf("background persist error=%q", persistErr)
			}
		}
		closeErr := deck.Close()
		if ctx.Err() != nil {
			if closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		log.Printf("device disconnected retry=%s error=%q", retryInterval, connectionErr)
		if closeErr != nil {
			log.Printf("device close error=%q", closeErr)
		}
		if !waitForReconnect(ctx, retryInterval) {
			log.Print("device shutdown reason=interrupt")
			return nil
		}
	}
}

func runConnected(ctx context.Context, deck demoDeck, state *demoState, model streamdeck.Model, fetchDemo fetchDemoFunc) error {
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eventResults := make(chan eventPostResult, 64)

	var pollResults chan pollResult
	var stopPoll func()
	if state.bridge {
		pollResults = make(chan pollResult, 1)
		stopPoll = startBridgePoll(connectionCtx, state, fetchDemo, pollResults)
		defer stopPoll()
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case result := <-eventResults:
			if result.err != nil {
				log.Printf("demo event error=%q", result.err)
				continue
			}
			if err := handleAck(deck, state, model, result.ack); err != nil {
				return err
			}
			continue
		case poll := <-pollResults:
			if poll.err != nil {
				log.Printf("demo poll error=%q", poll.err)
				continue
			}
			if err := handlePoll(deck, state, model, poll.demo); err != nil {
				return err
			}
			continue
		default:
		}

		result, err := deck.ReadEvents(inputReadTimeout)
		if errors.Is(err, streamdeck.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		logInputDiagnostics(result)
		for _, event := range result.Events {
			if state.bridge {
				// Pure renderer: no local interpretation or rerender. Log the
				// raw event, POST it, and let the ack/poll bring the new
				// server-owned state.
				logBridgeInput(state, event)
			} else if err := handleEvent(deck, state, model, event); err != nil {
				return err
			}
			if payload, ok := remoteEvent(event); ok {
				postEventAsync(connectionCtx, payload, eventResults)
			}
		}
	}
}

// startBridgePoll launches the poller goroutine on the controller's chosen cadence and
// returns a stop function. poll_ms is treated as opaque (currently 2000-5000).
func startBridgePoll(ctx context.Context, state *demoState, fetchDemo fetchDemoFunc, results chan<- pollResult) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := time.Duration(state.pollMS) * time.Millisecond
		if interval <= 0 {
			interval = 2 * time.Second
		}
		for {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
			demo, err := fetchDemo(ctx)
			if err == nil && demo.PollMS > 0 {
				interval = time.Duration(demo.PollMS) * time.Millisecond
			}
			select {
			case results <- pollResult{demo: demo, err: err}:
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// handleAck repaints from an event POST ack. revision-2 acks carry a state
// object with the server's resulting key/strip, allowing an immediate
// render without waiting for the next poll.
func handleAck(deck demoDeck, state *demoState, model streamdeck.Model, ack eventAck) error {
	state.remoteEventsSeen = ack.EventsSeen
	state.remoteLast = lastEventMessage(ack.Message)
	if ack.State != nil {
		if ack.State.Key != nil {
			state.activeKey = ack.State.Key
		}
		if ack.State.Strip != nil {
			state.activeStrip = ack.State.Strip
		}
	}
	log.Printf("demo ack events_seen=%d message=%q", ack.EventsSeen, ack.Message)
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	if state.bridge && state.activeKey != nil {
		if err := renderKey(deck, state, model, state.activeKey.Index); err != nil {
			return err
		}
	}
	return nil
}

// handlePoll applies one bridge-mode GET and re-renders from the server state.
func handlePoll(deck demoDeck, state *demoState, model streamdeck.Model, demo remoteDemo) error {
	if demo.Revision >= 2 && demo.Key != nil && demo.Strip != nil && demo.PollMS > 0 {
		state.activeKey = demo.Key
		state.activeStrip = demo.Strip
		state.pollMS = demo.PollMS
		if demo.Background != nil {
			state.background = demo.Background.Keys
		} else {
			state.background = nil
		}
	}
	state.remoteEventsSeen = demo.EventsSeen
	state.remoteLast = ""
	if demo.LastEvent != nil {
		state.remoteLast = lastEventMessage(demo.LastEvent.Summary)
	}
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	if state.activeKey != nil {
		if err := renderKey(deck, state, model, state.activeKey.Index); err != nil {
			return err
		}
	}
	return nil
}

func logBridgeInput(state *demoState, event streamdeck.Event) {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		state.latestInput = fmt.Sprintf("KEY %d %s", event.Key+1, pressedLabel(event.Pressed))
	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
		state.latestInput = fmt.Sprintf("D%d %+03d", event.Dial+1, event.Delta)
	case streamdeck.DialPressEvent:
		log.Printf("input dial dial=%d pressed=%t", event.Dial+1, event.Pressed)
		state.latestInput = fmt.Sprintf("D%d %s", event.Dial+1, pressedLabel(event.Pressed))
	case streamdeck.TouchEvent:
		logTouch(event)
		state.latestInput = touchInputSummary(event)
		state.latestTouch = &event
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func restoreDemo(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("restore brightness: %w", err)
	}
	for index := 0; index < model.KeyCount(); index++ {
		if err := renderKey(deck, state, model, index); err != nil {
			return err
		}
	}
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	return nil
}

func applyRemoteDemo(state *demoState, demo remoteDemo) error {
	state.remoteEventsSeen = demo.EventsSeen
	state.remoteLast = ""
	if demo.LastEvent != nil {
		state.remoteLast = lastEventMessage(demo.LastEvent.Summary)
	}
	// Mode is chosen from revision, never from the command string: the
	// command field stays "run_hardware_demo" forever as deliberate backward
	// compatibility, so it is ignored here.
	if demo.Revision >= 2 && demo.Key != nil && demo.Strip != nil && demo.PollMS > 0 {
		state.bridge = true
		state.activeKey = demo.Key
		state.activeStrip = demo.Strip
		state.pollMS = demo.PollMS
		if demo.Background != nil {
			state.background = demo.Background.Keys
		} else {
			// A response without a background means quiet paper; never render
			// stale frames from an earlier response.
			state.background = nil
		}
		if demo.Presentation.Brightness > 0 {
			state.brightness = demo.Presentation.Brightness
		}
		return nil
	}
	// revision < 2 or missing bridge fields: keep the classic rev-1 demo.
	state.bridge = false
	state.activeKey = nil
	state.activeStrip = nil
	state.background = nil
	state.pollMS = 0
	if demo.Presentation.Brightness > 0 {
		state.brightness = demo.Presentation.Brightness
	}
	state.remoteTheme = demo.Presentation.Theme
	state.remoteTitle = demo.Presentation.Title
	state.remoteMessage = demo.Presentation.Message
	return nil
}

func handleEvent(deck demoDeck, state *demoState, model streamdeck.Model, event streamdeck.Event) error {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		if event.Key < 0 || event.Key >= model.KeyCount() {
			return fmt.Errorf("key event index %d out of range for %s", event.Key, model)
		}
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		state.latestInput = fmt.Sprintf("KEY %d %s", event.Key+1, pressedLabel(event.Pressed))
		if event.Pressed {
			state.keys[event.Key] = !state.keys[event.Key]
			if err := renderKey(deck, state, model, event.Key); err != nil {
				return err
			}
			if model == streamdeck.ModelMini && (event.Key == 4 || event.Key == 5) {
				delta := -10
				if event.Key == 5 {
					delta = 10
				}
				state.brightness = clamp(state.brightness+delta, 0, 100)
				if err := deck.SetBrightness(state.brightness); err != nil {
					return err
				}
				log.Printf("output brightness=%d", state.brightness)
			}
		}
		return renderStrip(deck, state, model)

	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
		state.latestInput = fmt.Sprintf("D%d %+03d", event.Dial+1, event.Delta)
		switch event.Dial {
		case 0:
			state.counter += event.Delta
		case 1:
			next := clamp(state.brightness+event.Delta*5, 0, 100)
			if next != state.brightness {
				state.brightness = next
				if err := deck.SetBrightness(state.brightness); err != nil {
					return err
				}
			}
		case 2:
			previous := state.selectedKey
			state.selectedKey = wrap(state.selectedKey+event.Delta, streamdeck.KeyCount)
			if previous != state.selectedKey {
				if err := renderKey(deck, state, model, previous); err != nil {
					return err
				}
				if err := renderKey(deck, state, model, state.selectedKey); err != nil {
					return err
				}
			}
		case 3:
			state.mode = wrap(state.mode+event.Delta, len(displayModes))
		}
		return renderStrip(deck, state, model)

	case streamdeck.DialPressEvent:
		log.Printf("input dial dial=%d pressed=%t", event.Dial+1, event.Pressed)
		state.latestInput = fmt.Sprintf("D%d %s", event.Dial+1, pressedLabel(event.Pressed))
		if !event.Pressed {
			return renderStrip(deck, state, model)
		}
		switch event.Dial {
		case 0:
			state.counter = 0
		case 1:
			if state.brightness == 15 {
				state.brightness = 70
			} else {
				state.brightness = 15
			}
			if err := deck.SetBrightness(state.brightness); err != nil {
				return err
			}
		case 2:
			state.keys[state.selectedKey] = !state.keys[state.selectedKey]
			if err := renderKey(deck, state, model, state.selectedKey); err != nil {
				return err
			}
		case 3:
			state.mode = 0
		}
		return renderStrip(deck, state, model)

	case streamdeck.TouchEvent:
		logTouch(event)
		state.latestInput = touchInputSummary(event)
		state.latestTouch = &event
		return renderStrip(deck, state, model)

	default:
		return fmt.Errorf("unsupported normalized event %T", event)
	}
}

func renderKey(deck demoDeck, state *demoState, model streamdeck.Model, index int) error {
	width, height := model.KeyImageSize()
	view := render.KeyView{}
	if state.bridge {
		// Pure renderer: paint exactly what the server sent. The active key
		// carries its wire label and colors; every other key paints the
		// server background frame for its index, or quiet paper when the
		// server provides none.
		view.Index = index
		if state.activeKey != nil && state.activeKey.Index == index {
			view.Label = state.activeKey.Label
			view.BG, view.FG = keyRenderColors(state.activeKey)
		} else if frame := backgroundFrame(state.background, index); frame != nil {
			view.Label = frame.Label
			view.BG, view.FG = keyRenderColors(frame)
		} else {
			view.BG = bridgePaperBG
		}
	} else {
		view = render.KeyView{Index: index, On: state.keys[index], Selected: index == state.selectedKey}
	}
	img := render.KeySize(view, width, height)
	if err := deck.SetKeyImage(index, img); err != nil {
		return fmt.Errorf("render key %d: %w", index+1, err)
	}
	return nil
}

func renderStrip(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if !model.HasTouchStrip() {
		return nil
	}
	stripDeck, ok := deck.(touchStripDeck)
	if !ok {
		return fmt.Errorf("%s device does not provide touch-strip output", model)
	}
	var view render.StripView
	if state.bridge {
		view = render.StripView{
			EventsSeen: state.remoteEventsSeen,
			LastEvent:  state.remoteLast,
			Lines:      []string{}, // non-nil selects the bridge page
		}
		if state.activeStrip != nil {
			view.Title = state.activeStrip.Title
			view.Lines = state.activeStrip.Lines
			view.Page = state.activeStrip.Page
			view.Pages = state.activeStrip.Pages
			if view.Lines == nil {
				view.Lines = []string{}
			}
		}
	} else {
		view = render.StripView{
			Counter: state.counter, Brightness: state.brightness,
			SelectedKey: state.selectedKey, SelectedOn: state.keys[state.selectedKey],
			Mode: displayModes[state.mode], LastInput: state.latestInput, Touch: state.latestTouch,
			Theme: state.remoteTheme, Title: state.remoteTitle, Message: state.remoteMessage,
			EventsSeen: state.remoteEventsSeen, LastEvent: state.remoteLast,
		}
	}
	img := render.Strip(view)
	if err := stripDeck.SetTouchStripImage(img); err != nil {
		return fmt.Errorf("render touch strip: %w", err)
	}
	return nil
}

// backgroundFrame returns the server background frame for a key index, if any.
func backgroundFrame(frames []remoteKey, index int) *remoteKey {
	for i := range frames {
		if frames[i].Index == index {
			return &frames[i]
		}
	}
	return nil
}

// renderBackgroundKeys repaints every key from the server background (or quiet
// paper when absent) and restores brightness. Used at clean shutdown so the
// frames persisted in the device are controller-owned at the next power-on.
func renderBackgroundKeys(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("persist background brightness: %w", err)
	}
	width, height := model.KeyImageSize()
	for index := 0; index < model.KeyCount(); index++ {
		view := render.KeyView{Index: index, BG: bridgePaperBG}
		if frame := backgroundFrame(state.background, index); frame != nil {
			view.Label = frame.Label
			view.BG, view.FG = keyRenderColors(frame)
		}
		img := render.KeySize(view, width, height)
		if err := deck.SetKeyImage(index, img); err != nil {
			return fmt.Errorf("persist background key %d: %w", index+1, err)
		}
	}
	log.Printf("persisted %d background key frames for next boot", model.KeyCount())
	return nil
}

func logInputDiagnostics(result streamdeck.InputRead) {
	for _, diagnostic := range result.Diagnostics {
		log.Printf("input diagnostic=%q", diagnostic)
	}
}

func logTouch(event streamdeck.TouchEvent) {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		log.Printf("input touch kind=%s x=%d y=%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		log.Printf("input touch kind=FLICK start=%d,%d end=%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	}
}

func touchInputSummary(event streamdeck.TouchEvent) string {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		return fmt.Sprintf("%s %d,%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		return fmt.Sprintf("FLICK %d,%d>%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	default:
		return "TOUCH"
	}
}

func pressedLabel(pressed bool) string {
	if pressed {
		return "DOWN"
	}
	return "UP"
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func wrap(value, size int) int {
	return ((value % size) + size) % size
}
