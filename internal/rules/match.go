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
// eval carries the evaluation in progress: the trigger, the signals available
// to corroborate it, the outcome being accumulated, and the time window in
// force where this condition sits.
//
// The window is threaded rather than read off the corroboration clause because
// a window is a sibling of the clause it bounds, not a field of it. An `all`
// that declares both applies its window to every corroboration clause beneath
// it, which is the pairing the language documents and the one the correlator
// sizes its scan from.
type eval struct {
	trigger   Signal
	available []Signal
	out       *Outcome
	window    *TimeWindow
}

func (e *Engine) match(c CompiledCondition, trigger Signal, available []Signal, out *Outcome) bool {
	ev := &eval{trigger: trigger, available: available, out: out}
	return e.matchIn(c, ev)
}

func (e *Engine) matchIn(c CompiledCondition, ev *eval) bool {
	switch c.Kind {
	case KindImpossible:
		return false

	case KindAll:
		// A window declared among the children of this `all` bounds the
		// corroboration clauses beside it. Collected before any child runs so the
		// pairing does not depend on the order the author wrote the list in.
		inner := *ev
		for i := range c.All {
			if c.All[i].Kind == KindTimeWindow && c.All[i].Window != nil {
				inner.window = c.All[i].Window
			}
		}
		for i := range c.All {
			if !e.matchIn(c.All[i], &inner) {
				return false
			}
		}
		return true

	case KindAny:
		// Any is short-circuiting, so a rule that accepts several event types
		// records only the arm that actually fired. Recording all of them would
		// claim evidence the rule did not use.
		//
		// Degradation is the exception, and it is the exception invariant 3
		// needs. An arm that could not be evaluated is not an arm that held, and
		// `any` means "at least one of these held" — the author cannot know that
		// the satisfied arm is the right one when a sibling was unknowable. So an
		// any is degraded overall whenever any arm it actually evaluated
		// degraded, whether or not the matching arm was the one that degraded.
		// Arms after the one that fired are never evaluated, so they contribute
		// nothing: recording a degradation nobody looked for would be a guess.
		var unevaluated []Outcome
		for i := range c.Any {
			probe := Outcome{DegradedReason: DegradedNone}
			arm := *ev
			arm.out = &probe
			if e.matchIn(c.Any[i], &arm) {
				mergeInto(ev.out, probe)
				for _, p := range unevaluated {
					mergeDegraded(ev.out, p)
				}
				return true
			}
			if probe.Degraded {
				unevaluated = append(unevaluated, probe)
			}
		}
		// Nothing fired. The degradation is still a fact about the evaluation and
		// is recorded, exactly as an `all` records the degradation of a child
		// that failed.
		for _, p := range unevaluated {
			mergeDegraded(ev.out, p)
		}
		return false

	case KindNot:
		if c.Not == nil {
			return false
		}
		probe := Outcome{DegradedReason: DegradedNone}
		inner := *ev
		inner.out = &probe
		matched := e.matchIn(*c.Not, &inner)
		if probe.Degraded {
			// The negation of "we do not know" is "we do not know", not "yes".
			// A child that could not be evaluated does not satisfy its negation,
			// so the `not` fails, and the degradation is kept rather than
			// discarded with the probe: invariant 3 says a missing AI observation
			// is never a negative observation, and returning true here would
			// hand the operator a corroborated incident built from one.
			mergeDegraded(ev.out, probe)
			return false
		}
		return !matched

	case KindEventType:
		return c.EventTypes[ev.trigger.EventType]

	case KindDevice:
		return e.matchDevice(c.Device, ev.trigger)

	case KindZone:
		return e.matchZone(c.Zone, ev.trigger)

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
		// The window bounds the corroboration clause beside it; on its own it
		// constrains nothing, which is why an unused window is a compile-time
		// warning. It is threaded into the evaluation by the enclosing `all`.
		return true

	case KindClockDrift:
		return e.matchDrift(c.Drift, ev.trigger)

	case KindField:
		return e.matchField(c.Field, ev.trigger)

	case KindAIObservation:
		return e.matchAIObservation(c.AIObs, ev.trigger, ev.out)

	case KindCorroboration:
		return e.matchCorroboration(c.Corr, ev.trigger, ev.available, ev.window, ev.out)
	}
	return false
}

// matchDrift admits an event whose device drift is inside both the site's severe
// band and the rule's own bound.
//
// The two bounds are not the same test, and treating them as one was a boundary
// bug: the site's policy admits a device while `abs < SevereMs` and excludes it
// at `abs >= SevereMs`, while the rule admitted `abs <= MaxMS`. A bundle saying
// `maxMs: 10000` at a site with `driftSevereMs: 10000` therefore accepted
// exactly the devices the policy excluded, and the two disagreed silently
// because each looked self-consistent on its own.
//
// The site's severe band is authoritative. It is what the clock subsystem, the
// correlator and the operator's console already use to decide that a device is
// too far off to be trusted in a time window, and a rule is not entitled to
// declare a device usable that the site has already excluded from time-windowed
// corroboration. The rule's `maxMs` is a bound the author may tighten below the
// site policy, never loosen above it. So the effective bound is the stricter of
// the two, and both are compared at the boundary the same way.
func (e *Engine) matchDrift(d *DriftCondition, s Signal) bool {
	if d == nil {
		return true
	}
	abs := s.ClockDriftMS
	if abs < 0 {
		abs = -abs
	}
	severe := e.bundle.Settings.Clock.DriftSevereMS
	if severe > 0 && abs >= severe {
		return false
	}
	return abs <= d.MaxMS
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
		if !present {
			return false
		}
		// A type mismatch is not a comparison that succeeded either. An object
		// is genuinely not the string "open", and that is precisely why `ne`
		// over an object-valued path matched everything: equalScalar reports
		// false for an incomparable pair, and false was read as "not equal". A
		// payload field is untrusted input whose shape varies by vendor, so
		// "the vendor sent us an object where the rule expected a string" is
		// not evidence the rule author wanted.
		//
		// The decision is deliberately not to degrade. Degradation is the
		// record that a signal the rule wanted was unavailable, and a field of
		// the wrong type is available: the value is there and the rule cannot
		// use it. Silently degrading every `ne` over a variant payload would
		// make the incident queue unusable and would train operators to ignore
		// the flag. So `ne` against an incomparable type does not match, and
		// says nothing.
		return comparableScalar(value, f.Value) && !equalScalar(value, f.Value)
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
		if !present {
			// Absence is not evidence either way: a field that was not sent is
			// not a member of the list and not a non-member of it.
			return false
		}
		found := false
		anyComparable := false
		for _, v := range values {
			if !comparableScalar(value, v) {
				continue
			}
			anyComparable = true
			if equalScalar(value, v) {
				found = true
				break
			}
		}
		if f.Op == "in" {
			return found
		}
		// not_in over a value of a type the list never mentions is the same
		// mistake `ne` was making, and is refused the same way: no match, and no
		// degradation, because the field was present and unusable rather than
		// absent.
		return anyComparable && !found
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
		if prefix == "" {
			// HasPrefix(s, "") is true for every s, so an empty prefix would make
			// the condition match every present string — a rule an operator
			// believes narrows to one prefix and in fact filters on nothing but
			// presence. Rejected at compile time as well; this is the runtime
			// half, and the two are not redundant because a CompiledBundle can be
			// built without the compiler.
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
	// A profile mismatch is a fourth kind of "we do not know", and it degrades
	// like the other three. The observation stored against this event came from
	// some other profile, so the profile this rule asked was never asked: the
	// value there is an answer to a different question, and answering a
	// different question is not a negative answer to this one.
	if obs.ProfileID != c.ProfileID {
		out.Degraded = true
		out.DegradedReason = DegradedAbsent
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
		// The profile ran and answered, but the field the rule reads was not in
		// the answer. That is a missing Tier C signal, not a negative one, and it
		// is a different fact from a profile that answered "no": here the model
		// was asked and could not support a determination about this field.
		out.Degraded = true
		out.DegradedReason = DegradedAbsent
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
func (e *Engine) matchCorroboration(c *CompiledCorroboration, trigger Signal, available []Signal, window *TimeWindow, out *Outcome) bool {
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

	aiAvailable := usableAIObservation(trigger)

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
		if !e.inWindow(trigger.NormalizedAt, s.NormalizedAt, window) {
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

// usableAIObservation reports whether a signal carries an AI observation that
// can answer a question: present, not degraded, and not the model declining to
// support a determination. All three absences are the same fact to a rule — the
// Tier C signal is not available — which is invariant 3.
func usableAIObservation(s Signal) bool {
	return s.AIObservation != nil &&
		!s.AIObservation.Degraded &&
		!s.AIObservation.InsufficientEvidence
}

func degradeReasonOf(s Signal) DegradedReason {
	if s.AIObservation == nil {
		return DegradedAbsent
	}
	return degradeReasonFromString(s.AIObservation.DegradedReason)
}

// inWindow reports whether two normalized timestamps fall inside the rule's
// declared window, and inside the site's skew tolerance when the rule declared
// no window of its own.
//
// The rule chosen, and the reason for it:
//
//   - The author's window is the bound. `durationSeconds: 2` means two seconds.
//     The site-wide skew tolerance is not a substitute for it, and a rule
//     asking for 300 seconds at a site with a 5s tolerance is not silently cut
//     to 5s: it is a 300-second window, and if the site cannot produce the
//     corroboration inside it, the rule does not corroborate.
//   - The skew tolerance is how much clock disagreement is tolerated when
//     deciding whether two events are the same moment at all, so it widens the
//     author's bound by at most the tolerance. The effective window is
//     `duration + skewTolerance`, not `max(duration, skewTolerance)` and not
//     `skewTolerance` alone. It is also never narrower than the tolerance: a
//     zero-length window with a 5s tolerance still admits a signal 5s away,
//     because the site has said those are the same moment.
//   - `direction` is a signed constraint on the same bound, evaluated after it.
//     `before` keeps signals at or before the trigger, `after` at or after it.
//     The skew tolerance applies to the direction boundary too, for the same
//     reason it applies to the duration: two events the site considers
//     simultaneous have no order, and `before` must not turn that into a
//     failure. An empty direction is `either`.
//
// A window with no durationSeconds falls back to the site's skew tolerance
// alone, which is what a rule that named the window only to be read by the CLI
// meant.
func (e *Engine) inWindow(triggerAt, otherAt time.Time, w *TimeWindow) bool {
	if triggerAt.IsZero() || otherAt.IsZero() {
		return false
	}
	delta := otherAt.Sub(triggerAt)
	abs := delta
	if abs < 0 {
		abs = -abs
	}

	skew := e.ctx.SkewTolerance
	if w == nil {
		return abs <= skew
	}

	bounded := w.DurationSeconds > 0
	if bounded {
		limit := time.Duration(w.DurationSeconds)*time.Second + skew
		if abs > limit {
			return false
		}
	} else if abs > skew {
		return false
	}

	switch w.Direction {
	case "", "either":
		return true
	case "before":
		// delta <= 0, widened by the skew in the direction the site's tolerance
		// can justify.
		return delta <= skew
	case "after":
		return delta >= -skew
	}
	return true
}

// mergeDegraded copies one outcome's degradation onto another without
// carrying its evidence across.
//
// The distinction matters at every site that evaluates a condition into a
// throwaway probe: the evidence an arm used is the arm's, but a degradation is
// a fact about the evaluation as a whole, and dropping it with the probe is how
// a rule that could not ask the model reported itself fully corroborated.
func mergeDegraded(dst *Outcome, src Outcome) {
	if !src.Degraded {
		return
	}
	dst.Degraded = true
	if dst.DegradedReason == DegradedNone || dst.DegradedReason == DegradedAbsent {
		dst.DegradedReason = src.DegradedReason
	}
}

func mergeInto(dst *Outcome, src Outcome) {
	dst.SupportingEvents = append(dst.SupportingEvents, src.SupportingEvents...)
	dst.SupportingObservations = append(dst.SupportingObservations, src.SupportingObservations...)
	dst.ContradictingEvents = append(dst.ContradictingEvents, src.ContradictingEvents...)
	dst.DriftExclusions = append(dst.DriftExclusions, src.DriftExclusions...)
	mergeDegraded(dst, src)
}

// comparableScalar reports whether two scalars can be compared at all.
//
// equalScalar answers "are these equal", and answering false to an object and
// a string is a true statement about equality. It is the wrong answer to feed a
// `ne` or a `not_in`, which ask the opposite question and read false as "not
// equal, therefore match". This is the predicate that keeps the two apart: a
// pair that cannot be compared is not a comparison that succeeded, and it is
// also not a missing signal, so it degrades nothing.
func comparableScalar(a, b any) bool {
	if _, aok := asFloat(a); aok {
		_, bok := asFloat(b)
		return bok
	}
	if _, aok := a.(string); aok {
		_, bok := b.(string)
		return bok
	}
	if _, aok := a.(bool); aok {
		_, bok := b.(bool)
		return bok
	}
	return a == nil && b == nil
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
