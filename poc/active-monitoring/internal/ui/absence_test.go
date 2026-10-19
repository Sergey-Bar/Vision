package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
	"github.com/Sergey-Bar/vision/shared/aistate"
)

// The absence panel's controls, one test each.
//
// Every test here names the line it guards, because a control with no named line is
// a comment. The transcripts of each removal are in docs/UI-ABSENCE.md.

// absenceRow is one proposal in a seeded run: a detection, and optionally the
// answer for it. A proposal with no answer is `not_asked`, which is the only way to
// reach the state this panel exists for.
type absenceRow struct {
	id      string
	state   string
	verdict string
}

// seedAbsences writes a run's proposals. The detection is ACCEPTED first because
// `vlm_observations` carries a foreign key to it, exactly as the runner does it.
func seedAbsences(t *testing.T, s *store.Store, runID string, rows ...absenceRow) {
	t.Helper()
	ctx := context.Background()
	for i, r := range rows {
		at := time.Date(2026, 9, 30, 10, i, 0, 0, time.UTC)
		if err := s.Accept(ctx, store.Detection{
			ID: r.id, RunID: runID, At: at, Class: "car", Family: "vehicle",
			TrackID: i + 1, BoxTop: 100, BoxLeft: 100, BoxBottom: 200, BoxRight: 300,
			Score: 0.8, Provenance: store.ProvenanceSynthetic,
		}); err != nil {
			t.Fatal(err)
		}
		if r.state == "" {
			continue
		}
		if err := s.PutObservation(ctx, store.Observation{
			RunID: runID, DetectionID: r.id, PromptID: "alarm", AIState: r.state,
			Verdict: r.verdict, Summary: "the fence is empty and the road is clear",
			Confidence: 0.8, OutputHash: strings.Repeat("a", 64),
			ContractVersion: "hafnia/alarm/1", ModelIdentity: "hafnia (cosmos-3-nano-16b)",
			Temperature: 0.01, ReasonCode: "timeout",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// absent renders the panel for a known summary, so a branch test reads as the claim
// it is guarding rather than as plumbing.
func absent(a store.SearchSummary) string {
	return absencePanel(Data{Absence: a, AbsenceKnown: true})
}

// TestAnUnreadableBreakdownSaysNoDataAndNotFourZeros guards the FIRST branch of
// absencePanel.
//
// Four zeros standing in for a read that failed is the exact failure this panel
// exists to prevent, because a zero beside `no_objects` is a claim about a provider
// nobody called. The mutation is `if !d.AbsenceKnown {` -> `if false {`, which
// sends an unread window down the measurement path.
func TestAnUnreadableBreakdownSaysNoDataAndNotFourZeros(t *testing.T) {
	got := absencePanel(Data{})
	if !strings.Contains(got, "no data") {
		t.Fatalf("an unread breakdown did not say so:\n%s", got)
	}
	if !strings.Contains(got, "This is not a zero") {
		t.Fatalf("the no-data line does not warn against reading it as one:\n%s", got)
	}
	for _, forbidden := range []string{"no_objects", "cannot_tell", "proposed"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("an unread breakdown rendered %q; a count nobody read is a fabricated measurement:\n%s", forbidden, got)
		}
	}
}

// TestAnEmptyWindowShowsTheDetectorSentenceAndNotFourZeros guards
// `case a.Proposed == 0:`.
//
// Zero proposals is a measurement: the detector fired in a run where nothing was
// proposed. But it is a measurement about the DETECTOR, and it has no rows for any
// of the four causes to be counted against. The mutation is `case a.Proposed == 0:`
// -> `case false:`, which renders a table of four zeros under a window with no
// detections in it.
func TestAnEmptyWindowShowsTheDetectorSentenceAndNotFourZeros(t *testing.T) {
	got := absent(store.SearchSummary{})
	if !strings.Contains(got, "NO detections at all") {
		t.Fatalf("a window with no proposals does not say so:\n%s", got)
	}
	if !strings.Contains(got, "not about the model") {
		t.Fatalf("the sentence does not say whose silence it is:\n%s", got)
	}
	if strings.Contains(got, "<table") {
		t.Fatalf("an empty window rendered a table of counts; there was no row to count:\n%s", got)
	}
}

// TestAWindowNobodyWasAskedAboutPrintsNoCountForTheAskedCauses guards
// askableCauses' `if a.Answered > 0 {`.
//
// With nothing answered, `no_objects` and `cannot_tell` are counts over a set with
// no members. A reader cannot tell that from a real zero, and a real zero beside
// the word `no_objects` says the model ran and saw an empty frame. The mutation is
// `if a.Answered > 0 {` -> `if true {`, which prints both.
func TestAWindowNobodyWasAskedAboutPrintsNoCountForTheAskedCauses(t *testing.T) {
	got := absent(store.SearchSummary{Proposed: 3, NotAsked: 2, Degraded: 1})
	if !strings.Contains(got, "asked about <b>NOTHING</b>") {
		t.Fatalf("a run that consulted nobody does not say so:\n%s", got)
	}
	if !strings.Contains(got, "consulted nobody about them") {
		t.Fatalf("the sentence does not say the run asked nobody:\n%s", got)
	}
	// The two causes that ARE counts here, and only those two.
	for _, want := range []string{"not_asked", "degraded"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the panel does not show the cause %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"no_objects", "cannot_tell"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("the panel counted %q against a window with no asked rows; that is a measurement of nothing:\n%s", forbidden, got)
		}
	}
	// And it must not carry the negative sentence a reader would take away.
	if strings.Contains(got, "found nothing") {
		t.Fatalf("the panel claims the model found nothing in a window nobody asked about:\n%s", got)
	}
}

// TestAWindowThatWasNeverFullyAnsweredSaysSoOnTheScreen guards
// absenceNotice's `if !a.Unsearchable() {`.
//
// This is `printNotAbsence` on the screen, and it is the one refusal the surface
// must never get wrong: a window with an unasked proposal is not a window with no
// matches, and the screen has no query for a reader to notice the difference
// themselves. The mutation is `if !a.Unsearchable() {` -> `if true {`, which drops
// the notice.
func TestAWindowThatWasNeverFullyAnsweredSaysSoOnTheScreen(t *testing.T) {
	got := absent(store.SearchSummary{Proposed: 12, Answered: 9, NoObjects: 4, Degraded: 1, NotAsked: 2, CannotTell: 5})
	if !strings.Contains(got, "THIS RUN IS NOT FULLY SEARCHABLE") {
		t.Fatalf("a run with two unasked proposals printed no qualification:\n%s", got)
	}
	if !strings.Contains(got, "3 of the 12 proposals in it were never answered (2 not asked, 1 degraded)") {
		t.Fatalf("the notice does not carry the counts a reader needs to judge it:\n%s", got)
	}
	if !strings.Contains(got, "not evidence the model saw nothing") {
		t.Fatalf("the notice does not refuse the negative reading:\n%s", got)
	}
}

// TestAFullyAnsweredWindowSaysNothingAboutSearchability is the other half of the
// control above: the notice is a sentence about a defect, and a run with every
// proposal answered does not have one. A notice that fires always is noise, and
// noise is how a notice stops being read.
func TestAFullyAnsweredWindowSaysNothingAboutSearchability(t *testing.T) {
	got := absent(store.SearchSummary{Proposed: 6, Answered: 6, NoObjects: 4, CannotTell: 2})
	if strings.Contains(got, "NOT FULLY SEARCHABLE") {
		t.Fatalf("a fully answered run was reported as unsearchable:\n%s", got)
	}
	// All four causes are still shown, including their zeros: a cause with a real
	// zero here IS a measurement, and it is the only thing distinguishing a clean
	// run from a run nobody looked at.
	for _, want := range []string{"no_objects", "degraded", "not_asked", "cannot_tell"} {
		if !strings.Contains(got, want) {
			t.Fatalf("a fully answered window does not show the cause %q at all:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "6 proposal(s), asked and answered 6 (4 with no objects, 0 degraded, 0 not asked, 2 cannot tell)") {
		t.Fatalf("the totals line is not the one `pocctl search` prints:\n%s", got)
	}
}

// TestTheScreenReadsTheWindowThroughTheSearchCall guards the wiring in Gather: the
// panel's numbers come from `store.SearchObservations`, the same call `pocctl
// search` makes.
//
// The mutation is removing the `absenceSummary` call, which leaves the panel with no
// measurement at all and the page claiming nothing about what was asked.
func TestTheScreenReadsTheWindowThroughTheSearchCall(t *testing.T) {
	s, id := uiStore(t)
	seedAbsences(t, s, id,
		absenceRow{id: "d1", state: aistate.Asked, verdict: string(vlm.VerdictNone)},
		absenceRow{id: "d2", state: aistate.Asked, verdict: string(vlm.VerdictConfirmed)},
		absenceRow{id: "d3"},
		absenceRow{id: "d4"},
	)
	h := newUI(t, s, id).Handler()
	d := getJSON(t, h, "/api/summary")
	if !d.AbsenceKnown {
		t.Fatalf("the screen gathered no absence breakdown at all; every count on the panel would be invented. unavailable=%v", d.Unavailable)
	}
	want := store.SearchSummary{Proposed: 4, Answered: 2, NoObjects: 1, NotAsked: 2, CannotTell: 1}
	if d.Absence != want {
		t.Fatalf("Absence = %+v, want %+v; the panel must partition the window exactly as `pocctl search` does", d.Absence, want)
	}
	if d.Absence.HitsFound != 0 {
		t.Fatalf("HitsFound = %d on a screen that ran no query; it is a count of matches for a question nobody asked",
			d.Absence.HitsFound)
	}
	_, body := get(t, h, "/")
	for _, want := range []string{"why there is no answer", "THIS RUN IS NOT FULLY SEARCHABLE", "4 proposal(s), asked and answered 2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the live page does not carry %q:\n%s", want, body)
		}
	}
}

// TestAnUnreadableStoreGivesThePanelNoDataRatherThanFourZeros is the same control
// as TestAnUnreadableBreakdownSaysNoDataAndNotFourZeros, reached through a real
// failed read rather than a constructed value. A branch test proves the panel
// renders its input honestly; this proves the page ever produces that input.
func TestAnUnreadableStoreGivesThePanelNoDataRatherThanFourZeros(t *testing.T) {
	s, id := uiStore(t)
	seedAbsences(t, s, id, absenceRow{id: "d1", state: aistate.Asked, verdict: string(vlm.VerdictNone)})
	if _, err := s.DB().Exec("DROP TABLE vlm_observations"); err != nil {
		t.Fatal(err)
	}
	h := newUI(t, s, id).Handler()

	d := getJSON(t, h, "/api/summary")
	if d.AbsenceKnown {
		t.Fatalf("the breakdown read %+v from a store whose answer table is gone", d.Absence)
	}
	if !strings.Contains(strings.Join(d.Unavailable, "; "), "absence breakdown") {
		t.Fatalf("the feed does not name what it could not read: %v", d.Unavailable)
	}
	_, body := get(t, h, "/")
	if !strings.Contains(body, "no data") {
		t.Fatalf("the page rendered numbers while the answer table was unreadable:\n%s", body)
	}
	if strings.Contains(absencePanel(d), "<table") {
		t.Fatalf("the panel counted an unreadable window:\n%s", absencePanel(d))
	}
}

// TestThePanelNeverPrintsAPercentageOrARanking is rules 1 and 4 on this surface: a
// percentage against a row nobody asked about is a fabricated measurement, and a
// similarity rank is not replayable from stored facts.
//
// It is falsified by ADDING the violation rather than by removing a guard, because
// there is no line to remove: the control is the absence of the thing. The
// transcript in docs/UI-ABSENCE.md inserts a percentage into the panel and watches
// this test go red.
func TestThePanelNeverPrintsAPercentageOrARanking(t *testing.T) {
	for _, a := range []store.SearchSummary{
		{},
		{Proposed: 3, NotAsked: 2, Degraded: 1},
		{Proposed: 12, Answered: 9, NoObjects: 4, NotAsked: 2, CannotTell: 1},
		{Proposed: 6, Answered: 6, NoObjects: 4, CannotTell: 2},
	} {
		got := absent(a)
		if strings.Contains(got, "%") {
			t.Fatalf("the panel printed a percentage for %+v; there are no rates on this surface", a)
		}
		for _, forbidden := range []string{"ranked", "similarity", "most relevant", "relevance", "embedding"} {
			if strings.Contains(strings.ToLower(got), forbidden) {
				t.Fatalf("the panel ranked its content (%q) for %+v", forbidden, a)
			}
		}
	}
}

// TestThePanelPointsAtTheRecordedWindowAndNotAVideoFile is rule 5.
//
// The panel's one ask is a command, and the command's output is a clip reference,
// its exact bounds and its sha256. There is no continuous archive, so a pointer
// that offered a file would be an offer that fails when it is taken, which is the
// failure the evidence rules exist to stop.
func TestThePanelPointsAtTheRecordedWindowAndNotAVideoFile(t *testing.T) {
	d := Data{AbsenceKnown: true, DBPath: `C:\poc-data\poc.db`}
	d.Run = &store.Run{ID: "run-1"}
	got := absencePointer(d)
	for _, want := range []string{"pocctl search", "--run run-1", "clip reference", "exact bounds", "sha256", "no continuous video file"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the pointer does not say %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"full video", "whole file", ".mp4", "the video file"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("the pointer offers %q; there is no continuous archive to offer:\n%s", forbidden, got)
		}
	}
	// A data root with a space in it stays one argument rather than two.
	spaced := absencePointer(Data{AbsenceKnown: true, DBPath: `C:\Program Files\poc\poc.db`})
	if !strings.Contains(spaced, `&#34;C:\Program Files\poc&#34;`) {
		t.Fatalf("a data root with a space in it is not quoted:\n%s", spaced)
	}
}

// TestTheLivePageShowsBothInterestingWindows renders the two cases end to end,
// because a panel that is right in a unit test and absent from the page is a panel
// nobody sees.
func TestTheLivePageShowsBothInterestingWindows(t *testing.T) {
	t.Run("everything asked", func(t *testing.T) {
		s, id := uiStore(t)
		seedAbsences(t, s, id,
			absenceRow{id: "d1", state: aistate.Asked, verdict: string(vlm.VerdictNone)},
			absenceRow{id: "d2", state: aistate.Asked, verdict: string(vlm.VerdictNone)},
			absenceRow{id: "d3", state: aistate.Asked, verdict: string(vlm.VerdictConfirmed)},
		)
		_, body := get(t, newUI(t, s, id).Handler(), "/")
		if strings.Contains(body, "NOT FULLY SEARCHABLE") {
			t.Fatalf("a fully answered run was reported unsearchable:\n%s", body)
		}
		if !strings.Contains(body, "3 proposal(s), asked and answered 3 (2 with no objects, 0 degraded, 0 not asked, 1 cannot tell)") {
			t.Fatalf("the totals line is missing or has drifted:\n%s", body)
		}
	})
	t.Run("unasked proposals beside one answer", func(t *testing.T) {
		s, id := uiStore(t)
		seedAbsences(t, s, id,
			absenceRow{id: "d1", state: aistate.Asked, verdict: string(vlm.VerdictNone)},
			absenceRow{id: "d2"},
			absenceRow{id: "d3"},
		)
		h := newUI(t, s, id).Handler()
		_, body := get(t, h, "/")
		if !strings.Contains(body, "3 proposal(s), asked and answered 1 (1 with no objects, 0 degraded, 2 not asked, 0 cannot tell)") {
			t.Fatalf("the totals line is missing or has drifted:\n%s", body)
		}
		if !strings.Contains(body, "2 of the 3 proposals in it were never answered (2 not asked, 0 degraded)") {
			t.Fatalf("the notice does not carry the counts:\n%s", body)
		}
		if strings.Contains(body, "found nothing") {
			t.Fatalf("the page claims a negative answer for a window with two unasked proposals:\n%s", body)
		}
	})
	t.Run("nobody asked at all", func(t *testing.T) {
		s, id := uiStore(t)
		seedAbsences(t, s, id, absenceRow{id: "d1"}, absenceRow{id: "d2"}, absenceRow{id: "d3"})
		h := newUI(t, s, id).Handler()
		_, body := get(t, h, "/")
		if !strings.Contains(body, "asked about <b>NOTHING</b>") {
			t.Fatalf("the page does not say the model was asked about nothing:\n%s", body)
		}
		if !strings.Contains(body, "3 of the 3 proposals in it were never answered (3 not asked, 0 degraded)") {
			t.Fatalf("the notice does not carry the counts:\n%s", body)
		}
		if strings.Contains(absencePanel(getJSON(t, h, "/api/summary")), "no_objects") {
			t.Fatal("the page counted no_objects in a window with no asked rows")
		}
	})
}
