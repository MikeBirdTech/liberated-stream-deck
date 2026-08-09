package streamdeck

import (
	"encoding/binary"
	"fmt"
)

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

type dialButtonDecoder struct {
	initialized bool
	state       [DialCount]bool
}

type inputDecoder struct {
	keys        keyDecoder
	dialButtons dialButtonDecoder
}

func (d *inputDecoder) Decode(report []byte) (InputRead, error) {
	command, payload, err := inputPayload(report)
	if err != nil {
		return InputRead{}, err
	}

	switch command {
	case commandKeyState:
		result, err := d.keys.Decode(report)
		if err != nil {
			return InputRead{}, err
		}
		input := InputRead{KeyBaseline: result.Baseline}
		for _, event := range result.Events {
			input.Events = append(input.Events, event)
		}
		return input, nil
	case commandEncoder:
		return d.decodeEncoder(payload)
	case commandTouch:
		return decodeTouch(payload)
	default:
		return InputRead{Diagnostics: []string{fmt.Sprintf("unknown input command 0x%02x", command)}}, nil
	}
}

func (d *inputDecoder) decodeEncoder(payload []byte) (InputRead, error) {
	if len(payload) != DialCount+1 {
		return InputRead{}, fmt.Errorf("encoder payload has %d bytes, want %d", len(payload), DialCount+1)
	}

	switch payload[0] {
	case encoderContentButtons:
		next, err := decodeDialButtonSnapshot(payload[1:])
		if err != nil {
			return InputRead{}, err
		}
		if !d.dialButtons.initialized {
			d.dialButtons.initialized = true
			d.dialButtons.state = next
			baseline := DialButtonSnapshot{Pressed: next}
			return InputRead{DialBaseline: &baseline}, nil
		}

		result := InputRead{}
		for index := range next {
			if next[index] != d.dialButtons.state[index] {
				result.Events = append(result.Events, DialPressEvent{Dial: index, Pressed: next[index]})
			}
		}
		d.dialButtons.state = next
		return result, nil

	case encoderContentRotation:
		result := InputRead{}
		for index, value := range payload[1:] {
			delta := int(int8(value))
			if delta != 0 {
				result.Events = append(result.Events, DialRotateEvent{Dial: index, Delta: delta})
			}
		}
		return result, nil

	default:
		return InputRead{Diagnostics: []string{fmt.Sprintf("unknown encoder contents type 0x%02x", payload[0])}}, nil
	}
}

func decodeDialButtonSnapshot(payload []byte) ([DialCount]bool, error) {
	var snapshot [DialCount]bool
	if len(payload) != DialCount {
		return snapshot, fmt.Errorf("encoder button payload has %d state bytes, want %d", len(payload), DialCount)
	}
	for index, value := range payload {
		switch value {
		case 0x00:
		case 0x01:
			snapshot[index] = true
		default:
			return snapshot, fmt.Errorf("dial %d has invalid state byte 0x%02x", index, value)
		}
	}
	return snapshot, nil
}

func decodeTouch(payload []byte) (InputRead, error) {
	if len(payload) == 0 {
		return InputRead{}, fmt.Errorf("touch payload is empty")
	}

	var event TouchEvent
	switch payload[0] {
	case touchContentTap, touchContentPress:
		// Elgato documents a 10-byte payload for TAP and PRESS. The tested
		// Stream Deck Plus firmware emitted TAP with the same documented fields
		// followed by four additional reserved zero bytes, for a 14-byte
		// payload. Accept that uniform maximum-length shape for both types.
		if len(payload) != 0x0a && len(payload) != 0x0e {
			return InputRead{}, fmt.Errorf("touch type 0x%02x payload has %d bytes, want documented 10 or observed 14", payload[0], len(payload))
		}
		if payload[0] == touchContentTap {
			event.Kind = TouchTap
		} else {
			event.Kind = TouchPress
		}
		event.X = int(binary.LittleEndian.Uint16(payload[2:4]))
		event.Y = int(binary.LittleEndian.Uint16(payload[4:6]))

	case touchContentFlick:
		if len(payload) != 0x0e {
			return InputRead{}, fmt.Errorf("flick payload has %d bytes, want 14", len(payload))
		}
		event.Kind = TouchFlick
		event.StartX = int(binary.LittleEndian.Uint16(payload[2:4]))
		event.StartY = int(binary.LittleEndian.Uint16(payload[4:6]))
		event.EndX = int(binary.LittleEndian.Uint16(payload[6:8]))
		event.EndY = int(binary.LittleEndian.Uint16(payload[8:10]))

	default:
		return InputRead{Diagnostics: []string{fmt.Sprintf("unknown touch contents type 0x%02x", payload[0])}}, nil
	}

	result := InputRead{Events: []Event{event}}
	if anomaly := touchCoordinateAnomaly(event); anomaly != "" {
		result.Diagnostics = append(result.Diagnostics, anomaly)
	}
	return result, nil
}

func touchCoordinateAnomaly(event TouchEvent) string {
	valid := func(x, y int) bool {
		return x >= 0 && x < TouchStripWidth && y >= 0 && y < TouchStripHeight
	}
	switch event.Kind {
	case TouchTap, TouchPress:
		if !valid(event.X, event.Y) {
			return fmt.Sprintf("touch coordinate outside %dx%d logical window: %s %d,%d", TouchStripWidth, TouchStripHeight, event.Kind, event.X, event.Y)
		}
	case TouchFlick:
		if !valid(event.StartX, event.StartY) || !valid(event.EndX, event.EndY) {
			return fmt.Sprintf("touch coordinate outside %dx%d logical window: FLICK %d,%d -> %d,%d", TouchStripWidth, TouchStripHeight, event.StartX, event.StartY, event.EndX, event.EndY)
		}
	}
	return ""
}
