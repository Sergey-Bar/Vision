package testutil_test

// The harness's own isolation.
//
// The property is not "the tests pass". It is that two packages can run at the
// same time without dropping each other's tables, and a property like that is
// either true or it is not — so it is asserted directly rather than inferred from
// a green run that happened to be serialised by `-p 1`.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

const urlEnv = testutil.DatabaseURLEnv

func TestEachTestBinaryOwnsADatabaseNamedAfterItsPackage(t *testing.T) {
	got := testutil.Database(t)
	const want = "sp_test_internal_testutil"
	if got != want {
		t.Fatalf("the database is %q, want %q. A name derived from the caller's own file is the "+
			"only thing that stops two packages being handed the same database by accident", got, want)
	}
}

// TestTheDatabaseNameFitsPostgresIdentifierLimit.
//
// The harness refuses a name over 63 bytes rather than truncating it, because
// Postgres truncates and two long package paths would then share one database
// silently — and one package's Reset would drop the other's tables.
func TestTheDatabaseNameFitsPostgresIdentifierLimit(t *testing.T) {
	got := testutil.Database(t)
	if len(got) > 63 {
		t.Fatalf("the harness produced a %d-byte database name, which Postgres will truncate", len(got))
	}
	if !strings.HasPrefix(got, "sp_test_") {
		t.Errorf("the database name %q does not carry the harness prefix, so a stale one is "+
			"indistinguishable from somebody's data", got)
	}
}

// TestThePoolIsOnThisPackagesOwnDatabase is the assertion that carries the whole
// change. If it were still on the configured database, every package would share
// one `public` and `-p 1` would still be load-bearing.
func TestThePoolIsOnThisPackagesOwnDatabase(t *testing.T) {
	pool := testutil.Pool(t)
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("read current_database: %v", err)
	}
	if want := testutil.Database(t); name != want {
		t.Fatalf("the pool is on %q and this package owns %q. Two packages on one database is "+
			"the race -p 1 exists to avoid", name, want)
	}
}

// TestTheEnvironmentVariableNowNamesThisPackagesDatabase is the guarantee that
// makes the isolation total rather than partial.
//
// About a dozen packages do not use `testutil.Pool`: they open their own
// `dbx.Open` from `SP_TEST_DATABASE_URL`, because they are testing the connection
// path or need a different role. Those tests wrote to the configured database
// while `testutil.Pool` handed them a per-package one, and the symptom was a
// tenant that `NewTenant` had just created and `bootstrap-admin` could not see —
// forty-one copies of the same message, all of them the harness's fault.
func TestTheEnvironmentVariableNowNamesThisPackagesDatabase(t *testing.T) {
	testutil.Pool(t)
	configured := os.Getenv(urlEnv)
	if !strings.Contains(configured, "/"+testutil.Database(t)) {
		t.Errorf("%s is %q and does not name this package's database %q. A test that opens its "+
			"own connection will land somewhere this package cannot see",
			urlEnv, configured, testutil.Database(t))
	}
}

// TestMigration0008StillFindsItsTables is the red-check for the per-database
// decision, and it is the reason the decision was made this way.
//
// Migration 0008 hardcodes `n.nspname = 'public'` twice. A per-*schema* harness
// leaves that schema empty, so 0008's RLS loop applies no policy to anything — and
// the database's only tenant isolation would be the policies it did not create.
// It looked like it worked because the shared `public` held the previous run's
// tables.
//
// So this asserts the thing 0008 depends on, against the real migration: run it,
// and then check that a policy exists.
func TestMigration0008StillFindsItsTables(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()

	if err := migrateUp(ctx, t); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var policies int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_policies WHERE schemaname = 'public' AND policyname = 'tenant_isolation'`,
	).Scan(&policies)
	if err != nil {
		t.Fatalf("count tenant policies: %v", err)
	}
	if policies == 0 {
		t.Fatal("no tenant_isolation policy exists after a full migration. Migration 0008 applies " +
			"its policies by scanning pg_namespace for 'public'; a harness that gave it an empty " +
			"schema would apply none, and the database would have no tenant isolation at all")
	}
	t.Logf("%d tenant_isolation policies, which is one per tenant-scoped table", policies)
}

// TestTheHarnessResolvesTheCallersPackageThroughItsOwnHelpers is the red-check
// for the stack-walk in `databaseName`, and it exists because the first version
// got this wrong in a way no other test could see.
//
// `runtime.Caller(2)` is correct only while every path into the harness arrives
// at the same stack depth. `Reset` calls `Pool`, so a `Reset` from
// `internal/migrate` landed two frames short of the test and resolved to
// `internal/testutil` — and dropped *this* package's database instead of the
// caller's. Every test that took a pool first passed, because the pool was
// already memoised under the right name; the symptom was `internal/migrate`
// reporting `Reset` does nothing, eight packages recreating this database on every
// run, and eight unrelated packages' schemas silently untouched.
//
// The shape here is the shape that broke: a test reaching the harness through a
// helper rather than directly.
func TestTheHarnessResolvesTheCallersPackageThroughItsOwnHelpers(t *testing.T) {
	// viaHelper is one frame deeper than a direct call, exactly like Reset's call
	// to Pool.
	got := viaHelper(t)
	if want := testutil.Database(t); got != want {
		t.Fatalf("reaching the harness through a helper resolved the package as %q, want %q. "+
			"The name comes from the outermost frame outside testutil.go, so a helper in between "+
			"cannot change it", got, want)
	}
}

func viaHelper(t *testing.T) string {
	t.Helper()
	return testutil.Database(t)
}

// TestResetLeavesTheCallersPoolUsable is the shape every caller in the tree uses:
// take a pool, then reset.
//
// The first version of the per-package harness dropped the whole database in
// Reset, which closed the pool out from under every one of those callers and
// turned each subsequent statement into `acquire connection: closed pool` — 41
// failures in `internal/audit` alone. Reset empties the database; it does not
// re-create it.
func TestResetLeavesTheCallersPoolUsable(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()

	if err := migrateUp(ctx, t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE reset_probe (id int)`); err != nil {
		t.Fatalf("create the probe: %v", err)
	}

	testutil.Reset(t)

	// The pool the caller already holds still works.
	var one int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("the caller's pool stopped working after Reset: %v", err)
	}
	// And the database is empty again.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables `+
			`WHERE table_schema = 'public' AND table_name = 'reset_probe')`).Scan(&exists); err != nil {
		t.Fatalf("look for the probe: %v", err)
	}
	if exists {
		t.Error("Reset left a table behind, so a test expecting a clean schema reads its own rows")
	}
}

// migrateUp brings this package's own database to the current version. Local to
// this file so the red-check above reads as what it is: one migration, then one
// query about what that migration did.
func migrateUp(ctx context.Context, t *testing.T) error {
	t.Helper()
	if _, err := (&migrate.Runner{Pool: testutil.Pool(t)}).Up(ctx); err != nil {
		return err
	}
	return nil
}

// TestPoolSkipsRatherThanFailsWithoutADatabase is the contributor's path, and it
// is what `internal/skipaudit` exists to account for. This test cannot assert the
// skip from inside a run that has a database; it asserts that the variable name is
// the one the gate and the allow-list both name.
func TestPoolSkipsRatherThanFailsWithoutADatabase(t *testing.T) {
	if os.Getenv(urlEnv) == "" {
		t.Skip("no database configured; this test is about the path where there is none")
	}
	if !strings.Contains(testutil.DatabaseURLEnv, "SP_TEST_DATABASE_URL") {
		t.Errorf("the environment variable is %q and the allow-list and gate both name "+
			"SP_TEST_DATABASE_URL; if this is renamed, the skip reason a contributor reads is "+
			"not the one the gate matches", testutil.DatabaseURLEnv)
	}
}
