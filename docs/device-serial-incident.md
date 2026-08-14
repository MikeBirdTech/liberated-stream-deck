# Device incident: serial de-provisioning (2026-08-11) - state and recovery log

## Summary

During reverse-engineering of the undocumented comm layer, a probe sent
feature report `0x03/0x0C` with the payload
`08 4f 00 70 00 65 00 6e 00`, an attempted UTF-16 encoding of the host task
name "Open", to the Stream Deck Plus (PID 0x0084, serial originally
`A00WA3361NFL4P`, firmware AP2 2.0.3.7). The unit de-provisioned: every
identity path (USB string, getter 0x06) now returns `Invalid SN! `
permanently. Power cycles, including a 60-second full drain, do not restore
it.

Later static analysis disproved the premise behind that probe. The app's
apparent `[length][name]` value is libc++ short-string object storage used for
task logging, not a UTF-16 wire command. The destructive device result remains
real; only the original interpretation of the app-side bytes was wrong.

**This was caused by this project's own probing. The library must never
send feature report 0x03/0x0C.** See `protocol-undocumented-config.md`.

## Current device state (verified 2026-08-12)

- Serial: `Invalid SN! ` (USB string + getter 0x06). Permanent.
- Firmware: LD 2.0.0.0 / AP1 2.0.3.2 / AP2 2.0.3.7 - intact.
- Sleep duration 0 (disabled - Mike's preference), unit info normal
  (matrix 2x4, keys 120x120, LCD 800x480). Gallery reads 0/0 or 216/252
  inconsistently across boots (volatile/uninitialized fields).
- The deck is fully functional: keys, strip, dials, input, brightness,
  boot-frame uploads, partial windows, fills - all verified working.

## Exhaustively tried (all logged; do not repeat blindly)

1. Feature-report writes on every ID 0x00-0x10 and 0x03/0x01-0x0F with
   serial payloads: u8/u16 lengths, raw ASCII, UTF-16, every offset,
   with and without command bytes.
2. The undocumented factory report 0x03/0x0C: 400+ candidate command-name
   probes (`[len][UTF-16 name]`); names with inline ASCII serial args;
   Open->write->Close sequences; Open followed by an immediate sweep of every
   write form. These were based on the now-disproved wire-format inference and
   must not be repeated.
3. Output commands 0x01 (param 0-16), 0x02 (type 0x00-0x1F, 0xFE, 0xFF),
   0x05, 0x09 (types 0x00-0x10), 0x0A with serial payloads, including
   the app's commit controls ([0x0B][0x63][0x02][param-1], [0x03][0x07]).
4. Session-control forms [0x0B][0x01][b1..b3], [0x0B][0xA2][u32].
5. Writes directly to getter IDs (0x04-0x0A).
6. Read paths: getters 0x00-0x7F, windowed reads (0x09/0x0B address
   writes), factory read-command names with long input listening - the
   device has NO flash read-back over USB.
7. Vendor software: mac apps 7.4.2 and 7.5.1 (static analysis + runtime
   network capture), Windows app 7.4.2 (extracted from the official
   MSI): no serial-write code exists in any of them, no embedded
   firmware (the `:/firmwares/%1.bin` resource path is dead in all
   builds), no device-firmware endpoint is ever contacted, and the
   installed app generation has no device-firmware update UI.
8. 20.x/24.x CDN artifacts are Node runtimes, not the app.
9. Web/community: no serial provisioning documentation exists anywhere;
   community libraries only read serials; no Plus bootloader entry.

## Conclusion

The serial write exists only inside the deck's factory firmware,
reachable by Elgato's production tooling (not public). It may be
signature-gated. The firmware image itself is not embedded, cached,
downloadable (the update backend only serves it when Elgato's server
decides an update is warranted), archived publicly, or readable back
from the device over USB.

## Open doors (if any of these ever materialize)

- A Stream Deck Plus firmware image (owner dump, leak, or Elgato release):
  disassemble it to recover the exact provisioning write.
- A factory-tool leak.
- Physical MCU flash dump via SWD/JTAG (requires opening the unit; the
  flash may be read-protected).

## Mike's preferences on this deck

- Sleep stays disabled (0). Boot frame is controller-owned in bridge
  mode. The daemon is `com.mikebirdtech.liberated-stream-deck`
  (bin/deckdemo, bridge mode via LIBERATED_STREAM_DECK_CONTROLLER).
