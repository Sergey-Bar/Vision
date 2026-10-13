// Package simulate runs a rule bundle over a fixture set and diffs the outcome
// against the active bundle.
//
// The diff digest is the gate. Activation requires the operator to acknowledge the
// digest of the diff they reviewed, and the digest is computed over the diff
// itself rather than over the bundle - so if the fixtures or the active bundle
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

	"github.com/Sergey-Bar/vision/internal/rules"
)

// ChangeKind classifies a difference between two runs.
type ChangeKind string

// Diff kinds, most consequential first. An incident appearing or disappearing
// outranks a severity change, because that is what changes an operator's
// workload.
const (
	ChangeCreated         ChangeKind = "created"
	ChangeClosed          ChangeKind = "closed"
	ChangeSeverityChanged ChangeKind = "severity_changed"
	ChangeDegradedChanged ChangeKind = "degraded_flag_changed"
	ChangeStateChanged    ChangeKind = "verification_state_changed"
	// ChangeRuleChanged and ChangeEscalationChanged are the two differences
	// `classify` used to drop. See the note on classify for why they are
	// classified last rather than near the top.
	ChangeRuleChanged       ChangeKind = "rule_changed"
	ChangeEscalationChanged ChangeKind = "escalation_changed"
	ChangeNoChange          ChangeKind = "no_change"
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
	// ScenariosRun, MaxEvents and Truncated are recorded so a digest over a
	// truncated run cannot be mistaken for a digest over the whole fixture set.
	//
	// MaxEvents was declared here and never assigned by anything, so the field
	// read 0 in every report ever printed and promised a guarantee it did not
	// provide. It is assigned now, and a run that hit the cap additionally
	// carries a change row saying so - see DiffCapped.
	ScenariosRun int `json:"scenariosRun"`
	MaxEvents    int `json:"maxEvents"`
	// Truncated says the run stopped at MaxEvents rather than running out of
	// fixtures. It is false when the run was uncapped, which is the normal
	// case: 0 on its own is indistinguishable from "nobody said".
	Truncated   bool      `json:"truncated"`
	FixtureSet  string    `json:"fixtureSet"`
	SimulatedAt time.Time `json:"simulatedAt"`
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
//
// `Diff` is handed two complete outcome lists and truncated neither, so it makes
// no claim about a cap it was never told about: it calls DiffCapped with a cap
// of 0, which is the honest reading of "this run finished". A caller whose run
// *did* hit a cap must call DiffCapped, because Diff cannot see the difference
// between a run that stopped at the cap and one that stopped at the end of the
// fixture set - the two produce the same outcomes over the same prefix.
func Diff(bundleVersion, baselineVersion, fixtureSet string, before, after []Outcome) Report {
	return DiffCapped(bundleVersion, baselineVersion, fixtureSet, before, after, 0)
}

// DiffCapped is Diff with the cap that produced `after`, which is the one input
// a truncated run and a whole run share and a diff cannot otherwise recover.
//
// The cap reaches the digest, not just the report, because the guarantee the
// field makes is about the digest: "a digest over a truncated run cannot be
// mistaken for a digest over the whole fixture set". A truncated run's change
// list is a prefix of a whole run's, so a digest over the changes alone is
// identical, and reporting the truncation only in a field the gate does not read
// would leave the promise exactly as unkept as it was when MaxEvents was never
// assigned. So a capped run carries a change row naming the truncation, in the
// same shape the misaligned-run case above already uses, and the row is visible
// to the operator reading the report rather than hidden in a number.
func DiffCapped(bundleVersion, baselineVersion, fixtureSet string, before, after []Outcome, maxEvents int) Report {
	report := Report{
		BundleVersion:   bundleVersion,
		BaselineVersion: baselineVersion,
		FixtureSet:      fixtureSet,
		ScenariosRun:    distinctScenarios(after),
		MaxEvents:       maxEvents,
		Truncated:       maxEvents > 0 && len(after) >= maxEvents,
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
		return report.seal()
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
		return report.seal()
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
		report.Totals[totalsKey(kind)]++
	}

	return report.seal()
}

// seal is the one place a report is finished, so that sorting and the digest are
// computed over exactly the change list a caller can see - including the
// truncation row, which has to be in the list before the digest is taken or it
// does not reach the digest at all.
func (r *Report) seal() Report {
	if r.Truncated {
		r.Changes = append(r.Changes, Change{
			Kind:   ChangeStateChanged,
			Before: Outcome{State: "the whole fixture set"},
			After:  Outcome{State: fmt.Sprintf("truncated at %d outcome(s)", r.MaxEvents)},
		})
		r.Totals["truncated"]++
	}
	sort.SliceStable(r.Changes, func(i, j int) bool {
		if r.Changes[i].ScenarioID != r.Changes[j].ScenarioID {
			return r.Changes[i].ScenarioID < r.Changes[j].ScenarioID
		}
		return r.Changes[i].SignalID < r.Changes[j].SignalID
	})
	r.DiffDigest = DigestFor(r.BundleVersion, r.FixtureSet, r.Changes)
	return *r
}

func totalsKey(kind ChangeKind) string {
	switch kind {
	case ChangeCreated:
		return "created"
	case ChangeClosed:
		return "closed"
	case ChangeSeverityChanged:
		return "severityChanged"
	case ChangeDegradedChanged:
		return "degradedChanged"
	case ChangeStateChanged:
		return "stateChanged"
	case ChangeRuleChanged:
		return "ruleChanged"
	case ChangeEscalationChanged:
		return "escalationChanged"
	default:
		return string(kind)
	}
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
//
// It used to read IncidentRaised, Severity, Degraded and State, and nothing else.
// RuleID and Escalate were on the Outcome and never read, so a candidate bundle
// that swaps *which* rule raises an incident, or turns escalation on, produced
// classify == ChangeNoChange for that signal: no change row, no totals entry, and
// above all no contribution to the digest. An operator who acknowledged that
// digest had reviewed a report that could not have told them the deciding rule
// had changed underneath them. A change in rule identity is a change because the
// incident the operator triaged is now the output of a different piece of policy
// - the same incident, the same severity and the same state, decided by something
// else - and "same outcome" is not the same question as "same decision".
//
// They are classified last rather than near the top on purpose. Every difference
// above is one an operator can see in a console row; which rule fired is the
// subtlest of the seven, and a signal that changed severity *and* rule is already
// reported as a severity change. Nothing is lost by the ordering: every change row
// carries the full Before and After pair, so the rule id is on the row either
// way, and the kind names the most visible difference rather than the first one
// the comparison happens to reach.
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
	case before.RuleID != after.RuleID:
		return ChangeRuleChanged
	case before.Escalate != after.Escalate:
		return ChangeEscalationChanged
	default:
		return ChangeNoChange
	}
}

// digestOf is the diff digest.
//
// It is over the changes, over the bundle version, and over the fixture set
// name - but deliberately not over the run timestamp, because a simulation
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
