package streamdeck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"

	"golang.org/x/image/draw"
)

// The boot-frame upload channel. Unlike the documented image commands
// (0x07 key, 0x08 full-screen), uploads via command 0x09 with target 0x05 are
// persisted by the device and rendered at the next power-on. This channel was
// reverse-engineered from the official Elgato app (2026-08-10); it is not in
// the public HID documentation. Empirically verified: an 800x480 JPEG
// uploaded through UploadBootImage is what the deck shows at boot.
const (
	bootImageCommand byte = 0x09
	bootImageTarget  byte = 0x05

	// BootImageWidth / BootImageHeight are the full-LCD boot-frame size.
	BootImageWidth  = 800
	BootImageHeight = 480
)

// UploadBootImage uploads an image that the device persists as its power-on
// frame. The image is scaled to 800x480 and re-encoded as JPEG, then sent
// through the undocumented 0x09 chunked upload.
func (d *Deck) UploadBootImage(img image.Image) error {
	scaled := ScaleImage(img, BootImageWidth, BootImageHeight)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: 92}); err != nil {
		return fmt.Errorf("encode boot JPEG: %w", err)
	}
	reports, err := buildBootImageReports(encoded.Bytes())
	if err != nil {
		return err
	}
	return d.writeImageReports("boot image", int(bootImageTarget), reports)
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
