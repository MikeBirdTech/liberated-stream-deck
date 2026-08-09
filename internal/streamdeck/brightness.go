package streamdeck

import "fmt"

// SetBrightness sets the LCD backlight percentage. Values outside 0..100 are
// rejected; callers own any desired clamping policy.
func (d *Deck) SetBrightness(percent int) error {
	report, err := buildBrightnessReport(percent)
	if err != nil {
		return err
	}
	return d.handle.sendFeatureReport(fmt.Sprintf("set brightness to %d", percent), report)
}

func buildBrightnessReport(percent int) ([]byte, error) {
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("brightness %d out of range 0..100", percent)
	}
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandSetBrightness
	report[2] = byte(percent)
	return report, nil
}

// SetBrightness sets the Mini LCD backlight percentage. Values outside 0..100
// are rejected; callers own any desired clamping policy.
func (d *Mini) SetBrightness(percent int) error {
	report, err := buildMiniBrightnessReport(percent)
	if err != nil {
		return err
	}
	return d.handle.sendFeatureReport(fmt.Sprintf("set brightness to %d", percent), report)
}

func buildMiniBrightnessReport(percent int) ([]byte, error) {
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("brightness %d out of range 0..100", percent)
	}
	report := make([]byte, miniFeatureReportSize)
	report[0] = miniBrightnessReportID
	report[1] = miniCommandSetBrightness
	report[2] = miniBrightnessMagic0
	report[3] = miniBrightnessMagic1
	report[4] = miniBrightnessMagic2
	report[5] = byte(percent)
	return report, nil
}
