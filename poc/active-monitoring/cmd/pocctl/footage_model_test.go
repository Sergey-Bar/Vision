package main

// footage_model_test.go - the test that needs weights, and what it does when they are
// absent.
//
// # Why there is exactly ONE test that opens a session
//
// The ONNX Runtime environment is process-wide and can be initialised exactly once. A
// second `ortdetect.Open` in the same process fails with "The onnxruntime has already been
// initialized", and a teardown cannot be undone - `pocctl detector --selftest` orders its
// rows for exactly this reason.
//
// So this file does not have a narrow "the decoder refuses" test AND an end-to-end one: the
// second Open would fail for a reason that has nothing to do with either claim, and the
// failure would read as a runtime fault. Both claims - the session OPENS, and the decoder
// then REFUSES - are asserted inside the end-to-end test, where the output shows both facts
// in the order they happened.
//
// # What "no model" means here
//
// NOT RUN, printed in the skip message, and not PASS. A silent skip lets the suite go green
// over a machine that measured nothing, which is the defect this whole command exists to
// catch.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelsDirForTest finds the weights.
//
// The models directory is gitignored, so it exists in a checkout that has downloaded it and
// not in one that has not - and in an Agent Manager WORKTREE it is normally in the MAIN
// checkout, several levels above the worktree root, rather than in the worktree. The search
// therefore walks up looking for the one path shape this repository uses, and reports that it
// looked. SP_POC_MODELS_DIR overrides all of it.
func modelsDirForTest(t *testing.T) (string, bool) {
	t.Helper()
	var candidates []string
	if v := strings.TrimSpace(os.Getenv("SP_POC_MODELS_DIR")); v != "" {
		candidates = append(candidates, v)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("the package directory could not be read, so the weights search cannot start: %v", err)
	}
	candidates = append(candidates, filepath.Join(root, "..", "..", "models"))
	dir := root
	for i := 0; i < 8; i++ {
		dir = filepath.Dir(dir)
		candidates = append(candidates, filepath.Join(dir, "poc", "active-monitoring", "models"))
	}
	seen := map[string]bool{}
	for _, d := range candidates {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if _, err := os.Stat(filepath.Join(d, "yolov4n.onnx")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "onnxruntime.dll")); err != nil {
			continue
		}
		return d, true
	}
	return "", false
}

// TestTheAnchoredExportIsRefusedByNameAndTheRatesAreNotRun opens the real weights and
// asserts the whole refusal path end to end.
//
// Five claims, in the order the output makes them, which is why this is one test and not
// five:
//
//  1. THE GATE PASSES and the window is sampled: frames were really read. Without this the
//     test would pass on a run that measured nothing.
//  2. THE SESSION OPENED. The model name, its digest and its DECLARED INPUT are printed,
//     which come from the file on disk. A refusal to open is a DIFFERENT fact and sends an
//     operator to a Visual C++ redistributable instead of to the export.
//  3. THE DECODER REFUSED, by name, with the number of outputs it declared.
//  4. BOTH PASSES REFUSED, and the per-setting counts are printed so a reader can tell a
//     total refusal from a partial one. A crop pass that silently reverts to whole-frame
//     behaviour looks identical in a table and is not the same run.
//  5. THE RATES ARE NOT RUN, nothing printed a zero, and the report says the crop pass's
//     result is UNKNOWN rather than nil.
func TestTheAnchoredExportIsRefusedByNameAndTheRatesAreNotRun(t *testing.T) {
	dir, ok := modelsDirForTest(t)
	if !ok {
		t.Skip("NOT RUN: no yolov4n.onnx and onnxruntime.dll pair was found. The models directory is gitignored " +
			"and this worktree does not carry it. Run `pocctl fetch-model` and `pocctl fetch-runtime`, or set " +
			"SP_POC_MODELS_DIR. This row is NOT RUN and not PASS: nothing about the model was measured")
	}

	clip := filepath.Join("..", "..", "testdata", "injection-hostile.mp4")
	labels := filepath.Join("..", "..", "testdata", "labels.json")
	out := captureStdout(t, func() {
		// --for 2 rather than the default 30m: the window has to clear the gate's own
		// sample-count rule, and a thirty-minute window of a five-second fixture would
		// report ten frames declared and ten read while measuring five seconds of
		// material.
		if err := cmdFootage([]string{
			"--clip", clip, "--labels", labels,
			"--at", "1", "--for", "2", "--every", "0.5", "--models", dir, "--external",
		}); err != nil {
			t.Fatalf("the command refused to run at all: %v", err)
		}
	})

	// (1) frames were really sampled.
	if !strings.Contains(out, "frames        ") {
		t.Fatalf("the output does not report how many frames were read:\n%s", out)
	}
	if strings.Contains(out, "read, 0 ") {
		t.Fatalf("no frame was read, so nothing was measured and this row proves nothing:\n%s", out)
	}
	// (2) the session opened, and the model is named without the old spelling.
	if strings.Contains(out, "the session would not open") {
		t.Fatalf("the session did not open, so every claim below is about a runtime fault rather than "+
			"about the export:\n%s", out)
	}
	if !strings.Contains(out, "yolov4n.onnx") {
		t.Fatalf("the report does not name the model file it loaded:\n%s", out)
	}
	// Deliberately no assertion about the OLD name here. Embedding it would make this
	// file one of the places the tree-wide rename gate
	// `TestNothingInTheCommittedTreeStillNamesTheRenamedModel` has to find, and that
	// gate is the thing that owns the rename. Asserting the name that IS correct is
	// enough here.
	if !strings.Contains(out, "sha256 ") {
		t.Fatalf("the report does not print the model's digest, so its numbers cannot be tied to a file:\n%s", out)
	}
	if !strings.Contains(out, "416x416") {
		t.Fatalf("the report does not print the model's declared input, which is the divisor every "+
			"magnification factor depends on:\n%s", out)
	}
	// The arithmetic, which needs no model and is the part this tree CAN measure.
	if !strings.Contains(out, "with no model involved") {
		t.Fatalf("the crop arithmetic section is missing:\n%s", out)
	}
	if !strings.Contains(out, "40 px") {
		t.Fatalf("the arithmetic section does not state the object size it reports:\n%s", out)
	}
	// (3) the decoder refused, by name, with the output count.
	if !strings.Contains(out, "ANCHORED") {
		t.Fatalf("the decoder's anchored refusal is not printed:\n%s", out)
	}
	if !strings.Contains(out, "3 outputs") {
		t.Fatalf("the refusal does not report how many outputs the session declared:\n%s", out)
	}
	// (4) both passes refused, and each said how many.
	refusalLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "refusals      ") && !strings.Contains(line, "none") {
			refusalLines++
		}
	}
	if refusalLines != 2 {
		t.Fatalf("%d setting(s) reported a refusal; both the whole-frame pass and the crop pass must say so "+
			"separately, or a reader cannot tell a total refusal from a crop pass that silently reverted to "+
			"whole-frame behaviour:\n%s", refusalLines, out)
	}
	// (5) the rates are NOT RUN, nothing printed a zero, and the result is UNKNOWN.
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("the output does not say NOT RUN anywhere:\n%s", out)
	}
	if hasZeroRate(out) {
		t.Fatalf("a measurement over refusals printed a rate of zero somewhere:\n%s", out)
	}
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("the report does not say that whether the crop pass finds anything is UNKNOWN rather than "+
			"zero. That sentence is the honest result and it is the whole point:\n%s", out)
	}
}

// TestTheGroundTruthStateIsReportedBeforeAnyDetectionRuns: the order is a control. A reader who
// sees the rates NOT RUN for want of ground truth must be told that BEFORE the inference
// numbers, not after.
func TestTheGroundTruthStateIsReportedBeforeAnyDetectionRuns(t *testing.T) {
	state := describeTruth(nil, nil, footageWindow{At: 0, For: 30, Every: 1})
	if !strings.HasPrefix(state, notRun) {
		t.Fatalf("with no ground truth the state is %q; it must begin with NOT RUN", state)
	}
	if !strings.Contains(state, "no ground-truth object boxes") {
		t.Fatalf("the state does not say what is missing: %q", state)
	}
	// A named file that could not be read is an error, not this sentence.
	if _, err := readTruthFile(filepath.Join(t.TempDir(), "gone.json")); err == nil {
		t.Fatal("a --truth path that cannot be read produced no error")
	}
}
