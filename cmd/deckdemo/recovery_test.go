package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// statusLog records every device status the client reports.
type statusLog struct {
	mu       sync.Mutex
	statuses []deviceStatus
	err      error
}

func (l *statusLog) report(_ context.Context, status deviceStatus) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statuses = append(l.statuses, status)
	return l.err
}

func (l *statusLog) devices() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	devices := make([]string, 0, len(l.statuses))
	for _, status := range l.statuses {
		devices = append(devices, status.Device)
	}
	return devices
}

func (l *statusLog) first(device string) (deviceStatus, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, status := range l.statuses {
		if status.Device == device {
			return status, true
		}
	}
	return deviceStatus{}, false
}

// stuckDeck opens and identifies itself but rejects every write.
func stuckDeck() *fakeDemoDeck {
	deck := newFakeDemoDeck()
	deck.brightnessErr = errors.New("I/O Timeout")
	return deck
}

// identitySource is a deck identity that a test can change mid-run.
type identitySource struct {
	mu sync.Mutex
	id string
}

func (s *identitySource) get() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id, nil
}

func (s *identitySource) set(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id
}

func TestRunDemoRestsStuckDeckUntilItIsReplugged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy := newFakeDemoDeck()
	healthy.onStripImage = cancel
	identity := &identitySource{id: "DevSrvsID:1"}
	var mu sync.Mutex
	opens := 0
	open := func() (demoDeck, error) {
		mu.Lock()
		defer mu.Unlock()
		opens++
		if id, _ := identity.get(); id == "DevSrvsID:1" {
			return stuckDeck(), nil
		}
		return healthy, nil
	}
	openCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return opens
	}
	statuses := &statusLog{}
	done := make(chan error, 1)
	go func() {
		done <- runDemo(ctx, &demoState{brightness: 50}, demoEnv{
			open:          open,
			retryInterval: time.Millisecond,
			identity:      identity.get,
			report:        statuses.report,
			stuckAfter:    3,
			restSchedule:  []time.Duration{time.Hour},
		})
	}()

	waitUntil(t, "stuck report", func() bool { _, ok := statuses.first(deviceStuck); return ok })
	stuck, _ := statuses.first(deviceStuck)
	if stuck.Failures != 3 || stuck.Detail != "restore brightness: I/O Timeout" {
		t.Fatalf("stuck report = %+v, want 3 failures and the write error", stuck)
	}
	// The rest must leave the deck alone: no open, so no write.
	time.Sleep(30 * time.Millisecond)
	if got := openCount(); got != 3 {
		t.Fatalf("opens during rest = %d, want the 3 that failed", got)
	}

	// A replug gives the deck a new identity, which ends the hour-long rest.
	identity.set("DevSrvsID:2")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDemo: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rest did not end when the deck was replugged")
	}
	if len(healthy.brightnessCalls) != 1 || healthy.stripCount() != 1 {
		t.Fatalf("replugged deck was not restored: brightness=%v strips=%d", healthy.brightnessCalls, healthy.stripCount())
	}
	devices := statuses.devices()
	if devices[len(devices)-1] != deviceConnected {
		t.Fatalf("reported devices = %v, want the last to be connected", devices)
	}
}

func TestRunDemoRetriesBriefSetupFailuresWithoutResting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy := newFakeDemoDeck()
	healthy.onStripImage = cancel
	// Two failures, an absent deck, two more failures: never three in a row.
	sequence := []func() (demoDeck, error){
		func() (demoDeck, error) { return stuckDeck(), nil },
		func() (demoDeck, error) { return stuckDeck(), nil },
		func() (demoDeck, error) { return nil, errors.New("device absent") },
		func() (demoDeck, error) { return stuckDeck(), nil },
		func() (demoDeck, error) { return stuckDeck(), nil },
		func() (demoDeck, error) { return healthy, nil },
	}
	opens := 0
	open := func() (demoDeck, error) {
		if opens >= len(sequence) {
			return nil, errors.New("unexpected extra open")
		}
		next := sequence[opens]
		opens++
		return next()
	}
	statuses := &statusLog{}
	err := runDemo(ctx, &demoState{brightness: 50}, demoEnv{
		open:          open,
		retryInterval: time.Millisecond,
		report:        statuses.report,
		stuckAfter:    3,
		restSchedule:  []time.Duration{time.Hour},
	})
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	if opens != len(sequence) {
		t.Fatalf("opens = %d, want %d", opens, len(sequence))
	}
	if _, rested := statuses.first(deviceStuck); rested {
		t.Fatalf("brief failures were reported as stuck: %v", statuses.devices())
	}
	if got := statuses.devices(); len(got) != 2 || got[0] != deviceUnavailable || got[1] != deviceConnected {
		t.Fatalf("reported devices = %v, want [unavailable connected]", got)
	}
}

func TestRestStuckDeckFollowsScheduleAndProbes(t *testing.T) {
	env := demoEnv{
		retryInterval: time.Millisecond,
		restSchedule:  []time.Duration{20 * time.Millisecond, 60 * time.Millisecond},
	}.withDefaults()
	cause := errors.New("I/O Timeout")
	for _, test := range []struct {
		rests int
		want  time.Duration
	}{
		{rests: 0, want: 20 * time.Millisecond},
		{rests: 1, want: 60 * time.Millisecond},
		{rests: 7, want: 60 * time.Millisecond}, // the last entry repeats
	} {
		started := time.Now()
		outcome := restStuckDeck(context.Background(), env, &statusReporter{}, 5, test.rests, cause)
		if outcome != restElapsed {
			t.Fatalf("rests=%d outcome = %v, want restElapsed", test.rests, outcome)
		}
		if elapsed := time.Since(started); elapsed < test.want {
			t.Fatalf("rests=%d rested %s, want at least %s", test.rests, elapsed, test.want)
		}
	}
}

func TestRestStuckDeckStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := demoEnv{retryInterval: time.Millisecond, restSchedule: []time.Duration{time.Hour}}.withDefaults()
	if outcome := restStuckDeck(ctx, env, &statusReporter{}, 5, 0, errors.New("I/O Timeout")); outcome != restInterrupted {
		t.Fatalf("outcome = %v, want restInterrupted", outcome)
	}
}

func TestRestStuckDeckRepeatsStatusOnHeartbeat(t *testing.T) {
	statuses := &statusLog{}
	env := demoEnv{
		retryInterval: time.Millisecond,
		restSchedule:  []time.Duration{80 * time.Millisecond},
		heartbeat:     20 * time.Millisecond,
	}.withDefaults()
	reporter := &statusReporter{report: statuses.report, heartbeat: env.heartbeat}
	restStuckDeck(context.Background(), env, reporter, 5, 0, errors.New("I/O Timeout"))
	got := statuses.devices()
	if len(got) < 2 || len(got) > 6 {
		t.Fatalf("stuck reports during an 80ms rest with a 20ms heartbeat = %d, want a handful", len(got))
	}
	for _, device := range got {
		if device != deviceStuck {
			t.Fatalf("reported devices = %v, want only stuck", got)
		}
	}
}

func TestStatusReporterSendsChangesAndHeartbeats(t *testing.T) {
	statuses := &statusLog{}
	reporter := &statusReporter{report: statuses.report, heartbeat: time.Hour}
	ctx := context.Background()

	reporter.update(ctx, deviceStatus{Device: deviceUnavailable})
	reporter.update(ctx, deviceStatus{Device: deviceUnavailable})
	reporter.update(ctx, deviceStatus{Device: deviceConnected})
	if got := statuses.devices(); len(got) != 2 || got[0] != deviceUnavailable || got[1] != deviceConnected {
		t.Fatalf("reports = %v, want one per change", got)
	}

	// An unchanged status repeats once the heartbeat has passed.
	reporter.attemptedAt = time.Now().Add(-2 * time.Hour)
	reporter.update(ctx, deviceStatus{Device: deviceConnected})
	if got := statuses.devices(); len(got) != 3 {
		t.Fatalf("reports after heartbeat = %v, want a repeat", got)
	}
}

func TestStatusReporterRetriesFailedDeliveryOnHeartbeatOnly(t *testing.T) {
	statuses := &statusLog{err: errors.New("status=404")}
	reporter := &statusReporter{report: statuses.report, heartbeat: time.Hour}
	ctx := context.Background()

	reporter.update(ctx, deviceStatus{Device: deviceStuck})
	reporter.update(ctx, deviceStatus{Device: deviceStuck})
	if got := statuses.devices(); len(got) != 1 {
		t.Fatalf("attempts against a failing controller = %d, want 1 until the heartbeat", len(got))
	}
	if !reporter.failing {
		t.Fatal("failed delivery was not remembered")
	}

	statuses.mu.Lock()
	statuses.err = nil
	statuses.mu.Unlock()
	reporter.attemptedAt = time.Now().Add(-2 * time.Hour)
	reporter.update(ctx, deviceStatus{Device: deviceStuck})
	if got := statuses.devices(); len(got) != 2 || reporter.failing {
		t.Fatalf("heartbeat retry: attempts=%d failing=%t, want 2 and recovered", len(got), reporter.failing)
	}
}

func TestStatusReporterWithoutControllerIsSilent(t *testing.T) {
	reporter := &statusReporter{}
	reporter.update(context.Background(), deviceStatus{Device: deviceStuck})
}

func TestPostDeviceStatus(t *testing.T) {
	var got deviceStatus
	var method, path, contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, contentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode status body %q: %v", body, err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	status := deviceStatus{Device: deviceStuck, Detail: "restore brightness: I/O Timeout", Failures: 5}
	if err := postDeviceStatus(context.Background(), server.Client(), statusURL(server.URL+"/deck"), status); err != nil {
		t.Fatalf("postDeviceStatus: %v", err)
	}
	if method != http.MethodPost || path != "/deck/status" || contentType != "application/json" {
		t.Fatalf("request = %s %s content-type=%q", method, path, contentType)
	}
	if got != status {
		t.Fatalf("body = %+v, want %+v", got, status)
	}
}

func TestPostDeviceStatusReportsControllerWithoutEndpoint(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	err := postDeviceStatus(context.Background(), server.Client(), server.URL+"/status", deviceStatus{Device: deviceConnected})
	if err == nil {
		t.Fatal("a 404 from the controller was treated as delivered")
	}
}

func TestStatusURL(t *testing.T) {
	if got := statusURL(""); got != "" {
		t.Fatalf("statusURL without a controller = %q, want empty", got)
	}
	if got := statusURL("http://127.0.0.1:1/deck"); got != "http://127.0.0.1:1/deck/status" {
		t.Fatalf("statusURL = %q", got)
	}
}
