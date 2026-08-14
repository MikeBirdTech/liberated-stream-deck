# Stream Deck Plus firmware-update research

This document records static analysis and direct hardware probes of the
firmware-update transport on Stream Deck Plus (`20GBD9901`, USB PID `0x0084`).
Every claim is labelled by evidence class so a successful USB write is not
mistaken for device-side acceptance of an update.

## Evidence status

| Claim | Evidence |
| --- | --- |
| `ESDCommUpdateFWTask` passes a file path to backend virtual slot `+0xC0` | Static: Stream Deck 7.4.2 x86_64 app slice |
| The `20GBD9901` backend opens the path read-only and reads the file verbatim | Static: same app slice |
| The backend emits full 1024-byte output reports with command `0x05` | Static: same app slice |
| The byte-level header and 4096/1008-byte chunking below | Static: same app slice |
| The host performs no container parsing, checksum, signature verification, bootloader-entry command, or recovery exchange in this backend method | Static: same app slice |
| The Plus accepts full-size command-`0x05` output reports at the HID layer | Observed on hardware: three fixed probes, 2026-08-14 |
| Empty/incomplete, empty/final, and short-invalid/final probes leave firmware getters and normal output operational | Observed on hardware: same run |
| A successful HID write means the update payload was accepted | **Not established**; the protocol has no host-visible acknowledgement in this path |

The fixed probes establish that the recovered command reaches the device and
that these invalid forms cause no visible update transition. They do not prove
that the device interpreted or retained their payload bytes.

## Reproducible static artifact

The trace used the locally installed universal macOS application:

```text
application: Stream Deck 7.4.2 (build 22730)
identifier:  com.elgato.StreamDeck
team ID:     Y93VXCB8Q5
executable SHA-256:
  ceccd0920b2364c7d94b3bd38cca2151f1982d13d994b7f75627549bdda21918
x86_64 thin-slice SHA-256:
  48c81931e10ac30216c56df2d08f76aa25cabe9dc5c149547451ae5dfa485d0a
```

The relevant x86_64 static trace is:

1. `ESDCommUpdateFWTask` is identified by RTTI string at file offset
   `0x17709C7`.
2. Its task method at `0xCBBB60` loads the hardware backend and calls virtual
   slot `+0xC0`, passing the task's path string.
3. The `17ESDHW20GBD9901Imp` RTTI string is at `0x1777E28`. Its vtable's
   `+0xC0` entry resolves to the method at `0xD7BB80`.
4. That method converts the path to a `QString`, opens a `QFile` read-only,
   calls `QIODevice::readAll`, and copies those bytes directly into the report
   payloads described below.
5. Each HID write requests exactly 1024 bytes. Any short write fails the
   method. No feature report or other output command appears before or after
   the report loop in this method.

One way to reproduce the slice and hashes is:

```bash
lipo '/Applications/Elgato Stream Deck.app/Contents/MacOS/Stream Deck' \
  -thin x86_64 -output /tmp/streamdeck-7.4.2-x86_64
shasum -a 256 '/Applications/Elgato Stream Deck.app/Contents/MacOS/Stream Deck'
shasum -a 256 /tmp/streamdeck-7.4.2-x86_64
strings -a -t x /tmp/streamdeck-7.4.2-x86_64 | \
  grep -E 'ESDCommUpdateFWTask|17ESDHW20GBD9901Imp'
```

`dyld_info -symbolic_fixups` resolves the RTTI/vtable relationships, and LLDB
can disassemble the two method offsets without launching the application.

## Static byte-level transport

The backend divides the input file into outer blocks of at most 4096 bytes.
Each outer block is divided again into report payloads of at most 1008 bytes.
Every interrupt output report is padded to 1024 bytes:

```text
offset  size  meaning
0x00    1     output report ID: 0x02
0x01    1     command: 0x05
0x02    1     outer 4096-byte block index, starting at 0
0x03    1     1 on the final report of this outer block, otherwise 0
0x04    1     1 on the final report of the entire file, otherwise 0
0x05    2     little-endian report index within the outer block
0x07    2     little-endian payload byte count, 1..1008
0x09    1     constant 0x02 (role unknown; statically a fixed target/type)
0x0A    6     zero/reserved
0x10    n     verbatim file bytes
0x10+n  ...   zero padding through byte 1023
```

A full 4096-byte outer block therefore uses five reports: four 1008-byte
payloads followed by one 64-byte payload. Non-final outer blocks must contain
exactly 4096 bytes. The final report sets both done flags.

The one-byte outer index gives the observed framing a maximum unambiguous
range of 256 outer blocks (1 MiB). The vendor method does not visibly reject a
larger input before truncating that index. This is another reason not to infer
a safe writer from the static code.

### What the framing does not contain

There is no address, total file size, checksum, version, model identifier,
signature, or rollback token in this 16-byte header. The host backend does not
parse a firmware container: it streams the selected file unchanged. Any image
format, integrity field, cryptographic authorization, flash addressing, or
bootloader state transition must therefore be inside the opaque payload or
implemented by device firmware. The fixed negative probes and lack of a
device-side acknowledgement do not establish those semantics.

## Direct hardware probes (2026-08-14)

The probes ran on the physical Plus at PID `0x0084`. Baseline getters were LD
`2.0.0.0` / `0x325db0b1`, AP1 `2.0.3.2` / `0x8fe07412`, and AP2 `2.0.3.7` /
`0x6aa3cbc4`; sleep was 0 seconds and unit dimensions were normal. The existing
controller daemon was stopped for exclusive HID access and restored after the
run.

Each probe was one full 1024-byte output report:

| probe | first 16 bytes | payload | report SHA-256 | result |
| --- | --- | --- | --- | --- |
| incomplete empty | `02 05 00 00 00 00 00 00 00 02 00 00 00 00 00 00` | none | `16307d511ef890acbebe9da31fec11f551d63b4df905ceea04401f87734ba7dd` | 1024-byte write accepted; getters unchanged |
| final empty | `02 05 00 01 01 00 00 00 00 02 00 00 00 00 00 00` | none | `b643850a287da99dfc5334ea72fed28a9b18ffecf294167ac1cef9cef2f27842` | 1024-byte write accepted; getters unchanged |
| final invalid marker | `02 05 00 01 01 00 00 20 00 02 00 00 00 00 00 00` | `LIBERATED-FW-PROBE-NOT-AN-IMAGE\n` | `dca6656aa7253de4b80f43c972a227ccc87467fce43b47046683f7ac719bb82d` | 1024-byte write accepted; getters unchanged |

The marker payload SHA-256 was
`ec46662895eacbc4a141c00ef080f75ec9ed77f2507faf78907f0102e9a11c86`.
There was no visible reset or loss of enumeration. The unit-info gallery bytes
changed from `0/0` to `136/137`, matching their previously observed volatile
behavior; firmware versions, checksums, identity state, dimensions, and sleep
state did not change. Known-good key-fill output succeeded after the first and
third probes. After the controller LaunchAgent was restored it reopened the
Plus and restored all eight keys, the strip, and brightness.

`deckboot` exposes only these three exact reports; there is no arbitrary
firmware payload option:

```bash
go run ./cmd/deckboot -fwprobe incomplete-empty
go run ./cmd/deckboot -fwprobe final-empty
go run ./cmd/deckboot -fwprobe final-marker
```

These are **negative hardware probes**. A return value of 1024 proves that the
macOS HID stack and device endpoint accepted the report. Because the vendor
method performs no acknowledgement read, it does not prove that application
firmware recognized command `0x05` or accepted an update.

## Offline capture inspection

`deckfwinspect` accepts a raw concatenation of 1024-byte reports. It performs
no USB discovery and has no HID write path:

```bash
go run ./cmd/deckfwinspect -capture plus-update-reports.bin
go run ./cmd/deckfwinspect \
  -capture plus-update-reports.bin \
  -expect-sha256 <known-payload-sha256> \
  -extract recovered-payload.bin
```

The parser rejects incorrect report IDs or commands, target changes, index
gaps, invalid done flags, incomplete non-final outer blocks, oversized or
short non-final payloads, nonzero reserved bytes, nonzero padding, truncated
reports, data after transfer completion, and captures without a final done
report. It reports a SHA-256 fingerprint over the exact reassembled payload.
That digest detects accidental mismatch against a separately recorded hash;
it is not a signature or evidence of device authorization.

The parser test fixture at
`internal/firmwarecapture/testdata/synthetic-final-report.json` encodes one
small, human-readable report. Larger tests cover the 4096-byte outer boundary
and every validation rule. Separate tests assert the exact bytes and hashes of
all three fixed hardware probes.

## Next hardware questions

Further work should vary one field at a time while recording getters and USB
enumeration around every write: wrong target byte, nonzero starting indexes,
inconsistent completion flags, and a two-report incomplete sequence. The
device's volatile status getters `0x0B` and `0x0C` should also be sampled before
and after each probe to look for a state-machine signal that the standard
firmware getters do not expose.
