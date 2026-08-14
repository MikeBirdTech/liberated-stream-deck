package streamdeck

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestBuildFirmwareProbeReport(t *testing.T) {
	tests := []struct {
		probe       FirmwareProbe
		wantHeader  []byte
		wantPayload []byte
		wantHash    string
	}{
		{
			probe:      FirmwareProbeIncompleteEmpty,
			wantHeader: []byte{0x02, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0, 0, 0, 0, 0, 0},
			wantHash:   "16307d511ef890acbebe9da31fec11f551d63b4df905ceea04401f87734ba7dd",
		},
		{
			probe:      FirmwareProbeFinalEmpty,
			wantHeader: []byte{0x02, 0x05, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x02, 0, 0, 0, 0, 0, 0},
			wantHash:   "b643850a287da99dfc5334ea72fed28a9b18ffecf294167ac1cef9cef2f27842",
		},
		{
			probe:       FirmwareProbeFinalMarker,
			wantHeader:  []byte{0x02, 0x05, 0x00, 0x01, 0x01, 0x00, 0x00, 0x20, 0x00, 0x02, 0, 0, 0, 0, 0, 0},
			wantPayload: []byte(firmwareProbeMarker),
			wantHash:    "dca6656aa7253de4b80f43c972a227ccc87467fce43b47046683f7ac719bb82d",
		},
	}

	for _, test := range tests {
		t.Run(string(test.probe), func(t *testing.T) {
			report, result, err := buildFirmwareProbeReport(test.probe)
			if err != nil {
				t.Fatalf("buildFirmwareProbeReport: %v", err)
			}
			if len(report) != outputReportSize {
				t.Fatalf("report length = %d, want %d", len(report), outputReportSize)
			}
			if !bytes.Equal(report[:firmwareHeaderSize], test.wantHeader) {
				t.Fatalf("header = % x, want % x", report[:firmwareHeaderSize], test.wantHeader)
			}
			if !bytes.Equal(report[firmwareHeaderSize:firmwareHeaderSize+len(test.wantPayload)], test.wantPayload) {
				t.Fatalf("payload = %q, want %q", report[firmwareHeaderSize:firmwareHeaderSize+len(test.wantPayload)], test.wantPayload)
			}
			if !allBytesZero(report[firmwareHeaderSize+len(test.wantPayload):]) {
				t.Fatal("report has nonzero padding")
			}
			if result.Probe != test.probe || result.PayloadLength != len(test.wantPayload) {
				t.Fatalf("result = %+v", result)
			}
			if result.PayloadSHA256 != sha256.Sum256(test.wantPayload) {
				t.Fatalf("payload SHA-256 = %x", result.PayloadSHA256)
			}
			if result.ReportSHA256 != sha256.Sum256(report) {
				t.Fatalf("report SHA-256 = %x", result.ReportSHA256)
			}
			if got := fmt.Sprintf("%x", result.ReportSHA256); got != test.wantHash {
				t.Fatalf("report SHA-256 = %s, want %s", got, test.wantHash)
			}
		})
	}
}

func TestProbeFirmwareTransportWritesExactReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)

	result, err := deck.ProbeFirmwareTransport(FirmwareProbeFinalMarker)
	if err != nil {
		t.Fatalf("ProbeFirmwareTransport: %v", err)
	}
	if len(fake.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(fake.writes))
	}
	if got := sha256.Sum256(fake.writes[0]); got != result.ReportSHA256 {
		t.Fatalf("written report SHA-256 = %x, want %x", got, result.ReportSHA256)
	}
}

func TestProbeFirmwareTransportRejectsUnknownAndShortWrite(t *testing.T) {
	deck := newPlus(&fakeHIDDevice{})
	if _, err := deck.ProbeFirmwareTransport(FirmwareProbe("other")); err == nil {
		t.Fatal("unknown probe succeeded")
	}

	deck = newPlus(&fakeHIDDevice{shortWrite: true})
	if _, err := deck.ProbeFirmwareTransport(FirmwareProbeIncompleteEmpty); err == nil {
		t.Fatal("short write succeeded")
	}
}

func allBytesZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
