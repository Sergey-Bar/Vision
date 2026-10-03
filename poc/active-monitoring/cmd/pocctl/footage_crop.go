package main

// footage_crop.go - the CROP PASS: which regions are measured on their own, and the
// arithmetic that says what becomes of an object on its way to the model.
//
// # The geometry is NOT here, and that is the point
//
// The crop rectangle, the slice, and the mapping of a box found inside a crop back
// onto the frame are `ortdetect.Rect` and its three methods - `Clamp`, `Extract` and
// `Uncrop`. They are in `internal/ortdetect` because three numbers have to agree
// (letterbox the CROP, decode against the CROP, map back to the FRAME) and the
// failure when they do not is silent and total: every box lands in the wrong place
// on the picture at plausible coordinates with no error anywhere.
//
// This branch wrote its own `cropBoundsOf`, `cropPixels` and `composeBox` before
// `internal/ortdetect/crop.go` existed. They have been DELETED and every call site
// now goes through `Rect`. Two implementations of a crop-to-frame mapping is how a
// benchmark reports a recall the picture does not support, and this repository has
// shipped that class of defect twice.
//
// `TestTheCropGeometryIsNotReimplementedHere` is what holds that: it counts
// definition sites in this package and fails on a second spelling.
//
// # The POLICY is here, and why it could not move
//
// `internal/soak/crop.go` carries the same policy - coveredByAny, unionBoxes,
// cropDedupeIOU, the overlap gate, the cap - and it is the version that runs in a
// soak. Its entry point is `proposeCrops(f frameSource, ...)`, and `frameSource` is
// UNEXPORTED, so a `main` package cannot call it and there is no exported wrapper.
// `CropPass` is exported but only its configuration and counters are, not the
// function that turns a frame's novelty candidates into rectangles.
//
// So this is a genuine second implementation and it is labelled as one rather than
// described as a copy. It is not a near-duplicate: the soak policy also gates
// against the whole-frame detections, applies a novelty rate gate and a camera-view
// arbitration, none of which belong in a measurement that is comparing two passes
// over the same frames. What it shares is the pad, the area floor, the dedupe
// threshold and the containment test, and those four are the part that must not
// drift. The fix is to export `proposeCrops`'s policy - `soak.CropPolicy` with a
// `Propose` method taking exported types - and this file's four functions deleted
// with it. That is a change to `internal/soak`, which this work may not make.
//
// # What the crop pass is actually measuring
//
// The whole-frame pass hands a 1280x720 frame to a model declared at 416x416.
// [ortdetect.Letterbox] scales it by min(416/1280, 416/720) = 0.325, so an object
// 40 px wide in the frame arrives at the model 13 px wide. The same object inside a
// 200x150 crop arrives at 40 * 416/200 = 83 px. The factor is 6.4 on this material
// and 9.6 on a 1920x1080 frame, which is the figure the plan quotes.
//
// That is arithmetic and `pocctl detector --selftest` reports it with no footage at
// all, which is exactly what makes it worth little on its own: it says what WOULD
// happen to a 40-pixel object, and not whether any object is found. This file is
// where the arithmetic is spent on real pixels, and the honest result may well be
// that the crop pass finds nothing the whole-frame pass missed. That result prints.

import (
	"fmt"
	"math"
	"sort"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/proposal"
)

// proposalBox is the module's box vocabulary, named once here so the policy's
// signature does not carry an import that changes the arithmetic.
type proposalBox = proposal.Box

// cropPolicy is the arithmetic the crop pass is run under, declared once.
//
// Every field moves the numbers, so every field is printed beside them. A per-class
// table with no pad and no dedupe threshold beside it is a table whose reader has no
// way to reproduce it.
//
// The defaults are the ones `internal/soak` uses for the same fields, and
// `TestTheCropPolicyDefaultsAgreeWithSoak` is what keeps them honest where soak
// exports a value to compare against.
type cropPolicy struct {
	// MinArea is the smallest region worth measuring on its own, as a fraction of the
	// frame. It is SP_POC_MIN_BOX_AREA's unit for the same reason: a region smaller than
	// this is compression speckle, and running the model on it costs an inference to
	// learn nothing.
	MinArea float64
	// Pad is how much of a region's OWN size is added on each side before it is
	// clamped to the frame. A measured component is the pixels that changed, and the
	// object is bigger than the pixels that changed.
	Pad float64
	// DedupeIOU is the IoU at which one candidate region is treated as a restatement of
	// another. Measuring the same object twice is how a crop pass inflates its own
	// recall: two identical detections of one object are one true positive and one
	// false positive, and a table that counted the pair as two true positives would
	// report a crop pass that finds everything. It is the same 0.5 the soak crop pass
	// uses, for the same reason.
	DedupeIOU float64
	// CoveredFraction is how much of a candidate must already lie inside the accepted
	// regions before it is dropped as adding nothing.
	CoveredFraction float64
	// Max is how many regions one frame may contribute. It bounds the inferences a
	// frame costs, and it is reported rather than applied silently.
	Max int
}

// defaultCropPolicy returns the measured defaults.
func defaultCropPolicy() cropPolicy {
	return cropPolicy{
		// SP_POC_MIN_BOX_AREA's own default. Reused rather than re-picked so the crop
		// pass and the pipeline agree about what counts as an object.
		MinArea:         0.0005,
		Pad:             0.25,
		DedupeIOU:       0.5,
		CoveredFraction: 0.95,
		Max:             8,
	}
}

// Validate refuses a policy whose arithmetic nobody could interpret.
func (p cropPolicy) Validate() error {
	if p.MinArea <= 0 || p.MinArea >= 1 {
		return fmt.Errorf("footage: the crop minimum area is %g; it is a fraction of the frame and must be in (0,1)",
			p.MinArea)
	}
	if p.Pad < 0 {
		return fmt.Errorf("footage: the crop pad is %g; a negative pad shrinks the region it was meant to enlarge", p.Pad)
	}
	if p.DedupeIOU <= 0 || p.DedupeIOU >= 1 {
		return fmt.Errorf("footage: the crop dedupe IoU is %g; it must be in (0,1), because 0 would drop every "+
			"region against every other and 1 would drop none", p.DedupeIOU)
	}
	if p.CoveredFraction <= 0 || p.CoveredFraction > 1 {
		return fmt.Errorf("footage: the covered fraction is %g; it must be in (0,1]", p.CoveredFraction)
	}
	if p.Max < 1 {
		return fmt.Errorf("footage: at most %d regions may be measured per frame; a cap of zero measures no crop "+
			"at all and would report the crop pass as finding nothing", p.Max)
	}
	return nil
}

// padThenClamp is the ORDER, and the order is the control.
//
// A region is PADDED first and CLAMPED second. Doing it the other way round is not a
// different spelling, it is a different region: clamping an edge region first throws
// away the part of the pad that fell outside the frame, and padding afterwards puts it
// back outside the frame, where `Rect.Extract` has to ignore it. The recorded region
// would then be wider than the pixels actually measured, and every box mapped back
// onto the frame would be placed against a crop that is not the crop the model saw.
//
// The clamp itself is `Rect.Clamp`, and this function exists only to fix the ORDER
// in which the pad and the clamp happen.
//
// REDCHECK RC-31: swap the two steps below and
// TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame goes red.
func padThenClamp(b proposal.Box, pad float64, frameW, frameH int) (ortdetect.Rect, bool) {
	dx := b.Width() * pad
	dy := b.Height() * pad
	padded := proposal.Box{X1: b.X1 - dx, Y1: b.Y1 - dy, X2: b.X2 + dx, Y2: b.Y2 + dy}
	x0 := clampInt(int(math.Round(padded.X1*float64(frameW))), 0, frameW)
	y0 := clampInt(int(math.Round(padded.Y1*float64(frameH))), 0, frameH)
	x1 := clampInt(int(math.Round(padded.X2*float64(frameW))), 0, frameW)
	y1 := clampInt(int(math.Round(padded.Y2*float64(frameH))), 0, frameH)
	r := ortdetect.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	// A pad that leaves nothing is not a region. `Rect.Empty` is the whole-frame
	// spelling and the two must not be confused: an empty Rect passed to Extract is
	// the WHOLE FRAME, which would spend a full inference on a region nobody chose and
	// record it as a measurement of that region.
	if r.Empty() {
		return ortdetect.Rect{}, false
	}
	return r, true
}

// cropDedupeIOU reports whether two candidate regions are the same region.
//
// It is [bench.IoU] rather than a second IoU, and that is the point: `bench` is the
// module's one matching arithmetic, and a crop pass that deduplicated with its own
// would be the first place a measurement compared two numbers by two rules. The
// threshold is the policy's, not bench's, because deduplicating two regions and
// matching two detections are different questions.
func cropDedupeIOU(a, b ortdetect.Rect, threshold float64) bool {
	// Pixel units on both sides, which is what IoU needs: it is a ratio of areas and
	// is invariant to the unit as long as BOTH boxes are in the same one. Converting
	// to normalised fractions here would be a second rounding of the same rectangle.
	return bench.IoU(rectBox(a), rectBox(b)) >= threshold
}

// coveredByAny reports whether REGION adds nothing to what has already been accepted.
//
// CONTAINMENT and not IoU, because the two boxes are not alternatives: a small region
// inside a large accepted region has a low IoU against it and is still entirely
// redundant. The question asked is what fraction of REGION's own area is already
// inside the union, so the comparison is between the candidate and itself.
//
// A region with no area is reported as covered. A region with no area has nothing to
// add and must not be handed to a detector, which will letterbox it into a
// full-frame-shaped tensor and report whatever the padding produced.
//
// REDCHECK RC-32: delete the `if coveredByAny` drop in cropRegions and a frame with
// three overlapping components measures the same object three times.
// TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain.
func coveredByAny(region ortdetect.Rect, accepted []ortdetect.Rect, fraction float64) bool {
	if region.Empty() {
		return true
	}
	if len(accepted) == 0 {
		return false
	}
	u := unionRects(accepted)
	inter := overlapArea(rectBox(region), rectBox(u))
	own := float64(region.Area())
	if own <= 0 {
		return true
	}
	return inter/own >= fraction
}

// unionRects merges rectangles into the one that contains all of them.
func unionRects(rects []ortdetect.Rect) ortdetect.Rect {
	if len(rects) == 0 {
		return ortdetect.Rect{}
	}
	x0, y0, x1, y1 := rects[0].X, rects[0].Y, rects[0].X+rects[0].W, rects[0].Y+rects[0].H
	for _, r := range rects[1:] {
		x0, y0 = minInt2(x0, r.X), minInt2(y0, r.Y)
		x1, y1 = maxInt2(x1, r.X+r.W), maxInt2(y1, r.Y+r.H)
	}
	return ortdetect.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// rectBox expresses a rectangle as the module's normalised box, which is what the
// coverage arithmetic compares. It is a view and not a crop: no pixels are touched.
func rectBox(r ortdetect.Rect) proposal.Box {
	return proposal.Box{X1: float64(r.X), Y1: float64(r.Y),
		X2: float64(r.X + r.W), Y2: float64(r.Y + r.H)}
}

// overlapArea is the area two boxes share.
func overlapArea(a, b proposal.Box) float64 {
	w := minF(a.X2, b.X2) - maxF(a.X1, b.X1)
	h := minF(a.Y2, b.Y2) - maxF(a.Y1, b.Y1)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// cropPlan is what the policy decided for one frame.
type cropPlan struct {
	Regions []ortdetect.Rect
	// BelowMin is how many candidates the area floor removed.
	BelowMin int
	// Dedupe is how many candidates a dedupe or coverage rule removed.
	Dedupe int
	// Candidates is how many the novelty engine proposed, before any policy.
	Candidates int
}

// cropRegions turns novelty candidates into the rectangles the detector will see.
//
// The order is fixed and each step has a reason:
//
//	area floor -> pad -> clamp -> sort by area, largest first -> dedupe -> coverage -> cap
//
// Sorting by area before the dedupe is what makes the surviving set stable: two
// nearly-equal components are resolved in favour of the one that encloses more, and a
// frame's regions therefore do not depend on the order the connected components
// happened to be emitted in.
//
// The cap is applied LAST, so a frame's best regions survive rather than the first few
// the engine proposed.
//
// Note what is NOT here, and it is a difference from `internal/soak` rather than an
// omission: there is no overlap gate against the whole-frame pass. A measurement
// comparing two passes must not suppress the crops the whole-frame pass already
// covered, because the question is precisely whether those crops find anything extra.
func cropRegions(cands []novelty.Candidate, p cropPolicy, frameW, frameH int) cropPlan {
	plan := cropPlan{Candidates: len(cands)}
	var kept []ortdetect.Rect
	for _, c := range cands {
		if c.Box.Area() < p.MinArea {
			plan.BelowMin++
			continue
		}
		r, ok := padThenClamp(c.Box, p.Pad, frameW, frameH)
		if !ok {
			plan.Dedupe++
			continue
		}
		kept = append(kept, r)
	}
	// Largest first, ties broken by position so two frames of the same picture produce
	// the same plan.
	sort.SliceStable(kept, func(i, j int) bool {
		ai, aj := kept[i].Area(), kept[j].Area()
		if ai != aj {
			return ai > aj
		}
		if kept[i].X != kept[j].X {
			return kept[i].X < kept[j].X
		}
		return kept[i].Y < kept[j].Y
	})

	var accepted []ortdetect.Rect
	for _, r := range kept {
		if len(accepted) >= p.Max {
			plan.Dedupe++
			continue
		}
		if anyDuplicate(r, accepted, p.DedupeIOU) {
			plan.Dedupe++
			continue
		}
		// REDCHECK RC-32: the coverage drop, removed. Without it a small component
		// inside a large accepted one is measured again, and the same object is
		// counted twice.
		if coveredByAny(r, accepted, p.CoveredFraction) {
			plan.Dedupe++
			continue
		}
		accepted = append(accepted, r)
	}
	plan.Regions = accepted
	return plan
}

// anyDuplicate reports whether R restates one of ACCEPTED at the dedupe IoU.
func anyDuplicate(r ortdetect.Rect, accepted []ortdetect.Rect, threshold float64) bool {
	for _, a := range accepted {
		if cropDedupeIOU(r, a, threshold) {
			return true
		}
	}
	return false
}

// modelScale is the factor a WxH region is multiplied by on its way into a square
// model input.
//
// It is `min(input/w, input/h)` because that is what [ortdetect.Letterbox] computes,
// and it is REIMPLEMENTED here rather than imported because Letterbox takes pixels and
// returns a tensor: the only way to ask it for a scale is to letterbox a whole frame,
// which is not a question about a region.
//
// Two copies of a scale rule is exactly the failure this repository has already had,
// so the value is checked against the letterbox's own arithmetic in
// TestTheRegionScaleIsTheLetterboxScale. Unlike the crop GEOMETRY this duplication
// is unavoidable - it is one multiplication - and unlike the geometry it cannot move a
// box, so the worst a drift could do is misstate a factor in a table.
func modelScale(w, h, input int) float64 {
	if w <= 0 || h <= 0 || input <= 0 {
		return 0
	}
	return minF(float64(input)/float64(w), float64(input)/float64(h))
}

// magnification is how much larger an object of OBJECT px becomes on its way to the
// model, measured through a region rather than through the whole frame.
//
// This is the row `detector --selftest` prints with no footage, and the reason this
// command exists: the factor is arithmetic, and arithmetic does not say whether the
// object is FOUND.
func magnification(objectPx float64, region ortdetect.Rect, frameW, frameH, input int) (whole, crop, factor float64) {
	ws := modelScale(frameW, frameH, input)
	cs := modelScale(region.W, region.H, input)
	if ws <= 0 || cs <= 0 {
		return 0, 0, 0
	}
	whole = objectPx * ws
	crop = objectPx * cs
	return whole, crop, cs / ws
}
