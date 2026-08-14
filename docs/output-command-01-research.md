# Output command 0x01 model mapping

Output command `0x01` is not a Stream Deck Plus upload primitive in the
official application. The two implementations that build `0x01` reports
belong to `ESDHWCommHIDMiniImp`. The corresponding virtual methods in the
Plus `20GBD9901` backend build commands `0x09`, `0x07`, `0x08`/`0x0B`, and
`0x0C` instead.

This distinction matters because the original issue combined two different
Mini report forms and treated model-independent task calls as four direct
Plus call sites. There is no supported Plus target API to add from that static
evidence.

## Evidence source

The addresses below are from the x86_64 slice of Stream Deck
7.4.2.22730:

```text
SHA-256  48c81931e10ac30216c56df2d08f76aa25cabe9dc5c149547451ae5dfa485d0a
__text   VM 0x10000f100, file offset 0x0000f100
```

Relevant RTTI names are at file offsets `0x17780C8`
(`19ESDHWCommHIDMiniImp`) and `0x1777E28` (`17ESDHW20GBD9901Imp`).
The four relevant caller methods dispatch through backend virtual slots; they
do not call a raw `0x01` writer directly:

| caller method | backend slot(s) | Mini implementation | Plus implementation |
| --- | --- | --- | --- |
| `ESDCommBragiEventPolling` at `0x100D77030` | `+0x90`, alternate `+0x98` path | `0x01` done-marker upload; `+0x98` returns `-7` (not supported) | `0x07`; alternate selects `0x08` or `0x0B` |
| `ESDCommUploadFullImageTask` at `0x100D77890` | `+0x88` | `0x01` upload plus `0B 63` commit | `0x09` |
| `ESDCommUploadLogoTask` at `0x100D77E90` | `+0x90` | `0x01` done-marker upload | `0x07` |
| `ESDCommUploadXIconTask` at `0x100D78460` | `+0xA0` | returns `-7` (not supported) | `0x0C` |

The separately named `ESDCommUploadContextTask` is not one of these callers.
Its method at `0x100CBBC70` dispatches backend slot `+0x40`, outside the image
upload slots above.

The matching backend entries are:

| slot | Mini method | Plus method |
| --- | --- | --- |
| `+0x88` | `0x100D7EC10` (`0x01` plus commit) | `0x100D7B3F0` (`0x09`) |
| `+0x90` | `0x100D7EDE0` (`0x01` with final marker) | `0x100D7B570` (`0x07`) |
| `+0x98` | `0x100D7EF70` (returns `-7`) | `0x100D7B700` (`0x08` or `0x0B`) |
| `+0xA0` | `0x100D7EF80` (returns `-7`) | `0x100D7B8B0` (`0x0C`) |

## The two Mini forms

The commit form at `0x100D7EC10` sends 1024-byte reports with 1008-byte
payload chunks:

```text
02 01 [chunk index] 00 00 [target] 00 00 00 00 00 00 00 00 00 00
[payload at +0x10]
```

Byte `+4` remains zero in every chunk. After the last output report, the app
waits 100 ms and sends this 32-byte feature report:

```text
0b 63 02 [target - 1] 00 ...
```

`ESDCommUploadFullImageTask` obtains the target through backend slot `+0x108`.
The Mini implementation at `0x100D7F420` bounds the logical key index to
`0..5`, optionally reverses it for the device orientation, and maps it through
the table `[1, 2, 3, 4, 5, 6]` at file offset `0x17780B0`. The statically
supported meaning of this form is therefore a one-based physical Mini key
target, with the feature report selecting the corresponding zero-based key.

The separate done-marker form at `0x100D7EDE0` uses the same header except
that byte `+4` is `1` in the final chunk. It sends no `0B 63` feature report.
This is the form implemented by `Mini.SetKeyImage`: its public zero-based key
index is checked against `0..5` and encoded on the wire as `index + 1`.

## Controlled Plus hardware probe

Static dispatch proves that the vendor app does not use either Mini `0x01`
method for the Plus, but it does not prove that Plus firmware rejects the
bytes. On 2026-08-14, the commit form was therefore sent twice to the physical
Plus as one fixed, statically justified target. No parameters were swept.

Device baseline:

```text
PID       0x0084
LD        2.0.0.0 / 0x325db0b1
AP1       2.0.3.2 / 0x8fe07412
AP2       2.0.3.7 / 0x6aa3cbc4
serial    Invalid SN! (pre-existing incident)
sleep     0 seconds
```

Probe payload and reports:

```text
target                  1
payload                 valid 80x80 24-bit BMP, 19,254 bytes
payload SHA-256         274397d693f10abc8a0a87aab08c67b31bbe7e9adbf111bfcb2ea4eac2844f5f
output reports          20 x 1024-byte writes
payload per report      1008 bytes; final report 102 bytes plus zero padding
output header           02 01 [index] 00 00 01 00 00 00 00 00 00 00 00 00 00
commit report           0b 63 02 00 followed by zero padding to 32 bytes
commit SHA-256          2efba2054d8b6b38c773bd12a867c993ed57d4127228168f9f95bf2e89d3ecc6
```

Every output and feature write returned its complete byte count on both runs.
The firmware, serial, and sleep getter responses were byte-for-byte unchanged.
The unit-info response changed only at byte `0x0D`, exposed by this library as
`GalleryKeys`, from `0` to `32`. The same `32` survived closing the probing HID
handle and reopening the device in a separate process. A known Plus Fill Key
feature command also completed normally and did not clear the value. Starting
the normal controller, which restores the Plus image outputs, returned
`GalleryKeys` to `0`.

This is evidence of a device-held gallery-state side effect, not evidence that
target `1` is a usable Plus key-image store. There was no payload read-back or
validated visible mapping, normal Plus output superseded the state, and no
power-cycle persistence claim can be made. The library consequently exposes
no Plus `0x01` writer or target-specific API.

## Implementation decision

- Keep `Mini.SetKeyImage` on the documented Mini done-marker form.
- Do not append the `0B 63` finalizer to ordinary Mini key uploads.
- Do not expose either `0x01` form on `Deck` (Stream Deck Plus).
- Treat `GalleryKeys: 0 -> 32` as an observed but unclassified Plus side
  effect until a vendor capture or read-back path establishes semantics.
