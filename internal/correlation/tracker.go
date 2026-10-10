// Package correlation tracks entities across cameras/time and groups detections
// into CorrelatedIncident.
package correlation

import (
	"strconv"
	"time"
)

// Track represents a tracked entity.
type Track struct {
	ID           string
	Class        string
	CameraID     string
	FirstSeen    time.Time
	LastSeen     time.Time
	Position     Point
	BBoxHistory  []BBox
	DetectionIDs []string
}

// Tracker tracks entities across time and cameras using coarse position matching.
type Tracker struct {
	tracks      map[string]*Track
	nextID      int
	maxAge      time.Duration
	maxDist     float64
	maxBBoxDist float64
}

// TrackerConfig configures the tracker.
type TrackerConfig struct {
	MaxAge      time.Duration // max age of a track before expiry
	MaxDist     float64       // max distance for position matching
	MaxBBoxDist float64       // max bbox center distance for matching
}

// DefaultTrackerConfig returns sensible defaults.
func DefaultTrackerConfig() TrackerConfig {
	return TrackerConfig{
		MaxAge:      30 * time.Second,
		MaxDist:     150.0, // pixels
		MaxBBoxDist: 100.0, // pixels
	}
}

// NewTracker creates a new tracker.
func NewTracker(cfg TrackerConfig) *Tracker {
	return &Tracker{
		tracks:      make(map[string]*Track),
		maxAge:      cfg.MaxAge,
		maxDist:     cfg.MaxDist,
		maxBBoxDist: cfg.MaxBBoxDist,
	}
}

// Update updates the tracker with new detections and returns updated tracks.
func (t *Tracker) Update(detections []Detection, now time.Time) []*Track {
	matched := make(map[string]bool)

	// Try to match each detection to an existing track
	for _, det := range detections {
		bestTrack := t.bestMatch(det)
		if bestTrack != nil {
			bestTrack.LastSeen = now
			bestTrack.Position = BBoxCenter(det.BBox)
			bestTrack.BBoxHistory = append(bestTrack.BBoxHistory, det.BBox)
			if len(bestTrack.BBoxHistory) > 20 {
				bestTrack.BBoxHistory = bestTrack.BBoxHistory[len(bestTrack.BBoxHistory)-20:]
			}
			bestTrack.DetectionIDs = append(bestTrack.DetectionIDs, det.ID)
			matched[bestTrack.ID] = true
		} else {
			// Create new track
			t.nextID++
			newTrack := &Track{
				ID:           t.generateID(det.Class, det.CameraID),
				Class:        det.Class,
				CameraID:     det.CameraID,
				FirstSeen:    now,
				LastSeen:     now,
				Position:     BBoxCenter(det.BBox),
				BBoxHistory:  []BBox{det.BBox},
				DetectionIDs: []string{det.ID},
			}
			t.tracks[newTrack.ID] = newTrack
			matched[newTrack.ID] = true
		}
	}

	// Remove expired tracks
	for id, track := range t.tracks {
		if !matched[id] && now.Sub(track.LastSeen) > t.maxAge {
			delete(t.tracks, id)
		}
	}

	// Return active tracks
	result := make([]*Track, 0, len(t.tracks))
	for _, track := range t.tracks {
		if matched[track.ID] || now.Sub(track.LastSeen) <= t.maxAge {
			result = append(result, track)
		}
	}
	return result
}

// bestMatch finds the best matching track for a detection.
func (t *Tracker) bestMatch(det Detection) *Track {
	var best *Track
	bestDist := t.maxDist + 1

	detCenter := BBoxCenter(det.BBox)

	for _, track := range t.tracks {
		// Must match class and camera
		if track.Class != det.Class || track.CameraID != det.CameraID {
			continue
		}

		// Check position distance
		dist := distance(track.Position, detCenter)
		if dist > t.maxDist {
			continue
		}

		// Check bbox distance
		if len(track.BBoxHistory) > 0 {
			lastBBox := track.BBoxHistory[len(track.BBoxHistory)-1]
			bboxDist := distance(BBoxCenter(lastBBox), detCenter)
			if bboxDist > t.maxBBoxDist {
				continue
			}
		}

		if dist < bestDist {
			bestDist = dist
			best = track
		}
	}
	return best
}

// generateID generates a deterministic track ID.
//
// The counter is rendered as decimal, not as a rune: `string(rune(n))` is the
// character whose code point is n, which is why the demo's track ids were
// `person-cam-1-\x01` rather than `person-cam-1-1`.
func (t *Tracker) generateID(class, cameraID string) string {
	return class + "-" + cameraID + "-" + strconv.Itoa(t.nextID)
}

// Detection represents a single detection from a camera.
type Detection struct {
	ID         string
	Class      string
	CameraID   string
	BBox       BBox
	Confidence float64
	Timestamp  time.Time
}
