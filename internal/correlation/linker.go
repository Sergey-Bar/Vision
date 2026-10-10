// Package correlation tracks entities across cameras/time and groups detections
// into CorrelatedIncident.
package correlation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"
)

// LinkerConfig configures the linker.
type LinkerConfig struct {
	MaxTimeGap     time.Duration // max time gap between detections in same incident
	MaxSpatialDist float64       // max spatial distance for cross-camera linking
	MinDetections  int           // minimum detections to form an incident
	Cooldown       time.Duration // cooldown before creating new incident for same entity
}

// DefaultLinkerConfig returns sensible defaults.
func DefaultLinkerConfig() LinkerConfig {
	return LinkerConfig{
		MaxTimeGap:     60 * time.Second,
		MaxSpatialDist: 200.0,
		MinDetections:  2,
		Cooldown:       30 * time.Second,
	}
}

// Linker groups tracks into correlated incidents.
type Linker struct {
	cfg              LinkerConfig
	siteID           string
	incidents        map[string]*CorrelatedIncident
	entityIncident   map[string]string    // entityID -> incidentID
	lastIncidentTime map[string]time.Time // entityID -> last incident time
	nextIncidentID   int
}

// NewLinker creates a new linker.
func NewLinker(siteID string, cfg LinkerConfig) *Linker {
	return &Linker{
		cfg:              cfg,
		siteID:           siteID,
		incidents:        make(map[string]*CorrelatedIncident),
		entityIncident:   make(map[string]string),
		lastIncidentTime: make(map[string]time.Time),
	}
}

// Link processes tracks and updates/creates correlated incidents.
func (l *Linker) Link(tracks []*Track, now time.Time) []*CorrelatedIncident {
	// Group tracks by class
	tracksByClass := make(map[string][]*Track)
	for _, track := range tracks {
		tracksByClass[track.Class] = append(tracksByClass[track.Class], track)
	}

	// For each class, try to link tracks across cameras
	for _, classTracks := range tracksByClass {
		l.linkClass(classTracks, now)
	}

	// Update incident states and clean up old ones
	l.updateIncidents(now)

	// Return active incidents
	result := make([]*CorrelatedIncident, 0, len(l.incidents))
	for _, inc := range l.incidents {
		if inc.State == "ACTIVE" || inc.State == "VERIFYING" || inc.State == "ESCALATING" {
			result = append(result, inc)
		}
	}

	// Sort by last seen descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastSeen.After(result[j].LastSeen)
	})

	return result
}

// linkClass links tracks of the same class across cameras.
func (l *Linker) linkClass(tracks []*Track, now time.Time) {
	// The minimum is over DETECTIONS, not over distinct tracks. A single person
	// standing still for twenty seconds is one track carrying twenty
	// detections, and it is an incident; a single stray frame is one track
	// carrying one detection, and it is not. Counting tracks would refuse the
	// first and admit the second.
	totalDetections := 0
	for _, t := range tracks {
		totalDetections += len(t.DetectionIDs)
	}
	if totalDetections < l.cfg.MinDetections {
		return
	}

	// Sort tracks by first seen
	sort.Slice(tracks, func(i, j int) bool {
		return tracks[i].FirstSeen.Before(tracks[j].FirstSeen)
	})

	// Try to find or create an incident for these tracks
	for _, track := range tracks {
		// Check if this track is already linked to an incident
		if incID, ok := l.entityIncident[track.ID]; ok {
			inc := l.incidents[incID]
			if inc != nil && inc.State != "RESOLVED" && inc.State != "OVERRIDDEN" {
				l.addTrackToIncident(inc, track, now)
				continue
			}
		}

		// Check cooldown
		if lastTime, ok := l.lastIncidentTime[track.ID]; ok {
			if now.Sub(lastTime) < l.cfg.Cooldown {
				continue
			}
		}

		// Try to find an existing incident to join
		inc := l.findIncidentForTrack(track, now)
		if inc != nil {
			l.addTrackToIncident(inc, track, now)
		} else {
			// Create new incident
			l.createIncident(track, now)
		}
	}
}

// findIncidentForTrack finds an existing incident that this track could belong to.
func (l *Linker) findIncidentForTrack(track *Track, now time.Time) *CorrelatedIncident {
	for _, inc := range l.incidents {
		if inc.State == "RESOLVED" || inc.State == "OVERRIDDEN" {
			continue
		}

		// Check time gap
		if now.Sub(inc.LastSeen) > l.cfg.MaxTimeGap {
			continue
		}

		// Check if same class
		hasSameClass := false
		for _, e := range inc.Entities {
			if e.Class == track.Class {
				hasSameClass = true
				break
			}
		}
		if !hasSameClass {
			continue
		}

		// Check spatial proximity to any entity in the incident
		for _, entity := range inc.Entities {
			if entity.CameraID == track.CameraID {
				// Same camera - check position
				if len(entity.BBoxHistory) > 0 && len(track.BBoxHistory) > 0 {
					lastEntityBBox := entity.BBoxHistory[len(entity.BBoxHistory)-1]
					lastTrackBBox := track.BBoxHistory[len(track.BBoxHistory)-1]
					dist := distance(BBoxCenter(lastEntityBBox), BBoxCenter(lastTrackBBox))
					if dist <= l.cfg.MaxSpatialDist {
						return inc
					}
				}
			} else {
				// Different camera - check if cameras are adjacent in site topology
				// For now, allow cross-camera linking if time is close
				if now.Sub(inc.LastSeen) < l.cfg.MaxTimeGap/2 {
					return inc
				}
			}
		}
	}
	return nil
}

// addTrackToIncident adds a track to an existing incident.
func (l *Linker) addTrackToIncident(inc *CorrelatedIncident, track *Track, now time.Time) {
	// Check if track is already in incident
	for i := range inc.Entities {
		if inc.Entities[i].ID == track.ID {
			// Update existing entity
			inc.Entities[i].LastSeen = now
			inc.Entities[i].BBoxHistory = track.BBoxHistory
			inc.DetectionIDs = append(inc.DetectionIDs, track.DetectionIDs...)
			inc.LastSeen = now
			return
		}
	}

	// Add new entity
	entity := EntityRef{
		Class:       track.Class,
		CameraID:    track.CameraID,
		FirstSeen:   track.FirstSeen,
		LastSeen:    track.LastSeen,
		BBoxHistory: track.BBoxHistory,
	}
	inc.Entities = append(inc.Entities, entity)
	inc.DetectionIDs = append(inc.DetectionIDs, track.DetectionIDs...)

	// Add camera if not present
	found := false
	for _, camID := range inc.CameraIDs {
		if camID == track.CameraID {
			found = true
			break
		}
	}
	if !found {
		inc.CameraIDs = append(inc.CameraIDs, track.CameraID)
	}

	inc.LastSeen = now
	l.entityIncident[track.ID] = inc.ID
}

// createIncident creates a new correlated incident from a track.
func (l *Linker) createIncident(track *Track, now time.Time) {
	l.nextIncidentID++
	incID := l.generateIncidentID(track.Class, now)

	entity := EntityRef{
		Class:       track.Class,
		CameraID:    track.CameraID,
		FirstSeen:   track.FirstSeen,
		LastSeen:    track.LastSeen,
		BBoxHistory: track.BBoxHistory,
	}

	inc := &CorrelatedIncident{
		ID:            incID,
		SiteID:        l.siteID,
		State:         "ACTIVE",
		FirstSeen:     track.FirstSeen,
		LastSeen:      track.LastSeen,
		CameraIDs:     []string{track.CameraID},
		DetectionIDs:  track.DetectionIDs,
		Entities:      []EntityRef{entity},
		Anomalies:     []Anomaly{},
		ReasonChain:   ReasonChain{Steps: []ReasonStep{}, Why: ""},
		ThreatScore:   0,
		Action:        "OBSERVE",
		AutonomyState: "AUTO",
		Provenance: ProvenanceExt{
			SiteID:        l.siteID,
			CorrelationID: incID,
		},
	}

	l.incidents[incID] = inc
	l.entityIncident[track.ID] = incID
	l.lastIncidentTime[track.ID] = now
}

// generateIncidentID generates a deterministic incident ID.
func (l *Linker) generateIncidentID(class string, now time.Time) string {
	data := fmt.Sprintf("%s-%s-%d-%d", l.siteID, class, now.UnixMilli(), l.nextIncidentID)
	sum := sha256.Sum256([]byte(data))
	return "inc-" + hex.EncodeToString(sum[:])[:12]
}

// updateIncidents updates incident states and cleans up old ones.
func (l *Linker) updateIncidents(now time.Time) {
	for _, inc := range l.incidents {
		// Mark as resolved if no updates for a while
		if inc.State == "ACTIVE" && now.Sub(inc.LastSeen) > l.cfg.MaxTimeGap*2 {
			inc.State = "RESOLVED"
		}
	}
}

// GetIncident returns an incident by ID.
func (l *Linker) GetIncident(id string) *CorrelatedIncident {
	return l.incidents[id]
}

// AllIncidents returns all incidents.
func (l *Linker) AllIncidents() []*CorrelatedIncident {
	result := make([]*CorrelatedIncident, 0, len(l.incidents))
	for _, inc := range l.incidents {
		result = append(result, inc)
	}
	return result
}

// AddAnomaly adds an anomaly to an incident.
func (l *Linker) AddAnomaly(incidentID string, anomaly Anomaly) {
	inc := l.incidents[incidentID]
	if inc == nil {
		return
	}
	inc.Anomalies = append(inc.Anomalies, anomaly)
	inc.LastSeen = anomaly.LastSeen
}

// UpdateIncidentState updates an incident's state.
func (l *Linker) UpdateIncidentState(incidentID, state string) {
	inc := l.incidents[incidentID]
	if inc == nil {
		return
	}
	inc.State = state
}

// UpdateIncidentAction updates an incident's action and autonomy state.
func (l *Linker) UpdateIncidentAction(incidentID, action, autonomyState string) {
	inc := l.incidents[incidentID]
	if inc == nil {
		return
	}
	inc.Action = action
	inc.AutonomyState = autonomyState
}

// UpdateIncidentThreatScore updates an incident's threat score and reason chain.
func (l *Linker) UpdateIncidentThreatScore(incidentID string, score int, chain ReasonChain) {
	inc := l.incidents[incidentID]
	if inc == nil {
		return
	}
	inc.ThreatScore = score
	inc.ReasonChain = chain
}

// distance returns the Euclidean distance between two points.
func distance(a, b Point) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return math.Hypot(dx, dy)
}

// BBoxCenter returns the center of a bounding box.
func BBoxCenter(b BBox) Point {
	return Point{X: b.X + b.Width/2, Y: b.Y + b.Height/2}
}
