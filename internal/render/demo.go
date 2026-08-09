package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/MikeBirdTech/liberated-stream-deck-plus/internal/streamdeck"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	demoBackground = color.NRGBA{R: 15, G: 19, B: 25, A: 255}
	keyOff         = color.NRGBA{R: 40, G: 47, B: 58, A: 255}
	keyOn          = color.NRGBA{R: 15, G: 126, B: 74, A: 255}
	muted          = color.NRGBA{R: 142, G: 153, B: 168, A: 255}
	selected       = color.NRGBA{R: 255, G: 184, B: 45, A: 255}
	divider        = color.NRGBA{R: 59, G: 68, B: 82, A: 255}
	tapColor       = color.NRGBA{R: 45, G: 205, B: 255, A: 255}
	pressColor     = color.NRGBA{R: 255, G: 96, B: 180, A: 255}
	flickColor     = color.NRGBA{R: 164, G: 112, B: 255, A: 255}
)

// KeyView is the complete state needed to draw one demo key.
type KeyView struct {
	Index    int
	On       bool
	Selected bool
}

// Key draws a generated key label, state, and optional selection border.
func Key(view KeyView) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight))
	background := keyOff
	if view.On {
		background = keyOn
	}
	fill(img, img.Bounds(), background)

	borderColor := divider
	borderWidth := 2
	if view.Selected {
		borderColor = selected
		borderWidth = 5
	}
	drawBorder(img, img.Bounds(), borderWidth, borderColor)

	drawCenteredText(img, fmt.Sprintf("KEY %d", view.Index+1), 25, 2, white)
	state := "OFF"
	if view.On {
		state = "ON"
	}
	drawCenteredText(img, state, 70, 2, white)
	return img
}

// StripView is the complete state needed to draw the diagnostic touch strip.
type StripView struct {
	Counter     int
	Brightness  int
	SelectedKey int
	Mode        string
	Touch       *streamdeck.TouchEvent
}

// Strip draws the complete 800x100 diagnostic window image.
func Strip(view StripView) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight))
	fill(img, img.Bounds(), demoBackground)

	if view.Touch != nil {
		drawTouch(img, *view.Touch)
	}

	for _, x := range []int{130, 260, 390, 520} {
		fill(img, image.Rect(x, 0, x+1, streamdeck.TouchStripHeight), divider)
	}

	drawText(img, "D1 COUNTER", 10, 7, 1, muted)
	drawText(img, fmt.Sprintf("%+04d", view.Counter), 10, 35, 2, white)
	drawText(img, "D2 BRIGHT", 140, 7, 1, muted)
	drawText(img, fmt.Sprintf("%d%%", view.Brightness), 140, 35, 2, white)
	drawText(img, "D3 SELECT", 270, 7, 1, muted)
	drawText(img, fmt.Sprintf("KEY %d", view.SelectedKey+1), 270, 35, 2, white)
	drawText(img, "D4 MODE", 400, 7, 1, muted)
	drawText(img, view.Mode, 400, 35, modeScale(view.Mode), white)
	drawText(img, touchSummary(view.Touch), 530, 7, 1, white)

	return img
}

func modeScale(mode string) int {
	if len(mode) > 7 {
		return 1
	}
	return 2
}

func touchSummary(event *streamdeck.TouchEvent) string {
	if event == nil {
		return "TOUCH: NONE"
	}
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		return fmt.Sprintf("%s %d,%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		return fmt.Sprintf("FLICK %d,%d -> %d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	default:
		return "TOUCH: UNKNOWN"
	}
}

func drawTouch(dst draw.Image, event streamdeck.TouchEvent) {
	switch event.Kind {
	case streamdeck.TouchTap:
		drawCircle(dst, event.X, event.Y, 6, tapColor)
	case streamdeck.TouchPress:
		drawCircle(dst, event.X, event.Y, 8, pressColor)
	case streamdeck.TouchFlick:
		drawLine(dst, event.StartX, event.StartY, event.EndX, event.EndY, flickColor)
		drawCircle(dst, event.StartX, event.StartY, 6, flickColor)
		drawCircle(dst, event.EndX, event.EndY, 4, white)
	}
}

func drawBorder(dst draw.Image, bounds image.Rectangle, width int, c color.Color) {
	fill(dst, image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+width), c)
	fill(dst, image.Rect(bounds.Min.X, bounds.Max.Y-width, bounds.Max.X, bounds.Max.Y), c)
	fill(dst, image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Min.X+width, bounds.Max.Y), c)
	fill(dst, image.Rect(bounds.Max.X-width, bounds.Min.Y, bounds.Max.X, bounds.Max.Y), c)
}

func drawCenteredText(dst draw.Image, text string, y, scale int, c color.Color) {
	width := font.MeasureString(basicfont.Face7x13, text).Ceil() * scale
	x := (dst.Bounds().Dx() - width) / 2
	drawText(dst, text, x, y, scale, c)
}

func drawText(dst draw.Image, text string, x, y, scale int, c color.Color) {
	face := basicfont.Face7x13
	width := font.MeasureString(face, text).Ceil()
	height := face.Metrics().Height.Ceil()
	mask := image.NewAlpha(image.Rect(0, 0, width, height))
	drawer := font.Drawer{
		Dst:  mask,
		Src:  image.White,
		Face: face,
		Dot:  fixed.P(0, face.Metrics().Ascent.Ceil()),
	}
	drawer.DrawString(text)

	for sourceY := 0; sourceY < height; sourceY++ {
		for sourceX := 0; sourceX < width; sourceX++ {
			if mask.AlphaAt(sourceX, sourceY).A == 0 {
				continue
			}
			fill(dst, image.Rect(
				x+sourceX*scale,
				y+sourceY*scale,
				x+(sourceX+1)*scale,
				y+(sourceY+1)*scale,
			), c)
		}
	}
}

func drawCircle(dst draw.Image, centerX, centerY, radius int, c color.Color) {
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			if x*x+y*y <= radius*radius {
				fill(dst, image.Rect(centerX+x, centerY+y, centerX+x+1, centerY+y+1), c)
			}
		}
	}
}

func drawLine(dst draw.Image, x0, y0, x1, y1 int, c color.Color) {
	dx := abs(x1 - x0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	dy := -abs(y1 - y0)
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		drawCircle(dst, x0, y0, 1, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		twiceError := 2 * err
		if twiceError >= dy {
			err += dy
			x0 += sx
		}
		if twiceError <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
