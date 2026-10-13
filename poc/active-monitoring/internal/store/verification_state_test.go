package store

// The per-site, per-class verification-state slice, from the query side.
//
// The report section has its own tests; these are about what the QUERY does,
// because a section is only as honest as the rows it is handed. Three things are
// load-bearing and all three are the kind of thing a join does quietly:
//
//   * the slice's denominator is the RETAINED detections, not the run's total,
//     because a detection abandoned mid-pipeline never reached the AI plane;
//   * the site is the run's own source_arg and not something re-read from the
//     environment, so the identity the run was started with is the identity the
//     table is read by;
//   * a state that never happened is absent from the result and not a zero row,
//     so the report can tell "no detection was in this state" from "no
//     detection reached the query at all".
//
// The third is the one that is easy to get wrong by adding a `GROUP BY` without
// thinking about what a missing group looks like downstream.

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
)

func verificationTestRun(t *testing.T, s *Store, id, source string) {
	t.Helper()
	err := s.StartRun(context.Background(), Run{
		ID: id, Label: "verification", Mode: "live", SourceKind: "file", SourceArg: source,
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Minute), TargetSeconds: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func acceptDetection(t *testing.T, s *Store, d Detection) {
	t.Helper()
	if err := s.Accept(context.Background(), d); err != nil {
		t.Fatalf("accept %s: %v", d.ID, err)
	}
}

func TestVerificationStatesGroupBySiteClassAndState(t *testing.T) {
	s := testStore(t)
	verificationTestRun(t, s, "r1", "camera-east.mp4")
	verificationTestRun(t, s, "r2", "camera-west.mp4")

	// Two sites, three classes, and a blind state on one of each, plus an
	// abandonment that must not appear anywhere.
	// Accepted and then never moved anywhere: the four that follow are the
	// abandoned ones, and they must not appear in the slice's cells.
	for _, d := range []Detection{
		det("d1"),
		{ID: "d2", RunID: "r1", At: time.Now(), Class: "bird", TrackID: 2, AIState: "no_objects", Provenance: ProvenanceSynthetic},
		{ID: "d3", RunID: "r1", At: time.Now(), Class: "car", TrackID: 3, AIState: "degraded", Provenance: ProvenanceSynthetic},
		{ID: "d4", RunID: "r2", At: time.Now(), Class: "bird", TrackID: 4, AIState: "not_asked", Provenance: ProvenanceSynthetic},
	} {
		acceptDetection(t, s, d)
	}

	cells, err := s.VerificationStatesBySiteAndClass(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	// The query is scoped by run_id, so another run's rows must not arrive
	// through the site join however close their source string is.
	if len(cells) != 3 {
		t.Fatalf("got %d cells for the run, want 3: %+v", len(cells), cells)
	}
	for _, c := range cells {
		if c.Site != "camera-east.mp4" {
			t.Fatalf("the site is %q, want the run's own source_arg", c.Site)
		}
	}
	byKey := map[string]int{}
	for _, c := range cells {
		byKey[c.Class+"|"+c.State] = c.Count
	}
	if byKey["bird|asked_answered"] != 1 || byKey["bird|no_objects"] != 1 || byKey["car|degraded"] != 1 {
		t.Fatalf("the cells are %v, want one of each of the three (class, state) pairs", byKey)
	}
	// A state that never happened is absent, not zero.
	if _, ok := byKey["car|no_objects"]; ok {
		t.Fatal("a (class, state) pair that never happened produced a row; the report distinguishes an absent pair from a stated zero and needs the difference")
	}
}

func TestVerificationTotalsCountTheRunScopedStates(t *testing.T) {
	s := testStore(t)
	verificationTestRun(t, s, "r1", "camera-east.mp4")
	verificationTestRun(t, s, "r2", "camera-west.mp4")
	for _, d := range []Detection{
		det("d1"),
		{ID: "d2", RunID: "r1", At: time.Now(), Class: "bird", TrackID: 2, AIState: "no_objects", Provenance: ProvenanceSynthetic},
		{ID: "d3", RunID: "r1", At: time.Now(), Class: "car", TrackID: 3, AIState: "degraded", Provenance: ProvenanceSynthetic},
		{ID: "d4", RunID: "r2", At: time.Now(), Class: "bird", TrackID: 4, AIState: "not_asked", Provenance: ProvenanceSynthetic},
	} {
		acceptDetection(t, s, d)
	}

	got, err := s.VerificationTotals(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got["asked_answered"] != 1 || got["no_objects"] != 1 || got["degraded"] != 1 {
		t.Fatalf("the run's own totals are %v, want one of each", got)
	}
	// Another run's state must not be folded in through the totals either.
	if got["not_asked"] != 0 {
		t.Fatalf("the totals include another run's state: %v", got)
	}
}

// TestRetainedDetectionsShowsTheExclusion is the denominator being honest.
//
// `Count` is every detection of the run; `Retained` is the slice's denominator.
// If they are always equal the exclusion is decorative, and the section's own
// sentence -- which prints both -- stops being a measurement.
// TestRetainedDetectionsKeepsEveryRowThePlaneCouldHaveAnswered is the
// denominator being honest, and it is written the way it is because the first
// draft of it was wrong.
//
// That draft moved a detection to `dropped` and asserted the retained count
// fell. The store refused: `dropped` is not one of the seven statuses in
// CONTRACT.md, and `ValidStatus` will not write it. So the `NOT IN
// ('stalled','dropped')` clause this query shares with `CountByClassFamily` is
// defence-in-depth over a set of statuses nothing can produce — which is worth
// knowing out loud, because a reader who assumes it drops rows will compute a
// denominator that is never wrong and call the check a pass.
//
// What the slice's denominator actually is: every detection of the run, because
// every one of the seven statuses is a detection that reached the AI plane —
// including `push_dead`, which is a detection whose push failed and whose
// ai_state still says whether the model was ever asked.
func TestRetainedDetectionsKeepsEveryRowThePlaneCouldHaveAnswered(t *testing.T) {
	s := testStore(t)
	verificationTestRun(t, s, "r1", "camera-east.mp4")
	for _, d := range []Detection{det("d1"), det("d2"), det("d3")} {
		acceptDetection(t, s, d)
	}
	// A dead-lettered push is the one terminal failure a detection can reach,
	// and it still has a state. It must stay in the denominator: a detection
	// whose push was dead-lettered is exactly the one whose ai_state a reader
	// most wants to see.
	if err := s.Transition(context.Background(), "d3", StatusPushDead, "", "read-back failed"); err != nil {
		t.Fatalf("dead-lettering d3: %v", err)
	}

	total, retained, err := s.RetainedDetections(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if retained != 3 {
		t.Fatalf("retained = %d, want 3: every one of the seven statuses is a detection that reached the plane, and dropping the dead-lettered one would hide the run that most needed explaining", retained)
	}
	// And the totals must add up to that denominator, which is the property the
	// section prints beside its share.
	byState, err := s.VerificationTotals(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range byState {
		sum += n
	}
	if sum != retained {
		t.Fatalf("the totals sum to %d and the retained count is %d; a denominator the states do not add up to is a denominator nobody can check", sum, retained)
	}
	if byState[aistate.Asked] != 3 {
		t.Fatalf("the totals are %v, want three asked_answered", byState)
	}
}

// TestTheSliceIsScopedByRunID is the join, and it is the one place a slice can
// quietly widen.
//
// The site column comes from the runs table through the join, so a query that
// grouped without the run predicate would report every run's detections under
// whichever site string matched. The report asks about ONE run, so the answer
// must be one run's.
func TestTheSliceIsScopedByRunID(t *testing.T) {
	const runID = "r1"
	const otherRunID = "r2"
	s := testStore(t)
	verificationTestRun(t, s, runID, "camera-east.mp4")
	verificationTestRun(t, s, otherRunID, "camera-east.mp4")
	for _, d := range []Detection{det("d1"), det("d2")} {
		acceptDetection(t, s, d)
	}
	// The same site string, a different run.
	if err := s.Accept(context.Background(), Detection{ID: "d9", RunID: otherRunID,
		At: time.Now(), Class: "bird", TrackID: 9, AIState: aistate.NotAsked, Provenance: ProvenanceSynthetic}); err != nil {
		t.Fatal(err)
	}

	cells, err := s.VerificationStatesBySiteAndClass(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, c := range cells {
		sum += c.Count
	}
	if sum != 2 {
		t.Fatalf("the slice sums to %d across %d cell(s) for a run with two detections; another run's rows arrived through the site join: %+v",
			sum, len(cells), cells)
	}
}
