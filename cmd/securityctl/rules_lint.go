package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// lintReport is the machine-readable form. The exit code is the contract scripts
// branch on; the JSON is for a dashboard or a review tool.
type lintReport struct {
	Bundle    string           `json:"bundle"`
	Version   string           `json:"version,omitempty"`
	OK        bool             `json:"ok"`
	Rules     int              `json:"rules"`
	Errors    int              `json:"errors"`
	Warnings  int              `json:"warnings"`
	Problems  []rules.Problem  `json:"problems"`
	Conflicts []rules.Conflict `json:"conflicts,omitempty"`
}

// runRulesLint compiles every bundle in a file or directory and reports.
//
// Lint never activates and never writes. Activation is a separate command that
// additionally requires a reviewed simulate diff, and keeping the two apart is
// what makes "must pass lint before it can be activated" a property rather than
// a habit.
func runRulesLint(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules lint", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	bundlePath := fs.String("bundle", "", "Bundle file, or a directory of them.")
	asJSON := fs.Bool("json", false, "Emit machine-readable output.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &command.UsageError{Err: errors.New("pass the bundle with --bundle, not as a positional argument")}
	}

	path := *bundlePath
	if path == "" {
		path = "rules/builtin"
	}
	files, err := bundleFiles(path)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return command.Refuse("%s contains no .yaml bundle", path)
	}

	compiler := rules.NewCompiler()
	// KnownAIFields is left empty: lint runs without a database, so the
	// AI-observation field check is skipped rather than guessed. The compiler
	// warns when it cannot check, and `rules lint --against-site` will supply
	// the site's profiles once identity and the API exist.
	compiler.KnownAIFields = aiProfileFields()

	var reports []lintReport
	failures := 0
	for _, file := range files {
		report := lintOne(env, compiler, file, *asJSON)
		reports = append(reports, report)
		if !report.OK {
			failures++
		}
	}

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(env.Stdout, "\n%d bundle(s) linted, %d failed\n", len(reports), failures)
	}

	if failures > 0 {
		return command.Refuse("%d bundle(s) did not pass lint", failures)
	}
	return nil
}

func lintOne(env *command.Env, compiler *rules.Compiler, file string, asJSON bool) lintReport {
	raw, err := os.ReadFile(file)
	if err != nil {
		return lintReport{
			Bundle: file, OK: false,
			Problems: []rules.Problem{{
				Severity: rules.SeverityError, Code: "read", Message: err.Error(),
			}},
		}
	}

	compiled, problems := compiler.Compile(raw)
	report := lintReport{Bundle: file, OK: true, Problems: problems}
	if compiled != nil {
		report.Version = compiled.Version
		report.Rules = len(compiled.Compiled)
		report.Conflicts = compiled.Conflicts
	}
	for _, p := range problems {
		switch p.Severity {
		case rules.SeverityError:
			report.Errors++
		case rules.SeverityWarning:
			report.Warnings++
		}
	}
	report.OK = report.Errors == 0

	if !asJSON {
		name := filepath.Base(file)
		if report.OK {
			fmt.Fprintf(env.Stdout, "[ok  ] %-28s %s  %d rule(s), %d warning(s)\n",
				name, report.Version, report.Rules, report.Warnings)
		} else {
			fmt.Fprintf(env.Stdout, "[FAIL] %-28s %d error(s), %d warning(s)\n",
				name, report.Errors, report.Warnings)
		}
		for _, p := range problems {
			marker := "  warning"
			if p.Severity == rules.SeverityError {
				marker = "  error  "
			}
			fmt.Fprintf(env.Stdout, "%s %s\n", marker, p)
		}
	}
	return report
}

// aiProfileFields is the structured field surface of each M1 profile.
//
// It is the top-level keys of each profile's output schema — the only fields a
// rule may name. Model prose is deliberately absent: a rule that could match
// wording would change behaviour when the prompt is rephrased.
var aiProfileFields = func() map[string]map[string]bool {
	return map[string]map[string]bool{
		"restricted-person": {
			"insufficientEvidence": true, "persons": true, "accessState": true,
			"restrictedZoneBreach": true, "afterHours": true, "summary": true,
			"confidence": true,
		},
		"door-forced": {
			"insufficientEvidence": true, "doorState": true, "personPresent": true,
			"obstruction": true, "openDurationSeconds": true, "summary": true,
			"confidence": true,
		},
		"vehicle-at-gate": {
			"insufficientEvidence": true, "vehicleState": true, "vehicleCount": true,
			"plateLegible": true, "plateText": true, "gateState": true,
			"summary": true, "confidence": true,
		},
	}
}

// bundleFiles resolves a path to the bundle files to lint, sorted so a run is
// reproducible.
func bundleFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("rules lint: %w", err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("rules lint: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		out = append(out, filepath.Join(path, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
