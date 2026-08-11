package streamdeck

import (
	"fmt"
	"testing"
)

func TestBuildFillLCDReport(t *testing.T) {
	for _, rgb := range [][3]uint8{{0, 0, 0}, {12, 34, 56}, {255, 255, 255}} {
		t.Run(fmt.Sprintf("%02x%02x%02x", rgb[0], rgb[1], rgb[2]), func(t *testing.T) {
			report := buildFillLCDReport(rgb[0], rgb[1], rgb[2])
			if len(report) != featureReportSize {
				t.Fatalf("length = %d, want %d", len(report), featureReportSize)
			}
			if report[0] != featureReportID || report[1] != commandFillLCD {
				t.Fatalf("header = % x", report[:2])
			}
			if report[2] != rgb[0] || report[3] != rgb[1] || report[4] != rgb[2] {
				t.Fatalf("rgb = % x, want % x", report[2:5], rgb)
			}
			for _, value := range report[5:] {
				if value != 0 {
					t.Fatal("feature report is not zero-padded")
				}
			}
		})
	}
}

func TestFillLCDSendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.FillLCD(12, 34, 56); err != nil {
		t.Fatalf("FillLCD: %v", err)
	}
	if len(fake.featureReports) != 1 {
		t.Fatalf("feature reports = %d, want 1", len(fake.featureReports))
	}
	want := buildFillLCDReport(12, 34, 56)
	if string(fake.featureReports[0]) != string(want) {
		t.Fatalf("feature report = % x, want % x", fake.featureReports[0], want)
	}
}

func TestFillLCDRejectsShortFeatureWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortFeatureWrite: true}
	deck := newPlus(fake)
	if err := deck.FillLCD(0, 0, 0); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}
