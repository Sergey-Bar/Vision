package main

// The calibration command.
//
// # What it measures
//
// The threat engine against THIS INSTALL's own labelled history: the
// operator's dispositions joined to the incidents they were recorded against,
// with the features the engine read out of the incident's stored reason chain.
//
// That is the measurement nobody has made. The committed corpus is a fixture,
// and a fixture proves the ARITHMETIC is right; it says nothing about whether
// the engine is right on a real site, which is the question a security
// director asks.
//
// # Per site and per rule class
//
// A number for the whole install is a number for nothing. The engine's
// precision on one site's cameras says little about another site's, and its
// precision on one rule class says little about the rules that fire ten times a
// day. So the report is broken down by both, and each breakdown carries its own
// N, because a breakdown over four cases is a breakdown over four cases.
//
// # The report is an ARTEFACT
//
// Written to a file, hashed, and its digest recorded in the audit ledger, so it
// can be attached to a release note. That is what M1 gate 4 asks for: "the
// evaluation report attached". A printout that scrolls away is not attached to
// anything.
//
// # Every rate carries its N and its interval
//
// A rate with no N is a number with an undisclosed denominator, and a rate
// with no interval is a number with an undisclosed precision. The Wilson score
// interval is used because a proportion near zero or one has a skewed sampling
// distribution, and the normal approximation is exactly wrong at the ends --
// which is where a security product's rates live.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/calibration"
	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// ActionCalibrationReport is the audit action for a published calibration
// report.
//
// The action vocabulary is open, and a report that is not recorded is a
// printout.
const ActionCalibrationReport = "calibration.report"

// calibrationScope is one breakdown: a site, a rule class, or both empty for the
// whole install.
type calibrationScope struct {
	Site  string `json:"site,omitempty"`
	Class string `json:"class,omitempty"`
}

// calibrationNumbers is what one scope measured.
type calibrationNumbers struct {
	// Scope is what the numbers are about.
	Scope calibrationScope `json:"scope"`
	// N is how many cases the numbers are over. Every rate below divides by it.
	N int `json:"n"`
	// Cases and Escalations are the counts.
	Cases     int `json:"cases"`
	Escalated int `json:"escalated"`
	Attention int `json:"attention"`
	TruePos   int `json:"true_positives"`
	FalsePos  int `json:"false_positives"`
	Missed    int `json:"missed"`
	// The rates, each with its interval.
	EscalationPrecision *rateWithInterval `json:"escalation_precision"`
	EscalationRecall    *rateWithInterval `json:"escalation_recall"`
	AttentionPrecision  *rateWithInterval `json:"attention_precision"`
	AttentionRecall     *rateWithInterval `json:"attention_recall"`
	F1                  *rateWithInterval `json:"f1"`
	CalibrationError    *rateWithInterval `json:"calibration_error"`
	HAMPer1000          *rateWithInterval `json:"ham_per_1000"`
	// AttentionMinutes is the estimated operator time, in minutes.
	AttentionMinutes float64 `json:"attention_minutes"`
}

// rateWithInterval is one rate, with the interval that says how precise it is.
type rateWithInterval struct {
	Value float64 `json:"value"`
	// N is the denominator the rate is over.
	N int64 `json:"n"`
	// CI is the Wilson interval, and it is nil when the N is zero.
	CI *operatormetrics.Interval `json:"confidence_interval"`
}

// calibrationReport is the artefact.
type calibrationReport struct {
	// ReportVersion is the artefact's own contract.
	ReportVersion string `json:"report_version"`
	// Tenant is the install the report is about.
	Tenant string `json:"tenant"`
	// Window is the period the corpus covers. The zero bounds mean "the whole
	// history", and the report says so rather than pretending it was a period.
	Window struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	} `json:"window"`
	// GeneratedAt is when the report was built.
	GeneratedAt time.Time `json:"generated_at"`
	// Cases is how many labelled cases the install has.
	Cases int `json:"cases"`
	// Skipped is how many cases the generator could not read.
	//
	// It is reported and never folded into a denominator: a corpus that
	// silently dropped the cases it could not read would measure the easy half.
	Skipped int `json:"skipped"`
	// Whole is the install's own numbers.
	Whole calibrationNumbers `json:"whole"`
	// BySite and ByClass are the breakdowns.
	BySite  []calibrationNumbers `json:"by_site"`
	ByClass []calibrationNumbers `json:"by_class"`
	// Note carries what a reader needs to know and would otherwise have to
	// guess.
	Note string `json:"note"`
}

// calibrationCommand is the command.
func calibrationCommand() *command.Command {
	return &command.Command{
		Name:    "calibration",
		Summary: "Measure the threat engine against this install's own labelled history",
		Usage: `calibration — the engine, scored against the install's own history.

The corpus is generated from the operator's dispositions joined to the incidents
they were recorded against, with the features the engine read out of the
incident's stored reason chain. Every rate carries its N and its interval.

  calibration                                  the whole history
  calibration --from 2026-09-01 --to 2026-09-30
  calibration --site north-gate --class person
  calibration --format json --out calibration-2026-09.json
`,
		Run: runCalibration,
	}
}

func runCalibration(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("calibration", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	from := fs.String("from", "", "Window start, as an RFC3339 instant. Omitted means the whole history.")
	to := fs.String("to", "", "Window end, as an RFC3339 instant. Omitted means the whole history.")
	site := fs.String("site", "", "Site to measure. Omitted measures every site.")
	class := fs.String("class", "", "Rule class to measure. Omitted measures every class.")
	tenant := fs.String("tenant", "", "Tenant to measure. Omitted takes the configured one.")
	format := fs.String("format", "json", "Output format: json or csv.")
	out := fs.String("out", "", "Write the report to this file, inside the configured report root.")
	var asJSON bool
	jsonFlag(fs, &asJSON)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "json" && *format != "csv" {
		return command.Refuse("format is %q, want json or csv", *format)
	}
	if asJSON {
		*format = "json"
	}
	if *tenant == "" {
		*tenant = configuredTenant()
	}

	fromAt, err := parseInstant(*from, "from")
	if err != nil {
		return command.Refuse("%v", err)
	}
	toAt, err := parseInstant(*to, "to")
	if err != nil {
		return command.Refuse("%v", err)
	}

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	corpus, skipped, err := calibration.GenerateCorpusInWindow(ctx, pool, *tenant, fromAt, toAt)
	if err != nil {
		return err
	}
	if len(corpus.Cases) == 0 {
		return command.Refuse("the install has no labelled history in the window, so there is " +
			"nothing to measure. A rate over no cases is not a rate")
	}

	report := buildCalibrationReport(*tenant, fromAt, toAt, corpus, skipped, *site, *class)

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("calibration: render the report: %w", err)
	}
	body = append(body, '\n')

	if *out != "" {
		path, err := writeReportFile(*out, body)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		if err := recordCalibration(ctx, pool, *tenant, digest, path, report); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "wrote %s\n  digest %s\n", path, digest)
		return nil
	}

	if *format == "csv" {
		_, err := env.Stdout.Write([]byte(calibrationCSV(report)))
		return err
	}
	_, err = env.Stdout.Write(body)
	return err
}

// buildCalibrationReport assembles the artefact.
func buildCalibrationReport(tenant string, from, to time.Time, corpus calibration.Corpus, skipped int, site, class string) calibrationReport {
	out := calibrationReport{
		ReportVersion: "security.io/calibration-report/v1",
		Tenant:        tenant,
		GeneratedAt:   time.Now().UTC(),
		Cases:         len(corpus.Cases),
		Skipped:       skipped,
		Note: "The corpus is this install's own labelled history: the operator's dispositions " +
			"joined to the incidents they were recorded against, with the features the engine read " +
			"out of the incident's stored reason chain. A case the generator could not read is " +
			"counted and not scored, because a corpus that silently dropped the cases it could not " +
			"read would measure the easy half.",
	}
	out.Window.From = from
	out.Window.To = to
	out.Whole = measureScope(calibrationScope{}, corpus)
	out.BySite = scopes(corpus, site, class, "site", func(c calibration.Case) string { return c.Site })
	out.ByClass = scopes(corpus, site, class, "class", func(c calibration.Case) string { return c.Rule })
	return out
}

// scopes measures the corpus per distinct value of `key`.
//
// The dimension is named because a site breakdown and a class breakdown are
// different things, and a report that put a class in the site column would be
// read as a site.
func scopes(corpus calibration.Corpus, site, class, dimension string, key func(calibration.Case) string) []calibrationNumbers {
	seen := map[string]bool{}
	var names []string
	for _, c := range corpus.Cases {
		if site != "" && c.Site != site {
			continue
		}
		if class != "" && c.Rule != class {
			continue
		}
		name := key(c)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]calibrationNumbers, 0, len(names))
	for _, name := range names {
		subset := filterCases(corpus, site, class, name, key)
		if len(subset.Cases) == 0 {
			continue
		}
		scope := calibrationScope{}
		if dimension == "site" {
			scope.Site = name
		} else {
			scope.Class = name
		}
		out = append(out, measureScope(scope, subset))
	}
	return out
}

// filterCases is the corpus restricted to one scope.
func filterCases(corpus calibration.Corpus, site, class, name string, key func(calibration.Case) string) calibration.Corpus {
	out := calibration.Corpus{
		ContractVersion: corpus.ContractVersion,
		Provenance:      corpus.Provenance,
		Cases:           []calibration.Case{},
	}
	for _, c := range corpus.Cases {
		if site != "" && c.Site != site {
			continue
		}
		if class != "" && c.Rule != class {
			continue
		}
		if key(c) != name {
			continue
		}
		out.Cases = append(out.Cases, c)
	}
	return calibration.ComputePriors(out)
}

// measureScope scores one scope and turns the counts into rates with intervals.
func measureScope(scope calibrationScope, corpus calibration.Corpus) calibrationNumbers {
	report := calibration.Evaluate(corpus)
	engine := report.Calibrated
	out := calibrationNumbers{
		Scope:            scope,
		N:                report.Cases,
		Cases:            engine.Cases,
		Escalated:        engine.Escalated,
		Attention:        engine.Attention,
		TruePos:          engine.AttendedTruePositives,
		FalsePos:         engine.AttendedFalsePositives,
		Missed:           engine.Missed,
		AttentionMinutes: engine.AttentionMinutes,
	}
	denominator := int64(engine.Cases)
	// The four PROPORTIONS carry a Wilson interval.
	//
	// The other three do not, and that is not an omission: F1 is a harmonic
	// mean of two proportions, the calibration error is a mean distance and
	// HAM-per-1000 is a rate per thousand. None of them is a proportion, and a
	// Wilson interval on something that is not a proportion is a fabricated
	// number.
	truePositives := int64(engine.EscalatedTruePositives + engine.Missed)
	out.EscalationPrecision = proportion(engine.EscalationPrecision, int64(engine.EscalatedTruePositives), int64(engine.Escalated))
	out.EscalationRecall = proportion(engine.EscalationRecall, int64(engine.EscalatedTruePositives), truePositives)
	out.AttentionPrecision = proportion(engine.AttentionPrecision, int64(engine.AttendedTruePositives), int64(engine.Attention))
	out.AttentionRecall = proportion(engine.AttentionRecall, int64(engine.AttendedTruePositives), truePositives)
	out.F1 = plain(engine.F1, denominator)
	out.CalibrationError = plain(engine.CalibrationError, denominator)
	out.HAMPer1000 = plain(engine.HAMPer1000, denominator)
	return out
}

// proportion is one PROPORTION, with its successes, its N and its Wilson
// interval.
//
// The successes are the numerator's own count and the N is the denominator's,
// because a Wilson interval is computed on the PAIR. An interval computed with a
// numerator of zero is the interval of a rate of zero whatever the rate says,
// which is a fabricated number.
func proportion(value float64, successes, n int64) *rateWithInterval {
	out := &rateWithInterval{Value: value, N: n}
	if n > 0 {
		lower, upper := operatormetrics.WilsonInterval(successes, n, 1.959963984540054)
		out.CI = &operatormetrics.Interval{Method: "wilson", Lower: lower, Upper: upper}
	}
	return out
}

// plain is a number that is NOT a proportion, and it carries its N and no
// interval.
//
// F1, the calibration error and HAM-per-1000 are none of them proportions, and a
// Wilson interval on a quantity that is not a proportion is a fabricated number.
func plain(value float64, n int64) *rateWithInterval {
	return &rateWithInterval{Value: value, N: n}
}

// calibrationCSV renders the report as CSV.
func calibrationCSV(r calibrationReport) string {
	var b strings.Builder
	b.WriteString("scope,site,class,n,cases,escalated,attention,true_positives,false_positives,missed," +
		"escalation_precision,escalation_recall,attention_precision,attention_recall,f1," +
		"calibration_error,ham_per_1000,attention_minutes\n")
	write := func(n calibrationNumbers) {
		fmt.Fprintf(&b, "%s,%s,%s,%d,%d,%d,%d,%d,%d,%d,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.2f\n",
			scopeLabel(n.Scope), n.Scope.Site, n.Scope.Class, n.N, n.Cases, n.Escalated, n.Attention,
			n.TruePos, n.FalsePos, n.Missed,
			valueOf(n.EscalationPrecision), valueOf(n.EscalationRecall),
			valueOf(n.AttentionPrecision), valueOf(n.AttentionRecall),
			valueOf(n.F1), valueOf(n.CalibrationError), valueOf(n.HAMPer1000),
			n.AttentionMinutes)
	}
	write(r.Whole)
	for _, n := range r.BySite {
		write(n)
	}
	for _, n := range r.ByClass {
		write(n)
	}
	return b.String()
}

// scopeLabel names a scope for the CSV.
func scopeLabel(s calibrationScope) string {
	switch {
	case s.Site != "" && s.Class != "":
		return "site+class"
	case s.Site != "":
		return "site"
	case s.Class != "":
		return "class"
	}
	return "whole"
}

// valueOf is a rate's value, or zero when there is none.
func valueOf(r *rateWithInterval) float64 {
	if r == nil {
		return 0
	}
	return r.Value
}

// recordCalibration writes the publication into the audit ledger.
//
// The report is an ARTEFACT and the ledger is the record that it existed, and
// the two have to agree: a report on a disk with nothing recording it is a file
// somebody found.
func recordCalibration(ctx context.Context, pool *dbx.Pool, tenant, digest, path string, report calibrationReport) error {
	writer := &audit.Writer{Pool: pool}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("calibration: begin the publication record: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = writer.Append(ctx, tx, audit.Entry{
		TenantID:     tenant,
		Actor:        "calibration",
		ActorType:    audit.ActorSystem,
		Action:       ActionCalibrationReport,
		TargetType:   "calibration_report",
		TargetID:     fmt.Sprintf("calibration:%s:%d", report.GeneratedAt.UTC().Format("2006-01-02"), report.Cases),
		Reason:       digest,
		EvidenceRefs: []string{path},
	})
	if err != nil {
		return fmt.Errorf("calibration: record the publication: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("calibration: commit the publication record: %w", err)
	}
	return nil
}

// parseInstant parses an RFC3339 instant, and an empty string means "not set".
func parseInstant(raw, name string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("--%s=%q is not an RFC3339 instant (2026-09-01T00:00:00Z)", name, raw)
	}
	return t.UTC(), nil
}
