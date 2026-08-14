// Package firmwarecapture parses offline Stream Deck Plus firmware-update
// report captures. It deliberately contains no HID discovery or write path.
package firmwarecapture

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// ReportSize is the full interrupt output report size used by the Plus.
	ReportSize = 1024
	// HeaderSize is the statically recovered firmware report header size.
	HeaderSize = 16
	// PayloadSize is the largest payload carried by one report.
	PayloadSize = ReportSize - HeaderSize
	// OuterBlockSize is the file block size used by the vendor updater before
	// each block is split into PayloadSize-byte reports.
	OuterBlockSize = 4096

	outputReportID byte = 0x02
	updateCommand  byte = 0x05
	updateTarget   byte = 0x02
)

// Capture is a validated firmware-update report stream and its reassembled
// payload. SHA256 fingerprints the exact file bytes carried by the reports;
// it is not evidence of a device-side signature or authorization check.
type Capture struct {
	ReportCount     int
	OuterBlockCount int
	Payload         []byte
	SHA256          [sha256.Size]byte
}

// Parse validates and reassembles a raw concatenation of 1024-byte firmware
// update output reports. The format is static evidence recovered from the
// Stream Deck 7.4.2 20GBD9901 backend; it has not been validated by a genuine
// firmware capture or hardware update.
func Parse(r io.Reader) (Capture, error) {
	var capture Capture
	expectedOuter := 0
	expectedInner := 0
	outerBytes := 0
	transferDone := false

	for reportIndex := 0; ; reportIndex++ {
		var report [ReportSize]byte
		n, err := io.ReadFull(r, report[:])
		if err == io.EOF && n == 0 {
			break
		}
		if err != nil {
			return Capture{}, fmt.Errorf(
				"report %d is truncated: got %d of %d bytes: %w",
				reportIndex,
				n,
				ReportSize,
				err,
			)
		}
		if transferDone {
			return Capture{}, fmt.Errorf("report %d follows the transfer-done report", reportIndex)
		}

		if report[0] != outputReportID || report[1] != updateCommand {
			return Capture{}, fmt.Errorf(
				"report %d begins %02x %02x, want firmware report 02 05",
				reportIndex,
				report[0],
				report[1],
			)
		}
		if int(report[2]) != expectedOuter {
			return Capture{}, fmt.Errorf(
				"report %d outer index is %d, want %d",
				reportIndex,
				report[2],
				expectedOuter,
			)
		}
		innerIndex := int(binary.LittleEndian.Uint16(report[5:7]))
		if innerIndex != expectedInner {
			return Capture{}, fmt.Errorf(
				"report %d inner index is %d, want %d",
				reportIndex,
				innerIndex,
				expectedInner,
			)
		}
		if report[3] > 1 || report[4] > 1 {
			return Capture{}, fmt.Errorf(
				"report %d has non-boolean done flags %02x %02x",
				reportIndex,
				report[3],
				report[4],
			)
		}
		innerDone := report[3] == 1
		overallDone := report[4] == 1
		if overallDone && !innerDone {
			return Capture{}, fmt.Errorf("report %d marks transfer done before its outer block is done", reportIndex)
		}
		if report[9] != updateTarget {
			return Capture{}, fmt.Errorf(
				"report %d target is %02x, want 02",
				reportIndex,
				report[9],
			)
		}
		if !allZero(report[10:HeaderSize]) {
			return Capture{}, fmt.Errorf("report %d has nonzero reserved header bytes", reportIndex)
		}

		payloadLength := int(binary.LittleEndian.Uint16(report[7:9]))
		if payloadLength < 1 || payloadLength > PayloadSize {
			return Capture{}, fmt.Errorf(
				"report %d payload length is %d, want 1..%d",
				reportIndex,
				payloadLength,
				PayloadSize,
			)
		}
		if !innerDone && payloadLength != PayloadSize {
			return Capture{}, fmt.Errorf(
				"report %d has a short %d-byte payload without the outer-block done flag",
				reportIndex,
				payloadLength,
			)
		}
		if !allZero(report[HeaderSize+payloadLength:]) {
			return Capture{}, fmt.Errorf("report %d has nonzero payload padding", reportIndex)
		}

		capture.Payload = append(capture.Payload, report[HeaderSize:HeaderSize+payloadLength]...)
		capture.ReportCount++
		outerBytes += payloadLength
		if outerBytes > OuterBlockSize {
			return Capture{}, fmt.Errorf(
				"outer block %d carries %d bytes, maximum is %d",
				expectedOuter,
				outerBytes,
				OuterBlockSize,
			)
		}

		if !innerDone {
			expectedInner++
			continue
		}

		capture.OuterBlockCount++
		if overallDone {
			transferDone = true
			continue
		}
		if outerBytes != OuterBlockSize {
			return Capture{}, fmt.Errorf(
				"non-final outer block %d carries %d bytes, want %d",
				expectedOuter,
				outerBytes,
				OuterBlockSize,
			)
		}
		if expectedOuter == 0xff {
			return Capture{}, fmt.Errorf("outer block index would wrap after block %d", expectedOuter)
		}
		expectedOuter++
		expectedInner = 0
		outerBytes = 0
	}

	if capture.ReportCount == 0 {
		return Capture{}, fmt.Errorf("capture is empty")
	}
	if !transferDone {
		return Capture{}, fmt.Errorf("capture ends without a transfer-done report")
	}

	capture.SHA256 = sha256.Sum256(capture.Payload)
	return capture, nil
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
