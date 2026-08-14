# Undocumented persistent/config channels (reverse-engineered 2026-08-12)

Source: static analysis of the official Elgato Stream Deck app for macOS
(version 7.4.2.22730 era, x86_64 slice) plus on-device verification on a
Stream Deck Plus (PID 0x0084, firmware 2.0.3.7). This documents the
previously-unknown output and feature-report configuration layer that the
app uses for persistent storage, device settings, and session control.

## Output report command map (full)

All output reports are 1024-byte interrupt frames beginning `[0x02][cmd]`.

| cmd | use | header layout | commit control |
| --- | --- | --- | --- |
| 0x01 | persistent/config upload | `[02][01][idx][0x00][done][param][0x00 x8][data@0x10]`, chunks <=1008 | feature `[0x0B][0x63][0x02][param-1]` |
| 0x02 | persistent upload variant | `[02][02][type][done][data@+4]`, chunks <=1020 | feature `[0x03][0x07]` |
| 0x07 | key image | documented | - |
| 0x08 | full-screen LCD image | documented | - |
| 0x09 | power-on frame | `[02][09][type][done][idx@+4][size@+6][data@+8]` (reversed header) | - |
| 0x0B | touch-strip image | documented | - |
| 0x0C | partial window image | documented | - |

Output command 0x02 and its `[0x03][0x07]` commit, plus the 4096-byte
chunk uploader (uses 0x05/0x0A/0x0C variants), are the firmware-size
writers. The 0x01 channel's `param` byte selects a persistent target
(4 call sites in the app's send layer).

## Feature report map (from app call sites)

Confirmed setter forms used by the app (all 32-byte feature reports):

| report | purpose |
| --- | --- |
| `[0x03][0x01][0x00...]` | session opener/keepalive (undocumented) |
| `[0x03][0x02]` | Show Logo (documented) |
| `[0x03][0x05]` | Fill LCD (documented) |
| `[0x03][0x06][idx]` | Fill key (documented) |
| `[0x03][0x07][0x00...]` | commit for output-cmd-0x02 uploads (undocumented) |
| `[0x03][0x08][brightness]` | brightness (documented) |
| `[0x03][0x0D][u32 seconds]` | sleep duration (documented) |
| `[0x0B][0x63][0x02][param-1]` | commit for output-cmd-0x01 uploads (undocumented) |
| `[0x0B][0xA2][u32]` | session control, 32-bit value (undocumented) |
| `[0x0B][0x01][b1][b2][b3]` | session control, 3 bytes (undocumented) |

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

All wire facts above were re-verified on-device during this session
(output commands all issued as full 1024-byte frames; feature reports as
32-byte control writes). The 0x01/0x0B, 0x02/0x03-0x07 and session-control
pairs were probed with serial-region payloads; none altered the
de-provisioned serial state (see the FAULT REPORT above).
