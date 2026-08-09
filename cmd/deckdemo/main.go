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
		return fmt.Errorf(
			"no Stream Deck Plus found at %04x:%04x",
			streamdeck.VendorID,
			streamdeck.ProductID,
		)
	}
	for index, device := range devices {
		log.Printf(
			"device enumerate index=%d vid=%04x pid=%04x product=%q serial=%q interface=%d usage=%04x:%04x",
			index,
			device.VendorID,
			device.ProductID,
			device.Product,
			device.Serial,
			device.Interface,
			device.UsagePage,
			device.Usage,
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
	log.Printf(
		"device open vid=%04x pid=%04x product=%q serial=%q",
		info.VendorID,
		info.ProductID,
		info.Product,
		info.Serial,
	)

	if err := deck.SetKeyImage(0, render.OrientationKey()); err != nil {
		return fmt.Errorf("display generated orientation image on key 1: %w", err)
	}
	log.Print("output key=1 image=orientation-test expected=\"red top; blue bottom; green left; yellow right; white arrow up\"")
	log.Print("input note=\"first valid key report establishes baseline without transitions; if necessary, press/release key 1 twice\"")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			log.Print("device shutdown reason=interrupt")
			return nil
		default:
		}

		result, err := deck.ReadKeyEvents(inputReadTimeout)
		if errors.Is(err, streamdeck.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		if result.Baseline != nil {
			log.Printf("input key baseline pressed=%s", pressedKeys(*result.Baseline))
		}
		for _, event := range result.Events {
			log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		}
	}
}

func pressedKeys(snapshot streamdeck.KeySnapshot) string {
	pressed := make([]string, 0, streamdeck.KeyCount)
	for index, isPressed := range snapshot.Pressed {
		if isPressed {
			pressed = append(pressed, fmt.Sprintf("%d", index+1))
		}
	}
	if len(pressed) == 0 {
		return "none"
	}
	return strings.Join(pressed, ",")
}
