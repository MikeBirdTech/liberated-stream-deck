package main

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/visual"
)

// ---------- helpers ----------

var (
	testRed    = color.NRGBA{R: 0xd0, G: 0x20, B: 0x10, A: 255}
	testGreen  = color.NRGBA{R: 0x10, G: 0xa0, B: 0x20, A: 255}
	testBlue   = color.NRGBA{R: 0x10, G: 0x20, B: 0xd0, A: 255}
	testYellow = color.NRGBA{R: 0xf0, G: 0xe0, B: 0x10, A: 255}
	testInk    = color.NRGBA{R: 0x27, G: 0x2c, B: 0x24, A: 255}
)

func nativeFrame(revision string, c color.NRGBA) *remoteImage {
	return testFrame(revision, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight, c)
}

func wireFrame(revision string, c color.NRGBA, durationMS int) remoteFrame {
	return remoteFrame{DurationMS: durationMS, remoteImage: *nativeFrame(revision, c)}
}

func intp(v int) *int { return &v }

func bridgeStrip() *remoteStrip {
	return &remoteStrip{Page: 0, Pages: 1, Title: "Today", Lines: []string{"ready"}}
}

// bridgeState returns a bridge-mode state with eight static keys[] painted on
// the deck and the scheduler attached.
func bridgeState(t *testing.T, keys []remoteKey) (*demoState, *fakeDemoDeck) {
	t.Helper()
	state := &demoState{brightness: 70, bridge: true, activeStrip: bridgeStrip(), pollMS: 1000}
	state.applyWireKeys(keys, true)
	deck := newFakeDemoDeck()
	attachBridge(t, state, deck)
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	deck.waitKeyWrites(t, streamdeck.KeyCount)
	deck.settle()
	return state, deck
}

func eightKeys() []remoteKey {
	keys := make([]remoteKey, 0, streamdeck.KeyCount)
	for i := 0; i < streamdeck.KeyCount; i++ {
		keys = append(keys, remoteKey{Index: i, ID: "k" + string(rune('0'+i)), Label: "Key " + string(rune('1'+i)), State: "idle", BG: "#F6F5EE", FG: "#272C24"})
	}
	return keys
}

func pollDemo(keys []remoteKey) remoteDemo {
	demo := remoteDemo{Revision: 3, PollMS: 1000, Keys: keys, Strip: bridgeStrip()}
	if len(keys) > 0 {
		legacy := keys[0]
		demo.Key = &legacy
	}
	return demo
}

// pixelIs reports whether a corner pixel (away from any centered label text)
// is exactly the color.
func pixelIs(t *testing.T, img image.Image, c color.NRGBA) bool {
	t.Helper()
	if img == nil {
		return false
	}
	got := color.NRGBAModel.Convert(img.At(4, 4)).(color.NRGBA)
	return got == c
}

func (d *fakeDemoDeck) pushRead(read fakeRead) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads = append(d.reads, read)
}

func keyEvent(key int, pressed bool) fakeRead {
	return fakeRead{result: streamdeck.InputRead{Events: []streamdeck.Event{streamdeck.KeyEvent{Key: key, Pressed: pressed}}}}
}

// stubPostEvent replaces the event POST for the test with a scripted
// responder that delivers ack after delay.
func stubPostEvent(t *testing.T, delay time.Duration, ack func(payload map[string]any) eventAck) <-chan map[string]any {
	t.Helper()
	posted := make(chan map[string]any, 16)
	original := postEventAsync
	postEventAsync = func(ctx context.Context, payload map[string]any, results chan<- eventPostResult) {
		posted <- payload
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
			select {
			case results <- eventPostResult{ack: ack(payload)}:
			case <-ctx.Done():
			}
		}()
	}
	t.Cleanup(func() { postEventAsync = original })
	return posted
}

func runConnectedAsync(t *testing.T, ctx context.Context, deck *fakeDemoDeck, state *demoState, fetch fetchDemoFunc) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- runConnected(ctx, deck, state, streamdeck.ModelPlus, fetch) }()
	return done
}

// ---------- wire parsing ----------

func TestFetchRemoteDemoParsesKeysArrayVisualAndGeneration(t *testing.T) {
	frame := nativeFrame("f1", testRed)
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"command":"run_hardware_demo","revision":3,"poll_ms":2000,"generation":41,
			"key":{"index":0,"id":"a","label":"A","state":"idle","bg":"#111111","fg":"#eeeeee"},
			"keys":[
				{"index":0,"id":"a","label":"A","sub":"alpha","state":"idle","bg":"#111111","fg":"#eeeeee"},
				{"index":2,"id":"c","label":"C","state":"running","bg":"#222222","fg":"#eeeeee",
				 "image":{"revision":"img-c","mime_type":"image/png","data_b64":"`+frame.Data+`"},
				 "visual":{
				   "revision":"c-running-7","min_visible_ms":400,
				   "rest":{"revision":"rest-c","mime_type":"image/png","data_b64":"`+frame.Data+`"},
				   "animation":{"frames":[
				      {"duration_ms":120,"revision":"f1","mime_type":"image/png","data_b64":"`+frame.Data+`"},
				      {"duration_ms":80,"revision":"f2","mime_type":"image/png","data_b64":"`+frame.Data+`"}
				    ],"loop_count":0,"end":"hold"},
				   "press":{"frames":[{"duration_ms":60,"revision":"p1","mime_type":"image/png","data_b64":"`+frame.Data+`"}],"min_visible_ms":150}
				 }}
			],
			"strip":{"page":0,"pages":1,"title":"Today","lines":["x"]}
		}`), nil
	})
	demo, err := fetchRemoteDemo(context.Background(), client, "http://controller.test/deck")
	if err != nil {
		t.Fatalf("fetchRemoteDemo: %v", err)
	}
	if !isBridgeDemo(demo) || demo.Generation != 41 || len(demo.Keys) != 2 {
		t.Fatalf("demo = bridge:%t generation:%d keys:%d", isBridgeDemo(demo), demo.Generation, len(demo.Keys))
	}
	if demo.Keys[0].Sub != "alpha" || demo.Keys[1].Index != 2 || demo.Keys[1].Image == nil || demo.Keys[1].Image.Revision != "img-c" {
		t.Fatalf("keys = %+v", demo.Keys)
	}
	v := demo.Keys[1].Visual
	if v == nil || v.Revision != "c-running-7" || v.MinVisibleMS != 400 || v.Rest == nil || v.Rest.Revision != "rest-c" {
		t.Fatalf("visual = %+v", v)
	}
	if v.Animation == nil || len(v.Animation.Frames) != 2 || v.Animation.Frames[0].DurationMS != 120 || v.Animation.Frames[1].Revision != "f2" {
		t.Fatalf("animation = %+v", v.Animation)
	}
	if v.Animation.LoopCount == nil || *v.Animation.LoopCount != 0 || v.Animation.End != "hold" {
		t.Fatalf("animation loop/end = %v/%q", v.Animation.LoopCount, v.Animation.End)
	}
	if v.Press == nil || len(v.Press.Frames) != 1 || v.Press.Frames[0].DurationMS != 60 || v.Press.MinVisibleMS != 150 {
		t.Fatalf("press = %+v", v.Press)
	}
	if demo.Keys[0].Visual != nil {
		t.Fatalf("key without visual parsed one: %+v", demo.Keys[0].Visual)
	}
}

func TestPostEventAckStateCarriesKeysArrayAndGeneration(t *testing.T) {
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"ok":true,"events_seen":3,"message":"Key 2 down received",
			"state":{"generation":9,
			  "key":{"index":0,"label":"A"},
			  "keys":[{"index":1,"label":"B","state":"starting","visual":{"revision":"b-starting-1"}}],
			  "strip":{"page":0,"pages":1,"title":"Today","lines":[]}}}`), nil
	})
	ack, err := postEvent(context.Background(), client, "http://controller.test/event", map[string]any{"kind": "key", "index": 1, "pressed": true})
	if err != nil {
		t.Fatalf("postEvent: %v", err)
	}
	if ack.State == nil || ack.State.Generation != 9 || len(ack.State.Keys) != 1 || ack.State.Keys[0].Index != 1 || ack.State.Keys[0].Visual.Revision != "b-starting-1" {
		t.Fatalf("ack state = %+v", ack.State)
	}
}

func TestVisualSchemaFieldNames(t *testing.T) {
	// Lock the additive wire names so the documented schema and the parser
	// cannot drift apart silently.
	payload, err := json.Marshal(remoteKey{Visual: &remoteVisual{
		Revision: "r", MinVisibleMS: 1, Rest: &remoteImage{Revision: "x"},
		Animation: &remoteSequence{Frames: []remoteFrame{{DurationMS: 2}}, LoopCount: intp(0), End: "hold"},
		Press:     &remoteSequence{MinVisibleMS: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"visual":{`, `"revision":"r"`, `"min_visible_ms":1`, `"rest":{`, `"animation":{`, `"frames":[{"duration_ms":2,`, `"loop_count":0`, `"end":"hold"`, `"press":{`, `"min_visible_ms":3`, `"data_b64"`, `"mime_type"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("schema field %s missing from %s", want, payload)
		}
	}
}

// ---------- key resolution and revisions ----------

func TestResolveKeyPrecedence(t *testing.T) {
	state := &demoState{
		activeKey:  &remoteKey{Index: 1, Label: "legacy"},
		background: []remoteKey{{Index: 1, Label: "bg1"}, {Index: 2, Label: "bg2"}, {Index: 3, Label: "bg3"}},
	}
	state.applyWireKeys([]remoteKey{{Index: 2, Label: "keys2"}, {Index: 99, Label: "ignored"}}, true)
	if got := state.resolveKey(2).Label; got != "keys2" {
		t.Fatalf("keys[] should win for index 2, got %q", got)
	}
	if got := state.resolveKey(1).Label; got != "legacy" {
		t.Fatalf("legacy key should win over background for index 1, got %q", got)
	}
	if got := state.resolveKey(3).Label; got != "bg3" {
		t.Fatalf("background should cover index 3, got %q", got)
	}
	if state.resolveKey(4) != nil {
		t.Fatal("uncovered index should resolve to nil (quiet paper)")
	}
	// An ack merges by index; a GET replaces.
	state.applyWireKeys([]remoteKey{{Index: 3, Label: "ack3"}}, false)
	if state.resolveKey(2).Label != "keys2" || state.resolveKey(3).Label != "ack3" {
		t.Fatalf("ack merge wrong: 2=%q 3=%q", state.resolveKey(2).Label, state.resolveKey(3).Label)
	}
	state.applyWireKeys([]remoteKey{{Index: 0, Label: "get0"}}, true)
	if state.resolveKey(2).Label != "bg2" || state.resolveKey(0).Label != "get0" {
		t.Fatalf("GET replace wrong: 2=%q 0=%q", state.resolveKey(2).Label, state.resolveKey(0).Label)
	}
}

func TestKeyRevisionIdentity(t *testing.T) {
	a := &remoteKey{Index: 0, Label: "A", BG: "#111111", FG: "#eeeeee"}
	if keyRevision(a) != keyRevision(&remoteKey{Index: 3, Label: "A", BG: "#111111", FG: "#eeeeee"}) {
		t.Fatal("identical semantic keys must share a revision regardless of index")
	}
	if keyRevision(a) == keyRevision(&remoteKey{Label: "B", BG: "#111111", FG: "#eeeeee"}) {
		t.Fatal("label change must change the revision")
	}
	img := &remoteKey{Label: "A", Image: nativeFrame("img-1", testRed)}
	if keyRevision(img) == keyRevision(a) {
		t.Fatal("image must change the revision")
	}
	img2 := &remoteKey{Label: "A", Image: nativeFrame("img-1", testGreen)}
	if keyRevision(img) != keyRevision(img2) {
		t.Fatal("same image revision must keep the key revision")
	}
	vis := &remoteKey{Label: "A", Visual: &remoteVisual{Revision: "v-1"}}
	if keyRevision(vis) != "visual:v-1" {
		t.Fatalf("visual revision should be authoritative, got %q", keyRevision(vis))
	}
	derived := &remoteKey{Label: "A", Visual: &remoteVisual{MinVisibleMS: 10}}
	derived2 := &remoteKey{Label: "A", Visual: &remoteVisual{MinVisibleMS: 20}}
	if keyRevision(derived) == keyRevision(derived2) || keyRevision(derived) != keyRevision(&remoteKey{Label: "A", Visual: &remoteVisual{MinVisibleMS: 10}}) {
		t.Fatal("derived visual revision must follow content")
	}
	if keyRevision(nil) != "paper" {
		t.Fatal("nil key is quiet paper")
	}
}

// ---------- keys[] rendering ----------

func TestPollKeysArrayUpdatesEveryChangedKeyAndOnlyThose(t *testing.T) {
	keys := eightKeys()
	state, deck := bridgeState(t, keys)
	before := deck.keyWrites()

	changed := eightKeys()
	changed[1].State, changed[1].BG = "running", "#6FA25C"
	changed[4].State, changed[4].BG = "error", "#B3412F"
	changed[7].Image = nativeFrame("img-7", testBlue)
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(changed)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+3)
	deck.settle()
	if deck.keyWrites() != before+3 {
		t.Fatalf("writes = %d, want exactly 3 changed keys", deck.keyWrites()-before)
	}
	touched := map[int]bool{}
	for _, w := range deck.keyWriteLog()[before:] {
		touched[w.index] = true
	}
	if !touched[1] || !touched[4] || !touched[7] || len(touched) != 3 {
		t.Fatalf("repainted keys = %v, want {1,4,7}", touched)
	}
	if !pixelIs(t, deck.keyImage(7), testBlue) {
		t.Fatal("key 7 did not paint its image frame")
	}
	wantBG, _ := keyRenderColors(&changed[4])
	if !pixelIs(t, deck.keyImage(4), color.NRGBAModel.Convert(wantBG).(color.NRGBA)) {
		t.Fatal("key 4 did not paint its new background")
	}

	// The identical poll again is silent.
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(changed)); err != nil {
		t.Fatalf("handlePoll repeat: %v", err)
	}
	deck.settle()
	if deck.keyWrites() != before+3 {
		t.Fatalf("unchanged poll caused %d redundant writes", deck.keyWrites()-before-3)
	}
}

func TestAckKeysArrayForNonzeroKeyRepaintsThatKeyOnly(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	ack := eventAck{OK: true, EventsSeen: 1, Message: "Key 2 down received", State: &remoteState{
		Key:  &remoteKey{Index: 0, Label: "Key 1", State: "idle", BG: "#F6F5EE", FG: "#272C24"},
		Keys: []remoteKey{{Index: 2, Label: "Key 3", State: "starting", Image: nativeFrame("start-3", testYellow)}},
	}}
	if err := handleAck(deck, state, streamdeck.ModelPlus, ack); err != nil {
		t.Fatalf("handleAck: %v", err)
	}
	deck.waitKeyWrites(t, before+1)
	deck.settle()
	log := deck.keyWriteLog()[before:]
	if len(log) != 1 || log[0].index != 2 {
		t.Fatalf("ack writes = %v, want exactly key 2", log)
	}
	if !pixelIs(t, deck.keyImage(2), testYellow) {
		t.Fatal("key 2 did not paint the ack frame")
	}
	if state.resolveKey(2).State != "starting" {
		t.Fatal("ack keys[] entry not adopted")
	}
}

func TestLegacySingularKeyAckRepaintsItsOwnIndex(t *testing.T) {
	// No keys[] anywhere: the legacy singular key is the only source.
	state := &demoState{brightness: 70, bridge: true, activeStrip: bridgeStrip(), pollMS: 1000,
		activeKey:  &remoteKey{Index: 0, Label: "A", BG: "#F6F5EE", FG: "#272C24"},
		background: []remoteKey{{Index: 1, Label: "B", BG: "#272C24", FG: "#F6F5EE"}},
	}
	deck := newFakeDemoDeck()
	attachBridge(t, state, deck)
	if err := restoreDemo(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	deck.waitKeyWrites(t, streamdeck.KeyCount)
	deck.settle()
	before := deck.keyWrites()

	ack := eventAck{OK: true, State: &remoteState{Key: &remoteKey{Index: 1, Label: "B", State: "running", BG: "#6FA25C", FG: "#F6F5EE"}}}
	if err := handleAck(deck, state, streamdeck.ModelPlus, ack); err != nil {
		t.Fatalf("handleAck: %v", err)
	}
	// Key 1 adopts the legacy key; key 0, which the singular key no longer
	// names, falls back to quiet paper. Nothing else repaints.
	deck.waitKeyWrites(t, before+2)
	deck.settle()
	log := deck.keyWriteLog()[before:]
	if len(log) != 2 || (log[0].index != 1 && log[1].index != 1) || (log[0].index != 0 && log[1].index != 0) {
		t.Fatalf("legacy ack writes = %v, want exactly keys 1 and 0", log)
	}
	wantBG, _ := keyRenderColors(ack.State.Key)
	if !pixelIs(t, deck.keyImage(1), color.NRGBAModel.Convert(wantBG).(color.NRGBA)) {
		t.Fatal("key 1 did not adopt the legacy ack key")
	}
	if !pixelIs(t, deck.keyImage(0), bridgePaperBG) {
		t.Fatal("key 0 should return to quiet paper when the legacy key moves")
	}
}

func TestMiniBridgeRendersSixNativeKeys(t *testing.T) {
	state := &demoState{brightness: 70, bridge: true, activeStrip: bridgeStrip(), pollMS: 1000}
	state.applyWireKeys(eightKeys(), true)
	deck := newFakeDemoDeck()
	attachBridge(t, state, deck)
	if err := restoreDemo(deck, state, streamdeck.ModelMini); err != nil {
		t.Fatalf("restoreDemo: %v", err)
	}
	deck.waitKeyWrites(t, streamdeck.MiniKeyCount)
	deck.settle()
	if deck.keyWrites() != streamdeck.MiniKeyCount {
		t.Fatalf("mini key writes = %d, want %d", deck.keyWrites(), streamdeck.MiniKeyCount)
	}
	for i := 0; i < streamdeck.MiniKeyCount; i++ {
		if b := deck.keyImage(i).Bounds(); b.Dx() != streamdeck.MiniKeyImageWidth || b.Dy() != streamdeck.MiniKeyImageHeight {
			t.Fatalf("mini key %d bounds = %v", i, b)
		}
	}
}

// ---------- press feedback ----------

func TestKeyDownStartsLocalFeedbackBeforeDelayedAck(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	rest := deck.keyImage(0)
	before := deck.keyWrites()

	ackDelivered := make(chan struct{})
	var once sync.Once
	stubPostEvent(t, 200*time.Millisecond, func(payload map[string]any) eventAck {
		once.Do(func() { close(ackDelivered) })
		// Same resting presentation: the action started but its run record
		// is not visible yet.
		return eventAck{OK: true, State: &remoteState{Keys: eightKeys()}}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deck.pushRead(keyEvent(0, true))
	done := runConnectedAsync(t, ctx, deck, state, nil)

	deck.waitKeyWrites(t, before+1)
	select {
	case <-ackDelivered:
		t.Fatal("press feedback was not painted before the controller acknowledged")
	default:
	}
	pressed := deck.keyImage(0)
	if pressed == rest || !state.visuals().Pressed(0) {
		t.Fatal("key 0 did not get local press feedback")
	}
	r0, _, _, _ := rest.At(60, 60).RGBA()
	r1, _, _, _ := pressed.At(60, 60).RGBA()
	if r1 >= r0 {
		t.Fatalf("generic press feedback not a depression (%d >= %d)", r1, r0)
	}

	// The same-state ack arrives: press feedback stays until key-up.
	<-ackDelivered
	time.Sleep(50 * time.Millisecond)
	if deck.keyWrites() != before+1 || !state.visuals().Pressed(0) {
		t.Fatalf("same-state ack disturbed press feedback: writes=%d pressed=%t", deck.keyWrites()-before, state.visuals().Pressed(0))
	}

	// Key-up: the key returns to rest (identical frame object => cached).
	deck.pushRead(keyEvent(0, false))
	deck.waitKeyWrites(t, before+2)
	waitUntil(t, "press release", func() bool { return !state.visuals().Pressed(0) })
	if deck.keyImage(0) != rest {
		t.Fatal("key 0 did not return to its resting frame after key-up")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runConnected: %v", err)
	}
}

func TestControllerPressVisualOverridesGenericFallback(t *testing.T) {
	keys := eightKeys()
	keys[3].Visual = &remoteVisual{
		Revision: "k3-rest-1",
		Press:    &remoteSequence{Frames: []remoteFrame{wireFrame("k3-press", testBlue, 80)}, MinVisibleMS: 60},
	}
	state, deck := bridgeState(t, keys)
	before := deck.keyWrites()
	state.visuals().Press(3)
	deck.waitKeyWrites(t, before+1)
	if !pixelIs(t, deck.keyImage(3), testBlue) {
		t.Fatal("controller press frame was not used")
	}
	state.visuals().Release(3)
	deck.waitKeyWrites(t, before+2)
	if pixelIs(t, deck.keyImage(3), testBlue) {
		t.Fatal("key did not return to rest after release")
	}
}

func TestKeyUpBeforePaintStillShowsAndThenReleases(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	stubPostEvent(t, time.Hour, func(map[string]any) eventAck { return eventAck{OK: true} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deck.pushRead(fakeRead{result: streamdeck.InputRead{Events: []streamdeck.Event{
		streamdeck.KeyEvent{Key: 5, Pressed: true},
		streamdeck.KeyEvent{Key: 5, Pressed: false},
	}}})
	done := runConnectedAsync(t, ctx, deck, state, nil)
	started := time.Now()
	deck.waitKeyWrites(t, before+2)
	if elapsed := time.Since(started); elapsed < visual.DefaultPressMinVisible-20*time.Millisecond {
		t.Fatalf("press feedback released after %s, want at least %s", elapsed, visual.DefaultPressMinVisible)
	}
	log := deck.keyWriteLog()[before:]
	if log[0].index != 5 || log[1].index != 5 || log[1].img == log[0].img {
		t.Fatalf("press/release writes = %v", log)
	}
	cancel()
	<-done
}

func TestAckWithNewVisualSupersedesPressAndAnimates(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	starting := eightKeys()
	starting[2].Visual = &remoteVisual{
		Revision:     "k2-starting-1",
		MinVisibleMS: 100,
		Animation: &remoteSequence{Frames: []remoteFrame{
			wireFrame("s0", testRed, 40), wireFrame("s1", testGreen, 40), wireFrame("s2", testBlue, 40),
		}},
	}
	stubPostEvent(t, 20*time.Millisecond, func(map[string]any) eventAck {
		return eventAck{OK: true, State: &remoteState{Keys: starting[2:3]}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deck.pushRead(keyEvent(2, true))
	done := runConnectedAsync(t, ctx, deck, state, nil)
	// press, then s0 s1 s2, then rest
	deck.waitKeyWrites(t, before+5)
	deck.settle()
	log := deck.keyWriteLog()[before:]
	if len(log) != 5 {
		t.Fatalf("writes = %d", len(log))
	}
	if !pixelIs(t, log[1].img, testRed) || !pixelIs(t, log[2].img, testGreen) || !pixelIs(t, log[3].img, testBlue) {
		t.Fatal("starting animation frames out of order")
	}
	if state.visuals().Pressed(2) {
		t.Fatal("press overlay survived controller visual")
	}
	cancel()
	<-done
}

// ---------- animation through the bridge ----------

func TestAnimationPlaysFramesInOrderThenRests(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	keys := eightKeys()
	keys[1].Visual = &remoteVisual{Revision: "anim-1", Animation: &remoteSequence{
		Frames:    []remoteFrame{wireFrame("a0", testRed, 40), wireFrame("a1", testGreen, 40)},
		LoopCount: intp(2),
	}}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+5)
	deck.settle()
	log := deck.keyWriteLog()[before:]
	want := []color.NRGBA{testRed, testGreen, testRed, testGreen}
	for i, c := range want {
		if log[i].index != 1 || !pixelIs(t, log[i].img, c) {
			t.Fatalf("frame %d wrong: key %d", i, log[i].index)
		}
	}
	if log[4].index != 1 || pixelIs(t, log[4].img, testGreen) {
		t.Fatal("animation did not return to rest")
	}
}

func TestLoopingAnimationIsCancelledByNewAck(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	keys := eightKeys()
	keys[6].Visual = &remoteVisual{Revision: "running-1", Animation: &remoteSequence{
		Frames:    []remoteFrame{wireFrame("r0", testRed, 40), wireFrame("r1", testGreen, 40)},
		LoopCount: intp(0),
	}}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+4)

	success := eightKeys()
	success[6].Visual = &remoteVisual{Revision: "success-1", Rest: nativeFrame("ok", testYellow)}
	ack := eventAck{OK: true, State: &remoteState{Keys: success[6:7]}}
	if err := handleAck(deck, state, streamdeck.ModelPlus, ack); err != nil {
		t.Fatalf("handleAck: %v", err)
	}
	waitUntil(t, "success frame", func() bool { return pixelIs(t, deck.keyImage(6), testYellow) })
	mark := deck.keyWrites()
	time.Sleep(150 * time.Millisecond)
	if deck.keyWrites() != mark {
		t.Fatalf("cancelled loop kept writing: %d extra writes", deck.keyWrites()-mark)
	}
	for _, w := range deck.keyWriteLog()[mark:] {
		if pixelIs(t, w.img, testRed) || pixelIs(t, w.img, testGreen) {
			t.Fatal("stale loop frame written after newer visual")
		}
	}
}

func TestInvalidAnimationFallsBackToStaticAndLogsOnce(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	keys := eightKeys()
	keys[0].Image = nativeFrame("rest-0", testBlue)
	keys[0].Visual = &remoteVisual{Revision: "broken-1", Animation: &remoteSequence{Frames: []remoteFrame{
		wireFrame("ok", testRed, 40),
		{DurationMS: 40, remoteImage: remoteImage{Revision: "bad", Data: "!!!not-base64!!!"}},
	}}}
	keys[1].Visual = &remoteVisual{Revision: "empty-1", Animation: &remoteSequence{}}
	keys[2].Visual = &remoteVisual{Revision: "unknown-ref-1", Press: &remoteSequence{Frames: []remoteFrame{{DurationMS: 40, remoteImage: remoteImage{Revision: "never-sent"}}}}}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	// Keys 0 (new image) repaints to its static rest; keys 1 and 2 have the
	// same semantic rest but a new visual revision, so they repaint too
	// (same-content frames) but never animate.
	deck.waitKeyWrites(t, before+3)
	deck.settle()
	if deck.keyWrites() != before+3 {
		t.Fatalf("invalid animations produced %d writes, want 3 static repaints", deck.keyWrites()-before)
	}
	if !pixelIs(t, deck.keyImage(0), testBlue) {
		t.Fatal("key 0 did not fall back to its static image")
	}
	if len(state.invalidSequences) != 3 {
		t.Fatalf("invalid sequences logged = %d, want 3", len(state.invalidSequences))
	}
	// Key 2's press falls back to the generic depression.
	state.visuals().Press(2)
	deck.waitKeyWrites(t, before+4)
	state.visuals().Release(2)
}

func TestSimultaneousAnimationsOnMultipleKeysKeepOrder(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	keys := eightKeys()
	keys[0].Visual = &remoteVisual{Revision: "a", Animation: &remoteSequence{Frames: []remoteFrame{wireFrame("a0", testRed, 40), wireFrame("a1", testGreen, 40)}}}
	keys[5].Visual = &remoteVisual{Revision: "b", Animation: &remoteSequence{Frames: []remoteFrame{wireFrame("b0", testBlue, 60), wireFrame("b1", testYellow, 60)}}}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+6)
	deck.settle()
	perKey := map[int][]image.Image{}
	for _, w := range deck.keyWriteLog()[before:] {
		perKey[w.index] = append(perKey[w.index], w.img)
	}
	if len(perKey) != 2 || len(perKey[0]) != 3 || len(perKey[5]) != 3 {
		t.Fatalf("per-key writes = %v", perKey)
	}
	if !pixelIs(t, perKey[0][0], testRed) || !pixelIs(t, perKey[0][1], testGreen) || !pixelIs(t, perKey[5][0], testBlue) || !pixelIs(t, perKey[5][1], testYellow) {
		t.Fatal("per-key frame order broken")
	}
}

func TestVisualRestOverridesImageAndMinVisibleIsHonored(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	keys := eightKeys()
	keys[4].Image = nativeFrame("img-4", testRed)
	keys[4].Visual = &remoteVisual{Revision: "v1", MinVisibleMS: 200, Rest: nativeFrame("rest-4", testGreen)}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+1)
	painted := time.Now()
	if !pixelIs(t, deck.keyImage(4), testGreen) {
		t.Fatal("visual.rest did not override image")
	}
	keys[4].Visual = &remoteVisual{Revision: "v2", Rest: nativeFrame("rest-4b", testBlue)}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, pollDemo(keys)); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	deck.waitKeyWrites(t, before+2)
	if held := time.Since(painted); held < 180*time.Millisecond {
		t.Fatalf("min_visible_ms not honored: replaced after %s", held)
	}
	if !pixelIs(t, deck.keyImage(4), testBlue) {
		t.Fatal("new visual not applied after hold")
	}
}

// ---------- generation ordering ----------

func TestStalePollAndAckAreIgnoredUntilResync(t *testing.T) {
	state, deck := bridgeState(t, eightKeys())
	before := deck.keyWrites()
	fresh := eightKeys()
	fresh[0].State, fresh[0].BG = "running", "#6FA25C"
	ack := eventAck{OK: true, State: &remoteState{Generation: 10, Keys: fresh[0:1]}}
	if err := handleAck(deck, state, streamdeck.ModelPlus, ack); err != nil {
		t.Fatal(err)
	}
	deck.waitKeyWrites(t, before+1)

	stale := pollDemo(eightKeys())
	stale.Generation = 9
	for i := 0; i < 2; i++ {
		if err := handlePoll(deck, state, streamdeck.ModelPlus, stale); err != nil {
			t.Fatal(err)
		}
	}
	staleAck := eventAck{OK: true, State: &remoteState{Generation: 8, Keys: eightKeys()[0:1]}}
	if err := handleAck(deck, state, streamdeck.ModelPlus, staleAck); err != nil {
		t.Fatal(err)
	}
	deck.settle()
	if deck.keyWrites() != before+1 || state.resolveKey(0).State != "running" {
		t.Fatalf("stale payload applied: writes=%d state=%q", deck.keyWrites()-before, state.resolveKey(0).State)
	}
	// Third stale poll in a row: the controller restarted; resynchronize.
	if err := handlePoll(deck, state, streamdeck.ModelPlus, stale); err != nil {
		t.Fatal(err)
	}
	deck.waitKeyWrites(t, before+2)
	if state.generation != 9 || state.resolveKey(0).State != "idle" {
		t.Fatalf("resync failed: generation=%d state=%q", state.generation, state.resolveKey(0).State)
	}
	// Unordered payloads (generation 0) always apply.
	unordered := pollDemo(fresh)
	if err := handlePoll(deck, state, streamdeck.ModelPlus, unordered); err != nil {
		t.Fatal(err)
	}
	deck.waitKeyWrites(t, before+3)
}

// ---------- reconnect and shutdown ----------

func TestReconnectRestoresSteadyStateNotTransientAnimation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keys := eightKeys()
	keys[0].Visual = &remoteVisual{Revision: "starting-1", Animation: &remoteSequence{Frames: []remoteFrame{
		wireFrame("s0", testRed, 5000), wireFrame("s1", testGreen, 5000),
	}}}
	keys[1].Visual = &remoteVisual{Revision: "running-1", Animation: &remoteSequence{Frames: []remoteFrame{
		wireFrame("r0", testBlue, 5000), wireFrame("r1", testYellow, 5000),
	}, LoopCount: intp(0)}}
	demo := pollDemo(keys)
	demo.Presentation.Brightness = 70
	fetch := func(context.Context) (remoteDemo, error) { return demo, nil }

	first := newFakeDemoDeck()
	second := newFakeDemoDeck()
	// The first connection dies after the animation's first frames painted.
	first.onKeyImage = func(int, image.Image) {
		if len(first.keyImages) >= streamdeck.KeyCount && len(first.reads) == 0 {
			first.reads = []fakeRead{{err: context.DeadlineExceeded}}
		}
	}
	second.onKeyImage = func(int, image.Image) {
		if len(second.keyImages) >= streamdeck.KeyCount {
			cancel()
		}
	}
	decks := []*fakeDemoDeck{first, second}
	opens := 0
	open := func() (demoDeck, error) {
		if opens >= len(decks) {
			return nil, context.Canceled
		}
		deck := decks[opens]
		opens++
		return deck, nil
	}
	state := &demoState{brightness: 70}
	if err := runDemo(ctx, state, open, fetch, time.Millisecond); err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	if opens != 2 {
		t.Fatalf("opens = %d", opens)
	}
	if !pixelIs(t, first.keyImage(0), testRed) || !pixelIs(t, first.keyImage(1), testBlue) {
		t.Fatal("first connection did not show the animations' first frames")
	}
	// Second connection (its first write per key, before the shutdown
	// background repaint): the finite "starting" animation is not replayed,
	// key 0 shows its steady resting frame; the unbounded loop restarts.
	firstWrite := map[int]image.Image{}
	for _, w := range second.keyWriteLog() {
		if _, seen := firstWrite[w.index]; !seen {
			firstWrite[w.index] = w.img
		}
	}
	if pixelIs(t, firstWrite[0], testRed) || pixelIs(t, firstWrite[0], testGreen) {
		t.Fatal("reconnect replayed a transient finite animation")
	}
	if !pixelIs(t, firstWrite[1], testBlue) {
		t.Fatal("reconnect did not restart the looping animation from its first frame")
	}
	if len(firstWrite) != streamdeck.KeyCount {
		t.Fatalf("second connection restored %d keys", len(firstWrite))
	}
	if state.keyVisuals != nil {
		t.Fatal("scheduler not closed after runDemo returned")
	}
}

func TestShutdownStopsSchedulerGoroutine(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	keys := eightKeys()
	keys[3].Visual = &remoteVisual{Revision: "loop", Animation: &remoteSequence{Frames: []remoteFrame{
		wireFrame("l0", testRed, 40), wireFrame("l1", testGreen, 40),
	}, LoopCount: intp(0)}}
	demo := pollDemo(keys)
	deck := newFakeDemoDeck()
	deck.onKeyImage = func(int, image.Image) {
		if deck.keyImageCalls >= streamdeck.KeyCount+3 {
			cancel()
		}
	}
	state := &demoState{brightness: 70}
	open := func() (demoDeck, error) { return deck, nil }
	if err := runDemo(ctx, state, open, func(context.Context) (remoteDemo, error) { return demo, nil }, time.Millisecond); err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	if state.keyVisuals != nil {
		t.Fatal("scheduler still referenced after shutdown")
	}
	writes := deck.keyWrites()
	time.Sleep(100 * time.Millisecond)
	// Shutdown repaints the background frames once (renderBackgroundKeys);
	// nothing animates afterwards.
	if deck.keyWrites() != writes {
		t.Fatalf("key writes continued after shutdown: %d", deck.keyWrites()-writes)
	}
	waitUntil(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= baseline })
}

func TestRenderBackgroundKeysAtShutdownUsesRestFrames(t *testing.T) {
	state := &demoState{brightness: 65, bridge: true, background: []remoteKey{
		{Index: 2, Label: "Rack", Image: nativeFrame("rack", testInk), Visual: &remoteVisual{Revision: "x", Animation: &remoteSequence{Frames: []remoteFrame{wireFrame("z", testRed, 40)}}}},
	}}
	deck := newFakeDemoDeck()
	if err := renderBackgroundKeys(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderBackgroundKeys: %v", err)
	}
	if !pixelIs(t, deck.keyImage(2), testInk) {
		t.Fatal("background persisted an animation frame instead of the resting image")
	}
	want := render.KeySize(render.KeyView{Index: 4, BG: bridgePaperBG}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertImagesEqual(t, deck.keyImage(4), want, "uncovered key")
}
