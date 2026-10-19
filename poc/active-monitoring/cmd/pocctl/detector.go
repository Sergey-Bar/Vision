package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/shared/ortdetect"
)

// `pocctl detector` — the self-check.
//
// It exists because three refusals in this module point at it and it did not exist,
// which is a worse defect than it sounds: a refusal that names a command nobody can run
// sends an operator to a command that does nothing, and they conclude the module is
// broken rather than that the message was. The three refusals are the CUDA provider's
// (`internal/ortdetect/execution_provider_cgo.go`), the no-cgo build's, and the
// unparseable-answer path's.
//
// It answers one question in a fixed order — could this machine honour what the
// configuration asks for? — and it answers it in rows, because "it does not work" is
// not a diagnosis. Each row is PASS, FAIL or NOT RUN, and NOT RUN is never written as
// PASS.
//
// The rows are deliberately cheap. This opens the ONNX Runtime only in its LAST row,
// after everything else has passed, because a pre-flight that loads the runtime is not
// a pre-flight — it is the thing that just failed, and it has taken the process's ORT
// environment with it.

func detectorUsage() {
	fmt.Print(`pocctl detector — will this machine honour what was configured?

  detector --selftest    run every check and print PASS / FAIL / NOT RUN per row

Reads the same variables a run reads: SP_POC_ORT_LIB, SP_POC_DET_MODEL, SP_POC_ORT_EP,
SP_POC_ORT_CUDA_DEVICE, SP_POC_ORT_THREADS. Every one of them can be overridden with
the flag of the same name.

The last row opens a session and runs one inference. It is last because that is the
step that can take the process's ONNX Runtime environment with it, and a diagnostic
that leaves the process unable to try again is not a diagnostic.

NOT RUN means this machine could not evaluate the row. It is not a pass, and nothing
here prints it as one.
`)
}

func cmdDetector(args []string) error {
	flags, err := flagSet("detector", map[string]string{
		"selftest": boolSpec,
		"lib":      envOr("SP_POC_ORT_LIB", ""),
		"model":    envOr("SP_POC_DET_MODEL", ""),
		"ep":       envOr("SP_POC_ORT_EP", "cpu"),
		"device":   envOr("SP_POC_ORT_CUDA_DEVICE", "0"),
		"threads":  envOr("SP_POC_ORT_THREADS", "4"),
		"frames":   "1",
	}, args)
	if err != nil {
		detectorUsage()
		return err
	}
	if flags["selftest"] != "true" {
		detectorUsage()
		return fmt.Errorf("detector: nothing to do without --selftest")
	}
	threads := envInt("SP_POC_ORT_THREADS", flags["threads"], 4)
	dev := envInt("SP_POC_ORT_CUDA_DEVICE", flags["device"], 0)
	frames := envInt("SP_POC_SELFTEST_FRAMES", flags["frames"], 1)
	cfg := ortdetect.SessionConfig{
		LibraryPath:       flags["lib"],
		ModelPath:         flags["model"],
		Threads:           threads,
		ExecutionProvider: flags["ep"],
		CUDADeviceID:      dev,
	}

	fmt.Printf("detector --selftest\n")
	fmt.Printf("  SP_POC_ORT_EP         %s\n", orNone(cfg.ExecutionProvider, "(not set)"))
	fmt.Printf("  SP_POC_ORT_CUDA_DEVICE %s\n", orNone(strconv.Itoa(dev), "(not set)"))
	fmt.Printf("  SP_POC_ORT_THREADS    %d\n", threads)
	fmt.Printf("  SP_POC_ORT_LIB        %s\n", orNone(cfg.LibraryPath, "(not set)"))
	fmt.Printf("  SP_POC_DET_MODEL      %s\n", orNone(cfg.ModelPath, "(not set)"))
	fmt.Println()

	checks := ortdetect.Preflight(cfg)
	failed := 0
	for _, c := range checks {
		fmt.Printf("  %-8s %s\n", c.State, c.Name)
		for _, line := range wrapDetail(c.Detail, 88) {
			fmt.Printf("           %s\n", line)
		}
		if c.State == ortdetect.StateFail {
			failed++
		}
	}

	// The live row. Everything above is a file, a device or a string; this is the one
	// that loads the runtime, so it runs only if the rows above did not already
	// answer the question.
	if failed > 0 {
		fmt.Println()
		fmt.Println("  The rows above already say what is wrong, so the session was not attempted.")
		fmt.Println("  Fix those and run this again: an ORT environment that has been torn down cannot")
		fmt.Println("  be brought back in the same process, so a failed session would hide the cause.")
		return fmt.Errorf("detector --selftest: %d check(s) failed", failed)
	}
	if cfg.ModelPath == "" {
		fmt.Println()
		fmt.Printf("  %-8s %s\n", ortdetect.StateNotRun, "one inference")
		fmt.Println("           SP_POC_DET_MODEL is not set, so there is nothing to run the model on. This row")
		fmt.Println("           is NOT RUN and not PASS: the rows above prove the device is usable, which is")
		fmt.Println("           not the same claim as the model having run.")
		return nil
	}

	fmt.Println()
	rows, err := liveCheck(cfg, frames)
	// The crop-pass rows come off the SAME session, deliberately. A second session for
	// the same weights would double the memory and report a different latency, and the
	// comparison this command exists to make -- what a crop costs against a whole frame --
	// is only a comparison on one device in one process.
	if cropRows, cerr := cropCheckRowsFrom(cfg, frames); cerr != nil {
		rows = append(rows, ortdetect.Check{
			Name:  "crop pass: whole-frame vs crop",
			State: ortdetect.StateFail,
			Detail: fmt.Sprintf("%v. The rows above settle the DEVICE and this one needs it, so if they "+
				"passed and this did not the fault is in the crop path rather than in the machine", cerr),
		})
	} else {
		rows = append(rows, cropRows...)
	}
	for _, row := range rows {
		fmt.Printf("  %-8s %s\n", row.State, row.Name)
		for _, line := range wrapDetail(row.Detail, 88) {
			fmt.Printf("           %s\n", line)
		}
		if row.State == ortdetect.StateFail {
			err = errSilentFail
		}
	}
	return err
}

// errSilentFail marks "something in the last rows failed" without re-printing a message
// the rows already printed in full. The rows are the message; a second copy of it is
// noise a reader has to skip past to find the command that failed.
var errSilentFail = errors.New("detector --selftest: see the FAIL rows above")

// liveCheck opens a session and runs one input, as TWO rows.
//
// Two, because they are two different claims and collapsing them produced a row that
// read as "the GPU is broken" when the failure was the model's declared output shapes.
// A diagnostic that cannot tell a device fault from a model fault sends an operator to
// reinstall a driver.
//
//   - `session` PASS means: ONNX Runtime came up on this machine, the CUDA provider was
//     appended rather than refused, and the model PARSED. That is the device claim, and
//     it is the one this whole command exists to settle.
//   - `one inference` PASS means the graph actually executed. It can fail on a model
//     whose declared output shapes carry symbolic dimensions, which is a property of
//     the export and not of the GPU — and when it does, this says so in those words.
func liveCheck(cfg ortdetect.SessionConfig, frames int) ([]ortdetect.Check, error) {
	start := time.Now()
	sess, err := ortdetect.Open(cfg)
	if err != nil {
		return []ortdetect.Check{{
			Name:  "session",
			State: ortdetect.StateFail,
			Detail: fmt.Sprintf("%v. This is the DEVICE row: nothing about the GPU could be settled "+
				"because the runtime did not come up at all", err),
		}}, err
	}
	defer func() { _ = sess.Close() }()

	id := sess.Identity()
	sessionRow := ortdetect.Check{
		Name:  "session",
		State: ortdetect.StatePass,
		Detail: fmt.Sprintf("the runtime came up, the model parsed, and the %s execution provider was "+
			"appended rather than refused. weights %s (%d bytes, sha256 %s…), threads %d, provider %q as "+
			"CONFIGURED, device %d, %.3fs",
			id.ExecutionProvider, filepath.Base(id.ModelPath), id.Bytes, id.SHA256[:min(12, len(id.SHA256))],
			cfg.Threads, id.ExecutionProvider, id.CUDADeviceID, time.Since(start).Seconds()),
	}

	in := sess.Input()
	tensor := make([]float32, in.Channels()*in.H*in.W)
	for i := range tensor {
		tensor[i] = float32((i%7)-3) / 8
	}
	outs, ierr := sess.Infer(tensor, in.Layout)
	if ierr != nil {
		shapeRow := ortdetect.Check{
			Name:  "one inference",
			State: ortdetect.StateFail,
			Detail: fmt.Sprintf("%v. Read this next: if it names a SYMBOLIC or negative dimension in the "+
				"output shape, that is the MODEL's export and not this machine — a detector exported with "+
				"dynamic axes publishes declared output dims of -1, which cannot be pre-allocated, and "+
				"neither execution provider can run it through a path that allocates them itself. The DEVICE "+
				"row above is unaffected and still stands", ierr),
		}
		if !strings.Contains(ierr.Error(), "Invalid tensor shape") &&
			!strings.Contains(ierr.Error(), "invalid value -1") {
			shapeRow.Detail = fmt.Sprintf("%v. This is not obviously about the model, so treat it as a real "+
				"inference failure; the device row above still stands on its own", ierr)
		}
		return []ortdetect.Check{sessionRow, shapeRow}, errSilentFail
	}
	var shapes []string
	for _, o := range outs {
		shapes = append(shapes, fmt.Sprintf("%s%v", o.Name, o.Dims))
	}
	rows := []ortdetect.Check{sessionRow, {
		Name:  "one inference",
		State: ortdetect.StatePass,
		Detail: fmt.Sprintf("the graph ran end to end on the configured provider. input %dx%d %s, outputs %s, "+
			"%.3fs for this one call including session set-up. A single call is a proof the device works, not "+
			"a throughput number — pass --frames to measure one",
			in.W, in.H, in.Layout, strings.Join(shapes, ", "), time.Since(start).Seconds()),
	}}
	if frames > 1 {
		if lat, ferr := measureLatency(sess, in, frames); ferr != nil {
			rows = append(rows, ortdetect.Check{Name: fmt.Sprintf("%d warm frames", frames),
				State: ortdetect.StateFail, Detail: ferr.Error()})
		} else {
			rows = append(rows, latencyCheck(lat, sess.Input(), sess.Identity().ExecutionProvider, sess.Identity().CUDADeviceID))
		}
	}
	return rows, nil
}

// measureLatency times repeated inferences and DISCARDS the first.
//
// The first call on a CUDA provider builds the context, loads cuDNN and JIT-compiles
// every kernel in the graph, and it can be an order of magnitude slower than the rest.
// Including it is how a GPU gets reported as slow, and it is the single most misleading
// number this module could print — so it is dropped, and the report says how many were
// dropped rather than quietly averaging it away.
func measureLatency(sess *ortdetect.Session, in ortdetect.InputSpec, frames int) ([]time.Duration, error) {
	tensor := make([]float32, in.Channels()*in.H*in.W)
	for i := range tensor {
		tensor[i] = float32((i%7)-3) / 8
	}
	if _, err := sess.Infer(tensor, in.Layout); err != nil {
		return nil, err
	}
	lat := make([]time.Duration, 0, frames)
	for i := 0; i < frames; i++ {
		t0 := time.Now()
		if _, err := sess.Infer(tensor, in.Layout); err != nil {
			return nil, err
		}
		lat = append(lat, time.Since(t0))
	}
	return lat, nil
}

// latencyCheck renders the distribution.
//
// Median AND p95, because the median is what a pipeline plans on and the p95 is what a
// frame interval has to survive. A report with only the mean has neither: one 2-second
// cuDNN kernel compile drags the mean past the frame interval while every frame was
// inside it.
func latencyCheck(lat []time.Duration, in ortdetect.InputSpec, ep string, device int) ortdetect.Check {
	sorted := append([]time.Duration(nil), lat...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(q float64) time.Duration {
		i := int(float64(len(sorted)-1) * q)
		return sorted[i]
	}
	med, p95, mx := pick(0.5), pick(0.95), sorted[len(sorted)-1]
	// The highest --ai-fps this sustains at p95, which is the inverse of the p95 in
	// seconds. The constraint is NOT the 0.8 s frame interval: a detector slower than
	// the video skips frames, while a detector slower than the AI BUDGET is what makes
	// the pipe back up and end a run in ai_pipe_backpressure. Those are different
	// thresholds with different fixes, and conflating them sends an operator to change
	// the wrong number.
	ceiling := int(1000 / ms(p95))
	state := ortdetect.StatePass
	if ceiling < 1 {
		state = ortdetect.StateFail
	}
	return ortdetect.Check{
		Name:  fmt.Sprintf("%d warm frames", len(lat)),
		State: state,
		Detail: fmt.Sprintf("median %.0f ms, p95 %.0f ms, max %.0f ms over %d frames on provider %q device "+
			"%d, at %dx%d. The first frame is excluded and so is the mean: the first pays for CUDA context and "+
			"cuDNN kernel compilation, and a mean lets that one frame drag every other one past the budget. "+
			"This sustains about %d inferences a second at p95, so --ai-fps %d is the highest that stays "+
			"inside it; above that the pipe backs up and the run ends in ai_pipe_backpressure",
			ms(med), ms(p95), ms(mx), len(lat), ep, device, in.W, in.H, ceiling, ceiling),
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// wrapDetail breaks a paragraph to a width, on words.
//
// A refusal that prints one 400-character line is a refusal nobody reads on a terminal,
// and this module's whole style is a message that says what breaks and what to do.
func wrapDetail(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		line := ""
		for _, w := range words {
			switch {
			case line == "":
				line = w
			case len(line)+1+len(w) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
