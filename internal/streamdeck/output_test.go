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
	deck := newPlus(fake)
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
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, KeyImageWidth, KeyImageHeight))
	if err := deck.SetKeyImage(0, img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}

func TestBuildLCDImageReportsOneChunk(t *testing.T) {
	jpegData := make([]byte, imageChunkSize)
	jpegData[0] = 0xaa
	jpegData[len(jpegData)-1] = 0xbb
	reports, err := buildLCDImageReports(jpegData)
	if err != nil {
		t.Fatalf("buildLCDImageReports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	report := reports[0]
	if len(report) != outputReportSize {
		t.Fatalf("report size = %d, want %d", len(report), outputReportSize)
	}
	if report[0] != outputReportID || report[1] != commandUpdateLCDImage || report[2] != 0 || report[3] != 1 {
		t.Fatalf("unexpected header bytes: % x", report[:4])
	}
	if got := binary.LittleEndian.Uint16(report[4:6]); got != imageChunkSize {
		t.Fatalf("chunk size = %d, want %d", got, imageChunkSize)
	}
	if got := binary.LittleEndian.Uint16(report[6:8]); got != 0 {
		t.Fatalf("chunk index = %d, want 0", got)
	}
	if report[8] != 0xaa || report[len(report)-1] != 0xbb {
		t.Fatal("chunk payload was not copied to report boundaries")
	}
}

func TestBuildLCDImageReportsMultiChunk(t *testing.T) {
	jpegData := make([]byte, imageChunkSize*2+7)
	jpegData[0] = 0xaa
	jpegData[len(jpegData)-1] = 0xbb
	reports, err := buildLCDImageReports(jpegData)
	if err != nil {
		t.Fatalf("buildLCDImageReports: %v", err)
	}
	if len(reports) != 3 {
		t.Fatalf("reports = %d, want 3", len(reports))
	}
	for index, report := range reports {
		if len(report) != outputReportSize {
			t.Fatalf("report %d size = %d", index, len(report))
		}
		if report[0] != outputReportID || report[1] != commandUpdateLCDImage || report[2] != 0 {
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
		t.Fatal("LCD payload boundaries not preserved")
	}
	for _, value := range reports[2][imageHeaderSize+7:] {
		if value != 0 {
			t.Fatal("last LCD report padding is not zero-filled")
		}
	}
}

func TestBuildLCDImageReportsRejectsEmptyJPEG(t *testing.T) {
	if _, err := buildLCDImageReports(nil); err == nil {
		t.Fatal("empty JPEG returned nil error")
	}
}

func TestSetLCDImageWritesCompleteReports(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, LCDImageWidth, LCDImageHeight))
	if err := deck.SetLCDImage(img); err != nil {
		t.Fatalf("SetLCDImage: %v", err)
	}
	if len(fake.writes) < 2 {
		t.Fatalf("writes = %d, want multiple chunks for an 800x480 JPEG", len(fake.writes))
	}
	if fake.writes[len(fake.writes)-1][3] != 1 {
		t.Fatal("last write does not have final marker")
	}
	for index, report := range fake.writes {
		if len(report) != outputReportSize {
			t.Fatalf("write %d has %d bytes, want %d", index, len(report), outputReportSize)
		}
	}
}

func TestSetLCDImageRejectsShortWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortWrite: true}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, LCDImageWidth, LCDImageHeight))
	if err := deck.SetLCDImage(img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}

func TestBuildPartialWindowImageReportsOneChunk(t *testing.T) {
	jpegData := make([]byte, partialWindowChunkSize)
	jpegData[0] = 0xaa
	jpegData[len(jpegData)-1] = 0xbb
	reports, err := buildPartialWindowImageReports(25, 10, 100, 50, jpegData)
	if err != nil {
		t.Fatalf("buildPartialWindowImageReports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	report := reports[0]
	if len(report) != outputReportSize {
		t.Fatalf("report size = %d, want %d", len(report), outputReportSize)
	}
	if report[0] != outputReportID || report[1] != commandUpdatePartialWindow {
		t.Fatalf("unexpected report id/command: % x", report[:2])
	}
	if got := binary.LittleEndian.Uint16(report[2:4]); got != 25 {
		t.Fatalf("x = %d, want 25", got)
	}
	if got := binary.LittleEndian.Uint16(report[4:6]); got != 10 {
		t.Fatalf("y = %d, want 10", got)
	}
	if got := binary.LittleEndian.Uint16(report[6:8]); got != 100 {
		t.Fatalf("width = %d, want 100", got)
	}
	if got := binary.LittleEndian.Uint16(report[8:10]); got != 50 {
		t.Fatalf("height = %d, want 50", got)
	}
	if report[0x0a] != 1 {
		t.Fatalf("done flag = %d, want 1", report[0x0a])
	}
	if got := binary.LittleEndian.Uint16(report[0x0b:0x0d]); got != 0 {
		t.Fatalf("chunk index = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(report[0x0d:0x0f]); got != partialWindowChunkSize {
		t.Fatalf("chunk size = %d, want %d", got, partialWindowChunkSize)
	}
	if report[0x0f] != 0 {
		t.Fatalf("reserved byte = 0x%02x, want 0", report[0x0f])
	}
	if report[partialWindowHeaderSize] != 0xaa || report[len(report)-1] != 0xbb {
		t.Fatal("chunk payload was not copied to report boundaries")
	}
}

func TestBuildPartialWindowImageReportsMultiChunk(t *testing.T) {
	jpegData := make([]byte, partialWindowChunkSize+1)
	jpegData[len(jpegData)-1] = 0xcc
	reports, err := buildPartialWindowImageReports(0, 0, 20, 20, jpegData)
	if err != nil {
		t.Fatalf("buildPartialWindowImageReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}
	for index, report := range reports {
		if len(report) != outputReportSize {
			t.Fatalf("report %d size = %d", index, len(report))
		}
		if got := binary.LittleEndian.Uint16(report[0x0b:0x0d]); got != uint16(index) {
			t.Fatalf("report %d chunk index = %d", index, got)
		}
		if got := binary.LittleEndian.Uint16(report[2:4]); got != 0 || binary.LittleEndian.Uint16(report[6:8]) != 20 {
			t.Fatalf("report %d region header = % x", index, report[2:10])
		}
	}
	if reports[0][0x0a] != 0 || reports[1][0x0a] != 1 {
		t.Fatalf("done flags = %d,%d, want 0,1", reports[0][0x0a], reports[1][0x0a])
	}
	if got := binary.LittleEndian.Uint16(reports[1][0x0d:0x0f]); got != 1 {
		t.Fatalf("last chunk size = %d, want 1", got)
	}
	if reports[1][partialWindowHeaderSize] != 0xcc {
		t.Fatalf("last chunk first byte = 0x%02x, want 0xcc", reports[1][partialWindowHeaderSize])
	}
	for _, value := range reports[1][partialWindowHeaderSize+1:] {
		if value != 0 {
			t.Fatal("last report padding is not zero-filled")
		}
	}
}

func TestBuildPartialWindowImageReportsRejectsInvalidRegion(t *testing.T) {
	jpegData := []byte{1}
	for _, test := range []struct {
		name       string
		x, y, w, h int
	}{
		{name: "negative x", x: -1, y: 0, w: 1, h: 1},
		{name: "negative y", x: 0, y: -1, w: 1, h: 1},
		{name: "zero width", x: 0, y: 0, w: 0, h: 1},
		{name: "negative height", x: 0, y: 0, w: 1, h: -1},
		{name: "window overflow right", x: TouchStripWidth, y: 0, w: 1, h: 1},
		{name: "window overflow bottom", x: 0, y: TouchStripHeight, w: 1, h: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildPartialWindowImageReports(test.x, test.y, test.w, test.h, jpegData); err == nil {
				t.Fatal("invalid region returned nil error")
			}
		})
	}
	if _, err := buildPartialWindowImageReports(0, 0, 1, 1, nil); err == nil {
		t.Fatal("empty JPEG returned nil error")
	}
}

func TestSetPartialWindowImageWritesCompleteReports(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	if err := deck.SetPartialWindowImage(10, 0, img); err != nil {
		t.Fatalf("SetPartialWindowImage: %v", err)
	}
	if len(fake.writes) == 0 {
		t.Fatal("SetPartialWindowImage performed no HID writes")
	}
	for index, report := range fake.writes {
		if len(report) != outputReportSize {
			t.Fatalf("write %d has %d bytes, want %d", index, len(report), outputReportSize)
		}
	}
	if fake.writes[len(fake.writes)-1][0x0a] != 1 {
		t.Fatal("last write does not have final marker")
	}
}

func TestSetPartialWindowImageRejectsShortWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortWrite: true}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	err := deck.SetPartialWindowImage(0, 0, img)
	if err == nil {
		t.Fatal("short HID write returned nil error")
	}
	if len(fake.writes) != 1 {
		t.Fatalf("writes = %d, want 1 (short write must stop the upload)", len(fake.writes))
	}
}

func TestSetPartialWindowImageRejectsRegionOverflow(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, 100, 50))
	if err := deck.SetPartialWindowImage(TouchStripWidth-50, 60, img); err == nil {
		t.Fatal("region overflowing the window returned nil error")
	}
	if len(fake.writes) != 0 {
		t.Fatalf("writes = %d, want 0 (invalid region must not reach the device)", len(fake.writes))
	}
}

func TestSetPartialWindowImageRejectsNilAndEmptyImage(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.SetPartialWindowImage(0, 0, nil); err == nil {
		t.Fatal("nil image returned nil error")
	}
	if err := deck.SetPartialWindowImage(0, 0, image.NewNRGBA(image.Rect(5, 5, 5, 5))); err == nil {
		t.Fatal("empty-bounds image returned nil error")
	}
	if len(fake.writes) != 0 {
		t.Fatalf("writes = %d, want 0", len(fake.writes))
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
	deck := newPlus(fake)
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
	deck := newPlus(fake)
	img := image.NewNRGBA(image.Rect(0, 0, TouchStripWidth, TouchStripHeight))
	if err := deck.SetTouchStripImage(img); err == nil {
		t.Fatal("short HID write returned nil error")
	}
}

type fakeHIDDevice struct {
	writes            [][]byte
	featureReports    [][]byte
	reads             [][]byte
	readErr           error
	info              *hid.DeviceInfo
	infoErr           error
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

func (f *fakeHIDDevice) ReadWithTimeout(report []byte, _ time.Duration) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if len(f.reads) == 0 {
		return 0, errors.New("not implemented")
	}
	n := copy(report, f.reads[0])
	f.reads = f.reads[1:]
	return n, nil
}

func (f *fakeHIDDevice) Write(report []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), report...))
	if f.shortWrite {
		return len(report) - 1, nil
	}
	return len(report), nil
}

func (f *fakeHIDDevice) GetDeviceInfo() (*hid.DeviceInfo, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	if f.info == nil {
		return nil, errors.New("not implemented")
	}
	return f.info, nil
}

func (f *fakeHIDDevice) Close() error {
	f.closeCalls++
	return nil
}
