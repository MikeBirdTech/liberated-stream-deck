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
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/visual"
)

// Bridge-mode key rendering.
//
// Every LCD key write in bridge mode goes through one visual.Scheduler. The
// bridge translates the controller's wire keys into opaque visual programs
// (resting frame, optional animation, optional cached press feedback) and
// hands them to the scheduler, which owns pacing, cancellation, press
// feedback, and reconnect restoration. The bridge never interprets a key's
// meaning: it resolves which wire object describes each physical key,
// decodes its frames, and passes the result on verbatim.

const (
	keyIntervalEnvironment = "LIBERATED_STREAM_DECK_KEY_MIN_INTERVAL_MS"
	// maxSequenceFrames bounds one animation or press sequence.
	maxSequenceFrames = 64
	// keyFrameCacheLimit bounds the decoded key-frame cache (native-size
	// frames keyed by revision); the oldest entry is evicted on overflow.
	keyFrameCacheLimit = 512
	// maxWireKeyIndex bounds the per-index wire key maps.
	maxWireKeyIndex = 32
)

// keyFrameCache holds decoded, native-size key frames keyed by wire revision.
// A nil entry records a payload that failed to decode so it is logged once
// instead of on every poll. Eviction is oldest-first.
type keyFrameCache struct {
	frames map[string]image.Image
	order  []string
}

func (c *keyFrameCache) get(key string) (image.Image, bool) {
	frame, ok := c.frames[key]
	return frame, ok
}

func (c *keyFrameCache) put(key string, frame image.Image) {
	if c.frames == nil {
		c.frames = make(map[string]image.Image)
	}
	if _, exists := c.frames[key]; exists {
		c.frames[key] = frame
		return
	}
	for len(c.frames) >= keyFrameCacheLimit && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.frames, oldest)
	}
	c.frames[key] = frame
	c.order = append(c.order, key)
}

// visuals returns the process-wide key scheduler, creating it on first use.
func (s *demoState) visuals() *visual.Scheduler {
	if s.keyVisuals == nil {
		s.keyVisualErrors = make(chan error, 1)
		errs := s.keyVisualErrors
		s.keyVisuals = visual.New(visual.Options{
			MinInterval: keyMinimumInterval(),
			OnWriteError: func(key int, err error) {
				select {
				case errs <- fmt.Errorf("render key %d: %w", key+1, err):
				default:
				}
			},
		})
	}
	return s.keyVisuals
}

// closeVisuals stops the scheduler goroutine (idempotent, nil-safe).
func (s *demoState) closeVisuals() {
	if s.keyVisuals != nil {
		s.keyVisuals.Close()
		s.keyVisuals = nil
		s.keyVisualErrors = nil
	}
}

func keyMinimumInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(keyIntervalEnvironment))
	if raw == "" {
		return visual.DefaultMinInterval
	}
	milliseconds, err := strconv.Atoi(raw)
	if err != nil || milliseconds < 0 {
		log.Printf("invalid %s=%q; using %s", keyIntervalEnvironment, raw, visual.DefaultMinInterval)
		return visual.DefaultMinInterval
	}
	return time.Duration(milliseconds) * time.Millisecond
}

// applyWireKeys stores a keys[] array. replace=true (a GET snapshot) drops
// entries not present in the array; replace=false (an ack) merges by index.
func (s *demoState) applyWireKeys(keys []remoteKey, replace bool) {
	if replace {
		s.wireKeys = nil
	}
	if len(keys) == 0 {
		return
	}
	if s.wireKeys == nil {
		s.wireKeys = make(map[int]*remoteKey)
	}
	for i := range keys {
		key := keys[i]
		if key.Index < 0 || key.Index >= maxWireKeyIndex {
			continue
		}
		s.wireKeys[key.Index] = &key
	}
}

// resolveKey returns the wire object that describes one physical key:
// keys[] entry, then the legacy singular key when its index matches, then
// the background frame, then nil (quiet paper).
func (s *demoState) resolveKey(index int) *remoteKey {
	if key, ok := s.wireKeys[index]; ok {
		return key
	}
	if s.activeKey != nil && s.activeKey.Index == index {
		return s.activeKey
	}
	return backgroundFrame(s.background, index)
}

// keyRevision derives the visual identity of a wire key. Controllers that
// send visual.revision control it exactly; older payloads get a content
// revision so unchanged polls never repaint and changed ones always do.
func keyRevision(key *remoteKey) string {
	if key == nil {
		return "paper"
	}
	semantic := key.Label + "\x00" + key.Sub + "\x00" + key.BG + "\x00" + key.FG
	if key.Visual != nil {
		if key.Visual.Revision != "" {
			return "visual:" + key.Visual.Revision
		}
		payload, err := json.Marshal(key.Visual)
		if err != nil {
			payload = []byte(fmt.Sprintf("%#v", key.Visual))
		}
		return "visual-derived:" + digest(append(payload, semantic...))
	}
	if key.Image != nil && key.Image.Data != "" {
		if key.Image.Revision != "" {
			return "image:" + key.Image.Revision + "|" + digest([]byte(semantic))
		}
		return "image-data:" + digest([]byte(key.Image.Data+"\x00"+semantic))
	}
	return "semantic:" + digest([]byte(semantic))
}

func digest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}

// syncKeys pushes every physical key's program to the scheduler. Programs
// whose revision is already accepted are skipped before any decoding.
func (s *demoState) syncKeys(model streamdeck.Model) {
	for index := 0; index < model.KeyCount(); index++ {
		s.syncKey(model, index)
	}
}

func (s *demoState) syncKey(model streamdeck.Model, index int) {
	key := s.resolveKey(index)
	revision := keyRevision(key)
	sched := s.visuals()
	if sched.Revision(index) == revision {
		return
	}
	sched.SetProgram(index, s.keyProgram(model, index, key, revision))
}

// keyProgram builds the opaque visual program for one physical key.
func (s *demoState) keyProgram(model streamdeck.Model, index int, key *remoteKey, revision string) visual.Program {
	width, height := model.KeyImageSize()
	program := visual.Program{Revision: revision, Rest: s.restFrame(key, index, width, height)}
	if key == nil || key.Visual == nil {
		return program
	}
	v := key.Visual
	if v.Rest != nil {
		if rest := s.nativeFrame(index, "rest", v.Rest, width, height); rest != nil {
			program.Rest = rest
		}
	}
	program.MinVisible = millis(v.MinVisibleMS)
	if v.Animation != nil {
		if seq := s.sequence(index, "animation", revision, v.Animation, width, height); seq != nil {
			program.Animation = seq
		}
	}
	if v.Press != nil {
		if seq := s.sequence(index, "press", revision, v.Press, width, height); seq != nil {
			seq.HoldLast = true
			program.Press = seq
			program.PressMinVisible = millis(v.Press.MinVisibleMS)
		}
	}
	return program
}

// restFrame is the steady-state frame of a wire key: its decoded image scaled
// to the native key size, or the label/bg/fg rendering (quiet paper for nil).
func (s *demoState) restFrame(key *remoteKey, index, width, height int) image.Image {
	if key != nil && key.Image != nil {
		if frame := s.nativeFrame(index, "image", key.Image, width, height); frame != nil {
			return frame
		}
	}
	view := render.KeyView{Index: index, BG: bridgePaperBG}
	if key != nil {
		view.Label = key.Label
		view.BG, view.FG = keyRenderColors(key)
	}
	return render.KeySize(view, width, height)
}

// sequence decodes one wire sequence into native-size frames. Any invalid
// frame (bad base64/image, unknown revision reference, no frames, too many
// frames) invalidates the whole sequence so the key falls back to its static
// rendering; the reason is logged once per program revision.
func (s *demoState) sequence(index int, kind, revision string, wire *remoteSequence, width, height int) *visual.Sequence {
	invalid := func(reason string) *visual.Sequence {
		s.logInvalidSequenceOnce(revision+"/"+kind, fmt.Sprintf("bridge key %d %s ignored: %s", index+1, kind, reason))
		return nil
	}
	if len(wire.Frames) == 0 {
		return invalid("no frames")
	}
	if len(wire.Frames) > maxSequenceFrames {
		return invalid(fmt.Sprintf("%d frames exceeds limit %d", len(wire.Frames), maxSequenceFrames))
	}
	seq := &visual.Sequence{Loops: 1}
	for i := range wire.Frames {
		frame := &wire.Frames[i]
		img := s.nativeFrame(index, kind, &frame.remoteImage, width, height)
		if img == nil {
			return invalid(fmt.Sprintf("frame %d undecodable", i))
		}
		seq.Frames = append(seq.Frames, visual.Frame{Image: img, Duration: millis(frame.DurationMS)})
	}
	if wire.LoopCount != nil {
		if *wire.LoopCount < 0 {
			return invalid("negative loop_count")
		}
		seq.Loops = *wire.LoopCount
	}
	switch strings.ToLower(wire.End) {
	case "", "rest":
	case "hold":
		seq.HoldLast = true
	default:
		return invalid(fmt.Sprintf("unknown end %q", wire.End))
	}
	return seq
}

func (s *demoState) logInvalidSequenceOnce(key, message string) {
	if s.invalidSequences == nil || len(s.invalidSequences) >= 256 {
		s.invalidSequences = make(map[string]struct{})
	}
	if _, logged := s.invalidSequences[key]; logged {
		return
	}
	s.invalidSequences[key] = struct{}{}
	log.Print(message)
}

// nativeFrame decodes a wire image and scales it to the native key size,
// caching the result by revision. A frame carrying only a revision resolves
// against the cache (nil when unknown). Decode failures are cached as nil.
func (s *demoState) nativeFrame(index int, kind string, wire *remoteImage, width, height int) image.Image {
	if wire == nil {
		return nil
	}
	cacheKey := ""
	if wire.Revision != "" {
		cacheKey = fmt.Sprintf("%s@%dx%d", wire.Revision, width, height)
		if frame, cached := s.keyFrames.get(cacheKey); cached {
			return frame
		}
	}
	if wire.Data == "" {
		return nil
	}
	frame, err := decodeImageFrame(wire)
	if err != nil {
		log.Printf("bridge key %d %s image ignored: %v", index+1, kind, err)
		frame = nil
	} else {
		frame = streamdeck.ScaleImage(frame, width, height)
	}
	if cacheKey != "" {
		s.keyFrames.put(cacheKey, frame)
	}
	return frame
}

func millis(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
