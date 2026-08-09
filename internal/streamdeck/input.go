package streamdeck

import "fmt"

type keyDecoder struct {
	initialized bool
	state       [KeyCount]bool
}

func decodeKeySnapshot(report []byte) ([KeyCount]bool, error) {
	var snapshot [KeyCount]bool

	command, payload, err := inputPayload(report)
	if err != nil {
		return snapshot, err
	}
	if command != commandKeyState {
		return snapshot, errNotKeyReport
	}
	if len(payload) != KeyCount {
		return snapshot, fmt.Errorf("key payload has %d bytes, want %d", len(payload), KeyCount)
	}

	for index, value := range payload {
		switch value {
		case 0x00:
			snapshot[index] = false
		case 0x01:
			snapshot[index] = true
		default:
			return snapshot, fmt.Errorf("key %d has invalid state byte 0x%02x", index, value)
		}
	}

	return snapshot, nil
}

func (d *keyDecoder) Decode(report []byte) (KeyRead, error) {
	next, err := decodeKeySnapshot(report)
	if err != nil {
		return KeyRead{}, err
	}

	if !d.initialized {
		d.initialized = true
		d.state = next
		baseline := KeySnapshot{Pressed: next}
		return KeyRead{Baseline: &baseline}, nil
	}

	events := make([]KeyEvent, 0, KeyCount)
	for index := range next {
		if next[index] == d.state[index] {
			continue
		}
		events = append(events, KeyEvent{Key: index, Pressed: next[index]})
	}
	d.state = next

	return KeyRead{Events: events}, nil
}
