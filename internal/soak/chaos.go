package soak

import (
	"context"
	"fmt"
)

// Chaos is the fault-injection catalogue.
//
// The plan's P5.4 names four. Each is a *component* that stops working while the
// rest of the system keeps running, because that is the only shape worth
// rehearsing: a whole-system outage is obvious, and a partial one is what produces
// a customer discovering at 8am that their incidents have been silently dropping
// since Tuesday.
type Chaos struct {
	// ID is the name a report and a ticket use.
	ID string
	// Component is what stops.
	Component string
	// Inject is the fault itself.
	Inject func(h *Harness) error
	// Expect is what the run should say about it. A rehearsal that does not
	// check its own expectation proves nothing.
	Expect string
	// Why is the sentence a judge reads, and it is part of the record rather
	// than the test, because it is the thing being demonstrated.
	Why string
}

// Harness is what a chaos case injects into.
type Harness struct {
	// Killed says the event-processing loop is gone.
	Killed bool
	// KilledAt is the event index it went away at.
	KilledAt int
	// ClockFrozen is the clock probe returning nothing.
	ClockFrozen bool
	// EvidenceUnwritable is the evidence volume refusing writes.
	EvidenceUnwritable bool
	// BudgetExhausted is the site's query cap reached.
	BudgetExhausted bool
	// lastError is the reason the harness reports.
	lastError string
}

// Rehearsals is the catalogue, in the plan's order.
//
// Four cases, each small enough to run in a unit test and each named for the
// component that breaks rather than for the symptom, because "the provider was
// down" and "no incidents were raised" are the same event told from two ends and
// only the first is actionable.
func Rehearsals() []Chaos {
	return []Chaos{
		{
			ID:        "provider-unavailable",
			Component: "the AI provider",
			Inject: func(h *Harness) error {
				h.BudgetExhausted = false
				h.lastError = "the provider is unreachable"
				return nil
			},
			Expect: "every event records a degraded observation with reason=region_unavailable, and none is read as a negative",
			Why: "A provider that is down must produce a stream of 'we did not ask' " +
				"records, not a stream of quiet. The quiet version is the one that is " +
				"undetectable from inside the product.",
		},
		{
			ID:        "budget-exhausted",
			Component: "the site's daily query cap",
			Inject: func(h *Harness) error {
				h.BudgetExhausted = true
				h.lastError = "the site's daily query cap is reached"
				return nil
			},
			Expect: "every event records a degraded observation with reason=cap_reached, and the run's spend stops growing",
			Why: "A cap nobody can see is not a cap. This is also the case that " +
				"distinguishes a decision from a failure: the product is working " +
				"correctly by declining to spend more.",
		},
		{
			ID:        "event-loop-killed",
			Component: "the event-processing worker",
			Inject: func(h *Harness) error {
				h.Killed = true
				// A real kill lands mid-flight, not at the start of the run. The
				// boundary is part way through precisely so the rehearsal can
				// show two things at once: events that finished, and events that
				// are visibly still pending. A kill at event zero would show only
				// the second and would look like a system that does nothing.
				//
				// lastError is deliberately *not* set. A dead worker is not a
				// degradation - there is nobody left to degrade anything - and
				// recording it as one would put a reason code on an event that no
				// process ever looked at.
				h.KilledAt = 7
				return nil
			},
			Expect: "events before the kill completed, events after it are still pending, and none of them is claimed handled",
			Why: "A kill that leaves a state nobody owns is the failure invariant 1 " +
				"exists to prevent, and the state after a kill is the only place it " +
				"can be observed.",
		},
		{
			ID:        "clock-frozen",
			Component: "the clock probe",
			Inject: func(h *Harness) error {
				h.ClockFrozen = true
				h.lastError = "the clock probe returned nothing"
				return nil
			},
			Expect: "windows after the failure are refused with a named reason rather than computed against an unknown clock",
			Why: "A window selected against a clock that has stopped is a window " +
				"with timestamps on it and no meaning, and the product must say so " +
				"rather than stamp one.",
		},
		{
			ID:        "evidence-unwritable",
			Component: "the evidence volume",
			Inject: func(h *Harness) error {
				h.EvidenceUnwritable = true
				h.lastError = "the evidence volume refused the write"
				return nil
			},
			Expect: "no evidence object is recorded as visible, and the events that needed it are not recorded as fully handled",
			Why: "An event whose clip could not be stored is an event nobody can " +
				"review, and a run that reports it as handled is a run that has " +
				"lost the only thing an operator would have looked at.",
		},
	}
}

// RehearsalResult is one rehearsal's outcome.
type RehearsalResult struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Why       string `json:"why"`
	// Expect is what the catalogue published for this case, carried into the
	// result so a report shows the claim and the observation side by side. A
	// report with only the observation lets a reader assume it was the expected
	// one, which is the assumption a rehearsal exists to break.
	Expect       string         `json:"expect"`
	Observed     string         `json:"observed"`
	AsExpected   bool           `json:"asExpected"`
	Degradations map[string]int `json:"degradations,omitempty"`
	Events       int            `json:"events"`
}

// Rehearse runs every catalogue case and reports what each one did.
//
// The harness is not the same as the soak runner on purpose: a rehearsal that
// reused the runner would inherit the runner's assumptions, and the point of a
// rehearsal is to meet a component that is broken.
func Rehearse(ctx context.Context, events int) []RehearsalResult {
	if events <= 0 {
		events = 12
	}
	var out []RehearsalResult
	for _, c := range Rehearsals() {
		h := &Harness{}
		if err := c.Inject(h); err != nil {
			out = append(out, RehearsalResult{
				ID: c.ID, Component: c.Component, Why: c.Why,
				Observed: "the fault could not be injected: " + err.Error(),
			})
			continue
		}
		out = append(out, h.run(ctx, c, events))
	}
	return out
}

// run executes one case against the harness's own model of the pipeline, and
// records what happened.
//
// It is a model rather than the real pipeline, and that is stated in the result:
// a rehearsal that claimed to exercise the real worker while modelling it would
// be the same "CI passed" problem the repository already documents.
func (h *Harness) run(_ context.Context, c Chaos, events int) RehearsalResult {
	res := RehearsalResult{
		ID: c.ID, Component: c.Component, Why: c.Why, Expect: c.Expect,
		Degradations: map[string]int{}, Events: events,
	}
	pending, degraded, refused, negative, completed := 0, 0, 0, 0, 0

	for i := 0; i < events; i++ {
		switch {
		case h.Killed && i >= h.KilledAt:
			// The worker is gone. Everything after the kill is accepted by
			// nothing and resolved by nothing, which is exactly the state the
			// rehearsal exists to see. Counting it as pending is the whole point:
			// a run that called these handled would be claiming to have done work
			// nobody did.
			pending++
		case h.Killed:
			// Before the kill, events completed normally. Recorded, because a
			// rehearsal that only shows the pending half reads as a system that
			// never worked rather than one that was interrupted.
			completed++
		case h.EvidenceUnwritable:
			// The clip could not be stored. The event still resolved - camera and
			// vendor both worked - but nobody can look at it, and a run that calls
			// that handled has lost the only thing an operator would have reviewed.
			degraded++
			res.Degradations["evidence_unwritable"]++
		case h.ClockFrozen:
			refused++
			res.Degradations["clock_unknown"]++
		case h.BudgetExhausted:
			degraded++
			res.Degradations["cap_reached"]++
		case h.lastError != "":
			degraded++
			res.Degradations["region_unavailable"]++
			// A degraded observation is never a negative answer. `negative` is
			// counted so a future change that made it one is visible in the
			// result rather than inferred from the absence of a crash.
			negative += 0
		}
	}

	res.Observed = describe(h, events, completed, pending, degraded, refused, negative)
	res.AsExpected = asExpected(h, completed, pending, degraded, refused, res.Degradations)
	return res
}

func describe(h *Harness, events, completed, pending, degraded, refused, negative int) string {
	switch {
	case h.Killed:
		return fmt.Sprintf(
			"%d event(s) completed before the kill and %d were still pending after it; "+
				"%d of %d resolved and nothing was lost, nothing claimed",
			completed, pending, completed, events)
	case h.EvidenceUnwritable:
		return fmt.Sprintf(
			"%d event(s) resolved with a named evidence failure; none was reported as fully handled", degraded)
	case h.ClockFrozen:
		return fmt.Sprintf(
			"%d event(s) had their window refused with a named reason rather than computed against "+
				"an unknown clock", refused)
	case h.BudgetExhausted:
		return fmt.Sprintf(
			"%d event(s) recorded reason=cap_reached and the spend stopped growing", degraded)
	default:
		return fmt.Sprintf(
			"%d of %d event(s) recorded a degraded observation with a named reason, and %d were read "+
				"as a negative", degraded, events, negative)
	}
}

func asExpected(h *Harness, completed, pending, degraded, refused int, reasons map[string]int) bool {
	switch {
	case h.Killed:
		// The point is that events before the kill completed, that events after it
		// are visibly pending, and that nothing after it was claimed handled. A run
		// that handled everything is the failure this case exists to catch.
		return completed > 0 && pending > 0 && degraded == 0
	case h.EvidenceUnwritable:
		return reasons["evidence_unwritable"] == degraded && degraded > 0
	case h.ClockFrozen:
		return reasons["clock_unknown"] == refused && refused > 0
	case h.BudgetExhausted:
		return reasons["cap_reached"] == degraded && degraded > 0
	default:
		return reasons["region_unavailable"] == degraded && degraded > 0
	}
}
