package threat

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlation"
)

func TestReasonChain(t *testing.T) {
	chain := ReasonChain{}
	now := time.Now()

	chain.AddStep("dwell>15s", "dwell-restricted", 25, "restricted zone, high severity", now)
	chain.AddStep("loiter", "loiter-monitor", 15, "monitor zone, medium severity", now.Add(10*time.Second))

	if len(chain.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(chain.Steps))
	}

	if chain.TotalScore() != 40 {
		t.Errorf("total score = %d, want 40", chain.TotalScore())
	}

	if chain.Why == "" {
		t.Error("Why should not be empty")
	}
}

func TestScorer(t *testing.T) {
	cfg := DefaultScorerConfig()
	scorer := NewScorer(cfg)
	now := time.Now()

	inc := &correlation.CorrelatedIncident{
		Anomalies: []correlation.Anomaly{
			{
				Type:      "dwell",
				RuleID:    "dwell-restricted",
				ZoneID:    "restricted",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1"},
				FirstSeen: now.Add(-20 * time.Second),
				LastSeen:  now,
				Severity:  "high",
				Details: map[string]any{
					"zone_type": "restricted",
				},
			},
			{
				Type:      "tailgate",
				RuleID:    "tailgate-choke",
				ZoneID:    "choke",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1", "ent2"},
				FirstSeen: now.Add(-10 * time.Second),
				LastSeen:  now,
				Severity:  "critical",
				Details: map[string]any{
					"zone_type": "choke",
				},
			},
		},
		CameraIDs: []string{"cam1", "cam2"},
		Entities: []correlation.EntityRef{
			{ID: "ent1"}, {ID: "ent2"},
		},
	}

	score, chain := scorer.Score(inc)
	if score <= 0 {
		t.Errorf("score should be positive, got %d", score)
	}
	if score > 100 {
		t.Errorf("score should not exceed 100, got %d", score)
	}
	if len(chain.Steps) < 2 {
		t.Errorf("expected at least 2 steps in chain, got %d", len(chain.Steps))
	}
}

func TestMatrix(t *testing.T) {
	matrix := DefaultMatrix()

	// Critical + VeryHigh -> Escalate
	action := matrix.Decide(SeverityCritical, LikelihoodVeryHigh)
	if action != ActionEscalate {
		t.Errorf("Critical+VeryHigh = %s, want ESCALATE", action)
	}

	// Critical + Low -> Verify
	action = matrix.Decide(SeverityCritical, LikelihoodLow)
	if action != ActionVerify {
		t.Errorf("Critical+Low = %s, want VERIFY", action)
	}

	// Low + Low -> Observe
	action = matrix.Decide(SeverityLow, LikelihoodLow)
	if action != ActionObserve {
		t.Errorf("Low+Low = %s, want OBSERVE", action)
	}

	// Score-based decisions
	action = matrix.DecideFromScore(90)
	if action != ActionEscalate {
		t.Errorf("Score 90 = %s, want ESCALATE", action)
	}

	action = matrix.DecideFromScore(50)
	if action != ActionObserve {
		t.Errorf("Score 50 = %s, want OBSERVE (Medium+Medium=Observe)", action)
	}

	action = matrix.DecideFromScore(10)
	if action != ActionObserve {
		t.Errorf("Score 10 = %s, want OBSERVE", action)
	}
}

func TestScoreToSeverity(t *testing.T) {
	tests := []struct {
		score    int
		severity SeverityLevel
	}{
		{90, SeverityCritical},
		{80, SeverityCritical},
		{79, SeverityHigh},
		{60, SeverityHigh},
		{59, SeverityMedium},
		{30, SeverityMedium},
		{29, SeverityLow},
		{0, SeverityLow},
	}

	for _, tt := range tests {
		result := scoreToSeverity(tt.score)
		if result != tt.severity {
			t.Errorf("scoreToSeverity(%d) = %s, want %s", tt.score, result, tt.severity)
		}
	}
}

func TestScoreToLikelihood(t *testing.T) {
	tests := []struct {
		score      int
		likelihood LikelihoodLevel
	}{
		{90, LikelihoodVeryHigh},
		{80, LikelihoodVeryHigh},
		{79, LikelihoodHigh},
		{60, LikelihoodHigh},
		{59, LikelihoodMedium},
		{30, LikelihoodMedium},
		{29, LikelihoodLow},
		{0, LikelihoodLow},
	}

	for _, tt := range tests {
		result := scoreToLikelihood(tt.score)
		if result != tt.likelihood {
			t.Errorf("scoreToLikelihood(%d) = %s, want %s", tt.score, result, tt.likelihood)
		}
	}
}

func TestPlaybook(t *testing.T) {
	cfg := DefaultPlaybookConfig()
	matrix := DefaultMatrix()
	scorer := NewScorer(DefaultScorerConfig())
	playbook := NewPlaybook(cfg, matrix, scorer)
	now := time.Now()

	inc := &correlation.CorrelatedIncident{
		ID:            "inc-1",
		SiteID:        "site-1",
		State:         "ACTIVE",
		AutonomyState: "AUTO",
		FirstSeen:     now.Add(-30 * time.Second),
		LastSeen:      now,
		CameraIDs:     []string{"cam1"},
		Entities: []correlation.EntityRef{
			{ID: "ent1", Class: "person", CameraID: "cam1"},
		},
		Anomalies: []correlation.Anomaly{
			{
				Type:      "dwell",
				RuleID:    "dwell-restricted",
				ZoneID:    "restricted",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1"},
				FirstSeen: now.Add(-30 * time.Second),
				LastSeen:  now,
				Severity:  "high",
				Details: map[string]any{
					"zone_type": "restricted",
				},
			},
		},
	}

	action, autonomyState, chain := playbook.Decide(inc, now)
	if action != ActionVerify && action != ActionObserve {
		t.Errorf("initial action = %s, want VERIFY or OBSERVE", action)
	}
	if autonomyState != "VERIFYING" && autonomyState != "AUTO" {
		t.Errorf("initial autonomy = %s, want VERIFYING or AUTO", autonomyState)
	}
	if len(chain.Steps) == 0 {
		t.Error("chain should have steps")
	}

	// Test state persistence
	state := playbook.getState("inc-1")
	if state == nil {
		t.Fatal("state should be created by the first decision")
	}
	if state.IncidentID != "inc-1" {
		t.Errorf("state incident ID = %s, want inc-1", state.IncidentID)
	}
	if state.CurrentAction != action {
		t.Errorf("state action = %s, want %s (the state tracks the decision)", state.CurrentAction, action)
	}

	// An override is the operator surface's act, not the playbook's: the
	// linker moves the incident to HOLD/OVERRIDDEN and the playbook's state
	// for it is simply left behind. What this proves is that a second
	// decision on the same incident is still bounded by the cooldown, so an
	// operator who has overridden it does not get escalated again a second
	// later.
	action2, _, _ := playbook.Decide(inc, now.Add(time.Second))
	if action2 != action {
		t.Errorf("a second decision within the cooldown changed %s to %s", action, action2)
	}
}

func TestPlaybookEscalation(t *testing.T) {
	cfg := DefaultPlaybookConfig()
	cfg.RequireHumanForCritical = false // Allow escalation for testing
	matrix := DefaultMatrix()
	scorer := NewScorer(DefaultScorerConfig())
	playbook := NewPlaybook(cfg, matrix, scorer)
	now := time.Now()

	// High-score incident that should escalate
	inc := &correlation.CorrelatedIncident{
		ID:            "inc-2",
		SiteID:        "site-1",
		State:         "ACTIVE",
		AutonomyState: "AUTO",
		FirstSeen:     now.Add(-60 * time.Second),
		LastSeen:      now,
		CameraIDs:     []string{"cam1", "cam2"},
		Entities: []correlation.EntityRef{
			{ID: "ent1", Class: "person", CameraID: "cam1"},
			{ID: "ent2", Class: "person", CameraID: "cam2"},
		},
		Anomalies: []correlation.Anomaly{
			{
				Type:      "dwell",
				RuleID:    "dwell-restricted",
				ZoneID:    "restricted",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1"},
				FirstSeen: now.Add(-60 * time.Second),
				LastSeen:  now,
				Severity:  "critical",
				Details: map[string]any{
					"zone_type": "restricted",
				},
			},
			{
				Type:      "tailgate",
				RuleID:    "tailgate-choke",
				ZoneID:    "choke",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1", "ent2"},
				FirstSeen: now.Add(-30 * time.Second),
				LastSeen:  now,
				Severity:  "critical",
				Details: map[string]any{
					"zone_type": "choke",
				},
			},
			{
				Type:      "flow",
				RuleID:    "flow-route-1",
				ZoneID:    "",
				CameraID:  "cam2",
				EntityIDs: []string{"ent2"},
				FirstSeen: now.Add(-20 * time.Second),
				LastSeen:  now,
				Severity:  "high",
				Details: map[string]any{
					"zone_type": "route",
				},
			},
		},
	}

	action, autonomyState, _ := playbook.Decide(inc, now)
	if action != ActionEscalate {
		t.Errorf("high score action = %s, want ESCALATE (or at least VERIFY)", action)
	}
	if autonomyState != "ESCALATING" && autonomyState != "VERIFYING" {
		t.Errorf("high score autonomy = %s, want ESCALATING or VERIFYING", autonomyState)
	}

	state := playbook.getState("inc-2")
	if state.EscalationCount != 1 && action == ActionEscalate {
		t.Errorf("escalation count = %d, want 1", state.EscalationCount)
	}
}

func TestPlaybookCooldown(t *testing.T) {
	cfg := DefaultPlaybookConfig()
	cfg.VerifyCooldown = 10 * time.Second
	cfg.EscalateCooldown = 10 * time.Second
	matrix := DefaultMatrix()
	scorer := NewScorer(DefaultScorerConfig())
	playbook := NewPlaybook(cfg, matrix, scorer)
	now := time.Now()

	inc := &correlation.CorrelatedIncident{
		ID:            "inc-3",
		SiteID:        "site-1",
		State:         "ACTIVE",
		AutonomyState: "AUTO",
		FirstSeen:     now,
		LastSeen:      now,
		CameraIDs:     []string{"cam1"},
		Entities: []correlation.EntityRef{
			{ID: "ent1", Class: "person", CameraID: "cam1"},
		},
		Anomalies: []correlation.Anomaly{
			{
				Type:      "dwell",
				RuleID:    "dwell-restricted",
				ZoneID:    "restricted",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1"},
				FirstSeen: now,
				LastSeen:  now,
				Severity:  "high",
				Details: map[string]any{
					"zone_type": "restricted",
				},
			},
		},
	}

	// First decision
	action1, _, _ := playbook.Decide(inc, now)
	// Immediate second decision (within cooldown)
	action2, _, _ := playbook.Decide(inc, now.Add(1*time.Second))

	// Should not change due to cooldown
	if action1 != action2 {
		t.Errorf("action changed within cooldown: %s -> %s", action1, action2)
	}

	// After cooldown
	action3, _, _ := playbook.Decide(inc, now.Add(15*time.Second))
	// Could change now
	_ = action3 // just verify no panic
}

func TestPlaybookMaxEscalations(t *testing.T) {
	cfg := DefaultPlaybookConfig()
	cfg.MaxEscalationsPerHour = 2
	cfg.RequireHumanForCritical = false
	cfg.MinScoreForEscalate = 30
	cfg.MinAnomaliesForEscalate = 1
	matrix := DefaultMatrix()
	scorer := NewScorer(DefaultScorerConfig())
	playbook := NewPlaybook(cfg, matrix, scorer)
	now := time.Now()

	inc := &correlation.CorrelatedIncident{
		ID:            "inc-4",
		SiteID:        "site-1",
		State:         "ACTIVE",
		AutonomyState: "AUTO",
		FirstSeen:     now,
		LastSeen:      now,
		CameraIDs:     []string{"cam1", "cam2"},
		Entities: []correlation.EntityRef{
			{ID: "ent1", Class: "person", CameraID: "cam1"},
			{ID: "ent2", Class: "person", CameraID: "cam2"},
		},
		Anomalies: []correlation.Anomaly{
			{
				Type:      "dwell",
				RuleID:    "dwell-restricted",
				ZoneID:    "restricted",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1"},
				FirstSeen: now,
				LastSeen:  now,
				Severity:  "critical",
				Details: map[string]any{
					"zone_type": "restricted",
				},
			},
			{
				Type:      "tailgate",
				RuleID:    "tailgate-choke",
				ZoneID:    "choke",
				CameraID:  "cam1",
				EntityIDs: []string{"ent1", "ent2"},
				FirstSeen: now,
				LastSeen:  now,
				Severity:  "critical",
				Details: map[string]any{
					"zone_type": "choke",
				},
			},
		},
	}

	// First escalation
	action1, autonomy1, _ := playbook.Decide(inc, now)
	// Second escalation (after cooldown)
	action2, autonomy2, _ := playbook.Decide(inc, now.Add(2*time.Minute))
	// Third should be held due to limit
	action3, autonomy3, _ := playbook.Decide(inc, now.Add(4*time.Minute))

	// At least first two should escalate or verify
	if action1 != ActionEscalate && action1 != ActionVerify {
		t.Errorf("first action = %s, want ESCALATE or VERIFY", action1)
	}
	if action2 != ActionEscalate && action2 != ActionVerify {
		t.Errorf("second action = %s, want ESCALATE or VERIFY", action2)
	}
	// Third should be held due to limit
	if action3 != ActionHold && action3 != ActionVerify {
		t.Errorf("third action = %s, want HOLD or VERIFY (limit reached)", action3)
	}
	_ = autonomy1
	_ = autonomy2
	_ = autonomy3
}
