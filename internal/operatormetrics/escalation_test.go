package operatormetrics_test

// The AI's false-escalation companion.
//
// # The denominators
//
// The denominator is the escalations the operator JUDGED.
//
// Not the escalations there were: an escalation nobody judged is neither a true
// escalation nor a false one, and counting it either way is how a profile looks
// better than it is. And not the observations: a degraded observation and an
// insufficient-evidence one carry no verdict, so they are not escalations and
// they are not a denominator.
//
// # The verdict
//
// The model's own score field, read from the observation's structured output
// through the profile registry. A hard-coded field name would score every
// profile against one profile's claim.

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// observation writes one AI observation and, when outcome is set, the
// operator's disposition of it.
func observation(t *testing.T, h harness, incidentID, observationID, outcome string) {
	t.Helper()
	structured := `{"restrictedZoneBreach": true, "persons": 1}`
	if _, err := h.pool.WithSites(h.tenant, "test", []string{h.site}).Exec(context.Background(),
		`INSERT INTO ai_observations
			(observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
			 model, model_hash, runtime_version, prompt_template_id, prompt_version,
			 structured_output, insufficient_evidence, degraded_reason, degraded_detail,
			 output_schema_version, output_hash)
		 VALUES ($1, $2, $3, $4, 'evt-canary', 'restricted-person', 'vision-1',
		         repeat('b', 64), 'rt-1', 'tpl-1', '1', $5::jsonb, false, 'none', '',
		         1, repeat('c', 64))`,
		observationID, h.tenant, h.site, incidentID, structured); err != nil {
		t.Fatalf("insert the observation: %v", err)
	}
	if outcome == "" {
		return
	}
	if _, err := h.pool.WithSites(h.tenant, "test", []string{h.site}).Exec(context.Background(),
		`INSERT INTO dispositions
			(disposition_id, tenant_id, incident_id, actor_id, outcome, reason_code, notes,
			 agreement, related_observation_id, created_at)
		 VALUES ($1, $2, $3, 'operator-1', $4, 'canary', '', 'disagreed', $5, $6)`,
		"disp-"+observationID, h.tenant, incidentID, outcome, observationID, h.now); err != nil {
		t.Fatalf("insert the disposition: %v", err)
	}
}

// TestTheFalseEscalationRateIsTheShareOfJudgedEscalations is the companion.
//
// One false escalation out of one judged escalation is a rate of 1.00, and the
// N is 1.
func TestTheFalseEscalationRateIsTheShareOfJudgedEscalations(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	observation(t, h, id, "obs-canary-1", "false_positive")

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	win := operatormetrics.Window{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)}
	rate, err := reader.AIFalseEscalationRate(context.Background(), h.tenant, win)
	if err != nil {
		t.Fatalf("AIFalseEscalationRate: %v", err)
	}
	if rate.Judged != 1 {
		t.Errorf("the rate is over %d judged escalations, want 1", rate.Judged)
	}
	if rate.Value == nil {
		t.Fatal("the rate has no value")
	}
	if *rate.Value != 1 {
		t.Errorf("the rate is %v, want 1.00", *rate.Value)
	}
	if rate.CI == nil {
		t.Error("the rate has no interval")
	}
}

// TestAnUnjudgedEscalationIsNotADenominator is the discipline.
//
// An observation nobody judged is not a true escalation and not a false one,
// and it is never folded into a denominator.
func TestAnUnjudgedEscalationIsNotADenominator(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	observation(t, h, id, "obs-canary-1", "")

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	win := operatormetrics.Window{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)}
	rate, err := reader.AIFalseEscalationRate(context.Background(), h.tenant, win)
	if err != nil {
		t.Fatalf("AIFalseEscalationRate: %v", err)
	}
	if rate.Judged != 0 {
		t.Errorf("the rate is over %d judged escalations, want 0. An observation nobody judged "+
			"is not a denominator", rate.Judged)
	}
	if rate.Undispositioned != 1 {
		t.Errorf("the undispositioned count is %d, want 1. It is reported and never folded into "+
			"a denominator", rate.Undispositioned)
	}
	if rate.Value != nil {
		t.Errorf("the rate is %v over no judged escalations, want nil", *rate.Value)
	}
}

// TestAWindowWithNoObservationsHasNoRate is the honest absence.
func TestAWindowWithNoObservationsHasNoRate(t *testing.T) {
	h := setup(t)
	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	win := operatormetrics.Window{From: h.now.Add(-time.Hour), To: h.now.Add(time.Hour)}
	rate, err := reader.AIFalseEscalationRate(context.Background(), h.tenant, win)
	if err != nil {
		t.Fatalf("AIFalseEscalationRate: %v", err)
	}
	if rate.Value != nil {
		t.Errorf("the rate is %v over an empty window, want nil", *rate.Value)
	}
	if rate.Judged != 0 {
		t.Errorf("the rate is over %d judged escalations, want 0", rate.Judged)
	}
}
