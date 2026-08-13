package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
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
	bootImages      []image.Image
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

func (d *fakeDemoDeck) UploadBootImage(img image.Image) error {
	d.bootImages = append(d.bootImages, img)
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

// ---------- revision-2 bridge mode tests ----------

func TestApplyRemoteDemoModeSelection(t *testing.T) {
	key := &remoteKey{Index: 0, ID: "demo_task", Label: "Demo Task", State: "idle", BG: "#F6F5EE", FG: "#272C24"}
	strip := &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: idle"}}
	tests := []struct {
		name       string
		demo       remoteDemo
		wantBridge bool
	}{
		{
			name:       "revision 2 enters bridge regardless of command string",
			demo:       remoteDemo{Command: "run_bridge", Revision: 2, PollMS: 5000, Key: key, Strip: strip},
			wantBridge: true,
		},
		{
			name:       "revision 2 with missing fields stays rev1",
			demo:       remoteDemo{Command: "run_hardware_demo", Revision: 2, PollMS: 5000, Key: key},
			wantBridge: false,
		},
		{
			name:       "revision 1 with bridge fields stays rev1",
			demo:       remoteDemo{Command: "run_hardware_demo", Revision: 1, PollMS: 5000, Key: key, Strip: strip},
			wantBridge: false,
		},
		{
			name:       "revision 0 with no bridge fields stays rev1",
			demo:       remoteDemo{Command: "run_hardware_demo"},
			wantBridge: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &demoState{brightness: 15, bridge: true, activeKey: key, activeStrip: strip, pollMS: 9000}
			if err := applyRemoteDemo(state, test.demo); err != nil {
				t.Fatalf("applyRemoteDemo: %v", err)
			}
			if state.bridge != test.wantBridge {
				t.Fatalf("bridge = %t, want %t", state.bridge, test.wantBridge)
			}
			if test.wantBridge {
				if state.activeKey == nil || state.activeKey.Label != "Demo Task" {
					t.Fatalf("bridge key = %+v", state.activeKey)
				}
				if state.pollMS != 5000 {
					t.Fatalf("poll_ms = %d, want 5000", state.pollMS)
				}
			} else if state.activeKey != nil || state.activeStrip != nil {
				t.Fatalf("rev1 state kept bridge fields: key=%+v strip=%+v", state.activeKey, state.activeStrip)
			}
		})
	}
}

func TestBridgeRestoreRendersServerKeyAndQuietPeers(t *testing.T) {
	state := &demoState{
		brightness:  70,
		bridge:      true,
		activeKey:   &remoteKey{Index: 0, Label: "Demo Task", State: "idle", BG: "#F6F5EE", FG: "#272C24"},
		activeStrip: &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: idle"}},
	}
	deck := newFakeDemoDeck()
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}

	wantActive := render.KeySize(render.KeyView{Index: 0, Label: "Demo Task", BG: bridgePaperBG, FG: bridgePaperInk}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[0], wantActive, "bridge active key")
	for index := 1; index < streamdeck.KeyCount; index++ {
		wantQuiet := render.KeySize(render.KeyView{Index: index, BG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
		assertImagesEqual(t, deck.keyImages[index], wantQuiet, "quiet key")
	}
	wantStrip := render.Strip(render.StripView{Title: "Today", Lines: []string{"Demo Task: idle"}, Page: 0, Pages: 3})
	assertImagesEqual(t, deck.stripImages[0], wantStrip, "bridge strip")
}

func TestBridgeAckRepaintsKeyAndStripFromState(t *testing.T) {
	deck := newFakeDemoDeck()
	state := &demoState{
		brightness:  70,
		bridge:      true,
		activeKey:   &remoteKey{Index: 0, Label: "Demo Task", State: "idle", BG: "#F6F5EE", FG: "#272C24"},
		activeStrip: &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: idle"}},
	}
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	before := len(deck.stripImages)

	ackState := &remoteState{
		Key:   &remoteKey{Index: 0, Label: "Demo Task", State: "running", BG: "#6FA25C", FG: "#F6F5EE"},
		Strip: &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: running", "7 runs · 7 ok · 0 err"}},
	}
	ack := eventAck{OK: true, EventsSeen: 7, Message: "Key 0 down received - Demo Task started", State: ackState}
	if err := handleAck(deck, state, streamdeck.ModelPlus, ack); err != nil {
		t.Fatalf("handleAck: %v", err)
	}
	if !strings.Contains(state.remoteLast, "Demo Task started") {
		t.Fatalf("ack message not adopted: %q", state.remoteLast)
	}
	if state.activeKey.State != "running" || state.activeKey.BG != "#6FA25C" {
		t.Fatalf("ack key not adopted: %+v", state.activeKey)
	}
	wantBG, _ := keyRenderColors(ackState.Key)
	got := color.NRGBAModel.Convert(deck.keyImages[0].At(60, 60)).(color.NRGBA)
	if got != color.NRGBAModel.Convert(wantBG).(color.NRGBA) {
		t.Fatalf("key 0 background = %#v, want leaf %#v", got, wantBG)
	}
	if len(deck.stripImages) != before+1 {
		t.Fatalf("strip images = %d, want %d", len(deck.stripImages), before+1)
	}
}

func TestBridgePollAdoptsAndRepaintsServerState(t *testing.T) {
	deck := newFakeDemoDeck()
	state := &demoState{
		brightness:  70,
		bridge:      true,
		pollMS:      2000,
		activeKey:   &remoteKey{Index: 0, Label: "Demo Task", State: "idle", BG: "#F6F5EE", FG: "#272C24"},
		activeStrip: &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: idle"}},
	}
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}

	poll := remoteDemo{Revision: 2, PollMS: 3000, EventsSeen: 9}
	poll.Key = &remoteKey{Index: 0, Label: "Demo Task", State: "success", BG: "#55764A", FG: "#F6F5EE"}
	poll.Strip = &remoteStrip{Page: 1, Pages: 3, Title: "Next meeting", Lines: []string{"Demo Task: ok 08:28", "9 runs · 9 ok · 0 err"}}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, poll); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	if state.pollMS != 3000 || state.remoteEventsSeen != 9 {
		t.Fatalf("poll cadence/events = %d/%d", state.pollMS, state.remoteEventsSeen)
	}
	if state.activeStrip == nil || state.activeStrip.Page != 1 || state.activeStrip.Title != "Next meeting" {
		t.Fatalf("poll strip not adopted: %+v", state.activeStrip)
	}
	wantBG, _ := keyRenderColors(poll.Key)
	got := color.NRGBAModel.Convert(deck.keyImages[0].At(60, 60)).(color.NRGBA)
	if got != color.NRGBAModel.Convert(wantBG).(color.NRGBA) {
		t.Fatalf("key 0 background = %#v, want moss %#v", got, wantBG)
	}
}

func TestRunDemoRendersLocallyWhenEndpointUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deck := newFakeDemoDeck()
	deck.reads = []fakeRead{{err: errors.New("USB removed")}}
	openCalls := 0
	open := func() (demoDeck, error) {
		openCalls++
		if openCalls == 1 {
			return deck, nil
		}
		cancel()
		return nil, errors.New("end of test")
	}
	fetch := func(context.Context) (remoteDemo, error) {
		return remoteDemo{}, errors.New("endpoint down")
	}

	if err := runDemo(ctx, &demoState{brightness: 50}, open, fetch, time.Millisecond); err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	if len(deck.keyImages) != streamdeck.KeyCount {
		t.Fatalf("no local render on endpoint failure: keys = %d, want %d", len(deck.keyImages), streamdeck.KeyCount)
	}
	if deck.brightnessCalls[0] != 50 {
		t.Fatalf("brightness = %v, want [50]", deck.brightnessCalls)
	}
}

func TestBridgePollLoopFollowsServerCadence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	calls := 0
	fetch := func(context.Context) (remoteDemo, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return remoteDemo{Revision: 2, PollMS: 5}, nil
	}
	results := make(chan pollResult, 16)
	stop := startBridgePoll(ctx, &demoState{pollMS: 5}, fetch, results)
	defer stop()

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := calls
		mu.Unlock()
		if count >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d polls in budget", count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestApplyRemoteDemoAdoptsBackground(t *testing.T) {
	base := remoteDemo{Command: "run_hardware_demo", Revision: 2, PollMS: 5000,
		Key:   &remoteKey{Index: 0, Label: "Demo Task"},
		Strip: &remoteStrip{Page: 0, Pages: 3, Title: "Today"},
	}
	t.Run("background present is adopted", func(t *testing.T) {
		demo := base
		demo.Background = &remoteBackground{Keys: []remoteKey{
			{Index: 1, Label: "Pi 4", BG: "#272C24", FG: "#F6F5EE"},
		}}
		state := &demoState{brightness: 70, bridge: false}
		if err := applyRemoteDemo(state, demo); err != nil {
			t.Fatalf("applyRemoteDemo: %v", err)
		}
		if !state.bridge {
			t.Fatal("bridge not entered")
		}
		if len(state.background) != 1 || state.background[0].Label != "Pi 4" {
			t.Fatalf("background = %+v", state.background)
		}
	})
	t.Run("background absent keeps bridge with quiet default", func(t *testing.T) {
		state := &demoState{brightness: 70, background: []remoteKey{{Index: 1, Label: "stale"}}}
		if err := applyRemoteDemo(state, base); err != nil {
			t.Fatalf("applyRemoteDemo: %v", err)
		}
		if !state.bridge {
			t.Fatal("bridge not entered")
		}
		if len(state.background) != 0 {
			t.Fatalf("background not cleared: %+v", state.background)
		}
	})
	t.Run("rev1 ignores background", func(t *testing.T) {
		demo := base
		demo.Revision = 1
		demo.Background = &remoteBackground{Keys: []remoteKey{{Index: 1, Label: "Pi 4"}}}
		state := &demoState{brightness: 70, background: []remoteKey{{Index: 1, Label: "old"}}}
		if err := applyRemoteDemo(state, demo); err != nil {
			t.Fatalf("applyRemoteDemo: %v", err)
		}
		if state.bridge || len(state.background) != 0 {
			t.Fatalf("rev1 kept background: bridge=%t background=%+v", state.bridge, state.background)
		}
	})
}

func TestBridgeRestoreRendersBackgroundFrames(t *testing.T) {
	state := &demoState{
		brightness:  70,
		bridge:      true,
		activeKey:   &remoteKey{Index: 0, Label: "Demo Task", State: "idle", BG: "#F6F5EE", FG: "#272C24"},
		activeStrip: &remoteStrip{Page: 0, Pages: 3, Title: "Today", Lines: []string{"Demo Task: idle"}},
		background: []remoteKey{
			{Index: 1, Label: "Pi 4", BG: "#272C24", FG: "#F6F5EE"},
			{Index: 2, Label: "Pi Zero", BG: "#272C24", FG: "#F6F5EE"},
		},
	}
	deck := newFakeDemoDeck()
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}

	wantActive := render.KeySize(render.KeyView{Index: 0, Label: "Demo Task", BG: bridgePaperBG, FG: bridgePaperInk}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[0], wantActive, "active key wins")

	wantBG1 := render.KeySize(render.KeyView{Index: 1, Label: "Pi 4", BG: bridgePaperInk, FG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[1], wantBG1, "background frame key 1")

	wantBG2 := render.KeySize(render.KeyView{Index: 2, Label: "Pi Zero", BG: bridgePaperInk, FG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[2], wantBG2, "background frame key 2")

	wantQuiet := render.KeySize(render.KeyView{Index: 3, BG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[3], wantQuiet, "uncovered key stays quiet paper")
}

func TestRenderBackgroundKeysPersistsEveryKey(t *testing.T) {
	moss := color.NRGBA{R: 0x55, G: 0x76, B: 0x4a, A: 255} // #55764A
	state := &demoState{
		brightness: 65,
		bridge:     true,
		background: []remoteKey{
			{Index: 0, Label: "Idle", BG: "#55764A", FG: "#F6F5EE"},
			{Index: 7, Label: "Rack", BG: "#272C24", FG: "#F6F5EE"},
		},
	}
	deck := newFakeDemoDeck()
	if err := renderBackgroundKeys(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderBackgroundKeys: %v", err)
	}
	if len(deck.brightnessCalls) != 1 || deck.brightnessCalls[0] != 65 {
		t.Fatalf("brightness calls = %v, want [65]", deck.brightnessCalls)
	}
	if len(deck.keyImages) != streamdeck.KeyCount {
		t.Fatalf("keys = %d, want %d", len(deck.keyImages), streamdeck.KeyCount)
	}
	want0 := render.KeySize(render.KeyView{Index: 0, Label: "Idle", BG: moss, FG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[0], want0, "background key 0")

	want7 := render.KeySize(render.KeyView{Index: 7, Label: "Rack", BG: bridgePaperInk, FG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[7], want7, "background key 7")

	wantQuiet := render.KeySize(render.KeyView{Index: 4, BG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[4], wantQuiet, "uncovered key quiet paper")
}

func TestMaybeUploadBootImageRevisionGated(t *testing.T) {
	deck := newFakeDemoDeck()
	state := &demoState{bridge: true}

	raw := encodeTestPNG(16, 16)
	boot := &remoteBootImage{Revision: 1, Data: base64.StdEncoding.EncodeToString(raw)}

	if err := maybeUploadBootImage(deck, state, boot); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if len(deck.bootImages) != 1 {
		t.Fatalf("uploads = %d, want 1", len(deck.bootImages))
	}
	if b := deck.bootImages[0].Bounds(); b.Dx() != 16 || b.Dy() != 16 {
		t.Fatalf("uploaded image bounds = %v, want passthrough 16x16 (scaling is the library's job)", b)
	}
	if state.bootImageRev != 1 {
		t.Fatalf("bootImageRev = %d, want 1", state.bootImageRev)
	}

	if err := maybeUploadBootImage(deck, state, boot); err != nil {
		t.Fatalf("same-revision upload: %v", err)
	}
	if len(deck.bootImages) != 1 {
		t.Fatalf("same revision re-uploaded: %d uploads", len(deck.bootImages))
	}

	boot2 := &remoteBootImage{Revision: 2, Data: base64.StdEncoding.EncodeToString(raw)}
	if err := maybeUploadBootImage(deck, state, boot2); err != nil {
		t.Fatalf("second revision: %v", err)
	}
	if len(deck.bootImages) != 2 || state.bootImageRev != 2 {
		t.Fatalf("second revision not uploaded: %d uploads rev=%d", len(deck.bootImages), state.bootImageRev)
	}
}

func TestMaybeUploadBootImageSkipsOrFails(t *testing.T) {
	deck := newFakeDemoDeck()
	state := &demoState{bridge: true}
	if err := maybeUploadBootImage(deck, state, nil); err != nil {
		t.Fatalf("nil boot: %v", err)
	}
	if err := maybeUploadBootImage(deck, state, &remoteBootImage{Revision: 1}); err != nil {
		t.Fatalf("empty data: %v", err)
	}
	if len(deck.bootImages) != 0 {
		t.Fatalf("uploads = %d, want 0", len(deck.bootImages))
	}
	err := maybeUploadBootImage(deck, state, &remoteBootImage{Revision: 3, Data: "!!!not-base64!!!"})
	if err == nil {
		t.Fatal("bad base64 returned no error")
	}
	if state.bootImageRev != 0 {
		t.Fatalf("failed upload advanced revision to %d", state.bootImageRev)
	}
}

func encodeTestPNG(width, height int) []byte {
	return encodeTestPNGColor(width, height, color.NRGBA{R: 0x55, G: 0x76, B: 0x4a, A: 255})
}

func encodeTestPNGColor(width, height int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// testFrame builds a wire image payload: a solid-color PNG of the given size.
func testFrame(revision string, width, height int, c color.NRGBA) *remoteImage {
	return &remoteImage{
		Revision: revision,
		MimeType: "image/png",
		Data:     base64.StdEncoding.EncodeToString(encodeTestPNGColor(width, height, c)),
	}
}

func keyPixel(t *testing.T, deck *fakeDemoDeck, index, x, y int) color.NRGBA {
	t.Helper()
	img := deck.keyImages[index]
	if img == nil {
		t.Fatalf("key %d has no image", index)
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestBridgeRenderPaintsKeyImageFrame(t *testing.T) {
	red := color.NRGBA{R: 0xd0, G: 0x20, B: 0x10, A: 255}
	state := &demoState{
		bridge: true,
		activeKey: &remoteKey{
			Index: 0, Label: "Start", BG: "#F6F5EE", FG: "#272C24",
			Image: testFrame("rev-red", streamdeck.KeyImageWidth, streamdeck.KeyImageHeight, red),
		},
	}
	deck := newFakeDemoDeck()
	if err := renderKey(deck, state, streamdeck.ModelPlus, 0); err != nil {
		t.Fatalf("renderKey: %v", err)
	}
	if got := keyPixel(t, deck, 0, 60, 60); got != red {
		t.Fatalf("key 0 pixel = %#v, want image red %#v", got, red)
	}
	if b := deck.keyImages[0].Bounds(); b.Dx() != streamdeck.KeyImageWidth || b.Dy() != streamdeck.KeyImageHeight {
		t.Fatalf("key 0 bounds = %v, want native %dx%d", b, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	}
}

func TestBridgeRenderScalesImageToNativeKeySize(t *testing.T) {
	moss := color.NRGBA{R: 0x55, G: 0x76, B: 0x4a, A: 255}
	state := &demoState{
		bridge:    true,
		activeKey: &remoteKey{Index: 0, Label: "Tiny", Image: testFrame("rev-tiny", 2, 2, moss)},
	}
	deck := newFakeDemoDeck()
	if err := renderKey(deck, state, streamdeck.ModelPlus, 0); err != nil {
		t.Fatalf("renderKey: %v", err)
	}
	if b := deck.keyImages[0].Bounds(); b.Dx() != streamdeck.KeyImageWidth || b.Dy() != streamdeck.KeyImageHeight {
		t.Fatalf("key 0 bounds = %v, want scaled to %dx%d", b, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	}
	if got := keyPixel(t, deck, 0, 60, 60); got != moss {
		t.Fatalf("key 0 pixel = %#v, want scaled moss %#v", got, moss)
	}
}

func TestBridgeRenderFallsBackOnInvalidImage(t *testing.T) {
	state := &demoState{
		bridge: true,
		activeKey: &remoteKey{
			Index: 0, Label: "Start", BG: "#F6F5EE", FG: "#272C24",
			Image: &remoteImage{Revision: "rev-broken", MimeType: "image/png", Data: "!!!not-base64!!!"},
		},
	}
	deck := newFakeDemoDeck()
	if err := renderKey(deck, state, streamdeck.ModelPlus, 0); err != nil {
		t.Fatalf("renderKey: %v", err)
	}
	want := render.KeySize(render.KeyView{Index: 0, Label: "Start", BG: bridgePaperBG, FG: bridgePaperInk}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[0], want, "semantic fallback")
}

func TestImageFrameCachesByRevision(t *testing.T) {
	red := color.NRGBA{R: 0xd0, G: 0x20, B: 0x10, A: 255}
	green := color.NRGBA{R: 0x10, G: 0xa0, B: 0x20, A: 255}
	state := &demoState{}

	key := &remoteKey{Index: 0, Image: testFrame("rev-1", 4, 4, red)}
	first := state.imageFrame(key)
	if first == nil {
		t.Fatal("first decode returned nil")
	}

	// Same revision with different bytes must serve the cached frame: the
	// revision is the server's content digest and is trusted as the identity.
	key.Image = testFrame("rev-1", 4, 4, green)
	cached := state.imageFrame(key)
	if cached != first {
		t.Fatal("same revision was re-decoded instead of served from cache")
	}

	key.Image = testFrame("rev-2", 4, 4, green)
	fresh := state.imageFrame(key)
	if fresh == nil || fresh == first {
		t.Fatal("new revision did not decode a fresh frame")
	}

	// A failed decode is cached too, so a broken payload is not re-decoded
	// (or re-logged) on every repaint.
	key.Image = &remoteImage{Revision: "rev-bad", Data: "!!!not-base64!!!"}
	if state.imageFrame(key) != nil {
		t.Fatal("broken payload decoded to a frame")
	}
	key.Image = testFrame("rev-bad", 4, 4, red)
	if state.imageFrame(key) != nil {
		t.Fatal("cached decode failure was retried for the same revision")
	}
}

func TestRenderBackgroundKeysPaintsImageFrames(t *testing.T) {
	ink := color.NRGBA{R: 0x27, G: 0x2c, B: 0x24, A: 255}
	state := &demoState{
		brightness: 65,
		bridge:     true,
		background: []remoteKey{
			{Index: 3, Label: "Rack", BG: "#F6F5EE", FG: "#272C24", Image: testFrame("rev-rack", streamdeck.KeyImageWidth, streamdeck.KeyImageHeight, ink)},
		},
	}
	deck := newFakeDemoDeck()
	if err := renderBackgroundKeys(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderBackgroundKeys: %v", err)
	}
	if got := keyPixel(t, deck, 3, 60, 60); got != ink {
		t.Fatalf("key 3 pixel = %#v, want image ink %#v", got, ink)
	}
	wantQuiet := render.KeySize(render.KeyView{Index: 4, BG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImages[4], wantQuiet, "uncovered key quiet paper")
}
