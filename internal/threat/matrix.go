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
//
// # The shape of the table
//
// The severity axis is the CLAIM: how bad it would be if the incident were
// real. The likelihood axis is the EVIDENCE: how likely it is to be real.
// They are two different facts about two different things, and the table is
// over both of them.
//
// The legacy engine derived BOTH axes from one score with the same
// thresholds, which made twelve of the sixteen cells unreachable and left
// `ActionHold` in no cell at all. The calibrated engine computes the two
// axes from different facts, so every cell is reachable.
//
// # What HOLD means
//
// HOLD is the corner where the claim is serious and the evidence here is
// thin. There are two ways the likelihood gets to the bottom of the scale:
// the evidence was thin, or the rule is noise AT THIS SITE and the site's
// own base rate caps it there. In both cases the answer is the same — do not
// spend a human on it, do not escalate it, keep it in the queue and watch
// it.
//
// It is the one action that is a decision a human can flip: HOLD says "this
// rule is noise here", and a site that disagrees can say so.
func DefaultMatrix() *Matrix {
	return &Matrix{
		entries: []MatrixEntry{
			{SeverityCritical, LikelihoodVeryHigh, ActionEscalate},
			{SeverityCritical, LikelihoodHigh, ActionEscalate},
			{SeverityCritical, LikelihoodMedium, ActionVerify},
			{SeverityCritical, LikelihoodLow, ActionHold},
			{SeverityHigh, LikelihoodVeryHigh, ActionEscalate},
			{SeverityHigh, LikelihoodHigh, ActionVerify},
			{SeverityHigh, LikelihoodMedium, ActionVerify},
			{SeverityHigh, LikelihoodLow, ActionHold},
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
//
// It is the LEGACY path: one number in, one action out, with both axes
// derived from the same thresholds. It cannot reach `ActionHold`, because a
// score carries no evidence axis and HOLD is the cell where the claim is
// serious and the evidence here is thin — a claim the score cannot make.
//
// The calibrated engine computes the two axes separately and calls `Decide`.
func (m *Matrix) DecideFromScore(score int) Action {
	severity := scoreToSeverity(score)
	likelihood := scoreToLikelihood(score)
	return m.Decide(severity, likelihood)
}
