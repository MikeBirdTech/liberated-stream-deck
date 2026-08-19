package visual

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"testing"
	"time"
)

type safeRecorder struct {
	mu     sync.Mutex
	writes []write
	fail   error
	block  chan struct{} // when non-nil, SetKeyImage waits for it to close
	onCall func()
}

func (r *safeRecorder) SetKeyImage(index int, img image.Image) error {
	r.mu.Lock()
	block := r.block
	fail := r.fail
	onCall := r.onCall
	r.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	if block != nil {
		<-block
	}
	if fail != nil {
		return fail
	}
	r.mu.Lock()
	r.writes = append(r.writes, write{key: index, img: img})
	r.mu.Unlock()
	return nil
}

func (r *safeRecorder) snapshot() []write {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]write(nil), r.writes...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func newTestScheduler(t *testing.T, opts Options) (*Scheduler, *safeRecorder) {
	t.Helper()
	rec := &safeRecorder{}
	if opts.MinInterval == 0 {
		opts.MinInterval = time.Millisecond
	}
	if opts.MinFrameDuration == 0 {
		opts.MinFrameDuration = 5 * time.Millisecond
	}
	s := New(opts)
	t.Cleanup(s.Close)
	s.Attach(rec)
	return s, rec
}

func TestSchedulerWritesAsynchronouslyAndSkipsUnchangedRevision(t *testing.T) {
	s, rec := newTestScheduler(t, Options{})
	rest := frame(1)
	s.SetProgram(0, Program{Revision: "a", Rest: rest})
	waitFor(t, "first write", func() bool { return len(rec.snapshot()) == 1 })
	if s.Displayed(0) != rest || s.Revision(0) != "a" {
		t.Fatalf("displayed=%p revision=%q", s.Displayed(0), s.Revision(0))
	}
	s.SetProgram(0, Program{Revision: "a", Rest: frame(2)})
	time.Sleep(20 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("unchanged revision caused writes: %v", got)
	}
}

func TestSchedulerPlaysFramesInOrderOverRealTime(t *testing.T) {
	s, rec := newTestScheduler(t, Options{})
	rest, f0, f1, f2 := frame(0), frame(1), frame(2), frame(3)
	s.SetProgram(0, Program{Revision: "anim", Rest: rest, Animation: &Sequence{Loops: 1, Frames: []Frame{
		{Image: f0, Duration: 20 * time.Millisecond},
		{Image: f1, Duration: 20 * time.Millisecond},
		{Image: f2, Duration: 20 * time.Millisecond},
	}}})
	waitFor(t, "animation to finish", func() bool { return len(rec.snapshot()) == 4 })
	got := rec.snapshot()
	want := []image.Image{f0, f1, f2, rest}
	for i := range want {
		if got[i].img != want[i] {
			t.Fatalf("write %d = %p, want %p", i, got[i].img, want[i])
		}
	}
}

func TestSchedulerPressBeforeSlowAckAndAckSupersedes(t *testing.T) {
	s, rec := newTestScheduler(t, Options{DefaultPressMinVisible: 30 * time.Millisecond})
	rest := frame(100)
	s.SetProgram(0, Program{Revision: "rest", Rest: rest})
	waitFor(t, "rest", func() bool { return len(rec.snapshot()) == 1 })

	s.Press(0)
	waitFor(t, "press feedback", func() bool { return len(rec.snapshot()) == 2 })
	if !s.Pressed(0) {
		t.Fatal("press overlay not active")
	}
	// The "ack" arrives later with a new revision: it must land once the
	// press has been visible for its minimum time.
	starting := frame(50)
	s.SetProgram(0, Program{Revision: "starting", Rest: starting})
	waitFor(t, "starting frame", func() bool {
		got := rec.snapshot()
		return len(got) == 3 && got[2].img == starting
	})
	if s.Pressed(0) {
		t.Fatal("press overlay survived controller feedback")
	}
	s.Release(0)
	time.Sleep(20 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 3 {
		t.Fatalf("late release caused writes: %v", got)
	}
}

func TestSchedulerDetachWaitsForInFlightWrite(t *testing.T) {
	s, rec := newTestScheduler(t, Options{})
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	rec.mu.Lock()
	rec.block = release
	rec.onCall = func() { once.Do(func() { close(started) }) }
	rec.mu.Unlock()

	s.SetProgram(0, Program{Revision: "a", Rest: frame(1)})
	<-started

	detached := make(chan struct{})
	go func() {
		s.Detach()
		close(detached)
	}()
	select {
	case <-detached:
		t.Fatal("Detach returned while a write was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-detached:
	case <-time.After(time.Second):
		t.Fatal("Detach did not return after the write completed")
	}
	if len(rec.snapshot()) != 1 {
		t.Fatalf("writes = %v", rec.snapshot())
	}
	// Detached: further programs are retained but not written.
	s.SetProgram(0, Program{Revision: "b", Rest: frame(2)})
	time.Sleep(20 * time.Millisecond)
	if len(rec.snapshot()) != 1 {
		t.Fatal("write happened while detached")
	}
	rec.mu.Lock()
	rec.block = nil
	rec.mu.Unlock()
	s.Attach(rec)
	waitFor(t, "reattach write", func() bool { return len(rec.snapshot()) == 2 })
}

func TestSchedulerReportsWriteErrorsAndRetries(t *testing.T) {
	var mu sync.Mutex
	var errs []error
	s, rec := newTestScheduler(t, Options{
		FailureDelay: 10 * time.Millisecond,
		OnWriteError: func(key int, err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		},
	})
	rec.mu.Lock()
	rec.fail = errors.New("stalled")
	rec.mu.Unlock()
	s.SetProgram(0, Program{Revision: "a", Rest: frame(1)})
	waitFor(t, "error callback", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(errs) >= 1
	})
	rec.mu.Lock()
	rec.fail = nil
	rec.mu.Unlock()
	waitFor(t, "retry success", func() bool { return len(rec.snapshot()) == 1 })
}

func TestSchedulerCloseStopsGoroutineAndIsIdempotent(t *testing.T) {
	before := runtime.NumGoroutine()
	rec := &safeRecorder{}
	s := New(Options{MinInterval: time.Millisecond})
	s.Attach(rec)
	s.SetProgram(0, Program{Revision: "loop", Rest: frame(0), Animation: &Sequence{Frames: []Frame{
		{Image: frame(1), Duration: 5 * time.Millisecond},
		{Image: frame(2), Duration: 5 * time.Millisecond},
	}}})
	waitFor(t, "some animation writes", func() bool { return len(rec.snapshot()) >= 3 })
	s.Close()
	s.Close()
	select {
	case <-s.done:
	default:
		t.Fatal("scheduler goroutine still running after Close")
	}
	count := len(rec.snapshot())
	time.Sleep(30 * time.Millisecond)
	if len(rec.snapshot()) != count {
		t.Fatal("writes continued after Close")
	}
	waitFor(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before })
	// Calls after Close are harmless.
	s.SetProgram(0, Program{Revision: "x", Rest: frame(9)})
	s.Press(0)
	s.Release(0)
	s.Detach()
}

func TestSchedulerConcurrentCallersAreRaceFree(t *testing.T) {
	s, rec := newTestScheduler(t, Options{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				key := (g + i) % 8
				s.SetProgram(key, Program{Revision: string(rune('a' + i%5)), Rest: frame(uint8(i)), Animation: &Sequence{Frames: []Frame{
					{Image: frame(uint8(i + 1)), Duration: 5 * time.Millisecond},
				}}})
				s.Press(key)
				s.Release(key)
				if i%10 == 0 {
					s.Detach()
					s.Attach(rec)
				}
			}
		}(g)
	}
	wg.Wait()
	waitFor(t, "writes", func() bool { return len(rec.snapshot()) > 0 })
}
