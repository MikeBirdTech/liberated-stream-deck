package streamdeck

import "fmt"

// FillLCD fills the entire LCD with one RGB color. It is a documented setter
// feature report (0x03/0x05) with an RGB triplet; the fill is volatile and
// is replaced by the next image upload.
func (d *Deck) FillLCD(r, g, b uint8) error {
	return d.handle.sendFeatureReport(fmt.Sprintf("fill LCD with #%02x%02x%02x", r, g, b), buildFillLCDReport(r, g, b))
}

func buildFillLCDReport(r, g, b uint8) []byte {
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandFillLCD
	report[2] = r
	report[3] = g
	report[4] = b
	return report
}

// FillKey fills one LCD key with a single RGB color. It is a documented
// setter feature report (0x03/0x06) with a key index and RGB triplet; the
// fill is volatile and is replaced by the next image upload.
func (d *Deck) FillKey(index int, r, g, b uint8) error {
	report, err := buildFillKeyReport(index, r, g, b)
	if err != nil {
		return err
	}
	return d.handle.sendFeatureReport(fmt.Sprintf("fill key %d with #%02x%02x%02x", index, r, g, b), report)
}

func buildFillKeyReport(index int, r, g, b uint8) ([]byte, error) {
	if index < 0 || index >= KeyCount {
		return nil, fmt.Errorf("key index %d out of range 0..%d", index, KeyCount-1)
	}
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandFillKey
	report[2] = byte(index)
	report[3] = r
	report[4] = g
	report[5] = b
	return report, nil
}
