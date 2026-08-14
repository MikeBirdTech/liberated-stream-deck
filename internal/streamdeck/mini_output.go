package streamdeck

import (
	"fmt"
	"image"
	"math"
)

// SetKeyImage rotates, BMP-encodes, and uploads one exact-size Mini key image.
// It uses the Mini-specific command-0x01 final-chunk marker; it does not send
// the separate 0x0B/0x63 commit used by the vendor app's bulk-image task.
func (d *Mini) SetKeyImage(index int, img image.Image) error {
	encoded, err := encodeMiniKeyBMP(img)
	if err != nil {
		return err
	}
	reports, err := buildMiniKeyImageReports(index, encoded)
	if err != nil {
		return err
	}
	return d.handle.writeReports("key", index, reports)
}

func buildMiniKeyImageReports(index int, encodedBMP []byte) ([][]byte, error) {
	if index < 0 || index >= MiniKeyCount {
		return nil, fmt.Errorf("key index %d out of range 0..%d", index, MiniKeyCount-1)
	}
	if len(encodedBMP) == 0 {
		return nil, fmt.Errorf("key BMP is empty")
	}

	chunkCount := (len(encodedBMP) + miniImageChunkSize - 1) / miniImageChunkSize
	if chunkCount > math.MaxUint8+1 {
		return nil, fmt.Errorf("key BMP requires %d chunks, maximum is %d", chunkCount, math.MaxUint8+1)
	}

	reports := make([][]byte, 0, chunkCount)
	for page, offset := 0, 0; offset < len(encodedBMP); page++ {
		end := min(offset+miniImageChunkSize, len(encodedBMP))
		report := make([]byte, miniOutputReportSize)
		report[0] = miniOutputReportID
		report[1] = miniCommandUpdateKeyImage
		report[2] = byte(page)
		if end == len(encodedBMP) {
			report[4] = 0x01
		}
		report[5] = byte(index + 1)
		copy(report[miniImageHeaderSize:], encodedBMP[offset:end])
		reports = append(reports, report)
		offset = end
	}
	return reports, nil
}
