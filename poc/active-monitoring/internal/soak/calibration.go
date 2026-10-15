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
//
// # The trigger axis
//
// There are two ways a frame reaches a model in this module and they cost
// different numbers by a factor nobody should guess: the in-process detector,
// whose p99 a CUDA session measured at 16.4 ms on this box, and the Python
// sidecar, whose p99 is a loopback round trip plus a CPU decode. The record
// therefore says WHICH path it measured, and Applies refuses a detector record
// applied to a sidecar run — the same control as "same model, same library,
// same threads, same input", one axis longer. A record that does not say which
// path measured it cannot be applied to either, which is why WriteCalibration
// refuses to write one.
type DetectorCalibration struct {
	Model             string `json:"model"`
	ModelSHA256       string `json:"model_sha256"`
	Library           string `json:"library"`
	Threads           int    `json:"threads"`
	Input             int    `json:"input"`
	ExecutionProvider string `json:"execution_provider"`
	// Trigger is the path the p99 was measured on: TriggerDetector for the
	// in-process ONNX session, TriggerSidecar for the Python worker's HTTP
	// round trip. It is a REQUIRED field on write: a record that does not say
	// which path measured it is a number with no path, and applying it to
	// either one is the guess this record exists to refuse.
	Trigger string `json:"trigger"`
	// Address is the sidecar the record was measured against, on the sidecar
	// path only. A different sidecar is a different machine: the loopback hop
	// is part of the number.
	Address string `json:"address,omitempty"`
	// P50MS and P99MS are the measured latencies over Frames inferences.
	P50MS  float64 `json:"p50_ms"`
	P99MS  float64 `json:"p99_ms"`
	Frames int     `json:"frames"`
	// MeasuredAt is when the bench that wrote this record ran.
	MeasuredAt string `json:"measured_at"`
}

// The two paths a p99 can be measured on are the schema's own trigger
// vocabulary — `TriggerDetector` and `TriggerSidecar` in runner.go — so the
// record, the bench that writes it and the row that reads it cannot disagree
// about what the paths are called.

// CalibrationBuild is the build a record is about to be applied to.
//
// It is a struct rather than five positional arguments because the two paths
// fill it differently: a detector build names a library and a thread count, a
// sidecar build names the sidecar's address and the providers its decode ran
// on. Positional arguments would have to be remembered in one order for one
// path and another for the other, which is how the two halves drift.
type CalibrationBuild struct {
	Trigger string
	Model   string
	Library string
	Threads int
	Input   int
	Address string
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
	if c.Trigger != TriggerDetector && c.Trigger != TriggerSidecar {
		return fmt.Errorf("refusing to write a calibration with trigger %q: the record must say WHICH path "+
			"measured it (%s or %s), because a p99 from the in-process detector applied to a sidecar run "+
			"is a number with the wrong label", orNone(c.Trigger), TriggerDetector, TriggerSidecar)
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
//
// The TRIGGER is the first comparison, ahead of the model, and deliberately
// so: a detector p99 applied to a sidecar run is not a small error to be
// discovered after four other comparisons pass, it is a different number for a
// different path, and naming it first tells the operator which bench to run
// (`--trigger sidecar`) rather than sending them to re-time a session that was
// never the subject.
func (c DetectorCalibration) Applies(b CalibrationBuild) error {
	if c.Trigger != b.Trigger {
		return fmt.Errorf("it was measured on the %s trigger and this run is on the %s trigger. The two "+
			"paths cost different numbers — an in-process session and a loopback round trip plus a CPU "+
			"decode — so this record cannot describe this run. Time it with the matching pass:\n"+
			"    pocctl bench --images <dir> --data-root <the run's data root> --trigger %s",
			triggerName(c.Trigger), triggerName(b.Trigger), orNone(b.Trigger))
	}
	if !samePath(c.Model, b.Model) {
		return fmt.Errorf("it was measured against %s and this run names %s", orNone(c.Model), orNone(b.Model))
	}
	if !samePath(c.Library, b.Library) {
		return fmt.Errorf("it was measured against the runtime library %s and this run names %s", orNone(c.Library), orNone(b.Library))
	}
	if c.Threads != b.Threads {
		return fmt.Errorf("it was measured at %d thread(s) and this run declares %d; a p99 without its thread count describes no build", c.Threads, b.Threads)
	}
	if c.Input != b.Input {
		return fmt.Errorf("it was measured at input %d and this run declares %d; the input size is the single lever that changes the cost", c.Input, b.Input)
	}
	if normalizeAddr(c.Address) != normalizeAddr(b.Address) {
		return fmt.Errorf("it was measured against the sidecar at %s and this run names %s; a different "+
			"sidecar is a different machine and the loopback hop is part of the number",
			orNone(c.Address), orNone(b.Address))
	}
	return nil
}

// triggerName renders a trigger for a refusal, and says so when the record
// predates the field rather than printing an empty word.
func triggerName(t string) string {
	if strings.TrimSpace(t) == "" {
		return "(unrecorded)"
	}
	return t
}

// normalizeAddr compares two sidecar addresses the way the record needs: the
// same sidecar, however it was spelled. `http://127.0.0.1:8099` and
// `127.0.0.1:8099` are one address, and a refusal over a scheme prefix is a
// refusal nobody can act on.
func normalizeAddr(a string) string {
	s := strings.TrimSpace(a)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	return strings.TrimRight(s, "/")
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
	s := fmt.Sprintf("p50 %.1fms, p99 %.1fms over %d frames, %d threads, input %d, measured %s",
		c.P50MS, c.P99MS, c.Frames, c.Threads, c.Input, c.MeasuredAt)
	// The path is part of the number, so it is part of the sentence: a reader
	// comparing two runs has to see which one was a loopback round trip.
	if c.Trigger == TriggerSidecar {
		return "sidecar at " + orNone(c.Address) + ": " + s
	}
	return s
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
