# Undocumented report candidates and observed behavior

Source: static analysis of the official Elgato Stream Deck app for macOS
(version 7.4.2.22730 era, x86_64 slice) plus on-device verification on a
Stream Deck Plus (PID 0x0084, firmware 2.0.3.7). Static call-site evidence,
negative hardware probes and successfully observed device behavior are kept
separate below. Report ownership is model-specific: a host task's virtual call
can resolve to different commands on Mini and Plus.

Evidence terms used here:

- **Documented**: published by Elgato and covered by the library's report tests.
- **Observed**: produced the stated effect on the physical Plus.
- **Negative probe**: sent to the physical Plus but did not establish the
  proposed meaning.
- **Static only**: recovered from app disassembly; not validated on hardware.

## Observed and statically recovered output report forms

All output reports are 1024-byte interrupt frames beginning `[0x02][cmd]`.

| cmd | use | header layout | commit control | evidence |
| --- | --- | --- | --- | --- |
| 0x01 | Mini bulk key-image upload | `[02][01][idx][0x00][0x00][key 1..6][0x00 x10][data@0x10]`, chunks <=1008 | feature `[0x0B][0x63][0x02][key-1]` | static Mini backend mapping; fixed Plus target-1 probe changed only session gallery state |
| 0x01 | Mini single-key image upload | `[02][01][idx][0x00][done][key 1..6][0x00 x10][data@0x10]`, chunks <=1008 | - | documented Mini protocol and library tests |
| 0x02 | unclassified upload variant | `[02][02][type][done][data@+4]`, chunks <=1020 | feature `[0x03][0x07]` | static framing; negative probes only |
| 0x05 | Plus firmware file transport | `[02][05][outer][outer_done][file_done][inner u16][size u16][0x02][0x00 x6][data@0x10]`; 4096-byte outer blocks, chunks <=1008 | - | static Plus `20GBD9901` updater trace; three fixed negative probes accepted as full HID writes on hardware |
| 0x07 | key image | documented | - | documented and observed |
| 0x08 | full-screen LCD image | documented | - | documented and observed |
| 0x09 | power-on frame | `[02][09][type][done][idx@+4][size@+6][data@+8]` (reversed header) | - | observed, including persistence across power cycles |
| 0x0B | touch-strip image | documented | - | documented and observed |
| 0x0C | partial window image | documented | - | documented and observed |

The Plus `ESDCommUpdateFWTask` dispatches to model `20GBD9901`'s backend slot
`+0xC0`, which opens the selected file and sends it verbatim through the
4096-byte/command-0x05 uploader. On 2026-08-14 the physical Plus accepted three
fixed full-size reports at the HID layer: incomplete empty, final empty, and a
final 32-byte invalid marker. None caused a visible reset or firmware getter
change, and ordinary output remained operational. This is negative-probe
evidence rather than an acknowledgement of update semantics. See
`firmware-update-research.md` for exact bytes, hashes and before/after state.
Output command 0x02 and its `[0x03][0x07]` finalizer remain a separate
unclassified upload form. Command 0x01 belongs to the Mini backend, not the
Plus backend: model-independent upload tasks dispatch through virtual slots
that resolve to commands 0x09, 0x07, 0x08/0x0B and 0x0C on Plus. The two Mini
0x01 methods are also distinct: only the bulk form has the 0x0B/0x63 commit;
the documented per-key form uses the final-chunk byte. See
`output-command-01-research.md` for the address map and hardware results.

## Feature report map (from app call sites)

Documented and statically recovered forms (all 32-byte feature reports):

| report | purpose | evidence |
| --- | --- | --- |
| `[0x03][0x01][0x00...]` | unclassified control write; not used by Plus `Open` or `Claim` | static call site; negative probe only |
| `[0x03][0x02]` | Show Logo | documented and observed |
| `[0x03][0x05]` | Fill LCD | documented and observed |
| `[0x03][0x06][idx]` | Fill key | documented and observed |
| `[0x03][0x07][0x00...]` | paired finalizer for output-cmd-0x02 uploads | static pairing; negative probe only |
| `[0x03][0x08][brightness]` | brightness | documented and observed |
| `[0x03][0x0D][u32 seconds]` | sleep duration | documented and observed |
| `[0x0B][0x63][0x02][key-1]` | Mini bulk key-image commit | static Mini target mapping; direct Plus target-1 probe changed only `GalleryKeys` state |
| `[0x0B][0xA2][u32]` | unclassified 32-bit control form | static call site; negative probe only |
| `[0x0B][0x01][b1][b2][b3]` | unclassified three-byte control form | static call site; negative probe only |

Getters: 0x04 (FW LD), 0x05 (FW AP2), 0x06 (serial), 0x07 (FW AP1),
0x08 (unit info), 0x09 (boot-frame status), 0x0A (sleep duration),
0x0B/0x0C (device status; unstable/volatile). Feature report 0x00 fails.

## FAULT REPORT - the 0x03/0x0C factory channel

Feature report `0x03/0x0C` is a FACTORY-ONLY channel. During protocol
probing, the payload `08 4f 00 70 00 65 00 6e 00` (an attempted UTF-16
encoding of "Open") de-provisioned the unit: the stored serial was erased
and every identity path (USB string, getter 0x06) now returns "Invalid SN! "
permanently; a power cycle does not restore it. This observation proves that
the report is dangerous, but not that it is the app's transport for the host
task named `Open`. The app never sends this report in that task path.

There is NO known restore path: the vendor app contains no serial-write
command, the community has never documented serial provisioning, and no
firmware image that documents the write is publicly obtainable.

THIS REPORT MUST NEVER BE SENT BY THIS LIBRARY. The streamdeck library
does not expose it and never will. Any future protocol work must treat
feature report 0x03/0x0C as a one-way trip to a de-provisioned unit.

## Host task vocabulary (not a wire protocol)

An earlier static analysis mistook the 20 names built by the app for
length-prefixed UTF-16 commands. They are host-side task labels: Open,
SleepDog, ReadKeys, SetRGB, SetBacklight, UploadLogo,
UploadXYWHImage, Ping, UpdateFW, ShowLogo, UploadXIImage, Preheat,
SetTriggerLevel, SetLEDRGB, UploadContext, SetSleepDelay, Claim,
SetFullRGB, UploadFullImage and SetBSMode.

In the x86_64 app, the enum-to-name routine at file offset `0xcb4f40`
constructs a libc++ short `std::string`. For `Open`, the in-memory bytes begin
`08 4f 70 65 6e 00`: `0x08` is libc++ short-string metadata (the four-byte
length shifted left by one), followed by ordinary ASCII. It is neither a
wire buffer nor UTF-16. Its two callers at `0xcb644c` and `0xcb6bf5` pass the
result into task logging.

The task implementations dispatch directly to model-specific backend virtual
methods instead:

- `ESDCommOpenTask` calls backend slot `+0x30`. The HID implementation at
  `0xd7a7c0` enumerates/opens the HID device and sends no report.
- `ESDCommKeysClaimTask` calls backend slot `+0xd8`. Every inspected classic
  HID-derived model inherits the implementation at `0xd7aad0`, which returns
  `-7` (`NOT SUPPORTED`) and sends no report.

The strings DISCONNECTED, TIMED OUT, FAILED, NOT SUPPORTED, NOT OPEN and
SUCCESS are likewise host-side status formatting. The warm-up scheduler does
sequence Open, Claim, Preheat, firmware/capability reads and serial reads, but
that sequence is an application task graph rather than a single command
channel.

Consequently there is no Open/Claim wire handshake to add for the HID backend.
This library already owns the corresponding lifecycle by opening the HID
handle in `Open`, `OpenModel` or `OpenAny`. It must not attempt to reproduce
the scheduler by sending feature report `0x03/0x0C`.

## Reproducibility

The documented image and setting operations, plus the undocumented 0x09
power-on frame, produced their stated effects on the physical device. Earlier
0x01/0x0B, 0x02/0x03-0x07 and other unclassified control pairs did not alter
the de-provisioned serial state. A later fixed, valid-BMP 0x01/0x0B target-1
probe changed unit-info `GalleryKeys` from 0 to 32 across a HID close/reopen;
normal Plus image restoration returned it to 0. That is an observed state
change, but it does not validate a visible Plus target or durable store. See
`output-command-01-research.md` for exact bytes and hashes.
