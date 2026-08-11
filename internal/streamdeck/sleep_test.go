package streamdeck

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestBuildSetSleepDurationReport(t *testing.T) {
	for _, seconds := range []int{0, 1, 60, 900, math.MaxInt32} {
		t.Run(fmt.Sprintf("seconds_%d", seconds), func(t *testing.T) {
			report, err := buildSetSleepDurationReport(seconds)
			if err != nil {
				t.Fatalf("buildSetSleepDurationReport(%d): %v", seconds, err)
			}
			if len(report) != featureReportSize {
				t.Fatalf("length = %d, want %d", len(report), featureReportSize)
			}
			if report[0] != featureReportID || report[1] != commandSetSleepDuration {
				t.Fatalf("header = % x", report[:2])
			}
			if got := binary.LittleEndian.Uint32(report[2:6]); got != uint32(seconds) {
				t.Fatalf("duration = %d, want %d", got, seconds)
			}
			for _, value := range report[6:] {
				if value != 0 {
					t.Fatal("feature report is not zero-padded")
				}
			}
		})
	}
}

func TestBuildSetSleepDurationReportRejectsInvalidValues(t *testing.T) {
	for _, seconds := range []int{-1, math.MaxInt32 + 1} {
		if _, err := buildSetSleepDurationReport(seconds); err == nil {
			t.Fatalf("sleep duration %d returned nil error", seconds)
		}
	}
}

func TestSetSleepDurationSendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.SetSleepDuration(60); err != nil {
		t.Fatalf("SetSleepDuration: %v", err)
	}
	if len(fake.featureReports) != 1 {
		t.Fatalf("feature reports = %d, want 1", len(fake.featureReports))
	}
	want, _ := buildSetSleepDurationReport(60)
	if string(fake.featureReports[0]) != string(want) {
		t.Fatalf("feature report = % x, want % x", fake.featureReports[0], want)
	}
}

func TestSetSleepDurationRejectsShortFeatureWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortFeatureWrite: true}
	deck := newPlus(fake)
	if err := deck.SetSleepDuration(60); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}

func TestSetSleepDurationRejectsInvalidValueWithoutWriting(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.SetSleepDuration(-1); err == nil {
		t.Fatal("negative sleep duration returned nil error")
	}
	if len(fake.featureReports) != 0 {
		t.Fatalf("feature reports = %d, want 0 (invalid value must not reach the device)", len(fake.featureReports))
	}
}
