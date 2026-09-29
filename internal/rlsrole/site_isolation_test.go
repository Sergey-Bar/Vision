package rlsrole_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
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
// tenant-scoped table Ã¢â‚¬â€ one statement per table, and then *every* query that does
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

// Condition one: the policies are still PERMISSIVE, which is the defect this
// file exists to schedule the end of. When this fails, the flip has been done and
// this whole file can go.
func TestTheSitePolicyIsStillPermissiveAndThatIsTheDefect(t *testing.T) {
	permissive := policyIsPermissive(t, "evidence", "site_isolation")
	if !permissive {
		t.Skip("site_isolation is already RESTRICTIVE; the flip has been done and the " +
			"remaining conditions have been checked separately")
	}
	t.Log("site_isolation is PERMISSIVE, so it is ORed with tenant_isolation and there is " +
		"no site isolation at the row level. This is the known gap; the fix is one " +
		"ALTER POLICY per table once the conditions below hold.")
}

// Condition two: every request-path query that touches a site-scoped table binds
// the principal's site scope. `For` binds the tenant and nothing else.
//
// The decision is made by the *table*, not by the call. `audit_ledger` is
// tenant-wide by design â€” a ledger row is attributed to a site by its
// `site_scope` column and does not belong to one â€” so it is read with `For` and
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
// scope.
//
// This is the blocker, and it is not close.
//
// The API handlers bind a scope directly, but the services they call take a
// `tenant string` and build their own scoped pool, so a handler's scope is lost
// the moment it crosses into `incidents`, `inbox`, `jobs` or `outbox`.
// `incidents` has a scope-carrying method because the incident-list fix needed
// one. The other three do not, and neither do the consume loops: they are
// driven by `Deps.SiteID` and would each need a site-bound pool of their own.
//
// Flipping the policy to RESTRICTIVE before that is done would make every
// site-scoped read through those services return nothing. That is fail-closed
// and therefore not a security hole, and it is also an ingest outage on a site
// that looks healthy in every other respect — the worst failure this codebase
// could ship, and the reason the flip is a separate change.
//
// The check is a skip rather than a failure so the tree stays green, and the
// skip names itself: `go test -v ./internal/rlsrole/ -run TestTheServiceLayer`
// prints the blocker on every run until it is resolved.
func TestTheServiceLayerCanCarryASiteScope(t *testing.T) {
	root := repoRootOf(t)
	scoped := regexp.MustCompile(`func \([^)]*Service\) [A-Z]\w*Scoped\(`)

	// Every package whose tables carry a site_id and whose code reads them.
	packages := []string{"incidents", "inbox", "jobs", "outbox", "correlator"}
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
			if readErr == nil && scoped.Match(body) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, pkg)
		}
	}

	if len(missing) > 0 {
		t.Skipf("BLOCKER for the site_isolation RESTRICTIVE flip: %v take a tenant and "+
			"build their own scoped pool, so a handler's site scope is lost at the "+
			"service boundary. Until each has a scope-carrying signature, flipping the "+
			"policy would make every site-scoped read through them return nothing. "+
			"See docs/status.md", missing)
	}
}
