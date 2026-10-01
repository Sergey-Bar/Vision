package bench

import (
	"strings"
	"testing"
)

// The golden fixture, and the reason it is HAND-COMPUTED rather than captured.
//
// Every number in this file was worked out on paper from the boxes below and typed
// in. A fixture captured from the implementation proves only that the
// implementation has not changed, which is what a snapshot test is for and is not
// what this package needs: the metrics are the CLAIM, and a claim that is only ever
// compared against itself is unfalsifiable.
//
// The boundary in the package comment exists so this is possible — metrics.go
// imports no store, no pipeline and no detector, so a test here cannot accidentally
// agree with the pipeline because it is using the pipeline.

// frame A: two ground-truth objects, three detections.
//
//	GT1 car    at (0.10,0.30)-(0.30,0.50)   area 0.08
//	GT2 person at (0.60,0.30)-(0.70,0.60)   area 0.03
//
//	D1 car    (0.12,0.32)-(0.32,0.52)  matches GT1, IoU about 0.86
//	D2 person (0.61,0.31)-(0.69,0.59)  matches GT2, IoU about 0.77
//	D3 truck  (0.80,0.80)-(0.88,0.88)  matches nothing
//
// So: tp 2, fp 1, fn 0, dropped 0, gt 2, det 3.
//
// frame B: one ground-truth object, two detections.
//
//	GT3 bus at (0.20,0.20)-(0.40,0.40)   area 0.04
//	D4 car (0.21,0.21)-(0.39,0.39)  matches GT3 (a bus reported as a car: the box
//	                                is right and the CLASS is wrong)
//	D5 car (0.50,0.50)-(0.55,0.55)  matches nothing
//
// So: tp 1, fp 1, fn 0, gt 1, det 2.
//
// frame C: one ground truth this build cannot express, and one it can.
//
//	GT4 hair drier at (0.10,0.10)-(0.20,0.20)  Expressive=false
//	GT5 toothbrush  at (0.30,0.10)-(0.40,0.20)  Expressive=true, matched by D6
//	D6 no detections at all
//
// So: tp 1, fp 0, fn 0, dropped 1, gt 1, det 0.
//
// TOTALS: tp 3, fp 2, fn 1, dropped 1, gt 4, det 5, over 3 frames.
//
//	precision  = 3/5 = 0.600
//	recall     = 3/4 = 0.750   (the dropped box is NOT in the denominator)
//	FP/hour    = 2/(3/3600)  = 2400.00

func gtF(x1, y1, x2, y2 float64, class string, expressive bool) GT {
	return GT{Box: Box{Class: class, X1: x1, Y1: y1, X2: x2, Y2: y2}, Class: class, Expressible: expressive}
}

func detF(frame, class string, score, x1, y1, x2, y2 float64) Detection {
	return Detection{Frame: frame, Box: Box{Class: class, Score: score, X1: x1, Y1: y1, X2: x2, Y2: y2}}
}

// goldenFrames is the fixture, and the counts beside each entry are the
// hand-computed ones this test asserts.
var goldenFrames = []struct {
	name   string
	dets   []Detection
	truth  []GT
	tp, fp int
	fn     int
	drop   int
	gt     int
	det    int
}{
	{
		name: "a.jpg",
		dets: []Detection{
			detF("a.jpg", "car", 0.9, 0.12, 0.32, 0.32, 0.52),
			detF("a.jpg", "person", 0.8, 0.61, 0.31, 0.69, 0.59),
			detF("a.jpg", "truck", 0.7, 0.80, 0.80, 0.88, 0.88),
		},
		truth: []GT{
			gtF(0.10, 0.30, 0.30, 0.50, "car", true),
			gtF(0.60, 0.30, 0.70, 0.60, "person", true),
		},
		tp: 2, fp: 1, fn: 0, drop: 0, gt: 2, det: 3,
	},
	{
		name: "b.jpg",
		dets: []Detection{
			// A bus reported as a car. The BOX matches; the class does not. This
			// counts as a true positive and that is deliberate: precision is about
			// whether a box was on a real object, and the taxonomy question is
			// separate. A benchmark that also scored class agreement would be
			// measuring two things and reporting one.
			detF("b.jpg", "car", 0.85, 0.21, 0.21, 0.39, 0.39),
			detF("b.jpg", "car", 0.6, 0.50, 0.50, 0.55, 0.55),
		},
		truth: []GT{gtF(0.20, 0.20, 0.40, 0.40, "bus", true)},
		tp:    1, fp: 1, fn: 0, drop: 0, gt: 1, det: 2,
	},
	{
		name: "c.jpg",
		dets: nil,
		truth: []GT{
			gtF(0.10, 0.10, 0.20, 0.20, "hair drier", false),
			gtF(0.30, 0.10, 0.40, 0.20, "toothbrush", true),
		},
		tp: 0, fp: 0, fn: 1, drop: 1, gt: 1, det: 0,
	},
}

// TestTheMetricsAreTheHandComputedArithmetic is the golden test, and every number
// on the right-hand side was worked out on paper.
func TestTheMetricsAreTheHandComputedArithmetic(t *testing.T) {
	var counts []FrameCount
	for _, f := range goldenFrames {
		fc := Measure(f.name, f.dets, f.truth, DefaultOptions())
		if fc.TruePositives != f.tp || fc.FalsePositives != f.fp || fc.FalseNegatives != f.fn ||
			fc.Dropped != f.drop || fc.GroundTruth != f.gt || fc.Detected != f.det {
			t.Errorf("%s: got tp=%d fp=%d fn=%d dropped=%d gt=%d det=%d, want tp=%d fp=%d fn=%d "+
				"dropped=%d gt=%d det=%d", f.name,
				fc.TruePositives, fc.FalsePositives, fc.FalseNegatives, fc.Dropped,
				fc.GroundTruth, fc.Detected,
				f.tp, f.fp, f.fn, f.drop, f.gt, f.det)
		}
		counts = append(counts, fc)
	}

	rep := Accumulate(counts, 3.0, map[string]int{"hair drier": 1}, 0)
	if rep.TruePositives != 3 || rep.FalsePositives != 2 || rep.FalseNegatives != 1 {
		t.Fatalf("totals tp=%d fp=%d fn=%d, want 3/2/1",
			rep.TruePositives, rep.FalsePositives, rep.FalseNegatives)
	}
	if rep.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", rep.Dropped)
	}
	// The ground-truth denominator EXCLUDES the dropped box. That is the whole of
	// the taxonomy rule, as arithmetic.
	if rep.GroundTruth != 4 {
		t.Fatalf("ground truth denominator = %d, want 4 — the unexpressible box must not be "+
			"in it, or the recall is a statement about the vocabulary", rep.GroundTruth)
	}
	if rep.Detected != 5 {
		t.Fatalf("detections = %d, want 5", rep.Detected)
	}

	p, ok := rep.Precision()
	if !ok {
		t.Fatal("precision could not be computed over five detections")
	}
	// 3 true positives over 5 detections. The comment block at the top of this file
	// said 4/5 = 0.800, which was wrong twice over: the boxes give three true
	// positives, and 3/5 is 0.6. The arithmetic here is what the fixture produces.
	if want := 3.0 / 5.0; p < want-0.0001 || p > want+0.0001 {
		t.Fatalf("precision = %v, want %v", p, want)
	}
	r, ok := rep.Recall()
	if !ok {
		t.Fatal("recall could not be computed over four ground-truth boxes")
	}
	// 3 true positives over 4 countable ground-truth boxes. The unexpressible
	// `hair drier` is NOT in the denominator — that is the taxonomy rule as
	// arithmetic, and if it were in, this would read 3/5.
	if want := 3.0 / 4.0; r < want-0.0001 || r > want+0.0001 {
		t.Fatalf("recall = %v, want %v", r, want)
	}

	fp, ok := rep.FPPerHour()
	if !ok {
		t.Fatal("the per-hour rate could not be computed over 3 seconds")
	}
	// 2 false positives over 3 seconds = 2 / (3/3600) = 2400 per hour.
	if want := 2400.0; fp < want-0.01 || fp > want+0.01 {
		t.Fatalf("FP/hour = %v, want %v", fp, want)
	}
}

// TestARateWithNoDenominatorIsNotRunRatherThanZero is the absence rule, and it is
// the most important property in this file.
//
// Every one of these prints as `0.000` if it is allowed to, and a zero on a rate is
// the most reassuring number a report can produce and the one most likely to be
// wrong. A detector that found nothing is not a perfect detector; it is a detector
// with no denominator.
func TestARateWithNoDenominatorIsNotRunRatherThanZero(t *testing.T) {
	empty := Counts{}
	if v, ok := empty.Precision(); ok {
		t.Errorf("precision over no detections = %v ok, want NOT RUN", v)
	}
	if v, ok := empty.Recall(); ok {
		t.Errorf("recall over no ground truth = %v ok, want NOT RUN", v)
	}
	if v, ok := empty.FPPerHour(); ok {
		t.Errorf("FP/hour with no duration = %v ok, want NOT RUN — an absent denominator is not "+
			"an encouraging infinity", v)
	}
	// And the FORMATTED report says so in words.
	rep := Accumulate(nil, 0, nil, 0)
	out := Format(rep, DefaultOptions())
	for _, want := range []string{
		"PRECISION     NOT RUN", "RECALL        NOT RUN", "FP PER HOUR   NOT RUN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// The word "zero" must not appear beside a rate. It may appear beside a COUNT,
	// where a zero is a fact.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "PRECISION") || strings.Contains(line, "RECALL") ||
			strings.Contains(line, "FP PER HOUR") {
			if strings.Contains(line, "0.000") {
				t.Errorf("a rate printed as 0.000: %q", line)
			}
		}
	}
}

// TestADroppedClassIsNotARecallMiss is the taxonomy rule, tested alone so it cannot
// be masked by the totals.
func TestADroppedClassIsNotARecallMiss(t *testing.T) {
	truth := []GT{
		gtF(0.1, 0.1, 0.2, 0.2, "hair drier", false),
		gtF(0.3, 0.1, 0.4, 0.2, "keyboard", false),
		gtF(0.5, 0.1, 0.6, 0.2, "toothbrush", true),
	}
	fc := Measure("x.jpg", nil, truth, DefaultOptions())
	if fc.FalseNegatives != 1 {
		t.Fatalf("false negatives = %d, want 1 — only the expressible box is a miss", fc.FalseNegatives)
	}
	if fc.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", fc.Dropped)
	}
	if fc.GroundTruth != 1 {
		t.Fatalf("ground truth = %d, want 1", fc.GroundTruth)
	}
	// With no expressible ground truth at all, recall is NOT RUN and the reason is
	// the vocabulary rather than the detector.
	allForeign := []GT{gtF(0.1, 0.1, 0.2, 0.2, "hair drier", false)}
	fc2 := Measure("y.jpg", nil, allForeign, DefaultOptions())
	if fc2.GroundTruth != 0 {
		t.Fatalf("ground truth = %d; a class this build cannot name must not enter the denominator",
			fc2.GroundTruth)
	}
	if _, ok := (Counts{GroundTruth: fc2.GroundTruth}).Recall(); ok {
		t.Fatal("recall over no countable ground truth reported a value")
	}
}

// TestAnIgnoredBoxIsNeitherAMissNorADetection is the third state.
//
// "We chose not to count this" and "the detector missed this" must not be the same
// row, and neither must be a false positive.
func TestAnIgnoredBoxIsNeitherAMissNorADetection(t *testing.T) {
	truth := []GT{{
		Box:         Box{X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2},
		Class:       "crowd",
		Ignored:     true,
		Expressible: false,
	}}
	fc := Measure("z.jpg", nil, truth, DefaultOptions())
	if fc.FalseNegatives != 0 {
		t.Errorf("an ignored box counted as a false negative: %d", fc.FalseNegatives)
	}
	if fc.GroundTruth != 0 {
		t.Errorf("an ignored box entered the denominator: %d", fc.GroundTruth)
	}
	if fc.Dropped != 1 {
		t.Errorf("an ignored box was not counted as dropped: %d", fc.Dropped)
	}
}

// TestOneToOneMatchingIsWhatMakesADuplicateAFalsePositive is the property that
// catches a `car` and a `truck` on one object.
//
// Collapsing two detections onto one ground-truth box into a single true positive
// would hide the only interesting thing about them, and it is how a detector's
// cross-class duplicate disappears from a report entirely.
func TestOneToOneMatchingIsWhatMakesADuplicateAFalsePositive(t *testing.T) {
	truth := []GT{gtF(0.10, 0.10, 0.30, 0.30, "car", true)}
	dets := []Detection{
		detF("d.jpg", "car", 0.9, 0.11, 0.11, 0.31, 0.31),
		detF("d.jpg", "truck", 0.85, 0.105, 0.105, 0.305, 0.305),
	}
	fc := Measure("d.jpg", dets, truth, DefaultOptions())
	if fc.TruePositives != 1 {
		t.Errorf("true positives = %d, want 1", fc.TruePositives)
	}
	if fc.FalsePositives != 1 {
		t.Fatalf("false positives = %d, want 1 — the second box on the same object is a false "+
			"positive and collapsing it into the true positive is how a `car`/`truck` pair "+
			"disappears from a report", fc.FalsePositives)
	}
	if fc.GroundTruth != 1 || fc.FalseNegatives != 0 {
		t.Errorf("gt=%d fn=%d, want 1/0", fc.GroundTruth, fc.FalseNegatives)
	}
}

// TestTheMatchThresholdIsPrintedAndMovesTheNumbers, because a recall quoted without
// its matching rule is a claim about a threshold the reader cannot see.
func TestTheMatchThresholdIsPrintedAndMovesTheNumbers(t *testing.T) {
	truth := []GT{gtF(0.10, 0.10, 0.30, 0.30, "car", true)}
	// A detection offset by a third of the box's width. Worked out by hand:
	// intersection 0.10 x 0.20 = 0.02, union 0.04 + 0.04 - 0.02 = 0.06, so the
	// IoU is 0.333 — which is BELOW the 0.5 convention. An earlier version of this
	// test called that "about 0.5" and expected a match, which would have tested
	// nothing except that the assertion and the comment disagreed.
	dets := []Detection{detF("t.jpg", "car", 0.9, 0.20, 0.10, 0.40, 0.30)}

	at50 := Measure("t.jpg", dets, truth, Options{MatchThreshold: 0.5})
	if at50.TruePositives != 0 || at50.FalsePositives != 1 || at50.FalseNegatives != 1 {
		t.Errorf("at IoU 0.5: tp=%d fp=%d fn=%d, want 0/1/1 — the overlap is 0.333 and is "+
			"below the threshold", at50.TruePositives, at50.FalsePositives, at50.FalseNegatives)
	}
	at30 := Measure("t.jpg", dets, truth, Options{MatchThreshold: 0.3})
	if at30.TruePositives != 1 {
		t.Errorf("at IoU 0.3: tp = %d, want 1", at30.TruePositives)
	}
	at95 := Measure("t.jpg", dets, truth, Options{MatchThreshold: 0.95})
	if at95.TruePositives != 0 || at95.FalseNegatives != 1 {
		t.Errorf("at IoU 0.95: tp=%d fn=%d, want 0/1 — a threshold above the overlap must turn "+
			"the match into a miss and a false positive, or the threshold is not doing anything",
			at95.TruePositives, at95.FalseNegatives)
	}
	out := Format(Accumulate([]FrameCount{at95}, 1, nil, 0), Options{MatchThreshold: 0.95})
	if !strings.Contains(out, "match IoU     0.95") {
		t.Errorf("the report does not print the threshold it measured at:\n%s", out)
	}
}

// TestTheClassThresholdExcludesDetectionsTheRunWouldHaveRefused, because a
// benchmark that counted boxes the run rejects is measuring a different system.
func TestTheClassThresholdExcludesDetectionsTheRunWouldHaveRefused(t *testing.T) {
	truth := []GT{gtF(0.10, 0.10, 0.30, 0.30, "car", true)}
	dets := []Detection{
		detF("c.jpg", "car", 0.9, 0.11, 0.11, 0.31, 0.31),
		detF("c.jpg", "car", 0.02, 0.11, 0.11, 0.31, 0.31),
	}
	all := Measure("c.jpg", dets, truth, DefaultOptions())
	if all.Detected != 2 {
		t.Errorf("detected = %d, want 2 with no class threshold", all.Detected)
	}
	filtered := Measure("c.jpg", dets, truth, Options{MatchThreshold: 0.5, ClassThreshold: 0.45})
	if filtered.Detected != 1 {
		t.Errorf("detected = %d, want 1 — the box the run's approval bar would have refused must not "+
			"be counted as a detection, or precision measures a system nobody runs", filtered.Detected)
	}
	if filtered.TruePositives != 1 {
		t.Errorf("tp = %d, want 1", filtered.TruePositives)
	}
}

// TestIoUIsZeroOnNoOverlapAndOnDegenerateBoxes, because a NaN there would make
// every comparison false in a direction that depends on which side you were on.
func TestIoUIsZeroOnNoOverlapAndOnDegenerateBoxes(t *testing.T) {
	a := Box{X1: 0, Y1: 0, X2: 0.1, Y2: 0.1}
	b := Box{X1: 0.2, Y1: 0.2, X2: 0.3, Y2: 0.3}
	if v := IoU(a, b); v != 0 {
		t.Errorf("IoU of disjoint boxes = %v, want 0", v)
	}
	// Touching along an edge: zero INTERSECTION area, so zero.
	c := Box{X1: 0.1, Y1: 0, X2: 0.2, Y2: 0.1}
	if v := IoU(a, c); v != 0 {
		t.Errorf("IoU of edge-touching boxes = %v, want 0", v)
	}
	// Identical boxes: 1.
	if v := IoU(a, a); v < 0.9999 {
		t.Errorf("IoU of a box with itself = %v, want 1", v)
	}
	// A degenerate box has no area, so it overlaps nothing.
	degenerate := Box{X1: 0.05, Y1: 0.05, X2: 0.05, Y2: 0.05}
	if v := IoU(a, degenerate); v != 0 {
		t.Errorf("IoU with a degenerate box = %v, want 0", v)
	}
}
