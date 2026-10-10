// Package sitemodel loads and provides site topology for the autonomy layer.
package sitemodel

import "sort"

// Site represents a physical site with cameras, zones, routes and assets.
type Site struct {
	ID      string
	Cameras []Camera
	Zones   []Zone
	Routes  []Route
	Assets  []Asset
}

// Camera represents a camera in the site.
type Camera struct {
	ID       string
	ZoneIDs  []string
	Position Point
}

// Zone represents a zone in the site.
type Zone struct {
	ID      string
	Type    string // restricted | choke | monitor | asset
	Polygon []Point
	Name    string
}

// Route represents a route/path in the site.
type Route struct {
	ID               string
	Points           []Point
	AllowedDirection string // both | forward | backward
}

// Asset represents a physical asset in the site.
type Asset struct {
	ID       string
	Name     string
	Type     string
	ZoneID   string
	Position Point
	Metadata map[string]any
}

// ZoneByID returns a zone by its ID.
func (s *Site) ZoneByID(id string) *Zone {
	for i := range s.Zones {
		if s.Zones[i].ID == id {
			return &s.Zones[i]
		}
	}
	return nil
}

// CamerasForZone is THE camera→zone lookup: the ids declared to cover a zone,
// sorted and deduplicated.
//
// There is exactly one source for this — Camera.ZoneIDs — and this is the one
// accessor. Before it, the same lookup was implemented in four places with two
// orderings and two dedupe behaviours, which matters beyond tidiness: a camera
// whose zone_ids repeats a zone produced a different declared list in the
// artifact that feeds a digest than in the brief a human reads, and the
// comments claiming "exactly one source" were false while it happened.
//
// One accessor, one order, one dedupe rule, and every consumer reads the same
// list — including the POC's coverage lines, anomaly's coverage rule, the
// configurator's artifact and its CLI.
func (s *Site) CamerasForZone(zoneID string) []string {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(s.Cameras))
	for _, cam := range s.Cameras {
		for _, z := range cam.ZoneIDs {
			if z == zoneID && !seen[cam.ID] {
				seen[cam.ID] = true
				out = append(out, cam.ID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// RouteByID returns a route by its ID.
func (s *Site) RouteByID(id string) *Route {
	for i := range s.Routes {
		if s.Routes[i].ID == id {
			return &s.Routes[i]
		}
	}
	return nil
}
