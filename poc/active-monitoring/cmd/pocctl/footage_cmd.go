package main

// footage_cmd.go - `pocctl footage`: the two-pass measurement over a DECLARED
// window of licensed material.
//
// # What this command is for
//
// `pocctl detector --selftest` reports the crop ARITHMETIC with no footage at all:
// a 40-pixel object arrives at a 416-input model 8.7 pixels wide through the whole
// frame and 83.2 pixels wide through a 200x150 crop, a factor of 9.6. That is a
// true statement about resampling and it is worth very little on its own, because
// it says what would happen to a 40-pixel object and not whether any object is
// FOUND.
//
// This command spends that arithmetic on real pixels from real material, runs
// BOTH settings over the SAME frames, keeps the two apart, and reports what it
// measured beside what it did not. The headline result may be that the crop pass
// finds nothing the whole-frame pass missed. That result prints.
//
// # The order, and why the gate is first
//
//	licence gate -> window validation -> open the model -> sample frames ->
//	two passes -> per class, side by side -> what was NOT measured
//
// The gate runs to completion before a pixel is decoded, and the window is
// validated before the first frame is read. Both refusals are cheaper than the
// measurement and the first one decides whether the measurement may be quoted at
// all.
//
// # The two passes, kept apart
//
// WHOLE-FRAME: `det.Detect(seq, rgb, w, h)`. One inference per frame.
//
// CROP: for each region the novelty engine proposed, the frame is SLICED to that
// region, `Detect` is called on the slice with the slice's own dimensions, and
// each returned box is composed back onto the frame. `internal/ortdetect` has no
// region parameter and is out of scope for this work, so slicing is how a region
// is measured on its own. The composition is the whole of the risk and it is
// `composeBox` in footage_crop.go, beside a test that it is the inverse of the
// slice.
//
// The two settings share one novelty engine and never share a counter, a latency
// window or a table. Two engines would propose two different region sets from the
// same pixels, and a comparison between two different region sets measures the
// region proposer rather than the crop pass. A side-by-side comparison drawn from
// one accumulator is a comparison of nothing.
//
// # What this command does NOT measure, and says so on its face
//
// Arbitrary footage has no ground truth. `testdata/labels.json` records
// provenance, lines and expected time windows; it records no object boxes, and no
// box may be invented from a picture. So unless `--truth` supplies boxes a human
// drew, recall has no denominator and precision has nothing to be right or wrong
// about, and BOTH print `NOT RUN` with the reason. They never print 0.000.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

func footageUsage() {
	fmt.Print(`pocctl footage - two-pass measurement over a declared window of licensed material

  footage --clip <path> --from <s> --to <s> [--every <s>]

Runs the detector twice over the SAME frames and reports the two settings side by
side: whole-frame, and one inference per region the novelty engine proposed. The
crop pass exists because a 40-pixel object arrives at a 416-input model 8.7 pixels
wide through the whole frame and 83.2 pixels wide through a 200x150 crop, a factor
of nine.

  --clip         the material. It must have a row in testdata/labels.json with a
                 recorded licence AND a cleared_for_external_use field. Material
                 without either is refused before a pixel is decoded.
  --from --to    the window, in seconds.
  --every        the sampling interval. Refused above 1.5s: the smallest object
                 this system reports is on screen for about that long.
  --truth        a ground-truth box file. WITHOUT one, per-class precision and
                 recall print NOT RUN with the reason. There are no ground-truth
                 labels for arbitrary footage and this command will not invent any.
  --models       the directory holding yolov4n.onnx and onnxruntime.dll.

Every number on the output says where it came from. A number that could not be
measured prints NOT RUN and the reason, and never 0.000.
`)
}

// cmdFootage is the entry point.
func cmdFootage(args []string) error {
	flags, err := flagSet("footage", map[string]string{
		"clip": "", "labels": filepath.Join("testdata", "labels.json"),
		"from": "0", "to": "0", "every": "0.5",
		"models": "models",
		"model":  "", "ort": "",
		"ep":            envOr("SP_POC_ORT_EP", "cpu"),
		"cuda-device":   envOr("SP_POC_ORT_CUDA_DEVICE", "0"),
		"threads":       envOr("SP_POC_ORT_THREADS", "4"),
		"emit":          envOr("SP_POC_DET_SCORE", "0.05"),
		"max-boxes":     envOr("SP_POC_DET_MAX_BOXES", "100"),
		"nms-class":     "0.45",
		"nms-global":    "0.75",
		"min-score":     envOr("SP_POC_SCORE_THRESHOLD", "0"),
		"match-iou":     "0.5",
		"crop-min-area": "0.0005",
		"crop-pad":      "0.25",
		"crop-dedupe":   "0.5",
		"crop-max":      "8",
		"truth":         "",
		"out":           "",
	}, args)
	if err != nil {
		footageUsage()
		return err
	}
	clip := strings.TrimSpace(flags["clip"])
	if clip == "" {
		footageUsage()
		return fmt.Errorf("footage: --clip is required. This command measures ONE named piece of material, " +
			"because the licence gate reads a row per file and a measurement over 'the footage' is a " +
			"measurement over nothing anybody can check")
	}

	// --- 1. the licence gate ----------------------------------------------------
	clips, err := readFootageLabels(flags["labels"])
	if err != nil {
		printFootageHeader(clip, "licence gate: REFUSED")
		fmt.Printf("  %v\n\n", err)
		return err
	}
	label, err := resolveLabel(clips, clip)
	if err != nil {
		printFootageHeader(clip, "licence gate: REFUSED")
		fmt.Printf("  %v\n\n", err)
		return err
	}
	if err := checkLicenceGate(label); err != nil {
		printFootageHeader(clip, "licence gate: REFUSED")
		var lr *licenceRefusal
		if !asLicenceRefusal(err, &lr) {
			fmt.Printf("  %v\n\n", err)
			return err
		}
		fmt.Printf("  the row for %s may not be measured, for %d reason(s):\n\n", lr.File, len(lr.Reasons))
		for i, reason := range lr.ReasonLines() {
			for n, line := range wrapDetail(reason, 84) {
				if n == 0 {
					fmt.Printf("  %d. %s\n", i+1, line)
					continue
				}
				fmt.Printf("     %s\n", line)
			}
		}
		fmt.Println()
		fmt.Println("  Nothing was decoded, no model was opened and no number was computed. That is the")
		fmt.Println("  gate working: the measurement is refused before it can produce one.")
		fmt.Println()
		return err
	}
	printFootageHeader(clip, "licence gate: PASS")
	fmt.Printf("  licence      %s\n", trimTo(label.Licence, 76))
	fmt.Printf("  attribution  %s\n", orNone(trimTo(label.Attribution, 76), "NOT RECORDED"))
	fmt.Println("  cleared      yes - on this machine only; the row is not a right to publish")
	fmt.Printf("  provenance   %s\n", orNone(label.Provenance, "NOT RECORDED"))
	if label.SHA256 != "" {
		fmt.Printf("  sha256       %s\n", label.SHA256)
	}
	fmt.Println()

	// --- 2. the window ----------------------------------------------------------
	from, err := parseSeconds("footage", "--from", flags["from"])
	if err != nil {
		return err
	}
	to, err := parseSeconds("footage", "--to", flags["to"])
	if err != nil {
		return err
	}
	every, err := parseSeconds("footage", "--every", flags["every"])
	if err != nil {
		return err
	}
	win, err := validateFootageWindow(from, to, every)
	if err != nil {
		fmt.Println("window: REFUSED")
		fmt.Printf("  %v\n\n", err)
		return err
	}
	fmt.Println("window")
	fmt.Printf("  %v to %v, every %v, %d frame(s) declared\n\n", win.From, win.To, win.Every, win.Declared)

	// --- 3. the settings -------------------------------------------------------
	opts := bench.DefaultOptions()
	if _, serr := fmt.Sscan(flags["match-iou"], &opts.MatchThreshold); serr != nil {
		return fmt.Errorf("footage: --match-iou is %q, which is not a number. A match threshold that "+
			"silently resolved to the default would be a threshold the reader cannot see", flags["match-iou"])
	}
	opts.ClassThreshold = envFloat("SP_POC_SCORE_THRESHOLD", flags["min-score"], 0)

	policy := defaultCropPolicy()
	var perr error
	if policy.MinArea, perr = parseFraction("footage", "--crop-min-area", flags["crop-min-area"]); perr != nil {
		return perr
	}
	if policy.Pad, perr = parseFraction("footage", "--crop-pad", flags["crop-pad"]); perr != nil {
		return perr
	}
	if policy.DedupeIOU, perr = parseFraction("footage", "--crop-dedupe", flags["crop-dedupe"]); perr != nil {
		return perr
	}
	if policy.Max, perr = parseInt("footage", "--crop-max", flags["crop-max"]); perr != nil {
		return perr
	}
	if perr := policy.Validate(); perr != nil {
		return fmt.Errorf("footage: %w", perr)
	}

	truth, truthErr := readTruthFile(flags["truth"])
	truthState := describeTruth(truth, truthErr, win)
	fmt.Println("ground truth")
	for i, line := range wrapDetail(truthState, 84) {
		if i == 0 {
			fmt.Printf("  %s\n", line)
			continue
		}
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()

	// --- 4. the model, or the refusal that stops the measurement ---------------
	det, modelNote, detErr := openFootageDetector(flags, opts)
	var identity ortdetect.Identity
	var inputSpec ortdetect.InputSpec
	if det != nil {
		identity = det.Identity()
		inputSpec = det.Input()
		fmt.Println("detector")
		fmt.Printf("  model        %s\n", filepath.Base(identity.ModelPath))
		fmt.Printf("  sha256       %s\n", identity.SHA256)
		fmt.Printf("  licence      %s\n", licenceOrUnrecorded(identity.Licence))
		fmt.Printf("  input        %s %dx%d\n", inputSpec.Layout, inputSpec.W, inputSpec.H)
		fmt.Printf("  emit floor   %s - the model's, not the run's approval bar\n", flags["emit"])
		fmt.Printf("  match IoU    %.2f\n", opts.MatchThreshold)
		fmt.Printf("  provider     %s, threads %s\n", flags["ep"], flags["threads"])
		fmt.Println()
	} else {
		// The model is the one thing that may legitimately be absent, and the rest
		// of the measurement still runs: frames are sampled, regions are proposed
		// and the scale arithmetic is computed on real pixels. Only the detection
		// columns become NOT RUN.
		fmt.Println("detector")
		fmt.Printf("  NOT RUN: %v\n", detErr)
		for _, line := range wrapDetail(modelNote, 84) {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}

	// --- 5. the two passes ------------------------------------------------------
	result, err := measureTwoPasses(ctx(), clip, win, policy, det, truth)
	if err != nil {
		return err
	}
	out := renderFootageReport(footageReport{
		Clip: clip, Label: label, Window: win, Read: result.Read,
		Policy: policy, Opts: opts, Input: inputSpec, Identity: identity,
		Whole: result.whole, Crop: result.crop, TruthState: truthState,
		DetErr: detErr, Regions: result.regions,
	})
	fmt.Print(out)
	if path := flags["out"]; path != "" {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n\n", path)
	}
	return nil
}

func asLicenceRefusal(err error, dst **licenceRefusal) bool {
	r, ok := err.(*licenceRefusal)
	if ok {
		*dst = r
	}
	return ok
}

func printFootageHeader(clip, state string) {
	fmt.Println("# footage measure")
	fmt.Println()
	fmt.Printf("  material    %s\n", clip)
	fmt.Printf("  %s\n", state)
}

// parseSeconds reads a seconds-valued flag, refusing anything that is not a
// finite non-negative number.
//
// A flag that silently resolved to zero is how `--from abc` becomes "the window
// starts at the beginning of the file", which measures material the operator did
// not ask about and reports it under their name.
func parseSeconds(cmd, flag, raw string) (time.Duration, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("%s: --%s has no value", cmd, flag)
	}
	var f float64
	if _, err := fmt.Sscan(v, &f); err != nil {
		return 0, fmt.Errorf("%s: --%s is %q, which is not a number of seconds. A flag that silently "+
			"resolved to zero would measure material you did not ask about and report it under your name",
			cmd, flag, raw)
	}
	if f != f || f > 1e9 {
		return 0, fmt.Errorf("%s: --%s is %q, which is not a time this module can address", cmd, flag, raw)
	}
	return time.Duration(f * float64(time.Second)), nil
}

// parseFraction reads a flag that must be a finite number.
func parseFraction(cmd, flag, raw string) (float64, error) {
	var f float64
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("%s: --%s has no value", cmd, flag)
	}
	if _, err := fmt.Sscan(v, &f); err != nil {
		return 0, fmt.Errorf("%s: --%s is %q, which is not a number", cmd, flag, raw)
	}
	if f != f {
		return 0, fmt.Errorf("%s: --%s is not a number", cmd, flag)
	}
	return f, nil
}

func parseInt(cmd, flag, raw string) (int, error) {
	var n int
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("%s: --%s has no value", cmd, flag)
	}
	if _, err := fmt.Sscan(v, &n); err != nil {
		return 0, fmt.Errorf("%s: --%s is %q, which is not a whole number", cmd, flag, raw)
	}
	return n, nil
}

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
	// Frames is every classFrame this setting produced.
	Frames []classFrame
	// Regions is how many regions this setting measured, crop pass only.
	Regions int
	// BelowMin and Deduped are the novelty candidates this setting's policy
	// removed before any inference.
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
	why := ref.What
	for _, have := range r.RefusalWhy {
		if have == why {
			return
		}
	}
	r.RefusalWhy = append(r.RefusalWhy, why)
}

// twoPassResult is both settings, the read that fed them, and the measured
// geometry of every region - the last of which needs no model at all.
type twoPassResult struct {
	Read    windowRead
	whole   regime
	crop    regime
	regions []regionSample
}

// regionSample is one region's measured geometry on real pixels.
//
// It is here, and it is measured even when no model can be opened, because the
// magnification factor is arithmetic and arithmetic does not need a session. It
// is the part of this command that CAN be reported on this tree's weights file,
// and reporting it is not the same as reporting a detection.
type regionSample struct {
	At     time.Duration
	Region proposalBox
	Bounds cropBounds
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
		return twoPassResult{}, fmt.Errorf("footage: the novelty engine that proposes crop regions could not be "+
			"built, so the crop pass has no regions and would report a comparison it never made: %w", err)
	}

	res := twoPassResult{
		whole: regime{Name: "whole-frame"},
		crop:  regime{Name: "crop"},
	}

	sink := func(fr sampledFrame) error {
		name := fmt.Sprintf("%s@%v", filepath.Base(clip), fr.At)
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
			ans := det.Detect(uint64(fr.Index), fr.RGB, fr.W, fr.H)
			if ans.Refusal != nil {
				wh.noteRefusal(ans.Refusal)
			}
			frWh.Dets = detectionsOf(name, ans)
		}
		wh.Frames = append(wh.Frames, frWh)

		// -- crop -------------------------------------------------------------
		cp := &res.crop
		plan := cropRegions(cands, policy)
		cp.BelowMin += plan.BelowMin
		cp.Deduped += plan.Dedupe
		frCp := classFrame{Frame: name, Truth: tg}
		if len(plan.Regions) == 0 {
			cp.SilentFrames++
			// The crop pass still records the frame. A frame with no region is a
			// frame where novelty proposed nothing, and recording it with no
			// detections is what makes "the crop pass found nothing" a measurement
			// over the same frames rather than a shorter run.
			cp.Frames = append(cp.Frames, frCp)
			return nil
		}
		var boxes []bench.Detection
		for i, r := range plan.Regions {
			cb, ok := cropBoundsOf(fr.W, fr.H, r)
			if !ok {
				continue
			}
			cp.Regions++
			cp.Inferences++
			res.regions = append(res.regions, regionSample{At: fr.At, Region: r, Bounds: cb})
			if det == nil {
				continue
			}
			cropRGB := cropPixels(fr.RGB, fr.W, fr.H, cb)
			// The sequence number carries the region index so two inferences of one
			// frame are two entries and a crop's cost is attributable to a crop.
			ans := det.Detect(uint64(fr.Index)*1000+uint64(i), cropRGB, cb.W, cb.H)
			if ans.Refusal != nil {
				cp.noteRefusal(ans.Refusal)
				continue
			}
			for _, b := range ans.Boxes {
				boxes = append(boxes, bench.Detection{Frame: name, Box: composeBox(b, cb, fr.W, fr.H)})
			}
		}
		frCp.Dets = boxes
		cp.Frames = append(cp.Frames, frCp)
		return nil
	}

	read, err := readWindowFrames(c, clip, win, win.Declared, sink)
	res.Read = read
	if err != nil {
		return res, err
	}
	return res, nil
}

// detectionsOf converts one answer into bench detections.
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

// footageReport is everything the renderer needs, gathered so the renderer holds
// no measurement state of its own.
type footageReport struct {
	Clip       string
	Label      footageLabel
	Window     footageWindow
	Read       windowRead
	Policy     cropPolicy
	Opts       bench.Options
	Input      ortdetect.InputSpec
	Identity   ortdetect.Identity
	Whole      regime
	Crop       regime
	TruthState string
	DetErr     error
	Regions    []regionSample
}

func renderFootageReport(r footageReport) string {
	var b strings.Builder

	b.WriteString("# the two passes over the same frames\n\n")
	fmt.Fprintf(&b, "  material      %s\n", r.Clip)
	fmt.Fprintf(&b, "  window        [%v, %v] every %v\n", r.Window.From, r.Window.To, r.Window.Every)
	fmt.Fprintf(&b, "  frames        %d declared, %d read, %dx%d, %.1f s of material\n",
		r.Read.Declared, r.Read.Read, r.Read.W, r.Read.H, r.Window.Seconds())
	if r.Read.Declared != r.Read.Read {
		fmt.Fprintf(&b, "                the reader returned %d frame(s) where %d were declared. Both are printed\n", r.Read.Read, r.Read.Declared)
		b.WriteString("                because a window that delivered fewer samples than it promised is a\n")
		b.WriteString("                different measurement from one that delivered exactly what it declared\n")
	}
	fmt.Fprintf(&b, "  decode        %s\n", r.Read.Command)
	b.WriteString("\n")

	renderArithmetic(&b, r)

	renderRegime(&b, "WHOLE-FRAME  one inference per frame", r.Whole)
	b.WriteString("\n")
	renderRegime(&b, "CROP  one inference per region, every box composed back onto the frame", r.Crop)

	wholeRows := partitionByClass(r.Whole.Frames, r.Opts)
	cropRows := partitionByClass(r.Crop.Frames, r.Opts)
	b.WriteString(CommandPerClassSideBySide(wholeRows, cropRows, r.Opts))
	b.WriteString("\n")

	renderNotMeasured(&b, r)
	return b.String()
}

// renderArithmetic is the part of this command that needs no model, and so the
// part that is always measurable.
func renderArithmetic(b *strings.Builder, r footageReport) {
	b.WriteString("crop arithmetic, on these frames, with no model involved\n")
	fmt.Fprintf(b, "  policy        min area %.4f of the frame; pad %.2f of a region's own size on each side,\n",
		r.Policy.MinArea, r.Policy.Pad)
	fmt.Fprintf(b, "                then clamped to the frame; dedupe IoU %.2f; a region %.0f%% inside the\n",
		r.Policy.DedupeIOU, r.Policy.CoveredFraction*100)
	fmt.Fprintf(b, "                accepted union is dropped; at most %d per frame\n", r.Policy.Max)
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
		// No session, so the model's declared input is unknown. 416 is the declared
		// input of the export this repository measures, and it is LABELLED as the
		// assumption it is rather than printed as a fact: the factors scale linearly
		// with it, so a reader with a different model can correct them in one
		// multiplication.
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
	fmt.Fprintf(b, "    %-8s %-21s %-13s %9s %8s %7s\n",
		"at", "region (normalised)", "pixels", "whole px", "crop px", "factor")
	for _, s := range r.Regions {
		whole, crop, factor := magnification(40, s.Bounds.W, s.Bounds.H, r.Read.W, r.Read.H, input)
		fmt.Fprintf(b, "    %-8s %-21s %-13s %9.1f %8.1f %7.1f\n",
			shortDuration(s.At), shortBox(s.Region), s.Bounds.String(), whole, crop, factor)
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
// Every absence is named and none of them is rendered as a zero. A table of zeros
// over a measurement that did not run is worse than no table: it reads as a
// detector that finds nothing rather than as a detector that was never asked.
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
		b.WriteString("    divided by detections, and there were none of either: a zero here would be a\n")
		b.WriteString("    claim that every detection was wrong when there were no detections at all.\n\n")
	case r.Whole.Refusals+r.Crop.Refusals > 0:
		total := r.Whole.Inferences + r.Crop.Inferences
		fmt.Fprintf(b, "  - per-class precision and recall, for BOTH settings: NOT RUN.\n")
		fmt.Fprintf(b, "    The decoder refused %d of %d inference(s). A refusal is a named reason and not a\n",
			r.Whole.Refusals+r.Crop.Refusals, total)
		b.WriteString("    zero, and a table computed over refusals would be a table about a decoder that\n")
		b.WriteString("    could not read the model. Its reason, verbatim:\n\n")
		for _, line := range wrapDetail(r.Whole.FirstRefusal.Error(), 80) {
			fmt.Fprintf(b, "      %s\n", line)
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
		b.WriteString("    guess with a file name on it. Supply --truth, or mark the boxes with\n")
		b.WriteString("    `pocctl label`, and this becomes a measurement.\n\n")
	}

	b.WriteString("  - false positives per hour: NOT RUN.\n")
	b.WriteString("    A per-hour rate needs a duration and a clock the benchmark does not own, and\n")
	b.WriteString("    a zero here would be an encouraging infinity rather than a measurement.\n\n")

	b.WriteString("  - any class this build cannot name: counted as DROPPED, never as a miss.\n")
	b.WriteString("    That is a limit of the vocabulary and not of the detector, and a recall quoted\n")
	b.WriteString("    without the drop count beside it is a claim about the vocabulary.\n")
}

func shortBox(r proposalBox) string {
	return fmt.Sprintf("%.3f %.3f %.3f %.3f", r.X1, r.Y1, r.X2, r.Y2)
}

func shortDuration(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

// distinctRegionFrames counts the frames that contributed at least one region.
//
// It counts DISTINCT instants rather than regions, because the row beside it
// reads "N measured on their own, over M frames" and a reader comparing the two
// numbers is checking whether the cap is doing anything.
func distinctRegionFrames(samples []regionSample) int {
	seen := map[time.Duration]bool{}
	for _, s := range samples {
		seen[s.At] = true
	}
	return len(seen)
}
