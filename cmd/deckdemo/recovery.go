package main

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

// Device recovery. A deck can stop accepting writes while it still enumerates
// and opens (see "Stuck deck recovery" in the README). Hammering it with a
// write every few seconds never cleared that in the field, so once a run of
// setups has failed the client stops touching the deck, tells the controller,
// and rests. An unplug/replug is the recovery known to work, so a rest ends
// the moment the deck's USB identity changes.

const (
	// stuckSetupFailures is how many device setups in a row must fail before
	// the deck is treated as stuck. Transient write failures clear within a
	// few one-second retries; a stuck deck fails every one of them.
	stuckSetupFailures = 5
	// statusHeartbeatInterval repeats an unchanged device status so the
	// controller can tell a quiet client from a dead one.
	statusHeartbeatInterval = 10 * time.Second
)

// stuckRestSchedule is how long the deck is left alone before each probe of a
// stuck episode; the last entry repeats.
var stuckRestSchedule = []time.Duration{3 * time.Minute, 6 * time.Minute, 12 * time.Minute, 15 * time.Minute}

// Device states reported to the controller.
const (
	deviceConnected   = "connected"   // open and painted
	deviceUnavailable = "unavailable" // no supported deck could be opened
	deviceStuck       = "stuck"       // opens, but writes keep failing
)

// deviceStatus is what the client can currently do with the deck.
type deviceStatus struct {
	Device   string `json:"device"`
	Detail   string `json:"detail,omitempty"`
	Failures int    `json:"failures,omitempty"`
}

type reportStatusFunc func(context.Context, deviceStatus) error

// deckIdentityFunc names the supported decks on USB without opening them.
type deckIdentityFunc func() (string, error)

// demoEnv is everything runDemo needs from outside the process.
type demoEnv struct {
	open          openDeckFunc
	fetchDemo     fetchDemoFunc // nil: no controller configured
	retryInterval time.Duration

	// identity changes whenever a deck is unplugged, replugged, or
	// power-cycled. Nil means a rest can only end on its timer.
	identity deckIdentityFunc
	// report delivers the device status to the controller. Nil means there is
	// nobody to tell.
	report reportStatusFunc

	// Zero values select stuckSetupFailures, stuckRestSchedule, and
	// statusHeartbeatInterval.
	stuckAfter   int
	restSchedule []time.Duration
	heartbeat    time.Duration
}

func (e demoEnv) withDefaults() demoEnv {
	if e.stuckAfter <= 0 {
		e.stuckAfter = stuckSetupFailures
	}
	if len(e.restSchedule) == 0 {
		e.restSchedule = stuckRestSchedule
	}
	if e.heartbeat <= 0 {
		e.heartbeat = statusHeartbeatInterval
	}
	return e
}

// connectedDeckIdentity lists the HID paths of every supported deck. A path
// names one enumeration of one device, so the same deck gets a new one each
// time it reappears on USB.
func connectedDeckIdentity() (string, error) {
	decks, err := streamdeck.List()
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(decks))
	for _, deck := range decks {
		paths = append(paths, deck.Path)
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n"), nil
}

// statusReporter tells the controller the device status when it changes and
// repeats it on the heartbeat. Delivery is best effort: a controller that is
// down or predates the status endpoint is logged once and retried on the
// heartbeat, never treated as a device problem.
type statusReporter struct {
	report    reportStatusFunc
	heartbeat time.Duration

	attempted   string
	attemptedAt time.Time
	failing     bool
}

func (r *statusReporter) update(ctx context.Context, status deviceStatus) {
	if r.report == nil {
		return
	}
	if status.Device == r.attempted && time.Since(r.attemptedAt) < r.heartbeat {
		return
	}
	r.attempted, r.attemptedAt = status.Device, time.Now()
	if err := r.report(ctx, status); err != nil {
		if !r.failing && ctx.Err() == nil {
			log.Printf("device status report error=%q", err)
			r.failing = true
		}
		return
	}
	if r.failing {
		log.Printf("device status report accepted device=%s", status.Device)
		r.failing = false
	}
}

type restOutcome int

const (
	restElapsed     restOutcome = iota // time to probe the deck again
	restDeckChanged                    // the deck left or rejoined USB
	restInterrupted                    // shutting down
)

// restStuckDeck leaves a stuck deck alone for the next interval of the rest
// schedule. Nothing is written to the deck and it is not opened; only USB
// enumeration is watched, so an unplug/replug ends the rest immediately.
func restStuckDeck(ctx context.Context, env demoEnv, status *statusReporter, failures, rests int, cause error) restOutcome {
	rest := env.restSchedule[min(rests, len(env.restSchedule)-1)]
	log.Printf("device stuck failures=%d rest=%s error=%q (writes paused; unplug and replug the deck to recover now)", failures, rest, cause)
	report := deviceStatus{Device: deviceStuck, Detail: cause.Error(), Failures: failures}
	status.update(ctx, report)

	var before string
	watching := false
	if env.identity != nil {
		if id, err := env.identity(); err == nil {
			before, watching = id, true
		}
	}

	deadline := time.NewTimer(rest)
	defer deadline.Stop()
	tick := time.NewTicker(max(env.retryInterval, time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return restInterrupted
		case <-deadline.C:
			log.Printf("device stuck rest=%s elapsed; probing", rest)
			return restElapsed
		case <-tick.C:
			if watching {
				if id, err := env.identity(); err == nil && id != before {
					log.Print("device stuck rest ended: deck left or rejoined USB")
					return restDeckChanged
				}
			}
			status.update(ctx, report)
		}
	}
}
