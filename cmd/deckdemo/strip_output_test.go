package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

func TestStripRasterPNGAndJPEGDecodeAndScale(t *testing.T) {
	green := color.NRGBA{R: 0x28, G: 0x91, B: 0x48, A: 0xff}
	tests := []struct {
		name  string
		image *remoteImage
	}{
		{name: "PNG", image: testFrame("strip-png", 3, 7, green)},
		{name: "JPEG", image: testJPEGFrame("strip-jpeg", 13, 5, green)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := rasterStripState(test.image)
			desired := state.stripPresentation()
			if desired.baseRevision != "raster:"+test.image.Revision {
				t.Fatalf("base revision = %q", desired.baseRevision)
			}
			if got := desired.base.Bounds().Size(); got != (image.Point{X: streamdeck.TouchStripWidth, Y: streamdeck.TouchStripHeight}) {
				t.Fatalf("scaled bounds = %v, want 800x100", got)
			}
			got := color.NRGBAModel.Convert(desired.base.At(400, 50)).(color.NRGBA)
			if colorDistance(got, green) > 8 {
				t.Fatalf("scaled center = %#v, want near %#v", got, green)
			}
		})
	}
}

func TestStripRasterTakesPrecedenceOverSemanticRenderer(t *testing.T) {
	red := color.NRGBA{R: 0xd0, G: 0x20, B: 0x10, A: 0xff}
	state := rasterStripState(testFrame("strip-red", 800, 100, red))
	state.activeStrip.Title = "This must not be drawn"
	state.activeStrip.Lines = []string{"nor this"}
	state.activeStrip.Pages = 99
	state.remoteEventsSeen = 1234
	state.remoteLast = "nor this event"
	deck := newFakeDemoDeck()

	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderStrip: %v", err)
	}
	if len(deck.stripImages) != 1 {
		t.Fatalf("full writes = %d, want 1", len(deck.stripImages))
	}
	for _, point := range []image.Point{{0, 0}, {20, 10}, {400, 50}, {799, 99}} {
		if got := color.NRGBAModel.Convert(deck.stripImages[0].At(point.X, point.Y)).(color.NRGBA); got != red {
			t.Fatalf("pixel %v = %#v, want untouched raster %#v", point, got, red)
		}
	}
}

func TestStripMissingOrInvalidRasterUsesSemanticFallback(t *testing.T) {
	tests := []struct {
		name  string
		image *remoteImage
	}{
		{name: "missing"},
		{name: "invalid", image: &remoteImage{Revision: "broken-strip", MimeType: "image/png", Data: "not-base64"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &demoState{
				bridge: true,
				activeStrip: &remoteStrip{
					Page: 1, Pages: 3, Title: "Fallback", Lines: []string{"semantic line"}, Image: test.image,
				},
				remoteEventsSeen: 7,
				remoteLast:       "event",
			}
			deck := newFakeDemoDeck()
			if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
				t.Fatalf("renderStrip: %v", err)
			}
			want := render.Strip(state.stripView())
			assertImagesEqual(t, deck.stripImages[0], want, "semantic strip fallback")
		})
	}
}

func TestStripFailedDecodeIsCachedByRevision(t *testing.T) {
	state := rasterStripState(&remoteImage{Revision: "broken-once", MimeType: "image/png", Data: "not-base64"})
	first := state.stripPresentation()
	if first.baseRevision[:9] != "semantic:" {
		t.Fatalf("first presentation = %q, want fallback", first.baseRevision)
	}

	state.activeStrip.Image = testFrame("broken-once", 2, 2, color.NRGBA{R: 0xff, A: 0xff})
	second := state.stripPresentation()
	if second.baseRevision[:9] != "semantic:" {
		t.Fatalf("failed revision was decoded again: %q", second.baseRevision)
	}
}

func TestUnchangedAndChangedStripRevisionsGateFullWrites(t *testing.T) {
	red := color.NRGBA{R: 0xd0, A: 0xff}
	green := color.NRGBA{G: 0xa0, A: 0xff}
	state := rasterStripState(testFrame("strip-1", 4, 4, red))
	deck := newFakeDemoDeck()

	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("unchanged render: %v", err)
	}
	if len(deck.stripImages) != 1 {
		t.Fatalf("unchanged revision full writes = %d, want 1 total", len(deck.stripImages))
	}

	state.activeStrip.Image = testFrame("strip-2", 4, 4, green)
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("changed render: %v", err)
	}
	if len(deck.stripImages) != 2 {
		t.Fatalf("changed revision full writes = %d, want 2 total", len(deck.stripImages))
	}
}

func TestRepeatedUnchangedPollPerformsNoStripWrite(t *testing.T) {
	frame := testFrame("poll-strip", 4, 4, color.NRGBA{B: 0xc0, A: 0xff})
	state := rasterStripState(frame)
	state.pollMS = 2000
	state.activeKey = &remoteKey{Index: 0, Label: "Demo"}
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("initial render: %v", err)
	}

	poll := remoteDemo{Revision: 2, PollMS: 2000, Key: state.activeKey, Strip: state.activeStrip}
	if err := handlePoll(deck, state, streamdeck.ModelPlus, poll); err != nil {
		t.Fatalf("handlePoll: %v", err)
	}
	if len(deck.stripImages) != 1 {
		t.Fatalf("full writes after unchanged poll = %d, want 1", len(deck.stripImages))
	}
}

func TestFailedFullWriteDoesNotMarkRevisionDisplayed(t *testing.T) {
	deck := &recordingTouchDeck{failFull: 1}
	var output stripOutputState
	desired := &stripPresentation{
		baseRevision: "failed-full",
		base:         image.NewNRGBA(image.Rect(0, 0, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight)),
	}
	if err := output.enqueue(deck, desired); err == nil {
		t.Fatal("failed full write returned nil error")
	}
	output.mu.Lock()
	displayed := output.displayedBase
	output.mu.Unlock()
	if displayed != "" {
		t.Fatalf("failed revision marked displayed: %q", displayed)
	}
	if err := output.enqueue(deck, desired); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if deck.fullAttempts != 2 || deck.fullSuccesses() != 1 {
		t.Fatalf("full attempts/successes = %d/%d, want 2/1", deck.fullAttempts, deck.fullSuccesses())
	}
}

func TestReconnectRestoresUnchangedFullRevisionExactlyOnce(t *testing.T) {
	state := rasterStripState(testFrame("stable-strip", 4, 4, color.NRGBA{R: 0x44, A: 0xff}))
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("first render: %v", err)
	}
	state.stripOutput.startConnection()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("restore render: %v", err)
	}
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("post-restore unchanged render: %v", err)
	}
	if len(deck.stripImages) != 2 {
		t.Fatalf("full writes = %d, want initial + one restoration", len(deck.stripImages))
	}
}

func TestReconnectAndBaseChangePaintBaseBeforeRegions(t *testing.T) {
	deck := &recordingTouchDeck{}
	var output stripOutputState
	regionImage := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	desired := func(base string) *stripPresentation {
		return &stripPresentation{
			baseRevision: base,
			base:         image.NewNRGBA(image.Rect(0, 0, 800, 100)),
			regions: []stripRegionFrame{
				{x: 10, y: 5, revision: "region", img: regionImage},
			},
		}
	}
	if err := output.enqueue(deck, desired("base-1")); err != nil {
		t.Fatalf("initial display: %v", err)
	}
	if err := output.enqueue(deck, desired("base-2")); err != nil {
		t.Fatalf("base change: %v", err)
	}
	output.startConnection()
	if err := output.enqueue(deck, desired("base-2")); err != nil {
		t.Fatalf("reconnect display: %v", err)
	}
	if got, want := deck.operationsSnapshot(), []string{"full", "partial", "full", "partial", "full", "partial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operation order = %v, want %v", got, want)
	}
}

func TestChangedAndUnchangedStripRegionsUsePartialWrites(t *testing.T) {
	base := testFrame("base-1", 8, 8, color.NRGBA{R: 0x10, A: 0xff})
	first := regionFrame(12, 8, "region-1", 20, 10, color.NRGBA{G: 0x80, A: 0xff})
	state := rasterStripState(base, first)
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if len(deck.stripImages) != 1 || len(deck.stripRegions) != 1 {
		t.Fatalf("initial full/partial writes = %d/%d, want 1/1", len(deck.stripImages), len(deck.stripRegions))
	}

	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("unchanged render: %v", err)
	}
	if len(deck.stripRegions) != 1 {
		t.Fatalf("unchanged region wrote again: %d writes", len(deck.stripRegions))
	}

	state.activeStrip.Regions[0] = regionFrame(12, 8, "region-2", 20, 10, color.NRGBA{B: 0x90, A: 0xff})
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("changed render: %v", err)
	}
	if len(deck.stripImages) != 1 || len(deck.stripRegions) != 2 {
		t.Fatalf("changed full/partial writes = %d/%d, want 1/2", len(deck.stripImages), len(deck.stripRegions))
	}
	last := deck.stripRegions[1]
	if last.x != 12 || last.y != 8 || last.img.Bounds().Size() != (image.Point{X: 20, Y: 10}) {
		t.Fatalf("partial write = (%d,%d) %v", last.x, last.y, last.img.Bounds().Size())
	}
}

func TestRemovedRegionRestoresBaseThenRemainingRegions(t *testing.T) {
	state := rasterStripState(
		testFrame("base-remove", 4, 4, color.NRGBA{R: 0x20, A: 0xff}),
		regionFrame(0, 0, "left", 20, 20, color.NRGBA{G: 0x70, A: 0xff}),
		regionFrame(100, 0, "right", 20, 20, color.NRGBA{B: 0x70, A: 0xff}),
	)
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("first render: %v", err)
	}
	state.activeStrip.Regions = state.activeStrip.Regions[:1]
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("removed render: %v", err)
	}
	if len(deck.stripImages) != 2 {
		t.Fatalf("full writes = %d, want restoration write", len(deck.stripImages))
	}
	if len(deck.stripRegions) != 3 {
		t.Fatalf("partial writes = %d, want two initial + one reapplied", len(deck.stripRegions))
	}
}

func TestInvalidStripRegionsNeverReachOutput(t *testing.T) {
	validPixels := testFrame("region-pixels", 20, 10, color.NRGBA{G: 0x50, A: 0xff})
	state := rasterStripState(testFrame("valid-base", 4, 4, color.NRGBA{R: 0x20, A: 0xff}))
	state.activeStrip.Regions = []remoteRegion{
		{X: -1, Y: 0, remoteImage: *validPixels},
		{X: 0, Y: -1, remoteImage: *testFrame("negative-y", 20, 10, color.NRGBA{G: 0x50, A: 0xff})},
		{X: 790, Y: 0, remoteImage: *testFrame("right-overflow", 20, 10, color.NRGBA{G: 0x50, A: 0xff})},
		{X: 0, Y: 95, remoteImage: *testFrame("bottom-overflow", 20, 10, color.NRGBA{G: 0x50, A: 0xff})},
		{X: 0, Y: 0, remoteImage: remoteImage{Revision: "empty", MimeType: "image/png"}},
	}
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderStrip: %v", err)
	}
	if len(deck.stripImages) != 1 || len(deck.stripRegions) != 0 {
		t.Fatalf("full/partial writes = %d/%d, want 1/0", len(deck.stripImages), len(deck.stripRegions))
	}
}

func TestRegionsIgnoredWithoutValidFullBase(t *testing.T) {
	state := rasterStripState(
		&remoteImage{Revision: "invalid-base", MimeType: "image/png", Data: "broken"},
		regionFrame(0, 0, "valid-region", 20, 10, color.NRGBA{G: 0x90, A: 0xff}),
	)
	deck := newFakeDemoDeck()
	if err := renderStrip(deck, state, streamdeck.ModelPlus); err != nil {
		t.Fatalf("renderStrip: %v", err)
	}
	if len(deck.stripImages) != 1 || len(deck.stripRegions) != 0 {
		t.Fatalf("full/partial writes = %d/%d, want semantic full only", len(deck.stripImages), len(deck.stripRegions))
	}
	assertImagesEqual(t, deck.stripImages[0], render.Strip(state.stripView()), "fallback without patch")
}

func TestStripFrameCacheRemainsBounded(t *testing.T) {
	state := rasterStripState(nil)
	for index := 0; index < frameCacheLimit+20; index++ {
		state.activeStrip.Image = testFrame("bounded-"+strconv.Itoa(index), 2, 2, color.NRGBA{R: uint8(index), A: 0xff})
		state.stripPresentation()
	}
	state.stripOutput.mu.Lock()
	entries := len(state.stripOutput.frames)
	order := len(state.stripOutput.frameOrder)
	state.stripOutput.mu.Unlock()
	if entries > frameCacheLimit || order > frameCacheLimit {
		t.Fatalf("cache entries/order = %d/%d, limit %d", entries, order, frameCacheLimit)
	}
}

func TestStripOutputSerializesPollAndAckWrites(t *testing.T) {
	deck := &recordingTouchDeck{delay: 20 * time.Millisecond}
	var output stripOutputState
	first := &stripPresentation{baseRevision: "poll", base: image.NewNRGBA(image.Rect(0, 0, 800, 100))}
	second := &stripPresentation{baseRevision: "ack", base: image.NewNRGBA(image.Rect(0, 0, 800, 100))}

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		if err := output.enqueue(deck, first); err != nil {
			t.Errorf("poll enqueue: %v", err)
		}
	}()
	go func() {
		defer wait.Done()
		if err := output.enqueue(deck, second); err != nil {
			t.Errorf("ack enqueue: %v", err)
		}
	}()
	wait.Wait()
	if deck.maximumConcurrent() != 1 {
		t.Fatalf("maximum concurrent touch writes = %d, want 1", deck.maximumConcurrent())
	}
}

func TestStripOutputPacesAndCoalescesNewestDesiredFrame(t *testing.T) {
	deck := &recordingTouchDeck{}
	var output stripOutputState
	output.configure(30*time.Millisecond, 0)
	frame := func(revision string, value uint8) *stripPresentation {
		img := image.NewNRGBA(image.Rect(0, 0, 800, 100))
		img.SetNRGBA(0, 0, color.NRGBA{R: value, A: 0xff})
		return &stripPresentation{baseRevision: revision, base: img}
	}
	if err := output.enqueue(deck, frame("first", 1)); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := output.enqueue(deck, frame("superseded", 2)); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if err := output.enqueue(deck, frame("newest", 3)); err != nil {
		t.Fatalf("third enqueue: %v", err)
	}
	if deck.fullSuccesses() != 1 {
		t.Fatalf("writes before interval = %d, want 1", deck.fullSuccesses())
	}
	time.Sleep(40 * time.Millisecond)
	if err := output.flush(deck); err != nil {
		t.Fatalf("flush: %v", err)
	}
	images := deck.fullImagesSnapshot()
	if len(images) != 2 {
		t.Fatalf("writes after flush = %d, want 2", len(images))
	}
	if got := color.NRGBAModel.Convert(images[1].At(0, 0)).(color.NRGBA).R; got != 3 {
		t.Fatalf("coalesced frame pixel = %d, want newest 3", got)
	}
}

func rasterStripState(base *remoteImage, regions ...remoteRegion) *demoState {
	return &demoState{
		bridge: true,
		activeStrip: &remoteStrip{
			Page: 0, Pages: 1, Title: "Semantic fallback", Lines: []string{"fallback"},
			Image: base, Regions: regions,
		},
	}
}

func regionFrame(x, y int, revision string, width, height int, c color.NRGBA) remoteRegion {
	return remoteRegion{X: x, Y: y, remoteImage: *testFrame(revision, width, height, c)}
}

func testJPEGFrame(revision string, width, height int, c color.NRGBA) *remoteImage {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 95}); err != nil {
		panic(err)
	}
	return &remoteImage{Revision: revision, MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}
}

func colorDistance(a, b color.NRGBA) int {
	abs := func(value int) int {
		if value < 0 {
			return -value
		}
		return value
	}
	return abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
}

type recordingTouchDeck struct {
	mu            sync.Mutex
	fullImages    []image.Image
	partialImages []fakeStripRegion
	fullAttempts  int
	failFull      int
	failPartial   int
	active        int
	maxActive     int
	delay         time.Duration
	operations    []string
}

func (d *recordingTouchDeck) SetTouchStripImage(img image.Image) error {
	d.beginWrite()
	defer d.endWrite()
	d.mu.Lock()
	d.fullAttempts++
	if d.failFull > 0 {
		d.failFull--
		d.mu.Unlock()
		return errors.New("injected full write failure")
	}
	d.fullImages = append(d.fullImages, img)
	d.operations = append(d.operations, "full")
	d.mu.Unlock()
	return nil
}

func (d *recordingTouchDeck) SetPartialWindowImage(x, y int, img image.Image) error {
	d.beginWrite()
	defer d.endWrite()
	d.mu.Lock()
	if d.failPartial > 0 {
		d.failPartial--
		d.mu.Unlock()
		return errors.New("injected partial write failure")
	}
	d.partialImages = append(d.partialImages, fakeStripRegion{x: x, y: y, img: img})
	d.operations = append(d.operations, "partial")
	d.mu.Unlock()
	return nil
}

func (d *recordingTouchDeck) beginWrite() {
	d.mu.Lock()
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	delay := d.delay
	d.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
}

func (d *recordingTouchDeck) endWrite() {
	d.mu.Lock()
	d.active--
	d.mu.Unlock()
}

func (d *recordingTouchDeck) fullSuccesses() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.fullImages)
}

func (d *recordingTouchDeck) maximumConcurrent() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxActive
}

func (d *recordingTouchDeck) fullImagesSnapshot() []image.Image {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]image.Image(nil), d.fullImages...)
}

func (d *recordingTouchDeck) operationsSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.operations...)
}
