package streamdeck

import (
	"fmt"
	"testing"
)

func TestBuildBrightnessReport(t *testing.T) {
	for _, percent := range []int{0, 37, 100} {
		t.Run(fmt.Sprintf("percent_%d", percent), func(t *testing.T) {
			report, err := buildBrightnessReport(percent)
			if err != nil {
				t.Fatalf("buildBrightnessReport(%d): %v", percent, err)
			}
			if len(report) != featureReportSize {
				t.Fatalf("length = %d, want %d", len(report), featureReportSize)
			}
			if report[0] != featureReportID || report[1] != commandSetBrightness || report[2] != byte(percent) {
				t.Fatalf("header = % x", report[:3])
			}
			for _, value := range report[3:] {
				if value != 0 {
					t.Fatal("feature report is not zero-padded")
				}
			}
		})
	}
}

func TestBuildBrightnessReportRejectsInvalidValues(t *testing.T) {
	for _, percent := range []int{-1, 101} {
		if _, err := buildBrightnessReport(percent); err == nil {
			t.Fatalf("brightness %d returned nil error", percent)
		}
	}
}

func TestSetBrightnessSendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := &Deck{device: fake}
	if err := deck.SetBrightness(70); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	if len(fake.featureReports) != 1 {
		t.Fatalf("feature reports = %d, want 1", len(fake.featureReports))
	}
	want, _ := buildBrightnessReport(70)
	if string(fake.featureReports[0]) != string(want) {
		t.Fatalf("feature report = % x, want % x", fake.featureReports[0], want)
	}
}

func TestSetBrightnessRejectsShortFeatureWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortFeatureWrite: true}
	deck := &Deck{device: fake}
	if err := deck.SetBrightness(70); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}
