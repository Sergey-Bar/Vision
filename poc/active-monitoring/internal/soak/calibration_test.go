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
		P50MS:             7.1,
		P99MS:             18.4,
		Frames:            120,
		MeasuredAt:        "2026-10-14T20:00:00Z",
	}
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
	if err := c.Applies(c.Model, c.Library, c.Threads, c.Input); err != nil {
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
	if err := onDisk.Applies(rel.Model, rel.Library, rel.Threads, rel.Input); err != nil {
		t.Fatalf("the same file under two spellings was treated as two builds: %v", err)
	}
	for _, tc := range []struct {
		name           string
		model, library string
		threads, input int
		want           string
	}{
		{"another model", filepath.FromSlash(`C:\poc\models\yolov4-tiny.onnx`), c.Library, c.Threads, c.Input, "yolov4-tiny"},
		{"another library", c.Model, filepath.FromSlash(`C:\poc\models\onnxruntime-cpu.dll`), c.Threads, c.Input, "onnxruntime-cpu"},
		{"another thread count", c.Model, c.Library, 16, c.Input, "16"},
		{"another input size", c.Model, c.Library, c.Threads, 320, "320"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Applies(tc.model, tc.library, tc.threads, tc.input)
			if err == nil {
				t.Fatalf("the record was applied to a different build")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not name the part that differs (%q): %v", tc.want, err)
			}
		})
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
