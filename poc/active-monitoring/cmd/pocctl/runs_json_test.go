package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// The two JSON surfaces the hub reads: `pocctl runs --json` and
// `pocctl review --json`.
//
// The field names are the contract, so these tests parse the output with the
// exact names the consumer uses rather than into a map of whatever came back. A
// renamed key fails here instead of quietly reading as an empty string in the
// hub.

// --- seeding ----------------------------------------------------------------

// seedTwoRuns builds a store with two runs: an older one that recorded two
// detections, and a newer one, still OPEN, that recorded none. The ordering and
// the counts are the two things `pocctl runs` exists to print, and the open run
// is what makes the `endedAt` omission testable.
func seedTwoRuns(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	if err := st.StartRun(ctx, store.Run{
		ID: "run-older", Label: "older run", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	putDetection(t, st, "run-older", "d-old-1", at(10, 1))
	putDetection(t, st, "run-older", "d-old-2", at(10, 2))
	if err := st.EndRun(ctx, "run-older", at(10, 30), "done"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartRun(ctx, store.Run{
		ID: "run-newer", Label: "newer run", Mode: "live", SourceKind: "rtsp", SourceArg: "cam-1",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(11, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

// seedReviewRuns builds a store with two runs: one that pushed two events (one
// judged true_positive, one not yet judged) and one whose only detection was
// never pushed, which is the empty page.
func seedReviewRuns(t *testing.T) (root, pushed, quiet string) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	pushed = "run-review-pushed"
	if err := st.StartRun(ctx, store.Run{
		ID: pushed, Label: "review", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	putDetection(t, st, pushed, "d-marked", at(10, 5))
	putDetection(t, st, pushed, "d-unmarked", at(10, 6))
	for _, e := range []struct{ detection, tag string }{
		{"d-marked", "aicap-bird-marked01"},
		{"d-unmarked", "aicap-bird-unmark01"},
	} {
		if _, err := st.Enqueue(ctx, store.OutboxEntry{
			DetectionID: e.detection, Kind: store.KindEvent,
			Payload: `{"tag":"` + e.tag + `"}`, Tag: e.tag,
		}, 4); err != nil {
			t.Fatal(err)
		}
	}
	// One person's verdict on one of the two events, so `judged` has to count
	// rather than assume.
	if err := st.MarkGroundTruth(ctx, pushed, "aicap-bird-marked01", store.GroundTruthTruePositive, "Sergey", ""); err != nil {
		t.Fatal(err)
	}

	quiet = "run-review-quiet"
	if err := st.StartRun(ctx, store.Run{
		ID: quiet, Label: "quiet", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(11, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	// A detection that was never pushed: it is in `detections` and must NOT
	// appear in a review, because a review is of what left the process.
	putDetection(t, st, quiet, "d-never-pushed", at(11, 5))
	return root, pushed, quiet
}

// --- store.ListRuns ---------------------------------------------------------

func TestListRunsIsNewestFirstWithDetectionCounts(t *testing.T) {
	root := seedTwoRuns(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	list, err := st.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListRuns returned %d runs, want 2", len(list))
	}
	if list[0].Run.ID != "run-newer" || list[1].Run.ID != "run-older" {
		t.Fatalf("runs are not newest-first: %s then %s", list[0].Run.ID, list[1].Run.ID)
	}
	if list[0].Detections != 0 {
		t.Fatalf("the newer run's detection count = %d, want 0", list[0].Detections)
	}
	if list[1].Detections != 2 {
		t.Fatalf("the older run's detection count = %d, want 2", list[1].Detections)
	}
	if list[0].Run.EndedAt != nil {
		t.Fatalf("the open run came back ended at %s; an open run has no end time", list[0].Run.EndedAt)
	}
	if list[1].Run.EndedAt == nil || !list[1].Run.EndedAt.Equal(at(10, 30)) {
		t.Fatalf("the closed run's end time was not read back: %v", list[1].Run.EndedAt)
	}
	if list[1].Run.Label != "older run" || !list[1].Run.StartedAt.Equal(at(10, 0)) {
		t.Fatalf("the older run came back as %q starting %s", list[1].Run.Label, list[1].Run.StartedAt)
	}
}

// --- pocctl runs --json -----------------------------------------------------

// runsJSONTestDoc mirrors the contract's field names.
type runsJSONTestDoc struct {
	DataRoot    string `json:"dataRoot"`
	GeneratedAt string `json:"generatedAt"`
	Count       int    `json:"count"`
	Runs        []struct {
		ID            string  `json:"id"`
		Label         string  `json:"label"`
		Mode          string  `json:"mode"`
		SourceKind    string  `json:"sourceKind"`
		SourceArg     string  `json:"sourceArg"`
		StartedAt     string  `json:"startedAt"`
		EndedAt       string  `json:"endedAt"`
		TargetSeconds float64 `json:"targetSeconds"`
		Note          string  `json:"note"`
		Detections    int     `json:"detections"`
	} `json:"runs"`
}

func TestRunsJSONIsNewestFirstAndOmitsAnOpenEnd(t *testing.T) {
	root := seedTwoRuns(t)
	out := capture(t, []string{"--data-root", root, "--json"}, cmdRuns)

	var doc runsJSONTestDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("runs --json did not parse: %v\n%s", err, out)
	}
	if doc.DataRoot != root {
		t.Fatalf("dataRoot = %q, want %q as passed", doc.DataRoot, root)
	}
	if doc.GeneratedAt == "" {
		t.Fatal("generatedAt is missing")
	}
	if doc.Count != 2 || len(doc.Runs) != 2 {
		t.Fatalf("count = %d with %d run(s), want 2 and 2", doc.Count, len(doc.Runs))
	}
	if doc.Runs[0].ID != "run-newer" || doc.Runs[1].ID != "run-older" {
		t.Fatalf("runs are not newest-first: %s then %s", doc.Runs[0].ID, doc.Runs[1].ID)
	}
	if doc.Runs[0].StartedAt != "2026-09-30T11:00:00Z" {
		t.Fatalf("startedAt = %q, want RFC3339 UTC", doc.Runs[0].StartedAt)
	}
	if doc.Runs[0].EndedAt != "" {
		t.Fatalf("the open run printed endedAt %q", doc.Runs[0].EndedAt)
	}
	if doc.Runs[1].EndedAt != "2026-09-30T10:30:00Z" {
		t.Fatalf("the closed run printed endedAt %q", doc.Runs[1].EndedAt)
	}
	if doc.Runs[0].Detections != 0 || doc.Runs[1].Detections != 2 {
		t.Fatalf("detections = %d and %d, want 0 and 2", doc.Runs[0].Detections, doc.Runs[1].Detections)
	}
	if doc.Runs[0].SourceKind != "rtsp" || doc.Runs[1].SourceArg != "f.mp4" {
		t.Fatalf("sourceKind/sourceArg were not carried: %+v / %+v", doc.Runs[0], doc.Runs[1])
	}

	// `endedAt` must be OMITTED, not empty: a typed struct cannot tell those two
	// apart, so the key's presence is checked on the raw document.
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	runs, _ := raw["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("runs is %T with %d entries", raw["runs"], len(runs))
	}
	if _, has := runs[0].(map[string]any)["endedAt"]; has {
		t.Fatalf("the open run's endedAt key is present; it must be omitted:\n%s", out)
	}
	if _, has := runs[1].(map[string]any)["endedAt"]; !has {
		t.Fatalf("the closed run's endedAt key is missing:\n%s", out)
	}
}

func TestRunsJSONPrintsAnEmptyArrayForAnEmptyStore(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	out := capture(t, []string{"--data-root", root, "--json"}, cmdRuns)

	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("runs --json did not parse: %v\n%s", err, out)
	}
	runs, ok := raw["runs"].([]any)
	if !ok {
		t.Fatalf(`"runs" is %T, not an array; an empty store must print [] and never null`, raw["runs"])
	}
	if len(runs) != 0 {
		t.Fatalf("an empty store listed %d run(s)", len(runs))
	}
	if count, _ := raw["count"].(float64); count != 0 {
		t.Fatalf("count = %v, want 0", raw["count"])
	}
}

// TestRunsTextSaysThereAreNoRunsRatherThanZero: the text line for an empty store
// is "no runs", not "0 run(s)", because a zero would read as a measured count of
// nothing rather than an answer about an empty directory.
func TestRunsTextSaysThereAreNoRunsRatherThanZero(t *testing.T) {
	root := t.TempDir()
	out := capture(t, []string{"--data-root", root}, cmdRuns)
	if !strings.Contains(out, "no runs in "+root) {
		t.Fatalf("the empty text output does not name the empty store:\n%s", out)
	}
}

// --- pocctl review --json ---------------------------------------------------

// reviewJSONTestDoc mirrors the contract's field names.
type reviewJSONTestDoc struct {
	DataRoot    string `json:"dataRoot"`
	Run         string `json:"run"`
	GeneratedAt string `json:"generatedAt"`
	Count       int    `json:"count"`
	Judged      int    `json:"judged"`
	Rows        []struct {
		DetectionID string    `json:"detectionId"`
		At          string    `json:"at"`
		Class       string    `json:"class"`
		Family      string    `json:"family"`
		Score       float64   `json:"score"`
		Box         []float64 `json:"box"`
		Tag         string    `json:"tag"`
		ClipRef     string    `json:"clipRef"`
		Verdict     string    `json:"verdict"`
		MarkedBy    string    `json:"markedBy"`
		MarkedAt    string    `json:"markedAt"`
	} `json:"rows"`
}

func TestReviewJSONCarriesTheClipTheBoxAndTheVerdict(t *testing.T) {
	root, pushed, _ := seedReviewRuns(t)
	out := capture(t, []string{"--data-root", root, "--run", pushed, "--json"}, cmdReview)

	var doc reviewJSONTestDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("review --json did not parse: %v\n%s", err, out)
	}
	if doc.DataRoot != root || doc.Run != pushed {
		t.Fatalf("dataRoot/run = %q/%q, want %q/%q", doc.DataRoot, doc.Run, root, pushed)
	}
	if doc.Count != 2 || len(doc.Rows) != 2 {
		t.Fatalf("count = %d with %d row(s), want 2 and 2", doc.Count, len(doc.Rows))
	}
	if doc.Judged != 1 {
		t.Fatalf("judged = %d, want 1; exactly one of the two events was marked", doc.Judged)
	}
	// Newest first, so the unmarked event is the first row and the marked one is
	// the second.
	if doc.Rows[0].DetectionID != "d-unmarked" || doc.Rows[1].DetectionID != "d-marked" {
		t.Fatalf("rows are not newest-first: %s then %s", doc.Rows[0].DetectionID, doc.Rows[1].DetectionID)
	}
	for i, row := range doc.Rows {
		if len(row.Box) != 4 {
			t.Fatalf("row %d box = %v, want four numbers", i, row.Box)
		}
		if row.ClipRef == "" {
			t.Fatalf("row %d has no clipRef; the seeded evidence should have supplied one", i)
		}
		if row.Class == "" || row.Family == "" || row.Tag == "" {
			t.Fatalf("row %d lost class/family/tag: %+v", i, row)
		}
	}
	wantBox := []float64{0.286, 0.194, 0.408, 0.429}
	for i, got := range doc.Rows[1].Box {
		if math.Abs(got-wantBox[i]) > 1e-9 {
			t.Fatalf("box = %v, want %v", doc.Rows[1].Box, wantBox)
		}
	}
	wantClip := "ab/cd/" + shaFor("d-marked") + ".mp4"
	if doc.Rows[1].ClipRef != wantClip {
		t.Fatalf("clipRef = %q, want %q", doc.Rows[1].ClipRef, wantClip)
	}
	if doc.Rows[1].At != "2026-09-30T10:05:00Z" {
		t.Fatalf("at = %q, want RFC3339 UTC", doc.Rows[1].At)
	}
	if doc.Rows[1].Verdict != store.GroundTruthTruePositive || doc.Rows[1].MarkedBy != "Sergey" {
		t.Fatalf("verdict/markedBy = %q/%q, want true_positive/Sergey", doc.Rows[1].Verdict, doc.Rows[1].MarkedBy)
	}
	if doc.Rows[1].MarkedAt == "" {
		t.Fatal("a marked row printed no markedAt")
	}
	if _, err := time.Parse(time.RFC3339, doc.Rows[1].MarkedAt); err != nil {
		t.Fatalf("markedAt %q is not RFC3339: %v", doc.Rows[1].MarkedAt, err)
	}
	if doc.Rows[0].Verdict != "" || doc.Rows[0].MarkedBy != "" || doc.Rows[0].MarkedAt != "" {
		t.Fatalf("the unmarked row printed a verdict: %+v", doc.Rows[0])
	}

	// And the unmarked row must OMIT those keys, not print them empty: the hub
	// distinguishes "nobody looked" from "somebody answered" by their presence.
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	rows, _ := raw["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows is %T with %d entries", raw["rows"], len(rows))
	}
	first, _ := rows[0].(map[string]any)
	for _, key := range []string{"verdict", "markedBy", "markedAt"} {
		if _, has := first[key]; has {
			t.Fatalf("the unmarked row printed %q; it must be omitted:\n%s", key, out)
		}
	}
}

func TestReviewJSONPrintsAnEmptyArrayWhenNothingWasPushed(t *testing.T) {
	root, _, quiet := seedReviewRuns(t)
	out := capture(t, []string{"--data-root", root, "--run", quiet, "--json"}, cmdReview)

	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("review --json did not parse: %v\n%s", err, out)
	}
	rows, ok := raw["rows"].([]any)
	if !ok {
		t.Fatalf(`"rows" is %T, not an array; a run with no pushed events must print [] and never null`, raw["rows"])
	}
	if len(rows) != 0 {
		t.Fatalf("the quiet run listed %d row(s); its only detection was never pushed", len(rows))
	}
	if count, _ := raw["count"].(float64); count != 0 {
		t.Fatalf("count = %v, want 0", raw["count"])
	}
	if judged, _ := raw["judged"].(float64); judged != 0 {
		t.Fatalf("judged = %v, want 0", raw["judged"])
	}
	if raw["run"] != quiet {
		t.Fatalf("run = %v, want %q", raw["run"], quiet)
	}
}

// TestReviewTextIsUnchangedWithoutTheJSONFlag: `--json` must be additive. The
// text mode still writes the sheet and prints the same sentence, because the
// flag is the only thing that may change the output.
func TestReviewTextIsUnchangedWithoutTheJSONFlag(t *testing.T) {
	root, pushed, _ := seedReviewRuns(t)
	out := capture(t, []string{"--data-root", root, "--run", pushed}, cmdReview)

	if !strings.Contains(out, "2 event(s) for run "+pushed+", 1 already judged") {
		t.Fatalf("the text sheet changed:\n%s", out)
	}
	if !strings.Contains(out, "wrote ") {
		t.Fatalf("the text sheet no longer says where it went:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "review.html")); err != nil {
		t.Fatalf("the text mode wrote no sheet: %v", err)
	}
}
