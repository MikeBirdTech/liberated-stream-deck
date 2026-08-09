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
