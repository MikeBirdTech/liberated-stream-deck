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

Feature report `0x03/0x0C` is a FACTORY-ONLY command channel that executes
`[length-byte][UTF-16 command name]` payloads (e.g. `08 4f 00 70 00 65 00
6e 00` executes the command "Open"). Sending it de-provisions the unit:
the stored serial is erased and every identity path (USB string, getter
0x06) returns "Invalid SN! " permanently; a power cycle does not restore
it. There is NO known restore path: the vendor app contains no serial-write
command, the community has never documented serial provisioning, and no
firmware image that documents the write is publicly obtainable.

THIS REPORT MUST NEVER BE SENT BY THIS LIBRARY. The streamdeck library
does not expose it and never will. Any future protocol work must treat
feature report 0x03/0x0C as a one-way trip to a de-provisioned unit.

## Comm command vocabulary (UTF-16 session layer)

The 20 commands the app can build (length-prefixed UTF-16 names): Open,
SleepDog, ReadKeys, SetRGB, SetBacklight, UploadLogo, UploadXYWHImage,
Ping, UpdateFW, ShowLogo, UploadXIImage, Preheat, SetTriggerLevel,
SetLEDRGB, UploadContext, SetSleepDelay, Claim, SetFullRGB,
UploadFullImage, SetBSMode. Session status enum: DISCONNECTED, TIMED OUT,
FAILED, NOT SUPPORTED, NOT OPEN, SUCCESS. Warm-up sequence: Open -> Claim ->
Preheat -> Read FW -> Read capabilities -> Adjust capabilities -> Read
serial.

## Reproducibility

All wire facts above were re-verified on-device during this session
(output commands all issued as full 1024-byte frames; feature reports as
32-byte control writes). The 0x01/0x0B, 0x02/0x03-0x07 and session-control
pairs were probed with serial-region payloads; none altered the
de-provisioned serial state (see the FAULT REPORT above).
