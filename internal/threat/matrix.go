// Package threat provides explainable scoring and autonomous playbook for the autonomy layer.
package threat

// Action is the autonomous action to take.
//
// The playbook returns one, the linker carries it on the incident, and the
// operator surface renders it. It is a string rather than an int so a decision
// survives a round trip through JSON without a lookup table that could drift.
type Action string

// The four actions, in the order they appear in the matrix.
const (
	ActionObserve  Action = "OBSERVE"
	ActionVerify   Action = "VERIFY"
	ActionEscalate Action = "ESCALATE"
	ActionHold     Action = "HOLD"
)

// SeverityLevel is how bad it is, as derived from the threat score.
type SeverityLevel string

// The severity axis of the decision matrix.
const (
	SeverityLow      SeverityLevel = "low"
	SeverityMedium   SeverityLevel = "medium"
	SeverityHigh     SeverityLevel = "high"
	SeverityCritical SeverityLevel = "critical"
)

// LikelihoodLevel is how likely it is, as derived from the same threat score.
type LikelihoodLevel string

// The likelihood axis of the decision matrix.
const (
	LikelihoodLow      LikelihoodLevel = "low"
	LikelihoodMedium   LikelihoodLevel = "medium"
	LikelihoodHigh     LikelihoodLevel = "high"
	LikelihoodVeryHigh LikelihoodLevel = "very_high"
)

// MatrixEntry maps severity × likelihood to an action.
type MatrixEntry struct {
	Severity   SeverityLevel
	Likelihood LikelihoodLevel
	Action     Action
}

// Matrix is the decision matrix for autonomous actions.
type Matrix struct {
	entries []MatrixEntry
}

// DefaultMatrix returns the default severity × likelihood → action matrix.
func DefaultMatrix() *Matrix {
	return &Matrix{
		entries: []MatrixEntry{
			{SeverityCritical, LikelihoodVeryHigh, ActionEscalate},
			{SeverityCritical, LikelihoodHigh, ActionEscalate},
			{SeverityCritical, LikelihoodMedium, ActionVerify},
			{SeverityCritical, LikelihoodLow, ActionVerify},
			{SeverityHigh, LikelihoodVeryHigh, ActionEscalate},
			{SeverityHigh, LikelihoodHigh, ActionVerify},
			{SeverityHigh, LikelihoodMedium, ActionVerify},
			{SeverityHigh, LikelihoodLow, ActionObserve},
			{SeverityMedium, LikelihoodVeryHigh, ActionVerify},
			{SeverityMedium, LikelihoodHigh, ActionVerify},
			{SeverityMedium, LikelihoodMedium, ActionObserve},
			{SeverityMedium, LikelihoodLow, ActionObserve},
			{SeverityLow, LikelihoodVeryHigh, ActionVerify},
			{SeverityLow, LikelihoodHigh, ActionObserve},
			{SeverityLow, LikelihoodMedium, ActionObserve},
			{SeverityLow, LikelihoodLow, ActionObserve},
		},
	}
}

// Decide returns the action for a given severity and likelihood.
func (m *Matrix) Decide(severity SeverityLevel, likelihood LikelihoodLevel) Action {
	for _, e := range m.entries {
		if e.Severity == severity && e.Likelihood == likelihood {
			return e.Action
		}
	}
	return ActionObserve
}

// scoreToSeverity converts a threat score (0-100) to a severity level.
func scoreToSeverity(score int) SeverityLevel {
	switch {
	case score >= 80:
		return SeverityCritical
	case score >= 60:
		return SeverityHigh
	case score >= 30:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// scoreToLikelihood converts a threat score (0-100) to a likelihood level.
func scoreToLikelihood(score int) LikelihoodLevel {
	switch {
	case score >= 80:
		return LikelihoodVeryHigh
	case score >= 60:
		return LikelihoodHigh
	case score >= 30:
		return LikelihoodMedium
	default:
		return LikelihoodLow
	}
}

// DecideFromScore returns the action based on a threat score.
func (m *Matrix) DecideFromScore(score int) Action {
	severity := scoreToSeverity(score)
	likelihood := scoreToLikelihood(score)
	return m.Decide(severity, likelihood)
}
