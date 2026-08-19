package visual

import (
	"image"
	"time"
)

// engine is the pure, single-threaded state machine behind Scheduler. Every
// method must be called with the owning Scheduler's lock held (or from a test
// that owns the engine outright). It never sleeps, never spawns goroutines,
// and never touches hardware: step reports the single next write to perform
// and the next instant at which the state can change on its own.
type engine struct {
	writer    Writer
	keys      map[int]*keyState
	maxKeys   int
	limits    limits
	pressMin  time.Duration
	interval  time.Duration
	failDelay time.Duration
	fallback  func(image.Image) image.Image

	lastWrite time.Time
	cursor    int
}

// playback is an accepted program plus its play state.
type playback struct {
	program *Program
	// start is the instant the program first appeared on the device (its
	// frame was written, or was already displayed). Zero until then: the
	// program shows its first frame and its clocks have not begun.
	start time.Time
	// finished forces the program's end frame (set on reconnect so a
	// transient finite animation is not replayed from the middle).
	finished bool
}

// pressState is the local press-feedback overlay.
type pressState struct {
	seq        *Sequence
	minVisible time.Duration
	start      time.Time // first paint; zero until painted
	released   bool
}

type keyState struct {
	base           *playback
	pending        *Program
	press          *pressState
	displayed      image.Image
	retryNotBefore time.Time
}

// writeOp is one decided hardware write. base/press record the layer that
// produced the image so commit can stamp the right first-paint time even if
// the key's state changed while the write was in flight.
type writeOp struct {
	key   int
	img   image.Image
	base  *playback
	press *pressState
}

func (e *engine) key(index int) *keyState {
	ks, ok := e.keys[index]
	if !ok {
		ks = &keyState{}
		e.keys[index] = ks
	}
	return ks
}

func (e *engine) keyOK(index int) bool {
	return index >= 0 && index < e.maxKeys
}

// setProgram accepts a new authoritative program for a key.
func (e *engine) setProgram(index int, p Program, now time.Time) {
	if !e.keyOK(index) || p.Rest == nil {
		return
	}
	ks := e.key(index)
	if ks.base != nil && ks.base.program.Revision == p.Revision {
		// The accepted program is re-sent (unchanged poll): nothing newer
		// exists, so a waiting replacement is obsolete.
		ks.pending = nil
		return
	}
	if ks.pending != nil && ks.pending.Revision == p.Revision {
		return
	}
	program := p
	if ks.canReplace(now) {
		ks.adopt(&program)
		return
	}
	ks.pending = &program
}

// canReplace reports whether the visual currently occupying the key has
// satisfied its minimum visible duration. A press overlay or a program with a
// positive MinVisible must paint first and then hold; a program without a
// MinVisible can always be replaced (and coalesced before it ever paints).
func (ks *keyState) canReplace(now time.Time) bool {
	if ks.press != nil {
		return !ks.press.start.IsZero() && !now.Before(ks.press.start.Add(ks.press.minVisible))
	}
	if ks.base == nil || ks.base.program.MinVisible <= 0 {
		return true
	}
	return !ks.base.start.IsZero() && !now.Before(ks.base.start.Add(ks.base.program.MinVisible))
}

// holdUntil returns the instant at which canReplace becomes true, or zero if
// it is already true or depends on a paint that has not happened yet.
func (ks *keyState) holdUntil() time.Time {
	if ks.press != nil {
		if ks.press.start.IsZero() {
			return time.Time{}
		}
		return ks.press.start.Add(ks.press.minVisible)
	}
	if ks.base == nil || ks.base.program.MinVisible <= 0 || ks.base.start.IsZero() {
		return time.Time{}
	}
	return ks.base.start.Add(ks.base.program.MinVisible)
}

func (ks *keyState) adopt(p *Program) {
	ks.base = &playback{program: p}
	ks.pending = nil
	// Controller-provided feedback supersedes the local press overlay.
	ks.press = nil
}

// press starts press feedback on a key: the program's cached press sequence
// when it has one, otherwise a generic depression of the frame currently on
// the key. Press feedback is immediate and does not wait for the current
// program's MinVisible.
func (e *engine) press(index int, now time.Time) {
	if !e.keyOK(index) {
		return
	}
	ks := e.key(index)
	var program *Program
	if ks.base != nil {
		program = ks.base.program
	}
	var seq *Sequence
	if program != nil && program.Press.valid() {
		seq = program.Press
	} else {
		current := ks.displayed
		if current == nil {
			current, _ = ks.baseWanted(now, e.limits)
		}
		if current == nil || e.fallback == nil {
			return
		}
		img := e.fallback(current)
		if img == nil {
			return
		}
		seq = &Sequence{Frames: []Frame{{Image: img, Duration: time.Second}}, Loops: 1, HoldLast: true}
	}
	ks.press = &pressState{seq: seq, minVisible: program.pressMinVisible(e.pressMin)}
}

func (e *engine) release(index int, now time.Time) {
	if !e.keyOK(index) {
		return
	}
	ks, ok := e.keys[index]
	if !ok || ks.press == nil {
		return
	}
	ks.press.released = true
}

// baseWanted returns the frame the base program wants right now and the next
// instant it changes on its own (zero if static or not yet started).
func (ks *keyState) baseWanted(now time.Time, lim limits) (image.Image, time.Time) {
	base := ks.base
	if base == nil {
		return nil, time.Time{}
	}
	p := base.program
	if !p.Animation.valid() {
		return p.Rest, time.Time{}
	}
	if base.finished {
		return p.endFrame(), time.Time{}
	}
	var elapsed time.Duration
	if !base.start.IsZero() {
		elapsed = now.Sub(base.start)
	}
	img, until, finished := p.Animation.frameAt(elapsed, lim)
	if finished {
		return p.endFrame(), time.Time{}
	}
	if base.start.IsZero() {
		return img, time.Time{}
	}
	return img, now.Add(until)
}

func (p *Program) endFrame() image.Image {
	if p.Animation.valid() && p.Animation.HoldLast {
		return p.Animation.Frames[len(p.Animation.Frames)-1].Image
	}
	return p.Rest
}

// pressWanted returns the press overlay frame and its next change instant.
func (ks *keyState) pressWanted(now time.Time, lim limits) (image.Image, time.Time) {
	ps := ks.press
	var elapsed time.Duration
	if !ps.start.IsZero() {
		elapsed = now.Sub(ps.start)
	}
	img, until, finished := ps.seq.frameAt(elapsed, lim)
	if finished || ps.start.IsZero() {
		return img, time.Time{}
	}
	return img, now.Add(until)
}

// wanted returns the frame the key should show right now, the layer that
// produced it, and the next instant the frame changes on its own.
func (ks *keyState) wanted(now time.Time, lim limits) (image.Image, *playback, *pressState, time.Time) {
	if ks.press != nil {
		img, next := ks.pressWanted(now, lim)
		return img, nil, ks.press, next
	}
	img, next := ks.baseWanted(now, lim)
	return img, ks.base, nil, next
}

// settle advances a key's layer bookkeeping to now: ends a released press
// once its minimum visible time has passed, adopts a pending program once the
// current visual may be replaced, and stamps first-paint times for frames
// that are already on the device. It returns the next instant the key's
// state changes on its own (zero if none).
func (e *engine) settle(ks *keyState, now time.Time) time.Time {
	if ps := ks.press; ps != nil && ps.released && !ps.start.IsZero() && !now.Before(ps.start.Add(ps.minVisible)) {
		ks.press = nil
	}
	if ks.pending != nil && ks.canReplace(now) {
		ks.adopt(ks.pending)
	}

	img, base, press, next := ks.wanted(now, e.limits)
	if img != nil && img == ks.displayed {
		markPainted(base, press, now)
		// Stamping a first paint can start clocks; recompute deadlines.
		_, _, _, next = ks.wanted(now, e.limits)
	}
	if ps := ks.press; ps != nil && ps.released && !ps.start.IsZero() {
		next = earliest(next, ps.start.Add(ps.minVisible))
	}
	if ks.pending != nil {
		next = earliest(next, ks.holdUntil())
	}
	return next
}

func markPainted(base *playback, press *pressState, now time.Time) {
	if press != nil {
		if press.start.IsZero() {
			press.start = now
		}
		return
	}
	if base != nil && base.start.IsZero() {
		base.start = now
	}
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

// step settles every key and picks at most one write. It returns the write
// (nil when nothing is due) and the next instant the caller should call step
// again without external input (zero when there is none).
func (e *engine) step(now time.Time) (*writeOp, time.Time) {
	var next time.Time
	indexes := e.sortedKeys()
	for _, index := range indexes {
		next = earliest(next, e.settle(e.keys[index], now))
	}
	if e.writer == nil || len(indexes) == 0 {
		return nil, next
	}

	// Press feedback jumps the queue: input must be acknowledged before any
	// other pending repaint. Otherwise keys are served round-robin.
	var op, pressOp *writeOp
	advance, pressAdvance := 0, 0
	for i := range indexes {
		index := indexes[(e.cursor+i)%len(indexes)]
		ks := e.keys[index]
		img, base, press, _ := ks.wanted(now, e.limits)
		if img == nil || img == ks.displayed {
			continue
		}
		if now.Before(ks.retryNotBefore) {
			next = earliest(next, ks.retryNotBefore)
			continue
		}
		if op == nil {
			op = &writeOp{key: index, img: img, base: base, press: press}
			advance = i + 1
		}
		if press != nil && pressOp == nil {
			pressOp = &writeOp{key: index, img: img, base: base, press: press}
			pressAdvance = i + 1
		}
	}
	if pressOp != nil {
		op, advance = pressOp, pressAdvance
	}
	if op == nil {
		return nil, next
	}
	if !e.lastWrite.IsZero() {
		if paced := e.lastWrite.Add(e.interval); now.Before(paced) {
			return nil, earliest(next, paced)
		}
	}
	e.cursor = (e.cursor + advance) % len(indexes)
	return op, next
}

// commit records the outcome of a write decided by step.
func (e *engine) commit(op *writeOp, now time.Time, err error) {
	e.lastWrite = now
	ks, ok := e.keys[op.key]
	if !ok {
		return
	}
	if err != nil {
		ks.retryNotBefore = now.Add(e.failDelay)
		return
	}
	ks.retryNotBefore = time.Time{}
	ks.displayed = op.img
	// Only the layer that produced the frame is stamped, and only if it is
	// still the layer in place (the key may have changed during the write).
	if op.press != nil && ks.press == op.press {
		markPainted(nil, op.press, now)
	} else if op.base != nil && ks.base == op.base {
		markPainted(op.base, nil, now)
	}
}

// attach binds a writer for a new physical connection. Nothing is known to be
// displayed, local press overlays are dropped, waiting programs are adopted,
// finite animations settle to their end frame, and unbounded loops restart.
func (e *engine) attach(w Writer) {
	e.writer = w
	e.lastWrite = time.Time{}
	e.cursor = 0
	for _, ks := range e.keys {
		ks.displayed = nil
		ks.press = nil
		ks.retryNotBefore = time.Time{}
		if ks.pending != nil {
			ks.adopt(ks.pending)
		}
		if ks.base == nil {
			continue
		}
		ks.base.start = time.Time{}
		if anim := ks.base.program.Animation; anim.valid() && anim.Loops != 0 {
			ks.base.finished = true
		}
	}
}

func (e *engine) detach() {
	e.writer = nil
}

func (e *engine) sortedKeys() []int {
	indexes := make([]int, 0, len(e.keys))
	for index := range e.keys {
		indexes = append(indexes, index)
	}
	// Small fixed set (≤ maxKeys); insertion sort keeps it allocation-free
	// and deterministic.
	for i := 1; i < len(indexes); i++ {
		for j := i; j > 0 && indexes[j-1] > indexes[j]; j-- {
			indexes[j-1], indexes[j] = indexes[j], indexes[j-1]
		}
	}
	return indexes
}
