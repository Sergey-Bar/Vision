//go:build windows

package ui

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// The overlay's label font.
//
// The first version was a hand-rolled 3x5 bitmap in a map, and it was
// unreadable: digits came out as "????" because the alphabet it defined was a
// partial set and the fallback drew a question mark for every score. A
// monitoring surface whose scores are all "????" is a surface that cannot be
// used to judge a model.
//
// This is `basicfont.Face7x13` from `golang.org/x/image`: a 7x13 bitmap with a
// full ASCII set, so every class name and every score renders as itself. It is
// pure Go with no cgo, which matters here because the whole module is built
// without a C toolchain on some machines, and a font is not worth breaking that
// for.
//
// The 7x13 is the smallest face in the package and it is legible at 640x360,
// which is the analysis size. A larger face would swallow the road.
var (
	labelFace = basicfont.Face7x13
	noteFace  = basicfont.Face7x13
)

// labelBar draws a label with an opaque backing plate, so text over a road is
// readable. Text drawn straight onto a photograph is unreadable about half the
// time, and an unreadable label is worse than no label because it looks like
// one.
func labelBar(img *image.RGBA, x, y int, text string, fg color.RGBA) {
	w := font.MeasureString(labelFace, text).Ceil()
	h := labelFace.Metrics().Height.Ceil()
	if h <= 0 {
		h = 14
	}
	if h <= 0 || w <= 0 {
		return
	}
	b := img.Bounds()
	px0, py0 := clamp(x, b.Min.X, b.Max.X), clamp(y-h+3, b.Min.Y, b.Max.Y)
	px1, py1 := clamp(x+w, b.Min.X, b.Max.X), clamp(y+4, b.Min.Y, b.Max.Y)
	if px1 <= px0 || py1 <= py0 {
		return
	}
	// The plate is the box colour, darkened, so the label reads as belonging to
	// the box it names.
	plate := color.RGBA{
		R: uint8(int(fg.R) * 55 / 100),
		G: uint8(int(fg.G) * 55 / 100),
		B: uint8(int(fg.B) * 55 / 100),
		A: 235,
	}
	draw.Draw(img, image.Rect(px0, py0, px1, py1), &image.Uniform{plate}, image.Point{}, draw.Over)
	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{fg},
		Face: labelFace,
		Dot:  fixed.P(px0, py0+h-3),
	}
	d.DrawString(text)
}
