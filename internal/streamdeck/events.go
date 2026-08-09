package streamdeck

import "fmt"

// Event is a normalized physical input from the Stream Deck Plus.
type Event interface {
	isEvent()
}

// KeyEvent is a key state transition. Key uses a zero-based physical index.
type KeyEvent struct {
	Key     int
	Pressed bool
}

func (KeyEvent) isEvent() {}

// KeySnapshot is the baseline state established by the first valid key report.
type KeySnapshot struct {
	Pressed [KeyCount]bool
}

// KeyRead contains either an initial baseline or subsequent key transitions.
// The first valid key report never produces transitions.
type KeyRead struct {
	Baseline *KeySnapshot
	Events   []KeyEvent
}

// DialPressEvent is a rotary encoder button transition. Dial is zero-based.
type DialPressEvent struct {
	Dial    int
	Pressed bool
}

func (DialPressEvent) isEvent() {}

// DialRotateEvent is an immediate signed encoder movement. Positive is
// clockwise and negative is counter-clockwise.
type DialRotateEvent struct {
	Dial  int
	Delta int
}

func (DialRotateEvent) isEvent() {}

// DialButtonSnapshot is the baseline established by the first valid encoder
// button report.
type DialButtonSnapshot struct {
	Pressed [DialCount]bool
}

// TouchKind identifies one of the three interactions emitted by the device.
type TouchKind int

const (
	TouchTap TouchKind = iota
	TouchPress
	TouchFlick
)

func (k TouchKind) String() string {
	switch k {
	case TouchTap:
		return "TAP"
	case TouchPress:
		return "PRESS"
	case TouchFlick:
		return "FLICK"
	default:
		return fmt.Sprintf("TouchKind(%d)", k)
	}
}

// TouchEvent retains raw logical window coordinates. TAP and PRESS populate
// X/Y; FLICK populates StartX/StartY and EndX/EndY.
type TouchEvent struct {
	Kind TouchKind

	X int
	Y int

	StartX int
	StartY int
	EndX   int
	EndY   int
}

func (TouchEvent) isEvent() {}

// InputRead is the normalized result of one HID input report.
type InputRead struct {
	KeyBaseline  *KeySnapshot
	DialBaseline *DialButtonSnapshot
	Events       []Event
	Diagnostics  []string
}
