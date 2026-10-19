package soak

// The crop pass: cheap perception proposes where to look, and the expensive detector
// looks there at full resolution.
//
// # What it is for
//
// A whole-frame pass squeezes 1920x1080 into the model's 416x416, which is a 4.6x
// downscale. A car 200x100 px arrives as 43x21 and is found comfortably. A bird 40x30
// px at 30 m arrives as 8x6, and 8 px is below the floor at which any detector is
// reliable, so the whole-frame pass cannot find it and no threshold changes that. It is
// not a tuning problem. It is what one downscaled pass over a wide frame IS.
//
// The same bird inside a 200x150 crop arrives as roughly 100 px and is found easily.
// That is the difference between impossible and reliable.
//
// # The division of labour, and why it is drawn here
//
// This file is ORCHESTRATION and nothing else. It decides which regions are worth an
// inference, in what order, and how the two passes' boxes are combined. It does not
// touch a coordinate:
//
//   - the crop's extraction and the crop-to-frame mapping are `ortdetect.Rect`'s,
//     because that package already owns `Letterbox` and `Geometry.Unpad`, and a second
//     implementation of that arithmetic is how the letterbox divisor defect happened
//     the first time;
//   - the regions come from `internal/novelty`, which is pure Go, has no weights and no
//     licence, and already does connected components over a 64-wide luma grid.
//
// Everything here is therefore replaceable arithmetic over two answers, and the tests
// beside this file are about the POLICY, what gets skipped, what gets deduplicated and
// what gets counted, rather than about pixels.
//
// # The policy, and why each rule is there
//
//  0. The novelty pass runs at a RATE, not on every frame. A bird at 30 m is on screen
//     for about 1.5 s and has to be PROPOSED three times in that window or it is never
//     reliably cropped, so the rate is load-bearing rather than a cost knob. See
//     `novelty.Gate` and `SP_POC_NOVELTY_FPS`.
//  1. A region already covered by a whole-frame detection is NOT re-inferred. The
//     whole-frame pass has already looked at those pixels, at the model's resolution
//     over the whole frame. A second inference on a crop containing that car costs
//     about the same as the first and returns the same car.
//  2. A region below the minimum area is skipped. The novelty grid is 64 wide, so one
//     cell is 1/64 of the frame's width, and a component of two cells is the noise
//     floor of a luma difference and not an object.
//  3. A crop that covers the whole frame is not worth an inference. It is letterboxed
//     to the same 416x416, so it costs exactly what the whole-frame pass cost and finds
//     exactly what it found.
//  4. The number of crops per frame is capped, and the number left off is COUNTED. A
//     cap that silently drops is a cap that lies, and this repository has that sentence
//     on three other caps already.

import (
	"github.com/Sergey-Bar/vision-poc/internal/detect"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision/shared/ortdetect"
	"github.com/Sergey-Bar/vision/shared/sidecar"
)

// CropPass is the configuration of the crop pass and the counters it produces.
type CropPass struct {
	// Enabled says whether this run carries the crop pass at all. It is a required
	// setting rather than a default because the default here is the dangerous one: a
	// detector that cannot see a bird at 30 m and a report claiming to report
	// "everything that passes" are the same run, and nothing in the output says
	// otherwise.
	Enabled bool
	// Proposer supplies the candidate regions. Nil means no crop pass is possible, and
	// the setting is refused at startup rather than running and finding nothing.
	Proposer RegionProposer
	// MinArea is the smallest region worth a crop, as a fraction of the FRAME. It is a
	// fraction rather than a cell count so it survives a change of resolution: a
	// threshold in cells means something different on a 640-wide pipe and a 1920-wide
	// one, and the operator who set it would not know they had changed it.
	MinArea float64
	// Pad is how much context to add around a region, as a fraction of the region's own
	// size. Zero is legal and is the right choice when a component is tight against its
	// object; a small pad is what stops a bird at the edge of its component from being
	// cropped at the edge of itself.
	Pad float64
	// MaxRegions is the ceiling on crops per frame. What it left off is counted.
	MaxRegions int
	// CropMinFrames is the source-aware track minimum, handed to the detect engine.
	CropMinFrames int
	// Gate decides which decoded frames the novelty pass runs on.
	//
	// It is a GATE and not a rate, because the gate owns the frame rate: it has to know
	// the source's decoded fps to quantise its interval to a whole number of frames,
	// and a rate handed here without that would have to be rounded by the caller in a
	// second place.
	//
	// Nil means every frame, which is the honest reading and not a disabled gate.
	Gate *novelty.Gate
	// View is the camera-integrity detector.
	//
	// It is HERE and not beside the engine because it reads the novelty score this
	// pass has already computed. A second novelty pass for integrity would double the
	// cost of the crop gate to answer a question the first pass already answers, and the
	// two passes would disagree on the frame a camera moved because they sampled it at
	// different moments.
	//
	// Nil means view integrity is not watched, and the report says so.
	View *novelty.ViewChange
}

// RateSummary is the one line the report and the footer both print, so the novelty rate
// a reader sees is the rate the run used.
//
// It is a function rather than a stored string because the gate's counters change
// during the run, and a line rendered at startup with the rate but not the counts is a
// line that describes a different moment.
func (cp CropPass) RateSummary(decodeFPS int) string {
	return novelty.DescribeGate(cp.Gate, decodeFPS)
}

// CropCounters is what the crop pass did, per frame and cumulatively.
//
// Every field is a count of something that happened, and five of them are counts of
// things that did NOT happen. The refusals are the interesting half: "14 crops and 0
// because 14 regions overlapped a detection" is a run whose cost is understood, and
// "14 crops and 0 because the rate gate ran the novelty pass twice all run" is a
// different fault with a different owner.
type CropCounters struct {
	// Proposed is how many regions the proposer offered this frame.
	Proposed int
	// Inferred is how many crops were actually sent to the model.
	Inferred int
	// SkippedOverlap is how many were refused because a whole-frame detection already
	// covered them, or because the region was the whole frame.
	SkippedOverlap int
	// SkippedSmall is how many were refused as below the minimum area.
	SkippedSmall int
	// SkippedCap is how many were refused because MaxRegions was reached. A non-zero
	// value here on a busy frame is the signal that MaxRegions is too low, and it is
	// printed rather than absorbed.
	SkippedCap int
	// GatedOut is how many frames the NOVELTY RATE gate did not run the pass on.
	//
	// It is the counter that says whether the rate is too low. A gate that skips most
	// of the frames is a gate not proposing regions often enough for a 1.5 s dwell, and
	// the only other symptom is that the smallest objects have quietly gone missing.
	GatedOut int
	// ViewVerdict is what the camera-integrity detector concluded about this frame.
	ViewVerdict novelty.ViewVerdict
	// ViewChanges is how many frames this one DECLARED a view change.
	ViewChanges int
	// ViewSettling is how many frames this one was inside the settling window, with
	// presence suppressed.
	//
	// It is a count and not a flag because "the gate is open" and "the gate has been
	// open for a minute because the scene keeps changing" are different facts about a
	// camera that needs attention, and the second one is invisible in a boolean.
	ViewSettling int
	// BoxesFound is how many boxes the crop passes produced, before deduplication.
	BoxesFound int
	// BoxesKept is how many survived deduplication against the whole-frame boxes. A
	// large gap means the two passes disagree about the same object, which is a fact
	// about the material and worth printing.
	BoxesKept int
	// Refusals is how many crops the detector refused by name. Each one is logged,
	// because a crop pass that stops working is a run that silently reverts to the
	// whole-frame behaviour it was built to improve on.
	Refusals int
	// DedupedAway is how many crop boxes were dropped for overlapping a whole-frame box
	// beyond the IoU threshold.
	DedupedAway int
}

// Add folds one frame's counters into a run total.
func (c CropCounters) Add(o CropCounters) CropCounters {
	return CropCounters{
		Proposed: c.Proposed + o.Proposed, Inferred: c.Inferred + o.Inferred,
		SkippedOverlap: c.SkippedOverlap + o.SkippedOverlap,
		SkippedSmall:   c.SkippedSmall + o.SkippedSmall,
		SkippedCap:     c.SkippedCap + o.SkippedCap,
		GatedOut:       c.GatedOut + o.GatedOut,
		ViewChanges:    c.ViewChanges + o.ViewChanges,
		ViewSettling:   c.ViewSettling + o.ViewSettling,
		BoxesFound:     c.BoxesFound + o.BoxesFound,
		BoxesKept:      c.BoxesKept + o.BoxesKept,
		Refusals:       c.Refusals + o.Refusals,
		DedupedAway:    c.DedupedAway + o.DedupedAway,
	}
}

// cropProposal is one region worth a look, already in frame pixels.
type cropProposal struct {
	rect ortdetect.Rect
	// area is the region's fraction of the frame, kept because the report prints it and
	// recomputing it from a clamped rectangle would print the clamped area.
	area float64
}

// cropDedupeIOU is how much a crop box must overlap a whole-frame box before it is
// dropped as the same object.
//
// 0.5 rather than the tracker's association threshold because these are two
// MEASUREMENTS of one object from two different window sizes, and the same object
// measured through a crop is a different rectangle: the crop pass sees more of the
// object and its surroundings, so its box is systematically tighter and slightly
// offset. At the tracker's 0.3 the two passes would both be kept and the report would
// show a car twice.
const cropDedupeIOU = 0.5

// proposeCrops turns this frame's novelty candidates into pixel rectangles worth an
// inference.
//
// The order is: the rate gate, the area, the overlap gates against the region as
// proposed, the pad, the clamp, the region cap. Everything that removes a candidate is
// counted, because a filter with no counter is a filter whose effect nobody can measure.
//
// # Why the overlap gate runs BEFORE the pad
//
// The gate compares what the proposer SAID against what the whole-frame pass found, not
// the padded crop. Two reasons, and the second is the one that decides it:
//
//   - A pad is CONTEXT FOR THE MODEL, not a statement about the measurement. The
//     novelty engine measured a bird-sized region beside a car; padding that region to
//     give the model surroundings does not turn the proposal into a proposal about the
//     car. Gating on the padded crop means a region two pixels from a large detection
//     is skipped as soon as the pad exceeds the gap, so the pad knob silently decides
//     what gets measured, and the operator who set it is looking at a context fraction,
//     not at a detection filter.
//   - The cost argument is real but it is answered by the region's own size: a crop
//     that is mostly car costs a full inference, and the region being small is the
//     signal that the crop will be too. The pad is a fraction of a small region, so the
//     crop stays small.
func proposeCrops(f frameSource, whole []sidecar.Box, cp CropPass) ([]cropProposal, CropCounters) {
	var ct CropCounters
	if !cp.Enabled || cp.Proposer == nil || len(f.RGB) == 0 || f.Width <= 0 || f.Height <= 0 {
		return nil, ct
	}
	// The rate gate comes BEFORE the proposer. A frame the gate skipped costs nothing,
	// and calling the novelty engine and then discarding its answer would spend the
	// whole cost the gate exists to avoid.
	//
	// The clock is the frame's own arrival on a monotonic clock, not a wall clock,
	// because a wall clock that steps backwards would open the gate for a frame's worth
	// of time and then close it for an interval, and the counters would show a pattern
	// nobody asked for.
	if !cp.Gate.Due(f.Seq, f.AtNS) {
		ct.GatedOut++
		return nil, ct
	}
	cands, sc := cp.Proposer.Candidates(f.RGB, f.Width, f.Height)
	ct.Proposed = len(cands)

	// The view-change arbitration, AFTER the score exists and BEFORE any region is
	// turned into a crop.
	//
	// The order is the whole of the rule. The score has to be computed to know the view
	// moved; the regions have to be discarded rather than cropped, because a crop taken
	// from a frame whose camera is mid-pan describes nothing; and the caller has to be
	// told so it can invalidate its tracks and gate presence.
	//
	// The proposal is DROPPED rather than deferred. A region found on a frame whose view
	// has changed describes a position the camera was pointing at for one frame, and
	// cropping it would measure a place that no longer exists. Deferring would carry the
	// region forward into the next frame, where it does not exist either.
	if cp.View != nil {
		ct.ViewVerdict = cp.View.Observe(sc)
		var discard bool
		ct, discard = applyViewVerdict(ct, ct.ViewVerdict)
		if discard {
			return nil, ct
		}
	}

	frameArea := float64(f.Width * f.Height)
	out := make([]cropProposal, 0, len(cands))
	for _, c := range cands {
		if !c.Box.NonEmpty() {
			continue
		}
		area := c.Box.Area()
		if area < cp.MinArea || area*frameArea < 1 {
			ct.SkippedSmall++
			continue
		}
		// The region's own pixel rectangle, unpadded. This is what the gates below
		// read.
		rx0 := int(c.Box.X1 * float64(f.Width))
		ry0 := int(c.Box.Y1 * float64(f.Height))
		rx1 := int(c.Box.X2 * float64(f.Width))
		ry1 := int(c.Box.Y2 * float64(f.Height))
		if rx1 <= rx0 || ry1 <= ry0 {
			ct.SkippedSmall++
			continue
		}
		region := ortdetect.Rect{X: rx0, Y: ry0, W: rx1 - rx0, H: ry1 - ry0}

		// Rule 3: a region that IS the whole frame. A crop of it is letterboxed to the
		// same 416x416 as the whole-frame pass, so it costs the same and finds the
		// same. This is the common case at the start of a run, before a reference view
		// has settled.
		if region.W >= f.Width && region.H >= f.Height {
			ct.SkippedOverlap++
			continue
		}
		// Rule 1: a region the whole-frame pass already answered. OVERLAPS and not an
		// IoU test, because the question is whether the pixels were already looked at.
		// ABUTTING does not count: those regions share no pixels, so the crop still has
		// something to measure. A false positive here costs one skipped crop.
		if coveredByAny(region, f, whole) {
			ct.SkippedOverlap++
			continue
		}
		// The pad, applied last. It is a fraction of the region's OWN size, so a small
		// region gets a small context and a large one gets a large one; a pad expressed
		// as a fraction of the frame would be huge on a small region and absent on a
		// large one. The clamp is `ortdetect.Rect.Clamp`'s job and it REFUSES rather
		// than shrinking to nothing.
		padX := int(float64(region.W) * cp.Pad / 2)
		padY := int(float64(region.H) * cp.Pad / 2)
		rect, err := ortdetect.Rect{
			X: region.X - padX, Y: region.Y - padY,
			W: region.W + 2*padX, H: region.H + 2*padY,
		}.Clamp(f.Width, f.Height)
		if err != nil {
			// A region the proposer reported as inside the frame and that clamps to
			// nothing is a proposer bug, not a camera fault. It is counted with the
			// small ones and the run continues: refusing the whole frame over one bad
			// region would turn a cosmetic defect into an outage.
			ct.SkippedSmall++
			continue
		}
		if len(out) >= cp.MaxRegions {
			ct.SkippedCap++
			continue
		}
		out = append(out, cropProposal{rect: rect, area: area})
		ct.Inferred++
	}
	return out, ct
}

// applyViewVerdict folds a verdict into the counters and says whether this frame's
// regions must be DISCARDED.
//
// It is a function and not inline code in the caller because the two things it decides
// have to be decided together: the counter that says a change happened and the decision
// to throw the work away. A caller that counted without discarding would report a camera
// move while still cropping a frame mid-pan, and a caller that discarded without counting
// would silently stop finding things.
//
// The regions are DISCARDED and not deferred. A region found on a frame whose view has
// changed describes a position the camera was pointing at for one frame, and carrying it
// forward would measure a place that does not exist in the next frame either.
func applyViewVerdict(ct CropCounters, v novelty.ViewVerdict) (CropCounters, bool) {
	switch v {
	case novelty.ViewChanged:
		ct.ViewChanges++
		return ct, true
	case novelty.ViewSettling:
		ct.ViewSettling++
		return ct, true
	default:
		return ct, false
	}
}

// coveredByAny reports whether any whole-frame box overlaps this region.
//
// The conversion from a pixel rect to a normalised one happens HERE and in one
// direction only. Converting the whole-frame box to pixels instead would be the same
// arithmetic with the operands swapped, and swapping them is exactly how a crop gate
// ends up comparing a 200-pixel rectangle against a 0.2-sized fraction and matching
// nothing, which is a crop pass that runs, costs the full frame rate, and finds nothing
// new.
func coveredByAny(rect ortdetect.Rect, f frameSource, boxes []sidecar.Box) bool {
	cand := ortdetect.Box{
		X1: float64(rect.X) / float64(f.Width), Y1: float64(rect.Y) / float64(f.Height),
		X2: float64(rect.X+rect.W) / float64(f.Width), Y2: float64(rect.Y+rect.H) / float64(f.Height),
	}
	for _, b := range boxes {
		if ortdetect.Overlaps(cand, ortdetect.Box{X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2}) {
			return true
		}
	}
	return false
}

// unionBoxes merges two passes' boxes, dropping a crop box that measures the same
// object as a whole-frame one.
//
// The whole-frame box WINS on overlap. It was measured over the whole scene, which is
// what every other part of the system was calibrated against, so the crop pass can only
// ADD what the whole-frame pass missed and never move or replace what it found. A union
// that preferred the tighter crop box would quietly change the box on every detection
// the crop pass also saw, and the tracker's association history would hold two slightly
// different boxes for one object.
func unionBoxes(whole, cuts []sidecar.Box) (out []sidecar.Box, kept, deduped int) {
	out = append(out, whole...)
	kept = len(whole)
	for _, c := range cuts {
		if overlapsAnyAbove(c, whole, cropDedupeIOU) {
			deduped++
			continue
		}
		out = append(out, c)
		kept++
	}
	return out, kept, deduped
}

func overlapsAnyAbove(c sidecar.Box, against []sidecar.Box, min float64) bool {
	for _, b := range against {
		if boxIoU(sidecar.Box{X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2},
			ortdetect.Box{X1: c.X1, Y1: c.Y1, X2: c.X2, Y2: c.Y2}) >= min {
			return true
		}
	}
	return false
}

// boxIoU is intersection over union for two frame-normalised boxes.
//
// It is written here rather than exported from `ortdetect` because `ortdetect`'s own
// `iou` is unexported and lives beside an NMS that already owns the question for its
// own purpose. Exporting it would give the crop gate and the decoder the same name for
// two thresholds, the decoder's is a suppression bar and this one is a dedupe bar, and
// the two would drift apart silently.
func boxIoU(a, b ortdetect.Box) float64 {
	ix := minf(a.X2, b.X2) - maxf(a.X1, b.X1)
	if ix <= 0 {
		return 0
	}
	iy := minf(a.Y2, b.Y2) - maxf(a.Y1, b.Y1)
	if iy <= 0 {
		return 0
	}
	inter := ix * iy
	union := (a.X2-a.X1)*(a.Y2-a.Y1) + (b.X2-b.X1)*(b.Y2-b.Y1) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// cutSetOf is the membership set the detect engine reads Answer.Cut through.
//
// `sidecar.Box` and `detect.Box` are the SAME type, both aliases of `proposal.Box`, which is
// what makes this a one-conversion loop instead of a field-by-field copy. A
// reconstruction would be a second place for a coordinate to be rescaled, and a
// rescaled coordinate is a box in the wrong place on the picture.
func cutSetOf(cuts []sidecar.Box) map[detect.Box]bool {
	if len(cuts) == 0 {
		return nil
	}
	m := make(map[detect.Box]bool, len(cuts))
	for _, b := range cuts {
		m[detect.Box(b)] = true
	}
	return m
}

// frameSource is the part of recorder.Frame the crop pass needs.
//
// It is named here so `proposeCrops` is a function of its inputs and not of a
// recorder.Frame, which would drag the recorder's construction into a test of policy.
// Every field is copied, so a later mutation of the frame cannot change a decision that
// has already been recorded.
type frameSource struct {
	Seq           uint64
	RGB           []byte
	Width, Height int
	// AtNS is the frame's arrival on a monotonic clock, in nanoseconds. It is the only
	// clock the novelty gate reads, so a wall clock that steps cannot change how many
	// passes the run spends.
	AtNS int64
}

// noveltyScoreOf is the proposer's last score, for the decision row.
//
// It is read from the Proposer and not from `Runner.Trigger`, because in detector mode
// there is no Trigger and the novelty engine exists only to propose. Reading the wrong
// one would record the novelty engine's score against a trigger that never ran.
func noveltyScoreOf(cp CropPass) novelty.Score {
	if !cp.Enabled || cp.Proposer == nil {
		return novelty.Score{}
	}
	return cp.Proposer.Last()
}
