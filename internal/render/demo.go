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
	return KeySize(view, streamdeck.KeyImageWidth, streamdeck.KeyImageHeight)
}

// KeySize draws a generated key at a model's native key dimensions.
func KeySize(view KeyView, width, height int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	background := keyOff
	if view.On {
		background = keyOn
	}
	fill(img, img.Bounds(), background)

	borderColor := divider
	borderWidth := 2
	if view.Selected {
		borderColor = selected
		borderWidth = min(5, max(3, width/20))
	}
	drawBorder(img, img.Bounds(), borderWidth, borderColor)

	labelY := 25
	stateY := 70
	if height <= streamdeck.MiniKeyImageHeight {
		labelY = 14
		stateY = 48
	}
	drawCenteredText(img, fmt.Sprintf("KEY %d", view.Index+1), labelY, 2, white)
	state := "OFF"
	if view.On {
		state = "ON"
	}
	drawCenteredText(img, state, stateY, 2, white)
	return img
}

// StripView is the complete state needed to draw the diagnostic touch strip.
type StripView struct {
	Counter     int
	Brightness  int
	SelectedKey int
	SelectedOn  bool
	Mode        string
	LastInput   string
	Touch       *streamdeck.TouchEvent
}

// Strip draws the complete 800x100 diagnostic window image.
func Strip(view StripView) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight))
	fill(img, img.Bounds(), demoBackground)

	switch view.Mode {
	case "KEY TEST":
		drawKeyTest(img, view)
	case "TOUCH TEST":
		drawTouchTest(img, view.Touch)
	default:
		drawInputOverview(img, view)
	}

	return img
}

func drawInputOverview(dst draw.Image, view StripView) {
	fill(dst, image.Rect(385, 0, 387, streamdeck.TouchStripHeight), divider)
	fill(dst, image.Rect(0, 50, streamdeck.TouchStripWidth, 52), divider)
	drawText(dst, fmt.Sprintf("D1 %+03d", view.Counter), 15, 8, 3, white)
	drawText(dst, fmt.Sprintf("BRIGHT %d%%", view.Brightness), 415, 12, 2, white)
	drawText(dst, fmt.Sprintf("KEY %d", view.SelectedKey+1), 15, 59, 3, white)

	lastInput := view.LastInput
	if lastInput == "" {
		lastInput = "WAITING FOR INPUT"
	}
	scale := 2
	if len(lastInput) > 25 {
		scale = 1
	}
	drawText(dst, lastInput, 415, 61, scale, touchTextColor(view.Touch))
	drawText(dst, "INPUT", 750, 2, 1, muted)
}

func drawKeyTest(dst draw.Image, view StripView) {
	state := "OFF"
	stateColor := muted
	if view.SelectedOn {
		state = "ON"
		stateColor = keyOn
	}
	label := fmt.Sprintf("KEY %d", view.SelectedKey+1)
	drawText(dst, label, centeredTextX(dst.Bounds().Dx(), label, 4), 8, 4, selected)
	drawText(dst, state, centeredTextX(dst.Bounds().Dx(), state, 3), 55, 3, stateColor)
	drawText(dst, "TURN D3 TO SELECT  PRESS D3 TO TOGGLE", 10, 84, 1, muted)
	drawText(dst, "KEY TEST", 734, 2, 1, muted)
}

func drawTouchTest(dst draw.Image, event *streamdeck.TouchEvent) {
	if event == nil {
		drawText(dst, "TOUCH TEST", centeredTextX(dst.Bounds().Dx(), "TOUCH TEST", 3), 9, 3, white)
		drawText(dst, "TAP, PRESS, OR FLICK", centeredTextX(dst.Bounds().Dx(), "TAP, PRESS, OR FLICK", 2), 59, 2, muted)
		return
	}

	drawTouch(dst, *event)
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		kind := event.Kind.String()
		coordinates := fmt.Sprintf("x=%d  y=%d", event.X, event.Y)
		drawText(dst, kind, 20, 7, 4, touchTextColor(event))
		drawText(dst, coordinates, 260, 19, 3, white)
	case streamdeck.TouchFlick:
		direction := "->"
		if event.EndX < event.StartX {
			direction = "<-"
		}
		drawText(dst, "FLICK "+direction, 20, 7, 3, flickColor)
		coordinates := fmt.Sprintf("%d,%d -> %d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
		drawText(dst, coordinates, 20, 58, 2, white)
	}
	drawText(dst, "TOUCH TEST", 720, 84, 1, muted)
}

func touchTextColor(event *streamdeck.TouchEvent) color.Color {
	if event == nil {
		return white
	}
	switch event.Kind {
	case streamdeck.TouchTap:
		return tapColor
	case streamdeck.TouchPress:
		return pressColor
	case streamdeck.TouchFlick:
		return flickColor
	default:
		return white
	}
}

func centeredTextX(width int, text string, scale int) int {
	return (width - font.MeasureString(basicfont.Face7x13, text).Ceil()*scale) / 2
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
