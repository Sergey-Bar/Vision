package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The search scan, at the layer that owns the SQL.

// TestThePartitionIsExhaustive guards the `Answered` column of `searchSummary`.
//
// It used to count rows that merely HAD an observation, which counts a `degraded`
// ask, sent and failed, as an answer. So a window of three proposals with one
// answer, one lost ask and one never sent reported "asked and answered 2", and
// `cannot_tell`, which is the residue of `answered`, reported the lost ask as a
// positive claim that the frame did not settle it. Both numbers were read by a judge
// and are now on the operator's screen.
//
// The partition is exhaustive and that is checkable: Proposed = Answered + Degraded +
// NotAsked, every time, and no row is in two buckets or none. The mutation is
// `v.aistate=?` back to `v.detection_id IS NOT NULL` in the Answered column.
func TestThePartitionIsExhaustive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{
		ID: "run-part", Label: "part", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	// Four proposals, four fates: answered with no objects, answered with a verdict,
	// asked and degraded, and never asked.
	ask := func(id, state, verdict string) {
		t.Helper()
		if err := s.Accept(ctx, Detection{
			ID: id, RunID: "run-part", At: time.Date(2026, 9, 30, 10, 0, 1, 0, time.UTC),
			Class: "bird", Family: "bird", BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
			Provenance: ProvenanceSynthetic,
		}); err != nil {
			t.Fatal(err)
		}
		if state == "" {
			return
		}
		if err := s.PutObservation(ctx, Observation{
			RunID: "run-part", DetectionID: id, PromptID: "alarm", AIState: state, Verdict: verdict,
			Summary: "the fence is empty", Confidence: 0.8, OutputHash: strings.Repeat("d", 64),
			ContractVersion: "hafnia/alarm/1", ModelIdentity: "hafnia (cosmos-3-nano-16b)",
		}); err != nil {
			t.Fatal(err)
		}
	}
	ask("d-empty", "asked_answered", "none")
	ask("d-said", "asked_answered", "needs_review")
	ask("d-lost", "asked_failed", "none")
	ask("d-never", "", "")

	_, sum, err := s.SearchObservations(ctx, SearchQuery{RunID: "run-part"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Proposed != 4 || sum.Answered != 2 || sum.NoObjects != 1 || sum.Degraded != 1 || sum.NotAsked != 1 {
		t.Fatalf("sum = %+v; want 4 proposals = 2 answered + 1 degraded + 1 not asked, of which 1 answered had no objects", sum)
	}
	if sum.Answered+sum.Degraded+sum.NotAsked != sum.Proposed {
		t.Fatalf("sum = %+v; the partition is not exhaustive, so a proposal is either counted twice or not at all", sum)
	}
	if sum.CannotTell != 1 {
		t.Fatalf("CannotTell = %d, want 1; the residue of `answered` counted the degraded ask as a claim about the frame", sum.CannotTell)
	}
	if sum.Unsearchable() != true {
		t.Fatalf("sum = %+v; a window with a lost ask and an unasked proposal is not searchable", sum)
	}
	// And the same window with the degraded and unasked rows answered becomes
	// searchable, which is the whole of `Unsearchable`.
	if err := s.PutObservation(ctx, Observation{
		RunID: "run-part", DetectionID: "d-lost", PromptID: "alarm", AIState: "asked_answered",
		Verdict: "confirmed", Summary: "the fence is empty", OutputHash: strings.Repeat("d", 64),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObservation(ctx, Observation{
		RunID: "run-part", DetectionID: "d-never", PromptID: "alarm", AIState: "asked_answered",
		Verdict: "confirmed", Summary: "the fence is empty", OutputHash: strings.Repeat("d", 64),
	}); err != nil {
		t.Fatal(err)
	}
	_, sum2, err := s.SearchObservations(ctx, SearchQuery{RunID: "run-part"})
	if err != nil {
		t.Fatal(err)
	}
	if sum2.Unsearchable() {
		t.Fatalf("sum2 = %+v; every proposal has an answer now", sum2)
	}
	if sum2.Answered+sum2.Degraded+sum2.NotAsked != sum2.Proposed {
		t.Fatalf("sum2 = %+v; the partition stopped being exhaustive", sum2)
	}
}

// TestADetectionWithNoStoredClipDoesNotTakeTheWholeSearchDown guards the four
// nullable evidence columns in `SearchObservations`.
//
// The join is a LEFT JOIN, so a detection with no evidence row is a shape the
// schema permits and the query asks for: a clip that was refused, or an answer
// obtained without a clip. Two of the four columns were scanned as nullable and two
// were not, and the difference was not a distinction: it was one unconvertible NULL
// `time_begin` taking the whole search down, INCLUDING the window summary beside it.
// A run with one answer and no stored clip could not be searched at all, which means
// it could not be reported, which is the failure this module exists to prevent.
//
// The mutation is `var tbeg, tend sql.NullString` back to `var tb, te string`.
func TestADetectionWithNoStoredClipDoesNotTakeTheWholeSearchDown(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{
		ID: "run-noclip", Label: "noclip", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, Detection{
		ID: "d-noclip", RunID: "run-noclip", At: time.Date(2026, 9, 30, 10, 0, 5, 0, time.UTC),
		Class: "bird", Family: "bird", BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
		Provenance: ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	// An answer, and deliberately NO evidence row.
	if err := s.PutObservation(ctx, Observation{
		RunID: "run-noclip", DetectionID: "d-noclip", PromptID: "alarm", AIState: "asked_answered",
		Verdict: "false_positive", Summary: "a magpie on the fence and nothing else",
		Confidence: 0.8, OutputHash: strings.Repeat("c", 64), ContractVersion: "hafnia/alarm/1",
		ModelIdentity: "hafnia (cosmos-3-nano-16b)",
	}); err != nil {
		t.Fatal(err)
	}

	hits, sum, err := s.SearchObservations(ctx, SearchQuery{RunID: "run-noclip", Text: "magpie"})
	if err != nil {
		t.Fatalf("an answer with no stored clip took the whole search down: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1; the answer exists and the summary is about it", len(hits))
	}
	if sum.Answered != 1 || sum.Proposed != 1 {
		t.Fatalf("sum = %+v; the window summary must survive a detection with no evidence row", sum)
	}
	// And it is visibly an answer with nothing to pull, rather than a zero-time
	// window that reads as a measurement at the year 1.
	h := hits[0]
	if h.StorageRef != "" || h.SHA256 != "" {
		t.Fatalf("a detection with no evidence row was given a reference: %+v", h)
	}
	if !h.TimeBegin.IsZero() || !h.TimeEnd.IsZero() {
		t.Fatalf("bounds = %v .. %v, want zero; there are no recorded bounds to print", h.TimeBegin, h.TimeEnd)
	}
	// The sentence is still there, so the hit is a claim and not a bare row.
	if h.Summary == "" || h.ModelIdentity == "" || h.OutputHash == "" {
		t.Fatalf("the hit lost its attribution: %+v", h)
	}
}
