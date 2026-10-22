package main

// The seeder.
//
// # Why this test exists
//
// The seed is the program that creates the site, the users, the MFA factors,
// the devices and the active bundle for every demo and every evaluation. It
// had no test at all, which means the first thing anybody learns about a
// broken seed is that the demo does not start.
//
// # The three things worth proving
//
//   - it CREATES the topology: a tenant, a site, the zones and the devices;
//   - it is IDEMPOTENT on its principals: a second run finds the existing
//     administrator instead of creating a second one, and
//     `BootstrapAdmin` refuses a second administrator on purpose;
//   - it REFUSES a populated tenant rather than merging into one, because a
//     half-reset site has two of everything.
//
// # What it does not drive
//
// The ingest and evidence steps replay the committed fixtures through the real
// adapter and the real pipeline. That is the whole product running, and it
// belongs to the acceptance run rather than to a unit test: it needs the
// detector, the evidence store and a video surface, and a test that needs
// three of those to answer one question is a test that fails for the wrong
// reason most of the time.

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// seedOptions is the seeder's configuration, pointed at this package's
// committed inputs and at temporary directories for everything it writes.
func seedOptions(t *testing.T) options {
	t.Helper()
	root := moduleRoot(t)
	return options{
		tenant:       "seed-canary",
		site:         "seed-canary-site",
		timezone:     "UTC",
		integration:  "seed-canary-integration",
		fixturesDir:  filepath.Join(root, "testdata", "fixtures"),
		clipsDir:     filepath.Join(root, "poc", "active-monitoring", "testdata"),
		bundlePath:   filepath.Join(root, "rules", "builtin"),
		evidenceRoot: filepath.Join(t.TempDir(), "evidence"),
		secretKey:    filepath.Join(t.TempDir(), "secrets", "secret.key"),
		consoleTLS:   filepath.Join(t.TempDir(), "console-tls"),
		passphrase:   "not-a-secret-canary-passphrase",
		epoch:        "2026-03-01T09:00:00Z",
	}
}

// moduleRoot is the repository root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("the module root: %v", err)
	}
	return dir
}

// newTestSeeder is a seeder over the test's database.
func newTestSeeder(t *testing.T) *seeder {
	t.Helper()
	// The seeder reads the DSN from the environment, so the test points it at
	// the per-package database the harness made.
	t.Setenv("SP_DATABASE_URL", seedDSN(t))
	t.Setenv("SP_DATABASE_PASSWORD", "postgres")
	// The harness owns the database: asking for the pool CREATES it, and the
	// migrations are applied to it, so the seeder connects to a database that
	// has the schema it is going to write into.
	pool := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: pool}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	s, err := newSeeder(context.Background(), seedOptions(t))
	if err != nil {
		t.Fatalf("build the seeder: %v", err)
	}
	t.Cleanup(s.close)
	return s
}

// seedDSN is the DSN of THIS package's test database.
//
// The harness's `Database` returns the NAME, because the name is what a test
// asserts on; the seeder wants a DSN, so the name goes back into the base
// URL the harness was given.
func seedDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv(testutil.DatabaseURLEnv)
	if base == "" {
		t.Fatalf("%s is not set, so there is no database to seed", testutil.DatabaseURLEnv)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", testutil.DatabaseURLEnv, err)
	}
	u.Path = "/" + testutil.Database(t)
	return u.String()
}

// TestTheSeedCreatesTheTopology is the first step.
//
// A tenant, a site, the zones and the devices. The demo's judge walks a site
// with zones in it, and a seed that created a tenant and no zones would produce
// an empty console and a passing exit code.
func TestTheSeedCreatesTheTopology(t *testing.T) {
	s := newTestSeeder(t)
	ctx := context.Background()

	if err := s.writeTopology(ctx); err != nil {
		t.Fatalf("write the topology: %v", err)
	}

	var sites, zones, devices int
	pool := testutil.Pool(t)
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sites WHERE tenant_id = $1`, s.opts.tenant).Scan(&sites); err != nil {
		t.Fatalf("count the sites: %v", err)
	}
	if sites != 1 {
		t.Errorf("the seed created %d site(s), want 1", sites)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM zones WHERE tenant_id = $1`, s.opts.tenant).Scan(&zones); err != nil {
		t.Fatalf("count the zones: %v", err)
	}
	if zones == 0 {
		t.Error("the seed created no zones. The demo's judge walks a site with zones in it")
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM devices WHERE tenant_id = $1`, s.opts.tenant).Scan(&devices); err != nil {
		t.Fatalf("count the devices: %v", err)
	}
	if devices == 0 {
		t.Error("the seed created no devices, so nothing can ever be detected")
	}
}

// TestTheSeedIsIdempotentOnItsPrincipals is the second step, run twice.
//
// `BootstrapAdmin` refuses a second administrator on purpose, and the seed
// reads the existing one instead. That is what makes the seed idempotent
// rather than a one-shot, and it is the property the demo depends on: a judge
// who re-runs the seed gets the same site, not a second administrator.
func TestTheSeedIsIdempotentOnItsPrincipals(t *testing.T) {
	s := newTestSeeder(t)
	ctx := context.Background()

	if err := s.writeTopology(ctx); err != nil {
		t.Fatalf("write the topology: %v", err)
	}
	if err := s.writeIdentity(ctx, ""); err != nil {
		t.Fatalf("write the identity: %v", err)
	}
	first := len(s.report.Principals)
	if first == 0 {
		t.Fatal("the seed reported no principals")
	}

	// The second run finds the existing administrator and does not create a
	// second one. The REPORT is allowed to grow -- it is an account of what
	// the run did -- but the DATABASE is not.
	if err := s.writeIdentity(ctx, ""); err != nil {
		t.Fatalf("write the identity again: %v", err)
	}

	pool := testutil.Pool(t)
	var admins int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND username = $2`,
		s.opts.tenant, demoTenantAdmin).Scan(&admins); err != nil {
		t.Fatalf("count the administrators: %v", err)
	}
	if admins != 1 {
		t.Errorf("the tenant holds %d administrator(s) named %q, want 1. The seed is "+
			"idempotent: a second run finds the existing administrator",
			admins, demoTenantAdmin)
	}
}

// TestTheSeedRefusesAPopulatedTenant is the freshness check.
//
// A seed into a populated tenant is REFUSED rather than merged, because a
// half-reset site has two of everything. The refusal is the whole control: a
// merge is how a demo ends up with two bundles and one of them is active.
func TestTheSeedRefusesAPopulatedTenant(t *testing.T) {
	s := newTestSeeder(t)
	ctx := context.Background()

	already, err := s.assertFresh(ctx)
	if err != nil {
		t.Fatalf("assert fresh: %v", err)
	}
	if already {
		t.Fatal("a tenant that has never been seeded reports as already seeded")
	}

	if err := s.writeTopology(ctx); err != nil {
		t.Fatalf("write the topology: %v", err)
	}
	// A tenant with a topology and no ACTIVITY is still fresh: there is
	// nothing to merge with, and the seed is allowed to add to it.
	already, err = s.assertFresh(ctx)
	if err != nil {
		t.Fatalf("assert fresh: %v", err)
	}
	if already {
		t.Error("a tenant with a topology and no incidents reports as already seeded")
	}

	// One incident, and the tenant is POPULATED: a seed into it is refused
	// rather than merged, because a merge produces a site with two of
	// everything.
	if _, err := testutil.Pool(t).Exec(ctx, `
		INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
			 bundle_content_hash, policy_version, state, severity, title,
			 first_event_at, last_event_at, created_at)
		VALUES ('inc-canary', $1, $2, 'rule-canary', '0.1.0', 'hash', 'p1',
		        'open', 'low', 'canary', $3, $3, $3)`,
		s.opts.tenant, s.opts.site, s.opts.epoch); err != nil {
		t.Fatalf("insert the incident: %v", err)
	}
	already, err = s.assertFresh(ctx)
	if err != nil {
		t.Fatalf("assert fresh: %v", err)
	}
	if !already {
		t.Error("a tenant with an incident reports as fresh. A seed into a populated tenant " +
			"must be refused rather than merged")
	}
}
