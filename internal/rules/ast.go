package rules

import (
	"fmt"
	"sort"
	"strings"
)

// CompiledBundle is a bundle that passed every gate, with its rules indexed for
// evaluation.
type CompiledBundle struct {
	Bundle
	// Compiled rules in evaluation order: priority descending, then id ascending.
	//
	// The id tiebreak is not cosmetic. Two rules with the same priority that both
	// match must produce the same outcome, or evaluation depends on the order
	// they happened to be written in — and a bundle whose behaviour changes when
	// a line is moved is a bundle nobody can review.
	Compiled []CompiledRule
	// Conflicts records rule pairs that can both match with incompatible
	// outcomes. They are a configuration error, not something resolved by
	// picking one.
	Conflicts []Conflict
	// ContentHash identifies the bytes this bundle was compiled from.
	ContentHash string
}

// CompiledRule is one rule ready to evaluate.
type CompiledRule struct {
	Rule
	// Precomputed is the evaluation form: the condition tree flattened into a
	// slice of tests, so evaluation does no reflection and no map lookups on the
	// hot path at 20 events per second.
	Precomputed CompiledCondition
	// Severity is the resolved severity, defaulted once at compile time rather
	// than at every evaluation.
	Severity string
}

// CompiledCondition is a condition tree prepared for evaluation.
type CompiledCondition struct {
	Kind CompiledKind
	// All, Any and Not hold sub-conditions.
	All []CompiledCondition
	Any []CompiledCondition
	Not *CompiledCondition
	// EventTypes is the normalised set this matches.
	EventTypes map[string]bool
	Device     *DeviceMatch
	Zone       *ZoneMatch
	Schedule   *ScheduleMatch
	Window     *TimeWindow
	Corr       *CompiledCorroboration
	Field      *FieldCondition
	Drift      *DriftCondition
	AIObs      *AIObservationCondition
}

// CompiledKind names which arm of the condition is set, so evaluation is a
// switch rather than a walk of every field.
type CompiledKind string

// Compiled condition arms. Switching on the kind is what lets evaluation avoid
// inspecting every field of every node on the hot path.
const (
	KindAll           CompiledKind = "all"
	KindAny           CompiledKind = "any"
	KindNot           CompiledKind = "not"
	KindEventType     CompiledKind = "eventType"
	KindDevice        CompiledKind = "device"
	KindZone          CompiledKind = "zone"
	KindSchedule      CompiledKind = "schedule"
	KindTimeWindow    CompiledKind = "timeWindow"
	KindCorroboration CompiledKind = "corroborate"
	KindField         CompiledKind = "field"
	KindClockDrift    CompiledKind = "clockDrift"
	KindAIObservation CompiledKind = "aiObservation"
	// KindImpossible is a condition that can never be satisfied. It exists so
	// semantic validation can find it and report it at load time, rather than it
	// quietly never matching for ever.
	KindImpossible CompiledKind = "impossible"
)

// CompiledCorroboration is a corroboration requirement ready to evaluate.
type CompiledCorroboration struct {
	EventTypes []string
	// MinSignals is how many independent signals are required, including the
	// trigger itself if it qualifies.
	MinSignals int
	MinTier    Tier
	RequiresAI bool
	// IfAIObserved is non-nil exactly when an AI observation participates. A
	// nil here with RequiresAI true is a compile error, never a runtime default.
	IfAIObserved *IfAIObserved
}

// Conflict is two rules that can both match with incompatible outcomes.
type Conflict struct {
	Rules    []string `json:"rules"`
	Priority int      `json:"priority"`
	Reason   string   `json:"reason"`
	// Resolution is never populated. An incompatible pair is a configuration
	// error an operator must resolve; the engine has no authority to choose.
	Resolution string `json:"resolution"`
}

// compileBundle builds the evaluation form of a parsed bundle.
func compileBundle(b *Bundle) *CompiledBundle {
	out := &CompiledBundle{Bundle: *b}
	for _, r := range b.Rules {
		sev := r.Then.Severity
		if sev == "" {
			sev = "medium"
		}
		out.Compiled = append(out.Compiled, CompiledRule{
			Rule:        r,
			Precomputed: compileCondition(r.When, ""),
			Severity:    sev,
		})
	}
	sort.SliceStable(out.Compiled, func(i, j int) bool {
		if out.Compiled[i].Priority != out.Compiled[j].Priority {
			return out.Compiled[i].Priority > out.Compiled[j].Priority
		}
		return out.Compiled[i].ID < out.Compiled[j].ID
	})
	out.Conflicts = findConflicts(out.Compiled)
	return out
}

func compileCondition(c Condition, path string) CompiledCondition {
	// A condition with no arm at all can never be satisfied. Compiling it to an
	// explicit impossible arm means evaluation does not have to notice, and
	// semantic validation can point at the author.
	if c.All == nil && c.Any == nil && c.Not == nil && c.EventType == nil &&
		c.Device == nil && c.Zone == nil && c.Schedule == nil &&
		c.TimeWindow == nil && c.Corroborate == nil && c.Field == nil &&
		c.ClockDrift == nil && c.AIObservation == nil {
		return CompiledCondition{Kind: KindImpossible}
	}

	// "all" and "any" are checked first: a condition with both is ambiguous, and
	// semantic validation rejects it. Compiling it as "all" here only fixes which
	// diagnostic the author sees.
	if c.All != nil {
		sub := make([]CompiledCondition, 0, len(c.All))
		for i, child := range c.All {
			sub = append(sub, compileCondition(child, fmt.Sprintf("%s.all[%d]", path, i)))
		}
		return CompiledCondition{Kind: KindAll, All: sub}
	}
	if c.Any != nil {
		sub := make([]CompiledCondition, 0, len(c.Any))
		for i, child := range c.Any {
			sub = append(sub, compileCondition(child, fmt.Sprintf("%s.any[%d]", path, i)))
		}
		return CompiledCondition{Kind: KindAny, Any: sub}
	}
	if c.Not != nil {
		sub := compileCondition(*c.Not, path+".not")
		return CompiledCondition{Kind: KindNot, Not: &sub}
	}
	if c.EventType != nil {
		set := make(map[string]bool, len(c.EventType))
		for _, t := range c.EventType {
			set[t] = true
		}
		return CompiledCondition{Kind: KindEventType, EventTypes: set}
	}
	if c.Device != nil {
		return CompiledCondition{Kind: KindDevice, Device: c.Device}
	}
	if c.Zone != nil {
		return CompiledCondition{Kind: KindZone, Zone: c.Zone}
	}
	if c.Schedule != nil {
		return CompiledCondition{Kind: KindSchedule, Schedule: c.Schedule}
	}
	if c.TimeWindow != nil {
		w := *c.TimeWindow
		return CompiledCondition{Kind: KindTimeWindow, Window: &w}
	}
	if c.Corroborate != nil {
		corr := &CompiledCorroboration{
			EventTypes: c.Corroborate.EventTypes,
			MinSignals: c.Corroborate.MinSignals,
			MinTier:    c.Corroborate.MinEvidenceTier,
			RequiresAI: c.Corroborate.RequiresAIObservation,
		}
		if corr.MinTier == "" {
			corr.MinTier = TierB
		}
		if c.Corroborate.IfAIAvailable != nil {
			corr.IfAIObserved = c.Corroborate.IfAIAvailable
		}
		return CompiledCondition{Kind: KindCorroboration, Corr: corr}
	}
	if c.Field != nil {
		return CompiledCondition{Kind: KindField, Field: c.Field}
	}
	if c.ClockDrift != nil {
		return CompiledCondition{Kind: KindClockDrift, Drift: c.ClockDrift}
	}
	if c.AIObservation != nil {
		return CompiledCondition{Kind: KindAIObservation, AIObs: c.AIObservation}
	}
	return CompiledCondition{Kind: KindImpossible}
}

// findConflicts reports rule pairs that can both match with incompatible
// outcomes.
//
// This is the deterministic conflict precedence the plan requires, and the
// interesting half is the last clause: a pair is only a conflict when the
// outcomes are incompatible AND the priorities are equal. At different
// priorities the higher one wins, which is a decision the author made
// explicitly. At equal priorities with incompatible outcomes there is no
// principled choice, so it is refused.
func findConflicts(rules []CompiledRule) []Conflict {
	var out []Conflict
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			a, b := rules[i], rules[j]
			if a.Priority != b.Priority {
				continue
			}
			reason, incompatible := incompatibleActions(a.Then, b.Then)
			if !incompatible {
				continue
			}
			out = append(out, Conflict{
				Rules:    []string{a.ID, b.ID},
				Priority: a.Priority,
				Reason:   reason,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return strings.Join(out[i].Rules, ",") < strings.Join(out[j].Rules, ",")
	})
	return out
}

// incompatibleActions reports whether two actions contradict each other.
//
// The cases that matter are the ones where following both would be meaningless:
// suppressing and creating, marking a duplicate and escalating, and creating at
// two severities that an operator would triage differently.
func incompatibleActions(a, b Action) (string, bool) {
	switch {
	case a.Suppress != b.Suppress:
		return fmt.Sprintf("one rule suppresses the incident and the other does not (suppress=%v vs %v)",
			a.Suppress, b.Suppress), true
	case a.MarkDuplicate != b.MarkDuplicate:
		return fmt.Sprintf("one rule marks the event a duplicate and the other does not (markDuplicate=%v vs %v)",
			a.MarkDuplicate, b.MarkDuplicate), true
	case a.CreateIncident && b.CreateIncident:
		ra, rb := severityRank(a.Severity), severityRank(b.Severity)
		if ra != 0 && rb != 0 && ra != rb {
			return fmt.Sprintf("both create an incident at different severities (%s vs %s)",
				a.Severity, b.Severity), true
		}
		if a.Escalate != b.Escalate {
			return fmt.Sprintf("both create an incident but disagree on escalation (escalate=%v vs %v)",
				a.Escalate, b.Escalate), true
		}
	case a.Escalate && b.Suppress:
		return "one rule escalates and the other suppresses", true
	}
	return "", false
}
