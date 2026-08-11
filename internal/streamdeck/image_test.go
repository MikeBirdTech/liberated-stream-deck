package streamdeck

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestEncodeKeyJPEG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, KeyImageWidth, KeyImageHeight))
	encoded, err := encodeKeyJPEG(img)
	if err != nil {
		t.Fatalf("encodeKeyJPEG: %v", err)
	}
	if len(encoded) < 4 || encoded[0] != 0xff || encoded[1] != 0xd8 || encoded[len(encoded)-2] != 0xff || encoded[len(encoded)-1] != 0xd9 {
		t.Fatal("encoded bytes do not have JPEG SOI/EOI markers")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode encoded JPEG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: KeyImageWidth, Y: KeyImageHeight}) {
		t.Fatalf("decoded JPEG size = %v", got)
	}
}

func TestEncodeKeyJPEGRejectsInvalidImages(t *testing.T) {
	if _, err := encodeKeyJPEG(nil); err == nil {
		t.Fatal("nil image returned nil error")
	}
	if _, err := encodeKeyJPEG(image.NewNRGBA(image.Rect(0, 0, 119, 120))); err == nil {
		t.Fatal("wrong-size image returned nil error")
	}
}

func TestEncodeTouchStripJPEG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, TouchStripWidth, TouchStripHeight))
	encoded, err := encodeTouchStripJPEG(img)
	if err != nil {
		t.Fatalf("encodeTouchStripJPEG: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode encoded JPEG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: TouchStripWidth, Y: TouchStripHeight}) {
		t.Fatalf("decoded JPEG size = %v", got)
	}
}

func TestEncodeTouchStripJPEGRejectsInvalidDimensions(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, TouchStripWidth, TouchStripHeight-1))
	if _, err := encodeTouchStripJPEG(img); err == nil {
		t.Fatal("wrong-size strip image returned nil error")
	}
}

func TestEncodeLCDJPEG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, LCDImageWidth, LCDImageHeight))
	encoded, err := encodeLCDJPEG(img)
	if err != nil {
		t.Fatalf("encodeLCDJPEG: %v", err)
	}
	if len(encoded) < 4 || encoded[0] != 0xff || encoded[1] != 0xd8 || encoded[len(encoded)-2] != 0xff || encoded[len(encoded)-1] != 0xd9 {
		t.Fatal("encoded bytes do not have JPEG SOI/EOI markers")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode encoded JPEG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: LCDImageWidth, Y: LCDImageHeight}) {
		t.Fatalf("decoded JPEG size = %v", got)
	}
}

func TestEncodeLCDJPEGRejectsInvalidImages(t *testing.T) {
	if _, err := encodeLCDJPEG(nil); err == nil {
		t.Fatal("nil image returned nil error")
	}
	for _, size := range []image.Point{
		{X: LCDImageWidth - 1, Y: LCDImageHeight},
		{X: LCDImageWidth, Y: LCDImageHeight - 1},
		{X: 1, Y: 1},
	} {
		if _, err := encodeLCDJPEG(image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))); err == nil {
			t.Fatalf("wrong-size %v LCD image returned nil error", size)
		}
	}
}

func TestEncodePartialWindowJPEG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 300, 60))
	encoded, err := encodePartialWindowJPEG(img)
	if err != nil {
		t.Fatalf("encodePartialWindowJPEG: %v", err)
	}
	if len(encoded) < 4 || encoded[0] != 0xff || encoded[1] != 0xd8 || encoded[len(encoded)-2] != 0xff || encoded[len(encoded)-1] != 0xd9 {
		t.Fatal("encoded bytes do not have JPEG SOI/EOI markers")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode encoded JPEG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: 300, Y: 60}) {
		t.Fatalf("decoded JPEG size = %v", got)
	}
}

func TestEncodePartialWindowJPEGRejectsInvalidImages(t *testing.T) {
	if _, err := encodePartialWindowJPEG(nil); err == nil {
		t.Fatal("nil image returned nil error")
	}
	if _, err := encodePartialWindowJPEG(image.NewNRGBA(image.Rect(0, 0, 0, 0))); err == nil {
		t.Fatal("empty-bounds image returned nil error")
	}
}
