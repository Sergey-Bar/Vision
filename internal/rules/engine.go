package rules

import (
	"fmt"
	"time"
)

// Signal is one piece of evidence available to the evaluator.
//
// Everything an evaluation reads arrives here, and nothing an evaluation reads
// comes from the wall clock, the network, or the model. That is what makes an
// evaluation reproducible: given the same stored facts and the same stored
// AI observation, this returns the same outcome, which is DoD item 3.
type Signal struct {
	// EventID and SourceEventID identify the signal. EventID is the canonical
	// one; SourceEventID is kept so an explanation can name what the source
	// called it.
	EventID       string
	SourceEventID string
	EventType     string
	NormalizedAt  time.Time
	ObservedAt    time.Time
	DeviceID      string
	ZoneID        string
	DeviceKind    string
	DeviceTags    []string
	ZonePath      string
	// ClockDriftMS is the measured drift of the signal's device, already
	// applied to NormalizedAt.
	ClockDriftMS int32
	// Tier is this signal's evidence tier. A Tier C signal comes from an AI
	// observation.
	Tier Tier
	// Payload is the canonical payload, for field conditions.
	Payload map[string]any
	// AIObservation is the stored observation, when one exists for this signal.
	// Nil means absent, which is not the same as an observation saying "no".
	AIObservation *AIObservation
	// DriftedExcluded marks a signal whose device was past the severe band and is
	// therefore excluded from time-windowed corroboration.
	DriftedExcluded bool
}

// AIObservation is a stored AI observation as the evaluator sees it.
//
// Only the validated structured fields. There is deliberately no free-text
// field: a rule that could match model prose would be matching on wording that
// changes between model versions, and an incident that appears or disappears
// because a prompt was rephrased is not an incident anyone can defend.
type AIObservation struct {
	ObservationID string
	ProfileID     string
	// Structured is the validated output. Its top-level keys are the fields a
	// rule may name.
	Structured map[string]any
	// InsufficientEvidence is the model's own statement that it cannot support a
	// determination. It is not a negative answer and must not be treated as one.
	InsufficientEvidence bool
	// Degraded is set when the observation records a degradation rather than a
	// real inference. A degraded observation is not evidence at all.
	Degraded       bool
	DegradedReason string
}

// Context is everything an evaluation may read that is not a signal.
type Context struct {
	// Now is the site-local wall clock, supplied by the caller. Rules never read
	// a clock directly; a rule that did would be unreproducible on replay.
	Now time.Time
	// InSchedule reports whether the site-local time falls inside a named
	// schedule. Schedules are site data, not bundle data, so they are resolved by
	// the caller rather than compiled into the bundle.
	InSchedule func(id string, at time.Time) bool
	// SkewTolerance is the site's bound on how far apart two normalized
	// timestamps may be and still be the same moment.
	SkewTolerance time.Duration
	// SiteTimezone renders the site-local time, for schedules and for anything an
	// operator reads. Never the host's.
	SiteTimezone *time.Location
}

// Outcome is one rule's verdict.
type Outcome struct {
	RuleID   string
	Priority int
	Matched  bool
	// State is the verification state this rule produces.
	State VerificationState
	// Action is what the rule asks for.
	Action   Action
	Severity string
	// SupportingEvents are the signal ids that satisfied the rule.
	SupportingEvents []string
	// SupportingObservations are the observation ids that participated.
	SupportingObservations []string
	// ContradictingEvents are signals that contradict the rule.
	ContradictingEvents []string
	// DriftExclusions records devices excluded from a time window.
	DriftExclusions []Exclusion
	// Degraded is set when the evaluation had to proceed without a Tier C signal
	// the rule wanted. It is recorded on the outcome and, by the caller, on the
	// incident — so a pilot can measure how often the AI path was actually live
	// rather than assuming it was.
	Degraded       bool
	DegradedReason DegradedReason
	// Reason is a short machine-readable code.
	Reason string
	// Explanation is human-facing. It is never evaluated by another rule.
	Explanation string
}

// DegradedReason names why a Tier C signal was unavailable.
type DegradedReason string

const (
	DegradedNone             DegradedReason = "none"
	DegradedModelUnavailable DegradedReason = "model_unavailable"
	DegradedNoRuntime        DegradedReason = "no_runtime"
	DegradedTimeout          DegradedReason = "timeout"
	DegradedError            DegradedReason = "error"
	DegradedAbsent           DegradedReason = "absent"
)

// VerificationState is the deterministic verdict on an event.
type VerificationState string

const (
	// StateCorroborated means independent signals agreed.
	StateCorroborated VerificationState = "CORROBORATED"
	// StateUnverified means the rule matched but its corroboration was not met,
	// or was met without the AI signal it asked for.
	StateUnverified VerificationState = "UNVERIFIED"
	// StateContradicted means a signal contradicted the rule.
	StateContradicted VerificationState = "CONTRADICTED"
	// StateDuplicate means the event repeats one already accounted for.
	StateDuplicate VerificationState = "DUPLICATE"
	// StateSuppressed means a higher-priority rule suppressed it deliberately.
	StateSuppressed VerificationState = "SUPPRESSED"
)

// Exclusion records a device that was not trusted in a time window. It is
// surfaced on the outcome rather than applied silently: a corroborating signal
// that vanished without explanation is the kind of thing an operator stops
// trusting the product over.
type Exclusion struct {
	DeviceID string
	DriftMS  int32
	Reason   string
}

// Engine evaluates a compiled bundle against stored facts.
//
// It has no state, no clock of its own and no side effects. Two evaluations of
// the same signals produce the same outcomes, which is what lets a months-old
// decision be reproduced from stored facts and a stored observation.
type Engine struct {
	bundle *CompiledBundle
	ctx    Context
}

// NewEngine builds an evaluator.
func NewEngine(b *CompiledBundle, ctx Context) *Engine {
	if ctx.SkewTolerance <= 0 {
		ctx.SkewTolerance = time.Duration(b.Settings.Clock.SkewToleranceMS) * time.Millisecond
	}
	if ctx.SiteTimezone == nil {
		ctx.SiteTimezone = time.UTC
	}
	return &Engine{bundle: b, ctx: ctx}
}

// Evaluate runs every enabled rule against one trigger signal and the other
// signals available to corroborate it.
//
// Rules are evaluated in compiled order — priority descending, id ascending —
// and the first suppressing rule wins. The order is total and stable, so the
// result does not depend on how the bundle happened to be written.
//
// Unmatched rules are returned too, with Matched false. They are not decisions
// and Resolve ignores them as such, but they carry the site-wide facts the
// evaluation gathered on the way past: a device excluded from corroboration by
// clock drift, a degraded AI plane. Those are worth seeing on the days nothing
// was raised, because that is exactly when an operator asks why.
func (e *Engine) Evaluate(trigger Signal, available []Signal) []Outcome {
	var outcomes []Outcome

	for _, rule := range e.bundle.Compiled {
		if !rule.Enabled {
			continue
		}
		out := e.evaluateRule(rule, trigger, available)
		outcomes = append(outcomes, out)
		// A deliberate suppression is a decision by a higher-priority rule and
		// stops evaluation. Continuing past it would let a lower rule create an
		// incident the suppressing rule meant to prevent.
		if out.Matched && out.State == StateSuppressed {
			break
		}
	}
	return outcomes
}

// Resolve folds rule outcomes into one decision, using the deterministic
// precedence the plan specifies: hard suppression, then contradiction, then
// corroboration, then incident creation, then severity and escalation.
func (e *Engine) Resolve(outcomes []Outcome) Decision {
	d := Decision{State: StateUnverified, Reason: "no_rule_matched", DegradedReason: DegradedNone}

	// Drift exclusions are a property of the evaluation, not of any one rule's
	// decision, and they are collected even when nothing matched. An operator
	// asking "why does this camera never corroborate anything" needs the answer
	// on the days nothing was decided just as much as on the days something was.
	for _, o := range outcomes {
		d.DriftExclusions = append(d.DriftExclusions, o.DriftExclusions...)
		if o.Degraded && d.DegradedReason == DegradedNone {
			d.Degraded = true
			d.DegradedReason = o.DegradedReason
		}
	}

	for _, o := range outcomes {
		if o.State == StateSuppressed && o.Matched {
			return Decision{
				State: StateSuppressed, RuleID: o.RuleID, Reason: o.Reason,
				SupportingEvents: o.SupportingEvents,
				DriftExclusions:  d.DriftExclusions,
				Degraded:         d.Degraded,
				DegradedReason:   d.DegradedReason,
			}
		}
	}

	for _, o := range outcomes {
		if o.State == StateContradicted && o.Matched {
			return Decision{
				State: StateContradicted, RuleID: o.RuleID, Reason: o.Reason,
				ContradictingEvents: o.ContradictingEvents,
				DriftExclusions:     d.DriftExclusions,
				Degraded:            d.Degraded,
				DegradedReason:      d.DegradedReason,
			}
		}
	}

	var best *Outcome
	for i := range outcomes {
		o := outcomes[i]
		if !o.Matched || !o.Action.CreateIncident {
			continue
		}
		if best == nil || severityRank(o.Severity) > severityRank(best.Severity) {
			best = &outcomes[i]
		}
	}
	if best == nil {
		for i := range outcomes {
			if outcomes[i].Matched {
				return Decision{
					State: outcomes[i].State, RuleID: outcomes[i].RuleID, Reason: outcomes[i].Reason,
					SupportingEvents: outcomes[i].SupportingEvents,
					DriftExclusions:  d.DriftExclusions,
					Degraded:         d.Degraded || outcomes[i].Degraded,
					DegradedReason:   firstDegradedReason(d, outcomes[i]),
				}
			}
		}
		return d
	}

	state := best.State
	if state == "" {
		state = StateCorroborated
	}
	return Decision{
		State:                  state,
		RuleID:                 best.RuleID,
		Severity:               best.Severity,
		Reason:                 best.Reason,
		Action:                 best.Action,
		SupportingEvents:       best.SupportingEvents,
		SupportingObservations: best.SupportingObservations,
		DriftExclusions:        append(append([]Exclusion{}, d.DriftExclusions...), best.DriftExclusions...),
		Degraded:               d.Degraded || best.Degraded,
		DegradedReason:         firstDegradedReason(d, *best),
		Escalate:               best.Action.Escalate,
	}
}

func firstDegradedReason(fallback Decision, o Outcome) DegradedReason {
	if o.Degraded && o.DegradedReason != DegradedNone {
		return o.DegradedReason
	}
	if fallback.Degraded && fallback.DegradedReason != DegradedNone {
		return fallback.DegradedReason
	}
	return DegradedNone
}

// Decision is the folded verdict for one event.
type Decision struct {
	State                  VerificationState
	RuleID                 string
	Severity               string
	Reason                 string
	Action                 Action
	Escalate               bool
	SupportingEvents       []string
	SupportingObservations []string
	ContradictingEvents    []string
	DriftExclusions        []Exclusion
	Degraded               bool
	DegradedReason         DegradedReason
}

func (e *Engine) evaluateRule(rule CompiledRule, trigger Signal, available []Signal) Outcome {
	out := Outcome{
		RuleID:         rule.ID,
		Priority:       rule.Priority,
		Severity:       rule.Severity,
		DegradedReason: DegradedNone,
	}

	if !e.match(rule.Precomputed, trigger, available, &out) {
		return out
	}
	out.Matched = true
	out.Action = rule.Then

	if rule.Then.Suppress {
		out.State = StateSuppressed
		out.Reason = "suppressed_by_rule"
		out.Explanation = fmt.Sprintf("Rule %s suppressed this event.", rule.ID)
		return out
	}
	if rule.Then.MarkDuplicate {
		out.State = StateDuplicate
		out.Reason = "marked_duplicate"
		out.Explanation = fmt.Sprintf("Rule %s marked this event a duplicate.", rule.ID)
		return out
	}

	state, reason := e.stateFor(rule, &out)
	out.State = state
	out.Reason = reason
	if reason == "corroborated" {
		out.Explanation = fmt.Sprintf(
			"Rule %s matched and %d independent signal(s) agreed within %s of normalized time.",
			rule.ID, len(out.SupportingEvents), e.ctx.SkewTolerance)
	} else {
		out.Explanation = fmt.Sprintf("Rule %s matched but the outcome is %s: %s.",
			rule.ID, state, reason)
	}
	return out
}

// stateFor decides the verification state, which is the one place a rule's
// corroboration requirement turns into a verdict.
//
// Degradation is checked first, and deliberately so. If a Tier C signal the rule
// asked for was absent, the count of the remaining signals says nothing about
// whether the incident is corroborated — it says how many non-AI signals
// agreed. Reporting CORROBORATED there would tell an operator the evidence was
// sufficient when the rule itself said it was not.
func (e *Engine) stateFor(rule CompiledRule, out *Outcome) (VerificationState, string) {
	if !rule.Then.CreateIncident {
		return StateCorroborated, "matched_no_incident"
	}
	if out.Degraded {
		return StateUnverified, "degraded_per_rule_" + string(out.DegradedReason)
	}
	corr := findCorroboration(rule.Precomputed)
	if corr == nil {
		return StateCorroborated, "no_corroboration_required"
	}
	if len(out.SupportingEvents) >= corr.MinSignals {
		return StateCorroborated, "corroborated"
	}
	return StateUnverified, "insufficient_corroboration"
}

func findCorroboration(c CompiledCondition) *CompiledCorroboration {
	switch c.Kind {
	case KindCorroboration:
		return c.Corr
	case KindAll:
		for i := range c.All {
			if found := findCorroboration(c.All[i]); found != nil {
				return found
			}
		}
	case KindAny:
		for i := range c.Any {
			if found := findCorroboration(c.Any[i]); found != nil {
				return found
			}
		}
	case KindNot:
		if c.Not != nil {
			return findCorroboration(*c.Not)
		}
	}
	return nil
}
