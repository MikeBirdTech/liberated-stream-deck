package streamdeck

import (
	"fmt"
	"testing"
)

func TestBuildMiniBrightnessReport(t *testing.T) {
	for _, percent := range []int{0, 37, 100} {
		t.Run(fmt.Sprintf("percent_%d", percent), func(t *testing.T) {
			report, err := buildMiniBrightnessReport(percent)
			if err != nil {
				t.Fatalf("buildMiniBrightnessReport(%d): %v", percent, err)
			}
			if len(report) != miniFeatureReportSize {
				t.Fatalf("length = %d, want %d", len(report), miniFeatureReportSize)
			}
			wantHeader := []byte{0x05, 0x55, 0xaa, 0xd1, 0x01, byte(percent)}
			if string(report[:6]) != string(wantHeader) {
				t.Fatalf("header = % x, want % x", report[:6], wantHeader)
			}
			for _, value := range report[6:] {
				if value != 0 {
					t.Fatal("feature report is not zero-padded")
				}
			}
		})
	}
}

func TestBuildMiniBrightnessReportRejectsInvalidValues(t *testing.T) {
	for _, percent := range []int{-1, 101} {
		if _, err := buildMiniBrightnessReport(percent); err == nil {
			t.Fatalf("brightness %d returned nil error", percent)
		}
	}
}

func TestMiniSetBrightnessSendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	mini := newMini(fake)
	if err := mini.SetBrightness(70); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	want, _ := buildMiniBrightnessReport(70)
	if len(fake.featureReports) != 1 || string(fake.featureReports[0]) != string(want) {
		t.Fatalf("feature reports = % x, want % x", fake.featureReports, want)
	}
}

func TestMiniSetBrightnessRejectsShortFeatureWrite(t *testing.T) {
	mini := newMini(&fakeHIDDevice{shortFeatureWrite: true})
	if err := mini.SetBrightness(70); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}
