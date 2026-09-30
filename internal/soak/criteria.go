// Package soak is the long-run harness the plan's Phase 5 asks for.
//
// # Why the criteria are not the harness
//
// The obvious design is a `soak.Run` that returns a pass or a fail. That design
// cannot fail in any way the author did not anticipate, because the harness is
// both the thing under test and the thing grading it - and a harness that grades
// itself is a harness whose green is a statement about the harness.
//
// So the harness **records** and the criteria **read**. Nothing in this package
// decides whether it passed; `Criteria` holds eight named predicates over a
// recorded run, and each one can be made to fail by a caller who knows what they
// are doing. That is what makes "the soak passed" a statement about the system.
//
// # The eight criteria
//
// They are the plan's, in its order, each phrased as the fact it needs rather
// than the behaviour it wants:
//
//	1  every accepted event reached a terminal state          (zero events lost)
//	2  no event produced two side effects                    (zero duplicates)
//	3  no degraded observation was read as a negative         (never fabricated)
//	4  a degraded answer degrades and a refusal does not     (the boundary)
//	5  one active run per event; a re-run supersedes         (no run pile-up)
//	6  nothing was left in a non-terminal state              (durability)
//	7  no partial evidence object was ever visible           (the store)
//	8  no clock drift was silently ignored                   (windows)
package soak

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// EventState is where one ingested event ended up.
//
// It is an explicit enum rather than a set of booleans because the interesting
// failures are combinations: "reached a terminal state" and "was not lost" are
// the same claim seen from two sides, and an enum makes it impossible to be
// terminal in one sense and lost in another.
type EventState string

const (
	// EventPending means accepted and not yet resolved. A soak that ends with
	// any pending event has lost it.
	EventPending EventState = "pending"
	// EventNormalized means accepted, normalised and stored.
	EventNormalized EventState = "normalized"
	// EventIncidental means accepted and deliberately not queried - suppressed,
	// dead-lettered by policy. Terminal, and a fact rather than a loss.
	EventIncidental EventState = "incidental"
	// EventObserved means an observation was recorded for it.
	EventObserved EventState = "observed"
	// EventDeadLettered means it reached a terminal failure state. Terminal: the
	// plan counts a dead-lettered event as reaching a terminal state, because the
	// whole promise is that nothing is silently dropped.
	EventDeadLettered EventState = "dead_lettered"
	// EventLost means accepted and never resolved. The one state that fails.
	EventLost EventState = "lost"
)

// Terminal reports whether a state is an ending rather than a stage.
func (s EventState) Terminal() bool {
	switch s {
	case EventNormalized, EventObserved, EventIncidental, EventDeadLettered:
		return true
	}
	return false
}

// Evidence is one piece of evidence a run put through the store.
type Evidence struct {
	Ref       string
	SHA256    string
	SizeBytes int64
	// Visible is whether the object was readable at its final ref. A false here
	// with a non-zero size is a partial object, which is criterion 7.
	Visible bool
}

// ObservationRecord is one thing a run did or did not learn.
type ObservationRecord struct {
	EventID        string
	ProfileID      string
	PromptVersion  string
	Degraded       bool
	DegradedReason string
	// DegradedDetail is carried because criterion 3 is about the *reason*
	// surviving to the consumer, not only the flag.
	DegradedDetail string
	// ReadAsNegative is what a downstream consumer concluded from this
	// observation. It is recorded rather than derived, because the thing under
	// test is the consumer's conclusion and a test that re-derives it is testing
	// itself.
	ReadAsNegative bool
	// Superseded and Supersedes are the two halves of criterion 5.
	ObservationID  string
	Supersedes     string
	SupersededBy   string
	EvidenceHashes []string
	InputHashes    []string
	CostUSD        float64
	LatencyMS      int64
}

// Event is one thing that entered the system.
type Event struct {
	ID       string
	CameraID string
	State    EventState
	// ClockDriftMS is what the clock policy said about this event's window. A
	// drift that was neither widened for nor refused is criterion 8.
	DriftMS       int32
	WindowStart   time.Time
	WindowEnd     time.Time
	WindowWidened bool
	// WindowRefusal names the reason the window was refused, if it was.
	WindowRefusal string
	// SideEffects counts the authoritative things this event caused - an
	// incident, a notification, a write-back. More than one is criterion 2.
	SideEffects  []string
	Observations []ObservationRecord
	Evidence     []Evidence
}

// Run is everything one soak recorded.
type Run struct {
	StartedAt time.Time
	EndedAt   time.Time
	Events    []Event
	// ProviderCalls counts the money spent. Criterion 2's real content is that
	// this does not grow with retries.
	ProviderCalls int
	// CostUSD is the total spend for the run.
	CostUSD float64
	// Kills records the kill points exercised, so criterion 6 can say which ones
	// ran rather than asserting that kills were tested.
	Kills []string
	// Notes are things a run wants recorded and no criterion reads.
	Notes []string
}

// Duration is how long the run took.
func (r *Run) Duration() time.Duration { return r.EndedAt.Sub(r.StartedAt) }

// CriterionResult is one criterion's verdict.
type CriterionResult struct {
	// ID is the criterion's stable name, so a report can be diffed between runs
	// and a gate can name the one that broke.
	ID    string
	Title string
	Pass  bool
	// Failures are the specific observations that failed it, not a count. A
	// count tells a reader something is wrong; the observations tell them what.
	Failures []string
	// NotExercised names a criterion whose condition the run never produced.
	// It is distinct from passing: a soak that never killed a process has not
	// demonstrated that killing a process is survivable.
	NotExercised []string
}

// Criteria evaluates the eight.
//
// Every predicate is a pure function of a Run, and every one of them is proven
// to be capable of failing by a test that constructs a Run violating it. A
// criterion no test can falsify is a comment.
func Criteria(r *Run) []CriterionResult {
	return []CriterionResult{
		noEventsLost(r),
		noDuplicateSideEffects(r),
		neverFabricatedANegative(r),
		degradedAndRefusedAreDifferent(r),
		oneActiveRunPerEvent(r),
		nothingLeftNonTerminal(r),
		noPartialEvidenceEverVisible(r),
		noDriftSilentlyIgnored(r),
	}
}

// noEventsLost: every accepted event reached a terminal state.
func noEventsLost(r *Run) CriterionResult {
	res := CriterionResult{ID: "no-events-lost", Title: "every accepted event reached a terminal state"}
	for _, e := range r.Events {
		if !e.State.Terminal() {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"event %s on %s ended as %q, which is not an ending", e.ID, e.CameraID, e.State))
			continue
		}
		if e.State == EventLost {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"event %s on %s was lost", e.ID, e.CameraID))
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// noDuplicateSideEffects: nothing produced two authoritative effects.
func noDuplicateSideEffects(r *Run) CriterionResult {
	res := CriterionResult{ID: "no-duplicate-side-effects", Title: "no event produced two side effects"}
	for _, e := range r.Events {
		seen := map[string]int{}
		for _, s := range e.SideEffects {
			seen[s]++
		}
		for effect, n := range seen {
			if n > 1 {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s produced %q %d times", e.ID, effect, n))
			}
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// neverFabricatedANegative: no degraded observation was read as a negative.
//
// This is the criterion the whole product rests on, and the one whose failure
// mode is invisible: everything still works, the clip is stored, the hash
// matches, the incident is raised - and the output says a site was quiet when
// nobody asked.
func neverFabricatedANegative(r *Run) CriterionResult {
	res := CriterionResult{ID: "never-fabricated-a-negative", Title: "no missing observation was read as a negative"}
	for _, e := range r.Events {
		for _, o := range e.Observations {
			if !o.Degraded {
				continue
			}
			if o.ReadAsNegative {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: a %s observation (%s) was read as a negative", e.ID, o.DegradedReason, o.ProfileID))
			}
			if strings.TrimSpace(o.DegradedReason) == "" {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: a degraded observation with no reason reached a consumer", e.ID))
			}
			if strings.TrimSpace(o.DegradedDetail) == "" {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: the reason %s carried no detail, so a consumer could not tell what happened",
					e.ID, o.DegradedReason))
			}
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// degradedAndRefusedAreDifferent: a degraded answer degrades; a refusal does not.
func degradedAndRefusedAreDifferent(r *Run) CriterionResult {
	res := CriterionResult{ID: "degraded-is-not-refused", Title: "a degradation is not a refusal, and a refusal is not a degradation"}
	for _, e := range r.Events {
		for _, o := range e.Observations {
			switch {
			case o.Degraded && len(e.SideEffects) == 0 && strings.Contains(o.DegradedReason, "refus"):
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: a refusal produced no effect at all, so an operator sees nothing for a "+
						"condition the product declined to judge", e.ID))
			case o.Degraded && o.ReadAsNegative:
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: a degradation was treated as a refusal and discarded", e.ID))
			}
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// oneActiveRunPerEvent: exactly one observation per event is active, and a
// re-run supersedes rather than duplicates.
func oneActiveRunPerEvent(r *Run) CriterionResult {
	res := CriterionResult{ID: "one-active-run-per-event", Title: "one active run per event; a re-run supersedes"}
	for _, e := range r.Events {
		byID := map[string]ObservationRecord{}
		for _, o := range e.Observations {
			if o.ObservationID == "" {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: an observation with no id cannot be superseded", e.ID))
				continue
			}
			byID[o.ObservationID] = o
		}
		for _, o := range e.Observations {
			if o.Supersedes != "" {
				if _, ok := byID[o.Supersedes]; !ok {
					res.Failures = append(res.Failures, fmt.Sprintf(
						"event %s: observation %s supersedes %s, which is not in this run", e.ID, o.ObservationID, o.Supersedes))
				}
				if byID[o.Supersedes].SupersededBy != o.ObservationID {
					res.Failures = append(res.Failures, fmt.Sprintf(
						"event %s: observation %s claims to supersede %s and the claim is not reciprocal, "+
							"so the pair cannot be read as one replacement", e.ID, o.ObservationID, o.Supersedes))
				}
			}
			if o.SupersededBy != "" {
				if _, ok := byID[o.SupersededBy]; !ok {
					res.Failures = append(res.Failures, fmt.Sprintf(
						"event %s: observation %s is superseded by %s, which is not in this run", e.ID, o.ObservationID, o.SupersededBy))
				}
			}
		}
		// One active run per (event, profile, prompt version).
		active := map[string]int{}
		for _, o := range e.Observations {
			if o.SupersededBy != "" {
				continue
			}
			active[o.ProfileID+"|"+o.PromptVersion]++
		}
		for k, n := range active {
			if n > 1 {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: %d active observations for %s", e.ID, n, k))
			}
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// nothingLeftNonTerminal: durability held, and the kill points are named.
func nothingLeftNonTerminal(r *Run) CriterionResult {
	res := CriterionResult{ID: "nothing-left-non-terminal", Title: "nothing was left in a non-terminal state"}
	for _, e := range r.Events {
		if !e.State.Terminal() {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"event %s on %s ended as %q", e.ID, e.CameraID, e.State))
		}
	}
	res.Pass = len(res.Failures) == 0
	if len(r.Kills) == 0 {
		res.NotExercised = append(res.NotExercised,
			"no process was killed, so durability was not exercised at all")
	}
	// A run that claims kills but cannot say which points did not exercise the
	// criterion it is about.
	want := []string{"mid-receive", "mid-mux", "pre-commit"}
	for _, w := range want {
		found := false
		for _, k := range r.Kills {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			res.NotExercised = append(res.NotExercised, "no kill at "+w)
		}
	}
	return res
}

// noPartialEvidenceEverVisible: criterion 7, on the store.
func noPartialEvidenceEverVisible(r *Run) CriterionResult {
	res := CriterionResult{ID: "no-partial-evidence-visible", Title: "no partial evidence object was ever visible"}
	for _, e := range r.Events {
		for _, ev := range e.Evidence {
			if ev.SizeBytes > 0 && !ev.Visible {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"event %s: evidence %s wrote %d bytes and is not readable at its ref", e.ID, ev.Ref, ev.SizeBytes))
			}
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

// noDriftSilentlyIgnored: every measured drift either widened the window or was
// refused with a reason.
func noDriftSilentlyIgnored(r *Run) CriterionResult {
	res := CriterionResult{ID: "no-drift-silently-ignored", Title: "no clock drift was silently ignored"}
	sawDrift := false
	for _, e := range r.Events {
		if e.DriftMS == 0 {
			continue
		}
		sawDrift = true
		if !e.WindowWidened && e.WindowRefusal == "" {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"event %s: a drift of %dms neither widened the window nor was refused", e.ID, e.DriftMS))
		}
		if e.WindowWidened && e.WindowRefusal != "" {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"event %s: a drift of %dms both widened the window and refused it, so the decision is ambiguous",
				e.ID, e.DriftMS))
		}
	}
	res.Pass = len(res.Failures) == 0
	if !sawDrift {
		res.NotExercised = append(res.NotExercised, "no drift was measured, so window selection was not exercised")
	}
	return res
}

// Report renders the criteria for a human, in a stable order.
//
// Failures first and the specific observation first inside each, because the
// reader of a failed soak wants the line that broke, not a score.
func Report(r *Run) string {
	criteria := Criteria(r)
	var b strings.Builder
	fmt.Fprintf(&b, "soak: %d events, %d provider calls, $%.4f, %s\n",
		len(r.Events), r.ProviderCalls, r.CostUSD, r.Duration().Round(time.Second))

	sort.SliceStable(criteria, func(i, j int) bool {
		if criteria[i].Pass != criteria[j].Pass {
			return !criteria[i].Pass
		}
		return criteria[i].ID < criteria[j].ID
	})
	passed := 0
	for _, c := range criteria {
		if c.Pass {
			passed++
			fmt.Fprintf(&b, "  PASS  %-32s %s\n", c.ID, c.Title)
			for _, n := range c.NotExercised {
				fmt.Fprintf(&b, "        not exercised: %s\n", n)
			}
			continue
		}
		fmt.Fprintf(&b, "  FAIL  %-32s %s\n", c.ID, c.Title)
		for _, f := range c.Failures {
			fmt.Fprintf(&b, "        %s\n", f)
		}
	}
	fmt.Fprintf(&b, "%d of %d criteria passed\n", passed, len(criteria))
	return b.String()
}

// Summary is the machine-readable form, for a gate rather than a person.
type Summary struct {
	ReportVersion   int               `json:"reportVersion"`
	Events          int               `json:"events"`
	ProviderCalls   int               `json:"providerCalls"`
	CostUSD         float64           `json:"costUsd"`
	DurationSeconds float64           `json:"durationSeconds"`
	Criteria        []CriterionResult `json:"criteria"`
	Passed          int               `json:"passed"`
	Total           int               `json:"total"`
	Kills           []string          `json:"killPointsExercised,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
}

// Summarise renders a run for a gate.
func Summarise(r *Run) Summary {
	c := Criteria(r)
	passed := 0
	for _, x := range c {
		if x.Pass {
			passed++
		}
	}
	return Summary{
		ReportVersion:   1,
		Events:          len(r.Events),
		ProviderCalls:   r.ProviderCalls,
		CostUSD:         r.CostUSD,
		DurationSeconds: r.Duration().Seconds(),
		Criteria:        c,
		Passed:          passed,
		Total:           len(c),
		Kills:           r.Kills,
		Notes:           r.Notes,
	}
}

// AllPassed is the gate's own question: every criterion passed.
//
// It is a function on the criteria rather than on the report string, so a caller
// cannot pass a Run that has one failed criterion and a friendly banner.
func AllPassed(s Summary) bool {
	for _, c := range s.Criteria {
		if !c.Pass {
			return false
		}
	}
	return s.Passed == s.Total && s.Total > 0
}
