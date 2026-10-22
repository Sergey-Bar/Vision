package mutation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonMarshal writes a value the way the gate's own files are written.
func jsonMarshal(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

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

// The configuration and the tree agree.
//
// This is the gate's own configuration, checked against the tree. A gate whose
// configuration has quietly rotted reports green while measuring nothing, and
// that is the shape this repository keeps being bitten by.
func TestTheConfigurationAndTheTreeAgree(t *testing.T) {
	findings, err := check(repoRoot(t))
	if err != nil {
		t.Fatalf("mutation: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// The covered-code floor is the floor that was measured.
//
// The floor is 100, and it was MEASURED rather than guessed: every mutation in
// the code the critical path's tests execute is caught by an assertion.
//
// A floor that somebody can lower by hand is a floor that is not a floor. This
// test is what makes lowering it a decision rather than an accident: the
// configuration says 99, and this goes red.
//
// It is also the red-check for the gate, in the shape the red-check table
// wants: the mutation changes the number, and the number is the control.
func TestTheCoveredMSIFloorIsTheOneThatWasMeasured(t *testing.T) {
	cfg, err := loadConfig(repoRoot(t))
	if err != nil {
		t.Fatalf("load the configuration: %v", err)
	}
	if cfg.MinCoveredMSI != 100.0 {
		t.Errorf("the covered-code mutation floor is %v, want 100. The floor was measured on "+
			"2026-10-21 -- every mutation in the code the critical path's tests execute is "+
			"caught by an assertion -- and lowering it is a decision with a red test, not an "+
			"edit", cfg.MinCoveredMSI)
	}
}

// The scope is the critical path.
//
// The mutation gate is worth running on the packages the product's spine runs
// through, and not worth running on a script nobody calls. The scope is
// stated here so that a package added to it is a deliberate act.
func TestTheScopeIsTheCriticalPath(t *testing.T) {
	cfg, err := loadConfig(repoRoot(t))
	if err != nil {
		t.Fatalf("load the configuration: %v", err)
	}
	if len(cfg.Packages) < 20 {
		t.Errorf("the mutation scope names %d packages, want at least 20. A scope that has "+
			"shrunk to a handful is a scope that has been trimmed by accident", len(cfg.Packages))
	}
	for _, want := range []string{
		"./internal/api/...",
		"./internal/audit/...",
		"./internal/incidents/...",
		"./internal/rules/...",
		"./internal/inbox/...",
		"./internal/normalize/...",
		"./internal/evidence/...",
		"./internal/checkpoint/...",
	} {
		found := false
		for _, p := range cfg.Packages {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the mutation scope does not name %s. The scope is the critical path, and "+
				"a link missing from it is a link nobody is mutating", want)
		}
	}
}

// The gate can fail.
//
// The control on the control. Every check in `Check` is driven against a
// fixture and the test asserts the check fires, so a check that is deleted or
// made unfireable is caught here rather than at the moment it matters.
func TestTheGateCanFail(t *testing.T) {
	cases := []struct {
		rule   string
		mutate func(cfg *Config, base *Baseline)
		want   string
	}{
		{
			rule:   "a floor of zero is not a floor",
			mutate: func(cfg *Config, base *Baseline) { cfg.MinCoveredMSI = 0 },
			want:   "a floor of zero is not a floor",
		},
		{
			rule:   "a package that is not in the tree",
			mutate: func(cfg *Config, base *Baseline) { cfg.Packages = append(cfg.Packages, "./internal/ghost/...") },
			want:   "is not a directory in the tree",
		},
		{
			rule: "a survivor with no reason",
			mutate: func(cfg *Config, base *Baseline) {
				base.Survivors = append(base.Survivors, Survivor{File: "internal/mutation/mutation.go", Line: 1, Mutator: "statement/remove"})
			},
			want: "carries no reason",
		},
		{
			rule: "a survivor naming a file that is not there",
			mutate: func(cfg *Config, base *Baseline) {
				base.Survivors = append(base.Survivors, Survivor{File: "internal/mutation/ghost.go", Line: 1, Mutator: "statement/remove", Why: "because"})
			},
			want: "names a file that does not exist",
		},
		{
			rule:   "a sandbox that does not exclude the virtualenv",
			mutate: func(cfg *Config, base *Baseline) { cfg.Excluded = []string{".git"} },
			want:   "does not exclude",
		},
		{
			rule:   "a scope that names no package",
			mutate: func(cfg *Config, base *Baseline) { cfg.Packages = nil },
			want:   "names no package",
		},
	}

	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			cfg, base := fixture(t)
			c.mutate(cfg, base)
			findings := verifyFixture(t, cfg, base)
			if len(findings) == 0 {
				t.Fatalf("the gate said nothing about: %s", c.rule)
			}
			joined := ""
			for _, f := range findings {
				joined += f.String() + "\n"
			}
			if !strings.Contains(joined, c.want) {
				t.Errorf("the gate fired, but not about the rule under test.\n  wanted a finding containing %q\n  got:\n%s", c.want, joined)
			}
		})
	}
}

// fixture: a configuration and a baseline that are both clean.
func fixture(t *testing.T) (*Config, *Baseline) {
	t.Helper()
	cfg := &Config{
		Packages:           []string{"./internal/mutation/..."},
		MinCoveredMSI:      100,
		MinMSI:             0,
		Workers:            4,
		TimeoutCoefficient: 3,
		Baseline:           "internal/mutation/baseline.json",
		Excluded:           []string{".git", "poc", ".venv-poc", "web/spa"},
	}
	base := &Baseline{Survivors: nil}
	return cfg, base
}

// verifyFixture runs the gate's checks over a fixture.
//
// The checks read the configuration and the baseline FROM DISK, so the
// fixture writes them out and the checks read them back. A fixture that used
// a different path would be a fixture that proved the rules against a
// configuration the real gate never sees.
func verifyFixture(t *testing.T, cfg *Config, base *Baseline) []Finding {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "mutation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "clock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "clock", "clock.go"), []byte("package clock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "mutation", "mutation.go"), []byte("package mutation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "mutation", "baseline.json"), []byte(`{"survivors":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := jsonMarshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "mutation", "mutation.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rawBase, err := jsonMarshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "mutation", "baseline.json"), rawBase, 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := check(dir)
	if err != nil {
		t.Fatalf("check the fixture: %v", err)
	}
	return findings
}
