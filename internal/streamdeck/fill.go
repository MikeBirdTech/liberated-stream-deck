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
