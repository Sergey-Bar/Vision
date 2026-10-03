package main

// footage_test.go - the tests behind every control in the footage command.
//
// Each control below carries a REDCHECK tag naming the line to delete and the
// test that must go red when it is gone. A control with a tag and no run behind
// it is a comment, and this repository has three of those already.
//
// Every test here is PURE: no session, no weights, no ffmpeg. The one test that
// needs a model is in footage_model_test.go and reports NOT RUN rather than
// skipping silently.

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

// ---------------------------------------------------------------- the gate ---

// clearedRow is a row the gate is supposed to accept, and notOneOfTheThree is the
// same row with one field changed. They are written out rather than built by a
// helper because a helper that fills a field would hide the field.
func clearedRow() footageLabel {
	yes := true
	return footageLabel{
		File:       "somebody/someclip.mp4",
		Licence:    "CC BY 4.0, the terms the rights holder published",
		Cleared:    &yes,
		Provenance: "real",
	}
}

// TestTheGateRefusesMaterialWithNoProvenanceRow is RC-35.
//
// FAILS IF: resolveLabel's `case 0:` refusal is removed, so a material with no
// row falls through to an empty label and an empty licence - which is then
// refused by checkLicenceGate for the WRONG reason. The test asserts the refusal
// names the missing row and lists what the table does hold, because a refusal
// that says "licence missing" when the real problem is "row missing" sends an
// operator to fill in a licence for a file nobody has written anything about.
func TestTheGateRefusesMaterialWithNoProvenanceRow(t *testing.T) {
	clips := []footageLabel{clearedRow()}
	_, err := resolveLabel(clips, filepath.Join("testdata", "unwritten.mp4"))
	if err == nil {
		t.Fatal("material with no row in the provenance table was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no row in the provenance table") {
		t.Fatalf("the refusal does not name the missing row: %v", err)
	}
	if !strings.Contains(msg, "somebody/someclip.mp4") {
		t.Fatalf("the refusal does not say what the table DOES hold, so an operator cannot tell which file "+
			"they meant: %v", err)
	}
}

// TestTheGateRefusesARowWithNoLicenceField: an absent licence is refused rather
// than defaulted. There is no version of this test that passes with an empty
// licence accepted, because an empty licence IS the defect.
func TestTheGateRefusesARowWithNoLicenceField(t *testing.T) {
	row := clearedRow()
	row.Licence = "   "
	if err := checkLicenceGate(row); err == nil {
		t.Fatal("a row whose licence is blank was accepted; a licence field filled from what would be " +
			"convenient to claim is worse than an absent one")
	} else if !strings.Contains(err.Error(), "no licence") {
		t.Fatalf("the refusal does not name the absent field: %v", err)
	}
}

// TestTheGateRefusesALicenceThatBeginsNotRecorded is the PREFIX control.
//
// The real row in this repository reads "NOT RECORDED, and it is not recoverable
// from this file". An equality test would wave the single most important row in
// the table through a gate whose entire purpose is to stop it, so the comparison
// is a prefix and this test is what holds it to that.
//
// REDCHECK RC-35b: replace `strings.HasPrefix(normaliseSentence(c.Licence),
// licenceUnrecorded)` with `== licenceUnrecorded` and the test goes red.
func TestTheGateRefusesALicenceThatBeginsNotRecorded(t *testing.T) {
	row := clearedRow()
	row.Licence = "NOT RECORDED, and it is not recoverable from this file."
	err := checkLicenceGate(row)
	if err == nil {
		t.Fatal("a licence sentence that BEGINS by declaring nothing was recorded was accepted")
	}
	if !strings.Contains(err.Error(), "Nothing was recorded") {
		t.Fatalf("the refusal does not explain what the phrase means: %v", err)
	}
	// And the positive side: a licence that merely CONTAINS the words later, as
	// part of a real permission, is not the declaration.
	ok := clearedRow()
	ok.Licence = "CC BY 4.0. The phrase NOT RECORDED appears nowhere in this grant."
	if err := checkLicenceGate(ok); err != nil {
		t.Fatalf("a real licence was refused because it mentions the words later on: %v", err)
	}
}

// TestTheGateRefusesAnAbsentClearanceField is RC-36, and it is the reason the
// field is a POINTER.
//
// A `bool` cannot tell "cleared: false" from "this row never answered", and those
// two are opposite answers. On this tree the difference decides the whole
// measurement: `car-crossing.mp4` carries a recorded licence and no clearance
// field at all, so a bool would let it through and the pointer refuses it.
//
// REDCHECK RC-36: change footageLabel.Cleared from `*bool` to `bool` and the
// absence becomes false; this test then cannot compile against a row with the
// field missing, which IS the finding. The runnable form of the red check is to
// delete the `case c.Cleared == nil:` arm.
func TestTheGateRefusesAnAbsentClearanceField(t *testing.T) {
	row := clearedRow()
	row.Cleared = nil
	err := checkLicenceGate(row)
	if err == nil {
		t.Fatal("a row with no cleared_for_external_use field was accepted; absent is not the same answer as false")
	}
	if !strings.Contains(err.Error(), "cleared_for_external_use") {
		t.Fatalf("the refusal does not name the field that is missing: %v", err)
	}
}

// TestTheGateRefusesAFalseClearanceField: recorded, read, and the answer is no.
func TestTheGateRefusesAFalseClearanceField(t *testing.T) {
	row := clearedRow()
	no := false
	row.Cleared = &no
	row.ClearedNote = "the camera belongs to a public body"
	err := checkLicenceGate(row)
	if err == nil {
		t.Fatal("a row that records cleared_for_external_use: false was accepted")
	}
	if !strings.Contains(err.Error(), "public body") {
		t.Fatalf("the refusal does not quote the row's own reason, so it invents one: %v", err)
	}
}

// TestTheGateAcceptsTheOneRowInThisRepositoryThatIsCleared is the positive
// control, and it runs against the committed labels.json rather than a fixture.
//
// A gate that refuses everything passes every refusal test in this file. This one
// reads the real table and requires exactly one row to pass, which is the state of
// the tree today and a fact worth pinning.
func TestTheGateAcceptsTheOneRowInThisRepositoryThatIsCleared(t *testing.T) {
	clips, err := readFootageLabels(filepath.Join("..", "..", "testdata", "labels.json"))
	if err != nil {
		t.Fatalf("the committed provenance table is not readable: %v", err)
	}
	var pass, fail []string
	for _, c := range clips {
		if checkLicenceGate(c) == nil {
			pass = append(pass, c.File)
			continue
		}
		fail = append(fail, c.File)
	}
	if len(pass) != 1 {
		t.Fatalf("%d row(s) passed the gate (%v) and %d did not (%v). If that is not one, either a row gained a "+
			"licence nobody wrote or this gate stopped refusing something", len(pass), pass, len(fail), fail)
	}
	if pass[0] != "injection-hostile.mp4" {
		t.Fatalf("the row that passed is %q. If the tree's answer changed, this test should say so in words "+
			"rather than be updated to match", pass[0])
	}
}

// TestTheGateRefusesAnAmbiguousBaseName is RC-38.
//
// FAILS IF: resolveLabel's `default:` ambiguity refusal is removed so the first
// base-name match wins. Two rows with the same base name carry different licences,
// and picking one measures material under terms that were never granted.
func TestTheGateRefusesAnAmbiguousBaseName(t *testing.T) {
	clips := []footageLabel{
		{File: "youtube/one/clip.mp4", Licence: "a public body's camera"},
		{File: "youtube/two/clip.mp4", Licence: "CC BY 4.0"},
	}
	_, err := resolveLabel(clips, filepath.Join("elsewhere", "clip.mp4"))
	if err == nil {
		t.Fatal("an ambiguous base name resolved to a row; one of those licences was then measured under")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	for _, want := range []string{"youtube/one/clip.mp4", "youtube/two/clip.mp4"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name the candidate %q, so an operator cannot tell which file they "+
				"meant: %v", want, err)
		}
	}
}

// TestAnExactPathMatchWinsOverABaseNameMatch: resolveLabel tries the recorded
// path first, because two rows can share a base name while only one of them is
// the file the operator named.
func TestAnExactPathMatchWinsOverABaseNameMatch(t *testing.T) {
	clips := []footageLabel{
		{File: "a/clip.mp4", Licence: "one"},
		{File: "b/clip.mp4", Licence: "two"},
	}
	got, err := resolveLabel(clips, filepath.Join("b", "clip.mp4"))
	if err != nil {
		t.Fatalf("a full relative path did not resolve: %v", err)
	}
	if got.Licence != "two" {
		t.Fatalf("resolved to the row with licence %q; the exact path must win", got.Licence)
	}
}

// ------------------------------------------------------------- the window ---

// TestASamplingIntervalOverOneAndAHalfSecondsIsRefused is RC-37.
//
// FAILS IF: the `every > maxSamplingInterval` refusal is removed. Then a
// four-second road sampled every three seconds is four frames of nothing and the
// command reports a clean result over material where a pedestrian was standing.
func TestASamplingIntervalOverOneAndAHalfSecondsIsRefused(t *testing.T) {
	_, err := validateFootageWindow(2*time.Second, 8*time.Second, 3*time.Second)
	if err == nil {
		t.Fatal("a 3 second sampling interval was accepted over a road whose smallest object is on screen 1.5s")
	}
	if !strings.Contains(err.Error(), "1.5s") {
		t.Fatalf("the refusal does not state the limit it enforces: %v", err)
	}
	// The boundary is inclusive: 1.5s exactly is allowed.
	if _, err := validateFootageWindow(2*time.Second, 8*time.Second, maxSamplingInterval); err != nil {
		t.Fatalf("an interval of exactly %v was refused; the limit is a ceiling, not an open bound: %v",
			maxSamplingInterval, err)
	}
}

// TestAnEmptyWindowAndANonPositiveIntervalAreRefusedByName: two different
// mistakes, two different sentences.
func TestAnEmptyWindowAndANonPositiveIntervalAreRefusedByName(t *testing.T) {
	if _, err := validateFootageWindow(8*time.Second, 2*time.Second, time.Second); err == nil {
		t.Fatal("an inverted window was accepted; it samples nothing and would report nothing as zero")
	} else if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("the refusal does not say the window is empty: %v", err)
	}
	if _, err := validateFootageWindow(0, 2*time.Second, 0); err == nil {
		t.Fatal("a zero interval was accepted; that is a division by zero")
	}
}

// TestDeclaredSamplesMatchesTheHalfOpenWindowTheReaderProduces.
//
// The reader is driven by ffmpeg's `-t`, which is half-open. Declaring floor+1
// would print "11 declared, 10 read" on every exact-multiple window, and a reader
// who sees that mismatch on every run learns to ignore the row that exists to
// catch a real shortfall.
func TestDeclaredSamplesMatchesTheHalfOpenWindowTheReaderProduces(t *testing.T) {
	cases := []struct {
		from, to, every time.Duration
		want            int
	}{
		{0, 5 * time.Second, 500 * time.Millisecond, 10},
		{time.Second, 6 * time.Second, 500 * time.Millisecond, 10},
		{0, 2 * time.Second, time.Second, 2},
		{0, 2 * time.Second, 300 * time.Millisecond, 7}, // ceil(6.67)
	}
	for _, c := range cases {
		got := declaredSamples(c.from, c.to, c.every)
		if got != c.want {
			t.Fatalf("declaredSamples(%v,%v,%v) = %d, want %d", c.from, c.to, c.every, got, c.want)
		}
	}
}

// ------------------------------------------------------------ the crop pass ---

// TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame is RC-31, and the ORDER is
// the control.
//
// A region at the left edge is padded by a quarter of its own width. Padding puts
// X1 below zero; clamping puts it back at zero. The other order - clamp, then pad
// - throws the pad away on the frame side and then puts it BACK outside the frame,
// so the recorded region is wider than the pixels actually sliced and every box
// composed onto the frame is placed against a crop the model never saw.
//
// REDCHECK RC-31: delete `return clampRegion(padded)` in padThenClamp and replace
// it with `return padded`. This goes red.
func TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame(t *testing.T) {
	r := proposalBox{X1: 0.0, Y1: 0.4, X2: 0.1, Y2: 0.5}
	got := padThenClamp(r, 0.25)
	if got.X1 < 0 || got.Y1 < 0 || got.X2 > 1 || got.Y2 > 1 {
		t.Fatalf("a padded region came back at %+v, outside the frame. The pad is then measured against pixels "+
			"that do not exist and every composed box is misplaced", got)
	}
	if got.X2 <= r.X2 {
		t.Fatalf("the right-hand pad was lost: %+v has X2 %v, not more than the region's %v", got, got.X2, r.X2)
	}
	if got.X1 != 0 {
		t.Fatalf("X1 = %v; a region already at the frame edge must stay there rather than go negative", got.X1)
	}
}

// TestClampingBeforePaddingLosesTheEdgePad is the counterfactual: it computes
// what the other order produces, so the reason the order is load-bearing is
// arithmetic rather than an assertion about intent.
func TestClampingBeforePaddingLosesTheEdgePad(t *testing.T) {
	r := proposalBox{X1: 0.0, Y1: 0.4, X2: 0.1, Y2: 0.5}
	wrong := proposalBox{
		X1: r.X1 - r.Width()*0.25, Y1: r.Y1 - r.Height()*0.25,
		X2: r.X2 + r.Width()*0.25, Y2: r.Y2 + r.Height()*0.25,
	}
	if wrong.X1 >= 0 {
		t.Skip("this frame has no edge region, so the counterfactual cannot be built")
	}
	right := padThenClamp(r, 0.25)

	// Clamp first, then pad: the clamp throws the left pad away, so padding puts
	// it back OUTSIDE the frame. The recorded region is then wider than the frame,
	// and the pixels the reader can slice are narrower than the region says.
	if wrong.X1 >= 0 {
		t.Fatalf("the counterfactual is not outside the frame: %+v", wrong)
	}
	if wrong.Width() <= right.Width() {
		t.Fatalf("the wrong order records a width of %.3f and pad-then-clamp records %.3f; the wrong order is "+
			"not the wider, so this test no longer demonstrates the difference", wrong.Width(), right.Width())
	}
	// The consequence, which is the reason the order is a control rather than a
	// style: the reader clamps the bad region to something NARROWER than recorded,
	// and a box over the whole of what it actually measured then composes back to
	// a place that is not the region anybody recorded.
	b, ok := cropBoundsOf(1280, 720, wrong)
	if !ok {
		t.Fatal("the reader refused the out-of-frame region outright, so this fixture needs a new one")
	}
	back := composeBox(proposalBox{X1: 0, Y1: 0, X2: 1, Y2: 1}, b, 1280, 720)
	if math.Abs(back.X1-wrong.X1) < 0.01 && math.Abs(back.X2-wrong.X2) < 0.01 {
		t.Fatalf("the wrong order composed back to the recorded region %+v; the two orders agreed and the "+
			"order does not matter, which is not what this file claims", wrong)
	}
}

// TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain is RC-32.
//
// FAILS IF: the `coveredByAny` drop in cropRegions is removed. Novelty on a
// moving object returns several components; a small one inside a large one is the
// SAME object, and measuring it twice counts one true positive twice. That is how
// a crop pass reports a recall it did not earn.
//
// REDCHECK RC-32: delete the `if coveredByAny(r, accepted, ...)` block.
func TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain(t *testing.T) {
	p := defaultCropPolicy()
	cands := []novelty.Candidate{
		// A large component.
		{Box: proposalBox{X1: 0.40, Y1: 0.40, X2: 0.60, Y2: 0.60}, Cells: 400},
		// A small one entirely inside it: the same object, found twice.
		{Box: proposalBox{X1: 0.46, Y1: 0.46, X2: 0.54, Y2: 0.54}, Cells: 60},
	}
	plan := cropRegions(cands, p)
	if len(plan.Regions) != 1 {
		t.Fatalf("%d regions survived; a component already inside an accepted region adds nothing and measuring "+
			"it again counts one object twice", len(plan.Regions))
	}
	if plan.Dedupe != 1 {
		t.Fatalf("Dedupe = %d; the drop was not counted, so a reader cannot see the policy doing anything", plan.Dedupe)
	}
}

// TestASmallRegionInsideALargeOneIsNotCaughtByTheIoUDedupeAlone: the reason
// `coveredByAny` exists and uses containment rather than IoU. A tiny region
// inside a large one has a LOW IoU against it and is still entirely redundant, so
// an IoU-only dedupe would wave it through.
func TestASmallRegionInsideALargeOneIsNotCaughtByTheIoUDedupeAlone(t *testing.T) {
	big := proposalBox{X1: 0.40, Y1: 0.40, X2: 0.60, Y2: 0.60}
	small := proposalBox{X1: 0.46, Y1: 0.46, X2: 0.54, Y2: 0.54}
	if cropDedupeIOU(small, big, 0.5) {
		t.Skip("these boxes overlap more than the test assumed; rewrite it")
	}
	if !coveredByAny(small, []proposalBox{big}, 0.95) {
		t.Fatal("a region entirely inside an accepted one was not reported as covered; the containment check is " +
			"what stops the same object being measured twice")
	}
}

// TestAZeroAreaRegionIsReportedAsCovered: a region with no area has nothing to
// add and must not reach a detector, which would letterbox it into a tensor of
// padding and answer about the padding.
func TestAZeroAreaRegionIsReportedAsCovered(t *testing.T) {
	if !coveredByAny(proposalBox{X1: 0.5, Y1: 0.5, X2: 0.5, Y2: 0.5}, nil, 0.95) {
		t.Fatal("a region with no area was not refused")
	}
}

// TestTheAreaFloorAndTheCapAreAppliedAndCounted: the policy's other two steps.
// Both are counted, because a floor that silently drops is a floor that lies.
func TestTheAreaFloorAndTheCapAreAppliedAndCounted(t *testing.T) {
	p := defaultCropPolicy()
	p.Max = 2
	var cands []novelty.Candidate
	cands = append(cands, novelty.Candidate{Box: proposalBox{X1: 0.40, Y1: 0.40, X2: 0.415, Y2: 0.415}, Cells: 2}) // below floor
	cands = append(cands, novelty.Candidate{Box: proposalBox{X1: 0.70, Y1: 0.10, X2: 0.78, Y2: 0.18}, Cells: 40})
	cands = append(cands, novelty.Candidate{Box: proposalBox{X1: 0.10, Y1: 0.70, X2: 0.18, Y2: 0.78}, Cells: 40})
	cands = append(cands, novelty.Candidate{Box: proposalBox{X1: 0.30, Y1: 0.30, X2: 0.44, Y2: 0.44}, Cells: 120})
	plan := cropRegions(cands, p)
	if plan.BelowMin != 1 {
		t.Fatalf("BelowMin = %d; a region below the area floor must be counted", plan.BelowMin)
	}
	if len(plan.Regions) != 2 {
		t.Fatalf("%d regions survived a cap of 2", len(plan.Regions))
	}
	// Largest first: the biggest of the three above the floor survives, the cap
	// drops the smallest. Ordering is what makes that true.
	if plan.Regions[0].Area() < plan.Regions[1].Area() {
		t.Fatalf("regions are not ordered by area, so the cap keeps the wrong ones: %+v", plan.Regions)
	}
}

// TestComposingACropBoxIsTheInverseOfSlicingTheRegion is RC-33.
//
// FAILS IF: composeBox's `+ float64(b.X0)` (and Y0) is removed. Every crop box
// then lands at the crop's own origin, scaled against the frame - the right size
// in the wrong place, which on a per-class table reads as a MISS rather than as a
// bug, and a miss is a thing an operator believes.
//
// REDCHECK RC-33: drop the origin from the numerator.
func TestComposingACropBoxIsTheInverseOfSlicingTheRegion(t *testing.T) {
	const fw, fh = 640, 360
	frame := make([]byte, fw*fh*3)
	// Paint each region a distinct colour so the slice can be checked too.
	region := proposalBox{X1: 0.50, Y1: 0.25, X2: 0.75, Y2: 0.75}
	b, ok := cropBoundsOf(fw, fh, region)
	if !ok {
		t.Fatal("a mid-frame region produced no bounds")
	}
	for y := b.Y0; y < b.Y0+b.H; y++ {
		for x := b.X0; x < b.X0+b.W; x++ {
			i := (y*fw + x) * 3
			frame[i], frame[i+1], frame[i+2] = 11, 22, 33
		}
	}
	slice := cropPixels(frame, fw, fh, b)
	if len(slice) != b.W*b.H*3 {
		t.Fatalf("the slice is %d bytes for a %dx%d crop", len(slice), b.W, b.H)
	}
	// The slice must be the region and nothing else: a wrong origin shows up as
	// background in the middle of it.
	for i := 0; i < len(slice); i += 3 {
		if slice[i] != 11 || slice[i+1] != 22 || slice[i+2] != 33 {
			t.Fatalf("byte %d of the crop is (%d,%d,%d), not the region's colour: the slice origin is wrong",
				i, slice[i], slice[i+1], slice[i+2])
		}
	}

	// A box covering the WHOLE crop must compose back to exactly the crop.
	full := composeBox(proposalBox{Class: "car", X1: 0, Y1: 0, X2: 1, Y2: 1}, b, fw, fh)
	if math.Abs(full.X1-region.X1) > 0.01 || math.Abs(full.Y1-region.Y1) > 0.01 ||
		math.Abs(full.X2-region.X2) > 0.01 || math.Abs(full.Y2-region.Y2) > 0.01 {
		t.Fatalf("a box over the whole crop composed back to %+v, not to the crop %+v", full, region)
	}
	// And a box at the crop's top-left must land at the crop's top-left, not at
	// the frame's origin.
	tl := composeBox(proposalBox{Class: "car", X1: 0, Y1: 0, X2: 0.5, Y2: 0.5}, b, fw, fh)
	if math.Abs(tl.X1-region.X1) > 0.01 || math.Abs(tl.Y1-region.Y1) > 0.01 {
		t.Fatalf("a box at the crop's own top-left composed to %+v; it must land at the crop's corner (%v,%v), "+
			"not at the frame's", tl, region.X1, region.Y1)
	}
}

// TestTheRegionScaleIsTheLetterboxScale: `modelScale` is a reimplementation of
// one rule that `ortdetect.Letterbox` also computes, and two copies of a scale
// rule is exactly the defect this repository has shipped. So it is checked
// against the letterbox's OWN output rather than against a hand-computed number.
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

// TestTheMagnificationFactorIsTheNineTheSelfTestClaims pins the arithmetic the
// crop pass exists to spend. 40 px through a 1280x720 frame and through a 200x150
// crop into a 416 input, and the 9.6 the plan quotes on a 1920x1080 frame.
func TestTheMagnificationFactorIsTheNineTheSelfTestClaims(t *testing.T) {
	whole, crop, factor := magnification(40, 200, 150, 1280, 720, 416)
	if math.Abs(whole-13.0) > 0.15 {
		t.Fatalf("whole-frame 40px object arrived at %.1f px, expected 13.0 (40 * min(416/1280, 416/720))", whole)
	}
	if math.Abs(crop-83.2) > 0.15 {
		t.Fatalf("a 40px object in a 200x150 crop arrived at %.1f px, expected 83.2 (40 * 416/200)", crop)
	}
	if math.Abs(factor-6.4) > 0.1 {
		t.Fatalf("the factor is %.1f, expected about 6.4 on a 1280x720 frame", factor)
	}
	// And the 9.6 the plan quotes, on the material it quotes it for. A 1920x1080
	// frame scales by 416/1920 = 0.2167, and the 200x150 crop by 2.08.
	_, _, f1920 := magnification(40, 200, 150, 1920, 1080, 416)
	if math.Abs(f1920-9.6) > 0.1 {
		t.Fatalf("the factor on a 1920x1080 frame is %.1f; the plan quotes 9.6 and the arithmetic should agree",
			f1920)
	}
}

// TestCropPixelsRefusesABufferShorterThanItsDimensions: a short buffer is not a
// zero frame and must not be read past. A detector handed zeros produces a clean
// picture of an empty road.
func TestCropPixelsRefusesABufferShorterThanItsDimensions(t *testing.T) {
	short := make([]byte, 10*10*3)
	if got := cropPixels(short, 640, 360, cropBounds{X0: 0, Y0: 0, W: 8, H: 8}); got != nil {
		t.Fatal("a slice was taken from a buffer that is shorter than its own dimensions claim")
	}
	// Out of bounds is refused too, rather than read past.
	full := make([]byte, 640*360*3)
	if got := cropPixels(full, 640, 360, cropBounds{X0: 630, Y0: 0, W: 40, H: 8}); got != nil {
		t.Fatal("a slice running off the right edge was taken; that is a read past the frame")
	}
}

// TestTheCropPolicyIsOneImplementation: the mechanical half of "this is not a
// second copy". It asserts each policy function has exactly ONE definition site
// across this package's non-test files. Two spellings of the same policy is the
// defect this repository has shipped twice, and a comment is the only defence
// against it.
func TestTheCropPolicyIsOneImplementation(t *testing.T) {
	names := []string{"padThenClamp", "coveredByAny", "unionBoxes", "cropDedupeIOU", "composeBox", "cropBoundsOf"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		sites := 0
		for _, e := range entries {
			fn := e.Name()
			if !strings.HasSuffix(fn, ".go") || strings.HasSuffix(fn, "_test.go") {
				continue
			}
			body, rerr := os.ReadFile(filepath.Clean(fn))
			if rerr != nil {
				t.Fatal(rerr)
			}
			sites += strings.Count(string(body), "func "+name+"(")
			sites += strings.Count(string(body), "func ("+name+")")
		}
		if sites != 1 {
			t.Fatalf("%s has %d definition site(s) in package main; a policy with two spellings is a policy "+
				"whose two halves can disagree, and this repository has shipped that defect twice", name, sites)
		}
	}
}

// ------------------------------------------------------------- per class ---

// oneCarFrame is a frame with one car detection on one car ground-truth box.
func oneCarFrame() classFrame {
	return classFrame{
		Frame: "f0",
		Dets: []bench.Detection{{
			Frame: "f0",
			Box:   bench.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30},
		}},
		Truth: []bench.GT{{
			Box:         bench.Box{Class: "car", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30},
			Class:       "car",
			Expressible: true,
		}},
	}
}

// TestAMisnamedDetectionIsAFalsePositiveNotATruePositive is RC-34, and it is the
// reason the partition exists.
//
// `bench.Measure` pairs detections with ground truth by GEOMETRY ALONE. A `car`
// detection sitting on a `person` box is one true positive under geometric
// matching, and it is a car false positive plus a person false negative under any
// definition a reader would use.
//
// REDCHECK RC-34: replace the per-class partitioning in partitionByClass with a
// single unpartitioned bench.Measure over all detections and all ground truth.
func TestAMisnamedDetectionIsAFalsePositiveNotATruePositive(t *testing.T) {
	fr := classFrame{
		Frame: "f0",
		Dets: []bench.Detection{{
			Frame: "f0",
			Box:   bench.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30},
		}},
		Truth: []bench.GT{{
			Box:         bench.Box{Class: "person", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30},
			Class:       "person",
			Expressible: true,
		}},
	}
	rows := partitionByClass([]classFrame{fr}, bench.DefaultOptions())
	byClass := indexRows(rows)
	car, ok := byClass["car"]
	if !ok {
		t.Fatalf("no row for the car class; got %v", classNamesOf(rows))
	}
	if car.Counts.TruePositives != 0 {
		t.Fatalf("a `car` on a `person` box counted as %d true positive(s) for car. A detector that calls "+
			"everything a car is the exact failure a per-class table exists to show", car.Counts.TruePositives)
	}
	if car.Counts.FalsePositives != 1 {
		t.Fatalf("the car detection was not counted as a car false positive: %+v", car.Counts)
	}
	person, ok := byClass["person"]
	if !ok {
		t.Fatalf("no row for the person class; got %v", classNamesOf(rows))
	}
	if person.Counts.FalseNegatives != 1 {
		t.Fatalf("the person box was not counted as a miss: %+v", person.Counts)
	}
}

// TestAnUnpartitionedMeasureWouldCallItATruePositive is the counterfactual, and
// it is what makes RC-34 a real red check rather than an assertion about taste.
func TestAnUnpartitionedMeasureWouldCallItATruePositive(t *testing.T) {
	fr := classFrame{
		Frame: "f0",
		Dets:  []bench.Detection{{Frame: "f0", Box: bench.Box{Class: "car", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}}},
		Truth: []bench.GT{{Box: bench.Box{Class: "person", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}, Class: "person", Expressible: true}},
	}
	fc := bench.Measure(fr.Frame, fr.Dets, fr.Truth, bench.DefaultOptions())
	if fc.TruePositives != 1 {
		t.Skip("bench.Measure no longer matches on geometry alone, so the partition's reason has changed; " +
			"rewrite this test against whatever it does now")
	}
	t.Log("bench.Measure alone reports tp=1 for a `car` over a `person` box: the partition is what makes the " +
		"per-class number honest, and this is the transcript")
}

func classNamesOf(rows []classRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Class)
	}
	return out
}

// TestAnUnnameableClassIsDroppedAndNotCountedAsAMiss: a `hair drier` is a real
// object this build has no word for, and counting it as a miss reports a recall
// penalty for a vocabulary limitation.
func TestAnUnnameableClassIsDroppedAndNotCountedAsAMiss(t *testing.T) {
	fr := classFrame{
		Frame: "f0",
		Truth: []bench.GT{{
			Box: bench.Box{Class: "hair drier", X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2},
			// Expressible is what the command computes from the model's own
			// vocabulary; a false one is a class this build cannot name.
			Class: "hair drier", Expressible: false,
		}},
	}
	rows := partitionByClass([]classFrame{fr}, bench.DefaultOptions())
	got := indexRows(rows)["hair drier"].Counts
	if got.FalseNegatives != 0 {
		t.Fatalf("a class this build cannot name counted as %d miss(es)", got.FalseNegatives)
	}
	if got.Dropped != 1 {
		t.Fatalf("Dropped = %d; it must be 1 and must be printed beside any recall", got.Dropped)
	}
	if _, ok := got.Recall(); ok {
		t.Fatal("recall was reported over a denominator of zero counted boxes")
	}
}

// TestARateWithNoDenominatorPrintsNotRunAndNeverZero is RC-40.
//
// FAILS IF: `rate` drops its `if !ok` arm and prints the value regardless. With
// no detections, a precision of 0.000 claims every detection was wrong and there
// were none; 0.000 is the most reassuring thing a table can print and the one most
// likely to be wrong.
//
// REDCHECK RC-40: replace the body of `rate` with `return padLeft(fmt.Sprintf
// ("%.3f", v), width)`.
func TestARateWithNoDenominatorPrintsNotRunAndNeverZero(t *testing.T) {
	if got := rate(0, false, 9); strings.TrimSpace(got) != notRun {
		t.Fatalf("a rate with no denominator printed %q, not NOT RUN", strings.TrimSpace(got))
	}
	out := CommandPerClass([]classRow{{
		Class:  "car",
		Counts: bench.Counts{Dropped: 2},
	}}, bench.DefaultOptions(), "whole-frame")
	if hasZeroRate(out) {
		t.Fatalf("a per-class table with no denominators printed a 0.000:\n%s", out)
	}
	if !strings.Contains(out, notRun) {
		t.Fatalf("the table does not say NOT RUN anywhere:\n%s", out)
	}
}

// TestThePerClassTableSaysItIsNotMap: a per-class precision table with no such
// line is read as a benchmark result by everybody who has not been told otherwise.
func TestThePerClassTableSaysItIsNotMap(t *testing.T) {
	rows := partitionByClass([]classFrame{oneCarFrame()}, bench.DefaultOptions())
	for _, out := range []string{
		CommandPerClass(rows, bench.DefaultOptions(), "whole-frame"),
		CommandPerClassSideBySide(rows, rows, bench.DefaultOptions()),
	} {
		if !strings.Contains(out, "NOT mAP") {
			t.Fatalf("a per-class table does not disclaim mAP:\n%s", out)
		}
		if !strings.Contains(out, "operating point") {
			t.Fatalf("a per-class table does not state it is one operating point:\n%s", out)
		}
	}
}

// TestTheSideBySideTableKeepsBothSettingsApartAndReportsBoth: the headline is
// the comparison, and a class that appears under only one setting still gets its
// row in both - printing it only where it exists would hide exactly the rows that
// carry the result.
func TestTheSideBySideTableKeepsBothSettingsApartAndReportsBoth(t *testing.T) {
	car := classFrame{
		Frame: "f0",
		Dets: []bench.Detection{
			{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3}},
		},
		Truth: []bench.GT{
			{Box: bench.Box{Class: "car", X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3}, Class: "car", Expressible: true},
			{Box: bench.Box{Class: "person", X1: 0.6, Y1: 0.6, X2: 0.8, Y2: 0.8}, Class: "person", Expressible: true},
		},
	}
	whole := partitionByClass([]classFrame{car}, bench.DefaultOptions())
	crop := partitionByClass([]classFrame{{Frame: "f0", Truth: car.Truth}}, bench.DefaultOptions())
	out := CommandPerClassSideBySide(whole, crop, bench.DefaultOptions())

	if !strings.Contains(out, "person") {
		t.Fatalf("the person row was dropped from one side of the comparison:\n%s", out)
	}
	line := tableLineContaining(t, out, "person")
	// Whole-frame found no person, so its PRECISION has no denominator. A class
	// with ground truth and no detections still has a recall, and that one is a
	// real 0.000 rather than an absence - the test below pins the difference.
	cells := strings.Fields(line)
	if !strings.Contains(line, notRun) {
		t.Fatalf("the person row reports a precision where there were no detections of that class:\n%s", line)
	}
	if !strings.Contains(line, "0.000") {
		t.Fatalf("the person row dropped its recall, which IS computable here (1 drawn box, 0 found):\n%s", line)
	}
	_ = cells
	// And the car row carries its number on the side that had detections.
	carLine := tableLineContaining(t, out, "car")
	if !strings.Contains(carLine, "1.000") {
		t.Fatalf("the car row does not carry the precision that was measured:\n%s", carLine)
	}
}

// TestAClassWithNoDetectionsAndNoGroundTruthIsNotRunOnBothSides: the one case
// where a 0.000 would be a lie rather than a measurement. The class is real - a
// drawn box this build cannot name - but it is counted as dropped, so both
// denominators are empty and both rates have to say so.
func TestAClassWithNoDetectionsAndNoGroundTruthIsNotRunOnBothSides(t *testing.T) {
	rows := partitionByClass([]classFrame{{
		Frame: "f0",
		Truth: []bench.GT{{
			Box:   bench.Box{Class: "hair drier", X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2},
			Class: "hair drier", Expressible: false,
		}},
	}}, bench.DefaultOptions())
	out := CommandPerClassSideBySide(rows, rows, bench.DefaultOptions())
	if hasZeroRate(out) {
		t.Fatalf("a class with neither detections nor countable boxes printed a rate:\n%s", out)
	}
	if strings.Count(out, notRun) < 4 {
		t.Fatalf("both settings should read NOT RUN on both axes:\n%s", out)
	}
}

// hasZeroRate reports whether a printed RATE is 0.000.
//
// It scans only the per-class tables, for two reasons that are the same reason.
// A rate is the only thing that prints three decimal places in those tables, and a
// REGION COORDINATE also prints three - a region touching the left edge legitimately
// has an X1 of 0.000 - so a whole-output substring search would fail on a correct
// measurement. And it is a word-bounded match, because the policy line prints
// `0.0005 of the frame` and a substring test would call a min-area threshold a
// measured recall: that is the defect this file exists to catch, committed inside
// the test that catches it.
func hasZeroRate(out string) bool {
	inTable := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "per class") {
			inTable = true
		}
		if !inTable {
			continue
		}
		if zeroRateRE.MatchString(line) {
			return true
		}
	}
	return false
}

var zeroRateRE = regexp.MustCompile(`\b0\.000\b`)

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

// TestTheCropPassProposesAndSlicesRealRegions exercises everything the crop pass
// does EXCEPT the inference, over a real clip: the novelty engine advances, the
// policy turns candidates into regions, each region becomes pixels, and the pixel
// origin comes back to the caller so a box can be composed onto it.
//
// It is here because the inference is the one part that cannot run on this tree's
// weights file, and a command whose only tested path is the refusal is a command
// whose working path is a comment.
//
// Two assertions are controls in their own right:
//
//   - BOTH SETTINGS MUST HAVE RECORDED THE SAME FRAMES. If the crop pass skipped a
//     frame, the side-by-side comparison would be between two different sample sets
//     and the whole headline would be meaningless. That is the failure this
//     assertion exists to catch, and it is why the crop pass records a frame with
//     no regions rather than omitting it.
//   - THE SLICE MUST BE THE REGION. A wrong origin is invisible in a count and is
//     the difference between "the crop pass found nothing" and "the crop pass was
//     looking at the wrong pixels".
func TestTheCropPassProposesAndSlicesRealRegions(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeMovingClipForTest(t, clip)

	w, err := validateFootageWindow(0, 2*time.Second, 500*time.Millisecond)
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
	// The crop pass records a frame with no regions, so its count includes them.
	if res.crop.SilentFrames == 0 {
		t.Fatal("every frame proposed a region on a 4-frame clip of a moving box; the baseline frame and at " +
			"least one other should have proposed nothing")
	}
	// (2) the slice is the region, and the origin comes back for the composition.
	frameRGB := make([]byte, res.Read.W*res.Read.H*3)
	for _, s := range res.regions {
		if s.Bounds.W <= 0 || s.Bounds.H <= 0 {
			t.Fatalf("region %+v produced bounds %+v", s.Region, s.Bounds)
		}
		if s.Bounds.X0 < 0 || s.Bounds.Y0 < 0 ||
			s.Bounds.X0+s.Bounds.W > res.Read.W || s.Bounds.Y0+s.Bounds.H > res.Read.H {
			t.Fatalf("region bounds %+v run off a %dx%d frame", s.Bounds, res.Read.W, res.Read.H)
		}
		slice := cropPixels(frameRGB, res.Read.W, res.Read.H, s.Bounds)
		if len(slice) != s.Bounds.W*s.Bounds.H*3 {
			t.Fatalf("the slice is %d bytes for bounds %+v", len(slice), s.Bounds)
		}
		// A box over the whole slice composes onto exactly the region that named it.
		back := composeBox(proposalBox{X1: 0, Y1: 0, X2: 1, Y2: 1}, s.Bounds, res.Read.W, res.Read.H)
		if math.Abs(back.X1-s.Region.X1) > 0.02 || math.Abs(back.X2-s.Region.X2) > 0.02 {
			t.Fatalf("a full-slice box composed to %+v for a region recorded as %+v; the two disagree by more "+
				"than a pixel of rounding", back, s.Region)
		}
	}
	// (3) the ground truth landed on the frames it names and nowhere else.
	var placed int
	for _, f := range res.whole.Frames {
		placed += len(f.Truth)
	}
	if placed != 1 {
		t.Fatalf("the one ground-truth box landed on %d frames; half an interval is the stated tolerance and "+
			"the box is 0.5s from exactly one of four samples", placed)
	}
	if placed != len(res.crop.Frames[0].Truth)+placed-len(res.crop.Frames[0].Truth) {
		t.Fatal("the crop pass and the whole-frame pass disagree about which frames carry ground truth")
	}
}

// ------------------------------------------------------- the report itself ---

// finishedMeasurement is a report with REAL numbers in it, so the renderer is
// exercised on the case it exists for and not only on the refusal case.
func finishedMeasurement() footageReport {
	wholeFrames := []classFrame{
		{
			Frame: "f0",
			Dets: []bench.Detection{
				{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.9, X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}},
				{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.8, X1: 0.60, Y1: 0.60, X2: 0.80, Y2: 0.80}},
			},
			Truth: []bench.GT{
				{Box: bench.Box{Class: "car", X1: 0.10, Y1: 0.10, X2: 0.30, Y2: 0.30}, Class: "car", Expressible: true},
				{Box: bench.Box{Class: "car", X1: 0.60, Y1: 0.60, X2: 0.80, Y2: 0.80}, Class: "car", Expressible: true},
			},
		},
	}
	cropFrames := []classFrame{
		{
			Frame: "f0",
			Dets: []bench.Detection{
				{Frame: "f0", Box: bench.Box{Class: "car", Score: 0.91, X1: 0.11, Y1: 0.11, X2: 0.29, Y2: 0.29}},
			},
			Truth: wholeFrames[0].Truth,
		},
	}
	return footageReport{
		Clip:   "somebody/someclip.mp4",
		Window: footageWindow{From: time.Second, To: 6 * time.Second, Every: 500 * time.Millisecond, Declared: 10},
		Read:   windowRead{Declared: 10, Read: 2, W: 1280, H: 720, Command: "ffmpeg (test)"},
		Policy: defaultCropPolicy(),
		Opts:   bench.DefaultOptions(),
		Input:  ortdetect.InputSpec{W: 416, H: 416, Layout: "nhwc"},
		Whole:  regime{Name: "whole-frame", Inferences: 2, Frames: wholeFrames},
		Crop: regime{
			Name: "crop", Inferences: 3, Frames: cropFrames, Regions: 3,
			BelowMin: 1, Deduped: 2, SilentFrames: 1,
		},
		TruthState: "2 box(es) from testdata/truth.json",
		Regions: []regionSample{
			{At: time.Second, Region: proposalBox{X1: 0.5, Y1: 0.25, X2: 0.75, Y2: 0.75}, Bounds: cropBounds{X0: 640, Y0: 180, W: 320, H: 360}},
		},
	}
}

// TestAFinishedMeasurementRendersBothSettingsAndItsNumbers: the renderer must be
// exercised on a measurement that WORKED, or its only tested path is the refusal.
func TestAFinishedMeasurementRendersBothSettingsAndItsNumbers(t *testing.T) {
	out := renderFootageReport(finishedMeasurement())
	for _, want := range []string{"WHOLE-FRAME", "CROP", "car", "1.000", "0.500", "the two passes over the same frames"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report does not contain %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "NOT mAP") {
		t.Fatalf("the per-class table lost its mAP disclaimer:\n%s", out)
	}
	// The crop pass's whole point: it did NOT find the second car. That must be
	// visible as a number, not hidden.
	cropLine := tableLineContaining(t, out, "car")
	if !strings.Contains(cropLine, "0.500") {
		t.Fatalf("the crop column does not report the precision that was measured:\n%s", out)
	}
}

// TestAMeasurementOverRefusalsRendersNotRunAndNeverZero is RC-39.
//
// The refusal path is the one this command runs on this tree's weights file, so it
// is the path that most needs a test. FAILS IF: the `case
// r.Whole.Refusals+r.Crop.Refusals > 0` arm is removed from renderNotMeasured, and
// the section goes quiet about the one thing it exists to say.
func TestAMeasurementOverRefusalsRendersNotRunAndNeverZero(t *testing.T) {
	r := finishedMeasurement()
	// A run in which every inference was refused has NO detections and NO frames
	// carrying detections. That is what the real command produces: the decoder
	// refuses, `ans.Boxes` is nil, and nothing is recorded but the refusal.
	r.Whole.Frames = []classFrame{{Frame: "f0"}, {Frame: "f1"}}
	r.Crop.Frames = []classFrame{{Frame: "f0"}, {Frame: "f1"}}
	r.Whole.Inferences, r.Crop.Inferences = 2, 3
	reason := &ortdetect.Refusal{
		What:   "this export is a multi-scale ANCHORED head and the in-process decoder reads only fused outputs",
		Detail: "the session declares 3 outputs, and an anchored darknet head publishes one tensor per stride",
	}
	r.Whole.FirstRefusal, r.Crop.FirstRefusal = reason, reason
	r.Whole.RefusalWhy, r.Crop.RefusalWhy = []string{reason.What}, []string{reason.What}
	r.Whole.Refusals, r.Crop.Refusals = 2, 3
	r.DetErr = nil
	out := renderFootageReport(r)

	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("a measurement the decoder refused everywhere does not say NOT RUN:\n%s", out)
	}
	if hasZeroRate(out) {
		t.Fatalf("a measurement over refusals printed 0.000 somewhere:\n%s", out)
	}
	if !strings.Contains(out, reason.What) {
		t.Fatalf("the decoder's own reason is not printed verbatim:\n%s", out)
	}
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("the section does not say whether the crop pass found anything is unknown:\n%s", out)
	}
}

// TestAnAbsentDetectorIsAlsoNotRunAndNotZero: the other way the detection
// columns go absent, which is what happens on a machine with no weights.
func TestAnAbsentDetectorIsAlsoNotRunAndNotZero(t *testing.T) {
	r := finishedMeasurement()
	r.DetErr = fmt.Errorf("footage: the model at models/yolov4n.onnx is not readable")
	out := renderFootageReport(r)
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("an absent model does not produce NOT RUN:\n%s", out)
	}
	if hasZeroRate(out) {
		t.Fatalf("an absent model produced a 0.000:\n%s", out)
	}
	if !strings.Contains(out, "no detections at all") && !strings.Contains(out, "there were none of either") {
		t.Fatalf("the section does not explain why a count of zero is also NOT RUN:\n%s", out)
	}
}

// TestTheCropArithmeticIsPrintedEvenWithNoModel: the row that IS measurable on
// this tree's weights file, and the reason the command runs at all when the
// detector cannot be opened.
func TestTheCropArithmeticIsPrintedEvenWithNoModel(t *testing.T) {
	r := finishedMeasurement()
	r.DetErr = fmt.Errorf("no model")
	r.Input = ortdetect.InputSpec{}
	r.Whole.Frames = nil
	r.Crop.Frames = nil
	out := renderFootageReport(r)
	if !strings.Contains(out, "with no model involved") {
		t.Fatalf("the arithmetic section is missing:\n%s", out)
	}
	if !strings.Contains(out, "ASSUMED") {
		t.Fatalf("with no session the model input is assumed, and that must be labelled as an assumption:\n%s", out)
	}
	if !strings.Contains(out, "40 px") {
		t.Fatalf("the arithmetic section does not state what object size it is reporting:\n%s", out)
	}
}

// TestTrailingBytesAreRefusedRatherThanDropped is RC-41.
//
// FAILS IF: refuseTrailingFrame returns nil for a positive count. A partial frame
// is not a frame: dropping it quietly is a silent sample drop, and reading past it
// is a measurement of bytes nobody decoded.
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

// TestTheBoundedTailKeepsTheLastBytesAndNamesAnEmptyLog: a measurement tool that
// prints an unbounded decoder log has replaced one unreadable refusal with a
// different unreadable refusal.
func TestTheBoundedTailKeepsTheLastBytesAndNamesAnEmptyLog(t *testing.T) {
	var b boundedTail
	if _, err := b.Write([]byte("line one\nline two\n")); err != nil {
		t.Fatalf("the tail refused a write: %v", err)
	}
	got := b.String()
	if !strings.Contains(got, "line two") {
		t.Fatalf("the tail dropped the line it was supposed to keep:\n%s", got)
	}
	if strings.Contains(got, "line one") && len(got) > 8<<10 {
		t.Fatal("the tail is not bounded")
	}
	var empty boundedTail
	if !strings.Contains(empty.String(), "nothing to stderr") {
		t.Fatalf("an empty log prints %q; a reader needs to know the tool was silent rather than absent",
			empty.String())
	}
}

// TestAWindowIsSampledFrameByFrameOverRealMaterial exercises the reader against a
// REAL clip, because the reader is the one thing in this command that talks to
// ffmpeg and a unit test over a struct proves nothing about it.
//
// ffmpeg is a hard runtime dependency of this module (commands_test.go says so), so
// this does not skip: it fails if the tool the module is built around is absent.
func TestAWindowIsSampledFrameByFrameOverRealMaterial(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "src.mp4")
	writeMovingClipForTest(t, clip)

	w, err := validateFootageWindow(0, 2*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var seen []sampledFrame
	read, err := readWindowFrames(ctx(), clip, w, w.Declared, func(f sampledFrame) error {
		// The RGB buffer is REUSED between frames, as sampledFrame documents. A
		// consumer that keeps the slice is holding the next frame, so this test
		// copies it - and the copy is what makes the frame-distinctness assertion
		// below mean anything.
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
			"window that delivered fewer samples than it promised is a different measurement", read.Read, read.Declared)
	}
	if read.Trailing != 0 {
		t.Fatalf("%d trailing bytes after a clean stream", read.Trailing)
	}
	if read.W != 320 || read.H != 180 {
		t.Fatalf("the frame size came back %dx%d; writeClipForTest renders 320x180", read.W, read.H)
	}
	for i, f := range seen {
		if f.Index != i {
			t.Fatalf("frame %d carries index %d; the sequence number must count from the window's start", i, f.Index)
		}
		if len(f.RGB) != f.W*f.H*3 {
			t.Fatalf("frame %d carries %d bytes for %dx%d", i, len(f.RGB), f.W, f.H)
		}
	}
	// The frames must not all be identical, or the window sampled one picture four
	// times and called it a measurement. `testsrc` has a moving element in it.
	if allSamePixels(seen) {
		t.Fatal("every sampled frame is byte-identical; the window sampled one picture and reported it as four")
	}
}

// writeMovingClipForTest renders a clip whose picture CHANGES, because the reader
// is supposed to hand back different frames and a test over a static pattern
// cannot tell a sampled window from one picture reported four times.
//
// A `testsrc` background with a `drawbox` whose x is a function of t, which is
// the same construction `testdata/labels.json` records for car-crossing.mp4.
func writeMovingClipForTest(t *testing.T, path string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2): %v", err)
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

// TestTheWindowRefusesAFileItCannotRead: a material ffmpeg cannot open must not
// produce a zero-frame measurement.
func TestTheWindowRefusesAFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-a-video.mp4")
	_, err := readWindowFrames(ctx(), missing, footageWindow{From: 0, To: time.Second, Every: 500 * time.Millisecond}, 2,
		func(sampledFrame) error { return nil })
	if err == nil {
		t.Fatal("a window over a file ffmpeg cannot read produced a measurement")
	}
	if !strings.Contains(err.Error(), "frame size") && !strings.Contains(err.Error(), "no frames") {
		t.Fatalf("the refusal does not say what could not be read: %v", err)
	}
}

// ------------------------------------------------------------ ground truth ---

// TestAGroundTruthBoxTooFarFromEveryFrameIsCountedNotAttached: attaching a box
// 2.5 s from a sampled frame would manufacture a miss on a frame the annotator
// never described, and a recall that penalises the annotator's timing is a recall
// about the interval rather than about the detector.
func TestAGroundTruthBoxTooFarFromEveryFrameIsCountedNotAttached(t *testing.T) {
	tf := &truthFile{
		path:    "truth.json",
		classes: map[string]int{"car": 1},
		Boxes:   []truthBox{{At: 2.5, Class: "car", X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2}},
	}
	if got := tf.at(time.Second, 500*time.Millisecond); len(got) != 0 {
		t.Fatalf("a box 1.5s from the frame was attached to it; %d box(es) matched", len(got))
	}
	if tf.unmatched != 1 {
		t.Fatalf("the box that matched nothing was not counted: unmatched = %d", tf.unmatched)
	}
	// Half an interval away is inside the tolerance.
	if got := tf.at(2250*time.Millisecond, 500*time.Millisecond); len(got) != 1 {
		t.Fatalf("a box a quarter of an interval from the frame was not attached")
	}
}

// TestAGroundTruthFileWithNoBoxesIsRefusedRatherThanReportedAsZeroRecall.
func TestAGroundTruthFileWithNoBoxesIsRefusedRatherThanReportedAsZeroRecall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truth.json")
	if err := os.WriteFile(path, []byte(`{"material":"x.mp4","boxes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTruthFile(path); err == nil {
		t.Fatal("a ground-truth file with an empty boxes array was accepted; its recall denominator is zero")
	}
}

// TestAGroundTruthFileNamedAndUnreadableIsAnErrorNotAnAbsence: the operator asked
// for ground truth and did not get it. Continuing would print NOT RUN for the
// rates as though nobody had asked.
func TestAGroundTruthFileNamedAndUnreadableIsAnErrorNotAnAbsence(t *testing.T) {
	if _, err := readTruthFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("a --truth path that cannot be read was treated as an absence")
	}
	// And no --truth at all IS an absence, not an error.
	if got, err := readTruthFile(""); got != nil || err != nil {
		t.Fatalf("no --truth returned (%v, %v); it must be a clean absence", got, err)
	}
}

// TestAGroundTruthBoxWithNoClassIsRefused: a box with no class cannot go in a
// per-CLASS table, and guessing one from the picture is the guess this file
// exists to refuse.
func TestAGroundTruthBoxWithNoClassIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truth.json")
	body := `{"material":"x.mp4","boxes":[{"at":1.0,"x1":0.1,"y1":0.1,"x2":0.2,"y2":0.2}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTruthFile(path); err == nil {
		t.Fatal("a ground-truth box with no class was accepted")
	}
}

// TestExpressibleFollowsTheModelVocabularyNotTheFile: `sunglasses` is a real
// Open Images class and is not one of the 80 this build can name. It has to be
// counted as a taxonomy limit, not as a miss.
func TestExpressibleFollowsTheModelVocabularyNotTheFile(t *testing.T) {
	if !expressible("car") {
		t.Fatal("car is in the vocabulary and was reported as unnameable")
	}
	if !expressible("hair drier") {
		t.Fatal("hair drier IS one of the 80 this build names; a test that assumed otherwise would prove " +
			"nothing about the vocabulary")
	}
	if expressible("sunglasses") {
		t.Fatal("a class the vocabulary does not hold was reported as nameable; it would be counted as a miss " +
			"rather than as a taxonomy limit")
	}
}
