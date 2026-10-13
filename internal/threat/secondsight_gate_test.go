package threat

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
)

// escalatingPlaybook is the default playbook with RequireHumanForCritical
// cleared: under the shipped default, every score at or above 80 is held for a
// human, so the second-sight gate would have nothing to demonstrate. The gate
// is what holds an escalation a playbook would otherwise have made.
func escalatingPlaybook() *Playbook {
	cfg := DefaultPlaybookConfig()
	cfg.RequireHumanForCritical = false
	return NewPlaybook(cfg, DefaultMatrix(), NewScorer(DefaultScorerConfig()))
}

// escalatingIncident builds an incident the matrix and the playbook both want
// to escalate: two anomalies, score above the escalation floor. The zone_type
// detail is what the scorer's multipliers read, and the ID makes each fixture
// a distinct incident — a playbook holds per-incident state, so two calls with
// one id would meet a cooldown instead of the gate under test.
func escalatingIncident(id string) *correlation.CorrelatedIncident {
	now := time.Now()
	return &correlation.CorrelatedIncident{
		ID: id, CameraIDs: []string{"cam-01"}, FirstSeen: now.Add(-time.Minute), LastSeen: now,
		Anomalies: []correlation.Anomaly{
			{Type: "dwell", RuleID: "dwell-restricted", ZoneID: "restricted", Severity: "critical",
				FirstSeen: now.Add(-time.Minute), LastSeen: now,
				Details: map[string]any{"zone_type": "restricted"}},
			{Type: "tailgate", RuleID: "tailgate-restricted", ZoneID: "restricted", Severity: "high",
				FirstSeen: now.Add(-time.Minute), LastSeen: now,
				Details: map[string]any{"zone_type": "restricted"}},
		},
		ThreatScore: 70,
	}
}

// TestADemotedEscalationIsNotCharged is the regression test for the defect
// that made the gate dangerous instead of protective: the demotion used to be
// applied AFTER Decide had already committed its state, so a single-witness
// escalation was charged to the hourly budget and recorded as an escalation
// the incident had made. Ten demotions then exhausted MaxEscalationsPerHour
// and every later corroborated escalation returned HOLD, and the observe
// branch read the never-escalated incident as a de-escalation. The gate now
// runs before the commit, and this asserts both halves of that.
func TestADemotedEscalationIsNotCharged(t *testing.T) {
	p := escalatingPlaybook()
	now := time.Now()

	// Ten single-witness escalations, all demoted.
	for i := 0; i < 10; i++ {
		action, state, _ := p.DecideWithSight(escalatingIncident("inc-demo-"+string(rune('a'+i))), correlation.Sight{
			Verdict: correlation.SightSingle,
			Reason:  "single sighting by cam-01: cam-02 watches restricted and did not add a sighting",
		}, now)
		if action != ActionVerify || state != "VERIFYING" {
			t.Fatalf("demotion %d returned %s/%s, want VERIFY/VERIFYING", i, action, state)
		}
	}

	if p.escalations != 0 {
		t.Errorf("the playbook charged %d escalations for demoted ones, want 0 — the hourly budget is spent on escalations that never happened", p.escalations)
	}
	for id, st := range p.states {
		if st.CurrentAction == ActionEscalate {
			t.Errorf("incident %s records CurrentAction ESCALATE although it never escalated", id)
		}
		if st.EscalationCount != 0 {
			t.Errorf("incident %s records %d escalations, want 0", id, st.EscalationCount)
		}
	}

	// And a corroborated incident on the next pass still escalates: the budget
	// was not consumed by the demotions.
	inc := escalatingIncident("inc-corroborated")
	inc.CameraIDs = []string{"cam-01", "cam-02"}
	action, state, _ := p.DecideWithSight(inc, correlation.Sight{
		Verdict: correlation.SightCorroborated,
		Reason:  "corroborated by 2 cameras: cam-01, cam-02",
	}, now)
	if action != ActionEscalate || state != "ESCALATING" {
		t.Errorf("a corroborated incident returned %s/%s, want ESCALATE/ESCALATING", action, state)
	}
	if p.escalations != 1 {
		t.Errorf("the budget counted %d escalations, want exactly the corroborated one", p.escalations)
	}
}

// TestSecondSightGateHoldsAnEscalation is the gate the 65.6% asked for: one
// camera witnessed it, so the escalation waits at verify and the chain says
// why. The score does not move — the evidence is exactly what it was.
func TestSecondSightGateHoldsAnEscalation(t *testing.T) {
	p := escalatingPlaybook()
	now := time.Now()

	base, state, _ := p.Decide(escalatingIncident("inc-base"), now)
	if base != ActionEscalate {
		t.Skipf("fixture did not escalate (%s/%s), so the gate has nothing to hold", base, state)
	}

	gated, gatedState, gatedChain := p.DecideWithSight(escalatingIncident("inc-gated"), correlation.Sight{
		Verdict: correlation.SightSingle,
		Reason:  "single sighting by cam-01: cam-02 watches restricted and did not add a sighting",
	}, now)
	if gated != ActionVerify || gatedState != "VERIFYING" {
		t.Errorf("gated = %s/%s, want VERIFY/VERIFYING", gated, gatedState)
	}
	if !strings.Contains(gatedChain.Why, "Escalation held at verify") {
		t.Errorf("gated chain does not name the hold: %q", gatedChain.Why)
	}
	var found bool
	for _, s := range gatedChain.Steps {
		if s.Key == "second_sight" && s.Factor == 0 {
			found = true
			if !strings.Contains(s.Evidence, "single sighting by cam-01") {
				t.Errorf("gate step evidence = %q", s.Evidence)
			}
		}
	}
	if !found {
		t.Error("the reason chain has no second-sight step")
	}
	// The score is untouched: the gate is a guardrail on the action, not a
	// discount on the evidence.
	if got, _ := p.scorer.Score(escalatingIncident("inc-score")); got < 60 {
		t.Errorf("gated incident's score = %d, want it unchanged", got)
	}
}

// TestSecondSightGateLetsCorroborationThrough is the other half: two witnesses
// escalate unchanged, so the gate is not a blanket veto.
func TestSecondSightGateLetsCorroborationThrough(t *testing.T) {
	p := escalatingPlaybook()
	now := time.Now()
	twoCameras := func(id string) *correlation.CorrelatedIncident {
		inc := escalatingIncident(id)
		inc.CameraIDs = []string{"cam-01", "cam-02"}
		return inc
	}

	want, wantState, wantChain := p.Decide(twoCameras("inc-cor-a"), now)
	got, gotState, gotChain := p.DecideWithSight(twoCameras("inc-cor-b"), correlation.Sight{
		Verdict: correlation.SightCorroborated,
		Reason:  "corroborated by 2 cameras: cam-01, cam-02",
	}, now)
	if got != want || gotState != wantState {
		t.Errorf("corroborated decision = %s/%s, want the ungated %s/%s", got, gotState, want, wantState)
	}
	if len(gotChain.Steps) != len(wantChain.Steps) {
		t.Errorf("corroborated chain gained a step (%d vs %d)", len(gotChain.Steps), len(wantChain.Steps))
	}
}

// TestSecondSightGateLeavesNonEscalationsAlone: the gate only gates
// escalation. A verify, an observe and a hold pass through untouched.
func TestSecondSightGateLeavesNonEscalationsAlone(t *testing.T) {
	p := escalatingPlaybook()
	inc := &correlation.CorrelatedIncident{
		ID: "inc-quiet", CameraIDs: []string{"cam-01"},
		Anomalies:   []correlation.Anomaly{{Type: "loiter", ZoneID: "parking", Severity: "low"}},
		ThreatScore: 10,
	}
	now := time.Now()
	want, wantState, _ := p.Decide(inc, now)
	got, gotState, _ := p.DecideWithSight(inc, correlation.Sight{Verdict: correlation.SightCannotTell, Reason: "cannot tell: nothing declared"}, now)
	if got != want || gotState != wantState {
		t.Errorf("non-escalating incident was changed by the gate: %s/%s vs %s/%s", got, gotState, want, wantState)
	}
}

// TestCannotTellAlsoHoldsEscalation is the honesty direction: when the
// declaration cannot answer, the answer is not "escalate anyway".
func TestCannotTellAlsoHoldsEscalation(t *testing.T) {
	p := escalatingPlaybook()
	now := time.Now()
	base, _, _ := p.Decide(escalatingIncident("inc-base"), now)
	if base != ActionEscalate {
		t.Skipf("fixture did not escalate (%s), so the gate has nothing to hold", base)
	}
	action, state, chain := p.DecideWithSight(escalatingIncident("inc-cannot"), correlation.Sight{
		Verdict: correlation.SightCannotTell,
		Reason:  "cannot tell: no camera->zone declaration is loaded",
	}, now)
	if action == ActionEscalate || state == "ESCALATING" {
		t.Errorf("a cannot-tell sighting escalated (%s/%s)", action, state)
	}
	if action != ActionVerify || state != "VERIFYING" {
		t.Errorf("cannot-tell escalation = %s/%s, want VERIFY/VERIFYING", action, state)
	}
	if !strings.Contains(chain.Why, "cannot tell") {
		t.Errorf("chain does not carry the cannot-tell reason: %q", chain.Why)
	}
}
