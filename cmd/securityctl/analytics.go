package main

// The analytics command: the durable report, the export, and the repair path
// that rebuilds the rollup.
//
// # Why a command and not a scrape
//
// Every number this prints is a Postgres computation over the tables the
// product already writes. The live registry in `internal/observability` answers
// "what is happening right now" and it is a process's memory: it resets on
// restart, so a number anybody signs -- a gate artefact, a release note, a
// customer's pilot report -- cannot come from one.
//
// `securityctl efficiency` already reads the same source tables directly. The
// difference is that `efficiency` computes its numbers at the moment it is
// asked, and `analytics` reads a ROLLUP: a derivation stored per bucket, so a
// report is a read rather than a rescan, and so two reports over the same
// window are the same report.
//
// # The North Star, and what it travels with
//
// The headline number is operator attention minutes per 1000 canonical events.
// It is never printed alone. The four companions -- the operator override rate,
// the AI false-escalation rate, the evidence completeness and the missed
// incident rate -- travel with it, and the report type carries them as a
// struct so a report with three of the four cannot be built at all.
//
// The reason is not tidiness. A North Star with no companion is a number that
// improves when the product gets worse, and the four companions are the four
// ways the product can be quietly bad.
//
// # No target
//
// There is no target in this output. M1 is baseline only: the target is set in
// M2 from the M1 baseline's variance, and a target set before the variance is
// known is a number somebody chose rather than a number anybody measured. The
// report says "no target set" rather than printing a bare number, because a
// bare number next to a reader's expectation is a target whether it was
// measured or not.
//
// # The export
//
// CSV, because CSV is the format a security director's spreadsheet opens.
//
// The `--out` path names a file INSIDE the configured report root, and the
// name is validated by `internal/evidence`'s path guard before anything is
// written. A path that crosses out of the root is a path that reaches
// wherever the process can write, and the guard that already refuses those is
// the one that refuses them here.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

func analyticsCommand() *command.Command {
	c := &command.Command{
		Name:    "analytics",
		Summary: "The durable analytics report: the North Star and its four companions",
		Usage: `analytics — the report that comes out of Postgres, not out of the registry.

What this reports:
  north-star     operator attention minutes per 1000 canonical events, with
                 the four companions that check it: the operator override
                 rate, the AI false-escalation rate, the evidence
                 completeness and the missed-incident rate
  metrics        the rolled-up numbers behind it, each with its N

No target is printed. M1 is baseline only: the target is set in M2 from this
baseline's variance, and a target set before the variance is known is a number
somebody chose.

  analytics report                       the last 24 hours
  analytics report --hours 720           the last 30 days
  analytics report --format csv --out weekly.csv
  analytics rebuild --hours 24           re-derive the rollup for the last 24 hours
`,
	}
	c.Register(analyticsReportCommand())
	c.Register(analyticsRebuildCommand())
	c.Register(analyticsIncidentRateCommand())
	return c
}

// analyticsIncidentRateCommand is M1 gate 5's ratio.
//
// "The 14-day run records events ingested → canonical events → incidents raised,
// per camera, and the ratio is published in the release notes." This is the
// number that goes in the release notes.
func analyticsIncidentRateCommand() *command.Command {
	return &command.Command{
		Name:    "incident-rate",
		Summary: "M1 gate 5's ratio: incidents raised per 1,000 canonical events, per camera",
		Usage: `analytics incident-rate — the ratio, per camera, over a window.

The default window is the fortnight M1 gate 5 names. The ratio is incidents
raised per 1,000 canonical events, and an incident is attributed to the camera
whose event triggered it.

  analytics incident-rate
  analytics incident-rate --hours 336 --json
`,
		Run: runAnalyticsIncidentRate,
	}
}

func runAnalyticsIncidentRate(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("analytics incident-rate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	hours := fs.Int("hours", 14*24, "Window length in hours. The gate names a 14-day run.")
	tenant := fs.String("tenant", "", "Tenant to report over. Omitted takes the configured one.")
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hours < 1 {
		return command.Refuse("window is %d hour(s), want at least 1", *hours)
	}
	if *tenant == "" {
		*tenant = configuredTenant()
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	reader := &operatormetrics.Reader{Pool: pool, Sites: configuredSites()}
	report, err := reader.IncidentRate(ctx, *tenant, analyticsWindow(*hours, time.Now()))
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintf(env.Stdout, "Incident rate  %s to %s\n",
		report.Window.From.UTC().Format(time.RFC3339), report.Window.To.UTC().Format(time.RFC3339))
	fmt.Fprintf(env.Stdout, "%-24s %14s %10s %12s %s\n",
		"camera", "canonical", "incidents", "per 1,000", "95% interval")
	for _, row := range report.Rows {
		name := row.DeviceID
		if name == "" {
			name = "(no device)"
		}
		fmt.Fprintf(env.Stdout, "%-24s %14d %10d %12s %s\n",
			name, row.CanonicalEvents, row.IncidentsRaised,
			formatOptionalFloat(row.Rate), formatInterval(row.CI))
	}
	fmt.Fprintf(env.Stdout, "%-24s %14d %10d %12s %s\n",
		"TOTAL", report.Total.CanonicalEvents, report.Total.IncidentsRaised,
		formatOptionalFloat(report.Total.Rate), formatInterval(report.Total.CI))
	fmt.Fprintf(env.Stdout, "\n%s\n", report.Note)
	return nil
}

// formatInterval renders an interval, or the words that say there is not one.
func formatInterval(ci *operatormetrics.Interval) string {
	if ci == nil {
		return "not measured"
	}
	return fmt.Sprintf("%.2f to %.2f", ci.Lower, ci.Upper)
}

// analyticsReportCommand prints the report, and optionally exports it.
func analyticsReportCommand() *command.Command {
	return &command.Command{
		Name:    "report",
		Summary: "The durable report: the North Star, its companions and the numbers behind them",
		Usage: `analytics report — the report over one window.

  --hours N         the window, in hours. Positive, and at least 1.
  --from, --to      explicit bounds, as RFC3339 instants. They override --hours.
  --site            a site to report over. Omitted reports every site the
                    caller may see.
  --class           a rule class. Omitted reports every class.
  --format          json or csv. Default json.
  --out FILE        write the export to FILE, inside the configured report root.
  --json            shorthand for --format json.

  analytics report
  analytics report --hours 720 --format csv --out weekly-2026-10-21.csv
`,
		Run: runAnalyticsReport,
	}
}

// analyticsWindow is the window a report covers.
//
// It is bucket-ALIGNED, because the rollup is stored per bucket and a window
// that cuts a bucket in half reports a fraction of a bucket as if it were a
// whole one.
func analyticsWindow(hours int, now time.Time) operatormetrics.Window {
	to := now.UTC().Truncate(time.Hour)
	from := to.Add(-time.Duration(hours) * time.Hour)
	return operatormetrics.Window{From: from, To: to}
}

// runAnalyticsReport prints the report and, when asked, exports it.
func runAnalyticsReport(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("analytics report", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	hours := fs.Int("hours", 24, "Window length in hours. Positive, and bounded below by 1.")
	from := fs.String("from", "", "Window start, as an RFC3339 instant. Overrides --hours.")
	to := fs.String("to", "", "Window end, as an RFC3339 instant. Overrides --hours.")
	tenant := fs.String("tenant", "", "Tenant to report over. Omitted takes the configured one.")
	site := fs.String("site", "", "Site to report over. Omitted reports every site the caller may see.")
	class := fs.String("class", "", "Rule class to report over. Omitted reports every class.")
	format := fs.String("format", "json", "Output format: json or csv.")
	out := fs.String("out", "", "Write the export to this file, inside the configured report root.")
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hours < 1 {
		return command.Refuse("window is %d hour(s), want at least 1: a metric with no window is a metric nobody can compare", *hours)
	}
	if *tenant == "" {
		*tenant = configuredTenant()
	}
	if *format != "json" && *format != "csv" {
		return command.Refuse("format is %q, want json or csv", *format)
	}
	if asJSON {
		*format = "json"
	}

	win := analyticsWindow(*hours, time.Now())
	if *from != "" || *to != "" {
		parsed, err := analyticsBounds(*from, *to, *hours, time.Now())
		if err != nil {
			return command.Refuse("%v", err)
		}
		win = parsed
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	reader := &operatormetrics.Reader{Pool: pool, Sites: configuredSites()}
	report, err := reader.Read(ctx, *tenant, *site, *class, win)
	if err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return command.Refuse("the report does not validate against its own rules: %v", err)
	}

	if *format == "csv" {
		csvBytes, err := analyticsCSV(report)
		if err != nil {
			return err
		}
		if *out == "" {
			_, err := env.Stdout.Write(csvBytes)
			return err
		}
		path, err := writeReportFile(*out, csvBytes)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stderr, "wrote %s\n", path)
		return nil
	}

	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// analyticsBounds parses an explicit window.
//
// The bounds are aligned to the hour for the same reason the default window
// is: the rollup is stored per bucket, and a window that cuts one in half
// reports a fraction of a bucket as if it were a whole one.
func analyticsBounds(from, to string, hours int, now time.Time) (operatormetrics.Window, error) {
	end := now.UTC().Truncate(time.Hour)
	if to != "" {
		parsed, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return operatormetrics.Window{}, fmt.Errorf("--to=%q is not an RFC3339 instant", to)
		}
		end = parsed.UTC()
	}
	start := end.Add(-time.Duration(hours) * time.Hour)
	if from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return operatormetrics.Window{}, fmt.Errorf("--from=%q is not an RFC3339 instant", from)
		}
		start = parsed.UTC()
	}
	aligned := operatormetrics.Window{From: start.Truncate(time.Hour), To: end.Truncate(time.Hour)}
	if !aligned.To.After(aligned.From) {
		return operatormetrics.Window{}, fmt.Errorf(
			"the window [%s, %s) covers no whole hour", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	return aligned, nil
}

// analyticsCSV renders the report as CSV.
//
// The shape is one row per metric, with the window, the N and the source on
// every row, because a spreadsheet is a place where a column of numbers with
// no window is a column of numbers nobody can interpret.
//
// The North Star and its companions are rows too, and the companion that was
// not measured is a row with an EMPTY value and an N of zero. It is not
// omitted: a spreadsheet with a missing row and a spreadsheet with a row that
// says "not measured" are different documents, and only one of them is honest.
func analyticsCSV(rep operatormetrics.Report) ([]byte, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)

	if err := w.Write([]string{
		"metric", "value", "n", "window_from", "window_to", "computed_at", "source", "scope",
	}); err != nil {
		return nil, fmt.Errorf("analytics: write the header: %w", err)
	}

	rows := make([][8]string, 0, len(rep.Metrics)+5)
	for _, m := range rep.Metrics {
		rows = append(rows, [8]string{
			m.Metric,
			strconv.FormatFloat(m.Value, 'f', -1, 64),
			strconv.FormatInt(m.N, 10),
			rep.Window.From.UTC().Format(time.RFC3339),
			rep.Window.To.UTC().Format(time.RFC3339),
			m.ComputedAt.UTC().Format(time.RFC3339),
			rep.Source,
			scopeOf(m.SiteID, m.RuleID),
		})
	}
	if ns := rep.NorthStar; ns != nil {
		rows = append(rows, [8]string{
			ns.Metric,
			formatOptionalFloat(ns.Value),
			strconv.FormatInt(ns.N, 10),
			rep.Window.From.UTC().Format(time.RFC3339),
			rep.Window.To.UTC().Format(time.RFC3339),
			ns.ComputedAt.UTC().Format(time.RFC3339),
			rep.Source,
			"north-star",
		})
		for _, c := range []operatormetrics.Rate{
			ns.Companions.OperatorOverrideRate,
			ns.Companions.AIFalseEscalationRate,
			ns.Companions.EvidenceCompleteness,
			ns.Companions.MissedIncidentRate,
		} {
			rows = append(rows, [8]string{
				c.Metric,
				formatOptionalFloat(c.Value),
				strconv.FormatInt(c.N, 10),
				rep.Window.From.UTC().Format(time.RFC3339),
				rep.Window.To.UTC().Format(time.RFC3339),
				c.ComputedAt.UTC().Format(time.RFC3339),
				rep.Source,
				"companion",
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	for _, r := range rows {
		if err := w.Write(r[:]); err != nil {
			return nil, fmt.Errorf("analytics: write a row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("analytics: write the CSV: %w", err)
	}
	return []byte(b.String()), nil
}

// scopeOf renders a row's scope.
func scopeOf(site, rule string) string {
	if site == "" && rule == "" {
		return ""
	}
	return site + "/" + rule
}

// formatOptionalFloat renders a value that may be absent.
//
// An absent value is an EMPTY CELL and not a zero: zero is a measurement, and a
// spreadsheet that cannot tell the two is a spreadsheet that will be read wrong.
func formatOptionalFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// writeReportFile writes the export inside the configured report root.
//
// The name is validated by `internal/evidence`'s path guard -- the one that
// already refuses traversal, absolute paths, NUL bytes and over-long
// references -- and the file is written under the root with the root's own
// permissions.
//
// The guard is reused rather than rewritten because a second path guard is a
// second set of rules, and the two would differ in the case that matters: a
// path that crosses out of the root.
func writeReportFile(name string, body []byte) (string, error) {
	if err := evidence.ValidateRef(name); err != nil {
		return "", command.Refuse("the report path %q was refused: %v. The name is a file INSIDE "+
			"the configured report root, not a path of its own", name, err)
	}
	root := reportRoot()
	if root == "" {
		return "", command.Refuse("no report root is configured. Set SP_REPORT_ROOT, or leave the " +
			"report on the standard output")
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", fmt.Errorf("analytics: create the report directory: %w", err)
	}
	if err := os.WriteFile(full, body, 0o640); err != nil {
		return "", fmt.Errorf("analytics: write the report: %w", err)
	}
	return full, nil
}

// reportRoot is where the reports go.
//
// It comes from the same configuration the core reads, so the CLI and the
// daemon write to the same place. It falls back to the evidence root's reports
// directory, which is where the artefacts belong: on the same volume, under the
// same owner.
func reportRoot() string {
	if v := strings.TrimSpace(os.Getenv("SP_REPORT_ROOT")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("SP_EVIDENCE_ROOT")); v != "" {
		return filepath.Join(v, "reports")
	}
	return ""
}

// analyticsRebuildCommand re-derives the rollup.
//
// This is the REPAIR path, and it is safe to run at any time: the rollup is a
// derivation, so writing the same bucket twice produces the same rows. That is
// what makes this a repair rather than a second source of truth.
func analyticsRebuildCommand() *command.Command {
	return &command.Command{
		Name:    "rebuild",
		Summary: "Re-derive the durable rollup for a window",
		Usage: `analytics rebuild — recompute the rollup from the source tables.

The rollup is a derivation, so this is safe to run at any time and produces
the same rows a second time. It is the repair path for a window that was
missed, and it is how an operator says "recompute this".

  analytics rebuild --hours 24
`,
		Run: runAnalyticsRebuild,
	}
}

func runAnalyticsRebuild(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("analytics rebuild", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	hours := fs.Int("hours", 24, "Window length in hours. Positive, and bounded below by 1.")
	tenant := fs.String("tenant", "", "Tenant to rebuild over. Omitted takes the configured one.")
	site := fs.String("site", "", "Site to rebuild. Omitted takes the install's own site.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hours < 1 {
		return command.Refuse("window is %d hour(s), want at least 1: a metric with no window is a metric nobody can compare", *hours)
	}
	if *tenant == "" {
		*tenant = configuredTenant()
	}
	if *site == "" {
		*site = configuredSite()
	}
	if *site == "" {
		return command.Refuse("no site to rebuild. Set SP_SITE_ID, or pass --site: the rollup is " +
			"site-scoped, and a derivation with no site would read nothing and write nothing")
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	w := analyticsWindow(*hours, time.Now())
	rollup := &operatormetrics.Rollup{Pool: pool, SiteID: *site}
	// The window is walked BUCKET BY BUCKET, because a bucket is what the
	// table's key is. One write for the whole window would land in a bucket
	// whose bounds are the window's, which is a different row from the
	// hourly ones the pipeline writes, and two readers of the same window
	// would then disagree.
	for start := w.From; start.Before(w.To); start = start.Add(time.Hour) {
		end := start.Add(time.Hour)
		if end.After(w.To) {
			end = w.To
		}
		result, err := rollup.Write(ctx, *tenant, operatormetrics.Bucket{Start: start, End: end})
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s  %d metric(s), %d row(s)\n",
			start.UTC().Format(time.RFC3339), result.Metrics, result.Rows)
	}
	return nil
}

// configuredSites is the scope the CLI reads with.
//
// The CLI is an operator tool and it reads the SAME scope the core's configured
// principal has, because the rollup is site-scoped and the `site_isolation`
// policy is RESTRICTIVE: a reader that bound no scope would see nothing, and a
// reader that bound the wrong scope would see another site's rows.
//
// `SP_PRINCIPAL_SITES` names the scope when it is set, and the install's own
// site is the scope when it is not, which is the single-site install's case.
func configuredSites() []string {
	if v := strings.TrimSpace(os.Getenv("SP_PRINCIPAL_SITES")); v != "" {
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if s := strings.TrimSpace(os.Getenv("SP_SITE_ID")); s != "" {
		return []string{s}
	}
	return nil
}

// analyticsDoctorLine is the one line `securityctl doctor` prints about the
// rollup.
//
// It is here rather than in the doctor file so that the vocabulary of the
// report and the vocabulary of the doctor cannot drift: the doctor names the
// metric, the report carries it, and the two are the same string.
func analyticsDoctorLine(freshest time.Time, now time.Time) string {
	if freshest.IsZero() {
		return "the durable rollup holds no rows: run `securityctl analytics rebuild` to derive them"
	}
	age := now.Sub(freshest)
	return fmt.Sprintf("the durable rollup's freshest bucket is %s old (%s)",
		strings.TrimSpace(age.Round(time.Minute).String()), freshest.UTC().Format(time.RFC3339))
}
