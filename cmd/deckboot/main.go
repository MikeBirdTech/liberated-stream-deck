// deckboot persists a power-on frame to a connected Stream Deck Plus using
// the undocumented 0x09 boot-frame channel (see the Protocol section of the
// README). A useful tool while auditing device capabilities: it exercises the
// same path ESDCommUploadLogoTask uses in the official app. The -lcd flag
// instead targets the documented 0x08 full-screen LCD channel, which is
// display-only (volatile; does not survive a power cycle).
//
// Usage:
//
//	deckboot -image boot.png          upload an image (PNG/JPEG, any size)
//	deckboot -color 5594f6            upload a solid color as RRGGBB
//	deckboot -lcd screen.png          paint the full LCD (display-only)
//	deckboot -partial 40,20,region.png
//	                                  paint a region of the touch window
//	deckboot -logo                    show the boot logo right now
//	deckboot -filllcd 003366          fill the whole LCD with a color
//	deckboot -fillkey 4,ff8800        fill one key (index 0-7) with a color
//	deckboot -sleep 300               set idle time before sleep, seconds (0 disables)
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strconv"
	"strings"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

func parseRGB(hex string) ([3]uint8, error) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return [3]uint8{}, fmt.Errorf("color must be RRGGBB, got %s", hex)
	}
	var c [3]uint8
	for i := 0; i < 3; i++ {
		if _, err := fmt.Sscanf(hex[i*2:i*2+2], "%02x", &c[i]); err != nil {
			return [3]uint8{}, fmt.Errorf("parse %q: %w", hex[i*2:i*2+2], err)
		}
	}
	return c, nil
}

func main() {
	imagePath := flag.String("image", "", "path to a PNG/JPEG boot image")
	colorHex := flag.String("color", "", "solid color as RRGGBB")
	lcdPath := flag.String("lcd", "", "path to a PNG/JPEG full-LCD image (display-only)")
	partial := flag.String("partial", "x,y,file", "paint a touch-window region; X,Y top-left, PNG/JPEG file")
	logo := flag.Bool("logo", false, "forcibly display the boot logo")
	fillLCDHex := flag.String("filllcd", "", "fill the whole LCD with a color as RRGGBB")
	fillKey := flag.String("fillkey", "", "fill one key with a color as <index>,<RRGGBB>")
	sleepSeconds := flag.Int("sleep", -1, "set idle time before sleep in seconds (0 disables)")
	flag.Parse()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: deckboot -image <file> | -color <RRGGBB> | -lcd <file> | -partial <x>,<y>,<file> | -logo | -filllcd <RRGGBB> | -fillkey <index>,<RRGGBB> | -sleep <seconds>\n")
		flag.PrintDefaults()
	}
	selected := 0
	for _, set := range []bool{*imagePath != "", *colorHex != "", *lcdPath != "", *partial != "x,y,file", *logo, *fillLCDHex != "", *fillKey != "", *sleepSeconds >= 0} {
		if set {
			selected++
		}
	}
	if selected == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if selected > 1 {
		fmt.Fprintln(os.Stderr, "choose exactly one of -image, -color, -lcd, -partial, -logo, -filllcd, -fillkey, or -sleep")
		os.Exit(2)
	}

	var fillKeyIndex int
	var fillKeyColor [3]uint8
	if *fillKey != "" {
		parts := strings.Split(*fillKey, ",")
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "fillkey must be <index>,<RRGGBB>, got", *fillKey)
			os.Exit(2)
		}
		var err error
		fillKeyIndex, err = strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || fillKeyIndex < 0 || fillKeyIndex >= streamdeck.KeyCount {
			fmt.Fprintf(os.Stderr, "fillkey index must be 0..%d, got %q\n", streamdeck.KeyCount-1, parts[0])
			os.Exit(2)
		}
		fillKeyColor, err = parseRGB(parts[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "fillkey:", err)
			os.Exit(2)
		}
	}

	var partialX, partialY int
	var partialPath string
	if *partial != "x,y,file" {
		parts := strings.Split(*partial, ",")
		if len(parts) != 3 {
			fmt.Fprintln(os.Stderr, "partial must be <x>,<y>,<file>, got", *partial)
			os.Exit(2)
		}
		var err error
		partialX, err = strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || partialX < 0 {
			fmt.Fprintln(os.Stderr, "partial x must be a non-negative integer, got", parts[0])
			os.Exit(2)
		}
		partialY, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || partialY < 0 {
			fmt.Fprintln(os.Stderr, "partial y must be a non-negative integer, got", parts[1])
			os.Exit(2)
		}
		partialPath = parts[2]
	}

	var img image.Image
	switch {
	case *imagePath != "" || *lcdPath != "" || partialPath != "":
		path := *imagePath
		if path == "" {
			path = *lcdPath
		}
		if path == "" {
			path = partialPath
		}
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open:", err)
			os.Exit(1)
		}
		defer f.Close()
		img, _, err = image.Decode(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decode:", err)
			os.Exit(1)
		}
	case *colorHex != "":
		c, err := parseRGB(*colorHex)
		if err != nil {
			fmt.Fprintln(os.Stderr, "color:", err)
			os.Exit(2)
		}
		img = image.NewNRGBA(image.Rect(0, 0, streamdeck.BootImageWidth, streamdeck.BootImageHeight))
		fill := color.NRGBA{R: c[0], G: c[1], B: c[2], A: 255}
		m := img.(*image.NRGBA)
		for y := 0; y < streamdeck.BootImageHeight; y++ {
			for x := 0; x < streamdeck.BootImageWidth; x++ {
				m.SetNRGBA(x, y, fill)
			}
		}
	}

	deck, err := streamdeck.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "open deck:", err)
		os.Exit(1)
	}
	defer deck.Close()
	if *logo {
		if err := deck.ShowLogo(); err != nil {
			fmt.Fprintln(os.Stderr, "show logo:", err)
			os.Exit(1)
		}
		fmt.Println("boot logo displayed")
		return
	}
	if *fillLCDHex != "" {
		c, err := parseRGB(*fillLCDHex)
		if err != nil {
			fmt.Fprintln(os.Stderr, "filllcd:", err)
			os.Exit(2)
		}
		if err := deck.FillLCD(c[0], c[1], c[2]); err != nil {
			fmt.Fprintln(os.Stderr, "fill LCD:", err)
			os.Exit(1)
		}
		fmt.Printf("LCD filled with #%02x%02x%02x (volatile)\n", c[0], c[1], c[2])
		return
	}
	if *fillKey != "" {
		if err := deck.FillKey(fillKeyIndex, fillKeyColor[0], fillKeyColor[1], fillKeyColor[2]); err != nil {
			fmt.Fprintln(os.Stderr, "fill key:", err)
			os.Exit(1)
		}
		fmt.Printf("key %d filled with #%02x%02x%02x (volatile)\n", fillKeyIndex, fillKeyColor[0], fillKeyColor[1], fillKeyColor[2])
		return
	}
	if *sleepSeconds >= 0 {
		if err := deck.SetSleepDuration(*sleepSeconds); err != nil {
			fmt.Fprintln(os.Stderr, "set sleep duration:", err)
			os.Exit(1)
		}
		fmt.Printf("sleep duration set to %d s (persisted on device)\n", *sleepSeconds)
		return
	}
	if *lcdPath != "" {
		if err := deck.SetLCDImage(streamdeck.ScaleImage(img, streamdeck.LCDImageWidth, streamdeck.LCDImageHeight)); err != nil {
			fmt.Fprintln(os.Stderr, "lcd paint:", err)
			os.Exit(1)
		}
		fmt.Printf("full LCD painted (%dx%d source, display-only; it will not survive a power cycle)\n", img.Bounds().Dx(), img.Bounds().Dy())
		return
	}
	if partialPath != "" {
		if err := deck.SetPartialWindowImage(partialX, partialY, img); err != nil {
			fmt.Fprintln(os.Stderr, "partial paint:", err)
			os.Exit(1)
		}
		fmt.Printf("partial window %dx%d painted at (%d,%d), display-only\n", img.Bounds().Dx(), img.Bounds().Dy(), partialX, partialY)
		return
	}
	if err := deck.UploadBootImage(img); err != nil {
		fmt.Fprintln(os.Stderr, "upload:", err)
		os.Exit(1)
	}
	fmt.Printf("boot frame persisted (%dx%d source); it will show at the next power-on\n", img.Bounds().Dx(), img.Bounds().Dy())
}
