package main

import (
	"os"
	"path/filepath"
	"testing"

	tax "github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

// The two refusals that are about WHAT MUST NOT HAPPEN, rather than about a
// number being wrong:
//
//   - a benchmark over a FINISHED run must not open a model (RC-17), because a
//     benchmark that re-decodes the material measures the model rather than the
//     run, and the two disagree whenever the run used different settings — which is
//     most of the time.
//   - fetch-groundtruth must not write its manifest over the EVENT marks file,
//     because both are called "ground truth" and only one of them is.
//
// Both were found by the red-check table complaining that its own controls had no
// test behind them, which is the gate working.

// TestARunBenchmarkRefusesToOpenAModel is RC-17's subject.
//
// It exists because the control was a bare `if` condition inside `benchRun`, and
// removing it left the suite GREEN. A control no test watches is a comment, and the
// first thing this gate caught was its own table.
func TestARunBenchmarkRefusesToOpenAModel(t *testing.T) {
	// `refusesToOpenModel` reads SP_POC_ORT_LIB from the ENVIRONMENT on purpose —
	// that is RC-17 — so this test has to pin that variable or it fails on every
	// machine where the runtime is actually installed, which is every machine this
	// gate is written for. A test that only passes on an unconfigured machine is a
	// test that measures the machine rather than the code.
	t.Setenv("SP_POC_ORT_LIB", "")
	t.Setenv("SP_POC_DET_MODEL", "")

	// Nothing named: nothing to refuse.
	if refusesToOpenModel("", "") {
		t.Error("a benchmark with no model and no library named thinks it was asked to open one")
	}

	// Either one is enough to trigger the refusal, because the library alone is
	// enough to load a session: naming a model with no library fails to open, and
	// naming a library with no model opens nothing but proves the intent.
	for _, tc := range []struct {
		name       string
		lib, model string
	}{
		{"a library only", `models\onnxruntime.dll`, ""},
		{"a model only", "", "models/yolov8n.onnx"},
		{"both", `models\onnxruntime.dll`, "models/yolov8n.onnx"},
		{"a relative library", "onnxruntime.dll", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !refusesToOpenModel(tc.lib, tc.model) {
				t.Fatalf("%s did not trigger the refusal", tc.name)
			}
		})
	}

	// Whitespace is not a path. An operator whose shell has SP_POC_ORT_LIB=" "
	// has named nothing, and treating that as a named library would print a
	// refusal for a configuration that has none — which is a gate that lies.
	if refusesToOpenModel("   ", "") {
		t.Error("a whitespace-only library path was treated as a named library")
	}

	// And the ambient case the comment above describes, asserted rather than assumed:
	// a library in the shell is a library named, with no flag anywhere.
	t.Setenv("SP_POC_ORT_LIB", `models\onnxruntime.dll`)
	if !refusesToOpenModel("", "") {
		t.Error("a library in the environment did not trigger the refusal, which is the whole reason the " +
			"predicate reads the environment and not only the flags")
	}
	t.Setenv("SP_POC_ORT_LIB", "   ")
	if refusesToOpenModel("", "") {
		t.Error("a whitespace-only library in the environment was treated as a named library")
	}
}

// TestTheRunBenchmarkAlsoReadsTheEnvironment is the half that is easy to miss.
//
// The flags are the less likely route. An operator with SP_POC_ORT_LIB exported in
// their shell and a run measurement requested would otherwise get a model opened
// behind their back, and the refusal would never print because the flags were
// empty.
func TestTheRunBenchmarkAlsoReadsTheEnvironment(t *testing.T) {
	t.Setenv("SP_POC_ORT_LIB", `models\onnxruntime.dll`)
	if !refusesToOpenModel("", "") {
		t.Fatal("an exported SP_POC_ORT_LIB did not trigger the refusal. The flags were empty, so a " +
			"run measurement would have opened a model behind the operator's back")
	}
}

// TestTheStillManifestCannotOverwriteTheEventMarks is the one that is genuinely
// unrecoverable if it goes wrong.
//
// `testdata/groundtruth.json` holds a person's judgements about PUSHED EVENTS.
// `fetch-groundtruth` writes a manifest about a STILL SET. Both are called
// "ground truth", the paths are one directory apart, and writing the second over
// the first would destroy the only record of a marking session that ever happened
// — silently, because nothing about either command says they are the same file.
func TestTheStillManifestCannotOverwriteTheEventMarks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidate  string
		wantRefuse bool
	}{
		{"the still manifest's own path", groundtruthManifest, false},
		{"the event marks", eventGroundtruth, true},
		{"the event marks, relative", `./testdata/groundtruth.json`, true},
		{"the event marks, dotted", `testdata/./groundtruth.json`, true},
		{"something else entirely", "testdata/other.json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if sameFile(tc.candidate, eventGroundtruth) != tc.wantRefuse {
				t.Fatalf("sameFile(%q, eventGroundtruth) = %v, want %v",
					tc.candidate, sameFile(tc.candidate, eventGroundtruth), tc.wantRefuse)
			}
		})
	}

	// And the two paths must actually be DIFFERENT, or the guard above is guarding
	// a collision that cannot happen and the real collision is unguarded.
	if sameFile(groundtruthManifest, eventGroundtruth) {
		t.Fatalf("the still manifest and the event marks are the same file (%s); one of them will "+
			"be destroyed by the other and the guard above protects neither", groundtruthManifest)
	}
}

// TestTheStillManifestCarriesBothLicencesSeparately, because Open Images' images
// and its annotation CSVs carry different terms and a manifest naming one of them
// is a manifest somebody relied on and was wrong.
func TestTheStillManifestCarriesBothLicencesSeparately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "still.json")

	// A manifest with one licence blank is REFUSED, not written with a dash.
	partial := GroundtruthManifest{ImageLicence: "CC BY 2.0", AnnotationLicence: ""}
	if err := writeGroundtruthManifest(path, partial); err == nil {
		t.Fatal("a manifest naming one of the two licences was written")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the refused manifest was written anyway")
	}
	if err := writeGroundtruthManifest(path, GroundtruthManifest{AnnotationLicence: "CC BY 4.0"}); err == nil {
		t.Fatal("a manifest naming only the annotation licence was written")
	}

	both := GroundtruthManifest{
		ImageLicence:      "CC BY 2.0",
		AnnotationLicence: "CC BY 4.0",
		Attribution:       "Open Images",
	}
	if err := writeGroundtruthManifest(path, both); err != nil {
		t.Fatalf("a manifest naming both licences was refused: %v", err)
	}
	got, err := readGroundtruthManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ImageLicence != both.ImageLicence || got.AnnotationLicence != both.AnnotationLicence {
		t.Fatalf("the licences did not survive: %+v", got)
	}
}

// TestTheOpenImagesClassMappingIsClosed is the taxonomy rule, checked at the
// boundary where it is applied.
//
// A class with no COCO equivalent drops its box with the class NAMED. Mapping
// `hair drier` onto `toothbrush` to keep a denominator would report a recall the
// benchmark did not measure.
func TestTheOpenImagesClassMappingIsClosed(t *testing.T) {
	for _, tc := range []struct{ oi, want string }{
		{"Car", "car"}, {"Truck", "truck"}, {"Bus", "bus"},
		{"Motorcycle", "motorcycle"}, {"Bicycle", "bicycle"},
		{"Traffic light", "traffic light"}, {"Traffic signal", "traffic light"},
		{"Stop sign", "stop sign"}, {"Fire hydrant", "fire hydrant"},
		// A person is split four ways in Open Images and one in COCO, and a
		// detector that found the body found the person.
		{"Person", "person"}, {"Human body", "person"}, {"Human face", "person"},
	} {
		got, ok := cocoEquivalent[tc.oi]
		if !ok {
			t.Errorf("%q has no mapping and should", tc.oi)
			continue
		}
		if got != tc.want {
			t.Errorf("%q maps to %q, want %q", tc.oi, got, tc.want)
		}
	}

	// And every mapping lands on a REAL COCO name. A mapping to a word the
	// vocabulary does not have would produce a box the taxonomy cannot place, which
	// is the drop the mapping exists to avoid.
	for oi, coco := range cocoEquivalent {
		if tax.FamilyOf(coco) == "" {
			t.Errorf("%q maps to %q, which the taxonomy cannot place", oi, coco)
		}
	}

	// And the classes that must NOT be mapped, because no COCO word covers them.
	for _, oi := range []string{"Hair drier", "Toothbrush", "Scissors", "Teddy bear", "Bookshelf"} {
		if _, ok := cocoEquivalent[oi]; ok {
			t.Errorf("%q is mapped to something, and it should be dropped with its class named: "+
				"COCO has no word for it and mapping it anyway would report a recall the benchmark "+
				"did not measure", oi)
		}
	}
}
