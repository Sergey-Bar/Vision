package rules_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// This file is one test per diagnosed defect in the engine. Each fails on the
// code as it was and passes on the code as it is now, because a rule-engine fix
// with no test is a change of opinion rather than a fix.

// outcomes evaluates a bundle and returns the per-rule outcomes, which is what
// most of these tests need to assert against: a decision that folds to
// UNVERIFIED for the wrong reason is a different defect from a rule that
// matched when it should not have.
func outcomes(t *testing.T, b *rules.CompiledBundle, trigger rules.Signal, others ...rules.Signal) []rules.Outcome {
	t.Helper()
	e := rules.NewEngine(b, afterHours())
	return e.Evaluate(trigger, others)
}

func only(t *testing.T, got []rules.Outcome) rules.Outcome {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("the bundle produced %d outcomes, want 1; the fixture is wrong", len(got))
	}
	return got[0]
}

func aiField() string {
	return "aiObservation: {profileId: restricted-person, field: restrictedZoneBreach, equals: true}\n"
}

// ---------------------------------------------------------------------------
// R1. `not` over an AI condition.
// ---------------------------------------------------------------------------

// The negation of "we do not know" is "we do not know", not "yes".
//
// `not: {aiObservation: {...}}` used to evaluate its child into a throwaway
// probe and return !matched, discarding the probe. The child set Degraded and
// returned false on a missing observation, so the negation returned true on
// every event with no AI observation at all — and threw the degradation away, so
// the outcome came back CORROBORATED. The rule did the opposite of what its
// author wrote and the operator was told the incident was fully corroborated.
func TestNotOverAMissingAIObservationIsNotTrue(t *testing.T) {
	b, problems := compileRules(t, wrap(`
  - id: a.not-ai
    enabled: true
    priority: 10
    when:
      not:
        `+aiField()+`    then: {createIncident: true, severity: high}
`))
	_ = problems

	motion := signal("e1", "motion", 0)
	motion.AIObservation = nil

	got := only(t, outcomes(t, b, motion))
	if got.Matched {
		t.Error("`not` over a missing AI observation matched. The child could not be " +
			"evaluated, so its negation is unknown, not true")
	}
	if !got.Degraded {
		t.Error("the degradation was discarded with the probe, so a rule that consumed " +
			"a Tier C signal reports itself whole")
	}
}

// A `not` whose child could be evaluated still negates normally. The R1 fix must
// not turn every negation into a failure.
func TestNotOverAPresentAIObservationStillNegates(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-ai
    enabled: true
    priority: 10
    when:
      not:
        `+aiField()+`    then: {createIncident: true, severity: high}
`))

	// The model said no: the child is evaluated and returns false, so the
	// negation holds and the rule fires — un-degraded, because a real negative
	// answer is not a degradation.
	motion := signal("e1", "motion", 0)
	motion.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": false},
	}

	got := only(t, outcomes(t, b, motion))
	if !got.Matched {
		t.Fatalf("`not` over a present negative observation did not match (%s)", got.Reason)
	}
	if got.Degraded {
		t.Error("a rule that read a real negative observation reports itself degraded")
	}
}

// ---------------------------------------------------------------------------
// R2. `any` and degradation from the arm that could not be evaluated.
// ---------------------------------------------------------------------------

// `any` means "at least one of these held". An arm that could not be evaluated
// is not an arm that held, and the author cannot know that the satisfied arm is
// the right one when a sibling was unknowable — so a degraded arm degrades the
// `any` whatever fired.
func TestAnyIsDegradedWhenAnArmItEvaluatedDegraded(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.any-ai-or-field
    enabled: true
    priority: 10
    when:
      any:
        - `+aiField()+`        - field: {path: vendor.Type, op: eq, value: motion}
    then: {createIncident: true, severity: high}
`))

	motion := signal("e1", "motion", 0)
	motion.AIObservation = nil

	got := only(t, outcomes(t, b, motion))
	if !got.Matched {
		t.Fatal("the field arm did not fire; the fixture is wrong")
	}
	if !got.Degraded {
		t.Error("the `any` matched on the field arm with the AI plane down and reported " +
			"itself whole. The AI arm degraded and its degradation was thrown away")
	}
	if got.State == rules.StateCorroborated {
		t.Error("a decision that consumed a Tier C signal without one is CORROBORATED")
	}
}

// The arms after the one that fired were never evaluated, so they contribute no
// degradation. Recording one would be a guess about evidence nobody looked for.
func TestAnyDoesNotDegradeForAnArmItNeverEvaluated(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.any-field-then-ai
    enabled: true
    priority: 10
    when:
      any:
        - field: {path: vendor.Type, op: eq, value: motion}
        - `+aiField()+`    then: {createIncident: true, severity: high}
`))

	motion := signal("e1", "motion", 0)
	motion.AIObservation = nil

	got := only(t, outcomes(t, b, motion))
	if !got.Matched {
		t.Fatal("the field arm did not fire; the fixture is wrong")
	}
	if got.Degraded {
		t.Error("the `any` is degraded by an arm it never reached; the short-circuit " +
			"means that arm was never evaluated")
	}
}

// ---------------------------------------------------------------------------
// R3. One definition of "this clause consumes a Tier C signal".
// ---------------------------------------------------------------------------

// The validator and the engine disagreed about what consumes a Tier C signal, so
// a bundle could declare an UNVERIFIED fallback the engine never consulted.
//
// `minEvidenceTier: C` means a Tier C signal participates. That clause used to
// satisfy the validator's `ifAIAvailable` requirement while leaving the compiled
// rule's RequiresAI false, which switched the AI veto off at evaluation time:
// the AI plane was entirely uninvolved and the declared fallback was dead.
func TestMinEvidenceTierCActivatesTheDeclaredFallback(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.tier-c-without-requires-flag
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [motion]
            minSignals: 1
            minEvidenceTier: C
            ifAIAvailable: {degradeTo: UNVERIFIED}
    then: {createIncident: true, severity: high}
`))

	motion := signal("e1", "motion", 0)
	motion.AIObservation = nil

	got := only(t, outcomes(t, b, motion))
	if !got.Matched {
		t.Fatalf("the rule did not match (%s); the fixture is wrong", got.Reason)
	}
	if got.State == rules.StateCorroborated {
		t.Error("a clause requiring Tier C evidence reported CORROBORATED with the AI " +
			"plane down. The declared UNVERIFIED fallback was dead configuration")
	}
	if !got.Degraded {
		t.Error("the outcome is not marked degraded; a pilot could not measure how often " +
			"the AI path was live")
	}
}

// The same clause with a real Tier C observation still corroborates, so the R3
// fix did not simply make every Tier C rule UNVERIFIED.
func TestMinEvidenceTierCCorroboratesWithAnObservation(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.tier-c-without-requires-flag
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [motion]
            minSignals: 1
            minEvidenceTier: C
            ifAIAvailable: {degradeTo: UNVERIFIED}
    then: {createIncident: true, severity: high}
`))

	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}
	motion := signal("e1", "motion", 0)
	motion.AIObservation = obs

	got := only(t, outcomes(t, b, motion))
	if got.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", got.State, got.Reason)
	}
	if got.Degraded {
		t.Error("a decision made with a real observation reports itself degraded")
	}
}

// ---------------------------------------------------------------------------
// R4. The time window is enforced.
// ---------------------------------------------------------------------------

// The window the author wrote is the bound, and the site's skew tolerance widens
// it by at most the tolerance. A 300-second window at a site with a 5s tolerance
// used to be silently cut to 5s, so signals a minute apart could never
// corroborate no matter what the bundle asked for — and the correlator had sized
// a 300-second database scan to find them.
func TestALongWindowIsNotSilentlyCutToTheSkewTolerance(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.long-window
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 300}
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 2
            minEvidenceTier: B
    then: {createIncident: true, severity: high}
`))

	// Sixty seconds apart, inside the author's 300s, far outside the site's 5s
	// skew tolerance.
	motion := signal("e1", "motion", 0)
	other := signal("e2", "analytics", 60000)
	other.Tier = rules.TierB

	got := only(t, outcomes(t, b, motion, other))
	if !got.Matched {
		t.Fatalf("a signal 60s away did not corroborate inside a 300s window (%s). "+
			"durationSeconds was not enforced; the site tolerance was the only bound", got.Reason)
	}
	if got.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", got.State, got.Reason)
	}
}

// The window is a bound, so a signal past it does not corroborate however close
// the site tolerance is.
func TestASignalOutsideTheWindowDoesNotCorroborate(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.short-window
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 2}
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 2
            minEvidenceTier: B
    then: {createIncident: true, severity: high}
`))

	// Eight seconds: inside the 5s skew tolerance alone, past the 2s window
	// widened by it.
	motion := signal("e1", "motion", 0)
	other := signal("e2", "analytics", 8000)
	other.Tier = rules.TierB

	got := only(t, outcomes(t, b, motion, other))
	if got.Matched {
		t.Errorf("a signal 8s away corroborated a rule that asked for a 2s window: %v",
			got.SupportingEvents)
	}
}

// `direction` is a signed constraint on the same bound. It used to behave as
// `either` whatever it said.
func TestWindowDirectionIsEnforced(t *testing.T) {
	const clause = `
  - id: a.directional
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 300, direction: %s}
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 2
            minEvidenceTier: B
    then: {createIncident: true, severity: high}
`

	run := func(direction string, offsetMS int64) bool {
		t.Helper()
		b, _ := compileRules(t, wrap(fmt.Sprintf(clause, direction)))
		motion := signal("e1", "motion", 0)
		other := signal("e2", "analytics", offsetMS)
		other.Tier = rules.TierB
		return only(t, outcomes(t, b, motion, other)).Matched
	}

	if !run("before", -60000) {
		t.Error(`direction: before rejected a signal 60s earlier than the trigger`)
	}
	if run("before", 60000) {
		t.Error(`direction: before accepted a signal 60s later than the trigger`)
	}
	if !run("after", 60000) {
		t.Error(`direction: after rejected a signal 60s later than the trigger`)
	}
	if run("after", -60000) {
		t.Error(`direction: after accepted a signal 60s earlier than the trigger`)
	}
	if !run("either", -60000) || !run("either", 60000) {
		t.Error(`direction: either rejected a signal on one side of the trigger`)
	}
}

// A window the site cannot resolve a direction for is not a failure: the skew
// tolerance is what makes two events "the same moment", and simultaneous events
// have no order.
func TestDirectionBoundaryIsWidenedByTheSkewTolerance(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.directional
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 300, direction: before}
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 2
            minEvidenceTier: B
    then: {createIncident: true, severity: high}
`))

	// Two seconds late, inside the site's 5s tolerance: the site says these are
	// the same moment, and `before` must not turn that into a failure.
	motion := signal("e1", "motion", 0)
	other := signal("e2", "analytics", 2000)
	other.Tier = rules.TierB

	if !only(t, outcomes(t, b, motion, other)).Matched {
		t.Error("`before` rejected a signal 2s later than the trigger, inside the site's " +
			"5s tolerance")
	}
}

// ---------------------------------------------------------------------------
// R5. `ne` and `not_in` over a type the rule never mentions.
// ---------------------------------------------------------------------------

// A type mismatch is not a comparison that succeeded.
//
// equalScalar answers "are these equal", and false is the correct answer for an
// object and a string. `ne` and `not_in` ask the opposite question and read that
// false as "not equal, therefore match", so `{op: ne, value: "open"}` fired on
// every event whose `state` was an object. A payload is vendor-shaped and
// untrusted, so this fires on whatever a site happens to send.
func TestNeOverAnIncomparableTypeDoesNotMatch(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-open
    enabled: true
    priority: 10
    when:
      field: {path: state, op: ne, value: "open"}
    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "device_state", 0)
	event.Payload = map[string]any{"state": map[string]any{"open": true}}

	got := only(t, outcomes(t, b, event))
	if got.Matched {
		t.Error("`ne` matched an object against a string. A type mismatch is not a " +
			"comparison that succeeded")
	}
	if got.Degraded {
		t.Error("a field of an unexpected type degraded the decision. The value is " +
			"present and unusable, which is not a missing signal")
	}
}

// The same rule still fires on a string that genuinely differs, so the fix did
// not turn `ne` off.
func TestNeOverAComparableStringStillMatches(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-open
    enabled: true
    priority: 10
    when:
      field: {path: state, op: ne, value: "open"}
    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "device_state", 0)
	event.Payload = map[string]any{"state": "closed"}

	if !only(t, outcomes(t, b, event)).Matched {
		t.Error("ne did not match \"closed\" against \"open\"")
	}
	event.Payload = map[string]any{"state": "open"}
	if only(t, outcomes(t, b, event)).Matched {
		t.Error("ne matched \"open\" against \"open\"")
	}
}

func TestNotInOverAnIncomparableTypeDoesNotMatch(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-in
    enabled: true
    priority: 10
    when:
      field: {path: state, op: not_in, value: ["open", "closed"]}
    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "device_state", 0)
	event.Payload = map[string]any{"state": []any{"open"}}

	got := only(t, outcomes(t, b, event))
	if got.Matched {
		t.Error("`not_in` matched an array against a list of strings")
	}
	if got.Degraded {
		t.Error("an incomparable value degraded the decision")
	}
}

// A field the vendor did not send is still not evidence either way, for `ne` and
// `not_in` alike.
func TestNeOverAMissingFieldIsNotInequality(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-open
    enabled: true
    priority: 10
    when:
      field: {path: state, op: ne, value: "open"}
    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "device_state", 0)
	event.Payload = map[string]any{}

	if only(t, outcomes(t, b, event)).Matched {
		t.Error("`ne` matched an event with no state field at all")
	}
}

// ---------------------------------------------------------------------------
// R6. `contains_prefix` with an empty value.
// ---------------------------------------------------------------------------

// HasPrefix(s, "") is true for every s, so an empty prefix made the condition
// match every present string. It is refused at compile time, where the author
// sees it with a file location.
func TestContainsPrefixWithAnEmptyValueIsRejected(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.empty-prefix
    enabled: true
    priority: 10
    when:
      field: {path: vendor.Message, op: contains_prefix, value: ""}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "field_value_empty_prefix") {
		t.Errorf("an empty prefix was accepted: %+v", problems)
	}
}

// And refused at evaluation, which is not redundant: a CompiledBundle can be
// built without the compiler, and the test below builds one.
func TestContainsPrefixWithAnEmptyValueDoesNotMatch(t *testing.T) {
	b := &rules.CompiledBundle{Compiled: []rules.CompiledRule{{
		Rule: rules.Rule{ID: "empty-prefix", Enabled: true},
		Precomputed: rules.CompiledCondition{
			Kind:  rules.KindField,
			Field: &rules.FieldCondition{Path: "vendor.Message", Op: "contains_prefix", Value: ""},
		},
	}}}

	event := signal("e1", "device_state", 0)
	if only(t, outcomes(t, b, event)).Matched {
		t.Error("an empty prefix matched every present string")
	}
}

// A real prefix is unaffected.
func TestContainsPrefixWithAPrefixStillMatches(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.prefix
    enabled: true
    priority: 10
    when:
      field: {path: vendor.Message, op: contains_prefix, value: "maint"}
    then: {createIncident: true}
`))

	event := signal("e1", "device_state", 0)
	event.Payload = map[string]any{"vendor": map[string]any{"Message": "maintenance mode"}}
	if !only(t, outcomes(t, b, event)).Matched {
		t.Error("a real prefix did not match")
	}
}

// ---------------------------------------------------------------------------
// R7. The two AI failure paths that returned false un-degraded.
// ---------------------------------------------------------------------------

// An observation from a different profile is not an answer to this question: the
// profile the rule asked was never asked. That is the same fact as no
// observation at all, and it used to leave the outcome eligible for CORROBORATED.
func TestAnObservationFromAnotherProfileDegrades(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.ai
    enabled: true
    priority: 10
    when:
      `+aiField()+`    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "motion", 0)
	event.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "vehicle-at-gate",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	got := only(t, outcomes(t, b, event))
	if got.Matched {
		t.Error("a rule read an observation from a profile it did not name")
	}
	if !got.Degraded {
		t.Error("a profile mismatch is a missing Tier C signal and did not degrade the " +
			"outcome; a clause that failed this way stayed eligible for CORROBORATED")
	}
}

// The profile ran and answered, but the field the rule reads was not in the
// answer. That is a missing Tier C signal too, and a different fact from the
// profile answering "no".
func TestAMissingStructuredFieldDegrades(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.ai
    enabled: true
    priority: 10
    when:
      `+aiField()+`    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "motion", 0)
	event.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"confidence": 0.9},
	}

	got := only(t, outcomes(t, b, event))
	if got.Matched {
		t.Error("the rule matched on an observation that does not carry its field")
	}
	if !got.Degraded {
		t.Error("a missing structured field is a missing Tier C signal and did not " +
			"degrade the outcome")
	}
}

// A present field that compares false is the one real negative answer, and it
// must not degrade.
func TestANegativeAnswerIsNotADegradation(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.ai
    enabled: true
    priority: 10
    when:
      `+aiField()+`    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "motion", 0)
	event.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": false},
	}

	got := only(t, outcomes(t, b, event))
	if got.Matched {
		t.Error("the rule matched on a negative answer")
	}
	if got.Degraded {
		t.Error("a real negative observation is not a degradation")
	}
}

// ---------------------------------------------------------------------------
// R8. Which corroboration clause governs the decision.
// ---------------------------------------------------------------------------

// The clause that governs a decision is the one on the path that matched.
// `any: [field, corroborate{minSignals: 3}]` used to match on the field arm and
// then compare the supporting count against a clause that had never run, so the
// rule was permanently demoted to UNVERIFIED with zero supporting events.
func TestACorroborationClauseInAnUnfiredAnyArmDoesNotDemote(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.any-with-corroboration
    enabled: true
    priority: 10
    when:
      any:
        - field: {path: vendor.Type, op: eq, value: motion}
        - corroborate:
            eventTypes: [analytics]
            minSignals: 3
    then: {createIncident: true, severity: high}
`))

	motion := signal("e1", "motion", 0)
	got := only(t, outcomes(t, b, motion))
	if !got.Matched {
		t.Fatal("the field arm did not fire; the fixture is wrong")
	}
	if got.State == rules.StateUnverified {
		t.Errorf("the rule is permanently demoted (%s) by a corroboration clause in an "+
			"arm that never fired", got.Reason)
	}
	if got.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", got.State, got.Reason)
	}
}

// `not: {corroborate: {...}}` matched *because* corroboration was absent. The
// search used to descend into the `not` and return the very clause the rule had
// just negated, so the rule could never be corroborated, by construction.
func TestACorroborationClauseUnderNotDoesNotDemote(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.not-corroborated
    enabled: true
    priority: 10
    when:
      not:
        corroborate:
          eventTypes: [analytics]
          minSignals: 2
    then: {createIncident: true, severity: high}
`))

	// No analytics signal at all, so the clause does not hold and the negation
	// fires. That is the rule's whole intent.
	got := only(t, outcomes(t, b, signal("e1", "motion", 0)))
	if !got.Matched {
		t.Fatalf("the rule did not match (%s); the fixture is wrong", got.Reason)
	}
	if got.State == rules.StateUnverified {
		t.Errorf("a rule that fires when corroboration is absent is demoted (%s) by the "+
			"clause it negated", got.Reason)
	}
}

// A clause on the path that matched still governs, so the fix did not turn
// corroboration into a no-op.
func TestACorroborationClauseOnTheMatchedPathStillGoverns(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.corroborated
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [analytics]
            minSignals: 2
    then: {createIncident: true, severity: high}
`))

	// The trigger alone is one signal; the clause needs two.
	got := only(t, outcomes(t, b, signal("e1", "motion", 0)))
	if got.Matched {
		t.Error("the rule matched with one signal where it asked for two")
	}
	if got.State == rules.StateCorroborated {
		t.Error("a rule whose corroboration was not met reported CORROBORATED")
	}
}

// ---------------------------------------------------------------------------
// R9. Resolve ranks by priority first, severity second.
// ---------------------------------------------------------------------------

// The AST states the contract: at different priorities the higher one wins.
// Resolve used to rank by severity alone, so a low-priority critical rule beat a
// high-priority low one and the author's stated precedence was ignored.
func TestResolveRanksByPriorityBeforeSeverity(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.high-priority-low-severity
    enabled: true
    priority: 900
    when: {eventType: [motion]}
    then: {createIncident: true, severity: low, title: high priority}
  - id: a.low-priority-critical
    enabled: true
    priority: 100
    when: {eventType: [motion]}
    then: {createIncident: true, severity: critical, title: low priority}
`))

	e := rules.NewEngine(b, afterHours())
	d := e.Resolve(e.Evaluate(signal("e1", "motion", 0), nil))

	if d.RuleID != "a.high-priority-low-severity" {
		t.Errorf("rule %q decided the event, want the higher-priority one. Severity "+
			"was the only key, so a low-priority critical rule outranked it", d.RuleID)
	}
	if d.Severity != "low" {
		t.Errorf("severity is %q, want low", d.Severity)
	}
}

// Severity is still the tiebreak at equal priority. The compiler refuses such a
// pair as a conflict, so the tiebreak is only reachable on a bundle assembled
// without it — which is a shape a caller can produce, and the fold must not
// then pick by document order.
func TestResolveUsesSeverityAsTheTiebreak(t *testing.T) {
	rule := func(id, severity string) rules.CompiledRule {
		return rules.CompiledRule{
			Rule: rules.Rule{
				ID: id, Enabled: true, Priority: 900,
				Then: rules.Action{CreateIncident: true, Severity: severity},
			},
			Precomputed: rules.CompiledCondition{
				Kind:       rules.KindEventType,
				EventTypes: map[string]bool{"motion": true},
			},
			Severity: severity,
		}
	}
	b := &rules.CompiledBundle{Compiled: []rules.CompiledRule{
		rule("a.both", "low"), rule("z.both", "critical"),
	}}

	e := rules.NewEngine(b, afterHours())
	d := e.Resolve(e.Evaluate(signal("e1", "motion", 0), nil))

	if d.RuleID != "z.both" || d.Severity != "critical" {
		t.Errorf("rule %q at %q decided it; at equal priority the higher severity wins",
			d.RuleID, d.Severity)
	}
}

// ---------------------------------------------------------------------------
// R10. Drift exclusions were stored twice.
// ---------------------------------------------------------------------------

// Every outcome's exclusions are collected before the fold, so the best
// outcome's were already there. Appending them again doubled the count, and the
// count is the number an operator reads to decide whether a site has a clock
// problem.
func TestDriftExclusionsAreNotCountedTwice(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.corroborated
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [motion, analytics, access.granted]
            minSignals: 2
    then: {createIncident: true, severity: high}
`))

	trigger := signal("e1", "motion", 0)
	clean := signal("e2", "analytics", 1000)
	drifted := signal("e3", "access.granted", 40000)
	drifted.Tier = rules.TierB
	drifted.DeviceID = "cam-drifted"
	drifted.ClockDriftMS = 40000
	drifted.DriftedExcluded = true

	e := rules.NewEngine(b, afterHours())
	d := e.Resolve(e.Evaluate(trigger, []rules.Signal{clean, drifted}))

	if len(d.DriftExclusions) != 1 {
		t.Errorf("the decision carries %d drift exclusions, want 1: %+v",
			len(d.DriftExclusions), d.DriftExclusions)
	}
	if d.DriftExclusions[0].DeviceID != "cam-drifted" {
		t.Errorf("the exclusion names %q, want cam-drifted", d.DriftExclusions[0].DeviceID)
	}
}

// The exclusion is still recorded on the decision, not swallowed by the fix.
func TestDriftExclusionsAreStillRecorded(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.corroborated
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - corroborate:
            eventTypes: [motion, analytics]
            minSignals: 2
    then: {createIncident: true, severity: high}
`))

	trigger := signal("e1", "motion", 0)
	drifted := signal("e2", "analytics", 40000)
	drifted.Tier = rules.TierB
	drifted.ClockDriftMS = 40000
	drifted.DriftedExcluded = true

	e := rules.NewEngine(b, afterHours())
	d := e.Resolve(e.Evaluate(trigger, []rules.Signal{drifted}))

	if len(d.DriftExclusions) == 0 {
		t.Error("a corroborating signal was dropped without explanation")
	}
}

// ---------------------------------------------------------------------------
// R11. then.requireAIObservation is read by nothing.
// ---------------------------------------------------------------------------

// It is in the published schema, printed by the CLI, and was enforced nowhere.
// It is enforced now: an outcome produced without a usable observation is
// UNVERIFIED rather than CORROBORATED.
func TestActionRequireAIObservationIsEnforced(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.action-requires-ai
    enabled: true
    priority: 10
    when: {eventType: [motion]}
    then: {createIncident: true, severity: high, requireAIObservation: true}
`))

	got := only(t, outcomes(t, b, signal("e1", "motion", 0)))
	if !got.Matched {
		t.Fatalf("the rule did not match (%s); the fixture is wrong", got.Reason)
	}
	if got.State == rules.StateCorroborated {
		t.Error("an action declared requireAIObservation and reported CORROBORATED with " +
			"no observation at all; the field was read by nothing")
	}
	if got.State != rules.StateUnverified {
		t.Errorf("state is %s, want UNVERIFIED", got.State)
	}
}

// With an observation present the same rule is not held back, so the
// enforcement is not a permanent UNVERIFIED.
func TestActionRequireAIObservationPassesWithAnObservation(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.action-requires-ai
    enabled: true
    priority: 10
    when: {eventType: [motion]}
    then: {createIncident: true, severity: high, requireAIObservation: true}
`))

	event := signal("e1", "motion", 0)
	event.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	got := only(t, outcomes(t, b, event))
	if got.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", got.State, got.Reason)
	}
	if got.Degraded {
		t.Error("a decision made with a usable observation reports itself degraded")
	}
}

// ---------------------------------------------------------------------------
// R12. The rule's drift bound and the site's severe band.
// ---------------------------------------------------------------------------

// `Policy.Band` excludes a device at `abs >= SevereMs`; the rule's clockDrift
// admitted `abs <= MaxMS`. A bundle saying `maxMs: 10000` at a site with
// `driftSevereMs: 10000` therefore accepted exactly the devices the policy
// excludes, and neither side could tell from reading itself.
//
// The site's severe band is authoritative: a rule may tighten a bound below it
// but not loosen it above.
func TestADeviceAtTheSevereBandIsNotAdmittedByAEqualRuleBound(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.drift-bound
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - clockDrift: {maxMs: 10000}
    then: {createIncident: true, severity: high}
`))

	// Exactly at the severe band, where the site's policy excludes.
	atBand := signal("e1", "motion", 0)
	atBand.ClockDriftMS = 10000
	if only(t, outcomes(t, b, atBand)).Matched {
		t.Error("a device at the site's severe drift band was admitted by a rule whose " +
			"bound happens to be the same number")
	}

	// Just inside it, so the rule is not simply refusing everything.
	inside := signal("e2", "motion", 0)
	inside.ClockDriftMS = 9999
	if !only(t, outcomes(t, b, inside)).Matched {
		t.Error("a device just inside the severe band was refused")
	}
}

// The rule's own bound still bites below the site's, which is the direction the
// author is entitled to tighten in.
func TestARuleMayTightenTheDriftBoundBelowTheSiteBand(t *testing.T) {
	b, _ := compileRules(t, wrap(`
  - id: a.drift-bound
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - clockDrift: {maxMs: 500}
    then: {createIncident: true, severity: high}
`))

	event := signal("e1", "motion", 0)
	event.ClockDriftMS = 800
	if only(t, outcomes(t, b, event)).Matched {
		t.Error("a device drifted 800ms was admitted by a rule bounding drift at 500ms")
	}
}

// ---------------------------------------------------------------------------
// R13. The unused_time_window diagnostic.
// ---------------------------------------------------------------------------

// The parameter was the rule's disabled flag and the warning fired on it, so an
// enabled rule with a dead window was told nothing and a disabled one was. The
// condition that matters is whether a corroboration clause exists to read the
// window.
func TestAnEnabledRuleWithADeadTimeWindowWarns(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.dead-window
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 30}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "unused_time_window") {
		t.Errorf("an enabled rule whose time window bounds nothing produced no warning: %+v",
			problems)
	}
}

func TestADisabledRuleWithADeadTimeWindowAlsoWarns(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.dead-window
    enabled: false
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 30}
    then: {createIncident: true}
`))
	if !hasProblem(problems, "unused_time_window") {
		t.Errorf("a disabled rule's unread window produced no warning: %+v", problems)
	}
}

// A window beside a corroboration clause is read, so it is not dead
// configuration. The window is enforced now, so this warning would be wrong.
func TestATimeWindowWithCorroborationDoesNotWarn(t *testing.T) {
	_, problems := compile(t, wrap(`
  - id: a.live-window
    enabled: true
    priority: 10
    when:
      all:
        - eventType: [motion]
        - timeWindow: {field: normalized_at, durationSeconds: 30}
        - corroborate:
            eventTypes: [analytics]
            minSignals: 2
    then: {createIncident: true}
`))
	if hasProblem(problems, "unused_time_window") {
		t.Errorf("a window with a corroboration clause to read it warned: %+v", problems)
	}
}

// The built-in bundle is the one that ships. It must keep compiling clean: these
// changes are not allowed to introduce a diagnostic a release archive would
// fail on.
func TestBuiltinBundleHasNoNewDiagnostics(t *testing.T) {
	raw, err := os.ReadFile(repoPath(t, "rules", "builtin", "bundle.yaml"))
	if err != nil {
		t.Fatalf("read built-in bundle: %v", err)
	}
	_, problems := rules.NewCompiler().Compile(raw)
	for _, p := range problems {
		if p.Code == "unused_time_window" || p.Code == "action_require_ai_observation" ||
			p.Code == "field_value_empty_prefix" || p.Code == "if_ai_unavailable_unused" {
			t.Errorf("the built-in bundle now reports %s: %s", p.Code, p)
		}
	}
}

// A sanity check on the fixture itself: the built-in window test above is only
// meaningful if the bundle under test really has a window on it.
func TestTheWindowFixtureHasAWindow(t *testing.T) {
	if base.IsZero() {
		t.Fatal("the test base time is zero, so every window comparison is degenerate")
	}
	if afterHours().SkewTolerance != 5*time.Second {
		t.Fatal("the fixture skew tolerance is not 5s; the window tests assume it")
	}
}
