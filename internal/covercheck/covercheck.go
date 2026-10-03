// Package covercheck is the gate for a coverage floor that cannot go down.
//
// # Why this exists
//
// A coverage number nobody compares to anything is a number in a transcript. This
// repository has one, printed by `scripts/verify.ps1`, and nothing failed when a
// package's tests were deleted outright — because the number went from 56% to 55%
// and 55% is not a failure.
//
// So: a per-package floor, committed, checked on every run, and — the part that
// makes it a ratchet rather than a threshold — a package that rises *above* its
// floor by more than the margin fails too and asks for the floor to be raised. A
// floor that only fails downwards gets lowered once, by whoever is having a bad
// afternoon, and then everything above it is free.
//
// # What "coverage" means here
//
// Statement coverage, from `go test -coverprofile`, aggregated per package
// directory. Not branch coverage, because `go test` does not produce it and a
// metric this repository cannot measure with a flag is a metric it will report
// without measuring.
//
// The profile is produced by the `test` gate itself rather than by a second full
// run, because a coverage run is the same run with `-coverprofile` added and
// paying for it twice would be the fastest way to get this gate switched off.
//
// # The baseline
//
// `floors.json`, written by `-write-baseline` from a real run and committed. It
// carries the measured value and the date it was measured, because a floor with no
// date is a floor nobody can tell is stale.
//
// # The two directions
//
//   - **Below the floor**: a test was deleted, or a branch was added without one.
//     This is the gate doing its job.
//   - **Above the floor plus the margin**: the floor is out of date. The finding
//     says so, and names the new number, so raising it is a one-line edit rather
//     than a measurement somebody has to redo. The margin is small because the
//     point is to catch a forgotten test, not to nag about a refactor.
//
// # What is deliberately not here
//
// There is no floor for `cmd/`, `migrations/` or `tools/`. `migrations` is SQL with
// a Python harness beside it and no Go statements, and a floor of zero for a
// directory with no code is a floor that teaches a reader to ignore the file.
package covercheck

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Package is one package directory's measured coverage.
type Package struct {
	// Name is the directory relative to the module root, e.g. `internal/rules`.
	Name string
	// Statements is how many statements the package's non-test files have.
	Statements int
	// Covered is how many of them at least one test executed.
	Covered int
}

// Percent is statement coverage as a fraction of one hundred, not of one: every
// other tool prints a percentage and a floor that is a percentage is a floor
// somebody has to remember to divide.
func (p Package) Percent() float64 {
	if p.Statements == 0 {
		return 0
	}
	return float64(p.Covered) * 100 / float64(p.Statements)
}

// Floor is one package's recorded minimum, with the date it was measured.
type Floor struct {
	// Percent is the recorded minimum.
	Percent float64 `json:"percent"`
	// Measured is when the number was taken, YYYY-MM-DD.
	Measured string `json:"measured"`
}

// Baseline is the committed file, and the reason the whole thing exists.
type Baseline struct {
	Note    []string `json:"note"`
	Package string   `json:"package"`
	Mode    string   `json:"mode"`
	// Margin is how far above a floor a package may rise before the gate asks for
	// the floor to be raised.
	Margin float64          `json:"margin"`
	Floors map[string]Floor `json:"floors"`
	// Excluded names the path prefixes deliberately not floored.
	//
	// It is in the file rather than in the checker because the two halves have to
	// agree and only one of them wrote the intent. The first version derived
	// `internal/`-only inside `Derive` and checked every package inside `Check`, so
	// the run that wrote the baseline reported eight packages as unknown on the very
	// profile it had just written — `cmd/securityctl` at 36% and four `tools/`
	// commands at 0%. A rule one half of a tool knows and the other does not is two
	// rules.
	Excluded []string `json:"excluded"`
	// Build holds the build modes these floors were measured under.
	//
	// It is here because coverage is not a property of the source. The same tree,
	// the same tests, no code change, measured twice:
	//
	//	internal/migrate   78.6% without -race    71.4% with -race
	//
	// The race detector changes inlining and the race runtime's own paths, so the
	// set of executed blocks is not the same set. A floor derived from one mode is
	// therefore not a floor for the other, and a gate that fires spuriously in both
	// directions is a gate that gets switched off — which is exactly what this gate
	// did, on consecutive runs, for a package nobody had touched.
	//
	// When the run's mode is not listed, the gate says so and checks nothing. That
	// is the honest outcome: it is reporting that it cannot measure, not that
	// everything passed.
	Build []string `json:"build"`
}

// isExcluded reports whether a package is deliberately outside the gate.
func (b Baseline) isExcluded(name string) bool {
	for _, prefix := range b.Excluded {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// DefaultBuild is the build mode `scripts/verify.ps1` runs the suite under, and so
// the mode a floor has to be measured in to mean anything at the gate.
const DefaultBuild = "race"

// isBuildListed reports whether the floors in this file hold for the run's mode.
func (b Baseline) isBuildListed(build string) bool {
	if len(b.Build) == 0 {
		// A file written before this field existed makes no claim about modes, and
		// guessing one is how the gate compares numbers measured two ways.
		return build == DefaultBuild
	}
	for _, m := range b.Build {
		if m == build {
			return true
		}
	}
	return false
}

// DefaultMargin is the default rise tolerance, in percentage points.
//
// Two points: enough that a refactor which happens to cover one more line does not
// fail the build, small enough that a forgotten test — which is usually tens of
// lines — is caught.
const DefaultMargin = 2.0

// Finding is one thing the gate is unhappy about.
type Finding struct {
	// Package is the directory the finding is about.
	Package string
	// Rule names the direction: `below-floor` or `stale-floor`.
	Rule string
	// Detail says what happened and what to do about it.
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: %s", f.Package, f.Rule, f.Detail)
}

// Rules, exported so a caller can switch on them.
const (
	RuleBelowFloor = "coverage is below the committed floor"
	RuleStaleFloor = "coverage is above the committed floor plus the margin"
	RuleUnknown    = "the package has no recorded floor"
	RuleUnlisted   = "the package has a floor but no coverage in the profile"
	// RuleBuildNotMeasured is the gate declining to compare numbers taken under a
	// different build mode.
	RuleBuildNotMeasured = "the floors were measured under a different build mode"
)

// Parse reads a `go test -coverprofile` stream and aggregates it per package.
//
// The profile's first line is `mode: set|atomic|count`; the rest are
// `file.go:line.col,line.col numStatements count`, one per basic block. The
// `file.go` part is a module-relative path with forward slashes on every platform,
// which is one of the reasons this is not a shell one-liner.
func Parse(r io.Reader, modulePrefix string) ([]Package, error) {
	sc := bufio.NewScanner(r)
	// A profile line is short, but a generated file's blocks are numerous and a
	// scanner failure here reads as "no coverage", which is a floor of zero
	// reported as a measurement.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	agg := map[string]*Package{}
	// generated is the set of profile paths whose file carries the generator's
	// marker. Read from the tree rather than guessed from the extension, for the
	// same reason `internal/deadexport` reads it: a file that starts being
	// hand-edited stops carrying the marker, and excluding it by name would then
	// be a hole.
	generated := generatedFiles(modulePrefix)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			if strings.HasPrefix(line, "mode:") {
				continue
			}
		}
		// file.go:line.col,line.col numStatements count
		//
		// Split from the right, on fields rather than on a space: a profile line
		// whose path contained a space would break a last-index split, and the
		// failure of that is "malformed coverage line" on a line that is fine.
		parts := strings.Fields(line)
		if len(parts) < 3 {
			return nil, fmt.Errorf("malformed coverage line %q", line)
		}
		file := strings.Join(parts[:len(parts)-2], " ")
		// The path is `file.go:line.col,line.col`, and the position is part of
		// the field. Leaving it on means `HasSuffix(rel, ".pb.go")` is never true
		// and the generated-file exclusion silently measures generated code — which
		// is how `internal/contracts/securityv1` got a floor of 0% for a package
		// whose every statement is protoc output.
		file = stripPosition(file)
		stmts, err := strconv.Atoi(parts[len(parts)-2])
		if err != nil {
			return nil, fmt.Errorf("malformed statement count in %q: %w", line, err)
		}
		count, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			return nil, fmt.Errorf("malformed execution count in %q: %w", line, err)
		}

		rel := strings.TrimPrefix(file, modulePrefix)
		dir := path.Dir(rel)
		if strings.HasSuffix(dir, "_test") {
			// An external test package's own file. Its statements are the test's,
			// not the package's, and counting them would let a package meet its
			// floor by testing itself in a separate directory.
			continue
		}
		if strings.HasSuffix(rel, ".pb.go") || generated[file] {
			// Generated code is not held to a coverage floor. `securityv1` is a few
			// hundred protoc-emitted accessors, and a floor for it is either zero —
			// which teaches a reader to ignore this file — or a number that has to
			// be raised every time a field is added to a proto.
			continue
		}
		p, ok := agg[dir]
		if !ok {
			p = &Package{Name: dir}
			agg[dir] = p
		}
		p.Statements += stmts
		if count > 0 {
			p.Covered += stmts
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the coverage profile: %w", err)
	}

	out := make([]Package, 0, len(agg))
	for _, p := range agg {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Check compares the measured packages against the baseline and returns what it
// found, in both directions.
//
// `build` is the mode the suite was run under. See `Baseline.Build` for why it is
// carried rather than ignored.
func Check(pkgs []Package, b Baseline, build string) []Finding {
	var out []Finding

	if !b.isBuildListed(build) {
		// Reported, not silently empty: "the gate could not run" and "the gate ran
		// and found nothing" must not look the same on the console.
		out = append(out, Finding{
			Package: "(baseline)", Rule: RuleBuildNotMeasured,
			Detail: fmt.Sprintf("these floors were measured under a %s build and this run was %s, "+
				"so nothing was checked. Coverage is not a property of the source: internal/migrate "+
				"measures 78.6%% without -race and 71.4%% with it, on the same tree. Re-measure with "+
				"`-write-baseline` under the mode the gate runs",
				strings.Join(b.Build, ","), build),
		})
		return out
	}

	seen := map[string]bool{}
	for _, p := range pkgs {
		seen[p.Name] = true
		if b.isExcluded(p.Name) {
			continue
		}
		f, ok := b.Floors[p.Name]
		if !ok {
			out = append(out, Finding{
				Package: p.Name, Rule: RuleUnknown,
				Detail: fmt.Sprintf("no floor is recorded and the package has %d statements at %.1f%%. "+
					"Run `go run ./tools/covercheck -write-baseline` to record it, or add its path to "+
					"the file's `excluded` list if it is not supposed to be covered",
					p.Statements, p.Percent()),
			})
			continue
		}
		// Both sides are rounded to one decimal before they are compared, because
		// `Derive` writes the rounded value and an unrounded 34.24 is below a
		// recorded 34.2. The gate reported a package as below its own floor on the
		// run that wrote the floor, which is the kind of thing that gets the
		// comparison loosened instead of the comparison fixed.
		got := round1(p.Percent())
		if got < f.Percent-1e-9 {
			out = append(out, Finding{
				Package: p.Name, Rule: RuleBelowFloor,
				Detail: fmt.Sprintf("%.1f%% against a floor of %.1f%% recorded on %s. Either a test was "+
					"deleted or a branch was added without one; both are worth knowing about",
					got, f.Percent, f.Measured),
			})
			continue
		}
		if got > f.Percent+b.Margin+1e-9 {
			out = append(out, Finding{
				Package: p.Name, Rule: RuleStaleFloor,
				Detail: fmt.Sprintf("%.1f%% against a floor of %.1f%% recorded on %s, and the margin is "+
					"%.1f. Raise the floor to %.1f: a floor that only fails downwards gets lowered once "+
					"and everything above it is free",
					got, f.Percent, f.Measured, b.Margin, got),
			})
		}
	}

	for name := range b.Floors {
		if seen[name] {
			continue
		}
		out = append(out, Finding{
			Package: name, Rule: RuleUnlisted,
			Detail: "a floor is recorded for this package and the profile contains none of it. It was " +
				"deleted, renamed, or the run did not include it — and a floor for a package that no " +
				"longer exists is an entry nobody is maintaining",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// Derive builds a baseline from a measured run, for `-write-baseline`.
//
// Written from a measurement rather than rounded by hand, because the whole point
// of the file is that nobody edits a number in it.
func Derive(pkgs []Package, module, mode string, margin float64, measured, build string) Baseline {
	b := Baseline{
		Package:  module,
		Mode:     mode,
		Margin:   margin,
		Build:    []string{build},
		Floors:   map[string]Floor{},
		Excluded: []string{"cmd/", "tools/", "migrations/"},
		Note: []string{
			"The per-package coverage floors. Written by `tools/covercheck -write-baseline` from a " +
				"real run, and read by the same tool as a gate.",
			"",
			"These are floors, not targets. A package below its floor fails the build. A package above " +
				"its floor plus the margin also fails, and asks for the floor to be raised — a floor " +
				"that only fails downwards is a floor somebody lowers once and everything above it is " +
				"free afterwards.",
			"",
			"They are statement coverage from `go test -coverprofile`, not branch coverage: `go test` " +
				"does not produce branch coverage, and a metric this repository cannot measure with a " +
				"flag is a metric it reports without measuring.",
			"",
			"The floors hold for the build modes in `build`. Coverage is not a property of the source: " +
				"internal/migrate measures 78.6% without -race and 71.4% with it on an unchanged tree, so a " +
				"floor from one mode is not a floor for the other, and `check` reports that it could not " +
				"measure rather than comparing numbers that do not belong together.",
			"",
			"`cmd/`, `migrations/` and `tools/` are named in `excluded` and are not floored. `migrations` " +
				"is SQL with a Python harness beside it and no Go statements, and a floor of zero for a " +
				"directory with no code teaches a reader to ignore this file.",
		},
	}
	for _, p := range pkgs {
		if p.Statements == 0 {
			continue
		}
		if b.isExcluded(p.Name) {
			continue
		}
		b.Floors[p.Name] = Floor{Percent: round1(p.Percent()), Measured: measured}
	}
	return b
}

// LoadBaseline reads a baseline file.
func LoadBaseline(path string) (Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("read the coverage baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return Baseline{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if b.Margin == 0 {
		b.Margin = DefaultMargin
	}
	return b, nil
}

// Save writes a baseline, and refuses to write a file with no floors in it.
//
// A gate that overwrites its own baseline with an empty map on a run where the
// profile failed to parse has just deleted the control and reported success.
func (b Baseline) Save(path string) error {
	if len(b.Floors) == 0 {
		return fmt.Errorf("refusing to write a baseline with no floors: the profile that produced it " +
			"was empty, and a baseline that says nothing is checked is worse than none")
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the baseline: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Report renders the measured table, so the run shows the numbers and not only the
// verdict. A floor nobody can see the measured value of is a floor nobody can tell
// is close to failing.
func Report(pkgs []Package, b Baseline) string {
	var s strings.Builder
	fmt.Fprintf(&s, "    %d package(s) measured against %d recorded floor(s), margin %.1f\n",
		len(pkgs), len(b.Floors), b.Margin)
	for _, p := range pkgs {
		if b.isExcluded(p.Name) {
			fmt.Fprintf(&s, "  -- %-34s %6.1f%%   (excluded by the baseline's list)\n",
				p.Name, p.Percent())
			continue
		}
		f, ok := b.Floors[p.Name]
		if !ok {
			fmt.Fprintf(&s, "      %-34s %6.1f%%   (no floor)\n", p.Name, p.Percent())
			continue
		}
		got := round1(p.Percent())
		mark := "  "
		switch {
		case got < f.Percent-1e-9:
			mark = "!!"
		case got > f.Percent+b.Margin+1e-9:
			mark = "^^"
		}
		fmt.Fprintf(&s, "    %s %-34s %6.1f%%   floor %.1f%% (%s)\n",
			mark, p.Name, got, f.Percent, f.Measured)
	}
	return s.String()
}

// stripPosition removes a trailing `:line.col,line.col` from a profile path.
//
// A digit after the colon is what makes it a position rather than part of a name:
// `internal/rules/engine.go` has no colon, and a Windows drive letter is followed
// by a backslash, so neither is mistaken for one.
func stripPosition(file string) string {
	i := strings.LastIndexByte(file, ':')
	if i <= 0 || i+1 >= len(file) {
		return file
	}
	if file[i+1] < '0' || file[i+1] > '9' {
		return file
	}
	return file[:i]
}

// generatedFiles finds the profile paths whose file carries the generator's
// marker.
//
// It walks the working directory — which is the module root, because that is where
// `go test` ran — and returns module-prefixed paths, because that is what the
// profile's own entries look like. The first version walked the module path as if
// it were a directory, found nothing, and floored `securityv1` at 0%: a floor of
// zero for a package whose entire contents are protoc output, which teaches a
// reader to ignore the file.
func generatedFiles(modulePrefix string) map[string]bool {
	out := map[string]bool{}
	if modulePrefix == "" {
		return out
	}
	walk := func(p string, d fs.DirEntry, err error) error {
		// A path this gate cannot read is skipped rather than fatal. The walk is an
		// *exclusion*: if it fails, generated code is measured and something gets a
		// floor of 0% for protoc output. That is a wrong number in a file somebody
		// will trust, and it is worse than an exclusion that did not apply — which is
		// why this returns nil rather than the error, and why the measured result is
		// printed for every package so a 0% can be seen.
		if err != nil {
			return nil //nolint:nilerr // an exclusion that cannot be applied must not fail the run
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".kilo", "node_modules", "__pycache__", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil //nolint:nilerr // same reason as above, on a leaf
		}
		if hasGeneratedMarker(src) {
			out[strings.TrimSuffix(modulePrefix, "/")+"/"+filepath.ToSlash(p)] = true
		}
		return nil
	}
	_ = filepath.WalkDir(".", walk)
	return out
}

func hasGeneratedMarker(src []byte) bool {
	head := src
	if len(head) > 4096 {
		head = head[:4096]
	}
	for _, line := range strings.Split(string(head), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		if c, ok := strings.CutPrefix(trimmed, "//"); ok {
			c = strings.TrimSpace(c)
			if !strings.HasPrefix(c, "Code generated by") {
				continue
			}
			return strings.Contains(c, "DO NOT EDIT.")
		}
	}
	return false
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
