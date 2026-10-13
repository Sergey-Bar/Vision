package sqlguard_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/sqlguard"
)

// goSQL wraps a statement the way the product writes one: inside a backtick raw
// string. The guard reads raw strings, so a test that used a quoted string would
// be exercising a path nothing uses.
func goSQL(stmt string) string { return "q(" + "`" + stmt + "`" + ")" }

func repoRoot(t *testing.T) string {
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

// Every SQL literal in the tree, checked against the tables the migrations
// declare.
//
// This is the gate. The three rules it enforces are the three that three
// separate adversarial reviews each found at least one instance of:
//
//   - a mutation of a tenant-scoped table with no tenant predicate
//     (`outbox.deliver`, `outbox.markFailed` — both wrote with
//     `WHERE outbox_id = $1` and no tenant at all);
//   - an UPDATE of a leased table with no lease-owner constraint
//     (`inbox.Fail`, `inbox.Complete`, `outbox.deliver`);
//   - a mutation of an append-only table by anything other than INSERT
//     (`internal/audit` does none, and the test below says so).
//
// A test suite cannot find these. Each needs a lease expiry, a second worker, or
// a race to manifest. A rule can, and this one is derived from the migrations
// rather than from a hand-written table list, so a table added tomorrow is
// covered today.
func TestNoMutationBypassesTheTenantOrTheLease(t *testing.T) {
	root := repoRoot(t)

	tables, err := sqlguard.TablesFromMigrations(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatalf("read the migrations: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("no tables were derived from the migrations; the guard would pass by " +
			"knowing nothing, which is the failure mode this package exists to remove")
	}

	// A table with a tenant id, or a lease, has to be there or the rules below
	// have nothing to say. If the derivation ever stops working, the gate must go
	// red rather than quietly cover less.
	for _, want := range []string{"inbox_events", "outbox_events", "jobs", "audit_ledger"} {
		facts, ok := tables[want]
		if !ok {
			t.Errorf("the table %q was not derived from the migrations. The guard is a "+
				"function of the schema, and a function that quietly returns less is worse "+
				"than no function", want)
			continue
		}
		if !facts.TenantScoped {
			t.Errorf("%q was derived with TenantScoped false; every table in this schema "+
				"carries a tenant id", want)
		}
	}
	for _, want := range []string{"inbox_events", "outbox_events", "jobs"} {
		if facts, ok := tables[want]; ok && !facts.Leased {
			t.Errorf("%q was derived with Leased false; it has a lease_owner column", want)
		}
	}
	if facts, ok := tables["audit_ledger"]; !ok || !facts.AppendOnly {
		t.Error(`"audit_ledger" was not derived as append-only. Its trigger is what makes
		invariant 4 enforceable, and the derivation reads the trigger's definition, not
		its name`)
	}

	var findings []sqlguard.Finding
	files := 0
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if strings.HasPrefix(rel, "migrations"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "tools"+string(filepath.Separator)) {
			// Migrations are allowed to do anything to a table — that is what a
			// migration is for — and the contract tools read the contract, not the
			// schema.
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files++
		findings = append(findings, sqlguard.Check(rel, string(body), tables)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if files == 0 {
		t.Fatal("no Go files were scanned; the gate would pass by scanning nothing")
	}

	if len(findings) > 0 {
		// An exemption is reported, not failed on ? and not hidden either. The
		// point of requiring a reason next to the marker is that the reason is
		// read, so every one of them is logged on every run. An exemption nobody
		// ever sees is an exemption nobody ever judges.
		var violations, excused []sqlguard.Finding
		for _, f := range findings {
			if strings.HasPrefix(f.Rule, "exempt:") {
				excused = append(excused, f)
			} else {
				violations = append(violations, f)
			}
		}
		sort.Slice(violations, func(i, j int) bool {
			return violations[i].String() < violations[j].String()
		})
		for _, f := range violations {
			t.Errorf("%s", f)
		}
		if len(violations) > 0 {
			t.Errorf("%d statement(s) break a SQL rule. Each of these was found by hand "+
				"during this release; the rule is what stops the next one", len(violations))
		}
		for _, f := range excused {
			t.Logf("exempted: %s:%d: %s", f.File, f.Line, f.Rule)
		}
		t.Logf("%d statement(s) exempted, each with a reason on the statement", len(excused))
	}
	t.Logf("scanned %d file(s) against %d table(s)", files, len(tables))
}

// The rules are only worth having if they fire. Each is exercised against a
// synthetic statement that is the shape of the real defect, and each is paired
// with a compliant statement that must pass — because a guard that fires on
// everything is a guard people disable, and a guard that fires on nothing is the
// bug it was written for.
func TestEachRuleFiresOnTheShapeItIsFor(t *testing.T) {
	tables := map[string]sqlguard.TableFacts{
		"inbox_events":  {Name: "inbox_events", TenantScoped: true, Leased: true},
		"outbox_events": {Name: "outbox_events", TenantScoped: true, Leased: true},
		"audit_ledger":  {Name: "audit_ledger", TenantScoped: true, AppendOnly: true},
		"clock_pairs":   {Name: "clock_pairs", TenantScoped: false, Leased: false},
	}

	cases := []struct {
		name      string
		source    string
		wantRules []string
		wantClean bool
	}{
		{
			name:   "a mutation with no tenant predicate is the outbox deliver defect",
			source: goSQL(`UPDATE outbox_events SET status = 'delivered' WHERE outbox_id = $1`),
			wantRules: []string{
				"must constrain tenant_id",
				"must constrain lease_owner",
			},
		},
		{
			name:      "a leased update with the tenant but not the owner is the inbox Fail defect",
			source:    goSQL(`UPDATE inbox_events SET status = 'failed'\n\t\t\t WHERE tenant_id = $1 AND source_event_id = $3`),
			wantRules: []string{"must constrain lease_owner"},
		},
		{
			name:   "an update of an append-only table",
			source: goSQL(`UPDATE audit_ledger SET reason = 'x' WHERE tenant_id = $1 AND sequence = $2`),
			wantRules: []string{
				"append-only table is written by INSERT",
			},
		},
		{
			name: "a compliant mutation passes",
			source: goSQL(`UPDATE inbox_events SET status = 'processed'
			WHERE tenant_id = $1 AND source_event_id = $3
			  AND status = 'processing' AND lease_owner IS NOT DISTINCT FROM $4`),
			wantClean: true,
		},
		{
			name:      "a compliant append-only insert passes",
			source:    goSQL(`INSERT INTO audit_ledger (tenant_id, action)\n\t\tVALUES ($1, $2)`),
			wantClean: true,
		},
		{
			name:      "a mutation of a table with no tenant is not a rule violation",
			source:    goSQL(`UPDATE clock_pairs SET drift_ms = 0 WHERE pair_id = $1`),
			wantClean: true,
		},
		{
			name:      "a select is not a mutation",
			source:    goSQL(`SELECT outbox_id FROM outbox_events WHERE outbox_id = $1`),
			wantClean: true,
		},
		{
			name:   "a delete with no tenant predicate",
			source: goSQL(`DELETE FROM outbox_events WHERE outbox_id = $1`),
			wantRules: []string{
				"must constrain tenant_id",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqlguard.Check("test.go", tc.source, tables)
			if tc.wantClean {
				if len(got) != 0 {
					t.Errorf("a compliant statement produced findings:\n  %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("the rule did not fire on: %s", tc.source)
			}
			joined := ""
			for _, f := range got {
				joined += f.Rule + "\n"
			}
			for _, want := range tc.wantRules {
				if !strings.Contains(joined, want) {
					t.Errorf("the finding did not say %q; it said:\n%s", want, joined)
				}
			}
		})
	}
}

// The table list is derived from the migrations, so it is worth proving the
// derivation reads what it claims to read. A parser that silently returns an
// empty map would make every other test in this file pass for the wrong reason.
func TestTheTableListIsDerivedFromTheMigrationsAndNotInvented(t *testing.T) {
	root := repoRoot(t)
	tables, err := sqlguard.TablesFromMigrations(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatalf("read the migrations: %v", err)
	}

	dir := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the migration directory: %v", err)
	}
	// Distinct tables, not statements. The migrations declare 86 CREATE TABLE
	// statements and 32 tables: migration 0015 alone declares 26 partitions,
	// which inherit their parent's facts and are not written to directly, and
	// several tables are declared once per expansion.
	//
	// The first version of this assertion compared against the statement count
	// and failed on a derivation that was covering *more* than it needed to. A
	// test whose expectation is wrong in the strict direction is a test that
	// gets deleted instead of fixed.
	declared := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(strings.ToUpper(line), "CREATE TABLE") {
				continue
			}
			// A partition declaration is
			//   CREATE TABLE x PARTITION OF y FOR VALUES FROM (...)
			// and is not a table with its own facts. The marker is later in the
			// line than the name, so the test has to look at the line.
			if strings.Contains(strings.ToUpper(line), " PARTITION OF ") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "CREATE TABLE"))
			fields := strings.Fields(name)
			switch {
			case len(fields) >= 4 && strings.EqualFold(fields[0], "if") &&
				strings.EqualFold(fields[1], "not") && strings.EqualFold(fields[2], "exists"):
				name = fields[3]
			case len(fields) >= 3 && strings.EqualFold(fields[0], "if") &&
				strings.EqualFold(fields[1], "exists"):
				name = fields[2]
			case len(fields) > 0:
				name = fields[0]
			}
			declared[strings.ToLower(strings.Trim(name, `(";`))] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("the migrations declare no tables; the fixture is wrong, not the parser")
	}
	if len(tables) < len(declared) {
		t.Errorf("the migrations declare %d distinct table(s) and the derivation produced %d "+
			"facts. A guard that covers less than the schema while reporting green is the exact "+
			"condition this package exists to remove", len(declared), len(tables))
	}
	// And the two rules cannot be vacuous: there has to be at least one table
	// carrying each fact the rules branch on.
	var tenantScoped, leased, appendOnly int
	for _, f := range tables {
		if f.TenantScoped {
			tenantScoped++
		}
		if f.Leased {
			leased++
		}
		if f.AppendOnly {
			appendOnly++
		}
	}
	if tenantScoped == 0 || leased == 0 || appendOnly == 0 {
		t.Errorf("the derivation found %d tenant-scoped, %d leased and %d append-only table(s). "+
			"A rule with no table to apply it to passes for the wrong reason",
			tenantScoped, leased, appendOnly)
	}
}
