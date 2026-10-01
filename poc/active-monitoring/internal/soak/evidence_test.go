package soak

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// TestTheEvidenceRowPointsAtTheDetectionThatExists is AC3's whole reason for
// existing, and it was never true until 2026-10-01.
//
// `cutAndStore` minted its own id — `cutDetectionID(sha, at)` — and wrote that into
// BOTH `id` and `detection_id`. The second column is a FOREIGN KEY to
// `detections(id)`, and a freshly minted `ev-<hash>` names a row that has never
// existed, so every `PutEvidence` failed with `FOREIGN KEY constraint failed`.
//
// It stayed invisible because the failure is a legitimate-looking outcome: a clip
// that cannot be cut is a real thing that happens, the runner transitions the
// detection to `clip_refused`, records a fault, and carries on. So a run printed
// ten detections and zero stored evidence and every other criterion stayed green.
//
// The fix was one line — pass the detection's own id in — and the regression needs a
// test that would have caught the whole thing, which is what this is: the store
// rejects evidence for a detection that does not exist, so the two are not
// interchangeable and a future mint-from-hash would fail loudly rather than quietly.
func TestTheEvidenceRowPointsAtTheDetectionThatExists(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const runID = "run-ev-1"
	if err := st.StartRun(ctx, store.Run{
		ID: runID, Label: "evidence", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	const detectionID = "det-real"
	if err := st.Accept(ctx, store.Detection{
		ID: detectionID, RunID: runID, At: time.Date(2026, 9, 30, 10, 0, 5, 0, time.UTC),
		Class: "motion", Family: "other", Direction: "left-to-right", Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}

	sha := "e60759b6bd0088c1b5c9e0cee23118c7e15b035f3cec4f4cd87f4d89615788f2"
	row := store.Evidence{
		ID: evidenceID(detectionID, sha), DetectionID: detectionID, SHA256: sha,
		StorageRef: "ab/cd/" + sha + ".mp4", Bytes: 1024, Trim: "0.000-2.000",
		TimeBegin: time.Date(2026, 9, 30, 10, 0, 3, 0, time.UTC),
		TimeEnd:   time.Date(2026, 9, 30, 10, 0, 7, 0, time.UTC), ReadbackOK: true,
	}
	if err := st.PutEvidence(ctx, row); err != nil {
		t.Fatalf("evidence for a detection that exists was refused: %v", err)
	}

	// The row is readable back against the DETECTION, not against a hash of the
	// clip. This is the shape the runner had and did not have.
	got, err := st.EvidenceFor(ctx, detectionID)
	if err != nil {
		t.Fatalf("the evidence row is not readable against the detection it belongs to: %v", err)
	}
	if len(got) != 1 || got[0].SHA256 != sha {
		t.Fatalf("evidence read back as %+v, want one row with sha %s", got, sha)
	}
	if got[0].DetectionID != detectionID {
		t.Fatalf("the row's detection_id is %q; a minted hash here is what made AC3 unreachable", got[0].DetectionID)
	}

	// `evidenceID` is a function of the DETECTION and the bytes, so two clips
	// for one detection are two rows and one clip cannot be claimed by two
	// different detections.
	if evidenceID(detectionID, sha) == evidenceID(detectionID, "b"+sha[1:]) {
		t.Fatal("two clips for one detection produced the same evidence id; the second would overwrite the first")
	}
	if evidenceID(detectionID, sha) == evidenceID("det-other", sha) {
		t.Fatal("two detections sharing a clip hash produced the same evidence id")
	}
}
