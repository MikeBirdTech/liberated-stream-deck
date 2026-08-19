package visual

import (
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

// frame builds a distinct, tiny opaque image so identity comparisons are
// meaningful and failures print something recognizable.
func frame(shade uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = shade
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	return img
}

type write struct {
	key int
	img image.Image
}

type recorder struct {
	writes []write
	fail   error
}

func (r *recorder) SetKeyImage(index int, img image.Image) error {
	if r.fail != nil {
		return r.fail
	}
	r.writes = append(r.writes, write{key: index, img: img})
	return nil
}

// harness drives the engine deterministically: time only moves when a test
// says so, and pump performs every write that is due at the current instant.
type harness struct {
	t   *testing.T
	e   *engine
	rec *recorder
	now time.Time
}

func newHarness(t *testing.T, interval time.Duration) *harness {
	t.Helper()
	rec := &recorder{}
	h := &harness{
		t:   t,
		rec: rec,
		now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		e: &engine{
			keys:      make(map[int]*keyState),
			maxKeys:   8,
			interval:  interval,
			failDelay: time.Second,
			pressMin:  150 * time.Millisecond,
			fallback:  Depress,
			limits:    limits{minFrame: 10 * time.Millisecond, maxLoops: 100, maxLoop: 10 * time.Second},
		},
	}
	h.e.attach(rec)
	return h
}

// pump performs all writes due now and returns the next self-change instant.
func (h *harness) pump() time.Time {
	h.t.Helper()
	for i := 0; i < 1000; i++ {
		op, next := h.e.step(h.now)
		if op == nil {
			return next
		}
		err := h.e.writer.SetKeyImage(op.key, op.img)
		h.e.commit(op, h.now, err)
	}
	h.t.Fatal("pump did not settle")
	return time.Time{}
}

func (h *harness) advance(d time.Duration) time.Time {
	h.now = h.now.Add(d)
	return h.pump()
}

func (h *harness) writes() []write {
	w := h.rec.writes
	h.rec.writes = nil
	return w
}

func (h *harness) expectWrites(want ...write) {
	h.t.Helper()
	got := h.writes()
	if len(got) != len(want) {
		h.t.Fatalf("writes = %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].key != want[i].key || got[i].img != want[i].img {
			h.t.Fatalf("write %d = key %d img %p, want key %d img %p", i, got[i].key, got[i].img, want[i].key, want[i].img)
		}
	}
}

func staticProgram(rev string, rest image.Image) Program {
	return Program{Revision: rev, Rest: rest}
}

func animated(rev string, rest image.Image, loops int, hold bool, durations []time.Duration, frames ...image.Image) Program {
	seq := &Sequence{Loops: loops, HoldLast: hold}
	for i, img := range frames {
		seq.Frames = append(seq.Frames, Frame{Image: img, Duration: durations[i]})
	}
	return Program{Revision: rev, Rest: rest, Animation: seq}
}

func TestStaticProgramPaintsOnceAndUnchangedRevisionIsSilent(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(10)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.expectWrites(write{0, rest})

	// Same revision with a different (even different-pointer) frame: silent.
	h.e.setProgram(0, staticProgram("a", frame(11)), h.now)
	h.advance(time.Second)
	h.expectWrites()

	other := frame(12)
	h.e.setProgram(0, staticProgram("b", other), h.now)
	h.pump()
	h.expectWrites(write{0, other})
}

func TestNilRestAndOutOfRangeKeysAreIgnored(t *testing.T) {
	h := newHarness(t, 0)
	h.e.setProgram(0, Program{Revision: "a"}, h.now)
	h.e.setProgram(99, staticProgram("a", frame(1)), h.now)
	h.e.setProgram(-1, staticProgram("a", frame(1)), h.now)
	h.e.press(99, h.now)
	h.pump()
	h.expectWrites()
	if len(h.e.keys) != 0 {
		t.Fatalf("key state created for ignored programs: %d", len(h.e.keys))
	}
}

func TestAnimationFrameOrderDurationsAndDeadlines(t *testing.T) {
	h := newHarness(t, 0)
	rest, f0, f1, f2 := frame(0), frame(1), frame(2), frame(3)
	h.e.setProgram(0, animated("anim", rest, 1, false,
		[]time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}, f0, f1, f2), h.now)
	start := h.now
	next := h.pump()
	h.expectWrites(write{0, f0})
	if !next.Equal(start.Add(100 * time.Millisecond)) {
		t.Fatalf("next = %v, want +100ms", next.Sub(start))
	}

	// Nothing changes before the boundary.
	next = h.advance(99 * time.Millisecond)
	h.expectWrites()
	if !next.Equal(start.Add(100 * time.Millisecond)) {
		t.Fatalf("next = %v, want +100ms", next.Sub(start))
	}

	next = h.advance(time.Millisecond)
	h.expectWrites(write{0, f1})
	if !next.Equal(start.Add(300 * time.Millisecond)) {
		t.Fatalf("next = %v, want +300ms", next.Sub(start))
	}

	next = h.advance(200 * time.Millisecond)
	h.expectWrites(write{0, f2})
	if !next.Equal(start.Add(600 * time.Millisecond)) {
		t.Fatalf("next = %v, want +600ms", next.Sub(start))
	}

	// Finite play ends on the resting frame with no further deadline.
	next = h.advance(300 * time.Millisecond)
	h.expectWrites(write{0, rest})
	if !next.IsZero() {
		t.Fatalf("next after completion = %v, want none", next)
	}
	h.advance(time.Hour)
	h.expectWrites()
}

func TestHoldLastKeepsFinalFrame(t *testing.T) {
	h := newHarness(t, 0)
	rest, f0, f1 := frame(0), frame(1), frame(2)
	h.e.setProgram(0, animated("anim", rest, 1, true, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond}, f0, f1), h.now)
	h.pump()
	h.advance(50 * time.Millisecond)
	h.advance(50 * time.Millisecond)
	h.advance(time.Hour)
	h.expectWrites(write{0, f0}, write{0, f1})
	if h.e.keys[0].displayed != f1 {
		t.Fatal("final frame not held")
	}
}

func TestBoundedLoopPlaysCountThenRests(t *testing.T) {
	h := newHarness(t, 0)
	rest, f0, f1 := frame(0), frame(1), frame(2)
	h.e.setProgram(0, animated("loop2", rest, 2, false, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}, f0, f1), h.now)
	h.pump()
	for i := 0; i < 6; i++ {
		h.advance(10 * time.Millisecond)
	}
	h.expectWrites(write{0, f0}, write{0, f1}, write{0, f0}, write{0, f1}, write{0, rest})
}

func TestUnboundedLoopRepeatsUntilReplacedOrDurationCap(t *testing.T) {
	h := newHarness(t, 0)
	h.e.limits.maxLoop = 100 * time.Millisecond
	rest, f0, f1 := frame(0), frame(1), frame(2)
	h.e.setProgram(0, animated("run", rest, 0, false, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}, f0, f1), h.now)
	h.pump()
	for i := 0; i < 5; i++ {
		h.advance(10 * time.Millisecond)
	}
	h.expectWrites(write{0, f0}, write{0, f1}, write{0, f0}, write{0, f1}, write{0, f0}, write{0, f1})

	// Replacement cancels the loop immediately.
	other := frame(9)
	h.e.setProgram(0, staticProgram("done", other), h.now)
	h.pump()
	h.advance(time.Second)
	h.expectWrites(write{0, other})

	// A fresh unbounded loop settles to its end frame at the duration cap.
	h.e.setProgram(0, animated("run2", rest, 0, false, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}, f0, f1), h.now)
	h.pump()
	h.writes()
	for i := 0; i < 12; i++ {
		h.advance(10 * time.Millisecond)
	}
	got := h.writes()
	if len(got) == 0 || got[len(got)-1].img != rest {
		t.Fatalf("capped loop did not settle on rest: %v", got)
	}
	h.advance(time.Hour)
	h.expectWrites()
}

func TestExplicitLoopCountIsClamped(t *testing.T) {
	h := newHarness(t, 0)
	h.e.limits.maxLoops = 1
	rest, f0 := frame(0), frame(1)
	h.e.setProgram(0, animated("x", rest, 50, false, []time.Duration{10 * time.Millisecond}, f0), h.now)
	h.pump()
	h.advance(10 * time.Millisecond)
	h.expectWrites(write{0, f0}, write{0, rest})
}

func TestFrameDurationsAreClampedToMinimum(t *testing.T) {
	h := newHarness(t, 0)
	rest, f0, f1 := frame(0), frame(1), frame(2)
	h.e.setProgram(0, animated("fast", rest, 1, false, []time.Duration{0, -5}, f0, f1), h.now)
	start := h.now
	next := h.pump()
	if !next.Equal(start.Add(10 * time.Millisecond)) {
		t.Fatalf("next = %v, want clamped +10ms", next.Sub(start))
	}
}

func TestMinVisibleDefersReplacementThenAppliesNewest(t *testing.T) {
	h := newHarness(t, 0)
	a, b, c := frame(1), frame(2), frame(3)
	pa := staticProgram("a", a)
	pa.MinVisible = 300 * time.Millisecond
	h.e.setProgram(0, pa, h.now)
	h.pump()
	h.expectWrites(write{0, a})
	start := h.now

	h.advance(100 * time.Millisecond)
	h.e.setProgram(0, staticProgram("b", b), h.now)
	next := h.pump()
	h.expectWrites()
	if !next.Equal(start.Add(300 * time.Millisecond)) {
		t.Fatalf("hold deadline = %v, want +300ms", next.Sub(start))
	}
	// A newer program arriving during the hold coalesces over the older one.
	h.advance(50 * time.Millisecond)
	h.e.setProgram(0, staticProgram("c", c), h.now)
	h.pump()
	h.expectWrites()

	h.advance(149 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{0, c})
	if h.e.keys[0].pending != nil {
		t.Fatal("pending not cleared")
	}
}

func TestMinVisibleProgramMustPaintBeforeReplacement(t *testing.T) {
	h := newHarness(t, 0)
	h.e.detach()
	a, b := frame(1), frame(2)
	pa := staticProgram("a", a)
	pa.MinVisible = 100 * time.Millisecond
	h.e.setProgram(0, pa, h.now)
	h.e.setProgram(0, staticProgram("b", b), h.now)
	h.advance(time.Hour)
	h.expectWrites()

	// On attach, a waiting program is adopted directly (nothing was seen).
	h.e.attach(h.rec)
	h.pump()
	h.expectWrites(write{0, b})
}

func TestZeroMinVisibleProgramsCoalesceWhileDetached(t *testing.T) {
	h := newHarness(t, 0)
	h.e.detach()
	for i, img := range []image.Image{frame(1), frame(2), frame(3)} {
		h.e.setProgram(0, staticProgram(string(rune('a'+i)), img), h.now)
	}
	last := frame(4)
	h.e.setProgram(0, staticProgram("d", last), h.now)
	h.pump()
	h.expectWrites()
	h.e.attach(h.rec)
	h.pump()
	h.expectWrites(write{0, last})
}

func TestNewProgramCancelsOldAnimationAndStaleFramesNeverWrite(t *testing.T) {
	h := newHarness(t, 0)
	rest, a0, a1, a2 := frame(0), frame(1), frame(2), frame(3)
	h.e.setProgram(0, animated("old", rest, 0, false, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}, a0, a1, a2), h.now)
	h.pump()
	h.advance(50 * time.Millisecond)
	h.expectWrites(write{0, a0}, write{0, a1})

	// The old animation's next boundary is 50ms away; the new program lands
	// between boundaries and from then on only its frames may be written.
	b0, b1 := frame(10), frame(11)
	h.advance(20 * time.Millisecond)
	h.e.setProgram(0, animated("new", rest, 1, false, []time.Duration{30 * time.Millisecond, 30 * time.Millisecond}, b0, b1), h.now)
	h.pump()
	for i := 0; i < 20; i++ {
		h.advance(10 * time.Millisecond)
	}
	for _, w := range h.writes() {
		if w.img == a0 || w.img == a1 || w.img == a2 {
			t.Fatalf("stale frame of cancelled animation written: %v", w)
		}
	}
	if h.e.keys[0].displayed != rest {
		t.Fatal("new finite animation did not end on rest")
	}
}

func TestGenericPressFallbackDepressesCurrentFrameAndReleasesAfterMinVisible(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(100)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.writes()

	h.e.press(0, h.now)
	h.pump()
	got := h.writes()
	if len(got) != 1 || got[0].img == rest {
		t.Fatalf("press writes = %v, want one depressed frame", got)
	}
	pressed := got[0].img
	if b := pressed.Bounds(); b != rest.Bounds() {
		t.Fatalf("depressed bounds = %v, want %v", b, rest.Bounds())
	}
	// The depressed frame is darker than the source.
	r0, _, _, _ := rest.At(2, 2).RGBA()
	r1, _, _, _ := pressed.At(2, 2).RGBA()
	if r1 >= r0 {
		t.Fatalf("depressed frame not darker: %d >= %d", r1, r0)
	}

	// Release before the minimum visible time: nothing changes yet.
	h.advance(50 * time.Millisecond)
	h.e.release(0, h.now)
	next := h.pump()
	h.expectWrites()
	if want := h.now.Add(100 * time.Millisecond); !next.Equal(want) {
		t.Fatalf("release deadline = %v, want %v", next, want)
	}
	h.advance(99 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{0, rest})
	if h.e.keys[0].press != nil {
		t.Fatal("press overlay not cleared")
	}
}

func TestReleaseAfterMinVisibleEndsPressImmediately(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(100)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.e.press(0, h.now)
	h.pump()
	h.writes()
	h.advance(time.Second)
	h.expectWrites()
	h.e.release(0, h.now)
	h.pump()
	h.expectWrites(write{0, rest})
}

func TestControllerPressSequenceOverridesGenericFallback(t *testing.T) {
	h := newHarness(t, 0)
	rest, p0, p1 := frame(100), frame(1), frame(2)
	prog := staticProgram("a", rest)
	prog.Press = &Sequence{Frames: []Frame{{Image: p0, Duration: 40 * time.Millisecond}, {Image: p1, Duration: 40 * time.Millisecond}}, Loops: 1}
	prog.PressMinVisible = 200 * time.Millisecond
	h.e.setProgram(0, prog, h.now)
	h.pump()
	h.writes()

	h.e.press(0, h.now)
	h.pump()
	h.expectWrites(write{0, p0})
	h.advance(40 * time.Millisecond)
	h.expectWrites(write{0, p1})
	// Sequence finished while held: last frame holds.
	h.advance(time.Second)
	h.expectWrites()
	h.e.release(0, h.now)
	h.pump()
	h.expectWrites(write{0, rest})
}

func TestTapShorterThanPaintStillShowsPressFeedback(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(100)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.writes()

	// Down and up before the scheduler had any chance to write.
	h.e.press(0, h.now)
	h.e.release(0, h.now)
	h.pump()
	got := h.writes()
	if len(got) != 1 || got[0].img == rest {
		t.Fatalf("writes = %v, want the press frame", got)
	}
	h.advance(149 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{0, rest})
}

func TestPressIsImmediateEvenDuringBaseMinVisibleHold(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(100)
	prog := staticProgram("a", rest)
	prog.MinVisible = time.Hour
	h.e.setProgram(0, prog, h.now)
	h.pump()
	h.writes()
	h.e.press(0, h.now)
	h.pump()
	if got := h.writes(); len(got) != 1 {
		t.Fatalf("press during hold writes = %v, want 1", got)
	}
}

func TestSameRevisionAckDuringPressKeepsPressUntilRelease(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(100)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.e.press(0, h.now)
	h.pump()
	pressed := h.writes()[1].img

	// Controller acknowledges with the same resting frame (action started,
	// run record not yet visible): the local feedback must stay.
	h.advance(30 * time.Millisecond)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.advance(time.Second)
	h.expectWrites()
	if h.e.keys[0].displayed != pressed {
		t.Fatal("same-revision ack removed press feedback")
	}
	h.e.release(0, h.now)
	h.pump()
	h.expectWrites(write{0, rest})
}

func TestNewRevisionSupersedesPressOnceMinVisibleElapsed(t *testing.T) {
	h := newHarness(t, 0)
	rest, starting := frame(100), frame(50)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.pump()
	h.e.press(0, h.now)
	h.pump()
	h.writes()

	h.advance(20 * time.Millisecond)
	h.e.setProgram(0, staticProgram("starting", starting), h.now)
	h.pump()
	h.expectWrites()
	h.advance(129 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{0, starting})
	if h.e.keys[0].press != nil {
		t.Fatal("press overlay survived controller feedback")
	}
	// A late key-up is a no-op.
	h.e.release(0, h.now)
	h.advance(time.Second)
	h.expectWrites()
}

func TestPressOnKeyWithoutProgramIsIgnored(t *testing.T) {
	h := newHarness(t, 0)
	h.e.press(3, h.now)
	h.e.release(3, h.now)
	h.pump()
	h.expectWrites()
}

func TestRepressRestartsFeedback(t *testing.T) {
	h := newHarness(t, 0)
	rest, p0, p1 := frame(100), frame(1), frame(2)
	prog := staticProgram("a", rest)
	prog.Press = &Sequence{Frames: []Frame{{Image: p0, Duration: 50 * time.Millisecond}, {Image: p1, Duration: 50 * time.Millisecond}}, Loops: 1}
	h.e.setProgram(0, prog, h.now)
	h.pump()
	h.e.press(0, h.now)
	h.pump()
	h.advance(50 * time.Millisecond)
	h.writes()
	h.e.release(0, h.now)
	h.e.press(0, h.now)
	h.pump()
	h.expectWrites(write{0, p0})
}

func TestSimultaneousAnimationsKeepPerKeyOrder(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(0)
	a := []image.Image{frame(1), frame(2), frame(3)}
	b := []image.Image{frame(11), frame(12)}
	c := frame(21)
	h.e.setProgram(0, animated("a", rest, 1, false, []time.Duration{30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond}, a...), h.now)
	h.e.setProgram(1, animated("b", rest, 2, false, []time.Duration{45 * time.Millisecond, 45 * time.Millisecond}, b...), h.now)
	h.e.setProgram(2, staticProgram("c", c), h.now)
	h.pump()
	for i := 0; i < 20; i++ {
		h.advance(15 * time.Millisecond)
	}
	perKey := map[int][]image.Image{}
	for _, w := range h.writes() {
		perKey[w.key] = append(perKey[w.key], w.img)
	}
	wantA := []image.Image{a[0], a[1], a[2], rest}
	wantB := []image.Image{b[0], b[1], b[0], b[1], rest}
	if !sameImages(perKey[0], wantA) || !sameImages(perKey[1], wantB) || !sameImages(perKey[2], []image.Image{c}) {
		t.Fatalf("per-key frames = %v", perKey)
	}
}

func sameImages(got, want []image.Image) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPacingSpacesWritesAndRotatesKeys(t *testing.T) {
	h := newHarness(t, 20*time.Millisecond)
	f := []image.Image{frame(1), frame(2), frame(3)}
	for i := range f {
		h.e.setProgram(i, staticProgram("p", f[i]), h.now)
	}
	start := h.now
	next := h.pump()
	h.expectWrites(write{0, f[0]})
	if !next.Equal(start.Add(20 * time.Millisecond)) {
		t.Fatalf("paced deadline = %v, want +20ms", next.Sub(start))
	}
	h.advance(19 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{1, f[1]})
	h.advance(20 * time.Millisecond)
	h.expectWrites(write{2, f[2]})
	h.advance(time.Second)
	h.expectWrites()
}

func TestPressFeedbackJumpsTheWriteQueue(t *testing.T) {
	h := newHarness(t, 20*time.Millisecond)
	rest := frame(1)
	h.e.setProgram(7, staticProgram("r", rest), h.now)
	h.pump()
	h.writes()
	// Seven keys become dirty at once, then key 7 is pressed.
	for i := 0; i < 7; i++ {
		h.e.setProgram(i, staticProgram("p", frame(uint8(10+i))), h.now)
	}
	h.advance(20 * time.Millisecond) // key 0 written (pacing)
	h.e.press(7, h.now)
	h.advance(20 * time.Millisecond)
	got := h.writes()
	if len(got) != 2 || got[0].key != 0 || got[1].key != 7 {
		t.Fatalf("writes = %v, want key 0 then the pressed key 7 ahead of keys 1-6", got)
	}
}

func TestSupersededFramesCoalesceUnderPacing(t *testing.T) {
	h := newHarness(t, 100*time.Millisecond)
	rest, f0, f1, f2 := frame(0), frame(1), frame(2), frame(3)
	h.e.setProgram(0, staticProgram("x", rest), h.now)
	h.pump()
	h.writes()
	// Ten-millisecond frames under 100ms pacing: intermediate frames are
	// skipped, never queued.
	h.e.setProgram(0, animated("a", rest, 1, true, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}, f0, f1, f2), h.now)
	h.advance(100 * time.Millisecond)
	h.expectWrites(write{0, f0})
	h.advance(100 * time.Millisecond)
	h.expectWrites(write{0, f2})
}

func TestWriteFailureHoldsKeyBackThenRetries(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(1)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.rec.fail = errors.New("usb stalled")
	next := h.pump()
	h.expectWrites()
	if want := h.now.Add(time.Second); !next.Equal(want) {
		t.Fatalf("retry deadline = %v, want %v", next, want)
	}
	if h.e.keys[0].displayed != nil {
		t.Fatal("failed write marked displayed")
	}
	h.rec.fail = nil
	h.advance(999 * time.Millisecond)
	h.expectWrites()
	h.advance(time.Millisecond)
	h.expectWrites(write{0, rest})
}

func TestAttachRestoresSteadyStateNotTransientAnimation(t *testing.T) {
	h := newHarness(t, 0)
	rest0, a0, a1 := frame(0), frame(1), frame(2)
	rest1, l0, l1 := frame(10), frame(11), frame(12)
	rest2 := frame(20)
	// Key 0: finite animation in flight. Key 1: unbounded loop. Key 2: static
	// with a live press overlay and a pending replacement.
	h.e.setProgram(0, animated("fin", rest0, 1, false, []time.Duration{time.Second, time.Second}, a0, a1), h.now)
	h.e.setProgram(1, animated("loop", rest1, 0, false, []time.Duration{time.Second, time.Second}, l0, l1), h.now)
	p2 := staticProgram("s", rest2)
	p2.MinVisible = time.Hour
	h.e.setProgram(2, p2, h.now)
	h.pump()
	h.e.press(2, h.now)
	h.pump()
	rest2b := frame(21)
	h.e.setProgram(2, staticProgram("s2", rest2b), h.now)
	h.advance(time.Second)
	h.writes()

	h.e.detach()
	h.advance(time.Minute)
	h.expectWrites()
	h.e.attach(h.rec)
	h.pump()
	got := map[int]image.Image{}
	for _, w := range h.writes() {
		got[w.key] = w.img
	}
	if got[0] != rest0 {
		t.Fatalf("key 0 restored %p, want rest (finite animation settled)", got[0])
	}
	if got[1] != l0 {
		t.Fatalf("key 1 restored %p, want loop frame 0 (unbounded loop restarted)", got[1])
	}
	if got[2] != rest2b {
		t.Fatalf("key 2 restored %p, want pending program adopted and press dropped", got[2])
	}
	if h.e.keys[2].press != nil {
		t.Fatal("press overlay survived reconnect")
	}
}

func TestDetachStopsWritesButKeepsPrograms(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(1)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	h.e.detach()
	h.pump()
	h.expectWrites()
	h.e.attach(h.rec)
	h.pump()
	h.expectWrites(write{0, rest})
}

func TestCommitIgnoresLayerChangedDuringWrite(t *testing.T) {
	h := newHarness(t, 0)
	rest := frame(1)
	h.e.setProgram(0, staticProgram("a", rest), h.now)
	op, _ := h.e.step(h.now)
	if op == nil || op.img != rest {
		t.Fatalf("step op = %v", op)
	}
	// While the write is "in flight" a press arrives.
	h.e.press(0, h.now)
	h.e.commit(op, h.now, nil)
	if h.e.keys[0].displayed != rest {
		t.Fatal("displayed not recorded")
	}
	if !h.e.keys[0].press.start.IsZero() {
		t.Fatal("press stamped as painted by a base-layer write")
	}
	if h.e.keys[0].base.start.IsZero() {
		t.Fatal("base paint not stamped")
	}
}

func TestDepressProducesOpaqueDimmedInsetFrame(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	out := Depress(src)
	if out.Bounds() != src.Bounds() {
		t.Fatalf("bounds = %v", out.Bounds())
	}
	center := color.RGBAModel.Convert(out.At(60, 60)).(color.RGBA)
	edge := color.RGBAModel.Convert(out.At(1, 1)).(color.RGBA)
	if center.R >= 200 || center.R < 140 {
		t.Fatalf("center = %v, want dimmed source", center)
	}
	if edge.R >= center.R {
		t.Fatalf("edge %v not darker than center %v", edge, center)
	}
	if center.A != 255 || edge.A != 255 {
		t.Fatal("output not opaque")
	}
	if Depress(nil) != nil {
		t.Fatal("nil input must yield nil")
	}
}
