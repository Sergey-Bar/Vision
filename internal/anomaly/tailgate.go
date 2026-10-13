// Package anomaly provides deterministic behavior rules for the autonomy layer.
package anomaly

import (
	"math"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// TailgateRule detects tailgate-like entry (two entities close together).
type TailgateRule struct {
	ZoneID      string
	MaxDistance float64
	MaxTimeGap  time.Duration
	MinSeverity string
	RuleID      string
}

// newTailgateRule creates a new tailgate rule.
func newTailgateRule(zoneID string, maxDistance float64, maxTimeGap time.Duration) *TailgateRule {
	return &TailgateRule{
		ZoneID:      zoneID,
		MaxDistance: maxDistance,
		MaxTimeGap:  maxTimeGap,
		MinSeverity: "high",
		RuleID:      "tailgate-" + zoneID,
	}
}

// Name implements Rule.
func (r *TailgateRule) Name() string { return r.RuleID }

// Describe implements Rule.
func (r *TailgateRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      r.ZoneID,
		Type:        anomalyTailgate,
		Threshold:   formatThreshold(r.MaxTimeGap),
		MinSeverity: r.MinSeverity,
	}
}

// Evaluate reports an anomaly for each pair of entities inside ZoneID that are
// within MaxDistance of one another and entered within MaxTimeGap. `now` is
// accepted to satisfy the Rule interface; the pairing is a fact about space and
// time, not about the clock.
func (r *TailgateRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, _ time.Time) []correlation.Anomaly {
	var anomalies []correlation.Anomaly
	zone := site.ZoneByID(r.ZoneID)
	if zone == nil {
		return anomalies
	}

	// Get entities that entered this zone
	zoneEntities := make([]*correlation.EntityRef, 0)
	for i := range inc.Entities {
		entity := &inc.Entities[i]
		inZone := false
		for _, bbox := range entity.BBoxHistory {
			center := correlation.BBoxCenter(bbox)
			siteCenter := sitemodel.Point{X: center.X, Y: center.Y}
			if sitemodel.PointInPolygon(siteCenter, zone.Polygon) {
				inZone = true
				break
			}
		}
		if inZone {
			zoneEntities = append(zoneEntities, entity)
		}
	}

	// Check pairs for proximity in space and time
	for i := 0; i < len(zoneEntities); i++ {
		for j := i + 1; j < len(zoneEntities); j++ {
			e1 := zoneEntities[i]
			e2 := zoneEntities[j]

			// Check time proximity
			timeDiff := e1.FirstSeen.Sub(e2.FirstSeen)
			if timeDiff < 0 {
				timeDiff = -timeDiff
			}
			if timeDiff > r.MaxTimeGap {
				continue
			}

			// Check spatial proximity (use last known positions)
			if len(e1.BBoxHistory) == 0 || len(e2.BBoxHistory) == 0 {
				continue
			}
			center1 := correlation.BBoxCenter(e1.BBoxHistory[len(e1.BBoxHistory)-1])
			center2 := correlation.BBoxCenter(e2.BBoxHistory[len(e2.BBoxHistory)-1])
			dist := math.Hypot(center1.X-center2.X, center1.Y-center2.Y)

			if dist <= r.MaxDistance {
				anomaly := correlation.Anomaly{
					Type:      "tailgate",
					RuleID:    r.RuleID,
					ZoneID:    r.ZoneID,
					CameraID:  e1.CameraID,
					EntityIDs: []string{e1.ID, e2.ID},
					FirstSeen: minTime(e1.FirstSeen, e2.FirstSeen),
					LastSeen:  maxTime(e1.LastSeen, e2.LastSeen),
					Severity:  r.MinSeverity,
					Details: map[string]any{
						"distance":       dist,
						"max_distance":   r.MaxDistance,
						"time_gap":       timeDiff.Seconds(),
						"max_time_gap":   r.MaxTimeGap.Seconds(),
						"entity_classes": []string{e1.Class, e2.Class},
						"zone_type":      zone.Type,
					},
				}
				anomalies = append(anomalies, anomaly)
			}
		}
	}
	return anomalies
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
