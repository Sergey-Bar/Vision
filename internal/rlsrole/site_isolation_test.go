package rlsrole

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

func repoRootOf(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the repository root")
		}
		dir = parent
	}
	return ""
}

// The two policies on every tenant table are PERMISSIVE, so PostgreSQL ORs them
// and `tenant_isolation OR site_isolation` is `tenant_isolation`.
//
// The intent is stated in migration 0008 as "so the two concerns compose: tenant
// first, then site", which is AND. The DDL implements OR, so there is no site
// isolation at the database layer at all: the API handlers' `p.CanSeeSite`
// checks are the whole control, and the comment describes a defence that does
// not exist.
//
// Fixing it is `ALTER POLICY site_isolation ... AS RESTRICTIVE` on every
// tenant-scoped table - and the set of them is not fixed, because 0008 applied the
// policy by enumerating whatever tenant-bearing relations existed at the time, and
// later migrations added more. That is exactly why this is one statement per table:
// a list in a document is not a control, because the next migration that adds a
// tenant-bearing table has to add its own `ALTER POLICY`, and nothing here makes
// that happen automatically.
// not set `app.site_ids` sees no site-bound rows. That is fail-closed, which is
// the right direction and also the dangerous one: a consume loop that forgets
// its scope ingests nothing, and that failure is invisible until a site has
// stopped receiving events.
//
// So the flip is deliberately not done in the same change as the plumbing. These
// tests are the readiness check: each one states a condition that must hold
// before the policy changes, and each fails today for the right reason. When
// they all pass, the flip is one migration and no code change.
//
// This file is the handoff. Deleting it after the flip is the last step.

// The scan is derived from the migrations so a table added tomorrow is included.
func siteScopedTables(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRootOf(t), "migrations"))
	if err != nil {
		t.Fatalf("read the migrations: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(repoRootOf(t), "migrations", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(body)
		// A `CREATE TABLE` whose body declares `site_id` is site-scoped. This is
		// the same derivation the sql guard uses, kept in step deliberately: two
		// different notions of which tables are site-scoped is one of the ways a
		// readiness check stops meaning anything.
		for _, block := range createTableBodies(text) {
			if columnRe.MatchString(block) {
				name := strings.Fields(block)[0]
				seen[strings.ToLower(name)] = true
			}
		}
	}
	var out []string
	for name := range seen {
		out = append(out, name)
	}
	return out
}

var (
	createTableRe = regexp.MustCompile(`(?is)create\s+table\s+([a-z_][a-z0-9_]*)\s*\((.*?)\n\);`)
	columnRe      = regexp.MustCompile(`(?im)^\s*site_id\s+[a-z]`)
)

func createTableBodies(sqlText string) []string {
	var out []string
	for _, m := range createTableRe.FindAllStringSubmatch(sqlText, -1) {
		if len(m) < 3 {
			continue
		}
		if strings.Contains(strings.ToUpper(m[1]), "PARTITION") {
			continue
		}
		out = append(out, m[1]+" "+m[2])
	}
	return out
}

// policyIsPermissive asks the database, rather than reading the migration text.
//
// The question is what PostgreSQL will do, and the answer is a property of the
// catalogue rather than of the file that created the policy. Asking the
// catalogue is also how a reader can check this for themselves:
//
//	SELECT polname, polpermissive FROM pg_policy WHERE polrelid = 'evidence'::regclass;
func policyIsPermissive(t *testing.T, table, policy string) bool {
	t.Helper()
	raw := testutil.Pool(t) // skips when there is no database
	var permissive bool
	err := raw.QueryRow(context.Background(),
		`SELECT polpermissive FROM pg_policy
		  WHERE polrelid = $1::regclass AND polname = $2`, table, policy).Scan(&permissive)
	if err != nil {
		t.Fatalf("read the %s policy on %s: %v", policy, table, err)
	}
	return permissive
}

// Condition one: the policy is now RESTRICTIVE, and this is the assertion that
// says so.
//
// Before migration 21 both policies were PERMISSIVE and PostgreSQL ORed them, so
// `tenant_isolation OR site_isolation` was `tenant_isolation` and there was no
// site isolation at the row level at all. This file existed to schedule that
// change; the change is made, and the first condition below is now the proof.
func TestTheSitePolicyIsRestrictiveSoTheTwoCompose(t *testing.T) {
	if policyIsPermissive(t, "evidence", "site_isolation") {
		t.Fatal("site_isolation is PERMISSIVE. PostgreSQL ORs multiple permissive policies " +
			"for the same command, so tenant_isolation OR site_isolation is " +
			"tenant_isolation and there is no site isolation in the database. " +
			"migrations/0021_site_isolation_restrictive has not been applied")
	}
	// And the tenant policy stays permissive, because a table whose only policies
	// are RESTRICTIVE allows nothing at all.
	if !policyIsPermissive(t, "evidence", "tenant_isolation") {
		t.Error("tenant_isolation is RESTRICTIVE. A table with no permissive policy " +
			"admits no rows to anybody, including the tenant that owns them")
	}
}

// The composition itself, which is the property neither policy expresses alone.
//
// The other test in this package runs as the superuser, which bypasses RLS, so it
// cannot see this. Only a canary running as the production role can, which is why
// the first draft of these assertions could not be written at all.
func TestTheTwoPoliciesComposeAsAnd(t *testing.T) {
	pool := productionPool(t)
	raw := testutil.Pool(t)
	ctx := context.Background()
	tenant, site, _ := testutil.NewTenant(t, raw)
	_, otherSite, _ := testutil.NewTenant(t, raw)

	// One row at the tenant's own site.
	writer := &inbox.Writer{Pool: pool}
	if _, err := writer.Write(ctx, inbox.WriteOptions{
		TenantID: tenant, SiteID: site, IntegrationID: "int-1",
		SourceEventID: "src-compose", Payload: []byte(`{"Id":"src-compose"}`),
		ReceivedAt: clockAt,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Scoped to the right site: sees the row.
	var n int
	testutil.AsRole(t, pool.Pool, "security_core", tenant, []string{site}, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM inbox_events`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
	})
	if n != 1 {
		t.Errorf("a reader scoped to the row's own site sees %d row(s), want 1", n)
	}

	// Same tenant, another site: sees nothing. Under the old permissive policies
	// this returned the row, and no test anywhere could have said so.
	n = 0
	testutil.AsRole(t, pool.Pool, "security_core", tenant, []string{otherSite}, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM inbox_events`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
	})
	if n != 0 {
		t.Errorf("a reader scoped to another site in the same tenant sees %d of this "+
			"tenant's row(s). The two policies are still ORing", n)
	}

	// No site at all: sees nothing, because an unspecified scope is not "every
	// site". That is the fail-closed direction, and it is why every product
	// statement was changed to name its site before this migration ran.
	n = -1
	testutil.AsRole(t, pool.Pool, "security_core", tenant, nil, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM inbox_events`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
	})
	if n != 0 {
		t.Errorf("a reader with no site scope sees %d row(s). An unspecified scope must "+
			"not be read as every site", n)
	}
}

// Condition two: every request-path query that touches a site-scoped table binds
// the principal's site scope. `For` binds the tenant and nothing else.
//
// The decision is made by the *table*, not by the call. `audit_ledger` is
// tenant-wide by design. A ledger row is attributed to a site by its own
// `site_scope` column and does not *belong* to one, so it is read with `For` and
// a rule that flagged it would be wrong in the strict direction - which is how a
// guard gets switched off.
// a rule that flagged it would be wrong in the strict direction, which is how a
// guard gets switched off.
func TestEveryRequestPathQueryOnASiteScopedTableBindsASiteScope(t *testing.T) {
	root := repoRootOf(t)
	scoped := map[string]bool{}
	for _, name := range siteScopedTables(t) {
		scoped[name] = true
	}
	if len(scoped) == 0 {
		t.Fatal("no site-scoped tables were derived from the migrations; the check would " +
			"pass for the wrong reason")
	}

	// A call that binds only a tenant, with the line it is on.
	bare := regexp.MustCompile(`\.For\(\s*(?:p|s)\.TenantID`)
	// The table named in the statement that follows it.
	from := regexp.MustCompile(`(?is)FROM\s+([a-z_][a-z0-9_]*)`)

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !strings.HasPrefix(rel, "internal"+string(filepath.Separator)+"api") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			if !bare.MatchString(line) {
				continue
			}
			// Look ahead for the statement's table; a Go call puts the SQL in
			// the following literal, and a wide SELECT column list can push FROM
			// several lines down.
			window := 10
			lines := strings.Split(string(body), "\n")
			if i+window > len(lines) {
				window = len(lines) - i
			}
			rest := strings.Join(lines[i:i+window], " ")
			m := from.FindStringSubmatch(rest)
			if m == nil {
				offenders = append(offenders, fmt.Sprintf("%s:%d (table not found)", rel, i+1))
				continue
			}
			if scoped[strings.ToLower(m[1])] {
				offenders = append(offenders, fmt.Sprintf("%s:%d reads %s", rel, i+1, m[1]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("%v read a site-scoped table binding only a tenant. The RLS policy is "+
			"permissive, so this changes nothing today, and it is exactly what would "+
			"break the day the policy becomes RESTRICTIVE", offenders)
	}
}

// Condition three: every service that reads a site-scoped table can carry a
// Condition three, which used to be the blocker and is now done.
//
// The service layer carries a site scope. The statements that did not -- the
// inbox write, the normalizer's insert, and the backlog count -- are exactly what
// the RLS canary found when the policy flipped, because every other test in the
// tree connects as the superuser and bypasses RLS entirely. That is the argument
// for a canary: without one, a fail-closed policy has no failure to find.
func TestTheServiceLayerCarriesASiteScope(t *testing.T) {
	root := repoRootOf(t)
	scoped := regexp.MustCompile(`func \([^)]*Service\) [A-Z]\w*Scoped\(`)
	// A `SiteIDs` on a `TenantContext`, or a `WithSites` call, is a service that
	// can narrow itself. Either is what this is looking for.
	// Either shape counts: a service that can narrow itself. A lease-guarded
	// terminal write runs as the system worker on the strength of the lease, which is
	// a stronger predicate than site scope, and that is written as `leasedWrite`.
	carries := regexp.MustCompile(`SiteIDs:\s*\[\]string\{|WithSites\(|leasedWrite\(`)

	packages := []string{"incidents", "inbox", "jobs", "outbox", "correlator", "normalize"}
	var missing []string
	for _, pkg := range packages {
		dir := filepath.Join(root, "internal", pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		found := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			body, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
			if readErr != nil {
				continue
			}
			if scoped.Match(body) || carries.Match(body) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, pkg)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%v cannot narrow itself to a site. Since migration 21 a statement that "+
			"reaches a site-scoped table without naming its site sees nothing, so a "+
			"service that cannot pass a scope is a service that cannot read its own data",
			missing)
	}
}
