package streamdeck

// ShowLogo immediately displays the device's persisted power-on frame
// (written through the undocumented 0x09 channel), without a power cycle.
// It is the documented setter feature report 0x03/0x02 ("Show Logo", no
// parameters) and renders on top of whatever the LCD currently shows.
func (d *Deck) ShowLogo() error {
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandShowLogo
	return d.handle.sendFeatureReport("show logo", report)
}
