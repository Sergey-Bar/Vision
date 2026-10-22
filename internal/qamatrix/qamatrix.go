// Package qamatrix is the categorical half of the QA pyramid.
//
// # Why this exists
//
// Coverage answers one question: was the code executed? It is a real question
// and this repository already asks it, with a ratchet that fails in both
// directions (`internal/covercheck`). But a package can be ninety per cent
// covered and still have no boundary test, no refusal test, no concurrency
// test and no tenant-isolation test, and the coverage number cannot tell the
// difference between a package that was measured thoroughly and a package
// whose one uncovered tenth is the branch that refuses a cross-tenant write.
//
// The categories are the closed set of questions a security product has to be
// able to answer about itself:
//
//	functional       does the claimed behaviour happen?
//	boundary         what happens at the edges?
//	error-refusal    is every refusal the contract promises?
//	invariant        do the five laws in AGENTS.md hold?
//	state-machine    are the transitions complete and total?
//	concurrency      does it survive two writers?
//	durability       does it survive `kill -9`?
//	tenant-isolation can one tenant read another's?
//	contract         do both ends of the wire agree?
//	time             is the clock the site's clock?
//	security         can it be attacked?
//	observability    can an operator see what happened?
//	a11y             can a keyboard and a screen reader use it?
//	performance      does it hold under load?
//
// The list is CLOSED on purpose. An open list is a list that grows until it
// means nothing, and a matrix whose categories are chosen per package is a
// matrix that only says the package is fine.
//
// # The registry, and the rule that makes it worth reading
//
// `registry.json` carries one row per package and one entry per category. A
// category is either covered by a NAMED TEST that exists in that package's
// test files, or it carries a reason saying why the category does not apply
// to that package. A blank is a finding.
//
// The load-bearing rule is the fifth: **a named test that does not exist is a
// finding.** A row that claims coverage nobody can find is a claim, not a
// control, and this repository has been bitten by that shape before -- the
// `red-check` table once held thirty plausible mutations nobody had run, and
// the gate reported thirty guarded controls while proving none of them.
//
// So the gate greps the package's own `*_test.go` files for `func <Name>(`
// and reports a name it cannot find. A typo, a renamed test and a test that
// was deleted all fail here, which is the point: the matrix is checked
// against the tree rather than trusted.
//
// # What the gate refuses
//
//  1. A package in the tree that is not in the registry. A new package cannot
//     be added without being classified.
//  2. A registry entry for a package that does not exist.
//  3. A tier assignment that disagrees with the tier table.
//  4. A category name outside the closed set.
//  5. A named test that does not exist.
//  6. A Tier A package with a blank category: a category is either a named
//     test or a reason, never nothing.
//  7. A Tier B/C package with a blank category: allowed, but the count of
//     blanks is printed, so the gap is visible and shrinking.
//  8. A `criticalPath` stage whose `provedBy` names a test that does not exist.
//  9. A critical-path stage that is missing. The stage list is closed, and it
//     is the spine of the product: ingest, normalize, rule match, incident,
//     attention queue, operator read, disposition, audit ledger, checkpoint,
//     replay.
//
// # The human-readable half
//
// `docs/qa-matrix.md` is GENERATED from the registry and checked in, and the
// gate fails when the two disagree. The same discipline `internal/statuscheck`
// applies to `docs/status.md`: a number nobody derives is a number nobody
// checks, and a table a person reads and a table a machine checks are two
// truths unless one of them is generated from the other.
//
// # What it does not claim
//
// A category with a named test is a category with ONE test. It is not a
// proof that the category is covered, and the matrix says so: the categories
// are a checklist of the questions that have been asked, not a certificate
// that the answers are right. The `red-check` table is what proves the
// answers bite, and this gate is what proves the answers are findable.
package qamatrix

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// closedCategories is the set, and it is closed.
//
// The order is the order the questions are asked, and it is the order the
// generated document uses, so a reader and a machine see the same table.
var closedCategories = []string{
	"functional",
	"boundary",
	"error-refusal",
	"invariant",
	"state-machine",
	"concurrency",
	"durability",
	"tenant-isolation",
	"contract",
	"time",
	"security",
	"observability",
	"a11y",
	"performance",
}

// closedStages is the product's critical path, and it is closed.
//
// The chain is the one `AGENTS.md` protects and the one the plan names:
//
//	ingest -> normalize -> rule match -> incident -> attention queue
//	-> operator read -> disposition -> audit ledger -> checkpoint -> replay
//
// A stage that is dropped is a stage nobody is proving, and the omission is
// invisible: the rest of the chain still passes. So the list lives here, in
// the code, and the registry is checked against it. The registry says WHO
// proves each stage; the code says WHICH stages exist.
var closedStages = []string{
	"ingest",
	"normalize",
	"rule-match",
	"incident",
	"attention-queue",
	"operator-reads",
	"disposition",
	"audit-ledger",
	"checkpoint",
	"replay",
}

// Registry is the whole matrix, as it is written down.
type Registry struct {
	// Note is the document's own preamble, carried into the generated file so
	// the two cannot say different things about what the registry is.
	Note []string `json:"note"`
	// Categories is the registry's own list of the closed set. It is checked
	// against the code's list, because a registry that quietly drops a
	// category is a registry that quietly stops asking a question.
	Categories []string `json:"categories"`
	// Tiers maps a tier name to the packages in it.
	Tiers map[string][]string `json:"tiers"`
	// Packages is one row per package.
	Packages []Package `json:"packages"`
	// CriticalPath is the spine.
	CriticalPath []Stage `json:"criticalPath"`
}

// Package is one row: a package, its tier, and its categories.
type Package struct {
	// Package is the path, relative to the repository root, in slash form.
	Package string `json:"package"`
	// Tier is the tier it belongs to.
	Tier string `json:"tier"`
	// Categories maps a category name to its entry.
	Categories map[string]Category `json:"categories"`
}

// Category is one entry: covered by named tests, or not applicable, and why.
//
// The two fields are mutually exclusive on purpose. A category that names
// tests AND carries a reason is a category that is both covered and excused,
// which is a way of saying nothing, and the gate refuses it.
//
// The shape is a plain object rather than a union of "an array or an object",
// because a custom `UnmarshalJSON` would be a method the exported-symbol gate
// would have to be told to ignore, and a gate that needs an exemption for its
// own encoding is a gate with one more thing to keep in step.
type Category struct {
	// Tests are the named tests that cover the category, in the package's own
	// test files.
	Tests []string `json:"tests,omitempty"`
	// Reason says why the category does not apply, when it does not.
	Reason string `json:"reason,omitempty"`
}

// blank reports whether the category says nothing at all.
func (c Category) blank() bool {
	return len(c.Tests) == 0 && strings.TrimSpace(c.Reason) == ""
}

// Stage is one link of the critical path.
type Stage struct {
	// Stage names the link.
	Stage string `json:"stage"`
	// ProvedBy names what proves it: `package.TestName`, or a path to a
	// browser spec.
	ProvedBy []string `json:"provedBy"`
}

// LoadRegistry reads the registry from the repository root.
func loadRegistry(root string) (*Registry, error) {
	raw, err := os.ReadFile(filepath.Join(root, "internal", "qamatrix", "registry.json"))
	if err != nil {
		return nil, fmt.Errorf("read the registry: %w", err)
	}
	var r Registry
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse the registry: %w", err)
	}
	return &r, nil
}

// marshalRegistry writes a registry back out, for the fixture tests.
//
// The writer is here rather than in the test file so that the shape the
// fixture writes is the shape the loader reads: a fixture that used a
// different writer would be a fixture that proved the rules against a
// document the real gate never sees.
func marshalRegistry(reg *Registry) ([]byte, error) {
	out, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// finding is one problem, with the place a reader has to go.
type finding struct {
	subject string
	problem string
}

func (f finding) String() string {
	return f.subject + ": " + f.problem
}

// testFuncRe finds a test function's name in a Go test file.
var testFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]*)\s*\(`)

// tree is what the derivation found: the packages, and each package's test
// functions.
type tree struct {
	// packages is every package directory in the root module, in slash form
	// relative to the root. The root module itself is the empty string and is
	// not a package here, because the root holds no Go source.
	packages []string
	// tests maps a package to the test functions declared in its own
	// `*_test.go` files.
	tests map[string]map[string]bool
}

// has reports whether a package is in the tree.
func (t tree) has(pkg string) bool {
	for _, p := range t.packages {
		if p == pkg {
			return true
		}
	}
	return false
}

// hasTest reports whether a package declares a test function.
func (t tree) hasTest(pkg, name string) bool {
	names, ok := t.tests[pkg]
	if !ok {
		return false
	}
	return names[name]
}

// derive walks the tree and returns the packages and their test functions.
//
// The walk rule is the go tool's own, minus the toolchain, and it is the
// same rule `internal/roadmapcheck` uses, so the two agree about what a
// package is:
//
//   - a directory whose name begins with "." or "_" is ignored by the go tool
//   - a directory holding its own go.mod is a separate module, and `poc/`
//     holds one at `poc/active-monitoring`
//   - `node_modules` is a package manager's working directory, and the go
//     tool DOES pick up Go files inside it, so a count that depended on what
//     a developer installed would be a count of the machine rather than of
//     the repository
//   - a directory holding at least one `.go` file is a package
func derive(root string) (tree, error) {
	t := tree{tests: map[string]map[string]bool{}}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if name == "node_modules" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
		}
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return readErr
		}
		hasGo := false
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				hasGo = true
				break
			}
		}
		if !hasGo {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		t.packages = append(t.packages, rel)
		names, nameErr := testFunctions(path)
		if nameErr != nil {
			return nameErr
		}
		t.tests[rel] = names
		return nil
	})
	if err != nil {
		return t, err
	}
	sort.Strings(t.packages)
	return t, nil
}

// testFunctions collects the test function names declared in a directory.
func testFunctions(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, readErr
		}
		for _, m := range testFuncRe.FindAllStringSubmatch(string(raw), -1) {
			out[m[1]] = true
		}
	}
	return out, nil
}

// check is the whole gate.
//
// It returns every problem rather than the first, because the person fixing
// this needs the entire list: the categories are independent defects and a
// gate that stops at the first is a gate that takes nine runs to clear.
func check(root string) ([]finding, error) {
	reg, err := loadRegistry(root)
	if err != nil {
		return nil, err
	}
	t, err := derive(root)
	if err != nil {
		return nil, err
	}

	var out []finding

	// The registry's own category list must be the closed set.
	for _, c := range reg.Categories {
		if !contains(closedCategories, c) {
			out = append(out, finding{
				subject: "the registry's category list",
				problem: fmt.Sprintf("%q is not one of the fourteen closed categories, so a row "+
					"using it would be asking a question nobody agreed to ask", c),
			})
		}
	}
	for _, c := range closedCategories {
		if !contains(reg.Categories, c) {
			out = append(out, finding{
				subject: "the registry's category list",
				problem: fmt.Sprintf("%q is missing. The list is closed, and a registry that "+
					"quietly drops a category is a registry that quietly stops asking a question", c),
			})
		}
	}

	// The tier table and the rows must agree, in both directions.
	tierOf := map[string]string{}
	for tier, members := range reg.Tiers {
		for _, m := range members {
			if prev, ok := tierOf[m]; ok {
				out = append(out, finding{
					subject: "the tier table",
					problem: fmt.Sprintf("%s is in both tier %s and tier %s. A package in two "+
						"tiers is a package in no tier", m, prev, tier),
				})
				continue
			}
			tierOf[m] = tier
		}
	}

	rows := map[string]Package{}
	for _, p := range reg.Packages {
		if p.Package == "" {
			out = append(out, finding{"the registry's rows", "a row has no package name"})
			continue
		}
		if _, dup := rows[p.Package]; dup {
			out = append(out, finding{
				subject: "the registry's rows",
				problem: fmt.Sprintf("%s has more than one row. One row per package is what "+
					"makes the matrix a matrix", p.Package),
			})
			continue
		}
		rows[p.Package] = p
	}

	// Rule 1: a package in the tree that is not in the registry.
	for _, p := range t.packages {
		if _, ok := rows[p]; !ok {
			out = append(out, finding{
				subject: p,
				problem: "is a package in the tree and is not in the registry. A new package " +
					"cannot be added without being classified: the categories are the questions " +
					"the product has to be able to answer about itself, and a package that is not " +
					"in the matrix is a package nobody has asked",
			})
		}
	}

	for _, p := range reg.Packages {
		// Rule 2: a registry entry for a package that does not exist.
		if !t.has(p.Package) {
			out = append(out, finding{
				subject: p.Package,
				problem: "is in the registry and is not a package in the tree. It was renamed, " +
					"removed, or it never existed, and a row for a package that is not there is " +
					"a row that looks like coverage",
			})
			continue
		}

		// Rule 3: a tier assignment that disagrees with the tier table.
		declared, inTable := tierOf[p.Package]
		if !inTable {
			out = append(out, finding{
				subject: p.Package,
				problem: "is not in the tier table, so its tier cannot be checked. Every " +
					"package is in exactly one tier, and the tier is what says how much " +
					"coverage it owes",
			})
		} else if declared != p.Tier {
			out = append(out, finding{
				subject: p.Package,
				problem: fmt.Sprintf("is tier %q in its row and tier %q in the tier table. "+
					"The two are the same claim and they disagree", p.Tier, declared),
			})
		}

		// Rule 4: a category name outside the closed set.
		for name := range p.Categories {
			if !contains(closedCategories, name) {
				out = append(out, finding{
					subject: p.Package,
					problem: fmt.Sprintf("carries the category %q, which is not one of the "+
						"fourteen closed categories", name),
				})
			}
		}

		blank := 0
		for _, name := range closedCategories {
			cat, present := p.Categories[name]
			if !present {
				blank++
				continue
			}
			// A category that names tests AND carries a reason is a category that
			// is both covered and excused, which is a way of saying nothing.
			if len(cat.Tests) > 0 && strings.TrimSpace(cat.Reason) != "" {
				out = append(out, finding{
					subject: p.Package + " / " + name,
					problem: "names tests AND carries a reason. A category is either " +
						"covered or not applicable, and one that is both says nothing",
				})
			}
			// Rule 5: a named test that does not exist.
			for _, tst := range cat.Tests {
				if !t.hasTest(p.Package, tst) {
					out = append(out, finding{
						subject: p.Package + " / " + name,
						problem: fmt.Sprintf("names the test %q, and that test does not exist in "+
							"the package's own test files. A row that claims coverage nobody "+
							"can find is a claim, not a control", tst),
					})
				}
			}
			if cat.blank() {
				blank++
			}
		}

		// Rule 6: a Tier A package with a blank category.
		if p.Tier == "A" && blank > 0 {
			out = append(out, finding{
				subject: p.Package,
				problem: fmt.Sprintf("is a Tier A package and %d of its %d categories are blank. "+
					"A category is either a named test that exists or a reason saying why it "+
					"does not apply, and a blank is neither. The critical path owes an answer "+
					"for every question", blank, len(closedCategories)),
			})
		}
	}

	// Rule 8 and 9: the critical path.
	stages := map[string]Stage{}
	for _, s := range reg.CriticalPath {
		if _, dup := stages[s.Stage]; dup {
			out = append(out, finding{
				subject: "the critical path",
				problem: fmt.Sprintf("the stage %q appears twice. One stage, one set of proofs", s.Stage),
			})
			continue
		}
		stages[s.Stage] = s
	}
	for _, want := range closedStages {
		s, ok := stages[want]
		if !ok {
			out = append(out, finding{
				subject: "the critical path",
				problem: fmt.Sprintf("the stage %q is missing. The chain is closed, and a stage "+
					"that is dropped is a stage nobody is proving -- the rest of the chain still "+
					"passes", want),
			})
			continue
		}
		if len(s.ProvedBy) == 0 {
			out = append(out, finding{
				subject: "the critical path / " + want,
				problem: "names nothing that proves it",
			})
			continue
		}
		for _, proof := range s.ProvedBy {
			if err := checkProof(root, t, proof); err != nil {
				out = append(out, finding{
					subject: "the critical path / " + want,
					problem: err.Error(),
				})
			}
		}
	}
	for name := range stages {
		if !contains(closedStages, name) {
			out = append(out, finding{
				subject: "the critical path",
				problem: fmt.Sprintf("the stage %q is not one of the ten closed stages", name),
			})
		}
	}

	// The generated document must match the registry.
	generated := renderDoc(reg, t)
	docPath := filepath.Join(root, "docs", "qa-matrix.md")
	onDisk, readErr := os.ReadFile(docPath)
	if readErr != nil {
		out = append(out, finding{
			subject: "docs/qa-matrix.md",
			problem: "cannot be read, so the matrix has no human-readable half",
		})
	} else if string(onDisk) != generated {
		out = append(out, finding{
			subject: "docs/qa-matrix.md",
			problem: "does not match the registry it is generated from. Regenerate it " +
				"(QAMATRIX_WRITE_DOCS=1 go test ./internal/qamatrix/...) and commit the result",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].subject != out[j].subject {
			return out[i].subject < out[j].subject
		}
		return out[i].problem < out[j].problem
	})
	return out, nil
}

// checkProof resolves one `provedBy` entry.
//
// Two shapes are accepted: `package.TestName`, which must name a test
// function declared in that package's own test files, and a path to a FILE,
// which must exist. The second shape is for the browser specs, which prove
// things a Go test cannot reach.
//
// The discriminator is the FILE EXTENSION and not the presence of a slash:
// `internal/alpha.TestAlpha` has a slash in it and is a test, and a rule that
// read the slash as a path would call every Go proof a missing file.
func checkProof(root string, t tree, proof string) error {
	if isFileProof(proof) {
		path := filepath.Join(root, filepath.FromSlash(proof))
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("names %q, and that file does not exist. A stage that is proved "+
				"by a file that is not there is a stage that is not proved", proof)
		}
		return nil
	}
	idx := strings.LastIndex(proof, ".")
	if idx <= 0 {
		return fmt.Errorf("names %q, which is neither `package.TestName` nor a path to a file", proof)
	}
	pkg, name := proof[:idx], proof[idx+1:]
	if !t.has(pkg) {
		return fmt.Errorf("names %q, and %s is not a package in the tree", proof, pkg)
	}
	if !t.hasTest(pkg, name) {
		return fmt.Errorf("names %q, and the test %s does not exist in that package's own test "+
			"files. A stage that is proved by a test nobody can find is a stage that is not proved",
			proof, name)
	}
	return nil
}

// fileProofExtensions are the extensions that make a proof a FILE rather than
// a package-qualified test.
var fileProofExtensions = []string{
	".ts", ".tsx", ".js", ".jsx", ".mjs", ".json",
	".sh", ".ps1", ".py", ".sql", ".yaml", ".yml", ".html", ".md",
}

// isFileProof reports whether a proof names a file.
func isFileProof(proof string) bool {
	lower := strings.ToLower(proof)
	for _, ext := range fileProofExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// blanks counts the blank categories across the registry.
//
// The count is printed on every run, and it is the visible, shrinking number
// the plan asks for: a Tier B or C package may leave a category blank, and the
// blank is not a failure, but it is not invisible either.
func blanks(reg *Registry) int {
	n := 0
	for _, p := range reg.Packages {
		for _, name := range closedCategories {
			cat, ok := p.Categories[name]
			if !ok || cat.blank() {
				n++
			}
		}
	}
	return n
}

// summary renders the run's one line.
func summary(reg *Registry, t tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "packages in the tree: %d, classified: %d, blank categories: %d of %d\n",
		len(t.packages), len(reg.Packages), blanks(reg), len(reg.Packages)*len(closedCategories))
	for _, tier := range []string{"A", "B", "C"} {
		members := reg.Tiers[tier]
		if len(members) == 0 {
			continue
		}
		classified := 0
		for _, m := range members {
			if _, ok := findRow(reg, m); ok {
				classified++
			}
		}
		fmt.Fprintf(&b, "  tier %s: %d packages, %d classified\n", tier, len(members), classified)
	}
	fmt.Fprintf(&b, "  critical path: %d stages\n", len(reg.CriticalPath))
	return b.String()
}

// findRow finds a package's row.
func findRow(reg *Registry, name string) (Package, bool) {
	for _, p := range reg.Packages {
		if p.Package == name {
			return p, true
		}
	}
	return Package{}, false
}

// contains reports whether a list holds a value.
func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
