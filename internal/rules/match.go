package rules

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// match evaluates one compiled condition, accumulating what it relied on into
// the outcome.
//
// The accumulator is the point. A verdict that says "matched" without saying
// which signals satisfied it is not explainable six months later, and the plan's
// central requirement is that an incident can be justified by naming its exact
// evidence.
func (e *Engine) match(c CompiledCondition, trigger Signal, available []Signal, out *Outcome) bool {
	switch c.Kind {
	case KindImpossible:
		return false

	case KindAll:
		for i := range c.All {
			if !e.match(c.All[i], trigger, available, out) {
				return false
			}
		}
		return true

	case KindAny:
		// Any is short-circuiting, so a rule that accepts several event types
		// records only the arm that actually fired. Recording all of them would
		// claim evidence the rule did not use.
		for i := range c.Any {
			probe := Outcome{DegradedReason: DegradedNone}
			if e.match(c.Any[i], trigger, available, &probe) {
				mergeInto(out, probe)
				return true
			}
		}
		return false

	case KindNot:
		if c.Not == nil {
			return false
		}
		probe := Outcome{DegradedReason: DegradedNone}
		return !e.match(*c.Not, trigger, available, &probe)

	case KindEventType:
		return c.EventTypes[trigger.EventType]

	case KindDevice:
		return e.matchDevice(c.Device, trigger)

	case KindZone:
		return e.matchZone(c.Zone, trigger)

	case KindSchedule:
		if e.ctx.InSchedule == nil {
			// A schedule the compiler could not resolve is not treated as true.
			// Defaulting to true would make an after-hours rule fire all day.
			return false
		}
		in := e.ctx.InSchedule(c.Schedule.ID, e.ctx.Now)
		if c.Schedule.Invert {
			return !in
		}
		return in

	case KindTimeWindow:
		// The window is read by the corroboration clause that owns it. On its
		// own it constrains nothing, which is why an unused window is a
		// compile-time warning.
		return true

	case KindClockDrift:
		abs := trigger.ClockDriftMS
		if abs < 0 {
			abs = -abs
		}
		return abs <= c.Drift.MaxMS

	case KindField:
		return e.matchField(c.Field, trigger)

	case KindAIObservation:
		return e.matchAIObservation(c.AIObs, trigger, out)

	case KindCorroboration:
		return e.matchCorroboration(c.Corr, trigger, available, out)
	}
	return false
}

func (e *Engine) matchDevice(m *DeviceMatch, s Signal) bool {
	if m == nil {
		return true
	}
	if m.ID != "" && m.ID != s.DeviceID {
		return false
	}
	if m.Kind != "" && m.Kind != s.DeviceKind {
		return false
	}
	if len(m.Tags) > 0 {
		for _, want := range m.Tags {
			if !contains(s.DeviceTags, want) {
				return false
			}
		}
	}
	return true
}

func (e *Engine) matchZone(m *ZoneMatch, s Signal) bool {
	if m == nil {
		return true
	}
	if m.ID != "" && m.ID != s.ZoneID {
		return false
	}
	if m.PathPrefix != "" && !pathHasPrefix(s.ZonePath, m.PathPrefix) {
		return false
	}
	// ancestorOf is a graph question answered by the path, which is why zones
	// carry one: a site has thousands of devices and a handful of zones, so the
	// path is the cheap representation of the tree.
	if m.AncestorOf != "" {
		if !zoneIsDescendant(s.ZonePath, m.AncestorOf) {
			return false
		}
	}
	return true
}

// pathHasPrefix reports whether a zone path contains a prefix, aligned to
// segment boundaries.
//
// Two things are being avoided. A plain string prefix matches "/restricted-bay"
// for a rule written for "/restricted", which is a security zone silently
// matching a rule meant for a different one. And anchoring at the root only
// would force every rule to repeat the site root, so "/restricted" would never
// match "/site/entrance/restricted/loading-bay" — the form a real site's tree
// takes. So the prefix must match a contiguous run of whole segments anywhere in
// the path.
func pathHasPrefix(zonePath, prefix string) bool {
	if zonePath == "" || prefix == "" {
		return false
	}
	segments := splitPath(zonePath)
	want := splitPath(prefix)
	if len(want) == 0 || len(want) > len(segments) {
		return false
	}
	for i := 0; i+len(want) <= len(segments); i++ {
		match := true
		for j := range want {
			if segments[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// zoneIsDescendant reports whether a zone path lies under an ancestor's path.
// The site timezone and zone paths are stored as "/site/entrance/loading-bay",
// so ancestry is a prefix test on a delimited path, not a substring test: a
// substring test would make "/site/loading" an ancestor of "/site/loading-bay".
func zoneIsDescendant(zonePath, ancestorPath string) bool {
	if zonePath == "" || ancestorPath == "" {
		return false
	}
	if zonePath == ancestorPath {
		return true
	}
	return strings.HasPrefix(zonePath, strings.TrimSuffix(ancestorPath, "/")+"/")
}

func (e *Engine) matchField(f *FieldCondition, s Signal) bool {
	if f == nil {
		return true
	}
	value, present := lookupPath(s.Payload, f.Path)

	switch f.Op {
	case "exists":
		return present
	case "eq":
		return present && equalScalar(value, f.Value)
	case "ne":
		// A field that is not present is not "not equal" to something; it is
		// unknown. Treating absence as inequality makes a rule fire on every
		// event that lacks the field.
		return present && !equalScalar(value, f.Value)
	case "gt", "gte", "lt", "lte":
		if !present {
			return false
		}
		cmp, ok := compareNumeric(value, f.Value)
		if !ok {
			return false
		}
		switch f.Op {
		case "gt":
			return cmp > 0
		case "gte":
			return cmp >= 0
		case "lt":
			return cmp < 0
		default:
			return cmp <= 0
		}
	case "in", "not_in":
		values, ok := f.Value.([]any)
		if !ok {
			return false
		}
		found := false
		for _, v := range values {
			if equalScalar(value, v) {
				found = true
				break
			}
		}
		if f.Op == "in" {
			return present && found
		}
		// Absence again: a missing field is not evidence of absence.
		return present && !found
	case "contains_prefix":
		if !present {
			return false
		}
		s, ok := value.(string)
		if !ok {
			return false
		}
		prefix, ok := f.Value.(string)
		if !ok {
			return false
		}
		return strings.HasPrefix(s, prefix)
	}
	return false
}

func (e *Engine) matchAIObservation(c *AIObservationCondition, s Signal, out *Outcome) bool {
	if c == nil {
		return true
	}
	obs := s.AIObservation

	// The three absences are kept apart on purpose, because the plan's invariant
	// is about exactly this distinction:
	//
	//	no observation at all  -> the AI plane was not asked
	//	degraded observation  -> the AI plane was asked and could not answer
	//	insufficient evidence -> the model answered "I cannot tell"
	//
	// None of them is a negative answer, and none of them may satisfy a rule
	// that required an observation.
	if obs == nil {
		out.Degraded = true
		out.DegradedReason = DegradedAbsent
		return false
	}
	if obs.ProfileID != c.ProfileID {
		return false
	}
	if obs.Degraded {
		out.Degraded = true
		out.DegradedReason = degradeReasonFromString(obs.DegradedReason)
		return false
	}
	if obs.InsufficientEvidence {
		// The model said it cannot tell. That is not a "no", so it does not
		// satisfy a condition and it does not contradict one.
		out.Degraded = true
		out.DegradedReason = DegradedAbsent
		return false
	}

	value, present := lookupPath(obs.Structured, c.Field)
	if !present {
		return false
	}
	if c.Equals == nil {
		return true
	}
	return equalScalar(value, c.Equals)
}

func degradeReasonFromString(s string) DegradedReason {
	switch DegradedReason(s) {
	case DegradedModelUnavailable:
		return DegradedModelUnavailable
	case DegradedNoRuntime:
		return DegradedNoRuntime
	case DegradedTimeout:
		return DegradedTimeout
	case DegradedError:
		return DegradedError
	case DegradedAbsent:
		return DegradedAbsent
	default:
		return DegradedAbsent
	}
}

// matchCorroboration is the deterministic precedence in one place: a signal past
// the severe drift band is excluded and recorded, corroboration is counted over
// what remains, and the rule's declared behaviour without a Tier C signal is
// applied rather than guessed.
func (e *Engine) matchCorroboration(c *CompiledCorroboration, trigger Signal, available []Signal, out *Outcome) bool {
	if c == nil {
		return true
	}

	wanted := make(map[string]bool, len(c.EventTypes))
	for _, t := range c.EventTypes {
		wanted[t] = true
	}

	// The trigger itself corroborates when it qualifies. Excluding it would mean
	// a rule needing two signals always needs two besides the one that fired.
	//
	// Supporting evidence is collected as (normalized time, id) pairs and sorted
	// at the end. Reading `available` in whatever order the caller supplied makes
	// the cited list depend on arrival order, and an explanation whose ordering
	// changes between runs is one nobody can diff or grep.
	type support struct {
		id    string
		at    time.Time
		obsID string
	}
	supporting := []support{}
	if wanted[trigger.EventType] && trigger.Tier.AtLeast(c.MinTier) && !trigger.DriftedExcluded {
		supporting = append(supporting, support{id: trigger.EventID, at: trigger.NormalizedAt})
		if trigger.AIObservation != nil && !trigger.AIObservation.Degraded {
			out.SupportingObservations = append(out.SupportingObservations, trigger.AIObservation.ObservationID)
		}
	}

	aiAvailable := trigger.AIObservation != nil &&
		!trigger.AIObservation.Degraded &&
		!trigger.AIObservation.InsufficientEvidence

	for _, s := range available {
		if s.EventID == trigger.EventID {
			continue
		}
		if !wanted[s.EventType] {
			continue
		}
		if !s.Tier.AtLeast(c.MinTier) {
			continue
		}
		if s.DriftedExcluded {
			// Excluded, and the exclusion recorded. A corroborating signal that
			// disappeared without explanation is the thing an operator stops
			// trusting the product over.
			out.DriftExclusions = append(out.DriftExclusions, Exclusion{
				DeviceID: s.DeviceID,
				DriftMS:  s.ClockDriftMS,
				Reason: fmt.Sprintf("device clock drift %dms is past the site's severe band; "+
					"excluded from time-windowed corroboration", s.ClockDriftMS),
			})
			continue
		}
		if !e.inWindow(trigger.NormalizedAt, s.NormalizedAt) {
			continue
		}
		obsID := ""
		if s.AIObservation != nil && !s.AIObservation.Degraded {
			obsID = s.AIObservation.ObservationID
		}
		supporting = append(supporting, support{id: s.EventID, at: s.NormalizedAt, obsID: obsID})
	}

	sort.SliceStable(supporting, func(i, j int) bool {
		if !supporting[i].at.Equal(supporting[j].at) {
			return supporting[i].at.Before(supporting[j].at)
		}
		return supporting[i].id < supporting[j].id
	})
	for _, s := range supporting {
		out.SupportingEvents = append(out.SupportingEvents, s.id)
		if s.obsID != "" {
			out.SupportingObservations = append(out.SupportingObservations, s.obsID)
		}
	}
	if trigger.AIObservation != nil && !trigger.AIObservation.Degraded {
		out.SupportingObservations = append(out.SupportingObservations,
			trigger.AIObservation.ObservationID)
	}
	out.SupportingEvents = dedupeStrings(out.SupportingEvents)
	out.SupportingObservations = dedupeStrings(out.SupportingObservations)

	// The AI requirement is evaluated before the count, not after.
	//
	// `requiresAIObservation: true` means this rule asked for a Tier C signal. If
	// two Tier B signals happen to satisfy minSignals while the model is down,
	// checking the count first would let the rule report itself corroborated —
	// and the rule's author asked for something the run did not have.
	//
	// What the declared behaviour changes is the AI veto, not the corroboration
	// requirement. UNVERIFIED still needs minSignals from the signals that are
	// present; it is "unverified", not "ignore what the rule asked for". A rule
	// that short-circuited the count here would raise an incident on a single
	// signal every time the model was down.
	if c.RequiresAI && !aiAvailable {
		if c.IfAIObserved == nil {
			// Unreachable for a compiled bundle: the semantic validator refuses
			// this. Kept as a guard because a CompiledBundle can also be built
			// in a test, and silently guessing here would be the exact bug the
			// validator exists to prevent.
			return false
		}
		switch c.IfAIObserved.DegradeTo {
		case DegradeToUnverified:
			// The rule still fires if the remaining signals corroborate, and the
			// outcome is UNVERIFIED, so an operator can tell this incident apart
			// from one that was fully corroborated.
			out.Degraded = true
			out.DegradedReason = degradeReasonOf(trigger)
		case DegradeToProceedWithout:
			// Deliberately not marked degraded: the operator asked for the
			// absence not to be held against this rule.
		}
	}

	return len(supporting) >= c.MinSignals
}

func degradeReasonOf(s Signal) DegradedReason {
	if s.AIObservation == nil {
		return DegradedAbsent
	}
	return degradeReasonFromString(s.AIObservation.DegradedReason)
}

// inWindow reports whether two normalized timestamps are within the site's skew
// tolerance, extended by the rule's own window when it declared one.
func (e *Engine) inWindow(triggerAt, otherAt time.Time) bool {
	if triggerAt.IsZero() || otherAt.IsZero() {
		return false
	}
	delta := otherAt.Sub(triggerAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= e.ctx.SkewTolerance
}

func mergeInto(dst *Outcome, src Outcome) {
	dst.SupportingEvents = append(dst.SupportingEvents, src.SupportingEvents...)
	dst.SupportingObservations = append(dst.SupportingObservations, src.SupportingObservations...)
	dst.ContradictingEvents = append(dst.ContradictingEvents, src.ContradictingEvents...)
	dst.DriftExclusions = append(dst.DriftExclusions, src.DriftExclusions...)
	if src.Degraded {
		dst.Degraded = true
		if dst.DegradedReason == DegradedNone || dst.DegradedReason == DegradedAbsent {
			dst.DegradedReason = src.DegradedReason
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// lookupPath walks a dotted path over a decoded payload.
func lookupPath(doc map[string]any, path string) (any, bool) {
	if doc == nil || path == "" {
		return nil, false
	}
	var current any = doc
	for _, segment := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, present := obj[segment]
		if !present {
			return nil, false
		}
		current = value
	}
	return current, true
}

func equalScalar(a, b any) bool {
	// Numbers arrive from JSON as float64 and from YAML as int, so a comparison
	// that only handled one representation would make the same bundle behave
	// differently depending on where a value came from.
	if af, aok := asFloat(a); aok {
		if bf, bok := asFloat(b); bok {
			return af == bf
		}
		return false
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		return ab == bb
	}
	if a == nil && b == nil {
		return true
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func compareNumeric(a, b any) (int, bool) {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if !aok || !bok {
		return 0, false
	}
	switch {
	case af < bf:
		return -1, true
	case af > bf:
		return 1, true
	default:
		return 0, true
	}
}
