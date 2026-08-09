# Liberated Stream Deck Plus

This repository is a macOS proof of concept for operating an Elgato Stream
Deck Plus directly over USB HID, without using the Elgato plugin runtime.

The current implementation intentionally stops at Milestone 3:

- enumerate VID/PID `0x0fd9:0x0084`;
- open the first matching device with `github.com/sstallion/go-hid`;
- baseline and decode key-state reports;
- log key 1 press/release transitions; and
- send a generated 120x120 JPEG orientation card to physical key 1.

Run it with:

```bash
go run ./cmd/deckdemo
```

The first valid key report establishes the decoder baseline and does not emit
transitions. If the device does not send a snapshot immediately after opening,
press and release key 1 once to establish the baseline, then press and release
it again to observe both transitions.

The orientation card should show red at the top, blue at the bottom, green on
the left, yellow on the right, and a white arrow pointing upward.

No dial, touch-strip, brightness, reconnect, daemon, or the controller behavior is
implemented yet.
