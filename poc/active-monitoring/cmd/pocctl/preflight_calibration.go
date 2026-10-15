package main

import (
	"fmt"

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
type calibrationResult struct {
	calibration *soak.DetectorCalibration
	p99MS       float64
	note        string
}

func calibrationFor(cfg config.Config) calibrationResult {
	if cfg.TriggerMode != config.TriggerModeDetector {
		return calibrationResult{}
	}
	cal, err := soak.LoadCalibration(cfg.DataRoot)
	if err != nil {
		return calibrationResult{note: err.Error()}
	}
	if err := cal.Applies(cfg.DetModel, cfg.ORTLib, cfg.ORTThreads, cfg.DetInput); err != nil {
		return calibrationResult{calibration: cal, note: err.Error()}
	}
	return calibrationResult{calibration: cal, p99MS: cal.MeasuredP99()}
}

// calibrationLine prints the outcome once, before the table, so the reader knows
// which p99 the row is about to use and does not have to infer it from a number.
func calibrationLine(r calibrationResult) string {
	switch {
	case r.calibration == nil && r.note == "":
		return ""
	case r.calibration == nil:
		return fmt.Sprintf("detector calibration: none recorded. %s", r.note)
	case r.p99MS > 0:
		return fmt.Sprintf("detector calibration: %s", r.calibration.Describe())
	default:
		return fmt.Sprintf("detector calibration: recorded, but it does not describe this run: %s", r.note)
	}
}
