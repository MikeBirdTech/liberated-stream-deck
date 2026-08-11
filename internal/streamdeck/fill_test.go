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

func TestBuildFillKeyReport(t *testing.T) {
	for _, test := range []struct {
		index   int
		r, g, b uint8
	}{
		{index: 0, r: 255, g: 0, b: 0},
		{index: 7, r: 12, g: 34, b: 56},
	} {
		report, err := buildFillKeyReport(test.index, test.r, test.g, test.b)
		if err != nil {
			t.Fatalf("buildFillKeyReport(%d): %v", test.index, err)
		}
		if len(report) != featureReportSize {
			t.Fatalf("length = %d, want %d", len(report), featureReportSize)
		}
		if report[0] != featureReportID || report[1] != commandFillKey {
			t.Fatalf("header = % x", report[:2])
		}
		if report[2] != byte(test.index) {
			t.Fatalf("index = %d, want %d", report[2], test.index)
		}
		if report[3] != test.r || report[4] != test.g || report[5] != test.b {
			t.Fatalf("rgb = % x, want % x", report[3:6], [3]uint8{test.r, test.g, test.b})
		}
		for _, value := range report[6:] {
			if value != 0 {
				t.Fatal("feature report is not zero-padded")
			}
		}
	}
}

func TestBuildFillKeyReportRejectsInvalidIndex(t *testing.T) {
	for _, index := range []int{-1, KeyCount, 100} {
		if _, err := buildFillKeyReport(index, 0, 0, 0); err == nil {
			t.Fatalf("key index %d returned nil error", index)
		}
	}
}

func TestFillKeySendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.FillKey(3, 1, 2, 3); err != nil {
		t.Fatalf("FillKey: %v", err)
	}
	if len(fake.featureReports) != 1 {
		t.Fatalf("feature reports = %d, want 1", len(fake.featureReports))
	}
	want, _ := buildFillKeyReport(3, 1, 2, 3)
	if string(fake.featureReports[0]) != string(want) {
		t.Fatalf("feature report = % x, want % x", fake.featureReports[0], want)
	}
}

func TestFillKeyRejectsShortFeatureWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortFeatureWrite: true}
	deck := newPlus(fake)
	if err := deck.FillKey(0, 0, 0, 0); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}

func TestFillKeyRejectsInvalidIndexWithoutWriting(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.FillKey(KeyCount, 1, 2, 3); err == nil {
		t.Fatal("out-of-range key index returned nil error")
	}
	if len(fake.featureReports) != 0 {
		t.Fatalf("feature reports = %d, want 0 (invalid index must not reach the device)", len(fake.featureReports))
	}
}
