package render

import (
	"image/color"
	"testing"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

func TestKeyDimensionsAndSelectionBorder(t *testing.T) {
	img := Key(KeyView{Index: 7, On: true, Selected: true})
	if got := img.Bounds().Dx(); got != streamdeck.KeyImageWidth {
		t.Fatalf("width = %d", got)
	}
	if got := img.Bounds().Dy(); got != streamdeck.KeyImageHeight {
		t.Fatalf("height = %d", got)
	}
	assertDemoColor(t, img.At(0, 0), selected, "selection border")
	assertDemoColor(t, img.At(60, 60), keyOn, "on background")
}

func TestMiniKeyDimensionsAndLayout(t *testing.T) {
	img := KeySize(KeyView{Index: 5, On: true, Selected: true}, streamdeck.MiniKeyImageWidth, streamdeck.MiniKeyImageHeight)
	if got := img.Bounds().Dx(); got != streamdeck.MiniKeyImageWidth {
		t.Fatalf("width = %d", got)
	}
	if got := img.Bounds().Dy(); got != streamdeck.MiniKeyImageHeight {
		t.Fatalf("height = %d", got)
	}
	assertDemoColor(t, img.At(0, 0), selected, "selection border")
	assertDemoColor(t, img.At(40, 40), keyOn, "on background")
}

func TestStripDimensions(t *testing.T) {
	touch := streamdeck.TouchEvent{Kind: streamdeck.TouchFlick, StartX: 120, StartY: 50, EndX: 690, EndY: 48}
	img := Strip(StripView{Counter: 3, Brightness: 70, SelectedKey: 3, Mode: "INPUT", Touch: &touch})
	if got := img.Bounds().Dx(); got != streamdeck.TouchStripWidth {
		t.Fatalf("width = %d", got)
	}
	if got := img.Bounds().Dy(); got != streamdeck.TouchStripHeight {
		t.Fatalf("height = %d", got)
	}
}

func assertDemoColor(t *testing.T, got color.Color, want color.NRGBA, location string) {
	t.Helper()
	gotNRGBA := color.NRGBAModel.Convert(got).(color.NRGBA)
	if gotNRGBA != want {
		t.Fatalf("%s = %#v, want %#v", location, gotNRGBA, want)
	}
}
