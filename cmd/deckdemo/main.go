package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

const (
	inputReadTimeout  = 50 * time.Millisecond
	reconnectInterval = time.Second
)

var displayModes = [...]string{"INPUT", "KEY TEST", "TOUCH TEST"}

type demoState struct {
	keys        [streamdeck.KeyCount]bool
	counter     int
	brightness  int
	selectedKey int
	mode        int
	latestInput string
	latestTouch *streamdeck.TouchEvent
}

type demoDeck interface {
	Info() (streamdeck.DeviceInfo, error)
	ReadEvents(time.Duration) (streamdeck.InputRead, error)
	SetBrightness(int) error
	SetKeyImage(int, image.Image) error
	SetTouchStripImage(image.Image) error
	Close() error
}

type openDeckFunc func() (demoDeck, error)

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("deckdemo: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	state := demoState{brightness: 70}
	return runDemo(ctx, &state, func() (demoDeck, error) {
		return streamdeck.Open()
	}, reconnectInterval)
}

func runDemo(ctx context.Context, state *demoState, open openDeckFunc, retryInterval time.Duration) error {
	for {
		if ctx.Err() != nil {
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		deck, err := open()
		if err != nil {
			log.Printf("device unavailable retry=%s error=%q", retryInterval, err)
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		info, setupErr := deck.Info()
		if setupErr == nil {
			setupErr = restoreDemo(deck, state)
		}
		if setupErr != nil {
			log.Printf("device setup failed retry=%s error=%q", retryInterval, setupErr)
			if closeErr := deck.Close(); closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		log.Printf("device connected vid=%04x pid=%04x product=%q", info.VendorID, info.ProductID, info.Product)
		log.Printf("output restored keys=1-8 strip=800x100 brightness=%d", state.brightness)

		connectionErr := runConnected(ctx, deck, state)
		closeErr := deck.Close()
		if ctx.Err() != nil {
			if closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		log.Printf("device disconnected retry=%s error=%q", retryInterval, connectionErr)
		if closeErr != nil {
			log.Printf("device close error=%q", closeErr)
		}
		if !waitForReconnect(ctx, retryInterval) {
			log.Print("device shutdown reason=interrupt")
			return nil
		}
	}
}

func runConnected(ctx context.Context, deck demoDeck, state *demoState) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		result, err := deck.ReadEvents(inputReadTimeout)
		if errors.Is(err, streamdeck.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		logInputDiagnostics(result)
		for _, event := range result.Events {
			if err := handleEvent(deck, state, event); err != nil {
				return err
			}
		}
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func restoreDemo(deck demoDeck, state *demoState) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("restore brightness: %w", err)
	}
	for index := range state.keys {
		if err := renderKey(deck, state, index); err != nil {
			return err
		}
	}
	if err := renderStrip(deck, state); err != nil {
		return err
	}
	return nil
}

func handleEvent(deck demoDeck, state *demoState, event streamdeck.Event) error {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		state.latestInput = fmt.Sprintf("KEY %d %s", event.Key+1, pressedLabel(event.Pressed))
		if event.Pressed {
			state.keys[event.Key] = !state.keys[event.Key]
			if err := renderKey(deck, state, event.Key); err != nil {
				return err
			}
		}
		return renderStrip(deck, state)

	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
		state.latestInput = fmt.Sprintf("D%d %+03d", event.Dial+1, event.Delta)
		switch event.Dial {
		case 0:
			state.counter += event.Delta
		case 1:
			next := clamp(state.brightness+event.Delta*5, 0, 100)
			if next != state.brightness {
				state.brightness = next
				if err := deck.SetBrightness(state.brightness); err != nil {
					return err
				}
			}
		case 2:
			previous := state.selectedKey
			state.selectedKey = wrap(state.selectedKey+event.Delta, streamdeck.KeyCount)
			if previous != state.selectedKey {
				if err := renderKey(deck, state, previous); err != nil {
					return err
				}
				if err := renderKey(deck, state, state.selectedKey); err != nil {
					return err
				}
			}
		case 3:
			state.mode = wrap(state.mode+event.Delta, len(displayModes))
		}
		return renderStrip(deck, state)

	case streamdeck.DialPressEvent:
		log.Printf("input dial dial=%d pressed=%t", event.Dial+1, event.Pressed)
		state.latestInput = fmt.Sprintf("D%d %s", event.Dial+1, pressedLabel(event.Pressed))
		if !event.Pressed {
			return renderStrip(deck, state)
		}
		switch event.Dial {
		case 0:
			state.counter = 0
		case 1:
			if state.brightness == 15 {
				state.brightness = 70
			} else {
				state.brightness = 15
			}
			if err := deck.SetBrightness(state.brightness); err != nil {
				return err
			}
		case 2:
			state.keys[state.selectedKey] = !state.keys[state.selectedKey]
			if err := renderKey(deck, state, state.selectedKey); err != nil {
				return err
			}
		case 3:
			state.mode = 0
		}
		return renderStrip(deck, state)

	case streamdeck.TouchEvent:
		logTouch(event)
		state.latestInput = touchInputSummary(event)
		state.latestTouch = &event
		return renderStrip(deck, state)

	default:
		return fmt.Errorf("unsupported normalized event %T", event)
	}
}

func renderKey(deck demoDeck, state *demoState, index int) error {
	img := render.Key(render.KeyView{Index: index, On: state.keys[index], Selected: index == state.selectedKey})
	if err := deck.SetKeyImage(index, img); err != nil {
		return fmt.Errorf("render key %d: %w", index+1, err)
	}
	return nil
}

func renderStrip(deck demoDeck, state *demoState) error {
	img := render.Strip(render.StripView{
		Counter: state.counter, Brightness: state.brightness,
		SelectedKey: state.selectedKey, SelectedOn: state.keys[state.selectedKey],
		Mode: displayModes[state.mode], LastInput: state.latestInput, Touch: state.latestTouch,
	})
	if err := deck.SetTouchStripImage(img); err != nil {
		return fmt.Errorf("render touch strip: %w", err)
	}
	return nil
}

func logInputDiagnostics(result streamdeck.InputRead) {
	for _, diagnostic := range result.Diagnostics {
		log.Printf("input diagnostic=%q", diagnostic)
	}
}

func logTouch(event streamdeck.TouchEvent) {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		log.Printf("input touch kind=%s x=%d y=%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		log.Printf("input touch kind=FLICK start=%d,%d end=%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	}
}

func touchInputSummary(event streamdeck.TouchEvent) string {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		return fmt.Sprintf("%s %d,%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		return fmt.Sprintf("FLICK %d,%d>%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	default:
		return "TOUCH"
	}
}

func pressedLabel(pressed bool) string {
	if pressed {
		return "DOWN"
	}
	return "UP"
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func wrap(value, size int) int {
	return ((value % size) + size) % size
}
