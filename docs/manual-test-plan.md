# Stream Deck physical-testing runbook

Use this runbook against one committed revision. Run commands from the
repository root unless a step says otherwise. Do not run `deckdemo` with
`sudo`; permission failures are part of the Linux test.

Record the revision before each platform session:

```bash
git rev-parse HEAD
```

In expected log lines below, angle-bracketed text such as `<USB product>` or
`<error>` is variable. All other text should match exactly. A positive dial
delta can be greater than `+1`, and a negative delta can be less than `-1`, if
the firmware combines fast turns into one report.

## 1. Pre-flight

### macOS device and process checks

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PRE-MAC-1 | Open Apple menu **About This Mac → More Info → System Report**, select **USB**, then select the Stream Deck. | Vendor ID is `0x0fd9`. Plus Product ID is `0x0084`; original Mini Product ID is `0x0063`. Record the displayed product and serial. | The USB-tree label can vary. Any other PID is a stop condition: record it and do not treat a protocol failure as a test result. A 2022 Mini may report `0x0090`, which this revision does not enable. |
| PRE-MAC-2 | In the Stream Deck menu-bar application choose **Quit Stream Deck**, or focus it and press Command-Q. Then run `pgrep -ifl 'Stream Deck'`. | `pgrep` prints nothing and exits with status 1, confirming no Stream Deck application process remains. | A Finder path or unrelated text containing the phrase is acceptable after inspection. A running Elgato Stream Deck process is not acceptable because it can consume input reports. |
| PRE-MAC-3 | From a normal Terminal window, run `CGO_ENABLED=1 go build ./...`. | Command exits 0 with no output. | Compiler progress from an empty cache is acceptable. `operation not permitted`, open failures in a sandbox, or Apple framework/compiler errors mean the environment is not ready; move to an unrestricted Terminal or install Command Line Tools before testing. |

### Raspberry Pi device and udev checks

Perform these after installing the rule as described in the README.

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PRE-PI-1 | Run `uname -m`, `go version`, and `go env GOOS GOARCH CGO_ENABLED`. | Architecture is `aarch64`; Go is 1.24 or later; Go values are `linux`, `arm64`, and `1`. | `arm64` from `uname` is an acceptable synonym on some distributions. `armv7l`, Go older than 1.24, or `CGO_ENABLED=0` is a stop condition for this run. |
| PRE-PI-2 | Connect the deck and run `lsusb -d 0fd9:`. | Mini line contains ID `0fd9:0063`; Plus line contains `0fd9:0084`. | Manufacturer/product wording can differ. No line means a USB/cable/power problem. Another PID must be reported before testing. |
| PRE-PI-3 | Find the Elgato hidraw node: run `for node in /dev/hidraw*; do udevadm info --attribute-walk --name="$node" 2>/dev/null | grep -q 'ATTRS{idVendor}=="0fd9"' && echo "$node"; done`. Use the resulting `/dev/hidrawX` in later commands. | At least one `/dev/hidrawX` path prints. | Multiple paths can print if multiple Elgato HID interfaces/devices exist; inspect each in the next step. No path means hidraw is unavailable or the device did not enumerate. |
| PRE-PI-4 | Run `udevadm info --attribute-walk --name=/dev/hidrawX | grep -E 'idVendor|idProduct'`, substituting the path found above. | The same parent block contains `ATTRS{idVendor}=="0fd9"` and `ATTRS{idProduct}=="0063"` for Mini or `"0084"` for Plus. | Repeated parent attributes are normal. A different product block means the wrong hidraw node was selected. |
| PRE-PI-5 | Run `sudo udevadm test "$(udevadm info -q path -n /dev/hidrawX)" 2>&1 | grep -E '50-streamdeck.rules|plugdev|uaccess'`. | Output names `50-streamdeck.rules` and shows the `plugdev`/`uaccess` assignment. | `udevadm test` may print unrelated warnings. No matching rule output means reinstall the rule, reload udev, and reconnect the device. |
| PRE-PI-6 | Run `id`, `ls -l /dev/hidrawX`, and, if available, `getfacl /dev/hidrawX`. | User is in `plugdev`; node is group `plugdev` with group read/write (`crw-rw----`), or the active user has an ACL granting `rw-`. | A trailing `+` in `ls -l` indicates ACLs and is acceptable. Root-only access, no `rw`, or group membership not visible in `id` is a failure; log out/in and reconnect after fixing it. |

## 2. macOS + Plus regression checklist

Start from an unrestricted Terminal with the Elgato application fully quit.
Capture the session:

```bash
mkdir -p test-logs
go run ./cmd/deckdemo 2>&1 | tee test-logs/macos-plus.log
```

### Connection, restoration, and first input

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PLUS-CONNECT | Launch the command with the Plus connected. | Logs `device connected vid=0fd9 pid=0084 product="<USB product>" model="Stream Deck Plus"`, then `output restored keys=1-8 strip=800x100 brightness=70`. All eight keys and the strip display the demo. | USB product text may vary. Repeated `device unavailable`, wrong model/PID, setup failure, missing key/strip image, or rotated/mirrored output is a failure. |
| PLUS-FIRST | Immediately after `output restored`, before touching anything else, press and release key 1, then press and release dial 1. | First actions are not swallowed. Logs, in order per control: `input key key=1 pressed=true`, `input key key=1 pressed=false`, `input dial dial=1 pressed=true`, `input dial dial=1 pressed=false`. | Interleaving is acceptable only if actions overlap. A missing first `pressed=true`, a release without its press, or input appearing only after a second attempt is a failure. |

### Eight LCD keys

For each row, press once, observe the key toggle, then release.

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PLUS-K1 | Press/release key 1. | `input key key=1 pressed=true` then `input key key=1 pressed=false`; key 1 changes ON/OFF. | Log timing may precede redraw slightly. Missing/duplicate transition or another key changing is a failure. |
| PLUS-K2 | Press/release key 2. | `input key key=2 pressed=true` then `input key key=2 pressed=false`; key 2 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K3 | Press/release key 3. | `input key key=3 pressed=true` then `input key key=3 pressed=false`; key 3 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K4 | Press/release key 4. | `input key key=4 pressed=true` then `input key key=4 pressed=false`; key 4 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K5 | Press/release key 5. | `input key key=5 pressed=true` then `input key key=5 pressed=false`; key 5 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K6 | Press/release key 6. | `input key key=6 pressed=true` then `input key key=6 pressed=false`; key 6 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K7 | Press/release key 7. | `input key key=7 pressed=true` then `input key key=7 pressed=false`; key 7 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |
| PLUS-K8 | Press/release key 8. | `input key key=8 pressed=true` then `input key key=8 pressed=false`; key 8 changes ON/OFF. | Same variation as PLUS-K1; wrong index is a failure. |

### Four dial rotations and buttons

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PLUS-D1-ROTATE | Turn dial 1 one detent clockwise, then counter-clockwise. | `input dial dial=1 delta=+1` then `input dial dial=1 delta=-1`; strip counter changes and returns. | Magnitudes such as `+2`/`-2` are acceptable for a fast turn. Zero, wrong sign/index, or no counter redraw is a failure. |
| PLUS-D2-ROTATE | Turn dial 2 one detent clockwise, then counter-clockwise. | `input dial dial=2 delta=+1` then `input dial dial=2 delta=-1`; brightness rises five points then returns. | Coalesced magnitude is acceptable and changes brightness by five times that magnitude. No visible brightness change or wrong index/sign is a failure. |
| PLUS-D3-ROTATE | Turn dial 3 clockwise, then counter-clockwise. | `input dial dial=3 delta=+1` then `input dial dial=3 delta=-1`; the selected-key border advances then returns, wrapping at ends. | Coalesced deltas may skip multiple keys consistently. Wrong direction/index or stale borders is a failure. |
| PLUS-D4-ROTATE | Turn dial 4 clockwise through the views and back. | Each detent logs `input dial dial=4 delta=+1` or `delta=-1`; strip cycles INPUT, KEY TEST, TOUCH TEST and wraps. | Coalesced deltas can skip a view consistently. A frozen/corrupt strip or dial output on another index is a failure. |
| PLUS-D1-PRESS | Press/release dial 1 after making the counter nonzero. | `input dial dial=1 pressed=true`, then `... pressed=false`; counter resets to zero. | Redraw can occur before release. Missing transition or nonzero counter is a failure. |
| PLUS-D2-PRESS | Press/release dial 2 twice. | Each press/release logs dial 2. First press changes brightness to 15%; second changes it to 70%. | If starting brightness was changed, first press still selects 15 unless it is already 15. Missing visible change or incorrect toggle is a failure. |
| PLUS-D3-PRESS | Select a key with dial 3, then press/release dial 3. | `input dial dial=3 pressed=true`, then `... pressed=false`; selected key toggles ON/OFF. | Redraw can precede release. Another key toggling or no toggle is a failure. |
| PLUS-D4-PRESS | Select KEY TEST or TOUCH TEST, then press/release dial 4. | `input dial dial=4 pressed=true`, then `... pressed=false`; strip returns to INPUT. | Redraw can precede release. Remaining in the prior view is a failure. |

### Touch strip and runtime behavior

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PLUS-TAP | Briefly tap near the middle of the strip. | `input touch kind=TAP x=<0-799> y=<0-99>` and a TAP visualization near the contact. | Coordinates need not equal finger position exactly. Out-of-range coordinates, PRESS/FLICK for a clean tap, or no event is a failure. |
| PLUS-PRESS | Press and hold near the middle until a PRESS is emitted, then release. | `input touch kind=PRESS x=<0-799> y=<0-99>` and a PRESS visualization. | Firmware timing can vary. TAP before the hold is acceptable only if followed by PRESS; no PRESS after a deliberate hold is a failure. |
| PLUS-FLICK | Make a clear left-to-right flick, then a right-to-left flick. | Lines such as `input touch kind=FLICK start=<x1>,<y1> end=<x2>,<y2>`; first has `x2>x1`, second `x2<x1`. | Coordinate endpoints vary. Reversed directions, out-of-range points, or no FLICK is a failure. |
| PLUS-BRIGHTNESS | Starting at 70, rotate dial 2 down repeatedly to 0 and up repeatedly to 100. | Each detent logs dial 2; visible brightness changes in 5% steps, clamps at 0 and 100, and never wraps. | Coalesced deltas can move multiple 5% steps. Values outside 0..100, wraparound, or HID write errors are failures. |
| PLUS-REPLUG | With the demo running, toggle several keys and set non-default brightness. Unplug USB, wait for retries, then reconnect. | On unplug: `device disconnected retry=1s error="<USB/HID error>"`; while absent, `device unavailable retry=1s error="<open error>"`; after reconnect, the two PLUS-CONNECT lines recur with the saved brightness and prior key/strip state restored. New input works. | Exact OS error text and number of retry lines vary. Process exit, lost state, partial output, no reconnect, or swallowed first post-reconnect press is a failure. |
| PLUS-CTRL-C | Press Control-C once while connected. Repeat once in a separate run while the deck is absent/retrying. | Each run logs `device shutdown reason=interrupt` once and exits successfully without a panic. | Shell `signal: interrupt` text from `go run` can appear after the app line. Hanging, duplicate close errors, panic, or requiring a second Control-C is a failure. |

## 3. macOS + Mini checklist

Confirm PID `0x0063` in System Information before treating this as a supported
Mini test. Capture the session:

```bash
mkdir -p test-logs
go run ./cmd/deckdemo 2>&1 | tee test-logs/macos-mini.log
```

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| MINI-MAC-CONNECT | Launch with the Mini connected. | `device connected vid=0fd9 pid=0063 product="<USB product>" model="Stream Deck Mini"`, then `output restored keys=1-6 key-size=80x80 brightness=70`. Six keys show upright, readable labels with no crop or mirroring. | Product text may vary. A `0090` PID is a report-back condition, not a protocol failure. JPEG-looking corruption, rotated labels, wrong key assignment, or any strip setup attempt is a failure. |
| MINI-MAC-FIRST | Immediately after restore, press/release key 1 once. | First press is delivered: `input key key=1 pressed=true`, then `input key key=1 pressed=false`; key 1 toggles. | A missing first press or a response only on the second press is a failure. |
| MINI-MAC-K1 | Press/release key 1. | Key 1 toggles; exact true/false key-1 lines appear. | Wrong key, duplicate/missing transition, or no image update is a failure. |
| MINI-MAC-K2 | Press/release key 2. | Key 2 toggles; `input key key=2 pressed=true` then `... false`. | Same allowed timing variation; wrong index is a failure. |
| MINI-MAC-K3 | Press/release key 3. | Key 3 toggles; `input key key=3 pressed=true` then `... false`. | Same allowed timing variation; wrong index is a failure. |
| MINI-MAC-K4 | Press/release key 4. | Key 4 toggles; `input key key=4 pressed=true` then `... false`. | Same allowed timing variation; wrong index is a failure. |
| MINI-MAC-K5 | Press/release key 5. | Key 5 toggles; key-5 true/false lines appear; on press `output brightness=<10 lower, minimum 0>` also appears. | Brightness log occurs between press and release. Wrong key or an increase is a failure. |
| MINI-MAC-K6 | Press/release key 6. | Key 6 toggles; key-6 true/false lines appear; on press `output brightness=<10 higher, maximum 100>` also appears. | Brightness log occurs between press and release. Wrong key or a decrease is a failure. |
| MINI-MAC-IMAGES | Inspect every key after toggling it both ON and OFF. | Every image fills one 80x80 key cell; `KEY N` and ON/OFF are upright, centered, readable, correctly indexed, and not mirrored. | Minor LCD color/contrast differences are acceptable. Rotation, mirror, crop, tearing, cross-key placement, or stale partial BMP data is a failure. |
| MINI-MAC-BRIGHTNESS | Press key 5 repeatedly until the log reaches `output brightness=0`, press it once more, then press key 6 until `output brightness=100` and once more. | Brightness changes in 10% steps, visibly dims/brightens, remains at 0/100 on the extra presses, and logs the clamped value. | LCD may not become completely black at 0 depending on hardware. Wraparound, values outside the bounds, no visible change, or feature-write errors are failures. |
| MINI-MAC-NO-PLUS | Let the demo run for 30 seconds while pressing Mini keys. Search the captured log with `grep -E 'input (dial|touch)|strip=' test-logs/macos-mini.log`. | `grep` prints nothing. No dial/touch events and no strip restoration occur. | No variation: matching lines indicate incorrect model routing. |
| MINI-MAC-REPLUG | Set non-default keys/brightness, unplug, wait, reconnect, then press a key once. | Disconnect/retry lines appear as in PLUS-REPLUG; Mini connect/restore lines recur; all six key states and brightness return; the first post-reconnect press works. | OS error wording varies. Exit, failed reconnect, lost state, wrong image orientation, or swallowed input is a failure. |
| MINI-MAC-CTRL-C | Press Control-C once while connected. | `device shutdown reason=interrupt`; clean exit with no panic. | `go run` may add `signal: interrupt`. Hanging or requiring another signal is a failure. |

## 4. Raspberry Pi + Mini checklist

### Fresh-environment build

Use a fresh 64-bit Raspberry Pi OS installation or record why the environment
is not fresh.

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| PI-BUILD-DEPS | Run `sudo apt update` and `sudo apt install -y gcc libudev-dev git usbutils`. | Commands complete successfully; packages are installed. | Already-installed packages are acceptable. Missing repositories, package errors, or a 32-bit-only environment must be fixed before continuing. |
| PI-BUILD-GO | Install official Go 1.24+ `linux-arm64`, then run the PRE-PI-1 commands. | Go reports `linux/arm64`, version 1.24+, CGO enabled. | A newer Go release is acceptable. Wrong architecture or disabled CGO is a failure. |
| PI-BUILD-CLONE | Run `git clone https://github.com/MikeBirdTech/liberated-stream-deck.git`, `cd liberated-stream-deck`, check out the revision under test, then run `CGO_ENABLED=1 go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...`. | All four commands exit 0. The first build links against `libudev` without custom tags. | Download/compiler progress is acceptable. Missing `-ludev`, C compiler errors, architecture errors, test failure, vet output, or build failure is a failure. Preserve complete output. |
| PI-UDEV-INSTALL | Install `install/udev/50-streamdeck.rules` using the README commands, add the user to `plugdev`, log out/in, reconnect USB, and complete PRE-PI-2 through PRE-PI-6. | PID is `0063`; rule is selected; ordinary user has read/write access. | ACL access instead of group access is acceptable. Needing `sudo` to access hidraw is a failure. |

Capture environment and demo logs:

```bash
mkdir -p test-logs
{
  date -Is
  uname -a
  cat /etc/os-release
  go version
  go env GOOS GOARCH CGO_ENABLED
  git rev-parse HEAD
  lsusb -d 0fd9:
  id
} 2>&1 | tee test-logs/pi-mini-environment.log

go run ./cmd/deckdemo 2>&1 | tee test-logs/pi-mini.log
```

### Non-root Mini operation

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| MINI-PI-NONROOT | Confirm `id -u` is not `0`, then launch the capture command without `sudo`. | Connect/restore logs exactly match MINI-MAC-CONNECT, and no permission error appears. | Product text may differ. `permission denied`, success only under `sudo`, or repeated unavailable lines while `lsusb` sees the deck is a failure. |
| MINI-PI-FIRST | Immediately press/release key 1 after restore. | Key-1 true/false lines appear on the first attempt and key 1 toggles. | A second attempt being required is a failure. |
| MINI-PI-K1 | Press/release key 1. | Correct key-1 transitions and image toggle. | Wrong/missing/duplicate index or update is a failure. |
| MINI-PI-K2 | Press/release key 2. | Correct key-2 transitions and image toggle. | Wrong/missing/duplicate index or update is a failure. |
| MINI-PI-K3 | Press/release key 3. | Correct key-3 transitions and image toggle. | Wrong/missing/duplicate index or update is a failure. |
| MINI-PI-K4 | Press/release key 4. | Correct key-4 transitions and image toggle. | Wrong/missing/duplicate index or update is a failure. |
| MINI-PI-K5 | Press/release key 5. | Correct transitions/image toggle plus a 10% brightness decrease and `output brightness=<value>`. | Log ordering around the release can vary. Wrong direction is a failure. |
| MINI-PI-K6 | Press/release key 6. | Correct transitions/image toggle plus a 10% brightness increase and `output brightness=<value>`. | Log ordering around the release can vary. Wrong direction is a failure. |
| MINI-PI-IMAGES | Repeat MINI-MAC-IMAGES. | All six 80x80 images are upright, readable, centered, correctly assigned, and update cleanly. | Minor panel color differences are acceptable. Rotation/mirror/crop/corruption is a failure. |
| MINI-PI-BRIGHTNESS | Repeat MINI-MAC-BRIGHTNESS from 70 down to 0 and up to 100. | 10% visible steps and clamped `output brightness=0`/`100` logs. | Panel may retain faint light at 0. Wraparound or HID errors are failures. |
| MINI-PI-NO-PLUS | Run 30 seconds and search `test-logs/pi-mini.log` for `input (dial|touch)` or `strip=`. | No matches. | Any match is a model-routing failure. |
| MINI-PI-REPLUG | While running, set non-default state, unplug USB for at least 5 seconds, then reconnect and press a key. | Disconnect/unavailable lines, then Mini reconnect/restore; state returns and first input works. | Retry count/error wording varies. Process exit, no reconnect, state loss, or permission loss after replug is a failure. |
| MINI-PI-CTRL-C | Press Control-C once. | `device shutdown reason=interrupt`; clean exit. | Shell signal text is acceptable. Hang/panic is a failure. |

### Reboot persistence

| ID | Command or action | Expected observation | Acceptable variation / failure signature |
| --- | --- | --- | --- |
| MINI-PI-REBOOT | Stop the demo, leave Mini connected, run `sudo reboot`, log back in, and repeat PRE-PI-2, PRE-PI-5, and PRE-PI-6 without reinstalling/reloading the rule. | Rule remains installed; hidraw is group/ACL writable by the ordinary user after boot. | The hidraw number may change. Lost permissions or needing a manual udev reload is a failure. |
| MINI-PI-POSTBOOT | Run `go run ./cmd/deckdemo` without `sudo`, press/release key 1 once, then Control-C. Append output with `2>&1 | tee -a test-logs/pi-mini-postboot.log`. | Mini connects/restores, first key press works, and shutdown is clean. | Product text/hidraw number may differ. Permission error, swallowed first press, or failed output is a failure. |
| MINI-PI-SECOND-REPLUG | Start another non-root run after reboot, unplug/reconnect USB, and verify one key plus brightness. | Permission remains valid on the newly created hidraw node; reconnect restores state; key and brightness writes succeed. | hidraw number change is normal. Any need for `sudo`, rule reload, or reboot is a failure. |

Keep these files for review:

- `test-logs/pi-mini-environment.log`
- complete output from all four build/check commands
- `test-logs/pi-mini.log`
- `test-logs/pi-mini-postboot.log`
- `ls -l` and `getfacl` output for the Mini hidraw node before and after reboot

## 5. Verification scorecard

Mark exactly one of PASS, FAIL, or NOT-TESTED for every row. Put log timestamps,
observed PID, or a short reason in Notes.

| ID | PASS | FAIL | NOT-TESTED | Notes |
| --- | --- | --- | --- | --- |
| PRE-MAC-1 | [ ] | [ ] | [ ] | |
| PRE-MAC-2 | [ ] | [ ] | [ ] | |
| PRE-MAC-3 | [ ] | [ ] | [ ] | |
| PRE-PI-1 | [ ] | [ ] | [ ] | |
| PRE-PI-2 | [ ] | [ ] | [ ] | |
| PRE-PI-3 | [ ] | [ ] | [ ] | |
| PRE-PI-4 | [ ] | [ ] | [ ] | |
| PRE-PI-5 | [ ] | [ ] | [ ] | |
| PRE-PI-6 | [ ] | [ ] | [ ] | |
| PLUS-CONNECT | [ ] | [ ] | [ ] | |
| PLUS-FIRST | [ ] | [ ] | [ ] | |
| PLUS-K1 | [ ] | [ ] | [ ] | |
| PLUS-K2 | [ ] | [ ] | [ ] | |
| PLUS-K3 | [ ] | [ ] | [ ] | |
| PLUS-K4 | [ ] | [ ] | [ ] | |
| PLUS-K5 | [ ] | [ ] | [ ] | |
| PLUS-K6 | [ ] | [ ] | [ ] | |
| PLUS-K7 | [ ] | [ ] | [ ] | |
| PLUS-K8 | [ ] | [ ] | [ ] | |
| PLUS-D1-ROTATE | [ ] | [ ] | [ ] | |
| PLUS-D2-ROTATE | [ ] | [ ] | [ ] | |
| PLUS-D3-ROTATE | [ ] | [ ] | [ ] | |
| PLUS-D4-ROTATE | [ ] | [ ] | [ ] | |
| PLUS-D1-PRESS | [ ] | [ ] | [ ] | |
| PLUS-D2-PRESS | [ ] | [ ] | [ ] | |
| PLUS-D3-PRESS | [ ] | [ ] | [ ] | |
| PLUS-D4-PRESS | [ ] | [ ] | [ ] | |
| PLUS-TAP | [ ] | [ ] | [ ] | |
| PLUS-PRESS | [ ] | [ ] | [ ] | |
| PLUS-FLICK | [ ] | [ ] | [ ] | |
| PLUS-BRIGHTNESS | [ ] | [ ] | [ ] | |
| PLUS-REPLUG | [ ] | [ ] | [ ] | |
| PLUS-CTRL-C | [ ] | [ ] | [ ] | |
| MINI-MAC-CONNECT | [ ] | [ ] | [ ] | |
| MINI-MAC-FIRST | [ ] | [ ] | [ ] | |
| MINI-MAC-K1 | [ ] | [ ] | [ ] | |
| MINI-MAC-K2 | [ ] | [ ] | [ ] | |
| MINI-MAC-K3 | [ ] | [ ] | [ ] | |
| MINI-MAC-K4 | [ ] | [ ] | [ ] | |
| MINI-MAC-K5 | [ ] | [ ] | [ ] | |
| MINI-MAC-K6 | [ ] | [ ] | [ ] | |
| MINI-MAC-IMAGES | [ ] | [ ] | [ ] | |
| MINI-MAC-BRIGHTNESS | [ ] | [ ] | [ ] | |
| MINI-MAC-NO-PLUS | [ ] | [ ] | [ ] | |
| MINI-MAC-REPLUG | [ ] | [ ] | [ ] | |
| MINI-MAC-CTRL-C | [ ] | [ ] | [ ] | |
| PI-BUILD-DEPS | [ ] | [ ] | [ ] | |
| PI-BUILD-GO | [ ] | [ ] | [ ] | |
| PI-BUILD-CLONE | [ ] | [ ] | [ ] | |
| PI-UDEV-INSTALL | [ ] | [ ] | [ ] | |
| MINI-PI-NONROOT | [ ] | [ ] | [ ] | |
| MINI-PI-FIRST | [ ] | [ ] | [ ] | |
| MINI-PI-K1 | [ ] | [ ] | [ ] | |
| MINI-PI-K2 | [ ] | [ ] | [ ] | |
| MINI-PI-K3 | [ ] | [ ] | [ ] | |
| MINI-PI-K4 | [ ] | [ ] | [ ] | |
| MINI-PI-K5 | [ ] | [ ] | [ ] | |
| MINI-PI-K6 | [ ] | [ ] | [ ] | |
| MINI-PI-IMAGES | [ ] | [ ] | [ ] | |
| MINI-PI-BRIGHTNESS | [ ] | [ ] | [ ] | |
| MINI-PI-NO-PLUS | [ ] | [ ] | [ ] | |
| MINI-PI-REPLUG | [ ] | [ ] | [ ] | |
| MINI-PI-CTRL-C | [ ] | [ ] | [ ] | |
| MINI-PI-REBOOT | [ ] | [ ] | [ ] | |
| MINI-PI-POSTBOOT | [ ] | [ ] | [ ] | |
| MINI-PI-SECOND-REPLUG | [ ] | [ ] | [ ] | |

## Report-back format

Return the filled scorecard plus this summary:

```text
Revision:
Tester/date/time zone:

Hardware:
- Plus model / serial / observed VID:PID:
- Mini model / serial / observed VID:PID:

Platforms:
- Mac model, macOS version, architecture:
- Pi model, Raspberry Pi OS version, kernel, architecture:
- Go version on each:

Failures or NOT-TESTED items:
- Scorecard ID:
- Exact action:
- Expected:
- Observed:
- Reproducible (always/intermittent/once):
- Relevant log timestamps:

Attachments:
- macos-plus.log
- macos-mini.log
- Pi environment/build/demo/postboot logs
- hidraw ls/getfacl output before and after reboot
- photos of any rotated, mirrored, cropped, corrupt, or misassigned images
```
