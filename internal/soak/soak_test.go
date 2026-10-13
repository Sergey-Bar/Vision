package soak

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/evidence"
)

// A criterion nobody can falsify is a comment with a pass symbol next to it.
//
// Every test in this file therefore does one of two things: it constructs a Run
// that *violates* one criterion and asserts that criterion fails, or it
// constructs a healthy Run and asserts the whole thing passes. There is no test
// here that only asserts green.

func aHealthyRun() *Run {
	return &Run{
		StartedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 7, 1, 0, 10, 0, 0, time.UTC),
		Events: []Event{{
			ID: "evt-1", CameraID: "cam-001", State: EventObserved,
			Observations: []ObservationRecord{{
				EventID: "evt-1", ObservationID: "obs-1", ProfileID: "general-monitoring",
				PromptVersion: "v1", DegradedDetail: "x",
			}},
			SideEffects: []string{"incident:obs-1"},
		}},
		Kills: []string{"mid-receive", "mid-mux", "pre-commit"},
	}
}

func criterion(t *testing.T, r *Run, id string) CriterionResult {
	t.Helper()
	for _, c := range Criteria(r) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no criterion with id %q", id)
	return CriterionResult{}
}

func TestEveryCriterionHasANegativeCase(t *testing.T) {
	// Each entry constructs a Run that violates exactly one criterion. If a
	// predicate is written that cannot fail, the corresponding case here fails
	// with "the criterion passed on a run that violates it".
	cases := []struct {
		id  string
		run func() *Run
	}{
		{"no-events-lost", func() *Run {
			r := aHealthyRun()
			r.Events[0].State = EventPending
			return r
		}},
		{"no-events-lost", func() *Run {
			r := aHealthyRun()
			r.Events[0].State = EventLost
			return r
		}},
		{"no-duplicate-side-effects", func() *Run {
			r := aHealthyRun()
			r.Events[0].SideEffects = []string{"incident:obs-1", "incident:obs-1"}
			return r
		}},
		{"never-fabricated-a-negative", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations[0].Degraded = true
			r.Events[0].Observations[0].DegradedReason = "no_runtime"
			r.Events[0].Observations[0].ReadAsNegative = true
			return r
		}},
		{"never-fabricated-a-negative", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations[0].Degraded = true
			r.Events[0].Observations[0].DegradedReason = "no_runtime"
			r.Events[0].Observations[0].DegradedDetail = ""
			return r
		}},
		{"degraded-is-not-refused", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations[0].Degraded = true
			r.Events[0].Observations[0].DegradedReason = "region_refused"
			r.Events[0].Observations[0].ReadAsNegative = true
			return r
		}},
		{"one-active-run-per-event", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations = append(r.Events[0].Observations, ObservationRecord{
				EventID: "evt-1", ObservationID: "obs-2", ProfileID: "general-monitoring",
				PromptVersion: "v1",
			})
			return r
		}},
		{"one-active-run-per-event", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations[0].Supersedes = "obs-nowhere"
			return r
		}},
		{"one-active-run-per-event", func() *Run {
			r := aHealthyRun()
			r.Events[0].Observations[0].ObservationID = ""
			return r
		}},
		{"nothing-left-non-terminal", func() *Run {
			r := aHealthyRun()
			r.Events[0].State = EventPending
			return r
		}},
		{"no-partial-evidence-visible", func() *Run {
			r := aHealthyRun()
			r.Events[0].Evidence = []Evidence{{Ref: "a/b/c", SizeBytes: 1024, Visible: false}}
			return r
		}},
		{"no-drift-silently-ignored", func() *Run {
			r := aHealthyRun()
			r.Events[0].DriftMS = 5000
			return r
		}},
		{"no-drift-silently-ignored", func() *Run {
			r := aHealthyRun()
			r.Events[0].DriftMS = 5000
			r.Events[0].WindowWidened = true
			r.Events[0].WindowRefusal = "clock_drift_severe"
			return r
		}},
	}
	for _, tc := range cases {
		if c := criterion(t, tc.run(), tc.id); c.Pass {
			t.Errorf("criterion %q passed on a run that violates it", tc.id)
		}
	}
}

func TestAHealthyRunPassesEveryCriterion(t *testing.T) {
	s := Summarise(aHealthyRun())
	if !AllPassed(s) {
		t.Fatalf("a healthy run failed:\n%s", Report(aHealthyRun()))
	}
	if s.Total != 8 {
		t.Errorf("there are %d criteria; the plan publishes eight", s.Total)
	}
}

func TestAHealthWithoutFaultInjectionIsReportedAsNotExercised(t *testing.T) {
	// A green run that never killed a process has not demonstrated that killing
	// one is survivable, and saying so is the difference between a soak and a
	// test that only ran.
	r := aHealthyRun()
	r.Kills = nil
	c := criterion(t, r, "nothing-left-non-terminal")
	if c.Pass != true {
		t.Fatal("a run with no non-terminal events should pass the criterion")
	}
	if len(c.NotExercised) == 0 {
		t.Error("a run that killed nothing reported the durability criterion as fully exercised")
	}

	// And a run with no drift says so rather than claiming windows were checked.
	r2 := aHealthyRun()
	c2 := criterion(t, r2, "no-drift-silently-ignored")
	if len(c2.NotExercised) == 0 {
		t.Error("a run that measured no drift reported window selection as exercised")
	}
}

func TestAllPassedCannotBeFooledByAFriendlyReport(t *testing.T) {
	r := aHealthyRun()
	r.Events[0].State = EventLost
	s := Summarise(r)
	if AllPassed(s) {
		t.Error("AllPassed accepted a summary with a lost event")
	}
	// And a summary with no criteria at all is not a pass either.
	if AllPassed(Summary{}) {
		t.Error("AllPassed accepted an empty summary; zero criteria is not a pass")
	}
}

// TestTheRealPipelinePassesTheCriteria runs the actual runner rather than a
// hand-built record, so the criteria are checked against what the code does and
// not against what a fixture says it does.
func TestTheRealPipelinePassesTheCriteria(t *testing.T) {
	run := runOnce(t, Options{Events: 40, Cameras: 4, Retries: 3})
	s := Summarise(run)
	if !AllPassed(s) {
		t.Fatalf("the real pipeline failed its own criteria:\n%s", Report(run))
	}
	// The retry count is the point: three attempts per event must cost one call
	// per event, not three. Fifteen cents times three is the difference between a
	// product with a cost model and one without.
	wantCalls := 40
	if s.ProviderCalls != wantCalls {
		t.Errorf("the provider was called %d times for %d events with 3 attempts each; "+
			"a retry must not pay", s.ProviderCalls, wantCalls)
	}
}

// TestAnOutageIsRecordedAndRecovered: the shape of every real outage, and the
// case where getting it wrong is most expensive - the product going deaf.
func TestAnOutageIsRecordedAndRecovered(t *testing.T) {
	run := runOnce(t, Options{
		Events: 20, Cameras: 2,
		Answer: AnswerUnavailable, AnswerFrom: 10, Recovered: AnswerConfirm,
	})
	for _, c := range Criteria(run) {
		if c.ID == "never-fabricated-a-negative" && !c.Pass {
			t.Errorf("an outage produced a fabricated negative:\n%s", strings.Join(c.Failures, "\n"))
		}
	}
	degraded, answered := 0, 0
	for _, e := range run.Events {
		for _, o := range e.Observations {
			if o.Degraded {
				degraded++
			} else {
				answered++
			}
		}
	}
	if degraded == 0 {
		t.Error("the outage was not recorded as degraded")
	}
	if answered == 0 {
		t.Error("the run never recovered; a provider that came back must be asked again")
	}
}

func TestSevereDriftIsRefusedByName(t *testing.T) {
	run := runOnce(t, Options{Events: 10, Cameras: 2, DriftMS: 45000, DriftFromEvent: 5})
	refused := 0
	for _, e := range run.Events {
		if e.WindowRefusal == "clock_drift_severe" {
			refused++
		}
	}
	if refused == 0 {
		t.Error("a drift of 45 seconds neither widened a window nor refused one")
	}
	if c := criterion(t, run, "no-drift-silently-ignored"); !c.Pass {
		t.Errorf("a run with a named refusal failed the drift criterion:\n%s", strings.Join(c.Failures, "\n"))
	}
}

func TestEvidenceIsStoredAndReadableBack(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/poc/testsrc.h264")
	if err != nil {
		t.Skipf("no fixture: %v", err)
	}
	run := runOnce(t, Options{Events: 6, Cameras: 2, Fixture: fixture})
	total := 0
	for _, e := range run.Events {
		for _, ev := range e.Evidence {
			total++
			if !ev.Visible {
				t.Errorf("event %s: evidence %s was written and is not readable", e.ID, ev.Ref)
			}
		}
	}
	if total == 0 {
		t.Fatal("no evidence was stored; the store path was not exercised")
	}
}

func runOnce(t *testing.T, opts Options) *Run {
	t.Helper()
	store, err := evidence.New(evidence.Options{Root: filepath.Join(t.TempDir(), "evidence")})
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	provider := NewStub(AnswerSequence(opts.Answer, opts.AnswerFrom, opts.Recovered)...)
	return NewRunner(store, provider, opts).Run(context.Background())
}
