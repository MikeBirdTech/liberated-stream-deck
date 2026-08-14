package streamdeck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"

	"golang.org/x/image/draw"
)

// The standby-frame upload channel. Unlike the documented image commands
// (0x07 key, 0x08 full-screen), uploads via command 0x09 with target 0x05 are
// persisted by the device and rendered whenever the Plus is powered but not
// controlled by the app. This channel was reverse-engineered from the official
// Elgato app (2026-08-10); it is not in the public HID documentation.
const (
	bootImageCommand byte = 0x09
	bootImageTarget  byte = 0x05

	// StandbyImageWidth / StandbyImageHeight are the Plus standby-frame size.
	StandbyImageWidth  = 800
	StandbyImageHeight = 480

	// BootImageWidth and BootImageHeight are retained for compatibility.
	// Deprecated: use StandbyImageWidth and StandbyImageHeight.
	BootImageWidth  = StandbyImageWidth
	BootImageHeight = StandbyImageHeight
)

// SetStandbyImage persists the exact-size image a Stream Deck Plus displays
// while powered but disconnected from its controlling app. Requiring the
// native size avoids silently changing an image through this low-level API.
// The Deck receiver makes this API Plus-only; Mini does not implement it.
func (d *Deck) SetStandbyImage(img image.Image) error {
	if img == nil {
		return fmt.Errorf("standby image is nil")
	}
	bounds := img.Bounds()
	if bounds.Dx() != StandbyImageWidth || bounds.Dy() != StandbyImageHeight {
		return fmt.Errorf(
			"standby image must be %dx%d, got %dx%d",
			StandbyImageWidth,
			StandbyImageHeight,
			bounds.Dx(),
			bounds.Dy(),
		)
	}
	return d.uploadStandbyImage(img)
}

// UploadBootImage scales img to the standby frame's native size and persists
// it. It is retained as a compatibility wrapper for callers that relied on
// automatic scaling.
//
// Deprecated: use SetStandbyImage with an exact 800x480 image.
func (d *Deck) UploadBootImage(img image.Image) error {
	if img == nil {
		return fmt.Errorf("boot image is nil")
	}
	return d.uploadStandbyImage(ScaleImage(img, StandbyImageWidth, StandbyImageHeight))
}

func (d *Deck) uploadStandbyImage(img image.Image) error {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 92}); err != nil {
		return fmt.Errorf("encode standby JPEG: %w", err)
	}
	reports, err := buildBootImageReports(encoded.Bytes())
	if err != nil {
		return err
	}
	return d.writeImageReports("standby image", int(bootImageTarget), reports)
}

// buildBootImageReports chunks a JPEG into 1024-byte output reports for the
// boot-frame channel. Note the header differs from the documented image
// commands: chunk index precedes chunk size (indices at +4..5, sizes at
// +6..7), matching the official app's boot uploader and verified on-device.
func buildBootImageReports(encodedJPEG []byte) ([][]byte, error) {
	if len(encodedJPEG) == 0 {
		return nil, fmt.Errorf("boot JPEG is empty")
	}
	chunkCount := (len(encodedJPEG) + imageChunkSize - 1) / imageChunkSize
	reports := make([][]byte, 0, chunkCount)
	for chunkIndex, offset := 0, 0; offset < len(encodedJPEG); chunkIndex++ {
		end := min(offset+imageChunkSize, len(encodedJPEG))
		report := make([]byte, outputReportSize)
		report[0] = outputReportID
		report[1] = bootImageCommand
		report[2] = bootImageTarget
		if end == len(encodedJPEG) {
			report[3] = 0x01
		}
		binary.LittleEndian.PutUint16(report[4:6], uint16(chunkIndex))
		binary.LittleEndian.PutUint16(report[6:8], uint16(end-offset))
		copy(report[imageHeaderSize:], encodedJPEG[offset:end])
		reports = append(reports, report)
		offset = end
	}
	return reports, nil
}

// ScaleImage scales src to width x height, keeping the result opaque with
// RGB colors. Sources that already match are returned unchanged.
func ScaleImage(src image.Image, width, height int) image.Image {
	bounds := src.Bounds()
	if bounds.Dx() == width && bounds.Dy() == height {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	return dst
}
