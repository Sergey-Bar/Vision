package api

// The coverage panel's ASSEMBLY, tested without a database.
//
// The four kinds, their fixed order, the zero rows and the "measured but unknown"
// distinction are all in this file, and none of them need Postgres. What needs
// Postgres is whether the counts are right, and those are three statements whose
// shape the red-check below pins.
//
// # What these tests are for
//
// A panel that silently drops a row, reorders its rows, or renders a healthy site as
// broken is worse than no panel, because an operator who has been trained by it
// learns to skip it. The cases below are the ways that happens.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubAbsence answers the three counts from a map keyed by kind.
type stubAbsence struct {
	n      map[string]int
	worst  map[string]string
	since  map[string]time.Time
	err    error
	called []string
}

func (s *stubAbsence) BlindIntegrations(_ context.Context, _, _ string) (int, string, time.Time, error) {
	s.called = append(s.called, "vendor_feed")
	return s.n["vendor_feed"], s.worst["vendor_feed"], s.since["vendor_feed"], s.err
}

func (s *stubAbsence) DegradedObservations(_ context.Context, _, _ string) (int, string, time.Time, error) {
	s.called = append(s.called, "ai_plane")
	return s.n["ai_plane"], s.worst["ai_plane"], s.since["ai_plane"], s.err
}

func (s *stubAbsence) DriftedDevices(_ context.Context, _, _ string) (int, string, time.Time, error) {
	s.called = append(s.called, "clock_drift")
	return s.n["clock_drift"], s.worst["clock_drift"], s.since["clock_drift"], s.err
}

// A healthy site renders four rows, all zero.
//
// Not an empty table. A row that disappears when it is fine is a row that is only
// interesting when it is broken, and a reader who has been trained by that stops
// reading the table -- including on the day it is broken.
func TestACoveragePanelAlwaysHasItsFourRows(t *testing.T) {
	srv := &Server{now: func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }}
	reader := &stubAbsence{n: map[string]int{}, worst: map[string]string{}, since: map[string]time.Time{}}

	views, err := srv.Absence(context.Background(), reader, nil, "acme", []string{"north-gate"})
	if err != nil {
		t.Fatalf("read the panel: %v", err)
	}
	if len(views) != 4 {
		t.Fatalf("the panel has %d rows, want 4", len(views))
	}
	want := []string{"vendor_feed", "ai_plane", "terminal_failures", "clock_drift"}
	for i, v := range views {
		if v.Kind != want[i] {
			t.Errorf("row %d is %q, want %q. The order is fixed so the table can be "+
				"scanned; a panel that reorders between loads makes an operator re-learn it",
				i, v.Kind, want[i])
		}
		if v.Count != 0 {
			t.Errorf("%s has count %d on a site the stub says is clean", v.Kind, v.Count)
		}
		if v.Breaking {
			t.Errorf("%s is marked breaking with a count of 0. A row that is 'breaking' and "+
				"has nothing behind it is a finding with no evidence", v.Kind)
		}
		if v.What == "" {
			t.Errorf("%s has no action sentence. A row with no action is a decoration", v.Kind)
		}
	}
}

// Every kind is READ, even when the count is zero.
//
// The panel has to call all three readers on a healthy site, because "we measured and
// found nothing" and "we did not measure" are different answers and only the first one
// is a pass. A short-circuit on `count == 0` would make the second look like the
// first.
func TestACoveragePanelMeasuresEveryKindEvenWhenNothingIsWrong(t *testing.T) {
	srv := &Server{now: time.Now}
	reader := &stubAbsence{n: map[string]int{}, worst: map[string]string{}, since: map[string]time.Time{}}

	if _, err := srv.Absence(context.Background(), reader, nil, "acme", []string{"north-gate"}); err != nil {
		t.Fatalf("read the panel: %v", err)
	}
	for _, want := range []string{"vendor_feed", "ai_plane", "clock_drift"} {
		found := false
		for _, got := range reader.called {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was never read on a clean site, so a zero on this panel cannot "+
				"be told apart from a kind this build does not measure", want)
		}
	}
}

// A degraded model produces a COUNT and a REASON, and never a negative answer.
//
// This is the test that pins invariant 3 at the presentation layer. The stub answers
// with the worst case the product is designed around — seven observations, all with
// `no_runtime` as the reason — and the assertion is on the whole rendered surface: the
// kind names the plane, the count is seven, the reason survives to the view, and
// nothing anywhere produces a sentence about what was or was not seen.
func TestADegradedModelIsRenderedAsACountAndAReasonAndNeverAsAnAbsence(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	srv := &Server{now: func() time.Time { return now }}
	reader := &stubAbsence{
		n:     map[string]int{"ai_plane": 7},
		worst: map[string]string{"ai_plane": "no AI runtime is configured for this install"},
		since: map[string]time.Time{"ai_plane": now.Add(-90 * time.Minute)},
	}

	views, err := srv.Absence(context.Background(), reader, nil, "acme", []string{"north-gate"})
	if err != nil {
		t.Fatalf("read the panel: %v", err)
	}
	var plane AbsenceView
	for _, v := range views {
		if v.Kind == "ai_plane" {
			plane = v
		}
	}
	if plane.Count != 7 {
		t.Fatalf("the AI plane row has count %d, want 7", plane.Count)
	}
	if !plane.Breaking {
		t.Error("seven degraded observations are not marked breaking")
	}
	if !strings.Contains(plane.Worst, "no AI runtime") {
		t.Errorf("the reason did not survive: %q. '7 degraded' is a number; '7 degraded "+
			"because there is no runtime' is a fact an operator can act on", plane.Worst)
	}
	if got := plane.SinceAge; got != 90*time.Minute {
		t.Errorf("the age is %s, want 1h30m. An age the reader has to compute from a "+
			"timestamp is an age they get wrong", got)
	}

	// The whole rendered surface, not just the struct. These are the sentences a
	// degraded model must never produce, and they are listed because the temptation
	// to add one is exactly what this panel exists to resist.
	for _, forbidden := range []string{
		"no intrusions", "nothing detected", "no activity", "clear", "all clear",
		"no incidents", "no events",
	} {
		for _, v := range views {
			if strings.Contains(strings.ToLower(v.What), forbidden) ||
				strings.Contains(strings.ToLower(v.Worst), forbidden) {
				t.Errorf("the panel says %q, which is a claim about the world rather "+
					"than about coverage. A model that did not answer is not a model that "+
					"saw nothing", forbidden)
			}
		}
	}
}

// A principal with no site scope gets four zero rows rather than an error or a
// crash.
//
// The safe reading of a misconfigured scope is no access, and the panel has to render
// SOMETHING for it: a principal with a role and no assignment is misconfigured, and a
// page that 500s on a misconfiguration teaches an operator to avoid the page rather
// than to fix the assignment.
func TestAPrincipalWithNoSiteScopeGetsFourZeroRows(t *testing.T) {
	srv := &Server{now: time.Now}
	reader := &stubAbsence{
		n:     map[string]int{"ai_plane": 99}, // would be alarming if it leaked
		worst: map[string]string{"ai_plane": "would be alarming if it leaked"},
	}

	views, err := srv.Absence(context.Background(), reader, nil, "acme", nil)
	if err != nil {
		t.Fatalf("read the panel: %v", err)
	}
	if len(views) != 4 {
		t.Fatalf("the panel has %d rows for a principal with no scope, want 4", len(views))
	}
	for _, v := range views {
		if v.Count != 0 {
			t.Errorf("%s reported %d for a principal with no site scope. The reader "+
				"was never called, so any number here would be invented", v.Kind, v.Count)
		}
	}
}

// A read that fails fails the page rather than rendering a zero.
//
// The opposite of the row above, and it is the dangerous one. A panel that swallows a
// database error and renders four zeroes says "nothing is wrong" when the truth is
// "we could not find out", which is the exact confusion the panel exists to remove.
func TestAFailedReadIsAnErrorAndNotFourZeroes(t *testing.T) {
	srv := &Server{now: time.Now}
	reader := &stubAbsence{
		n:   map[string]int{},
		err: errors.New("the database is unreachable"),
	}

	views, err := srv.Absence(context.Background(), reader, nil, "acme", []string{"north-gate"})
	if err == nil {
		t.Fatalf("a failed read returned %d rows and no error. A panel that renders "+
			"zeros when it could not read says 'nothing is wrong' when the truth is "+
			"'we could not find out'", len(views))
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("the error does not carry what failed: %v", err)
	}
}

// A reader that is not configured is four zero rows, and says so.
//
// Different from the failure above on purpose: a build with no absence reader is a
// build that does not measure this, and "not configured" is a fact about the BUILD
// rather than a transient failure. It renders as zeroes and the console's own
// capability handling is what tells the reader the page is unconfigured.
func TestANilReaderIsFourZeroesRatherThanAPanic(t *testing.T) {
	srv := &Server{now: time.Now}
	views, err := srv.Absence(context.Background(), nil, nil, "acme", []string{"north-gate"})
	if err != nil {
		t.Fatalf("a nil reader was an error: %v. It is a build that does not measure this, "+
			"which is different from a read that failed", err)
	}
	if len(views) != 4 {
		t.Fatalf("the panel has %d rows with no reader, want 4", len(views))
	}
}

// The four kinds are declared in one place and the panel reads them from there.
//
// Not a test so much as a statement of why the list exists: a second list somewhere
// that has to agree with this one is a list that will disagree, and a panel whose
// rows differ between the query and the template is a panel that renders an empty
// column.
func TestTheKindListIsTheOneThePanelRenders(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range absenceKinds {
		if seen[k.name] {
			t.Errorf("%s appears twice in absenceKinds", k.name)
		}
		seen[k.name] = true
		if k.what == "" {
			t.Errorf("%s has no action sentence in the declaration", k.name)
		}
	}
	for _, want := range []string{"vendor_feed", "ai_plane", "terminal_failures", "clock_drift"} {
		if !seen[want] {
			t.Errorf("%s is not declared. The four rows are what an operator scans for; "+
				"a fifth kind added without a row here is invisible", want)
		}
	}
}
