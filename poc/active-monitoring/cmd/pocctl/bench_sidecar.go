package main

// `bench images --trigger sidecar` â€” the same record, measured on the other
// path.
//
// # Why this exists beside the in-process pass
//
// The preflight row multiplies a MEASURED p99 by the declared frame rate, and
// the number is the whole gate. There are two ways a frame reaches a model in
// this module and they cost different numbers by a factor nobody should guess:
// the in-process ONNX session, whose p99 the CUDA bench measured at 16.4 ms on
// this box, and the Python sidecar, whose p99 is a loopback round trip plus a
// CPU decode. A sidecar run that borrowed the detector's number would be
// cleared against a session it never opens â€” the exact defect the calibration
// record exists to end â€” so the sidecar path gets its own pass over the same
// still set, writing the same record with its trigger field saying which path
// measured it.
//
// # What is the same on purpose
//
// The warm-up is DISCARDED, the count comes from the same variable, the record
// is written by the same function to the same file, and a data root with no
// measured record refuses the same way. Two passes that agree on everything
// except which endpoint the frames go to is what makes the two numbers
// comparable; two passes that also disagreed about their discipline would make
// them merely two numbers.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

// sidecarBenchDeadline is the per-request deadline the timing pass sends.
//
// It is larger than a run's declared `SP_POC_AI_DEADLINE_MS` because the bench
// is measuring the cost, not enforcing the budget: a CPU decode of a 416x416
// yolov4 is tens of milliseconds, and a bench that killed its own measurement
// at the run's deadline would record the deadline rather than the cost.
const sidecarBenchDeadline = 10 * time.Second

// benchSidecar drives the sidecar over the still set and records the loopback
// p99. It opens NO in-process session: a sidecar bench that loaded the ONNX
// Runtime would measure a machine the run does not use, and would require the
// GPU library on a box whose sidecar path never touches it.
func benchSidecar(flags map[string]string) error {
	dir := flags["images"]
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("bench: the still set is not readable: %w", err)
	}
	// The manifest is required for the same reason the detector pass requires
	// it: the material nobody recorded the licence of produces numbers nobody
	// may publish, and a timing set is not an exception â€” the p99 is quoted in
	// exactly the places the metrics would be.
	man, err := readGroundtruthManifest(flags["manifest"])
	if err != nil {
		return fmt.Errorf("bench: %w\n\n  Without a manifest there is no record of where this material "+
			"came from or under what licence, and a benchmark over material nobody may publish "+
			"produces numbers nobody may show. Run `pocctl fetch-groundtruth` first.", err)
	}
	if man.ImageLicence == "" || man.AnnotationLicence == "" {
		return fmt.Errorf("bench: the manifest records no licence for %s. Refusing to measure it.", dir)
	}

	addr := envOr("SP_POC_AI_ADDR", flags["ai-addr"])
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("bench --trigger sidecar: no sidecar address. Pass --ai-addr " +
			"http://127.0.0.1:8099 or set SP_POC_AI_ADDR; the loopback hop is part of the number " +
			"and a pass that did not say where it sent its frames measured no build in particular")
	}
	model := envOr("SP_POC_AI_MODEL", flags["model"])
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("bench --trigger sidecar: no model was named. The record says WHICH model " +
			"the p99 describes, and a record without one is a number about no export. Pass --model or " +
			"set SP_POC_AI_MODEL")
	}
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("bench --trigger sidecar: the model %s could not be read: %w. The record "+
			"carries the model's SHA-256 next to its path, and a digest nobody can compute is a "+
			"provenance nobody can check", model, err)
	}
	sha, _, err := hashFile(model)
	if err != nil {
		return fmt.Errorf("bench: the model %s could not be hashed: %w", model, err)
	}

	client, err := ai.New(addr, sidecarBenchDeadline)
	if err != nil {
		return fmt.Errorf("bench --trigger sidecar: %w", err)
	}
	// The probe is not a courtesy. The record's Model/Library/Input come from
	// what the SIDECAR reports about itself, so the record describes the
	// sidecar that was measured rather than the one the operator believes is
	// running; and the model name is cross-checked, because a sidecar serving
	// a different export is a p99 about a model nobody is using.
	probe, err := client.Probe(ctx())
	if err != nil {
		return fmt.Errorf("bench --trigger sidecar: the sidecar at %s did not answer /healthz: %w\n\n"+
			"  Start one with `pocctl run-sidecar` (or `python pyworker/server.py --model %s "+
			"--anchors <the model's .anchors.txt>`) and try again.", addr, err, model)
	}
	if probe.Model != "" && !strings.EqualFold(probe.Model, filepath.Base(model)) {
		return fmt.Errorf("bench --trigger sidecar: the sidecar reports it loaded %s and this bench "+
			"names %s. A p99 measured through a different export describes that export, and writing "+
			"it against this model's path would be the label defect this record exists to end",
			probe.Model, model)
	}

	images, err := loadStillSet(dir, man)
	if err != nil {
		return err
	}
	if len(images) == 0 {
		return fmt.Errorf("bench: %s holds no images the manifest lists, so there is nothing to time", dir)
	}

	input := probe.InputSize[0]
	if input <= 0 {
		input = probe.InputSize[1]
	}
	library := strings.Join(probe.Providers, ",")
	if library == "" {
		library = "(unreported)"
	}

	fmt.Printf("bench: %s\n", dir)
	fmt.Printf("  trigger      sidecar (the Python worker's HTTP endpoint; NO in-process session)\n")
	fmt.Printf("  sidecar      %s\n", addr)
	fmt.Printf("  model        %s\n", model)
	fmt.Printf("  sha256       %s\n", sha)
	fmt.Printf("  providers    %s\n", library)
	fmt.Printf("  input        %dx%d (the sidecar's own; the frame is letterboxed into it)\n", input, input)
	fmt.Println()

	count := envInt("SP_POC_BENCH_FRAMES", flags["frames"], defaultBenchFrames)
	if count == 0 {
		fmt.Println("  --frames 0: no timing pass, so no calibration was recorded.")
		return nil
	}

	cal, line, err := sidecarCalibration(client, images[0], count, model, addr, library, input, sha)
	if err != nil {
		return err
	}
	fmt.Print(line)
	root := flags["data-root"]
	if root == "" {
		root = strings.TrimSpace(os.Getenv("SP_POC_DATA_ROOT"))
	}
	if root == "" {
		fmt.Println("  no --data-root and no SP_POC_DATA_ROOT, so the calibration was NOT recorded.")
		fmt.Println("  `pocctl preflight`'s AI row will report the sidecar path as unmeasured until it")
		fmt.Println("  is: re-run with --data-root <the run's data root>.")
		return nil
	}
	if err := soak.WriteCalibration(root, cal); err != nil {
		return err
	}
	fmt.Printf("  calibration recorded at %s â€” `pocctl preflight` reads it for the AI row.\n",
		soak.CalibrationPath(root))
	return nil
}

// sidecarCalibration decodes one still, drives `count` timed round trips over
// it and returns the calibration those round trips describe, plus the line the
// bench prints. The decode is the only thing it does that needs ffmpeg, and it
// is kept out of [sidecarTimings] so the timing discipline — the warm-up and
// the refusal wall — is testable with an image made of bytes.
func sidecarCalibration(client *ai.Client, img stillImage, count int,
	model, addr, library string, input int, sha string) (soak.DetectorCalibration, string, error) {
	rgb, w, h, err := readImageRGB(img.Path)
	if err != nil {
		return soak.DetectorCalibration{}, "", fmt.Errorf("bench: %s: %w", img.Path, err)
	}
	p50, p99, n, err := sidecarTimings(client, rgb, w, h, count)
	if err != nil {
		return soak.DetectorCalibration{}, "", err
	}
	cal := soak.DetectorCalibration{
		Model:             model,
		ModelSHA256:       sha,
		Library:           library,
		Threads:           0, // the sidecar declares no thread count; the path has none to record
		Input:             input,
		ExecutionProvider: library,
		Trigger:           soak.TriggerSidecar,
		Address:           addr,
		P50MS:             float64(p50.Microseconds()) / 1000.0,
		P99MS:             float64(p99.Microseconds()) / 1000.0,
		Frames:            n,
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  timing pass     %d round trips of %s to %s, first one discarded as the warm-up\n",
		n, img.Name, addr)
	fmt.Fprintf(&b, "                  p50 %.1fms  p99 %.1fms  (loopback + a %s decode)\n",
		cal.P50MS, cal.P99MS, library)
	return cal, b.String(), nil
}

// sidecarTimings drives the sidecar over one frame `count+1` times and returns
// the p50/p99 of the MEASURED window.
//
// The first inference is DISCARDED, not averaged and not reported. On the
// sidecar path it pays for the same thing the in-process warm-up pays for plus
// the worker's own lazy state: the first HTTP round trip includes connection
// setup, the first numpy allocation, and on a CUDA session the provider's
// algorithm selection. Keeping it would write a calibration whose p99 refuses
// a sidecar that demonstrably answers.
//
// A REFUSED round trip is a wall, not a sample. The client reports its
// refusals inside the Result with a Latency, so a loop that only collected
// latencies would measure the cost of failing and call it the cost of
// answering: a p99 over degradations is a number about nothing, and it is
// exactly the number the AI row would then multiply by the frame rate.
func sidecarTimings(client *ai.Client, rgb []byte, w, h, count int) (p50, p99 time.Duration, n int, err error) {
	total := count + 1
	lat := ai.NewLatencies(total)
	refused := 0
	var refusal string
	for i := 0; i < total; i++ {
		r := client.Detect(ctx(), uint64(i+1), rgb, w, h)
		if i == 0 {
			// The warm-up: connection setup, the worker's first allocation,
			// and on a CUDA session the provider's algorithm selection. Kept
			// out of the window and out of the record, and said below.
			continue
		}
		if ai.AbsenceOf(r.State) || r.Unparsed {
			refused++
			if refusal == "" {
				refusal = r.Reason
			}
		}
		lat.Add(r.Latency)
	}
	if refused > 0 {
		return 0, 0, 0, fmt.Errorf("bench --trigger sidecar: %d of %d round trips "+
			"were REFUSED, so no calibration was recorded. The first refusal: %s\n\n"+
			"  A client reports a refusal with a latency, so a pass that only collected latencies "+
			"would measure the cost of failing and call it the cost of answering. Fix the refusal "+
			"(a sidecar without its anchors refuses an anchored model by name) and time it again.",
			refused, count, refusal)
	}
	var ok bool
	p50, _, p99, n, ok = lat.Percentiles()
	if !ok {
		return 0, 0, 0, fmt.Errorf("bench --trigger sidecar: no round trip was measured")
	}
	return p50, p99, n, nil
}
