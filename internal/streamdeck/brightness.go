package streamdeck

import "fmt"

// SetBrightness sets the LCD backlight percentage. Values outside 0..100 are
// rejected; callers own any desired clamping policy.
func (d *Deck) SetBrightness(percent int) error {
	report, err := buildBrightnessReport(percent)
	if err != nil {
		return err
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.deviceMu.RLock()
	defer d.deviceMu.RUnlock()
	if d.closed {
		return ErrClosed
	}
	n, err := d.device.SendFeatureReport(report)
	if err != nil {
		return fmt.Errorf("set brightness to %d: %w", percent, err)
	}
	if n != len(report) {
		return fmt.Errorf("set brightness to %d: wrote %d bytes, want %d", percent, n, len(report))
	}
	return nil
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
