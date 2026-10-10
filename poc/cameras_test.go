package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/sitemodel"
	"github.com/Sergey-Bar/Ai-Captain/internal/telemetry"
)

// loadTestSite is the shared demo site: two cameras, a restricted zone and a
// choke point, the smallest model that exercises camera→zone declarations.
func loadTestSite(t *testing.T) *sitemodel.Site {
	t.Helper()
	site, err := sitemodel.LoadFromBytes([]byte(testSiteYAML))
	if err != nil {
		t.Fatalf("load site: %v", err)
	}
	return site
}

// writeTestSite writes the shared demo site to a temp file, so a fixture can
// enable the site the way the real config does and get the autonomy layer,
// the fleet and the coverage rule built by NewStreamIngest rather than by
// hand.
func writeTestSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	if err := os.WriteFile(path, []byte(testSiteYAML), 0o600); err != nil {
		t.Fatalf("write site: %v", err)
	}
	return path
}

const testSiteYAML = `
site:
  id: "fleet-site"
  cameras:
    - id: "cam-01"
      zone_ids: ["entrance"]
    - id: "cam-02"
      zone_ids: ["restricted"]
  zones:
    - id: "restricted"
      type: "restricted"
      polygon:
        - x: 550
          y: 50
        - x: 950
          y: 50
        - x: 950
          y: 450
        - x: 550
          y: 450
    - id: "entrance"
      type: "choke"
      polygon:
        - x: 100
          y: 300
        - x: 350
          y: 300
        - x: 350
          y: 550
        - x: 100
          y: 550
`

func newFleetIngest(t *testing.T) *StreamIngest {
	t.Helper()
	s := &StreamIngest{
		config: &Config{
			Cameras: []CameraConfig{
				{ID: "cam-01", Name: "City Square", YouTubeURL: "https://www.youtube.com/watch?v=VGnFLdQW39A"},
			},
			Live: LiveConfig{CaptureFPS: 1},
		},
		siteModel: loadTestSite(t),
	}
	s.buildFleet()
	return s
}

// TestBuildFleetMergesSiteModelAndConfig is the registry contract: the site
// model declares which cameras exist, the config says which have a stream,
// and a declared camera with no stream is registered but never started —
// reported, not invented.
func TestBuildFleetMergesSiteModelAndConfig(t *testing.T) {
	s := newFleetIngest(t)

	c1 := s.fleet.get("cam-01")
	if c1 == nil {
		t.Fatal("cam-01 is not in the fleet")
	}
	if c1.url == "" {
		t.Error("cam-01 has no stream URL from the config")
	}
	if len(c1.zoneID) != 1 || c1.zoneID[0] != "entrance" {
		t.Errorf("cam-01 zone declaration = %v, want [entrance] from the site model", c1.zoneID)
	}

	c2 := s.fleet.get("cam-02")
	if c2 == nil {
		t.Fatal("cam-02 (site-model only) is not in the fleet")
	}
	if c2.url != "" {
		t.Errorf("cam-02 has a URL it was never configured with: %q", c2.url)
	}

	sim := s.fleet.get(simCameraID)
	if sim == nil || !sim.sim {
		t.Fatal("the demo pseudo-camera is missing from the fleet")
	}

	live := s.fleet.liveCameras()
	if len(live) != 1 || live[0].id != "cam-01" {
		t.Fatalf("liveCameras = %v, want only cam-01", cameraIDs(live))
	}
}

// TestFleetDegradesByNameNotByProcess is the per-camera degradation contract:
// one camera failing records a reason on that camera and leaves the others'
// counters and states untouched.
func TestFleetDegradesByNameNotByProcess(t *testing.T) {
	s := newFleetIngest(t)
	now := time.Now().UTC()
	w := healthWindowsFor(1)

	// cam-01 is producing frames.
	c1 := s.fleet.get("cam-01")
	c1.frames.Store(120)
	c1.lastFrame.Store(now.Add(-500 * time.Millisecond).UnixNano())

	// cam-02's stream dies: it names its own reason.
	c2 := s.fleet.get("cam-02")
	c2.fail("capture ended: frame read ended: EOF")

	if got := c1.healthFor(now, w); got != camFresh {
		t.Errorf("healthy camera reads %q after another camera failed", got)
	}
	if got := c2.healthFor(now, w); got != camSilent {
		t.Errorf("failed camera reads %q, want %q", got, camSilent)
	}
	if r := c2.failReason(); r == "" {
		t.Error("failed camera carries no reason")
	}
	if !s.fleet.anyLive(now, w) {
		t.Error("a fleet with one live camera reports nothing live")
	}
}

// TestFleetStatusIsOneRowPerCamera checks the status API's shape: every
// registered camera appears exactly once, in a stable order, with its named
// state.
func TestFleetStatusIsOneRowPerCamera(t *testing.T) {
	s := newFleetIngest(t)
	now := time.Now().UTC()
	rows := s.fleetStatus(now)
	if len(rows) != 3 {
		t.Fatalf("fleet reported %d cameras, want 3 (cam-01, cam-02, sim)", len(rows))
	}
	seen := map[string]bool{}
	for i, r := range rows {
		if seen[r.ID] {
			t.Errorf("camera %s appears twice", r.ID)
		}
		seen[r.ID] = true
		if i > 0 && rows[i-1].ID >= r.ID {
			t.Errorf("fleet order is not stable: %s after %s", r.ID, rows[i-1].ID)
		}
		if r.State == "" {
			t.Errorf("camera %s has no state", r.ID)
		}
	}
}

// TestFleetSourceLineNamesDegradedCameras checks the aggregate source line:
// the fleet reports what it is actually running and names what is dark,
// rather than claiming a source it is not.
func TestFleetSourceLineNamesDegradedCameras(t *testing.T) {
	s := newFleetIngest(t)
	now := time.Now().UTC()

	// Nothing live yet.
	source, reason := s.fleetSourceLine()
	if source != "starting" || reason == "" {
		t.Errorf("idle fleet line = %q/%q, want starting with a reason", source, reason)
	}

	// One camera streaming, one dead: the line names the dead one.
	c1 := s.fleet.get("cam-01")
	c1.lastFrame.Store(now.Add(-time.Second).UnixNano())
	c2 := s.fleet.get("cam-02")
	c2.fail("stream resolve: yt-dlp is not on PATH")
	source, reason = s.fleetSourceLine()
	if source != "cameras" {
		t.Errorf("source with one live camera = %q, want cameras", source)
	}
	if !strings.Contains(reason, "cam-02") {
		t.Errorf("source reason %q does not name the degraded camera", reason)
	}
}

// TestTakeCameraHealthSamplesCountsTransitionsOnce checks that the telemetry
// counters record a camera crossing into a degraded state once, not once per
// observation. The other cameras are held live so the counter measures this
// camera's transition and nothing else.
func TestTakeCameraHealthSamplesCountsTransitionsOnce(t *testing.T) {
	s := newFleetIngest(t)
	s.telemetry = telemetry.NewMetricsCollector()

	now := time.Now().UTC()
	for _, id := range []string{"cam-01", simCameraID} {
		c := s.fleet.get(id)
		c.frames.Store(10)
		c.lastFrame.Store(now.Add(-time.Second).UnixNano())
	}
	c := s.fleet.get("cam-02")
	c.started.Store(true)
	c.fail("capture ended")

	// Several samples of the same silent camera.
	for i := 0; i < 5; i++ {
		s.takeCameraHealthSamples(now.Add(time.Duration(i) * time.Second))
	}
	live := s.telemetry.GetLiveTelemetry()
	if live.CamerasSilent != 1 {
		t.Errorf("silent transitions = %d, want 1", live.CamerasSilent)
	}
}

func cameraIDs(cams []*cameraRuntime) []string {
	out := make([]string, 0, len(cams))
	for _, c := range cams {
		out = append(out, c.id)
	}
	return out
}
