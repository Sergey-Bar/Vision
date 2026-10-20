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
	"time"

	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/simulate"
)

// runRulesSimulate runs a candidate bundle over the fixture set and prints the
// diff digest that activation requires.
//
// The digest is over the diff, not over the bundle, and not over the run time.
// Re-running the simulation to confirm a review therefore produces the same
// digest, while a change to the fixtures or to the active bundle produces a
// different one and the review no longer applies.
func runRulesSimulate(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules simulate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	bundlePath := fs.String("bundle", "", "Candidate bundle file. Defaults to the newest in rules/builtin.")
	fixturePath := fs.String("fixtures", filepath.Join("testdata", "fixtures"), "Fixture set directory.")
	baselinePath := fs.String("baseline", "", "Bundle to diff against. Defaults to the stored active bundle.")
	asJSON := fs.Bool("json", false, "Emit machine-readable output.")
	maxEvents := fs.Int("max-events", 100000, "Cap on evaluated signals.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &command.UsageError{Err: errors.New("pass the bundle with --bundle, not as a positional argument")}
	}

	path := *bundlePath
	if path == "" {
		files, err := bundleFiles("rules/builtin")
		if err != nil || len(files) == 0 {
			return command.Refuse("no candidate bundle; pass --bundle")
		}
		path = files[len(files)-1]
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rules simulate: %w", err)
	}

	compiler := newSiteCompiler()
	candidate, problems := compiler.Compile(raw)
	for _, p := range problems {
		if p.Severity == "error" {
			return command.Refuse("candidate bundle does not compile: %s", p)
		}
	}
	if candidate == nil {
		return command.Refuse("candidate bundle did not compile")
	}

	scenarios, err := scenariosFromFixtures(ctx, *fixturePath)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		return command.Refuse("%s contains no scenarios", *fixturePath)
	}

	runner := &simulate.Runner{
		ClockNow:      func() time.Time { return time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC) },
		SkewTolerance: time.Duration(candidate.Settings.Clock.SkewToleranceMS) * time.Millisecond,
		InSchedule:    businessHoursSchedule,
	}
	after, err := runner.Run(candidate, scenarios, *maxEvents)
	if err != nil {
		return err
	}

	var before []simulate.Outcome
	baselineVersion := ""
	if *baselinePath != "" {
		baselineRaw, err := os.ReadFile(*baselinePath)
		if err != nil {
			return fmt.Errorf("rules simulate: %w", err)
		}
		baseline, _ := compiler.Compile(baselineRaw)
		if baseline != nil {
			baselineVersion = baseline.Version
			before, _ = runner.Run(baseline, scenarios, *maxEvents)
		}
	} else {
		var baselineRaw []byte
		var activeErr error
		baselineVersion, baselineRaw, activeErr = activeBundle(ctx)
		if activeErr == nil && len(baselineRaw) > 0 {
			baseline, _ := compiler.Compile(baselineRaw)
			if baseline != nil {
				baselineVersion = baseline.Version
				before, _ = runner.Run(baseline, scenarios, *maxEvents)
			}
		}
	}

	fixtureSet := filepath.Base(*fixturePath)
	// DiffCapped, not Diff: `--max-events` was accepted and printed and then
	// thrown away, so a run that stopped early produced a digest indistinguishable
	// from a run that saw everything. The truncation now reaches the digest and
	// the report, which is what the flag's documentation has been promising.
	report := simulate.DiffCapped(candidate.Version, baselineVersion, fixtureSet,
		before, after, *maxEvents)

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
		return nil
	}

	fmt.Fprintf(env.Stdout, "candidate  : %s\n", report.BundleVersion)
	fmt.Fprintf(env.Stdout, "baseline   : %s\n", orNone(report.BaselineVersion))
	fmt.Fprintf(env.Stdout, "fixtures   : %s (%d scenario(s), %d outcome(s))\n",
		report.FixtureSet, report.ScenariosRun, len(after))
	fmt.Fprintf(env.Stdout, "changes    : %d\n", len(report.Changes))
	for _, c := range report.Changes {
		fmt.Fprintf(env.Stdout, "  %-24s %s/%s  %s -> %s\n",
			c.Kind, c.ScenarioID, c.SignalID,
			describeOutcome(c.Before), describeOutcome(c.After))
	}
	fmt.Fprintf(env.Stdout, "totals     : %s\n", formatTotals(report.Totals))
	// The AI plane's state, on every run.
	//
	// The plan's P4.2 is that this path runs with the plane killed and *shows
	// the degraded path*. The per-change lines already carry the degradation, but
	// a first run has no baseline and therefore prints no change lines, so the
	// claim would be invisible exactly when somebody first runs it. Stating it
	// here makes the one thing this simulation is for present in the summary a
	// reader sees without scrolling.
	printDegradedSummary(env, after)
	fmt.Fprintf(env.Stdout, "\nactivate with:\n  securityctl rules activate --version %s --diff-digest %s\n",
		report.BundleVersion, report.DiffDigest)
	return nil
}

// degradedReasonCount tallies the reasons a run recorded, so a reader sees which
// failure they are looking at rather than only how many.
func degradedReasonCount(after []simulate.Outcome) (int, map[string]int) {
	var n int
	byReason := map[string]int{}
	for _, o := range after {
		if !o.Degraded {
			continue
		}
		n++
		reason := o.DegradedReason
		if reason == "" {
			// A degradation with no reason is the failure this whole repository
			// treats as the worst one, and a simulation that printed a count and
			// no reason would hide it.
			reason = "UNNAMED"
		}
		byReason[reason]++
	}
	return n, byReason
}

func printDegradedSummary(env *command.Env, after []simulate.Outcome) {
	degraded, byReason := degradedReasonCount(after)
	raised := 0
	for _, o := range after {
		if o.IncidentRaised {
			raised++
		}
	}
	fmt.Fprintf(env.Stdout, "\nAI PLANE: absent.\n")
	fmt.Fprintf(env.Stdout, "  This simulation runs with no model configured, so every request_vlm dispatch is a\n")
	fmt.Fprintf(env.Stdout, "  degraded observation carrying a named reason - the counts below are what this run\n")
	fmt.Fprintf(env.Stdout, "  actually recorded. `no_runtime` and `absent` are deliberately different facts and\n")
	fmt.Fprintf(env.Stdout, "  the report does not collapse them.\n")
	fmt.Fprintf(env.Stdout, "  An incident is still raised on the evidence the deterministic spine can see on its\n")
	fmt.Fprintf(env.Stdout, "  own; it is marked UNVERIFIED rather than blocked, and no rule treats the\n")
	fmt.Fprintf(env.Stdout, "  absence as a negative answer.\n")
	fmt.Fprintf(env.Stdout, "  %d of %d incident(s) raised, %d outcome(s) degraded", raised, len(after), degraded)
	if degraded > 0 {
		var parts []string
		for reason, n := range byReason {
			parts = append(parts, reason+"="+fmt.Sprint(n))
		}
		sort.Strings(parts)
		fmt.Fprintf(env.Stdout, " (%s)", strings.Join(parts, " "))
	}
	fmt.Fprintln(env.Stdout)
	if byReason["UNNAMED"] > 0 {
		fmt.Fprintf(env.Stdout, "  WARNING: %d degradation(s) carried no reason. That is the failure this product\n", byReason["UNNAMED"])
		fmt.Fprintf(env.Stdout, "  treats as its worst, and it must not survive to an operator.\n")
	}
}

func describeOutcome(o simulate.Outcome) string {
	if !o.IncidentRaised {
		return "no incident"
	}
	return fmt.Sprintf("%s@%s%s", o.State, o.Severity, degradedSuffix(o.Degraded, o.DegradedReason))
}

func degradedSuffix(degraded bool, reason string) string {
	if !degraded {
		return ""
	}
	return " (degraded:" + reason + ")"
}

func formatTotals(totals map[string]int) string {
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, totals[k]))
	}
	return joinWith(parts, " ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none — this would be the first bundle)"
	}
	return s
}

// runRulesActivate turns a stored bundle on, against a simulation the operator
// reviewed.
//
// It takes the report, not a digest. A digest on its own proves only that
// somebody typed 64 hex characters; the report is what the operator actually
// read, and it is what the gate recomputes the digest from. `--report` takes the
// file `rules simulate --json > report.json` produced, so the thing acknowledged
// and the thing verified are the same bytes.
func runRulesActivate(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules activate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	version := fs.String("version", "", "Bundle version to activate.")
	reportPath := fs.String("report", "", "Path to the JSON report `rules simulate --json` produced.")
	// The old spelling, accepted and ignored.
	//
	// A digest on its own cannot be checked -- the gate re-hashes the report's
	// change list and compares it with the digest the report carries -- so an
	// operator who typed one was never authorised by the digest. The flag is
	// still accepted so that an old command line is a refusal with a reason
	// rather than "flag provided but not defined".
	_ = fs.String("diff-digest", "", "Deprecated. The report carries the digest; a digest on its own is not checkable.")
	fs.Usage = func() {
		fmt.Fprintln(env.Stderr, "usage: securityctl rules activate --version V --report report.json")
		fmt.Fprintln(env.Stderr,
			"The report is re-hashed and compared with the digest it carries, so a report "+
				"edited after it was read is refused.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return &command.UsageError{Err: errors.New("--version is required")}
	}
	if *reportPath == "" {
		return &command.UsageError{Err: errors.New(
			"--report is required. A digest on its own cannot be checked, so activation takes the " +
				"simulation report the operator reviewed; produce it with `rules simulate --json`")}
	}

	body, err := os.ReadFile(*reportPath)
	if err != nil {
		return fmt.Errorf("read the simulation report: %w", err)
	}
	var report simulate.Report
	if err := json.Unmarshal(body, &report); err != nil {
		return fmt.Errorf("the simulation report is not the JSON `rules simulate --json` "+
			"produces: %w", err)
	}

	tenant := configuredTenant()
	svc := newBundleService(ctx)
	if err := svc.Activate(ctx, tenant, *version, report, "securityctl"); err != nil {
		var gate *bundles.ErrGateNotSatisfied
		if errors.As(err, &gate) {
			return command.Refuse("%s", gate.Reason)
		}
		return err
	}
	fmt.Fprintf(env.Stdout, "activated rule bundle %s for tenant %s (reviewed diff %s)\n",
		*version, tenant, report.DiffDigest)
	return nil
}

func runRulesRollback(ctx context.Context, env *command.Env, args []string) error {
	tenant := configuredTenant()
	svc := newBundleService(ctx)
	version, err := svc.Rollback(ctx, tenant, "securityctl")
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "rolled back to rule bundle %s\n", version)
	return nil
}

func runRulesShow(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules show", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "Emit machine-readable output.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tenant := configuredTenant()
	svc := newBundleService(ctx)
	list, err := svc.List(ctx, tenant)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"active": activeVersionOf(list), "bundles": list})
	}
	if len(list) == 0 {
		fmt.Fprintf(env.Stdout, "no bundles stored for tenant %s\n", tenant)
		return nil
	}
	fmt.Fprintf(env.Stdout, "%-10s %-10s %-8s %-20s %s\n", "ACTIVE", "VERSION", "LINT", "CREATED", "CONTENT HASH")
	for _, m := range list {
		active := ""
		if m.Active {
			active = "*"
		}
		fmt.Fprintf(env.Stdout, "%-10s %-10s %-8s %-20s %s\n",
			active, m.Version, m.LintStatus, m.CreatedAt.Format("2006-01-02T15:04"), m.ContentHash[:12])
	}
	return nil
}

func activeVersionOf(list []bundles.Meta) string {
	for _, m := range list {
		if m.Active {
			return m.Version
		}
	}
	return ""
}

// runRulesStore compiles and stores a bundle without activating it.
func runRulesStore(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("rules store", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	bundlePath := fs.String("bundle", "", "Bundle file to store.")
	source := fs.String("source", "", "Source reference recorded with the bundle.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundlePath == "" {
		return &command.UsageError{Err: errors.New("--bundle is required")}
	}

	raw, err := os.ReadFile(*bundlePath)
	if err != nil {
		return fmt.Errorf("rules store: %w", err)
	}

	tenant := configuredTenant()
	svc := newBundleService(ctx)
	if svc == nil {
		return errors.New("rules store: no database is configured; set SP_DATABASE_URL")
	}
	meta, problems, err := svc.Store(ctx, tenant, raw, *source, "securityctl", "")
	if err != nil {
		return err
	}
	for _, p := range problems {
		level := "warning"
		if p.Severity == "error" {
			level = "error"
		}
		fmt.Fprintf(env.Stderr, "  %s %s\n", level, p)
	}
	if meta.Version == "" {
		return command.Refuse("%s did not compile; nothing was stored", *bundlePath)
	}
	fmt.Fprintf(env.Stdout, "stored rule bundle %s (%s, %d problem(s)); it is not active.\n",
		meta.Version, meta.ContentHash[:12], len(problems))
	fmt.Fprintf(env.Stdout, "simulate and activate it with:\n"+
		"  securityctl rules simulate --bundle %s\n"+
		"  securityctl rules activate --version %s --diff-digest <digest>\n",
		*bundlePath, meta.Version)
	return nil
}
