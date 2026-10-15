package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

// TestCalibrationForResolvesTheDetectorRowsP99 covers the wiring that used to
// be missing entirely.
//
// `pocctl preflight` built its detector row from an input struct with the model
// field, the library field and the p99 field all zero, so EVERY detector run
// failed a row its own configuration had already satisfied — on a machine where
// SP_POC_DET_MODEL, SP_POC_ORT_LIB and SP_POC_ORT_THREADS are required by the
// schema and cannot be absent. The row could not pass, which is the shape of a
// gate nobody reads.
//
// The fix is this function, and a test of it is the only way to watch the
// difference: the missing wiring was invisible to every other test because the
// caller built the struct inline.
func TestCalibrationForResolvesTheDetectorRowsP99(t *testing.T) {
	env := calibrationWorld(t, &soak.DetectorCalibration{
		Threads: 4, Input: 640, ExecutionProvider: "cuda",
		P50MS: 7, P99MS: 18.4, Frames: 120, MeasuredAt: "2026-10-14T20:00:00Z",
	})

	got := calibrationFor(env.cfg())
	if got.p99MS != 18.4 {
		t.Fatalf("p99 = %v, want the calibration's measured 18.4; the row multiplies this by the frame rate", got.p99MS)
	}
	if got.calibration == nil || got.note != "" {
		t.Fatalf("a clean calibration produced a note: %q", got.note)
	}
	if line := calibrationLine(got); !strings.Contains(line, "p99 18.4ms") {
		t.Fatalf("the printed line does not carry the number: %q", line)
	}
}

func TestCalibrationForRefusesWhatItCannotUse(t *testing.T) {
	// Absent: the refusal names the command that produces one.
	env := calibrationWorld(t, nil)
	got := calibrationFor(env.cfg())
	if got.p99MS != 0 || got.calibration != nil {
		t.Fatalf("an absent calibration produced a p99: %+v", got)
	}
	for _, want := range []string{"bench", "--data-root"} {
		if !strings.Contains(got.note, want) {
			t.Errorf("the note does not name %q: %s", want, got.note)
		}
	}

	// Present but for another build: the note names the part that differs, and
	// the record is still returned so the row can print which build WAS timed.
	env = calibrationWorld(t, &soak.DetectorCalibration{
		Threads: 16, Input: 640, P99MS: 12, Frames: 100, MeasuredAt: "2026-10-14T20:00:00Z",
	})
	got = calibrationFor(env.cfg())
	if got.p99MS != 0 {
		t.Fatalf("a calibration for 16 threads produced a p99 for a 4-thread run")
	}
	if got.calibration == nil || !strings.Contains(got.note, "16") {
		t.Fatalf("the note does not carry the record and name the difference: %+v", got.note)
	}
	if line := calibrationLine(got); !strings.Contains(line, "does not describe this run") {
		t.Fatalf("the printed line does not say the record does not apply: %q", line)
	}
}

// TestCalibrationForIsSilentWhenThereIsNoDetector: a novelty run never opens a
// model, and an absent calibration must not refuse it — that is the sidecar-row
// defect again, one row over.
func TestCalibrationForIsSilentWhenThereIsNoDetector(t *testing.T) {
	got := calibrationFor(config.Config{TriggerMode: config.TriggerModeNovelty, DataRoot: t.TempDir()})
	if got.calibration != nil || got.note != "" || got.p99MS != 0 {
		t.Fatalf("a non-detector run produced calibration work: %+v", got)
	}
	if line := calibrationLine(got); line != "" {
		t.Fatalf("a non-detector run printed a calibration line: %q", line)
	}
}

// calibrationWorld is a data root with a named model and library that exist,
// and — when cal is non-nil — a recorded calibration for the FOUR-THREAD build
// the Config below declares.
type calibrationWorldEnv struct {
	root       string
	model, lib string
}

func (e calibrationWorldEnv) cfg() config.Config {
	return config.Config{
		TriggerMode: config.TriggerModeDetector, DataRoot: e.root,
		DetModel: e.model, ORTLib: e.lib, ORTThreads: 4, DetInput: 640,
	}
}

func calibrationWorld(t *testing.T, cal *soak.DetectorCalibration) calibrationWorldEnv {
	t.Helper()
	root := t.TempDir()
	env := calibrationWorldEnv{
		root:  root,
		model: root + `\yolov4n.onnx`,
		lib:   root + `\onnxruntime.dll`,
	}
	for _, p := range []string{env.model, env.lib} {
		if err := os.WriteFile(p, []byte("not a real model"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if cal == nil {
		return env
	}
	cal.Model = env.model
	cal.Library = env.lib
	if err := soak.WriteCalibration(root, *cal); err != nil {
		t.Fatal(err)
	}
	return env
}
