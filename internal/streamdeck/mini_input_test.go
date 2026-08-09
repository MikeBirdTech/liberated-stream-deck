package streamdeck

import (
	"reflect"
	"testing"
)

func TestMiniInputDecoderEmitsSnapshotTransitions(t *testing.T) {
	var decoder miniInputDecoder

	result, err := decoder.Decode(miniKeyReport(true, false, false, false, false, true))
	if err != nil {
		t.Fatalf("Decode first snapshot: %v", err)
	}
	assertInputEvents(t, result.Events,
		KeyEvent{Key: 0, Pressed: true},
		KeyEvent{Key: 5, Pressed: true},
	)

	result, err = decoder.Decode(miniKeyReport(true, false, true, false, false, false))
	if err != nil {
		t.Fatalf("Decode changed snapshot: %v", err)
	}
	assertInputEvents(t, result.Events,
		KeyEvent{Key: 2, Pressed: true},
		KeyEvent{Key: 5, Pressed: false},
	)

	result, err = decoder.Decode(miniKeyReport(true, false, true, false, false, false))
	if err != nil {
		t.Fatalf("Decode unchanged snapshot: %v", err)
	}
	assertInputEvents(t, result.Events)
}

func TestMiniInputDecoderRejectsInvalidStateWithoutChangingSnapshot(t *testing.T) {
	var decoder miniInputDecoder
	invalid := miniKeyReport(false, false, false, false, false, false)
	invalid[1] = 0x02
	if _, err := decoder.Decode(invalid); err == nil {
		t.Fatal("invalid state byte returned nil error")
	}

	result, err := decoder.Decode(miniKeyReport(true, false, false, false, false, false))
	if err != nil {
		t.Fatalf("Decode valid snapshot: %v", err)
	}
	assertInputEvents(t, result.Events, KeyEvent{Key: 0, Pressed: true})
}

func TestDecodeMiniKeySnapshotValidation(t *testing.T) {
	for name, report := range map[string][]byte{
		"empty":           nil,
		"short":           {miniInputReportID, 0, 0},
		"wrong report ID": {0x02, 0, 0, 0, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMiniKeySnapshot(report); err == nil {
				t.Fatal("decodeMiniKeySnapshot returned nil error")
			}
		})
	}
}

func TestMiniReadEventsUsesMiniReportShape(t *testing.T) {
	fake := &fakeHIDDevice{reads: [][]byte{miniKeyReport(false, true, false, false, false, false)}}
	mini := newMini(fake)
	result, err := mini.ReadEvents(0)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	want := []Event{KeyEvent{Key: 1, Pressed: true}}
	if !reflect.DeepEqual(result.Events, want) {
		t.Fatalf("events = %#v, want %#v", result.Events, want)
	}
}

func miniKeyReport(states ...bool) []byte {
	report := make([]byte, miniInputReportSize)
	report[0] = miniInputReportID
	for index, pressed := range states {
		if pressed {
			report[index+1] = 0x01
		}
	}
	return report
}
