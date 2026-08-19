package visual

import (
	"image"
	"sync"
	"time"
)

// Writer paints one complete key frame. SetKeyImage is called from the
// scheduler goroutine only, one call at a time, never concurrently.
type Writer interface {
	SetKeyImage(index int, img image.Image) error
}

// Defaults for Options zero values.
const (
	// DefaultMinInterval is the minimum gap between any two key writes. Key
	// image uploads are ordinary output reports (measured at roughly 4 ms
	// each on a Plus), so 20 ms keeps the aggregate below ~50 writes/s.
	DefaultMinInterval = 20 * time.Millisecond
	// DefaultMinFrameDuration bounds per-key frame rate at 25 fps.
	DefaultMinFrameDuration = 40 * time.Millisecond
	// DefaultMaxLoops bounds explicit loop counts.
	DefaultMaxLoops = 10000
	// DefaultMaxLoopDuration bounds "repeat until replaced" playback; after
	// it the sequence settles on its end frame until a new revision arrives.
	DefaultMaxLoopDuration = 30 * time.Minute
	// DefaultPressMinVisible keeps press feedback on the key long enough to
	// be perceived even for the shortest tap or fastest acknowledgement.
	DefaultPressMinVisible = 150 * time.Millisecond
	// DefaultFailureDelay holds a key back after a failed write so a wedged
	// endpoint is not hammered while the connection recovers.
	DefaultFailureDelay = time.Second
	// DefaultMaxKeys bounds the per-key state map.
	DefaultMaxKeys = 32
)

// Options configures a Scheduler. Zero values select the defaults above.
type Options struct {
	MinInterval            time.Duration
	MinFrameDuration       time.Duration
	MaxLoops               int
	MaxLoopDuration        time.Duration
	DefaultPressMinVisible time.Duration
	FailureDelay           time.Duration
	MaxKeys                int
	// PressFallback derives generic press feedback from the frame currently
	// on the key when a program carries no press sequence. Defaults to
	// Depress. Returning nil suppresses the fallback.
	PressFallback func(image.Image) image.Image
	// OnWriteError is called (outside the scheduler lock) after a failed
	// hardware write. The key is retried after FailureDelay.
	OnWriteError func(key int, err error)
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Scheduler owns all key writes for a device. It is safe for concurrent use;
// every method returns immediately and the single scheduler goroutine does
// the writing. Programs survive Detach/Attach cycles (reconnects); Close
// stops the goroutine and waits for it to exit.
type Scheduler struct {
	mu       sync.Mutex
	cond     *sync.Cond
	engine   engine
	now      func() time.Time
	onError  func(int, error)
	inflight bool
	closed   bool
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// New creates a scheduler and starts its goroutine.
func New(opts Options) *Scheduler {
	s := &Scheduler{
		engine: engine{
			keys:      make(map[int]*keyState),
			maxKeys:   orInt(opts.MaxKeys, DefaultMaxKeys),
			interval:  orDuration(opts.MinInterval, DefaultMinInterval),
			failDelay: orDuration(opts.FailureDelay, DefaultFailureDelay),
			pressMin:  orDuration(opts.DefaultPressMinVisible, DefaultPressMinVisible),
			fallback:  opts.PressFallback,
			limits: limits{
				minFrame: orDuration(opts.MinFrameDuration, DefaultMinFrameDuration),
				maxLoops: orInt(opts.MaxLoops, DefaultMaxLoops),
				maxLoop:  orDuration(opts.MaxLoopDuration, DefaultMaxLoopDuration),
			},
		},
		now:     opts.Now,
		onError: opts.OnWriteError,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	if s.engine.fallback == nil {
		s.engine.fallback = Depress
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.cond = sync.NewCond(&s.mu)
	go s.run()
	return s
}

func orInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

func orDuration(v, d time.Duration) time.Duration {
	if v <= 0 {
		return d
	}
	return v
}

// SetProgram makes p the authoritative visual for a key. A program whose
// revision is already accepted is ignored; a different revision replaces the
// current visual as soon as its minimum visible time allows and cancels any
// animation still playing. Programs without a Rest frame are ignored.
func (s *Scheduler) SetProgram(key int, p Program) {
	s.mu.Lock()
	s.engine.setProgram(key, p, s.now())
	s.mu.Unlock()
	s.poke()
}

// Press starts press feedback for a key-down event.
func (s *Scheduler) Press(key int) {
	s.mu.Lock()
	s.engine.press(key, s.now())
	s.mu.Unlock()
	s.poke()
}

// Release ends press feedback for a key-up event once its minimum visible
// time has elapsed.
func (s *Scheduler) Release(key int) {
	s.mu.Lock()
	s.engine.release(key, s.now())
	s.mu.Unlock()
	s.poke()
}

// Attach binds the scheduler to a freshly connected device. Every key is
// repainted with its latest steady state: waiting programs are adopted,
// finite animations show their end frame, unbounded loops restart, and local
// press overlays are dropped.
func (s *Scheduler) Attach(w Writer) {
	s.mu.Lock()
	s.engine.attach(w)
	s.mu.Unlock()
	s.poke()
}

// Detach stops writes (for example before closing the device). It returns
// once no write is in flight. Programs are retained for the next Attach.
func (s *Scheduler) Detach() {
	s.mu.Lock()
	s.engine.detach()
	for s.inflight {
		s.cond.Wait()
	}
	s.mu.Unlock()
}

// Close detaches and stops the scheduler goroutine, waiting for it to exit.
// It is safe to call more than once.
func (s *Scheduler) Close() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.engine.detach()
		s.mu.Unlock()
		close(s.stop)
	})
	<-s.done
}

// Displayed reports the frame the scheduler believes is on a key (nil when
// nothing has been written since the last Attach).
func (s *Scheduler) Displayed(key int) image.Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ks, ok := s.engine.keys[key]; ok {
		return ks.displayed
	}
	return nil
}

// Pressed reports whether a press overlay is active on a key.
func (s *Scheduler) Pressed(key int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks, ok := s.engine.keys[key]
	return ok && ks.press != nil
}

// Revision reports the revision of the program currently accepted for a key
// (empty when none).
func (s *Scheduler) Revision(key int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ks, ok := s.engine.keys[key]; ok && ks.base != nil {
		return ks.base.program.Revision
	}
	return ""
}

func (s *Scheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) run() {
	defer close(s.done)
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		op, next := s.engine.step(s.now())
		if op != nil {
			writer := s.engine.writer
			s.inflight = true
			s.mu.Unlock()

			err := writer.SetKeyImage(op.key, op.img)

			s.mu.Lock()
			s.inflight = false
			s.engine.commit(op, s.now(), err)
			s.cond.Broadcast()
			s.mu.Unlock()
			if err != nil && s.onError != nil {
				s.onError(op.key, err)
			}
			continue
		}
		s.mu.Unlock()

		var due <-chan time.Time
		var timer *time.Timer
		if !next.IsZero() {
			delay := next.Sub(s.now())
			if delay < time.Millisecond {
				delay = time.Millisecond
			}
			timer = time.NewTimer(delay)
			due = timer.C
		}
		select {
		case <-s.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.wake:
		case <-due:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
