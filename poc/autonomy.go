package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlation"
	"github.com/Sergey-Bar/Ai-Captain/internal/threat"
	"github.com/Sergey-Bar/Ai-Captain/poc/ws"
)

// autonomyPass runs the autonomy layer over one frame's detections: track
// entities across cameras, link them into a correlated incident, evaluate the
// behaviour rules, score the threat and let the playbook decide what to do.
// It is a single deterministic pass over facts the pipeline already has.
//
// cameraID is the camera that saw this frame ” the real one, from the fleet,
// never a stand-in for the site. The pass is serialized across cameras
// (passMu) because the tracker, the linker and the playbook keep their state
// in maps, and N camera loops publishing at once is exactly the concurrency
// that would corrupt them; serialization is also what makes the shared
// tracker's "all cameras' tracks at decision time" true rather than racy.
func (s *StreamIngest) autonomyPass(cameraID string, now time.Time, frame int64, objs []frameObject) {
	if s.siteModel == nil || s.tracker == nil || s.linker == nil {
		return
	}
	s.passMu.Lock()
	defer s.passMu.Unlock()

	dets := make([]correlation.Detection, 0, len(objs))
	for _, o := range objs {
		cls := "other"
		switch o.Category {
		case "vehicle":
			cls = "vehicle"
		case "biological":
			cls = "person"
		}
		dets = append(dets, correlation.Detection{
			// The detection id is camera-scoped: each camera's local
			// tracker numbers its objects from t0001, and a detection id
			// shared by two cameras would make the linker's evidence
			// citations point at the wrong camera's sighting.
			ID:         cameraID + "/" + o.ID,
			Class:      cls,
			CameraID:   cameraID,
			BBox:       correlation.BBox{X: o.BBox.X, Y: o.BBox.Y, Width: o.BBox.Width, Height: o.BBox.Height},
			Confidence: o.Conf,
			Timestamp:  now,
		})
	}

	for range dets {
		s.telemetry.RecordDetection()
		s.telemetry.RecordZoneContext()
	}

	tracks := s.tracker.Update(dets, now)
	incidents := s.linker.Link(tracks, now)
	if len(incidents) == 0 {
		return
	}

	// The share of THIS pass's detections that landed in a correlated
	// incident is the correlation hit rate, so it is counted over exactly the
	// detections this pass submitted ” never over the incident's whole
	// history, which would re-count every earlier frame on every pass and
	// inflate the rate past 100%.
	inIncident := make(map[string]bool, len(incidents))
	for _, inc := range incidents {
		for _, id := range inc.DetectionIDs {
			inIncident[id] = true
		}
	}
	correlated := 0
	for _, d := range dets {
		if inIncident[d.ID] {
			correlated++
		}
	}
	s.telemetry.RecordCorrelated(correlated)

	for _, inc := range incidents {
		// Evaluate the behaviour rules over what the incident has seen.
		anomalies := s.anomalyRules.Evaluate(inc, s.siteModel, now)
		if len(anomalies) == 0 {
			continue
		}
		for _, a := range anomalies {
			s.linker.AddAnomaly(inc.ID, a)
			// The coverage rule's finding is a hand-over line too: a zone
			// whose every declared camera was degraded when the decision was
			// made is exactly what the next shift needs to read, and an
			// anomaly nobody recorded is an observation nobody keeps.
			// The coverage finding itself is an anomaly and is recorded as
			// one; the duty-log LINE is written by the sweep in
			// recordCoverageSweep, which runs on the metrics loop's clock over
			// every zone and therefore sees both directions of the
			// transition. Deriving the line from this pass instead could only
			// ever see one: the rule emits an anomaly only while a zone IS
			// under-observed, so recovery was never recorded and the state map
			// latched.
		}

		// Score and decide. The playbook owns the cooldowns and guardrails, so
		// the score is recomputed from the incident's own anomalies rather
		// than carried from the playbook's internal evaluation. The second
		// sight is declaration-based corroboration: one camera's sighting
		// never escalates on its own.
		sight := correlation.SecondSight(inc, tracks, s.zoneCameraMap())
		s.lastSight.Store(inc.ID, sight)
		action, autonomyState, tchain := s.playbook.DecideWithSight(inc, sight, now)
		score, _ := s.threatScorer.Score(inc)
		chain := toCorrelationChain(tchain)
		s.linker.UpdateIncidentThreatScore(inc.ID, score, chain)
		s.linker.UpdateIncidentAction(inc.ID, string(action), autonomyState)
		inc.ThreatScore = score
		inc.ReasonChain = chain
		inc.Action = string(action)
		inc.AutonomyState = autonomyState

		s.telemetry.RecordIncident()
		switch action {
		case threat.ActionEscalate:
			s.telemetry.RecordEscalation()
			s.recordEscalation(now)
		case threat.ActionVerify:
			s.telemetry.RecordTimeToDecision(now.Sub(inc.FirstSeen))
		}

		// The duty log takes the decision line first, and the brief is
		// rebuilt after it ” so a hand-over read at any moment contains
		// every decision that was broadcast before it. The Held variant,
		// because this pass holds passMu.
		if s.logTransition(inc, string(action), autonomyState, chainReason(chain)) {
			s.rebuildBriefHeld()
		}

		s.wsHub.Broadcast(ws.Message{
			Type:      "correlated",
			Data:      map[string]any{"incident": inc},
			Timestamp: now,
		})
		s.wsHub.Broadcast(ws.Message{
			Type:      "threat",
			Data:      inc,
			Timestamp: now,
		})
		s.wsHub.Broadcast(ws.Message{
			Type: "autonomy",
			Data: map[string]any{
				"incident_id":  inc.ID,
				"state":        autonomyState,
				"action":       string(action),
				"threat_score": inc.ThreatScore,
				"first_seen":   inc.FirstSeen,
				"last_seen":    inc.LastSeen,
				"camera_ids":   inc.CameraIDs,
				"second_sight": sight,
				"frame":        frame,
				"provenance":   inc.Provenance,
			},
			Timestamp: now,
		})
		// One incident per pass is plenty for a demo; the cooldowns in the
		// playbook are what stop a site from escalating a hundred times a
		// minute, and broadcasting every one of them would flood the client.
		return
	}
}

// sightCache remembers the most recent second-sight verdict per incident so
// an API client that is not on the websocket sees the same judgement the
// operator surface does. It is memory-only: the authority is the duty log's
// transition lines, and a restart clears the cache rather than inventing a
// verdict for an incident it never saw.
type sightCache struct {
	mu sync.Mutex
	m  map[string]correlation.Sight
}

func newSightCache() *sightCache { return &sightCache{m: make(map[string]correlation.Sight)} }

func (c *sightCache) Store(id string, s correlation.Sight) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = s
}

func (c *sightCache) Get(id string) (correlation.Sight, bool) {
	if c == nil {
		return correlation.Sight{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[id]
	return s, ok
}

// Forget drops an ended incident's verdict, so a re-opened incident is judged
// fresh rather than inheriting a stale corroboration record.
func (c *sightCache) Forget(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, id)
}

// Keys lists the ids the cache still holds a verdict for. It exists so the
// janitor can walk what is remembered — the linker never prunes its incidents,
// so walking all of them per tick would re-do the same deletes for ever.
func (c *sightCache) Keys() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.m))
	for id := range c.m {
		out = append(out, id)
	}
	return out
}

// forgetIncident drops an ended incident's per-incident memory: the last
// duty-log decision key (so a later line about the same id is a new line, not a
// suppressed one) and the last second-sight verdict. It takes the handover's own
// lock for its map ” the same order logTransition uses, so the two cannot
// deadlock.
//
// The maps are keyed by incident id and the linker never prunes its incidents,
// so without this the ingest holds one entry per incident a shift produced,
// including the retained slices behind them.
func (s *StreamIngest) forgetIncident(incidentID string) {
	if s.lastSight != nil {
		s.lastSight.Forget(incidentID)
	}
	if s.handover == nil {
		return
	}
	s.handover.mu.Lock()
	delete(s.handover.lastDecision, incidentID)
	s.handover.mu.Unlock()
}

// forgetTerminalIncidents is the janitor for the per-incident memories.
//
// It runs on the metrics loop's clock rather than inside the autonomy pass
// because the pass only ever sees incidents the linker still reports as live:
// `Link` returns ACTIVE/VERIFYING/ESCALATING, so a resolved incident never
// reaches the pass at all. The linker marks incidents resolved in its own
// update pass and never prunes them, which leaves exactly one place to notice
// an incident that has ended.
//
// It walks the ids this ingest still remembers, not every incident the linker
// holds. The linker never prunes, so walking its whole map per tick would cost
// O(every incident of the shift) every 5s for ever, re-issuing the same
// deletes; and the remembered set is exactly the small thing worth checking,
// because an incident with no memory here needs no forgetting.
// `AllIncidents` is map-ordered, so a positional cursor would be unsound; a set
// of remembered ids is both sound and bounded.
//
// It reads the linker under passMu, because that is the lock the autonomy pass
// holds while writing the linker's maps: an unsynchronized read beside a
// concurrent write is not a rare race in Go, it is a fatal "concurrent map
// read and map write" that kills the process mid-shift.
func (s *StreamIngest) forgetTerminalIncidents() {
	if s.linker == nil || s.handover == nil {
		return
	}
	s.passMu.Lock()
	defer s.passMu.Unlock()

	s.handover.mu.Lock()
	ids := make([]string, 0, len(s.handover.lastDecision))
	for id := range s.handover.lastDecision {
		ids = append(ids, id)
	}
	s.handover.mu.Unlock()
	ids = append(ids, s.lastSight.Keys()...)

	for _, id := range ids {
		inc := s.linker.GetIncident(id)
		if inc != nil && inc.State != "RESOLVED" && inc.State != "OVERRIDDEN" {
			continue
		}
		// Either terminal, or the linker dropped it (which it does not do
		// today): either way a memory with no live owner goes.
		s.forgetIncident(id)
	}
}

// chainReason is the reason text a duty-log line carries for one decision.
//
// It is derived from the scorer's own reason chain, and the chain's `Why`
// prose when there is any — but a chain whose Why is empty still has steps,
// and the steps ARE the reason: which rule fired, for how much, on what
// evidence. Formatting them as text is what makes "explain my reasoning in
// audit-ready format" true for a decision the playbook made without prose.
// Returning the empty string here instead would have the duty log refuse the
// line (it refuses a decision with no reason), and because lastDecision was
// written before the append, that refusal would silently suppress the
// incident's decision for the rest of the shift.
func chainReason(chain correlation.ReasonChain) string {
	if chain.Why != "" {
		return chain.Why
	}
	if len(chain.Steps) == 0 {
		return "no correlated anomaly, so the default applies"
	}
	parts := make([]string, 0, len(chain.Steps))
	for _, s := range chain.Steps {
		parts = append(parts, fmt.Sprintf("%s %+d (%s)", s.Key, s.Factor, s.Evidence))
	}
	return strings.Join(parts, "; ")
}

// toCorrelationChain translates the scorer's reason chain into the correlation
// package's. The two are deliberately separate types: the autonomy layer's
// provenance extends the spine's rather than replacing it, and a shared type
// would make internal/correlation take a dependency on internal/threat.
func toCorrelationChain(src threat.ReasonChain) correlation.ReasonChain {
	out := correlation.ReasonChain{Why: src.Why}
	for _, s := range src.Steps {
		out.Steps = append(out.Steps, correlation.ReasonStep{
			Key:      s.Key,
			Rule:     s.Rule,
			Factor:   s.Factor,
			Evidence: s.Evidence,
			At:       s.At,
		})
	}
	return out
}
