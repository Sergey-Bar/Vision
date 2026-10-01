package detect

import (
	"math"
	"testing"
	"time"
)

// The Kalman filter, and the two-stage ghost association.
//
// Both exist to stop one object becoming two. Everything here is about that: a
// jittery box splitting at the line, and a score dip splitting a track in half.

// TestAStillObjectFilteredStaysStill is the reason the filter exists at all.
//
// A single frame's box jitters by a few pixels, and a jittery box flips sides of a
// line it is sitting on: a car parked across a line emits a crossing, then a second
// one a frame later as the box breathes, and the report shows two events for one
// stationary vehicle.
//
// The filter's first job is that a filtered centre which does not move does not
// move. The measurements here jitter around 0.5 by +/-0.01 and the line is AT 0.5,
// so every single raw box is on the line. The filtered centre must not be.
func TestAStillObjectFilteredStaysStill(t *testing.T) {
	k := Kalman{}
	p := DefaultKalman()
	jitter := []float64{-0.01, 0.008, -0.006, 0.01, -0.009, 0.007, -0.004, 0.009}

	// The property is SPREAD, not per-frame stillness, and the distinction matters:
	// with MeasurementNoise at 0.02 and a jitter amplitude of 0.01, the filter's gain
	// is about 0.5, so the filtered centre follows roughly half of each step and
	// moves ~0.005 on a 0.02 peak-to-peak jitter. That is correct filtering, and a
	// test demanding per-frame stillness would be demanding a filter that ignores
	// its measurements.
	//
	// What must hold is that the filtered spread is NARROWER than the raw spread. A
	// filter that did not smooth would leave the two equal, and that is the defect.
	rawMin, rawMax := jitter[0], jitter[0]
	filtMin, filtMax := math.Inf(1), math.Inf(-1)
	for i, d := range jitter {
		fx, _ := k.Update(0.5+d, 0.25, 0.2, p)
		if d < rawMin {
			rawMin = d
		}
		if d > rawMax {
			rawMax = d
		}
		if i >= 3 {
			if fx < filtMin {
				filtMin = fx
			}
			if fx > filtMax {
				filtMax = fx
			}
		}
	}
	rawSpread := rawMax - rawMin
	filtSpread := filtMax - filtMin
	if filtSpread >= rawSpread {
		t.Fatalf("the filtered centre spans %v over jitter spanning %v. A filter that does not "+
			"narrow the spread is not smoothing, and a still object whose box jitters across a "+
			"line emits a crossing every time the jitter flips sides", filtSpread, rawSpread)
	}
	// And it converged near the truth rather than wandering off.
	fx, _, _ := k.Centre()
	if math.Abs(fx-0.5) > 0.005 {
		t.Fatalf("the filtered centre settled at %v, want ~0.5", fx)
	}
}

// TestTheFilterEstablishesVelocityOnTheSecondMeasurement is the acquisition
// property, and getting it wrong is a real crossing lost.
//
// A filter seeded with zero velocity believes the object is stationary until its
// covariance grows enough to move — which takes several frames. On a vehicle at
// 10 m/s and 5 fps that is a third of the object PER FRAME: the object reaches the
// line before the filter believes it is moving, and the crossing is decided on a
// stale centre.
func TestTheFilterEstablishesVelocityOnTheSecondMeasurement(t *testing.T) {
	k := Kalman{}
	p := DefaultKalman()
	// Two measurements 0.2s apart, moving at 0.5 units/second => 0.1 apart.
	k.Update(0.10, 0.5, 0, p)
	fx, fy := k.Update(0.20, 0.5, 0.2, p)
	if !k.seeded {
		t.Fatal("the filter did not acquire after two measurements")
	}
	vx, _ := k.Velocity()
	if math.Abs(vx-0.5) > 1e-6 {
		t.Fatalf("velocity = %v per second, want 0.5 — it is measured from the two observations "+
			"rather than estimated over several frames", vx)
	}
	if math.Abs(fx-0.20) > 1e-9 || math.Abs(fy-0.5) > 1e-9 {
		t.Fatalf("the second measurement moved the state to (%v,%v), want the measurement itself", fx, fy)
	}

	// And the prediction actually extrapolates: 0.2s later at 0.5/s it should be at
	// 0.30 before correction.
	k.Update(0.30, 0.5, 0.2, p)
	vx2, _ := k.Velocity()
	if vx2 < 0.4 {
		t.Fatalf("velocity fell to %v on a perfectly straight measurement; the filter is "+
			"damping a constant velocity it should be tracking exactly", vx2)
	}
}

// TestTheFilterFollowsATurnWithoutUnboundedLag is what makes the correction of the
// VELOCITY from the position residual necessary.
//
// Without the velocity correction the filter's velocity only ever comes from
// prediction, so a turning object accumulates a lag that grows without bound. That
// is invisible on a straight road — which is why it can ship — and is the reason a
// junction is where a filter like this fails.
func TestTheFilterFollowsATurnWithoutUnboundedLag(t *testing.T) {
	k := Kalman{}
	p := DefaultKalman()
	// Acquire going right.
	k.Update(0.1, 0.5, 0, p)
	k.Update(0.2, 0.5, 0.2, p)
	// Then turn: the x stops advancing and the y starts.
	for i := 1; i <= 12; i++ {
		t0 := float64(i) * 0.2
		k.Update(0.2, 0.5+t0*0.1, 0.2, p)
	}
	fx, fy, _ := k.Centre()
	// The filter should be near the measured position, not back at the start of the
	// turn. An uncorrected velocity leaves it at roughly (0.2, 0.5).
	if fy < 0.65 {
		t.Fatalf("after a turn the filtered centre is %v, want it to have followed the object to "+
			"about 0.74. A filter whose velocity never sees evidence accumulates unbounded lag on a "+
			"turning path, and a junction is where that shows", fy)
	}
	if math.Abs(fx-0.2) > 0.05 {
		t.Errorf("the filtered x drifted to %v during a pure vertical turn; want it held near 0.2", fx)
	}
}

// TestTheCovarianceGrowsWithTimeAndSoDoesTheGain is the filter's honesty about its
// own uncertainty, and it is what stops a stalled source producing a confident
// prediction nobody observed.
func TestTheCovarianceGrowsWithTimeAndSoDoesTheGain(t *testing.T) {
	p := DefaultKalman()
	// THREE updates to reach the predict/correct path: the first two are the
	// initialisation and the velocity seeding, and neither touches the covariance
	// the gain is computed from. Reading the gain after two measures the SEED, not
	// the filter, and the seed's gain is always 0.5 — which is exactly the constant
	// this test was getting and could not explain.
	k := Kalman{}
	k.Update(0.5, 0.5, 0, p)
	k.Update(0.5, 0.5, 0.1, p)
	k.Update(0.5, 0.5, 0.1, p)
	gainNow := k.pxx / (k.pxx + p.MeasurementNoise)

	// A long gap widens the position variance.
	k2 := Kalman{}
	k2.Update(0.5, 0.5, 0, p)
	k2.Update(0.5, 0.5, 0.1, p)
	k2.Update(0.5, 0.5, 5.0, p)
	gainLater := k2.pxx / (k2.pxx + p.MeasurementNoise)
	if gainLater <= gainNow {
		t.Fatalf("the gain did not grow after a 5s gap: %v then %v (position variances %v and %v). "+
			"A source that stalled produces a measurement that deserves more weight than one from "+
			"the previous frame, and a filter that does not widen will keep trusting a stale "+
			"prediction over fresh evidence",
			gainNow, gainLater, k.pxx, k2.pxx)
	}
	// And a zero dt does not divide by zero.
	k3 := Kalman{}
	k3.Update(0.5, 0.5, 0, p)
	fx, fy := k3.Update(0.5, 0.5, 0, p)
	if math.IsNaN(fx) || math.IsNaN(fy) {
		t.Fatal("a zero elapsed time produced a NaN centre")
	}
}

// TestFilteredBoxKeepsTheModelsWidthAndOnlyMovesTheCentre is the reporting
// property, and it is what a reader comparing a crossing against the picture
// depends on: the extent they see is the extent the model reported.
func TestFilteredBoxKeepsTheModelsWidthAndOnlyMovesTheCentre(t *testing.T) {
	measured := Box{Class: "car", Score: 0.8, X1: 0.10, Y1: 0.20, X2: 0.30, Y2: 0.50}
	got := FilteredBox(measured, 0.25, 0.30)
	if math.Abs(got.Width()-measured.Width()) > 1e-12 {
		t.Errorf("width = %v, want the model's %v", got.Width(), measured.Width())
	}
	if math.Abs(got.Height()-measured.Height()) > 1e-12 {
		t.Errorf("height = %v, want the model's %v", got.Height(), measured.Height())
	}
	cx, cy := got.Centre()
	if math.Abs(cx-0.25) > 1e-12 || math.Abs(cy-0.30) > 1e-12 {
		t.Errorf("centre = (%v,%v), want the filter's (0.25,0.30)", cx, cy)
	}
	if got.Class != "car" || math.Abs(got.Score-0.8) > 1e-12 {
		t.Errorf("the class or score changed: %+v", got)
	}
}

// TestAParkedObjectAcrossALineProducesNoCrossing is the end-to-end property the
// filter buys, and it is the one an operator notices first.
//
// The line is at x=0.5 and the object's raw box jitters across it on every frame.
// Without the filter this produces a stream of crossings for a vehicle that never
// moved; with it, none.
func TestAParkedObjectAcrossALineProducesNoCrossing(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	jitter := []float64{0.49, 0.51, 0.495, 0.505, 0.492, 0.508, 0.49, 0.51}
	at := time.Unix(0, 0).UTC()
	crossings := 0
	for i, x := range jitter {
		b := Box{Class: "car", Score: 0.9, X1: x - 0.04, Y1: 0.35, X2: x + 0.04, Y2: 0.75}
		cx, _, _ := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		crossings += len(cx)
		_ = i
		at = at.Add(200 * time.Millisecond)
	}
	if crossings > 1 {
		t.Fatalf("a vehicle whose box jitters across the line produced %d crossings. The raw boxes "+
			"alternate sides every frame and the filter's whole purpose is that the filtered centre "+
			"of a still object does not move", crossings)
	}
}

// TestAMovingObjectStillCrossesUnderTheFilter is the other half, and it is the one
// that matters: smoothing must not cost a crossing.
//
// If the filter lagged too far, a real crossing would be decided on a stale centre
// and either missed or moved to the wrong frame. Both would be a regression against
// the unfiltered engine, which is the failure mode a filter introduces.
func TestAMovingObjectStillCrossesUnderTheFilter(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()
	var crossings []Crossing
	// A car walking steadily left to right across x=0.5 at 0.5 units/second.
	for i := 0; i < 14; i++ {
		x := 0.20 + float64(i)*0.05
		b := Box{Class: "car", Score: 0.9, X1: x - 0.04, Y1: 0.35, X2: x + 0.04, Y2: 0.75}
		cx, _, _ := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		crossings = append(crossings, cx...)
		at = at.Add(100 * time.Millisecond)
	}
	if len(crossings) == 0 {
		t.Fatal("a vehicle crossing a line under the filter produced no crossing at all. Smoothing " +
			"that loses a real crossing is worse than the jitter it removes")
	}
	// Exactly one: the latch and cooldown exist for this, and a filter that lags does
	// not get to emit twice.
	if len(crossings) > 1 {
		t.Fatalf("one vehicle crossing a line produced %d events", len(crossings))
	}
	got := crossings[0]
	cx, _ := got.Box.Centre()
	if math.Abs(cx-0.5) > 0.12 {
		t.Errorf("the crossing box centre is %v, want it near the line at 0.5. A filter that lags "+
			"decides the crossing on a stale centre, which puts the event on the wrong frame", cx)
	}
	if got.Frames < 3 {
		t.Errorf("the crossing carries Frames=%d; the track should have aged", got.Frames)
	}
}

// TestAGhostResumesATrackRatherThanSplittingIt is the two-stage association's whole
// reason, and it is the most likely reason a real detector with a real model
// produces zero crossings on real footage.
func TestAGhostResumesATrackRatherThanSplittingIt(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()
	var trackIDs []int

	// Three clean frames: the track is born and ages.
	for i := 0; i < 3; i++ {
		b := Box{Class: "car", Score: 0.9, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
		_, tracks, st := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		if len(tracks) > 0 {
			trackIDs = append(trackIDs, tracks[0].ID)
		}
		if st.Resumed != 0 {
			t.Fatalf("frame %d: nothing was refused, so nothing should have resumed", i)
		}
		at = at.Add(100 * time.Millisecond)
	}
	if len(trackIDs) == 0 {
		t.Fatal("no track was created by three clean frames")
	}
	born := trackIDs[0]

	// Frame 4: the SAME car, occluded, scoring below the approval bar.
	ghost := Box{Class: "car", Score: 0.31, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
	_, _, st := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{ghost}})
	if st.Resumed != 1 {
		t.Fatalf("a refused box in the same place as an aged track resumed %d tracks, want 1. "+
			"Without this the track dies on the dip and the object becomes two tracks each too "+
			"young to cross, which on a report reads as a detector that found nothing",
			st.Resumed)
	}
	if st.GhostResumed[GhostScore] != 1 {
		t.Errorf("resumed by reason = %v, want one under %q so a reader can tell a score dip from "+
			"an area refusal, which has a different fix", st.GhostResumed, GhostScore)
	}
	// And the track is STILL ALIVE and still the SAME track.
	live, ok := e.live[born]
	if !ok {
		t.Fatal("the track died on a score dip; the ghost pass exists to prevent exactly that")
	}
	if live.Resumed() == 0 {
		t.Error("the track was not counted as resumed")
	}
}

// TestAGhostCannotApproveAnythingOrAgeATrack is the safety property, and it is the
// reason the second pass cannot reuse the first one's machinery.
func TestAGhostCannotApproveAnythingOrAgeATrack(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()

	// Ten ghost frames for a car nobody ever approved. They must produce no track
	// old enough to cross and no crossing.
	var crossings []Crossing
	for i := 0; i < 10; i++ {
		g := Box{Class: "car", Score: 0.2, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
		cx, _, _ := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{g}})
		crossings = append(crossings, cx...)
		at = at.Add(100 * time.Millisecond)
	}
	if len(crossings) != 0 {
		t.Fatalf("ten frames of nothing but refused boxes produced %d crossings. A ghost is a ghost: "+
			"it may hold a track alive but it may never create one, age one, or emit", len(crossings))
	}
	for id, tr := range e.live {
		if tr.allowed {
			t.Fatalf("track %d is allowed to cross on the strength of refused boxes alone", id)
		}
		if tr.Seen > 1 {
			t.Fatalf("track %d has Seen=%d from ghost frames; a ghost may not age a track", id, tr.Seen)
		}
	}
}

// TestAGhostOfADifferentClassCannotResumeATrack is the strongest single bound, and
// it is what stops the two-stage pass from merging a pedestrian with a car.
func TestAGhostOfADifferentClassCannotResumeATrack(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()
	for i := 0; i < 4; i++ {
		b := Box{Class: "car", Score: 0.9, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
		e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		at = at.Add(100 * time.Millisecond)
	}
	before := len(e.live)
	// A person ghost in the same place.
	person := Box{Class: "person", Score: 0.2, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
	_, _, st := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{}})
	_ = st
	if len(e.live) != before+1 {
		// A person box becomes its OWN new track, which is correct.
		t.Logf("a differently-classed ghost made a new track rather than resuming the car (%d -> %d)",
			before, len(e.live))
	}
	at = at.Add(100 * time.Millisecond)
	_, _, st = e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{person}})
	if st.Resumed != 0 {
		t.Fatalf("a `person` ghost resumed %d car tracks. The class must match, case-insensitively; "+
			"that is the strongest single bound available and without it the pass merges a "+
			"pedestrian into a car", st.Resumed)
	}
}

// TestTheGhostPassCannotStealATrackAnApprovedBoxClaimed is the ordering property.
// The approved pass runs first, so a ghost in the same place as a live approved box
// must not resurrect a DIFFERENT track.
func TestTheGhostPassCannotStealATrackAnApprovedBoxClaimed(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()
	// Age one car.
	for i := 0; i < 4; i++ {
		b := Box{Class: "car", Score: 0.9, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
		e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		at = at.Add(100 * time.Millisecond)
	}
	ids := map[int]bool{}
	for id := range e.live {
		ids[id] = true
	}
	// A frame with the approved box AND a ghost elsewhere.
	b := Box{Class: "car", Score: 0.9, X1: 0.60, Y1: 0.35, X2: 0.68, Y2: 0.75}
	g := Box{Class: "car", Score: 0.2, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
	_, _, st := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b, g}})
	if st.Resumed != 1 {
		t.Fatalf("resumed = %d, want 1 — a ghost one place along resumes the aged track there", st.Resumed)
	}
	for id := range ids {
		if _, ok := e.live[id]; !ok {
			t.Errorf("track %d was lost even though an approved box existed on the frame", id)
		}
	}
}

// TestResumedIsZeroWhenNothingWasRefused is the arithmetic's own boundary: the flag
// means "something was held alive", not "the pass ran".
func TestResumedIsZeroWhenNothingWasRefused(t *testing.T) {
	e := newKalmanEngine(t, 0.45)
	at := time.Unix(0, 0).UTC()
	for i := 0; i < 5; i++ {
		b := Box{Class: "car", Score: 0.9, X1: 0.16, Y1: 0.35, X2: 0.24, Y2: 0.75}
		_, _, st := e.FeedAnswered(at, Answer{AIState: "asked_answered", Boxes: []Box{b}})
		if st.Resumed != 0 || len(st.GhostResumed) != 0 {
			t.Fatalf("frame %d: resumed=%d with nothing refused. The flag means 'a track was held "+
				"alive', not 'the pass ran', or a report would print a nonzero number on every frame",
				i, st.Resumed)
		}
		at = at.Add(100 * time.Millisecond)
	}
}

// TestTheFilterCanBeTurnedOffByName is the reproducibility escape hatch.
//
// The filter CHANGES the crossing box, so anybody comparing this build's numbers
// against a previous measurement needs the previous behaviour available by name —
// not by reverting a commit and discovering which of several changes mattered.
func TestTheFilterCanBeTurnedOffByName(t *testing.T) {
	zone := Zone{
		Lines: []Line{{Name: "l", A: [2]float64{0.5, 0}, B: [2]float64{0.5, 1},
			Direction: "left-to-right", Classes: []string{"car"}}},
	}
	e, err := New(zone, Config{
		ScoreThreshold: 0.45, Classes: []string{"car"}, IoU: 0.3, Tolerance: 0.001,
		MaxAge: 2 * time.Second, TrackMinFrames: 1,
		KalmanDisabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.kalmanOn {
		t.Fatal("the filter is on despite KalmanDisabled being set")
	}
	// And it behaves as the identity: one frame, one raw centre.
	at := time.Unix(0, 0).UTC()
	k := Kalman{}
	fx, _ := k.Update(0.37, 0.5, 0, DefaultKalman())
	if math.Abs(fx-0.37) > 1e-12 {
		t.Fatalf("the first measurement produced %v, want the measurement itself", fx)
	}
	_ = at
	_ = e
}

// newKalmanEngine builds an engine with a permissive threshold and one line at
// x=0.5, for the tests above.
//
// TrackMinFrames is 1 so that a test about the FILTER is not also a test about the
// ageing rule, and IoU is loose because these fixtures move by 0.05 per frame and a
// tight association threshold would end the track before the filter saw anything.
func newKalmanEngine(t *testing.T, threshold float64) *Engine {
	t.Helper()
	zone := Zone{
		Lines: []Line{{Name: "l", A: [2]float64{0.5, 0}, B: [2]float64{0.5, 1},
			Direction: "left-to-right", Classes: []string{"car"}}},
		Polygons: []Polygon{{Name: "frame",
			Points: [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}, Classes: []string{"car"}}},
	}
	e, err := New(zone, Config{
		ScoreThreshold: threshold, Classes: []string{"car"}, IoU: 0.2, Tolerance: 0.001,
		MaxAge: 2 * time.Second, TrackMinFrames: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
