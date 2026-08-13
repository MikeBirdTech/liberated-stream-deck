package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const (
	defaultTouchStripUploadInterval = 100 * time.Millisecond
	defaultTouchStripFailureDelay   = time.Second
	touchStripIntervalEnvironment   = "LIBERATED_STREAM_DECK_TOUCH_MIN_INTERVAL_MS"
)

// touchStripDeck is the complete touch-strip output surface used by the HTTP
// bridge. Both methods use ordinary output reports; neither is a feature
// report operation.
type touchStripDeck interface {
	SetTouchStripImage(image.Image) error
	SetPartialWindowImage(int, int, image.Image) error
}

type stripRegionFrame struct {
	x        int
	y        int
	revision string
	img      image.Image
}

func (r stripRegionFrame) width() int  { return r.img.Bounds().Dx() }
func (r stripRegionFrame) height() int { return r.img.Bounds().Dy() }

func (r stripRegionFrame) stamp() stripRegionStamp {
	return stripRegionStamp{x: r.x, y: r.y, width: r.width(), height: r.height(), revision: r.revision}
}

type stripRegionStamp struct {
	x        int
	y        int
	width    int
	height   int
	revision string
}

func (s stripRegionStamp) sameGeometry(other stripRegionStamp) bool {
	return s.x == other.x && s.y == other.y && s.width == other.width && s.height == other.height
}

func (s stripRegionStamp) overlaps(other stripRegionStamp) bool {
	return s.x < other.x+other.width && other.x < s.x+s.width &&
		s.y < other.y+other.height && other.y < s.y+s.height
}

type stripPresentation struct {
	baseRevision string
	base         image.Image
	regions      []stripRegionFrame
}

// stripOutputState owns all touch-strip writes. desired is replaced whenever
// a newer poll or event acknowledgement arrives, which coalesces queued work.
// displayed state is physical-connection-local; the decoded cache survives a
// reconnect.
type stripOutputState struct {
	mu sync.Mutex

	desired          *stripPresentation
	displayedBase    string
	displayedRegions []stripRegionStamp
	lastUpload       time.Time
	retryNotBefore   time.Time
	minimumInterval  time.Duration
	failureDelay     time.Duration

	frames     map[string]image.Image
	frameOrder []string
}

func (o *stripOutputState) configure(minimumInterval, failureDelay time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.minimumInterval = max(minimumInterval, 0)
	o.failureDelay = max(failureDelay, 0)
}

// startConnection invalidates only physical display tracking. Decoded pixels
// remain available, while the next desired presentation must restore its base
// exactly once before any patches are applied.
func (o *stripOutputState) startConnection() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.desired = nil
	o.displayedBase = ""
	o.displayedRegions = nil
	o.lastUpload = time.Time{}
}

func (o *stripOutputState) cachedFrame(key string) (image.Image, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	frame, ok := o.frames[key]
	return frame, ok
}

func (o *stripOutputState) cacheFrame(key string, frame image.Image) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.frames == nil {
		o.frames = make(map[string]image.Image)
	}
	if _, exists := o.frames[key]; exists {
		o.frames[key] = frame
		return
	}
	if len(o.frames) >= frameCacheLimit {
		oldest := o.frameOrder[0]
		o.frameOrder = o.frameOrder[1:]
		delete(o.frames, oldest)
	}
	o.frames[key] = frame
	o.frameOrder = append(o.frameOrder, key)
}

func (o *stripOutputState) enqueue(deck touchStripDeck, desired *stripPresentation) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.desired = desired
	return o.flushLocked(deck, time.Now())
}

func (o *stripOutputState) flush(deck touchStripDeck) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flushLocked(deck, time.Now())
}

func (o *stripOutputState) flushLocked(deck touchStripDeck, now time.Time) error {
	for o.desired != nil {
		if now.Before(o.retryNotBefore) || (!o.lastUpload.IsZero() && now.Before(o.lastUpload.Add(o.minimumInterval))) {
			return nil
		}
		complete, err := o.writeNextLocked(deck)
		if err != nil {
			if o.failureDelay > 0 {
				o.retryNotBefore = time.Now().Add(o.failureDelay)
			}
			return err
		}
		o.lastUpload = time.Now()
		o.retryNotBefore = time.Time{}
		if complete {
			o.desired = nil
			return nil
		}
		if o.minimumInterval > 0 {
			return nil
		}
		now = o.lastUpload
	}
	return nil
}

// writeNextLocked performs at most one complete HID image operation. Display
// tracking changes only after that operation returns success.
func (o *stripOutputState) writeNextLocked(deck touchStripDeck) (bool, error) {
	desired := o.desired
	if desired == nil {
		return true, nil
	}

	if o.displayedBase != desired.baseRevision || regionsNeedBaseRestore(o.displayedRegions, desired.regions) {
		if err := deck.SetTouchStripImage(desired.base); err != nil {
			return false, fmt.Errorf("render touch strip base: %w", err)
		}
		o.displayedBase = desired.baseRevision
		o.displayedRegions = nil
		return len(desired.regions) == 0, nil
	}

	for index, region := range desired.regions {
		stamp := region.stamp()
		if index < len(o.displayedRegions) && o.displayedRegions[index].revision == stamp.revision {
			continue
		}
		if err := deck.SetPartialWindowImage(region.x, region.y, region.img); err != nil {
			return false, fmt.Errorf("render touch strip region (%d,%d)+%dx%d: %w", region.x, region.y, region.width(), region.height(), err)
		}
		if index == len(o.displayedRegions) {
			o.displayedRegions = append(o.displayedRegions, stamp)
		} else {
			o.displayedRegions[index] = stamp
		}
		// If a changed earlier patch overlaps a later patch, the later patch
		// must be replayed to preserve authoritative list order. Non-overlapping
		// unchanged patches remain suppressed.
		for later := index + 1; later < len(o.displayedRegions) && later < len(desired.regions); later++ {
			if stamp.overlaps(desired.regions[later].stamp()) {
				o.displayedRegions[later].revision = ""
			}
		}
		return len(o.displayedRegions) == len(desired.regions) && regionsMatch(o.displayedRegions, desired.regions), nil
	}
	return true, nil
}

func regionsNeedBaseRestore(displayed []stripRegionStamp, desired []stripRegionFrame) bool {
	if len(displayed) > len(desired) {
		return true
	}
	for index, stamp := range displayed {
		if !stamp.sameGeometry(desired[index].stamp()) {
			return true
		}
	}
	return false
}

func regionsMatch(displayed []stripRegionStamp, desired []stripRegionFrame) bool {
	if len(displayed) != len(desired) {
		return false
	}
	for index, stamp := range displayed {
		want := desired[index].stamp()
		if !stamp.sameGeometry(want) || stamp.revision != want.revision {
			return false
		}
	}
	return true
}

func touchStripMinimumInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(touchStripIntervalEnvironment))
	if raw == "" {
		return defaultTouchStripUploadInterval
	}
	milliseconds, err := strconv.Atoi(raw)
	if err != nil || milliseconds < 0 {
		log.Printf("invalid %s=%q; using %s", touchStripIntervalEnvironment, raw, defaultTouchStripUploadInterval)
		return defaultTouchStripUploadInterval
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func renderStrip(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if !model.HasTouchStrip() {
		return nil
	}
	stripDeck, ok := deck.(touchStripDeck)
	if !ok {
		return fmt.Errorf("%s device does not provide touch-strip output", model)
	}
	desired := state.stripPresentation()
	return state.stripOutput.enqueue(stripDeck, desired)
}

func flushStrip(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if !model.HasTouchStrip() {
		return nil
	}
	stripDeck, ok := deck.(touchStripDeck)
	if !ok {
		return fmt.Errorf("%s device does not provide touch-strip output", model)
	}
	return state.stripOutput.flush(stripDeck)
}

func (s *demoState) stripPresentation() *stripPresentation {
	view := s.stripView()
	fallback := func() *stripPresentation {
		return &stripPresentation{
			baseRevision: "semantic:" + stripViewRevision(view),
			base:         render.Strip(view),
		}
	}
	if !s.bridge || s.activeStrip == nil || s.activeStrip.Image == nil || s.activeStrip.Image.Data == "" {
		return fallback()
	}

	base, revision, ok := s.stripImageFrame("base", s.activeStrip.Image, true)
	if !ok {
		return fallback()
	}
	desired := &stripPresentation{baseRevision: "raster:" + revision, base: base}
	for index := range s.activeStrip.Regions {
		region := &s.activeStrip.Regions[index]
		if region.X < 0 || region.Y < 0 || region.Data == "" {
			continue
		}
		frame, regionRevision, valid := s.stripImageFrame("region", &region.remoteImage, false)
		if !valid {
			continue
		}
		width, height := frame.Bounds().Dx(), frame.Bounds().Dy()
		if width <= 0 || height <= 0 || region.X+width > streamdeck.TouchStripWidth || region.Y+height > streamdeck.TouchStripHeight {
			continue
		}
		desired.regions = append(desired.regions, stripRegionFrame{
			x: region.X, y: region.Y, revision: regionRevision, img: frame,
		})
	}
	return desired
}

func (s *demoState) stripView() render.StripView {
	if s.bridge {
		view := render.StripView{
			EventsSeen: s.remoteEventsSeen,
			LastEvent:  s.remoteLast,
			Lines:      []string{},
		}
		if s.activeStrip != nil {
			view.Title = s.activeStrip.Title
			view.Lines = s.activeStrip.Lines
			view.Page = s.activeStrip.Page
			view.Pages = s.activeStrip.Pages
			if view.Lines == nil {
				view.Lines = []string{}
			}
		}
		return view
	}
	return render.StripView{
		Counter: s.counter, Brightness: s.brightness,
		SelectedKey: s.selectedKey, SelectedOn: s.keys[s.selectedKey],
		Mode: displayModes[s.mode], LastInput: s.latestInput, Touch: s.latestTouch,
		Theme: s.remoteTheme, Title: s.remoteTitle, Message: s.remoteMessage,
		EventsSeen: s.remoteEventsSeen, LastEvent: s.remoteLast,
	}
}

func (s *demoState) stripImageFrame(kind string, wire *remoteImage, scaleFull bool) (image.Image, string, bool) {
	revision := wire.Revision
	cacheKey := ""
	if revision != "" {
		cacheKey = kind + ":" + revision
		if frame, cached := s.stripOutput.cachedFrame(cacheKey); cached {
			return frame, revision, frame != nil
		}
	}

	frame, err := decodeImageFrame(wire)
	if err == nil && scaleFull {
		frame = streamdeck.ScaleImage(frame, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight)
	}
	if err != nil {
		log.Printf("bridge strip %s image ignored: %v", kind, err)
		frame = nil
	}
	if cacheKey != "" {
		s.stripOutput.cacheFrame(cacheKey, frame)
	}
	if revision == "" {
		revision = imagePayloadRevision(wire.Data)
	}
	return frame, revision, frame != nil
}

func stripViewRevision(view render.StripView) string {
	payload, err := json.Marshal(view)
	if err != nil {
		payload = []byte(fmt.Sprintf("%#v", view))
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func imagePayloadRevision(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
