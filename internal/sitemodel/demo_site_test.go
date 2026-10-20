package sitemodel

import "testing"

// TestTheDemoSiteModelLoads is the demo's own site map.
//
// It is a file in the repository that the demo points the product at, and a
// file the product cannot parse is a demo that starts by refusing to start.
// The check is here rather than in the demo script because a file that a Go
// program reads should be read by a Go test.
func TestTheDemoSiteModelLoads(t *testing.T) {
	site, err := LoadFromFile("../../deploy/demo-site.yaml")
	if err != nil {
		t.Fatalf("the demo's site model does not load: %v", err)
	}
	if site.ID != "demo-site" {
		t.Errorf("the demo's site model is for %q, want demo-site", site.ID)
	}
	if len(site.Zones) == 0 {
		t.Error("the demo's site model has no zones, so the console's map would be empty")
	}
	if len(site.Cameras) == 0 {
		t.Error("the demo's site model has no cameras, so the console's map would be empty")
	}
}

// TestThePocSiteModelLoads is the same check for the POC's own site map.
//
// It is here because the two files have the same shape, and if the loader
// cannot read one of them then the console's map is empty for a reason nobody
// can see -- the map renders as an empty room and every zone condition is
// unreachable.
func TestThePocSiteModelLoads(t *testing.T) {
	site, err := LoadFromFile("../../poc/sitemap.yaml")
	if err != nil {
		t.Fatalf("the POC's site model does not load: %v", err)
	}
	if len(site.Zones) == 0 {
		t.Error("the POC's site model has no zones")
	}
}
