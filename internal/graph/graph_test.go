package graph_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/graph"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func service(t *testing.T) (*graph.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	wrapped, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(wrapped.Close)
	tenant, site, _ := testutil.NewTenant(t, raw)
	return &graph.Service{Pool: wrapped}, tenant, site
}
func topologyEdge(tenant, site string) graph.Edge {
	return graph.Edge{
		TenantID: tenant, EdgeID: "e-site-zone",
		FromType: "SITE", FromID: site, Relation: "contains",
		ToType: "ZONE", ToID: "zone-entrance",
		Provenance: graph.Provenance{
			AssertedBy: "adapter:milestone", Method: "asserted", Confidence: 1.0,
		},
	}
}

// An edge with no provenance is refused. It is the one somebody would want to be
// able to deny, which is why the schema requires the field and this requires it
// before the write.
func TestEdgeRequiresProvenance(t *testing.T) {
	e := topologyEdge("t", "s")
	e.AssertedBy = ""
	err := e.Validate()
	if err == nil {
		t.Fatal("an edge with no asserted_by was accepted")
	}
	if !strings.Contains(err.Error(), "provenance") {
		t.Errorf("error should name the missing provenance, got %q", err)
	}
}

func TestEdgeVocabularyIsClosed(t *testing.T) {
	cases := []graph.Edge{
		func() graph.Edge { e := topologyEdge("t", "s"); e.Relation = "knows"; return e }(),
		func() graph.Edge { e := topologyEdge("t", "s"); e.Method = "guessed"; return e }(),
		func() graph.Edge { e := topologyEdge("t", "s"); e.Confidence = 1.5; return e }(),
		func() graph.Edge { e := topologyEdge("t", "s"); e.TenantID = ""; return e }(),
		func() graph.Edge {
			e := topologyEdge("t", "s")
			e.Method = "inferred"
			return e
		}(),
	}
	for i, e := range cases {
		if err := e.Validate(); err == nil {
			t.Errorf("case %d: an invalid edge was accepted", i)
		}
	}

	// An inferred edge that names what inferred it is fine.
	good := topologyEdge("t", "s")
	good.Method = "inferred"
	good.RuleOrModelID = "core.zone-by-path"
	if err := good.Validate(); err != nil {
		t.Errorf("a properly attributed inferred edge was rejected: %v", err)
	}
}

func TestGraphBuildsTopologyAndTraverses(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	edges := []graph.Edge{
		topologyEdge(tenant, site),
		{
			TenantID: tenant, EdgeID: "e-zone-camera",
			FromType: "ZONE", FromID: "zone-entrance", Relation: "contains",
			ToType: "DEVICE", ToID: "cam-1",
			Provenance: graph.Provenance{AssertedBy: "adapter:milestone", Method: "observed", Confidence: 1.0},
		},
	}
	added, err := svc.AddEdges(ctx, edges)
	if err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	if added != 2 {
		t.Errorf("added %d edges, want 2", added)
	}

	// Re-adding is a no-op, not an error: a replayed event re-asserts topology.
	again, err := svc.AddEdges(ctx, edges)
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if again != 0 {
		t.Errorf("re-adding added %d edges; a duplicate must be silent", again)
	}

	result, err := svc.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 3})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(result.EdgeIDs) != 2 {
		t.Errorf("traversal found %d edges, want 2: %+v", len(result.EdgeIDs), result.EdgeIDs)
	}
	if result.VisitedAtDepth != 2 {
		t.Errorf("traversal reached depth %d, want 2", result.VisitedAtDepth)
	}
}

// Traversal is bounded, because an untrusted adapter supplies the node names and
// an unbounded walk is a way to consume the database.
func TestTraversalIsBounded(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	// A chain of 50 zones rooted at the site, deeper than the depth limit.
	edges := []graph.Edge{topologyEdge(tenant, site)}
	edges[0].ToID = "zone-0"
	for i := 1; i < 50; i++ {
		edges = append(edges, graph.Edge{
			TenantID: tenant, EdgeID: "chain-" + itoa(i),
			FromType: "ZONE", FromID: "zone-" + itoa(i-1), Relation: "contains",
			ToType: "ZONE", ToID: "zone-" + itoa(i),
			Provenance: graph.Provenance{AssertedBy: "test", Method: "asserted", Confidence: 1},
		})
	}
	if _, err := svc.AddEdges(ctx, edges); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	shallow, err := svc.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 3})
	if err != nil {
		t.Fatalf("Walk shallow: %v", err)
	}
	if len(shallow.EdgeIDs) != 3 {
		t.Errorf("depth 3 returned %d edges, want 3", len(shallow.EdgeIDs))
	}

	capped, err := svc.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 40, MaxNodes: 5})
	if err != nil {
		t.Fatalf("Walk capped: %v", err)
	}
	if len(capped.EdgeIDs) > 5 {
		t.Errorf("node cap returned %d edges, want at most 5", len(capped.EdgeIDs))
	}
	if !capped.Truncated {
		t.Error("a capped traversal did not report itself truncated; a caller cannot tell " +
			"a complete answer from a partial one")
	}
}

// Revocation hides an edge from traversal without deleting it. A rebuilt
// projection has to be able to see that a claim was once made.
func TestRevocationHidesWithoutDeleting(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	if _, err := svc.AddEdges(ctx, []graph.Edge{topologyEdge(tenant, site)}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	if err := svc.Revoke(ctx, tenant, "e-site-zone", "admin", "camera moved", ""); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	result, err := svc.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 3})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, id := range result.EdgeIDs {
		if id == "e-site-zone" {
			t.Error("a revoked edge is still visible to traversal")
		}
	}

	snap, err := svc.Snapshot(ctx, tenant)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Edges) != 0 {
		t.Errorf("a snapshot of the active graph contains %d revoked edge(s)", len(snap.Edges))
	}

	// The row is still there.
	var raw int
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT count(*) FROM edges WHERE tenant_id = $1 AND edge_id = 'e-site-zone'`,
		tenant).Scan(&raw); err != nil {
		t.Fatalf("count: %v", err)
	}
	if raw != 1 {
		t.Error("revocation deleted the row; a rebuild could no longer see that the claim was made")
	}
}

func TestSnapshotIsStable(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.AddEdges(ctx, []graph.Edge{topologyEdge(tenant, site)}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	a, err := svc.Snapshot(ctx, tenant)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	b, err := svc.Snapshot(ctx, tenant)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	ja, _ := a.MarshalJSON()
	jb, _ := b.MarshalJSON()
	// TakenAt differs, so compare the edge list, which is the part a rebuild
	// depends on.
	if len(a.Edges) != len(b.Edges) {
		t.Fatalf("two snapshots of the same graph differ: %d vs %d edges", len(a.Edges), len(b.Edges))
	}
	for i := range a.Edges {
		if a.Edges[i].EdgeID != b.Edges[i].EdgeID {
			t.Errorf("edge %d differs between snapshots: %q vs %q", i, a.Edges[i].EdgeID, b.Edges[i].EdgeID)
		}
	}
	_ = ja
	_ = jb
}

func TestSnapshotRefusesAnUnknownRelation(t *testing.T) {
	svc, tenant, site := service(t)
	_, err := svc.Walk(context.Background(), tenant, "SITE", site,
		graph.Traversal{FollowedRelations: []string{"knows"}})
	if err == nil {
		t.Error("a traversal following an unknown relation was allowed")
	}
}

func TestZonePathsFeedTheRuleCompiler(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	_ = site
	testutil.Exec(t, testutil.Pool(t), `
		INSERT INTO zones (tenant_id, zone_id, site_id, name, path, depth)
		VALUES ($1, 'zone-entrance', $2, 'Entrance', '/site/entrance', 1),
		       ($1, 'zone-loading',  $2, 'Loading',  '/site/entrance/loading', 2)`,
		tenant, site)

	paths, err := svc.ZonePaths(ctx, tenant)
	if err != nil {
		t.Fatalf("ZonePaths: %v", err)
	}
	if !paths["/site/entrance/loading"] {
		t.Error("the nested zone path is missing; a rule matching it would be rejected as unknown")
	}
	if !paths["/site/entrance"] {
		t.Error("the parent zone path is missing")
	}
}

func TestCheckRebuildReportsProblems(t *testing.T) {
	svc, tenant, _ := service(t)
	ctx := context.Background()

	// An edge citing a source event that was never stored.
	if _, err := svc.AddEdges(ctx, []graph.Edge{{
		TenantID: tenant, EdgeID: "e-orphan",
		FromType: "DEVICE", FromID: "cam-1", Relation: "observed_by",
		ToType: "EVENT", ToID: "evt-missing",
		Provenance: graph.Provenance{
			AssertedBy: "adapter", SourceEventID: "src-missing", Method: "observed", Confidence: 1,
		},
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	report, err := svc.CheckRebuild(ctx, tenant)
	if err != nil {
		t.Fatalf("CheckRebuild: %v", err)
	}
	if report.OrphanSourceEvents != 1 {
		t.Errorf("orphan source events: %d, want 1", report.OrphanSourceEvents)
	}
	if len(report.Problems) == 0 {
		t.Error("a rebuild check that found a problem reported none")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var _ = time.Now
