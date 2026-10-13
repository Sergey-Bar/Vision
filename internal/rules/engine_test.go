package rules_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/rules"
)

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func signal(id, eventType string, offsetMS int64) rules.Signal {
	return rules.Signal{
		EventID:       id,
		SourceEventID: id,
		EventType:     eventType,
		NormalizedAt:  base.Add(time.Duration(offsetMS) * time.Millisecond),
		DeviceID:      "cam-1",
		DeviceKind:    "camera",
		ZoneID:        "zone-loading-bay",
		ZonePath:      "/site/entrance/loading-bay",
		Tier:          rules.TierA,
		Payload: map[string]any{
			"vendor": map[string]any{"Type": eventType, "Message": "ok"},
		},
	}
}

func afterHours() rules.Context {
	return rules.Context{
		Now: siteNow,
		// The built-in bundle's schedule is "business hours, inverted". A real
		// site resolves schedules from its own data; the test says so explicitly
		// rather than relying on a default.
		InSchedule: func(id string, at time.Time) bool {
			if id != "business-hours" {
				return false
			}
			h := at.UTC().Hour()
			return h >= 8 && h < 18
		},
		SkewTolerance: 5 * time.Second,
		SiteTimezone:  time.UTC,
	}
}

func eval(t *testing.T, b *rules.CompiledBundle, trigger rules.Signal, others ...rules.Signal) rules.Decision {
	t.Helper()
	e := rules.NewEngine(b, afterHours())
	return e.Resolve(e.Evaluate(trigger, others))
}

// ---------------------------------------------------------------------------
// The plan's central AI invariant, enforced at evaluation time.
// ---------------------------------------------------------------------------

// With no AI observation at all, the rule's declared degradation applies and the
// outcome is UNVERIFIED — never CORROBORATED, and never "no person detected".
func TestMissingAIObservationDegradesToUnverified(t *testing.T) {
	b := builtin(t)

	motion := signal("e1", "motion", 0)
	motion.AIObservation = nil

	analytics := signal("e2", "analytics", 1200)
	analytics.Tier = rules.TierB
	analytics.AIObservation = nil

	// Restricted zone path, so the zone condition matches.
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	analytics.ZonePath = motion.ZonePath

	d := eval(t, b, motion, analytics)
	if d.RuleID != "core.person-in-restricted-zone-after-hours" {
		t.Fatalf("rule %q decided this event, want core.person-in-restricted-zone-after-hours", d.RuleID)
	}
	if d.State != rules.StateUnverified {
		t.Errorf("state is %s, want UNVERIFIED. A missing AI observation must never produce "+
			"a corroborated outcome", d.State)
	}
	if !d.Degraded {
		t.Error("the decision is not marked degraded; a pilot could not measure how often " +
			"the AI path was actually live")
	}
	if d.DegradedReason != rules.DegradedAbsent {
		t.Errorf("degraded reason is %q, want absent", d.DegradedReason)
	}
	if d.Severity == "" {
		t.Error("a degraded decision still produced no severity; the operator gets nothing to triage")
	}
}

// An observation recording a degradation is not evidence at all. A degraded
// observation and an absent one are the same to the rule, and both differ from a
// negative answer.
func TestDegradedObservationIsNotEvidence(t *testing.T) {
	b := builtin(t)

	motion := signal("e1", "motion", 0)
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	motion.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured:     map[string]any{"restrictedZoneBreach": false},
		Degraded:       true,
		DegradedReason: "timeout",
	}

	analytics := signal("e2", "analytics", 1000)
	analytics.Tier = rules.TierB
	analytics.ZonePath = motion.ZonePath
	analytics.AIObservation = &rules.AIObservation{
		ObservationID: "obs-2", ProfileID: "restricted-person",
		Structured:     map[string]any{"restrictedZoneBreach": false},
		Degraded:       true,
		DegradedReason: "timeout",
	}

	d := eval(t, b, motion, analytics)
	if d.State == rules.StateCorroborated {
		t.Error("a degraded observation corroborated an incident; degradation is not evidence")
	}
	if d.DegradedReason != rules.DegradedTimeout {
		t.Errorf("degraded reason is %q, want timeout", d.DegradedReason)
	}
}

// The model answering "I cannot tell" is not the model answering "no". It does
// not satisfy a condition and it does not contradict one.
func TestInsufficientEvidenceIsNotANegativeAnswer(t *testing.T) {
	b := builtin(t)

	motion := signal("e1", "motion", 0)
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	motion.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured:           map[string]any{"restrictedZoneBreach": false},
		InsufficientEvidence: true,
	}

	analytics := signal("e2", "analytics", 1000)
	analytics.Tier = rules.TierB
	analytics.ZonePath = motion.ZonePath
	analytics.AIObservation = &rules.AIObservation{
		ObservationID: "obs-2", ProfileID: "restricted-person",
		Structured:           map[string]any{"restrictedZoneBreach": false},
		InsufficientEvidence: true,
	}

	d := eval(t, b, motion, analytics)
	if d.State == rules.StateCorroborated {
		t.Error("insufficient evidence was treated as corroboration")
	}
	if d.RuleID == "" {
		t.Error("no rule decided the event; the operator sees nothing at all")
	}
}

// With a real observation present, the same facts corroborate.
func TestPresentObservationCorroborates(t *testing.T) {
	b := builtin(t)

	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true, "confidence": 0.94},
	}
	motion := signal("e1", "motion", 0)
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	motion.AIObservation = obs

	analytics := signal("e2", "analytics", 1200)
	analytics.Tier = rules.TierB
	analytics.ZonePath = motion.ZonePath
	analytics.AIObservation = obs

	d := eval(t, b, motion, analytics)
	if d.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", d.State, d.Reason)
	}
	if d.Degraded {
		t.Error("a fully corroborated decision is marked degraded")
	}
	if len(d.SupportingObservations) == 0 {
		t.Error("no observation is cited; the decision cannot be explained by model and prompt later")
	}
	if d.Severity != "high" {
		t.Errorf("severity is %q, want high", d.Severity)
	}
}

// ---------------------------------------------------------------------------
// Clock drift.
// ---------------------------------------------------------------------------

// A device past the severe band is excluded from time-windowed corroboration, and
// the exclusion is recorded on the decision rather than applied silently.
//
// The scenario is the one the plan names: good signals that do corroborate, plus
// a drifted one that must not. Without the good ones the rule fails for a second
// reason and the exclusion would never be reached, which would test nothing
// about the drift policy.
func TestDriftedDeviceIsExcludedAndRecorded(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	trigger := signal("e1", "motion", 0)
	trigger.ZonePath = "/site/entrance/restricted/loading-bay"
	trigger.AIObservation = obs

	clean := signal("e2", "analytics", 1200)
	clean.Tier = rules.TierB
	clean.ZonePath = trigger.ZonePath
	clean.AIObservation = obs

	// A third camera running 40 seconds fast. Its timestamp is unusable for a
	// five-second window, so it must not corroborate, and it must say why it did
	// not.
	drifted := signal("e3", "access.granted", 40000)
	drifted.Tier = rules.TierB
	drifted.DeviceID = "cam-drifted"
	drifted.ZonePath = trigger.ZonePath
	drifted.ClockDriftMS = 40000
	drifted.DriftedExcluded = true
	drifted.AIObservation = obs

	d := eval(t, b, trigger, clean, drifted)

	if d.State != rules.StateCorroborated {
		t.Fatalf("state is %s (%s), want CORROBORATED", d.State, d.Reason)
	}
	for _, e := range d.SupportingEvents {
		if e == "e3" {
			t.Error("a device in the severe drift band corroborated the event")
		}
	}
	if len(d.DriftExclusions) == 0 {
		t.Fatal("the exclusion was applied silently; a corroborating signal that vanished " +
			"without explanation is what an operator stops trusting the product over")
	}
	if !strings.Contains(d.DriftExclusions[0].Reason, "severe") {
		t.Errorf("the exclusion reason does not say why: %q", d.DriftExclusions[0].Reason)
	}
	if d.DriftExclusions[0].DeviceID != "cam-drifted" {
		t.Errorf("the exclusion names %q, want cam-drifted", d.DriftExclusions[0].DeviceID)
	}
}

// Drift is a property of the site, not of one rule's decision, so it is surfaced
// even on the days nothing was decided. An operator asking why a camera never
// corroborates anything needs the answer on those days too.
func TestDriftIsSurfacedEvenWhenNoIncidentIsRaised(t *testing.T) {
	b := builtin(t)
	trigger := signal("e1", "motion", 0)
	trigger.ZonePath = "/site/entrance/restricted/loading-bay"

	drifted := signal("e2", "analytics", 40000)
	drifted.Tier = rules.TierB
	drifted.ZonePath = trigger.ZonePath
	drifted.ClockDriftMS = 40000
	drifted.DriftedExcluded = true

	d := eval(t, b, trigger, drifted)
	if d.Severity != "" && d.Action.CreateIncident {
		t.Fatalf("rule %q created an incident, want none; the fixture is wrong", d.RuleID)
	}
	if len(d.DriftExclusions) == 0 {
		t.Error("drift was dropped because no incident was raised")
	}
}

// A signal just outside the window does not corroborate, and a signal just
// inside it does. The boundary is the site's, not the host's.
func TestTimeWindowUsesTheSiteSkewTolerance(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	run := func(offsetMS int64) rules.Decision {
		motion := signal("e1", "motion", 0)
		motion.ZonePath = "/site/entrance/restricted/loading-bay"
		motion.AIObservation = obs
		other := signal("e2", "analytics", offsetMS)
		other.Tier = rules.TierB
		other.ZonePath = motion.ZonePath
		other.AIObservation = obs
		return eval(t, b, motion, other)
	}

	if d := run(3000); d.State != rules.StateCorroborated {
		t.Errorf("a signal 3s away did not corroborate under a 5s tolerance: %s (%s)", d.State, d.Reason)
	}
	if d := run(9000); d.State == rules.StateCorroborated {
		t.Error("a signal 9s away corroborated under a 5s tolerance")
	}
}

// ---------------------------------------------------------------------------
// Determinism, suppression and precedence.
// ---------------------------------------------------------------------------

// A deliberate suppression short-circuits everything below it.
func TestSuppressionWins(t *testing.T) {
	b := builtin(t)

	// The built-in suppression fires on low-confidence analytics. The zone and
	// schedule conditions of the production rule would also match, so this is
	// the case where precedence has to be right.
	suppressed := signal("e1", "analytics", 0)
	suppressed.ZonePath = "/site/entrance/restricted/loading-bay"
	suppressed.Payload = map[string]any{
		"vendor": map[string]any{
			"Type": "Analytics", "Message": "ok",
			"Analytics": map[string]any{"Confidence": 0.10},
		},
	}
	suppressed.AIObservation = &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	other := signal("e2", "motion", 1000)
	other.ZonePath = suppressed.ZonePath
	other.AIObservation = suppressed.AIObservation

	d := eval(t, b, suppressed, other)
	if d.State != rules.StateSuppressed {
		t.Errorf("state is %s (%s), want SUPPRESSED", d.State, d.Reason)
	}
	if d.RuleID != "core.suppress.analytics-below-threshold" {
		t.Errorf("rule %q suppressed it, want core.suppress.analytics-below-threshold", d.RuleID)
	}
}

// The same signals must produce the same decision every time, and the order they
// are offered in must not matter. This is the property a months-old replay
// depends on.
func TestEvaluationIsDeterministic(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	motion := signal("e1", "motion", 0)
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	motion.AIObservation = obs

	others := []rules.Signal{}
	for i, typ := range []string{"analytics", "access.granted", "device_state"} {
		s := signal("e"+string(rune('2'+i)), typ, int64(500*(i+1)))
		s.Tier = rules.TierB
		s.ZonePath = motion.ZonePath
		s.AIObservation = obs
		others = append(others, s)
	}

	first := eval(t, b, motion, others...)
	for run := 0; run < 10; run++ {
		got := eval(t, b, motion, others...)
		if got.State != first.State || got.RuleID != first.RuleID || got.Reason != first.Reason {
			t.Fatalf("run %d decided %s/%s/%s, first run decided %s/%s/%s",
				run, got.State, got.RuleID, got.Reason, first.State, first.RuleID, first.Reason)
		}
		if !sameStrings(got.SupportingEvents, first.SupportingEvents) {
			t.Fatalf("run %d cited %v, first run cited %v",
				run, got.SupportingEvents, first.SupportingEvents)
		}
	}

	reversed := []rules.Signal{others[2], others[0], others[1]}
	got := eval(t, b, motion, reversed...)
	if !sameStrings(got.SupportingEvents, first.SupportingEvents) {
		t.Errorf("the order signals were offered in changed the cited evidence: %v vs %v",
			got.SupportingEvents, first.SupportingEvents)
	}
}

// The same evidence must give the same decision regardless of the trigger's
// position, because correlation is driven by normalized time and never by
// arrival order.
func TestOutOfOrderArrivalDoesNotChangeTheOutcome(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	earlier := signal("e-early", "analytics", -2000)
	earlier.Tier = rules.TierB
	earlier.ZonePath = "/site/entrance/restricted/loading-bay"
	earlier.AIObservation = obs

	later := signal("e-late", "motion", 0)
	later.ZonePath = earlier.ZonePath
	later.AIObservation = obs

	// Arrival order says the analytics came first.
	d := eval(t, b, later, earlier)
	if d.State != rules.StateCorroborated {
		t.Errorf("state is %s (%s), want CORROBORATED", d.State, d.Reason)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A decision must be explainable by naming what it relied on, without
// re-running anything. This is the shape the console and `incident explain`
// read.
func TestDecisionCarriesItsProvenance(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}
	motion := signal("e1", "motion", 0)
	motion.ZonePath = "/site/entrance/restricted/loading-bay"
	motion.AIObservation = obs
	other := signal("e2", "analytics", 900)
	other.Tier = rules.TierB
	other.ZonePath = motion.ZonePath
	other.AIObservation = obs

	d := eval(t, b, motion, other)
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal decision: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatalf("unmarshal decision: %v", err)
	}
	for _, field := range []string{"State", "RuleID", "Severity", "Reason",
		"SupportingEvents", "SupportingObservations"} {
		if _, ok := doc[field]; !ok {
			t.Errorf("the serialized decision has no %s: %s", field, encoded)
		}
	}
	if len(d.SupportingEvents) < 2 {
		t.Errorf("only %d supporting events cited, want at least 2: %v",
			len(d.SupportingEvents), d.SupportingEvents)
	}
}

// The whole product path must work with the AI plane completely absent. A bundle
// whose only rule needs a model is a bundle that cannot run on a site with no
// GPU, and the built-in bundle is not that.
func TestBuiltInBundleRunsWithNoAIObservationAtAll(t *testing.T) {
	b := builtin(t)
	obs := &rules.AIObservation{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Structured: map[string]any{"restrictedZoneBreach": true},
	}

	// A door forced after hours: motion plus an access denial, no model.
	access := signal("e1", "access.denied", 0)
	access.DeviceID = "door-1"
	access.DeviceKind = "door"
	access.ZonePath = "/site/entrance"
	access.Tier = rules.TierA
	access.AIObservation = nil

	motion := signal("e2", "motion", 2000)
	motion.ZonePath = "/site/entrance"
	motion.Tier = rules.TierA
	motion.AIObservation = nil

	d := eval(t, b, access, motion)
	if d.RuleID != "core.door-forced-after-hours" {
		t.Fatalf("rule %q decided it, want core.door-forced-after-hours", d.RuleID)
	}
	if d.State == rules.StateCorroborated && d.Degraded {
		t.Errorf("a decision that needs no model reports itself degraded")
	}
	if d.Severity != "critical" {
		t.Errorf("severity is %q, want critical", d.Severity)
	}
	_ = obs
}

// A shadow profile must not create an incident and must not suppress one.
func TestShadowProfilesHaveNoAuthoritativeEffect(t *testing.T) {
	b := builtin(t)
	shadow := map[string]bool{}
	for _, r := range b.Compiled {
		if strings.HasPrefix(r.ID, "shadow.") {
			shadow[r.ID] = true
			if r.Then.CreateIncident {
				t.Errorf("shadow rule %s creates an incident", r.ID)
			}
			if r.Then.Suppress {
				t.Errorf("shadow rule %s suppresses an event", r.ID)
			}
			if r.Then.Escalate {
				t.Errorf("shadow rule %s escalates", r.ID)
			}
		}
	}
	if len(shadow) == 0 {
		t.Fatal("the built-in bundle has no shadow rules; shadow evaluation is not represented")
	}
}
