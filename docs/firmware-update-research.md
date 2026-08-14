# Stream Deck Plus firmware-update research

This document records the firmware-update path for Stream Deck Plus
(`20GBD9901`, USB PID `0x0084`) without turning the library into a firmware
writer. Every claim is labelled by evidence class. Static framing is useful
for inspecting a future capture, but it is not proof that a device accepts a
payload or that interruption is recoverable.

## Evidence status

| Claim | Evidence |
| --- | --- |
| `ESDCommUpdateFWTask` passes a file path to backend virtual slot `+0xC0` | Static: Stream Deck 7.4.2 x86_64 app slice |
| The `20GBD9901` backend opens the path read-only and reads the file verbatim | Static: same app slice |
| The backend emits full 1024-byte output reports with command `0x05` | Static: same app slice |
| The byte-level header and 4096/1008-byte chunking below | Static: same app slice |
| The host performs no container parsing, checksum, signature verification, bootloader-entry command, or recovery exchange in this backend method | Static: same app slice |
| A genuine Plus firmware payload has this transport on the wire | **Not captured** |
| The Plus accepts, authenticates, installs, or rolls back a payload | **Not established** |
| Bootloader USB identity, failure behavior, and recovery procedure | **Not established** |

The repository contains a synthetic parser fixture only. It is deliberately
labelled synthetic and must never be cited as a device capture. A genuine,
provenance-preserving firmware artifact or vendor-app capture is still needed
before issue #25's hardware acceptance criteria are complete.

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
implemented by device firmware. None is established without a real artifact
or capture.

Output command `0x05` must remain classified as **static-only firmware
transport**. It has not been sent by this repository and is not exposed by the
library.

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

The test fixture at
`internal/firmwarecapture/testdata/synthetic-final-report.json` encodes one
small, human-readable report. Larger tests cover the 4096-byte outer boundary
and every validation rule without containing or generating a device writer.

## Artifact and capture handling

When a real artifact or capture becomes available:

1. Preserve the original bytes read-only. Record source, acquisition date,
   model, current and offered firmware versions, app build, VID/PID, USB
   descriptors, and SHA-256 before analysis.
2. Keep the raw HID capture, extracted `0x05` reports, and reassembled payload
   as separate files with hashes. Do not normalize padding or edit the source.
3. Run `deckfwinspect` and compare the reassembled hash with any independently
   captured file hash. A mismatch blocks all later work.
4. Analyze the payload offline for magic, section table, load addresses,
   version/model binding, compression/encryption, checksums, and signatures.
   Record unknown fields as unknown; do not infer them from a single sample.
5. Obtain at least two versions or an independent device dump before treating
   changing fields as checksums, addresses, counters, or signatures.

## Controlled hardware validation and recovery gates

No end-to-end write should occur until every gate below is satisfied:

1. **Compatibility gate:** genuine payload provenance, exact `20GBD9901` /
   PID `0x0084` match, known current and target versions, offline structural
   parse, and independently recorded hashes.
2. **Passive bootloader gate:** observe the vendor updater first. Record
   whether the device re-enumerates, its bootloader VID/PID and descriptors,
   the command that causes the transition, timing, acknowledgements, and the
   post-update version getters. Do not guess an entry command.
3. **Recovery gate:** have a known-good image, a proven way to reach the
   bootloader without working application firmware, and a tested restore path.
   If recovery requires SWD/JTAG, confirm the pads, voltage, MCU, readout
   protection, and a successful non-destructive connection before USB writes.
4. **Isolation gate:** use a sacrificial or explicitly recoverable Plus on a
   powered, logged USB path. Disconnect unrelated Stream Deck devices and stop
   all other HID clients.
5. **Execution gate:** implement only a model- and version-locked replay of a
   validated capture, with full-size-write checks and immutable input hashes.
   A generic command writer or parameter sweep is out of scope.
6. **Failure gate:** do not test cable pulls, corrupt chunks, replay, downgrade,
   or power loss until the clean update and recovery path have each succeeded
   more than once.

Until those gates are met, the safe deliverable is the static map and offline
parser—not a device-writing API.
