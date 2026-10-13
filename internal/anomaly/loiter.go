// Package anomaly provides deterministic behavior rules for the autonomy layer.
package anomaly

import (
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// LoiterRule detects loitering pattern (revisits / back-and-forth) in a zone.
type LoiterRule struct {
	ZoneID      string
	MinRevisits int
	TimeWindow  time.Duration
	MaxDistance float64
	MinSeverity string
	RuleID      string
}

// newLoiterRule creates a new loiter rule.
func newLoiterRule(zoneID string, minRevisits int, timeWindow time.Duration) *LoiterRule {
	return &LoiterRule{
		ZoneID:      zoneID,
		MinRevisits: minRevisits,
		TimeWindow:  timeWindow,
		MaxDistance: 100.0,
		MinSeverity: "medium",
		RuleID:      "loiter-" + zoneID,
	}
}

// Name implements Rule.
func (r *LoiterRule) Name() string { return r.RuleID }

// Describe implements Rule.
func (r *LoiterRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      r.ZoneID,
		Type:        anomalyLoiter,
		Threshold:   fmt.Sprintf("%d sightings / %s", r.MinRevisits, formatThreshold(r.TimeWindow)),
		MinSeverity: r.MinSeverity,
	}
}

// Evaluate reports an anomaly for each entity that enters ZoneID at least
// MinRevisits times inside TimeWindow. `now` is accepted to satisfy the Rule
// interface; the loiter pattern is bounded by the window, not by the clock.
func (r *LoiterRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, _ time.Time) []correlation.Anomaly {
	var anomalies []correlation.Anomaly
	zone := site.ZoneByID(r.ZoneID)
	if zone == nil {
		return anomalies
	}

	for _, entity := range inc.Entities {
		if entity.CameraID == "" {
			continue
		}

		// Count revisits to the zone
		revisits := 0
		var lastVisit time.Time
		firstVisit := time.Time{}

		for _, bbox := range entity.BBoxHistory {
			center := correlation.BBoxCenter(bbox)
			siteCenter := sitemodel.Point{X: center.X, Y: center.Y}
			if sitemodel.PointInPolygon(siteCenter, zone.Polygon) {
				revisits++
				if firstVisit.IsZero() {
					firstVisit = entity.FirstSeen
				}
				lastVisit = entity.LastSeen
			}
		}

		if revisits >= r.MinRevisits && !firstVisit.IsZero() {
			windowDuration := lastVisit.Sub(firstVisit)
			if windowDuration <= r.TimeWindow {
				anomaly := correlation.Anomaly{
					Type:      "loiter",
					RuleID:    r.RuleID,
					ZoneID:    r.ZoneID,
					CameraID:  entity.CameraID,
					EntityIDs: []string{entity.ID},
					FirstSeen: firstVisit,
					LastSeen:  lastVisit,
					Severity:  r.MinSeverity,
					Details: map[string]any{
						"revisits":           revisits,
						"min_revisits":       r.MinRevisits,
						"window_seconds":     windowDuration.Seconds(),
						"max_window_seconds": r.TimeWindow.Seconds(),
						"zone_type":          zone.Type,
					},
				}
				anomalies = append(anomalies, anomaly)
			}
		}
	}
	return anomalies
}
