package detect

import (
	"strings"
	"testing"
	"time"
)

// The order test. T9.5 asserts that the tracker sees APPROVED boxes only, and the
// only way to assert that is to watch what it saw.
//
// The bug it guards against is specific and was real: the tracker used to be fed
// every box over the score threshold, so lowering SP_POC_SCORE_THRESHOLD to catch
// smaller vehicles also created tracks for lamp posts. Nothing on the screen or
// in the report said so, because the screen drew the same unfiltered list the
// tracker saw and the report counted the same thing. The two agreed with each
// other and both were wrong.

// kerbZone is a zone with one vertical line down the middle and no polygons, so
// a box either side of x=0.5 is on a different side of it. It is declared here
// rather than reusing the other test file's `testZone`, which loads a file from
// disk and exists to test the LOADER rather than the order of the five steps.
func kerbZone() Zone {
	return Zone{
		Source: "order_test",
		Lines: []Line{{
			Name: "kerb", Direction: "west-to-east",
			A: [2]float64{0.5, 0.0}, B: [2]float64{0.5, 1.0},
		}},
	}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(kerbZone(), Config{
		ScoreThreshold: 0.45,
		Classes:        []string{"car", "person", "bird"},
		IoU:            0.3,
		MaxAge:         2 * time.Second,
		Cooldown:       10 * time.Second,
		Tolerance:      0.01,
		GhostScore:     0.10,
		MinArea:        0.0005,
		TrackMinFrames: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestTheTrackerNeverSeesAGhost is T9.5 stated as a behaviour. A chair scoring
// 0.99 must not become a track, no matter how many frames it appears, and no
// matter that it is comfortably above every threshold: the class filter runs
// BEFORE the tracker, not after it.
func TestTheTrackerNeverSeesAGhost(t *testing.T) {
	e := testEngine(t)
	now := time.Now()
	var last []Track
	for i := 0; i < 10; i++ {
		_, last, _ = e.Feed(now.Add(time.Duration(i)*100*time.Millisecond),
			[]Box{box("chair", 0.99, 0.20, 0.20, 0.50, 0.50)})
	}
	for _, tr := range last {
		if strings.EqualFold(tr.Class, "chair") {
			t.Fatalf("a chair became track %d: the tracker saw a ghost. The class filter runs before the tracker, not after it", tr.ID)
		}
	}
	if c := e.Counters(); c.BoxesApproved != 0 {
		t.Errorf("boxes approved = %d for ten frames of a chair; the class filter is not before the tracker", c.BoxesApproved)
	}
	// And the chair IS on the screen, as a ghost with its reason, which is the
	// other half of the fix: refusing it is not the same as hiding it. An
	// operator watching an empty road needs to see that a chair scored 0.99 and
	// was not approved, or they will go and lower the score threshold.
	if got := reasons(e.LastSplit().Ghosts)[GhostClass]; got != 1 {
		t.Errorf("the chair is not on the screen as a ghost: %v", e.LastSplit().Ghosts)
	}
}

// TestALowScoreBoxNeverBecomesATrack: the score filter is before the tracker for
// the same reason, and this is the case an operator creates by accident when they
// lower the threshold looking for a smaller vehicle and a dimmer one at the same
// time.
func TestALowScoreBoxNeverBecomesATrack(t *testing.T) {
	e := testEngine(t)
	now := time.Now()
	var last []Track
	for i := 0; i < 10; i++ {
		_, last, _ = e.Feed(now.Add(time.Duration(i)*100*time.Millisecond),
			[]Box{box("car", 0.20, 0.20, 0.20, 0.50, 0.50)})
	}
	for _, tr := range last {
		if strings.EqualFold(tr.Class, "car") {
			t.Fatalf("a 0.20 car became track %d: the score filter is not before the tracker", tr.ID)
		}
	}
	if c := e.Counters(); c.BoxesApproved != 0 {
		t.Errorf("boxes approved = %d, want 0", c.BoxesApproved)
	}
}

// TestAOneFrameTrackCannotCross is the time half of the split, and it is the
// other bug the order fixes: a track created and lost inside one frame is a
// compression artefact, and a line-crossing machine that sees it produces a
// crossing that never happened.
//
// The box walks the line in a single frame, which is the shape of the artefact:
// an object cannot really move from one side of a line to the other in 200 ms
// unless it is a detection glitch.
func TestAOneFrameTrackCannotCross(t *testing.T) {
	e := testEngine(t)
	at := time.Now()
	// One frame, the box straddling the line with its centre on the far side.
	crossings, _, st := e.Feed(at, []Box{box("car", 0.9, 0.40, 0.30, 0.60, 0.50)})
	if len(crossings) != 0 {
		t.Fatalf("a one-frame track produced %d crossings: SP_POC_TRACK_MIN_FRAMES is not being honoured", len(crossings))
	}
	if st.DroppedTooNew != 1 {
		t.Errorf("dropped-too-new = %d, want 1: a young box must be DEMOTED, not silently dropped", st.DroppedTooNew)
	}
	if got := reasons(e.LastSplit().Ghosts)[GhostTooNew]; got != 1 {
		t.Errorf("the demoted box was not drawn as a ghost with its own reason: %v", e.LastSplit().Ghosts)
	}

	// Now the same walk, slowly. The box has to STAY CONTIGUOUS as it crosses:
	// association is by IoU, so a box that teleports from one side of the line to
	// the other in one frame is a new object by the tracker's own rules, and a
	// new object is young. A real vehicle crosses a line continuously and its
	// boxes overlap from frame to frame, so the fixture walks it across in steps.
	//
	// A 0.4-wide box moving 0.1 per frame: the overlap between consecutive boxes
	// is 0.30 of a 0.50 union, an IoU of 0.6, comfortably over SP_POC_TRACK_IOU.
	e2 := testEngine(t)
	_, _, _ = e2.Feed(at, []Box{box("car", 0.9, 0.15, 0.30, 0.55, 0.70)})                           // centre x 0.35, seen 1
	_, _, _ = e2.Feed(at.Add(100*time.Millisecond), []Box{box("car", 0.9, 0.25, 0.30, 0.65, 0.70)}) // 0.45, seen 2
	// The centre passes the line on the THIRD frame, and the track has been seen
	// three times, so this is the frame the crossing belongs to. Collecting it
	// from the fourth frame instead would be reading the wrong frame's answer and
	// the cooldown would (correctly) have suppressed a second one.
	walked, _, _ := e2.Feed(at.Add(200*time.Millisecond), []Box{box("car", 0.9, 0.35, 0.30, 0.75, 0.70)})
	_, _, _ = e2.Feed(at.Add(300*time.Millisecond), []Box{box("car", 0.9, 0.45, 0.30, 0.85, 0.70)})
	if len(walked) != 1 {
		t.Fatalf("a car seen three times and walked across the line produced %d crossings, want 1. A filter that refuses every young track also refuses every real one", len(walked))
	}
	if walked[0].Frames < 3 {
		t.Errorf("the crossing reports %d frames; a track below SP_POC_TRACK_MIN_FRAMES was allowed to cross", walked[0].Frames)
	}
}

// TestTheApprovedSetDoesNotChangeMidRun: a run whose approved set could change
// would emit an event for a class it had already decided against. The class set is
// resolved at construction and the split reads it every frame.
//
// What the test asserts is that the ONLY thing that changes across a hundred
// frames is the YOUNG attribute. A car is young on frame one and approved from
// frame three; a chair is refused for its class on every single frame, because
// time is not a thing that can rehabilitate a class.
func TestTheApprovedSetDoesNotChangeMidRun(t *testing.T) {
	e := testEngine(t)
	at := time.Now()
	frame := func(i int, bs ...Box) Split {
		return e.FeedSplitAt(at.Add(time.Duration(i)*100*time.Millisecond), bs)
	}
	car := box("car", 0.9, 0.15, 0.30, 0.55, 0.70)
	chair := box("chair", 0.99, 0.15, 0.30, 0.55, 0.70)

	first := frame(0, car, chair)
	if got := reasons(first.Ghosts)[GhostTooNew]; got != 1 {
		t.Errorf("frame 1 ghosts = %v, want the car demoted as too new: a track with one frame behind it is not yet an object", first.Ghosts)
	}
	if got := reasons(first.Ghosts)[GhostClass]; got != 1 {
		t.Errorf("frame 1 ghosts = %v, want the chair refused for its class", first.Ghosts)
	}
	// The ONLY thing that changes across a hundred frames is the YOUNG attribute:
	// the car is young while its track has fewer than three frames behind it, and
	// approved from the third frame on. Nothing else moves.
	//
	// The loop CONTINUES from frame 1 and does not restart at frame 0. `first`
	// above WAS the first feed, so a loop starting at 0 would be reading the
	// second one, and every assertion about how many frames are behind the track
	// would be off by one against a threshold that is exact by design.
	for i := 1; i < 100; i++ {
		s := frame(i, car, chair)
		seen := i + 1
		wantYoung := seen < 3
		if gotYoung := reasons(s.Ghosts)[GhostTooNew] > 0; gotYoung != wantYoung {
			t.Fatalf("frame %d (track seen %d times, SP_POC_TRACK_MIN_FRAMES=3): too-new = %v, want %v", i, seen, gotYoung, wantYoung)
		}
		if got := reasons(s.Ghosts)[GhostClass]; got != 1 {
			t.Fatalf("frame %d: the chair's class verdict changed (ghosts = %v). Time cannot rehabilitate a class this run does not approve, and a verdict that changed mid-run would emit an event for a class already decided against", i, reasons(s.Ghosts))
		}
		wantApproved := 1
		if wantYoung {
			wantApproved = 0
		}
		if len(s.Approved) != wantApproved {
			t.Fatalf("frame %d (seen %d): approved = %v, want %d boxes", i, seen, classesOf(s.Approved), wantApproved)
		}
		if !wantYoung && s.Approved[0].Class != "car" {
			t.Fatalf("frame %d: approved = %v, want the car", i, classesOf(s.Approved))
		}
	}
}

// TestThePictureAndTheDecisionAgree is D6 as a behaviour. Everything the screen
// draws as approved is exactly what the engine tracked, and no box is in both
// lists.
//
// The one subtlety, and it is worth stating because it is easy to get backwards:
// a TOO-NEW box has a track and is also a ghost. It is not a contradiction. The
// track exists — that is the whole point, it is what the count of frames behind
// the box comes from — and the ghost says the object is not yet allowed to
// become an event. A test that asserted "tracked implies approved" would fail on
// the one box whose entire reason for existing is to be not-yet-approved.
func TestThePictureAndTheDecisionAgree(t *testing.T) {
	e := testEngine(t)
	at := time.Now()
	// Three frames, so the car is out of its too-new period and the test is about
	// the steady state rather than about the first frame.
	boxesIn := []Box{
		box("car", 0.90, 0.20, 0.20, 0.50, 0.50),   // approved
		box("car", 0.20, 0.60, 0.20, 0.90, 0.50),   // below score
		box("chair", 0.90, 0.60, 0.60, 0.90, 0.90), // class
		box("car", 0.90, 0.10, 0.60, 0.105, 0.61),  // too small
	}
	for i := 0; i < 3; i++ {
		_, _, _ = e.Feed(at.Add(time.Duration(i)*100*time.Millisecond), boxesIn)
	}

	drawn := map[Box]bool{}
	for _, tr := range e.LiveTracks() {
		drawn[tr.Box] = true
	}
	s := e.LastSplit()
	for _, b := range s.Approved {
		if !drawn[b] {
			t.Errorf("the screen would draw %s as approved and the engine tracks nothing at those coordinates: the picture and the decision disagree", b.Class)
		}
	}
	for _, g := range s.Ghosts {
		for _, a := range s.Approved {
			if g.Box == a {
				t.Fatal("a box is in both the approved list and the ghost list")
			}
		}
		// A tracked ghost is only allowed to be tracked for the TIME reason. A box
		// the tracker is following that the split refused for its class, score or
		// area would mean the class filter is not before the tracker.
		if drawn[g.Box] && g.Reason != GhostTooNew {
			t.Errorf("the engine is tracking a box the split refused for %q: the class, score and area filters run before the tracker, not after it", g.Reason)
		}
	}
	// One track, for the one approved car.
	if got := len(e.LiveTracks()); got != 1 {
		t.Errorf("the engine has %d tracks, want 1: only the approved car", got)
	}
}

// TestAZoneRefusalIsAReasonAndNotASilentDrop: the polygon check is one of the
// five, and a box refused for being outside a polygon needs to say so. An
// operator who has drawn a polygon and sees no events needs to know whether
// nothing was detected or nothing was permitted.
func TestAZoneRefusalIsAReasonAndNotASilentDrop(t *testing.T) {
	zone := Zone{
		Source:   "order_test",
		Lines:    []Line{{Name: "kerb", Direction: "west-to-east", A: [2]float64{0.5, 0.0}, B: [2]float64{0.5, 1.0}}},
		Polygons: []Polygon{{Name: "north", Points: [][2]float64{{0, 0}, {1, 0}, {1, 0.5}, {0, 0.5}}}},
	}
	e, err := New(zone, Config{
		ScoreThreshold: 0.45, Classes: []string{"car"}, IoU: 0.3,
		MaxAge: time.Second, Cooldown: time.Second, Tolerance: 0.01,
		GhostScore: 0.10, MinArea: 0.0005, TrackMinFrames: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Inside the polygon (y 0.05..0.25) and outside it (y 0.7..0.9).
	_, _, _ = e.Feed(time.Now(), []Box{
		box("car", 0.9, 0.20, 0.05, 0.40, 0.25),
		box("car", 0.9, 0.20, 0.70, 0.40, 0.90),
	})
	if got := reasons(e.LastSplit().Ghosts)[GhostZone]; got != 1 {
		t.Errorf("ghosts by zone = %d, want 1. An operator who has drawn a polygon needs to see that a box was refused for being outside it, not that nothing was there", got)
	}
}
