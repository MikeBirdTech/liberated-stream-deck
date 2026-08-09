package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

const inputReadTimeout = 50 * time.Millisecond

var displayModes = [...]string{"INPUT", "KEY TEST", "TOUCH TEST"}

type demoState struct {
	keys        [streamdeck.KeyCount]bool
	counter     int
	brightness  int
	selectedKey int
	mode        int
	latestTouch *streamdeck.TouchEvent
}

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("deckdemo: %v", err)
		os.Exit(1)
	}
}

func run() error {
	devices, err := streamdeck.List()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return fmt.Errorf("no Stream Deck Plus found at %04x:%04x", streamdeck.VendorID, streamdeck.ProductID)
	}
	for index, device := range devices {
		log.Printf(
			"device enumerate index=%d vid=%04x pid=%04x product=%q serial=%q interface=%d usage=%04x:%04x",
			index, device.VendorID, device.ProductID, device.Product, device.Serial,
			device.Interface, device.UsagePage, device.Usage,
		)
	}

	deck, err := streamdeck.Open()
	if err != nil {
		return err
	}
	defer func() {
		if err := deck.Close(); err != nil {
			log.Printf("device close error=%q", err)
		}
	}()

	info, err := deck.Info()
	if err != nil {
		return err
	}
	log.Printf("device open vid=%04x pid=%04x product=%q serial=%q", info.VendorID, info.ProductID, info.Product, info.Serial)

	state := demoState{brightness: 70}
	if err := initializeDemo(deck, &state); err != nil {
		return err
	}
	log.Print("output initialized keys=1-8 strip=800x100 brightness=70")
	log.Print("input note=\"first valid key and dial-button snapshots establish independent baselines without transitions\"")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			log.Print("device shutdown reason=interrupt")
			return nil
		default:
		}

		result, err := deck.ReadEvents(inputReadTimeout)
		if errors.Is(err, streamdeck.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		logInputMetadata(result)
		for _, event := range result.Events {
			if err := handleEvent(deck, &state, event); err != nil {
				return err
			}
		}
	}
}

func initializeDemo(deck *streamdeck.Deck, state *demoState) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("initialize brightness: %w", err)
	}
	for index := range state.keys {
		if err := renderKey(deck, state, index); err != nil {
			return err
		}
	}
	return renderStrip(deck, state)
}

func handleEvent(deck *streamdeck.Deck, state *demoState, event streamdeck.Event) error {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		if !event.Pressed {
			return nil
		}
		state.keys[event.Key] = !state.keys[event.Key]
		return renderKey(deck, state, event.Key)

	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
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
		if !event.Pressed {
			return nil
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
		state.latestTouch = &event
		return renderStrip(deck, state)

	default:
		return fmt.Errorf("unsupported normalized event %T", event)
	}
}

func renderKey(deck *streamdeck.Deck, state *demoState, index int) error {
	img := render.Key(render.KeyView{Index: index, On: state.keys[index], Selected: index == state.selectedKey})
	if err := deck.SetKeyImage(index, img); err != nil {
		return fmt.Errorf("render key %d: %w", index+1, err)
	}
	return nil
}

func renderStrip(deck *streamdeck.Deck, state *demoState) error {
	img := render.Strip(render.StripView{
		Counter: state.counter, Brightness: state.brightness,
		SelectedKey: state.selectedKey, Mode: displayModes[state.mode], Touch: state.latestTouch,
	})
	if err := deck.SetTouchStripImage(img); err != nil {
		return fmt.Errorf("render touch strip: %w", err)
	}
	return nil
}

func logInputMetadata(result streamdeck.InputRead) {
	if result.KeyBaseline != nil {
		log.Printf("input key baseline pressed=%s", pressedKeys(*result.KeyBaseline))
	}
	if result.DialBaseline != nil {
		log.Printf("input dial baseline pressed=%s", pressedDials(*result.DialBaseline))
	}
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

func pressedKeys(snapshot streamdeck.KeySnapshot) string {
	pressed := make([]string, 0, streamdeck.KeyCount)
	for index, isPressed := range snapshot.Pressed {
		if isPressed {
			pressed = append(pressed, fmt.Sprintf("%d", index+1))
		}
	}
	return joinedOrNone(pressed)
}

func pressedDials(snapshot streamdeck.DialButtonSnapshot) string {
	pressed := make([]string, 0, streamdeck.DialCount)
	for index, isPressed := range snapshot.Pressed {
		if isPressed {
			pressed = append(pressed, fmt.Sprintf("%d", index+1))
		}
	}
	return joinedOrNone(pressed)
}

func joinedOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func wrap(value, size int) int {
	return ((value % size) + size) % size
}
