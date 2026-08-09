package streamdeck

// KeyEvent is a key state transition. Key uses a zero-based physical index.
type KeyEvent struct {
	Key     int
	Pressed bool
}

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
