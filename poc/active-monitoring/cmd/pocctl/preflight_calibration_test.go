package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision/shared/sidecar"
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
		Trigger: soak.TriggerDetector,
		Threads: 4, Input: 640, ExecutionProvider: "cuda",
		P50MS: 7, P99MS: 18.4, Frames: 120, MeasuredAt: "2026-10-14T20:00:00Z",
	})

	got := calibrationFor(env.cfg(), nil)
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
	got := calibrationFor(env.cfg(), nil)
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
		Trigger: soak.TriggerDetector,
		Threads: 16, Input: 640, P99MS: 12, Frames: 100, MeasuredAt: "2026-10-14T20:00:00Z",
	})
	got = calibrationFor(env.cfg(), nil)
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
	got := calibrationFor(config.Config{TriggerMode: config.TriggerModeNovelty, DataRoot: t.TempDir()}, nil)
	if got.calibration != nil || got.note != "" || got.p99MS != 0 {
		t.Fatalf("a non-detector run produced calibration work: %+v", got)
	}
	if line := calibrationLine(got); line != "" {
		t.Fatalf("a non-detector run printed a calibration line: %q", line)
	}
}

// TestCalibrationForRefusesADetectorRecordOnASidecarRun is the wiring half of
// the trigger axis: the record in the data root was written by the in-process
// pass, the run is on the sidecar path, and the p99 the AI row would multiply
// is not the one that was measured. The refusal names the bench that produces
// the right record, so the operator knows what to run rather than being told
// the build is wrong.
func TestCalibrationForRefusesADetectorRecordOnASidecarRun(t *testing.T) {
	env := calibrationWorld(t, &soak.DetectorCalibration{
		Trigger: soak.TriggerDetector,
		Threads: 4, Input: 416, ExecutionProvider: "cuda",
		P50MS: 13.9, P99MS: 16.4, Frames: 40, MeasuredAt: "2026-10-15T10:00:00Z",
	})
	addr := fakeSidecarURL(t, filepath.Base(env.aiModel), 416, []string{"CPUExecutionProvider"})
	cfg := env.sidecarCfg(addr)
	got := calibrationFor(cfg, nil)
	// No client was supplied, so the sidecar's own model/providers/input could
	// not be read at all — which is its own refusal, and not a p99.
	if got.p99MS != 0 {
		t.Fatalf("a run whose sidecar could not be probed produced a p99: %+v", got)
	}
	if !strings.Contains(got.note, "sidecar client") {
		t.Fatalf("the note does not say the sidecar could not be read: %q", got.note)
	}

	// With a probed sidecar, the DETECTOR record is refused BY NAME and the
	// sidecar record is what the AI row multiplies.
	client, err := sidecar.New(addr, time.Second)
	if err != nil {
		t.Fatalf("sidecar.New: %v", err)
	}
	got = calibrationFor(cfg, client)
	if got.p99MS != 0 {
		t.Fatalf("a detector calibration produced a p99 for a sidecar run: %+v", got)
	}
	for _, want := range []string{"detector trigger", "sidecar trigger", "--trigger sidecar"} {
		if !strings.Contains(got.note, want) {
			t.Errorf("the note does not say %q: %s", want, got.note)
		}
	}
	if line := calibrationLine(got); !strings.Contains(line, "sidecar calibration") {
		t.Errorf("the printed line does not name the path being resolved: %q", line)
	}

	// The same data root now holding a SIDECAR record for the sidecar the run
	// would talk to: the AI row gets the measured loopback number.
	if err := soak.WriteCalibration(env.root, soak.DetectorCalibration{
		Trigger: soak.TriggerSidecar, Address: addr,
		Model: cfg.AIModel, Library: "CPUExecutionProvider",
		Input: 416, P50MS: 41.2, P99MS: 63.8, Frames: 40,
		MeasuredAt: "2026-10-15T11:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got = calibrationFor(cfg, client)
	if got.p99MS != 63.8 {
		t.Fatalf("p99 = %v, want the sidecar record's measured 63.8", got.p99MS)
	}
	if !got.sidecar {
		t.Fatal("the resolved record did not report the path it measured")
	}
	if got.detectorP99MS() != 0 {
		t.Error("the detector row would multiply the sidecar's p99")
	}
}

// TestCalibrationForRefusesASidecarRecordThatNamesAnotherModel: the probe says
// which model the sidecar LOADED, and a p99 about a different export is a p99
// about a model nobody is decoding with.
func TestCalibrationForRefusesASidecarRecordThatNamesAnotherModel(t *testing.T) {
	env := calibrationWorld(t, nil)
	cfg := env.sidecarCfg(fakeSidecarURL(t, "other-export.onnx", 416, []string{"CPUExecutionProvider"}))
	client, err := sidecar.New(cfg.AIAddr, time.Second)
	if err != nil {
		t.Fatalf("sidecar.New: %v", err)
	}
	got := calibrationFor(cfg, client)
	if got.p99MS != 0 {
		t.Fatalf("a mismatched sidecar produced a p99: %+v", got)
	}
	if !strings.Contains(got.note, "other-export.onnx") {
		t.Fatalf("the note does not name the model the sidecar loaded: %q", got.note)
	}
}

// calibrationWorld is a data root with a named model and library that exist,
// and — when cal is non-nil — a recorded calibration for the FOUR-THREAD build
// the Config below declares.
type calibrationWorldEnv struct {
	root       string
	model, lib string
	aiModel    string
}

func (e calibrationWorldEnv) cfg() config.Config {
	return config.Config{
		TriggerMode: config.TriggerModeDetector, DataRoot: e.root,
		DetModel: e.model, ORTLib: e.lib, ORTThreads: 4, DetInput: 640,
	}
}

// sidecarCfg is the same world declared as a SIDECAR run: the model is named
// for the sidecar and the address is where its HTTP endpoint would be.
func (e calibrationWorldEnv) sidecarCfg(addr string) config.Config {
	return config.Config{
		TriggerMode: config.TriggerModeSidecar, DataRoot: e.root,
		AIModel: e.aiModel, AIAddr: addr,
	}
}

func calibrationWorld(t *testing.T, cal *soak.DetectorCalibration) calibrationWorldEnv {
	t.Helper()
	root := t.TempDir()
	env := calibrationWorldEnv{
		root:    root,
		model:   filepath.Join(root, "yolov4n.onnx"),
		lib:     filepath.Join(root, "onnxruntime.dll"),
		aiModel: filepath.Join(root, "sidecar-yolov4n.onnx"),
	}
	for _, p := range []string{env.model, env.lib, env.aiModel} {
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

// fakeSidecarURL stands up a sidecar's /healthz so the probe the calibration
// resolves against has something to read. It is the real wire format — ready,
// model, input_width/height, providers — because a test double that drifts
// from the shape `pyworker/server.py` sends is a double that proves nothing
// about the run.
func fakeSidecarURL(t *testing.T, model string, input int, providers []string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready": true, "model": model,
			"input_width": input, "input_height": input,
			"providers": providers,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
