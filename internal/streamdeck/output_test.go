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

func TestBuildTouchStripImageReports(t *testing.T) {
	jpegData := make([]byte, imageChunkSize*2+7)
	jpegData[0] = 0xaa
	jpegData[len(jpegData)-1] = 0xbb
	reports, err := buildTouchStripImageReports(jpegData)
	if err != nil {
		t.Fatalf("buildTouchStripImageReports: %v", err)
	}
	if len(reports) != 3 {
		t.Fatalf("reports = %d, want 3", len(reports))
	}
	for index, report := range reports {
		if len(report) != outputReportSize {
			t.Fatalf("report %d size = %d", index, len(report))
		}
		if report[0] != outputReportID || report[1] != commandUpdateTouchStrip || report[2] != 0 {
			t.Fatalf("report %d header = % x", index, report[:3])
		}
		if got := binary.LittleEndian.Uint16(report[6:8]); got != uint16(index) {
			t.Fatalf("report %d chunk index = %d", index, got)
		}
	}
	if reports[0][3] != 0 || reports[1][3] != 0 || reports[2][3] != 1 {
		t.Fatalf("final flags = %d,%d,%d", reports[0][3], reports[1][3], reports[2][3])
	}
	if got := binary.LittleEndian.Uint16(reports[2][4:6]); got != 7 {
		t.Fatalf("last size = %d, want 7", got)
	}
	if reports[0][imageHeaderSize] != 0xaa || reports[2][imageHeaderSize+6] != 0xbb {
		t.Fatal("strip payload boundaries not preserved")
	}
	for _, value := range reports[2][imageHeaderSize+7:] {
		if value != 0 {
			t.Fatal("last strip report padding is not zero-filled")
		}
	}
}

func TestSetTouchStripImageWritesMultipleCompleteReports(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := &Deck{device: fake}
	img := image.NewNRGBA(image.Rect(0, 0, TouchStripWidth, TouchStripHeight))
	if err := deck.SetTouchStripImage(img); err != nil {
		t.Fatalf("SetTouchStripImage: %v", err)
	}
	if len(fake.writes) < 2 {
		t.Fatalf("writes = %d, want multiple", len(fake.writes))
	}
	if fake.writes[len(fake.writes)-1][3] != 1 {
		t.Fatal("last write does not have final marker")
	}
}

func TestSetTouchStripImageRejectsShortWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortWrite: true}
	deck := &Deck{device: fake}
	img := image.NewNRGBA(image.Rect(0, 0, TouchStripWidth, TouchStripHeight))
	if err := deck.SetTouchStripImage(img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}

type fakeHIDDevice struct {
	writes            [][]byte
	featureReports    [][]byte
	shortWrite        bool
	shortFeatureWrite bool
	closeCalls        int
}

func (f *fakeHIDDevice) SendFeatureReport(report []byte) (int, error) {
	f.featureReports = append(f.featureReports, append([]byte(nil), report...))
	if f.shortFeatureWrite {
		return len(report) - 1, nil
	}
	return len(report), nil
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

func (f *fakeHIDDevice) Close() error {
	f.closeCalls++
	return nil
}
