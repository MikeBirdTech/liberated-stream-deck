package streamdeck

import (
	"encoding/binary"
	"errors"
	"testing"
)

func getterResponse(reportID byte, body ...byte) []byte {
	response := make([]byte, featureReportSize)
	response[0] = reportID
	copy(response[1:], body)
	return response
}

func TestFirmwareVersionParsing(t *testing.T) {
	for _, test := range []struct {
		name     string
		reportID byte
		want     FirmwareVersion
	}{
		{name: "LD", reportID: 0x04, want: FirmwareVersion{Version: "2.0.3.7", Checksum: 0x01020304}},
		{name: "AP2", reportID: 0x05, want: FirmwareVersion{Version: "1.0.0", Checksum: 0}},
		{name: "AP1", reportID: 0x07, want: FirmwareVersion{Version: "3.1", Checksum: 0xffffffff}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := getterResponse(test.reportID, 0x0c,
				byte(test.want.Checksum), byte(test.want.Checksum>>8), byte(test.want.Checksum>>16), byte(test.want.Checksum>>24))
			copy(response[6:], test.want.Version)
			got, err := parseFirmwareVersion(response, test.reportID, test.name)
			if err != nil {
				t.Fatalf("parseFirmwareVersion: %v", err)
			}
			if got.Version != test.want.Version {
				t.Fatalf("version = %q, want %q", got.Version, test.want.Version)
			}
			if got.Checksum != test.want.Checksum {
				t.Fatalf("checksum = %d, want %d", got.Checksum, test.want.Checksum)
			}
		})
	}
}

func TestFirmwareVersionParsingRejectsBadReport(t *testing.T) {
	response := getterResponse(0x05, 0x0c)
	if _, err := parseFirmwareVersion(response, 0x04, "LD"); err == nil {
		t.Fatal("mismatched report ID returned nil error")
	}
	if _, err := parseFirmwareVersion(response[:10], 0x05, "AP2"); err == nil {
		t.Fatal("short response returned nil error")
	}
}

func TestFirmwareVersionsRoundTrip(t *testing.T) {
	fake := &fakeHIDDevice{getterResponses: map[byte][]byte{
		0x04: getterResponse(0x04, 0x0c, 0, 0, 0, 0, '2', '.', '0', '.', '3', '.', '7'),
		0x05: getterResponse(0x05, 0x0c, 0, 0, 0, 0, '1', '.', '0', '.', '0'),
		0x07: getterResponse(0x07, 0x0c, 0, 0, 0, 0, '9', '.', '9'),
	}}
	deck := newPlus(fake)
	ld, err := deck.FirmwareVersionLD()
	if err != nil {
		t.Fatalf("FirmwareVersionLD: %v", err)
	}
	if ld.Version != "2.0.3.7" {
		t.Fatalf("LD version = %q", ld.Version)
	}
	ap2, err := deck.FirmwareVersionAP2()
	if err != nil {
		t.Fatalf("FirmwareVersionAP2: %v", err)
	}
	if ap2.Version != "1.0.0" {
		t.Fatalf("AP2 version = %q", ap2.Version)
	}
	ap1, err := deck.FirmwareVersionAP1()
	if err != nil {
		t.Fatalf("FirmwareVersionAP1: %v", err)
	}
	if ap1.Version != "9.9" {
		t.Fatalf("AP1 version = %q", ap1.Version)
	}
	if len(fake.getterRequests) != 3 {
		t.Fatalf("getter requests = %d, want 3", len(fake.getterRequests))
	}
	for index, request := range fake.getterRequests {
		if request[0] != []byte{0x04, 0x05, 0x07}[index] {
			t.Fatalf("request %d report ID = 0x%02x", index, request[0])
		}
	}
}

func TestSerialNumberRoundTrip(t *testing.T) {
	serial := []byte("A00WA3361NFL4P")
	fake := &fakeHIDDevice{getterResponses: map[byte][]byte{
		0x06: getterResponse(0x06, byte(len(serial))),
	}}
	response := fake.getterResponses[0x06]
	copy(response[2:], serial)
	fake.getterResponses[0x06] = response

	deck := newPlus(fake)
	got, err := deck.UnitSerialNumber()
	if err != nil {
		t.Fatalf("UnitSerialNumber: %v", err)
	}
	if got != string(serial) {
		t.Fatalf("serial = %q, want %q", got, serial)
	}
	if fake.getterRequests[0][0] != 0x06 {
		t.Fatalf("request report ID = 0x%02x, want 0x06", fake.getterRequests[0][0])
	}
}

func TestSerialNumberRejectsBadReport(t *testing.T) {
	if _, err := parseSerialNumber(getterResponse(0x05, 0x0c)); err == nil {
		t.Fatal("mismatched report ID returned nil error")
	}
	if _, err := parseSerialNumber(getterResponse(0x06, 0x00)); err == nil {
		t.Fatal("zero data length returned nil error")
	}
	if _, err := parseSerialNumber(getterResponse(0x06, 0x20)); err == nil {
		t.Fatal("data length exceeding report returned nil error")
	}
}

func TestUnitInfoRoundTrip(t *testing.T) {
	response := getterResponse(0x08)
	response[0x01] = 4 // rows
	response[0x02] = 2 // columns
	binary.LittleEndian.PutUint16(response[0x03:0x05], 120)
	binary.LittleEndian.PutUint16(response[0x05:0x07], 120)
	binary.LittleEndian.PutUint16(response[0x07:0x09], 800)
	binary.LittleEndian.PutUint16(response[0x09:0x0b], 480)
	response[0x0b] = 24 // bpp
	response[0x0c] = 1  // color scheme
	response[0x0d] = 8  // gallery keys
	response[0x0e] = 1  // gallery lcd
	response[0x0f] = 0  // demo frames

	fake := &fakeHIDDevice{getterResponses: map[byte][]byte{0x08: response}}
	deck := newPlus(fake)
	info, err := deck.UnitInfo()
	if err != nil {
		t.Fatalf("UnitInfo: %v", err)
	}
	want := UnitInfo{
		MatrixRows: 4, MatrixColumns: 2,
		KeyWidth: 120, KeyHeight: 120,
		LCDWidth: 800, LCDHeight: 480,
		ImageBPP: 24, ColorScheme: 1,
		GalleryKeys: 8, GalleryLCD: 1, DemoFrames: 0,
	}
	if info != want {
		t.Fatalf("unit info = %+v, want %+v", info, want)
	}
}

func TestUnitInfoRejectsShortReport(t *testing.T) {
	if _, err := parseUnitInfo(getterResponse(0x08)[:16]); err == nil {
		t.Fatal("short unit-info response returned nil error")
	}
}

func TestSleepDurationRoundTrip(t *testing.T) {
	for _, seconds := range []int{0, 30, 2147483647} {
		response := getterResponse(0x0a, 0x04,
			byte(seconds), byte(seconds>>8), byte(seconds>>16), byte(seconds>>24))
		fake := &fakeHIDDevice{getterResponses: map[byte][]byte{0x0a: response}}
		deck := newPlus(fake)
		got, err := deck.SleepDuration()
		if err != nil {
			t.Fatalf("SleepDuration(%d): %v", seconds, err)
		}
		if got != seconds {
			t.Fatalf("sleep duration = %d, want %d", got, seconds)
		}
	}
}

func TestSleepDurationRejectsBadReport(t *testing.T) {
	if _, err := parseSleepDuration(getterResponse(0x0a, 0x04, 0, 0, 0, 0)[:4]); err == nil {
		t.Fatal("short response returned nil error")
	}
	if _, err := parseSleepDuration(getterResponse(0x0b, 0x04, 0, 0, 0, 0)); err == nil {
		t.Fatal("mismatched report ID returned nil error")
	}
}

func TestGettersFailOnClosedDeck(t *testing.T) {
	fake := &fakeHIDDevice{getterResponses: map[byte][]byte{0x0a: getterResponse(0x0a, 0x04, 0, 0, 0, 0)}}
	deck := newPlus(fake)
	if err := deck.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := deck.SleepDuration(); !errors.Is(err, ErrClosed) {
		t.Fatalf("SleepDuration error = %v, want ErrClosed", err)
	}
	if _, err := deck.UnitSerialNumber(); !errors.Is(err, ErrClosed) {
		t.Fatalf("UnitSerialNumber error = %v, want ErrClosed", err)
	}
	if _, err := deck.UnitInfo(); !errors.Is(err, ErrClosed) {
		t.Fatalf("UnitInfo error = %v, want ErrClosed", err)
	}
	if _, err := deck.FirmwareVersionLD(); !errors.Is(err, ErrClosed) {
		t.Fatalf("FirmwareVersionLD error = %v, want ErrClosed", err)
	}
}
