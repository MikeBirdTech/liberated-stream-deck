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

	// partialWindowHeaderSize is the report layout of command 0x0C: it is
	// 16 bytes instead of 8 because the region coordinates and size precede
	// the transfer fields (see buildPartialWindowImageReports).
	partialWindowHeaderSize = 16
	partialWindowChunkSize  = outputReportSize - partialWindowHeaderSize
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

// SetLCDImage encodes and uploads one exact-size full-screen LCD image. The
// upload is display-only: unlike the boot-frame channel (command 0x09) it
// does not survive a power cycle.
func (d *Deck) SetLCDImage(img image.Image) error {
	encoded, err := encodeLCDJPEG(img)
	if err != nil {
		return err
	}
	reports, err := buildLCDImageReports(encoded)
	if err != nil {
		return err
	}
	return d.writeImageReports("LCD", 0, reports)
}

func (d *Deck) writeImageReports(name string, target int, reports [][]byte) error {
	return d.handle.writeReports(name, target, reports)
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

func buildLCDImageReports(encodedJPEG []byte) ([][]byte, error) {
	return buildImageReports(commandUpdateLCDImage, 0, "LCD", encodedJPEG)
}

// SetPartialWindowImage encodes and uploads an image into a rectangular
// region of the touchscreen window. x and y are the logical top-left corner
// of the region in the window's 800x100 coordinate space; the image's own
// bounds define the region size, and the whole region must fit inside the
// window. Like the other documented image uploads the result is volatile.
func (d *Deck) SetPartialWindowImage(x, y int, img image.Image) error {
	encoded, err := encodePartialWindowJPEG(img)
	if err != nil {
		return err
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	reports, err := buildPartialWindowImageReports(x, y, width, height, encoded)
	if err != nil {
		return err
	}
	return d.writeImageReports("partial window", 0, reports)
}

// buildPartialWindowImageReports chunks a region JPEG into 1024-byte output
// reports for the documented partial-window command 0x0C. Unlike the other
// image commands the header is 16 bytes: coordinates and size first
// (x at +2, y at +4, width at +6, height at +8), then the done flag at +0x0A,
// chunk index at +0x0B, chunk size at +0x0D, a reserved byte at +0x0F, and
// the payload from +0x10. Coordinates are logical, without accounting for
// image rotation, matching the published command description.
func buildPartialWindowImageReports(x, y, width, height int, encodedJPEG []byte) ([][]byte, error) {
	if x < 0 || y < 0 {
		return nil, fmt.Errorf("partial-window origin (%d,%d) out of range", x, y)
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("partial-window size %dx%d must be positive", width, height)
	}
	if x+width > TouchStripWidth || y+height > TouchStripHeight {
		return nil, fmt.Errorf(
			"partial-window region (%d,%d)+%dx%d exceeds %dx%d window",
			x,
			y,
			width,
			height,
			TouchStripWidth,
			TouchStripHeight,
		)
	}
	if len(encodedJPEG) == 0 {
		return nil, fmt.Errorf("partial-window JPEG is empty")
	}

	chunkCount := (len(encodedJPEG) + partialWindowChunkSize - 1) / partialWindowChunkSize
	if chunkCount > math.MaxUint16+1 {
		return nil, fmt.Errorf("partial-window JPEG requires %d chunks, maximum is %d", chunkCount, math.MaxUint16+1)
	}

	reports := make([][]byte, 0, chunkCount)
	for chunkIndex, offset := 0, 0; offset < len(encodedJPEG); chunkIndex++ {
		end := min(offset+partialWindowChunkSize, len(encodedJPEG))

		report := make([]byte, outputReportSize)
		report[0] = outputReportID
		report[1] = commandUpdatePartialWindow
		binary.LittleEndian.PutUint16(report[2:4], uint16(x))
		binary.LittleEndian.PutUint16(report[4:6], uint16(y))
		binary.LittleEndian.PutUint16(report[6:8], uint16(width))
		binary.LittleEndian.PutUint16(report[8:10], uint16(height))
		if end == len(encodedJPEG) {
			report[0x0a] = 0x01
		}
		binary.LittleEndian.PutUint16(report[0x0b:0x0d], uint16(chunkIndex))
		binary.LittleEndian.PutUint16(report[0x0d:0x0f], uint16(end-offset))
		// report[0x0f] is reserved and stays zero.
		copy(report[partialWindowHeaderSize:], encodedJPEG[offset:end])

		reports = append(reports, report)
		offset = end
	}
	return reports, nil
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
