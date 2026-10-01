package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// seedSearchRun builds a store with one run, one detection and NO observation for
// it: the `not_asked` case, which is the one every summary built from the answer
// table alone cannot see.
func seedSearchRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const run = "run-search-1"
	if err := st.StartRun(ctx, store.Run{
		ID: run, Label: "search", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	putDetection(t, st, run, "d-unasked", at(10, 5))
	return root, run
}

// seedBriefRun builds a store with two heartbeated hours: one quiet and one with a
// cleared answer, so the hour table has more than one row and the ordering matters.
func seedBriefRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const run = "run-brief-1"
	if err := st.StartRun(ctx, store.Run{
		ID: run, Label: "brief", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 7200,
	}); err != nil {
		t.Fatal(err)
	}
	// Frames is a CUMULATIVE counter, so the second beat carries the higher value
	// and the brief must show the per-hour DIFFERENCE.
	for _, h := range []store.Heartbeat{
		{RunID: run, At: at(10, 30), Frames: 300, Detections: 0},
		{RunID: run, At: at(11, 0), Frames: 300, Detections: 0},
		{RunID: run, At: at(11, 30), Frames: 1500, Detections: 1},
	} {
		if err := st.Beat(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	putDetection(t, st, run, "d1", at(11, 10))
	return root, run
}

func putDetection(t *testing.T, st *store.Store, runID, id string, when time.Time) {
	t.Helper()
	ctx := context.Background()
	// The detection is ACCEPTED first, because `decisions`, `evidence` and
	// `vlm_observations` all carry a foreign key to it. In the pipeline this is the
	// R2 boundary: the event is committed before anything else happens to it.
	if err := st.Accept(ctx, store.Detection{
		ID: id, RunID: runID, At: when, Class: "bird", Family: "bird",
		Score: 0.7, BoxTop: 194, BoxLeft: 286, BoxBottom: 429, BoxRight: 408,
		Direction: "left_to_right", Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: id, RunID: runID, TriggerMode: "temporal", TriggerClass: "bird",
		Score: 0.7, Threshold: 0.35, Release: 0.3, Sustain: 1, Streak: 1,
		ScoreReason: "above_threshold", RegionSource: "measured",
		RegionTop: 194, RegionLeft: 286, RegionBottom: 429, RegionRight: 408,
	}); err != nil {
		t.Fatal(err)
	}
	// The evidence row is written after the decision, as the runner does it, and it
	// is the thing a judge pulls the clip by.
	if err := st.PutEvidence(ctx, store.Evidence{
		ID: id + "-ev", DetectionID: id, SHA256: shaFor(id), StorageRef: filepath.ToSlash("ab/cd/" + shaFor(id) + ".mp4"),
		Bytes: 1024, Trim: "0.000-2.000", TimeBegin: when.Add(-2 * time.Second), TimeEnd: when.Add(2 * time.Second),
		ReadbackOK: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 9, 30, hour, minute, 0, 0, time.UTC)
}

// shaFor is a 64-character hex string derived from the detection id, so two
// detections in a seeded store do not collide on evidence.sha256's unique index.
func shaFor(id string) string {
	sum := store.HashOf([]byte(id))
	for len(sum) < 64 {
		sum += sum
	}
	return sum[:64]
}
