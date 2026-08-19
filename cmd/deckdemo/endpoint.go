package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

// Controller endpoint configuration. The optional companion controller that
// remote-commands the deck is a deployment detail, never part of this
// repository: its base URL comes from the LIBERATED_STREAM_DECK_CONTROLLER
// environment variable at startup. Empty means no controller is configured
// and deckdemo runs in classic local mode without any network I/O.
var (
	controllerBaseURL, controllerEventURL = controllerURLs()
)

func controllerURLs() (base, event string) {
	base = strings.TrimRight(os.Getenv("LIBERATED_STREAM_DECK_CONTROLLER"), "/")
	if base == "" {
		return "", ""
	}
	return base, base + "/event"
}

var demoHTTPClient = &http.Client{Timeout: time.Second}

type remoteDemo struct {
	Command      string `json:"command"`
	Revision     int    `json:"revision"`
	Presentation struct {
		Theme      string `json:"theme"`
		Title      string `json:"title"`
		Message    string `json:"message"`
		Brightness int    `json:"brightness"`
	} `json:"presentation"`
	Key        *remoteKey        `json:"key"`
	Keys       []remoteKey       `json:"keys"`
	Strip      *remoteStrip      `json:"strip"`
	Background *remoteBackground `json:"background"`
	BootImage  *remoteBootImage  `json:"boot_image"`
	PollMS     int               `json:"poll_ms"`
	EventsSeen int               `json:"events_seen"`
	LastEvent  *struct {
		Summary string `json:"summary"`
	} `json:"last_event"`
	// Generation optionally orders payloads. When present (non-zero) on a
	// poll response and on ack state objects, a payload older than the newest
	// one already applied is ignored, so a slow poll cannot revert a fresher
	// acknowledgement. Absent means "always apply" (legacy behavior).
	Generation int64 `json:"generation"`
}

// remoteKey is the server-owned presentation of one LCD key. State is opaque:
// the deck never interprets it; it paints exactly the wire bg/fg and label.
// When the optional image is present and decodable it takes precedence; the
// label/bg/fg stay as the semantic fallback for absent or invalid images.
type remoteKey struct {
	Index  int           `json:"index"`
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Sub    string        `json:"sub"`
	State  string        `json:"state"`
	BG     string        `json:"bg"`
	FG     string        `json:"fg"`
	Image  *remoteImage  `json:"image"`
	Visual *remoteVisual `json:"visual"`
}

// remoteVisual is the optional opaque visual program for one key: a resting
// frame, an optional timed animation, and optional press feedback that is
// cached before any input arrives. Liberated plays it without knowing what it
// means. Revision identifies the whole program; a key whose visual revision
// is unchanged is never repainted, and a new revision replaces whatever the
// key is showing (after the current visual's minimum visible time). Every
// part is optional: a visual with only a revision behaves like a plain
// image/label key.
type remoteVisual struct {
	Revision string `json:"revision"`
	// MinVisibleMS keeps this visual on the key for at least that long once
	// it has painted before a newer revision may replace it.
	MinVisibleMS int `json:"min_visible_ms"`
	// Rest overrides the key's image as the steady-state frame.
	Rest *remoteImage `json:"rest"`
	// Animation plays from the visual's first paint.
	Animation *remoteSequence `json:"animation"`
	// Press plays on key-down, holds its last frame while the key is down,
	// and ends on key-up (never before its min_visible_ms).
	Press *remoteSequence `json:"press"`
}

// remoteSequence is a timed list of raster frames.
type remoteSequence struct {
	Frames []remoteFrame `json:"frames"`
	// LoopCount is the number of plays: absent means 1; 0 means repeat
	// until the visual is replaced (bounded by the deck's loop-duration cap).
	LoopCount *int `json:"loop_count"`
	// End is "rest" (default: return to the resting frame) or "hold" (keep
	// the last frame). Ignored for press sequences, which always hold while
	// the key is down.
	End string `json:"end"`
	// MinVisibleMS applies to press sequences only (the visual's own
	// min_visible_ms covers animations); absent selects the deck default.
	MinVisibleMS int `json:"min_visible_ms"`
}

// remoteFrame is one animation frame: an image plus its display duration. A
// frame may carry only a revision (no data_b64) to reference a frame already
// supplied elsewhere in the same or an earlier payload under that revision.
type remoteFrame struct {
	DurationMS int `json:"duration_ms"`
	remoteImage
}

// remoteImage is an opaque server-rendered raster frame for one key: a
// base64-encoded PNG or JPEG that the deck scales to the native key size and
// paints verbatim, never interpreting its contents. Revision is the server's
// content digest and keys the decoded-frame cache; MimeType is informational
// only (the payload format is sniffed during decode).
type remoteImage struct {
	Revision string `json:"revision"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data_b64"`
}

// remoteStrip is the server-owned presentation of the touch strip. A valid
// image is the complete base frame and takes precedence over the semantic
// title/lines fallback. Regions are native-coordinate patches layered over a
// valid base frame in list order.
type remoteStrip struct {
	Page    int            `json:"page"`
	Pages   int            `json:"pages"`
	Title   string         `json:"title"`
	Lines   []string       `json:"lines"`
	Image   *remoteImage   `json:"image"`
	Regions []remoteRegion `json:"regions"`
}

// remoteRegion is an opaque raster patch positioned in the native 800x100
// touch-strip coordinate space. The decoded image bounds define its size.
type remoteRegion struct {
	X int `json:"x"`
	Y int `json:"y"`
	remoteImage
}

// remoteBackground is the optional server-owned frame set for all keys. Keys
// not covered by the active key render these frames (previous default: quiet
// paper). Rendering them persists them in the device, so a clean shutdown
// repaints them to make the next power-on display the controller-owned.
type remoteBackground struct {
	Keys []remoteKey `json:"keys"`
}

// remoteBootImage is an optional server-provided power-on frame. the controller
// increments Revision to trigger a (re)upload; Data is a base64-encoded PNG or
// JPEG of any size - the deck scales it to 800x480 and persists it on-device
// via the undocumented 0x09 upload, so it shows at the next power-on.
type remoteBootImage struct {
	Revision int    `json:"revision"`
	Data     string `json:"data_b64"`
}

// remoteState is the state object returned on event acks. Its key/strip shapes
// are identical to the GET fields so the deck can repaint immediately from an
// ack without polling.
type remoteState struct {
	Key        *remoteKey   `json:"key"`
	Keys       []remoteKey  `json:"keys"`
	Strip      *remoteStrip `json:"strip"`
	Generation int64        `json:"generation"`
}

type eventAck struct {
	OK         bool         `json:"ok"`
	EventsSeen int          `json:"events_seen"`
	Message    string       `json:"message"`
	State      *remoteState `json:"state"`
}

type eventPostResult struct {
	ack eventAck
	err error
}

func fetchRemoteDemo(ctx context.Context, client *http.Client, url string) (remoteDemo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return remoteDemo{}, fmt.Errorf("create demo request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return remoteDemo{}, fmt.Errorf("get demo: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return remoteDemo{}, fmt.Errorf("get demo: status=%d body=%q", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var demo remoteDemo
	if err := json.NewDecoder(response.Body).Decode(&demo); err != nil {
		return remoteDemo{}, fmt.Errorf("decode demo response: %w", err)
	}
	return demo, nil
}

func remoteEvent(event streamdeck.Event) (map[string]any, bool) {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		return map[string]any{"kind": "key", "index": event.Key, "pressed": event.Pressed}, true
	case streamdeck.DialRotateEvent:
		return map[string]any{"kind": "dial_rotate", "index": event.Dial, "delta": event.Delta}, true
	case streamdeck.DialPressEvent:
		return map[string]any{"kind": "dial_press", "index": event.Dial, "pressed": event.Pressed}, true
	case streamdeck.TouchEvent:
		switch event.Kind {
		case streamdeck.TouchTap:
			return map[string]any{"kind": "touch_tap", "x": event.X, "y": event.Y}, true
		case streamdeck.TouchPress:
			return map[string]any{"kind": "touch_press", "x": event.X, "y": event.Y}, true
		case streamdeck.TouchFlick:
			return map[string]any{
				"kind":  "touch_flick",
				"start": map[string]int{"x": event.StartX, "y": event.StartY},
				"end":   map[string]int{"x": event.EndX, "y": event.EndY},
			}, true
		}
	}
	return nil, false
}

// postEventAsync POSTs one raw event without blocking the caller. It is a
// variable so tests can substitute a delayed or scripted controller.
var postEventAsync = func(ctx context.Context, payload map[string]any, results chan<- eventPostResult) {
	go func() {
		ack, err := postEvent(ctx, demoHTTPClient, controllerEventURL, payload)
		select {
		case results <- eventPostResult{ack: ack, err: err}:
		case <-ctx.Done():
		}
	}()
}

func postEvent(ctx context.Context, client *http.Client, url string, payload map[string]any) (eventAck, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return eventAck{}, fmt.Errorf("encode event: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return eventAck{}, fmt.Errorf("create event request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return eventAck{}, fmt.Errorf("post event: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
			return eventAck{}, fmt.Errorf("post event: status=%d", response.StatusCode)
		}
		return eventAck{}, fmt.Errorf("post event: status=%d error=%q", response.StatusCode, failure.Error)
	}

	var ack eventAck
	if err := json.NewDecoder(response.Body).Decode(&ack); err != nil {
		return eventAck{}, fmt.Errorf("decode event ack: %w", err)
	}
	if !ack.OK {
		return eventAck{}, fmt.Errorf("event ack was not ok")
	}
	return ack, nil
}

func lastEventMessage(message string) string {
	message = strings.TrimSuffix(message, " received")
	return strings.ReplaceAll(message, " → ", " -> ")
}
