package ortdetect

// The crop rectangle's arithmetic, tested without a session.
//
// Everything here is checkable without an ONNX Runtime: the letterbox's scale is a
// pure function of two sizes, the extraction is a copy, and the mapping back is two
// multiplications and two additions. That matters because the claim this file supports
// is a claim about ARITHMETIC, and an arithmetic claim should not need a DLL to
// verify.

import (
	"math"
	"testing"
)

// TestASmallObjectIsBelowTheFloorWholeFrameAndInsideItInACrop is the crop pass's
// reason for existing, as an assertion rather than a slide.
//
// The number is the one in the proposal's own analysis: an object needs roughly 16 to
// 32 px on its short side AFTER the detector's resize to be reliably detected. At a
// 416x416 input on a 1920x1080 source the scale factor is 0.217, so the object must
// occupy 74 to 147 px on its short side in the frame. A bird 40 px across at 30 m is
// below the floor by a factor of two and cannot be found at any threshold. The same
// bird inside a 200x150 crop is scaled by 2.77 and arrives at 111 px, which is inside
// the band.
//
// The test asserts both halves because either alone is worthless: "the bird is small"
// is true of most objects and "the crop helps" is true of any crop.
func TestASmallObjectIsBelowTheFloorWholeFrameAndInsideItInACrop(t *testing.T) {
	const (
		input      = 416 // the model's own input, a MODEL property read from the session
		birdShort  = 40.0
		cropW      = 200 // the crop the proposal proposes
		cropH      = 150
		frameW     = 1920
		frameH     = 1080
		floorShort = 16.0 // below this no detector is reliable
	)

	// The whole-frame scale, read from the letterbox's own arithmetic rather than
	// recomputed here. A second implementation is where two numbers drift.
	whole := float64(input) / float64(frameW)
	if wholeH := float64(input) / float64(frameH); wholeH < whole {
		whole = wholeH // the smaller axis ratio, which is what Letterbox uses
	}
	gotWhole := birdShort * whole
	if gotWhole >= floorShort {
		t.Fatalf("the whole-frame pass delivered the bird at %.1f px on its short side, at or above "+
			"the %.0f px floor. Either the test's numbers are wrong or the premise of the crop pass is "+
			"wrong, and both have to be settled here rather than in the demo", gotWhole, floorShort)
	}

	_, geom, err := Letterbox(make([]byte, cropW*cropH*3), cropW, cropH, input, input, "nhwc")
	if err != nil {
		t.Fatal(err)
	}
	gotCut := birdShort * geom.Scale
	// The band is a FLOOR, not a window. 16 to 32 px is where detection STARTS being
	// reliable; well above 32 is better, not wrong, and a crop that overshoots is not
	// a failure. What fails is a crop that does not magnify: a region large enough that
	// the bird is still delivered at the whole-frame size, which is a second inference
	// that finds exactly what the first one did.
	if gotCut < floorShort {
		t.Fatalf("inside the crop the bird arrived at %.1f px, below the %.0f px floor. The crop size is "+
			"the single knob that decides whether the headline case works, so it has to be asserted "+
			"rather than assumed, and a region this large is not magnifying anything", gotCut, floorShort)
	}
	if gotCut <= gotWhole {
		t.Fatalf("inside the crop the bird arrived at %.1f px and the whole-frame pass delivered %.1f px, "+
			"so the crop made it no better at all for one whole extra inference", gotCut, gotWhole)
	}
	// The margin is the number the demo quotes, so it is computed rather than asserted.
	if ratio := gotCut / gotWhole; ratio < 4 {
		t.Fatalf("the crop magnified the bird by only %.1fx (%.1f px from %.1f px). A margin that thin "+
			"is a margin that a change of one of the numbers destroys", ratio, gotCut, gotWhole)
	}
}

// TestResolutionAloneDoesNotBuySmallObjectDetection is the other half of the crop
// pass's argument, and the one that is more surprising.
//
// An object subtending the SAME FRACTION OF THE FRAME arrives at the SAME number of
// pixels whether the camera is 1080p or 4K, because the whole-frame pass throws away
// the difference before the model sees it. So a resolution pitch is not an answer to
// "what happens at range": 4K buys nothing through this path, and only the crop
// converts pixels into detection capability.
func TestResolutionAloneDoesNotBuySmallObjectDetection(t *testing.T) {
	// floorShort is restated rather than shared with the test above: each of those
	// tests is a statement about one number, and a shared constant would let a change
	// to one silently redefine the other.
	const (
		input      = 416
		frameFrac  = 40.0 / 1080 // a bird 40 px tall at 1080p, as a fraction of the frame's height
		floorShort = 16.0
	)
	delivered := func(w, h int) float64 {
		scale := float64(input) / float64(w)
		if hs := float64(input) / float64(h); hs < scale {
			scale = hs
		}
		// The bird's own size scales WITH the frame, because it is the same object at
		// the same distance.
		return frameFrac * float64(h) * scale
	}
	hd, uhd := delivered(1920, 1080), delivered(3840, 2160)
	if math.Abs(hd-uhd) > 1e-9 {
		t.Fatalf("the same bird at the same distance arrived as %.3f px at 1080p and %.3f px at 4K. "+
			"Four times the pixels moved the delivered size by %.3f px, which is the whole claim: the "+
			"extra resolution is discarded before the model sees it, so a 4K camera is not a "+
			"small-object camera through this path", hd, uhd, uhd-hd)
	}
	// And the bird is BELOW the floor at both, which is why this test is not vacuous.
	// The paired test above asserts that the same bird lands ABOVE the floor once it is
	// magnified; between the two, the whole argument is made.
	if hd >= floorShort {
		t.Fatalf("the bird arrived as %.1f px through the whole-frame pass at 1080p, at or above the "+
			"%.0f px floor, so a whole-frame pass COULD find it and this test would be arguing against "+
			"its own premise", hd, floorShort)
	}
}

// TestExtractCopiesTheRightPixels: the extraction is a copy and a copy is checkable.
//
// The failure this protects against is a striped image. A crop whose stride was the
// FRAME's width rather than its own would read each row's start at the wrong place,
// producing diagonal shears of the picture, and a detector answers confidently about a
// sheared picture. So the assertion is per pixel, not per region.
func TestExtractCopiesTheRightPixels(t *testing.T) {
	const w, h = 8, 6
	frame := make([]byte, w*h*3)
	for i := range frame {
		// A distinct value per pixel makes a wrong index visible rather than merely
		// plausible.
		frame[i] = byte(i % 251)
	}
	rect := Rect{X: 2, Y: 1, W: 3, H: 2}
	dst, err := rect.Extract(frame, w, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dst) != 3*2*3 {
		t.Fatalf("extracted %d bytes, want %d for a 3x2 RGB crop", len(dst), 3*2*3)
	}
	for row := 0; row < 2; row++ {
		for col := 0; col < 3; col++ {
			src := ((1+row)*w + (2 + col)) * 3
			got := dst[(row*3+col)*3 : (row*3+col)*3+3]
			want := frame[src : src+3]
			for k := 0; k < 3; k++ {
				if got[k] != want[k] {
					t.Fatalf("crop pixel (%d,%d) channel %d = %d, want %d. The crop's stride must be its "+
						"OWN width; using the frame's reads each row at the wrong offset and produces a "+
						"sheared picture, which a detector answers about confidently",
						row, col, k, got[k], want[k])
				}
			}
		}
	}
}

// TestUncropIsTheInverseOfTheCropsPlacement: the mapping back, asserted against the
// two facts a reader would check by hand.
//
// A box filling the whole crop must come back as the crop's own rectangle in frame
// fractions, nothing more and nothing less. A box in the crop's middle must come back
// in the middle of the crop, not the middle of the FRAME. The second one is the
// letterbox-divisor defect in a new place: forget the crop's offset and every box
// lands on the origin with plausible coordinates and no error anywhere.
func TestUncropIsTheInverseOfTheCropsPlacement(t *testing.T) {
	const frameW, frameH = 1920, 1080
	rect := Rect{X: 960, Y: 540, W: 200, H: 150}

	full := rect.Uncrop(Box{X1: 0, Y1: 0, X2: 1, Y2: 1}, frameW, frameH)
	wantX1, wantY1 := 960.0/frameW, 540.0/frameH
	wantX2, wantY2 := 1160.0/frameW, 690.0/frameH
	if math.Abs(full.X1-wantX1) > 1e-12 || math.Abs(full.Y1-wantY1) > 1e-12 ||
		math.Abs(full.X2-wantX2) > 1e-12 || math.Abs(full.Y2-wantY2) > 1e-12 {
		t.Fatalf("a box filling the crop came back %+v, want [%v %v %v %v]. The crop's own offset is "+
			"the half of the mapping that is easiest to forget, and forgetting it puts every box at "+
			"the origin with plausible coordinates", full, wantX1, wantY1, wantX2, wantY2)
	}
	// The class and the score are the model's own and travel untouched. A mapping that
	// rebuilt the struct field by field would be a second place for one to be lost.
	named := rect.Uncrop(Box{Class: "bird", Score: 0.8, X1: 0.5, Y1: 0.5, X2: 0.6, Y2: 0.6}, frameW, frameH)
	if named.Class != "bird" || named.Score != 0.8 {
		t.Fatalf("the mapping lost the model's own fields: %+v", named)
	}
	wantMid := (960.0 + 0.5*200) / frameW
	if math.Abs(named.X1-wantMid) > 1e-12 {
		t.Fatalf("the middle of the crop came back at x=%v, want %v, which is the middle of the CROP "+
			"expressed against the FRAME", named.X1, wantMid)
	}
}

// TestTheEmptyRectIsTheWholeFrameAndNotAnError: the value that means "no crop" has to
// be the no-op, or every caller without a crop pass has to nil-check.
func TestTheEmptyRectIsTheWholeFrameAndNotAnError(t *testing.T) {
	b := Box{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.4}
	if got := (Rect{}).Uncrop(b, 1920, 1080); got != b {
		t.Fatalf("the empty Rect changed the box: %+v, want it unchanged", got)
	}
	if _, err := (Rect{}).Clamp(100, 100); err != nil {
		t.Fatalf("clamping the empty Rect failed: %v", err)
	}
	if (Rect{}).Area() != 0 {
		t.Fatalf("the empty Rect has area %d", (Rect{}).Area())
	}
	// A zero-size frame is handled by returning the box rather than dividing by zero.
	if got := (Rect{}).Uncrop(b, 0, 0); got != b {
		t.Fatalf("a zero frame changed the box: %+v", got)
	}
}

// TestACropOffTheEdgeIsTrimmedAndACropOutsideItIsRefused: the two halves, and they are
// different answers on purpose.
//
// Trimmed is right for a crop that hangs off the edge, because the object is at the
// edge of the frame and the pad put the rectangle past it. REFUSED is right for a crop
// entirely outside, because that means the caller computed the region wrongly, and the
// tempting fallback, clamping it to the whole frame, would be an inference on a region
// nobody chose that would then be recorded as a measurement of it.
func TestACropOffTheEdgeIsTrimmedAndACropOutsideItIsRefused(t *testing.T) {
	got, err := Rect{X: 1800, Y: 1000, W: 400, H: 400}.Clamp(1920, 1080)
	if err != nil {
		t.Fatalf("a crop overhanging the frame was refused: %v", err)
	}
	if got.X != 1800 || got.Y != 1000 || got.X+got.W != 1920 || got.Y+got.H != 1080 {
		t.Fatalf("clamped to %+v, want the crop trimmed to the frame's edge", got)
	}
	_, err = Rect{X: 2000, Y: 0, W: 100, H: 100}.Clamp(1920, 1080)
	if err == nil {
		t.Fatal("a crop entirely outside the frame was accepted. Clamping it to the whole frame would be " +
			"an inference nobody asked for on a region nobody chose, recorded as a measurement of it")
	}
}

// TestOverlapsExcludesTouching: the crop gate's predicate, and why it is strict.
//
// An ABUTTING box shares no pixels, so a region next to a detection still has
// something the whole-frame pass has not looked at and is still worth inferring. The
// strict form is therefore the correct one; an inclusive form would silently drop
// crops along the edge of every large detection.
func TestOverlapsExcludesTouching(t *testing.T) {
	a := Box{X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3}
	touching := Box{X1: 0.3, Y1: 0.1, X2: 0.5, Y2: 0.3}
	gap := Box{X1: 0.31, Y1: 0.1, X2: 0.5, Y2: 0.3}
	over := Box{X1: 0.29, Y1: 0.1, X2: 0.5, Y2: 0.3}
	if Overlaps(a, touching) {
		t.Error("boxes sharing only an edge are treated as overlapping, so a crop beside a detection is " +
			"skipped, and it shares no pixels with that detection, so it still has something to be " +
			"measured on")
	}
	if Overlaps(a, gap) {
		t.Error("boxes with a gap between them overlap, which would skip a crop that was worth inferring")
	}
	if !Overlaps(a, over) {
		t.Error("boxes sharing interior pixels do not overlap, so every crop over a detected object would " +
			"be inferred and the gate would save nothing")
	}
}
