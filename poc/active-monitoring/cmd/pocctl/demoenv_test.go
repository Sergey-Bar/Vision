package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/ai-captain-poc/internal/config"
)

// lookupFrom turns a `NAME=value` slice into the accessor `config` reads through.
//
// It is the whole reason this file exists. The demo builds an environment as a
// slice of strings and hands it to a CHILD PROCESS, so the only thing that has
// ever checked the slice is the child refusing to start. Reading the same slice
// through the same accessor the child uses turns "run it and find out where it
// stopped" into an assertion.
func lookupFrom(env []string) config.Lookup {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// demoEnvFor builds the demo's own environment.
//
// `config.Load` checks that the anchors file, the weights and the runtime
// library can actually be READ, so the paths are real files in a temp directory
// rather than plausible-looking literals. An empty file is enough: the loader
// asks whether the bytes are reachable, and this test is about the schema's
// view of the slice, not about decoding an export.
func demoEnvFor(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	models := filepath.Join(root, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"model.onnx", "model.anchors.txt", ortLibraryName()} {
		if err := os.WriteFile(filepath.Join(models, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pyworker", "server.py"), nil, 0o600); err != nil {
		if mkErr := os.MkdirAll(filepath.Join(root, "pyworker"), 0o755); mkErr != nil {
			t.Fatalf("mkdir pyworker: %v", mkErr)
		}
		if err2 := os.WriteFile(filepath.Join(root, "pyworker", "server.py"), nil, 0o600); err2 != nil {
			t.Fatalf("write server.py: %v", err2)
		}
	}
	model := filepath.Join(models, "model.onnx")
	return demoEnv(root, filepath.Join(root, ".demo-data"), filepath.Join(root, "clip.mp4"),
		filepath.Join(root, "zones.json"), model, anchorsBeside(model, root),
		"127.0.0.1:8098", "127.0.0.1:8097", 8, &relay{})
}

// TestTheDemoEnvironmentIsOneTheDemoOwnChildWouldAccept is the control for the
// whole defect class this repository keeps re-finding: a hand-built environment
// that falls behind the schema.
//
// `demoEnv` is a slice of 66 strings. Adding a required variable to the schema
// does not add it here, does not fail any test in `internal/config`, and does not
// change one line of `demoEnv`. The result is a demo that assembles an
// environment its own child refuses — and the refusal arrives at step 6 of 7,
// after two builds, a fault rehearsal and a listening simulator, which is the
// most expensive place in this module to discover a missing name.
//
// So the assertion is not "the important variables are present". It is
// `config.Load` on the demo's own slice, which is exactly what `watch` does with
// it, one process earlier. Every schema rule applies: the required-by-name set,
// the `when:` conditions under `SP_POC_TRIGGER_MODE=detector`, the score chain,
// the NMS ordering, and the class taxonomy.
func TestTheDemoEnvironmentIsOneTheDemoOwnChildWouldAccept(t *testing.T) {
	env := demoEnvFor(t)
	lookup := lookupFrom(env)

	// The named form first, so a failure says WHICH variable rather than only
	// that a Config was refused. `Load` reports all of them at once for an
	// operator; here one at a time is more useful, because this failure is read
	// by whoever changed the slice.
	if missing := config.RequiredUnder(lookup); len(missing) > 0 {
		for _, name := range missing {
			t.Errorf("%s is required by the schema and `pocctl demo` does not set it, so `watch` "+
				"refuses to start at step 6 of 7 with a name this file could have printed instead",
				name)
		}
		t.Fatalf("%d required variable(s) missing from demoEnv; the slice is a literal and nothing "+
			"in the schema knows it exists, which is why only running the command found it", len(missing))
	}

	cfg, err := config.Load(lookup)
	if err != nil {
		t.Fatalf("the environment `pocctl demo` builds is one its own child refuses: %v\n\n"+
			"This is the assertion that would have caught the inverted score chain "+
			"(SP_POC_DET_SCORE above SP_POC_GHOST_MIN_SCORE), which killed `pocctl demo` at step 6 "+
			"of 7 while the banner had already told the operator the run was configured.", err)
	}
	// A slice that loads is not enough on its own: it must load AS THE SHIPPED
	// PATH. A demo environment that quietly ran the novelty trigger would satisfy
	// every line above and demonstrate nothing this module now claims to ship.
	if got := cfg.TriggerMode; got != config.TriggerModeDetector {
		t.Errorf("demoEnv builds a %q run, and the shipped path is %q. The demo is the thing an "+
			"operator runs in front of a customer, so it has to demonstrate the path that ships.",
			got, config.TriggerModeDetector)
	}
}

// TestTheScoreChainInTheDemoAscends is the narrower version of the assertion
// above, split out because the chain is the one failure that produces a picture
// rather than an error.
//
// Every other rule in the schema refuses at startup. The score chain does not: an
// inverted chain lies in BOTH directions at once, so a box the model scored below
// its own emit floor is drawn as a ghost while a box it did emit is refused. The
// run completes, the report is written, and every number in it is computed over a
// picture showing objects that are not there and omitting objects that are.
func TestTheScoreChainInTheDemoAscends(t *testing.T) {
	cfg, err := config.Load(lookupFrom(demoEnvFor(t)))
	if err != nil {
		t.Fatalf("the demo's own environment does not load: %v", err)
	}
	if cfg.DetScore > cfg.GhostMinScore {
		t.Errorf("SP_POC_DET_SCORE=%v is above SP_POC_GHOST_MIN_SCORE=%v. The chain must read "+
			"DET_SCORE <= GHOST_MIN_SCORE <= SCORE_THRESHOLD, and it is the only one of the three "+
			"relations that no downstream arithmetic can detect.", cfg.DetScore, cfg.GhostMinScore)
	}
	if cfg.GhostMinScore > cfg.ScoreThreshold {
		t.Errorf("SP_POC_GHOST_MIN_SCORE=%v is above SP_POC_SCORE_THRESHOLD=%v, so the rejected "+
			"band is upside down: everything between them would be drawn dimmed and approved at once.",
			cfg.GhostMinScore, cfg.ScoreThreshold)
	}
}

// TestTheAnchorsFileIsTheSiblingOfTheModelIsAsked covers `--model`.
//
// `SP_POC_DET_MODEL` and `SP_POC_AI_ANCHORS` are two halves of one assertion — an
// anchored export's anchor boxes ship beside its weights under the same base name
// — and they were written as two independent literals in one slice. `--model` has
// no companion flag, so pointing the demo at any model other than the one the
// literal happened to name paired the right weights with the WRONG anchor file,
// and the demo refused at step 1 on a file the operator never asked for.
func TestTheAnchorsFileIsTheSiblingOfTheModelIsAsked(t *testing.T) {
	root := filepath.FromSlash("C:/poc")
	for _, tc := range []struct{ model, want string }{
		{filepath.Join(root, "models", "yolov8n.onnx"), filepath.Join(root, "models", "yolov8n.anchors.txt")},
		{filepath.Join(root, "models", "yolov4n.onnx"), filepath.Join(root, "models", "yolov4n.anchors.txt")},
		{filepath.Join(root, "custom-export.onnx"), filepath.Join(root, "custom-export.anchors.txt")},
		// A RELATIVE path keeps its own directory. The anchors belong BESIDE the
		// weights, and `--model models\yolov8n.onnx` was spelled relative to a
		// working directory; resolving its sibling against the module root instead
		// would point at a different file whenever the two disagree, which is the
		// bug this replaced.
		{filepath.Join("models", "yolov8n.onnx"), filepath.Join("models", "yolov8n.anchors.txt")},
		// A BARE name has no directory to keep, so it resolves the way the demo
		// resolves a bare model: against the module root.
		{"yolov8n.onnx", filepath.Join(root, "yolov8n.anchors.txt")},
		// Not an `.onnx` path: the caller gets the sibling of what they named
		// rather than a guess about an export this code knows nothing of.
		{"weights.bin", "weights.bin.anchors.txt"},
	} {
		if got := anchorsBeside(tc.model, root); got != tc.want {
			t.Errorf("anchorsBeside(%q) = %q, want %q. The demo pairs this with --model, and a "+
				"mismatch here is a run told the sidecar one export's anchors for another export's weights.",
				tc.model, got, tc.want)
		}
	}
}

// TestTheBannerNamesTheShippedTrigger covers printTriggerBanner.
//
// The banner's stated job is to be derived from the configuration rather than
// written beside it. It had branches for `sidecar` and `novelty` and none for
// `detector`, so the mode this module ships with fell through to the
// unrecognised-value arm and the first screen of the headline command printed
//
//	trigger: "detector" — not a mode this build operates. The run will refuse by name.
//
// while the run went on to complete. A closed set of three modes and a
// hand-written branch per mode, where the mode that became the default is the one
// nobody added a branch for.
func TestTheBannerNamesTheShippedTrigger(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		want   string
		absent []string
	}{
		{config.TriggerModeDetector, "in-process ONNX detector", []string{"not a mode this build operates"}},
		{config.TriggerModeSidecar, "per-frame ONNX sidecar", []string{"not a mode this build operates"}},
		{config.TriggerModeNovelty, "local novelty engine", []string{"not a mode this build operates"}},
	} {
		out := captureBanner(t, []string{"SP_POC_TRIGGER_MODE=" + tc.mode, "SP_POC_CLASSES=car"})
		if !strings.Contains(out, tc.want) {
			t.Errorf("banner for %q does not describe that mode; it says %q, want it to name %q",
				tc.mode, strings.TrimSpace(out), tc.want)
		}
		for _, bad := range tc.absent {
			if strings.Contains(out, bad) {
				t.Errorf("banner for %q says %q, which is a statement about a run that does not happen",
					tc.mode, bad)
			}
		}
	}

	// An ABSENT variable is the structural default resolving to a real mode, and
	// it is what a caller that assembled its environment by hand and forgot this
	// one line produces. Calling it "not a mode this build operates" names a value
	// nobody wrote, and the reader cannot act on it.
	out := captureBanner(t, []string{"SP_POC_CLASSES=car"})
	if strings.Contains(out, "not a mode this build operates") {
		t.Errorf("an absent SP_POC_TRIGGER_MODE is reported as an invalid mode; it is the schema's " +
			"structural default for a variable nobody set, and the two need different sentences")
	}
	if !strings.Contains(out, config.TriggerModeDetector) {
		t.Errorf("an absent SP_POC_TRIGGER_MODE does not say which mode the run will use; it printed %q",
			strings.TrimSpace(out))
	}
}

func captureBanner(t *testing.T, env []string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	printTriggerBanner(env)
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	return buf.String()
}
