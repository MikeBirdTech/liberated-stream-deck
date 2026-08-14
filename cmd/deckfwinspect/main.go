// deckfwinspect validates and reassembles an offline Stream Deck Plus
// firmware-update HID capture. It never opens or writes to a USB device.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/firmwarecapture"
)

func main() {
	capturePath := flag.String("capture", "", "raw concatenated 1024-byte reports, or - for stdin")
	extractPath := flag.String("extract", "", "optional path for the validated reassembled payload")
	expectedHash := flag.String("expect-sha256", "", "optional expected payload SHA-256")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: deckfwinspect -capture <reports.bin|-> [-expect-sha256 <hex>] [-extract <payload.bin>]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *capturePath == "" {
		flag.Usage()
		os.Exit(2)
	}

	input, closeInput, err := openInput(*capturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open capture:", err)
		os.Exit(1)
	}
	defer closeInput()

	capture, err := firmwarecapture.Parse(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid capture:", err)
		os.Exit(1)
	}
	if *expectedHash != "" {
		want, err := parseSHA256(*expectedHash)
		if err != nil {
			fmt.Fprintln(os.Stderr, "expected SHA-256:", err)
			os.Exit(2)
		}
		if capture.SHA256 != want {
			fmt.Fprintf(os.Stderr, "payload SHA-256 mismatch: got %x, want %x\n", capture.SHA256, want)
			os.Exit(1)
		}
	}

	if *extractPath != "" {
		if err := os.WriteFile(*extractPath, capture.Payload, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write payload:", err)
			os.Exit(1)
		}
	}

	fmt.Printf("reports: %d\n", capture.ReportCount)
	fmt.Printf("outer_blocks: %d\n", capture.OuterBlockCount)
	fmt.Printf("payload_bytes: %d\n", len(capture.Payload))
	fmt.Printf("payload_sha256: %x\n", capture.SHA256)
	if *extractPath != "" {
		fmt.Printf("extracted: %s\n", *extractPath)
	}
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}

func parseSHA256(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return result, err
	}
	if len(decoded) != sha256.Size {
		return result, fmt.Errorf("got %d bytes, want %d", len(decoded), sha256.Size)
	}
	copy(result[:], decoded)
	return result, nil
}
