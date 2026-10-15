package soak

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCalibration() DetectorCalibration {
	return DetectorCalibration{
		Model:             filepath.FromSlash(`C:\poc\models\yolov4n.onnx`),
		ModelSHA256:       "E60759B6BD0088C1B5C9E0CEE23118C7E15B035F3CEC4F4CD87F4D89615788F2",
		Library:           filepath.FromSlash(`C:\poc\models\onnxruntime.dll`),
		Threads:           4,
		Input:             640,
		ExecutionProvider: "cuda",
		Trigger:           TriggerDetector,
		P50MS:             7.1,
		P99MS:             18.4,
		Frames:            120,
		MeasuredAt:        "2026-10-14T20:00:00Z",
	}
}

// testSidecarCalibration is the same record measured on the OTHER path: a
// loopback round trip plus a Python decode, which is a different number by a
// factor nobody should guess.
func testSidecarCalibration() DetectorCalibration {
	c := testCalibration()
	c.Trigger = TriggerSidecar
	c.Library = "CPUExecutionProvider"
	c.Threads = 0
	c.Address = "http://127.0.0.1:8099"
	c.P50MS = 41.2
	c.P99MS = 63.8
	c.ExecutionProvider = "CPUExecutionProvider"
	return c
}

// TestACalibrationRoundTripsThroughTheDataRoot is the boring half, asserted
// because the interesting half depends on it: a record that does not survive its
// own file is a record every other test would be testing the encoding of.
func TestACalibrationRoundTripsThroughTheDataRoot(t *testing.T) {
	root := t.TempDir()
	want := testCalibration()
	if err := WriteCalibration(root, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadCalibration(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ModelSHA256 != want.ModelSHA256 || got.P99MS != want.P99MS || got.Frames != want.Frames {
		t.Fatalf("the record did not survive its own file: %+v", got)
	}
	if got.P50MS != want.P50MS {
		t.Fatalf("p50 = %v, want %v", got.P50MS, want.P50MS)
	}
}

// TestAnUnmeasuredCalibrationIsRefusedOnWrite: the record is the gate's only
// source of a p99, so a zero written to disk becomes a measured value the moment
// somebody reads it. The write refuses rather than the read guessing.
func TestAnUnmeasuredCalibrationIsRefusedOnWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DetectorCalibration)
	}{
		{"no p99", func(c *DetectorCalibration) { c.P99MS = 0 }},
		{"no frames", func(c *DetectorCalibration) { c.Frames = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCalibration()
			tc.mutate(&c)
			if err := WriteCalibration(t.TempDir(), c); err == nil {
				t.Fatal("an unmeasured calibration was written to disk")
			}
		})
	}
}

// TestAnAbsentCalibrationNamesTheCommandThatProducesOne: the refusal is read by
// an operator who has just been told their run will not start, and "no p99" is
// not an action.
func TestAnAbsentCalibrationNamesTheCommandThatProducesOne(t *testing.T) {
	_, err := LoadCalibration(t.TempDir())
	if err == nil {
		t.Fatal("an absent calibration loaded")
	}
	for _, want := range []string{"bench", "--data-root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestACalibrationAppliesOnlyToTheBuildItMeasured is the control for the whole
// record: a p99 from one build applied to another is a number with the wrong
// label, which is the defect class this repository exists to end.
func TestACalibrationAppliesOnlyToTheBuildItMeasured(t *testing.T) {
	c := testCalibration()
	build := func(mutate func(*CalibrationBuild)) CalibrationBuild {
		b := CalibrationBuild{
			Trigger: TriggerDetector, Model: c.Model, Library: c.Library,
			Threads: c.Threads, Input: c.Input,
		}
		if mutate != nil {
			mutate(&b)
		}
		return b
	}
	if err := c.Applies(build(nil)); err != nil {
		t.Fatalf("the record does not apply to the build it measured: %v", err)
	}
	// When both files really exist, the file system is the authority: the same
	// inode under two spellings is one model, whichever directory the two
	// commands were started from. (Without files on disk there is nothing to
	// resolve: a relative name and an unrelated absolute name are two
	// different candidate files, and saying they match would be the guess this
	// record exists to refuse.)
	dir := t.TempDir()
	model := filepath.Join(dir, "yolov4n.onnx")
	lib := filepath.Join(dir, "onnxruntime.dll")
	for _, p := range []string{model, lib} {
		if err := os.WriteFile(p, []byte("not a real model"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	onDisk := testCalibration()
	onDisk.Model = model
	onDisk.Library = lib
	rel := testCalibration()
	rel.Model = filepath.Join(".", filepath.Base(model))
	rel.Library = filepath.Join(".", filepath.Base(lib))
	// The run reads them by a path relative to a working directory that is the
	// temp dir itself, so the two spellings name one file.
	t.Chdir(dir)
	if err := onDisk.Applies(build(func(b *CalibrationBuild) {
		b.Model, b.Library = rel.Model, rel.Library
	})); err != nil {
		t.Fatalf("the same file under two spellings was treated as two builds: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*CalibrationBuild)
		want   string
	}{
		{"another model", func(b *CalibrationBuild) { b.Model = filepath.FromSlash(`C:\poc\models\yolov4-tiny.onnx`) }, "yolov4-tiny"},
		{"another library", func(b *CalibrationBuild) { b.Library = filepath.FromSlash(`C:\poc\models\onnxruntime-cpu.dll`) }, "onnxruntime-cpu"},
		{"another thread count", func(b *CalibrationBuild) { b.Threads = 16 }, "16"},
		{"another input size", func(b *CalibrationBuild) { b.Input = 320 }, "320"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Applies(build(tc.mutate))
			if err == nil {
				t.Fatalf("the record was applied to a different build")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not name the part that differs (%q): %v", tc.want, err)
			}
		})
	}
}

// TestACalibrationFromOnePathIsNotAppliedToTheOther is the trigger axis, and it
// is the one the plan writes a whole phase for: the in-process session and the
// Python sidecar cost different numbers by a factor nobody should guess, so a
// record from one applied to a run on the other is a p99 with the wrong path.
//
// The refusal names BOTH paths and the bench that would produce the right one,
// because "the calibration is for a different build" sends the operator to
// re-time a session that was never the subject.
func TestACalibrationFromOnePathIsNotAppliedToTheOther(t *testing.T) {
	det := testCalibration()
	side := testSidecarCalibration()
	sideBuild := CalibrationBuild{
		Trigger: TriggerSidecar, Model: side.Model, Library: side.Library,
		Threads: side.Threads, Input: side.Input, Address: side.Address,
	}
	// The sidecar record applies to a sidecar run, and the detector record
	// does not — even though every other field a detector build would compare
	// happens to be satisfiable.
	err := det.Applies(sideBuild)
	if err == nil {
		t.Fatal("a DETECTOR calibration was applied to a sidecar run")
	}
	for _, want := range []string{"detector trigger", "sidecar trigger", "--trigger sidecar"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// And the other direction: a sidecar p99 must not clear a detector row.
	err = side.Applies(CalibrationBuild{
		Trigger: TriggerDetector, Model: det.Model, Library: det.Library,
		Threads: det.Threads, Input: det.Input,
	})
	if err == nil {
		t.Fatal("a SIDECAR calibration was applied to a detector run")
	}
	// A record that predates the field applies to NEITHER path: it does not
	// say which one measured it, and that is the refusal rather than a guess.
	legacy := testCalibration()
	legacy.Trigger = ""
	err = legacy.Applies(CalibrationBuild{
		Trigger: TriggerDetector, Model: det.Model, Library: det.Library,
		Threads: det.Threads, Input: det.Input,
	})
	if err == nil {
		t.Fatal("a record with no trigger was applied to a run")
	}
	if !strings.Contains(err.Error(), "(unrecorded)") {
		t.Errorf("the refusal does not say the record carries no trigger: %v", err)
	}
	// The sidecar record DOES apply to the sidecar build it measured — the
	// control is about the axis, not about refusing sidecar records.
	if err := side.Applies(sideBuild); err != nil {
		t.Fatalf("the sidecar record does not apply to the sidecar build it measured: %v", err)
	}
	// And the address is part of that build: a different sidecar is a
	// different machine, and the loopback hop is part of the number.
	err = side.Applies(func() CalibrationBuild {
		b := sideBuild
		b.Address = "http://127.0.0.1:8098"
		return b
	}())
	if err == nil {
		t.Fatal("a calibration measured against one sidecar was applied to another")
	}
	// Two spellings of one address are one sidecar: the scheme prefix is not
	// a fact about the machine.
	if err := side.Applies(func() CalibrationBuild {
		b := sideBuild
		b.Address = "127.0.0.1:8099"
		return b
	}()); err != nil {
		t.Fatalf("the same sidecar under two spellings was treated as two machines: %v", err)
	}
}

// TestACalibrationMustSayWhichPathMeasuredIt: the write refuses a record with
// no trigger, because a p99 that does not say which path measured it cannot be
// applied to either one.
func TestACalibrationMustSayWhichPathMeasuredIt(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"absent", ""},
		{"unknown", "gpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testCalibration()
			c.Trigger = tc.trigger
			if err := WriteCalibration(t.TempDir(), c); err == nil {
				t.Fatalf("a calibration with trigger %q was written", tc.trigger)
			} else if !strings.Contains(err.Error(), "WHICH path") {
				t.Fatalf("the refusal does not say what the field is for: %v", err)
			}
		})
	}
	// The sidecar trigger is a first-class value, not an extension of the
	// detector one.
	c := testSidecarCalibration()
	if err := WriteCalibration(t.TempDir(), c); err != nil {
		t.Fatalf("a sidecar calibration was refused: %v", err)
	}
}

// TestTheSidecarRecordSurvivesItsOwnFile: the two new fields are part of the
// record, and a record that loses its address on the way through the file is a
// record that will refuse the run it was measured for.
func TestTheSidecarRecordSurvivesItsOwnFile(t *testing.T) {
	root := t.TempDir()
	want := testSidecarCalibration()
	if err := WriteCalibration(root, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadCalibration(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Trigger != TriggerSidecar || got.Address != want.Address || got.P99MS != want.P99MS {
		t.Fatalf("the record did not survive its own file: %+v", got)
	}
	// And the rendered line names the path: a reader comparing two runs has to
	// see which one was a loopback round trip.
	if line := got.Describe(); !strings.Contains(line, "sidecar at http://127.0.0.1:8099") {
		t.Fatalf("the described record does not name the sidecar path: %q", line)
	}
}

// TestMeasuredP99IsZeroWhenThereIsNothingMeasured: the gate refuses a
// multiplication by zero, so the accessor has to be the place that decides what
// "nothing" is rather than every caller remembering.
func TestMeasuredP99IsZeroWhenThereIsNothingMeasured(t *testing.T) {
	c := testCalibration()
	if got := c.MeasuredP99(); got != c.P99MS {
		t.Fatalf("p99 = %v, want %v", got, c.P99MS)
	}
	c.P99MS = 0
	if got := c.MeasuredP99(); got != 0 {
		t.Fatalf("an unmeasured record still reported %v", got)
	}
	var none *DetectorCalibration
	if got := none.MeasuredP99(); got != 0 {
		t.Fatalf("a nil record reported %v", got)
	}
}

func TestTheCalibrationFileIsInTheDataRoot(t *testing.T) {
	if filepath.Base(CalibrationFile) != "detector-calibration.json" {
		t.Fatalf("the calibration file is %q; preflight looks for the data root's copy and the two must not drift", CalibrationFile)
	}
	root := t.TempDir()
	if got := CalibrationPath(root); got != filepath.Join(root, "detector-calibration.json") {
		t.Fatalf("path = %q", got)
	}
	// And the record is actually there after a write, which is the only
	// end-to-end property of the path that matters.
	if err := WriteCalibration(root, testCalibration()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(CalibrationPath(root)); err != nil {
		t.Fatalf("the write did not land where the loader looks: %v", err)
	}
}
