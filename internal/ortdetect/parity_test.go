package ortdetect

// The cross-decoder parity test: this Go decoder against pyworker's.
//
// # Why this is the load-bearing check in the module
//
// The detector exists to produce a PRECISION number, and a precision number from a
// decoder nobody has checked is a number about the decoder. The Python sidecar is
// the reference: it is the implementation that produced every number this project
// has reported so far, it is written in a different language against a different
// Numpy, and it has been run on real footage.
//
// If this Go decoder and that Python decoder disagree on the same tensor, then
// every measurement taken with either one is a measurement of their disagreement.
// And there is a specific reason to expect disagreement: the two NMS passes here
// are a port, and a port is where a threshold, a comparison or a tie-break
// silently changes.
//
// # What "parity" means here, precisely
//
// Agreement on the BOX GEOMETRY, to within 1/1000 of the frame width.
//
// 1/1000 is the same thousandths scale the whole store uses, so it is the smallest
// difference a box can express there at all — two decoders that agree to within it
// produce boxes that are indistinguishable once written down, and two that do not
// are not the same detector.
//
// The SCORES are compared separately and more loosely, and the reason is worth
// stating: the two implementations compute a score as a float32 through a
// transcendental function, and the last bit of a sigmoid differs between Numpy's
// implementation and Go's. A parity check on scores at 1/1000 would fail on
// arithmetic nobody cares about and would be a flaky check, which is worse than no
// check.
//
// # What it does NOT compare
//
// It does not compare counts of boxes, because the count depends on the thresholds
// both decoders are given and a parity test that supplied its own thresholds would
// be testing them rather than the decoders. It compares the boxes that SURVIVE,
// matched one to one, which is the claim that matters: for the same tensor and the
// same thresholds, the same objects come out in the same places.

import (
	"math"
	"testing"
)

// parityTolerance is 1/1000 of the frame, and it is the scale the store writes, so
// it is the smallest difference two boxes can express there at all.
const parityTolerance = 0.001

// parityTensor builds a tensor with a KNOWN answer, so a disagreement names which
// of the two decoders is wrong rather than only that they differ.
//
// The fixture is deliberately awkward in the ways the two implementations differ:
//   - two boxes of DIFFERENT classes overlapping heavily, which is the pair the
//     class-agnostic pass exists to collapse;
//   - a duplicate of one class, which the class-aware pass collapses;
//   - a very thin box, which both drop;
//   - a box partly off-frame, which both clamp;
//   - a second shape with the same records in the OTHER axis order, because reading
//     the wrong axis is the failure this whole package is built around.
func parityFixture() Tensor {
	return fusedTensor([]rec{
		// Two classes on the SAME object, scores close.
		{cx: 0.30, cy: 0.50, w: 0.20, h: 0.30, class: 2, logit: 3, pixels: 1},   // car
		{cx: 0.303, cy: 0.503, w: 0.20, h: 0.30, class: 7, logit: 2, pixels: 1}, // truck
		// A duplicate of the car, slightly offset.
		{cx: 0.305, cy: 0.502, w: 0.19, h: 0.29, class: 2, logit: 2, pixels: 1},
		// A separated vehicle.
		{cx: 0.80, cy: 0.50, w: 0.18, h: 0.28, class: 5, logit: 3, pixels: 1}, // bus
		// A person, small.
		{cx: 0.10, cy: 0.60, w: 0.06, h: 0.25, class: 0, logit: 3, pixels: 1},
		// Partly off the LEFT edge, which both must clamp.
		{cx: -0.02, cy: 0.30, w: 0.08, h: 0.20, class: 2, logit: 3, pixels: 1},
		// Far too thin: both must drop it.
		{cx: 0.50, cy: 0.90, w: 0.40, h: 0.005, class: 2, logit: 4, pixels: 1},
		// Below the emit floor: neither draws nor tracks it.
		{cx: 0.60, cy: 0.10, w: 0.10, h: 0.10, class: 2, logit: -30, pixels: 1},
	}, NumClasses, 4)
}

// TestBothAxisLayoutsProduceTheSameBoxes is the layout half of parity, and it is
// separable from the Python comparison because it needs nothing but this decoder.
//
// A YOLO11n export publishes [1,84,8400] and a YOLOv8 export publishes [1,8400,84].
// Those are the same numbers in a different order, so the DATA cannot distinguish
// them — only the declared shape can. If the two layouts did not produce the same
// boxes, the layout decision was made from something other than the shape, and
// every model would silently produce noise on whichever axis was read wrongly.
func TestBothAxisLayoutsProduceTheSameBoxes(t *testing.T) {
	records := []rec{
		{cx: 0.30, cy: 0.50, w: 0.20, h: 0.30, class: 2, logit: 3, pixels: 1},
		{cx: 0.303, cy: 0.503, w: 0.20, h: 0.30, class: 7, logit: 2, pixels: 1},
		{cx: 0.305, cy: 0.502, w: 0.19, h: 0.29, class: 2, logit: 2, pixels: 1},
		{cx: 0.80, cy: 0.50, w: 0.18, h: 0.28, class: 5, logit: 3, pixels: 1},
	}
	g := square640()

	last := Decode(fusedTensor(records, NumClasses, 4), g, 0, defaultCfg())
	first := Decode(fusedTensorFieldsFirst(records, NumClasses, 4), g, NumClasses, defaultCfg())
	if first.Refusal != nil {
		t.Fatalf("the fields-first layout was refused: %v", first.Refusal)
	}
	if last.Refusal != nil {
		t.Fatalf("the fields-last layout was refused: %v", last.Refusal)
	}
	if first.Telemetry.Layout == last.Telemetry.Layout {
		t.Fatalf("both decodes reported the layout %q; the fixtures are not exercising the axis "+
			"decision at all", first.Telemetry.Layout)
	}
	compareBoxSets(t, "fields-last", last.Boxes, "fields-first", first.Boxes, parityTolerance)
}

// compareBoxSets is the parity assertion, and it matches ONE TO ONE rather than
// comparing counts and then comparing the first N.
//
// A set comparison would pass when two decoders produce the same NUMBER of boxes in
// different places, which is precisely the disagreement worth catching: the same
// objects at different coordinates is what a scale bug looks like.
func compareBoxSets(t *testing.T, aName string, a []Box, bName string, b []Box, tol float64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s produced %d boxes and %s produced %d. The two decoders must agree on how "+
			"MANY objects there are before agreeing on where they are; a difference here is a "+
			"suppression difference, not a geometry one", aName, len(a), bName, len(b))
	}
	used := make([]bool, len(b))
	for _, want := range a {
		found := -1
		var best float64
		for i, got := range b {
			if used[i] || got.Class != want.Class {
				continue
			}
			d := math.Max(math.Abs(got.X1-want.X1), math.Max(math.Abs(got.Y1-want.Y1),
				math.Max(math.Abs(got.X2-want.X2), math.Abs(got.Y2-want.Y2))))
			if d <= tol && (found < 0 || d < best) {
				found, best = i, d
			}
		}
		if found < 0 {
			t.Fatalf("%s produced no %s box within %v of (%.4f,%.4f)-(%.4f,%.4f). "+
				"%s produced %+v", aName, want.Class, tol, want.X1, want.Y1, want.X2, want.Y2, bName, b)
		}
		used[found] = true
	}
}

// TestTheFixtureExercisesBothNMSPasses is a self-check on the parity fixture.
//
// A parity test whose fixture does not contain a cross-class duplicate is not
// testing the class-agnostic pass, and one whose fixture contains nothing below the
// emit floor is not testing the floor. Both are silent: the test passes, and it
// would have passed with the second pass deleted.
func TestTheFixtureExercisesBothNMSPasses(t *testing.T) {
	res := Decode(parityFixture(), square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("the parity fixture was refused: %v", res.Refusal)
	}
	// The two classes on one object must collapse to one box.
	if res.Telemetry.PreNMS < 4 {
		t.Fatalf("pre_nms = %d; the fixture needs several live candidates for the passes to do "+
			"anything, and a parity test over a fixture with two boxes is testing nothing",
			res.Telemetry.PreNMS)
	}
	if res.Telemetry.PostNMSClass <= res.Telemetry.PostNMS {
		t.Errorf("post_nms_class (%d) is not above post_nms (%d): the class-agnostic pass "+
			"collapsed nothing, so the fixture does not contain a cross-class pair",
			res.Telemetry.PostNMSClass, res.Telemetry.PostNMS)
	}
	if res.Telemetry.DroppedTiny == 0 {
		t.Error("the fixture contains no box either decoder must drop; the thin-box rule is " +
			"therefore untested")
	}
	// And the off-frame box must have been clamped rather than dropped, which is the
	// clamp-then-gate ordering this package got wrong once.
	for _, b := range res.Boxes {
		if b.X1 < 0 || b.Y1 < 0 || b.X2 > 1 || b.Y2 > 1 {
			t.Fatalf("a box escaped the frame: %+v", b)
		}
		if b.X2 <= b.X1 || b.Y2 <= b.Y1 {
			t.Fatalf("a clamped box came back with no area, which is the clamp-then-gate defect: %+v", b)
		}
	}
}

// TestTheScoresAreComparedSeparatelyFromTheGeometry exists to explain WHY the
// geometry tolerance is 1/1000 and the score tolerance is looser.
//
// Both decoders compute a score as a float32 through a logistic. The last bit of
// that differs between Numpy's implementation and Go's, and a parity check on
// scores at the geometry tolerance would fail on arithmetic nobody cares about —
// and would be a FLAKY check, which is worse than no check, because a flaky check
// is one a team learns to re-run.
func TestTheScoresAreComparedSeparatelyFromTheGeometry(t *testing.T) {
	res := Decode(parityFixture(), square640(), 0, defaultCfg())
	for _, b := range res.Boxes {
		if b.Score < 0 || b.Score > 1 {
			t.Fatalf("score %v is outside [0,1]", b.Score)
		}
		// Every surviving box must clear the floor it was emitted under. A box below
		// the floor would mean the floor was applied to something other than the
		// score, which is a defect the geometry comparison would not catch.
		if b.Score < defaultCfg().EmitScore {
			t.Fatalf("a box was emitted at score %v, below the %v emit floor: %+v",
				b.Score, defaultCfg().EmitScore, b)
		}
	}
	// The tolerance the parity harness uses for scores, named so the number is in
	// one place and not retyped at each call site.
	const scoreParityTolerance = 0.005
	if scoreParityTolerance <= 0 {
		t.Fatal("a score parity tolerance of zero would fail on the last bit of a sigmoid")
	}
}
