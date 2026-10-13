package main

import (
	"context"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/sitemodel"
	"github.com/Sergey-Bar/vision/poc/ws"
)

// The alarm budget.
//
// The complaint this answers is the 91% who assigned a dollar value to
// minimizing false-positives: an escalation budget spent on the wrong event
// is not a rounding error, it is what trains a control room to ignore the
// screen. The playbook already owns the number (MaxEscalationsPerHour); this
// names where the site sits against it, from the escalations that actually
// happened, so a shift that is burning its budget can see it while it is
// happening rather than in the monthly report.

// The three bands, plus the honest fourth answer for "no budget configured".
const (
	alarmWithin      = "within_budget"
	alarmApproaching = "approaching_budget"
	alarmOver        = "over_budget"
	alarmUnbounded   = "unbounded"
)

// alarmBand places a count against a budget. Pure: two numbers in, one named
// band out, so the dashboard and any future console disagree about nothing.
func alarmBand(escalations, budget int) string {
	if budget <= 0 {
		return alarmUnbounded
	}
	switch {
	case escalations > budget:
		return alarmOver
	case escalations == budget:
		return alarmApproaching
	}
	return alarmWithin
}

// recordEscalation stamps the moment an escalation happened, pruning the
// times that have fallen out of the window so the ring stays bounded.
func (s *StreamIngest) recordEscalation(now time.Time) {
	cutoff := now.Add(-time.Hour)
	s.escMu.Lock()
	defer s.escMu.Unlock()
	times := make([]time.Time, 0, len(s.escalationTimes)+1)
	for _, t := range s.escalationTimes {
		if t.After(cutoff) {
			times = append(times, t)
		}
	}
	times = append(times, now)
	s.escalationTimes = times
}

// escalationsWithin counts the escalations in the last hour, and prunes what
// has expired so a long-running POC does not accumulate a history nobody
// reads.
func (s *StreamIngest) escalationsWithin(now time.Time) int {
	cutoff := now.Add(-time.Hour)
	s.escMu.Lock()
	defer s.escMu.Unlock()
	kept := 0
	for _, t := range s.escalationTimes {
		if t.After(cutoff) {
			kept++
		}
	}
	return kept
}

// patrolLoop walks the site's routes on the configured interval: a named
// reminder broadcast to the dashboard, a duty-log line recording that the
// reminder was issued, and a check endpoint for the operator to say they did
// the walk. The patrol is what turns "route-01 is overdue" from tribal
// knowledge into a line in the hand-over.
//
// The reminders and the check are two halves of the same record: the note the
// operator leaves when they check a camera is what makes the reminder
// accountable rather than nagging.
func (s *StreamIngest) patrolLoop(ctx context.Context) {
	if s.siteModel == nil || len(s.siteModel.Routes) == 0 {
		return
	}
	ticker := time.NewTicker(s.patrolInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			for _, route := range s.siteModel.Routes {
				s.wsHub.Broadcast(ws.Message{
					Type: "patrol",
					Data: map[string]any{
						"kind":     "due",
						"route_id": route.ID,
						"points":   len(route.Points),
						"cameras":  camerasForRoute(s.siteModel, route.ID),
						"due":      now.Format(time.RFC3339),
					},
					Timestamp: now,
				})
				s.logPatrolNote("patrol due: walk "+route.ID, route.ID)
			}
		}
	}
}

func (s *StreamIngest) patrolInterval() time.Duration {
	if s.patrolEvery > 0 {
		return s.patrolEvery
	}
	return 5 * time.Minute
}

// camerasForRoute names the cameras declared to cover a zone the route
// actually passes through, so a patrol reminder says which cameras the walk is
// supposed to look at. The route's zones come from the site's own polygons
// and the route's points: the site model has no route→zone declaration, and
// the geometry it does have is what a reminder may use.
func camerasForRoute(site *sitemodel.Site, routeID string) []string {
	var route *sitemodel.Route
	for i := range site.Routes {
		if site.Routes[i].ID == routeID {
			route = &site.Routes[i]
			break
		}
	}
	if route == nil {
		return nil
	}
	passed := make(map[string]bool)
	for _, point := range route.Points {
		for _, zone := range site.Zones {
			if sitemodel.PointInPolygon(point, zone.Polygon) {
				passed[zone.ID] = true
			}
		}
	}
	// The declaration comes from the one accessor, so the patrol reminder's
	// camera list and any coverage view derived from the same zone agree —
	// which a local walk of cam.ZoneIDs (with its own order and dedupe rule)
	// cannot promise.
	seen := map[string]bool{}
	var out []string
	for zoneID := range passed {
		for _, cam := range site.CamerasForZone(zoneID) {
			if !seen[cam] {
				seen[cam] = true
				out = append(out, cam)
			}
		}
	}
	sort.Strings(out)
	return out
}
