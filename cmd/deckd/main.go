package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/controlapi"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const defaultListenAddress = "127.0.0.1:28484"

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("deckd: %v", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := flag.String("listen", environmentOr("LIBERATED_STREAM_DECK_LISTEN", defaultListenAddress), "local HTTP listen address")
	modelFlag := flag.String("model", environmentOr("LIBERATED_STREAM_DECK_MODEL", "plus"), "device model: plus or mini")
	flag.Parse()

	model, err := parseModel(*modelFlag)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listenAddress, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	manager := controlapi.NewManager(model)
	managerDone := make(chan error, 1)
	go func() { managerDone <- manager.Run(ctx) }()

	server := &http.Server{
		Handler:           controlapi.NewHandler(manager),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	log.Printf("control API listening=%s model=%s version=%s", listener.Addr(), *modelFlag, controlapi.APIVersion)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-managerDone; err != nil {
			return err
		}
		return nil
	case err := <-managerDone:
		stop()
		_ = server.Close()
		return err
	case err := <-serverDone:
		stop()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve control API: %w", err)
	}
}

func parseModel(value string) (streamdeck.Model, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plus", "stream_deck_plus":
		return streamdeck.ModelPlus, nil
	case "mini", "stream_deck_mini":
		return streamdeck.ModelMini, nil
	default:
		return streamdeck.ModelUnknown, fmt.Errorf("unsupported model %q: use plus or mini", value)
	}
}

func environmentOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
