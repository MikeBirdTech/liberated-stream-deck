package streamdeck

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"golang.org/x/image/bmp"
)

func TestBuildMiniKeyImageReportsExactHeaders(t *testing.T) {
	data := make([]byte, miniImageChunkSize+3)
	data[0] = 0xaa
	data[miniImageChunkSize] = 0xbb
	data[len(data)-1] = 0xcc

	reports, err := buildMiniKeyImageReports(5, data)
	if err != nil {
		t.Fatalf("buildMiniKeyImageReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}
	wantFirstHeader := []byte{0x02, 0x01, 0x00, 0x00, 0x00, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	wantLastHeader := []byte{0x02, 0x01, 0x01, 0x00, 0x01, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(reports[0][:miniImageHeaderSize], wantFirstHeader) {
		t.Fatalf("first header = % x, want % x", reports[0][:miniImageHeaderSize], wantFirstHeader)
	}
	if !bytes.Equal(reports[1][:miniImageHeaderSize], wantLastHeader) {
		t.Fatalf("last header = % x, want % x", reports[1][:miniImageHeaderSize], wantLastHeader)
	}
	if len(reports[0]) != miniOutputReportSize || len(reports[1]) != miniOutputReportSize {
		t.Fatalf("report sizes = %d,%d, want %d", len(reports[0]), len(reports[1]), miniOutputReportSize)
	}
	if reports[0][miniImageHeaderSize] != 0xaa || reports[1][miniImageHeaderSize] != 0xbb || reports[1][miniImageHeaderSize+2] != 0xcc {
		t.Fatal("chunk payload boundaries were not preserved")
	}
	for _, value := range reports[1][miniImageHeaderSize+3:] {
		if value != 0 {
			t.Fatal("last report padding is not zero-filled")
		}
	}
}

func TestBuildMiniKeyImageReportsOneChunkHasFinalFlag(t *testing.T) {
	reports, err := buildMiniKeyImageReports(0, []byte{0xaa})
	if err != nil {
		t.Fatalf("buildMiniKeyImageReports: %v", err)
	}
	wantHeader := []byte{0x02, 0x01, 0x00, 0x00, 0x01, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if len(reports) != 1 || !bytes.Equal(reports[0][:miniImageHeaderSize], wantHeader) {
		t.Fatalf("header = % x, want % x", reports[0][:miniImageHeaderSize], wantHeader)
	}
}

func TestBuildMiniKeyImageReportsRejectsBounds(t *testing.T) {
	for _, index := range []int{-1, MiniKeyCount} {
		if _, err := buildMiniKeyImageReports(index, []byte{1}); err == nil {
			t.Fatalf("key index %d returned nil error", index)
		}
	}
	if _, err := buildMiniKeyImageReports(0, nil); err == nil {
		t.Fatal("empty BMP returned nil error")
	}
	tooLarge := make([]byte, miniImageChunkSize*256+1)
	if _, err := buildMiniKeyImageReports(0, tooLarge); err == nil {
		t.Fatal("BMP requiring more than 256 pages returned nil error")
	}
}

func TestEncodeMiniKeyBMPDimensionsAndClockwiseRotation(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, MiniKeyImageWidth, MiniKeyImageHeight))
	red := color.NRGBA{R: 255, A: 255}
	img.Set(0, 0, red)

	encoded, err := encodeMiniKeyBMP(img)
	if err != nil {
		t.Fatalf("encodeMiniKeyBMP: %v", err)
	}
	if len(encoded) < 2 || string(encoded[:2]) != "BM" {
		t.Fatalf("BMP signature = % x", encoded[:min(2, len(encoded))])
	}
	if got := binary.LittleEndian.Uint16(encoded[28:30]); got != 24 {
		t.Fatalf("BMP bits per pixel = %d, want 24", got)
	}
	if got, want := len(encoded), 54+MiniKeyImageWidth*MiniKeyImageHeight*3; got != want {
		t.Fatalf("BMP length = %d, want %d", got, want)
	}
	decoded, err := bmp.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode BMP: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: MiniKeyImageHeight, Y: MiniKeyImageWidth}) {
		t.Fatalf("decoded size = %v", got)
	}
	got := color.NRGBAModel.Convert(decoded.At(MiniKeyImageHeight-1, 0)).(color.NRGBA)
	if got.R < 250 || got.G != 0 || got.B != 0 {
		t.Fatalf("rotated top-right pixel = %#v, want red", got)
	}
}

func TestEncodeMiniKeyBMPRejectsInvalidImages(t *testing.T) {
	if _, err := encodeMiniKeyBMP(nil); err == nil {
		t.Fatal("nil image returned nil error")
	}
	if _, err := encodeMiniKeyBMP(image.NewNRGBA(image.Rect(0, 0, 79, 80))); err == nil {
		t.Fatal("wrong-size image returned nil error")
	}
}

func TestMiniSetKeyImageWritesCompleteReports(t *testing.T) {
	fake := &fakeHIDDevice{}
	mini := newMini(fake)
	img := image.NewNRGBA(image.Rect(0, 0, MiniKeyImageWidth, MiniKeyImageHeight))
	if err := mini.SetKeyImage(0, img); err != nil {
		t.Fatalf("SetKeyImage: %v", err)
	}
	if len(fake.writes) < 2 {
		t.Fatalf("writes = %d, want multiple", len(fake.writes))
	}
	for index, report := range fake.writes {
		if len(report) != miniOutputReportSize {
			t.Fatalf("write %d size = %d, want %d", index, len(report), miniOutputReportSize)
		}
	}
	if fake.writes[len(fake.writes)-1][4] != 0x01 {
		t.Fatal("last write does not have final marker")
	}
}

func TestMiniSetKeyImageRejectsShortWrite(t *testing.T) {
	mini := newMini(&fakeHIDDevice{shortWrite: true})
	img := image.NewNRGBA(image.Rect(0, 0, MiniKeyImageWidth, MiniKeyImageHeight))
	if err := mini.SetKeyImage(0, img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}
