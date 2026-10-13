package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// The S4 golden test, and it is a golden test of the STRUCTURE only.
//
// `AGENTS.md` forbids golden-testing VLM prose, and the reason is not pedantry: a
// model's sentence is a function of a hosted model that may be re-tuned between two
// runs of this test, so a golden file containing prose is a test that fails for
// reasons unrelated to the code and gets disabled within a week.
//
// So what is pinned here is everything a reader relies on and the model does not
// produce: the hour count, that every hour has a row, that every rendered answer
// resolves to a stored observation with its attribution, and that the scaffolding is
// byte-stable. The prose is never compared.

// TestTheBriefHasOneRowPerHeartbeatedHourAndNoOther is the row-completeness half.
// An hour with no detections still gets a row; an hour with no heartbeat does not
// appear at all, and that gap is a gap in liveness.
func TestTheBriefHasOneRowPerHeartbeatedHourAndNoOther(t *testing.T) {
	root, runID := seedBriefRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	beats, err := st.HourlyLiveness(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	// seedBriefRun writes heartbeats at 10:30, 11:00 and 11:30 — two hours, and the
	// second has a detection in it.
	if len(beats) != 2 {
		t.Fatalf("hours = %d (%s), want 2; one row per hour that reported, and no row invented for an hour that did not",
			len(beats), hourList(beats))
	}
	if got := beats[0].Hour.Format("15:04"); got != "10:00" {
		t.Fatalf("first hour = %s; the first heartbeat at 10:30 belongs to hour 10", got)
	}
	// Frames is a CUMULATIVE counter, so an hour's figure is the difference between
	// its own last sample and the previous hour's. Printing the raw counter would
	// show 1500 in every hour of a run that read 1500 frames in the first one.
	if beats[0].Frames != 300 {
		t.Fatalf("hour 10 frames = %d, want 300", beats[0].Frames)
	}
	if beats[1].Frames != 1200 {
		t.Fatalf("hour 11 frames = %d, want 1200; the counter is cumulative and the brief prints the per-hour difference", beats[1].Frames)
	}
	// And the quiet hour still has a row.
	if beats[0].Proposed != 0 || beats[0].Frames == 0 {
		t.Fatalf("hour 10 = %+v; a quiet hour must still be a row", beats[0])
	}
	if beats[1].Proposed != 1 {
		t.Fatalf("hour 11 Proposed = %d, want 1", beats[1].Proposed)
	}
}

// TestEveryRenderedAnswerResolvesToAStoredObservation is R8's test.
//
// The rule is that a caption is a claim, and a claim carries its attribution. A
// brief that prints a sentence with no verdict, no prompt id and no evidence ref
// beside it is printing a caption, and a caption is exactly what invariant 3 exists
// to stop looking like a measurement.
//
// It reads the store rather than the screen, and it reads it through
// `BriefAnswers` — which is the ONLY function `cmdBrief` renders its prose from. So
// "every rendered line resolves" is a statement about the actual data path, and a
// store that starts accepting an answer without an evidence ref fails here instead
// of producing an unattributed screen.
func TestEveryRenderedAnswerResolvesToAStoredObservation(t *testing.T) {
	root, runID := seedBriefRunWithAnswer(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	answers, err := st.BriefAnswers(context.Background(), runID, time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 {
		t.Fatalf("answers = %d, want the one seeded answered alarm", len(answers))
	}
	a := answers[0]
	if a.Summary == "" {
		t.Fatal("a rendered answer has no summary, so the line a reader sees is a verdict and nothing else")
	}
	if a.StorageRef == "" {
		t.Fatal("a rendered answer has no evidence ref, so nobody can open the clip and adjudicate it (AC6)")
	}
	if a.Verdict != string(vlm.VerdictConfirmed) || a.PromptID != "alarm" {
		t.Fatalf("a rendered answer carries verdict=%q prompt=%q; both are needed to read it", a.Verdict, a.PromptID)
	}
	if a.At.IsZero() {
		t.Fatal("a rendered answer has no timestamp, so a reader cannot place it in the run")
	}
	// The box must be normalised: `detections` stores 0..1000 thousandths and a brief
	// that printed those raw would show `194` where the question was "where in the
	// frame".
	if a.X1 < 0 || a.X1 > 1 || a.X2 < 0 || a.X2 > 1 || a.Y1 < 0 || a.Y1 > 1 || a.Y2 < 0 || a.Y2 > 1 {
		t.Fatalf("a box (%v,%v)-(%v,%v) is outside 0..1; the brief is printing raw thousandths", a.X1, a.Y1, a.X2, a.Y2)
	}
	// And it renders: the prose line on the screen is this stored sentence.
	out := runBrief(t, root, runID)
	if !strings.Contains(out, a.Summary) {
		t.Fatalf("the brief did not render the stored sentence:\n%s", out)
	}
	if !strings.Contains(out, a.StorageRef) {
		t.Fatalf("the brief rendered a sentence without its evidence ref beside it, which is R8:\n%s", out)
	}
}

// TestTheBriefScaffoldingIsByteStableAroundProse is the byte-stability half.
//
// The table around the prose — header, column labels, hour rows, footer — must be
// identical across renders of the same data. The prose is skipped, because it is
// the model's and may legitimately differ between a model version and the next.
func TestTheBriefScaffoldingIsByteStableAroundProse(t *testing.T) {
	root, runID := seedBriefRunWithAnswer(t)
	first := scaffolding(runBrief(t, root, runID))
	if strings.TrimSpace(first) == "" {
		t.Fatal("the brief printed no scaffolding at all; the stability check passed by comparing two empty documents")
	}
	// At least a header and one row per hour, or the comparison below is stable
	// because there is almost nothing in it.
	if lines := strings.Count(first, "\n") + 1; lines < 3 {
		t.Fatalf("the scaffolding is %d line(s), which is too little for a stability check to mean anything:\n%s", lines, first)
	}
	for i := 0; i < 4; i++ {
		if got := scaffolding(runBrief(t, root, runID)); got != first {
			t.Fatalf("render %d produced different scaffolding from the first:\n--- first ---\n%s\n--- again ---\n%s", i+2, first, got)
		}
	}
}

// scaffolding keeps the lines a model's prose cannot appear on.
//
// Everything here is this repository's own text and must be byte-identical between
// renders. The prose lines are the model's timestamp-and-verdict header, the
// sentence itself, and its indented evidence line.
func scaffolding(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "box ("), // the evidence line under a prose line
			strings.HasPrefix(t, "An hour with"),
			strings.HasPrefix(t, "An hour of"):
			// prose or a fixed footer: skipped
		case strings.HasPrefix(t, "brief:"), strings.HasPrefix(t, "hour"),
			strings.HasPrefix(t, "what the model said"):
			keep = append(keep, line)
		case len(t) > 0 && t[0] >= '0' && t[0] <= '9' && strings.Contains(t, ":") && strings.Contains(t, "  "):
			// the hour row: "<hour>  <frames>  <hits>  <what happened>"
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

func hourList(beats []store.HourBeat) string {
	var b strings.Builder
	for _, h := range beats {
		b.WriteString(h.Hour.Format("15:04") + " ")
	}
	return b.String()
}

// seedBriefRunWithAnswer is seedBriefRun plus one answered alarm, so the citation
// property is asserted on a run that actually has prose to attribute.
func seedBriefRunWithAnswer(t *testing.T) (root, runID string) {
	t.Helper()
	root, runID = seedBriefRun(t)
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	putObservation(t, st, runID, "d1", "asked_answered", "alarm",
		string(vlm.VerdictConfirmed), "a magpie landed on the fence and stayed for four seconds")
	return root, runID
}
