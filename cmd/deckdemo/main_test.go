package main

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

func TestRestoreDemoRestoresCompleteState(t *testing.T) {
	touch := &streamdeck.TouchEvent{Kind: streamdeck.TouchTap, X: 327, Y: 48}
	state := &demoState{
		keys:        [streamdeck.KeyCount]bool{true, false, true, false, false, true},
		counter:     23,
		brightness:  65,
		selectedKey: 2,
		mode:        2,
		latestInput: "TAP 327,48",
		latestTouch: touch,
	}
	deck := newFakeDemoDeck()

	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	if got := deck.brightnessCalls; len(got) != 1 || got[0] != 65 {
		t.Fatalf("brightness calls = %v, want [65]", got)
	}
	if len(deck.keyImages) != streamdeck.KeyCount {
		t.Fatalf("key images = %d, want %d", len(deck.keyImages), streamdeck.KeyCount)
	}
	for index := 0; index < streamdeck.KeyCount; index++ {
		want := render.Key(render.KeyView{Index: index, On: state.keys[index], Selected: index == state.selectedKey})
		assertImagesEqual(t, deck.keyImages[index], want, "key image")
	}
	if len(deck.stripImages) != 1 {
		t.Fatalf("strip images = %d, want 1", len(deck.stripImages))
	}
	wantStrip := render.Strip(render.StripView{
		Counter: state.counter, Brightness: state.brightness,
		SelectedKey: state.selectedKey, SelectedOn: state.keys[state.selectedKey],
		Mode: displayModes[state.mode], LastInput: state.latestInput, Touch: state.latestTouch,
	})
	assertImagesEqual(t, deck.stripImages[0], wantStrip, "strip image")
}

func TestRestoreDemoMiniRestoresSixNativeSizeKeysWithoutStrip(t *testing.T) {
	state := &demoState{
		keys:        [streamdeck.KeyCount]bool{true, false, true, false, true, false},
		brightness:  60,
		selectedKey: 2,
	}
	deck := newFakeDemoDeck()

	if err := restoreDemo(deck, state, streamdeck.ModelMini); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	if len(deck.brightnessCalls) != 1 || deck.brightnessCalls[0] != 60 {
		t.Fatalf("brightness calls = %v, want [60]", deck.brightnessCalls)
	}
	if len(deck.keyImages) != streamdeck.MiniKeyCount {
		t.Fatalf("key images = %d, want %d", len(deck.keyImages), streamdeck.MiniKeyCount)
	}
	for index, img := range deck.keyImages {
		if img.Bounds().Dx() != streamdeck.MiniKeyImageWidth || img.Bounds().Dy() != streamdeck.MiniKeyImageHeight {
			t.Fatalf("key %d size = %v, want 80x80", index, img.Bounds().Size())
		}
	}
	if len(deck.stripImages) != 0 {
		t.Fatalf("strip images = %d, want none", len(deck.stripImages))
	}
}

func TestMiniBrightnessKeysAdjustAndClampBrightness(t *testing.T) {
	deck := newFakeDemoDeck()
	state := &demoState{brightness: 5}
	if err := handleEvent(deck, state, streamdeck.ModelMini, streamdeck.KeyEvent{Key: 4, Pressed: true}); err != nil {
		t.Fatalf("handle decrease: %v", err)
	}
	if state.brightness != 0 {
		t.Fatalf("brightness = %d, want 0", state.brightness)
	}
	if err := handleEvent(deck, state, streamdeck.ModelMini, streamdeck.KeyEvent{Key: 5, Pressed: true}); err != nil {
		t.Fatalf("handle increase: %v", err)
	}
	if state.brightness != 10 {
		t.Fatalf("brightness = %d, want 10", state.brightness)
	}
	if got := deck.brightnessCalls; len(got) != 2 || got[0] != 0 || got[1] != 10 {
		t.Fatalf("brightness calls = %v, want [0 10]", got)
	}
	if len(deck.stripImages) != 0 {
		t.Fatalf("strip images = %d, want none", len(deck.stripImages))
	}
}

func TestRunDemoReconnectsAndRestoresState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := newFakeDemoDeck()
	first.reads = []fakeRead{{err: errors.New("USB removed")}}
	second := newFakeDemoDeck()
	second.onStripImage = cancel

	decks := []*fakeDemoDeck{first, second}
	openCalls := 0
	open := func() (demoDeck, error) {
		if openCalls >= len(decks) {
			return nil, errors.New("unexpected extra open")
		}
		deck := decks[openCalls]
		openCalls++
		return deck, nil
	}
	state := &demoState{
		keys:        [streamdeck.KeyCount]bool{false, true, false, true},
		brightness:  40,
		selectedKey: 3,
		mode:        1,
	}

	if err := runDemo(ctx, state, open, testRemoteDemo(state.brightness), time.Millisecond); err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	if openCalls != 2 {
		t.Fatalf("open calls = %d, want 2", openCalls)
	}
	for index, deck := range decks {
		if deck.closeCalls != 1 {
			t.Fatalf("deck %d close calls = %d, want 1", index, deck.closeCalls)
		}
		if len(deck.brightnessCalls) != 1 || deck.brightnessCalls[0] != state.brightness {
			t.Fatalf("deck %d brightness = %v", index, deck.brightnessCalls)
		}
		if len(deck.keyImages) != streamdeck.KeyCount || len(deck.stripImages) != 1 {
			t.Fatalf("deck %d restored keys=%d strips=%d", index, len(deck.keyImages), len(deck.stripImages))
		}
	}
}

func TestRunDemoCancellationInterruptsReconnectWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempted := make(chan struct{})
	done := make(chan error, 1)
	open := func() (demoDeck, error) {
		select {
		case <-attempted:
		default:
			close(attempted)
		}
		return nil, errors.New("device absent")
	}

	go func() {
		done <- runDemo(ctx, &demoState{brightness: 70}, open, testRemoteDemo(70), time.Hour)
	}()

	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("open was not attempted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDemo: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runDemo did not stop after cancellation")
	}
}

func testRemoteDemo(brightness int) fetchDemoFunc {
	return func(context.Context) (remoteDemo, error) {
		demo := remoteDemo{Command: "run_hardware_demo"}
		demo.Presentation.Brightness = brightness
		return demo, nil
	}
}

type fakeRead struct {
	result streamdeck.InputRead
	err    error
}

type fakeDemoDeck struct {
	mu              sync.Mutex
	reads           []fakeRead
	brightnessCalls []int
	keyImages       map[int]image.Image
	stripImages     []image.Image
	closeCalls      int
	onStripImage    func()
}

func newFakeDemoDeck() *fakeDemoDeck {
	return &fakeDemoDeck{keyImages: make(map[int]image.Image)}
}

func (d *fakeDemoDeck) Info() (streamdeck.DeviceInfo, error) {
	return streamdeck.DeviceInfo{
		VendorID: streamdeck.VendorID, ProductID: streamdeck.ProductID, Model: streamdeck.ModelPlus, Product: "Stream Deck +",
	}, nil
}

func (d *fakeDemoDeck) ReadEvents(time.Duration) (streamdeck.InputRead, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reads) == 0 {
		return streamdeck.InputRead{}, streamdeck.ErrTimeout
	}
	read := d.reads[0]
	d.reads = d.reads[1:]
	return read.result, read.err
}

func (d *fakeDemoDeck) SetBrightness(percent int) error {
	d.brightnessCalls = append(d.brightnessCalls, percent)
	return nil
}

func (d *fakeDemoDeck) SetKeyImage(index int, img image.Image) error {
	d.keyImages[index] = img
	return nil
}

func (d *fakeDemoDeck) SetTouchStripImage(img image.Image) error {
	d.stripImages = append(d.stripImages, img)
	if d.onStripImage != nil {
		d.onStripImage()
	}
	return nil
}

func (d *fakeDemoDeck) Close() error {
	d.closeCalls++
	return nil
}

func assertImagesEqual(t *testing.T, got, want image.Image, name string) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("%s nil: got=%v want=%v", name, got == nil, want == nil)
	}
	if got.Bounds() != want.Bounds() {
		t.Fatalf("%s bounds = %v, want %v", name, got.Bounds(), want.Bounds())
	}
	for y := got.Bounds().Min.Y; y < got.Bounds().Max.Y; y++ {
		for x := got.Bounds().Min.X; x < got.Bounds().Max.X; x++ {
			if got.At(x, y) != want.At(x, y) {
				t.Fatalf("%s differs at %d,%d", name, x, y)
			}
		}
	}
}
