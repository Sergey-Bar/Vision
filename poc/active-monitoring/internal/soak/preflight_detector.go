package soak

import (
	"fmt"
	"os"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
)

// The detector's preflight row, and the arithmetic it refuses on.
//
// # Why this is a gate and not a metric
//
// `internal/recorder`'s bounded queue BLOCKS rather than dropping frames, and the
// supervisor kills ffmpeg with a recorded fault. So a detector slower than the frame
// interval does not degrade gracefully: it produces backpressure on exactly the
// frames that matter, it fails AC1, and it does so at minute three of a demo.
//
// The check is one comparison — det_p99 x ai_fps < 1000 — computed on a MEASURED
// p99. Both numbers are printed, because a refusal that says only "the detector
// cannot hold the declared frame rate" sends its reader to a profiler, and one that
// says "det_p99=61.2ms x ai_fps=30 = 1836ms of a 1000ms second" tells them which of
// the two to change.
//
// # A missing measurement is NOT a passing measurement
//
// A p99 of zero is refused, not multiplied out. `HoldsFrameRate` refuses it for the
// same reason: a detector that has never run is not an infinitely fast detector, and
// a gate that passes because it multiplied by zero is worse than no gate at all.

// detectorWhy is the constant for the row.
//
// It is a CONSTANT and not whatever the check returned, for the reason the h264
// decoder row above gives: a row whose consequence disappears when it is satisfied
// stops explaining itself exactly when a reader is least alert for it.
const detectorWhy = "a detector that cannot hold the declared frame rate does not degrade, because the " +
	"bounded queue blocks rather than dropping frames: the run produces backpressure, fails AC1, and says " +
	"the camera dropped out rather than saying the detector was too slow"

// detectorMeasured renders the row's measured line.
//
// It is rendered even when the check FAILS, and it is the thing that makes the row
// actionable: a reader needs the four numbers, not the verdict.
func detectorMeasured(in PreflightInput) string {
	if in.DetectorP99MS <= 0 {
		if in.Calibration != nil {
			return "a calibration exists but does not apply to this run: " + in.CalibrationNote
		}
		return "no p99 measured yet; the model and library are named but nothing has been timed"
	}
	rate := in.AIFPS
	what := fmt.Sprintf("ai_fps=%d", in.AIFPS)
	if in.AIFPS == 0 {
		what = fmt.Sprintf("ai_fps=0 (every decoded frame, source %.3f fps)", in.SourceReport.FPS)
		rate = int(in.SourceReport.FPS + 0.999)
	}
	calibrated := ""
	if in.Calibration != nil {
		calibrated = fmt.Sprintf(" [calibrated %s over %d frames]", in.Calibration.MeasuredAt, in.Calibration.Frames)
	}
	return fmt.Sprintf("det_p99=%.1fms x %s = %.0fms of a 1000ms second, threads=%d%s",
		in.DetectorP99MS, what, in.DetectorP99MS*float64(rate), in.DetectorThreads, calibrated)
}

// detectorPreflightOK is the gate itself.
//
// Three refusals, in the order a reader should meet them: the model is not named,
// the library is not named or not on disk, and the arithmetic does not fit.
func detectorPreflightOK(in PreflightInput) bool {
	if strings.TrimSpace(in.DetectorModel) == "" {
		return false
	}
	if strings.TrimSpace(in.DetectorLibrary) == "" {
		return false
	}
	if _, err := os.Stat(in.DetectorLibrary); err != nil {
		return false
	}
	if in.DetectorThreads < 1 {
		return false
	}
	ok, _ := detectorArithmetic(in)
	return ok
}

// detectorArithmetic is the one comparison, shared with the place that measures it.
//
// It calls [ortdetect.Latencies.HoldsFrameRate] rather than repeating the
// arithmetic, because the gate and the banner that prints the achievable rate must
// not be able to disagree — and two copies of a threshold is two places to change
// one of them.
func detectorArithmetic(in PreflightInput) (ok bool, arithmetic string) {
	return frameRateArithmetic(in.DetectorP99MS, in)
}

// sidecarArithmetic is the same one comparison for the sidecar path.
//
// The cost is different — a loopback round trip plus a Python decode — and the
// threshold is the same, which is the point: the bounded queue blocks rather
// than dropping frames whichever process is behind it, so a sidecar that
// cannot hold the declared rate produces the same backpressure and fails AC1
// the same way.
//
// The noun in the printed sentence is relabelled. The sentence comes from the
// shared comparison ([ortdetect.Latencies.HoldsFrameRate]), whose "det_p99"
// names the in-process session's number; on this path the number is the
// sidecar's, and a row that printed the wrong noun while quoting the right
// number would be read as the detector's measurement all the same.
func sidecarArithmetic(in PreflightInput) (ok bool, arithmetic string) {
	ok, arithmetic = frameRateArithmetic(in.SidecarP99MS, in)
	return ok, strings.ReplaceAll(arithmetic, "det_p99", "sidecar p99")
}

// frameRateArithmetic is the comparison itself, over a p99 whichever path
// measured it. One function so the two rows cannot disagree about the
// arithmetic, and so the lever named in either refusal is the same lever.
func frameRateArithmetic(p99MS float64, in PreflightInput) (ok bool, arithmetic string) {
	l := ortdetect.NewLatencies(1)
	if p99MS > 0 {
		l.Observe(p99MS)
	}
	return l.HoldsFrameRate(in.AIFPS, in.SourceReport.FPS)
}

// detectorDetail is the refusal's text, and it names the one thing to change.
func detectorDetail(in PreflightInput) string {
	if strings.TrimSpace(in.DetectorModel) == "" {
		return "SP_POC_TRIGGER_MODE=detector and no model was named. A detector run that silently " +
			"opened no file is a run that decodes frames, draws nothing, and reports a zero detection " +
			"rate with no error — which is what the previous build did for two real runs. " +
			"Run `pocctl fetch-model` and record its licence and SHA-256."
	}
	if strings.TrimSpace(in.DetectorLibrary) == "" {
		return "SP_POC_TRIGGER_MODE=detector and no runtime library was named. It is named by PATH " +
			"rather than discovered from PATH because two ONNX Runtime versions can be installed on one " +
			"machine and they are not ABI-compatible with each other's Go bindings. " +
			"Run `pocctl fetch-runtime`."
	}
	if _, err := os.Stat(in.DetectorLibrary); err != nil {
		return fmt.Sprintf("the ONNX Runtime library is not where SP_POC_ORT_LIB says: %s: %v. "+
			"Point the variable at a file that exists, or run `pocctl fetch-runtime`",
			in.DetectorLibrary, err)
	}
	if in.DetectorThreads < 1 {
		return fmt.Sprintf("SP_POC_ORT_THREADS=%d. A p99 without its thread count describes no build, "+
			"and ORT would otherwise pick from the machine's core count so the same run differs "+
			"between a four-core laptop and a server", in.DetectorThreads)
	}
	ok, arithmetic := detectorArithmetic(in)
	if ok {
		return "the measured p99 fits inside the declared rate: " + arithmetic
	}
	if in.DetectorP99MS <= 0 {
		// Two different absences, and they are not the same sentence. A record
		// that exists and does not apply names the part that differs, because
		// "run the bench again" is wrong advice for a machine that ran it
		// yesterday on a different library.
		if in.CalibrationNote != "" {
			return "the recorded calibration does not describe this run: " + in.CalibrationNote + ". " +
				arithmetic
		}
		return "no inference latency has been measured, so the declared rate cannot be checked. " +
			"Run `pocctl bench --images ... --data-root <the run's data root>` to time the model first: " +
			"a detector that has never run is not an infinitely fast detector, and this gate will not " +
			"pass by multiplying an absence by a rate. " + arithmetic
	}
	return "the detector cannot hold the declared frame rate: " + arithmetic + ". " +
		"Lower SP_POC_AI_FPS, or use a smaller SP_POC_DET_INPUT — the input size is the single " +
		"lever that changes the cost, and it is a MODEL property, so a smaller one means a model " +
		"exported for it."
}

// DetectorPreflightRow is the whole row as a value, for a caller that wants to print
// it without running the gate.
//
// It exists because `pocctl preflight` prints the table and `pocctl demo` needs the
// same decision, and two callers assembling the same text is how a demo and a gate
// end up disagreeing about the same machine.
func DetectorPreflightRow(in PreflightInput) Check {
	return Check{
		Name:     "the in-process detector is named, hashed, and can hold the declared frame rate",
		Pass:     detectorPreflightOK(in),
		Why:      detectorWhy,
		Measured: detectorMeasured(in),
		Detail:   detectorDetail(in),
	}
}

// unusedConfigGuard keeps the config import honest in the one build where the
// detector row is compiled out.
var _ = config.TriggerModeDetector
