package streamdeck

import "testing"

func TestShowLogoSendsCompleteFeatureReport(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.ShowLogo(); err != nil {
		t.Fatalf("ShowLogo: %v", err)
	}
	if len(fake.featureReports) != 1 {
		t.Fatalf("feature reports = %d, want 1", len(fake.featureReports))
	}
	report := fake.featureReports[0]
	if len(report) != featureReportSize {
		t.Fatalf("report length = %d, want %d", len(report), featureReportSize)
	}
	if report[0] != featureReportID || report[1] != commandShowLogo {
		t.Fatalf("header = % x, want %02x %02x", report[:2], featureReportID, commandShowLogo)
	}
	for _, value := range report[2:] {
		if value != 0 {
			t.Fatal("show-logo feature report is not zero-padded")
		}
	}
}

func TestShowLogoRejectsShortFeatureWrite(t *testing.T) {
	fake := &fakeHIDDevice{shortFeatureWrite: true}
	deck := newPlus(fake)
	if err := deck.ShowLogo(); err == nil {
		t.Fatal("short feature write returned nil error")
	}
}
