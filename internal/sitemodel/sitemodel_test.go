package sitemodel

import (
	"testing"
)

func TestPointInPolygon(t *testing.T) {
	poly := []Point{
		{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10},
	}

	tests := []struct {
		name     string
		p        Point
		expected bool
	}{
		{"inside", Point{X: 5, Y: 5}, true},
		{"on_edge", Point{X: 5, Y: 0}, true},
		{"on_vertex", Point{X: 0, Y: 0}, true},
		{"outside", Point{X: 15, Y: 5}, false},
		{"far_outside", Point{X: -5, Y: -5}, false},
		{"degenerate_polygon", Point{X: 5, Y: 5}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.p
			if tt.name == "degenerate_polygon" {
				// A two-point polygon is not a region and must not contain
				// anything, however it is asked.
				if got := PointInPolygon(p, poly[:2]); got {
					t.Errorf("a two-point polygon contained %v", p)
				}
				return
			}
			if got := PointInPolygon(p, poly); got != tt.expected {
				t.Errorf("PointInPolygon(%v) = %v, want %v", tt.p, got, tt.expected)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	site, err := LoadFromFile("../../poc/sitemap.yaml")
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if site.ID != "demo-site" {
		t.Errorf("site.ID = %s, want demo-site", site.ID)
	}
	if len(site.Cameras) != 2 {
		t.Errorf("len(site.Cameras) = %d, want 2", len(site.Cameras))
	}
	if len(site.Zones) != 3 {
		t.Errorf("len(site.Zones) = %d, want 3", len(site.Zones))
	}
	if len(site.Routes) != 1 {
		t.Errorf("len(site.Routes) = %d, want 1", len(site.Routes))
	}
	if len(site.Assets) != 1 {
		t.Errorf("len(site.Assets) = %d, want 1", len(site.Assets))
	}

	// The lookups the behaviour rules use.
	if zone := site.ZoneByID("restricted"); zone == nil || zone.Type != "restricted" {
		t.Error("ZoneByID(restricted) did not return the restricted zone")
	}
	if zone := site.ZoneByID("no-such-zone"); zone != nil {
		t.Error("ZoneByID returned a zone for an unknown id")
	}
	if route := site.RouteByID("route-01"); route == nil || len(route.Points) != 4 {
		t.Error("RouteByID(route-01) did not return the four-point route")
	}

	// A camera's zone list is what the site map in the UI renders, so it is
	// asserted where it is loaded rather than left to a visual check.
	cam := site.Cameras[0]
	if cam.ID != "cam-01" || len(cam.ZoneIDs) != 2 {
		t.Errorf("first camera = %+v, want cam-01 covering two zones", cam)
	}

	// A point inside the restricted zone is reported by the rule's own test.
	restricted := site.ZoneByID("restricted")
	if !PointInPolygon(Point{X: 600, Y: 200}, restricted.Polygon) {
		t.Error("a point the demo places inside the restricted zone was reported outside")
	}
}

func TestLoadFromBytes(t *testing.T) {
	site, err := LoadFromBytes([]byte(`
site:
  id: "test-site"
  cameras:
    - id: "cam-1"
      zone_ids: ["zone-1"]
      position:
        x: 100
        y: 100
  zones:
    - id: "zone-1"
      type: "restricted"
      polygon:
        - x: 0
          y: 0
        - x: 10
          y: 0
        - x: 10
          y: 10
        - x: 0
          y: 10
      name: "Test Zone"
  routes: []
  assets: []
`))
	if err != nil {
		t.Fatalf("LoadFromBytes failed: %v", err)
	}
	if site.ID != "test-site" {
		t.Errorf("site.ID = %s, want test-site", site.ID)
	}
	if len(site.Cameras) != 1 || len(site.Zones) != 1 {
		t.Errorf("loaded %d cameras and %d zones, want 1 and 1", len(site.Cameras), len(site.Zones))
	}
}

func TestLoadFromBytesErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"missing_site_id", "site:\n  cameras: []\n  zones: []\n  routes: []\n  assets: []\n"},
		{"missing_camera_id", "site:\n  id: t\n  cameras:\n    - zone_ids: [z1]\n  zones:\n    - id: z1\n      polygon:\n        - {x: 0, y: 0}\n        - {x: 1, y: 0}\n        - {x: 1, y: 1}\n"},
		{"zone_too_few_points", "site:\n  id: t\n  cameras: []\n  zones:\n    - id: z1\n      polygon:\n        - {x: 0, y: 0}\n        - {x: 1, y: 0}\n"},
		{"route_too_few_points", "site:\n  id: t\n  cameras: []\n  zones: []\n  routes:\n    - id: r1\n      points:\n        - {x: 0, y: 0}\n"},
		{"missing_asset_id", "site:\n  id: t\n  cameras: []\n  zones: []\n  routes: []\n  assets:\n    - name: a\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadFromBytes([]byte(tt.yaml)); err == nil {
				t.Error("LoadFromBytes accepted an invalid site")
			}
		})
	}
}
