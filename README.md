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
- rectangular partial-window JPEG output (any region of the 800x100 window)
- full-screen 800x480 LCD JPEG output (display-only)
- whole-LCD color fill (documented setter feature report 0x03/0x05)
- single-key color fill (documented setter feature report 0x03/0x06)
- configurable sleep timeout (documented setter feature report 0x03/0x0D)
- diagnostic getters: firmware versions (0x04/0x05/0x07), serial (0x06),
  unit info (0x08), sleep duration (0x0A)
- on-demand display of the persisted power-on frame (documented setter 0x03/0x02)

Original Stream Deck Mini:

- all six 80x80 LCD keys
- key snapshot transition decoding
- rotated 24-bit BMP key output using the Mini-specific protocol
- no dial or touch events, matching the hardware

The Plus demo accepts controller-owned full 800x100 touch-strip raster frames
and native-coordinate partial-window updates. Full and partial writes are
revision-gated, serialized, paced, and restored safely after reconnects.

## Library API

`streamdeck.Device` is the common interface for supported models: `Info`,
`ReadEvents`, `SetBrightness`, `SetKeyImage`, and `Close`. `DeviceInfo.Model`
identifies the connected model.

- `List()` returns every supported model found by wildcard Elgato HID
  enumeration.
- `Open()` retains its original behavior and opens only the first Plus.
- `OpenModel(model)` opens an explicit model.
- `OpenAny()` tries the Plus first, then the Mini.

The concrete Plus `Deck` type still exposes `SetTouchStripImage` and
`SetLCDImage` (documented output command `0x08`, 800x480 JPEG, display-only).
`SetPartialWindowImage(x, y, img)` (documented output command `0x0C`) uploads
a JPEG into any rectangular region of the 800x100 touchscreen window, using
logical coordinates as published; the region is the image's own bounds and
must fit inside the window. `ShowLogo()` (documented setter feature report
`0x03/0x02`) immediately displays the persisted power-on frame without a
power cycle. `FillLCD(r, g, b)` (documented setter
feature report `0x03/0x05`) fills the entire LCD with one RGB color, and
`FillKey(index, r, g, b)` (documented setter feature report `0x03/0x06`)
fills a single LCD key; the fill colors are volatile. `SetSleepDuration(seconds)`
(documented setter feature report `0x03/0x0D`) sets the idle time before
sleep in seconds (`0` disables) and is persisted on-device, with
`SleepDuration()` reading it back. The diagnostic getters are
`FirmwareVersionLD`/`FirmwareVersionAP1`/`FirmwareVersionAP2` (0x04/0x07/0x05,
version string plus checksum), `UnitSerialNumber` (0x06), and `UnitInfo`
(0x08: keypad matrix, key/LCD geometry, image gallery). Like all
documented image commands, the uploads are volatile: use `UploadBootImage`
when the frame must survive a power cycle. Key, dial, and encoder indexes in
the library are zero-based physical indexes.

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

### Remote presentation

`deckdemo` can be remote-commanded by a local companion controller over HTTP.
The controller's base URL is read from the `LIBERATED_STREAM_DECK_CONTROLLER`
environment variable at startup; when unset (the default) the deck runs the
classic local render without any network I/O. The address is a deployment
detail of this machine and is intentionally never part of this repository -
`install/macos/install.sh` injects it into the generated LaunchAgent plist
when the variable is set at install time.

- revision 2 selects **bridge mode**: the controller owns all state and semantics and
  the deck is a pure renderer. It paints the server-provided key (label plus
  the exact `bg`/`fg` hex colors from the wire, other keys in quiet paper),
  paints the server-provided strip page (title, lines, page dots, events
  seen), POSTs raw physical events unchanged, and re-renders immediately from
  each event ack's `state` object and from periodic GETs on the
  server-chosen `poll_ms` cadence. Page position and key colors are always
  server-derived; the deck never interprets what a key/dial/flick means.
- Any key object (the active `key`, every entry in `background.keys`, and
  keys inside an ack's `state`) may carry an optional `image` object: a
  server-rendered raster frame as base64 PNG/JPEG (`data_b64`), a
  `revision` content digest, and an informational `mime_type`. The deck
  decodes the payload, scales it to the native key size, caches the decoded
  frame by `revision`, and paints it verbatim - it never interprets the
  pixels. If the image is absent or fails to decode, the key falls back to
  the label/bg/fg rendering, so older or image-unaware controllers and
  payloads keep working unchanged.
- The `strip` object may carry the same optional `image` shape. A valid
  base64 PNG/JPEG is scaled to exactly 800x100 and becomes the complete visual
  authority: no locally rendered title, lines, counters, event text, page dots,
  or decorations are added. Missing or invalid images retain the title/lines
  renderer as a backward-compatible fallback. The shape is accepted from both
  GET responses and event-ack `state.strip` objects.
- A raster-backed `strip` may also carry `regions`, each with native `x`/`y`
  coordinates plus `revision`, `mime_type`, and `data_b64`. The decoded image
  dimensions define the patch size. Valid changed patches use
  `SetPartialWindowImage`; invalid or out-of-bounds patches are ignored. Patches
  are never applied without a valid full base frame. Removing or repositioning
  a patch restores the base and then reapplies the authoritative remaining
  list so stale pixels cannot survive.
- Decoded full frames, patches, and failures are cached by revision in a
  bounded cache. Displayed revisions are tracked per physical connection, so
  unchanged polls and acks perform no extra touch-strip writes. Reconnects
  invalidate only display tracking: the current full frame is restored once,
  followed by its current patches, while decoded pixels remain cached.
- All touch-strip output goes through one coalescing writer. It keeps only the
  newest desired state and applies at most one operation per pacing interval;
  `LIBERATED_STREAM_DECK_TOUCH_MIN_INTERVAL_MS` configures the interval (100 ms
  by default). A failed operation is not marked displayed and is held back from
  immediate retry while the normal connection recovery path runs.
- The optional `boot_image` object (`revision` + base64-encoded PNG/JPEG)
  persists a server-chosen 800x480 power-on frame to the device. Uploads use
  the undocumented boot-frame channel (command `0x09`, target `0x05`, JPEG,
  chunk index before chunk size in the header) reverse-engineered from the
  official app on 2026-08-10; unlike the documented image commands the result
  survives power-off. Only a revision change triggers an upload.
- The optional `background` object defines full-key idle frames (same
  label/bg/fg shape as `key`, one entry per index). Keys outside the active
  key render these frames instead of quiet paper, and a clean shutdown
  (SIGINT or SIGTERM) repaints every key to them. Note: repainting only
  updates the live display - key-frame writes do NOT survive a power cycle
  on the tested Plus firmware (verified 2026-08-10); the image shown at
  power-on is whatever device flash contains, only written by the official
  Elgato software.
- revision < 2, or a missing bridge section, keeps the classic rev-1 local
  demo behavior.
- If the endpoint is unreachable, times out, or returns an unreadable body,
  the deck falls back to the classic local render instead of going dark.

The boot-frame channel is also recorded in the Protocol section: it is the
one capability of this library that is not part of Elgato's public HID docs.

The `command` field stays `"run_hardware_demo"` forever as backward
compatibility and is deliberately not used to select the mode. (Previously the
deck refused to draw on any other value and sat dark; that behavior is gone.)

If both a Plus and a Mini are attached, the demo connects the Plus first
(`OpenAny` preference); a targeted run for the other model uses `OpenModel`.

## Install (macOS launchd agent)

One command turns a fresh clone into a long-running daemon that renders and
drives a connected deck, surviving logout and reboots:

```bash
bash install/macos/install.sh          # build + install + launch (RunAtLoad, KeepAlive)
bash install/macos/install.sh status   # current launchd + process + log state
bash install/macos/install.sh uninstall
```

The agent's launchd label is `com.mikebirdtech.liberated-stream-deck`. It runs
`bin/deckdemo` from the checkout with logs under
`~/Library/Logs/liberated-stream-deck/`.

Note: `deckdemo` is remote-commanded by a local companion controller over
HTTP; revision 2 runs in bridge mode (see the remote presentation notes in
the demo section). Any endpoint failure falls back to classic local rendering, so the
deck is never left dark.

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

The boot-frame channel (undocumented, reverse-engineered 2026-08-10): a
full-image upload via output command `0x09`. Frames are the standard
1024-byte chunked reports but with chunk index at header offset +4 and chunk
size at +6 (reversed compared with the documented commands). The payload is
an 800x480 JPEG. Uploads through this channel are persisted on-device and
rendered at the next power-on.

The type byte at report offset +2 does not select distinct stores on the
tested firmware: every value probed (0x00 through 0x06, 2026-08-11, one
upload per power cycle) persisted and rendered as the power-on frame, last
upload wins. The earlier "blank boot screen" notes for targets 0x00-0x02
were an upload-content artifact (wrong chunk header), not type-specific
slots. `UploadBootImage` sends type 0x05; the resulting frame is what the
documented Show Logo command displays.

> ### Do not send feature report `0x03/0x0C` (factory channel)
> Report `0x03/0x0C` is a factory-only command channel: it executes
> length-prefixed UTF-16 command names, and sending it (e.g. the command
> `Open`) permanently de-provisions the unit - the stored serial is erased
> and cannot be restored by this library, the vendor app, or a power
> cycle. This library never sends it. See
> `docs/protocol-undocumented-config.md` for the full mapped config layer.

Feature-report setters such as Fill LCD (`0x03/0x05`) are reliable at any
normal client pacing, but sustained rapid-fire writes have a limit that was
measured on the real deck (2026-08-11). Verified clean: continuous fills for
60 seconds at every tested pace from 0.5/s to 5/s (up to 300 consecutive
fills), and 30 repetitions of a 10-fill burst at 100 ms cadence followed by
five seconds of idle (300 fills total). Failure observed: with writes issued
with no gap at all (~128 fills in five seconds), at 100 ms cadence (~280
fills), and in one of two 200-250 ms-cadence runs (~180 fills); the other
200 ms run stayed clean for 300 fills, so the threshold varies between runs.
Once the endpoint stops accepting writes, every further
`IOHIDDeviceSetReport` fails with an IOKit error - `0xE00002BC` (immediate,
device NAK/STALL) or `0xE00002D6` (five-second I/O timeout). Stopping the
writes lets the endpoint recover on its own: observed working again within
about three minutes, and in one early run within seconds; an unplug/replug
is the guaranteed recovery (verified). No permanent damage was seen in any
test. Practical guidance: keep continuous fill pacing at a few per second or
below and give the device idle gaps between animation bursts.

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
