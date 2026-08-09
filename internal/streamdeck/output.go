package streamdeck

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
)

const (
	keyImageHeaderSize = 8
	keyImageChunkSize  = outputReportSize - keyImageHeaderSize
)

// SetKeyImage encodes and uploads one exact-size key image.
func (d *Deck) SetKeyImage(index int, img image.Image) error {
	encoded, err := encodeKeyJPEG(img)
	if err != nil {
		return err
	}
	reports, err := buildKeyImageReports(index, encoded)
	if err != nil {
		return err
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	for chunkIndex, report := range reports {
		n, err := d.device.Write(report)
		if err != nil {
			return fmt.Errorf("write key %d image chunk %d: %w", index, chunkIndex, err)
		}
		if n != len(report) {
			return fmt.Errorf(
				"write key %d image chunk %d: wrote %d bytes, want %d",
				index,
				chunkIndex,
				n,
				len(report),
			)
		}
	}
	return nil
}

func buildKeyImageReports(index int, encodedJPEG []byte) ([][]byte, error) {
	if index < 0 || index >= KeyCount {
		return nil, fmt.Errorf("key index %d out of range 0..%d", index, KeyCount-1)
	}
	if len(encodedJPEG) == 0 {
		return nil, fmt.Errorf("key JPEG is empty")
	}

	chunkCount := (len(encodedJPEG) + keyImageChunkSize - 1) / keyImageChunkSize
	if chunkCount > math.MaxUint16+1 {
		return nil, fmt.Errorf("key JPEG requires %d chunks, maximum is %d", chunkCount, math.MaxUint16+1)
	}

	reports := make([][]byte, 0, chunkCount)
	for chunkIndex, offset := 0, 0; offset < len(encodedJPEG); chunkIndex++ {
		end := min(offset+keyImageChunkSize, len(encodedJPEG))
		chunk := encodedJPEG[offset:end]

		report := make([]byte, outputReportSize)
		report[0] = outputReportID
		report[1] = commandUpdateKeyImage
		report[2] = byte(index)
		if end == len(encodedJPEG) {
			report[3] = 0x01
		}
		binary.LittleEndian.PutUint16(report[4:6], uint16(len(chunk)))
		binary.LittleEndian.PutUint16(report[6:8], uint16(chunkIndex))
		copy(report[keyImageHeaderSize:], chunk)

		reports = append(reports, report)
		offset = end
	}
	return reports, nil
}
