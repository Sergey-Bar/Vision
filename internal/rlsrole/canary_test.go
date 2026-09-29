// Package rlsrole runs a slice of the product's own flows as the real
// production database role.
//
// Every other suite connects as a superuser, which bypasses row-level security.
// A superuser suite cannot see the class of bug where a query against a
// tenant-scoped table is issued without a tenant context: Postgres returns an
// empty result rather than an error, the code reads that as "no roles" or "no
// evidence", and the product misbehaves on a site that is configured correctly.
//
// This package connects as security_core - the role the product itself uses, and
// the one RLS applies to - and drives the flows that matter. It is deliberately
// short: it is a canary, not a second copy of the suite.
package rlsrole

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/graph"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/normalize"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// productionRolePassword is the password the migration-created role is given for
// the test. It exists only in the test database and only for this suite.
const productionRolePassword = "rlsrole-test-password"

var clockAt = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// productionPool is a pool that connects as security_core, with RLS in force.
func productionPool(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The migration created the role NOLOGIN and without a password. This gives
	// it one, in the test database only.
	if _, err := raw.Exec(ctx,
		`ALTER ROLE security_core LOGIN PASSWORD '`+productionRolePassword+`'`); err != nil {
		t.Fatalf("give security_core a login: %v", err)
	}

	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	// The same database, reached as the role the product uses. Applied after the
	// DSN, because the DSN names the superuser.
	cfg.User = "security_core"
	cfg.ApplicationName = "rlsrole-canary"

	pool, err := dbx.Open(ctx, cfg, productionRolePassword)
	if err != nil {
		// Fail, never skip. A canary that skips when it cannot reach the database
		// is a canary that silently stops protecting, and the one time this bug
		// class matters is on a site whose permissions were misconfigured.
		t.Fatalf("cannot connect as security_core: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRolesAndScopeAreReadableUnderRLS is the canary.
//
// Roles and site assignments are read on every session resolve. If the read is
// issued without a tenant context, RLS returns nothing, the principal is built
// with no roles and no sites, and every capability and site check refuses - the
// console is unreachable while the login keeps returning a session. It fails
// closed, which is the right direction and still an outage.
func TestRolesAndScopeAreReadableUnderRLS(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)
	ident := &identity.Service{
		Pool:  pool,
		Audit: &audit.Writer{Pool: pool},
		Now:   func() time.Time { return clockAt },
	}

	userID, err := ident.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("bootstrap as security_core: %v", err)
	}
	if userID == "" {
		t.Fatal("no administrator was created")
	}

	res, err := ident.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("password step: %v", err)
	}
	if res.Session == nil {
		t.Fatalf("the password step returned no session: %+v", res)
	}

	principal := res.Session.Principal
	if len(principal.Roles) == 0 {
		t.Fatal("the principal has no roles. The role and site read is being issued without a " +
			"tenant context, so RLS returns nothing and every capability check refuses")
	}
	found := false
	for _, r := range principal.Roles {
		if r == identity.RoleTenantAdmin {
			found = true
		}
	}
	if !found {
		t.Errorf("the principal's roles are %v, want tenant_admin", principal.Roles)
	}
	if !principal.Can(identity.CapIncidentRead) {
		t.Error("a tenant administrator cannot read incidents")
	}
	if !principal.CanSeeSite(site) {
		t.Errorf("the administrator is not assigned to site %q; the site assignment was not read", site)
	}
}

// TestTenantScopedReadsSeeOnlyTheirOwnTenant is the second canary: the scope has
// to work in both directions. Empty would be an outage; too wide would be a
// disclosure.
func TestTenantScopedReadsSeeOnlyTheirOwnTenant(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenantA, siteA, _ := testutil.NewTenant(t, raw)
	tenantB, siteB, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)
	writer := &inbox.Writer{Pool: pool}

	opts := inbox.WriteOptions{
		TenantID: tenantA, SiteID: siteA, IntegrationID: "int-1",
		SourceEventID: "src-a", Payload: []byte(`{"Id":"src-a"}`), ReceivedAt: clockAt,
	}
	accepted, err := writer.Write(ctx, opts)
	if err != nil {
		t.Fatalf("write as security_core: %v", err)
	}
	if accepted.Duplicate {
		t.Fatal("the first write was reported as a duplicate")
	}

	counts, err := writer.Counts(ctx, tenantA, siteA)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Received != 1 {
		t.Errorf("tenant A sees %d received event(s), want 1; the backlog read is not "+
			"seeing its own tenant's rows", counts.Received)
	}
	// Tenant B's rows are invisible two ways over now: a different tenant id, and a
	// different site. The site check is the new one, so both are exercised.
	other, err := writer.Counts(ctx, tenantB, siteB)
	if err != nil {
		t.Fatalf("counts for the other tenant: %v", err)
	}
	if other.Received != 0 {
		t.Errorf("tenant B sees %d of tenant A's events; the tenant context is not scoping "+
			"the read", other.Received)
	}
}

// TestGraphEdgeWritesCarryTheTenantContext is the fourth canary.
//
// AddEdge and AddEdges once issued their INSERT against the bare pool, so no
// app.tenant_id was set. The suite is a superuser suite and could not see it;
// under the role the product runs as, RLS's WITH CHECK refuses the write
// outright. The graph is what the console renders and what an incident cites, so
// this is a total loss of the feature rather than a degraded one.
func TestGraphEdgeWritesCarryTheTenantContext(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenantA, siteA, _ := testutil.NewTenant(t, raw)
	tenantB, _, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)
	svc := &graph.Service{Pool: pool}

	single := graph.Edge{
		TenantID: tenantA, EdgeID: "edge-single-" + tenantA,
		FromType: "SITE", FromID: siteA, Relation: "contains",
		ToType: "ZONE", ToID: "zone-a",
		Provenance: graph.Provenance{
			AssertedBy: "adapter:milestone", Method: "asserted", Confidence: 1.0,
		},
	}
	added, err := svc.AddEdge(ctx, single)
	if err != nil {
		t.Fatalf("AddEdge as security_core: %v", err)
	}
	if !added {
		t.Error("the first edge write was reported as a duplicate")
	}

	// The batch path is a transaction and had the same defect.
	batch := []graph.Edge{
		{
			TenantID: tenantA, EdgeID: "edge-batch-a-" + tenantA,
			FromType: "ZONE", FromID: "zone-a", Relation: "contains",
			ToType: "DEVICE", ToID: "dev-a",
			Provenance: graph.Provenance{
				AssertedBy: "adapter:milestone", Method: "observed", Confidence: 1.0,
			},
		},
		{
			TenantID: tenantB, EdgeID: "edge-batch-b-" + tenantB,
			FromType: "ZONE", FromID: "zone-b", Relation: "contains",
			ToType: "DEVICE", ToID: "dev-b",
			Provenance: graph.Provenance{
				AssertedBy: "adapter:milestone", Method: "observed", Confidence: 1.0,
			},
		},
	}
	n, err := svc.AddEdges(ctx, batch)
	if err != nil {
		t.Fatalf("AddEdges as security_core: %v", err)
	}
	if n != 2 {
		t.Errorf("AddEdges reported %d new edge(s), want 2", n)
	}

	// And the write is invisible to a tenant that did not make it.
	tenantASnap, err := svc.Snapshot(ctx, tenantA)
	if err != nil {
		t.Fatalf("snapshot tenant A: %v", err)
	}
	if tenantASnap.Count != 2 {
		t.Errorf("tenant A sees %d of its own edges, want 2", tenantASnap.Count)
	}
	tenantBSnap, err := svc.Snapshot(ctx, tenantB)
	if err != nil {
		t.Fatalf("snapshot tenant B: %v", err)
	}
	if tenantBSnap.Count != 1 {
		t.Errorf("tenant B sees %d edges, want only its own 1", tenantBSnap.Count)
	}
	for _, e := range tenantBSnap.Edges {
		if e.TenantID != tenantB {
			t.Errorf("tenant B's snapshot contains an edge owned by %q", e.TenantID)
		}
	}

	// A bare count, with no tenant predicate in the SQL at all. Snapshot and Walk
	// both filter by tenant in the query, so they cannot detect a view that hands
	// back every row; only an unfiltered read can. edges_active is
	// security_invoker for exactly this reason, and this is the assertion that
	// keeps it that way.
	var leaked int
	if err := pool.For("some-other-tenant", "canary").QueryRow(ctx,
		`SELECT count(*) FROM edges_active`).Scan(&leaked); err != nil {
		t.Fatalf("unfiltered read of edges_active: %v", err)
	}
	if leaked != 0 {
		t.Errorf("a tenant owning no edges read %d row(s) from edges_active. The view is "+
			"not security_invoker, so RLS is evaluated for the view's owner and the "+
			"tenant policies on `edges` never apply to a traversal", leaked)
	}
}

// TestGraphTraversalReadsCarryTheTenantContext is the fifth canary.
//
// Walk issued its recursive traversal against the bare pool, so no app.tenant_id
// was set on that connection. Under the role the product runs as, edges_active
// is security_invoker (migration 0014) and edges has FORCE ROW LEVEL SECURITY,
// so the policy tenant_id = app_current_tenant() OR app_is_system_worker()
// evaluated NULL and every row was filtered. No error, no warning, no log line:
// the traversal returned an empty graph for a tenant that had written edges
// seconds earlier. Snapshot and CheckRebuild were already scoped, which is why
// the console rendered a topology with no path through it and nothing anywhere
// said why.
func TestGraphTraversalReadsCarryTheTenantContext(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)
	svc := &graph.Service{Pool: pool}

	added, err := svc.AddEdges(ctx, []graph.Edge{
		{
			TenantID: tenant, EdgeID: "walk-site-" + tenant,
			FromType: "SITE", FromID: site, Relation: "contains", ToType: "ZONE", ToID: "zone-walk",
			Provenance: graph.Provenance{
				AssertedBy: "adapter:milestone", Method: "asserted", Confidence: 1.0,
			},
		},
		{
			TenantID: tenant, EdgeID: "walk-zone-" + tenant,
			FromType: "ZONE", FromID: "zone-walk", Relation: "contains", ToType: "DEVICE", ToID: "dev-walk",
			Provenance: graph.Provenance{
				AssertedBy: "adapter:milestone", Method: "observed", Confidence: 1.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("AddEdges as security_core: %v", err)
	}
	if added != 2 {
		t.Fatalf("added %d edges, want 2", added)
	}

	res, err := svc.Walk(ctx, tenant, "SITE", site, graph.Traversal{MaxDepth: 3})
	if err != nil {
		t.Fatalf("Walk as security_core: %v", err)
	}
	if len(res.EdgeIDs) != 2 {
		t.Errorf("the traversal found %d edges, want 2. The write landed (AddEdges "+
			"reported it) and the read cannot see it, which is RLS emptying the "+
			"query rather than the graph being empty: %+v", len(res.EdgeIDs), res.EdgeIDs)
	}
	if res.VisitedAtDepth != 2 {
		t.Errorf("the traversal reached depth %d, want 2", res.VisitedAtDepth)
	}
}

// TestNormalizerWritesUnderRLS is the third canary: the write path, which is where
// an insert against a tenant-scoped table with no context would fail outright.
func TestNormalizerWritesUnderRLS(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)
	writer := &inbox.Writer{Pool: pool}
	normalizer := normalize.New(pool)

	if _, err := writer.Write(ctx, inbox.WriteOptions{
		TenantID: tenant, SiteID: site, IntegrationID: "int-1",
		SourceEventID: "src-rls", Payload: []byte(`{"Id":"src-rls"}`), ReceivedAt: clockAt,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	leases, err := writer.ClaimFor(ctx, tenant, "worker", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim under RLS: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("claimed %d events, want 1", len(leases))
	}
	res, err := normalizer.Normalize(ctx, normalize.Input{
		Key:         leases[0].Key,
		SiteID:      site,
		EventType:   "motion",
		ObservedAt:  clockAt,
		ReceivedAt:  clockAt,
		ClockSource: "vms",
		Payload:     []byte(`{"Id":"src-rls"}`),
		LeaseOwner:  "worker",
	})
	if err != nil {
		t.Fatalf("normalize under RLS: %v", err)
	}
	if res.EventID == "" {
		t.Fatal("no canonical event was produced")
	}
}
