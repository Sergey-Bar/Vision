package main

import "math"

// estimateColor names the dominant colour of a detected vehicle by sampling
// the pixels the detector's box points at. It is deliberately simple — a grid
// mean and a hue bucket, not a classifier — because the label only has to be
// the one a human would say ("the red car") and a wrong colour on a box a
// person can see is a defect they will spot immediately.
//
// Returns "" when the box is degenerate or sits outside the frame, which the
// caller turns into a colourless label rather than a guess.
func estimateColor(px []byte, w, h int, b wireBox) string {
	// Sample an inner grid so the background around the vehicle does not vote.
	x0 := int(b.X + b.Width*0.18)
	y0 := int(b.Y + b.Height*0.18)
	x1 := int(b.X + b.Width*0.82)
	y1 := int(b.Y + b.Height*0.82)
	if x1 <= x0 || y1 <= y0 || x0 < 0 || y0 < 0 || x1 >= w || y1 >= h {
		return ""
	}

	var sr, sg, sb float64
	var n float64
	const grid = 5
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x := x0 + (x1-x0)*gx/(grid-1)
			y := y0 + (y1-y0)*gy/(grid-1)
			i := (y*w + x) * 3
			if i+2 >= len(px) {
				continue
			}
			sr += float64(px[i])
			sg += float64(px[i+1])
			sb += float64(px[i+2])
			n++
		}
	}
	if n == 0 {
		return ""
	}
	r, g, bch := sr/n/255, sg/n/255, sb/n/255
	return nameColor(r, g, bch)
}

// nameColor maps an RGB triple (0..1) to the colour word. Low-saturation
// pixels are named by brightness (white/silver/gray/black) because that is how
// a person names a white or silver car; chromatic ones by hue bucket.
func nameColor(r, g, b float64) string {
	// Named `hi` and `lo` rather than `max` and `min`: those are Go builtins
	// since 1.21 and shadowing them here bought nothing.
	hi := math.Max(r, math.Max(g, b))
	lo := math.Min(r, math.Min(g, b))
	v := hi
	s := 0.0
	if hi > 0 {
		s = (hi - lo) / hi
	}

	if s < 0.18 {
		switch {
		case v >= 0.78:
			return "white"
		case v < 0.16:
			return "black"
		case v < 0.40:
			return "gray"
		default:
			return "silver"
		}
	}

	h := hueDeg(r, g, b, hi, lo)
	// Brown is a dark orange, and it is checked first: at low brightness the
	// same hue slides under the red bucket, which is how a brown car gets
	// called red.
	if h >= 10 && h < 50 && v < 0.42 {
		return "brown"
	}
	switch {
	case h < 15 || h >= 345:
		return "red"
	case h < 42:
		return "orange"
	case h < 70:
		return "yellow"
	case h < 165:
		return "green"
	case h < 255:
		return "blue"
	case h < 300:
		return "purple"
	default:
		return "red"
	}
}

func hueDeg(r, g, b, hi, lo float64) float64 {
	d := hi - lo
	if d == 0 {
		return 0
	}
	var h float64
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h
}
