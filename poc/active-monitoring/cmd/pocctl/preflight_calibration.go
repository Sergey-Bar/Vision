package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

// calibrationFor resolves the detector row's p99 from a MEASURED record.
//
// It is a function of the configuration and nothing else, so a test can supply
// a temp data root and watch every branch: a record that applies, a record that
// names a different build, and no record at all. The alternative — reading the
// file inline in cmdPreflight — is a branch only the real filesystem can reach,
// which is how the missing wiring described in preflight.go survived.
//
// Three outcomes, and the row's own detail prints whichever happened:
//
//	applies   the record's p99 is used, and the row can do its arithmetic
//	differs   the row refuses naming the part that differs, so the operator
//	          knows whether to re-run the bench or fix a variable
//	absent    the row refuses with the command that produces the record
//
// A run that is not in detector mode gets all three as nothing to say: there is
// no per-frame model to calibrate, and an absent calibration must not refuse a
// novelty run that never opens one.
//
// # The two paths, and the one record
//
// There are two per-frame models in this module and they cost different
// numbers by a factor nobody should guess: the in-process ONNX session and the
// Python sidecar's loopback round trip. Both are timed by `pocctl bench
// --images` — the detector pass opens a session, the sidecar pass drives the
// sidecar's own HTTP endpoint — and both write the SAME record with a trigger
// field saying which path measured it. So this function reads one file and
// resolves it against the build the run declares, and `Applies` refuses a
// detector record applied to a sidecar run by name: the two numbers are not
// interchangeable and borrowing one for the other is the defect the record
// exists to end.
type calibrationResult struct {
	calibration *soak.DetectorCalibration
	p99MS       float64
	note        string
	// sidecar is set when the resolved record measured the sidecar path. The
	// detector row ignores it; the AI row multiplies it.
	sidecar bool
}

func calibrationFor(cfg config.Config, client *ai.Client) calibrationResult {
	switch cfg.TriggerMode {
	case config.TriggerModeDetector:
		return calibrationForBuild(cfg, soak.CalibrationBuild{
			Trigger: soak.TriggerDetector,
			Model:   cfg.DetModel,
			Library: cfg.ORTLib,
			Threads: cfg.ORTThreads,
			Input:   cfg.DetInput,
		}, false)
	case config.TriggerModeSidecar:
		return calibrationForSidecar(cfg, client)
	default:
		return calibrationResult{}
	}
}

// calibrationForSidecar resolves the sidecar path's record.
//
// The build it resolves against is what the SIDECAR ITSELF reports — the model
// it loaded, the providers it is running on and the input it letterboxes to —
// plus the address the run would send frames to. The providers are part of the
// build and not decoration: the same sidecar on CPU and on CUDA is two latencies
// an order of magnitude apart, and a record from one applied to the other is a
// number with the wrong label.
func calibrationForSidecar(cfg config.Config, client *ai.Client) calibrationResult {
	if client == nil {
		return calibrationResult{note: "SP_POC_TRIGGER_MODE=sidecar and no sidecar client could be " +
			"built, so the sidecar's own model, providers and input could not be read and no calibration " +
			"can be resolved against this run"}
	}
	probe, err := client.Probe(ctx())
	if err != nil {
		return calibrationResult{note: fmt.Sprintf("the sidecar at %s did not answer /healthz (%v), so "+
			"the sidecar's own model, providers and input could not be read and no calibration can be "+
			"resolved against this run", cfg.AIAddr, err)}
	}
	library := strings.Join(probe.Providers, ",")
	input := probe.InputSize[0]
	if input <= 0 {
		input = probe.InputSize[1]
	}
	res := calibrationForBuild(cfg, soak.CalibrationBuild{
		Trigger: soak.TriggerSidecar,
		Model:   cfg.AIModel,
		Library: library,
		Threads: 0,
		Input:   input,
		Address: cfg.AIAddr,
	}, true)
	// The probe also says which model the sidecar LOADED. A run naming one
	// export while the sidecar decodes with another's weights is a p99 about a
	// model nobody is using, and it is caught here rather than by the run.
	if probe.Model != "" && !strings.EqualFold(probe.Model, filepath.Base(cfg.AIModel)) {
		return calibrationResult{note: fmt.Sprintf("the sidecar reports it loaded %s and this run names %s. "+
			"A calibration is about the model that decoded the frames, and this sidecar is decoding with a "+
			"different one", probe.Model, cfg.AIModel)}
	}
	return res
}

// calibrationForBuild is the shared resolution: load the record, apply it to
// the build, report one of the three outcomes. `sidecar` is which path's
// outcome this is, so the printed line and the row that consumes it agree.
func calibrationForBuild(cfg config.Config, build soak.CalibrationBuild, sidecar bool) calibrationResult {
	cal, err := soak.LoadCalibration(cfg.DataRoot)
	if err != nil {
		return calibrationResult{note: err.Error(), sidecar: sidecar}
	}
	if err := cal.Applies(build); err != nil {
		return calibrationResult{calibration: cal, note: err.Error(), sidecar: sidecar}
	}
	return calibrationResult{calibration: cal, p99MS: cal.MeasuredP99(), sidecar: sidecar}
}

// detectorP99MS is the p99 for the DETECTOR row, which is empty when the
// record that resolved measured the sidecar path.
func (r calibrationResult) detectorP99MS() float64 {
	if r.sidecar {
		return 0
	}
	return r.p99MS
}

// sidecarP99MS is the p99 for the AI row, which is empty when the record that
// resolved measured the detector path — or when nothing resolved at all, in
// which case the note says why.
func (r calibrationResult) sidecarP99MS() float64 {
	if !r.sidecar {
		return 0
	}
	return r.p99MS
}

func (r calibrationResult) sidecarCalibration() *soak.DetectorCalibration {
	if !r.sidecar {
		return nil
	}
	return r.calibration
}

func (r calibrationResult) sidecarNote() string {
	if !r.sidecar {
		return ""
	}
	return r.note
}

// calibrationLine prints the outcome once, before the table, so the reader knows
// which p99 the row is about to use and does not have to infer it from a number.
func calibrationLine(r calibrationResult) string {
	label := "detector calibration"
	if r.sidecar {
		label = "sidecar calibration"
	}
	switch {
	case r.calibration == nil && r.note == "":
		return ""
	case r.calibration == nil:
		return fmt.Sprintf("%s: none recorded. %s", label, r.note)
	case r.p99MS > 0:
		return fmt.Sprintf("%s: %s", label, r.calibration.Describe())
	default:
		return fmt.Sprintf("%s: recorded, but it does not describe this run: %s", label, r.note)
	}
}
