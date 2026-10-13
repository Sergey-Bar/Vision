package main

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/poc/spatial"
)

func TestExtractVideoID(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=VGnFLdQW39A": "VGnFLdQW39A",
		"https://youtu.be/VGnFLdQW39A":                "VGnFLdQW39A",
		"https://www.youtube.com/live/VGnFLdQW39A":    "VGnFLdQW39A",
		"https://www.youtube.com/embed/VGnFLdQW39A":   "VGnFLdQW39A",
		"VGnFLdQW39A":                     "VGnFLdQW39A",
		"https://example.com/not-youtube": "",
		"":                                "",
	}
	for in, want := range cases {
		if got := extractVideoID(in); got != want {
			t.Errorf("extractVideoID(%q) = %q, want %q", in, got, want)
		}
	}
}

func newTestEngine() *spatial.Engine {
	return spatial.NewEngine([]spatial.ZoneConfig{
		{
			ID: "z-entry", Name: "Restricted", Type: "no-entry",
			Polygon:        [][]float64{{0, 0}, {100, 0}, {100, 100}, {0, 100}},
			AlertOnEntry:   true,
			AlertOnDwell:   true,
			DwellThreshold: 5 * time.Second,
		},
		{
			ID: "z-monitor", Name: "Entrance", Type: "monitoring",
			Polygon:        [][]float64{{200, 0}, {300, 0}, {300, 100}, {200, 100}},
			AlertOnEntry:   false,
			AlertOnDwell:   true,
			DwellThreshold: 5 * time.Second,
		},
	})
}

func newTestWatch(t *testing.T) *zoneWatch {
	t.Helper()
	return newZoneWatch(newTestEngine(), nil)
}

func hit(zone, obj string) spatial.Alert {
	return spatial.Alert{ZoneID: zone, ZoneName: zone, ObjectID: obj, ObjectLabel: obj, AlertType: "entry"}
}

func TestTrackPresenceEntryFiresOnceAndResets(t *testing.T) {
	watch := newTestWatch(t)
	t0 := time.Now()

	out := watch.trackPresence([]spatial.Alert{hit("z-entry", "obj_1")}, t0)
	if len(out) != 1 || out[0].AlertType != "entry" {
		t.Fatalf("first crossing: got %d alerts (%v), want one entry", len(out), out)
	}

	// Still inside: no repeat entry, no dwell before the threshold.
	out = watch.trackPresence([]spatial.Alert{hit("z-entry", "obj_1")}, t0.Add(2*time.Second))
	if len(out) != 0 {
		t.Fatalf("holding: got %d alerts, want 0", len(out))
	}

	// Past the dwell threshold: exactly one dwell alert.
	out = watch.trackPresence([]spatial.Alert{hit("z-entry", "obj_1")}, t0.Add(6*time.Second))
	if len(out) != 1 || out[0].AlertType != "dwell" {
		t.Fatalf("dwell: got %d alerts (%v), want one dwell", len(out), out)
	}
	out = watch.trackPresence([]spatial.Alert{hit("z-entry", "obj_1")}, t0.Add(8*time.Second))
	if len(out) != 0 {
		t.Fatalf("dwell already sent: got %d alerts, want 0", len(out))
	}

	// Leaving clears state; a re-entry must alert again.
	watch.trackPresence(nil, t0.Add(9*time.Second))
	out = watch.trackPresence([]spatial.Alert{hit("z-entry", "obj_1")}, t0.Add(10*time.Second))
	if len(out) != 1 || out[0].AlertType != "entry" {
		t.Fatalf("re-entry: got %d alerts (%v), want one entry", len(out), out)
	}
}

func TestTrackPresenceNoEntryForMonitoringZone(t *testing.T) {
	watch := newTestWatch(t)
	t0 := time.Now()

	out := watch.trackPresence([]spatial.Alert{hit("z-monitor", "obj_2")}, t0)
	if len(out) != 0 {
		t.Fatalf("monitoring zone announced an entry: %v", out)
	}
	out = watch.trackPresence([]spatial.Alert{hit("z-monitor", "obj_2")}, t0.Add(6*time.Second))
	if len(out) != 1 || out[0].AlertType != "dwell" {
		t.Fatalf("monitoring dwell: got %v, want one dwell", out)
	}
}

func TestSpawnMixCoversAllCategories(t *testing.T) {
	sim := newSimulator(newTestEngine(), nil, 42)
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		sim.spawn(now)
	}
	for _, o := range sim.objects {
		seen[o.category] = true
		if o.born.IsZero() || o.life <= 0 {
			t.Fatalf("object %s missing lifetime (born=%v life=%v)", o.id, o.born, o.life)
		}
	}
	for _, cat := range []string{"vehicle", "biological", "other"} {
		if !seen[cat] {
			t.Errorf("spawn mix never produced category %q in 500 spawns", cat)
		}
	}
}
