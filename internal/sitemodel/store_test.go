package sitemodel_test

import (
	"context"
	"os"
	"testing"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// store builds the service against a migrated, per-package database.
func store(t *testing.T) (*sitemodel.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	wrapped, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(wrapped.Close)
	return &sitemodel.Service{Pool: wrapped}, tenant, site
}

func siteOf(cameras ...sitemodel.Camera) *sitemodel.Site {
	return &sitemodel.Site{ID: "s", Cameras: cameras}
}

// TestTheDerivedAdjacencyRoundTrips.
//
// The derivation is pure and tested above; this is the write path: what the
// model derived is what the service stores, and a read answers in either
// direction of the pair the table keeps once.
func TestTheDerivedAdjacencyRoundTrips(t *testing.T) {
	svc, tenant, site := store(t)
	ctx := context.Background()

	model := siteOf(
		sitemodel.Camera{ID: "cam-a", ZoneIDs: []string{"z1"}},
		sitemodel.Camera{ID: "cam-z", ZoneIDs: []string{"z1"}},
		sitemodel.Camera{ID: "cam-c", ZoneIDs: []string{"z3"}},
	)
	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-1", model); err != nil {
		t.Fatalf("ReplaceDerivedAdjacency: %v", err)
	}

	got, err := svc.Neighbours(ctx, tenant, site, "cam-a")
	if err != nil {
		t.Fatalf("AdjacentCameras: %v", err)
	}
	if len(got) != 1 || got[0] != "cam-z" {
		t.Fatalf("neighbours of cam-a: %v, want [cam-z]", got)
	}
	// The other direction of the stored pair.
	got, err = svc.Neighbours(ctx, tenant, site, "cam-z")
	if err != nil {
		t.Fatalf("AdjacentCameras: %v", err)
	}
	if len(got) != 1 || got[0] != "cam-a" {
		t.Errorf("neighbours of cam-z: %v, want [cam-a]. The pair is stored once "+
			"and the question is symmetric", got)
	}
}

// TestAReloadRemovesWhatTheModelNoLongerStates.
//
// The table is a projection, not an append log. A camera removed from a zone
// stops being its neighbour's neighbour on the next reload — which is what the
// delete-before-insert in the service exists for.
func TestAReloadRemovesWhatTheModelNoLongerStates(t *testing.T) {
	svc, tenant, site := store(t)
	ctx := context.Background()

	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-1",
		siteOf(
			sitemodel.Camera{ID: "cam-a", ZoneIDs: []string{"z1"}},
			sitemodel.Camera{ID: "cam-b", ZoneIDs: []string{"z1"}},
		)); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-2",
		siteOf(sitemodel.Camera{ID: "cam-a", ZoneIDs: nil})); err != nil {
		t.Fatalf("second load: %v", err)
	}

	got, err := svc.Neighbours(ctx, tenant, site, "cam-a")
	if err != nil {
		t.Fatalf("AdjacentCameras: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("neighbours of cam-a after the zone was removed: %v, want none. "+
			"A projection that accumulates stale facts is worse than one that "+
			"recomputes", got)
	}
}

// TestTheStoredRelationIsTheDerivedOne,AndTheDerivationAndTheReadAgree.
//
// Three claims in one: the row carries the vocabulary the code declares (not a
// second spelling), the read answers what the pure derivation answers, and the
// relation that survived is the one the model stated. A table whose `relation`
// column and whose code disagree is a table a reader cannot trust.
func TestTheStoredRelationIsTheDerivedOneAndTheReadAgrees(t *testing.T) {
	svc, tenant, site := store(t)
	ctx := context.Background()

	model := siteOf(
		sitemodel.Camera{ID: "cam-a", ZoneIDs: []string{"z1"}},
		sitemodel.Camera{ID: "cam-z", ZoneIDs: []string{"z1"}},
	)
	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-1", model); err != nil {
		t.Fatalf("ReplaceFromModel: %v", err)
	}

	var relation, source string
	err := testutil.Pool(t).QueryRow(ctx,
		`SELECT relation, source FROM camera_adjacency
		  WHERE tenant_id = $1 AND site_id = $2 AND camera_id = 'cam-a'`,
		tenant, site).Scan(&relation, &source)
	if err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if relation != sitemodel.RelationSharesZone {
		t.Errorf("the stored relation is %q, want %q. The vocabulary lives in the "+
			"code the table is written by", relation, sitemodel.RelationSharesZone)
	}
	if source != sitemodel.ModelSource {
		t.Errorf("the stored source is %q, want %q", source, sitemodel.ModelSource)
	}

	// The read answers in both directions of the pair. The pair's derivation is
	// asserted by the internal test, which owns the pure logic.
	derived := siteOf(
		sitemodel.Camera{ID: "cam-a", ZoneIDs: []string{"z1"}},
		sitemodel.Camera{ID: "cam-z", ZoneIDs: []string{"z1"}},
	).AdjacentCameras()
	stored, err := svc.Neighbours(ctx, tenant, site, "cam-z")
	if err != nil {
		t.Fatalf("Neighbours: %v", err)
	}
	if len(derived) != 1 || len(stored) != 1 || derived[0].CameraID != stored[0] {
		t.Errorf("the read says %v and the derivation says [cam-a ~ cam-z]", stored)
	}
	// And the derivation itself is what the write is derived from: an empty
	// model derives no adjacency at all.
	empty := &sitemodel.Site{ID: "s"}
	if got := empty.AdjacentCameras(); len(got) != 0 {
		t.Errorf("a site with no cameras derived %d adjacencies", len(got))
	}
}

// TestAnOverlappingFieldOfViewRoundTrips.
//
// The other relation, asserted on the write path it has no other coverage for:
// a site that declared two cameras' bearings gets the overlap relation stored,
// with the vocabulary the code declares. Without this the FOV relation is
// tested only in the negative — which is the shape of a feature nobody has
// watched work.
func TestAnOverlappingFieldOfViewRoundTrips(t *testing.T) {
	svc, tenant, site := store(t)
	ctx := context.Background()

	bearingA, extent := 10.0, 40.0
	bearingB := 14.0
	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-1", siteOf(
		sitemodel.Camera{ID: "cam-a", FieldOfView: &sitemodel.FieldOfView{
			BearingDegrees: &bearingA, ExtentDegrees: &extent,
		}},
		sitemodel.Camera{ID: "cam-b", FieldOfView: &sitemodel.FieldOfView{
			BearingDegrees: &bearingB, ExtentDegrees: &extent,
		}},
	)); err != nil {
		t.Fatalf("ReplaceFromModel: %v", err)
	}

	var relation string
	err := testutil.Pool(t).QueryRow(ctx,
		`SELECT relation FROM camera_adjacency
		  WHERE tenant_id = $1 AND site_id = $2 AND camera_id = 'cam-a'`,
		tenant, site).Scan(&relation)
	if err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if relation != sitemodel.RelationOverlapsFieldOfView {
		t.Errorf("the stored relation is %q, want %q. Two declared views 4 degrees "+
			"apart with wide extents overlap on the ground", relation,
			sitemodel.RelationOverlapsFieldOfView)
	}
}

// TestAnUndeclaredFieldOfViewWritesNoOverlap.
//
// The privacy-shaped rule from the derivation, asserted on the write path: a
// camera that declared no view writes no overlap row rather than a zero or a
// defaulted one.
func TestAnUndeclaredFieldOfViewWritesNoOverlap(t *testing.T) {
	svc, tenant, site := store(t)
	ctx := context.Background()

	bearing, extent := 10.0, 40.0
	if err := svc.ReplaceFromModel(ctx, tenant, site, "model-1", siteOf(
		sitemodel.Camera{ID: "cam-a"},
		sitemodel.Camera{ID: "cam-b", FieldOfView: &sitemodel.FieldOfView{
			BearingDegrees: &bearing, ExtentDegrees: &extent,
		}},
	)); err != nil {
		t.Fatalf("ReplaceDerivedAdjacency: %v", err)
	}
	for _, id := range []string{"cam-a", "cam-b"} {
		got, err := svc.Neighbours(ctx, tenant, site, id)
		if err != nil {
			t.Fatalf("AdjacentCameras: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("neighbours of %s: %v, want none. An unmeasured view is not "+
				"a zero view", id, got)
		}
	}
}
