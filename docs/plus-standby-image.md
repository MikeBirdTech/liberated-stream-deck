# Stream Deck Plus standby image

Issue #29 asked whether the Plus has a separate stored sleep/screensaver image.
The official app does not use one. It exposes two features with similar UI
wording but different implementations:

- **Standby screen** is the persistent 800x480 frame displayed while the deck
  is powered but the Stream Deck app is disconnected. It uses the existing
  output command `0x09` path.
- **Set Screensaver** selects host-side renderers (image, clock, fire, plasma,
  and others). Those render ordinary live frames while the app remains
  connected; they are not uploaded to a second firmware image slot.

## Vendor-app trace

The x86_64 Stream Deck 7.4.2 executable used for this trace has SHA-256
`48c81931e10ac30216c56df2d08f76aa25cabe9dc5c149547451ae5dfa485d0a`.

The advanced-preferences Qt metadata names the class
`ESDAdvancedPrefsDialog` and its three methods `accept`,
`onSetLogoButtonClicked`, and `onResetLogoButtonClicked`. The corresponding UI
text calls the feature "Standby screen" and describes it as the image shown
when the device is powered but disconnected from the app.

The two handlers are:

| Handler | Address | Trace |
|---|---:|---|
| Set Image | `0x1007A4A20` | opens an image, converts it to `QImage`, calls `0x1007A7E00` |
| Reset | `0x1007A51A0` | loads the built-in image, calls the same `0x1007A7E00` |

`0x1007A7E00` schedules the image after 500 ms. Its callback at
`0x1007AB440` passes the `QImage` to `0x100CB13E0`, which queues the vendor
full-image upload task. On the Plus backend that task uses vtable slot `+0x88`,
the method at `0x100D7B3F0` that builds output command `0x09`. This is the same
path already mapped and exercised as the persistent power-on frame. The task's
single-full-frame branch passes target `0`; its alternate per-key branch maps
each logical index through backend slot `+0x108`.

The sleep UI is separate. The binary contains `ESDImageScreenSaver::renderNext`
with a `QImage` callback, clock and effect renderer classes, the host preference
`map_dev_sleep_wallpaper_path`, and the URL scheme `wallpaper://image/`. No
second persistent-image upload follows from that path.

## Wire format and persistence

`SetStandbyImage` accepts an exact 800x480 image on the Plus API, JPEG-encodes
it, and sends 1024-byte output reports:

| Offset | Value |
|---:|---|
| `+0` | output report ID `0x02` |
| `+1` | command `0x09` |
| `+2` | target/type `0x05` |
| `+3` | `0x01` on the final report, otherwise `0x00` |
| `+4..5` | zero-based chunk index, little-endian |
| `+6..7` | payload byte count, little-endian |
| `+8..1023` | JPEG payload, zero-padded |

The device persists the last complete upload in flash. It is rendered after a
power cycle and by feature report `03 02` (`ShowLogo`). Earlier hardware tests
also established that target values `0x00` through `0x06` all select this same
store on the tested Plus firmware. This library retains its already
hardware-validated target `0x05`; the target byte is not a distinct slot on
that firmware.

There is no standby-specific wake packet. While connected, the controller
replaces the standby frame with normal key/LCD/touch output. Sleep screensavers
are likewise live controller output. Consequently this work adds no guessed
"wake" command and performs no command sweep.

## Fixed hardware validation

The new API was exercised against the connected Stream Deck Plus on
2026-08-14. Before the write, the fixed getters reported:

```text
PID       0x0084
LD        2.0.0.0 / 0x325db0b1
AP1       2.0.3.2 / 0x8fe07412
AP2       2.0.3.7 / 0x6aa3cbc4
serial    Invalid SN! (pre-existing incident)
sleep     0 seconds
LCD       800x480
```

With the normal controller temporarily stopped, `deckboot -color 2f6fed`
created one exact 800x480 frame and called `SetStandbyImage`. The complete
command-`0x09` report sequence returned successfully. `deckboot -logo` then
sent the fixed `03 02` feature report successfully. No target or command bytes
were swept.

The normal controller was immediately restarted. It reconnected, logged
`boot image persisted revision=1 source=800x480`, and restored the live key and
touch-strip output, replacing the temporary validation frame with the
controller-owned standby image. This run did not repeat the earlier physical
power-cycle test; persistence across power-off was already established by the
fixed command-`0x09` hardware work summarized above.

## Public API boundary

`SetStandbyImage` is a method on `*Deck`, the concrete Stream Deck Plus type,
so unsupported models cannot call it through the common `Device` API. It
rejects nil and non-800x480 images before any HID write. The older
`UploadBootImage` name is retained for source compatibility and continues to
scale arbitrary input to 800x480 before using the same uploader.
