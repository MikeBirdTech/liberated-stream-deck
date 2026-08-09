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
