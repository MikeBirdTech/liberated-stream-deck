package controlapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

func TestDecodeOperationsCoversEveryPlusOutput(t *testing.T) {
	commands := []Command{
		{Type: CommandSetBrightness, Brightness: intPointer(0)},
		{Type: CommandSetKeyImage, Index: intPointer(7), Image: testImagePayload(t, 120, 120, color.NRGBA{R: 1, G: 2, B: 3, A: 255})},
		{Type: CommandSetLCDImage, Image: testImagePayload(t, 800, 480, color.NRGBA{R: 4, G: 5, B: 6, A: 255})},
		{Type: CommandSetTouchStrip, Image: testImagePayload(t, 800, 100, color.NRGBA{R: 7, G: 8, B: 9, A: 255})},
		{Type: CommandSetTouchRegion, X: intPointer(790), Y: intPointer(90), Image: testImagePayload(t, 10, 10, color.White)},
	}

	operations, err := decodeOperations(streamdeck.ModelPlus, commands)
	if err != nil {
		t.Fatalf("decodeOperations: %v", err)
	}
	if len(operations) != len(commands) {
		t.Fatalf("operations = %d, want %d", len(operations), len(commands))
	}
	for index, operation := range operations {
		if operation.typeName != commands[index].Type {
			t.Fatalf("operation[%d].type = %q, want %q", index, operation.typeName, commands[index].Type)
		}
	}
	if operations[0].brightness != 0 {
		t.Fatalf("zero brightness was not preserved")
	}
}

func TestDecodeOperationsRejectsScalingAndOutOfBoundsRegions(t *testing.T) {
	tests := []struct {
		name    string
		command Command
		want    string
	}{
		{
			name:    "wrong native key dimensions",
			command: Command{Type: CommandSetKeyImage, Index: intPointer(0), Image: testImagePayload(t, 119, 120, color.Black)},
			want:    "want exactly 120x120",
		},
		{
			name:    "region overflow",
			command: Command{Type: CommandSetTouchRegion, X: intPointer(799), Y: intPointer(99), Image: testImagePayload(t, 2, 2, color.Black)},
			want:    "exceeds 800x100",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeOperations(streamdeck.ModelPlus, []Command{test.command})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestManagerAppliesBatchAndPublishesEveryInputShape(t *testing.T) {
	deck := newFakePlusDeck()
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) { return deck, nil })
	manager.reconnectDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager.Run: %v", err)
		}
	})
	waitFor(t, time.Second, func() bool { return manager.Snapshot().Connected })

	commands := []Command{
		{Type: CommandSetBrightness, Brightness: intPointer(37)},
		{Type: CommandSetLCDImage, Image: testImagePayload(t, 800, 480, color.Black)},
		{Type: CommandSetKeyImage, Index: intPointer(0), Image: testImagePayload(t, 120, 120, color.White)},
		{Type: CommandSetTouchStrip, Image: testImagePayload(t, 800, 100, color.Black)},
		{Type: CommandSetTouchRegion, X: intPointer(4), Y: intPointer(5), Image: testImagePayload(t, 6, 7, color.White)},
	}
	operations, err := decodeOperations(streamdeck.ModelPlus, commands)
	if err != nil {
		t.Fatalf("decodeOperations: %v", err)
	}
	ack, err := manager.Submit(context.Background(), "batch-1", operations)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ack.Status != "applied" || !ack.Connected || ack.Generation != 1 {
		t.Fatalf("ack = %+v", ack)
	}
	if got, want := deck.callNames(), []string{"brightness:37", "lcd", "key:0", "strip", "region:4,5"}; !equalStrings(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}

	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()
	deck.reads <- streamdeck.InputRead{Events: []streamdeck.Event{
		streamdeck.KeyEvent{Key: 0, Pressed: true},
		streamdeck.DialPressEvent{Dial: 0, Pressed: false},
		streamdeck.DialRotateEvent{Dial: 1, Delta: -2},
		streamdeck.TouchEvent{Kind: streamdeck.TouchTap, X: 0, Y: 0},
		streamdeck.TouchEvent{Kind: streamdeck.TouchPress, X: 4, Y: 5},
		streamdeck.TouchEvent{Kind: streamdeck.TouchFlick, StartX: 1, StartY: 2, EndX: 3, EndY: 4},
	}, Diagnostics: []string{"unknown report retained"}}

	wantKinds := []string{"key", "dial_press", "dial_rotate", "touch_tap", "touch_press", "touch_flick", "diagnostic"}
	for _, want := range wantKinds {
		select {
		case event := <-events:
			if event.Kind != want {
				t.Fatalf("event kind = %q, want %q", event.Kind, want)
			}
			if event.Sequence == 0 || event.Timestamp == "" {
				t.Fatalf("event missing sequence/timestamp: %+v", event)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestManagerQueuesWhileDisconnectedAndRestores(t *testing.T) {
	deck := newFakePlusDeck()
	allowOpen := make(chan struct{})
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) {
		select {
		case <-allowOpen:
			return deck, nil
		default:
			return nil, errors.New("not connected")
		}
	})
	manager.reconnectDelay = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager.Run: %v", err)
		}
	})

	commands := []Command{
		{Type: CommandSetBrightness, Brightness: intPointer(22)},
		{Type: CommandSetKeyImage, Index: intPointer(2), Image: testImagePayload(t, 120, 120, color.White)},
		{Type: CommandSetTouchRegion, X: intPointer(10), Y: intPointer(20), Image: testImagePayload(t, 5, 6, color.White)},
	}
	operations, err := decodeOperations(streamdeck.ModelPlus, commands)
	if err != nil {
		t.Fatalf("decodeOperations: %v", err)
	}
	ack, err := manager.Submit(context.Background(), "offline", operations)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ack.Status != "queued" || ack.Connected {
		t.Fatalf("ack = %+v, want queued/disconnected", ack)
	}
	close(allowOpen)
	waitFor(t, time.Second, func() bool { return manager.Snapshot().Connected })
	if got, want := deck.callNames(), []string{"brightness:22", "key:2", "strip"}; !equalStrings(got, want) {
		t.Fatalf("restore calls = %v, want %v", got, want)
	}
}

func TestManagerPublishesInputDuringSlowOutput(t *testing.T) {
	deck := newFakePlusDeck()
	deck.lcdStarted = make(chan struct{})
	deck.lcdRelease = make(chan struct{})
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) { return deck, nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager.Run: %v", err)
		}
	})
	waitFor(t, time.Second, func() bool { return manager.Snapshot().Connected })
	t.Cleanup(func() {
		select {
		case <-deck.lcdRelease:
		default:
			close(deck.lcdRelease)
		}
	})

	operations, err := decodeOperations(streamdeck.ModelPlus, []Command{{
		Type: CommandSetLCDImage, Image: testImagePayload(t, 800, 480, color.Black),
	}})
	if err != nil {
		t.Fatalf("decodeOperations: %v", err)
	}
	ackResult := make(chan CommandAck, 1)
	go func() {
		ack, _ := manager.Submit(context.Background(), "slow-lcd", operations)
		ackResult <- ack
	}()
	select {
	case <-deck.lcdStarted:
	case <-time.After(time.Second):
		t.Fatal("LCD write did not start")
	}

	events, unsubscribe := manager.Subscribe()
	defer unsubscribe()
	deck.reads <- streamdeck.InputRead{Events: []streamdeck.Event{streamdeck.KeyEvent{Key: 3, Pressed: true}}}
	select {
	case event := <-events:
		if event.Kind != "key" || event.Index == nil || *event.Index != 3 || event.Pressed == nil || !*event.Pressed {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("input was blocked behind the LCD write")
	}

	close(deck.lcdRelease)
	select {
	case ack := <-ackResult:
		if ack.Status != "applied" {
			t.Fatalf("ack = %+v", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("LCD command did not complete")
	}
}

func TestHTTPCommandsStateAndEventStream(t *testing.T) {
	deck := newFakePlusDeck()
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) { return deck, nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager.Run: %v", err)
		}
	})
	waitFor(t, time.Second, func() bool { return manager.Snapshot().Connected })

	server := httptest.NewServer(NewHandler(manager))
	defer server.Close()
	response, err := http.Post(server.URL+"/v1/commands", "application/json", strings.NewReader(`{
		"request_id":"http-1",
		"commands":[{"type":"set_brightness","brightness":44}]
	}`))
	if err != nil {
		t.Fatalf("POST commands: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d body=%s", response.StatusCode, body)
	}
	var ack CommandAck
	if err := json.NewDecoder(response.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.RequestID != "http-1" || ack.Status != "applied" {
		t.Fatalf("ack = %+v", ack)
	}

	eventRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatalf("create event request: %v", err)
	}
	eventResponse, err := http.DefaultClient.Do(eventRequest)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer eventResponse.Body.Close()
	reader := bufio.NewReader(eventResponse.Body)
	readUntilContains(t, reader, "event: ready", time.Second)
	deck.reads <- streamdeck.InputRead{Events: []streamdeck.Event{streamdeck.KeyEvent{Key: 0, Pressed: true}}}
	readUntilContains(t, reader, "event: key", time.Second)
	line := readUntilContains(t, reader, `"kind":"key"`, time.Second)
	if !strings.Contains(line, `"index":0`) || !strings.Contains(line, `"pressed":true`) {
		t.Fatalf("event data = %q", line)
	}
}

func TestHTTPRejectsUnknownFields(t *testing.T) {
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) { return nil, errors.New("absent") })
	request := httptest.NewRequest(http.MethodPost, "/v1/commands", strings.NewReader(`{"commands":[{"type":"set_brightness","brightness":50,"meaning":"volume"}]}`))
	recorder := httptest.NewRecorder()
	NewHandler(manager).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOpenAPIDocumentIsServedAndDescribesEveryCommand(t *testing.T) {
	manager := newManager(streamdeck.ModelPlus, func() (hardwareDevice, error) { return nil, errors.New("absent") })
	request := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	recorder := httptest.NewRecorder()
	NewHandler(manager).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var document map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", document["openapi"])
	}
	for _, command := range capabilitiesFor(streamdeck.ModelPlus).SupportedCommands {
		if !bytes.Contains(recorder.Body.Bytes(), []byte(`"`+command+`"`)) {
			t.Errorf("OpenAPI document does not contain command %q", command)
		}
	}
}

type fakePlusDeck struct {
	mu         sync.Mutex
	calls      []string
	reads      chan streamdeck.InputRead
	closed     bool
	lcdStarted chan struct{}
	lcdRelease chan struct{}
	lcdOnce    sync.Once
}

func newFakePlusDeck() *fakePlusDeck {
	return &fakePlusDeck{reads: make(chan streamdeck.InputRead, 8)}
}

func (d *fakePlusDeck) Info() (streamdeck.DeviceInfo, error) {
	return streamdeck.DeviceInfo{VendorID: streamdeck.VendorID, ProductID: streamdeck.ProductID, Model: streamdeck.ModelPlus, Manufacturer: "Elgato", Product: "Stream Deck +"}, nil
}

func (d *fakePlusDeck) ReadEvents(timeout time.Duration) (streamdeck.InputRead, error) {
	if timeout == 0 {
		select {
		case result := <-d.reads:
			return result, nil
		default:
			return streamdeck.InputRead{}, streamdeck.ErrTimeout
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-d.reads:
		return result, nil
	case <-timer.C:
		return streamdeck.InputRead{}, streamdeck.ErrTimeout
	}
}

func (d *fakePlusDeck) SetBrightness(value int) error {
	d.record("brightness:" + itoa(value))
	return nil
}

func (d *fakePlusDeck) SetKeyImage(index int, _ image.Image) error {
	d.record("key:" + itoa(index))
	return nil
}

func (d *fakePlusDeck) SetLCDImage(image.Image) error {
	d.record("lcd")
	if d.lcdStarted != nil {
		d.lcdOnce.Do(func() { close(d.lcdStarted) })
		<-d.lcdRelease
	}
	return nil
}

func (d *fakePlusDeck) SetTouchStripImage(image.Image) error {
	d.record("strip")
	return nil
}

func (d *fakePlusDeck) SetPartialWindowImage(x, y int, _ image.Image) error {
	d.record("region:" + itoa(x) + "," + itoa(y))
	return nil
}

func (d *fakePlusDeck) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return nil
}

func (d *fakePlusDeck) record(call string) {
	d.mu.Lock()
	d.calls = append(d.calls, call)
	d.mu.Unlock()
}

func (d *fakePlusDeck) callNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func testImagePayload(t *testing.T, width, height int, fill color.Color) *ImagePayload {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return &ImagePayload{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func readUntilContains(t *testing.T, reader *bufio.Reader, substring string, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	results := make(chan result, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil || strings.Contains(line, substring) {
				results <- result{line: line, err: err}
				return
			}
		}
	}()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("read event stream: %v", result.err)
		}
		return result.line
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %q", substring)
		return ""
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buffer [32]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}
