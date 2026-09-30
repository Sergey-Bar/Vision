package overlay

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

// The low-level primitives. They are here rather than in overlay.go because they
// are the part a geometry test can check one pixel at a time, and a test that has
// to reason about labels and families to check a line is a test that stops being
// run.

// fillRect is a translucent wash. alphaBlend is not available on every path and is
// slower than it looks on a 640x384 frame at six frames a second, so the blend is
// done by hand over the box's own pixels.
//
// A FILL rather than an outline: an outline says "somewhere in here", a fill says
// "this is the object" while still letting the pixels underneath be read.
func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	b := img.Bounds()
	x0, y0 := clampInt(r.Min.X, b.Min.X, b.Max.X), clampInt(r.Min.Y, b.Min.Y, b.Max.Y)
	x1, y1 := clampInt(r.Max.X, b.Min.X, b.Max.X), clampInt(r.Max.Y, b.Min.Y, b.Max.Y)
	const a = 20
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			o := img.PixOffset(x, y)
			img.Pix[o+0] = uint8((int(img.Pix[o+0])*(255-a) + int(c.R)*a) / 255)
			img.Pix[o+1] = uint8((int(img.Pix[o+1])*(255-a) + int(c.G)*a) / 255)
			img.Pix[o+2] = uint8((int(img.Pix[o+2])*(255-a) + int(c.B)*a) / 255)
		}
	}
}

func drawOutline(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	drawHLine(img, r.Min.X, r.Max.X, r.Min.Y, c)
	drawHLine(img, r.Min.X, r.Max.X, r.Max.Y, c)
	drawVLine(img, r.Min.Y, r.Max.Y, r.Min.X, c)
	drawVLine(img, r.Min.Y, r.Max.Y, r.Max.X, c)
}

func drawHLine(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	if y < 0 || y >= img.Bounds().Dy() {
		return
	}
	for x := x0; x <= x1; x++ {
		if x >= 0 && x < img.Bounds().Dx() {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawVLine(img *image.RGBA, y0, y1, x int, c color.RGBA) {
	if x < 0 || x >= img.Bounds().Dx() {
		return
	}
	for y := y0; y <= y1; y++ {
		if y >= 0 && y < img.Bounds().Dy() {
			img.SetRGBA(x, y, c)
		}
	}
}

// drawLine is Bresenham. The lines are a handful of pixels wide and there is no
// dependency for a line.
func drawLine(img *image.RGBA, l Line, c color.RGBA) {
	b := img.Bounds()
	x0, y0 := scale(l.A[0], b.Dx()), scale(l.A[1], b.Dy())
	x1, y1 := scale(l.B[0], b.Dx()), scale(l.B[1], b.Dy())
	dx := absInt(x1 - x0)
	dy := -absInt(y1 - y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		img.SetRGBA(clampInt(x0, b.Min.X, b.Max.X-1), clampInt(y0, b.Min.Y, b.Max.Y-1), c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// drawNote puts a line at the bottom left, for anything the picture is choosing not
// to show. A viewer who can see that 12 boxes were left off is being told; one who
// cannot is being misled by an omission.
func drawNote(img *image.RGBA, text string) {
	labelBarAt(img, 4, img.Bounds().Dy()-4, text, noteColour)
}

// SuppressionText is the note line for what the cap left off (T8.5).
//
// Suppression is COUNTED, never silent, and it is counted PER FAMILY. One total
// would hide which family the cap actually ate, and "we drew 12 of 40" without
// saying which 28 is a number nobody can act on: an operator who sees 6 vehicles
// missing knows to raise SP_POC_MAX_BOXES_DRAWN, and one who sees 6 chairs missing
// does not.
//
// The families are printed in the taxonomy's own order, not in map order, so two
// frames of the same run produce the same line and a diff of two logs is readable.
func SuppressionText(suppressed map[classify.Family]int) string {
	total := 0
	var parts []string
	for _, f := range classify.Families {
		n := suppressed[f]
		if n <= 0 {
			continue
		}
		total += n
		parts = append(parts, fmt.Sprintf("%s %d", f, n))
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("+%d not drawn (%s)", total, strings.Join(parts, ", "))
}
