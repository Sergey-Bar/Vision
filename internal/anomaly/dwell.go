// Package anomaly provides deterministic behavior rules for the autonomy layer.
package anomaly

import (
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// The anomaly types this package's rules emit. Unexported, because a caller
// that wants a type name asks a rule to Describe itself: the names are the
// rules' own, and exporting them invited a second table beside the rules.
const (
	anomalyDwell    = "dwell"
	anomalyLoiter   = "loiter"
	anomalyTailgate = "tailgate"
)

// Rule is the interface for anomaly detection rules.
type Rule interface {
	// Name returns the rule name.
	Name() string
	// Describe returns the rule's own account of itself: the zone it fires
	// for, the anomaly type it emits, what it measures and the severity it
	// starts at. The configurator renders these, so they come from the rule
	// rather than from a table maintained beside it.
	Describe() RuleSummary
	// Evaluate evaluates the rule against an incident and returns anomalies.
	Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, now time.Time) []correlation.Anomaly
}

// RuleSet is a collection of rules evaluated sequentially.
type RuleSet struct {
	rules []Rule
}

// newRuleSet creates a new rule set.
func newRuleSet(rules ...Rule) *RuleSet {
	return &RuleSet{rules: rules}
}

// Add appends rules to a rule set. It is how a caller with knowledge the
// site model does not hold — camera health, for the coverage rule — extends
// the rules DefaultRuleSet built from declarations.
func (rs *RuleSet) Add(rules ...Rule) {
	rs.rules = append(rs.rules, rules...)
}

// Rules returns the rule set's rules, for introspection and tests.
func (rs *RuleSet) Rules() []Rule { return rs.rules }

// Evaluate runs all rules and returns all anomalies.
func (rs *RuleSet) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, now time.Time) []correlation.Anomaly {
	var allAnomalies []correlation.Anomaly
	for _, rule := range rs.rules {
		anomalies := rule.Evaluate(inc, site, now)
		allAnomalies = append(allAnomalies, anomalies...)
	}
	return allAnomalies
}

// DwellRule detects dwell in a zone beyond a threshold.
type DwellRule struct {
	ZoneID      string
	Threshold   time.Duration
	MinSeverity string
	RuleID      string
}

// newDwellRule creates a new dwell rule.
func newDwellRule(zoneID string, threshold time.Duration) *DwellRule {
	return &DwellRule{
		ZoneID:      zoneID,
		Threshold:   threshold,
		MinSeverity: "medium",
		RuleID:      "dwell-" + zoneID,
	}
}

// Name implements Rule.
func (r *DwellRule) Name() string { return r.RuleID }

// Describe implements Rule.
func (r *DwellRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      r.ZoneID,
		Type:        anomalyDwell,
		Threshold:   formatThreshold(r.Threshold),
		MinSeverity: r.MinSeverity,
	}
}

// Evaluate reports an anomaly for each entity that has been inside ZoneID for
// at least Threshold by the time `now` arrives. Unlike the other rules this one
// reads the clock, because dwell is defined by how long something has been
// somewhere.
func (r *DwellRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, now time.Time) []correlation.Anomaly {
	var anomalies []correlation.Anomaly
	zone := site.ZoneByID(r.ZoneID)
	if zone == nil {
		return anomalies
	}

	for _, entity := range inc.Entities {
		// Check if entity was in this zone
		inZone := false
		for _, bbox := range entity.BBoxHistory {
			center := correlation.BBoxCenter(bbox)
			siteCenter := sitemodel.Point{X: center.X, Y: center.Y}
			if sitemodel.PointInPolygon(siteCenter, zone.Polygon) {
				inZone = true
				break
			}
		}

		if !inZone {
			continue
		}

		dwellTime := now.Sub(entity.FirstSeen)
		if dwellTime >= r.Threshold {
			anomaly := correlation.Anomaly{
				Type:      "dwell",
				RuleID:    r.RuleID,
				ZoneID:    r.ZoneID,
				CameraID:  entity.CameraID,
				EntityIDs: []string{entity.ID},
				FirstSeen: entity.FirstSeen,
				LastSeen:  now,
				Severity:  r.MinSeverity,
				Details: map[string]any{
					"dwell_time_seconds": dwellTime.Seconds(),
					"threshold_seconds":  r.Threshold.Seconds(),
					"zone_type":          zone.Type,
				},
			}
			anomalies = append(anomalies, anomaly)
		}
	}
	return anomalies
}
