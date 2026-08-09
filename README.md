# Liberated Stream Deck

Liberated Stream Deck is a small Go library and diagnostic demo for controlling
Elgato Stream Deck hardware directly over USB HID, without requiring the Elgato
Stream Deck application at runtime. It uses factory firmware; no firmware
replacement is needed.

## Status

Stream Deck Plus and Original Stream Deck Mini support are physically verified
on Apple Silicon macOS. Linux/arm64 enablement is code-complete; Raspberry Pi
hardware verification is still pending. See the [manual test
plan](docs/manual-test-plan.md) for the exact remaining hardware checks.

## Support matrix

| Model | USB PID | Platform | Implementation | Physical verification |
| --- | --- | --- | --- | --- |
| Stream Deck Plus | `0x0084` | Apple Silicon macOS | Complete | Verified |
| Stream Deck Plus | `0x0084` | Linux/arm64, including Raspberry Pi | Expected to work through the same hidraw backend | Not tested |
| Original Stream Deck Mini | `0x0063` | Apple Silicon macOS | Complete | Verified |
| Original Stream Deck Mini | `0x0063` | Linux/arm64, including Raspberry Pi | Complete | Pending |

All supported devices use Elgato vendor ID `0x0FD9`. The 2022 Mini uses PID
`0x0090` according to Elgato's device table. It is not enabled yet because the
target hardware PID still needs confirmation; the original Mini PID is isolated
as `MiniProductID` in `internal/streamdeck/mini_protocol.go`.

Windows and Intel macOS have not been verified and are not currently claimed as
supported.

## Features

Shared features:

- direct USB HID discovery, input, and output
- model-aware enumeration and automatic Plus-first connection
- generated native-size key images
- brightness control from 0 through 100 percent
- first key presses delivered immediately after startup
- unplug/replug recovery with UI and brightness restoration
- clean, idempotent shutdown

Stream Deck Plus:

- all eight 120x120 LCD keys
- all four rotary encoders and encoder buttons
- touch-strip TAP, PRESS, and FLICK input
- complete 800x100 touch-strip JPEG output

Original Stream Deck Mini:

- all six 80x80 LCD keys
- key snapshot transition decoding
- rotated 24-bit BMP key output using the Mini-specific protocol
- no dial or touch events, matching the hardware

The Plus demo continues to send complete 800x100 touch-strip redraws. Partial
strip updates remain out of scope.

## Library API

`streamdeck.Device` is the common interface for supported models: `Info`,
`ReadEvents`, `SetBrightness`, `SetKeyImage`, and `Close`. `DeviceInfo.Model`
identifies the connected model.

- `List()` returns every supported model found by wildcard Elgato HID
  enumeration.
- `Open()` retains its original behavior and opens only the first Plus.
- `OpenModel(model)` opens an explicit model.
- `OpenAny()` tries the Plus first, then the Mini.

The concrete Plus `Deck` type still exposes `SetTouchStripImage`. Key, dial, and
encoder indexes in the library are zero-based physical indexes.

## Demo

Connect either supported deck and run:

```bash
go run ./cmd/deckdemo
```

The process can start while a deck is absent. It retries discovery about once
per second, remains alive if USB is unplugged, and restores the current state
after reconnection. At connection time it logs VID, PID, USB product, and the
detected model.

Plus controls:

- LCD keys 1-8 toggle their own ON/OFF state.
- Dial 1 changes a counter; pressing it resets the counter.
- Dial 2 changes brightness in five-percent steps; pressing it toggles between
  15 and 70 percent.
- Dial 3 changes the selected key; pressing it toggles that key.
- Dial 4 changes between INPUT, KEY TEST, and TOUCH TEST views; pressing it
  returns to INPUT.
- TAP, PRESS, and FLICK gestures are shown with their device coordinates.

Mini controls:

- LCD keys 1-6 toggle their own ON/OFF state.
- Pressing key 5 also reduces brightness by 10 percent.
- Pressing key 6 also increases brightness by 10 percent.
- No strip, dial, or touch output is rendered or logged.

Demo indexing is one-based in labels and logs. Stop the demo with Control-C.

## Elgato application conflict

On the physically tested Mac, the Elgato Stream Deck application could coexist
well enough for enumeration and display output, but it consumed or contended
for input reports. Fully quit the Elgato Stream Deck application for reliable
direct input.

Linux has no Elgato Stream Deck application, so that conflict does not exist.
Another third-party Stream Deck daemon can still claim or contend for the HID
device and should be stopped during testing.

## macOS execution environments

USB HID access may fail from a restricted or sandboxed execution environment
even when the same executable works from a normal local terminal. Treat a
sandbox-only open or enumeration failure separately from a device or protocol
failure.

## Building on macOS

Requirements:

- Go 1.24 or later (the module declares Go 1.24)
- CGO enabled
- Apple Command Line Tools, or Xcode, providing a C compiler and macOS SDK

Build everything with:

```bash
CGO_ENABLED=1 go build ./...
```

On macOS, `go-hid` compiles its included HIDAPI source and links the required
Apple frameworks through CGO. A separately installed HIDAPI dynamic library is
not required.

## Raspberry Pi / Linux setup

Use a 64-bit Raspberry Pi OS release on an arm64-capable Pi. Install the native
build and USB inspection dependencies:

```bash
sudo apt update
sudo apt install -y gcc libudev-dev git usbutils
```

Install Go 1.24 or later for `linux-arm64` using the official
[Go installation instructions](https://go.dev/doc/install), then confirm:

```bash
go version
go env GOOS GOARCH CGO_ENABLED
```

Expected values include `linux`, `arm64`, and `1`. `go-hid` v0.15.0 includes
HIDAPI's Linux hidraw backend and links `-ludev`; no build tags or separately
installed HIDAPI library are needed. Build on the Pi with:

```bash
git clone https://github.com/MikeBirdTech/liberated-stream-deck.git
cd liberated-stream-deck
CGO_ENABLED=1 go build ./...
```

Install the included udev rules and ensure the interactive user belongs to the
`plugdev` group:

```bash
sudo install -m 0644 install/udev/50-streamdeck.rules /etc/udev/rules.d/50-streamdeck.rules
sudo usermod -aG plugdev "$USER"
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=hidraw
```

Log out and back in after changing group membership, then unplug and reconnect
the deck. Run `go run ./cmd/deckdemo` as the ordinary user, not with `sudo`.
Detailed permission checks and reboot verification are in the
[manual test plan](docs/manual-test-plan.md).

## Protocol

The Plus path follows Elgato's published
[Stream Deck Plus HID documentation](https://docs.elgato.com/streamdeck/hid/stream-deck-plus/)
and [general HID documentation](https://docs.elgato.com/streamdeck/hid/general/).
The Mini path follows Elgato's separate
[Stream Deck Mini HID documentation](https://docs.elgato.com/streamdeck/hid/mini/)
and the independently exercised `python-elgato-streamdeck` Mini implementation.

The original Mini protocol is materially different from both the Plus and the
15-key Original protocol: it uses 65-byte key input snapshots; 1024-byte image
reports with a 16-byte header and rotated BMP payload; and a 17-byte brightness
feature report beginning `05 55 aa d1 01`. The 8191-byte image report sometimes
called the "classic" format belongs to the 15-key Original, not PID `0x0063`.

One Plus compatibility detail comes from physical testing: the tested firmware
produced TAP/PRESS-compatible reports with a 14-byte payload, including the
documented fields followed by reserved bytes. The published packet description
uses a 10-byte payload. The decoder accepts both forms, preserves the documented
coordinate offsets, and ignores the additional reserved bytes.

## Testing

Run the automated checks with:

```bash
CGO_ENABLED=1 go test ./...
go test -race ./...
go vet ./...
go build ./...
```

The tests cover both model routes, Mini snapshot transitions and invalid state
bytes, exact Mini image and brightness report bytes, Mini BMP orientation and
bounds, existing Plus input/output behavior, short HID writes, complete UI
restoration, reconnect sequencing, cancellation, rendering, and idempotent
close behavior.

Automated tests do not replace physical verification of HID permissions, USB
removal detection, firmware behavior, image orientation and appearance, or
input contention. Current hardware status is recorded in the support matrix.

## Project scope

This project intentionally remains a small direct-HID library and diagnostic
demo. Plugin systems, configurable UI frameworks, installers, menu-bar
packaging, partial strip updates, full-screen LCD commands, and animation
systems are outside the current scope.
