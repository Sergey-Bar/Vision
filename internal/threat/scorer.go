// Package threat provides explainable scoring and autonomous playbook for the autonomy layer.
package threat

import (
	"fmt"

	"github.com/Sergey-Bar/vision/internal/correlation"
)

// ScorerConfig configures the threat scorer.
type ScorerConfig struct {
	// Base scores for anomaly types
	DwellScore    int
	LoiterScore   int
	FlowScore     int
	TailgateScore int
	// Multipliers
	ZoneTypeMultipliers map[string]float64
	SeverityMultipliers map[string]float64
	// Maximum score
	MaxScore int
}

// DefaultScorerConfig returns default scoring configuration.
func DefaultScorerConfig() ScorerConfig {
	return ScorerConfig{
		DwellScore:    20,
		LoiterScore:   15,
		FlowScore:     25,
		TailgateScore: 30,
		ZoneTypeMultipliers: map[string]float64{
			"restricted": 2.0,
			"choke":      1.5,
			"monitor":    1.0,
			"asset":      1.5,
			"route":      1.2,
		},
		SeverityMultipliers: map[string]float64{
			"low":      0.5,
			"medium":   1.0,
			"high":     1.5,
			"critical": 2.0,
		},
		MaxScore: 100,
	}
}

// Scorer calculates threat scores from anomalies.
type Scorer struct {
	cfg ScorerConfig
}

// NewScorer creates a new threat scorer.
func NewScorer(cfg ScorerConfig) *Scorer {
	return &Scorer{cfg: cfg}
}

// Score calculates the threat score and reason chain for an incident.
func (s *Scorer) Score(inc *correlation.CorrelatedIncident) (int, ReasonChain) {
	chain := ReasonChain{Steps: []ReasonStep{}}

	for _, anomaly := range inc.Anomalies {
		baseScore := s.baseScore(anomaly.Type)
		if baseScore == 0 {
			continue
		}

		// The zone type is read with a comma-ok, not a bare assertion. A
		// rule outside the four built-in ones — a bundle, a customer's
		// custom rule, the baseline coverage rule — may carry an anomaly
		// whose Details have no zone_type, and a scorer that panics on it
		// takes the whole autonomy layer down for one missing detail.
		zoneType, _ := anomaly.Details["zone_type"].(string)
		zoneMultiplier := s.cfg.ZoneTypeMultipliers[zoneType]
		if zoneMultiplier == 0 {
			zoneMultiplier = 1.0
		}

		severityMultiplier := s.cfg.SeverityMultipliers[anomaly.Severity]
		if severityMultiplier == 0 {
			severityMultiplier = 1.0
		}

		factor := int(float64(baseScore) * zoneMultiplier * severityMultiplier)
		chain.AddStep(
			anomaly.Type+"_score",
			anomaly.RuleID,
			factor,
			zoneType+" zone, "+anomaly.Severity+" severity",
			anomaly.LastSeen,
		)
	}

	// Add correlation bonus for multi-camera incidents. The evidence string is
	// the count rendered as decimal, because `string(rune(n))` is the character
	// whose code point is n and would print a control character instead.
	if len(inc.CameraIDs) > 1 {
		chain.AddStep(
			"multi_camera",
			"correlation",
			10,
			fmt.Sprintf("incident spans %d cameras", len(inc.CameraIDs)),
			inc.LastSeen,
		)
	}

	// Add entity count factor
	if len(inc.Entities) > 1 {
		chain.AddStep(
			"multi_entity",
			"correlation",
			5*len(inc.Entities),
			fmt.Sprintf("%d entities correlated", len(inc.Entities)),
			inc.LastSeen,
		)
	}

	score := chain.TotalScore()
	if score > s.cfg.MaxScore {
		score = s.cfg.MaxScore
	}
	return score, chain
}

func (s *Scorer) baseScore(anomalyType string) int {
	switch anomalyType {
	case "dwell":
		return s.cfg.DwellScore
	case "loiter":
		return s.cfg.LoiterScore
	case "flow":
		return s.cfg.FlowScore
	case "tailgate":
		return s.cfg.TailgateScore
	default:
		return 0
	}
}
