package ortdetect

// The crop rectangle, and the arithmetic that puts a box found inside one back into
// frame coordinates.
//
// # Why this file exists and why it is here
//
// A whole-frame pass squeezes 1920x1080 into 416x416, which is a 4.6x downscale. A
// car 200x100 px arrives as 43x21 and is found comfortably. A bird 40x30 px at 30 m
// arrives as 8x6, and 8 px is below the floor at which any detector is reliable, so a
// whole-frame pass cannot find it and no amount of tuning changes that. It is not a
// threshold problem. It is what one downscaled pass over a wide frame IS.
//
// The fix is to look again, closer. A 200x150 crop containing that bird letterboxes to
// 416x416, so the bird arrives as roughly 100 px instead of 8. That is the difference
// between impossible and reliable, and it is the whole of the change.
//
// # Why the geometry lives in ortdetect and nowhere else
//
// Three numbers have to be kept consistent, and the failure mode when they are not is
// silent and total:
//
//	letterbox the CROP     (the crop's own size is the frame size here)
//	decode against the CROP (so the decoder divides by the crop's CONTENT size)
//	map the result back to the FRAME
//
// Get the third wrong, scale by the crop's width as though it were the frame's or
// forget the crop's offset, and every box lands in the wrong place on the picture at
// plausible coordinates with no error anywhere. This repository has shipped that class
// of defect twice: the letterbox divisor, where the divisor was the canvas rather
// than the content, and the mask component that pinned every box to the top-left
// corner because a zero-value Box has X1 == 0.
//
// So the crop-to-frame mapping is a method on [Rect] in this package, and the
// extraction is a method on [Rect]. There is no second implementation to drift, and a
// caller that wants to crop something cannot get the arithmetic wrong by writing it
// themselves, because the only arithmetic is exported once.

// Rect is a crop rectangle in FRAME pixels.
//
// Pixels and not normalised fractions, because the crop slices a byte buffer and a
// fraction would be multiplied by the frame size by the caller, which is a rounding
// step performed by whoever is holding the rectangle on a value that has to survive
// being stored, compared and printed. X and Y are the top-left corner and W and H are
// the extent; the right and bottom edges are X+W and Y+H.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether this rectangle selects nothing, which is how "measure the
// whole frame" is spelled.
//
// It is a predicate rather than a pointer parameter because a nil *Rect would make
// every call site a nil check, and a nil check that is wrong is a panic in the frame
// loop. The zero Rect is the whole frame, and the zero Rect is always meaningful.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Clamp returns the rectangle moved and trimmed to fit a frameW by frameH frame.
//
// It refuses to produce an empty rectangle out of a non-empty one. A crop entirely
// outside the frame means the caller computed the region wrongly, and returning the
// empty Rect would turn that mistake into a silent whole-frame inference, which is the
// most expensive kind of mistake this package can make because it looks like it
// worked.
func (r Rect) Clamp(frameW, frameH int) (Rect, error) {
	if r.Empty() {
		return Rect{}, nil
	}
	x0, y0 := maxInt(0, r.X), maxInt(0, r.Y)
	x1, y1 := minInt(frameW, r.X+r.W), minInt(frameH, r.Y+r.H)
	if x1 <= x0 || y1 <= y0 {
		return Rect{}, &Refusal{
			What: sprintf("the crop rectangle %d,%d %dx%d lies entirely outside the %dx%d frame",
				r.X, r.Y, r.W, r.H, frameW, frameH),
			Detail: "no model was called. Clamping an outside crop to the whole frame would be an " +
				"inference the operator did not ask for, on a region nobody chose, and it would be " +
				"recorded as a measurement of that region",
		}
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, nil
}

// Area is the crop's area in pixels.
func (r Rect) Area() int { return r.W * r.H }

// Extract copies the rectangle's pixels out of a packed RGB frame into dst, and
// returns the buffer to use next time.
//
// It writes a CONTINUOUS buffer of r.W by r.H pixels with the same three-bytes
// layout, so [Letterbox] can be handed it with no knowledge that a crop happened: the
// stride is the crop's width, and that is exactly the frame width of the buffer being
// letterboxed. The destination is grown when it is too small and reused when it is
// not, because at 25 fps a per-frame allocation of a few hundred kilobytes is the kind
// of cost that shows up as the difference between holding a frame rate and not.
//
// The bounds are checked here and not left to the caller. A crop that ran past the end
// of a row would read the next row's pixels and produce a striped image that a
// detector answers confidently about.
func (r Rect) Extract(rgb []byte, frameW, frameH int, dst []byte) ([]byte, error) {
	if r.Empty() {
		return dst, nil
	}
	c, err := r.Clamp(frameW, frameH)
	if err != nil {
		return dst, err
	}
	need := c.W * c.H * 3
	if cap(dst) < need {
		dst = make([]byte, need)
	}
	out := dst[:need]
	for row := 0; row < c.H; row++ {
		src := ((c.Y+row)*frameW + c.X) * 3
		copy(out[row*c.W*3:(row+1)*c.W*3], rgb[src:src+c.W*3])
	}
	return out, nil
}

// Uncrop maps a box from crop-normalised coordinates back to frame-normalised ones.
//
// This is the whole of the crop-to-frame mapping and it exists as a named method for
// the reason the letterbox divisor exists: the arithmetic is two multiplications, two
// additions and two divisions, and written inline it is written again somewhere else
// two years from now with one of them missing.
//
// b is normalised against the CROP, which is what [Decode] returns when it was handed
// the crop's geometry. frameW and frameH are the FRAME's, because the result has to
// be normalised against the frame for every downstream consumer to agree that 0..1
// means the picture an operator is looking at.
//
// An empty Rect returns b unchanged. That is the whole-frame case, and it is correct
// rather than merely harmless: nothing was cropped, so there is nothing to undo.
func (r Rect) Uncrop(b Box, frameW, frameH int) Box {
	if r.Empty() || frameW <= 0 || frameH <= 0 {
		return b
	}
	fx := float64(frameW)
	fy := float64(frameH)
	rx := float64(r.X)
	ry := float64(r.Y)
	rw := float64(r.W)
	rh := float64(r.H)
	return Box{
		Class: b.Class, Score: b.Score,
		X1: (rx + b.X1*rw) / fx,
		Y1: (ry + b.Y1*rh) / fy,
		X2: (rx + b.X2*rw) / fx,
		Y2: (ry + b.Y2*rh) / fy,
	}
}

// Overlaps reports whether two frame-normalised boxes share interior pixels.
//
// Strict overlap, so two boxes that merely ABUT do not overlap, and that is the right
// answer here rather than an off-by-an-epsilon. The caller is deciding whether a crop
// is worth an inference, and a region touching a detection's edge shares NO pixels
// with it: the crop still has something the whole-frame pass has not looked at, so it
// is still worth inferring.
//
// It is a float comparison with no epsilon, and the reason there does not need one is
// that this gate saves work. A false positive costs a skipped crop on a frame where
// the object was probably already found. A false negative costs one extra inference,
// which is the 17 ms the crop pass is trying to spend well. A gate whose error was
// expensive would need the epsilon, and this comment would then say so.
func Overlaps(a, b Box) bool {
	return a.X1 < b.X2 && b.X1 < a.X2 && a.Y1 < b.Y2 && b.Y1 < a.Y2
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
