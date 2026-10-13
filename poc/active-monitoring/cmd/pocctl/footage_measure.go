package main

// footage_measure.go - the two passes over the declared window, and the report.
//
// # What this measures
//
// `pocctl detector --selftest` reports the crop ARITHMETIC with no footage at all: a
// 40-pixel object arrives at a 416-input model 8.7 pixels wide through the whole frame
// and 83.2 pixels wide through a 200x150 crop, a factor of 9.6. That is a true
// statement about resampling and it is worth very little on its own, because it says
// what would happen to a 40-pixel object and not whether any object is FOUND.
//
// This spends that arithmetic on real pixels from real material, runs BOTH settings
// over the SAME frames, keeps them apart, and reports what it measured beside what it
// did not. The headline result may be that the crop pass finds nothing the whole-frame
// pass missed. That result prints.
//
// # The two passes, kept apart
//
// WHOLE-FRAME: `det.Detect(seq, rgb, w, h, ortdetect.Rect{})`. One inference per frame.
//
// CROP: the same call with the region's own `ortdetect.Rect`. `Detect` extracts the
// rectangle, letterboxes it, decodes against the crop's geometry and uncrops the result
// onto the frame, all inside `internal/ortdetect`.
//
// The geometry is upstream's and not this file's, and this branch deleted its own copy
// of it rather than keeping both. Three numbers have to agree (letterbox the CROP,
// decode against the CROP, map back to the FRAME) and the failure when they do not is
// silent and total: every box lands in the wrong place at plausible coordinates with no
// error anywhere. This file holds one call site per step and no arithmetic of its own.
//
// The two settings share one novelty engine and never share a counter, a latency window
// or a table. Two engines would propose two different region sets from the same pixels,
// and a comparison between two different region sets measures the region proposer rather
// than the crop pass.
//
// # What this does NOT measure, and says so on its face
//
// Arbitrary footage has no ground truth. Unless `--truth` supplies boxes a human drew,
// recall has no denominator and precision has nothing to be right or wrong about, and
// BOTH print `NOT RUN` with the reason. They never print 0.000.
//
// And if the decoder refuses the export, that is the measurement result it is: the
// arithmetic above was measured on real pixels, and whether the crop pass finds anything
// is UNKNOWN.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/bench"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
)

// regime is one setting's measurement over the window.
type regime struct {
	// Name is what the table calls it.
	Name string
	// Inferences is how many times the model was actually called.
	Inferences int
	// Refusals counts inferences the decoder refused.
	Refusals int
	// RefusalWhy is the decoder's own name for the refusal, deduplicated.
	RefusalWhy []string
	// FirstRefusal is the first refusal in full, printed verbatim.
	FirstRefusal *ortdetect.Refusal
	// Frames is every frame this setting saw, in order. The whole-frame and crop settings
	// MUST have the same length: a side-by-side comparison between two different sample
	// sets is a comparison of nothing.
	Frames []classFrame
	// Regions is how many regions this setting measured, crop pass only.
	Regions int
	// BelowMin and Deduped are the novelty candidates this setting's policy removed before
	// any inference.
	BelowMin int
	Deduped  int
	// SilentFrames is how many frames proposed no region above the floor.
	SilentFrames int
}

func (r *regime) noteRefusal(ref *ortdetect.Refusal) {
	r.Refusals++
	if r.FirstRefusal == nil {
		r.FirstRefusal = ref
	}
	for _, have := range r.RefusalWhy {
		if have == ref.What {
			return
		}
	}
	r.RefusalWhy = append(r.RefusalWhy, ref.What)
}

// classFrame is one frame's two sides, ready to hand to bench.
type classFrame struct {
	Frame string
	// Dets carry their class in the embedded Box. Truth carries its class in GT.Class.
	Dets  []bench.Detection
	Truth []bench.GT
}

// twoPassResult is both settings, the read that fed them, and the measured geometry of
// every region - the last of which needs no model at all.
type twoPassResult struct {
	Read    windowRead
	whole   regime
	crop    regime
	regions []regionSample
}

// regionSample is one region's measured geometry on real pixels.
//
// It is measured even when no model can be opened, because the magnification factor is
// arithmetic and arithmetic does not need a session. It is the part of this command that
// CAN be reported against an anchored export, and reporting it is not the same as
// reporting a detection.
type regionSample struct {
	At    float64
	Rect  ortdetect.Rect
	Frame int
}

// measureTwoPasses samples the window and runs both settings over it.
func measureTwoPasses(c context.Context, clip string, win footageWindow, policy cropPolicy,
	det *ortdetect.Detector, truth *truthFile) (twoPassResult, error) {

	engine, err := novelty.New(novelty.Config{
		Mode:     novelty.ModeTemporal,
		Class:    "novelty_region",
		PromptID: "alarm",
	})
	if err != nil {
		return twoPassResult{}, fmt.Errorf("footage: the novelty engine that proposes crop regions could not "+
			"be built, so the crop pass has no regions and would report a comparison it never made: %w", err)
	}

	res := twoPassResult{
		whole: regime{Name: "whole-frame"},
		crop:  regime{Name: "crop"},
	}

	// The novelty engine is advanced ONCE per frame and shared by both settings,
	// deliberately: two engines would propose two different region sets from the same
	// pixels, and a comparison between two different region sets measures the region
	// proposer rather than the crop pass.
	sink := func(fr sampledFrame) error {
		name := fmt.Sprintf("%s@%.1fs", filepath.Base(clip), fr.At)
		var tg []bench.GT
		if truth != nil {
			tg = truth.at(fr.At, win.Every)
		}
		cands, _ := engine.Candidates(fr.RGB, fr.W, fr.H)

		// -- whole-frame ------------------------------------------------------
		wh := &res.whole
		wh.Inferences++
		frWh := classFrame{Frame: name, Truth: tg}
		if det != nil {
			// The ZERO Rect is the whole frame, and it is spelled rather than defaulted so
			// a reader of this line can see that the two passes differ in exactly one
			// argument.
			ans := det.Detect(uint64(fr.Index), fr.RGB, fr.W, fr.H, ortdetect.Rect{})
			if ans.Refusal != nil {
				wh.noteRefusal(ans.Refusal)
			}
			frWh.Dets = detectionsOf(name, ans)
		}
		wh.Frames = append(wh.Frames, frWh)

		// -- crop -------------------------------------------------------------
		cp := &res.crop
		plan := cropRegions(cands, policy, fr.W, fr.H)
		cp.BelowMin += plan.BelowMin
		cp.Deduped += plan.Dedupe
		frCp := classFrame{Frame: name, Truth: tg}
		if len(plan.Regions) == 0 {
			cp.SilentFrames++
			// The crop pass still records the frame. A frame with no region is a frame where
			// novelty proposed nothing, and recording it with no detections is what makes
			// "the crop pass found nothing" a measurement over the same frames rather than a
			// shorter run.
			cp.Frames = append(cp.Frames, frCp)
			return nil
		}
		var boxes []bench.Detection
		for i, r := range plan.Regions {
			cp.Regions++
			cp.Inferences++
			res.regions = append(res.regions, regionSample{At: fr.At, Rect: r, Frame: fr.Index})
			if det == nil {
				continue
			}
			// ONE call, and the crop pass is upstream's. The sequence number carries the
			// region index so two inferences of one frame are two entries and a crop's cost
			// is attributable to a crop.
			ans := det.Detect(uint64(fr.Index)*1000+uint64(i), fr.RGB, fr.W, fr.H, r)
			if ans.Refusal != nil {
				cp.noteRefusal(ans.Refusal)
				continue
			}
			boxes = append(boxes, detectionsOf(name, ans)...)
		}
		frCp.Dets = boxes
		cp.Frames = append(cp.Frames, frCp)
		return nil
	}

	read, err := readWindowFrames(c, clip, win, int(win.For/win.Every), sink)
	res.Read = read
	if err != nil {
		return res, err
	}
	return res, nil
}

// detectionsOf converts one answer into bench detections.
//
// It is a function because BOTH passes return frame-normalised boxes already - `Detect`
// uncrops inside the package that owns the geometry - so there is one conversion and it
// is not the pass's business which pass produced the answer.
func detectionsOf(frame string, ans ortdetect.Answer) []bench.Detection {
	if len(ans.Boxes) == 0 {
		return nil
	}
	out := make([]bench.Detection, 0, len(ans.Boxes))
	for _, b := range ans.Boxes {
		out = append(out, bench.Detection{Frame: frame, Box: b})
	}
	return out
}

// footageReport is everything the renderer needs, gathered so the renderer holds no
// measurement state of its own.
type footageReport struct {
	Clip       string
	Label      clipLabel
	Window     footageWindow
	Read       windowRead
	Policy     cropPolicy
	Opts       bench.Options
	Input      ortdetect.InputSpec
	Identity   ortdetect.Identity
	Whole      regime
	Crop       regime
	TruthState string
	TruthBoxes *truthFile
	DetErr     error
	Regions    []regionSample
}

func renderFootageReport(r footageReport) string {
	var b strings.Builder

	b.WriteString("\n# the two passes over the same frames\n\n")
	fmt.Fprintf(&b, "  material      %s\n", r.Clip)
	fmt.Fprintf(&b, "  window        %s\n", r.Window.describeWindow())
	fmt.Fprintf(&b, "  frames        %d declared, %d read, %dx%d\n", r.Read.Declared, r.Read.Read, r.Read.W, r.Read.H)
	if r.Read.Declared != r.Read.Read {
		fmt.Fprintf(&b, "                the reader returned %d frame(s) where %d were declared. Both are "+
			"printed because a window that delivered fewer samples than it promised is a different "+
			"measurement from one that delivered exactly what it declared\n", r.Read.Read, r.Read.Declared)
	}
	fmt.Fprintf(&b, "  decode        %s\n", r.Read.Command)
	b.WriteString("\n")

	renderArithmetic(&b, r)

	renderRegime(&b, "WHOLE-FRAME  one inference per frame", r.Whole)
	b.WriteString("\n")
	renderRegime(&b, "CROP  one inference per region, every box mapped back onto the frame by Detect", r.Crop)

	// Both settings' arithmetic comes from bench: Measure partitions each frame by class,
	// Accumulate folds the frames, and FoldClassCounts sums the partitions. No rate in this
	// report is computed in this package.
	wholeRows := foldFrames(r.Whole.Frames, r.Opts)
	cropRows := foldFrames(r.Crop.Frames, r.Opts)
	writePerClassSideBySide(&b, wholeRows, cropRows, r.Opts)
	b.WriteString("\n")

	renderNotMeasured(&b, r)
	return b.String()
}

// foldFrames runs bench over a setting's frames and returns the per-class partition.
//
// It is a function rather than inlined because BOTH settings must go through it, and one
// place that partitions is one place that can be checked.
func foldFrames(frames []classFrame, o bench.Options) []bench.ClassCounts {
	counts := make([]bench.FrameCount, 0, len(frames))
	for _, f := range frames {
		counts = append(counts, bench.Measure(f.Frame, f.Dets, f.Truth, o))
	}
	return bench.FoldClassCounts(counts)
}

// renderArithmetic is the part of this command that needs no model, and so the part that
// is always measurable.
func renderArithmetic(b *strings.Builder, r footageReport) {
	b.WriteString("crop arithmetic, on these frames, with no model involved\n")
	fmt.Fprintf(b, "  policy        min area %.4f of the frame; pad %.2f of a region's own size on each side,\n",
		r.Policy.MinArea, r.Policy.Pad)
	fmt.Fprintf(b, "                then clamped to the frame; dedupe IoU %.2f; a region %.0f%% inside the\n",
		r.Policy.DedupeIOU, r.Policy.CoveredFraction*100)
	fmt.Fprintf(b, "                accepted union is dropped; at most %d per frame. The geometry is\n",
		r.Policy.Max)
	b.WriteString("                ortdetect.Rect, and the extraction and the mapping back are inside Detect\n")
	fmt.Fprintf(b, "  regions       %d measured on their own, over %d distinct frame(s)\n",
		len(r.Regions), distinctRegionFrames(r.Regions))
	if len(r.Regions) == 0 {
		b.WriteString("                no region was proposed above the floor anywhere in this window, so there\n")
		b.WriteString("                is nothing to magnify. That is a measurement about the material and the\n")
		b.WriteString("                novelty engine. It is NOT a statement that the crop pass found nothing.\n\n")
		return
	}

	input := r.Input.W
	if input <= 0 {
		// No session, so the model's declared input is unknown. 416 is the declared input of
		// the export this repository measures and it is LABELLED as the assumption it is
		// rather than printed as a fact: the factors scale linearly with it, so a reader with
		// a different model corrects them in one multiplication.
		input = 416
	}
	fmt.Fprintf(b, "  model input   %d, ", input)
	if r.Input.W <= 0 {
		b.WriteString("ASSUMED - no session was open, so the model's own declaration was not read. The\n")
		b.WriteString("                factors scale linearly with this number\n")
	} else {
		b.WriteString("read back from the session\n")
	}
	b.WriteString("\n")
	b.WriteString("  what a 40 px-wide object becomes on its way to the model, per region:\n\n")
	fmt.Fprintf(b, "    %-8s %-20s %-8s %9s %8s %7s\n", "at", "crop rect", "pixels", "whole px", "crop px", "factor")
	for _, s := range r.Regions {
		whole, crop, factor := magnification(40, s.Rect, r.Read.W, r.Read.H, input)
		fmt.Fprintf(b, "    %-8s %-20s %-8s %9.1f %8.1f %7.1f\n", shortSeconds(s.At), rectString(s.Rect),
			fmt.Sprintf("%dx%d", s.Rect.W, s.Rect.H), whole, crop, factor)
	}
	b.WriteString("\n  These are RESAMPLING factors. They say what becomes of an object on its way to the\n")
	b.WriteString("  model and they say nothing about whether the model finds it. A factor of nine on\n")
	b.WriteString("  an object the model never saw is a factor of nine on nothing.\n\n")
}

func renderRegime(b *strings.Builder, title string, reg regime) {
	fmt.Fprintf(b, "%s\n", title)
	fmt.Fprintf(b, "  inferences    %d\n", reg.Inferences)
	fmt.Fprintf(b, "  frames        %d\n", len(reg.Frames))
	if reg.Regions > 0 {
		fmt.Fprintf(b, "  regions       %d measured on their own; %d frame(s) proposed no region above the floor\n",
			reg.Regions, reg.SilentFrames)
		fmt.Fprintf(b, "  candidates    %d removed by the area floor, %d by the dedupe and coverage rules\n",
			reg.BelowMin, reg.Deduped)
	}
	if reg.Refusals == 0 {
		b.WriteString("  refusals      none\n")
	} else {
		fmt.Fprintf(b, "  refusals      %d of %d inference(s). The decoder's own reasons:\n", reg.Refusals, reg.Inferences)
		for _, why := range reg.RefusalWhy {
			fmt.Fprintf(b, "                %s\n", why)
		}
		if reg.FirstRefusal != nil {
			for _, line := range wrapDetail(reg.FirstRefusal.Detail, 82) {
				fmt.Fprintf(b, "                %s\n", line)
			}
		}
	}
	b.WriteString("\n")
}

// renderNotMeasured is the section this command exists to keep honest.
//
// Every absence is named and none of them is rendered as a zero. A table of zeros over a
// measurement that did not run is worse than no table: it reads as a detector that finds
// nothing rather than as a detector that was never asked.
func renderNotMeasured(b *strings.Builder, r footageReport) {
	b.WriteString("WHAT WAS NOT MEASURED\n\n")

	switch {
	case r.DetErr != nil:
		b.WriteString("  - per-class precision and recall, for BOTH settings: NOT RUN.\n")
		b.WriteString("    The detector could not be opened, so the model was never called. The reason:\n\n")
		for _, line := range wrapDetail(r.DetErr.Error(), 82) {
			fmt.Fprintf(b, "      %s\n", line)
		}
		b.WriteString("\n    A COUNT of zero detections was not measured either. A precision is detections\n")
		b.WriteString("    divided by detections, and there were none of either: a zero here would be a claim\n")
		b.WriteString("    that every detection was wrong when there were no detections at all.\n\n")
	case r.Whole.Refusals+r.Crop.Refusals > 0:
		total := r.Whole.Inferences + r.Crop.Inferences
		fmt.Fprintf(b, "  - per-class precision and recall, for BOTH settings: NOT RUN.\n")
		fmt.Fprintf(b, "    The decoder refused %d of %d inference(s). A refusal is a named reason and not a\n",
			r.Whole.Refusals+r.Crop.Refusals, total)
		b.WriteString("    zero, and a table computed over refusals would be a table about a decoder that\n")
		b.WriteString("    could not read the model. Its reason, verbatim:\n\n")
		if r.Whole.FirstRefusal != nil {
			for _, line := range wrapDetail(r.Whole.FirstRefusal.Error(), 80) {
				fmt.Fprintf(b, "      %s\n", line)
			}
		}
		b.WriteString("\n    So the arithmetic above is real and was measured on real pixels, and whether\n")
		b.WriteString("    the crop pass finds an object the whole-frame pass missed is UNKNOWN. Not zero,\n")
		b.WriteString("    not worse: unknown. This build does not decode this class of export at all.\n\n")
	}

	if strings.HasPrefix(r.TruthState, notRun) {
		b.WriteString("  - precision and recall, a second time and for a different reason: NOT RUN.\n")
		b.WriteString("    There are no ground-truth labels for arbitrary footage. testdata/labels.json\n")
		b.WriteString("    records provenance, a line and an expected time window; it records no object\n")
		b.WriteString("    boxes, and a box drawn by looking at the picture is not ground truth - it is a\n")
		b.WriteString("    guess with a file name on it. Supply --truth and this becomes a measurement.\n\n")
	} else if r.TruthBoxes != nil && r.TruthBoxes.unmatched > 0 {
		fmt.Fprintf(b, "  - %d ground-truth box(es) matched no sampled frame and were counted, not attached.\n",
			r.TruthBoxes.unmatched)
		fmt.Fprintf(b, "    The tolerance is %.2fs and those boxes were further from every sample than that.\n",
			truthToleranceSeconds(r.Window.Every))
		b.WriteString("    Attaching them anyway would have manufactured a miss on a frame the annotator\n")
		b.WriteString("    never described, and a recall that penalises the annotator's timing is a recall\n")
		b.WriteString("    about the sampling interval rather than about the detector.\n\n")
	}

	b.WriteString("  - false positives per hour: NOT RUN.\n")
	b.WriteString("    A per-hour rate needs a duration and a clock the benchmark does not own, and a\n")
	b.WriteString("    zero here would be an encouraging infinity rather than a measurement.\n\n")

	b.WriteString("  - any class this build cannot name: counted as DROPPED, never as a miss.\n")
	b.WriteString("    That is a limit of the vocabulary and not of the detector, and a recall quoted\n")
	b.WriteString("    without the drop count beside it is a claim about the vocabulary.\n")
}

func rectString(r ortdetect.Rect) string { return fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.W, r.H) }

func shortSeconds(v float64) string { return fmt.Sprintf("%.1fs", v) }

// distinctRegionFrames counts the frames that contributed at least one region.
//
// It counts DISTINCT instants rather than regions, because the row beside it reads "N
// measured on their own, over M frames" and a reader comparing the two numbers is
// checking whether the cap is doing anything.
func distinctRegionFrames(samples []regionSample) int {
	seen := map[float64]bool{}
	for _, s := range samples {
		seen[s.At] = true
	}
	return len(seen)
}
