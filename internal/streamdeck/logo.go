package streamdeck

// ShowLogo forcibly displays the device's boot logo: the persisted boot
// frame written through the undocumented 0x09 channel, shown immediately
// without a power cycle. It is a documented setter feature report
// (0x03/0x02) with no parameters and renders on top of whatever the LCD
// currently shows.
func (d *Deck) ShowLogo() error {
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandShowLogo
	return d.handle.sendFeatureReport("show logo", report)
}
