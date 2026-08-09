package streamdeck

import "fmt"

type miniInputDecoder struct {
	state [MiniKeyCount]bool
}

func (d *miniInputDecoder) Decode(report []byte) (InputRead, error) {
	next, err := decodeMiniKeySnapshot(report)
	if err != nil {
		return InputRead{}, err
	}

	result := InputRead{}
	for index := range next {
		if next[index] != d.state[index] {
			result.Events = append(result.Events, KeyEvent{Key: index, Pressed: next[index]})
		}
	}
	d.state = next
	return result, nil
}

func decodeMiniKeySnapshot(report []byte) ([MiniKeyCount]bool, error) {
	var snapshot [MiniKeyCount]bool
	if len(report) < MiniKeyCount+1 {
		return snapshot, fmt.Errorf("Mini input report has %d bytes, want at least %d", len(report), MiniKeyCount+1)
	}
	if report[0] != miniInputReportID {
		return snapshot, fmt.Errorf("unexpected Mini input report ID 0x%02x", report[0])
	}

	for index, value := range report[1 : MiniKeyCount+1] {
		switch value {
		case 0x00:
		case 0x01:
			snapshot[index] = true
		default:
			return snapshot, fmt.Errorf("key %d has invalid state byte 0x%02x", index, value)
		}
	}
	return snapshot, nil
}
