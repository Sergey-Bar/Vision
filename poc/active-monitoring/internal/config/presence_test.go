package config

// The presence-mode and crop-pass refusals.
//
// Every test in this file states a consequence and a remedy, because that is the shape
// a refusal in this package has: SP_POC_NAME, what breaks, then set it with X. A refusal
// that names a variable without saying what it costs is a lint message, and an operator
// who reads it cannot tell whether the run is broken or merely strict.

import (
	"strings"
	"testing"
)

// TestAnEmptyZonesValueIsNotARefusal: the primary configuration.
//
// It used to be one. SP_POC_ZONES was required because crossing was the only mode, and a
// run with no zone could only produce zero events, so the refusal said exactly that.
// Presence mode made it false, and a guard written against an earlier engine outliving
// the engine is how a primary configuration becomes a refusal.
func TestAnEmptyZonesValueIsNotARefusal(t *testing.T) {
	m := complete(t)
	delete(m, "SP_POC_ZONES")
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("an empty SP_POC_ZONES was refused, and it is the PRIMARY configuration: %v", err)
	}
	if cfg.ZonesPath != "" {
		t.Fatalf("ZonesPath = %q, want the empty string, which is presence mode", cfg.ZonesPath)
	}
	// And an explicitly empty value is the same thing, not a distinct spelling that might
	// behave differently.
	m["SP_POC_ZONES"] = "  "
	cfg2, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("a whitespace SP_POC_ZONES was refused: %v", err)
	}
	if cfg2.ZonesPath != "" {
		t.Fatalf("ZonesPath = %q for a whitespace value, want the empty string", cfg2.ZonesPath)
	}
	// And naming one switches mode, with the path carried through untouched.
	m["SP_POC_ZONES"] = `testdata\zones.json`
	cfg3, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("a named zone file was refused: %v", err)
	}
	if cfg3.ZonesPath != `testdata\zones.json` {
		t.Fatalf("ZonesPath = %q, want the path as given", cfg3.ZonesPath)
	}
}

// TestALiveSourceWithNoLicenceIsStillRefused is the mirror, and it is here because the
// fix above made one refusal optional and the failure mode of that fix is making them
// all optional.
func TestALiveSourceWithNoLicenceIsStillRefused(t *testing.T) {
	m := complete(t)
	delete(m, "SP_POC_ZONES")
	m["SP_POC_SOURCE_KIND"] = "rtsp"
	m["SP_POC_SOURCE_ARG"] = "rtsp://example.invalid/live"
	delete(m, "SP_POC_SOURCE_LICENCE")
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("a live source with no recorded permission basis was accepted. Watching a feed and " +
			"storing clips of it are different acts, and the second is the camera operator's to permit")
	}
	if !strings.Contains(err.Error(), "SP_POC_SOURCE_LICENCE") {
		t.Fatalf("the refusal does not name the variable that is missing: %v", err)
	}
}

// TestTheMinimumCanNeverExceedTheSamplesADwellTimeProduces is the cross-check that was
// missing, and it is the one that decides whether the headline case works.
//
// SP_POC_TRACK_MIN_FRAMES and SP_POC_AI_FPS were validated independently and behaved as
// if no relationship existed, because no relationship had been stated. The combination in
// which the minimum exceeds the samples a crossing-sized object can produce was
// accepted, and a run in that state reports a clean zero detections with no error,
// because the rule that erases the object is working exactly as written.
func TestTheMinimumCanNeverExceedTheSamplesADwellTimeProduces(t *testing.T) {
	m := complete(t)
	m["SP_POC_AI_FPS"] = "2"
	m["SP_POC_TRACK_MIN_FRAMES"] = "9" // 2 per second over a 2 s dwell is 4 samples
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("a minimum of 9 samples at 2 inferences per second was accepted. A crossing-sized object " +
			"is on screen for about two seconds, which is four samples, so every object this run exists " +
			"to report is silently dropped and nothing anywhere says so")
	}
	msg := err.Error()
	for _, want := range []string{
		"SP_POC_TRACK_MIN_FRAMES", // names the variable
		"SP_POC_AI_FPS",           // names the other one
		"SP_POC_CROP_MIN_FRAMES",  // names the second remedy, the one the crop pass exists for
		"zero detections",         // states the consequence
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}
	// And the remedy has to be one that WORKS. Raising AI_FPS to 3 gives 6 samples against
	// a minimum of 9, which is still short, so the message must name 5 or more.
	if !strings.Contains(msg, "at least 5") {
		t.Errorf("the remedy does not name a rate that satisfies the minimum:\n%s", msg)
	}
}

// TestTheCrossCheckDoesNotFireOnAWorkingCombination: a refusal that fires on a
// configuration which works is a refusal that trains people to ignore it.
func TestTheCrossCheckDoesNotFireOnAWorkingCombination(t *testing.T) {
	m := complete(t)
	m["SP_POC_AI_FPS"] = "2"
	m["SP_POC_TRACK_MIN_FRAMES"] = "3" // 2 per second over 2 s is 4 samples, so 3 is reachable
	if _, err := Load(lookupFrom(m)); err != nil {
		t.Fatalf("a reachable minimum was refused: %v", err)
	}
	// And AI_FPS=0 means "every decoded frame", which is as fast as the pipeline goes and
	// must never be refused by this check.
	m["SP_POC_AI_FPS"] = "0"
	m["SP_POC_TRACK_MIN_FRAMES"] = "9"
	if _, err := Load(lookupFrom(m)); err != nil {
		t.Fatalf("AI_FPS=0 with a minimum of 9 was refused: %v. Zero is a RATE here and it means "+
			"every decoded frame", err)
	}
}

// TestTheCropPassOnOffSwitchIsRequiredAndNamed: SP_POC_CROP_PASS is required rather than
// defaulted, because the default is the dangerous one.
func TestTheCropPassOnOffSwitchIsRequiredAndNamed(t *testing.T) {
	m := detectorWorld(t)
	delete(m, "SP_POC_CROP_PASS")
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("a detector run with no statement about the crop pass was accepted. A detector that " +
			"cannot see a bird at 30 m and a report claiming to report everything that passes are the " +
			"same run, and nothing in its output says otherwise")
	}
	if !strings.Contains(err.Error(), "SP_POC_CROP_PASS") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
	// Both answers load, and false is the rollback.
	m["SP_POC_CROP_PASS"] = "false"
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("SP_POC_CROP_PASS=false was refused: %v", err)
	}
	if cfg.CropPass {
		t.Fatal("SP_POC_CROP_PASS=false produced CropPass=true")
	}
	m["SP_POC_CROP_PASS"] = "true"
	cfg, err = Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("SP_POC_CROP_PASS=true was refused: %v", err)
	}
	if !cfg.CropPass {
		t.Fatal("SP_POC_CROP_PASS=true produced CropPass=false")
	}
}

// TestTheSourceAwareMinimumMayNotBeStricterThanTheGlobalOne: a lower bar that is higher
// is not a lower bar.
func TestTheSourceAwareMinimumMayNotBeStricterThanTheGlobalOne(t *testing.T) {
	m := detectorWorld(t)
	m["SP_POC_CROP_PASS"] = "true"
	m["SP_POC_TRACK_MIN_FRAMES"] = "2"
	m["SP_POC_CROP_MIN_FRAMES"] = "5"
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("a crop-sourced minimum of 5 against a whole-frame minimum of 2 was accepted. That " +
			"holds a MAGNIFIED sighting to the stricter bar, so the crop pass costs a whole extra " +
			"inference per sighting and buys nothing")
	}
	msg := err.Error()
	for _, want := range []string{"SP_POC_CROP_MIN_FRAMES", "SP_POC_TRACK_MIN_FRAMES", "STRICTER"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}
	// And the working combination loads.
	m["SP_POC_CROP_MIN_FRAMES"] = "2"
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("a crop minimum at the global minimum was refused: %v", err)
	}
	if cfg.CropMinFrames != 2 || cfg.TrackMinFrames != 2 {
		t.Fatalf("the pair loaded as %d and %d, want 2 and 2", cfg.CropMinFrames, cfg.TrackMinFrames)
	}
}

// TestTheCropPassKnobsHaveTheirDeclaredDefaults: the structural defaults are the
// numbers the report will print, so they are asserted rather than trusted.
func TestTheCropPassKnobsHaveTheirDeclaredDefaults(t *testing.T) {
	m := detectorWorld(t)
	for _, k := range []string{"SP_POC_CROP_MIN_AREA", "SP_POC_CROP_PAD",
		"SP_POC_CROP_MAX_REGIONS", "SP_POC_CROP_MIN_FRAMES"} {
		delete(m, k)
	}
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatalf("the crop pass's structural defaults did not fill in: %v", err)
	}
	if cfg.CropMinArea != 0.0004 {
		t.Errorf("CropMinArea = %v, want 0.0004, a hundredth of a percent of the frame", cfg.CropMinArea)
	}
	if cfg.CropPad != 0.35 {
		t.Errorf("CropPad = %v, want 0.35", cfg.CropPad)
	}
	if cfg.CropMaxRegions != 4 {
		t.Errorf("CropMaxRegions = %d, want 4", cfg.CropMaxRegions)
	}
	if cfg.CropMinFrames != 2 {
		t.Errorf("CropMinFrames = %d, want 2", cfg.CropMinFrames)
	}
}

// TestAZeroRegionCapIsRefusedOnlyWhenThePassIsOn: a cap of zero with the pass off is an
// inert knob; with the pass on it is a run that may infer nothing.
func TestAZeroRegionCapIsRefusedOnlyWhenThePassIsOn(t *testing.T) {
	m := detectorWorld(t)
	m["SP_POC_CROP_PASS"] = "false"
	m["SP_POC_CROP_MAX_REGIONS"] = "0"
	if _, err := Load(lookupFrom(m)); err != nil {
		t.Fatalf("an inert knob was refused while the pass was off: %v", err)
	}
	m["SP_POC_CROP_PASS"] = "true"
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("an enabled crop pass with a cap of zero was accepted. That is a novelty engine with " +
			"an inference budget of zero, and it would look exactly like a detector that cannot see " +
			"small objects")
	}
	if !strings.Contains(err.Error(), "SP_POC_CROP_MAX_REGIONS") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

// TestANegativeCropPadIsRefused: a negative pad crops away part of the region it is
// meant to give context to.
func TestANegativeCropPadIsRefused(t *testing.T) {
	m := detectorWorld(t)
	m["SP_POC_CROP_PASS"] = "true"
	m["SP_POC_CROP_PAD"] = "-0.2"
	_, err := Load(lookupFrom(m))
	if err == nil || !strings.Contains(err.Error(), "SP_POC_CROP_PAD") {
		t.Fatalf("a negative crop pad was accepted: %v", err)
	}
}

// TestCeilDivRoundsUp: the remedy half of a refusal has to name a value that works.
//
// Rounding down would print SP_POC_AI_FPS=1 for a minimum of 3 over a 2 s dwell, which
// is still one sample short, so an operator who followed the advice exactly would be
// refused again. A refusal whose remedy does not work teaches people that these messages
// are approximate.
func TestCeilDivRoundsUp(t *testing.T) {
	for _, c := range []struct{ a, b, want int }{
		{3, 2, 2}, {4, 2, 2}, {5, 2, 3}, {1, 1, 1}, {0, 2, 0},
	} {
		if got := ceilDiv(c.a, c.b); got != c.want {
			t.Errorf("ceilDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// And the guard: a non-positive divisor cannot divide, and must not be reached from
	// the message path.
	if got := ceilDiv(7, 0); got != 7 {
		t.Errorf("ceilDiv(7, 0) = %d, want 7 returned unchanged rather than a division by zero", got)
	}
}
