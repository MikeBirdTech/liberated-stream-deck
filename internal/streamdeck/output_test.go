package streamdeck

import (
	"encoding/binary"
	"errors"
	"image"
	"testing"
	"time"

	"github.com/sstallion/go-hid"
)

func TestBuildKeyImageReportsOneChunk(t *testing.T) {
	jpegData := make([]byte, keyImageChunkSize)
	jpegData[0] = 0xaa
	jpegData[len(jpegData)-1] = 0xbb
	reports, err := buildKeyImageReports(0, jpegData)
	if err != nil {
		t.Fatalf("buildKeyImageReports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	report := reports[0]
	if len(report) != outputReportSize {
		t.Fatalf("report size = %d, want %d", len(report), outputReportSize)
	}
	if report[0] != outputReportID || report[1] != commandUpdateKeyImage || report[2] != 0 || report[3] != 1 {
		t.Fatalf("unexpected header bytes: % x", report[:4])
	}
	if got := binary.LittleEndian.Uint16(report[4:6]); got != keyImageChunkSize {
		t.Fatalf("chunk size = %d, want %d", got, keyImageChunkSize)
	}
	if got := binary.LittleEndian.Uint16(report[6:8]); got != 0 {
		t.Fatalf("chunk index = %d, want 0", got)
	}
	if report[8] != 0xaa || report[len(report)-1] != 0xbb {
		t.Fatal("chunk payload was not copied to report boundaries")
	}
}

func TestBuildKeyImageReportsTwoChunks(t *testing.T) {
	jpegData := make([]byte, keyImageChunkSize+1)
	jpegData[len(jpegData)-1] = 0xcc
	reports, err := buildKeyImageReports(7, jpegData)
	if err != nil {
		t.Fatalf("buildKeyImageReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("got %d reports, want 2", len(reports))
	}
	if reports[0][3] != 0 || reports[1][3] != 1 {
		t.Fatalf("final flags = %d,%d, want 0,1", reports[0][3], reports[1][3])
	}
	if got := binary.LittleEndian.Uint16(reports[1][4:6]); got != 1 {
		t.Fatalf("last chunk size = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(reports[1][6:8]); got != 1 {
		t.Fatalf("last chunk index = %d, want 1", got)
	}
	if reports[1][8] != 0xcc {
		t.Fatalf("last chunk first byte = 0x%02x, want 0xcc", reports[1][8])
	}
	for _, value := range reports[1][9:] {
		if value != 0 {
			t.Fatal("last report padding is not zero-filled")
		}
	}
}

func TestBuildKeyImageReportsRejectsInvalidInput(t *testing.T) {
	for _, index := range []int{-1, KeyCount} {
		if _, err := buildKeyImageReports(index, []byte{1}); err == nil {
			t.Fatalf("key index %d returned nil error", index)
		}
	}
	if _, err := buildKeyImageReports(0, nil); err == nil {
		t.Fatal("empty JPEG returned nil error")
	}
}

func TestSetKeyImageWritesCompleteReports(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := &Deck{device: fake}
	img := image.NewNRGBA(image.Rect(0, 0, KeyImageWidth, KeyImageHeight))
	if err := deck.SetKeyImage(0, img); err != nil {
		t.Fatalf("SetKeyImage: %v", err)
	}
	if len(fake.writes) == 0 {
		t.Fatal("SetKeyImage performed no HID writes")
	}
	for index, report := range fake.writes {
		if len(report) != outputReportSize {
			t.Fatalf("write %d has %d bytes, want %d", index, len(report), outputReportSize)
		}
	}
}

func TestSetKeyImageRejectsShortWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortWrite: true}
	deck := &Deck{device: fake}
	img := image.NewNRGBA(image.Rect(0, 0, KeyImageWidth, KeyImageHeight))
	if err := deck.SetKeyImage(0, img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}

type fakeHIDDevice struct {
	writes     [][]byte
	shortWrite bool
}

func (f *fakeHIDDevice) ReadWithTimeout([]byte, time.Duration) (int, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeHIDDevice) Write(report []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), report...))
	if f.shortWrite {
		return len(report) - 1, nil
	}
	return len(report), nil
}

func (f *fakeHIDDevice) GetDeviceInfo() (*hid.DeviceInfo, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHIDDevice) Close() error { return nil }
