package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// --- RC-9: the search surface must not print "no matches" for a window the model
// never searched --------------------------------------------------

// TestSearchRefusesToSayNoMatchesForAnUnaskedWindow is RC-9. The failure it stops
// is the one a judge is most likely to find: a run whose verifier never got a key,
// queried with a word that does appear in one prompt file, prints "no matches" — and
// a reader concludes the model looked and saw nothing, when the model was never
// asked.
func TestSearchRefusesToSayNoMatchesForAnUnaskedWindow(t *testing.T) {
	root, runID := seedSearchRun(t)
	out := runSearch(t, root, runID, "--query", "a magpie on the fence")

	if !strings.Contains(out, "not_asked") && !strings.Contains(out, "NEVER ASKED") {
		t.Fatalf("the window's model was never asked and the output does not say so:\n%s", out)
	}
	if !strings.Contains(out, "the model was asked about NOTHING") {
		t.Fatalf("no answer was obtained and the output does not say the model was never asked:\n%s", out)
	}
	if !strings.Contains(out, "THIS WINDOW IS NOT FULLY SEARCHABLE") {
		t.Fatalf("RC-9: an unsearchable window printed no qualification on its absence:\n%s", out)
	}
	// And the sentence a reader would take away must not be the negative one.
	if strings.Contains(out, "the model found nothing") {
		t.Fatalf("the output claims a negative answer that was never given:\n%s", out)
	}
}

// TestSearchCallsNoProvider is the structural half of R7: the surface reads a store
// and holds no observer and no client, so it cannot reach a provider at all.
func TestSearchCallsNoProvider(t *testing.T) {
	root, runID := seedSearchRun(t)
	// SP_POC_VLM_API_KEY is set to a value no provider would accept. If the search
	// reached a provider it would fail; a clean run against real stored rows is the
	// observation that it did not.
	t.Setenv("SP_POC_VLM_API_KEY", "unreachable-on-purpose")
	out := runSearch(t, root, runID, "--query", "magpie")
	if strings.Contains(out, "ApiKey") || strings.Contains(out, "no API key") {
		t.Fatalf("the search tried to build a provider client:\n%s", out)
	}
}

// TestAnUnansweredWindowIsUnsearchableAndAFullyAnsweredOneIsNot: the store's own
// answer to "can this window support the sentence the model found nothing".
func TestAnUnansweredWindowIsUnsearchableAndAFullyAnsweredOneIsNot(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	_, sum, err := st.SearchObservations(context.Background(), store.SearchQuery{RunID: runID, Text: "magpie"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Proposed != 1 {
		t.Fatalf("Proposed = %d; the partition is over detections, and a detection with no observation row is not_asked", sum.Proposed)
	}
	if sum.NotAsked != 1 || sum.Answered != 0 {
		t.Fatalf("sum = %+v; one detection with no observation must be counted as not_asked", sum)
	}
	if !sum.Unsearchable() {
		t.Fatal("a window whose only proposal was never asked reports itself searchable")
	}

	// Now answer it, and the same window must become searchable.
	if err := st.PutObservation(context.Background(), store.Observation{
		RunID: runID, DetectionID: "d-unasked", PromptID: "alarm", AIState: "asked_answered",
		Verdict: string(vlm.VerdictFalsePositive), Summary: "no magpie in frame; the fence is empty",
		Confidence: 0.9, OutputHash: strings.Repeat("a", 64), ContractVersion: "v1",
		ModelIdentity: "hafnia (cosmos-3-nano-16b)",
	}); err != nil {
		t.Fatal(err)
	}
	hits, sum2, err := st.SearchObservations(context.Background(), store.SearchQuery{RunID: runID, Text: "magpie"})
	if err != nil {
		t.Fatal(err)
	}
	if sum2.Unsearchable() {
		t.Fatalf("sum2 = %+v; a window whose every proposal was answered is searchable", sum2)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	// The hit carries the attribution and the evidence pointer, not just prose.
	h := hits[0]
	if h.OutputHash == "" || h.ModelIdentity == "" || h.ContractVersion == "" || h.Verdict == "" || h.AIState != "asked_answered" {
		t.Fatalf("the hit is a caption without attribution: %+v", h)
	}
}

// TestADegradedAnswerIsNotANegativeOne: `degraded` is asked-and-failed. It must
// not be counted as `no_objects`, and a window containing one must be unsearchable.
func TestADegradedAnswerIsNotANegativeOne(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.PutObservation(context.Background(), store.Observation{
		RunID: runID, DetectionID: "d-unasked", PromptID: "alarm", AIState: "asked_failed",
		Verdict: string(vlm.VerdictNone), ReasonCode: "timeout",
	}); err != nil {
		t.Fatal(err)
	}
	_, sum, err := st.SearchObservations(context.Background(), store.SearchQuery{RunID: runID, Text: "magpie"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NoObjects != 0 {
		t.Fatalf("a failed answer was counted as a negative one: %+v", sum)
	}
	if !sum.Unsearchable() {
		t.Fatalf("a window with a lost answer reports itself searchable: %+v", sum)
	}
}

// --- S4: the brief's hour classification --------------------------------

// TestTheFourHourStatesDoNotPrintTheSameSentence is S4's red check.
func TestTheFourHourStatesDoNotPrintTheSameSentence(t *testing.T) {
	cases := []struct {
		name string
		h    store.HourBeat
		want string
	}{
		{"dead stream", store.HourBeat{Frames: 0, Proposed: 0}, "stream was DOWN"},
		{"quiet stream", store.HourBeat{Frames: 900, Proposed: 0}, "stream up, detector found nothing"},
		{"never asked", store.HourBeat{Frames: 900, Proposed: 3, Answered: 0}, "NEVER ASKED"},
		{"asked and cleared", store.HourBeat{Frames: 900, Proposed: 3, Answered: 3}, "CLEARED them all"},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hourSentence(tc.h)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("sentence = %q, want it to contain %q", got, tc.want)
			}
			for other, sentence := range seen {
				if sentence == got {
					t.Fatalf("%q and %q print the SAME sentence %q", tc.name, other, got)
				}
			}
			seen[tc.name] = got
		})
	}
}

// TestTheBriefIsByteStable: a brief must be the same document for the same data, or
// "regenerate it" is not a check on anything.
func TestTheBriefIsByteStable(t *testing.T) {
	root, runID := seedBriefRun(t)
	first := runBrief(t, root, runID)
	for i := 0; i < 3; i++ {
		if got := runBrief(t, root, runID); got != first {
			t.Fatalf("run %d differed from the first:\n--- first ---\n%s\n--- again ---\n%s", i+2, first, got)
		}
	}
}

// TestAnHourWithNoHeartbeatIsNotAPass: a brief with no heartbeats at all is a run
// that never reported liveness, and it must say that rather than print an empty
// table.
func TestAnHourWithNoHeartbeatIsNotAPass(t *testing.T) {
	root, runID := seedSearchRun(t)
	err := cmdBrief([]string{"--data-root", root, "--run", runID})
	if err == nil {
		t.Fatal("a brief with no heartbeats was printed as a table")
	}
	if !strings.Contains(err.Error(), "no heartbeats") {
		t.Fatalf("the refusal does not name what is missing: %v", err)
	}
}

// --- helpers ----------------------------------------------------------

// runSearch runs `pocctl search` with the console captured and returns its output.
func runSearch(t *testing.T, root, runID string, args ...string) string {
	t.Helper()
	return capture(t, append([]string{"--data-root", root, "--run", runID}, args...), cmdSearch)
}

func runBrief(t *testing.T, root, runID string) string {
	t.Helper()
	return capture(t, []string{"--data-root", root, "--run", runID}, cmdBrief)
}

// capture runs a subcommand with stdout redirected and returns what it printed.
//
// The console is captured rather than the return value because the whole of these
// commands' behaviour IS the printing: a refusal printed as a table of zeros is a
// different outcome from the same refusal returned as an error, and only the output
// can tell them apart.
func capture(t *testing.T, args []string, cmd func([]string) error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	cmdErr := cmd(args)
	w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	if cmdErr != nil {
		t.Fatalf("the command refused: %v\n%s", cmdErr, out)
	}
	return out
}
