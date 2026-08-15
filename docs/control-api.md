# Local control API

`deckd` is a hardware boundary for applications that control a Stream Deck. It
does not assign meaning to inputs, generate layouts, execute actions, or contain
agent logic. Applications provide pixels and interpret the physical events it
returns.

## Run

```bash
go run ./cmd/deckd
```

The default is a Stream Deck Plus on `127.0.0.1:28484`. Configuration can be
provided by flags or environment variables:

```bash
go run ./cmd/deckd -listen 127.0.0.1:28484 -model plus
LIBERATED_STREAM_DECK_LISTEN=127.0.0.1:28484 LIBERATED_STREAM_DECK_MODEL=plus go run ./cmd/deckd
```

`-model mini` selects the original Stream Deck Mini. The listener defaults to
loopback so the unauthenticated API is not exposed to the network.

## Discover capabilities

```http
GET /v1/capabilities
```

The response reports connection state, exact native surface dimensions,
physical event kinds, and supported command names. Capabilities remain
available while the device is unplugged.

The complete machine-readable contract is served from `GET /v1/openapi.json`.
Applications can use it for client generation or runtime tool discovery.

For a Plus, the live output surfaces are:

| Surface | Native dimensions | Updates |
| --- | ---: | --- |
| Key 0 through 7 | 120x120 each | Complete key frame |
| Upper LCD | 800x480 | Complete LCD frame |
| Touch strip | 800x100 | Complete frame or rectangular region |

Indexes and coordinates are zero-based. Clients should discover these values
rather than hard-code them when supporting more than one model.

## Send hardware commands

```http
POST /v1/commands
Content-Type: application/json
```

Every request is a serialized batch. No other client's commands can interleave
inside a batch, although the hardware does not provide a display-wide atomic or
vsync commit.

```json
{
  "request_id": "frame-42",
  "commands": [
    {
      "type": "set_brightness",
      "brightness": 70
    },
    {
      "type": "set_key_image",
      "index": 0,
      "image": {
        "revision": "sha256-or-client-revision",
        "mime_type": "image/png",
        "data_base64": "..."
      }
    }
  ]
}
```

Supported Plus commands:

- `set_brightness`: `brightness` from 0 through 100.
- `set_key_image`: `index` and an exact 120x120 `image`.
- `set_lcd_image`: an exact 800x480 `image`.
- `set_touch_strip_image`: an exact 800x100 `image`.
- `set_touch_strip_region`: `x`, `y`, and a naturally sized `image` that fits
  entirely inside the 800x100 strip.

Images must be base64-encoded PNG or JPEG. `mime_type` must match the actual
payload. `revision` is an optional opaque client identifier; when omitted,
`deckd` uses a content-derived SHA-256 revision internally. Native
dimensions are strict: `deckd` never silently scales client artwork.

The device protocol currently transmits Plus frames as JPEG at quality 90, even
when an application supplies PNG. Native coordinates are exact, but bit-exact
lossless color output is therefore not claimed.

A successful live write returns HTTP 200 and `"status":"applied"`. When the
device is absent or a hardware write fails, the accepted desired state returns
HTTP 202 and `"status":"queued"`. It is restored automatically after the HID
connection recovers.

The latest complete LCD frame is authoritative for the upper display and clears
remembered per-key overlays. Key frames sent afterward remain overlays and are
restored after that LCD frame on reconnect. The touch strip is maintained as a
complete desired framebuffer: region updates are composited into it so a
reconnect can restore the exact latest strip state. If the first strip command
is a region, the unspecified pixels are black.

## Observe desired state

```http
GET /v1/state
```

The response contains connection state, the monotonically increasing accepted
generation, desired brightness, and the remembered revision for each surface.
It intentionally does not return image bytes.

## Receive physical input

```http
GET /v1/events
Accept: text/event-stream
```

The endpoint is a Server-Sent Events stream. It immediately sends a `ready`
event containing the current state, followed by normalized device events:

```text
id: 18
event: key
data: {"sequence":18,"timestamp":"2026-08-14T12:34:56.123Z","kind":"key","index":0,"pressed":true}

id: 19
event: dial_rotate
data: {"sequence":19,"timestamp":"2026-08-14T12:34:56.456Z","kind":"dial_rotate","index":1,"delta":-2}
```

Plus event kinds are `key`, `dial_press`, `dial_rotate`, `touch_tap`,
`touch_press`, and `touch_flick`. Connection transitions use
`device_connected` and `device_disconnected`; unexpected decodable HID content
uses `diagnostic`.

The service never blocks the HID reader on a slow client. Each subscriber has a
256-event buffer. A subscriber that exhausts it is disconnected, forcing that
application to reconnect instead of silently continuing after losing a key
release or other transition.

SSE is a live stream, not a replay log. Applications that disconnect should
re-read `/v1/state` before resuming.

## Health

```http
GET /v1/health
```

This confirms that the API process is serving. It does not imply that hardware
is connected; use `/v1/capabilities` or `/v1/state` for that.

## Physical verification

The complete Plus output API was exercised on the connected Apple Silicon
macOS unit on 2026-08-14. Each request completed as `applied` after a real HID
write:

| Command | Observed request-to-HID completion |
| --- | ---: |
| Brightness | 10 ms |
| Full 800x480 LCD calibration frame | 83 ms |
| Eight 120x120 key calibration frames in one batch | 32 ms total |
| Full 800x100 touch-strip calibration frame | 30 ms |
| 160x40 touch-strip region at (320,30) | 22 ms |

These are single-run observations, not guaranteed performance bounds. The
service reported the real connected VID/PID (`0x0FD9:0x0084`) and retained all
five accepted generations in `/v1/state`. After the test, the pre-existing
`deckdemo` LaunchAgent was restored and verified running.
