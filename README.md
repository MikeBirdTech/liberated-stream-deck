# Liberated Stream Deck Plus

This repository is a macOS proof of concept for operating one Elgato Stream
Deck Plus directly over USB HID, without the Elgato desktop application or
plugin runtime. It uses the factory firmware; no firmware replacement is
needed.

The current hardware-focused demo implements:

- enumeration and ordinary open of VID/PID `0x0fd9:0x0084` through
  `github.com/sstallion/go-hid`;
- normalized input for eight keys, four encoder rotations, four encoder
  buttons, and touchscreen TAP, PRESS, and FLICK interactions;
- generated JPEG output for all eight 120x120 key images and the full 800x100
  touchscreen window; and
- strict 0..100 hardware brightness control.

Run it with:

```bash
go run ./cmd/deckdemo
```

Fully quit the Elgato Stream Deck application before input testing. On the
tested Mac, Elgato and this demo could both open the device and send display
output, but input reached this demo only after Elgato Stream Deck quit.

The first valid key-state report and first valid encoder-button report establish
independent baselines and do not emit synthetic transitions. Rotation and
touch reports are immediate. If needed, press and release one key and one dial
once to establish those baselines before testing subsequent transitions.

One tested firmware discrepancy is handled explicitly: Elgato documents a
10-byte TAP payload, while the physical unit reported the documented fields in
a 14-byte payload with four additional reserved zero bytes. The decoder accepts
both the documented 10-byte and observed 14-byte shapes for TAP/PRESS without
changing coordinate offsets.

The implementation intentionally does not include reconnect, partial-window
updates, the full 800x480 LCD command, daemon packaging, configuration, or
the controller integration.

Protocol authority: [Elgato Stream Deck + HID API](https://docs.elgato.com/streamdeck/hid/stream-deck-plus/)
and [Elgato HID general reference](https://docs.elgato.com/streamdeck/hid/general/).
