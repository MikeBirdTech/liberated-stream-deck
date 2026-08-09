package streamdeck

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

const imageJPEGQuality = 90

func encodeKeyJPEG(img image.Image) ([]byte, error) {
	return encodeExactJPEG("key", img, KeyImageWidth, KeyImageHeight)
}

func encodeTouchStripJPEG(img image.Image) ([]byte, error) {
	return encodeExactJPEG("touch-strip", img, TouchStripWidth, TouchStripHeight)
}

func encodeExactJPEG(name string, img image.Image, width, height int) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("%s image is nil", name)
	}
	bounds := img.Bounds()
	if bounds.Dx() != width || bounds.Dy() != height {
		return nil, fmt.Errorf(
			"%s image is %dx%d, want %dx%d",
			name,
			bounds.Dx(),
			bounds.Dy(),
			width,
			height,
		)
	}

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: imageJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode %s image as JPEG: %w", name, err)
	}
	return encoded.Bytes(), nil
}
