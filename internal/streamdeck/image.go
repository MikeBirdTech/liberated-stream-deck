package streamdeck

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

const keyJPEGQuality = 90

func encodeKeyJPEG(img image.Image) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("key image is nil")
	}
	bounds := img.Bounds()
	if bounds.Dx() != KeyImageWidth || bounds.Dy() != KeyImageHeight {
		return nil, fmt.Errorf(
			"key image is %dx%d, want %dx%d",
			bounds.Dx(),
			bounds.Dy(),
			KeyImageWidth,
			KeyImageHeight,
		)
	}

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: keyJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode key image as JPEG: %w", err)
	}
	return encoded.Bytes(), nil
}
