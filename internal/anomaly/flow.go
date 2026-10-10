// Package anomaly provides deterministic behavior rules for the autonomy layer.
package anomaly

import (
	"math"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlation"
	"github.com/Sergey-Bar/Ai-Captain/internal/sitemodel"
)

const anomalyFlow = "flow"

// FlowRule detects flow violations (wrong direction on a route).
type FlowRule struct {
	RouteID     string
	MinSeverity string
	RuleID      string
}

// newFlowRule creates a new flow rule.
func newFlowRule(routeID string) *FlowRule {
	return &FlowRule{
		RouteID:     routeID,
		MinSeverity: "high",
		RuleID:      "flow-" + routeID,
	}
}

// Name implements Rule.
func (r *FlowRule) Name() string { return r.RuleID }

// Describe implements Rule.
func (r *FlowRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      r.RouteID,
		Type:        anomalyFlow,
		Threshold:   "direction",
		MinSeverity: r.MinSeverity,
	}
}

// Evaluate reports an anomaly for each entity travelling against the route's
// declared direction. `now` is accepted to satisfy the Rule interface; the
// violation is a fact about direction, not about the clock.
func (r *FlowRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, _ time.Time) []correlation.Anomaly {
	var anomalies []correlation.Anomaly
	route := site.RouteByID(r.RouteID)
	if route == nil || len(route.Points) < 2 {
		return anomalies
	}

	// Convert route points to correlation.Point
	routePoints := make([]correlation.Point, len(route.Points))
	for i, p := range route.Points {
		routePoints[i] = correlation.Point{X: p.X, Y: p.Y}
	}

	// Determine allowed direction vector
	allowedForward := r.routeDirection(routePoints[0], routePoints[len(routePoints)-1])

	for _, entity := range inc.Entities {
		if len(entity.BBoxHistory) < 2 {
			continue
		}

		// Check movement direction
		firstCenter := correlation.BBoxCenter(entity.BBoxHistory[0])
		lastCenter := correlation.BBoxCenter(entity.BBoxHistory[len(entity.BBoxHistory)-1])

		movement := r.routeDirection(firstCenter, lastCenter)
		if movement == (correlation.Point{}) {
			continue
		}

		// Check if movement is opposite to allowed direction
		dot := allowedForward.X*movement.X + allowedForward.Y*movement.Y
		normAllowed := math.Hypot(allowedForward.X, allowedForward.Y)
		normMovement := math.Hypot(movement.X, movement.Y)

		if normAllowed > 0 && normMovement > 0 {
			cosAngle := dot / (normAllowed * normMovement)
			// If cosAngle < 0, movement is generally opposite to allowed direction
			if cosAngle < -0.5 { // more than 120 degrees off
				// Verify entity is on or near the route
				onRoute := false
				for _, bbox := range entity.BBoxHistory {
					center := correlation.BBoxCenter(bbox)
					if r.pointNearRoute(center, routePoints) {
						onRoute = true
						break
					}
				}

				if onRoute {
					anomaly := correlation.Anomaly{
						Type:      "flow",
						RuleID:    r.RuleID,
						ZoneID:    "",
						CameraID:  entity.CameraID,
						EntityIDs: []string{entity.ID},
						FirstSeen: entity.FirstSeen,
						LastSeen:  entity.LastSeen,
						Severity:  r.MinSeverity,
						Details: map[string]any{
							"route_id":       r.RouteID,
							"allowed_dir":    r.RouteID + "-forward",
							"movement_angle": math.Acos(cosAngle) * 180 / math.Pi,
							"zone_type":      "route",
						},
					}
					anomalies = append(anomalies, anomaly)
				}
			}
		}
	}
	return anomalies
}

func (r *FlowRule) routeDirection(from, to correlation.Point) correlation.Point {
	dx := to.X - from.X
	dy := to.Y - from.Y
	norm := math.Hypot(dx, dy)
	if norm == 0 {
		return correlation.Point{}
	}
	return correlation.Point{X: dx / norm, Y: dy / norm}
}

func (r *FlowRule) pointNearRoute(p correlation.Point, routePoints []correlation.Point) bool {
	const maxDist = 50.0
	for i := 0; i < len(routePoints)-1; i++ {
		a := routePoints[i]
		b := routePoints[i+1]
		if pointToSegmentDist(p, a, b) <= maxDist {
			return true
		}
	}
	return false
}

func pointToSegmentDist(p, a, b correlation.Point) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	if dx == 0 && dy == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	projX := a.X + t*dx
	projY := a.Y + t*dy
	return math.Hypot(p.X-projX, p.Y-projY)
}
