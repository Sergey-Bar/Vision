package overlay

// The overlay's label font.
//
// The first version was a hand-rolled 3x5 bitmap in a map, and it was unreadable:
// digits came out as "????" because the alphabet it defined was a partial set and
// the fallback drew a question mark for every score. A monitoring surface whose
// scores are all "????" is a surface that cannot be used to judge a model.
//
// This is `basicfont.Face7x13` from `golang.org/x/image`: a 7x13 bitmap with a full
// ASCII set, so every class name and every score renders as itself. It is pure Go
// with no cgo, which matters here because the whole module is built without a C
// toolchain on some machines, and a font is not worth breaking that for.
//
// The 7x13 is the smallest face in the package and it is legible at 640x384, which
// is the analysis size. A larger face would swallow the road.
//
// It lives here, and here ONLY, with NO build tag. It used to be
// `internal/ui/font_windows.go` under `//go:build windows`, so `internal/ui` did
// not compile on Linux at all: `labelBar` and `labelFace` existed on one platform
// and every caller of them was a compile error on the other. That is D7, and the
// fix is not a second copy on Unix — it is the face having no platform at all.

import (
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	labelFace = basicfont.Face7x13
	noteFace  = basicfont.Face7x13
)

// Marker geometry, in pixels. D-5: a FILLED TRIANGLE, apex at the box's top-centre
// pointing DOWN, base 11 px above it.
//
// A constant shape means "tracked object" and nothing else. An outline said "some
// box somewhere in here"; a fill says "this is the object", and the triangle points
// at the thing it names from above, so a marker's label can sit beside it without
// covering what the marker is pointing at.
const (
	markerHeight   = 11
	markerHalfBase = 5
	markerGap      = 2
	markerOutline  = 1
)

// outlineColour is the 1 px dark outline around a marker (T8.1). A family fill on
// an arbitrary background is legible for two of the four families and not for the
// other two; a dark outline is legible on all of them, and it is one pixel.
var outlineColour = color.RGBA{16, 16, 16, 255}

// drawMarker draws the tracked-object triangle for one box.
func drawMarker(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	b := img.Bounds()
	// The apex is at the top-centre of the box, just above it, pointing down into
	// the box. It is clamped to the frame so a box at the very top still gets a
	// marker rather than one drawn off the canvas where nobody can see it.
	apexX := r.Min.X + r.Dx()/2
	apexY := clampInt(r.Min.Y-markerGap, b.Min.Y+markerHeight, b.Max.Y-1)
	baseY := apexY - markerHeight
	if baseY < b.Min.Y {
		// Not enough room above: point UP from the box's top edge instead of
		// drawing a triangle that is mostly off the picture. A marker that is half
		// invisible is worse than one that points the other way.
		apexY = clampInt(r.Max.Y+markerGap, b.Min.Y+1, b.Max.Y-1)
		baseY = apexY + markerHeight
		if baseY > b.Max.Y {
			return
		}
	}
	half := markerHalfBase
	if apexX-half < b.Min.X {
		half = apexX - b.Min.X
	}
	if apexX+half >= b.Max.X {
		half = b.Max.X - 1 - apexX
	}
	if half <= 0 {
		return
	}
	// Outline first, one pixel wider on every side, then the fill on top. Painting
	// the outline afterwards would eat the fill's own edge.
	fillTriangle(img, apexX-(half+markerOutline), baseY-markerOutline,
		apexX+(half+markerOutline), baseY-markerOutline, apexX, apexY+markerOutline, outlineColour)
	fillTriangle(img, apexX-half, baseY, apexX+half, baseY, apexX, apexY, c)
}

// fillTriangle fills a triangle by scanline.
//
// Each scanline collects its intersections with the three edges and fills between
// consecutive pairs. A HORIZONTAL edge contributes BOTH of its endpoints on the
// scanline it lies on, and contributes nothing on any other: that is the detail
// that makes the base of a downward triangle a wide horizontal run instead of a
// single pixel. The earlier version returned a midpoint for a horizontal edge,
// which is not an intersection with anything, and every triangle came out as a
// spike.
func fillTriangle(img *image.RGBA, x0, y0, x1, y1, x2, y2 int, c color.RGBA) {
	b := img.Bounds()
	lo, hi := minInt3(y0, y1, y2), maxInt3(y0, y1, y2)
	var xs [6]int
	for y := clampInt(lo, b.Min.Y, b.Max.Y-1); y <= clampInt(hi, b.Min.Y, b.Max.Y-1); y++ {
		n := 0
		edge := func(ax, ay, bx, by int) {
			if ay == by {
				if y == ay {
					xs[n] = ax
					n++
					xs[n] = bx
					n++
				}
				return
			}
			if y < minInt(ay, by) || y > maxInt(ay, by) {
				return
			}
			xs[n] = ax + (bx-ax)*(y-ay)/(by-ay)
			n++
		}
		edge(x0, y0, x1, y1)
		edge(x1, y1, x2, y2)
		edge(x2, y2, x0, y0)
		if n < 2 {
			continue
		}
		sortInts(xs[:n])
		for i := 0; i+1 < n; i += 2 {
			for x := clampInt(xs[i], b.Min.X, b.Max.X-1); x <= clampInt(xs[i+1], b.Min.X, b.Max.X-1); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// sortInts is a tiny insertion sort over a slice of at most six elements. It is
// not `sort.Slice` because that allocates a closure per call and this runs per
// scanline of every marker on every frame.
func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// labelBar draws a label with an opaque backing plate beside the marker (T8.2), and
// flips it to the left of the box when it would run off the frame edge.
//
// The plate is opaque because text over a road is unreadable about half the time,
// and an unreadable label is worse than no label because it looks like one.
func labelBar(img *image.RGBA, r image.Rectangle, text string, fg color.RGBA, bounds image.Rectangle) {
	if strings.TrimSpace(text) == "" {
		return
	}
	cx, _ := r.Min.X+r.Dx()/2, r.Min.Y
	// Beside the apex: right of the marker by default, to the left if the right
	// would run off. A label clipped by the canvas edge is a label nobody reads,
	// and the picture would say the object has no class.
	w := font.MeasureString(labelFace, text).Ceil()
	x := cx + markerHalfBase + 2
	if x+w >= bounds.Max.X-2 {
		x = cx - markerHalfBase - 2 - w
	}
	if x < bounds.Min.X {
		// Neither side fits: put it at the left edge rather than dropping it. A
		// label that overlaps its own box is a much smaller problem than a box
		// with no label at all.
		x = bounds.Min.X + 2
	}
	y := r.Min.Y - markerGap
	labelBarAt(img, x, y, text, fg)
}

// labelBarAt draws a plate with its baseline at y, which is the top of the box the
// label belongs to.
func labelBarAt(img *image.RGBA, x, y int, text string, fg color.RGBA) {
	w := font.MeasureString(labelFace, text).Ceil()
	h := labelFace.Metrics().Height.Ceil()
	if h <= 0 {
		h = 14
	}
	if w <= 0 || h <= 0 {
		return
	}
	b := img.Bounds()
	px0, py0 := clampInt(x, b.Min.X, b.Max.X), clampInt(y-h+3, b.Min.Y, b.Max.Y)
	px1, py1 := clampInt(x+w, b.Min.X, b.Max.X), clampInt(y+4, b.Min.Y, b.Max.Y)
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

// drawVelocity draws the direction arrow from the track's last two centres (T8.3).
//
// The length is clamped to a quarter of the frame: an arrow is a direction, and an
// arrow that crosses half the picture is a second box competing with the first one.
// A track with no previous centre draws no arrow rather than one from the corner.
func drawVelocity(img *image.RGBA, tb TrackedBox, bounds image.Rectangle, c color.RGBA) {
	if !tb.HasPrev {
		return
	}
	cx, cy := tb.Box.Centre()
	px := tb.PrevX * float64(bounds.Dx())
	py := tb.PrevY * float64(bounds.Dy())
	dx, dy := cx*float64(bounds.Dx())-px, cy*float64(bounds.Dy())-py
	dist := len2(dx, dy)
	if dist < 4 {
		// A box that has not moved is not going anywhere, and an arrow of three
		// pixels is a smudge rather than a direction.
		return
	}
	limit := float64(minInt(bounds.Dx(), bounds.Dy())) / 4
	if limit < 1 {
		limit = 1
	}
	if dist > limit {
		scaleTo := limit / dist
		dx *= scaleTo
		dy *= scaleTo
	}
	// From the previous centre, in the direction of travel, ending short of the
	// current centre so the arrow does not cover the box it describes.
	x0 := int(px)
	y0 := int(py)
	x1 := int(px + dx*0.7)
	y1 := int(py + dy*0.7)
	drawLine(img, Line{A: [2]float64{float64(x0) / float64(bounds.Dx()), float64(y0) / float64(bounds.Dy())},
		B: [2]float64{float64(x1) / float64(bounds.Dx()), float64(y1) / float64(bounds.Dy())}}, c)
}

func len2(x, y float64) float64 { return sqrt(x*x + y*y) }

// sqrt is math.Sqrt inlined for two call sites, and named so the intent reads.
func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	// Newton's method converges in a handful of iterations for the range this is
	// called with (a screen's diagonal), and a math import for one call would be a
	// dependency to explain.
	z := v
	for i := 0; i < 12; i++ {
		z = 0.5 * (z + v/z)
	}
	return z
}

func fixed2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func itoa(n int) string { return strconv.Itoa(n) }

func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minInt3(a, b, c int) int { return minInt(minInt(a, b), c) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt3(a, b, c int) int { return maxInt(maxInt(a, b), c) }
