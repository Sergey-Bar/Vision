package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/ui"
)

// The two surfaces that describe the same window must describe it in the same
// words, and this is the test that says so.
//
// `pocctl search` is what a judge runs after the run, and the operator screen is
// what somebody watched DURING it. Both answer "what did the model say about this,
// and what was it never asked about". Two vocabularies for one window is how a run
// ends up with an answer on the console and a different one in the evidence, and the
// reconciliation never happens because both readings are individually defensible.
//
// The fragments below are quoted from `printAbsence` in search.go. They are not
// paraphrases and they are not allowed to drift: if one of them is reworded here,
// this test fails and the two surfaces are reworded together or not at all.

// windowBody is the operator screen's page for a run, read through the same handler
// the browser uses.
func windowBody(t *testing.T, root, runID string) string {
	t.Helper()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := ui.New(st, runID, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the screen answered %d", rec.Code)
	}
	return rec.Body.String()
}

func TestTheScreenAndTheCommandSayTheSameThingsAboutTheSameWindow(t *testing.T) {
	root, runID := seedSearchRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// Three proposals and three different fates, so every branch on both surfaces
	// has something to say: one answered `needs_review` (a `cannot_tell`), one asked
	// and lost (`degraded`), and one never sent at all (`not_asked`).
	putObservation(t, st, runID, "d-unasked", "asked_answered", "alarm", "needs_review",
		"a magpie on the fence and nothing else")
	putDetection(t, st, runID, "d-deg", at(10, 6))
	putObservation(t, st, runID, "d-deg", "asked_failed", "alarm", "none", "the fence is empty")
	putDetection(t, st, runID, "d-never", at(10, 7))
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	command := runSearch(t, root, runID, "--query", "magpie")
	screen := windowBody(t, root, runID)

	for _, shared := range []string{
		// The totals line is byte-identical: same partition, same arithmetic, same order.
		"3 proposal(s), asked and answered 1 (0 with no objects, 1 degraded, 1 not asked, 1 cannot tell)",
		"asked, and failed, late or malformed. A missing answer here is NOT a negative answer",
		"the detector proposed them and the verifier never saw them",
		"answered with insufficient evidence or needs_review, which is a positive claim that the frame did not settle it",
		"NOT FULLY SEARCHABLE",
		"2 of the 3 proposals in it were never answered (1 not asked, 1 degraded)",
	} {
		if !strings.Contains(command, shared) {
			t.Fatalf("pocctl search no longer prints %q, and the screen quotes it:\n%s", shared, command)
		}
		if !strings.Contains(screen, shared) {
			t.Fatalf("the screen does not carry %q:\n%s", shared, screen)
		}
	}
	// And neither surface is allowed the sentence a reader would take away.
	for _, forbidden := range []string{"the model found nothing", "found no matches about"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("pocctl search claims %q for a window with an unasked proposal:\n%s", forbidden, command)
		}
		if strings.Contains(screen, forbidden) {
			t.Fatalf("the screen claims %q for a run with an unasked proposal:\n%s", forbidden, screen)
		}
	}
}
