package soak

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The detector's measured calibration, and the one place a p99 may come from.
//
// # Why a record and not a flag
//
// The preflight row below multiplies a MEASURED p99 by the declared frame rate
// and refuses the run when the product does not fit inside a second. The
// number is the whole gate, so it cannot be typed on a command line: a p99 an
// operator asserts is a p99 nobody measured, and a gate that accepts one is a
// gate that passes because somebody hoped. The record is written by
// `pocctl bench --images`, which opens the session and times it, and read by
// preflight, which refuses when there is no record or when the record describes
// a different build.
//
// # What "a different build" means
//
// The same model path, the same ONNX Runtime library, the same thread count and
// the same declared input size. Any of those changing changes the latency — a
// different library is a different runtime, a different thread count is a
// different machine as far as the arithmetic is concerned — so a record from
// one build applied to another is a number with the wrong label, which is the
// exact defect this repository exists to end. The model's SHA-256 is recorded
// beside them so a reader can see WHICH file was timed without hashing 250 MB
// again.
type DetectorCalibration struct {
	Model             string `json:"model"`
	ModelSHA256       string `json:"model_sha256"`
	Library           string `json:"library"`
	Threads           int    `json:"threads"`
	Input             int    `json:"input"`
	ExecutionProvider string `json:"execution_provider"`
	// P50MS and P99MS are the measured latencies over Frames inferences.
	P50MS  float64 `json:"p50_ms"`
	P99MS  float64 `json:"p99_ms"`
	Frames int     `json:"frames"`
	// MeasuredAt is when the bench that wrote this record ran.
	MeasuredAt string `json:"measured_at"`
}

// CalibrationFile is the record's name inside the run's data root.
const CalibrationFile = "detector-calibration.json"

// CalibrationPath is where the record lives for a data root.
func CalibrationPath(dataRoot string) string { return filepath.Join(dataRoot, CalibrationFile) }

// LoadCalibration reads the record. An absent record is an error, not a zero
// value: a caller that receives an empty struct would treat "never measured" as
// "measured at zero", which is the multiplication by zero this gate refuses.
func LoadCalibration(dataRoot string) (*DetectorCalibration, error) {
	path := CalibrationPath(dataRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no measured calibration at %s: %w\n\n"+
			"  The detector's preflight row multiplies a MEASURED p99 by the declared frame rate, and a\n"+
			"  detector that has never run is not an infinitely fast one. Time it with:\n"+
			"    pocctl bench --images <dir> --data-root %s", path, err, dataRoot)
	}
	var c DetectorCalibration
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("the calibration at %s is not readable: %w", path, err)
	}
	return &c, nil
}

// WriteCalibration records a measured calibration.
func WriteCalibration(dataRoot string, c DetectorCalibration) error {
	if c.P99MS <= 0 {
		return fmt.Errorf("refusing to write a calibration with p99=%v: an unmeasured value written to disk becomes a measured one the moment somebody reads it", c.P99MS)
	}
	if c.Frames <= 0 {
		return fmt.Errorf("refusing to write a calibration over %d frames", c.Frames)
	}
	if strings.TrimSpace(c.MeasuredAt) == "" {
		c.MeasuredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("the data root %s could not be created for the calibration: %w", dataRoot, err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := CalibrationPath(dataRoot)
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("the calibration at %s could not be written: %w", path, err)
	}
	return nil
}

// Applies reports whether this record describes the build that is about to
// run, and says in words which part does not match when it does not.
//
// The comparison is on the CLEANED ABSOLUTE PATH: the bench that wrote the
// record and the run that reads it spell the same file differently whenever one
// was run from a different working directory, and "the calibration is for a
// different model" over the same model is the refusal nobody can act on.
func (c DetectorCalibration) Applies(model, library string, threads, input int) error {
	if !samePath(c.Model, model) {
		return fmt.Errorf("it was measured against %s and this run names %s", orNone(c.Model), orNone(model))
	}
	if !samePath(c.Library, library) {
		return fmt.Errorf("it was measured against the runtime library %s and this run names %s", orNone(c.Library), orNone(library))
	}
	if c.Threads != threads {
		return fmt.Errorf("it was measured at %d thread(s) and this run declares %d; a p99 without its thread count describes no build", c.Threads, threads)
	}
	if c.Input != input {
		return fmt.Errorf("it was measured at input %d and this run declares %d; the input size is the single lever that changes the cost", c.Input, input)
	}
	return nil
}

// MeasuredP99 is the number the row multiplies, or zero when there is nothing
// measured. A separate accessor rather than reading the field because "zero"
// must be indistinguishable from "absent" at the call site too.
func (c *DetectorCalibration) MeasuredP99() float64 {
	if c == nil || c.P99MS <= 0 || c.Frames <= 0 {
		return 0
	}
	return c.P99MS
}

// Describe renders the record for the row's measured line.
func (c *DetectorCalibration) Describe() string {
	if c == nil {
		return "no calibration"
	}
	return fmt.Sprintf("p50 %.1fms, p99 %.1fms over %d frames, %d threads, input %d, measured %s",
		c.P50MS, c.P99MS, c.Frames, c.Threads, c.Input, c.MeasuredAt)
}

// samePath compares two paths the way the record needs: the same FILE is the
// same build, however it was spelled. Two spellings of one file are common —
// the bench that wrote the record and the run that reads it are started from
// different working directories more often than not — and "the calibration is
// for a different model" over the same model is the refusal nobody can act on.
//
// When both paths exist on disk the file system is the authority (os.SameFile
// resolves links and even different volumes' spellings of one inode). When they
// do not, the cleaned absolute form is the fallback, because that is all that
// can be said about a file that is not there.
func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	if sa, err := os.Stat(a); err == nil {
		if sb, err := os.Stat(b); err == nil {
			return os.SameFile(sa, sb)
		}
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
