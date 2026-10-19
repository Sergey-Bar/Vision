package main

// The crop self-test's two judgements, exercised without a model.
//
// Both are pure functions of the letterbox's own scale, and both are the WHOLE of what
// `detector --selftest` claims about the crop pass. Testing them through the command
// would need a weights file and a runtime library, so every assertion about them would
// be NOT RUN on a machine without the weights -- which is most machines, and every one
// that reviews the code.

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/shared/ortdetect"
)

// TestTheCropVerdictNeedsBothHalves: a PASS needs the problem AND the cure.
func TestTheCropVerdictNeedsBothHalves(t *testing.T) {
	// The real numbers from the measured run: 8.7 px whole-frame, 83.2 px through a
	// 200x150 crop.
	v, detail := cropVerdict(8.7, 83.2, 16)
	if v.State != ortdetect.StatePass {
		t.Fatalf("the measured case reported %v, want PASS: %s", v.State, detail)
	}
	for _, want := range []string{"8.7", "83.2", "9.6"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail does not carry %q, so a reader cannot check the arithmetic: %s", want, detail)
		}
	}
	// It never claims a detection, at any input.
	if strings.Contains(strings.ToLower(detail), "detect") &&
		!strings.Contains(strings.ToLower(detail), "whether the detector") {
		t.Error("the verdict claims a detection. Finding a synthetic square is not finding a bird, and " +
			"this row must not say otherwise")
	}

	// A whole-frame pass that is already above the floor does NOT demonstrate the
	// problem. This is the failure the row exists to prevent: a frame size that needs no
	// crop being reported as proving that one does.
	v, detail = cropVerdict(24.0, 60.0, 16)
	if v.State != ortdetect.StateFail {
		t.Fatalf("a whole-frame delivery of 24 px, above the 16 px floor, reported %v. That frame size "+
			"does not need a crop and the row must not claim one: %s", v.State, detail)
	}
	if !strings.Contains(detail, "does NOT demonstrate") {
		t.Errorf("the failure does not say the frame size is not a demonstration: %s", detail)
	}

	// A crop that stays below the floor is a crop that costs a whole inference for
	// nothing.
	v, detail = cropVerdict(4.0, 12.0, 16)
	if v.State != ortdetect.StateFail {
		t.Fatalf("a crop delivery of 12 px, below the floor, reported %v: %s", v.State, detail)
	}
	if !strings.Contains(detail, "too large to help") {
		t.Errorf("the failure does not say the crop is too large: %s", detail)
	}

	// A thin margin is not the margin the cost argument rests on.
	v, detail = cropVerdict(8.0, 20.0, 16)
	if v.State != ortdetect.StateFail {
		t.Fatalf("a factor of 2.5 reported %v. The argument needs the order of magnitude, not an "+
			"improvement: %s", v.State, detail)
	}
	if !strings.Contains(detail, "not the margin") {
		t.Errorf("the failure does not say the margin is too thin: %s", detail)
	}

	// A zero whole-frame delivery must not divide by zero into a factor of zero and a
	// misleading PASS.
	v, _ = cropVerdict(0, 83.2, 16)
	if v.State == ortdetect.StatePass {
		t.Error("a whole-frame delivery of zero passed. Nothing was delivered, which is the strongest " +
			"possible statement that the measurement is broken")
	}
}

// TestTheCropCoordinateRowCatchesAnUnmappedBox: the letterbox divisor defect, in a new
// place.
//
// Dropping the crop's offset when mapping a box back puts every box at the frame's
// origin, with plausible coordinates and no error anywhere. The containment test is what
// notices, and it is tested against exactly that shape.
func TestTheCropCoordinateRowCatchesAnUnmappedBox(t *testing.T) {
	// The crop occupies a middle region of the frame, so its frame coordinates are
	// well away from the origin.
	loX, loY := 0.45, 0.44
	hiX, hiY := 0.56, 0.58
	const tol = 1e-3

	// A correctly mapped box: inside the crop.
	good := ortdetect.Box{Class: "bird", Score: 0.8, X1: 0.47, Y1: 0.46, X2: 0.52, Y2: 0.53}
	if !cropBoxInside(good, loX, loY, hiX, hiY, tol) {
		t.Fatal("a box inside the crop was rejected")
	}
	// The unmapped shape: the same box, with the crop's offset NOT applied. This is
	// what dropping `rx + b.X1*rw` produces, and it is the defect.
	unmapped := ortdetect.Box{Class: "bird", Score: 0.8, X1: 0.02, Y1: 0.02, X2: 0.07, Y2: 0.09}
	if cropBoxInside(unmapped, loX, loY, hiX, hiY, tol) {
		t.Fatal("an unmapped box passed the containment test. It sits at the frame's origin while the " +
			"crop is in the middle, which is exactly the letterbox divisor defect and nothing notices it")
	}
	// A box larger than the crop is also outside it, and also has to be caught.
	tooBig := ortdetect.Box{Class: "car", Score: 0.8, X1: 0.10, Y1: 0.10, X2: 0.90, Y2: 0.90}
	if cropBoxInside(tooBig, loX, loY, hiX, hiY, tol) {
		t.Fatal("a box larger than the crop passed")
	}
	// The tolerance is one thousandth of the frame, so a box exactly on the boundary is
	// inside. A strict comparison would fail a correct box, which is worse than failing
	// to notice a wrong one: it trains a reader to distrust the row.
	onEdge := ortdetect.Box{Class: "car", Score: 0.8, X1: hiX, Y1: loY, X2: hiX + 1e-4, Y2: hiY}
	if !cropBoxInside(onEdge, loX, loY, hiX, hiY, tol) {
		t.Fatal("a box exactly on the crop's boundary was rejected")
	}
}

// TestTheFrameIsDeterministic: the measurement has to be the same on two machines.
//
// A difference in the numbers must be a difference in the code and not in the input, so
// the synthetic frame is built from a fixed seed and asserted byte-identical. A frame
// that varied between runs would make every number above a claim about the clock.
func TestTheFrameIsDeterministic(t *testing.T) {
	a := cropFrame(40, 40, 900, 500, 1)
	b := cropFrame(40, 40, 900, 500, 1)
	if len(a) != cropFrameW*cropFrameH*3 {
		t.Fatalf("the frame is %d bytes, want %d for %dx%d RGB", len(a), cropFrameW*cropFrameH*3, cropFrameW, cropFrameH)
	}
	if string(a) != string(b) {
		t.Fatal("two calls produced different frames. Every measured number in this command depends on " +
			"the input being byte-identical between runs and between machines")
	}
	// And the object is actually brighter than the field, or the measurement is of
	// nothing at all.
	field := a[(500*cropFrameW+900)*3]
	obj := a[(500*cropFrameW+900)*3]
	_ = field
	_ = obj
	if a[(500*cropFrameW+910)*3] <= a[(10*cropFrameW+10)*3] {
		t.Fatal("the object is not brighter than the field, so the letterbox is being asked about a " +
			"uniform frame and every number it produces is about the empty case")
	}
}

// TestDeliveredPxIsTheLetterboxScale: the arithmetic under the row.
//
// The row's whole claim is "40 px arrives at N px", and N is the letterbox's own scale
// times 40. If this function ever stopped being that, the row would still print two
// confident numbers and neither would be about the object.
func TestDeliveredPxIsTheLetterboxScale(t *testing.T) {
	if got := deliveredPx(0.2167, cropObjectPx); got < 8.6 || got > 8.7 {
		t.Errorf("at the measured 1080p scale a 40 px object is delivered as %.2f px, want about 8.7",
			got)
	}
	if got := deliveredPx(2.7733, cropObjectPx); got < 110 || got > 112 {
		t.Errorf("at the measured crop scale a 40 px object is delivered as %.2f px, want about 111", got)
	}
}
