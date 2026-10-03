package detect

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

func th(approved ...string) Thresholds {
	set := map[string]bool{}
	for _, a := range approved {
		set[strings.ToLower(a)] = true
	}
	for _, c := range classify.Classes(classify.FamilyVehicle) {
		set[c] = true
	}
	for _, c := range classify.Classes(classify.FamilyAnimal) {
		set[c] = true
	}
	return Thresholds{
		Score: 0.45, GhostScore: 0.10, MinArea: 0.0005, TrackMinFrames: 3,
		Approved: func(c string) bool { return set[strings.ToLower(strings.TrimSpace(c))] },
	}
}

func box(class string, score, x1, y1, x2, y2 float64) Box {
	return Box{Class: class, Score: score, X1: x1, Y1: y1, X2: x2, Y2: y2}
}

func reasons(ghosts []Ghost) map[string]int {
	out := map[string]int{}
	for _, g := range ghosts {
		out[g.Reason]++
	}
	return out
}

// TestASplitIsTwoListsAndTheyDoNotOverlap is T5.1's whole claim. A box is
// EITHER approved or refused, and a box in both lists is a box the engine would
// both track and count as a rejection, which is the bug the single list had.
func TestASplitIsTwoListsAndTheyDoNotOverlap(t *testing.T) {
	s := Apply([]Box{
		box("car", 0.90, 0.1, 0.1, 0.3, 0.4),   // approved
		box("bird", 0.30, 0.1, 0.1, 0.3, 0.4),  // below score
		box("chair", 0.90, 0.1, 0.1, 0.3, 0.4), // class not approved
	}, th("car"), Zone{})

	if len(s.Approved) != 1 || s.Approved[0].Class != "car" {
		t.Fatalf("approved = %v, want exactly one car", classesOf(s.Approved))
	}
	got := reasons(s.Ghosts)
	if got[GhostScore] != 1 {
		t.Errorf("ghosts by score = %v, want 1 bird", got)
	}
	if got[GhostClass] != 1 {
		t.Errorf("ghosts by class = %v, want 1 chair", got)
	}
	for _, g := range s.Ghosts {
		for _, a := range s.Approved {
			if g.Box == a {
				t.Fatal("a box is in both the approved list and the ghost list")
			}
		}
	}
}

// TestTheRefusalReasonIsOnTheBox: four reasons, four different fixes, and a
// single "rejected: 40" is four pieces of advice a reader can act on none of.
func TestTheRefusalReasonIsOnTheBox(t *testing.T) {
	// A class that IS approved, so the class check does not mask the others.
	t0 := th("car")
	t0.MinArea = 0.05 // a big minimum, so a small box fails on area
	s := Apply([]Box{
		// The approved one is big enough to clear that minimum: 0.20 x 0.30 is
		// 0.06 of the frame, and a minimum of 0.05 is above the small boxes and
		// below this one.
		box("car", 0.90, 0.10, 0.10, 0.30, 0.40),
		box("car", 0.90, 0.40, 0.40, 0.41, 0.41),   // too small
		box("car", 0.30, 0.10, 0.60, 0.30, 0.90),   // below score
		box("chair", 0.90, 0.10, 0.10, 0.20, 0.20), // class
		box("car", 0.90, 0.60, 0.10, 0.40, 0.10),   // inverted
	}, t0, Zone{})

	got := reasons(s.Ghosts)
	for reason, want := range map[string]int{
		GhostArea:  1,
		GhostScore: 1,
		GhostClass: 1,
		// An inverted box is refused as invalid. It is the same silent
		// coordinate-scale failure the sidecar's own gate catches, and it is
		// named here too rather than being lumped in with the class filter.
		GhostInvalid: 1,
	} {
		if got[reason] != want {
			t.Errorf("ghosts by %q = %d, want %d (all reasons: %v)", reason, got[reason], want, got)
		}
	}
	if len(s.Approved) != 1 {
		t.Errorf("approved = %v, want exactly the one big car", classesOf(s.Approved))
	}
}

// TestTheGhostBandIsTheDifferenceBetweenDrawnAndSilent: below
// SP_POC_GHOST_MIN_SCORE nothing is drawn at all, and above it a refused box is
// drawn dimmed. The band exists because a picture showing only what passed cannot
// show what was considered.
func TestTheGhostBandIsTheDifferenceBetweenDrawnAndSilent(t *testing.T) {
	t0 := th("car")
	t0.GhostScore = 0.20
	s := Apply([]Box{
		box("car", 0.90, 0.10, 0.10, 0.30, 0.40), // approved
		box("car", 0.30, 0.10, 0.50, 0.30, 0.80), // refused, in the band
		box("car", 0.05, 0.50, 0.10, 0.70, 0.40), // refused, below the band
	}, t0, Zone{})
	got := reasons(s.Ghosts)
	if got[GhostScore] != 1 {
		t.Errorf("ghosts = %v, want only the one inside the band: a box below the ghost floor must not be drawn at all", got)
	}
	// And with the ghost floor ABOVE the refused box's score, nothing is drawn at
	// all — which is the pre-fix behaviour, and the reason the band is a required
	// variable rather than an optional extra.
	t1 := th("car")
	t1.GhostScore = 0.40
	s2 := Apply([]Box{box("car", 0.30, 0.10, 0.50, 0.30, 0.80)}, t1, Zone{})
	if len(s2.Ghosts) != 0 {
		t.Errorf("a ghost floor above the refused box's score still drew %d ghosts: a picture showing only what passed cannot show what was considered", len(s2.Ghosts))
	}
}

// TestAnUnapprovedClassIsNotReportedAsANearMiss: a box of a class nobody approved
// is refused for its CLASS even when it scores well above the threshold. If it
// were reported as "a good detection that missed the bar", an operator would
// respond by lowering SP_POC_SCORE_THRESHOLD — and get more chairs.
func TestAnUnapprovedClassIsNotReportedAsANearMiss(t *testing.T) {
	s := Apply([]Box{box("chair", 0.99, 0.1, 0.1, 0.3, 0.4)}, th("car"), Zone{})
	if len(s.Approved) != 0 {
		t.Fatal("a chair scored 0.99 and was approved")
	}
	got := reasons(s.Ghosts)
	if got[GhostClass] != 1 {
		t.Errorf("ghosts = %v, want the chair refused for its class", got)
	}
	if got[GhostScore] != 0 {
		t.Errorf("ghosts = %v, want NO score rejection: the chair cleared the score and saying otherwise sends the operator to the wrong threshold", got)
	}
}

// TestAClassOutsideTheTaxonomyIsRefusedNotApproved: the approved set is the
// run's decision, not the taxonomy's. A model from another zoo emitting `a
// really new class` is not on the list, and the run must not approve it because
// it happens to be a string.
func TestAClassOutsideTheTaxonomyIsRefusedNotApproved(t *testing.T) {
	s := Apply([]Box{box("a really new class", 0.99, 0.1, 0.1, 0.3, 0.4)}, th("car"), Zone{})
	if len(s.Approved) != 0 {
		t.Fatal("a class that is not on the approved list was approved because it is a string")
	}
	// And it is a `chair` as far as the family tally is concerned, which is what
	// makes an unmapped vocabulary visible as a wall of `other` rather than
	// silently.
	if f := classify.FamilyOf("a really new class"); f != classify.FamilyOther {
		t.Errorf("an unknown class is %s, want other", f)
	}
}

// TestANilApprovedPredicateApprovesNothing is the dangerous direction. Getting it
// backwards puts every class a camera can see onto a crossing line and the run
// reports a busy road.
func TestANilApprovedPredicateApprovesNothing(t *testing.T) {
	t0 := Thresholds{Score: 0.0, GhostScore: 0.0, MinArea: 0}
	s := Apply([]Box{box("car", 0.99, 0.1, 0.1, 0.3, 0.4)}, t0, Zone{})
	if len(s.Approved) != 0 {
		t.Fatal("a nil predicate approved everything: every class a camera can see would reach a crossing line")
	}
}

// TestTheTallyKeepsApprovedAndRejectedApart is T5.2. "We found 12 cars" and "we
// rejected 9 cars" are different measurements and one number for both is
// unreadable.
func TestTheTallyKeepsApprovedAndRejectedApart(t *testing.T) {
	tal := NewTally()
	for i := 0; i < 3; i++ {
		tal = tal.Add(Split{Approved: []Box{box("car", 0.9, 0.1, 0.1, 0.3, 0.4), box("truck", 0.9, 0.4, 0.1, 0.6, 0.4)}})
	}
	tal = tal.Add(Split{Ghosts: []Ghost{
		{Box: box("bird", 0.3, 0.1, 0.6, 0.2, 0.8), Reason: GhostScore},
		{Box: box("bird", 0.2, 0.3, 0.6, 0.4, 0.8), Reason: GhostArea},
	}})

	rows := map[classify.Family]TallyRow{}
	for _, r := range tal.Rows() {
		rows[r.Family] = r
	}
	if rows[classify.FamilyVehicle].Approved != 6 {
		t.Errorf("vehicle approved = %d, want 6", rows[classify.FamilyVehicle].Approved)
	}
	if rows[classify.FamilyAnimal].Approved != 0 {
		t.Errorf("animal approved = %d, want 0: the birds were refused, not approved", rows[classify.FamilyAnimal].Approved)
	}
	a := rows[classify.FamilyAnimal]
	if a.RejectedTotal != 2 {
		t.Errorf("animal rejected = %d, want 2", a.RejectedTotal)
	}
	if len(a.Reasons) != 2 {
		t.Errorf("the animal row does not break the rejection down: %+v", a.Reasons)
	}
	// And a family that was never seen still has a row, with a ZERO. A zero here
	// is a measurement: the run looked and found no other-class objects.
	if _, ok := rows[classify.FamilyOther]; !ok {
		t.Error("the `other` family has no row; a family that never appeared is a family with a zero, not a family that is missing")
	}
	if rows[classify.FamilyOther].Approved != 0 || rows[classify.FamilyOther].RejectedTotal != 0 {
		t.Errorf("the `other` row is not zero: %+v", rows[classify.FamilyOther])
	}
	// The vehicle row must carry no animal rejections. A tally that mixed them
	// would be unreadable in exactly the way this split exists to prevent.
	for _, r := range rows[classify.FamilyVehicle].Reasons {
		if strings.Contains(r.Reason, "score") {
			t.Errorf("the vehicle row carries a score rejection: %+v", r)
		}
	}
}

// TestTheRowsAreInAFixedOrder: two runs' reports must be comparable line by line,
// and a map's iteration order must not leak into a diff.
func TestTheRowsAreInAFixedOrder(t *testing.T) {
	tal := NewTally()
	for i := 0; i < 20; i++ {
		// Insert in a deliberately unhelpful order, with families arriving in
		// whatever order the map happens to produce.
		tal = tal.Add(Split{
			Approved: []Box{box("person", 0.9, 0.1, 0.1, 0.2, 0.3), box("car", 0.9, 0.3, 0.1, 0.4, 0.3), box("chair", 0.9, 0.5, 0.1, 0.6, 0.3)},
		})
	}
	rows := tal.Rows()
	want := []classify.Family{classify.FamilyVehicle, classify.FamilyPerson, classify.FamilyAnimal, classify.FamilyOther}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, r := range rows {
		if r.Family != want[i] {
			t.Fatalf("row %d is %s, want %s: the order must be the taxonomy's, not the map's", i, r.Family, want[i])
		}
	}
	// And it is stable across repeated calls on the same tally.
	for i := 0; i < 5; i++ {
		for j, r := range tal.Rows() {
			if r.Family != want[j] {
				t.Fatal("the row order changed between calls on the same tally")
			}
		}
	}
}

// TestTheCountersKeepTheTwoMetricSetsApart is T5.4, and the load-bearing part is
// the denominator: a rate over ASKED frames reports zero when the sidecar was
// unreachable, and that zero is the most expensive sentence in this module.
func TestTheCountersKeepTheTwoMetricSetsApart(t *testing.T) {
	c := NewCounters()
	// Ten frames asked, five answered with boxes, five never answered.
	for i := 0; i < 5; i++ {
		c.Observe(Split{AIState: aistate.Asked,
			Approved:   []Box{box("car", 0.9, 0.1, 0.1, 0.3, 0.4)},
			Candidates: 1000})
	}
	for i := 0; i < 5; i++ {
		c.Observe(Split{AIState: aistate.Degraded})
	}
	if c.FramesAsked != 10 || c.FramesAnswered != 5 {
		t.Fatalf("asked=%d answered=%d, want 10 and 5", c.FramesAsked, c.FramesAnswered)
	}
	rate, ok := c.DetectionRate()
	if !ok {
		t.Fatal("no detection rate over five answered frames")
	}
	if rate != 1.0 {
		t.Errorf("rate = %v, want 1.0: the denominator is ANSWERED frames, not asked ones", rate)
	}
	if c.FramesAbsent[aistate.Degraded] != 5 {
		t.Errorf("absent frames = %v, want 5 degraded", c.FramesAbsent)
	}
	if c.AbsentFrames() != 5 {
		t.Errorf("AbsentFrames = %d, want 5", c.AbsentFrames())
	}
	share, ok := c.CandidateShare()
	if !ok || share != 5.0/5000 {
		t.Errorf("candidate share = %v (ok=%v), want 5/5000: the classifier found 5 out of 5000 and that is the number a reader needs", share, ok)
	}
}

// TestARateOverNoAnsweredFramesIsNotAZero is the failure this counters exist to
// prevent: a run whose sidecar was down for the whole run, reporting "0.00
// detections per frame" as a confident measurement of a quiet road.
func TestARateOverNoAnsweredFramesIsNotAZero(t *testing.T) {
	c := NewCounters()
	for i := 0; i < 100; i++ {
		c.Observe(Split{AIState: aistate.Degraded})
	}
	if _, ok := c.DetectionRate(); ok {
		t.Fatal("a rate was computed over zero answered frames: a sidecar that was down all run would report 0.00 and it would look like a quiet road")
	}
	if _, ok := c.CandidateShare(); ok {
		t.Fatal("a candidate share was computed with no candidates")
	}
	// And an answer that was refused as uninterpretable is an absence, not a
	// frame with no objects. This is the one the four-bug collapse would have got
	// wrong: it was a HEALTHY run with nonsense in it.
	c.Observe(Split{AIState: aistate.CannotTell, Candidates: 8112})
	if c.FramesAnswered != 0 {
		t.Error("a cannot_tell frame was counted as an answered frame")
	}
	if c.FramesAbsent[aistate.CannotTell] != 1 {
		t.Errorf("absent states = %v, want one cannot_tell", c.FramesAbsent)
	}
	if c.BoxCandidates != 8112 {
		t.Errorf("candidates = %d; the telemetry is kept even on a refused frame so it stays diagnosable", c.BoxCandidates)
	}
}

// TestAnEmptyHealthyFrameIsNotAnAbsence: the distinction the whole five-word
// vocabulary exists for. "The model looked and saw nothing" and "the sidecar was
// not asked" are different claims and they must not share a counter.
func TestAnEmptyHealthyFrameIsNotAnAbsence(t *testing.T) {
	c := NewCounters()
	c.Observe(Split{AIState: aistate.NoObjects, Candidates: 1200})
	if c.FramesAnswered != 1 {
		t.Error("a no_objects frame was not counted as answered: the model looked and saw nothing, which is a measurement")
	}
	if c.AbsentFrames() != 0 {
		t.Error("a no_objects frame was counted as absent")
	}
	if c.FramesNoObjects != 1 {
		t.Error("a no_objects frame was not counted in FramesNoObjects")
	}
	// The candidate count is what makes the zero readable: 1200 candidates and a
	// pre-NMS of 0 means the model did not fire, which is different from a
	// pre-NMS of 41 and a post-NMS of 0, which means the decoder threw them away.
	if c.BoxCandidates != 1200 {
		t.Errorf("candidates = %d, want 1200", c.BoxCandidates)
	}
}

// TestMarkTooNewDemotesAOneFrameTrack: a shadow, a reflection and a compression
// artefact all look like an object for exactly one frame.
func TestMarkTooNewDemotesAOneFrameTrack(t *testing.T) {
	seen := map[string]int{"fresh": 1, "old": 9}
	constMin := func(Box) int { return 3 }
	boxesIn := []Box{box("fresh", 0.9, 0.1, 0.1, 0.3, 0.4), box("old", 0.9, 0.5, 0.1, 0.7, 0.4)}
	kept, ghosts := MarkTooNew(boxesIn, func(b Box) int { return seen[b.Class] }, constMin)
	if len(kept) != 1 || kept[0].Class != "old" {
		t.Fatalf("kept = %v, want only the track seen nine times", classesOf(kept))
	}
	if len(ghosts) != 1 || ghosts[0].Reason != GhostTooNew {
		t.Fatalf("ghosts = %+v, want one demoted as too new", ghosts)
	}
	// A minimum of one is a no-op, and it is spelled out rather than falling out
	// of a comparison.
	all, none := MarkTooNew(boxesIn, func(b Box) int { return seen[b.Class] }, func(Box) int { return 1 })
	if len(all) != 2 || len(none) != 0 {
		t.Errorf("a minimum of one demoted something: %v", classesOf(all))
	}
	// A nil predicate is a no-op too: a caller with no tracker has not said the
	// boxes are young, it has said it does not know.
	all, none = MarkTooNew(boxesIn, nil, constMin)
	if len(all) != 2 || len(none) != 0 {
		t.Errorf("a nil predicate demoted something: %v", classesOf(all))
	}
	// A nil MINIMUM is a no-op for the same reason, and it is checked separately
	// because the two are different mistakes: a missing tracker and a missing bar are
	// not the same absence.
	all, none = MarkTooNew(boxesIn, func(b Box) int { return seen[b.Class] }, nil)
	if len(all) != 2 || len(none) != 0 {
		t.Errorf("a nil minimum demoted something: %v", classesOf(all))
	}
}

// TestTheMinimumIsPerBoxAndNotOneNumberForTheFrame: the source-aware minimum. On one
// frame a car is found by the whole-frame pass and a bird is found in a crop, and they
// are held to different bars in the same call.
//
// This is the test that protects the crop pass's headline case. A bird 40 px across at
// 30 m arrives as eight pixels after a whole-frame downscale and is below the detection
// floor, so the crop pass is the only thing that will ever see it, and the crop pass
// produces it on exactly two samples. A single global minimum either erases the bird or
// re-admits the shadow the rule exists to stop.
func TestTheMinimumIsPerBoxAndNotOneNumberForTheFrame(t *testing.T) {
	seen := map[string]int{"car": 2, "bird": 2}
	// Two sightings for each. The car's bar is 3 and the bird's is 2.
	bars := map[string]int{"car": 3, "bird": 2}
	in := []Box{box("car", 0.9, 0.1, 0.1, 0.3, 0.4), box("bird", 0.8, 0.6, 0.6, 0.65, 0.65)}
	kept, ghosts := MarkTooNew(in,
		func(b Box) int { return seen[b.Class] },
		func(b Box) int { return bars[b.Class] })
	if len(kept) != 1 || kept[0].Class != "bird" {
		t.Fatalf("kept = %v, want only the crop-sourced bird at two samples. The two-sample whole-frame "+
			"car was demoted and the two-sample magnified bird was not, or the other way round, and both "+
			"are the same defect", classesOf(kept))
	}
	if len(ghosts) != 1 || ghosts[0].Class != "car" {
		t.Fatalf("ghosts = %+v, want the two-sample whole-frame car demoted as too new", ghosts)
	}
}

// TestTheClassHistogramIsByClassAndNotByFamily: an Alarm Definition is written
// against a class. A family tally cannot answer "how many cars".
func TestTheClassHistogramIsByClassAndNotByFamily(t *testing.T) {
	h := ClassHistogram([]Box{
		box("car", 0.9, 0.1, 0.1, 0.2, 0.2),
		box("car", 0.9, 0.3, 0.1, 0.4, 0.2),
		box("truck", 0.9, 0.5, 0.1, 0.6, 0.2),
	})
	if h["car"] != 2 || h["truck"] != 1 {
		t.Errorf("histogram = %v, want car=2 truck=1", h)
	}
	if _, ok := h["vehicle"]; ok {
		t.Error("the histogram has a `vehicle` key: a rule names a class, and `vehicle` is not one")
	}
	// And it renders stably, biggest first, so two runs' log lines diff.
	if got := FormatClassList(h); got != "car=2 truck=1" {
		t.Errorf("FormatClassList = %q", got)
	}
	if got := FormatClassList(nil); got != "(none)" {
		t.Errorf("FormatClassList(nil) = %q; an empty histogram is 'none', not a zero that looks like a count", got)
	}
}

func classesOf(bs []Box) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Class)
	}
	return out
}
