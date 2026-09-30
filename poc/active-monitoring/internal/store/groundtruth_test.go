package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A run with one pushed event, so the join has something to join to.
func runWithOnePush(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const runID, detID, tag = "run-gt", "det-gt", "aicap-car-0123456789abcdef01234567"
	if err := s.StartRun(context.Background(), Run{
		ID: runID, Label: "ground truth", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Minute), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(context.Background(), Detection{
		ID: detID, RunID: runID, At: time.Now(), Class: "car", Family: "vehicle",
		TrackID: 3, BoxTop: 400, BoxLeft: 100, BoxBottom: 520, BoxRight: 260,
		Direction: "left-to-right", AIState: "asked_answered", Provenance: ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(context.Background(), OutboxEntry{
		DetectionID: detID, Kind: KindEvent, Payload: `{"tag":"` + tag + `"}`, Tag: tag,
	}, 4); err != nil {
		t.Fatal(err)
	}
	return s, runID, tag
}

// TestNobodyLookingIsNotZero: the whole of D-8. A run nobody reviewed must read
// as NOT RUN, and the mechanism for that is `measured` being false — there is
// deliberately no way to get "0 false positives" out of this without a person
// having written two marks.
func TestNobodyLookingIsNotZero(t *testing.T) {
	s, runID, _ := runWithOnePush(t)
	tp, fp, measured, err := s.Precision(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if measured {
		t.Fatalf("a run nobody reviewed reports measured=true (tp=%d fp=%d)", tp, fp)
	}
	if tp != 0 || fp != 0 {
		t.Fatalf("counts are %d/%d for a run nobody reviewed; they must be zero AND unmeasured, and only the second is different from a rate of 0", tp, fp)
	}
}

// TestAMarkIsRefusedForATagThisRunNeverPushed is the JOIN, and it is the reason
// Precision cannot be fed a number about an event that never left the process.
//
// The failure it prevents is subtle and expensive: a review file carried over
// from a previous run would otherwise mark events in THIS run's name, and the
// precision figure would be printed with this run's id on it while describing a
// different run entirely.
func TestAMarkIsRefusedForATagThisRunNeverPushed(t *testing.T) {
	s, runID, tag := runWithOnePush(t)
	err := s.MarkGroundTruth(context.Background(), runID, "aicap-car-somethingelse0000", GroundTruthFalsePositive, "a reviewer", "")
	if err == nil {
		t.Fatal("a mark for a tag this run never pushed was accepted; a precision figure would then describe an event nobody sent")
	}
	if !strings.Contains(err.Error(), "not a tag this run pushed") {
		t.Fatalf("the refusal must say what is wrong: %v", err)
	}
	// A refusal nobody can act on is a refusal nobody will act on, so it names
	// the tags this run DID push.
	if !strings.Contains(err.Error(), tag) {
		t.Fatalf("the refusal does not name a tag this run pushed, so the reader cannot fix their review: %v", err)
	}
	// And nothing was recorded by the refused attempt.
	if _, _, measured, _ := s.Precision(context.Background(), runID); measured {
		t.Fatal("a refused mark still moved the counters")
	}
}

// TestTheSameTagOnAnotherRunIsStillRefused: the join is on (run, tag), not on
// tag alone. A review is about one run's events and must not travel.
func TestTheSameTagOnAnotherRunIsStillRefused(t *testing.T) {
	s, _, tag := runWithOnePush(t)
	const other = "run-gt-other"
	if err := s.StartRun(context.Background(), Run{
		ID: other, Label: "another", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Minute), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGroundTruth(context.Background(), other, tag, GroundTruthTruePositive, "a reviewer", ""); err == nil {
		t.Fatal("a tag pushed by run A was accepted as a mark on run B; a review must not travel between runs")
	}
}

// TestTheVerdictsAreTwoWordsAndNoMore.
func TestTheVerdictsAreTwoWordsAndNoMore(t *testing.T) {
	s, runID, tag := runWithOnePush(t)
	for _, v := range []string{"", "true", "false", "maybe", "TP", "unknown", "TRUE_POSITIVE"} {
		if err := s.MarkGroundTruth(context.Background(), runID, tag, v, "a reviewer", ""); err == nil {
			t.Fatalf("%q was accepted as a verdict; a third word would be counted by a report nobody could read it with", v)
		}
	}
	if err := s.MarkGroundTruth(context.Background(), runID, "", GroundTruthTruePositive, "a reviewer", ""); err == nil {
		t.Fatal("a mark with no tag was accepted; an empty tag is a verdict about nothing")
	}
	if err := s.MarkGroundTruth(context.Background(), runID, tag, GroundTruthTruePositive, "", ""); err != nil {
		t.Fatalf("a mark with no author is a legitimate mark to RECORD: %v", err)
	}
	// ...and it must not read back as a measurement, because `HumanMarking.Verdict`
	// keys off the author. The store's job is to keep it; the report's is to
	// refuse to print it, and this asserts the half the store owns.
	got, err := s.GroundTruth(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Verdict != GroundTruthTruePositive || got[0].By != "" {
		t.Fatalf("the mark did not read back as recorded: %+v", got)
	}
}

// TestReMarkingTheSameTagReplacesTheVerdict: a reviewer who changes their mind
// must be able to, and the second reading must be the only one left. An
// append-only review would count one event twice and make precision
// unreproducible.
func TestReMarkingTheSameTagReplacesTheVerdict(t *testing.T) {
	s, runID, tag := runWithOnePush(t)
	c := context.Background()
	if err := s.MarkGroundTruth(c, runID, tag, GroundTruthTruePositive, "a reviewer", "first look"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGroundTruth(c, runID, tag, GroundTruthFalsePositive, "a reviewer", "second look"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GroundTruth(c, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d marks for one tag; a re-mark replaces, it does not append", len(got))
	}
	if got[0].Verdict != GroundTruthFalsePositive || got[0].Note != "second look" {
		t.Fatalf("the mark was not replaced: %+v", got[0])
	}
	tp, fp, measured, err := s.Precision(c, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !measured || tp != 0 || fp != 1 {
		t.Fatalf("precision after a re-mark = %d/%d measured=%v, want 0/1 measured", tp, fp, measured)
	}
}

// TestAnUnmarkedEventIsNotATruePositive: precision is TP/(TP+FP) and NOT
// TP/examined. Folding an unmarked event in would make the rate BETTER the less
// anyone reviewed, which is the exact shape of a metric that rewards not looking.
func TestAnUnmarkedEventIsNotATruePositive(t *testing.T) {
	s, runID, tag := runWithOnePush(t)
	c := context.Background()
	if err := s.MarkGroundTruth(c, runID, tag, GroundTruthTruePositive, "a reviewer", ""); err != nil {
		t.Fatal(err)
	}
	tp, fp, measured, err := s.Precision(c, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !measured || tp != 1 || fp != 0 {
		t.Fatalf("precision = %d/%d measured=%v, want 1/0 measured", tp, fp, measured)
	}
	if n, err := s.Count(c, runID); err != nil || n != 1 {
		t.Fatalf("the run should hold exactly one detection, and the denominator is the MARKS not the detections: count=%d err=%v", n, err)
	}
}
