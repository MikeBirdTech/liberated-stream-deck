package render

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
)

var (
	background  = color.NRGBA{R: 18, G: 22, B: 28, A: 255}
	topRed      = color.NRGBA{R: 220, G: 42, B: 42, A: 255}
	bottomBlue  = color.NRGBA{R: 32, G: 92, B: 220, A: 255}
	leftGreen   = color.NRGBA{R: 30, G: 190, B: 90, A: 255}
	rightYellow = color.NRGBA{R: 245, G: 205, B: 45, A: 255}
	white       = color.NRGBA{R: 250, G: 250, B: 250, A: 255}
)

// OrientationKey generates an asymmetric key image entirely in memory.
// Expected physical orientation: red top, blue bottom, green left, yellow
// right, and a white arrow pointing up.
func OrientationKey() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight))
	fill(img, img.Bounds(), background)
	fill(img, image.Rect(0, 0, 120, 20), topRed)
	fill(img, image.Rect(0, 100, 120, 120), bottomBlue)
	fill(img, image.Rect(0, 42, 20, 78), leftGreen)
	fill(img, image.Rect(100, 42, 120, 78), rightYellow)

	// Up-arrow shaft.
	fill(img, image.Rect(56, 47, 64, 89), white)
	// Up-arrow head, widest at the bottom and pointed at the top.
	for row := 0; row < 22; row++ {
		halfWidth := row / 2
		fill(img, image.Rect(60-halfWidth, 25+row, 61+halfWidth, 26+row), white)
	}

	return img
}

func fill(dst draw.Image, rect image.Rectangle, c color.Color) {
	draw.Draw(dst, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
}
