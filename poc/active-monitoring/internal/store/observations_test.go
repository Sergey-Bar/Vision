package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// The durable half of RC-1: the store refuses an attribution row that carries a
// verdict on an ask nobody answered.
//
// It exists beside the parser's rule rather than instead of it. The parser clears
// the verdict at parse time, which protects everything that goes through
// [vlm.ParseVerdict]. This protects everything that does not — a second writer, a
// migration, a future import of a stored observation — and the table this guard sits
// on is the one table a proof chain reads. A rule enforced only at the front door
// is one refactor away from being enforced nowhere.

func observationRig(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	runID := "run-obs"
	if err := s.StartRun(ctx, Run{ID: runID, Mode: "live", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, Detection{
		ID: "d1", RunID: runID, At: time.Now(), Class: "motion", Family: "vehicle", Provenance: ProvenanceReal,
	}); err != nil {
		t.Fatal(err)
	}
	return s, runID
}

func TestTheStoreRefusesAVerdictOnAnUnansweredAsk(t *testing.T) {
	ctx := context.Background()
	s, runID := observationRig(t)

	for _, state := range []string{"degraded", "not_asked", "cannot_tell", "no_objects"} {
		err := s.PutObservation(ctx, Observation{
			DetectionID: "d1", RunID: runID, AIState: state, Verdict: string(vlm.VerdictFalsePositive),
		})
		if err == nil {
			t.Fatalf("the store recorded a false_positive against the ask state %q", state)
		}
		if !strings.Contains(err.Error(), "fabricated negative") {
			t.Fatalf("the refusal does not say what the defect is called: %v", err)
		}
		// And `none` on the same state is fine: that is the honest row.
		if err := s.PutObservation(ctx, Observation{
			DetectionID: "d1", RunID: runID, AIState: state, Verdict: string(vlm.VerdictNone),
			ReasonCode: "region_unavailable",
		}); err != nil {
			t.Fatalf("the store refused the honest row for state %q: %v", state, err)
		}
	}

	// An answered ask may carry any verdict in the closed set, and nothing outside
	// it.
	for _, v := range vlm.AllVerdicts() {
		if err := s.PutObservation(ctx, Observation{
			DetectionID: "d1", RunID: runID, AIState: "asked_answered", Verdict: string(v),
		}); err != nil {
			t.Fatalf("the store refused the verdict %q on an answered ask: %v", v, err)
		}
	}
	if err := s.PutObservation(ctx, Observation{
		DetectionID: "d1", RunID: runID, AIState: "asked_answered", Verdict: "probably_a_car",
	}); err == nil {
		t.Fatal("the store recorded a verdict outside the closed set")
	}
}

// TestTheStoreRefusesARowWithNoAskState: an attribution row that does not say
// whether the model was asked is the fabricated negative with the label removed.
func TestTheStoreRefusesARowWithNoAskState(t *testing.T) {
	ctx := context.Background()
	s, runID := observationRig(t)
	err := s.PutObservation(ctx, Observation{DetectionID: "d1", RunID: runID, AIState: "   ", Verdict: "none"})
	if err == nil {
		t.Fatal("the store recorded an attribution row with no ask state")
	}
	if !strings.Contains(err.Error(), "asked") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
}

// TestTheStoreRefusesAnAllZeroRegion is RC-6's durable half.
//
// A decision whose region measured out as nothing is a missing measurement, and
// storing it as a row of zeros is how a report prints a number that no measurement
// produced.
func TestTheStoreRefusesAnAllZeroRegion(t *testing.T) {
	ctx := context.Background()
	s, runID := observationRig(t)
	err := s.PutDecision(ctx, Decision{
		DetectionID: "d1", RunID: runID, TriggerMode: "temporal", TriggerClass: "motion",
		Score: 0.4, Threshold: 0.35, RegionSource: "measured",
	})
	if err == nil {
		t.Fatal("the store recorded a decision with an all-zero region")
	}
	if !strings.Contains(err.Error(), "all-zero") {
		t.Fatalf("the refusal does not say the region is all zero: %v", err)
	}
	// An inverted region is the same defect wearing a different hat.
	if err := s.PutDecision(ctx, Decision{
		DetectionID: "d1", RunID: runID, TriggerMode: "temporal", TriggerClass: "motion",
		Score: 0.4, Threshold: 0.35, RegionSource: "measured",
		RegionTop: 900, RegionLeft: 900, RegionBottom: 100, RegionRight: 100,
	}); err == nil {
		t.Fatal("the store recorded an inverted region")
	}
	// And a whole-frame region is real: `top=0` with `bottom=1000` is not zero.
	if err := s.PutDecision(ctx, Decision{
		DetectionID: "d1", RunID: runID, TriggerMode: "reference", TriggerClass: "camera_integrity",
		Score: 0.9, Threshold: 0.35, RegionSource: "whole_frame",
		RegionTop: 0, RegionLeft: 0, RegionBottom: 1000, RegionRight: 1000,
	}); err != nil {
		t.Fatalf("the store refused a whole-frame region: %v", err)
	}
}

// TestTheObservationTallySeparatesAbsenceFromAnAnswer: "we asked nothing" and "every
// answer was needs_review" are different facts about a run, and a report that
// collapses them cannot answer "did anything go wrong".
func TestTheObservationTallySeparatesAbsenceFromAnAnswer(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	runID := "run-tally"
	if err := s.StartRun(ctx, Run{ID: runID, Mode: "live", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id      string
		state   string
		verdict string
	}{
		{"d1", "asked_answered", "confirmed"},
		{"d2", "asked_answered", "false_positive"},
		{"d3", "asked_answered", "needs_review"},
		{"d4", "degraded", "none"},
		{"d5", "not_asked", "none"},
	}
	for _, r := range rows {
		if err := s.Accept(ctx, Detection{
			ID: r.id, RunID: runID, At: time.Now(), Class: "motion", Family: "vehicle", Provenance: ProvenanceReal,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutObservation(ctx, Observation{
			DetectionID: r.id, RunID: runID, AIState: r.state, Verdict: r.verdict,
		}); err != nil {
			t.Fatal(err)
		}
	}
	tally, err := s.ObservationTally(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	want := ObservationCounts{Confirmed: 1, FalsePositive: 1, NeedsReview: 1, None: 2, Asked: 3, Absent: 2, Total: 5}
	if tally != want {
		t.Fatalf("the tally is %+v rather than %+v", tally, want)
	}
	latest, err := s.LatestObservation(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("LatestObservation returned nothing for a run with five answers")
	}
}

// TestAPinnedReferenceNeedsItsContentHash: a reference nobody can be handed is not
// evidence, and a reference row without a hash is a promise.
func TestAPinnedReferenceNeedsItsContentHash(t *testing.T) {
	ctx := context.Background()
	s, runID := observationRig(t)
	if err := s.PinReference(ctx, Reference{
		RunID: runID, CameraGUID: "cam-1", StorageRef: "aa/bb/aa.bin", Source: "first_frame",
	}); err == nil {
		t.Fatal("a pinned reference with no content hash was recorded")
	}
	sum := strings.Repeat("ab", 32)
	if err := s.PinReference(ctx, Reference{
		RunID: runID, CameraGUID: "cam-1", StorageRef: "aa/bb/aa.bin", SHA256: sum,
		PinnedAt: time.Now(), Source: "first_frame",
	}); err != nil {
		t.Fatal(err)
	}
	ref, err := s.Reference(ctx, runID, "cam-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.SHA256 != sum || ref.Source != "first_frame" {
		t.Fatalf("the reference read back as %+v", ref)
	}
	last, err := s.LastReference(ctx, "cam-1")
	if err != nil {
		t.Fatal(err)
	}
	if last.RunID != runID {
		t.Fatalf("LastReference found run %q rather than %q", last.RunID, runID)
	}
	// And the run id is sanitised before it becomes a filename, because it reaches
	// the filesystem through SealPath.
	s2, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	got := s2.SealPath("../../windows/system32")
	if strings.Contains(got, "..") {
		t.Fatalf("an operator-supplied run id reached the filesystem unescaped: %s", got)
	}
}
