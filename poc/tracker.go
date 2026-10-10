package main

import (
	"fmt"
	"sync"
	"time"
)

// tracker gives detections a stable identity across frames.
//
// The sidecar answers per frame with no memory, so five cars in frame 12 are
// five anonymous boxes again in frame 13. Zone presence, dwell timing and
// per-rule cooldowns all key on an object id — without continuity, every frame
// would read as a fresh entry and the alert feed would flood with "entry" at
// the capture rate. This is the smallest identity that fixes that: greedy
// nearest-neighbour matching on class + centre distance, with an expiry.
//
// It is not a tracker in the vision sense — no prediction, no appearance
// features, re-identification after an occlusion creates a new id — because
// everything downstream only needs an id to stay stable WHILE the object is
// continuously visible, which this provides.
type tracker struct {
	mu     sync.Mutex
	nextID int
	tracks []*track
}

type track struct {
	id       string
	key      string // category|sub: the match class
	cx, cy   float64
	lastSeen time.Time
}

const (
	// matchRadiusPix is how far a detection's centre may move between frames
	// and still be the same track. At 1 fps capture a car at 60 km/h moves
	// ~17 px; 220 px is generous room for that plus detection jitter while
	// still separating two nearby objects of the same class.
	matchRadiusPix = 220.0
	// trackTTL is how long an unmatched track is remembered.
	trackTTL = 6 * time.Second
)

// update assigns stable ids to this frame's detections and expires stale
// tracks. Detections are matched in order; each track is taken by at most one
// detection per frame.
func (t *tracker) update(dets []frameObject, now time.Time) []frameObject {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.tracks {
		if now.Sub(t.tracks[i].lastSeen) > trackTTL {
			t.tracks[i] = nil
		}
	}
	kept := t.tracks[:0]
	for _, tr := range t.tracks {
		if tr != nil {
			kept = append(kept, tr)
		}
	}
	t.tracks = kept

	used := make([]bool, len(t.tracks))
	for i := range dets {
		d := &dets[i]
		key := d.Category + "|" + d.Sub
		cx := d.BBox.X + d.BBox.Width/2
		cy := d.BBox.Y + d.BBox.Height/2

		best, bestDist := -1, matchRadiusPix
		for i, tr := range t.tracks {
			if used[i] || tr.key != key {
				continue
			}
			dx := tr.cx - cx
			dy := tr.cy - cy
			dist := dx*dx + dy*dy
			if dist <= bestDist*bestDist {
				best, bestDist = i, dist
			}
		}
		if best >= 0 {
			tr := t.tracks[best]
			used[best] = true
			d.ID = tr.id
			tr.cx, tr.cy = cx, cy
			tr.lastSeen = now
			continue
		}
		t.nextID++
		id := fmt.Sprintf("t%04d", t.nextID)
		t.tracks = append(t.tracks, &track{
			id: id, key: key, cx: cx, cy: cy, lastSeen: now,
		})
		used = append(used, true)
		d.ID = id
	}
	return dets
}
