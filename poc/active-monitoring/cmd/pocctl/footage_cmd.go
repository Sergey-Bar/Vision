package main

// cmdFootage: measure the detector over real material, behind a licence gate.
//
// # The shape of the command
//
//	pocctl footage --clip FILE [--labels testdata/labels.json]
//	                [--at HH:MM:SS] [--for 30m] [--every 1s]
//	                [--external] [--truth boxes.json]
//
// `--external` is the one that matters. Without it the numbers are for this machine;
// WITH it, the command additionally refuses any material whose provenance row does not
// say it may be shown to somebody else. A number printed on a laptop is a local claim.
// A number in a submission is an external act, and the tool should know the difference
// rather than the operator remembering it.
//
// # What it refuses, and why the refusals are the feature
//
//   - no provenance row: nothing recorded, so nothing quotable
//   - `licence` empty or absent: an absent field is an unrecorded fact
//   - `licence` reading NOT RECORDED: a real state, and the one that blocks
//   - `cleared_for_external_use` absent: "permissive" and "nobody decided" are different
//   - a base name matching two rows with different licences: refused, not guessed
//   - a sampling interval longer than the smallest dwell: the table would carry a zero
//     for the small object and the zero would read as a detector limitation
//
// # What it measures, and what that measurement can and cannot say
//
// It samples the declared window with ONE ffmpeg process and runs the detector twice
// over the SAME frames: whole-frame, and one inference per region the novelty engine
// proposed, every box mapped back onto the frame. Both settings get a per-class
// precision and a recall, side by side, and the disclaimer that this is not an mAP is
// on the table.
//
// It does not fetch. Fetching is a separate question with a separate answer per source,
// and a tool that both fetches and measures makes this gate optional by accident: it is
// one argument away from being bypassed by the fetch.
//
// # What it does NOT do, and says so on its face
//
// Arbitrary footage has no ground truth. `testdata/labels.json` records provenance, a
// line and an expected time window; it records no object boxes, and no box may be
// invented from a picture. So unless `--truth` supplies boxes a human drew, recall has
// no denominator and precision has nothing to be right or wrong about, and BOTH print
// `NOT RUN` with the reason. They never print a zero.
//
// The decoder in this build reads fused, single-output exports. An anchored multi-scale
// export is refused BY NAME, and the refusal is reported as the measurement result it
// is rather than as an empty table: what the crop pass finds is UNKNOWN, which is not
// zero and not worse.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
)

func footageUsage() {
	fmt.Println(`pocctl footage - measure the detector over material you have the right to use

USAGE
  pocctl footage --clip FILE [flags]

FLAGS
  --clip PATH      the video to measure. Required.
  --labels PATH    the provenance table. Default testdata/labels.json
  --at OFFSET      where to start. MM:SS, HH:MM:SS, or seconds. Default 0
  --for DURATION   how long to measure. The same spellings. Default 30m
  --every INTERVAL how often to sample. Default 1s
  --external       the numbers are for somebody else, so refuse anything not cleared
                   to be shown outside this machine
  --truth PATH     ground-truth object boxes, drawn by a person. WITHOUT one, per-class
                   precision and recall print NOT RUN with the reason: this repository
                   has no ground-truth labels for arbitrary footage and this command
                   will not invent any. A box drawn by looking at the picture is a guess
                   with a file name on it
  --out PATH       also write the report to a file
  --models DIR     where yolov4n.onnx and onnxruntime.dll live. Default models
  --match-iou N    the IoU at which a detection matches a drawn box. Default 0.5
  --crop-min-area  smallest region worth its own inference, as a fraction of the frame
  --crop-pad       context added around a region, as a fraction of the region's size
  --crop-max       most regions one frame may contribute

WHY IT REFUSES
  Every number this command prints is derived from somebody's camera. A number whose
  input nobody can account for is a number nobody may quote, so the gate is in the
  TOOL rather than in a README: material with no provenance row, an absent or
  NOT RECORDED licence, or an absent cleared_for_external_use flag is refused by name.

  It also refuses a sampling interval longer than ` + fmt.Sprintf("%.1f", NoveltyDwellSeconds) + `s. An
  object on screen for that long cannot appear in frames further apart, and the
  resulting table would carry a zero for the smallest thing this system exists to
  report, which reads as a detector limitation rather than a sampling plan that
  never looked.

WHAT IT PRINTS AND WHAT IT DOES NOT
  Two rates at one operating point, for two settings, on the same frames. Not an mAP:
  there is no confidence sweep and no interpolated curve in this module.

  Whatever it could not measure prints NOT RUN with the reason, and never a zero. That
  includes the case that matters most here: this build's decoder reads fused exports
  only, so an anchored multi-scale export is refused by name and the crop pass's
  result is reported as UNKNOWN rather than as a detector that found nothing.`)
}

func cmdFootage(args []string) error {
	flags, err := flagSet("footage", map[string]string{
		"clip": "", "labels": "", "at": "0", "for": "30m", "every": "1s",
		"external": boolSpec,
		// The measurement's own flags. Every one of them has a default that reproduces
		// the pipeline's own setting, so a run with no flags after --clip is a run over
		// the material the module was tuned on.
		"match-iou":     "0.5",
		"min-score":     envOr("SP_POC_SCORE_THRESHOLD", "0"),
		"truth":         "",
		"out":           "",
		"models":        "models",
		"model":         "",
		"ort":           "",
		"ep":            envOr("SP_POC_ORT_EP", "cpu"),
		"cuda-device":   envOr("SP_POC_ORT_CUDA_DEVICE", "0"),
		"threads":       envOr("SP_POC_ORT_THREADS", "4"),
		"emit":          envOr("SP_POC_DET_SCORE", "0.05"),
		"max-boxes":     envOr("SP_POC_DET_MAX_BOXES", "100"),
		"nms-class":     "0.45",
		"nms-global":    "0.75",
		"crop-min-area": "",
		"crop-pad":      "",
		"crop-max":      "",
	}, args)
	if err != nil {
		footageUsage()
		return err
	}
	clip := strings.TrimSpace(flags["clip"])
	if clip == "" {
		footageUsage()
		return fmt.Errorf("footage: --clip is required. This command measures material you already have; " +
			"it does not fetch, because fetching is a separate question with a separate answer per source")
	}

	// 1. THE GATE, before anything is read from the video. Refusing first means a
	// rejected file is never opened, so nothing about it is even decoded.
	labelsPath := strings.TrimSpace(flags["labels"])
	if labelsPath == "" {
		found, ferr := findLabelsTable()
		if ferr != nil {
			return ferr
		}
		labelsPath = found
	}
	labels, err := loadLabels(labelsPath)
	if err != nil {
		return err
	}
	label, err := labelFor(labels, clip)
	if err != nil {
		return err
	}
	if err := refuseUncleared(clip, label, flags["external"] == "true"); err != nil {
		return err
	}

	// 2. The window, before the file is opened, so a bad interval is reported without
	// paying for a decode.
	win, err := parseFootageWindow(flags["at"], flags["for"], flags["every"])
	if err != nil {
		return err
	}

	// 3. The file itself.
	st, err := os.Stat(clip)
	if err != nil {
		return fmt.Errorf("footage: cannot open %s: %w", clip, err)
	}
	if st.IsDir() {
		return fmt.Errorf("footage: %s is a directory, not a clip", clip)
	}
	if st.Size() == 0 {
		return fmt.Errorf("footage: %s is empty. A zero-byte file has no frames, and every number over "+
			"it would be an average over nothing", clip)
	}

	// The licence is cleared and the window is well-formed. Everything up to here can be
	// reported without having touched the material.
	fmt.Printf("footage: the gate passed\n")
	fmt.Printf("  %s\n", clip)
	fmt.Printf("  provenance table %s\n", labelsPath)
	fmt.Printf("  provenance %s, retrieved %s\n", orNone(label.Provenance, "(not recorded)"),
		orNone(label.Retrieved, "(not recorded)"))
	fmt.Printf("  licence %s\n", summariseLicence(label.Licence))
	fmt.Printf("  cleared for external use: %s\n", clearedWord(label))
	fmt.Printf("  contains people: %s\n", label.ContainsPe.Word())
	fmt.Printf("  window %s\n", win.describeWindow())
	fmt.Println()

	// 4. THE MEASUREMENT. This is the piece the gate was holding the door for.
	//
	// Everything above is the gate and the window, both of which can be reported
	// without decoding a pixel. From here the command samples the declared window with
	// ONE ffmpeg process, runs the detector twice over the SAME frames, and prints the
	// two settings side by side.
	//
	// It does not fail when the model cannot be opened. A missing decoder is a fact
	// about the decoder, and a command that refuses to report it would replace a named
	// absence with a stack trace. The frame count, the crop geometry and the
	// magnification arithmetic are all measurable without a model, so they are measured,
	// and only the detection columns become NOT RUN.
	truth, truthErr := readTruthFile(strings.TrimSpace(flags["truth"]))
	fmt.Printf("  ground truth %s\n", describeTruth(truth, truthErr, win))
	fmt.Println()

	opts := bench.DefaultOptions()
	if v := strings.TrimSpace(flags["match-iou"]); v != "" {
		f, ferr := parseClock(v)
		if ferr != nil {
			return fmt.Errorf("footage: --match-iou is %q, which is not a number. A match threshold that "+
				"silently resolved to the default would be a threshold the reader cannot see", v)
		}
		opts.MatchThreshold = f
	}
	opts.ClassThreshold = envFloat("SP_POC_SCORE_THRESHOLD", flags["min-score"], 0)

	policy := defaultCropPolicy()
	if v := strings.TrimSpace(flags["crop-min-area"]); v != "" {
		f, ferr := parseClock(v)
		if ferr != nil {
			return fmt.Errorf("footage: --crop-min-area is %q, which is not a number", v)
		}
		policy.MinArea = f
	}
	if v := strings.TrimSpace(flags["crop-pad"]); v != "" {
		f, ferr := parseClock(v)
		if ferr != nil {
			return fmt.Errorf("footage: --crop-pad is %q, which is not a number", v)
		}
		policy.Pad = f
	}
	if v := strings.TrimSpace(flags["crop-max"]); v != "" {
		n, nerr := strconv.Atoi(v)
		if nerr != nil {
			return fmt.Errorf("footage: --crop-max is %q, which is not a whole number", v)
		}
		policy.Max = n
	}
	if perr := policy.Validate(); perr != nil {
		return perr
	}

	det, modelNote, detErr := openFootageDetector(flags, opts)
	var identity ortdetect.Identity
	var inputSpec ortdetect.InputSpec
	if det != nil {
		identity, inputSpec = det.Identity(), det.Input()
		fmt.Printf("  detector %s, sha256 %s\n", filepath.Base(identity.ModelPath), identity.SHA256)
		fmt.Printf("           licence %s, input %s %dx%d, provider %s\n",
			licenceOrUnrecorded(identity.Licence), inputSpec.Layout, inputSpec.W, inputSpec.H, flags["ep"])
	} else {
		fmt.Printf("  detector NOT RUN: %v\n", detErr)
		fmt.Printf("           %s\n", modelNote)
	}
	fmt.Println()

	result, err := measureTwoPasses(ctx(), clip, win, policy, det, truth)
	if err != nil {
		return err
	}
	out := renderFootageReport(footageReport{
		Clip: clip, Label: label, Window: win, Read: result.Read,
		Policy: policy, Opts: opts, Input: inputSpec, Identity: identity,
		Whole: result.whole, Crop: result.crop, TruthState: describeTruth(truth, truthErr, win),
		DetErr: detErr, Regions: result.regions, TruthBoxes: truth,
	})
	fmt.Print(out)
	if path := strings.TrimSpace(flags["out"]); path != "" {
		if werr := os.WriteFile(path, []byte(out), 0o644); werr != nil {
			return werr
		}
		fmt.Printf("wrote %s\n", path)
	}
	return nil
}

// findLabelsTable looks for the provenance table and says WHERE it found it.
//
// It searches rather than guessing one relative path, because every other subcommand in
// this binary is invoked from the module root while the binary itself lives one level
// down, and a hard-coded guess is wrong for half the invocations. It PRINTS the path it
// used: a gate that silently read a different table than the one the operator had in
// mind is a gate that gates nothing.
func findLabelsTable() (string, error) {
	var tried []string
	for _, cand := range []string{
		filepath.Join("testdata", "labels.json"),
		filepath.Join("..", "testdata", "labels.json"),
		filepath.Join("..", "..", "testdata", "labels.json"),
	} {
		tried = append(tried, cand)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("footage: no provenance table found. Looked in %s. Pass --labels with the "+
		"path, and note that this command REFUSES material with no row: a table it cannot find is a "+
		"gate that cannot run", strings.Join(tried, ", "))
}

func parseFootageWindow(at, for_, every string) (footageWindow, error) {
	a, err := parseClock(at)
	if err != nil {
		return footageWindow{}, err
	}
	f, err := parseClock(for_)
	if err != nil {
		return footageWindow{}, err
	}
	e, err := parseClock(every)
	if err != nil {
		return footageWindow{}, err
	}
	w := footageWindow{At: a, For: f, Every: e}
	return w, w.validate()
}

// summariseLicence shortens a licence field for the one line above.
//
// It prints the FIRST SENTENCE and says how much it cut, because a licence paragraph
// on a status line is unreadable and a shortened licence that does not say it was
// shortened is a quotation nobody can check.
func summariseLicence(l string) string {
	l = strings.TrimSpace(l)
	if l == "" {
		return "NOT RECORDED (the field is empty, which is not the same as permissive)"
	}
	if u := upper(l); strings.HasPrefix(u, "NOT RECORDED") {
		return "NOT RECORDED"
	}
	sentence := l
	if i := strings.IndexAny(sentence, ".!?"); i > 0 {
		sentence = sentence[:i+1]
	}
	if rest := len(l) - len(sentence); rest > 0 {
		return fmt.Sprintf("%s (%d more character(s) in the table)", sentence, rest)
	}
	return sentence
}

func clearedWord(c clipLabel) string {
	cleared, known := c.clearedForExternalUse()
	switch {
	case !known:
		return "NOT RECORDED (the field is absent)"
	case cleared:
		return "yes"
	default:
		return "no"
	}
}
