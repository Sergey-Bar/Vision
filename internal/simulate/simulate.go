// Package simulate runs a rule bundle over a fixture set and diffs the outcome
// against the active bundle.
//
// The diff digest is the gate. Activation requires the operator to acknowledge the
// digest of the diff they reviewed, and the digest is computed over the diff
// itself rather than over the bundle — so if the fixtures or the active bundle
// move after a review, the simulation produces a different digest and the
// acknowledgement no longer applies.
//
// A bundle that can be activated without this step is a bundle nobody read the
// consequences of, which is the failure mode this exists to prevent.
package simulate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// ChangeKind classifies a difference between two runs.
type ChangeKind string

const (
	ChangeCreated         ChangeKind = "created"
	ChangeClosed          ChangeKind = "closed"
	ChangeSeverityChanged ChangeKind = "severity_changed"
	ChangeDegradedChanged ChangeKind = "degraded_flag_changed"
	ChangeStateChanged    ChangeKind = "verification_state_changed"
	ChangeNoChange        ChangeKind = "no_change"
)

// Change is one difference between two runs.
type Change struct {
	Kind ChangeKind `json:"kind"`
	// ScenarioID and SignalID are the location. The before and after outcomes
	// carry their own, and these name the same thing, because a reviewer reading
	// a diff line needs the location without having to look inside both sides.
	ScenarioID string  `json:"scenarioId"`
	SignalID   string  `json:"signalId"`
	Before     Outcome `json:"before"`
	After      Outcome `json:"after"`
}

// Outcome is what one signal produced under one bundle.
type Outcome struct {
	// ScenarioID and SignalID identify what produced this outcome, so a diff
	// names a location rather than a position that moves when the fixture set
	// grows.
	ScenarioID string `json:"scenarioId"`
	SignalID   string `json:"signalId"`

	IncidentRaised bool   `json:"incidentRaised"`
	State          string `json:"state"`
	Severity       string `json:"severity,omitempty"`
	RuleID         string `json:"ruleId,omitempty"`
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degradedReason,omitempty"`
	Escalate       bool   `json:"escalate"`
}

// Report is a simulate run's result.
type Report struct {
	BundleVersion   string `json:"bundleVersion"`
	BaselineVersion string `json:"baselineVersion"`
	// DiffDigest is what activation requires. It is over the changes, so a
	// different result under the same bundle is a different digest.
	DiffDigest string         `json:"diffDigest"`
	Changes    []Change       `json:"changes"`
	Totals     map[string]int `json:"totals"`
	// ScenariosRun and MaxEvents are recorded so a digest over a truncated run
	// cannot be mistaken for a digest over the whole fixture set.
	ScenariosRun int       `json:"scenariosRun"`
	MaxEvents    int       `json:"maxEvents"`
	FixtureSet   string    `json:"fixtureSet"`
	SimulatedAt  time.Time `json:"simulatedAt"`
}

// Scenario is one named set of signals to run a bundle over.
type Scenario struct {
	ID      string
	Signals []rules.Signal
}

// Runner executes bundles over scenarios.
type Runner struct {
	// Context is supplied per run rather than stored, because a run is a
	// simulation and must not inherit a live session's schedule or clock.
	// A diff that depended on when somebody ran it would not be reviewable.
	ClockNow func() time.Time
	// SiteTimezone renders times. It cannot affect the decision.
	SiteTimezone *time.Location
	// SkewTolerance is the site's.
	SkewTolerance time.Duration
	// InSchedule resolves a schedule id against a site-local instant.
	InSchedule func(id string, at time.Time) bool
}

// Run evaluates a bundle over the scenarios.
func (r *Runner) Run(bundle *rules.CompiledBundle, scenarios []Scenario, maxEvents int) ([]Outcome, error) {
	if bundle == nil {
		return nil, fmt.Errorf("simulate: no bundle")
	}
	if r.ClockNow == nil {
		r.ClockNow = func() time.Time { return time.Now().UTC() }
	}
	if r.SiteTimezone == nil {
		r.SiteTimezone = time.UTC
	}
	if r.SkewTolerance <= 0 {
		r.SkewTolerance = time.Duration(bundle.Settings.Clock.SkewToleranceMS) * time.Millisecond
	}

	engine := rules.NewEngine(bundle, rules.Context{
		Now:           r.ClockNow(),
		InSchedule:    r.InSchedule,
		SkewTolerance: r.SkewTolerance,
		SiteTimezone:  r.SiteTimezone,
	})

	var out []Outcome
	for _, s := range scenarios {
		for i, trigger := range s.Signals {
			if maxEvents > 0 && len(out) >= maxEvents {
				return out, nil
			}
			// Every other signal in the scenario is available to corroborate the
			// one being evaluated, which is how the fixture's corroboration cases
			// exercise anything at all.
			others := make([]rules.Signal, 0, len(s.Signals)-1)
			others = append(others, s.Signals[:i]...)
			others = append(others, s.Signals[i+1:]...)

			d := engine.Resolve(engine.Evaluate(trigger, others))
			out = append(out, Outcome{
				ScenarioID:     s.ID,
				SignalID:       trigger.EventID,
				IncidentRaised: d.Action.CreateIncident,
				State:          string(d.State),
				Severity:       d.Severity,
				RuleID:         d.RuleID,
				Degraded:       d.Degraded,
				DegradedReason: string(d.DegradedReason),
				Escalate:       d.Escalate,
			})
		}
	}
	return out, nil
}

// Diff compares two runs and produces the report activation is gated on.
func Diff(bundleVersion, baselineVersion, fixtureSet string, before, after []Outcome) Report {
	report := Report{
		BundleVersion:   bundleVersion,
		BaselineVersion: baselineVersion,
		FixtureSet:      fixtureSet,
		ScenariosRun:    distinctScenarios(after),
		SimulatedAt:     time.Now().UTC(),
		Totals:          map[string]int{"scenarios": distinctScenarios(after), "outcomes": len(after)},
	}

	// With no baseline there is nothing to diff against. Saying so is the honest
	// answer; synthesising a "change" from an empty run would put a fabricated
	// line in a report an operator is about to acknowledge, and the whole point of
	// the digest is that what they read is what happens.
	if len(before) == 0 {
		report.Totals["baseline"] = 0
		report.Totals["firstBundle"] = 1
		for _, a := range after {
			report.Totals[outcomeKey(a)]++
		}
		report.DiffDigest = DigestFor(bundleVersion, fixtureSet, nil)
		return report
	}

	// The two runs must line up. A different number of results means a different
	// fixture set or a different bundle shape, and a diff over misaligned runs is
	// not a diff.
	if len(before) != len(after) {
		report.Changes = []Change{{
			Kind:   ChangeStateChanged,
			Before: Outcome{State: fmt.Sprintf("%d outcomes", len(before))},
			After:  Outcome{State: fmt.Sprintf("%d outcomes", len(after))},
		}}
		report.DiffDigest = DigestFor(bundleVersion, fixtureSet, report.Changes)
		return report
	}
	for i := range before {
		b, a := before[i], after[i]
		kind := classify(b, a)
		if kind == ChangeNoChange {
			// Unchanged outcomes are still counted, so a report says what the
			// bundle does rather than only what it changes.
			report.Totals[string(ChangeNoChange)]++
			continue
		}
		report.Changes = append(report.Changes, Change{
			Kind: kind, ScenarioID: a.ScenarioID, SignalID: a.SignalID,
			Before: b, After: a,
		})
		switch kind {
		case ChangeCreated:
			report.Totals["created"]++
		case ChangeClosed:
			report.Totals["closed"]++
		case ChangeSeverityChanged:
			report.Totals["severityChanged"]++
		case ChangeDegradedChanged:
			report.Totals["degradedChanged"]++
		case ChangeStateChanged:
			report.Totals["stateChanged"]++
		}
	}

	sort.SliceStable(report.Changes, func(i, j int) bool {
		if report.Changes[i].ScenarioID != report.Changes[j].ScenarioID {
			return report.Changes[i].ScenarioID < report.Changes[j].ScenarioID
		}
		return report.Changes[i].SignalID < report.Changes[j].SignalID
	})
	report.DiffDigest = DigestFor(bundleVersion, fixtureSet, report.Changes)
	return report
}

// outcomeKey names a totals bucket for one outcome, so a first-bundle report
// says what the bundle does rather than only that it changed nothing.
func outcomeKey(o Outcome) string {
	switch {
	case !o.IncidentRaised:
		return "noIncident"
	case o.Degraded:
		return "degradedIncident"
	default:
		return "incident:" + o.State
	}
}

func distinctScenarios(outcomes []Outcome) int {
	seen := map[string]bool{}
	for _, o := range outcomes {
		seen[o.ScenarioID] = true
	}
	return len(seen)
}

// classify names the most consequential difference, in that order. An incident
// appearing or disappearing outranks a severity change, because that is what an
// operator's workload changes by.
func classify(before, after Outcome) ChangeKind {
	switch {
	case !before.IncidentRaised && after.IncidentRaised:
		return ChangeCreated
	case before.IncidentRaised && !after.IncidentRaised:
		return ChangeClosed
	case before.Severity != after.Severity:
		return ChangeSeverityChanged
	case before.Degraded != after.Degraded:
		return ChangeDegradedChanged
	case before.State != after.State:
		return ChangeStateChanged
	default:
		return ChangeNoChange
	}
}

// digestOf is the diff digest.
//
// It is over the changes, over the bundle version, and over the fixture set
// name — but deliberately not over the run timestamp, because a simulation
// re-run to confirm a review must produce the same digest or the review cannot be
// reproduced.
func digestOf(changes []Change) string {
	type digestChange struct {
		Kind     ChangeKind `json:"kind"`
		Scenario string     `json:"scenario"`
		Signal   string     `json:"signal"`
		Before   Outcome    `json:"before"`
		After    Outcome    `json:"after"`
	}
	payload := struct {
		Changes []digestChange `json:"changes"`
	}{Changes: make([]digestChange, 0, len(changes))}
	for _, c := range changes {
		payload.Changes = append(payload.Changes, digestChange{
			Kind: c.Kind, Scenario: c.ScenarioID, Signal: c.SignalID,
			Before: c.Before, After: c.After,
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshalling a fixed struct of strings and bools cannot fail, so this
		// would be a programming error rather than a runtime condition.
		return "0000000000000000000000000000000000000000000000000000000000000000"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// DigestFor computes the digest activation checks, from the parts that identify
// the simulation rather than its moment.
func DigestFor(bundleVersion, fixtureSet string, changes []Change) string {
	sum := sha256.Sum256([]byte(bundleVersion + "\x00" + fixtureSet + "\x00" + digestOf(changes)))
	return hex.EncodeToString(sum[:])
}
