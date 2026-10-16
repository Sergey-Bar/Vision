package main

import (
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

// TestTheDetectorTimingPassSaysWhichPathMeasuredIt is the control the detector
// half of the calibration record lacked.
//
// `soak.WriteCalibration` refuses a record whose trigger is neither of the two
// paths, and the detector pass built its record without one - so
// `pocctl bench --trigger detector` ran every inference, printed a p50/p99, and
// then wrote nothing. Found by running `scripts/live-demo.ps1 -TriggerMode
// detector`: the calibrate act's output and the preflight refusal two acts later
// disagreed, and the disagreement was this record.
//
// The record is built here the way the bench builds it, so removing the trigger
// line fails a test instead of a demo. The second half is the writer's own gate
// rather than a copy of it: a record the bench cannot write is a calibration the
// run cannot have.
func TestTheDetectorTimingPassSaysWhichPathMeasuredIt(t *testing.T) {
	cal := detectorCalibration("yolov4n.onnx", "1881fe9c506c970d", "onnxruntime.dll", 4, 416, "cuda", 16.2, 22.4, 40)

	if cal.Trigger != soak.TriggerDetector {
		t.Fatalf("the detector timing pass wrote trigger %q; the record must say WHICH path "+
			"measured the p99 (%q or %q), and this pass is the detector's", cal.Trigger,
			soak.TriggerDetector, soak.TriggerSidecar)
	}
	if err := soak.WriteCalibration(t.TempDir(), cal); err != nil {
		t.Fatalf("the record the detector pass builds was refused by WriteCalibration: %v", err)
	}
}
