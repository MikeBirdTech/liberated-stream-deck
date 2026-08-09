package streamdeck

import (
	"encoding/binary"
	"errors"
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
