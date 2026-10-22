package qamatrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// The registry and the tree agree.
//
// This is the gate. It is the whole of `internal/qamatrix`: every rule below
// is proved able to fire against a fixture, and this is the one that says the
// real tree and the real registry are in step.
func TestTheRegistryAndTheTreeAgree(t *testing.T) {
	root := repoRoot(t)
	findings, err := check(root)
	if err != nil {
		t.Fatalf("qamatrix: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatalf("load the registry: %v", err)
	}
	tree, err := derive(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	t.Logf("\n%s", summary(reg, tree))
}

// The generated document is the one on disk.
//
// `docs/qa-matrix.md` is what a reader opens, and it is generated from the
// registry so the two cannot say different things. This test exists because
// the comparison is in `check`, and a comparison inside a function that
// nothing calls is a comparison that never happens.
func TestTheGeneratedDocumentIsOnDisk(t *testing.T) {
	root := repoRoot(t)
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatalf("load the registry: %v", err)
	}
	tree, err := derive(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "docs", "qa-matrix.md"))
	if err != nil {
		t.Fatalf("read docs/qa-matrix.md: %v", err)
	}
	if got, want := string(onDisk), renderDoc(reg, tree); got != want {
		t.Errorf("docs/qa-matrix.md is not the document the registry generates.\n"+
			"  regenerate it with: QAMATRIX_WRITE_DOCS=1 go test ./internal/qamatrix/...\n"+
			"  first difference: %s", firstDifference(got, want))
	}
}

// TestTheGateCanFail is the control on the control.
//
// A gate that has never been seen to fail is a gate that reports green
// forever, and this repository has been bitten by that shape: the `red-check`
// table once held thirty plausible mutations nobody had run, and the gate
// reported thirty guarded controls while proving none of them.
//
// So each of the nine rules is driven against a FIXTURE -- a synthetic tree
// and a synthetic registry -- and the test asserts the rule fires. If a rule
// is deleted, or its condition is inverted, or it is made unfireable, this
// test goes red.
//
// The fixture is deliberately small and deliberately broken in one way at a
// time, so a failure names the rule rather than the fixture.
func TestTheGateCanFail(t *testing.T) {
	cases := []struct {
		rule string
		// mutate breaks the fixture in exactly one way.
		mutate func(reg *Registry, t *tree)
		// want is a phrase the finding must contain.
		want string
	}{
		{
			rule:   "a package in the tree that is not in the registry",
			mutate: func(reg *Registry, t *tree) { reg.Packages = reg.Packages[1:] },
			want:   "is not in the registry",
		},
		{
			rule:   "a registry entry for a package that does not exist",
			mutate: func(reg *Registry, t *tree) { reg.Packages[0].Package = "internal/ghost" },
			want:   "is not a package in the tree",
		},
		{
			rule: "a tier assignment that disagrees with the tier table",
			mutate: func(reg *Registry, t *tree) {
				reg.Packages[0].Tier = "B"
			},
			want: "in its row and tier",
		},
		{
			rule: "a category name outside the closed set",
			mutate: func(reg *Registry, t *tree) {
				reg.Packages[0].Categories["vibes"] = Category{Tests: []string{"TestNothing"}}
			},
			want: "not one of the fourteen closed categories",
		},
		{
			rule: "a named test that does not exist",
			mutate: func(reg *Registry, t *tree) {
				reg.Packages[0].Categories["functional"] = Category{Tests: []string{"TestNeverWritten"}}
			},
			want: "does not exist in the package's own test files",
		},
		{
			rule: "a Tier A package with a blank category",
			mutate: func(reg *Registry, t *tree) {
				delete(reg.Packages[0].Categories, "boundary")
			},
			want: "categories are blank",
		},
		{
			rule: "a critical-path stage whose proof names a test that does not exist",
			mutate: func(reg *Registry, t *tree) {
				reg.CriticalPath[0].ProvedBy = []string{"internal/alpha.TestNeverWritten"}
			},
			want: "does not exist in that package's own test files",
		},
		{
			rule: "a critical-path stage that is missing",
			mutate: func(reg *Registry, t *tree) {
				reg.CriticalPath = reg.CriticalPath[1:]
			},
			want: "is missing",
		},
		{
			rule: "a category that names tests AND carries a reason",
			mutate: func(reg *Registry, t *tree) {
				reg.Packages[0].Categories["functional"] = Category{
					Tests:  []string{"TestAlpha"},
					Reason: "not applicable here",
				}
			},
			want: "names tests AND carries a reason",
		},
	}

	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			reg, tree := fixture(t)
			c.mutate(reg, tree)
			findings := verifyFixture(t, reg, tree)
			if len(findings) == 0 {
				t.Fatalf("the gate said nothing about: %s", c.rule)
			}
			joined := strings.Join(findings, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("the gate fired, but not about the rule under test.\n  wanted a finding containing %q\n  got:\n%s", c.want, joined)
			}
		})
	}
}

// The fixture: one package, one test, one fully classified row, and the whole
// closed set of stages.
//
// It is a FIXTURE rather than a copy of the real registry on purpose: a copy
// would be a second thing to keep in step, and the point is to break one
// thing at a time.
func fixture(t *testing.T) (*Registry, *tree) {
	t.Helper()
	tr := &tree{
		packages: []string{"internal/alpha"},
		tests:    map[string]map[string]bool{"internal/alpha": {"TestAlpha": true}},
	}
	cats := map[string]Category{}
	for _, c := range closedCategories {
		cats[c] = Category{Tests: []string{"TestAlpha"}}
	}
	reg := &Registry{
		Categories: append([]string(nil), closedCategories...),
		Tiers:      map[string][]string{"A": {"internal/alpha"}},
		Packages: []Package{
			{Package: "internal/alpha", Tier: "A", Categories: cats},
		},
	}
	for _, s := range closedStages {
		reg.CriticalPath = append(reg.CriticalPath, Stage{
			Stage:    s,
			ProvedBy: []string{"internal/alpha.TestAlpha"},
		})
	}
	return reg, tr
}

// verifyFixture runs the gate's rules over a fixture.
//
// The rules are written against a registry and a tree, and the real gate
// loads them from disk. This runs the same rules over the fixture, so the
// fixture exercises the REAL conditions rather than a copy of them.
func verifyFixture(t *testing.T, reg *Registry, tr *tree) []string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "qamatrix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The document is written from the fixture so the document rule does not
	// fire and hide the rule under test.
	if err := os.WriteFile(filepath.Join(dir, "docs", "qa-matrix.md"), []byte(renderDoc(reg, *tr)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The tree is written so the "package in the tree" rule sees the fixture's
	// package and nothing else.
	writeFixtureTree(t, dir, tr)

	raw, err := marshalRegistry(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "qamatrix", "registry.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := check(dir)
	if err != nil {
		t.Fatalf("check the fixture: %v", err)
	}
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.String())
	}
	return out
}

// writeFixtureTree writes the fixture's packages into a temporary tree.
func writeFixtureTree(t *testing.T, root string, tr *tree) {
	t.Helper()
	for _, pkg := range tr.packages {
		dir := filepath.Join(root, filepath.FromSlash(pkg))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package alpha\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		names := tr.tests[pkg]
		var b strings.Builder
		for name := range names {
			b.WriteString("package alpha\n\nfunc " + name + "(t *testing.T) {}\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "alpha_test.go"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// firstDifference reports where two strings first differ, for a message a
// person can act on.
func firstDifference(got, want string) string {
	g := strings.Split(got, "\n")
	w := strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			return "line " + itoa(i+1) + ":\n    on disk:   " + g[i] + "\n    generated: " + w[i]
		}
	}
	if len(g) != len(w) {
		return "the file has " + itoa(len(g)) + " lines and the generated one has " + itoa(len(w))
	}
	return "the files are identical"
}

// itoa is `strconv.Itoa` without the import, kept local so the message helper
// stays a helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestWriteDocs regenerates the document when the environment asks for it.
//
// The mode exists so the document is produced by the same code that checks
// it. A separate generator is a second implementation, and a second
// implementation is a second thing to be wrong.
func TestWriteDocs(t *testing.T) {
	if os.Getenv("QAMATRIX_WRITE_DOCS") != "1" {
		t.Skip("set QAMATRIX_WRITE_DOCS=1 to regenerate docs/qa-matrix.md")
	}
	root := repoRoot(t)
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatalf("load the registry: %v", err)
	}
	tr, err := derive(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	path := filepath.Join(root, "docs", "qa-matrix.md")
	if err := os.WriteFile(path, []byte(renderDoc(reg, tr)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}
