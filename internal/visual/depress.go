package visual

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

// Depression tuning for the generic press fallback. The effect is deliberately
// neutral: it carries no meaning, it only makes the key look physically
// pressed - the artwork sinks slightly and dims, framed by a darker rim in the
// key's own dominant tone.
const (
	depressInset  = 0.06 // fraction of each edge the artwork recedes by
	depressDim    = 0.78 // brightness multiplier for the sunken artwork
	depressRimDim = 0.45 // brightness multiplier for the rim tone
)

// Depress renders the generic press feedback for a frame: the frame dimmed
// and inset inside a darker rim derived from its own average color. The
// result has the same bounds as the input and is fully opaque. It is the
// default Options.PressFallback.
func Depress(src image.Image) image.Image {
	if src == nil {
		return nil
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil
	}

	rim := dim(averageColor(src), depressRimDim)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(rim), image.Point{}, draw.Src)

	insetX := int(float64(width)*depressInset + 0.5)
	insetY := int(float64(height)*depressInset + 0.5)
	inner := image.Rect(insetX, insetY, width-insetX, height-insetY)
	if inner.Dx() <= 0 || inner.Dy() <= 0 {
		return dst
	}
	draw.ApproxBiLinear.Scale(dst, inner, src, bounds, draw.Src, nil)
	for y := inner.Min.Y; y < inner.Max.Y; y++ {
		for x := inner.Min.X; x < inner.Max.X; x++ {
			offset := dst.PixOffset(x, y)
			pix := dst.Pix[offset : offset+4 : offset+4]
			pix[0] = uint8(float64(pix[0]) * depressDim)
			pix[1] = uint8(float64(pix[1]) * depressDim)
			pix[2] = uint8(float64(pix[2]) * depressDim)
			pix[3] = 0xff
		}
	}
	return dst
}

func averageColor(src image.Image) color.RGBA {
	bounds := src.Bounds()
	var r, g, b, n uint64
	// Sample a coarse grid: plenty for a dominant tone, cheap for any size.
	stepX := max(bounds.Dx()/24, 1)
	stepY := max(bounds.Dy()/24, 1)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			cr, cg, cb, _ := src.At(x, y).RGBA()
			r += uint64(cr >> 8)
			g += uint64(cg >> 8)
			b += uint64(cb >> 8)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{A: 0xff}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 0xff}
}

func dim(c color.RGBA, factor float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c.R) * factor),
		G: uint8(float64(c.G) * factor),
		B: uint8(float64(c.B) * factor),
		A: 0xff,
	}
}
