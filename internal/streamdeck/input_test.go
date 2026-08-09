package streamdeck

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestKeyDecoderUsesFirstValidReportAsBaseline(t *testing.T) {
	var decoder keyDecoder
	baselineReport := keyReport(t, true, false, false, false, false, false, false, false)

	result, err := decoder.Decode(baselineReport)
	if err != nil {
		t.Fatalf("Decode baseline: %v", err)
	}
	if result.Baseline == nil {
		t.Fatal("baseline is nil")
	}
	if !result.Baseline.Pressed[0] {
		t.Fatal("baseline did not retain key 1 pressed state")
	}
	if len(result.Events) != 0 {
		t.Fatalf("baseline emitted %d transition events, want 0", len(result.Events))
	}

	result, err = decoder.Decode(keyReport(t, false, false, false, false, false, false, false, false))
	if err != nil {
		t.Fatalf("Decode release: %v", err)
	}
	assertKeyEvents(t, result.Events, KeyEvent{Key: 0, Pressed: false})

	result, err = decoder.Decode(keyReport(t, true, false, false, false, false, false, false, false))
	if err != nil {
		t.Fatalf("Decode press: %v", err)
	}
	assertKeyEvents(t, result.Events, KeyEvent{Key: 0, Pressed: true})
}

func TestKeyDecoderEmitsEveryChangedKey(t *testing.T) {
	var decoder keyDecoder
	if _, err := decoder.Decode(keyReport(t, false, false, false, false, false, false, false, false)); err != nil {
		t.Fatalf("Decode baseline: %v", err)
	}

	result, err := decoder.Decode(keyReport(t, true, false, false, false, false, false, false, true))
	if err != nil {
		t.Fatalf("Decode changes: %v", err)
	}
	assertKeyEvents(
		t,
		result.Events,
		KeyEvent{Key: 0, Pressed: true},
		KeyEvent{Key: 7, Pressed: true},
	)

	result, err = decoder.Decode(keyReport(t, true, false, false, false, false, false, false, true))
	if err != nil {
		t.Fatalf("Decode unchanged snapshot: %v", err)
	}
	if len(result.Events) != 0 {
		t.Fatalf("unchanged snapshot emitted %d events", len(result.Events))
	}
}

func TestInputDecoderMapsAllKeyIndexes(t *testing.T) {
	var decoder inputDecoder
	if _, err := decoder.Decode(keyReport(t, false, false, false, false, false, false, false, false)); err != nil {
		t.Fatalf("Decode baseline: %v", err)
	}

	for key := 0; key < KeyCount; key++ {
		states := make([]bool, KeyCount)
		states[key] = true
		result, err := decoder.Decode(keyReport(t, states...))
		if err != nil {
			t.Fatalf("Decode key %d press: %v", key, err)
		}
		assertInputEvents(t, result.Events, KeyEvent{Key: key, Pressed: true})

		result, err = decoder.Decode(keyReport(t, false, false, false, false, false, false, false, false))
		if err != nil {
			t.Fatalf("Decode key %d release: %v", key, err)
		}
		assertInputEvents(t, result.Events, KeyEvent{Key: key, Pressed: false})
	}
}

func TestDialButtonDecoderUsesFirstValidReportAsBaseline(t *testing.T) {
	var decoder inputDecoder
	result, err := decoder.Decode(encoderButtonReport(true, false, false, false))
	if err != nil {
		t.Fatalf("Decode baseline: %v", err)
	}
	if result.DialBaseline == nil || !result.DialBaseline.Pressed[0] {
		t.Fatalf("dial baseline = %+v", result.DialBaseline)
	}
	if len(result.Events) != 0 {
		t.Fatalf("baseline events = %v, want none", result.Events)
	}

	result, err = decoder.Decode(encoderButtonReport(false, false, false, true))
	if err != nil {
		t.Fatalf("Decode transitions: %v", err)
	}
	assertInputEvents(t, result.Events,
		DialPressEvent{Dial: 0, Pressed: false},
		DialPressEvent{Dial: 3, Pressed: true},
	)

	result, err = decoder.Decode(encoderButtonReport(false, false, false, false))
	if err != nil {
		t.Fatalf("Decode dial 4 release: %v", err)
	}
	assertInputEvents(t, result.Events, DialPressEvent{Dial: 3, Pressed: false})
}

func TestDialRotationDecodesSignedAndSimultaneousDeltas(t *testing.T) {
	var decoder inputDecoder
	result, err := decoder.Decode(encoderRotationReport(1, -1, 5, -5))
	if err != nil {
		t.Fatalf("Decode rotations: %v", err)
	}
	assertInputEvents(t, result.Events,
		DialRotateEvent{Dial: 0, Delta: 1},
		DialRotateEvent{Dial: 1, Delta: -1},
		DialRotateEvent{Dial: 2, Delta: 5},
		DialRotateEvent{Dial: 3, Delta: -5},
	)
}

func TestDialRotationFFIsNegativeOne(t *testing.T) {
	var decoder inputDecoder
	report := inputReport(commandEncoder, []byte{encoderContentRotation, 0xff, 0, 0, 0})
	result, err := decoder.Decode(report)
	if err != nil {
		t.Fatalf("Decode rotation: %v", err)
	}
	assertInputEvents(t, result.Events, DialRotateEvent{Dial: 0, Delta: -1})
}

func TestDialRotationOmitsZeroDeltas(t *testing.T) {
	var decoder inputDecoder
	result, err := decoder.Decode(encoderRotationReport(0, 0, 0, 0))
	if err != nil {
		t.Fatalf("Decode rotations: %v", err)
	}
	if len(result.Events) != 0 {
		t.Fatalf("events = %v, want none", result.Events)
	}
}

func TestDecodeTouchTapAndPress(t *testing.T) {
	for _, test := range []struct {
		name string
		kind byte
		want TouchEvent
	}{
		{name: "tap", kind: touchContentTap, want: TouchEvent{Kind: TouchTap, X: 513, Y: 87}},
		{name: "press", kind: touchContentPress, want: TouchEvent{Kind: TouchPress, X: 513, Y: 87}},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := make([]byte, 0x0a)
			payload[0] = test.kind
			binary.LittleEndian.PutUint16(payload[2:4], 513)
			binary.LittleEndian.PutUint16(payload[4:6], 87)
			result, err := decodeTouch(payload)
			if err != nil {
				t.Fatalf("decodeTouch: %v", err)
			}
			assertInputEvents(t, result.Events, test.want)
			if len(result.Diagnostics) != 0 {
				t.Fatalf("diagnostics = %v", result.Diagnostics)
			}
		})
	}
}

func TestDecodeTouchTapAcceptsObservedFourteenBytePayload(t *testing.T) {
	payload := make([]byte, 0x0e)
	payload[0] = touchContentTap
	payload[1] = 1
	binary.LittleEndian.PutUint16(payload[2:4], 372)
	binary.LittleEndian.PutUint16(payload[4:6], 63)
	result, err := decodeTouch(payload)
	if err != nil {
		t.Fatalf("decodeTouch: %v", err)
	}
	assertInputEvents(t, result.Events, TouchEvent{Kind: TouchTap, X: 372, Y: 63})
}

func TestDecodeTouchFlickLittleEndianCoordinates(t *testing.T) {
	payload := make([]byte, 0x0e)
	payload[0] = touchContentFlick
	binary.LittleEndian.PutUint16(payload[2:4], 120)
	binary.LittleEndian.PutUint16(payload[4:6], 50)
	binary.LittleEndian.PutUint16(payload[6:8], 690)
	binary.LittleEndian.PutUint16(payload[8:10], 48)
	result, err := decodeTouch(payload)
	if err != nil {
		t.Fatalf("decodeTouch: %v", err)
	}
	assertInputEvents(t, result.Events, TouchEvent{
		Kind: TouchFlick, StartX: 120, StartY: 50, EndX: 690, EndY: 48,
	})
}

func TestDecodeTouchMalformedReports(t *testing.T) {
	tests := map[string][]byte{
		"empty":           nil,
		"short tap":       {touchContentTap, 0, 1, 0, 2, 0},
		"short press":     {touchContentPress, 0, 1, 0, 2, 0},
		"truncated flick": {touchContentFlick, 0, 1, 0, 2, 0, 3, 0, 4, 0},
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeTouch(payload); err == nil {
				t.Fatal("decodeTouch returned nil error")
			}
		})
	}
}

func TestDecodeUnknownTouchTypeIsDiagnostic(t *testing.T) {
	result, err := decodeTouch([]byte{0x7f})
	if err != nil {
		t.Fatalf("decodeTouch: %v", err)
	}
	if len(result.Events) != 0 || len(result.Diagnostics) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestDecodeTouchReportsOutOfRangeCoordinates(t *testing.T) {
	payload := make([]byte, 0x0a)
	payload[0] = touchContentTap
	binary.LittleEndian.PutUint16(payload[2:4], TouchStripWidth)
	binary.LittleEndian.PutUint16(payload[4:6], TouchStripHeight)
	result, err := decodeTouch(payload)
	if err != nil {
		t.Fatalf("decodeTouch: %v", err)
	}
	assertInputEvents(t, result.Events, TouchEvent{Kind: TouchTap, X: TouchStripWidth, Y: TouchStripHeight})
	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want one anomaly", result.Diagnostics)
	}
}

func TestInvalidReportDoesNotEstablishBaseline(t *testing.T) {
	var decoder keyDecoder
	invalid := keyReport(t, false, false, false, false, false, false, false, false)
	invalid[4] = 0x02
	if _, err := decoder.Decode(invalid); err == nil {
		t.Fatal("Decode invalid state returned nil error")
	}

	result, err := decoder.Decode(keyReport(t, false, false, false, false, false, false, false, false))
	if err != nil {
		t.Fatalf("Decode first valid report: %v", err)
	}
	if result.Baseline == nil {
		t.Fatal("first valid report did not establish baseline")
	}
}

func TestDecodeKeySnapshotRejectsMalformedReports(t *testing.T) {
	tests := map[string][]byte{
		"empty":              nil,
		"wrong report ID":    {0x02, commandKeyState, 0x00, 0x00},
		"truncated payload":  {inputReportID, commandKeyState, 0x08, 0x00, 0x00},
		"wrong payload size": {inputReportID, commandKeyState, 0x07, 0x00, 0, 0, 0, 0, 0, 0, 0},
	}
	for name, report := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeKeySnapshot(report); err == nil {
				t.Fatal("decodeKeySnapshot returned nil error")
			}
		})
	}
}

func TestDecodeKeySnapshotIdentifiesOtherCommands(t *testing.T) {
	report := make([]byte, inputHeaderSize)
	report[0] = inputReportID
	report[1] = 0x03
	if _, err := decodeKeySnapshot(report); !errors.Is(err, errNotKeyReport) {
		t.Fatalf("error = %v, want errNotKeyReport", err)
	}
}

func keyReport(t *testing.T, states ...bool) []byte {
	t.Helper()
	if len(states) != KeyCount {
		t.Fatalf("keyReport got %d states, want %d", len(states), KeyCount)
	}
	report := make([]byte, inputHeaderSize+KeyCount)
	report[0] = inputReportID
	report[1] = commandKeyState
	binary.LittleEndian.PutUint16(report[2:4], KeyCount)
	for index, pressed := range states {
		if pressed {
			report[4+index] = 0x01
		}
	}
	return report
}

func assertKeyEvents(t *testing.T, got []KeyEvent, want ...KeyEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("event %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func assertInputEvents(t *testing.T, got []Event, want ...Event) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func inputReport(command byte, payload []byte) []byte {
	report := make([]byte, inputHeaderSize+len(payload))
	report[0] = inputReportID
	report[1] = command
	binary.LittleEndian.PutUint16(report[2:4], uint16(len(payload)))
	copy(report[inputHeaderSize:], payload)
	return report
}

func encoderButtonReport(states ...bool) []byte {
	payload := make([]byte, DialCount+1)
	payload[0] = encoderContentButtons
	for index, pressed := range states {
		if pressed {
			payload[index+1] = 1
		}
	}
	return inputReport(commandEncoder, payload)
}

func encoderRotationReport(deltas ...int8) []byte {
	payload := make([]byte, DialCount+1)
	payload[0] = encoderContentRotation
	for index, delta := range deltas {
		payload[index+1] = byte(delta)
	}
	return inputReport(commandEncoder, payload)
}
