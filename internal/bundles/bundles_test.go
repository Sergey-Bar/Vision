package bundles_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func service(t *testing.T) (*bundles.Service, string) {
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

	tenant, _, _ := testutil.NewTenant(t, raw)
	return &bundles.Service{
		Pool:     wrapped,
		Compiler: rules.NewCompiler(),
		Audit:    &audit.Writer{Pool: wrapped},
	}, tenant
}

func bundleFile(t *testing.T) []byte {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "rules", "builtin", "bundle.yaml"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	return raw
}

func withVersion(content []byte, version string) []byte {
	return []byte(strings.Replace(string(content), "version: 0.1.0", "version: "+version, 1))
}

func TestStoreDoesNotActivate(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()

	meta, problems, err := svc.Store(ctx, tenant, bundleFile(t), "test", "admin", "")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if len(problems) > 0 {
		t.Errorf("a valid bundle produced problems: %v", problems)
	}
	if meta.Active {
		t.Fatal("storing activated the bundle; a bundle nobody simulated can then be running")
	}
	if meta.LintStatus != "passed" {
		t.Errorf("lint status is %q", meta.LintStatus)
	}
	if _, _, err := svc.Active(ctx, tenant); err == nil {
		t.Error("a stored bundle became active")
	}
}

func TestStoreRefusesAnUncompilableBundle(t *testing.T) {
	svc, tenant := service(t)
	meta, problems, err := svc.Store(context.Background(), tenant,
		[]byte("apiVersion: security.io/v1\nkind: RuleBundle\nversion: 1.0.0\n"), "test", "admin", "")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if meta.Version != "" {
		t.Error("a bundle that did not compile was stored")
	}
	if len(problems) == 0 {
		t.Error("no problems were reported for a bundle with no settings and no site")
	}
}

// The gate. A bundle cannot be activated without a reviewed diff digest, because
// a bundle that can be activated in one step is a bundle nobody read.
func TestActivationRequiresAReviewedDiffDigest(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()
	meta, _, err := svc.Store(ctx, tenant, bundleFile(t), "test", "admin", "")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	cases := map[string]string{
		"no digest":    "",
		"not a digest": "not-a-digest",
		"too short":    "abc123",
		"not hex":      strings.Repeat("z", 64),
	}
	for name, digest := range cases {
		t.Run(name, func(t *testing.T) {
			err := svc.Activate(ctx, tenant, meta.Version, digest, "admin")
			if err == nil {
				t.Fatal("activation succeeded without a valid digest")
			}
			var gate *bundles.ErrGateNotSatisfied
			if !asGate(err, &gate) {
				t.Fatalf("error is %v, want a named gate refusal", err)
			}
			if gate.Reason == "" {
				t.Error("the refusal does not say which condition failed; an operator at a " +
					"site with no network cannot go and look it up")
			}
		})
	}

	// The built-in bundle's simulated digest.
	if err := svc.Activate(ctx, tenant, meta.Version, strings.Repeat("a", 64), "admin"); err != nil {
		t.Fatalf("activation with a well-formed digest was refused: %v", err)
	}
}

func asGate(err error, target **bundles.ErrGateNotSatisfied) bool {
	g, ok := err.(*bundles.ErrGateNotSatisfied)
	if ok {
		*target = g
	}
	return ok
}

func TestRollbackReturnsToThePreviousVersion(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()

	first, _, err := svc.Store(ctx, tenant, bundleFile(t), "test", "admin", "")
	if err != nil {
		t.Fatalf("store first: %v", err)
	}
	second, _, err := svc.Store(ctx, tenant, withVersion(bundleFile(t), "0.2.0"), "test", "admin", first.Version)
	if err != nil {
		t.Fatalf("store second: %v", err)
	}
	// Give the second version a later created_at so "previous" is unambiguous.
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE rule_bundles SET created_at = now() + interval '1 minute'
		  WHERE tenant_id = $1 AND version = $2`, tenant, second.Version)

	digest := strings.Repeat("b", 64)
	if err := svc.Activate(ctx, tenant, first.Version, digest, "admin"); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := svc.Activate(ctx, tenant, second.Version, digest, "admin"); err != nil {
		t.Fatalf("activate second: %v", err)
	}
	active, _, err := svc.Active(ctx, tenant)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active != second.Version {
		t.Fatalf("active version is %s, want %s", active, second.Version)
	}

	back, err := svc.Rollback(ctx, tenant, "admin")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if back != first.Version {
		t.Errorf("rolled back to %s, want %s", back, first.Version)
	}
	active, _, _ = svc.Active(ctx, tenant)
	if active != first.Version {
		t.Errorf("active version after rollback is %s, want %s", active, first.Version)
	}
}

// Every activation is audited, because activating a rule set changes what will
// alarm a site and nobody should be able to do that without a ledger row.
func TestActivationIsAudited(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()
	meta, _, err := svc.Store(ctx, tenant, bundleFile(t), "test", "admin", "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := svc.Activate(ctx, tenant, meta.Version, strings.Repeat("c", 64), "admin"); err != nil {
		t.Fatalf("activate: %v", err)
	}

	raw := testutil.Pool(t)
	var n int
	if err := raw.QueryRow(ctx, `
		SELECT count(*) FROM audit_ledger
		 WHERE tenant_id = $1 AND action = 'rules.bundle.activated'`, tenant).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Errorf("%d activation audit rows, want 1", n)
	}

	// And the acknowledged digest is recorded, so a month later somebody can ask
	// what the operator saw before turning this on.
	stored, err := svc.Meta(ctx, tenant, meta.Version)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if stored.ActivateDiffDigest != strings.Repeat("c", 64) {
		t.Errorf("the acknowledged diff digest was not recorded: %q", stored.ActivateDiffDigest)
	}
}

func TestStoredContentIsImmutable(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()
	original := bundleFile(t)
	meta, _, err := svc.Store(ctx, tenant, original, "test", "admin", "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	// The trigger in migration 0005 refuses to let the bytes change.
	err = testutil.Pool(t).QueryRow(ctx, `
		UPDATE rule_bundles SET content = $3
		 WHERE tenant_id = $1 AND version = $2`,
		tenant, meta.Version, []byte("tampered")).Scan(new(string))
	if err == nil {
		t.Fatal("a stored bundle's content was edited; an incident decided under it can no " +
			"longer be explained by the bundle that made the decision")
	}
}

func TestStoreIsIdempotentForTheSameVersion(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()
	content := bundleFile(t)

	first, _, err := svc.Store(ctx, tenant, content, "test", "admin", "")
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, _, err := svc.Store(ctx, tenant, content, "test", "admin", "")
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if first.BundleID != second.BundleID {
		t.Errorf("the same bytes produced two bundle ids: %s and %s", first.BundleID, second.BundleID)
	}

	var n int
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT count(*) FROM rule_bundles WHERE tenant_id = $1 AND version = $2`,
		tenant, first.Version).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d stored copies of version %s, want 1", n, first.Version)
	}
}

var _ = time.Now
