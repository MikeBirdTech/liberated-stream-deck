# Bridge key visuals: `keys[]`, press feedback, and animation

This is the additive revision-3 contract between a controller (Hestia) and the
`deckdemo` bridge for LCD keys. It extends the remote-presentation protocol
described in the README without changing any existing field. Everything here
is optional: a revision-2/3 controller that sends only `key`, `background`,
and static `image` objects keeps working exactly as before.

Ownership stays where it was:

- The controller owns meaning, lifecycle state, colors, artwork, typography,
  animation frames, and timing intent.
- The bridge owns hardware input, event delivery, frame decoding and caching,
  safe playback, HID pacing, cancellation, reconnect recovery, and rendering
  frames exactly as supplied. It contains no state names: "starting",
  "running", "success", and "error" are just different visual revisions to it.

## 1. Additive wire schema

### 1.1 GET response (top level)

```jsonc
{
  "command": "run_hardware_demo",      // unchanged, ignored for mode selection
  "revision": 3,
  "poll_ms": 2000,
  "generation": 1755600000123,         // NEW, optional: monotonic payload order
  "presentation": { "brightness": 70 },
  "key":  { ... },                     // legacy singular key (unchanged)
  "keys": [ { ... }, { ... } ],        // NEW: every LCD key presentation
  "background": { "keys": [ ... ] },   // unchanged
  "strip": { ... },                    // unchanged
  "boot_image": { ... }                // unchanged
}
```

### 1.2 Event acknowledgement `state`

```jsonc
{
  "ok": true,
  "events_seen": 42,
  "message": "Key 2 down received",
  "state": {
    "generation": 1755600000124,       // NEW, optional
    "key":  { ... },                   // legacy singular key (unchanged)
    "keys": [ { ... } ],               // NEW: keys this ack changes (or all)
    "strip": { ... }                   // unchanged
  }
}
```

### 1.3 Key object

```jsonc
{
  "index": 2,
  "id": "cleanup",
  "label": "Cleanup",
  "sub": "nightly",                    // NEW, optional: part of the key identity
  "state": "running",                  // opaque to the bridge
  "bg": "#272C24",
  "fg": "#F6F5EE",
  "image":  { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "..." },
  "visual": { ... }                    // NEW, optional: see 1.4
}
```

### 1.4 `visual` object (NEW)

```jsonc
"visual": {
  "revision": "cleanup-running-7",     // identity of the whole program
  "min_visible_ms": 400,               // hold once painted before replacement
  "rest": { "revision": "...", "mime_type": "image/png", "data_b64": "..." },
  "animation": {
    "frames": [
      { "duration_ms": 120, "revision": "cleanup-run-f0", "mime_type": "image/png", "data_b64": "..." },
      { "duration_ms": 120, "revision": "cleanup-run-f1", "mime_type": "image/png", "data_b64": "..." }
    ],
    "loop_count": 0,                   // absent=1 play, N=N plays, 0=repeat (bounded)
    "end": "rest"                      // "rest" (default) or "hold"
  },
  "press": {
    "frames": [
      { "duration_ms": 60, "revision": "cleanup-press-f0", "mime_type": "image/png", "data_b64": "..." }
    ],
    "loop_count": 1,
    "min_visible_ms": 150              // press feedback hold (default 150)
  }
}
```

Field reference:

| Field | Type | Meaning |
| --- | --- | --- |
| `visual.revision` | string | Identity of the program. Same revision ⇒ no repaint, animation keeps playing. New revision ⇒ replaces the key's visual (see §3). Strongly recommended; when absent the bridge derives one from the visual's content. |
| `visual.min_visible_ms` | int | Minimum time the visual stays on the key once it has painted before a newer revision may replace it. `0`/absent: replaceable immediately (and skipped if superseded before it ever paints). |
| `visual.rest` | image | Steady-state frame. Falls back to the key's `image`, then to the label/bg/fg rendering. |
| `visual.animation` | sequence | Plays from the visual's first paint. Absent ⇒ static. |
| `visual.press` | sequence | Cached press feedback. Starts on key-down, holds its last frame while the key is down, ends on key-up but never before `press.min_visible_ms`. Absent ⇒ the bridge renders a neutral depression of the current frame. |
| `sequence.frames[]` | array | 1..64 frames, each an image object plus `duration_ms`. |
| `frame.duration_ms` | int | Display time. Values below the bridge floor (40 ms) are raised to the floor. |
| `frame.revision` | string | Frame identity; keys the decoded-frame cache. A frame with a `revision` and no `data_b64` refers to a frame already supplied under that revision (in this payload or earlier); unknown references invalidate the sequence. |
| `sequence.loop_count` | int | Plays. Absent ⇒ 1. `N>0` ⇒ N plays (clamped to 10 000). `0` ⇒ repeat until the visual is replaced, bounded by a 30-minute cap after which the end frame shows. |
| `sequence.end` | string | After a finite play: `"rest"` (default) returns to `rest`; `"hold"` keeps the last frame. Ignored for `press`. |
| `press.min_visible_ms` | int | Minimum time press feedback stays on the key once painted. Absent ⇒ 150 ms. |
| `generation` | int64 | Optional monotonic payload order (see §2.3). |

Image objects are unchanged: `revision`, informational `mime_type`, and
base64 PNG or JPEG in `data_b64`. Frames are scaled to the native key size
(Plus 120×120, Mini 80×80); render at native size to avoid scaling.

## 2. Precedence and fallback

### 2.1 Which wire object describes physical key *i*

1. `keys[]` entry with `index == i` (GET replaces the whole array; an ack
   merges by index and leaves unnamed keys untouched).
2. Legacy singular `key`, when `key.index == i`.
3. `background.keys` entry with `index == i`.
4. Quiet paper (#F6F5EE).

Only changed keys repaint. "Changed" means the resolved visual revision
differs from the one already accepted for that physical key.

### 2.2 Which pixels a key shows

| Situation | Shown |
| --- | --- |
| `visual.animation` valid and playing | the animation frame due now |
| finite animation finished | `rest` (`end: "rest"`) or the last frame (`end: "hold"`) |
| no/invalid animation | `rest` |
| key held down / press hold | `visual.press` frame, or the generic depression of the frame that was on the key |
| `rest` absent or undecodable | `image`, else label/bg/fg rendering |

Invalid animation or press payloads (undecodable frame, unresolved revision
reference, zero or more than 64 frames, negative `loop_count`, unknown `end`)
drop only that sequence: the key still shows its static rest frame. The
reason is logged once per visual revision. An invalid `visual.rest` falls back
to `image`/label.

### 2.3 Key revision derivation (for old payloads)

| Key payload | Bridge revision |
| --- | --- |
| `visual.revision` set | `visual:<revision>` |
| `visual` without revision | hash of the visual content plus label/sub/bg/fg |
| `image.revision` set | `image:<revision>` plus label/sub/bg/fg hash |
| `image` without revision | hash of `data_b64` plus label/sub/bg/fg |
| label/bg/fg only | hash of label/sub/bg/fg |
| no key | `paper` |

So unchanged revision-2/3 polls never repaint, and any visible change does.

### 2.4 Payload ordering (`generation`)

When both polls and acks carry `generation`, a payload older than the newest
applied one is ignored (an in-flight poll cannot revert a fresher ack; a late
key-up ack cannot revert a key-down ack). Three consecutive stale polls mean
the controller restarted its counter: the third is accepted. Absent or `0`
always applies. Use a value that is monotonic across restarts, e.g. a
millisecond timestamp mixed with a sequence number.

## 3. Playback, cancellation, and reconnect semantics

**Layers.** Every key has a base visual (the accepted program) and, during a
press, a press overlay. The overlay is painted over whatever the base shows
and removed on release; the base continues in wall-clock time underneath.

**Key-down.** The bridge immediately paints the key's cached press visual (or
the generic depression of the frame on the key) and POSTs the raw event
exactly as before. No HTTP round trip is involved. The bridge never triggers
the action or interprets the key.

**Key-up.** The overlay ends at `max(key-up, first paint + min_visible_ms)`.
A tap shorter than one HID write still produces visible feedback.

**Acknowledgement / poll with the same revision.** No-op: the overlay keeps
showing until key-up. This is the "action started, run record not yet
visible" case - the user still sees their press.

**Acknowledgement / poll with a new revision.** The new program replaces the
key's visual - the press overlay included - as soon as the visual currently
on the key has satisfied its minimum visible time (press: `press.min_visible_ms`,
base: `visual.min_visible_ms`). If two new revisions arrive during that hold,
the newest wins; the superseded one never paints. Once accepted, the old
animation can never write again: frames are computed from the accepted
program at write time, never queued.

**Animation clocks** start at the program's first paint on the key, so a
short "starting" animation plays from its first frame even if pacing delayed
it. Frame writes that fall behind (pacing, slow USB) are coalesced: the frame
due *now* is written, intermediate ones are skipped. Per-key frame order is
always preserved.

**Bounded loops.** `loop_count: 0` repeats until replaced, capped at 30
minutes, then settles on the end frame. Re-issue a new revision for longer
states.

**Reconnect.** Programs survive unplug/replug. On reconnect every key is
repainted with its latest steady state: pending programs are adopted, finite
animations show their end frame (never replayed from the middle), unbounded
loops restart from frame 0, and press overlays are dropped. The usual
reconnect GET then applies any newer revisions.

**Shutdown.** The scheduler stops before the clean-shutdown background
repaint; the persisted background frames are rest frames, never animation
frames.

**Write failure.** A failed key write is reported to the connection loop and
ends the connection exactly as before; the reconnect path restores every key.

## 4. Safe timing and rate limits

| Limit | Default | Configurable |
| --- | --- | --- |
| Minimum gap between any two key writes | 20 ms (≈50 writes/s aggregate) | `LIBERATED_STREAM_DECK_KEY_MIN_INTERVAL_MS` |
| Minimum frame duration | 40 ms per key (25 fps) | - |
| Frames per sequence | 64 | - |
| Explicit `loop_count` | ≤ 10 000 | - |
| `loop_count: 0` runtime | 30 min | - |
| Press feedback minimum visible | 150 ms | `press.min_visible_ms` |

Guidance for the controller: 4-10 fps is plenty for these animations; keep
the sum of all keys' frame rates under ~40 writes/s (pacing will otherwise
coalesce frames); keep sequences short (2-12 frames) and prefer revision-only
frame references when several keys share artwork. Feature-report setters
(fills) have a separately measured wedge threshold (README); key image writes
are output reports and were measured at ~4 ms each on the Plus, but sustained
multi-key animation has not yet been soak-tested on hardware.

## 5. Compatibility

- Old controllers (revision 2/3 with `key`/`background`/`image` only): no
  behavior change except that presses now get the bridge's generic depression
  feedback and that unchanged polls no longer rewrite the active key.
- Old bridges (before this contract) ignore `keys[]`, `visual`, `sub`, and
  `generation`; Hestia should keep sending `key` and `background` so they stay
  correct.
- Controllers must not depend on a press frame being shown for a fixed time
  longer than `min_visible_ms`; the bridge may hold it longer while pacing.
- Pressable dials: the Plus has no per-dial display; dial feedback would be a
  touch-strip region write. The scheduler is surface-agnostic, but the strip
  writer is a separate coalescing path today - dial press feedback remains a
  documented extension (a `visual` on the strip `regions[]` would use the same
  program shape).

## 6. Examples

The `data_b64` values below are real 2×2 PNGs (scaled by the bridge) so the
payloads are valid as written; production frames should be native 120×120.

```
MOSS  = iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAF0lEQVR4nGIJLfNiYGBgYgADQAAAAP//DVsBHLpc4C4AAAAASUVORK5CYII=
LEAF  = iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAF0lEQVR4nGLJXxTDwMDAxAAGgAAAAP//EYMBdPCaCLgAAAAASUVORK5CYII=
EMBER = iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAF0lEQVR4nGLZ7KjPwMDAxAAGgAAAAP//DnwBKnKDrF8AAAAASUVORK5CYII=
```

### 6.1 Resting key with cached press feedback (GET `keys[]` entry)

```json
{
  "index": 1, "id": "cleanup", "label": "Cleanup", "sub": "nightly", "state": "idle",
  "bg": "#272C24", "fg": "#F6F5EE",
  "image": { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "MOSS" },
  "visual": {
    "revision": "cleanup-idle-3",
    "press": {
      "frames": [ { "duration_ms": 80, "revision": "cleanup-press-3", "mime_type": "image/png", "data_b64": "LEAF" } ],
      "min_visible_ms": 150
    }
  }
}
```

### 6.2 Immediate press feedback

Nothing is sent: on key-down the bridge paints `cleanup-press-3` (cached from
6.1) within one HID write and POSTs `{"kind":"key","index":1,"pressed":true}`.
If the key had no `visual.press`, the bridge paints the depression of
`cleanup-rest-3` instead.

### 6.3 Starting animation (ack to the key-down)

```json
{
  "ok": true, "events_seen": 43, "message": "Key 2 down received - Cleanup starting",
  "state": {
    "generation": 1755600000200,
    "keys": [ {
      "index": 1, "id": "cleanup", "label": "Cleanup", "state": "starting", "bg": "#272C24", "fg": "#F6F5EE",
      "image": { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "MOSS" },
      "visual": {
        "revision": "cleanup-starting-44",
        "min_visible_ms": 400,
        "animation": {
          "frames": [
            { "duration_ms": 100, "revision": "cleanup-start-f0", "mime_type": "image/png", "data_b64": "LEAF" },
            { "duration_ms": 100, "revision": "cleanup-start-f1", "mime_type": "image/png", "data_b64": "MOSS" },
            { "duration_ms": 100, "revision": "cleanup-start-f2", "mime_type": "image/png", "data_b64": "LEAF" }
          ],
          "loop_count": 1,
          "end": "hold"
        },
        "press": { "frames": [ { "duration_ms": 80, "revision": "cleanup-press-3" } ] }
      }
    } ],
    "strip": { "page": 0, "pages": 2, "title": "Today", "lines": ["Cleanup: starting"] }
  }
}
```

The press overlay stays for its 150 ms, then the starting animation plays
once and holds its last frame; a newer revision can replace it after 400 ms.
The `press` frame is a revision-only reference to the frame cached from 6.1.

### 6.4 Looping running animation (GET or ack)

```json
{
  "index": 1, "id": "cleanup", "label": "Cleanup", "state": "running", "bg": "#272C24", "fg": "#F6F5EE",
  "image": { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "MOSS" },
  "visual": {
    "revision": "cleanup-running-44",
    "animation": {
      "frames": [
        { "duration_ms": 250, "revision": "cleanup-run-f0", "mime_type": "image/png", "data_b64": "LEAF" },
        { "duration_ms": 250, "revision": "cleanup-run-f1", "mime_type": "image/png", "data_b64": "MOSS" },
        { "duration_ms": 250, "revision": "cleanup-run-f2", "mime_type": "image/png", "data_b64": "LEAF" },
        { "duration_ms": 250, "revision": "cleanup-run-f3", "mime_type": "image/png", "data_b64": "MOSS" }
      ],
      "loop_count": 0
    }
  }
}
```

Unchanged polls (same `visual.revision`) keep it looping; it restarts from
frame 0 after a replug; it settles on `rest` after 30 minutes unless a new
revision arrives.

### 6.5 Success completion (ack or poll)

```json
{
  "index": 1, "id": "cleanup", "label": "Cleanup", "state": "success", "bg": "#55764A", "fg": "#F6F5EE",
  "image": { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "MOSS" },
  "visual": {
    "revision": "cleanup-success-44",
    "min_visible_ms": 1200,
    "animation": {
      "frames": [
        { "duration_ms": 120, "revision": "cleanup-ok-f0", "mime_type": "image/png", "data_b64": "LEAF" },
        { "duration_ms": 120, "revision": "cleanup-ok-f1", "mime_type": "image/png", "data_b64": "MOSS" },
        { "duration_ms": 900, "revision": "cleanup-ok-f2", "mime_type": "image/png", "data_b64": "LEAF" }
      ],
      "loop_count": 1,
      "end": "rest"
    }
  }
}
```

After the 1.14 s play the key returns to `cleanup-rest-3`. Hestia typically
follows with `cleanup-idle-45` (the 6.1 shape) on the next poll.

### 6.6 Error completion

```json
{
  "index": 1, "id": "cleanup", "label": "Cleanup", "state": "error", "bg": "#B3412F", "fg": "#F6F5EE",
  "image": { "revision": "cleanup-rest-3", "mime_type": "image/png", "data_b64": "MOSS" },
  "visual": {
    "revision": "cleanup-error-44",
    "min_visible_ms": 1500,
    "rest": { "revision": "cleanup-error-rest-44", "mime_type": "image/png", "data_b64": "EMBER" },
    "animation": {
      "frames": [
        { "duration_ms": 90, "revision": "cleanup-err-f0", "mime_type": "image/png", "data_b64": "EMBER" },
        { "duration_ms": 90, "revision": "cleanup-err-f1", "mime_type": "image/png", "data_b64": "MOSS" },
        { "duration_ms": 90, "revision": "cleanup-err-f0" },
        { "duration_ms": 90, "revision": "cleanup-err-f1" },
        { "duration_ms": 90, "revision": "cleanup-err-f0" }
      ],
      "loop_count": 1,
      "end": "rest"
    }
  }
}
```

`visual.rest` overrides `image` so the key stays on the error frame (EMBER)
after the shake until Hestia issues the next revision.

### 6.7 Full `keys[]` acknowledgement updating a nonzero key

```json
{
  "ok": true, "events_seen": 51, "message": "Key 3 down received - Deploy starting",
  "state": {
    "generation": 1755600000900,
    "key":  { "index": 0, "id": "workday", "label": "Workday", "state": "idle", "bg": "#F6F5EE", "fg": "#272C24" },
    "keys": [
      { "index": 0, "id": "workday", "label": "Workday", "state": "idle",     "bg": "#F6F5EE", "fg": "#272C24", "visual": { "revision": "workday-idle-9" } },
      { "index": 1, "id": "cleanup", "label": "Cleanup", "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "cleanup-idle-45" } },
      { "index": 2, "id": "deploy",  "label": "Deploy",  "state": "starting", "bg": "#272C24", "fg": "#F6F5EE",
        "visual": { "revision": "deploy-starting-12", "min_visible_ms": 400,
                    "animation": { "frames": [ { "duration_ms": 100, "revision": "deploy-start-f0", "mime_type": "image/png", "data_b64": "LEAF" } ], "end": "hold" } } },
      { "index": 3, "id": "pi4",     "label": "Pi 4",    "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "pi4-idle-2" } },
      { "index": 4, "id": "pizero",  "label": "Pi Zero", "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "pizero-idle-2" } },
      { "index": 5, "id": "rack",    "label": "Rack",    "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "rack-idle-2" } },
      { "index": 6, "id": "music",   "label": "Music",   "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "music-idle-2" } },
      { "index": 7, "id": "lights",  "label": "Lights",  "state": "idle",     "bg": "#272C24", "fg": "#F6F5EE", "visual": { "revision": "lights-idle-2" } }
    ],
    "strip": { "page": 0, "pages": 2, "title": "Today", "lines": ["Deploy: starting"] }
  }
}
```

Only key 2 changed revision, so only key 2 is written; key 0 is untouched
even though the legacy `key` names it.

## 7. Hestia implementation checklist

1. **GET returns every key's press feedback up front.** Put `visual.press`
   (or rely on the generic depression) on every `keys[]` entry in the normal
   GET so the bridge has it cached before any input.
2. **Every relevant ack returns authoritative `state.keys[]`** - at least the
   keys that changed, with the full `visual` for each. Keep returning the
   legacy `state.key` and `state.strip`.
3. **Emit a distinct transient "starting" visual immediately** in the
   key-down ack, even before the run record exists, with its own
   `visual.revision` and a `min_visible_ms` (300-500 ms works well).
4. **Bump `visual.revision` whenever appearance or animation changes**
   (idle→starting→running→success/error→idle); keep it stable while nothing
   changes so unchanged polls are free. Include the run id or a counter.
5. **Supply the frames, colors, typography, and durations** - native 120×120
   PNG, 2-12 frames, 80-250 ms per frame; give each frame a stable
   `revision` and reuse frames by revision-only reference.
6. **Drive the transitions from the action lifecycle**: starting (ack) →
   running (`loop_count: 0`, refreshed by polls with the same revision) →
   success/error (finite, `min_visible_ms` ≥ 1 s, `end: "rest"` or a
   `visual.rest` override) → idle.
7. **Keep fast transitions perceptible**: set `min_visible_ms` on starting
   and completion visuals; the bridge already guarantees 150 ms of press
   feedback even when the ack carries the same resting frame.
8. **Send `generation`** (monotonic across restarts) on GET responses and
   ack `state` objects so a slow poll can never revert a fresher ack.
9. Optional: set `sub` and keep `image`/`background` populated for older
   bridges.
