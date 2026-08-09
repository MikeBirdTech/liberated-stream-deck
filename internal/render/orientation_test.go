package render

import (
	"image/color"
	"testing"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

func TestOrientationKeyDimensionsAndDirections(t *testing.T) {
	img := OrientationKey()
	if got := img.Bounds().Dx(); got != streamdeck.KeyImageWidth {
		t.Fatalf("width = %d, want %d", got, streamdeck.KeyImageWidth)
	}
	if got := img.Bounds().Dy(); got != streamdeck.KeyImageHeight {
		t.Fatalf("height = %d, want %d", got, streamdeck.KeyImageHeight)
	}

	assertColor(t, img.At(10, 10), topRed, "top")
	assertColor(t, img.At(10, 110), bottomBlue, "bottom")
	assertColor(t, img.At(10, 60), leftGreen, "left")
	assertColor(t, img.At(110, 60), rightYellow, "right")
	assertColor(t, img.At(60, 25), white, "arrow tip")
}

func assertColor(t *testing.T, got color.Color, want color.NRGBA, location string) {
	t.Helper()
	gotNRGBA := color.NRGBAModel.Convert(got).(color.NRGBA)
	if gotNRGBA != want {
		t.Fatalf("%s color = %#v, want %#v", location, gotNRGBA, want)
	}
}
