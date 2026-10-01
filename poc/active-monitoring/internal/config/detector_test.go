package config

import (
	"strings"
	"testing"
)

// TestTheScoreChainMustReadTheRightWay is RC-19, and it is the one configuration
// rule here that catches a defect no downstream check can.
//
// The chain is:
//
//	SP_POC_DET_SCORE  <=  SP_POC_GHOST_MIN_SCORE  <=  SP_POC_SCORE_THRESHOLD
//	   model emit floor      draw floor, recovery      approval bar
//
// The right-hand link (GHOST <= THRESHOLD) was already enforced. This test covers
// the left-hand one, and the reason it needs its own test is that an inverted
// chain lies in BOTH directions simultaneously:
//
//   - with DET_SCORE above GHOST_MIN_SCORE, a candidate the model scored BELOW
//     its own emit floor is still drawn as a ghost — the picture shows an object
//     the model said was not there;
//   - and a candidate the model scored above its emit floor is refused below the
//     ghost floor — the picture omits an object that was there.
//
// Neither half is individually detectable downstream. Every arithmetic still
// runs, every row is still well-formed, and the report is still a clean table of
// numbers computed over a picture that is wrong in both directions at once. Only
// the ordering is checkable, so it is checked by name.
func TestTheScoreChainMustReadTheRightWay(t *testing.T) {
	world := detectorWorld(t)
	if _, err := Load(lookupFrom(world)); err != nil {
		t.Fatalf("the detector world must load before anything is broken in it: %v", err)
	}

	world["SP_POC_DET_SCORE"] = "0.30"
	world["SP_POC_GHOST_MIN_SCORE"] = "0.10"
	world["SP_POC_SCORE_THRESHOLD"] = "0.45"
	_, err := Load(lookupFrom(world))
	if err == nil {
		t.Fatal("an inverted score chain was accepted: an emit floor above the ghost floor means the " +
			"picture shows objects the model scored below its own floor and omits the ones it did not")
	}
	for _, want := range []string{
		"SP_POC_DET_SCORE", "SP_POC_GHOST_MIN_SCORE", "inverted", "chain",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	// Equality at the left link is a deliberate choice — a zero-width band between
	// the emit floor and the ghost floor means "nothing is ever drawn as a ghost",
	// which is a real setting and not a contradiction. It must be ALLOWED, or the
	// refusal above would be refusing a spelling as well as an inversion.
	world["SP_POC_DET_SCORE"] = "0.10"
	if _, err := Load(lookupFrom(world)); err != nil {
		t.Fatalf("an equal emit floor and ghost floor is a deliberate zero-width band, not an inversion: %v", err)
	}
}

// TestTheDetectorVariablesAreNotAskedOfAMotionRun is the gating, and it is an
// assertion rather than an omission.
//
// The detector's eight variables describe an ONNX file, a runtime library and a
// thread count. A `novelty`-mode run opens none of them, and requiring them would
// mean an operator running the licence-free path has to declare a model they will
// never load — which is how a "no dependencies" path stops being one.
func TestTheDetectorVariablesAreNotAskedOfAMotionRun(t *testing.T) {
	world := complete(t)
	world["SP_POC_TRIGGER_MODE"] = TriggerModeNovelty
	for name := range conditionalValues() {
		delete(world, name)
	}
	// Also drop the sidecar variables, so this really is a motion run and not a
	// novelty run carrying a Python model.
	delete(world, "SP_POC_AI_MODEL")
	delete(world, "SP_POC_SIDECAR_SCRIPT")
	delete(world, "SP_POC_AI_ANCHORS")
	world["SP_POC_VLM_PROVIDER"] = "none"
	delete(world, "SP_POC_VLM_REGION")
	delete(world, "SP_POC_VLM_API_KEY")
	delete(world, "SP_POC_VLM_MODEL")
	delete(world, "SP_POC_VLM_DAILY_CAP")

	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("a motion run must not be asked about ONNX files, a runtime library or a thread count: %v", err)
	}
	if cfg.TriggerMode != TriggerModeNovelty {
		t.Fatalf("trigger mode = %q", cfg.TriggerMode)
	}
	// And the box log is required in EVERY mode, because the tracker's replay
	// depends on it whatever produced the boxes.
	if cfg.BoxLogFrames != 20000 {
		t.Fatalf("box log frames = %d; the per-frame log is bounded in every mode", cfg.BoxLogFrames)
	}
}

// TestAIDetectionRateOfZeroMeansEveryFrame is the one new spelling this phase
// added, and it is a semantic change to an existing variable rather than a new
// one.
//
// SP_POC_AI_FPS used to be validated as "at least one", which made the most
// demanding mode — full-rate detection — the one mode the configuration could not
// express. Refusing the spelling would not have been stricter; it would have made
// the throughput check unreachable, because the arithmetic that enforces it needs a
// rate to multiply the measured p99 by.
//
// Zero is therefore not "unset" and not "off". It is "every decoded frame", and
// the demand it implies is enforced where the measurement exists: preflight
// refuses by name when det_p99 x ai_fps cannot fit in a second.
func TestAIDetectionRateOfZeroMeansEveryFrame(t *testing.T) {
	world := complete(t)
	world["SP_POC_AI_FPS"] = "0"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("AI_FPS=0 must load: it means every decoded frame, which is the mode the detector runs in: %v", err)
	}
	if cfg.AIFPS != 0 {
		t.Fatalf("AI_FPS = %d, want 0", cfg.AIFPS)
	}

	// Negative is still a refusal. Zero is a rate; minus one is not.
	world["SP_POC_AI_FPS"] = "-1"
	if _, err := Load(lookupFrom(world)); err == nil {
		t.Fatal("a negative detection rate was accepted")
	} else if !strings.Contains(err.Error(), "SP_POC_AI_FPS") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

// TestTheGlobalSuppressionPassMustBeTheLooserOne is the second NMS rule, and it
// exists because the two passes do different jobs that cannot be swapped.
//
// The class-aware pass collapses duplicates of ONE class. The class-agnostic pass
// exists to collapse a `car` box and a `truck` box on the SAME object — a pair the
// class-aware pass structurally cannot touch, because it never compares across
// classes. So the global pass only earns its name by being looser. Inverted, it
// duplicates the first pass's work and every cross-class pair survives as two boxes
// and two crossings, which is a false-positive rate that measures the taxonomy
// rather than the detector.
func TestTheGlobalSuppressionPassMustBeTheLooserOne(t *testing.T) {
	world := detectorWorld(t)
	world["SP_POC_DET_NMS_CLASS"] = "0.75"
	world["SP_POC_DET_NMS_GLOBAL"] = "0.45"
	_, err := Load(lookupFrom(world))
	if err == nil {
		t.Fatal("a class-agnostic pass tighter than the class-aware one was accepted")
	}
	for _, want := range []string{"SP_POC_DET_NMS_GLOBAL", "SP_POC_DET_NMS_CLASS", "looser"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestEveryTriggerModeIsAcceptedAndNothingElse is the closed set, in both
// directions. A mode that validates but cannot run is a build that accepts a
// configuration it will fail at the first frame; a mode that runs but does not
// validate is worse, because it runs without having been checked.
func TestEveryTriggerModeIsAcceptedAndNothingElse(t *testing.T) {
	for _, mode := range []string{TriggerModeDetector, TriggerModeNovelty, TriggerModeSidecar} {
		if !ValidTriggerMode(mode) {
			t.Errorf("%q is operated by this build but ValidTriggerMode refuses it", mode)
		}
	}
	for _, mode := range []string{"", "DETECTOR", "yolo", "python", "novelty ", "onnx"} {
		if ValidTriggerMode(mode) {
			t.Errorf("ValidTriggerMode accepts %q, which this build does not operate", mode)
		}
	}

	// The set is ONE declaration. soak and store alias these constants rather
	// than repeating the literals, and this is the test that would notice if one
	// of them were changed to a copy.
	for _, bad := range []string{"motion", "classic", "onnx", "yolo"} {
		world := detectorWorld(t)
		world["SP_POC_TRIGGER_MODE"] = bad
		if _, err := Load(lookupFrom(world)); err == nil {
			t.Errorf("trigger mode %q was accepted", bad)
		} else if !strings.Contains(err.Error(), "SP_POC_TRIGGER_MODE") {
			t.Errorf("the refusal for %q does not name the variable: %v", bad, err)
		}
	}

	// An EMPTY value is not a fourth mode: this module treats empty as unset and
	// falls back to the declared structural default. That is the rule that keeps
	// "a variable set to nothing has said nothing" true for every variable.
	//
	// The world is the DETECTOR one, because the declared default is now `detector`
	// and falling back to it genuinely requires the eight detector variables. Testing
	// the fallback against a sidecar world would have passed for the wrong reason —
	// it would have been testing the sidecar default's requirement set, and the flip
	// would have passed unnoticed.
	world := detectorWorld(t)
	world["SP_POC_TRIGGER_MODE"] = ""
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("an empty trigger mode must fall back to the declared default, not fail: %v", err)
	}
	if got, want := cfg.TriggerMode, schemaByName["SP_POC_TRIGGER_MODE"].structuralVal; got != want {
		t.Fatalf("an empty trigger mode resolved to %q, want the declared default %q", got, want)
	}
	if !ValidTriggerMode(cfg.TriggerMode) {
		t.Fatalf("the declared default %q is not a mode this build operates", cfg.TriggerMode)
	}

	// And the fallback world is REQUIRED to carry its own variables. A default that
	// selects a mode whose variables are not otherwise demanded would be a default
	// that produces a run which cannot be configured — so a detector default with no
	// detector variables present must fail by name.
	bare := complete(t)
	for name := range conditionalValues() {
		delete(bare, name)
	}
	bare["SP_POC_TRIGGER_MODE"] = ""
	if _, err := Load(lookupFrom(bare)); err == nil {
		t.Fatalf("the default resolved to %q with none of its variables present, so a run using the "+
			"default would fail at startup rather than at configuration", schemaByName["SP_POC_TRIGGER_MODE"].structuralVal)
	} else if !strings.Contains(err.Error(), "SP_POC_DET_MODEL") {
		t.Fatalf("the refusal does not name a missing variable: %v", err)
	}
}
