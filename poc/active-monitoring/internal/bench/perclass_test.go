package bench

// The per-class partition, and the arithmetic that is easy to get wrong.
//
// The one that matters: an object found as the WRONG class is a miss for the class it is
// and a false positive for the class it was called. Counting it as a true positive for
// either produces two correct-looking rates for one mistake, and a table that cannot do
// that is not measuring what a reviewer thinks it is measuring.

import (
	"testing"
)

// aGT is a ground-truth box at a known place with a known class.
func aGT(class string, x1, y1, x2, y2 float64) GT {
	return GT{Box: Box{X1: x1, Y1: y1, X2: x2, Y2: y2}, Class: class, Expressible: true}
}

// aDet is a detection at the same place with its own class and score.
func aDet(class string, x1, y1, x2, y2, score float64) Detection {
	return Detection{Box: Box{X1: x1, Y1: y1, X2: x2, Y2: y2, Class: class, Score: score}}
}

func classRow(cs []ClassCounts, class string) *ClassCounts {
	for i := range cs {
		if cs[i].Class == class {
			return &cs[i]
		}
	}
	return nil
}

// TestAClassFoundAsTheWrongClassIsNotATruePositive is the control.
//
// A bird found as a car is a miss for `bird` and a false positive for `car`. Counting it
// as a hit for both gives two true positives out of two boxes and reports a perfect run
// on a frame where the detector got the species wrong.
func TestAClassFoundAsTheWrongClassIsNotATruePositive(t *testing.T) {
	fc := Measure("f.jpg",
		[]Detection{aDet("car", 0.10, 0.10, 0.30, 0.30, 0.9)},
		[]GT{aGT("bird", 0.10, 0.10, 0.30, 0.30)},
		DefaultOptions())
	cs := fc.ByClass
	bird, car := classRow(cs, "bird"), classRow(cs, "car")
	if bird == nil || car == nil {
		t.Fatalf("both classes need a row: got %+v", cs)
	}
	if bird.TruePositives != 0 {
		t.Errorf("bird records %d true positive(s). The detection was called a car; calling it a bird "+
			"hit is the error this test exists for", bird.TruePositives)
	}
	if bird.GroundTruth != 1 {
		t.Errorf("bird's denominator is %d, want the one labelled bird", bird.GroundTruth)
	}
	if bird.FalseNegatives != 1 {
		t.Errorf("bird records %d miss(es), want 1", bird.FalseNegatives)
	}
	if bird.Misread != 1 {
		t.Errorf("bird records %d misread(s), want 1: something WAS found and called wrong, which is a "+
			"worse failure than nothing being found and a different one to repair", bird.Misread)
	}
	if r, ok := bird.Recall(); !ok || r != 0 {
		t.Errorf("bird's recall is %v (ok=%v), want 0: nothing was correctly identified as a bird", r, ok)
	}
	// The car side counts it as a false positive: a `car` was asserted where there is no
	// car, and precision over car detections is the number that falls.
	if car.FalsePositives != 1 {
		t.Errorf("car records %d false positive(s), want 1", car.FalsePositives)
	}
	if p, ok := car.Precision(); !ok || p != 0 {
		t.Errorf("car's precision is %v (ok=%v), want 0", p, ok)
	}
	// And the global total is a hit, because the object WAS found. That is correct and it
	// is exactly why the per-class table exists: the total says "found it" and the table
	// says "called it a car".
	if fc.TruePositives != 1 {
		t.Errorf("the frame total records %d true positive(s). An object matched at the threshold IS a "+
			"true positive for the FRAME; the class being wrong is what the per-class row is for",
			fc.TruePositives)
	}
}

// TestEachClassGetsItsOwnDenominator: the reason the table exists.
//
// One figure over cars and birds can be 0.98 because the cars are perfect and the birds
// are never found. Per class, the bird row says so.
func TestEachClassGetsItsOwnDenominator(t *testing.T) {
	fc := Measure("f.jpg",
		[]Detection{
			aDet("car", 0.10, 0.10, 0.30, 0.30, 0.9),
			aDet("car", 0.60, 0.10, 0.80, 0.30, 0.9),
			// One car detected with no car in the frame: a false positive.
			aDet("car", 0.10, 0.60, 0.30, 0.80, 0.9),
		},
		[]GT{
			aGT("car", 0.10, 0.10, 0.30, 0.30),
			aGT("car", 0.60, 0.10, 0.80, 0.30),
			aGT("bird", 0.10, 0.40, 0.30, 0.50),
		},
		DefaultOptions())
	cs := fc.ByClass
	car, bird := classRow(cs, "car"), classRow(cs, "bird")
	if car == nil || bird == nil {
		t.Fatalf("both classes need a row, including the one with no detections: got %+v", cs)
	}
	// Cars: two of two found, one spurious detection.
	if car.TruePositives != 2 || car.GroundTruth != 2 || car.FalsePositives != 1 || car.Detected != 3 {
		t.Errorf("car is %+v, want 2 found of 2 labelled with 1 false positive over 3 detections", car)
	}
	if p, _ := car.Precision(); p < 0.66 || p > 0.67 {
		t.Errorf("car's precision is %.3f, want about 0.667", p)
	}
	// Birds: one labelled, none found.
	if bird.TruePositives != 0 || bird.GroundTruth != 1 {
		t.Errorf("bird is %+v, want 0 found of 1 labelled", bird)
	}
	if r, _ := bird.Recall(); r != 0 {
		t.Errorf("bird's recall is %.3f, want 0", r)
	}
	// And the bird's precision is NOT RUN rather than 0.00: there were no bird
	// detections, so precision is undefined rather than zero. A printed 0.00 would be a
	// claim that every bird detection was wrong, and there were none.
	if _, ok := bird.Precision(); ok {
		t.Error("bird's precision computed over zero detections. That is undefined, not zero, and printing " +
			"a number there is a claim about nobody's work")
	}
}

// TestAnUnmatchedDetectionIsAFalsePositiveForItsOwnClassOnly.
func TestAnUnmatchedDetectionIsAFalsePositiveForItsOwnClassOnly(t *testing.T) {
	fc := Measure("f.jpg",
		[]Detection{aDet("car", 0.10, 0.10, 0.30, 0.30, 0.9)},
		[]GT{aGT("bird", 0.60, 0.60, 0.80, 0.80)},
		DefaultOptions())
	cs := fc.ByClass
	car, bird := classRow(cs, "car"), classRow(cs, "bird")
	if car == nil || car.FalsePositives != 1 || car.Detected != 1 {
		t.Errorf("car is %+v, want one false positive over one detection", car)
	}
	if bird == nil || bird.FalseNegatives != 1 || bird.Misread != 0 {
		t.Errorf("bird is %+v, want one plain miss and NO misread: nothing was matched to the bird, so "+
			"this is not a wrong label", bird)
	}
}

// TestAnUnexpressibleClassIsDroppedAndNotCountedAsAMiss: the taxonomy is not the
// detector's fault.
//
// A class this build cannot name is a vocabulary penalty. Counting it as a false
// negative would report a recall figure that is really a statement about the labels,
// and it would do so on exactly the classes a reviewer cares about -- the ones nobody
// labelled.
func TestAnUnexpressibleClassIsDroppedAndNotCountedAsAMiss(t *testing.T) {
	gt := aGT("monarch butterfly", 0.1, 0.1, 0.2, 0.2)
	gt.Expressible = false
	fc := Measure("f.jpg", nil, []GT{gt}, DefaultOptions())
	if fc.FalseNegatives != 0 {
		t.Errorf("a miss on an inexpressible class counted %d time(s). That is a vocabulary penalty and "+
			"it belongs in Dropped", fc.FalseNegatives)
	}
	if fc.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1", fc.Dropped)
	}
	for _, c := range fc.ByClass {
		if c.Class == "monarch butterfly" && c.GroundTruth != 0 {
			t.Errorf("an inexpressible class got a per-class row with denominator %d", c.GroundTruth)
		}
	}
}

// TestAnIgnoredRegionIsNotAMissEither: somebody said to skip it.
func TestAnIgnoredRegionIsNotAMissEither(t *testing.T) {
	gt := aGT("bird", 0.1, 0.1, 0.2, 0.2)
	gt.Ignored = true
	fc := Measure("f.jpg", nil, []GT{gt}, DefaultOptions())
	if fc.FalseNegatives != 0 || fc.Dropped != 1 {
		t.Fatalf("an ignored region counted as %d miss(es) and %d drop(s); it is a drop", fc.FalseNegatives, fc.Dropped)
	}
}

// TestFoldedCountsAreTheSumOfTheFrames: a run's table is its frames' tables.
func TestFoldedCountsAreTheSumOfTheFrames(t *testing.T) {
	frames := []FrameCount{
		Measure("a.jpg", []Detection{aDet("car", 0.1, 0.1, 0.3, 0.3, 0.9)},
			[]GT{aGT("car", 0.1, 0.1, 0.3, 0.3)}, DefaultOptions()),
		Measure("b.jpg", []Detection{aDet("car", 0.1, 0.1, 0.3, 0.3, 0.9)},
			[]GT{aGT("car", 0.1, 0.1, 0.3, 0.3)}, DefaultOptions()),
	}
	folded := FoldClassCounts(frames)
	car := classRow(folded, "car")
	if car == nil {
		t.Fatalf("the folded table has no car row: %+v", folded)
	}
	if car.TruePositives != 2 || car.GroundTruth != 2 {
		t.Errorf("folded car is %+v, want 2 found of 2. The fold is a SUM of the frames and not a "+
			"recomputation, so two identical frames must double", car)
	}
	rep := Accumulate(frames, 2, nil, 0)
	rc := classRow(rep.ClassCounts, "car")
	if rc == nil || rc.TruePositives != 2 {
		t.Errorf("the report's own per-class table is %+v, want 2 true positives", rep.ClassCounts)
	}
}

// TestTheRowOrderIsStable: a table that reorders itself between two runs of the same
// material cannot be compared by eye.
func TestTheRowOrderIsStable(t *testing.T) {
	fc := Measure("f.jpg",
		[]Detection{
			aDet("zebra", 0.10, 0.10, 0.30, 0.30, 0.9),
			aDet("car", 0.60, 0.60, 0.80, 0.80, 0.9),
			aDet("bird", 0.10, 0.60, 0.30, 0.80, 0.9),
		},
		[]GT{
			aGT("zebra", 0.10, 0.10, 0.30, 0.30),
			aGT("car", 0.60, 0.60, 0.80, 0.80),
			aGT("bird", 0.10, 0.60, 0.30, 0.80),
		},
		DefaultOptions())
	want := []string{"bird", "car", "zebra"}
	for i, c := range fc.ByClass {
		if i >= len(want) || c.Class != want[i] {
			t.Fatalf("row order is %+v, want %v sorted by class name", fc.ByClass, want)
		}
	}
}
