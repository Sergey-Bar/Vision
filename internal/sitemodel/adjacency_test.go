package sitemodel

import (
	"math"
	"testing"
)

func f(v float64) *float64 { return &v }

// TestTwoCamerasOnTheSameZoneAreAdjacent.
//
// The zone declarations are the input the operator already owns. Two cameras on
// the same zone are adjacent whether or not anyone measured their bearings, and
// this is the relation that works on every site.
func TestTwoCamerasOnTheSameZoneAreAdjacent(t *testing.T) {
	site := &Site{
		ID: "s1",
		Cameras: []Camera{
			{ID: "cam-a", ZoneIDs: []string{"z1"}},
			{ID: "cam-b", ZoneIDs: []string{"z1", "z2"}},
			{ID: "cam-c", ZoneIDs: []string{"z3"}},
		},
	}

	got := site.AdjacentCameras()
	if len(got) != 1 {
		t.Fatalf("derived %d adjacencies, want exactly 1 (cam-a ~ cam-b): %+v", len(got), got)
	}
	if got[0].CameraID != "cam-a" || got[0].NeighbourID != "cam-b" {
		t.Errorf("the pair is %s~%s, want cam-a~cam-b", got[0].CameraID, got[0].NeighbourID)
	}
	if got[0].Relation != RelationSharesZone {
		t.Errorf("relation is %q, want %q", got[0].Relation, RelationSharesZone)
	}
}

// TestThePairIsCanonicalised.
//
// Adjacency is symmetric, so it is stored once. A mirror row would make
// "neighbours of A" answer about a pair that was never recorded, and would give
// the table two rows to revoke for one fact. The derivation canonicalises the
// pair the same way the schema's CHECK does — two independent statements of one
// rule, which is what a rule that matters looks like.
func TestThePairIsCanonicalised(t *testing.T) {
	site := &Site{
		ID:      "s1",
		Cameras: []Camera{{ID: "cam-z", ZoneIDs: []string{"z1"}}, {ID: "cam-a", ZoneIDs: []string{"z1"}}},
	}
	got := site.AdjacentCameras()
	if len(got) != 1 {
		t.Fatalf("derived %d adjacencies, want 1: %+v", len(got), got)
	}
	if got[0].CameraID != "cam-a" || got[0].NeighbourID != "cam-z" {
		t.Errorf("the pair is %s~%s; the canonical form is the lexicographically "+
			"smaller id first", got[0].CameraID, got[0].NeighbourID)
	}
}

// TestAnUnmeasuredFieldOfViewProducesNoOverlap.
//
// **An unmeasured field of view is not a zero field of view.** A defaulted
// bearing would draw a corridor between two cameras that have never seen each
// other's ground, and the table would then carry a fact the site never
// asserted. This is the same refusal the adapter makes for an unresolved event
// type, and it belongs here because adjacency feeds retrieval: a false
// neighbour costs a lookup, a fabricated one costs a wrong answer with
// confidence in it.
func TestAnUnmeasuredFieldOfViewProducesNoOverlap(t *testing.T) {
	site := &Site{
		ID: "s1",
		Cameras: []Camera{
			{ID: "cam-a", FieldOfView: nil},
			{ID: "cam-b", FieldOfView: fvFor(10, 40)},
		},
	}
	if got := site.AdjacentCameras(); len(got) != 0 {
		t.Errorf("derived %d overlaps from a camera with no declared view: %+v", len(got), got)
	}

	// Half a view is still no view: a bearing with no extent is unknown, not
	// everything.
	site.Cameras[0].FieldOfView = &FieldOfView{BearingDegrees: f(11)}
	if got := site.AdjacentCameras(); len(got) != 0 {
		t.Errorf("derived %d overlaps from a camera with a bearing and no extent: %+v", len(got), got)
	}
}

// TestOverlappingFieldsOfViewDeriveTheOverlapRelation.
//
// The geometry is a planar approximation on purpose: two cameras at 10 and 14
// degrees with wide extents see the same ground, and one at 10 and 170 do not.
func TestOverlappingFieldsOfViewDeriveTheOverlapRelation(t *testing.T) {
	site := &Site{
		ID: "s1",
		Cameras: []Camera{
			{ID: "cam-a", FieldOfView: fvFor(10, 40)},
			{ID: "cam-b", FieldOfView: fvFor(14, 40)},
			{ID: "cam-c", FieldOfView: fvFor(170, 30)},
		},
	}
	got := site.AdjacentCameras()
	if len(got) != 1 {
		t.Fatalf("derived %d overlaps, want 1: %+v", len(got), got)
	}
	if got[0].Relation != RelationOverlapsFieldOfView {
		t.Errorf("relation is %q, want %q", got[0].Relation, RelationOverlapsFieldOfView)
	}
	if got[0].CameraID != "cam-a" || got[0].NeighbourID != "cam-b" {
		t.Errorf("the pair is %s~%s, want cam-a~cam-b", got[0].CameraID, got[0].NeighbourID)
	}
}

// TestACameraIsNotItsOwnNeighbour.
//
// The schema refuses it too (`camera_adjacency_no_self`), and this is the other
// half of that: a derivation that linked a camera to itself would fail at write
// time on a site nobody can debug from the table's contents.
func TestACameraIsNotItsOwnNeighbour(t *testing.T) {
	site := &Site{ID: "s1", Cameras: []Camera{{ID: "cam-a", ZoneIDs: []string{"z1", "z1"}}}}
	for _, adj := range site.AdjacentCameras() {
		if adj.CameraID == adj.NeighbourID {
			t.Fatalf("a camera was linked to itself: %+v", adj)
		}
	}
}

// TestNeighboursOfAnswersInBothDirections.
//
// The pair is stored once; the question is symmetric. An implementation that
// only matched `camera_id` would answer "no neighbours" for the lexically
// larger camera on every pair, which is the quietest possible wrong answer —
// it looks like a sparse site.
func TestNeighboursOfAnswersInBothDirections(t *testing.T) {
	site := &Site{ID: "s1", Cameras: []Camera{
		{ID: "cam-a", ZoneIDs: []string{"z1"}},
		{ID: "cam-z", ZoneIDs: []string{"z1"}},
	}}
	for _, id := range []string{"cam-a", "cam-z"} {
		got := site.neighboursOf(id)
		if len(got) != 1 {
			t.Fatalf("neighbours of %s: %v, want one", id, got)
		}
		want := "cam-z"
		if id == "cam-z" {
			want = "cam-a"
		}
		if got[0] != want {
			t.Errorf("neighbours of %s: got %s, want %s", id, got[0], want)
		}
	}
}

// TestTheDerivationIsStableAcrossReloads.
//
// A projection that reorders is a projection a reconciliation rewrites on every
// load, and "did anything change?" becomes unanswerable from the table.
func TestTheDerivationIsStableAcrossReloads(t *testing.T) {
	model := &Site{ID: "s1", Cameras: []Camera{
		{ID: "cam-b", ZoneIDs: []string{"z1", "z2"}},
		{ID: "cam-a", ZoneIDs: []string{"z2"}},
		{ID: "cam-c", ZoneIDs: []string{"z1"}},
	}}
	first := model.AdjacentCameras()
	for i := 0; i < 25; i++ {
		again := model.AdjacentCameras()
		if len(again) != len(first) {
			t.Fatalf("reload %d derived %d adjacencies, the first derived %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("reload %d position %d differs: %+v vs %+v", i, j, again[j], first[j])
			}
		}
	}
}

// TestTheLoadedYAMLCarriesAViewWhenTheSiteDeclaresOne.
//
// The loader keeps nil as nil. A site that has not measured its cameras gets
// no overlaps rather than false ones, and a site that has gets them — the
// distinction is the whole point of the optional block.
func TestTheLoadedYAMLCarriesAViewWhenTheSiteDeclaresOne(t *testing.T) {
	loaded, err := LoadFromBytes([]byte(`
site:
  id: s1
  cameras:
    - id: cam-a
      zone_ids: [z1]
      field_of_view:
        bearing_degrees: 10
        extent_degrees: 40
    - id: cam-b
      zone_ids: [z1]
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Cameras[0].FieldOfView == nil {
		t.Fatal("the declared field_of_view was dropped; the derivation cannot " +
			"answer the overlap question for a site that answered it")
	}
	if *loaded.Cameras[0].FieldOfView.BearingDegrees != 10 {
		t.Errorf("bearing is %v, want 10", *loaded.Cameras[0].FieldOfView.BearingDegrees)
	}
	if loaded.Cameras[1].FieldOfView != nil {
		t.Error("a camera with no field_of_view acquired one; an unmeasured view " +
			"must stay nil rather than become a zero view")
	}
}

// TestAngleBetweenWrapsAroundNorth.
//
// A camera at 359 degrees faces a camera at 1 degree: one degree apart, across
// north. The naive answer is 358.
func TestAngleBetweenWrapsAroundNorth(t *testing.T) {
	if got := angleBetween(359, 1); math.Abs(got-2) > 1e-9 {
		t.Errorf("angleBetween(359, 1) = %v, want 2", got)
	}
	if got := angleBetween(90, 270); math.Abs(got-180) > 1e-9 {
		t.Errorf("angleBetween(90, 270) = %v, want 180", got)
	}
}

// TestTheToleranceWidensTheAnswerForTheSameReasonItExists.
//
// A false neighbour costs a retrieval; a missing neighbour costs an incident.
// The tolerance argument is the operator's judgment about that trade, and the
// default is wide rather than clever.
func TestTheToleranceWidensTheAnswerForTheSameReasonItExists(t *testing.T) {
	a, b := fvFor(0, 10), fvFor(12, 10)
	if a.overlapsGround(b, 0) {
		t.Error("ten-degree extents 12 degrees apart overlap with no tolerance")
	}
	if !a.overlapsGround(b, 5) {
		t.Error("a 5-degree tolerance did not widen the answer; the tolerance is " +
			"the operator's judgment about a false neighbour versus a missing one")
	}
}

func fvFor(bearing, extent float64) *FieldOfView {
	return &FieldOfView{BearingDegrees: &bearing, ExtentDegrees: &extent}
}
