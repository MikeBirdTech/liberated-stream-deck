package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

func TestFetchRemoteDemo(t *testing.T) {
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		return jsonResponse(http.StatusOK, `{
			"command":"run_hardware_demo",
			"revision":1,
			"presentation":{"theme":"paper","title":"Demo Garden","message":"Demo link established","brightness":70},
			"events_seen":17,
			"last_event":{"kind":"dial_rotate","summary":"Dial 2 +3"}
		}`), nil
	})

	demo, err := fetchRemoteDemo(context.Background(), client, "http://playground.test/demo")
	if err != nil {
		t.Fatalf("fetchRemoteDemo: %v", err)
	}
	if demo.Command != "run_hardware_demo" || demo.Revision != 1 || demo.Presentation.Theme != "paper" {
		t.Fatalf("demo = %+v", demo)
	}
	if demo.Presentation.Title != "Demo Garden" || demo.Presentation.Message != "Demo link established" || demo.Presentation.Brightness != 70 {
		t.Fatalf("presentation = %+v", demo.Presentation)
	}
	if demo.EventsSeen != 17 || demo.LastEvent == nil || demo.LastEvent.Summary != "Dial 2 +3" {
		t.Fatalf("event state = count %d last %+v", demo.EventsSeen, demo.LastEvent)
	}
}

func TestRemoteEventShapes(t *testing.T) {
	tests := []struct {
		name  string
		event streamdeck.Event
		want  string
	}{
		{name: "key", event: streamdeck.KeyEvent{Key: 0, Pressed: true}, want: `{"kind":"key","index":0,"pressed":true}`},
		{name: "dial rotate", event: streamdeck.DialRotateEvent{Dial: 1, Delta: 3}, want: `{"kind":"dial_rotate","index":1,"delta":3}`},
		{name: "dial press", event: streamdeck.DialPressEvent{Dial: 2, Pressed: true}, want: `{"kind":"dial_press","index":2,"pressed":true}`},
		{name: "tap", event: streamdeck.TouchEvent{Kind: streamdeck.TouchTap, X: 327, Y: 48}, want: `{"kind":"touch_tap","x":327,"y":48}`},
		{name: "press", event: streamdeck.TouchEvent{Kind: streamdeck.TouchPress, X: 383, Y: 49}, want: `{"kind":"touch_press","x":383,"y":49}`},
		{name: "flick", event: streamdeck.TouchEvent{Kind: streamdeck.TouchFlick, StartX: 189, StartY: 46, EndX: 242, EndY: 44}, want: `{"kind":"touch_flick","start":{"x":189,"y":46},"end":{"x":242,"y":44}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, ok := remoteEvent(test.event)
			if !ok {
				t.Fatal("remoteEvent returned ok=false")
			}
			gotJSON, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("Marshal payload: %v", err)
			}
			var got, want any
			if err := json.Unmarshal(gotJSON, &got); err != nil {
				t.Fatalf("Unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatalf("Unmarshal want: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("payload = %s, want %s", gotJSON, test.want)
			}
		})
	}
}

func TestPostEventReturnsAck(t *testing.T) {
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", request.Method)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(body) != `{"delta":3,"index":1,"kind":"dial_rotate"}` {
			t.Fatalf("body = %s", body)
		}
		return jsonResponse(http.StatusOK, `{"ok":true,"events_seen":17,"message":"Dial 2 +3 received"}`), nil
	})

	ack, err := postEvent(context.Background(), client, "http://playground.test/event", map[string]any{
		"kind": "dial_rotate", "index": 1, "delta": 3,
	})
	if err != nil {
		t.Fatalf("postEvent: %v", err)
	}
	if ack != (eventAck{OK: true, EventsSeen: 17, Message: "Dial 2 +3 received"}) {
		t.Fatalf("ack = %+v", ack)
	}
	if got := lastEventMessage(ack.Message); got != "Dial 2 +3" {
		t.Fatalf("last message = %q", got)
	}
}

func TestPostEventReportsValidationFailure(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"error":"invalid index"}`), nil
	})

	_, err := postEvent(context.Background(), client, "http://playground.test/event", map[string]any{"kind": "key"})
	if err == nil || !strings.Contains(err.Error(), `status=400 error="invalid index"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestPostEventHonorsShortClientTimeout(t *testing.T) {
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		select {
		case <-time.After(100 * time.Millisecond):
			return jsonResponse(http.StatusOK, `{"ok":true,"events_seen":1,"message":"late"}`), nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	client.Timeout = 10 * time.Millisecond
	_, err := postEvent(context.Background(), client, "http://playground.test/event", map[string]any{"kind": "key"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testHTTPClient(roundTrip roundTripFunc) *http.Client {
	return &http.Client{Transport: roundTrip}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestApplyRemoteDemo(t *testing.T) {
	demo := remoteDemo{Command: "run_hardware_demo", Revision: 1, EventsSeen: 8}
	demo.Presentation.Theme = "paper"
	demo.Presentation.Title = "Demo Garden"
	demo.Presentation.Message = "Demo link established"
	demo.Presentation.Brightness = 70
	demo.LastEvent = &struct {
		Summary string `json:"summary"`
	}{Summary: "FLICK 189,46 → 242,44"}

	state := &demoState{brightness: 15}
	if err := applyRemoteDemo(state, demo); err != nil {
		t.Fatalf("applyRemoteDemo: %v", err)
	}
	if state.brightness != 70 || state.remoteTheme != "paper" || state.remoteEventsSeen != 8 {
		t.Fatalf("state = %+v", state)
	}
	if state.remoteTitle != "Demo Garden" || state.remoteMessage != "Demo link established" || state.remoteLast != "FLICK 189,46 -> 242,44" {
		t.Fatalf("presentation state = %+v", state)
	}
}

func TestFetchRemoteDemoRevision2BridgeFields(t *testing.T) {
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"command":"run_hardware_demo",
			"revision":2,
			"presentation":{"theme":"paper","title":"Demo Garden","message":"Demo link established","brightness":70},
			"key":{"index":0,"id":"demo_task","label":"Demo Task","state":"idle","bg":"#F6F5EE","fg":"#272C24"},
			"strip":{"page":1,"pages":3,"title":"Next meeting","lines":["Demo Task: running","25 runs · 25 ok · 0 err"]},
			"poll_ms":5000,
			"events_seen":25,
			"last_event":null
		}`), nil
	})

	demo, err := fetchRemoteDemo(context.Background(), client, "http://playground.test/demo")
	if err != nil {
		t.Fatalf("fetchRemoteDemo: %v", err)
	}
	if demo.Revision != 2 || demo.PollMS != 5000 || demo.EventsSeen != 25 {
		t.Fatalf("revision/poll/events = %d/%d/%d", demo.Revision, demo.PollMS, demo.EventsSeen)
	}
	if demo.Key == nil || demo.Key.Index != 0 || demo.Key.Label != "Demo Task" || demo.Key.State != "idle" ||
		demo.Key.BG != "#F6F5EE" || demo.Key.FG != "#272C24" || demo.Key.ID != "demo_task" {
		t.Fatalf("key = %+v", demo.Key)
	}
	if demo.Strip == nil || demo.Strip.Page != 1 || demo.Strip.Pages != 3 || demo.Strip.Title != "Next meeting" {
		t.Fatalf("strip = %+v", demo.Strip)
	}
	if len(demo.Strip.Lines) != 2 || demo.Strip.Lines[0] != "Demo Task: running" {
		t.Fatalf("strip lines = %v", demo.Strip.Lines)
	}
}

func TestPostEventAckCarriesServerState(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"ok":true,
			"events_seen":7,
			"message":"Key 0 down received - Demo Task started",
			"state":{
				"key":{"index":0,"id":"demo_task","label":"Demo Task","state":"running","bg":"#6FA25C","fg":"#F6F5EE"},
				"strip":{"page":0,"pages":3,"title":"Today","lines":["Demo Task: running","7 runs · 7 ok · 0 err"]}
			}
		}`), nil
	})

	ack, err := postEvent(context.Background(), client, "http://playground.test/event", map[string]any{"kind": "key", "index": 0, "pressed": true})
	if err != nil {
		t.Fatalf("postEvent: %v", err)
	}
	if ack.EventsSeen != 7 {
		t.Fatalf("events_seen = %d", ack.EventsSeen)
	}
	if ack.State == nil || ack.State.Key == nil || ack.State.Strip == nil {
		t.Fatalf("ack state missing: %+v", ack.State)
	}
	if ack.State.Key.State != "running" || ack.State.Key.BG != "#6FA25C" {
		t.Fatalf("ack key = %+v", ack.State.Key)
	}
	if ack.State.Strip.Lines[0] != "Demo Task: running" {
		t.Fatalf("ack strip lines = %v", ack.State.Strip.Lines)
	}
}

func TestFetchRemoteDemoParsesBackgroundFrames(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"command":"run_hardware_demo",
			"revision":2,
			"presentation":{"theme":"paper","title":"Demo Garden","message":"Demo link established","brightness":70},
			"key":{"index":0,"id":"demo_task","label":"Demo Task","state":"idle","bg":"#F6F5EE","fg":"#272C24"},
			"strip":{"page":0,"pages":3,"title":"Today","lines":["Demo Task: idle"]},
			"background":{"keys":[
				{"index":1,"label":"Pi 4","bg":"#272C24","fg":"#F6F5EE"},
				{"index":2,"label":"Pi Zero","bg":"#272C24","fg":"#F6F5EE"}
			]},
			"poll_ms":5000,
			"events_seen":3,
			"last_event":null
		}`), nil
	})

	demo, err := fetchRemoteDemo(context.Background(), client, "http://playground.test/demo")
	if err != nil {
		t.Fatalf("fetchRemoteDemo: %v", err)
	}
	if demo.Background == nil || len(demo.Background.Keys) != 2 {
		t.Fatalf("background = %+v", demo.Background)
	}
	if demo.Background.Keys[0].Index != 1 || demo.Background.Keys[0].Label != "Pi 4" ||
		demo.Background.Keys[0].BG != "#272C24" || demo.Background.Keys[0].FG != "#F6F5EE" {
		t.Fatalf("background key 0 = %+v", demo.Background.Keys[0])
	}
	if demo.Background.Keys[1].Index != 2 || demo.Background.Keys[1].Label != "Pi Zero" {
		t.Fatalf("background key 1 = %+v", demo.Background.Keys[1])
	}
}

func TestFetchRemoteDemoParsesKeyImages(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"command":"run_hardware_demo",
			"revision":2,
			"presentation":{"theme":"paper","title":"Demo Garden","message":"Demo link established","brightness":70},
			"key":{"index":0,"id":"demo_task","label":"Demo Task","state":"idle","bg":"#F6F5EE","fg":"#272C24",
				"image":{"revision":"sha256-aaa","mime_type":"image/png","data_b64":"cGl4ZWxz"}},
			"strip":{"page":0,"pages":3,"title":"Today","lines":["Demo Task: idle"]},
			"background":{"keys":[
				{"index":1,"label":"Pi 4","bg":"#272C24","fg":"#F6F5EE",
					"image":{"revision":"sha256-bbb","mime_type":"image/jpeg","data_b64":"anBlZw=="}},
				{"index":2,"label":"Pi Zero","bg":"#272C24","fg":"#F6F5EE"}
			]},
			"poll_ms":5000,
			"events_seen":3,
			"last_event":null
		}`), nil
	})

	demo, err := fetchRemoteDemo(context.Background(), client, "http://playground.test/demo")
	if err != nil {
		t.Fatalf("fetchRemoteDemo: %v", err)
	}
	if demo.Key == nil || demo.Key.Image == nil {
		t.Fatalf("key image missing: %+v", demo.Key)
	}
	if demo.Key.Image.Revision != "sha256-aaa" || demo.Key.Image.MimeType != "image/png" || demo.Key.Image.Data != "cGl4ZWxz" {
		t.Fatalf("key image = %+v", demo.Key.Image)
	}
	// The semantic fallback rides alongside the image.
	if demo.Key.Label != "Demo Task" || demo.Key.BG != "#F6F5EE" || demo.Key.FG != "#272C24" {
		t.Fatalf("key fallback = %+v", demo.Key)
	}
	if demo.Background.Keys[0].Image == nil || demo.Background.Keys[0].Image.Revision != "sha256-bbb" {
		t.Fatalf("background key 0 image = %+v", demo.Background.Keys[0].Image)
	}
	if demo.Background.Keys[1].Image != nil {
		t.Fatalf("background key 1 image = %+v, want none", demo.Background.Keys[1].Image)
	}
}

func TestPostEventAckStateCarriesKeyImage(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"ok":true,
			"events_seen":7,
			"message":"Key 0 down received - Demo Task started",
			"state":{
				"key":{"index":0,"id":"demo_task","label":"Demo Task","state":"running","bg":"#6FA25C","fg":"#F6F5EE",
					"image":{"revision":"sha256-ccc","mime_type":"image/png","data_b64":"cGl4ZWxz"}},
				"strip":{"page":0,"pages":3,"title":"Today","lines":["Demo Task: running"]}
			}
		}`), nil
	})

	ack, err := postEvent(context.Background(), client, "http://playground.test/event", map[string]any{"kind": "key", "index": 0, "pressed": true})
	if err != nil {
		t.Fatalf("postEvent: %v", err)
	}
	if ack.State == nil || ack.State.Key == nil || ack.State.Key.Image == nil {
		t.Fatalf("ack key image missing: %+v", ack.State)
	}
	if ack.State.Key.Image.Revision != "sha256-ccc" || ack.State.Key.Image.Data != "cGl4ZWxz" {
		t.Fatalf("ack key image = %+v", ack.State.Key.Image)
	}
}
