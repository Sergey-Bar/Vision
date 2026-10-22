// Package mutation holds the configuration of the mutation gate, and the record
// of what is known to survive it.
//
// # What it measures, and what it does not
//
// Coverage answers one question: was the code executed? Mutation testing
// answers a different one: if the code were WRONG, would anything notice?
//
// The two are not the same question, and the gap between them is where a
// security product's tests usually fail. A test that calls a function and
// asserts that it did not panic is coverage: the line ran, and the assertion
// says nothing about what the function computed. A mutation turns `if
// tenant != other` into `if tenant == other`, and the test that only checked
// "it did not panic" sails past.
//
// So the gate runs `go-mutesting` over the packages on the critical path and
// asks the question the coverage number cannot: of the code the tests
// EXECUTE, how many mutants do the assertions kill?
//
// # Why the scope is the critical path
//
// A mutant that survives in `internal/api` is a defect in the product's
// behaviour. A mutant that survives in a script nobody runs is a curiosity.
// The scope is the twenty-five packages the QA matrix calls Tier A, and the
// reason is in `docs/qa-matrix.md`: those are the packages the product's
// spine runs through.
//
// # Why the score that is checked is the COVERED one
//
// The overall mutation score counts every mutant in the package, including the
// ones in code no test reaches. Those are coverage gaps wearing a different
// hat, and the coverage floor already measures them. Counting them twice
// would make one number answer two questions, and a number that answers two
// questions answers neither.
//
// The covered-code score is the one that answers the question: of the code the
// tests run, what share of the changes to it do the assertions catch?
//
// # The floor is 100, and that is a claim
//
// A floor of 100 says: every mutation in the code the critical path's tests
// execute is caught by an assertion. It was MEASURED, not guessed, and the
// measurement is in `docs/status.md`.
//
// A floor that somebody can lower by hand is a floor that is not a floor, so
// the floor is pinned here: `mutation.json` states it, and this package's test
// asserts it is the value that was measured. Lowering it is a deliberate act
// with a red test, which is what makes it a decision rather than an accident.
//
// # The sandbox
//
// `go-mutesting` mutates the source in place. Running it against the working
// tree would leave a mutated file behind if the run was interrupted, and a
// mutation left applied in the tree is a landmine for every later gate --
// which is what `internal/treestate`'s second assertion exists to catch.
//
// So the gate COPIES the tree into a temporary directory, mutates the copy,
// and throws the copy away. The copy is what makes the gate safe to
// interrupt, and the exclusion list in `mutation.json` is what keeps the copy
// to a second: the Python virtualenv alone is fifty-six thousand files.
package mutation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the gate's configuration, as `mutation.json` states it.
type Config struct {
	// Note is the configuration's own preamble, carried so the file and this
	// package cannot say different things about what the gate is for.
	Note []string `json:"note"`
	// Packages are the package patterns under mutation.
	Packages []string `json:"packages"`
	// MinCoveredMSI is the floor on the mutation score over the code the tests
	// execute. Zero means "no floor", and the gate refuses a configuration
	// that says zero, because a floor of zero is not a floor.
	MinCoveredMSI float64 `json:"minCoveredMSI"`
	// MinMSI is the floor on the overall score. It is zero on purpose: the
	// overall score counts the code no test reaches, and the coverage floor
	// already measures that. A floor here would be one number answering two
	// questions.
	MinMSI float64 `json:"minMSI"`
	// Workers is how many mutants are tried at once.
	Workers int `json:"workers"`
	// TimeoutCoefficient scales the per-mutation test timeout by the clean
	// run's duration. Zero means the tool's own default, which is ten
	// seconds -- and ten seconds is shorter than the slowest package's own
	// suite takes, so the gate would report a timeout as a coverage failure.
	TimeoutCoefficient int `json:"timeoutCoefficient"`
	// Baseline is the file naming the known survivors.
	Baseline string `json:"baseline"`
	// Excluded is the list of directories the sandbox does not copy.
	Excluded []string `json:"excluded"`
}

// Survivor is one known-surviving mutant.
//
// The `Why` is not optional. A survivor with no reason is a hole with a
// bookmark in it: it is recorded, so it is out of the way, and it is still a
// hole.
type Survivor struct {
	// File is the mutated file, relative to the repository root.
	File string `json:"file"`
	// Line is the line the mutation was applied to.
	Line int `json:"line"`
	// Mutator is the mutation that survived, e.g. `conditional/negated`.
	Mutator string `json:"mutator"`
	// Why says why the assertions do not catch it.
	Why string `json:"why"`
}

// Baseline is the record of the known survivors.
type Baseline struct {
	Note      []string   `json:"note"`
	Survivors []Survivor `json:"survivors"`
}

// loadConfig reads the configuration.
func loadConfig(root string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Join(root, "internal", "mutation", "mutation.json"))
	if err != nil {
		return nil, fmt.Errorf("read the mutation configuration: %w", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse the mutation configuration: %w", err)
	}
	return &c, nil
}

// loadBaseline reads the record of known survivors.
func loadBaseline(root string) (*Baseline, error) {
	raw, err := os.ReadFile(filepath.Join(root, "internal", "mutation", "baseline.json"))
	if err != nil {
		return nil, fmt.Errorf("read the mutation baseline: %w", err)
	}
	var b Baseline
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("parse the mutation baseline: %w", err)
	}
	return &b, nil
}

// Finding is one problem with the gate's own configuration.
type Finding struct {
	Subject string
	Problem string
}

func (f Finding) String() string {
	return f.Subject + ": " + f.Problem
}

// check reports every problem with the configuration and the baseline.
//
// The point of checking the gate's own configuration is the same as the point
// of `internal/treestate`'s assertions: a gate whose configuration has
// quietly rotted is a gate that reports green while measuring nothing. The
// checks here are the ones that would otherwise be invisible:
//
//   - a floor of zero, which is not a floor
//   - a package that is not in the tree, which measures nothing
//   - a survivor naming a file or a line that is not there, which excuses
//     nothing
//   - a survivor with no reason, which is a hole with a bookmark in it
//   - an excluded directory that is not excluded, which makes the sandbox
//     copy fifty-six thousand Python files
func check(root string) ([]Finding, error) {
	cfg, err := loadConfig(root)
	if err != nil {
		return nil, err
	}
	base, err := loadBaseline(root)
	if err != nil {
		return nil, err
	}

	var out []Finding

	if cfg.MinCoveredMSI <= 0 {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: "the covered-code floor is zero, and a floor of zero is not a floor. " +
				"The gate would run, measure nothing and pass",
		})
	}
	if len(cfg.Packages) == 0 {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: "names no package, so the gate would mutate nothing and pass",
		})
	}
	for _, pattern := range cfg.Packages {
		pkg := strings.TrimSuffix(pattern, "/...")
		if pkg == "" {
			out = append(out, Finding{
				Subject: "the mutation configuration",
				Problem: fmt.Sprintf("the package pattern %q names no package", pattern),
			})
			continue
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(pkg))); statErr != nil {
			out = append(out, Finding{
				Subject: pkg,
				Problem: "is in the mutation scope and is not a directory in the tree, so the " +
					"gate would measure nothing for it",
			})
		}
	}
	if cfg.Workers <= 0 {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: "asks for no worker, so the gate would run serially or not at all",
		})
	}
	if cfg.TimeoutCoefficient <= 0 {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: "asks for no timeout coefficient, so the per-mutation timeout is the tool's " +
				"ten seconds. That is shorter than the slowest package's own suite takes -- " +
				"`internal/api` derives a key with argon2 on purpose, because a key that is " +
				"cheap to derive is a key that is cheap to guess -- so the gate would report a " +
				"timeout as a coverage failure",
		})
	}
	if cfg.Baseline == "" {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: "names no baseline file, so a surviving mutant would have nowhere to be " +
				"recorded and the gate would refuse every one of them without saying where to " +
				"put it",
		})
	} else if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(cfg.Baseline))); statErr != nil {
		out = append(out, Finding{
			Subject: "the mutation configuration",
			Problem: fmt.Sprintf("names the baseline %q, which does not exist", cfg.Baseline),
		})
	}
	for _, need := range []string{".git", "poc", ".venv-poc", "web/spa"} {
		if !contains(cfg.Excluded, need) {
			out = append(out, Finding{
				Subject: "the mutation configuration",
				Problem: fmt.Sprintf("does not exclude %q from the sandbox. The copy is what makes "+
					"the gate safe to interrupt, and the copy is fifty-six thousand files "+
					"without this", need),
			})
		}
	}

	for _, s := range base.Survivors {
		if s.File == "" || s.Line <= 0 || s.Mutator == "" {
			out = append(out, Finding{
				Subject: "the mutation baseline",
				Problem: fmt.Sprintf("a survivor names %q line %d mutator %q, which is not a "+
					"mutation", s.File, s.Line, s.Mutator),
			})
			continue
		}
		if strings.TrimSpace(s.Why) == "" {
			out = append(out, Finding{
				Subject: "the mutation baseline",
				Problem: fmt.Sprintf("the survivor at %s:%d (%s) carries no reason. A survivor "+
					"with no reason is a hole with a bookmark in it", s.File, s.Line, s.Mutator),
			})
		}
		path := filepath.Join(root, filepath.FromSlash(s.File))
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			out = append(out, Finding{
				Subject: "the mutation baseline",
				Problem: fmt.Sprintf("the survivor at %s:%d names a file that does not exist, so "+
					"it excuses nothing", s.File, s.Line),
			})
			continue
		}
		if s.Line > strings.Count(string(raw), "\n")+1 {
			out = append(out, Finding{
				Subject: "the mutation baseline",
				Problem: fmt.Sprintf("the survivor at %s:%d names a line the file does not have, "+
					"so it excuses nothing", s.File, s.Line),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Problem < out[j].Problem
	})
	return out, nil
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
