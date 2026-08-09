package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

const (
	demoEndpointURL  = "http://controller:9999/api/controller"
	eventEndpointURL = "http://controller:9999/api/controller/event"
)

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
	EventsSeen int `json:"events_seen"`
	LastEvent  *struct {
		Summary string `json:"summary"`
	} `json:"last_event"`
}

type eventAck struct {
	OK         bool   `json:"ok"`
	EventsSeen int    `json:"events_seen"`
	Message    string `json:"message"`
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

func postEventAsync(ctx context.Context, payload map[string]any, results chan<- eventPostResult) {
	go func() {
		ack, err := postEvent(ctx, demoHTTPClient, eventEndpointURL, payload)
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
