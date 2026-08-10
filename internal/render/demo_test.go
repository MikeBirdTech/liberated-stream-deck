package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
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

func TestPaperGardenStripUsesPalette(t *testing.T) {
	img := Strip(StripView{
		Theme: "paper", Title: "Demo Garden", Message: "Demo link established",
		EventsSeen: 17, LastEvent: "Dial 2 +3",
	})
	if got := img.Bounds().Size(); got.X != streamdeck.TouchStripWidth || got.Y != streamdeck.TouchStripHeight {
		t.Fatalf("size = %v", got)
	}
	assertDemoColor(t, img.At(0, 0), paperMoss, "moss rail")
	assertDemoColor(t, img.At(10, 0), paperGarden, "paper background")
	assertDemoColor(t, img.At(406, 20), paperLeaf, "leaf divider")

	wantColors := []color.NRGBA{paperInk, paperMoss, paperLeaf, paperAmber, paperRust}
	for _, want := range wantColors {
		if !imageContainsColor(img, want) {
			t.Fatalf("strip does not contain palette color %#v", want)
		}
	}
	if imageContainsColor(img, flickColor) {
		t.Fatal("paper strip contains purple")
	}
}

func imageContainsColor(img image.Image, want color.NRGBA) bool {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) == want {
				return true
			}
		}
	}
	return false
}

func assertDemoColor(t *testing.T, got color.Color, want color.NRGBA, location string) {
	t.Helper()
	gotNRGBA := color.NRGBAModel.Convert(got).(color.NRGBA)
	if gotNRGBA != want {
		t.Fatalf("%s = %#v, want %#v", location, gotNRGBA, want)
	}
}

func TestBridgeKeyPaintsLabelWithWireColors(t *testing.T) {
	bg := color.NRGBA{R: 0x6f, G: 0xa2, B: 0x5c, A: 255} // leaf #6FA25C
	fg := color.NRGBA{R: 0x27, G: 0x2c, B: 0x24, A: 255} // ink #272C24
	img := KeySize(KeyView{Index: 0, Label: "Demo Task", BG: bg, FG: fg}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	assertDemoColor(t, img.At(5, 5), bg, "wire background")
	assertDemoColor(t, img.At(60, 60), bg, "background below label")
	if !imageContainsColor(img, fg) {
		t.Fatalf("wire fg label text not painted")
	}
	if imageContainsColor(img, keyOn) || imageContainsColor(img, keyOff) {
		t.Fatal("bridge key leaked classic demo palette")
	}
}

func TestBridgeQuietKeyIsPlainPaper(t *testing.T) {
	img := KeySize(KeyView{Index: 3, BG: paperGarden}, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			assertDemoColor(t, img.At(x, y), paperGarden, "quiet key pixel")
		}
	}
}

func TestBridgeQuietMiniKeyIsPlainPaper(t *testing.T) {
	img := KeySize(KeyView{Index: 5, BG: paperGarden}, streamdeck.MiniKeyImageWidth, streamdeck.MiniKeyImageHeight)
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			assertDemoColor(t, img.At(x, y), paperGarden, "quiet mini key pixel")
		}
	}
}

func TestBridgeStripDrawsServerPage(t *testing.T) {
	img := Strip(StripView{
		Title: "Today", Lines: []string{"Demo Task: idle", "25 runs · 25 ok · 0 err"},
		Page: 1, Pages: 3, EventsSeen: 25, LastEvent: "Demo Task started",
	})
	if got := img.Bounds().Size(); got.X != streamdeck.TouchStripWidth || got.Y != streamdeck.TouchStripHeight {
		t.Fatalf("size = %v", got)
	}
	assertDemoColor(t, img.At(0, 0), paperMoss, "moss rail")
	assertDemoColor(t, img.At(10, 0), paperGarden, "paper background")
	for _, want := range []color.NRGBA{paperInk, paperMoss, paperAmber, paperRust} {
		if !imageContainsColor(img, want) {
			t.Fatalf("bridge strip does not contain palette color %#v", want)
		}
	}
	// page dots: current page (1) is moss, inactive pages (0,2) are faint
	assertDemoColor(t, img.At(31, 91), paperMoss, "current page dot")
	assertDemoColor(t, img.At(20, 91), paperFaint, "inactive page dot")
	if imageContainsColor(img, flickColor) {
		t.Fatal("bridge strip contains diagnostic purple")
	}
}
