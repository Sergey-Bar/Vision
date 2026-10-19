package main

// footage_measure_test.go - the tests behind the measurement's own controls.
//
// Every control here carries a REDCHECK tag naming the line to remove and the test that
// must go red when it is gone. A control with a tag and no run behind it is a comment,
// and this repository has three of those already.
//
// What is NOT here, and why, is as important as what is. This branch used to own the
// crop-to-frame geometry and the per-class partition; both now live upstream, in
// `internal/ortdetect` and `internal/bench`, and both arrived with their own tests. The
// controls went with the code, and duplicating them here would be the same defect the
// merge fixed. `TestTheCropGeometryIsNotReimplementedHere` is what holds that.
//
// Every test in this file is PURE or needs only ffmpeg. The one that needs weights is in
// footage_model_test.go and reports NOT RUN rather than skipping silently.

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/bench"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision/shared/ortdetect"
)

// ------------------------------------------------------------ the crop pass ---

// TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame is RC-31, and the ORDER is the
// control.
//
// A region at the left edge is padded by a quarter of its own width. Padding puts X
// negative; clamping puts it back at zero. The other order - clamp, then pad - throws
// the pad away on the frame side and then puts it BACK outside the frame, so the
// recorded region is wider than the pixels actually sliced, and every box the detector
// uncrops onto the frame is placed against a crop it never saw.
//
// REDCHECK RC-31: swap the two steps in padThenClamp and this goes red.
func TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame(t *testing.T) {
	const fw, fh = 1280, 720
	r := proposalBox{X1: 0.0, Y1: 0.4, X2: 0.1, Y2: 0.5}
	got, ok := padThenClamp(r, 0.25, fw, fh)
	if !ok {
		t.Fatal("an edge region produced no rectangle")
	}
	if got.X < 0 || got.Y < 0 || got.X+got.W > fw || got.Y+got.H > fh {
		t.Fatalf("a padded region came back at %s, outside the %dx%d frame. The pad is then measured against "+
			"pixels that do not exist and every mapped-back box is misplaced", rectString(got), fw, fh)
	}
	if got.X+got.W <= int(r.X2*float64(fw)) {
		t.Fatalf("the right-hand pad was lost: %s has no more width than the unpadded region", rectString(got))
	}
	if got.X != 0 {
		t.Fatalf("X = %d; a region already at the frame edge must stay there rather than go negative", got.X)
	}
	// And the rectangle must survive upstream's own Clamp unchanged, which is the
	// arithmetic Detect will actually apply.
	clamped, cerr := got.Clamp(fw, fh)
	if cerr != nil {
		t.Fatalf("upstream refused the rectangle this function produced: %v", cerr)
	}
	if clamped != got {
		t.Fatalf("Rect.Clamp changed the rectangle from %s to %s. Our padding and upstream's clamping are "+
			"doing the same job twice, and the one that runs second decides the answer", rectString(got),
			rectString(clamped))
	}
}

// TestClampingBeforePaddingLosesTheEdgePad is the counterfactual: it computes what the
// other order produces, so the reason the order is load-bearing is arithmetic rather than
// an assertion about intent.
func TestClampingBeforePaddingLosesTheEdgePad(t *testing.T) {
	const fw, fh = 1280, 720
	r := proposalBox{X1: 0.0, Y1: 0.4, X2: 0.1, Y2: 0.5}
	wrong := proposalBox{
		X1: r.X1 - r.Width()*0.25, Y1: r.Y1 - r.Height()*0.25,
		X2: r.X2 + r.Width()*0.25, Y2: r.Y2 + r.Height()*0.25,
	}
	right, _ := padThenClamp(r, 0.25, fw, fh)
	if wrong.X1 < 0 {
		t.Skip("this frame has no edge region, so the counterfactual cannot be built")
	}
	// The wrong order's recorded width exceeds the right one by exactly the pad that fell
	// outside the frame, and the pixels the reader can take are the narrower number.
	wrongW := int(math.Round(wrong.X2*float64(fw))) - int(math.Round(wrong.X1*float64(fw)))
	if wrongW <= right.W {
		t.Fatalf("the counterfactual is not wider (%d vs %d), so it no longer demonstrates the difference",
			wrongW, right.W)
	}
	// And a box over the whole of what was actually measured then composes back to a place
	// that is not the region anybody recorded.
	bad, _ := padThenClamp(wrong, 0, fw, fh)
	back := bad.Uncrop(ortdetect.Box{X1: 0, Y1: 0, X2: 1, Y2: 1}, fw, fh)
	if math.Abs(back.X1-wrong.X1) < 0.01 && math.Abs(back.X2-wrong.X2) < 0.01 {
		t.Fatalf("the wrong order composed back to the recorded region %+v; the two orders agreed and the "+
			"order does not matter, which is not what this file claims", wrong)
	}
}

// TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain is RC-32.
//
// Novelty on a moving object returns several components; a small one inside a large one
// is the SAME object, and measuring it twice counts one true positive twice. That is how
// a crop pass reports a recall it did not earn.
//
// REDCHECK RC-32: delete the `if coveredByAny(...)` drop in cropRegions.
func TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain(t *testing.T) {
	p := defaultCropPolicy()
	const fw, fh = 1280, 720
	cands := []novelty.Candidate{
		{Box: proposalBox{X1: 0.40, Y1: 0.40, X2: 0.60, Y2: 0.60}, Cells: 400}, // a large component
		{Box: proposalBox{X1: 0.46, Y1: 0.46, X2: 0.54, Y2: 0.54}, Cells: 60},  // one entirely inside it
	}
	plan := cropRegions(cands, p, fw, fh)
	if len(plan.Regions) != 1 {
		t.Fatalf("%d regions survived; a component already inside an accepted region adds nothing and "+
			"measuring it again counts one object twice", len(plan.Regions))
	}
	if plan.Dedupe != 1 {
		t.Fatalf("Dedupe = %d; the drop was not counted, so a reader cannot see the policy doing anything",
			plan.Dedupe)
	}
}

// TestASmallRegionInsideALargeOneIsNotCaughtByTheIoUDedupeAlone: the reason
// coveredByAny exists and uses containment rather than IoU. A tiny region inside a large
// one has a LOW IoU against it and is still entirely redundant, so an IoU-only dedupe
// would wave it through.
func TestASmallRegionInsideALargeOneIsNotCaughtByTheIoUDedupeAlone(t *testing.T) {
	const fw, fh = 1280, 720
	p := defaultCropPolicy()
	big := proposalBox{X1: 0.40, Y1: 0.40, X2: 0.60, Y2: 0.60}
	small := proposalBox{X1: 0.46, Y1: 0.46, X2: 0.54, Y2: 0.54}
	bigRect, ok := padThenClamp(big, 0, fw, fh)
	if !ok {
		t.Fatal("the large region produced no rectangle")
	}
	smallRect, ok := padThenClamp(small, 0, fw, fh)
	if !ok {
		t.Fatal("the small region produced no rectangle")
	}
	if cropDedupeIOU(smallRect, bigRect, p.DedupeIOU) {
		t.Skip("these boxes overlap more than the test assumed; rewrite it")
	}
	if !coveredByAny(smallRect, []ortdetect.Rect{bigRect}, p.CoveredFraction) {
		t.Fatal("a region entirely inside an accepted one was not reported as covered; the containment " +
			"check is what stops the same object being measured twice")
	}
}

// TestAnEmptyRegionIsReportedAsCovered: a region with no extent has nothing to add and
// must not reach a detector. `ortdetect.Rect` treats the zero Rect as the WHOLE FRAME, so
// an empty crop passed on is an inference nobody asked for, recorded as a measurement of
// that region.
func TestAnEmptyRegionIsReportedAsCovered(t *testing.T) {
	if !coveredByAny(ortdetect.Rect{}, nil, 0.95) {
		t.Fatal("a region with no extent was not refused")
	}
	if _, ok := padThenClamp(proposalBox{X1: 0.5, Y1: 0.5, X2: 0.5, Y2: 0.5}, 0.25, 1280, 720); ok {
		t.Fatal("a zero-extent region produced a usable rectangle; upstream would read that as the whole frame")
	}
}

// TestTheAreaFloorAndTheCapAreAppliedAndCounted: the policy's other two steps. Both are
// counted, because a floor that silently drops is a floor that lies.
func TestTheAreaFloorAndTheCapAreAppliedAndCounted(t *testing.T) {
	const fw, fh = 1280, 720
	p := defaultCropPolicy()
	p.Max = 2
	cands := []novelty.Candidate{
		{Box: proposalBox{X1: 0.40, Y1: 0.40, X2: 0.415, Y2: 0.415}, Cells: 2}, // below the floor
		{Box: proposalBox{X1: 0.70, Y1: 0.10, X2: 0.78, Y2: 0.18}, Cells: 40},
		{Box: proposalBox{X1: 0.10, Y1: 0.70, X2: 0.18, Y2: 0.78}, Cells: 40},
		{Box: proposalBox{X1: 0.30, Y1: 0.30, X2: 0.44, Y2: 0.44}, Cells: 120},
	}
	plan := cropRegions(cands, p, fw, fh)
	if plan.BelowMin != 1 {
		t.Fatalf("BelowMin = %d; a region below the area floor must be counted", plan.BelowMin)
	}
	if len(plan.Regions) != 2 {
		t.Fatalf("%d regions survived a cap of 2", len(plan.Regions))
	}
	// Largest first: the biggest of the three above the floor survives and the cap drops the
	// smallest. Ordering is what makes that true.
	if plan.Regions[0].Area() < plan.Regions[1].Area() {
		t.Fatalf("regions are not ordered by area, so the cap keeps the wrong ones: %+v", plan.Regions)
	}
}

// TestEveryProposedRegionIsInsideTheFrameAndNonEmpty: whatever the policy returns goes
// straight into `Detect` as a crop rectangle, so a rectangle outside the frame is a
// refusal per inference and a rectangle with no extent is the whole frame.
func TestEveryProposedRegionIsInsideTheFrameAndNonEmpty(t *testing.T) {
	const fw, fh = 1280, 720
	var cands []novelty.Candidate
	// Deliberately including regions at each edge and corner, where the pad falls outside.
	for _, b := range []proposalBox{
		{X1: 0.00, Y1: 0.00, X2: 0.10, Y2: 0.10},
		{X1: 0.90, Y1: 0.90, X2: 1.00, Y2: 1.00},
		{X1: 0.00, Y1: 0.45, X2: 0.08, Y2: 0.55},
		{X1: 0.92, Y1: 0.45, X2: 1.00, Y2: 0.55},
	} {
		cands = append(cands, novelty.Candidate{Box: b, Cells: 50})
	}
	for _, r := range cropRegions(cands, defaultCropPolicy(), fw, fh).Regions {
		if r.Empty() {
			t.Fatalf("the policy returned the zero Rect for %s, which upstream reads as the WHOLE FRAME", rectString(r))
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > fw || r.Y+r.H > fh {
			t.Fatalf("the policy returned %s, outside the %dx%d frame", rectString(r), fw, fh)
		}
		if _, cerr := r.Clamp(fw, fh); cerr != nil {
			t.Fatalf("upstream refused a rectangle the policy produced: %s: %v", rectString(r), cerr)
		}
	}
}

// TestTheRegionScaleIsTheLetterboxScale: `modelScale` is a reimplementation of one rule
// that `ortdetect.Letterbox` also computes, and two copies of a scale rule is exactly the
// defect this repository has shipped. So it is checked against the letterbox's OWN output
// rather than against a hand-computed number.
//
// Unlike the crop GEOMETRY this duplication could not be deleted: `Letterbox` takes pixels
// and returns a tensor, and the only way to ask it for a scale is to letterbox a whole
// frame, which is not a question about a region.
func TestTheRegionScaleIsTheLetterboxScale(t *testing.T) {
	for _, wh := range [][2]int{{1920, 1080}, {200, 150}, {640, 360}, {416, 416}, {1280, 720}} {
		frame := make([]byte, wh[0]*wh[1]*3)
		_, geom, err := ortdetect.Letterbox(frame, wh[0], wh[1], 416, 416, "nhwc")
		if err != nil {
			t.Fatalf("Letterbox refused a %dx%d frame: %v", wh[0], wh[1], err)
		}
		if got, want := modelScale(wh[0], wh[1], 416), geom.Scale; math.Abs(got-want) > 1e-9 {
			t.Fatalf("modelScale(%d,%d,416) = %v but Letterbox scaled by %v. Two copies of one scale rule is "+
				"how a magnification factor becomes a measurement of nothing", wh[0], wh[1], got, want)
		}
	}
}

// TestTheMagnificationFactorIsTheNineThePlanQuotes pins the arithmetic the crop pass
// exists to spend: a 40 px object through a 1280x720 frame and through a 200x150 crop
// into a 416 input, and the 9.6 the plan quotes on a 1920x1080 frame.
func TestTheMagnificationFactorIsTheNineThePlanQuotes(t *testing.T) {
	crop200x150 := ortdetect.Rect{X: 0, Y: 0, W: 200, H: 150}
	whole, crop, factor := magnification(40, crop200x150, 1280, 720, 416)
	if math.Abs(whole-13.0) > 0.15 {
		t.Fatalf("whole-frame 40px object arrived at %.1f px, expected 13.0 (40 * min(416/1280, 416/720))", whole)
	}
	if math.Abs(crop-83.2) > 0.15 {
		t.Fatalf("a 40px object in a 200x150 crop arrived at %.1f px, expected 83.2 (40 * 416/200)", crop)
	}
	if math.Abs(factor-6.4) > 0.1 {
		t.Fatalf("the factor is %.1f, expected about 6.4 on a 1280x720 frame", factor)
	}
	_, _, f1920 := magnification(40, crop200x150, 1920, 1080, 416)
	if math.Abs(f1920-9.6) > 0.1 {
		t.Fatalf("the factor on a 1920x1080 frame is %.1f; the plan quotes 9.6 and the arithmetic should agree",
			f1920)
	}
}

// TestTheCropGeometryIsNotReimplementedHere is the mechanical half of "this branch deleted
// its own copy". `internal/ortdetect` owns the rectangle, the extraction and the mapping
// back, and this package must not grow a second implementation of any of them.
//
// It counts definition sites in this package and fails on a second spelling. A duplicated
// mapping is how a benchmark reports a recall the picture does not support, and this
// repository has shipped that class of defect twice.
func TestTheCropGeometryIsNotReimplementedHere(t *testing.T) {
	banned := []string{
		"func cropPixels(", "func cropBoundsOf(", "func composeBox(",
		"func Uncrop(", "func (r Rect) Extract(", "func Extract(",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		fn := e.Name()
		if !strings.HasSuffix(fn, ".go") || strings.HasSuffix(fn, "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(filepath.Clean(fn))
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, name := range banned {
			if strings.Contains(string(body), name) {
				t.Errorf("%s defines %s. The crop geometry is ortdetect.Rect's and there is exactly one of "+
					"it; a second mapping places every crop box at plausible coordinates in the wrong place "+
					"with no error anywhere", fn, name)
			}
		}
	}
}

// TestThePerClassPartitionIsNotOptional is RC-34, and it is now a guard rather than a
// mutation: the partition this branch needed belongs to `internal/bench` upstream, which
// owns `ClassCounts` and its tests.
//
// What is guarded HERE is that this command actually goes through bench for both settings
// rather than summing anything of its own. A renderer that computed its own rates would
// agree with bench until the day it did not.
func TestThePerClassPartitionIsNotOptional(t *testing.T) {
	frames := []classFrame{{
		Frame: "f0",
		Dets: []bench.Detection{
			{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}},
		},
		Truth: []bench.GT{
			{Box: bench.Box{Class: "car", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}, Class: "car", Expressible: true},
			{Box: bench.Box{Class: "person", X1: 0.60, Y1: 0.60, X2: 0.80, Y2: 0.80}, Class: "person", Expressible: true},
		},
	}}
	rows := foldFrames(frames, bench.DefaultOptions())
	byClass := classIndex(rows)
	car, ok := byClass["car"]
	if !ok {
		t.Fatalf("no row for the car class; got %v", unionClassNames(rows, nil))
	}
	if car.TruePositives != 1 || car.FalsePositives != 0 {
		t.Fatalf("a correct `car` on a `car` box was not one true positive for car: %+v", car)
	}
	person, ok := byClass["person"]
	if !ok {
		t.Fatalf("no row for the person class; got %v", unionClassNames(rows, nil))
	}
	if person.FalseNegatives != 1 {
		t.Fatalf("the undrawn person box was not counted as a miss: %+v", person)
	}
	if p, pok := person.Precision(); pok {
		t.Fatalf("a class with no detections reported a precision of %v", p)
	}
}

// ------------------------------------------------------------ what it prints ---

// TestARateWithNoDenominatorPrintsNotRunAndNeverZero is RC-40.
//
// FAILS IF: the side-by-side renderer's `notRun` default is replaced by a formatted zero. A
// precision over no detections claims every detection was wrong and there were none; 0.000
// is the most reassuring thing a table can print and the one most likely to be wrong.
//
// The two halves are pinned separately because they are different absences. A class with
// boxes drawn and nothing found has NO PRECISION (there were no detections to be right about)
// and a real recall of 0.000. A class with detections and nothing drawn has a real precision
// and NO RECALL. Printing a zero for either would replace an absence with a measurement of
// nothing.
//
// It also pins a fact `bench` makes structurally: a class that appears on neither side has
// no row at all, because a dropped ground-truth box never enters the partition. "Both rates
// empty" is therefore not a table cell - it is the absence of the row.
//
// REDCHECK RC-40: make `classRow` print the value regardless of the ok flag.
func TestARateWithNoDenominatorPrintsNotRunAndNeverZero(t *testing.T) {
	drawnOnly := []classFrame{{
		Frame: "f0",
		Truth: []bench.GT{{
			Box: bench.Box{Class: "person", X1: 0.6, Y1: 0.6, X2: 0.8, Y2: 0.8}, Class: "person", Expressible: true,
		}},
	}}
	foundOnly := []classFrame{{
		Frame: "f0",
		Dets: []bench.Detection{{
			Frame: "f0", Box: bench.Box{Class: "kite", Score: 0.7, X1: 0.2, Y1: 0.2, X2: 0.4, Y2: 0.4},
		}},
	}}
	rows := foldFrames(append(append([]classFrame{}, drawnOnly...), foundOnly[0]), bench.DefaultOptions())
	byClass := classIndex(rows)

	person, ok := byClass["person"]
	if !ok {
		t.Fatalf("a class with a drawn box and no detections has no row: %v", unionClassNames(rows, nil))
	}
	if _, pok := person.Precision(); pok {
		t.Fatal("a class with no detections reported a precision; there was nothing to be right or wrong about")
	}
	rec, rok := person.Recall()
	if !rok || rec != 0 {
		t.Fatalf("a class with one drawn box and nothing found reported recall %v (ok=%v); that zero is a real "+
			"measurement and must print as 0.000", rec, rok)
	}
	kite, ok := byClass["kite"]
	if !ok {
		t.Fatalf("a class with detections and no drawn boxes has no row: %v", unionClassNames(rows, nil))
	}
	if _, rok := kite.Recall(); rok {
		t.Fatal("a class with no drawn boxes reported a recall; a recall of zero from no ground truth would " +
			"claim nothing was found when nothing was there to find")
	}
	// A detection nothing matched is a FALSE POSITIVE, so the precision is computable and it
	// is zero. This is the distinction that matters: no drawn box is not the same as a box
	// that was missed, and printing NOT RUN here would hide a real finding.
	if p, pok := kite.Precision(); !pok || p != 0 {
		t.Fatalf("one unmatched detection and no drawn boxes reported precision %v (ok=%v); the denominator "+
			"is 1 and the numerator is 0, so this zero is measured", p, pok)
	}

	var b strings.Builder
	writePerClassSideBySide(&b, rows, rows, bench.DefaultOptions())
	out := b.String()
	// Each class carries exactly ONE absence: a precision with no detections, or a recall with
	// no drawn boxes. Two absences on one row would mean a rate was invented where there was
	// no denominator at all.
	for _, name := range []string{"person", "kite"} {
		line := tableLineContaining(t, out, name)
		if got := strings.Count(line, notRun); got != 2 { // once per side of the comparison
			t.Fatalf("the %s row carries %d NOT RUN cells, one per side:\n%s", name, got, line)
		}
	}
	// And the one zero here is measured, so it must survive as one.
	if !strings.Contains(tableLineContaining(t, out, "person"), "0.000") {
		t.Fatalf("a measured recall of zero over one drawn box was not printed as 0.000:\n%s", out)
	}
	if !strings.Contains(tableLineContaining(t, out, "kite"), "0.000") {
		t.Fatalf("a measured precision of zero over one unmatched detection was not printed as 0.000:\n%s", out)
	}
}

// TestTheSideBySideTableRendersTheHeadlineItExistsFor: the crop pass found a class the
// whole-frame pass never named. That is the whole reason the table is side by side, and it
// has to be legible in one row - with the missing side marked absent rather than printed as a
// row of zeros, and the class appearing under BOTH settings so the reader sees the difference
// rather than inferring it from a row that exists on only one side.
func TestTheSideBySideTableRendersTheHeadlineItExistsFor(t *testing.T) {
	frames := []classFrame{{
		Frame: "f0",
		Dets: []bench.Detection{
			{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}},
		},
		Truth: []bench.GT{
			{Box: bench.Box{Class: "car", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}, Class: "car", Expressible: true},
			{Box: bench.Box{Class: "person", X1: 0.60, Y1: 0.60, X2: 0.80, Y2: 0.80}, Class: "person", Expressible: true},
		},
	}}
	cropFrames := []classFrame{{
		Frame: "f0",
		// The crop pass finds the person as well.
		Dets: []bench.Detection{
			{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.91, X1: 0.11, Y1: 0.11, X2: 0.29, Y2: 0.29}},
			{Frame: "f0", Box: bench.Box{Class: "person", Score: 0.88, X1: 0.61, Y1: 0.61, X2: 0.79, Y2: 0.79}},
		},
		Truth: frames[0].Truth,
	}}
	whole := foldFrames(frames, bench.DefaultOptions())
	crop := foldFrames(cropFrames, bench.DefaultOptions())
	var b strings.Builder
	writePerClassSideBySide(&b, whole, crop, bench.DefaultOptions())
	out := b.String()

	personLine := tableLineContaining(t, out, "person")
	if !strings.Contains(personLine, notRun) {
		t.Fatalf("the whole-frame side found no person, so its precision must read NOT RUN rather than a "+
			"number:\n%s", personLine)
	}
	if !strings.Contains(personLine, "1.000") {
		t.Fatalf("the crop side found the person and its row does not say so:\n%s", personLine)
	}
	// The disclaimer is the point of the table rather than an aside on it.
	if !strings.Contains(out, "NOT mAP") {
		t.Fatalf("the table does not disclaim mAP:\n%s", out)
	}
	if !strings.Contains(out, "one operating point") {
		t.Fatalf("the table does not state it is one operating point:\n%s", out)
	}
	// The person's whole-frame recall of 0.000 IS measured - one drawn box, nothing found -
	// so it must survive as a zero rather than be turned into NOT RUN. The absence on that
	// row is the precision, and only the precision.
	if !strings.Contains(personLine, "0.000") {
		t.Fatalf("a measured recall of zero was not printed as 0.000:\n%s", personLine)
	}
}

func tableLineContaining(t *testing.T, table, needle string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no row containing %q in:\n%s", needle, table)
	return ""
}

// hasZeroRate reports whether a printed RATE is 0.000.
//
// It is a word-bounded match and not a substring, and it looks only at the per-class
// tables. Both matter: the policy line prints `0.0005 of the frame` and a substring test
// would call a min-area threshold a measured recall, and a REGION COORDINATE also prints
// three decimals. That is the defect this command exists to catch, committed inside the
// test that catches it.
var zeroRateRE = regexp.MustCompile(`\b0\.000\b`)

func hasZeroRate(out string) bool {
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "BOTH SETTINGS") {
			inTable = true
		}
		if inTable && zeroRateRE.MatchString(line) {
			return true
		}
	}
	return false
}

// TestAMeasurementOverRefusalsRendersNotRunAndNeverZero is RC-39.
//
// FAILS IF: renderNotMeasured's refusal arm is removed, and the section goes quiet about
// the one thing it exists to say. On this tree's export that arm is the entire result, so
// a run of refusals would read like a run of zeros.
//
// REDCHECK RC-39: change `case r.Whole.Refusals+r.Crop.Refusals > 0:` to `case false:`.
func TestAMeasurementOverRefusalsRendersNotRunAndNeverZero(t *testing.T) {
	reason := &ortdetect.Refusal{
		What:   "this export is a multi-scale ANCHORED head and the in-process decoder reads only fused outputs",
		Detail: "the session declares 3 outputs, and an anchored darknet head publishes one tensor per stride",
	}
	r := footageReport{
		Clip:   "somebody/someclip.mp4",
		Window: footageWindow{At: 0, For: 2, Every: 0.5},
		Read:   windowRead{Declared: 4, Read: 4, W: 320, H: 180, Command: "ffmpeg (test)"},
		Policy: defaultCropPolicy(),
		Opts:   bench.DefaultOptions(),
		// A run in which every inference was refused has NO detections and no frames
		// carrying any: the decoder refuses, the answer carries no boxes, and nothing is
		// recorded but the refusal.
		Whole: regime{Name: "whole-frame", Inferences: 4, Refusals: 4,
			RefusalWhy: []string{reason.What}, FirstRefusal: reason,
			Frames: []classFrame{{Frame: "f0"}, {Frame: "f1"}, {Frame: "f2"}, {Frame: "f3"}}},
		Crop: regime{Name: "crop", Inferences: 3, Refusals: 3,
			RefusalWhy: []string{reason.What}, FirstRefusal: reason, Regions: 3, SilentFrames: 1,
			Frames: []classFrame{{Frame: "f0"}, {Frame: "f1"}, {Frame: "f2"}, {Frame: "f3"}}},
		TruthState: "NOT RUN - no --truth was given",
	}
	out := renderFootageReport(r)
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("a measurement the decoder refused everywhere does not say NOT RUN:\n%s", out)
	}
	if hasZeroRate(out) {
		t.Fatalf("a measurement over refusals printed a rate of zero somewhere:\n%s", out)
	}
	if !strings.Contains(out, reason.What) {
		t.Fatalf("the decoder's own reason is not printed verbatim:\n%s", out)
	}
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("the report does not say that whether the crop pass finds anything is unknown:\n%s", out)
	}
}

// TestAnAbsentDetectorIsAlsoNotRunAndNotZero: the other way the detection columns go
// absent, which is what happens on a machine with no weights.
func TestAnAbsentDetectorIsAlsoNotRunAndNotZero(t *testing.T) {
	r := footageReport{
		Clip:   "somebody/someclip.mp4",
		Window: footageWindow{At: 0, For: 2, Every: 0.5},
		Read:   windowRead{Declared: 4, Read: 4, W: 320, H: 180, Command: "ffmpeg (test)"},
		Policy: defaultCropPolicy(),
		Opts:   bench.DefaultOptions(),
		Whole:  regime{Name: "whole-frame", Inferences: 4, Frames: []classFrame{{Frame: "f0"}}},
		Crop:   regime{Name: "crop", Inferences: 3, Regions: 3, Frames: []classFrame{{Frame: "f0"}}},
		// One region, so the arithmetic section is not the "nothing to magnify" case.
		Regions:    []regionSample{{At: 0.5, Rect: ortdetect.Rect{X: 20, Y: 30, W: 64, H: 90}, Frame: 1}},
		TruthState: "NOT RUN - no --truth was given",
		DetErr:     fmt.Errorf("footage: the model at models/yolov4n.onnx is not readable"),
	}
	out := renderFootageReport(r)
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("an absent model does not produce NOT RUN:\n%s", out)
	}
	if hasZeroRate(out) {
		t.Fatalf("an absent model produced a zero rate:\n%s", out)
	}
	// The arithmetic IS measurable without a model, and this is the path that proves it.
	if !strings.Contains(out, "with no model involved") {
		t.Fatalf("the arithmetic section is missing:\n%s", out)
	}
	if !strings.Contains(out, "ASSUMED") {
		t.Fatalf("with no session the model input is assumed, and that must be labelled as an assumption:\n%s", out)
	}
	if !strings.Contains(out, "40 px") {
		t.Fatalf("the arithmetic section does not state what object size it is reporting:\n%s", out)
	}
	if !strings.Contains(out, "yolov4n.onnx") {
		t.Fatalf("the report does not name the file that is missing:\n%s", out)
	}
}

// ---------------------------------------------------------- the frame reader ---

// TestTrailingBytesAreRefusedRatherThanDropped is RC-41.
//
// FAILS IF: refuseTrailingFrame returns nil for a positive count. A partial frame is not a
// frame: dropping it quietly is a silent sample drop, and reading past it is a measurement
// of bytes nobody decoded.
//
// REDCHECK RC-41: make refuseTrailingFrame return nil for any input.
func TestTrailingBytesAreRefusedRatherThanDropped(t *testing.T) {
	if err := refuseTrailingFrame(0); err != nil {
		t.Fatalf("a complete stream was refused: %v", err)
	}
	err := refuseTrailingFrame(17)
	if err == nil {
		t.Fatal("a stream that ended mid-frame was accepted; the last sample would be a partial picture")
	}
	if !strings.Contains(err.Error(), "17") {
		t.Fatalf("the refusal does not say how many bytes were left over: %v", err)
	}
}

// TestTheBoundedTailKeepsTheLastBytesAndNamesAnEmptyLog: a measurement tool that prints an
// unbounded decoder log has replaced one unreadable refusal with a different unreadable
// refusal.
func TestTheBoundedTailKeepsTheLastBytesAndNamesAnEmptyLog(t *testing.T) {
	var b boundedTail
	if _, err := b.Write([]byte("line one\nline two\n")); err != nil {
		t.Fatalf("the tail refused a write: %v", err)
	}
	if got := b.String(); !strings.Contains(got, "line two") {
		t.Fatalf("the tail dropped the line it was supposed to keep:\n%s", got)
	}
	var empty boundedTail
	if !strings.Contains(empty.String(), "nothing to stderr") {
		t.Fatalf("an empty log prints %q; a reader needs to know the tool was silent rather than absent",
			empty.String())
	}
}

// TestAWindowIsSampledFrameByFrameOverRealMaterial exercises the reader against a REAL
// clip, because the reader is the one thing here that talks to ffmpeg and a unit test over
// a struct proves nothing about it.
//
// ffmpeg is a hard runtime dependency of this module, so this does not skip: it fails if the
// tool the module is built around is absent.
func TestAWindowIsSampledFrameByFrameOverRealMaterial(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeMovingClipForTest(t, clip)

	w, err := parseFootageWindow("0", "2", "0.5")
	if err != nil {
		t.Fatal(err)
	}
	var seen []sampledFrame
	read, err := readWindowFrames(ctx(), clip, w, int(w.For/w.Every), func(f sampledFrame) error {
		// The RGB buffer is REUSED between frames, as sampledFrame documents. A consumer
		// that keeps the slice is holding the next frame, so this test copies it - and the
		// copy is what makes the frame-distinctness assertion below mean anything.
		f.RGB = append([]byte(nil), f.RGB...)
		seen = append(seen, f)
		return nil
	})
	if err != nil {
		t.Fatalf("the window was not sampled: %v", err)
	}
	if read.Read == 0 {
		t.Fatal("the window yielded no frames")
	}
	if read.Read != read.Declared {
		t.Fatalf("%d frame(s) read where %d were declared. The two are printed on every run because a "+
			"window that delivered fewer samples than it promised is a different measurement",
			read.Read, read.Declared)
	}
	if read.Trailing != 0 {
		t.Fatalf("%d trailing bytes after a clean stream", read.Trailing)
	}
	if read.W != 320 || read.H != 180 {
		t.Fatalf("the frame size came back %dx%d; writeMovingClipForTest renders 320x180", read.W, read.H)
	}
	for i, f := range seen {
		if f.Index != i {
			t.Fatalf("frame %d carries index %d; the sequence number must count from the window's start", i, f.Index)
		}
		if len(f.RGB) != f.W*f.H*3 {
			t.Fatalf("frame %d carries %d bytes for %dx%d", i, len(f.RGB), f.W, f.H)
		}
	}
	// The frames must not all be identical, or the window sampled one picture four times
	// and called it a measurement.
	if allSamePixels(seen) {
		t.Fatal("every sampled frame is byte-identical; the window sampled one picture and reported it as four")
	}
}

// writeMovingClipForTest renders a clip whose picture CHANGES, because the reader is
// supposed to hand back different frames and a test over a static pattern cannot tell a
// sampled window from one picture reported four times.
//
// A `testsrc` background with a `drawbox` whose x is a function of t, which is the same
// construction `testdata/labels.json` records for car-crossing.mp4.
func writeMovingClipForTest(t *testing.T, path string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module: %v", err)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x180:rate=5",
		"-vf", "drawbox=x='mod(t*160\\,300)':y=70:w=40:h=40:color=red@1.0:t=fill",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", path}
	if out, rerr := exec.Command(ffmpeg, args...).CombinedOutput(); rerr != nil {
		t.Fatalf("ffmpeg could not render the moving test clip: %v: %s", rerr, out)
	}
}

func allSamePixels(frames []sampledFrame) bool {
	if len(frames) < 2 {
		return true
	}
	for i := 1; i < len(frames); i++ {
		if len(frames[i].RGB) != len(frames[0].RGB) {
			return false
		}
		for j := range frames[0].RGB {
			if frames[i].RGB[j] != frames[0].RGB[j] {
				return false
			}
		}
	}
	return true
}

// TestTheCropPassProposesAndSlicesRealRegions exercises everything the crop pass does
// EXCEPT the inference, over a real clip: the novelty engine advances, the policy turns
// candidates into rectangles, and every rectangle is checked against upstream's own
// `Clamp` - the arithmetic `Detect` will apply to it.
//
// It is here because the inference is the one part that cannot run against this tree's
// export, and a command whose only tested path is the refusal is a command whose working
// path is a comment.
//
// Two assertions are controls in their own right:
//
//   - BOTH SETTINGS MUST HAVE SEEN THE SAME FRAMES. If the crop pass skipped a frame, the
//     side-by-side comparison would be between two different sample sets and the whole
//     headline would be meaningless. That is why the crop pass records a frame with no
//     regions rather than omitting it.
//   - THE POLICY'S ARITHMETIC MUST AGREE WITH UPSTREAM'S. Our pad-then-clamp and
//     `Rect.Clamp` are the same clamp twice; if they ever disagree the crop is measured on
//     a rectangle upstream did not use.
func TestTheCropPassProposesAndSlicesRealRegions(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeMovingClipForTest(t, clip)

	w, err := parseFootageWindow("0", "2", "0.5")
	if err != nil {
		t.Fatal(err)
	}
	truth := &truthFile{
		path:    "in-test",
		classes: map[string]int{"car": 1},
		Boxes:   []truthBox{{At: 0.5, Class: "car", X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.5}},
	}
	res, err := measureTwoPasses(ctx(), clip, w, defaultCropPolicy(), nil, truth)
	if err != nil {
		t.Fatalf("the window could not be sampled: %v", err)
	}
	if len(res.regions) == 0 {
		t.Fatal("the novelty engine proposed no region on a clip with a moving box in it, so the crop pass " +
			"was never exercised over real pixels")
	}
	// (1) the same frames under both settings.
	if len(res.whole.Frames) != len(res.crop.Frames) {
		t.Fatalf("whole-frame recorded %d frame(s) and the crop pass recorded %d. The two settings are "+
			"compared side by side, and that comparison is meaningless unless they saw the same frames",
			len(res.whole.Frames), len(res.crop.Frames))
	}
	if len(res.whole.Frames) != res.Read.Read {
		t.Fatalf("%d frame(s) read, %d under the whole-frame pass and %d under the crop pass",
			res.Read.Read, len(res.whole.Frames), len(res.crop.Frames))
	}
	if res.crop.SilentFrames == 0 {
		t.Fatal("every frame proposed a region on a four-frame clip of a moving box; the baseline frame and " +
			"at least one other should have proposed nothing")
	}
	// (2) the policy's arithmetic agrees with upstream's, on every proposed region.
	for _, s := range res.regions {
		if s.Rect.Empty() {
			t.Fatalf("a proposed region is the zero Rect at %.1fs, which upstream reads as the WHOLE FRAME", s.At)
		}
		if s.Rect.X < 0 || s.Rect.Y < 0 || s.Rect.X+s.Rect.W > res.Read.W || s.Rect.Y+s.Rect.H > res.Read.H {
			t.Fatalf("a proposed region %s runs off a %dx%d frame", rectString(s.Rect), res.Read.W, res.Read.H)
		}
		clamped, cerr := s.Rect.Clamp(res.Read.W, res.Read.H)
		if cerr != nil {
			t.Fatalf("upstream refused a rectangle the policy produced: %s: %v", rectString(s.Rect), cerr)
		}
		if clamped != s.Rect {
			t.Fatalf("Rect.Clamp changed %s to %s. Our padding and upstream's clamping do the same job "+
				"twice and the one that runs second decides the answer", rectString(s.Rect), rectString(clamped))
		}
		// And the region must actually select pixels: a rectangle of the right size in the
		// wrong place is invisible in a count.
		got, xerr := s.Rect.Extract(make([]byte, res.Read.W*res.Read.H*3), res.Read.W, res.Read.H, nil)
		if xerr != nil {
			t.Fatalf("Extract refused a rectangle the policy produced: %s: %v", rectString(s.Rect), xerr)
		}
		if len(got) != s.Rect.W*s.Rect.H*3 {
			t.Fatalf("Extract returned %d bytes for a %s rectangle", len(got), rectString(s.Rect))
		}
	}
	// (3) the ground truth landed on the frames it names and nowhere else.
	var placed int
	for _, f := range res.whole.Frames {
		placed += len(f.Truth)
	}
	if placed != 1 {
		t.Fatalf("the one ground-truth box landed on %d frames; the tolerance is half the interval and the "+
			"box is 0.5s from exactly one of four samples", placed)
	}
}

// ------------------------------------------------------------ ground truth ---

// TestAGroundTruthBoxTooFarFromEveryFrameIsCountedNotAttached: attaching a box 2.5 s from a
// sampled frame would manufacture a miss on a frame the annotator never described, and a
// recall that penalises the annotator's timing is a recall about the interval rather than
// about the detector.
func TestAGroundTruthBoxTooFarFromEveryFrameIsCountedNotAttached(t *testing.T) {
	tf := &truthFile{
		path:    "truth.json",
		classes: map[string]int{"car": 1},
		Boxes:   []truthBox{{At: 2.5, Class: "car", X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2}},
	}
	if got := tf.at(1.0, 0.5); len(got) != 0 {
		t.Fatalf("a box 1.5s from the frame was attached to it; %d box(es) matched", len(got))
	}
	if tf.unmatched != 1 {
		t.Fatalf("the box that matched nothing was not counted: unmatched = %d", tf.unmatched)
	}
	// A quarter of an interval away is inside the tolerance.
	if got := tf.at(2.25, 0.5); len(got) != 1 {
		t.Fatal("a box a quarter of an interval from the frame was not attached")
	}
}

// TestAGroundTruthFileWithNoBoxesIsRefusedRatherThanReportedAsZeroRecall.
func TestAGroundTruthFileWithNoBoxesIsRefusedRatherThanReportedAsZeroRecall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truth.json")
	if err := os.WriteFile(path, []byte(`{"material":"x.mp4","boxes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTruthFile(path); err == nil {
		t.Fatal("a ground-truth file with an empty boxes array was accepted; its recall denominator is zero")
	}
}

// TestAGroundTruthFileNamedAndUnreadableIsAnErrorNotAnAbsence: the operator asked for ground
// truth and did not get it. Continuing would print NOT RUN for the rates as though nobody had
// asked.
func TestAGroundTruthFileNamedAndUnreadableIsAnErrorNotAnAbsence(t *testing.T) {
	if _, err := readTruthFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("a --truth path that cannot be read was treated as an absence")
	}
	// And no --truth at all IS an absence, not an error.
	if got, err := readTruthFile(""); got != nil || err != nil {
		t.Fatalf("no --truth returned (%v, %v); it must be a clean absence", got, err)
	}
}

// TestAGroundTruthBoxWithNoClassIsRefused: a box with no class cannot go in a per-CLASS table,
// and guessing one from the picture is the guess this file exists to refuse.
func TestAGroundTruthBoxWithNoClassIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truth.json")
	body := `{"material":"x.mp4","boxes":[{"at":1.0,"x1":0.1,"y1":0.1,"x2":0.2,"y2":0.2}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTruthFile(path); err == nil {
		t.Fatal("a ground-truth box with no class was accepted")
	}
}

// TestExpressibleFollowsTheModelVocabularyNotTheFile: `sunglasses` is a real Open Images class
// and is not one of the 80 this build can name. It has to be counted as a taxonomy limit, not
// as a miss.
func TestExpressibleFollowsTheModelVocabularyNotTheFile(t *testing.T) {
	if !expressible("car") {
		t.Fatal("car is in the vocabulary and was reported as unnameable")
	}
	if !expressible("hair drier") {
		t.Fatal("hair drier IS one of the 80 this build names; a test that assumed otherwise would prove " +
			"nothing about the vocabulary")
	}
	if expressible("sunglasses") {
		t.Fatal("a class the vocabulary does not hold was reported as nameable; it would be counted as a " +
			"miss rather than as a taxonomy limit")
	}
}
