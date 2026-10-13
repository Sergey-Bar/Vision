package roadmapcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's directory to the module root.
//
// A fixed relative path would break the moment the package moved, and a test
// that reads the wrong tree is worse than no test: it reports green about a
// document it never opened.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// TestRoadmapMatchesTheTree is the DoD for the roadmap documents.
//
// It exists because three documents carried three different stale counts of the
// same tree at the same time and nothing noticed. Every number is derived from
// the directory that defines it, so the only way to go green is to make the
// documents match.
func TestRoadmapMatchesTheTree(t *testing.T) {
	findings, err := Check(repoRoot(t))
	if err != nil {
		t.Fatalf("roadmapcheck: %v", err)
	}
	for _, f := range sorted(findings) {
		t.Errorf("%s", f)
	}
}

// TestDeriveCountsTheTreeDirectly pins the derivation itself.
//
// TestRoadmapMatchesTheTree would pass vacuously if Derive returned zeros and
// the documents happened to print zeros, so the numbers are asserted against the
// tree here independently. These are the values the documents are expected to
// print, and they are the thing a reader checks the document against by hand.
//
// Updating them is the cost of a tree change: a new package, a new migration or
// a new .proto fails here first, loudly, with the new count in the message.
func TestDeriveCountsTheTreeDirectly(t *testing.T) {
	d, err := derive(repoRoot(t))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if d.Packages != 83 {
		t.Errorf("Go packages: got %d, want 83", d.Packages)
	}
	if d.Migrations != 26 {
		t.Errorf("migrations: got %d, want 26", d.Migrations)
	}
	if d.Contracts != 13 {
		t.Errorf("protobuf contracts: got %d, want 13", d.Contracts)
	}
}

// TestTheDocumentsActuallyPrintTheCounts is the control on the control.
//
// Every assertion above is a comparison, and a comparison passes when both
// sides are zero or when the pattern silently stops matching. This test
// requires each claim to be live in the real document, so a future edit that
// rewords a sentence and drops the number cannot turn the gate into a no-op
// that reports green forever.
func TestTheDocumentsActuallyPrintTheCounts(t *testing.T) {
	root := repoRoot(t)
	d := mustDerive(t, root)
	for _, file := range targets {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(raw)
		for _, c := range claimsFor(d, file) {
			if !c.re.MatchString(text) {
				t.Errorf("%s prints no %q, so that claim is not checked by "+
					"TestRoadmapMatchesTheTree. Either state it with the correct "+
					"number or remove the check; a claim that cannot be seen is a "+
					"claim that reads as checked", file, c.label)
			}
		}
	}
}

// TestCountPackagesSkipsWhatTheGoToolSkips drives the walk rule on a synthetic
// tree, because the rule is what makes the count derive and the real tree only
// exercises the happy path.
//
// The cases are the three that would silently change the number: a nested
// module (`poc/active-monitoring` has its own go.mod and is not in `./...` at
// the root), a dot directory (`.kilo` holds an Agent Manager worktree that is a
// full second copy of this repository), and an underscore directory, which the
// go tool ignores. A directory holding no Go file is not a package either.
func TestCountPackagesSkipsWhatTheGoToolSkips(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/synthetic\n")
	write("internal/one/one.go", "package one\n")
	write("internal/two/two.go", "package two\n")
	write("cmd/tool/main.go", "package main\n")
	// Not a package: no Go file at all.
	write("docs/README.md", "# nothing to compile\n")
	// A nested module, and its packages, are a different module's business.
	write("poc/active-monitoring/go.mod", "module example.com/poc\n")
	write("poc/active-monitoring/monitor/monitor.go", "package monitor\n")
	// Dot and underscore directories are ignored by the go tool, and .kilo
	// holds a worktree that would otherwise double every count.
	write(".kilo/worktrees/x/internal/one/one.go", "package one\n")
	write("_ignored/thing.go", "package thing\n")

	got, err := countPackages(root)
	if err != nil {
		t.Fatalf("countPackages: %v", err)
	}
	if want := 3; got != want {
		t.Errorf("countPackages: got %d, want %d (internal/one, internal/two, cmd/tool)", got, want)
	}
}

// TestAStaleNumberIsReportedAndACorrectOneIsNot is the falsification step for
// the gate itself: a checker that never reports anything would pass every test
// above, so the reporting path is driven directly.
func TestAStaleNumberIsReportedAndACorrectOneIsNot(t *testing.T) {
	d := Derived{Packages: 82, Migrations: 25, Contracts: 13}
	const stale = "State today: 24 Go packages, 12 forward-only migrations, 13 contracts.\n"
	const current = "State today: 82 Go packages, 25 forward-only migrations, 13 contracts.\n"

	// The stale sentence trips the two claims that are present and wrong, and
	// reports the line it sits on. "13 contracts" agrees and stays silent.
	findings := checkText("ROADMAP.md", stale, d)
	if len(findings) != 2 {
		t.Fatalf("a stale sentence produced %d findings, want 2 (packages and migrations): %v", len(findings), findings)
	}
	if findings[0].Label != "Go package count" || findings[0].Got != 24 || findings[0].Want != 82 {
		t.Errorf("first finding is %+v, want the package claim 24 vs 82", findings[0])
	}
	if findings[1].Label != "migration count" || findings[1].Got != 12 || findings[1].Want != 25 {
		t.Errorf("second finding is %+v, want the migration claim 12 vs 25", findings[1])
	}
	if findings[0].File != "ROADMAP.md" || findings[0].Line != 1 {
		t.Errorf("finding is anchored to %s:%d, want ROADMAP.md:1", findings[0].File, findings[0].Line)
	}

	// The correct sentence is silent.
	clean := checkText("ROADMAP.md", current, d)
	if len(clean) != 0 {
		t.Errorf("a correct sentence produced findings: %v", clean)
	}
}

// TestHistoryIsNotReportedAsAClaim is the control on the internal/README.md
// anchoring.
//
// Trap 14 in that file records, accurately, that the tree once "had thirty leaf
// packages" — a statement about a past tree that must stay. The claim pattern
// is anchored to the sentence that makes a current claim ("to open N
// packages"), and this test drives both sentences through the checker to show
// the anchor is what separates them: the current claim is checked, the history
// is not, and neither produces a finding when both are correct.
func TestHistoryIsNotReportedAsAClaim(t *testing.T) {
	d := Derived{Packages: 82}
	const doc = "It answers *where things are* so neither of you has to open 82 packages " +
		"to find out.\n\n14. It said the suite was green \"across 24 packages\" when the " +
		"tree had thirty leaf packages, and it said the tree had thirty leaf packages, " +
		"and there are forty-two.\n"

	findings := checkText("internal/README.md", doc, d)
	for _, f := range findings {
		t.Errorf("history was reported as a claim: %s", f)
	}

	// And when the current sentence goes stale, exactly one finding appears —
	// the current claim, not the history.
	stale := strings.Replace(doc, "open 82 packages", "open 30 packages", 1)
	findings = checkText("internal/README.md", stale, d)
	if len(findings) != 1 {
		t.Fatalf("a stale current claim produced %d findings, want 1: %v", len(findings), findings)
	}
	if findings[0].Line != 1 {
		t.Errorf("finding is on line %d, want 1 (the current claim, not the history further down)", findings[0].Line)
	}
}

// TestMigrationsCountNumbersNotFiles pins the pair rule: an expand and a
// contract step are one migration, so two files are one count. A migration
// numbered twice would otherwise be counted twice, and the document would be
// asked to agree with a number that is not the migration count.
func TestMigrationsCountNumbersNotFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"0001_first.expand.sql",
		"0001_first.contract.sql",
		"0002_second.expand.sql",
		"0002_second.contract.sql",
		"README.md",
		"0003_draft.txt",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("-- sql\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := countMigrations(filepath.Dir(root))
	if err != nil {
		t.Fatalf("countMigrations: %v", err)
	}
	if got != 2 {
		t.Errorf("countMigrations: got %d, want 2 (two numbers, four sql files)", got)
	}
}

// checkText runs the claim patterns for one file over a synthetic document. It
// is the seam the two behaviour tests above drive, and it exists so those tests
// do not have to write the real documents to disk to prove the checker works.
//
// It returns no error because there is nothing in it that can fail: the document
// is already a string. An error return here would be a signature that promises
// a failure mode the function does not have, which is how a caller learns to
// write `if err != nil` around something that cannot be non-nil.
func checkText(file, text string, d Derived) []Finding {
	var out []Finding
	for _, c := range claimsFor(d, file) {
		for _, m := range c.re.FindAllStringSubmatchIndex(text, -1) {
			if len(m) < 4 {
				continue
			}
			got := atoiOrZero(text[m[2]:m[3]])
			if got == c.want {
				continue
			}
			out = append(out, Finding{
				Label: c.label,
				Want:  c.want,
				Got:   got,
				File:  file,
				Line:  lineOf(text, m[0]),
			})
		}
	}
	return out
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func mustDerive(t *testing.T, root string) Derived {
	t.Helper()
	d, err := derive(root)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return d
}
