package novelty

// Phase 0.6.4: the view-change detector.
//
// A moved camera is the strongest demo in this project if it is done deliberately: the
// system says ONCE that the camera moved and says nothing else. A system that reacts
// to a moved camera with fifty false detections is worse than one that reacts to
// nothing, because the operator has been told the camera moved AND that a hundred
// things happened, and the second of those is false.
//
// These tests are about the rule and about what it refuses.

import (
	"strings"
	"testing"
)

// movedScore is a frame where the camera moved: large illumination offset, wide spread.
func movedScore(illum, spread float64) Score {
	return Score{Mode: ModeTemporal, Illumination: illum, Spread: spread, Reason: ReasonChanged, Value: 0.6}
}

// quietScore is an ordinary frame with a car crossing: no whole-frame brightness shift.
func quietScore() Score {
	return Score{Mode: ModeTemporal, Illumination: 0.4, Spread: 2.0, Reason: ReasonChanged, Value: 0.05}
}

func newTestViewChange(t *testing.T) *ViewChange {
	t.Helper()
	v, err := NewViewChange(ViewChangeDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestOneCameraMoveIsDeclaredOnceAndNotOncePerFrame is the headline control.
//
// A moved camera produces an enormous novelty spike over several consecutive frames.
// A detector that fires on each of them declares the same camera move five times, and
// the operator reads five camera faults for one physical event.
func TestOneCameraMoveIsDeclaredOnceAndNotOncePerFrame(t *testing.T) {
	v := newTestViewChange(t)
	var changes, settling int
	for i := 0; i < 10; i++ {
		switch v.Observe(movedScore(-40, 30)) {
		case ViewChanged:
			changes++
		case ViewSettling:
			settling++
		}
	}
	if changes != 1 {
		t.Fatalf("ten frames of a moved camera produced %d change declarations, want 1. Ten frames of "+
			"a camera settling is ONE physical event, and an operator reading ten camera faults for one "+
			"move learns to ignore the line that says the camera moved", changes)
	}
	if settling == 0 {
		t.Fatal("no frame reported as settling. Suppression with no name is silence, and silence in a " +
			"security pipeline reads as \"nothing happened\"")
	}
	if v.Changes() != 1 {
		t.Fatalf("the detector reports %d changes, want 1", v.Changes())
	}
	if v.SuppressedFrames() == 0 {
		t.Fatal("no frame was counted as suppressed, so the gate exists and reports nothing")
	}
	if !v.Settling() {
		t.Fatal("the detector is not settling ten frames after a camera move")
	}
}

// TestACarCrossingIsNotACameraMove is the false-positive half, and it is the half that
// makes the rule usable.
//
// A car crossing the frame changes part of it. A camera panning changes all of it. The
// difference is visible in what the novelty engine already computes: the frame-wide
// illumination offset it removed, and how widely the residual is distributed.
func TestACarCrossingIsNotACameraMove(t *testing.T) {
	v := newTestViewChange(t)
	for i := 0; i < 50; i++ {
		if got := v.Observe(quietScore()); got != ViewStable {
			t.Fatalf("frame %d of an ordinary crossing reported %v. A car changes part of the frame and "+
				"leaves the frame-wide brightness alone; a rule that fired here would suppress every "+
				"crossing in a car park", i, got)
		}
	}
	if v.Changes() != 0 {
		t.Fatalf("an ordinary crossing declared %d camera changes", v.Changes())
	}
	if v.SuppressedFrames() != 0 {
		t.Fatalf("an ordinary crossing suppressed %d frames", v.SuppressedFrames())
	}
}

// TestABrightButLocalChangeIsNotACameraMove: illumination alone is not enough.
//
// A cloud crossing a window, or a light being switched on, moves the whole frame's luma
// without moving the camera. That is why the rule needs BOTH thresholds, and this is
// the assertion that keeps the spread term in it.
func TestABrightButLocalChangeIsNotACameraMove(t *testing.T) {
	v := newTestViewChange(t)
	// A huge brightness shift with a narrow residual: the offset was large and the
	// engine removed almost all of it.
	for i := 0; i < 10; i++ {
		if got := v.Observe(movedScore(-60, 1.0)); got != ViewStable {
			t.Fatalf("a global brightness change with a narrow residual reported %v. A light being "+
				"switched on looks exactly like that, and suppressing every presence event for the "+
				"duration of a cloud would be worse than the flood it prevents", got)
		}
	}
	// And the mirror: a wide residual with no brightness shift. A global compression
	// change does that, and a camera move through a doorway does too.
	v2 := newTestViewChange(t)
	for i := 0; i < 10; i++ {
		if got := v2.Observe(movedScore(0.1, 40)); got != ViewStable {
			t.Fatalf("a wide residual with no brightness shift reported %v", got)
		}
	}
	// Both together, over the sustain window, IS a camera move.
	v3 := newTestViewChange(t)
	declared := 0
	for i := 0; i < 4; i++ {
		if v3.Observe(movedScore(-60, 40)) == ViewChanged {
			declared++
		}
	}
	if declared != 1 {
		t.Fatalf("a large brightness shift AND a wide residual together declared %d changes, want 1",
			declared)
	}
}

// TestOneFrameOfMovementIsNotACameraMove is the anti-jitter half.
//
// One frame of a wide change is a door opening, a curtain, or a compression artefact. It
// is exactly the case the track-minimum rule exists to reject, and applying a different
// standard to the same phenomenon is how a pipeline ends up with a jitter rule and a
// no-jitter rule.
func TestOneFrameOfMovementIsNotACameraMove(t *testing.T) {
	v := newTestViewChange(t)
	if got := v.Observe(movedScore(-60, 40)); got != ViewStable {
		t.Fatalf("a SINGLE frame of movement reported %v. The default requires %d consecutive frames, and "+
			"one frame is exactly the single-frame noise the whole minimum-frames rule exists to reject",
			got, ViewChangeDefaults().SustainFrames)
	}
	if v.Observe(quietScore()) != ViewStable {
		t.Fatal("the frame after the movement did not return to stable")
	}
	if v.Observe(movedScore(-60, 40)) != ViewStable {
		t.Fatal("two non-consecutive frames of movement declared a change. The rule counts CONSECUTIVE " +
			"agreement, and two events an hour apart are two events")
	}
	// Two in a row, which is the configured sustain.
	if got := v.Observe(movedScore(-60, 40)); got != ViewChanged && v.Observe(movedScore(-60, 40)) != ViewChanged {
		t.Fatal("consecutive movement never declared a change")
	}
}

// TestASecondMoveIsNotDeclaredOnItsFirstFrame is the control on clearing the agreement
// counter.
//
// The first red-check of this file tried to protect "one move is declared once" by
// clearing `agree`, and the suite stayed GREEN. The reason is that the settling window
// already covers the frames after a change, so the counter is never read again during
// that stretch and the mutation is invisible to a test that only moves a camera once.
//
// The defect it would actually cause shows up on the SECOND move: with `agree` left at
// its high-water mark, a second camera move one minute later is declared on its FIRST
// frame instead of its second -- which is exactly the single-frame false positive the
// sustain rule exists to reject, reappearing on every move after the first.
func TestASecondMoveIsNotDeclaredOnItsFirstFrame(t *testing.T) {
	v := newTestViewChange(t)
	settle := ViewChangeDefaults().SettleFrames
	sustain := ViewChangeDefaults().SustainFrames

	// First move.
	if got := v.Observe(movedScore(-60, 40)); got == ViewChanged {
		t.Fatal("one frame of movement declared the first move")
	}
	if got := v.Observe(movedScore(-60, 40)); got != ViewChanged {
		t.Fatalf("two frames of movement reported %v, want a declaration", got)
	}
	// Let it settle.
	for i := 0; i < settle; i++ {
		v.Observe(quietScore())
	}
	if v.Settling() {
		t.Fatal("the window never closed")
	}

	// Second move, an hour later, and the question is WHICH frame declares it.
	first := v.Observe(movedScore(-60, 40))
	if first == ViewChanged {
		t.Fatalf("the second move was declared on its FIRST frame. The agreement counter was not " +
			"cleared, so it was still above the sustain threshold from the first move an hour ago, " +
			"and every move after the first is now one frame of noise away from a camera fault")
	}
	var second ViewVerdict
	for i := 1; i < sustain; i++ {
		second = v.Observe(movedScore(-60, 40))
	}
	if second != ViewChanged {
		t.Fatalf("the second move was never declared; last frame reported %v", second)
	}
	if v.Changes() != 2 {
		t.Fatalf("two physical moves declared %d changes", v.Changes())
	}
}

// TestAFrameWithNoMeasurementDecidesNothing: a corrupt frame is not a camera fault.
//
// Deciding on a frame the engine could not score is how a detector starts declaring
// camera faults on a run whose camera is fine. A corrupt frame is neither a change nor a
// confirmation, and it must not extend the settling window either: a stream of corrupt
// frames would otherwise hold the gate shut indefinitely.
func TestAFrameWithNoMeasurementDecidesNothing(t *testing.T) {
	v := newTestViewChange(t)
	for _, reason := range []Reason{ReasonCorrupt, ReasonFirstFrame} {
		if got := v.Observe(Score{Reason: reason, Illumination: -90, Spread: 80}); got != ViewStable {
			t.Fatalf("a frame whose reason is %q reported %v", reason, got)
		}
	}
	if v.Changes() != 0 {
		t.Fatalf("unmeasurable frames declared %d camera changes", v.Changes())
	}
	// And it does not consume a sustain frame, so a real move straight afterwards is
	// still declared.
	v.Observe(movedScore(-60, 40))
	if got := v.Observe(movedScore(-60, 40)); got != ViewChanged {
		t.Fatalf("a real move after an unmeasurable frame reported %v. The gap reset the agreement "+
			"counter, so the next two frames would start again -- correct, but only if the reset is "+
			"what happens", got)
	}
}

// TestSuppressCanBeOpenedWithoutClaimingADetection: an un-settable detector is a
// detector that suppresses forever.
//
// An operator who knows the camera moved, or a supervisor restarting after one, has to
// be able to gate presence WITHOUT the system claiming it detected anything. A gate that
// can only be opened by a detection is a gate whose state cannot be set from outside.
func TestSuppressCanBeOpenedWithoutClaimingADetection(t *testing.T) {
	v := newTestViewChange(t)
	if v.Changes() != 0 {
		t.Fatal("the detector starts with a change already counted")
	}
	v.Suppress()
	if !v.Settling() {
		t.Fatal("Suppress did not open the settling window")
	}
	if v.Changes() != 0 {
		t.Fatalf("Suppress declared %d changes. An operator telling the system the camera moved is not "+
			"the system detecting that the camera moved, and conflating them is a number on the report "+
			"that nobody can audit", v.Changes())
	}
	// And it closes on its own, without any further input from the detector.
	for i := 0; i < ViewChangeDefaults().SettleFrames; i++ {
		v.Observe(quietScore())
	}
	if v.Settling() {
		t.Fatal("the settling window never closed. A gate that cannot be closed is a gate that " +
			"suppresses forever, which is worse than the flood it prevents")
	}
}

// TestResetClearsEverythingForARestart: a restart must not inherit a suppression.
func TestResetClearsEverythingForARestart(t *testing.T) {
	v := newTestViewChange(t)
	v.Observe(movedScore(-60, 40))
	v.Observe(movedScore(-60, 40))
	v.Reset()
	if v.Settling() || v.Changes() != 0 || v.SuppressedFrames() != 0 {
		t.Fatalf("after Reset the detector reports settling=%v changes=%d suppressed=%d, want all clear. "+
			"A restart inheriting the previous run's gate is a pipeline that comes back muted",
			v.Settling(), v.Changes(), v.SuppressedFrames())
	}
}

// TestAViewChangeConfigThatCannotDecideIsRefused: the thresholds.
func TestAViewChangeConfigThatCannotDecideIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  ViewChangeConfig
		want string
	}{
		{"no illumination threshold", ViewChangeConfig{MinIllumination: 0, SustainFrames: 1, SettleFrames: 1},
			"cannot fire"},
		{"negative spread", ViewChangeConfig{MinIllumination: 5, MinSpread: -1, SustainFrames: 1, SettleFrames: 1},
			"negative"},
		{"one frame of sustain", ViewChangeConfig{MinIllumination: 5, SustainFrames: 0, SettleFrames: 1},
			"single-frame-noise"},
		{"no settle time", ViewChangeConfig{MinIllumination: 5, SustainFrames: 1, SettleFrames: 0},
			"does nothing"},
	} {
		if _, err := NewViewChange(c.cfg); err == nil {
			t.Errorf("%s was accepted: %s", c.name, c.want)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s was refused but not by the reason that matters: %v", c.name, err)
		}
	}
	// The defaults are usable.
	if _, err := NewViewChange(ViewChangeDefaults()); err != nil {
		t.Fatalf("the defaults are refused: %v", err)
	}
}

// TestANilDetectorDecidesNothingRatherThanPanicking: the nil contract, again.
//
// A caller that does not watch view integrity passes nil, and "not watched" is a real
// answer that has to be reportable.
func TestANilDetectorDecidesNothingRatherThanPanicking(t *testing.T) {
	var v *ViewChange
	if got := v.Observe(movedScore(-60, 40)); got != ViewStable {
		t.Fatalf("a nil detector reported %v", got)
	}
	v.Suppress()
	v.Reset()
	if v.Changes() != 0 || v.SuppressedFrames() != 0 || v.Settling() {
		t.Fatal("a nil detector reported state")
	}
	if !strings.Contains(v.Describe(), "not watched") {
		t.Fatalf("a nil detector describes itself as %q", v.Describe())
	}
}

// TestTheDescriptionNamesTheSuppression: the report line.
//
// A gate that reports "stable" while it is suppressing is a gate that lies on the one
// screen an operator is looking at during the seconds they most want to know.
func TestTheDescriptionNamesTheSuppression(t *testing.T) {
	v := newTestViewChange(t)
	if !strings.Contains(v.Describe(), "stable") {
		t.Fatalf("a fresh detector describes itself as %q", v.Describe())
	}
	v.Observe(movedScore(-60, 40))
	v.Observe(movedScore(-60, 40))
	d := v.Describe()
	if !strings.Contains(d, "1 change") {
		t.Errorf("the description does not name the change count: %q", d)
	}
	if !strings.Contains(d, "SETTLING") {
		t.Errorf("the description does not say presence is suppressed: %q", d)
	}
	if !strings.Contains(d, "suppressed") {
		t.Errorf("the description does not mention suppression at all: %q", d)
	}
}
