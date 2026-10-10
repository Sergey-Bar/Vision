// Package threat provides explainable scoring and autonomous playbook for the autonomy layer.
package threat

import (
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlation"
)

// PlaybookConfig configures the autonomous playbook.
type PlaybookConfig struct {
	// Cooldowns between state transitions
	VerifyCooldown   time.Duration
	EscalateCooldown time.Duration
	HoldCooldown     time.Duration
	// Minimum evidence thresholds
	MinAnomaliesForVerify   int
	MinAnomaliesForEscalate int
	MinScoreForVerify       int
	MinScoreForEscalate     int
	// Guardrails
	MaxEscalationsPerHour   int
	RequireHumanForCritical bool
}

// DefaultPlaybookConfig returns default playbook configuration.
func DefaultPlaybookConfig() PlaybookConfig {
	return PlaybookConfig{
		VerifyCooldown:          30 * time.Second,
		EscalateCooldown:        60 * time.Second,
		HoldCooldown:            300 * time.Second,
		MinAnomaliesForVerify:   1,
		MinAnomaliesForEscalate: 2,
		MinScoreForVerify:       30,
		MinScoreForEscalate:     60,
		MaxEscalationsPerHour:   10,
		RequireHumanForCritical: true,
	}
}

// PlaybookState tracks the state of an incident in the playbook.
type PlaybookState struct {
	IncidentID      string
	CurrentAction   Action
	CurrentState    string // AUTO | VERIFYING | ESCALATING | HOLD
	LastTransition  time.Time
	EscalationCount int
	LastEscalation  time.Time
}

// Playbook manages autonomous decision making for incidents.
type Playbook struct {
	cfg              PlaybookConfig
	matrix           *Matrix
	scorer           *Scorer
	states           map[string]*PlaybookState
	escalations      int
	escalationWindow time.Time
}

// NewPlaybook creates a new playbook.
func NewPlaybook(cfg PlaybookConfig, matrix *Matrix, scorer *Scorer) *Playbook {
	return &Playbook{
		cfg:              cfg,
		matrix:           matrix,
		scorer:           scorer,
		states:           make(map[string]*PlaybookState),
		escalationWindow: time.Now().Add(time.Hour),
	}
}

// Decide evaluates an incident and returns the autonomous decision.
func (p *Playbook) Decide(inc *correlation.CorrelatedIncident, now time.Time) (Action, string, ReasonChain) {
	return p.decide(inc, correlation.Sight{}, now)
}

// DecideWithSight is Decide with the second-sight gate applied: an escalation
// only one camera witnessed is held at VERIFY, and the reason chain says why.
//
// The gate is a guardrail, not a score factor — the score is what the
// evidence supports and the sighting is how much of that evidence there is.
// A demoted escalation is recorded as a zero-factor step so the operator
// reading the chain sees the hold and the reason, not a silently lower score.
//
// The gate runs BEFORE the playbook's state is committed, which is the whole
// point of this method existing rather than being applied afterwards: a
// demoted escalation must not be charged to the hourly budget (ten single-
// witness escalations used to exhaust it and make every later corroborated
// escalation return HOLD) and must not be recorded as an escalation the
// incident actually made (which put never-escalated incidents into HOLD for
// HoldCooldown and produced a duty-log line reading as a de-escalation from
// an escalation that never happened).
func (p *Playbook) DecideWithSight(inc *correlation.CorrelatedIncident, sight correlation.Sight, now time.Time) (Action, string, ReasonChain) {
	return p.decide(inc, sight, now)
}

// decide is the shared body. An empty Sight has no verdict and therefore never
// gates anything, which is why Decide is exactly this with nothing to check.
func (p *Playbook) decide(inc *correlation.CorrelatedIncident, sight correlation.Sight, now time.Time) (Action, string, ReasonChain) {
	// Score the incident
	score, chain := p.scorer.Score(inc)

	// Get or create playbook state
	state := p.getOrCreateState(inc.ID, inc.AutonomyState)

	// Reset escalation counter if window expired
	if now.After(p.escalationWindow) {
		p.escalations = 0
		p.escalationWindow = now.Add(time.Hour)
	}

	// Determine action from matrix
	severity := scoreToSeverity(score)
	likelihood := scoreToLikelihood(score)
	matrixAction := p.matrix.Decide(severity, likelihood)

	// Apply guardrails and cooldowns
	action, autonomyState := p.applyGuardrails(state, matrixAction, score, inc, now)

	// The second-sight gate, before the state is committed. An empty Sight
	// (the plain Decide path) has no verdict and gates nothing.
	if action == ActionEscalate && !sight.Corroborated() && sight.Verdict != "" {
		// A single witness — or a declaration that cannot answer — holds at
		// verify. The escalation is not forbidden; it waits for the second
		// camera that was supposed to be there.
		action, autonomyState = ActionVerify, "VERIFYING"
		chain.Steps = append(chain.Steps, ReasonStep{
			Key:      "second_sight",
			Rule:     "second-sight gate",
			Factor:   0,
			Evidence: sight.Reason,
			At:       now,
		})
		if !strings.HasSuffix(chain.Why, ".") && chain.Why != "" {
			chain.Why += "."
		}
		chain.Why += " Escalation held at verify: " + sight.Reason + "."
	}

	// Update state
	state.CurrentAction = action
	state.CurrentState = autonomyState
	state.LastTransition = now

	if action == ActionEscalate {
		state.EscalationCount++
		state.LastEscalation = now
		p.escalations++
	}

	return action, autonomyState, chain
}

func (p *Playbook) getOrCreateState(incidentID, currentAutonomyState string) *PlaybookState {
	if state, ok := p.states[incidentID]; ok {
		return state
	}
	state := &PlaybookState{
		IncidentID:     incidentID,
		CurrentState:   currentAutonomyState,
		LastTransition: time.Time{},
	}
	p.states[incidentID] = state
	return state
}

func (p *Playbook) applyGuardrails(state *PlaybookState, matrixAction Action, score int, inc *correlation.CorrelatedIncident, now time.Time) (Action, string) {
	// Check cooldowns
	if state.LastTransition.IsZero() {
		// First decision
		return p.initialAction(matrixAction, score, inc)
	}

	timeSinceLastTransition := now.Sub(state.LastTransition)

	// Cannot escalate too frequently
	if matrixAction == ActionEscalate {
		if timeSinceLastTransition < p.cfg.EscalateCooldown {
			return ActionVerify, "VERIFYING"
		}
		if p.escalations >= p.cfg.MaxEscalationsPerHour {
			return ActionHold, "HOLD"
		}
		if p.cfg.RequireHumanForCritical && score >= 80 {
			return ActionVerify, "VERIFYING"
		}
		if score < p.cfg.MinScoreForEscalate || len(inc.Anomalies) < p.cfg.MinAnomaliesForEscalate {
			return ActionVerify, "VERIFYING"
		}
		return ActionEscalate, "ESCALATING"
	}

	// Verify action
	if matrixAction == ActionVerify {
		if timeSinceLastTransition < p.cfg.VerifyCooldown {
			return state.CurrentAction, state.CurrentState
		}
		if score < p.cfg.MinScoreForVerify || len(inc.Anomalies) < p.cfg.MinAnomaliesForVerify {
			return ActionObserve, "AUTO"
		}
		return ActionVerify, "VERIFYING"
	}

	// Observe action
	if matrixAction == ActionObserve {
		if state.CurrentAction == ActionEscalate || state.CurrentAction == ActionVerify {
			// De-escalate after hold cooldown
			if timeSinceLastTransition > p.cfg.HoldCooldown {
				return ActionObserve, "AUTO"
			}
			return ActionHold, "HOLD"
		}
		return ActionObserve, "AUTO"
	}

	// Hold action
	if matrixAction == ActionHold {
		if timeSinceLastTransition < p.cfg.HoldCooldown {
			return state.CurrentAction, state.CurrentState
		}
		return ActionObserve, "AUTO"
	}

	return matrixAction, "AUTO"
}

func (p *Playbook) initialAction(matrixAction Action, score int, inc *correlation.CorrelatedIncident) (Action, string) {
	switch matrixAction {
	case ActionEscalate:
		if score >= p.cfg.MinScoreForEscalate && len(inc.Anomalies) >= p.cfg.MinAnomaliesForEscalate {
			if !p.cfg.RequireHumanForCritical || score < 80 {
				return ActionEscalate, "ESCALATING"
			}
			return ActionVerify, "VERIFYING"
		}
		return ActionVerify, "VERIFYING"
	case ActionVerify:
		if score >= p.cfg.MinScoreForVerify && len(inc.Anomalies) >= p.cfg.MinAnomaliesForVerify {
			return ActionVerify, "VERIFYING"
		}
		return ActionObserve, "AUTO"
	default:
		return ActionObserve, "AUTO"
	}
}

// getState returns the playbook state for an incident.
func (p *Playbook) getState(incidentID string) *PlaybookState {
	return p.states[incidentID]
}
