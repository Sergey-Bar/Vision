package novelty

import "sort"

// mask is a boolean grid of "this cell changed".
type mask struct {
	W, H  int
	cells []bool
}

// scoreMask turns a residual into a mask and reports how much of the frame
// survived.
//
// The threshold is one fixed constant rather than a fraction of the cells,
// because "the top 1% of cells changed" fires on a frame of pure noise and does
// not fire on a camera that has been covered.
func scoreMask(residual []float64, w, h int, threshold float64) (mask, int) {
	m := mask{W: w, H: h, cells: make([]bool, len(residual))}
	n := 0
	for i, v := range residual {
		if v > threshold {
			m.cells[i] = true
			n++
		}
	}
	return m, n
}

// components finds the connected regions in a mask.
//
// It is an explicit flood fill on a stack rather than a recursive one, because
// the grid is bounded by the frame and a 64x36 recursion is fine while a
// full-resolution one is not, and the code that runs on a caller's frame should
// not be the kind of code that needs a stack-size argument.
func components(m mask, minCells int) []component {
	var out []component
	seen := make([]bool, len(m.cells))
	stack := make([]int, 0, 256)
	for start := 0; start < len(m.cells); start++ {
		if !m.cells[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		c := component{}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%m.W, i/m.W
			// The FIRST cell seeds the box; the rest widen it.
			//
			// It used to be `minf(c.box.X1, x)` on a zero-value Box, and a zero Box
			// has X1 == 0 — so X1 and Y1 were pinned to the frame's top-left corner
			// for EVERY component, forever, and only X2 and Y2 moved. Every region
			// this engine proposed was a rectangle stretching from (0,0) to wherever
			// the change ended.
			//
			// Three things were wrong at once and all of them looked like something
			// else. On an operator's screen the box was the wrong shape. The tracker's
			// consecutive boxes all shared the left edge, their IoU collapsed, and
			// every frame started a NEW track — so `pocctl demo` produced 189 approved
			// boxes and zero crossings with no error. And a component's AREA, which
			// is what `SP_POC_MIN_BOX_AREA` is compared against, was the area of the
			// whole top-left corner rather than of the change.
			if c.cells == 0 {
				c.box.X1, c.box.Y1 = float64(x)/float64(m.W), float64(y)/float64(m.H)
				c.box.X2, c.box.Y2 = float64(x+1)/float64(m.W), float64(y+1)/float64(m.H)
			} else {
				c.box.X1 = minf(c.box.X1, float64(x)/float64(m.W))
				c.box.Y1 = minf(c.box.Y1, float64(y)/float64(m.H))
				c.box.X2 = maxf(c.box.X2, float64(x+1)/float64(m.W))
				c.box.Y2 = maxf(c.box.Y2, float64(y+1)/float64(m.H))
			}
			c.cells++
			for _, n := range neighbours(m.W, m.H, x, y) {
				if m.cells[n] && !seen[n] {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		if c.cells >= minCells {
			out = append(out, c)
		}
	}
	// Largest first, and ties broken by the box's left edge so two runs over the
	// same footage produce the same order and therefore the same report.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].cells != out[j].cells {
			return out[i].cells > out[j].cells
		}
		return out[i].box.X1 < out[j].box.X1
	})
	return out
}

// neighbours is the 8-connected neighbourhood of (x,y), clipped to the grid.
//
// Eight-connected rather than four because a thin diagonal object — a railing, a
// shoulder — is one object and four-connectivity splits it into a staircase of
// fragments, each too small to clear the minimum size.
func neighbours(w, h, x, y int) [8]int {
	var n [8]int
	n[0] = idx(w, h, x-1, y-1)
	n[1] = idx(w, h, x, y-1)
	n[2] = idx(w, h, x+1, y-1)
	n[3] = idx(w, h, x-1, y)
	n[4] = idx(w, h, x+1, y)
	n[5] = idx(w, h, x-1, y+1)
	n[6] = idx(w, h, x, y+1)
	n[7] = idx(w, h, x+1, y+1)
	return n
}

// idx clips an out-of-range coordinate to a valid cell.
//
// A clip rather than a sentinel: a sentinel index would be a valid cell and the
// flood fill would wander into the middle of the mask.
func idx(w, h, x, y int) int {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= w {
		x = w - 1
	}
	if y >= h {
		y = h - 1
	}
	return y*w + x
}

// component is one connected region and the cells it covers.
type component struct {
	box   Box
	cells int
}

func minf(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func maxf(a, b float64) float64 {
	if a < b {
		return b
	}
	return a
}
