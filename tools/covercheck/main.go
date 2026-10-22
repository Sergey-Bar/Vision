// Command covercheck compares a `go test -coverprofile` against the committed
// per-package floors, and fails when a package falls below its floor or rises far
// enough above it that the floor is out of date.
//
// The profile is the one the `test` gate already produced. Running the suite a
// second time to get coverage would double the longest gate in the script, and a
// gate that is expensive is a gate that gets run with the flag that makes it cheap.
//
// Usage:
//
//	go test -coverprofile=cov.out ./...
//	go run ./tools/covercheck -profile cov.out
//	go run ./tools/covercheck -profile cov.out -write-baseline
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/covercheck"
)

func main() {
	os.Exit(run())
}

func run() int {
	var profile, baseline, goMod string
	var write bool
	var build string
	var force bool
	flag.StringVar(&profile, "profile", "", "Path to a `go test -coverprofile` file. Required.")
	flag.StringVar(&baseline, "baseline", "internal/covercheck/floors.json", "Path to the committed floors.")
	flag.StringVar(&goMod, "go-mod", "go.mod", "Path to go.mod, whose module line the profile is prefixed with.")
	flag.StringVar(&build, "build", covercheck.DefaultBuild,
		"Build mode the suite ran under. The floors hold for the modes named in the baseline's "+
			"`build` list; a run under any other one is reported as unmeasured rather than compared.")
	flag.BoolVar(&write, "write-baseline", false, "Write the baseline from this run instead of checking against it.")
	flag.BoolVar(&force, "force", false, "With -write-baseline, overwrite even if this run measured only part of the tree.")
	flag.Parse()

	if profile == "" {
		fmt.Fprintln(os.Stderr, "covercheck: -profile is required")
		return 2
	}

	module, err := readModulePath(goMod)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
		return 1
	}

	f, err := os.Open(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covercheck: open %s: %v\n", profile, err)
		return 1
	}
	defer func() { _ = f.Close() }()

	pkgs, err := covercheck.Parse(f, module+"/")
	if err != nil {
		fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
		return 1
	}
	if len(pkgs) == 0 {
		fmt.Fprintln(os.Stderr, "covercheck: the profile measured nothing. This is not a pass: the "+
			"file this gate was pointed at contains no blocks, so every package would meet a floor of zero")
		return 1
	}

	if write {
		// The file's own excluded list is carried over, so writing the
		// baseline does not quietly floor the packages the file had
		// deliberately left out.
		excluded := []string(nil)
		if existing, err := covercheck.LoadBaseline(baseline); err == nil {
			excluded = existing.Excluded
		}
		b := covercheck.Derive(pkgs, module, "atomic", covercheck.DefaultMargin, time.Now().Format("2006-01-02"), build, excluded)
		if err := refusePartialBaseline(baseline, b, force); err != nil {
			fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
			return 1
		}
		if err := b.Save(baseline); err != nil {
			fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
			return 1
		}
		fmt.Print(covercheck.Report(pkgs, b))
		fmt.Printf("covercheck: wrote %d floors to %s from the run just measured.\n", len(b.Floors), baseline)
		fmt.Println("Read the table before committing it. A floor below what the package needs is worse " +
			"than no floor, because it looks like coverage.")
		return 0
	}

	b, err := covercheck.LoadBaseline(baseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
		return 1
	}

	fmt.Print(covercheck.Report(pkgs, b))
	findings := covercheck.Check(pkgs, b, build)
	// The unmeasured case is neither a pass nor a defect, and it must not be a
	// failure.
	//
	// `-NoRace` is a documented command and a default run on a machine with no C
	// compiler is `-NoRace` in all but name. The first version returned 1 for it, so
	// the gate reported "FAILED" on a tree nobody had changed — which is the exact
	// outcome the build-mode field exists to prevent, reintroduced through the exit
	// code. It prints, loudly and specifically, and it does not fail the run: a
	// floor from a `-race` measurement is not evidence about this run either way.
	measured := findings[:0]
	for _, f := range findings {
		if f.Rule == covercheck.RuleBuildNotMeasured {
			fmt.Printf("    %s\n", f.Detail)
			continue
		}
		measured = append(measured, f)
	}
	if len(measured) > 0 {
		for _, f := range measured {
			fmt.Fprintf(os.Stderr, "    %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "covercheck: FAILED with %d finding(s).\n", len(measured))
		return 1
	}
	fmt.Println("covercheck: ok. Every package is at or above its floor, and every floor is current.")
	return 0
}

// refusePartialBaseline refuses to replace a committed baseline with one derived
// from a partial run.
//
// The failure it prevents is quiet and total. `go test -coverprofile=coverage
// ./internal/migrate/` measures one package, and writing that as the baseline
// replaces forty-three floors with one — so the next full run reports every other
// package as having no recorded floor, and the fix is to re-measure by hand. It
// happened while writing this gate.
//
// The rule is deliberately not "the new baseline must contain every old floor":
// packages are deleted, and a rename would otherwise need `-force` forever. It is
// "most of them must survive", which catches a partial run and lets a deletion
// through.
func refusePartialBaseline(path string, next covercheck.Baseline, force bool) error {
	if force {
		return nil
	}
	existing, err := covercheck.LoadBaseline(path)
	if err != nil {
		// No baseline to lose, or one that cannot be parsed. Writing over an
		// unparseable file is the one case where doing nothing is wrong: the file is
		// not a baseline, so it is not a set of floors to protect, and refusing
		// would leave a broken file that also blocks the fix.
		if os.IsNotExist(errors.Unwrap(err)) {
			return nil
		}
		return fmt.Errorf("the committed baseline could not be read, so this run cannot tell whether it "+
			"is measuring the whole tree: %w", err)
	}
	if len(existing.Floors) == 0 {
		return nil
	}
	kept := 0
	for name := range next.Floors {
		if _, ok := existing.Floors[name]; ok {
			kept++
		}
	}
	if kept*2 >= len(existing.Floors) {
		return nil
	}
	return fmt.Errorf("refusing to write %d floors over an existing %d: the run measured only part "+
		"of the tree. `go test -coverprofile` over `./internal/one/` and writing that as the "+
		"baseline is how forty-three floors disappear without an error. Re-measure the whole tree, "+
		"or pass -force if the packages really were deleted",
		len(next.Floors), len(existing.Floors))
}

func readModulePath(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok {
			continue
		}
		name := strings.Trim(strings.TrimSpace(rest), `"`)
		if name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("%s has no module directive, so the profile's paths cannot be shortened. "+
		"This is not a pass: every package would be measured under a name the baseline does not hold, "+
		"and the gate would report them all as unknown", path)
}
