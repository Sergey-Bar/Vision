package main

// `bench images` — drive the detector over a labelled still set and measure it.
//
// # This is the half of the benchmark that holds a session, and it is separated
// from the metrics for a reason that is about the numbers rather than about tidiness.
//
// `internal/bench/metrics.go` is pure: it imports no store, no pipeline and no
// detector, so a test can check it against hand-computed arithmetic. This file
// holds an ONNX Runtime session, fetches images, and hands boxes to the pure half.
//
// The separation is what makes RC-16 possible — a red check that removes the
// detector's validity gate and watches the benchmark report a DIFFERENT number
// proves the benchmark is measuring the detector rather than restating its own
// input.
//
// # Privileges, and why they differ between entry points
//
// `bench images` HOLDS A SESSION, and that is legitimate: it is driving the
// detector over the still set, which is the thing being measured.
//
// `bench run`, over a recorded run, must NOT. It reads the store and computes
// metrics from what the run already recorded. A benchmark over a finished run that
// opened a model would be measuring the model rather than the run, and the two
// disagree whenever the run used different settings — which is most of the time.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/bench"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

func cmdBench(args []string) error {
	flags, err := flagSet("bench", map[string]string{
		"images": "", "run": "", "data-root": "", "zones": "",
		"model": "", "ort": "", "manifest": groundtruthManifest,
		"match-iou": "0.5", "threads": "0", "emit": "0.05",
		"nms-class": "0.45", "nms-global": "0.75", "max-boxes": "100",
		"min-score": "0", "out": "", "frames": "",
	}, args)
	if err != nil {
		return err
	}
	switch {
	case flags["images"] != "":
		return benchImages(flags)
	case flags["run"] != "":
		return benchRun(flags)
	}
	return fmt.Errorf("bench: pass --images <dir> to drive the detector over a still set, or --run <id> " +
		"to measure a finished run. They are different measurements with different privileges: " +
		"--images holds a detector session, --run does not and must not")
}

// refusesToOpenModel is RC-17's subject, and it is a FUNCTION so a test can call
// it.
//
// It was a bare `if` condition once, and the red check that removes it went GREEN:
// a control with no test behind it is not a control, it is a comment. That is the
// whole reason the gate exists and this is the first time it has caught the gate's
// own table rather than the code.
//
// The predicate reads the ENVIRONMENT as well as the flags, because the flags are
// the less likely route: an operator with SP_POC_ORT_LIB set in their shell and a
// run measurement requested would otherwise get a model opened behind their back.
func refusesToOpenModel(lib, model string) bool {
	return strings.TrimSpace(envOr("SP_POC_ORT_LIB", lib)) != "" ||
		strings.TrimSpace(model) != ""
}

// benchImages drives the detector over the still set.
//
// The refusal at the top is the important part: no manifest means no licence was
// recorded, and running a benchmark over material whose terms nobody wrote down
// produces numbers this project may not publish.
func benchImages(flags map[string]string) error {
	dir := flags["images"]
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("bench: the still set is not readable: %w", err)
	}
	man, err := readGroundtruthManifest(flags["manifest"])
	if err != nil {
		return fmt.Errorf("bench: %w\n\n  Without a manifest there is no record of where this material "+
			"came from or under what licence, and a benchmark over material nobody may publish "+
			"produces numbers nobody may show. Run `pocctl fetch-groundtruth` first.", err)
	}
	if man.ImageLicence == "" || man.AnnotationLicence == "" {
		return fmt.Errorf("bench: the manifest records no licence for %s. Refusing to measure it.", dir)
	}

	// The session. A model that cannot be opened is a refusal by name, and it is
	// made here rather than discovered per image.
	//
	// The four settings are resolved ONCE into locals and reused below, because
	// the session's identity and the calibration record that describes it have
	// to be the same four values. Two calls to envOr with different defaults is
	// how a record ends up describing a build nobody ran.
	benchLib := envOr("SP_POC_ORT_LIB", flags["ort"])
	benchModel := envOr("SP_POC_DET_MODEL", flags["model"])
	benchThreads := envInt("SP_POC_ORT_THREADS", flags["threads"], 4)
	benchEP := envOr("SP_POC_ORT_EP", "cpu")
	sess, err := ortdetect.Open(ortdetect.SessionConfig{
		LibraryPath: benchLib,
		ModelPath:   benchModel,
		Threads:     benchThreads,
		// A benchmark that ran on a different device than it printed is worse than no
		// benchmark, so the bench takes the provider from the same variables the run
		// does rather than defaulting. A typo is refused by name rather than treated
		// as the CPU.
		ExecutionProvider: benchEP,
		CUDADeviceID:      envInt("SP_POC_ORT_CUDA_DEVICE", "", 0),
	})
	if err != nil {
		return fmt.Errorf("bench: %w", err)
	}
	defer func() { _ = sess.Close() }()

	in := sess.Input()
	det, err := ortdetect.NewDetector(ortdetect.Config{
		Session: sess,
		Decode: ortdetect.DecodeConfig{
			EmitScore: envFloat("SP_POC_DET_SCORE", flags["emit"], 0.05),
			MaxBoxes:  envInt("SP_POC_DET_MAX_BOXES", flags["max-boxes"], 100),
			NMSClass:  envFloat("SP_POC_DET_NMS_CLASS", flags["nms-class"], 0.45),
			NMSGlobal: envFloat("SP_POC_DET_NMS_GLOBAL", flags["nms-global"], 0.75),
		},
		InputSize: in.W,
	})
	if err != nil {
		return fmt.Errorf("bench: %w", err)
	}

	fmt.Printf("bench: %s\n", dir)
	fmt.Printf("  model        %s\n", filepath.Base(sess.Identity().ModelPath))
	fmt.Printf("  sha256       %s\n", sess.Identity().SHA256)
	fmt.Printf("  licence      %s\n", licenceOrUnrecorded(sess.Identity().Licence))
	fmt.Printf("  input        %s %dx%d\n", in.Layout, in.W, in.H)
	fmt.Printf("  match IoU    %s\n", flags["match-iou"])
	fmt.Println()

	opts := bench.DefaultOptions()
	fmt.Sscan(flags["match-iou"], &opts.MatchThreshold)
	opts.ClassThreshold = envFloat("SP_POC_SCORE_THRESHOLD", flags["min-score"], 0)

	images, err := loadStillSet(dir, man)
	if err != nil {
		return err
	}
	if len(images) == 0 {
		return fmt.Errorf("bench: %s holds no images the manifest lists. The still set and the "+
			"annotations are out of step, and measuring one against the other would report a "+
			"recall over nothing", dir)
	}

	var frames []bench.FrameCount
	droppedByClass := map[string]int{}
	droppedFrames := 0
	negativeFrames := 0
	refusedFrames := 0

	for _, img := range images {
		rgb, w, h, err := readImageRGB(img.Path)
		if err != nil {
			return fmt.Errorf("bench: %s: %w", img.Path, err)
		}
		ans := det.Detect(0, rgb, w, h, ortdetect.Rect{})
		// A REFUSED frame is not a negative one, and the difference is the whole
		// value of this benchmark. `Detect` returns its refusal inside the Answer
		// rather than as an error, so a loop that only looks at len(ans.Boxes)
		// reads a frame the decoder could not decode as a frame where it found
		// nothing. The classification, and the control behind it, live in
		// bench_frames.go next to the test that fails when it is removed.
		outcome, line := classifyAnswer(ans)
		if outcome == OutcomeRefused {
			refusedFrames++
			fmt.Printf("  %-28s REFUSED, not measured: %s\n", img.Name, line)
			continue
		}
		if outcome == OutcomeNegative {
			// A negative frame with nothing drawn is the ONE combination that is
			// unambiguously right, and it is worth saying out loud per image so a
			// reader does not have to infer it from a row of zeros.
			negativeFrames++
			if len(img.Boxes) == 0 {
				fmt.Printf("  %-28s negative frame, nothing drawn, nothing there\n", img.Name)
				continue
			}
			// Nothing drawn and something there: the miss is measured by the
			// frame's own metrics below, not swallowed as a negative.
		}
		dets := make([]bench.Detection, 0, len(ans.Boxes))
		for _, b := range ans.Boxes {
			dets = append(dets, bench.Detection{Frame: img.Name, Box: b})
		}
		truth := make([]bench.GT, 0, len(img.Boxes))
		for _, gb := range img.Boxes {
			truth = append(truth, bench.GT{
				Box:         bench.Box{Class: gb.Class, X1: gb.X1, Y1: gb.Y1, X2: gb.X2},
				Class:       gb.Class,
				Expressible: true,
			})
		}
		for _, d := range img.DroppedClasses {
			droppedByClass[d]++
		}
		if len(img.DroppedClasses) > 0 {
			droppedFrames++
		}
		if len(truth) == 0 {
			negativeFrames++
		}
		fc := bench.Measure(img.Name, dets, truth, opts)
		frames = append(frames, fc)
		fmt.Printf("  %-28s gt %-4d det %-4d tp %-4d fp %-4d fn %-4d dropped %d\n",
			img.Name, fc.GroundTruth, fc.Detected, fc.TruePositives,
			fc.FalsePositives, fc.FalseNegatives, fc.Dropped)
	}

	// The duration. A still set has none, so the per-hour rate is NOT RUN — and
	// saying so is the whole point of computing it over a recorded run instead.
	rep := bench.Accumulate(frames, 0, droppedByClass, droppedFrames)
	out := bench.Format(rep, opts)
	fmt.Println()
	fmt.Print(out)
	if refusedFrames > 0 {
		// The whole set refused, or some of it did, and the metrics above are
		// over the frames that were measured. A benchmark whose subject refused
		// is not a benchmark: it is a timing pass with a metrics display, and
		// the line has to say so rather than let a reader divide by a
		// denominator the decoder never produced.
		fmt.Printf(`
  %d of %d frame(s) were REFUSED and are NOT counted in the metrics above: a frame
  the detector could not decode produced no boxes, and zero from a refusal is not
  zero from a search. The metrics here are over the %d frame(s) that were measured.
  The refusal is named per image above.

`, refusedFrames, len(images), len(images)-refusedFrames)
	}

	// The achievable rate, and the calibration record that carries it.
	//
	// Two passes, and they measure different things. The metrics pass above drove
	// the detector once per image, so its latency window holds one sample per
	// still — and on a GPU that first sample is the COLD one: measured on this
	// machine at 600.9ms for a kernel that costs about 12ms once cuDNN has loaded
	// and chosen its algorithms. A p99 over a sample set whose members are all
	// cold starts is a gate that refuses every run on a machine that works, so
	// the calibration is taken over a repeated pass and its first sample is
	// DISCARDED rather than averaged away.
	//
	// 0 means "not asked for", and it is refused rather than read as a small
	// number: `pocctl bench --frames 0` would otherwise write a calibration with
	// no samples in it and preflight would refuse for a reason nobody caused.
	timedFrames := envInt("SP_POC_BENCH_FRAMES", flags["frames"], defaultBenchFrames)
	if flags["frames"] != "" {
		n, err := strconv.Atoi(flags["frames"])
		if err != nil || n < 0 {
			return fmt.Errorf("bench: --frames %q is not a count. 0 means no timing pass; anything "+
				"above it is how many repeated inferences the calibration is taken over", flags["frames"])
		}
		timedFrames = n
	}
	if timedFrames > 0 {
		if len(images) == 0 {
			return fmt.Errorf("bench: %s holds no images the manifest lists, so there is nothing to time", dir)
		}
		cal, line, err := timeDetector(det, images[0], timedFrames, benchModel, benchLib, benchThreads, benchEP, sess.Identity().SHA256)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Print(line)
		root := flags["data-root"]
		if root == "" {
			root = strings.TrimSpace(os.Getenv("SP_POC_DATA_ROOT"))
		}
		if root == "" {
			fmt.Println("  no --data-root and no SP_POC_DATA_ROOT, so the calibration was NOT recorded.")
			fmt.Println("  `pocctl preflight`'s detector row will refuse until it is: re-run with")
			fmt.Println("  --data-root <the run's data root>.")
		} else if err := soak.WriteCalibration(root, cal); err != nil {
			return err
		} else {
			fmt.Printf("  calibration recorded at %s — `pocctl preflight` reads it for the detector row.\n",
				soak.CalibrationPath(root))
		}
	}
	if flags["out"] != "" {
		if err := writeBenchReport(flags["out"], rep, opts, sess.Identity()); err != nil {
			return err
		}
		fmt.Printf("\nwrote %s\n", flags["out"])
	}
	if negativeFrames == 0 {
		fmt.Println("\n  NO NEGATIVE FRAMES were measured. The false-positive count above is")
		fmt.Println("  therefore only the ones drawn on images that DID contain an object, which is")
		fmt.Println("  a different and much smaller number than the false-positive rate. Use")
		fmt.Println("  `pocctl bench --run` for the per-hour rate over real footage.")
	}
	return nil
}

// defaultBenchFrames is the timing pass's length when nothing says otherwise.
//
// Forty samples after a discarded warm-up gives a nearest-rank p99 at the
// fortieth order statistic, which is the second slowest inference rather than
// the single worst one. That is a number a frame rate can be planned against.
const defaultBenchFrames = 40

// timeDetector drives the detector over one still `count+1` times and returns
// the calibration those samples describe, plus the lines that print it.
//
// The first inference is DISCARDED, not averaged and not reported. On a CUDA
// session it is not an inference at all in any useful sense: it is cuDNN loading
// and choosing its algorithms, measured here at 600.9ms against a steady-state
// cost of about 12ms. Keeping it would write a calibration whose p99 refuses a
// detector that demonstrably runs; dropping it silently would describe a
// machine nobody benched. So it is dropped AND said so, in the output and in the
// record.
func timeDetector(det *ortdetect.Detector, img stillImage, count int,
	model, lib string, threads int, ep, sha string) (soak.DetectorCalibration, string, error) {
	rgb, w, h, err := readImageRGB(img.Path)
	if err != nil {
		return soak.DetectorCalibration{}, "", fmt.Errorf("bench: %s: %w", img.Path, err)
	}
	total := count + 1
	lat := ortdetect.NewLatencies(total)
	var p50, p99 float64
	var fps float64
	for i := 0; i < total; i++ {
		start := time.Now()
		_ = det.Detect(0, rgb, w, h, ortdetect.Rect{})
		ms := float64(time.Since(start).Microseconds()) / 1000.0
		if i == 0 {
			// The warm-up: spent on cuDNN's load and algorithm selection on a
			// CUDA session, and on the first-touch cost of everything on a CPU
			// one. Kept out of the window and out of the record.
			continue
		}
		lat.Observe(ms)
	}
	if v, ok := lat.P50(); ok {
		p50 = v
	}
	if v, ok := lat.P99(); ok {
		p99 = v
	}
	if v, ok := lat.AchievableFPS(); ok {
		fps = v
	}
	cal := soak.DetectorCalibration{
		Model:             model,
		ModelSHA256:       sha,
		Library:           lib,
		Threads:           threads,
		Input:             det.Session().Input().W,
		ExecutionProvider: ep,
		P50MS:             p50,
		P99MS:             p99,
		Frames:            lat.Count(),
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  timing pass     %d inferences of %s, first one discarded as the warm-up\n",
		lat.Count(), img.Name)
	fmt.Fprintf(&b, "                  p50 %.1fms  p99 %.1fms  achievable %.1f fps at %d threads, %s\n",
		p50, p99, fps, threads, ep)
	return cal, b.String(), nil
}

// stillImage is one image the manifest knows about, on disk.
type stillImage struct {
	Name           string
	Path           string
	Boxes          []GTBox
	DroppedClasses []string
}

// loadStillSet pairs the manifest's entries with files on disk.
//
// It is not a directory listing: the manifest is the authority on WHAT is in the
// set and a stray file somebody copied in is not part of it. Measuring a file the
// manifest does not list would put a box in the report with no ground truth behind
// it, and every such box counts as a false positive.
func loadStillSet(dir string, man *GroundtruthManifest) ([]stillImage, error) {
	out := make([]stillImage, 0, len(man.Images))
	for _, im := range man.Images {
		path := filepath.Join(dir, filepath.Base(im.Filename))
		if _, err := os.Stat(path); err != nil {
			// A declared image that is not on disk is a skipped frame, and it is
			// SKIPPED rather than counted: the manifest says the set contains it, and
			// the set does not. Silently measuring the rest would report a recall over
			// a set smaller than the one anybody read about.
			fmt.Printf("  (skipping %s: the manifest lists it and the disk does not have it)\n", im.ID)
			continue
		}
		out = append(out, stillImage{
			Name: im.ID, Path: path, Boxes: im.Boxes, DroppedClasses: im.DroppedClasses,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readGroundtruthManifest reads the provenance file.
func readGroundtruthManifest(path string) (*GroundtruthManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the ground-truth manifest is not readable: %w", err)
	}
	var man GroundtruthManifest
	if err := jsonUnmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("%s is not readable as a ground-truth manifest: %w", path, err)
	}
	return &man, nil
}

// writeBenchReport records the measurement beside the material it measured, so a
// number and its inputs travel together.
func writeBenchReport(path string, rep bench.Report, opts bench.Options, id ortdetect.Identity) error {
	payload := map[string]any{
		"measured_at":   time.Now().UTC().Format(time.RFC3339),
		"model_path":    id.ModelPath,
		"model_sha256":  id.SHA256,
		"model_licence": id.Licence,
		"match_iou":     opts.MatchThreshold,
		"counts":        rep.Counts,
		"dropped":       rep.DroppedByClass,
		"report":        bench.Format(rep, opts),
	}
	raw, err := jsonMarshalIndent(payload)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// benchRun measures a FINISHED run, and it does not open a model.
//
// The no-session property is structural rather than a matter of discipline: this
// function takes a store and a run id and has no path to a runtime library at all,
// so it cannot call one even by accident. A benchmark over a recorded run that
// opened a model would be measuring the model rather than the run, and the two
// disagree whenever the run used different settings — which is most of the time.
func benchRun(flags map[string]string) error {
	if refusesToOpenModel(flags["ort"], flags["model"]) {
		fmt.Println("bench: refusing to open a model for a run measurement.")
		fmt.Println("  A benchmark over a FINISHED run measures what the run recorded. Re-decoding")
		fmt.Println("  the material with a model here would measure the model instead, and the two")
		fmt.Println("  disagree whenever the run used different settings — which is most of the time.")
		fmt.Println("  The flags are ignored rather than honoured.")
	}
	dataRoot := flags["data-root"]
	if dataRoot == "" {
		dataRoot = filepath.Join(".", ".demo-data")
	}
	st, err := store.Open(dataRoot)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	rep, err := benchRecordedRun(context.Background(), st, flags["run"])
	if err != nil {
		return err
	}
	out := bench.Format(rep.Report, rep.Options)
	fmt.Print(out)
	if flags["out"] != "" {
		if err := writeBenchReport(flags["out"], rep.Report, rep.Options, ortdetect.Identity{}); err != nil {
			return err
		}
		fmt.Printf("\nwrote %s\n", flags["out"])
	}
	return nil
}

// unusedConfigGuard keeps the import honest for the config vocabulary this file
// names when it refuses a mode.
var _ = config.TriggerModeDetector
