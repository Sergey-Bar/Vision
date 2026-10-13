package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// The three predicates S3 puts on a HIT, as the plan writes them:
// `prompt_id = 'alarm' AND aistate = 'asked_answered' AND summary LIKE ?`.
//
// Each one exists because the alternative returns something an operator would read
// as an answer and is not one.

// TestSearchReturnsOnlyAnsweredAlarmAsks: a `camera_integrity` sentence is about the
// VIEW. Matching it against a search for a car is a match on the word "the".
func TestSearchReturnsOnlyAnsweredAlarmAsks(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	putObservation(t, st, runID, "d-unasked", "asked_answered", "alarm",
		string(vlm.VerdictFalsePositive), "no magpie on the fence; the fence is empty")

	// A camera_integrity answer, whose sentence contains the same words.
	putDetection(t, st, runID, "d-cam", at(10, 6))
	putObservation(t, st, runID, "d-cam", "asked_answered", "camera_integrity",
		string(vlm.VerdictFalsePositive), "the camera is unchanged and the fence is empty")

	hits, sum, err := st.SearchObservations(ctx, store.SearchQuery{RunID: runID, Text: "fence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1; the camera_integrity answer is a sentence about the view, not about the car", len(hits))
	}
	if hits[0].PromptID != "alarm" {
		t.Fatalf("prompt_id = %q, want alarm", hits[0].PromptID)
	}
	// And the summary counts the whole window, integrity answers included, because
	// the window is what a reader is being told about.
	if sum.Answered != 2 {
		t.Fatalf("Answered = %d; the window summary must count every asked answer, or \"the model found nothing\" becomes true and useless", sum.Answered)
	}
}

// TestSearchDoesNotReturnADegradedSentenceAsAMatch: a degraded row's summary is
// whatever was stored before the failure, and matching it is a failure wearing a
// sentence's clothes.
func TestSearchDoesNotReturnADegradedSentenceAsAMatch(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	putObservation(t, st, runID, "d-unasked", "asked_answered", "alarm",
		string(vlm.VerdictFalsePositive), "a magpie landed on the fence at 11:02")
	if err := st.PutObservation(ctx, store.Observation{
		RunID: runID, DetectionID: "d-unasked", PromptID: "alarm", AIState: "asked_failed",
		Verdict: string(vlm.VerdictNone), ReasonCode: "timeout",
	}); err != nil {
		t.Fatal(err)
	}

	hits, sum, err := st.SearchObservations(ctx, store.SearchQuery{RunID: runID, Text: "magpie"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d; a failed ask is not an answer and must not be returned as one", len(hits))
	}
	if !sum.Unsearchable() {
		t.Fatalf("sum = %+v; the only proposal failed, so the window cannot support a negative answer", sum)
	}
}

// TestHitsFoundAndTheHitsAgree: `--limit` prints `HitsFound`, so a count computed
// with different predicates than the rows is a number nobody can see is wrong.
func TestHitsFoundAndTheHitsAgree(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id := "d" + string(rune('a'+i))
		putDetection(t, st, runID, id, at(10, 7+i))
		putObservation(t, st, runID, id, "asked_answered", "alarm",
			string(vlm.VerdictConfirmed), "a magpie on the fence")
	}
	// Plus one that must not count.
	putDetection(t, st, runID, "d-cam", at(10, 20))
	putObservation(t, st, runID, "d-cam", "asked_answered", "camera_integrity",
		string(vlm.VerdictConfirmed), "a magpie on the fence")

	hits, sum, err := st.SearchObservations(ctx, store.SearchQuery{RunID: runID, Text: "magpie", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want the limit of 3", len(hits))
	}
	if sum.HitsFound != 5 {
		t.Fatalf("HitsFound = %d, want 5; a count computed on different predicates than the rows is a wrong number printed as a right one", sum.HitsFound)
	}
}

// TestTheSearchPageSaysItDoesNotUseEmbeddings: the plan requires the sentence on the
// page rather than in a document. An operator who believes the ranking is
// semantic trusts it past what substring matching can carry.
func TestTheSearchPageSaysItDoesNotUseEmbeddings(t *testing.T) {
	root, runID := seedSearchRun(t)
	out := runSearch(t, root, runID, "--query", "magpie")
	if !strings.Contains(out, "SUBSTRING") || !strings.Contains(out, "no embeddings") {
		t.Fatalf("the search page does not say how it ranks:\n%s", out)
	}
	if !strings.Contains(out, "not") || !strings.Contains(out, "replayable from stored facts") {
		t.Fatalf("the search page does not give the reason a similarity rank is refused:\n%s", out)
	}
}

func putObservation(t *testing.T, st *store.Store, runID, detectionID, aiState, promptID, verdict, summary string) {
	t.Helper()
	if err := st.PutObservation(context.Background(), store.Observation{
		RunID: runID, DetectionID: detectionID, PromptID: promptID, AIState: aiState,
		Verdict: verdict, Summary: summary, Confidence: 0.8,
		OutputHash: strings.Repeat("b", 64), ContractVersion: "hafnia/alarm/1",
		ModelIdentity: "hafnia (cosmos-3-nano-16b)", Temperature: 0.01,
	}); err != nil {
		t.Fatal(err)
	}
}
