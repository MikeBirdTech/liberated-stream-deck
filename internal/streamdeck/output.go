package streamdeck

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
)

const (
	imageHeaderSize    = 8
	imageChunkSize     = outputReportSize - imageHeaderSize
	keyImageHeaderSize = imageHeaderSize
	keyImageChunkSize  = imageChunkSize
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

	return d.writeImageReports("key", index, reports)
}

// SetTouchStripImage encodes and uploads one exact-size full window image.
func (d *Deck) SetTouchStripImage(img image.Image) error {
	encoded, err := encodeTouchStripJPEG(img)
	if err != nil {
		return err
	}
	reports, err := buildTouchStripImageReports(encoded)
	if err != nil {
		return err
	}
	return d.writeImageReports("touch strip", 0, reports)
}

func (d *Deck) writeImageReports(name string, target int, reports [][]byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.deviceMu.RLock()
	defer d.deviceMu.RUnlock()
	if d.closed {
		return ErrClosed
	}
	for chunkIndex, report := range reports {
		n, err := d.device.Write(report)
		if err != nil {
			return fmt.Errorf("write %s %d image chunk %d: %w", name, target, chunkIndex, err)
		}
		if n != len(report) {
			return fmt.Errorf(
				"write %s %d image chunk %d: wrote %d bytes, want %d",
				name,
				target,
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

	return buildImageReports(commandUpdateKeyImage, byte(index), "key", encodedJPEG)
}

func buildTouchStripImageReports(encodedJPEG []byte) ([][]byte, error) {
	return buildImageReports(commandUpdateTouchStrip, 0, "touch-strip", encodedJPEG)
}

func buildImageReports(command, target byte, name string, encodedJPEG []byte) ([][]byte, error) {
	if len(encodedJPEG) == 0 {
		return nil, fmt.Errorf("%s JPEG is empty", name)
	}

	chunkCount := (len(encodedJPEG) + imageChunkSize - 1) / imageChunkSize
	if chunkCount > math.MaxUint16+1 {
		return nil, fmt.Errorf("%s JPEG requires %d chunks, maximum is %d", name, chunkCount, math.MaxUint16+1)
	}

	reports := make([][]byte, 0, chunkCount)
	for chunkIndex, offset := 0, 0; offset < len(encodedJPEG); chunkIndex++ {
		end := min(offset+imageChunkSize, len(encodedJPEG))
		chunk := encodedJPEG[offset:end]

		report := make([]byte, outputReportSize)
		report[0] = outputReportID
		report[1] = command
		report[2] = target
		if end == len(encodedJPEG) {
			report[3] = 0x01
		}
		binary.LittleEndian.PutUint16(report[4:6], uint16(len(chunk)))
		binary.LittleEndian.PutUint16(report[6:8], uint16(chunkIndex))
		copy(report[imageHeaderSize:], chunk)

		reports = append(reports, report)
		offset = end
	}
	return reports, nil
}
