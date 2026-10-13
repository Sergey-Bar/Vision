// Package roadmapcheck holds the rules about the roadmap documents agreeing with
// the tree they describe.
//
// # Why this exists
//
// The roadmap states build state in prose. `ROADMAP.md` said "30 Go packages,
// twelve forward-only migrations", `docs/roadmap/master-roadmap.md` said "24 Go
// packages, twelve forward-only migrations", and `internal/README.md` told a
// reader they were about to open "thirty packages". The tree had long since
// moved past every one of those numbers: it declares eighty-three packages and
// twenty-five migrations, and the migration count alone had been wrong for
// thirteen migrations. All three documents were stale at once, and nothing
// noticed, because no claim in them was derived from anything.
//
// `internal/statuscheck` does this for `docs/status.md`, and its own history is
// the argument for deriving rather than correcting by hand: trap 14 in
// `internal/README.md` records that the trap list was itself stale while it
// described the staleness — it said the tree had thirty packages "and there are
// forty-two" at a time when the tree had neither. A number corrected once and
// left alone documents a tree that no longer exists. So the counts in this
// package are derived from the directory that defines them, and a roadmap
// document that prints a different number is a failing test.
//
// # What it checks
//
// Three claims, each anchored to the exact phrase the documents use, in
// `ROADMAP.md` and `docs/roadmap/master-roadmap.md`:
//
//   - "N Go packages" — the package count of the root module, derived by
//     walking the tree.
//   - "N forward-only migrations" — the distinct migration numbers in
//     `migrations/`.
//   - "N contracts" — the published protobuf contracts under
//     `contracts/protobuf`, which is what the phrase has always counted.
//
// Plus the package-map sentence in `internal/README.md` ("to open N packages"),
// because that file exists to tell a reader how big the tree is and a reader who
// is told thirty has been lied to.
//
// The claims are read from the whole document rather than from a marked block.
// `internal/statuscheck` scopes itself to `docs/status.md`'s Verification
// paragraph because that document records dated history alongside current state
// and a checker that reported history would punish an author for writing down
// why something changed. The roadmap documents carry no such history of these
// counts — the phrases above appear exactly once each, as the current state —
// so the whole file is the scope, and any future hit is reported with its file
// and line for a human to judge.
//
// # What it deliberately does not check
//
// The gate count. `internal/statuscheck` already derives it from the distinct
// gate names in `scripts/verify.ps1` and holds `docs/status.md` to it, and the
// roadmap documents say in as many words that they do not restate build state
// (`docs/roadmap/master-roadmap.md`: "docs/status.md is the authoritative build
// state and this roadmap does not restate it"). A second derivation of the same
// number would be a second thing to update whenever a gate is added, covering a
// sentence that does not exist.
package roadmapcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Derived is everything this package can count without a human maintaining the
// number. Adding a field here is adding a claim the gate will hold the
// documents to.
type Derived struct {
	// Packages is the number of Go packages in the root module.
	Packages int
	// Migrations is the number of distinct migration numbers in migrations/.
	Migrations int
	// Contracts is the number of published protobuf contracts.
	Contracts int
}

// Derive reads the tree and returns the counts.
//
// Unexported because the only caller is this package's own test and Check: the
// gate is run by `go test ./internal/roadmapcheck/`, and `internal/deadexport`
// holds every export to a reader outside its package. The `statuscheck`
// precedent keeps its derivation exported because `securityctl status` is
// supposed to print it; no such caller exists here, and exporting one for a
// caller nobody has written is the shape of the debt the gate exists to name.
func derive(root string) (Derived, error) {
	var out Derived
	var err error

	if out.Packages, err = countPackages(root); err != nil {
		return out, err
	}
	if out.Migrations, err = countMigrations(root); err != nil {
		return out, err
	}
	if out.Contracts, err = countContracts(root); err != nil {
		return out, err
	}
	return out, nil
}

// countPackages counts the directories that hold Go code in the root module.
//
// The rule is the go tool's own, minus the toolchain: a directory is a package
// when it holds at least one .go file, and three kinds of directory are skipped
// because the go tool skips them too.
//
//   - A directory whose name begins with "." or "_" is ignored by the go tool,
//     and `.kilo/` holds an Agent Manager worktree that is a full second copy of
//     this repository. Counting it would double every number in this package.
//   - A directory holding its own go.mod is a separate module. `poc/` is inside
//     the root module, but `poc/active-monitoring/` is not — it has its own
//     go.mod and is excluded from `./...` at the root, so it is excluded here.
//   - The root itself holds the module's own go.mod and is never skipped.
//
// The walk is what makes the count derive: adding a package and forgetting the
// documents is a failing test rather than a claim that reads as checked.
func countPackages(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			if name := d.Name(); strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return fs.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return fs.SkipDir
			}
		}
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return readErr
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				count++
				break
			}
		}
		return nil
	})
	return count, err
}

// migrationRe matches the four-digit prefix the migrator orders by. A migration
// is its number, so the count is the number of distinct numbers, not of files:
// an expand/contract pair is one migration.
var migrationRe = regexp.MustCompile(`^(\d+)_.*\.sql$`)

func countMigrations(root string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if m := migrationRe.FindStringSubmatch(e.Name()); m != nil {
			seen[m[1]] = true
		}
	}
	return len(seen), nil
}

// countContracts counts the published protobuf contracts.
//
// "N contracts" in the roadmap documents has always meant the .proto files:
// they are the wire, `contractcheck` diffs them against the baseline, and the
// number has read thirteen since the first protobuf landed. The JSON Schemas
// and the OpenAPI document are counted by their own claims in `docs/status.md`
// and are not folded in here.
func countContracts(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(filepath.Join(root, "contracts", "protobuf"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".proto") {
			count++
		}
		return nil
	})
	return count, err
}

// ---------------------------------------------------------------------------
// The claims.
// ---------------------------------------------------------------------------

// Finding is one place where a roadmap document and the tree disagree.
type Finding struct {
	// Label names the claim in the document's own words, so the message reads
	// as a correction rather than as an identifier nobody recognises.
	Label string
	// Want is the derived count.
	Want int
	// Got is the number the document printed.
	Got int
	// File is the document, relative to the repository root, which is where a
	// reader goes to fix it.
	File string
	// Line is the 1-based line in that document.
	Line int
}

func (f Finding) String() string {
	return f.File + ":" + strconv.Itoa(f.Line) + ": " + f.Label +
		" is " + strconv.Itoa(f.Got) + ", the tree says " + strconv.Itoa(f.Want) +
		". The milestone plan is where scope is locked, so a number in it that " +
		"the tree contradicts is a defect in the document, not a rounding " +
		"difference to note and move past"
}

// claim pairs a label with the pattern that finds the number the document
// printed next to it.
type claim struct {
	label string
	re    *regexp.Regexp
	want  int
}

var number = `(\d+)`

// claimsFor returns the claims read from one document, in the order they are
// reported.
func claimsFor(d Derived, file string) []claim {
	switch file {
	case "ROADMAP.md", "docs/roadmap/master-roadmap.md":
		return []claim{
			{
				label: "Go package count",
				// `\s+` rather than a literal space, because the documents wrap
				// mid-phrase: `ROADMAP.md` prints "82 Go" at the end of one line
				// and "packages, ..." at the start of the next.
				re:   regexp.MustCompile(number + `\s+Go\s+packages\b`),
				want: d.Packages,
			},
			{
				label: "migration count",
				re:    regexp.MustCompile(number + `\s+forward-only\s+migrations\b`),
				want:  d.Migrations,
			},
			{
				label: "contract count",
				re:    regexp.MustCompile(number + `\s+contracts\b`),
				want:  d.Contracts,
			},
		}
	case "internal/README.md":
		return []claim{
			{
				label: "Go package count",
				// Anchored to the sentence that makes the claim. "N packages"
				// alone would also match the file's history — trap 14 records
				// that the tree "had thirty leaf packages" once, which is an
				// accurate statement about a past tree and must stay.
				re:   regexp.MustCompile(`to open ` + number + `\s+packages\b`),
				want: d.Packages,
			},
		}
	}
	return nil
}

// targets is every document this package holds to the tree.
var targets = []string{
	"ROADMAP.md",
	"docs/roadmap/master-roadmap.md",
	"internal/README.md",
}

// Check returns every disagreement between the roadmap documents and the tree.
//
// A claim with no match is not a finding. A document is free to stop mentioning
// a count entirely, and demanding one would force prose onto a document whose
// author was deliberately saying less. What it refuses is a number that is
// present and wrong, because that is the case that reads as checked.
func Check(root string) ([]Finding, error) {
	d, err := derive(root)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, file := range targets {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		text := string(raw)
		for _, c := range claimsFor(d, file) {
			for _, m := range c.re.FindAllStringSubmatchIndex(text, -1) {
				if len(m) < 4 {
					continue
				}
				got, err := strconv.Atoi(text[m[2]:m[3]])
				if err != nil {
					continue
				}
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
	}
	return out, nil
}

// lineOf returns the 1-based line holding a byte offset.
func lineOf(s string, offset int) int {
	if offset > len(s) {
		offset = len(s)
	}
	return 1 + strings.Count(s[:offset], "\n")
}

// Sorted returns the findings ordered by document and line, so a run prints the
// same list every time and a reader fixing them works down the page.
//
// Unexported for the reason Derive is: its readers are the test and the gate's
// own reporting path. Sorting is presentation, and presentation that no caller
// outside the package asks for is not an interface.
func sorted(findings []Finding) []Finding {
	out := append([]Finding(nil), findings...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}
