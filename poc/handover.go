package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/dutylog"
	"github.com/Sergey-Bar/vision/poc/ws"
)

// The hand-over layer: the duty log and the brief.
//
// Two artifacts and one rule. The duty log is the record — one append-only,
// hash-chained JSONL file per shift, fsynced before the decision that caused
// it is broadcast or the drawer closes. The brief is the view — rendered from
// the log and the fleet's own state, so a hand-over read at 06:00 is the same
// document at 06:00 and 06:05 apart from what actually changed.
//
// Neither artifact is written by the autonomy pass inline in a way that can
// block a frame: both go through recordDecision, which appends, then
// re-renders and broadcasts. A log that cannot be written fails by name and
// the pipeline keeps running, because a dead POC proves nothing about the
// site it was watching.

// handover owns the duty-log writer and the latest rendered brief for one
// shift.
type handover struct {
	mu     sync.Mutex
	duty   *dutylog.Writer
	latest *brief.Brief

	// lastDecision is per incident: an autonomy pass re-evaluates an open
	// incident every frame, and writing a duty-log line per frame would bury
	// the shift's actual decisions under a thousand identical ones. Only a
	// real change of action or autonomy state is a line.
	lastDecision map[string]string
	// coverageState is per zone, for the same reason: a zone that went dark
	// is one line, not one per frame.
	coverageState map[string]string
}

// initHandover opens the shift's duty log. A failure is returned so the
// caller can say so by name — the POC then runs without the artifact rather
// than dying, and the reason is visible on the status line.
func (s *StreamIngest) initHandover(shift string) error {
	dir := s.config.Site.DutyLogDir
	if dir == "" {
		dir = "data/dutylog"
	}
	s.shift = shift
	w, err := dutylog.Open(dir, s.siteID, shift)
	if err != nil {
		return err
	}
	s.handover = &handover{
		duty:          w,
		lastDecision:  make(map[string]string),
		coverageState: make(map[string]string),
	}
	if err := w.Verify(); err != nil {
		// A resumed shift whose chain does not verify is refused loudly:
		// the artifact is either intact or it is not a record.
		return fmt.Errorf("shift %s: %w", shift, err)
	}
	log.Printf("hand-over: duty log %s", w.Path())
	return nil
}

// logTransition records an incident's decision only when it is a real change.
// It returns whether a line was written, so callers do not rebuild the brief
// for an unchanged state.
func (s *StreamIngest) logTransition(inc *correlation.CorrelatedIncident, action, autonomyState string, reason string) bool {
	if s.handover == nil {
		return false
	}
	key := action + "/" + autonomyState
	s.handover.mu.Lock()
	prev := s.handover.lastDecision[inc.ID]
	if prev == key {
		s.handover.mu.Unlock()
		return false
	}
	s.handover.mu.Unlock()

	_, err := s.handover.duty.Append(dutylog.Event{
		Kind:         dutylog.KindTransition,
		Actor:        "autonomy",
		TenantID:     s.tenantID,
		SiteID:       s.siteID,
		CameraIDs:    inc.CameraIDs,
		Subject:      inc.ID,
		Action:       key,
		Reason:       reason,
		Before:       prev,
		After:        autonomyState,
		EvidenceRefs: inc.DetectionIDs,
	})
	if err != nil {
		// The key is written only after a successful append. Writing it
		// before, as the first version did, meant a refused line (a decision
		// whose reason text was empty, which this log refuses) suppressed the
		// incident's decision for the whole shift — the decision happened,
		// the artifact never said so, and the next pass saw no change.
		log.Printf("dutylog: transition for %s not recorded: %v", inc.ID, err)
		return false
	}
	s.handover.mu.Lock()
	s.handover.lastDecision[inc.ID] = key
	s.handover.mu.Unlock()
	return true
}

// logOverride records an operator's override: the decision that was taken,
// the one the operator chose instead, and the reason they gave.
func (s *StreamIngest) logOverride(inc *correlation.CorrelatedIncident, fromAction, toAction, reason string) bool {
	if s.handover == nil {
		return true
	}
	_, err := s.handover.duty.Append(dutylog.Event{
		Kind:         dutylog.KindOverride,
		Actor:        "operator",
		TenantID:     s.tenantID,
		SiteID:       s.siteID,
		CameraIDs:    inc.CameraIDs,
		Subject:      inc.ID,
		Action:       sanitizeText(toAction),
		Reason:       sanitizeText(reason),
		Before:       sanitizeText(fromAction),
		After:        "OVERRIDDEN",
		EvidenceRefs: inc.DetectionIDs,
	})
	if err != nil {
		// The incident id is not logged: the only caller that holds a
		// URL-derived one is the override handler, and a log line that echoes
		// a request path is a log-injection point for a handful of control
		// characters. The error names the artifact; the caller knows the
		// incident it asked about.
		log.Printf("dutylog: override not recorded: %v", err)
		return false
	}
	return true
}

// logNote records an operator's patrol note against a camera or an incident.
// The free-text is sanitised here, at the writer, because every path into the
// artifact goes through these three functions and only one of their callers
// knew to clean.
func (s *StreamIngest) logNote(actor, subject, text string) bool {
	if s.handover == nil {
		return true
	}
	_, err := s.handover.duty.Append(dutylog.Event{
		Kind:     dutylog.KindNote,
		Actor:    sanitizeText(actor),
		Subject:  sanitizeText(subject),
		Reason:   sanitizeText(text),
		TenantID: s.tenantID,
		SiteID:   s.siteID,
	})
	if err != nil {
		log.Printf("dutylog: note not recorded: %v", err)
		return false
	}
	return true
}

// logPatrolNote records a patrol line. The subject is the thing the note is
// about and the reason is the note itself, which for a patrol is "walk
// route-01" or "checked cam-02" — the hand-over's patrol section is these
// lines and nothing else.
//
// It reports whether the line was written, so the caller can tell an operator
// the truth rather than acknowledging a record that does not exist.
func (s *StreamIngest) logPatrolNote(text, subject string) bool {
	if s.handover == nil {
		return true
	}
	_, err := s.handover.duty.Append(dutylog.Event{
		Kind:     dutylog.KindNote,
		Actor:    "patrol",
		Subject:  sanitizeText(subject),
		Reason:   sanitizeText(text),
		TenantID: s.tenantID,
		SiteID:   s.siteID,
	})
	if err != nil {
		log.Printf("dutylog: patrol note not recorded: %v", err)
		return false
	}
	return true
}

// rebuildBrief renders the shift's brief from the duty log and the fleet's
// own counters, stores it and broadcasts it. The brief is a derived view:
// events come from the log, cameras from the fleet, so the artifact and the
// summary cannot disagree.
//
// It reads the linker, whose maps are written under passMu by the autonomy
// pass. Two entry points exist because there are two callers: the pass itself
// (already holding passMu) calls rebuildBriefHeld, and every handler calls
// rebuildBrief, which takes the lock first. Taking it in both places would
// deadlock; taking it in neither is the data race a fatal map read/write
// makes of it.
func (s *StreamIngest) rebuildBrief() {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	s.rebuildBriefHeld()
}

// rebuildBriefHeld is rebuildBrief for a caller that already holds passMu.
func (s *StreamIngest) rebuildBriefHeld() {
	if s.handover == nil {
		return
	}
	facts := s.briefFactsHeld()
	b := brief.Render(facts)

	s.handover.mu.Lock()
	s.handover.latest = &b
	s.handover.mu.Unlock()

	s.wsHub.Broadcast(ws.Message{
		Type:      "brief",
		Data:      b,
		Timestamp: time.Now().UTC(),
	})
}

// briefFactsHeld composes the brief's inputs with passMu held. Incidents come
// from the linker (what the autonomy layer actually built), events from the
// duty log (the artifact), cameras from the fleet (its own counters). Nothing
// here infers a fact that one of the three does not hold.
//
// Only incidents that are still live are rendered: the linker never prunes its
// map, so every RESOLVED incident of the shift would otherwise sit in the
// brief for the rest of the process — a section that grows without bound and
// a headline that never returns to "no incidents".
func (s *StreamIngest) briefFactsHeld() brief.Facts {
	facts := brief.Facts{
		Shift:       s.shift,
		TenantID:    s.tenantID,
		SiteID:      s.siteID,
		GeneratedAt: time.Now().UTC(),
	}

	for _, inc := range s.linker.AllIncidents() {
		if inc.State == "RESOLVED" || inc.State == "OVERRIDDEN" {
			continue
		}
		facts.Incidents = append(facts.Incidents, brief.Incident{
			ID:            inc.ID,
			Title:         incidentTitle(inc),
			CameraIDs:     inc.CameraIDs,
			Anomalies:     anomalyTypes(inc),
			ThreatScore:   inc.ThreatScore,
			Action:        inc.Action,
			AutonomyState: inc.AutonomyState,
			Reason:        inc.ReasonChain.Why,
			State:         inc.State,
		})
	}

	// The events come from the writer's own ring rather than a re-read of the
	// file: a rebuild happens per decision, and re-parsing the whole shift's
	// log on each one is O(shift length) inside the autonomy pass. The ring
	// carries exactly what the file holds, resumed writer included.
	//
	// The event→facts mapping is `brief.FactsFromEvents`, the same function the
	// console's reader calls, so one artifact cannot render two different
	// briefs - the console filed coverage lines under actions while this
	// package excluded them, and the difference was invisible until a shift
	// went dark.
	if s.handover.duty != nil {
		fromEvents := brief.FactsFromEvents(s.handover.duty.Events())
		facts.Actions = fromEvents.Actions
		facts.Notes = fromEvents.Notes
		// Coverage is filled from the declaration and the fleet, which is what
		// this side knows that the log does not: which cameras are declared to
		// cover a zone, and which of them are watching.
		for _, z := range anomaly.UnderObservedZones(s.siteModel, s.cameraDegraded) {
			facts.Coverage = append(facts.Coverage, brief.ZoneCoverage{
				ZoneID:          z.ZoneID,
				DeclaredCameras: z.DeclaredCameras,
				DegradedCameras: z.DegradedCameras,
				UnderObserved:   z.UnderObserved,
				Unanswerable:    z.Unanswerable,
			})
		}
	}

	for _, c := range s.fleetStatus(time.Now().UTC()) {
		// The sim pseudo-camera is a source, not a camera on the wall: it
		// correctly goes quiet the moment a real camera takes the dashboard,
		// and counting its silence as a degraded camera would report a
		// camera that does not exist — the same exclusion the telemetry
		// sampler and the configurator's view both make.
		if c.Kind == camKindSim {
			continue
		}
		facts.Cameras = append(facts.Cameras, brief.Camera{
			ID: c.ID, State: c.State, Reason: c.Reason,
		})
	}
	return facts
}

// zoneCameraMap builds the declaration the site model owns: zone id → the
// camera ids declared to cover it. It is one consumer of the one accessor
// (sitemodel.Site.CamerasForZone), because there is no FOV geometry anywhere
// and a second implementation of this lookup is how the artifact and the
// brief start disagreeing. The other consumer is the second-sight gate in the
// autonomy pass; the brief's coverage section reaches the same declaration
// through anomaly.UnderObservedZones.
// zoneCameraMap is the site model's camera->zone declaration map, built once
// when the fleet is built. It is consulted once per incident per frame inside
// the autonomy pass, and the site model is loaded once and never mutated, so
// recomputing it per call was a map, a slice per zone and a sort for a value
// that cannot change — and a lazy init inside a per-frame path would need the
// very lock it runs under.
//
// The map reads the one accessor (sitemodel.Site.CamerasForZone), because there
// is no FOV geometry anywhere and a second implementation of this lookup is
// how the artifact and the brief start disagreeing.
func (s *StreamIngest) zoneCameraMap() map[string][]string {
	return s.zoneCameras
}

// latestBrief returns the most recently rendered brief, or nil.
func (s *StreamIngest) latestBrief() *brief.Brief {
	if s.handover == nil {
		return nil
	}
	s.handover.mu.Lock()
	defer s.handover.mu.Unlock()
	return s.handover.latest
}

func incidentTitle(inc *correlation.CorrelatedIncident) string {
	types := anomalyTypes(inc)
	title := "Correlated incident"
	if len(types) > 0 {
		title = types[0]
	}
	if len(inc.Entities) > 0 && inc.Entities[0].Class != "" {
		title += " · " + inc.Entities[0].Class
	}
	return title
}

func anomalyTypes(inc *correlation.CorrelatedIncident) []string {
	out := make([]string, 0, len(inc.Anomalies))
	seen := map[string]bool{}
	for _, a := range inc.Anomalies {
		if !seen[a.Type] {
			seen[a.Type] = true
			out = append(out, a.Type)
		}
	}
	return out
}
