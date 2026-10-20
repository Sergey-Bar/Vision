package anomaly

import (
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// The presence rule.
//
// It is the PRODUCT's anomaly, and it is not the same thing as the POC's dwell.
//
// The POC's dwell is an ENTITY's: something that stayed in a zone, tracked by
// its box, matched across frames by a centroid tracker. The product's record
// deliberately does not keep a person's position in a frame -- a position is a
// privacy-bearing field and the adapter's redaction allow-list does not carry
// one -- so the product cannot track an entity, and a product that claimed to
// would be claiming to know something it threw away.
//
// What the product CAN see is a CLASS present in a ZONE, seen by a DEVICE, for
// longer than the window. That is a weaker claim than an entity's dwell, and
// it is named differently for that reason: `presence`, not `dwell`.
//
// The weakness is the point rather than a compromise to apologise for. "A class
// keeps being reported in a zone" is a statement the product's own record
// supports, and the incident it raises is raised UNVERIFIED -- which is the
// product's word for "a rule matched and no second source agreed".
const (
	// anomalyPresence is the anomaly type this rule emits.
	anomalyPresence = "presence"
)

// presenceRule fires when a class has been present in a zone for the window.
//
// The rule is ONE and not one per zone, because the zone decides the severity
// and the multiplier rather than the rule: a presence in a restricted zone and a
// presence in a monitored zone are the same observation with different
// consequences, and two rules for one observation is a table waiting to drift.
type presenceRule struct {
	ruleID    string
	threshold time.Duration
}

// newPresenceRule builds the rule.
func newPresenceRule(threshold time.Duration) *presenceRule {
	return &presenceRule{ruleID: "presence", threshold: threshold}
}

// Name returns the rule name.
func (r *presenceRule) Name() string { return r.ruleID }

// Describe returns the rule's own account of itself.
func (r *presenceRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      "",
		Type:        anomalyPresence,
		Threshold:   r.threshold.String(),
		MinSeverity: "medium",
	}
}

// Evaluate returns the anomaly when the class has been present long enough.
//
// The zone comes from the incident's provenance rather than from the rule,
// because the rule is one and the zone is whatever the incident happened in.
func (r *presenceRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, now time.Time) []correlation.Anomaly {
	if inc == nil {
		return nil
	}
	zoneID := inc.Provenance.ZoneID
	if zoneID == "" {
		return nil
	}
	// The window is how long the incident has been open. A presence that has
	// lasted less than the threshold is a presence and not yet a dwell, and the
	// difference between the two is the difference between a class someone walked
	// past and a class that stayed.
	present := inc.LastSeen.Sub(inc.FirstSeen)
	if present < r.threshold {
		return nil
	}
	var zoneType string
	severity := "medium"
	if site != nil {
		if zone := site.ZoneByID(zoneID); zone != nil {
			zoneType = zone.Type
			switch zone.Type {
			case "restricted":
				severity = "high"
			case "choke":
				severity = "medium"
			default:
				severity = "low"
			}
		}
	}
	return []correlation.Anomaly{{
		Type:      anomalyPresence,
		RuleID:    r.ruleID,
		ZoneID:    zoneID,
		CameraID:  firstOf(inc.CameraIDs),
		FirstSeen: inc.FirstSeen,
		LastSeen:  now,
		Severity:  severity,
		Details: map[string]any{
			"present_time_seconds": present.Seconds(),
			"threshold_seconds":    r.threshold.Seconds(),
			"zone_type":            zoneType,
			"devices":              len(inc.CameraIDs),
		},
	}}
}

// presenceThreshold is how long a class must keep being reported in a zone
// before the presence is a finding.
//
// It is three seconds, and the reason is what the product's evidence actually
// is. A presence is measured over the CORROBORATING EVENTS, and the
// correlator reads a bounded number of them: at a busy site's event rate the
// candidates it can hold cover a few seconds of the site's history, so a
// threshold longer than that could never be reached and the rule would report
// nothing for ever.
//
// Three seconds is not a long time to be somewhere. It is, however, seventy
// detections of the same class in the same zone at a site's event rate, and a
// thing reported seventy times is not noise.
const presenceThreshold = 3 * time.Second

// PresenceRuleSet builds the rule set a PRODUCT runs.
//
// It is one rule and not `DefaultRuleSet`'s four, and the difference is the
// whole point: `DefaultRuleSet` is written for a correlated incident with
// tracked entities, and the product's record has no entities to track. A product
// that ran the entity rules over an incident with no entities would report
// every rule as having found nothing, and "nothing" is not a finding.
//
// The window is the one the correlator actually searched, so the autonomy
// layer and the rule bundle agree about how long is long. The threshold is
// bounded by it, because a presence longer than the window cannot be measured
// from the evidence the window covers.
func PresenceRuleSet(window time.Duration) *RuleSet {
	if window <= 0 {
		window = 15 * time.Second
	}
	threshold := presenceThreshold
	if half := window / 2; half < threshold {
		threshold = half
	}
	return newRuleSet(newPresenceRule(threshold))
}

// firstOf returns the first element, or the empty string.
func firstOf(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}
