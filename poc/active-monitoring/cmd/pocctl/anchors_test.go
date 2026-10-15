package main

// Tests for the anchors file: the nine numbers the sidecar cannot decode without.
//
// The discipline is the still set's, applied to the model: the manifest is the
// authority on what the material IS, the file on disk is what this machine has,
// and a test that does not compare them lets a checkout lose the decoder
// silently. Three records of one fact have to agree â€” the embedded bytes, the
// pinned digest and the licence record's `anchors` row â€” and a test is what
// stops them drifting apart one edit at a time.
//
// The shape refusals are asserted directly against `parseAnchorsFile`, because
// that is the only way a refusal is a control: a comment saying "malformed
// anchors are refused" is not tested by a run that only ever sees the good file.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTheAnchorsFixtureIsNinePairsInThreeScales is the checkout's half.
//
// A clean clone has no `models/` at all, so this is the assertion that the
// repository itself carries a decodable set: nine pairs, three scales of three,
// every pair a positive area. The yolov4-416 set is 3x9 and the sidecar's
// `load_anchors` refuses anything that does not divide into three.
func TestTheAnchorsFixtureIsNinePairsInThreeScales(t *testing.T) {
	b, err := anchorsFixtureBytes()
	if err != nil {
		t.Fatalf("the committed anchors copy could not be read: %v", err)
	}
	scales, err := parseAnchorsFile(b)
	if err != nil {
		t.Fatalf("the committed anchors file does not parse: %v", err)
	}
	if len(scales) != 3 {
		t.Fatalf("the file parsed into %d scales, want 3: a three-scale darknet head is read by three", len(scales))
	}
	total := 0
	for i, s := range scales {
		if len(s) != 3 {
			t.Errorf("scale %d holds %d anchors, want 3 (the yolov4-416 set is 3 per scale)", i, len(s))
		}
		for _, p := range s {
			if p.W <= 0 || p.H <= 0 {
				t.Errorf("scale %d holds an anchor with no area: %+v", i, p)
			}
		}
		total += len(s)
	}
	if total != 9 {
		t.Fatalf("the file holds %d anchors, want 9", total)
	}
	// And the numbers are the recorded yolov4-416 set, so a substitution is
	// caught here rather than by a sidecar decoding the wrong box sizes for a
	// whole run and reporting a clean scene while doing it.
	want := "10,13|16,30|33,23|30,61|62,45|59,119|116,90|156,198|373,326"
	var got []string
	for _, s := range scales {
		for _, p := range s {
			got = append(got, trimFloat(p.W)+","+trimFloat(p.H))
		}
	}
	if strings.Join(got, "|") != want {
		t.Fatalf("the anchors are %s, want the recorded yolov4-416 set %s", strings.Join(got, "|"), want)
	}
}

// TestAMalformedAnchorsFileIsRefused is the shape half, asserted as refusals.
//
// Each case is a file that would decode without complaining if the check were
// only advisory: eight pairs read as 3/3/2, ten as 3/3/4, a blank file as a
// sidecar that never sees a box, and a line with three numbers as a parse that
// silently drops one. The sidecar's `load_anchors` refuses all four; this is
// the same rule on the writing side, so a fetch never puts them on disk.
func TestAMalformedAnchorsFileIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"eight pairs do not make three scales", "10,13\n16,30\n33,23\n30,61\n62,45\n59,119\n116,90\n156,198\n", "three scales"},
		{"ten pairs do not either", "10,13\n16,30\n33,23\n30,61\n62,45\n59,119\n116,90\n156,198\n373,326\n12,14\n", "three scales"},
		{"an empty file holds no anchors", "# a comment and nothing else\n\n", "no anchors"},
		{"three numbers is not a pair", "10,13,16\n", "not a `width,height` pair"},
		{"a non-numeric anchor", "ten,thirteen\n", "non-numeric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseAnchorsFile([]byte(tc.body)); err == nil {
				t.Fatalf("a malformed anchors file was accepted")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}

// TestTheAnchorsBytesAreThePinnedOnes is the digest half, asserted against a
// corrupted copy rather than only against the good one: a pin nobody has seen
// refuse anything is a comment.
func TestTheAnchorsBytesAreThePinnedOnes(t *testing.T) {
	b, err := anchorsFixtureBytes()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := verifyAnchors(b); err != nil {
		t.Fatalf("the embedded anchors file does not match its own pin: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"one anchor changed", func(b []byte) []byte { return []byte(strings.Replace(string(b), "373,326", "373,327", 1)) }},
		{"a pair dropped", func(b []byte) []byte { return []byte(strings.Replace(string(b), "373,326\n", "", 1)) }},
		{"a byte appended", func(b []byte) []byte { return append(b, '\n') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyAnchors(tc.mutate(b)); err == nil {
				t.Fatal("corrupted anchors bytes matched the pinned digest")
			} else if !strings.Contains(err.Error(), "not the recorded yolov4-416 anchors") {
				t.Fatalf("the refusal does not name what the bytes are: %v", err)
			}
		})
	}
}

// TestTheAnchorsFileTheDemoDerivesIsThePinnedOne is the machine's half.
//
// The demo derives the anchors path as the model's sibling and `config` refuses
// a missing one by name, so a checkout with no `models/` never runs â€” but a
// checkout WITH one that nobody verified is the silent case this test covers.
// When the file is there it must be the pinned bytes, not a hand-edited copy.
func TestTheAnchorsFileTheDemoDerivesIsThePinnedOne(t *testing.T) {
	root, err := pocModuleRoot()
	if err != nil {
		t.Skipf("the module root could not be found, so the derived anchors file could not be located: %v", err)
	}
	derived := anchorsBeside(filepath.Join(root, "models", "yolov4n.onnx"), root)
	onDisk, err := os.ReadFile(derived)
	if err != nil {
		t.Skipf("%s is not on this machine (a clean clone has no models/; `pocctl fetch-model` writes it "+
			"from the committed copy). The fixture assertions above still hold.", derived)
	}
	if err := verifyAnchors(onDisk); err != nil {
		t.Fatalf("the anchors file the demo derives (%s) is not the pinned one: %v", derived, err)
	}
	fixture, err := anchorsFixtureBytes()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(onDisk) != string(fixture) {
		t.Fatalf("%s differs from the committed copy pyworker/fixtures/yolov4n.anchors.txt. The two are "+
			"one record: `pocctl fetch-model` writes the committed copy beside the weights, so a "+
			"difference means one of them was edited alone", derived)
	}
}

// TestTheAnchorsFileIsWrittenBesideTheModel covers the write half.
//
// A fetch that names the wrong destination is refused rather than writing the
// model's anchors beside a file nobody asked about, and the file it does write
// carries the pinned digest â€” which is what makes `fetch-model` a provenance
// step for the anchors and not a copy step.
func TestTheAnchorsFileIsWrittenBesideTheModel(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "models", "yolov4n.onnx")
	if err := os.MkdirAll(filepath.Dir(model), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("not really 257 MB of weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := writeAnchorsBeside(model)
	if err != nil {
		t.Fatalf("the anchors file was not written beside the model: %v", err)
	}
	if want := anchorsBeside(model, ""); path != want {
		t.Fatalf("wrote %s, want the model's sibling %s", path, want)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the written file could not be read back: %v", err)
	}
	if err := verifyAnchors(written); err != nil {
		t.Fatalf("the file written beside the model is not the pinned anchors: %v", err)
	}
	// A second write over a good file is a no-op that keeps the digest, which
	// is what makes `bootstrap` able to call it in the no-download branch.
	if _, err := writeAnchorsBeside(model); err != nil {
		t.Fatalf("writing over an existing pinned anchors file failed: %v", err)
	}
	// A hand-edited file is replaced, not kept: a fetch that quietly preserved
	// an operator's copy would leave the sidecar decoding with numbers nobody
	// recorded.
	if err := os.WriteFile(path, []byte("1,2\n3,4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if _, err := writeAnchorsBeside(model); err != nil {
			t.Fatalf("the rewritten anchors file failed: %v", err)
		}
	})
	if !strings.Contains(out, "did NOT match") {
		t.Errorf("the rewrite does not say the file on disk was not the pinned one: %s", out)
	}
	// And a destination that is not an .onnx path is refused: the anchors are
	// the sibling of an ONNX export, not of any file somebody pointed at.
	if _, err := writeAnchorsBeside(filepath.Join(dir, "weights.bin")); err == nil {
		t.Fatal("the anchors file was written beside a non-ONNX destination")
	}
}

// trimFloat renders an anchor number the way the cfg spells it, without a
// trailing ".0" â€” the comparison is against the recorded set, and a float
// formatting difference is not a content difference.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
