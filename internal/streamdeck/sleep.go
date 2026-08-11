package streamdeck

import (
	"encoding/binary"
	"fmt"
	"math"
)

// SetSleepDuration sets the idle time in seconds before the device enters
// sleep mode; zero disables sleep. It is a documented setter feature report
// (0x03/0x0D, INT32 seconds) and the setting is persisted on-device, so a
// value set here survives power cycles until it is changed again.
func (d *Deck) SetSleepDuration(seconds int) error {
	report, err := buildSetSleepDurationReport(seconds)
	if err != nil {
		return err
	}
	return d.handle.sendFeatureReport(fmt.Sprintf("set sleep duration to %ds", seconds), report)
}

func buildSetSleepDurationReport(seconds int) ([]byte, error) {
	if seconds < 0 || seconds > math.MaxInt32 {
		return nil, fmt.Errorf("sleep duration %d out of range 0..%d", seconds, math.MaxInt32)
	}
	report := make([]byte, featureReportSize)
	report[0] = featureReportID
	report[1] = commandSetSleepDuration
	binary.LittleEndian.PutUint32(report[2:6], uint32(seconds))
	return report, nil
}
