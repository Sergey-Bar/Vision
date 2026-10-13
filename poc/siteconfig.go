package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/dutylog"
	"github.com/Sergey-Bar/vision/internal/siteconfig"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// The POC's view of the autonomous configurator.
//
// The configurator itself lives in internal/siteconfig and is driven by
// `securityctl site-config` (cmd/securityctl/siteconfig.go); what this file
// adds is the POC's own inventory and measurements as its inputs, so the demo
// shows the configurator reading a fleet rather than a fixtures file:
//
//   - the camera registry is the fleet's cameras, declared, with no position
//     known — which the artifact marks `assumed` rather than guessing;
//   - the declared zones are the site model's, with the cameras each zone's
//     declaration names;
//   - the headroom is what the fleet measured: each camera's named health
//     state, its frame count and the sidecar's p95 latency;
//   - the learned zones come from the occupancy cube the capture loops
//     accumulate, through siteconfig.LearnZones.
//
// Nothing here decides anything. This is the configurator's input page: the
// artifact, its digest, the rules its zones imply, and the list of entries
// that are `assumed` and therefore need a human.

// occupancyCube is a camera's grid-occupancy counts: how many frames each cell
// held a detection in. It is the aggregated form of the observations
// LearnZones takes, accumulated by the capture loop that already decodes the
// pixels, and read by the site-config view.
type occupancyCube struct {
	mu     sync.Mutex
	cells  map[cellKey]int
	frames int
}

type cellKey struct{ row, col int }

// note marks the cells one frame's detections covered.
func (c *occupancyCube) note(cells []siteconfig.Cell) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames++
	for _, cell := range cells {
		c.cells[cellKey{cell.Row, cell.Col}]++
	}
}

// cube is the cube in the learner's own shape: a Cell-keyed map and the frame
// count the window covered.
func (c *occupancyCube) cube() siteconfig.Cube {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := make(map[siteconfig.Cell]int, len(c.cells))
	for cell, n := range c.cells {
		counts[siteconfig.Cell{Row: cell.row, Col: cell.col}] = n
	}
	return siteconfig.Cube{Counts: counts, Frames: c.frames}
}

// learnConfig is the binning this POC learns zones with: the siteconfig
// default over the POC's own frame geometry.
func learnConfig() siteconfig.LearnConfig {
	cfg := siteconfig.DefaultLearnConfig()
	cfg.MinOccupancyFrames = 20 // a fifth of a demo window, not of an hour
	return cfg
}

// siteConfigView is the generated artifact plus what a human needs to read
// before acting on it. It is deterministic for given inputs: the same fleet
// state renders the same summary.
func (s *StreamIngest) siteConfigView(now time.Time) map[string]any {
	inventory := make(siteconfig.StaticInventory, 0, 4)
	headroom := make([]siteconfig.CameraHeadroom, 0, 4)
	cubes := make(map[string]siteconfig.Cube, 4)

	for _, st := range s.fleetStatus(now) {
		if st.Kind == camKindSim {
			continue
		}
		inventory = append(inventory, siteconfig.Camera{
			ID:   st.ID,
			Name: st.Name,
			Prov: siteconfig.Provenance{
				Origin:   siteconfig.OriginDeclared,
				Evidence: "the POC's camera registry",
			},
		})
		headroom = append(headroom, siteconfig.CameraHeadroom{
			CameraID:   st.ID,
			Health:     st.State,
			FramesSeen: st.Frames,
		})
		// The occupancy the fleet measured, in the aggregated form: a cube the
		// capture loop already keeps, so this request allocates the grid's
		// size rather than one observation per occupied cell per frame.
		if c := s.fleet.get(st.ID); c != nil && c.occupancy != nil {
			if cube := c.occupancy.cube(); cube.Frames > 0 {
				cubes[st.ID] = cube
			}
		}
	}

	declared := make([]siteconfig.Zone, 0, 4)
	var routes []string
	if s.siteModel != nil {
		siteFile := ""
		if s.config.Site != nil {
			siteFile = s.config.Site.File
		}
		for _, z := range s.siteModel.Zones {
			declared = append(declared, siteconfig.Zone{
				ID:      z.ID,
				Type:    z.Type,
				Polygon: toConfigPoints(z.Polygon),
				Cameras: camerasForZone(s.siteModel, z.ID),
				Prov: siteconfig.Provenance{
					Origin:   siteconfig.OriginDeclared,
					Evidence: "the site model: " + siteFile,
				},
			})
		}
		for _, r := range s.siteModel.Routes {
			routes = append(routes, r.ID)
		}
	}

	in := siteconfig.GenerateInput{
		SiteID:        s.siteID,
		GeneratedAt:   now,
		Inventory:     inventory,
		DeclaredZones: declared,
		Observations:  nil,
		Cubes:         cubes,
		LearnCfg:      learnConfig(),
		Headroom:      headroom,
	}
	if s.inferLat != nil {
		if _, p95, _, _, ok := s.inferLat.Percentiles(); ok {
			for i := range in.Headroom {
				in.Headroom[i].InferP95 = p95
			}
		}
	}

	out := map[string]any{}
	art, err := siteconfig.Generate(in)
	if err != nil {
		out["error"] = err.Error()
		out["reason"] = "the configurator refused to generate; the fleet says why"
		return out
	}

	// The entries that need a human: the `assumed` ones. A camera with no
	// declared position is the common case, and saying so is the difference
	// between a configuration and a guess.
	var assumed []string
	for _, c := range art.Cameras {
		if c.Prov.Origin == siteconfig.OriginAssumed {
			assumed = append(assumed, c.ID+" (no position declared)")
		}
	}
	// And the learned zones, named so a human reviews them before they are
	// promoted: a learned zone is a proposal, and the artifact saying so is
	// the difference between a derivation and a decision.
	var learned []string
	for _, z := range art.Zones {
		if z.Prov.Origin == siteconfig.OriginLearned {
			learned = append(learned, fmt.Sprintf("%s (%s, confidence %.2f)", z.ID, z.Type, z.Confidence))
		}
	}

	out["digest"] = art.Digest()
	out["summary"] = art.Summary()
	out["assumed_entries"] = assumed
	out["learned_zones"] = learned
	out["analytics_rules"] = art.AnalyticsRulesFor(routes)
	out["zone_criticality"] = zoneCriticalityView(art)
	out["capture_limits"] = map[string]float64{
		"ceiling_fps": siteconfig.MaxCaptureFPS,
		"floor_fps":   siteconfig.MinCaptureFPS,
		"default_fps": siteconfig.DefaultCaptureFPS,
	}
	return out
}

func zoneCriticalityView(art *siteconfig.Artifact) map[string]string {
	out := map[string]string{}
	for _, c := range art.Cameras {
		out[c.ID] = siteconfig.ZoneCriticality(c.ID, art.Zones)
	}
	return out
}

// observationsFor materialises a camera's occupancy cube as the observations
// LearnZones takes. The two forms carry the same per-cell counts — a frame
// that covered four cells is the same four +1s as four frames that covered one
// each — which is what makes the cube the capture loop can afford and the
// input the learner wants the same measurement.

// noteOccupancy marks one frame's detection cells into the camera's cube.
func (c *cameraRuntime) noteOccupancy(cells []siteconfig.Cell) {
	if c.occupancy == nil {
		return
	}
	c.occupancy.note(cells)
}

// occupancyCellsFor maps frame-space boxes onto the learning grid. The grid is
// the POC's frame geometry, and a box marks the cell its top-left corner falls
// in — the coarse reading that makes "something was here for a while" a count
// rather than a model.
func occupancyCellsFor(objs []frameObject, cfg siteconfig.LearnConfig) []siteconfig.Cell {
	seen := map[siteconfig.Cell]bool{}
	for _, o := range objs {
		col := int(o.BBox.X / cfg.FrameW * float64(cfg.GridW))
		row := int(o.BBox.Y / cfg.FrameH * float64(cfg.GridH))
		if row < 0 || col < 0 || row >= cfg.GridH || col >= cfg.GridW {
			continue
		}
		cell := siteconfig.Cell{Row: row, Col: col}
		if !seen[cell] {
			seen[cell] = true
		}
	}
	out := make([]siteconfig.Cell, 0, len(seen))
	for cell := range seen {
		out = append(out, cell)
	}
	return out
}

// dutyLogView is the hand-over artifact as an API client sees it: the
// structured brief, the log's own dump text, and the chain's head hash, which
// is what makes an edited line visible.
func (s *StreamIngest) dutyLogView() map[string]any {
	out := map[string]any{"shift": s.shift, "site_id": s.siteID}
	if s.handover == nil || s.handover.duty == nil {
		out["error"] = "the duty log is not open"
		out["reason"] = "the hand-over artifact could not be opened; the pipeline runs without it"
		return out
	}
	out["path"] = s.handover.duty.Path()
	out["head_hash"] = s.handover.duty.LastHash()
	// The chain is verified over the events this writer already holds, not by
	// re-reading the file: the ring mirrors the file by contract (every branch
	// that writes appends to it), and the endpoint is polled every 10s per
	// open tab, so a full-file parse plus a SHA-256 per line per poll was a
	// cost that grew with the shift for 200 lines of output.
	events := s.handover.duty.Events()
	if err := dutylog.VerifyChain(events); err != nil {
		out["verification"] = err.Error()
	} else {
		out["verification"] = "ok"
	}
	out["lines"] = len(events)
	// The dump is bounded to the most recent events, and the bound and the
	// disclosure both come from the one place that declares them (the same
	// declaration the console's page reads), so the two surfaces cannot
	// disagree about how much of a shift they are showing.
	dump, note := dutylog.DumpTail(events)
	if note != "" {
		out["dump_note"] = note
	}
	out["dump"] = dump
	if b := s.latestBrief(); b != nil {
		out["brief"] = b
	}
	return out
}

// incidentDutyLog is one incident's own view of the log: the decisions,
// overrides and notes about it, so an audit question about an incident is one
// filter over the shift's log rather than a second file.
func (s *StreamIngest) incidentDutyLog(incidentID string) []dutylog.Event {
	if s.handover == nil || s.handover.duty == nil {
		return nil
	}
	events, err := dutylog.Extract(s.handover.duty.Path(), incidentID)
	if err != nil {
		// The incident id is request-derived and is not echoed into the log:
		// a line that quotes a request path is a log-injection point. The
		// error names the artifact, and the caller knows which incident it
		// asked about.
		log.Printf("dutylog: extract failed: %v", err)
		return nil
	}
	return events
}

// recordCoverageSweep is the duty-log line for zone coverage, and the only
// writer of it. Every zone is evaluated on the metrics loop's clock — covered
// and dark alike — which is what makes the state map record transitions in
// both directions.
//
// Deriving the line from the autonomy pass instead, as the first version did,
// could only ever see one direction: the coverage rule emits an anomaly only
// while a zone IS under-observed, so recovery was never recorded, the map
// latched, and a later dark window was suppressed as a repeat — a coverage
// line lost, and a console brief reporting the zone under-observed forever
// after the camera recovered. Deriving it from a camera's loop exit instead,
// as the second version did, fabricated an "under-observed → covered" line
// for a zone that never went dark. One sweep, one rule, both directions.
func (s *StreamIngest) recordCoverageSweep() {
	if s.handover == nil || s.siteModel == nil {
		return
	}
	for _, cov := range anomaly.UnderObservedZones(s.siteModel, s.cameraDegraded) {
		if cov.Unanswerable {
			// Nobody has declared which cameras cover this zone. That is a
			// gap in the declaration, not a transition in the fleet, and this
			// log records transitions.
			continue
		}
		s.zoneCoverage(cov.ZoneID, cov.UnderObserved, cov.DegradedCameras, cov.DeclaredCameras)
	}
}

// zoneCoverage is the duty log's only path into the artifact.
//
// The subject is the ZONE, never a camera: the earlier version named the camera
// that *saw* an object and wrote a line claiming that camera was silent, which
// was false. The state map suppresses repeats and never transitions, so a zone
// that recovers records its recovery and a later dark window records again.
//
// declared is the zone's full declaration and degraded the subset that is dark,
// so the brief's coverage row can distinguish "2 declared" from "2 degraded"
// rather than rendering the degraded set as the declared one.
func (s *StreamIngest) zoneCoverage(zoneID string, underObserved bool, degraded, declared []string) {
	if s.handover == nil || s.siteModel == nil {
		return
	}
	state := "covered"
	if underObserved {
		state = "under-observed"
	}
	s.handover.mu.Lock()
	if s.handover.coverageState == nil {
		s.handover.coverageState = map[string]string{}
	}
	if last := s.handover.coverageState[zoneID]; last == state {
		s.handover.mu.Unlock()
		return
	}
	s.handover.coverageState[zoneID] = state
	s.handover.mu.Unlock()

	reason := fmt.Sprintf("every declared camera of %s is watching", zoneID)
	before, after := "under-observed", "covered"
	if underObserved {
		before, after = "covered", "under-observed"
		if len(degraded) == 0 {
			// Only reachable for a zone nothing declares, which the sweep
			// skips — kept as a fallback rather than a claim about the site
			// model, because the old wording contradicted it.
			reason = fmt.Sprintf("no declared camera of %s is watching, and none is declared degraded", zoneID)
		} else {
			reason = fmt.Sprintf("every declared camera of %s (%s) is degraded", zoneID, strings.Join(degraded, ", "))
		}
	}
	if _, err := s.handover.duty.Append(dutylog.Event{
		Kind: dutylog.KindCoverage,
		// The declared cameras are the event's own field, so the brief
		// mapping reads them rather than parsing them back out of the reason
		// text.
		CameraIDs:    declared,
		Actor:        "autonomy",
		TenantID:     s.tenantID,
		SiteID:       s.siteID,
		Subject:      zoneID,
		Action:       state,
		Reason:       reason,
		Before:       before,
		After:        after,
		EvidenceRefs: degraded,
	}); err != nil {
		log.Printf("dutylog: coverage line for %s not recorded: %v", zoneID, err)
	}
}

func toConfigPoints(in []sitemodel.Point) []siteconfig.Point {
	out := make([]siteconfig.Point, 0, len(in))
	for _, p := range in {
		out = append(out, siteconfig.Point{X: p.X, Y: p.Y})
	}
	return out
}

// camerasForZone is the site model's declaration, read through the one
// accessor that owns it (see sitemodel.Site.CamerasForZone).
func camerasForZone(site *sitemodel.Site, zoneID string) []string {
	return site.CamerasForZone(zoneID)
}
