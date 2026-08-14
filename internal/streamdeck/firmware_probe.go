package streamdeck

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	firmwareTransportCommand byte = 0x05
	firmwareTransportTarget  byte = 0x02
	firmwareHeaderSize            = 16

	firmwareProbeMarker = "LIBERATED-FW-PROBE-NOT-AN-IMAGE\n"
)

// FirmwareProbe identifies one fixed diagnostic report for the Stream Deck
// Plus firmware transport. The probes do not accept caller-provided payloads.
type FirmwareProbe string

const (
	// FirmwareProbeIncompleteEmpty sends an initial, non-final report with a
	// deliberately invalid zero payload length.
	FirmwareProbeIncompleteEmpty FirmwareProbe = "incomplete-empty"
	// FirmwareProbeFinalEmpty sends a final report with a deliberately invalid
	// zero payload length.
	FirmwareProbeFinalEmpty FirmwareProbe = "final-empty"
	// FirmwareProbeFinalMarker sends one final report containing a fixed ASCII
	// marker that cannot plausibly be a firmware image.
	FirmwareProbeFinalMarker FirmwareProbe = "final-marker"
)

// FirmwareProbeResult records the exact report sent by ProbeFirmwareTransport.
type FirmwareProbeResult struct {
	Probe         FirmwareProbe
	Header        [firmwareHeaderSize]byte
	PayloadLength int
	PayloadSHA256 [sha256.Size]byte
	ReportSHA256  [sha256.Size]byte
}

// ProbeFirmwareTransport sends one fixed command-0x05 diagnostic report to a
// connected Stream Deck Plus. It is intentionally not a firmware writer: the
// caller cannot supply bytes, and every available probe is empty or contains
// a short unmistakably invalid marker.
func (d *Deck) ProbeFirmwareTransport(probe FirmwareProbe) (FirmwareProbeResult, error) {
	report, result, err := buildFirmwareProbeReport(probe)
	if err != nil {
		return FirmwareProbeResult{}, err
	}
	if err := d.handle.writeReports("firmware probe", 0, [][]byte{report}); err != nil {
		return FirmwareProbeResult{}, err
	}
	return result, nil
}

func buildFirmwareProbeReport(probe FirmwareProbe) ([]byte, FirmwareProbeResult, error) {
	report := make([]byte, outputReportSize)
	report[0] = outputReportID
	report[1] = firmwareTransportCommand
	report[2] = 0 // outer block index
	report[9] = firmwareTransportTarget

	var payload []byte
	switch probe {
	case FirmwareProbeIncompleteEmpty:
		// Both completion flags and the payload length stay zero.
	case FirmwareProbeFinalEmpty:
		report[3] = 1 // outer block complete
		report[4] = 1 // file complete
	case FirmwareProbeFinalMarker:
		report[3] = 1
		report[4] = 1
		payload = []byte(firmwareProbeMarker)
		binary.LittleEndian.PutUint16(report[7:9], uint16(len(payload)))
		copy(report[firmwareHeaderSize:], payload)
	default:
		return nil, FirmwareProbeResult{}, fmt.Errorf("unknown firmware probe %q", probe)
	}

	result := FirmwareProbeResult{
		Probe:         probe,
		PayloadLength: len(payload),
		PayloadSHA256: sha256.Sum256(payload),
		ReportSHA256:  sha256.Sum256(report),
	}
	copy(result.Header[:], report[:firmwareHeaderSize])
	return report, result, nil
}
