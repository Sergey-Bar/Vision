package main

// footage_crop.go - the CROP PASS, and the policy that decides which regions are
// measured on their own.
//
// # Where the policy lives, and why it is here rather than in internal/soak
//
// The task this file answers says the policy should be one implementation, and
// it is: this is it. It is NOT a second copy of one in `internal/soak`, and that
// is worth being exact about, because a duplicated policy is the specific defect
// this repository has shipped twice (three declarations of one Box, two
// wordings for one state).
//
// `internal/soak/crop.go` DOES NOT EXIST in this tree. The plan assumed a file
// that has not been written, and `internal/soak` is under an exclusive edit lock
// from another workstream, so writing it there would be a cross-workspace
// collision over a measurement tool. The policy is therefore implemented here,
// as ONE implementation, in PURE functions with no detector and no session in
// this file - so moving it is a copy of these functions and their tests, not a
// reconciliation of two copies. `TestTheCropPolicyIsOneImplementation` asserts
// the half of that which can be asserted mechanically: no second spelling of the
// policy exists in this package.
//
// # What the crop pass is actually measuring
//
// The whole-frame pass hands a 1280x720 frame to a model declared at 416x416.
// [ortdetect.Letterbox] scales it by min(416/1280, 416/720) = 0.2167, so an
// object 40 px wide in the frame arrives at the model 8.7 px wide. The same
// object inside a 200x150 crop arrives at 40 * 416/200 = 83.2 px. The factor is
// 9.6.
//
// That is arithmetic and `pocctl detector --selftest` reports it with no footage
// at all, which is exactly what makes it worth nothing on its own: it says what
// WOULD happen to a 40-pixel object, and not whether any object is found. This
// file is where the arithmetic is spent on real pixels, and the honest result
// may well be that the crop pass finds nothing the whole-frame pass missed. That
// result prints.
//
// # Why the detector is handed a SLICE rather than a rectangle
//
// `ortdetect.Detector.Detect(seq, rgb, w, h)` has no region parameter, and
// `internal/ortdetect` is off-limits for this work. So a region is measured by
// slicing the frame to the region's pixels and calling Detect on the slice with
// the slice's own dimensions, then composing each returned box back onto the
// frame. That composition is the whole of the risk and it is arithmetic on two
// axes: a box normalised against the CROP has to become a box normalised against
// the FRAME, and getting the origin wrong places a correctly-detected object in
// the wrong part of the picture - which on a per-class recall table reads as a
// miss rather than as a bug.

import (
	"fmt"
	"math"
	"sort"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/proposal"
)

// cropPolicy is the arithmetic the crop pass is run under, declared once.
//
// Every field moves the numbers, so every field is printed beside them. A
// per-class table with no pad and no dedupe threshold beside it is a table whose
// reader has no way to reproduce it.
type cropPolicy struct {
	// MinArea is the smallest region worth measuring on its own, as a fraction of
	// the frame. It is SP_POC_MIN_BOX_AREA's unit for the same reason: a region
	// smaller than this is compression speckle, and running the model on it costs
	// an inference to learn nothing.
	MinArea float64
	// Pad is how much of a region's OWN size is added on each side before it is
	// clamped to the frame. A measured component is the pixels that changed, and
	// the object is bigger than the pixels that changed.
	Pad float64
	// DedupeIOU is the IoU at which one candidate region is treated as a restatement
	// of another. Measuring the same object twice is how a crop pass inflates its
	// own recall: two identical detections of one object are one true positive and
	// one false positive, and a table that counted the pair as two true positives
	// would report a crop pass that finds everything.
	DedupeIOU float64
	// CoveredFraction is how much of a candidate must already lie inside the
	// accepted regions before it is dropped as adding nothing.
	CoveredFraction float64
	// Max is how many regions one frame may contribute. It bounds the inferences a
	// frame costs, and it is reported rather than applied silently.
	Max int
}

// defaultCropPolicy returns the measured defaults.
func defaultCropPolicy() cropPolicy {
	return cropPolicy{
		// SP_POC_MIN_BOX_AREA's own default. Reused rather than re-picked so the
		// crop pass and the pipeline agree about what counts as an object.
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
		return fmt.Errorf("the crop minimum area is %g; it is a fraction of the frame and must be in (0,1)", p.MinArea)
	}
	if p.Pad < 0 {
		return fmt.Errorf("the crop pad is %g; a negative pad shrinks the region it was meant to enlarge", p.Pad)
	}
	if p.DedupeIOU <= 0 || p.DedupeIOU >= 1 {
		return fmt.Errorf("the crop dedupe IoU is %g; it must be in (0,1), because 0 would drop every region "+
			"against every other and 1 would drop none", p.DedupeIOU)
	}
	if p.CoveredFraction <= 0 || p.CoveredFraction > 1 {
		return fmt.Errorf("the covered fraction is %g; it must be in (0,1]", p.CoveredFraction)
	}
	if p.Max < 1 {
		return fmt.Errorf("at most %d regions may be measured per frame; a cap of zero measures no crop at all "+
			"and would report the crop pass as finding nothing", p.Max)
	}
	return nil
}

// padThenClamp is the ORDER, and the order is the control.
//
// A region is PADDED first and CLAMPED second. Doing it the other way round is
// not a different spelling, it is a different region: clamping an edge region
// first throws away the part of the pad that fell outside the frame, and padding
// afterwards puts it back outside the frame, where the pixel slice has to ignore
// it. The recorded region would then be wider than the pixels actually measured,
// and every box composed back onto the frame would be placed against a crop that
// is not the crop the model saw.
//
// REDCHECK RC-31: delete the `return clampRegion(padded)` and this returns a box
// with X1 below zero. TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame.
func padThenClamp(b proposal.Box, pad float64) proposal.Box {
	w := b.Width() * pad
	h := b.Height() * pad
	padded := proposal.Box{X1: b.X1 - w, Y1: b.Y1 - h, X2: b.X2 + w, Y2: b.Y2 + h}
	// REDCHECK RC-31: the clamp, removed. Everything below measures the padded
	// region, so without this the recorded geometry disagrees with the pixels.
	return clampRegion(padded)
}

// clampRegion brings a box inside the frame.
func clampRegion(b proposal.Box) proposal.Box {
	return proposal.Box{
		X1: clamp01(b.X1), Y1: clamp01(b.Y1),
		X2: clamp01(b.X2), Y2: clamp01(b.Y2),
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// unionBoxes merges regions into the one box that contains all of them.
func unionBoxes(boxes []proposal.Box) proposal.Box {
	if len(boxes) == 0 {
		return proposal.Box{}
	}
	m := boxes[0]
	for _, b := range boxes[1:] {
		m = m.Union(b)
	}
	return m
}

// cropDedupeIOU reports whether two candidate regions are the same region.
//
// It is [bench.IoU] rather than a second IoU, and that is the point: `bench` is
// the module's one matching arithmetic, and a crop pass that deduplicated with
// its own would be the first place a measurement compared two numbers by two
// rules. The threshold is the policy's, not bench's, because deduplicating two
// regions and matching two detections are different questions.
func cropDedupeIOU(a, b proposal.Box, threshold float64) bool {
	return bench.IoU(a, b) >= threshold
}

// coveredByAny reports whether REGION adds nothing to what has already been
// accepted.
//
// CONTAINMENT and not IoU, because the two boxes are not alternatives: a small
// region inside a large accepted region has a low IoU against it and is still
// entirely redundant. The question asked is what fraction of REGION's own area is
// already inside the union, so the comparison is between the candidate and
// itself.
//
// A region with no area is reported as covered. A region with no area has nothing
// to add and must not be handed to a detector, which will letterbox it into a
// full-frame-shaped tensor and report whatever the padding produced.
//
// REDCHECK RC-32: delete the `if coveredByAny` drop in cropRegions and a frame
// with three overlapping components measures the same object three times.
// TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain.
func coveredByAny(region proposal.Box, accepted []proposal.Box, fraction float64) bool {
	own := region.Area()
	if own <= 0 {
		return true
	}
	if len(accepted) == 0 {
		return false
	}
	u := unionBoxes(accepted)
	inter := overlapArea(region, u)
	return inter/own >= fraction
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

// cropPlan is what the policy decided for one frame.
type cropPlan struct {
	Regions []proposal.Box
	// BelowMin is how many candidates the area floor removed.
	BelowMin int
	// Dedupe is how many candidates a dedupe or coverage rule removed.
	Dedupe int
	// Candidates is how many the novelty engine proposed, before any policy.
	Candidates int
}

// cropRegions turns novelty candidates into the regions the detector will see.
//
// The order is fixed and each step has a reason:
//
//	area floor -> pad -> clamp -> sort by area, largest first -> dedupe -> cap
//
// Sorting by area before the dedupe is what makes the surviving set stable: two
// nearly-equal components are resolved in favour of the one that encloses more,
// and a frame's regions therefore do not depend on the order the connected
// components happened to be emitted in.
//
// The cap is applied LAST, so a frame's best regions survive rather than the
// first few the engine proposed.
func cropRegions(cands []novelty.Candidate, p cropPolicy) cropPlan {
	plan := cropPlan{Candidates: len(cands)}
	var kept []proposal.Box
	for _, c := range cands {
		if c.Box.Area() < p.MinArea {
			plan.BelowMin++
			continue
		}
		r := padThenClamp(c.Box, p.Pad)
		if !r.NonEmpty() {
			// A padded-then-clamped region can collapse if the original was a
			// sliver off the edge; a collapsed region is not a region.
			plan.Dedupe++
			continue
		}
		kept = append(kept, r)
	}
	// Largest first, ties broken by position so two frames of the same picture
	// produce the same plan.
	sort.SliceStable(kept, func(i, j int) bool {
		ai, aj := kept[i].Area(), kept[j].Area()
		if ai != aj {
			return ai > aj
		}
		if kept[i].X1 != kept[j].X1 {
			return kept[i].X1 < kept[j].X1
		}
		return kept[i].Y1 < kept[j].Y1
	})

	var accepted []proposal.Box
	for _, r := range kept {
		if len(accepted) >= p.Max {
			plan.Dedupe++
			continue
		}
		if anyDuplicate(r, accepted, p.DedupeIOU) {
			plan.Dedupe++
			continue
		}
		// REDCHECK RC-32: the coverage drop, removed. Without it a small
		// component inside a large accepted one is measured again, and the same
		// object is counted twice.
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
func anyDuplicate(r proposal.Box, accepted []proposal.Box, threshold float64) bool {
	for _, a := range accepted {
		if cropDedupeIOU(r, a, threshold) {
			return true
		}
	}
	return false
}

// proposalBox is the module's box vocabulary, named once here so the crop policy's
// signature does not carry an import that changes the arithmetic.
type proposalBox = proposal.Box

// cropBounds is the pixel rectangle a region occupies in a frame.
//
// It is a TYPE and not four loose integers because the three things that need the
// pixel rectangle - the slice, the composition back onto the frame, and the
// magnification arithmetic - must agree about it exactly. Four integers passed
// separately is four chances for one of them to be rounded differently.
type cropBounds struct {
	X0, Y0, W, H int
}

// String renders it the way a crop filter would write it, so a reader can paste it
// into an ffmpeg command and get the same rectangle.
func (c cropBounds) String() string { return fmt.Sprintf("%dx%d+%d+%d", c.W, c.H, c.X0, c.Y0) }

// cropBoundsOf is the ONE place a region becomes pixels.
//
// It rounds to whole pixels and clamps into the frame, and a region that collapses
// under either is refused rather than measured: a zero-width crop letterboxed
// into a square model input produces a tensor of padding, and a detector handed
// that will answer about the padding.
//
// The origin comes back to the caller because the composition needs it. Recomputing
// it at the composition site is how a box ends up the right size in the wrong
// place.
func cropBoundsOf(fw, fh int, r proposal.Box) (cropBounds, bool) {
	if fw <= 0 || fh <= 0 || !r.NonEmpty() {
		return cropBounds{}, false
	}
	x0 := clampInt(int(roundF(r.X1*float64(fw))), 0, fw-1)
	y0 := clampInt(int(roundF(r.Y1*float64(fh))), 0, fh-1)
	x1 := clampInt(int(roundF(r.X2*float64(fw))), x0+1, fw)
	y1 := clampInt(int(roundF(r.Y2*float64(fh))), y0+1, fh)
	if x1-x0 <= 0 || y1-y0 <= 0 {
		return cropBounds{}, false
	}
	return cropBounds{X0: x0, Y0: y0, W: x1 - x0, H: y1 - y0}, true
}

// cropPixels slices a region's pixels out of a frame.
//
// It returns a fresh buffer, never a window onto the frame's own: the reader
// reuses one buffer for every frame it hands out, and a crop kept past its return
// would be a different picture by the time anybody looked at it.
func cropPixels(rgb []byte, fw, fh int, b cropBounds) []byte {
	if fw <= 0 || fh <= 0 || len(rgb) < fw*fh*3 || b.W <= 0 || b.H <= 0 {
		return nil
	}
	if b.X0 < 0 || b.Y0 < 0 || b.X0+b.W > fw || b.Y0+b.H > fh {
		return nil
	}
	out := make([]byte, b.W*b.H*3)
	rowBytes := b.W * 3
	for y := 0; y < b.H; y++ {
		src := ((b.Y0+y)*fw + b.X0) * 3
		copy(out[y*rowBytes:(y+1)*rowBytes], rgb[src:src+rowBytes])
	}
	return out
}

// composeBox maps a box measured ON a crop back onto the frame.
//
// This is the inverse of [cropPixels] in normalised coordinates: the crop's own
// pixel origin is added and the result divided by the frame, so a box covering
// the whole crop comes back as the crop itself.
//
// REDCHECK RC-33: delete the `+float64(b.X0)` and every crop box lands at the
// crop's origin, scaled against the frame - the right size in the wrong place, and
// on a per-class table that reads as a miss rather than as a bug.
// TestComposingACropBoxIsTheInverseOfSlicingTheRegion.
func composeBox(box proposal.Box, b cropBounds, fw, fh int) proposal.Box {
	if fw <= 0 || fh <= 0 || b.W <= 0 || b.H <= 0 {
		return proposal.Box{}
	}
	fw64, fh64 := float64(fw), float64(fh)
	cw64, ch64 := float64(b.W), float64(b.H)
	return proposal.Box{
		Class: box.Class,
		Score: box.Score,
		X1:    (float64(b.X0) + box.X1*cw64) / fw64,
		Y1:    (float64(b.Y0) + box.Y1*ch64) / fh64,
		X2:    (float64(b.X0) + box.X2*cw64) / fw64,
		Y2:    (float64(b.Y0) + box.Y2*ch64) / fh64,
	}
}

// modelScale is the factor a WxH region is multiplied by on its way into a
// square model input.
//
// It is `min(input/w, input/h)` because that is what [ortdetect.Letterbox]
// computes, and it is REIMPLEMENTED here rather than imported because Letterbox
// takes pixels and returns a tensor: the only way to ask it for a scale is to
// letterbox a whole frame, which is not a question about a region.
//
// Two copies of a scale rule is exactly the failure this repository has already
// had, so the value is checked against the letterbox's own arithmetic in
// TestTheRegionScaleIsTheLetterboxScale.
func modelScale(w, h, input int) float64 {
	if w <= 0 || h <= 0 || input <= 0 {
		return 0
	}
	return minF(float64(input)/float64(w), float64(input)/float64(h))
}

// magnification is how much larger an object of OBJECT px becomes on its way to
// the model, measured through a region rather than through the whole frame.
//
// This is the row `detector --selftest` prints with no footage, and the reason
// this command exists: the factor is arithmetic, and arithmetic does not say
// whether the object is FOUND.
func magnification(objectPx float64, regionW, regionH, frameW, frameH, input int) (whole, crop, factor float64) {
	ws := modelScale(frameW, frameH, input)
	cs := modelScale(regionW, regionH, input)
	if ws <= 0 || cs <= 0 {
		return 0, 0, 0
	}
	whole = objectPx * ws
	crop = objectPx * cs
	return whole, crop, cs / ws
}

func roundF(v float64) float64 { return math.Round(v) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
