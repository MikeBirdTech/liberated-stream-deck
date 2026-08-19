// Package visual plays opaque per-key visual programs on a Stream Deck.
//
// A controller supplies a Program per physical key: a resting frame, an
// optional timed animation, and an optional press-feedback sequence that is
// cached before any input arrives. The package decides nothing about what a
// key means; it only guarantees that the newest accepted program is what the
// hardware shows, that frames are written in order at a safe pace, that a
// superseded animation can never write after a newer revision was accepted,
// and that a press is visibly acknowledged even when the controller's
// acknowledgement is slow or carries the same resting frame.
//
// The Scheduler is surface-agnostic: it addresses a Writer by key index and
// works for any device whose surfaces are whole key frames (Plus, Mini, or a
// future control-API daemon).
package visual

import (
	"image"
	"time"
)

// Frame is one timed raster frame of a Sequence.
type Frame struct {
	Image    image.Image
	Duration time.Duration
}

// Sequence is a timed list of frames.
type Sequence struct {
	Frames []Frame
	// Loops is the number of complete plays. Zero means "repeat until the
	// program is replaced", bounded by Options.MaxLoopDuration; values above
	// Options.MaxLoops are clamped. One is a single play.
	Loops int
	// HoldLast keeps the final frame after a finite play completes instead of
	// returning to the program's resting frame.
	HoldLast bool
}

// Program is the complete authoritative visual for one key.
type Program struct {
	// Revision identifies the program. A SetProgram with the revision already
	// accepted for the key is a no-op, so unchanged polls never repaint.
	Revision string
	// Rest is the steady-state frame: shown when there is no animation, after
	// a finite animation completes (unless HoldLast), and after a press ends.
	// It must not be nil.
	Rest image.Image
	// Animation optionally plays from the program's first paint.
	Animation *Sequence
	// Press optionally supplies cached press feedback. It starts on key-down,
	// holds its last frame while the key stays down, and ends on key-up
	// (never before PressMinVisible has elapsed). When nil the scheduler
	// derives a generic depression of the current frame.
	Press *Sequence
	// MinVisible is the minimum time this program stays on the key once it
	// has painted before a newer program may replace it. Zero lets a newer
	// program replace it immediately (and coalesce it if it never painted).
	MinVisible time.Duration
	// PressMinVisible overrides Options.DefaultPressMinVisible for this
	// program's press feedback (including the generic fallback).
	PressMinVisible time.Duration
}

func (p *Program) pressMinVisible(fallback time.Duration) time.Duration {
	if p != nil && p.PressMinVisible > 0 {
		return p.PressMinVisible
	}
	return fallback
}

// total returns the duration of one play after duration clamping.
func (s *Sequence) cycle(minFrame time.Duration) time.Duration {
	var total time.Duration
	for _, frame := range s.Frames {
		total += clampFrame(frame.Duration, minFrame)
	}
	return total
}

func clampFrame(d, minFrame time.Duration) time.Duration {
	if d < minFrame {
		return minFrame
	}
	return d
}

// frameAt returns the frame shown at elapsed time into the play and the
// remaining time until the next frame boundary. finished reports that the
// sequence has completed all its plays (or hit the loop-duration bound).
func (s *Sequence) frameAt(elapsed time.Duration, o limits) (img image.Image, until time.Duration, finished bool) {
	if s == nil || len(s.Frames) == 0 {
		return nil, 0, true
	}
	cycle := s.cycle(o.minFrame)
	if cycle <= 0 {
		return s.Frames[len(s.Frames)-1].Image, 0, true
	}
	loops := s.Loops
	if loops > o.maxLoops {
		loops = o.maxLoops
	}
	if loops > 0 && elapsed >= cycle*time.Duration(loops) {
		return s.Frames[len(s.Frames)-1].Image, 0, true
	}
	if elapsed >= o.maxLoop {
		return s.Frames[len(s.Frames)-1].Image, 0, true
	}
	if elapsed < 0 {
		elapsed = 0
	}
	offset := elapsed % cycle
	for _, frame := range s.Frames {
		d := clampFrame(frame.Duration, o.minFrame)
		if offset < d {
			return frame.Image, d - offset, false
		}
		offset -= d
	}
	// Unreachable in practice (offset < cycle), but keep the last frame.
	return s.Frames[len(s.Frames)-1].Image, 0, false
}

// valid reports whether a sequence has at least one frame with an image.
func (s *Sequence) valid() bool {
	if s == nil || len(s.Frames) == 0 {
		return false
	}
	for _, frame := range s.Frames {
		if frame.Image == nil {
			return false
		}
	}
	return true
}

type limits struct {
	minFrame time.Duration
	maxLoops int
	maxLoop  time.Duration
}
