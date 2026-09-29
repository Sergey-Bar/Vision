package bundles_test

import (
	"context"
	"errors"
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
	"github.com/Sergey-Bar/Ai-Captain/internal/simulate"
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

// The gate. A bundle cannot be activated without a reviewed simulation, because
// a bundle that can be activated in one step is a bundle nobody read.
//
// The malformed-digest cases are here because they are still refusals the gate
// owes an operator a clear reason for. The substantive case - a well-formed
// digest that is not the digest of the changes - is
// TestActivationRecomputesTheDigestItIsGiven below.
func TestActivationRefusesAMalformedDigestWithAReason(t *testing.T) {
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
			rep := reviewed(meta.Version)
			rep.DiffDigest = digest
			err := svc.Activate(ctx, tenant, meta.Version, rep, "admin")
			if err == nil {
				t.Fatal("activation succeeded with a malformed digest")
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
}

// reviewed builds a report an operator could have produced by reading
// `securityctl rules simulate --json` and then editing it. Used where the test
// is about activation rather than about the simulation.
func reviewed(version string, changes ...simulate.Change) simulate.Report {
	rep := simulate.Report{BundleVersion: version, FixtureSet: "fixtures-1"}
	rep.Changes = changes
	rep.DiffDigest = simulate.DigestFor(rep.BundleVersion, rep.FixtureSet, rep.Changes)
	return rep
}

// Activation re-hashes the report it is given.
//
// It used to take a digest string and check only that 64 lowercase hex
// characters had been supplied. `strings.Repeat("a", 64)` activated a bundle —
// and the whole premise of `internal/simulate`, "a bundle whose simulation
// produced a different digest than the one acknowledged cannot be activated", was
// a description of a check that did not exist.
//
// The check is only as strong as the interface allows: the service does not hold
// the fixture set and cannot re-run the simulation. What it can do is recompute
// the digest from the acknowledged change list with the function that produced
// it, and refuse when the two disagree.
func TestActivationRecomputesTheDigestItIsGiven(t *testing.T) {
	svc, tenant := service(t)
	ctx := context.Background()
	meta, _, err := svc.Store(ctx, tenant, bundleFile(t), "test", "admin", "")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	change := simulate.Change{
		Kind: simulate.ChangeCreated, ScenarioID: "s-1", SignalID: "sig-1",
		Before: simulate.Outcome{IncidentRaised: false, Severity: "low"},
		After: simulate.Outcome{
			ScenarioID: "s-1", SignalID: "sig-1",
			IncidentRaised: true, Severity: "high", State: "unverified",
		},
	}

	// A digest typed by hand, with a change list that does not produce it.
	handWritten := reviewed(meta.Version, change)
	handWritten.DiffDigest = strings.Repeat("a", 64)
	err = svc.Activate(ctx, tenant, meta.Version, handWritten, "admin")
	if err == nil {
		t.Fatal("a hand-written digest activated the bundle. The gate checked only that 64 hex " +
			"characters had been supplied")
	}
	var gate *bundles.ErrGateNotSatisfied
	if !asGate(err, &gate) {
		t.Fatalf("error is %v, want a named gate refusal", err)
	}
	if !strings.Contains(gate.Reason, "not the digest of the acknowledged changes") {
		t.Errorf("the refusal does not say the change list and the digest disagree: %q", gate.Reason)
	}

	// A report for a different bundle says nothing about this one.
	other := reviewed("9.9.9", change)
	if err := svc.Activate(ctx, tenant, meta.Version, other, "admin"); err == nil {
		t.Error("a simulation of a different bundle activated this one")
	}

	// A report with no digest at all.
	none := reviewed(meta.Version, change)
	none.DiffDigest = ""
	if err := svc.Activate(ctx, tenant, meta.Version, none, "admin"); err == nil {
		t.Error("a report with no digest activated the bundle")
	}

	// And the honest one works.
	if err := svc.Activate(ctx, tenant, meta.Version, reviewed(meta.Version, change), "admin"); err != nil {
		t.Fatalf("a genuine simulation report was refused: %v", err)
	}
}

// A lint status that is a constant is a gate that cannot fire.
//
// The loop that set it assigned "passed" in both branches, so `lint_status` was
// always "passed" and Activate's `lintStatus != "passed"` check never ran. The
// derivation is now a named function, which is what makes it testable: the
// fixture bundle lints clean, so storing it cannot tell the two implementations
// apart, but handing the function an error-level problem can.
func TestLintStatusIsDerivedFromTheProblemsAndNotAConstant(t *testing.T) {
	cases := map[string]struct {
		problems []rules.Problem
		want     string
	}{
		"no problems": {nil, "passed"},
		"a warning does not fail a bundle": {
			[]rules.Problem{{Severity: rules.SeverityWarning, Message: "degrade_proceeds"}},
			"passed",
		},
		"an error fails it": {
			[]rules.Problem{{Severity: rules.SeverityError, Message: "unsupported operator"}},
			"failed",
		},
		"an error after a warning still fails it": {
			[]rules.Problem{
				{Severity: rules.SeverityWarning, Message: "degrade_proceeds"},
				{Severity: rules.SeverityError, Message: "unsupported operator"},
			},
			"failed",
		},
		"a warning after an error does not rescue it": {
			[]rules.Problem{
				{Severity: rules.SeverityError, Message: "unsupported operator"},
				{Severity: rules.SeverityWarning, Message: "degrade_proceeds"},
			},
			"failed",
		},
	}
	for name, tc := range cases {
		if got := bundles.LintStatusFor(tc.problems); got != tc.want {
			t.Errorf("%s: lint status is %q, want %q. A status that is the same for every "+
				"input is a constant, and Activate's `lintStatus != \"passed\"` gate against "+
				"one can never fire", name, got, tc.want)
		}
	}
}

func asGate(err error, target **bundles.ErrGateNotSatisfied) bool {
	// errors.As, not a type assertion: the gate's error is wrapped on its way
	// out of the store, and a type assertion would miss it.
	return errors.As(err, target)
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

	firstReport, secondReport := reviewed(first.Version), reviewed(second.Version)
	if err := svc.Activate(ctx, tenant, first.Version, firstReport, "admin"); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := svc.Activate(ctx, tenant, second.Version, secondReport, "admin"); err != nil {
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
	report := reviewed(meta.Version)
	if err := svc.Activate(ctx, tenant, meta.Version, report, "admin"); err != nil {
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
	if stored.ActivateDiffDigest != report.DiffDigest {
		t.Errorf("the acknowledged diff digest was not recorded: got %q, want %q",
			stored.ActivateDiffDigest, report.DiffDigest)
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
