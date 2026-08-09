# Liberated Stream Deck Plus

Liberated Stream Deck Plus is an open-source Go library and demo application
for controlling the Elgato Stream Deck Plus directly over USB HID on macOS,
without requiring the Elgato Stream Deck application at runtime. It uses the
device's factory firmware; no firmware replacement is needed.

## Status

This is early software, but its core Stream Deck Plus input and output have
been physically verified on real hardware. The implementation is intentionally
small and focused while the protocol and runtime behavior mature.

## Supported hardware

Currently supported and physically verified:

- Elgato Stream Deck Plus
- model `20GBD9901`
- USB vendor ID `0x0FD9`
- USB product ID `0x0084`

Other Stream Deck models are not currently supported.

## Supported platform

Currently physically verified:

- macOS on Apple Silicon

Windows, Linux, and Intel macOS have not been verified and are not currently
claimed as supported.

## Features

- direct USB HID discovery, input, and output
- all eight 120x120 LCD keys
- generated JPEG key images
- all four rotary encoders
- all four encoder buttons
- touch-strip TAP, PRESS, and FLICK input
- complete 800x100 touch-strip JPEG output
- brightness control from 0 through 100 percent
- first key and encoder-button presses delivered immediately after startup
- unplug/replug recovery with key UI, strip UI, and brightness restoration
- clean, idempotent shutdown

The demo continues to send complete 800x100 touch-strip redraws. Partial strip
updates are deliberately out of scope until real hardware use demonstrates a
need for them.

## Demo

Fully quit the Elgato Stream Deck application, connect the Stream Deck Plus,
and run:

```bash
go run ./cmd/deckdemo
```

The process can start while the deck is absent. It retries discovery about
once per second, remains alive if the USB device is unplugged, and restores the
current demo state after reconnection.

Demo controls:

- LCD keys 1-8 toggle their own ON/OFF state.
- Dial 1 changes a counter; pressing it resets the counter.
- Dial 2 changes brightness in five-percent steps; pressing it toggles between
  15 and 70 percent.
- Dial 3 changes the selected key; pressing it toggles that key.
- Dial 4 changes between INPUT, KEY TEST, and TOUCH TEST views; pressing it
  returns to INPUT.
- TAP, PRESS, and FLICK gestures are shown with their device coordinates.

The three strip views favor large, glanceable diagnostics instead of fitting
every value into one small line. Key indexing is one-based in the demo and its
logs; normalized library event indexes are zero-based.

Stop the demo with Control-C.

## Elgato application conflict

On the physically tested Mac, the Elgato Stream Deck application could coexist
well enough for device enumeration and display output. While it was running,
however, it consumed or contended for input reports. Fully quit the Elgato
Stream Deck application for reliable direct key, encoder, and touch input.

No claim is made that other Elgato applications must be quit.

## macOS execution environments

USB HID access may fail from a restricted or sandboxed execution environment
even when the same executable works from a normal local terminal. Treat a
sandbox-only open or enumeration failure separately from device or protocol
failure.

## Building

Requirements:

- Go 1.24 or later (the module currently declares Go 1.24)
- CGO enabled
- Apple Command Line Tools, or Xcode, providing a C compiler and macOS SDK
- [`github.com/sstallion/go-hid`](https://github.com/sstallion/go-hid), fetched
  by the Go module toolchain

Build everything with:

```bash
CGO_ENABLED=1 go build ./...
```

On macOS, `go-hid` compiles its included HIDAPI source and links the required
Apple frameworks through CGO. A separately installed HIDAPI dynamic library is
not required.

## Protocol

The implementation follows Elgato's published
[Stream Deck Plus HID documentation](https://docs.elgato.com/streamdeck/hid/stream-deck-plus/)
and [general Stream Deck HID documentation](https://docs.elgato.com/streamdeck/hid/general/).
Protocol constants, little-endian report fields, full-report sizes, and the
physical-event decoder remain visible in `internal/streamdeck`.

One compatibility detail comes from physical testing: the tested firmware
produced TAP/PRESS-compatible reports with a 14-byte payload, including the
documented fields followed by reserved bytes. The published packet description
uses a 10-byte payload. The decoder accepts both 10-byte and observed 14-byte
forms, keeps the same coordinate offsets, and ignores the additional reserved
bytes. This is an observed compatibility behavior for one tested firmware, not
a claim about every firmware version.

## Testing

Run the automated checks with:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

The tests cover report parsing, the physically observed 14-byte touch payload,
first-press transitions, output report construction, brightness bounds,
complete UI restoration, reconnect sequencing, cancellation, rendering, and
idempotent close behavior.

Automated tests do not replace physical verification of macOS HID permissions,
USB removal detection, firmware behavior, image orientation and appearance,
touch-strip readability, or contention with the Elgato Stream Deck
application.

## Project scope

This project currently targets the Stream Deck Plus on Apple Silicon macOS. It
is not presented as a universal Stream Deck framework. Support for other
models or platforms, plugin systems, configurable UI frameworks, installers,
menu-bar packaging, partial strip updates, the full 800x480 LCD command, and
animation systems are outside the current scope.
