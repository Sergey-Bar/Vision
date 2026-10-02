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
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
	"github.com/Sergey-Bar/ai-captain-poc/internal/config"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

func cmdBench(args []string) error {
	flags, err := flagSet("bench", map[string]string{
		"images": "", "run": "", "data-root": "", "zones": "",
		"model": "", "ort": "", "manifest": groundtruthManifest,
		"match-iou": "0.5", "threads": "0", "emit": "0.05",
		"nms-class": "0.45", "nms-global": "0.75", "max-boxes": "100",
		"min-score": "0", "out": "",
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
	sess, err := ortdetect.Open(ortdetect.SessionConfig{
		LibraryPath: envOr("SP_POC_ORT_LIB", flags["ort"]),
		ModelPath:   envOr("SP_POC_DET_MODEL", flags["model"]),
		Threads:     envInt("SP_POC_ORT_THREADS", flags["threads"], 4),
		// A benchmark that ran on a different device than it printed is worse than no
		// benchmark, so the bench takes the provider from the same variables the run
		// does rather than defaulting. A typo is refused by name rather than treated
		// as the CPU.
		ExecutionProvider: envOr("SP_POC_ORT_EP", "cpu"),
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

	for _, img := range images {
		rgb, w, h, err := readImageRGB(img.Path)
		if err != nil {
			return fmt.Errorf("bench: %s: %w", img.Path, err)
		}
		ans := det.Detect(0, rgb, w, h)
		dets := make([]bench.Detection, 0, len(ans.Boxes))
		for _, b := range ans.Boxes {
			dets = append(dets, bench.Detection{Frame: img.Name, Box: b})
		}
		truth := make([]bench.GT, 0, len(img.Boxes))
		for _, gb := range img.Boxes {
			truth = append(truth, bench.GT{
				Box:         bench.Box{Class: gb.Class, X1: gb.X1, Y1: gb.Y1, X2: gb.X2, Y2: gb.Y2},
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
		if len(ans.Boxes) == 0 && len(truth) == 0 {
			// A negative frame with nothing drawn is the ONE combination that is
			// unambiguously right, and it is worth saying out loud per image so a
			// reader does not have to infer it from a row of zeros.
			fmt.Printf("  %-28s negative frame, nothing drawn, nothing there\n", img.Name)
			continue
		}
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

	// The achievable rate IS available here, because the latency was measured.
	if p99, ok := det.Latencies().P99(); ok {
		fmt.Printf("\n  detector latency p50 %.1fms  p99 %.1fms over %d frames\n",
			mustP50(det), p99, det.Latencies().Count())
		if fps, ok := det.Latencies().AchievableFPS(); ok {
			fmt.Printf("  achievable      %.1f fps on this machine, at %d threads\n",
				fps, envInt("SP_POC_ORT_THREADS", "", 4))
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

func mustP50(d *ortdetect.Detector) float64 {
	if v, ok := d.Latencies().P50(); ok {
		return v
	}
	return 0
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
