// Package threat provides explainable scoring and autonomous playbook for the autonomy layer.
package threat

import (
	"time"
)

// ReasonStep is one step in a reason chain.
type ReasonStep struct {
	Key      string // "dwell>15s"
	Rule     string // rule name
	Factor   int    // score contribution
	Evidence string // short evidence
	At       time.Time
}

// ReasonChain is a chain of reasoning steps.
type ReasonChain struct {
	Steps []ReasonStep
	Why   string // human-readable summary
}

// AddStep adds a step to the reason chain.
func (rc *ReasonChain) AddStep(key, rule string, factor int, evidence string, at time.Time) {
	rc.Steps = append(rc.Steps, ReasonStep{
		Key:      key,
		Rule:     rule,
		Factor:   factor,
		Evidence: evidence,
		At:       at,
	})
	rc.rebuildWhy()
}

func (rc *ReasonChain) rebuildWhy() {
	if len(rc.Steps) == 0 {
		rc.Why = "no factors"
		return
	}
	var parts []string
	for _, s := range rc.Steps {
		parts = append(parts, s.Key)
	}
	rc.Why = "Triggered by: " + joinSteps(parts)
}

func joinSteps(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += " + " + parts[i]
	}
	return result
}

// TotalScore returns the sum of all factor scores.
func (rc *ReasonChain) TotalScore() int {
	total := 0
	for _, s := range rc.Steps {
		total += s.Factor
	}
	return total
}
