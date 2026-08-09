package streamdeck

import (
	"bytes"
	"fmt"
	"image"
	"image/color"

	"golang.org/x/image/bmp"
)

func encodeMiniKeyBMP(img image.Image) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("key image is nil")
	}
	bounds := img.Bounds()
	if bounds.Dx() != MiniKeyImageWidth || bounds.Dy() != MiniKeyImageHeight {
		return nil, fmt.Errorf(
			"key image is %dx%d, want %dx%d",
			bounds.Dx(),
			bounds.Dy(),
			MiniKeyImageWidth,
			MiniKeyImageHeight,
		)
	}

	rotated := image.NewRGBA(image.Rect(0, 0, MiniKeyImageHeight, MiniKeyImageWidth))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			rotated.SetRGBA(bounds.Dy()-1-y, x, color.RGBA{
				R: byte(r >> 8), G: byte(g >> 8), B: byte(b >> 8), A: 0xff,
			})
		}
	}

	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, rotated); err != nil {
		return nil, fmt.Errorf("encode key image as BMP: %w", err)
	}
	return encoded.Bytes(), nil
}
