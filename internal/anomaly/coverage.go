package anomaly

import (
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// Coverage: which zones is nobody actually watching?
//
// The survey's other number is 65.6% naming "corroborate an alert with a
// second camera's sighting" as a required feature, and the quiet corollary is
// that a zone whose every declared camera is dead is a zone the system
// reports nothing from — a silence that reads as "all clear". This file
// makes that silence say what it is.
//
// The rule that governs it, like SecondSight: declaration-based, because the
// site model's Camera.ZoneIDs is the only declared camera→zone link there is.
// Nothing here invents FOV geometry, and nothing here treats a camera we
// cannot see as a camera that saw nothing.

// ZoneCoverage is one zone's coverage verdict.
type ZoneCoverage struct {
	ZoneID          string   `json:"zone_id"`
	ZoneType        string   `json:"zone_type"`
	DeclaredCameras []string `json:"declared_cameras"`
	DegradedCameras []string `json:"degraded_cameras,omitempty"`
	UnderObserved   bool     `json:"under_observed"`
	Unanswerable    bool     `json:"unanswerable"`
}

// UnderObservedZones reports every zone in the site with what it takes to
// know whether anybody is watching it.
//
// The declared cameras come from the one accessor that owns the lookup,
// sitemodel.Site.CamerasForZone, so the coverage verdict and the artifact a
// human reads agree about which cameras cover a zone.
//
// A zone is under-observed when every camera declared to cover it is
// degraded — stale, silent or blind. A zone with no camera declared for it is
// unanswerable rather than under-observed: nobody has said which cameras
// cover it, and the honest answer is that the declaration cannot say, not
// that the zone is fine.
//
// degraded reports whether one camera is degraded. A camera the caller has no
// answer for counts as degraded, which is the direction that fails safe: the
// site prefers "nobody is watching this zone" over "this zone is covered" for
// a camera it cannot see.
func UnderObservedZones(site *sitemodel.Site, degraded func(cameraID string) bool) []ZoneCoverage {
	if site == nil {
		return nil
	}
	out := make([]ZoneCoverage, 0, len(site.Zones))
	for _, zone := range site.Zones {
		cams := site.CamerasForZone(zone.ID)
		cov := ZoneCoverage{
			ZoneID:          zone.ID,
			ZoneType:        zone.Type,
			DeclaredCameras: cams,
			Unanswerable:    len(cams) == 0,
		}
		if degraded != nil {
			for _, cam := range cams {
				if degraded(cam) {
					cov.DegradedCameras = append(cov.DegradedCameras, cam)
				}
			}
		}
		cov.UnderObserved = !cov.Unanswerable && len(cov.DegradedCameras) == len(cams)
		out = append(out, cov)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ZoneID < out[j].ZoneID })
	return out
}

// AnomalyUnderObserved is the anomaly type the coverage rule emits. It
// carries no score: coverage is evidence about the site's visibility, not
// evidence about the intrusion, and letting it move a threat score would
// mean a dark camera made an incident look worse. The scorer's own base
// table returns 0 for it, which is why the number does not move.
const AnomalyUnderObserved = "under_observed"

// CoverageRule is the baseline anomaly rule: an incident that happened in a
// zone nobody is watching carries an `under_observed` anomaly naming the
// zone and its declared cameras, so the decision is made with the site's own
// blind spot in front of it.
type CoverageRule struct {
	RuleID string
	// Degraded is the camera-health source, injected by the caller so this
	// rule stays pure: the site model supplies declarations, the caller
	// supplies health, and neither is guessed at.
	Degraded func(cameraID string) bool
}

// NewCoverageRule builds the baseline coverage rule.
func NewCoverageRule(degraded func(cameraID string) bool) *CoverageRule {
	return &CoverageRule{RuleID: "coverage-" + AnomalyUnderObserved, Degraded: degraded}
}

// Name implements Rule.
func (r *CoverageRule) Name() string { return r.RuleID }

// Describe implements Rule. It fires for whichever of the site's zones is
// under-observed at the time, so the zone is named as the site's zones rather
// than as one id.
func (r *CoverageRule) Describe() RuleSummary {
	return RuleSummary{
		ZoneID:      "under-observed zones",
		Type:        AnomalyUnderObserved,
		Threshold:   "every declared camera degraded",
		MinSeverity: "info",
	}
}

// Evaluate attaches an under-observed anomaly for each zone of the incident
// whose every declared camera is degraded.
func (r *CoverageRule) Evaluate(inc *correlation.CorrelatedIncident, site *sitemodel.Site, now time.Time) []correlation.Anomaly {
	var anomalies []correlation.Anomaly
	if r.Degraded == nil {
		return anomalies
	}
	for _, cov := range UnderObservedZones(site, r.Degraded) {
		if cov.Unanswerable {
			continue
		}
		if !cov.UnderObserved {
			continue
		}
		// Which of the incident's entities were in this zone? A coverage
		// finding attaches to what it covers, not to every incident on the
		// site at the time.
		var entities []string
		for _, e := range inc.Entities {
			zone := site.ZoneByID(cov.ZoneID)
			if zone == nil {
				continue
			}
			for _, bbox := range e.BBoxHistory {
				center := correlation.BBoxCenter(bbox)
				if sitemodel.PointInPolygon(sitemodel.Point{X: center.X, Y: center.Y}, zone.Polygon) {
					entities = append(entities, e.ID)
					break
				}
			}
		}
		if len(entities) == 0 {
			continue
		}
		cameraID := ""
		if len(inc.CameraIDs) > 0 {
			cameraID = inc.CameraIDs[0]
		}
		anomalies = append(anomalies, correlation.Anomaly{
			Type:      AnomalyUnderObserved,
			RuleID:    r.RuleID,
			ZoneID:    cov.ZoneID,
			CameraID:  cameraID,
			EntityIDs: entities,
			FirstSeen: inc.FirstSeen,
			LastSeen:  now,
			Severity:  "info",
			Details: map[string]any{
				"zone_type":        cov.ZoneType,
				"declared_cameras": cov.DeclaredCameras,
				"degraded_cameras": cov.DegradedCameras,
			},
		})
	}
	return anomalies
}

// String renders a coverage verdict for logs and the brief.
func (z ZoneCoverage) String() string {
	switch {
	case z.Unanswerable:
		return fmt.Sprintf("%s: no camera is declared to cover this zone", z.ZoneID)
	case z.UnderObserved:
		return fmt.Sprintf("%s: every declared camera (%s) is degraded", z.ZoneID, joinOr(z.DeclaredCameras))
	default:
		return fmt.Sprintf("%s: covered by %s", z.ZoneID, joinOr(z.DeclaredCameras))
	}
}

func joinOr(list []string) string {
	if len(list) == 0 {
		return "–"
	}
	return fmt.Sprint(list) // [a b] — the shape a log line can quote
}
