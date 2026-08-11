package streamdeck

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// VendorID is Elgato's USB vendor ID.
	VendorID uint16 = 0x0fd9
	// ProductID is the USB product ID for Stream Deck Plus.
	ProductID uint16 = 0x0084

	// KeyCount is the number of LCD keys on Stream Deck Plus.
	KeyCount = 8
	// KeyImageWidth is the required LCD key image width in pixels.
	KeyImageWidth = 120
	// KeyImageHeight is the required LCD key image height in pixels.
	KeyImageHeight = 120
	// DialCount is the number of rotary encoders on Stream Deck Plus.
	DialCount = 4
	// TouchStripWidth is the full touch-strip image width in pixels.
	TouchStripWidth = 800
	// TouchStripHeight is the full touch-strip image height in pixels.
	TouchStripHeight = 100
	// LCDImageWidth is the full-screen LCD image width in pixels.
	LCDImageWidth = 800
	// LCDImageHeight is the full-screen LCD image height in pixels.
	LCDImageHeight = 480

	inputReportSize   = 512
	outputReportSize  = 1024
	featureReportSize = 32

	inputReportID   byte = 0x01
	outputReportID  byte = 0x02
	featureReportID byte = 0x03

	commandKeyState       byte = 0x00
	commandTouch          byte = 0x02
	commandEncoder        byte = 0x03
	commandUpdateKeyImage byte = 0x07
	// commandUpdateLCDImage is the output-report command for a full-screen
	// 800x480 LCD image (documented). It shares its byte value with the
	// unrelated feature-report subcommand commandSetBrightness; the two
	// never appear in the same report type.
	commandUpdateLCDImage      byte = 0x08
	commandSetBrightness       byte = 0x08
	commandUpdateTouchStrip    byte = 0x0b
	commandUpdatePartialWindow byte = 0x0c

	encoderContentButtons  byte = 0x00
	encoderContentRotation byte = 0x01

	touchContentTap   byte = 0x01
	touchContentPress byte = 0x02
	touchContentFlick byte = 0x03
)

const inputHeaderSize = 4

var errNotKeyReport = errors.New("not a key-state input report")

func inputPayload(report []byte) (command byte, payload []byte, err error) {
	if len(report) < inputHeaderSize {
		return 0, nil, fmt.Errorf("input report too short: got %d bytes, need at least %d", len(report), inputHeaderSize)
	}
	if report[0] != inputReportID {
		return 0, nil, fmt.Errorf("unexpected input report ID 0x%02x", report[0])
	}

	payloadLength := int(binary.LittleEndian.Uint16(report[2:4]))
	if payloadLength > len(report)-inputHeaderSize {
		return 0, nil, fmt.Errorf(
			"input payload length %d exceeds available %d bytes",
			payloadLength,
			len(report)-inputHeaderSize,
		)
	}

	return report[1], report[inputHeaderSize : inputHeaderSize+payloadLength], nil
}

func meaningfulInputBytes(report []byte) []byte {
	if len(report) < inputHeaderSize {
		return report
	}
	length := inputHeaderSize + int(binary.LittleEndian.Uint16(report[2:4]))
	if length > len(report) {
		return report
	}
	return report[:length]
}
