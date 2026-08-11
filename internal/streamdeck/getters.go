package streamdeck

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// FirmwareVersion is one firmware component version reported by the device.
// The checksum is the 32-bit value the device includes with the version
// string; it is returned for diagnostics and not interpreted.
type FirmwareVersion struct {
	Version  string
	Checksum uint32
}

// UnitInfo is the hardware description returned by the documented getter
// feature report 0x08 (keypad matrix, key and LCD geometry, image gallery).
type UnitInfo struct {
	MatrixRows    uint8
	MatrixColumns uint8
	KeyWidth      uint16
	KeyHeight     uint16
	LCDWidth      uint16
	LCDHeight     uint16
	ImageBPP      uint8
	ColorScheme   uint8
	GalleryKeys   uint8
	GalleryLCD    uint8
	DemoFrames    uint8
}

// FirmwareVersionLD returns the LD firmware version string (report ID 0x04).
func (d *Deck) FirmwareVersionLD() (FirmwareVersion, error) {
	return d.firmwareVersion(0x04, "LD")
}

// FirmwareVersionAP2 returns the primary AP2 firmware version string
// (report ID 0x05).
func (d *Deck) FirmwareVersionAP2() (FirmwareVersion, error) {
	return d.firmwareVersion(0x05, "AP2")
}

// FirmwareVersionAP1 returns the AP1 firmware version string (report ID
// 0x07).
func (d *Deck) FirmwareVersionAP1() (FirmwareVersion, error) {
	return d.firmwareVersion(0x07, "AP1")
}

func (d *Deck) firmwareVersion(reportID byte, name string) (FirmwareVersion, error) {
	report, err := d.getFeature(fmt.Sprintf("get %s firmware version", name), reportID)
	if err != nil {
		return FirmwareVersion{}, err
	}
	return parseFirmwareVersion(report, reportID, name)
}

func parseFirmwareVersion(report []byte, reportID byte, name string) (FirmwareVersion, error) {
	if len(report) < 14 {
		return FirmwareVersion{}, fmt.Errorf(
			"%s firmware response too short: %d bytes, want at least 14", name, len(report),
		)
	}
	if report[0] != reportID {
		return FirmwareVersion{}, fmt.Errorf(
			"%s firmware response report ID 0x%02x, want 0x%02x", name, report[0], reportID,
		)
	}
	version := strings.TrimRight(string(report[6:14]), "\x00\x20")
	return FirmwareVersion{Version: version, Checksum: binary.LittleEndian.Uint32(report[2:6])}, nil
}

// UnitSerialNumber returns the unit's serial number string (report ID 0x06).
func (d *Deck) UnitSerialNumber() (string, error) {
	report, err := d.getFeature("get unit serial number", 0x06)
	if err != nil {
		return "", err
	}
	return parseSerialNumber(report)
}

func parseSerialNumber(report []byte) (string, error) {
	if len(report) < 2 {
		return "", fmt.Errorf("serial response too short: %d bytes", len(report))
	}
	if report[0] != 0x06 {
		return "", fmt.Errorf("serial response report ID 0x%02x, want 0x06", report[0])
	}
	length := int(report[1])
	if length == 0 || 2+length > len(report) {
		return "", fmt.Errorf("serial response length %d invalid for %d-byte report", report[1], len(report))
	}
	return strings.TrimRight(string(report[2:2+length]), "\x00\x20"), nil
}

// UnitInfo returns the documented hardware description (report ID 0x08).
func (d *Deck) UnitInfo() (UnitInfo, error) {
	report, err := d.getFeature("get unit information", 0x08)
	if err != nil {
		return UnitInfo{}, err
	}
	return parseUnitInfo(report)
}

func parseUnitInfo(report []byte) (UnitInfo, error) {
	if len(report) < 0x11 {
		return UnitInfo{}, fmt.Errorf("unit info response too short: %d bytes, want at least 17", len(report))
	}
	if report[0] != 0x08 {
		return UnitInfo{}, fmt.Errorf("unit info response report ID 0x%02x, want 0x08", report[0])
	}
	return UnitInfo{
		MatrixRows:    report[0x01],
		MatrixColumns: report[0x02],
		KeyWidth:      binary.LittleEndian.Uint16(report[0x03:0x05]),
		KeyHeight:     binary.LittleEndian.Uint16(report[0x05:0x07]),
		LCDWidth:      binary.LittleEndian.Uint16(report[0x07:0x09]),
		LCDHeight:     binary.LittleEndian.Uint16(report[0x09:0x0b]),
		ImageBPP:      report[0x0b],
		ColorScheme:   report[0x0c],
		GalleryKeys:   report[0x0d],
		GalleryLCD:    report[0x0e],
		DemoFrames:    report[0x0f],
	}, nil
}

// SleepDuration returns the configured idle time before sleep in seconds
// (report ID 0x0A); zero means sleep is disabled.
func (d *Deck) SleepDuration() (int, error) {
	report, err := d.getFeature("get sleep duration", 0x0a)
	if err != nil {
		return 0, err
	}
	return parseSleepDuration(report)
}

func parseSleepDuration(report []byte) (int, error) {
	if len(report) < 6 {
		return 0, fmt.Errorf("sleep duration response too short: %d bytes, want at least 6", len(report))
	}
	if report[0] != 0x0a {
		return 0, fmt.Errorf("sleep duration response report ID 0x%02x, want 0x0a", report[0])
	}
	return int(int32(binary.LittleEndian.Uint32(report[2:6]))), nil
}

func (d *Deck) getFeature(name string, reportID byte) ([]byte, error) {
	report := make([]byte, featureReportSize)
	report[0] = reportID
	return d.handle.getFeatureReport(name, report)
}
