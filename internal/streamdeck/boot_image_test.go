package streamdeck

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestUploadBootImageWrites089ChunkedFrames(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, BootImageWidth, BootImageHeight))
	if err := deck.UploadBootImage(img); err != nil {
		t.Fatalf("UploadBootImage: %v", err)
	}
	if len(fake.writes) == 0 {
		t.Fatal("no HID writes")
	}
	var payload []byte
	for index, report := range fake.writes {
		if len(report) != outputReportSize {
			t.Fatalf("write %d size = %d, want %d", index, len(report), outputReportSize)
		}
		if report[0] != 0x02 || report[1] != bootImageCommand || report[2] != bootImageTarget {
			t.Fatalf("write %d header = %02x %02x %02x, want 02 09 05", index, report[0], report[1], report[2])
		}
		done := report[3] == 0x01
		idx := binary.LittleEndian.Uint16(report[4:6])
		size := int(binary.LittleEndian.Uint16(report[6:8]))
		if idx != uint16(index) {
			t.Fatalf("write %d chunk index = %d", index, idx)
		}
		if size <= 0 || size > imageChunkSize {
			t.Fatalf("write %d chunk size = %d, want 1..%d", index, size, imageChunkSize)
		}
		if index < len(fake.writes)-1 && done {
			t.Fatalf("write %d marked done before final chunk", index)
		}
		if index == len(fake.writes)-1 && !done {
			t.Fatalf("final chunk missing done flag")
		}
		payload = append(payload, report[8:8+size]...)
	}
	if !bytes.HasPrefix(payload, []byte{0xff, 0xd8}) {
		t.Fatalf("payload does not start with JPEG magic: %02x %02x", payload[0], payload[1])
	}
	decoded, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("reassembled payload is not a valid JPEG: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != BootImageWidth || b.Dy() != BootImageHeight {
		t.Fatalf("decoded image = %v, want 800x480", b)
	}
}

func TestUploadBootImageScalesSmallSource(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	small := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	if err := deck.UploadBootImage(small); err != nil {
		t.Fatalf("UploadBootImage: %v", err)
	}
	var payload []byte
	for _, report := range fake.writes {
		size := int(binary.LittleEndian.Uint16(report[6:8]))
		payload = append(payload, report[8:8+size]...)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != BootImageWidth || b.Dy() != BootImageHeight {
		t.Fatalf("scaled image = %v, want 800x480", b)
	}
	if c := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); c == (color.NRGBA{}) {
		t.Fatal("scaled image is not opaque-filled")
	}
}

func TestScaleImagePassthroughOnExactSize(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, BootImageWidth, BootImageHeight))
	if got := ScaleImage(img, BootImageWidth, BootImageHeight); got != image.Image(img) {
		t.Fatal("exact-size source should be returned unchanged")
	}
}
