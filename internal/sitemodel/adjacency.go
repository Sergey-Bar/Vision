package sitemodel

import (
	"math"
	"sort"
)

// Adjacency is one derived relation between two cameras, as the schema
// `0026_camera_adjacency.expand.sql` stores it.
//
// The pair is canonicalised here — the same rule the table's CHECK states — so
// a caller cannot accidentally store both orderings of one symmetric fact.
type Adjacency struct {
	CameraID    string
	NeighbourID string
	Relation    string
}

// Relation values. Closed, because a relation the code cannot walk is a
// relation nobody should be able to write.
const (
	// RelationSharesZone: both cameras are declared on the same zone.
	RelationSharesZone = "shares_zone"
	// RelationOverlapsFieldOfView: the cameras' fields of view overlap on the
	// ground.
	RelationOverlapsFieldOfView = "overlaps_field_of_view"
)

// defaultFOVToleranceDegrees is how far the geometry errs toward answering
// "these might see the same ground".
//
// A false neighbour costs a retrieval, a missing neighbour costs an incident —
// so the default errs wide, and it is a declared constant rather than an
// infinity because an infinite tolerance would make every declared view overlap
// every other and the relation would carry no information at all. The first
// version of this function passed `math.MaxFloat64` and the third camera in a
// three-camera fixture overlapped the first, which is exactly what an infinity
// tolerance does.
const defaultFOVToleranceDegrees = 5

// AdjacentCameras derives every adjacency the loaded model states.
//
// Two relations, one row per unordered pair. `shares_zone` is derived from the
// zone declarations alone — the operator already owns them, and two cameras on
// the same restricted zone are adjacent whether or not anyone measured their
// bearings. `overlaps_field_of_view` is derived from the geometry, and is empty
// when a camera has not declared a bearing and an extent, because **an
// unmeasured field of view is not a zero field of view**: a defaulted bearing
// would draw a corridor between two cameras that have never seen each other's
// ground, and the table would then carry a fact the site never asserted.
//
// The result is sorted, so a reload of an unchanged model produces an
// unchanged list and a reconciliation has nothing to rewrite.
func (s *Site) AdjacentCameras() []Adjacency {
	if s == nil {
		return nil
	}
	byZone := map[string][]string{}
	declared := map[string]bool{}
	for _, cam := range s.Cameras {
		if cam.ID == "" {
			continue
		}
		declared[cam.ID] = true
		for _, z := range cam.ZoneIDs {
			byZone[z] = append(byZone[z], cam.ID)
		}
	}
	seen := map[string]bool{}
	var out []Adjacency
	add := func(a, b, relation string) {
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		key := lo + "\x00" + hi + "\x00" + relation
		if lo == hi || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Adjacency{CameraID: lo, NeighbourID: hi, Relation: relation})
	}
	for _, cameras := range byZone {
		sorted := append([]string(nil), cameras...)
		sort.Strings(sorted)
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				add(sorted[i], sorted[j], RelationSharesZone)
			}
		}
	}
	// Field-of-view overlap, for the cameras that have declared enough of a
	// view for the question to be answerable.
	for i := 0; i < len(s.Cameras); i++ {
		for j := i + 1; j < len(s.Cameras); j++ {
			a, b := s.Cameras[i], s.Cameras[j]
			if a.FieldOfView == nil || b.FieldOfView == nil {
				continue
			}
			if a.FieldOfView.overlapsGround(b.FieldOfView, defaultFOVToleranceDegrees) {
				add(a.ID, b.ID, RelationOverlapsFieldOfView)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CameraID != out[j].CameraID {
			return out[i].CameraID < out[j].CameraID
		}
		if out[i].NeighbourID != out[j].NeighbourID {
			return out[i].NeighbourID < out[j].NeighbourID
		}
		return out[i].Relation < out[j].Relation
	})
	return out
}

// neighboursOf answers the one question the product asks the table: which
// cameras are adjacent to this one, in either direction.
func (s *Site) neighboursOf(cameraID string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, adj := range s.AdjacentCameras() {
		if adj.CameraID == cameraID {
			add(adj.NeighbourID)
		}
		if adj.NeighbourID == cameraID {
			add(adj.CameraID)
		}
	}
	sort.Strings(out)
	return out
}

// FieldOfView is what a camera has declared it can see: a bearing in degrees
// and an angular extent. Both are optional because most sites have not
// measured theirs, and a nil FOV is the honest answer to "what does this camera
// see on the ground" at those sites.
type FieldOfView struct {
	// BearingDegrees is the centre of the view, degrees clockwise from north.
	BearingDegrees *float64
	// ExtentDegrees is the angular width of the view. A nil extent is a
	// camera pointing in a declared direction with an unmapped width, and the
	// overlap question for it is answered as "unknown", not as "everything".
	ExtentDegrees *float64
	// RangeMetres is how far the camera is useful, when the site knows it.
	RangeMetres *float64
}

// declared reports whether the camera has declared enough of a view for a
// ground-overlap question to be answerable at all.
func (f *FieldOfView) declared() bool {
	return f != nil && f.BearingDegrees != nil && f.ExtentDegrees != nil
}

// overlapsGround answers whether two declared views overlap, within the
// tolerance a caller declares.
//
// The geometry is deliberately the coarsest one that answers the question:
// two angular wedges centred on each camera's bearing, with extents, overlap
// when the angular distance between bearings is no more than half the sum of
// the extents. That is a planar approximation of a ground-plane question — the
// ground is not planar and a camera at height sees a wedge, not a sector — so
// the answer is an input to retrieval ("the camera next door might have seen
// this continue") and never a conclusion about what is or is not on any
// recording. The tolerance widens the answer for the same reason: a false
// neighbour loses a retrieval, a missing neighbour loses an incident.
//
// Undeclared views never overlap. See AdjacentCameras.
func (f *FieldOfView) overlapsGround(other *FieldOfView, tolerance float64) bool {
	if !f.declared() || !other.declared() {
		return false
	}
	extentSum := (*f.ExtentDegrees+*other.ExtentDegrees)/2 + tolerance
	between := angleBetween(*f.BearingDegrees, *other.BearingDegrees)
	return between <= extentSum
}

// angleBetween is the smaller angular distance between two bearings, in
// degrees, in [0,180].
func angleBetween(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}
