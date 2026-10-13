package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// The operator-efficiency report.
//
// Every number here is a Postgres computation, not a reading of the live
// registry. That distinction is the whole reason this command exists rather than
// scraping /v1/metrics: the counters are a process's memory and they reset on
// restart, so a number anybody signs - a gate artefact, a release note, a
// customer's pilot report - cannot come from one. `internal/observability` is for
// "what is happening right now"; this is for "what happened last month".
//
// It reports both halves of every number that has one. attention_minutes is
// capped and uncapped, because the uncapped one is an over-count by definition and
// pretending otherwise is how a metric built on a cap starts to flatter the
// product. MTTI is three numbers rather than one, because detection latency and
// investigation latency have different remedies. The median is the headline and
// the mean sits beside it.
func efficiencyCommand() *command.Command {
	// Run is set here rather than through a Flags closure, because this handler
	// owns its own FlagSet: the window is the one flag and --json is the second,
	// and the tree supports both shapes on purpose.
	return &command.Command{
		Name:    "efficiency",
		Summary: "The durable operator-efficiency numbers: attention, MTTI, MTTV",
		Usage: `efficiency — the numbers that come out of Postgres, not out of the registry.

What this reports:
  attention   capped and uncapped human minutes, from the incident.opened
              ledger rows; an incident nobody opened is not counted as zero
  mt-ti       detection + investigation latency per rule class, mean and median
  mt-tv       verification latency, median and p95 (PROPOSED definition)
  states      the verification-state distribution, including the two silent ones
  evidence-failures
              dead-lettered evidence-fetch jobs, the durable proxy

Every number is a Postgres computation. The live registry in
internal/observability answers "what is happening now"; this answers "what
happened in the window", which is the only shape a gate artefact or a customer
report may quote.

  efficiency --hours 720          the last 30 days
  efficiency --hours 24 --json    machine-readable
`,
		Run: runEfficiency,
	}
}

func runEfficiency(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("efficiency", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	hours := fs.Int("hours", 24, "Window length in hours. Positive, and bounded below by 1.")
	tenant := fs.String("tenant", "", "Tenant to report over. Omitted takes the configured one.")
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

	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	q := &operatormetrics.Queries{Pool: pool}
	to := time.Now().UTC()
	from := to.Add(-time.Duration(*hours) * time.Hour)

	report, err := buildEfficiencyReport(ctx, q, *tenant, from, to)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	writeEfficiencyHuman(env, report)
	return nil
}

// efficiencyReport is one JSON document, so a caller can decode the same shape
// the human output prints.
type efficiencyReport struct {
	Tenant           string                            `json:"tenant"`
	From             time.Time                         `json:"from"`
	To               time.Time                         `json:"to"`
	Attention        *operatormetrics.AttentionReport  `json:"attention"`
	MTTI             *operatormetrics.LatencyReport    `json:"mt_ti"`
	MTTV             *operatormetrics.MTTVReport       `json:"mt_tv"`
	Verification     *operatormetrics.StateCounts      `json:"verification_states"`
	Silence          *operatormetrics.StateSliceReport `json:"silence_by_site_and_class"`
	EvidenceFailures int64                             `json:"evidence_fetch_failures"`
	Cap              time.Duration                     `json:"attention_cap_seconds,omitempty"`
}

func buildEfficiencyReport(ctx context.Context, q *operatormetrics.Queries, tenant string, from, to time.Time) (efficiencyReport, error) {
	var out efficiencyReport
	out.Tenant, out.From, out.To = tenant, from, to
	out.Cap = operatormetrics.DefaultAttentionCap

	attention, err := q.Attention(ctx, tenant, from, to, out.Cap)
	if err != nil {
		return out, err
	}
	out.Attention = &attention

	mtti, err := q.MTTI(ctx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.MTTI = &mtti

	mttv, err := q.MTTV(ctx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.MTTV = &mttv

	states, err := q.VerificationStates(ctx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.Verification = &states

	// The per-cell slice: WHERE the system stays quiet, not only how often. This
	// is the autonomous-camera question — a rule suppressing at one site and not
	// another is a configuration question about that site, and the aggregate
	// averages the two into a plausible total that describes neither.
	silence, err := q.VerificationStatesBySiteAndClass(ctx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.Silence = &silence

	failures, err := q.EvidenceFetchFailures(ctx, tenant, from, to)
	if err != nil {
		return out, err
	}
	out.EvidenceFailures = failures
	return out, nil
}

// share is the silent fraction of a cell, guarding the divide for a cell with no
// decisions at all. A cell that appears with zero total is a query bug, not a
// silent rule, and the answer is 0 rather than NaN.
func share(c operatormetrics.StateSlice) float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Silent) / float64(c.Total)
}

// writeEfficiencyHuman prints the report a human reads.
//
// The layout names what each number is NOT as well as what it is. A report that
// prints only the flattering half of a two-sided number is how a cap becomes a
// claim.
func writeEfficiencyHuman(env *command.Env, r efficiencyReport) {
	fmt.Fprintf(env.Stdout, "operator efficiency for %s\n", r.Tenant)
	fmt.Fprintf(env.Stdout, "  window %s .. %s\n",
		r.From.UTC().Format(time.RFC3339), r.To.UTC().Format(time.RFC3339))
	fmt.Fprintln(env.Stdout)

	// Attention: the North Star numerator, both halves.
	fmt.Fprintln(env.Stdout, "attention_minutes  (capped and uncapped; both, always)")
	if r.Attention.Samples == 0 {
		fmt.Fprintln(env.Stdout, "  no incident in this window has both an incident.opened row and a disposition")
	} else {
		fmt.Fprintf(env.Stdout, "  capped    %6.1f minutes over %d incident(s)\n",
			r.Attention.CappedSeconds/60, r.Attention.Samples)
		fmt.Fprintf(env.Stdout, "  uncapped  %6.1f minutes over the same %d incident(s)\n",
			r.Attention.UncappedSeconds/60, r.Attention.Samples)
		fmt.Fprintf(env.Stdout, "  cap       %s per incident, a PROPOSED value: to be set from the M1 distribution\n",
			r.Cap)
		if r.Attention.CappedCount > 0 {
			fmt.Fprintf(env.Stdout, "  %d incident(s) hit the cap, so their uncapped number is known to be an over-count\n",
				r.Attention.CappedCount)
		}
	}
	if r.Attention.OpenCount > 0 {
		fmt.Fprintf(env.Stdout, "  %d incident(s) are open: attention still being spent, in no total yet\n",
			r.Attention.OpenCount)
	}
	fmt.Fprintln(env.Stdout)

	// MTTI: three numbers per class, median first.
	fmt.Fprintln(env.Stdout, "mt_ti_seconds  (detection + investigation; median is the headline)")
	if len(r.MTTI.Classes) == 0 {
		fmt.Fprintln(env.Stdout, "  no incident was dispositioned in this window")
	} else {
		fmt.Fprintf(env.Stdout, "  %-28s %8s %8s %8s %8s %8s\n",
			"RULE", "N", "MEDI(mt)", "MEAN(mt)", "MEDI(det)", "MEDI(inv)")
		for _, c := range r.MTTI.Classes {
			fmt.Fprintf(env.Stdout, "  %-28s %8d %8.1f %8.1f %8.1f %8.1f\n",
				shorten(c.RuleID, 28), c.Samples, c.MedianMTTI, c.MeanMTTI,
				c.MedianDetection, c.MedianInvestigation)
		}
	}
	fmt.Fprintln(env.Stdout)

	// MTTV: the proposed name, labelled.
	fmt.Fprintln(env.Stdout, "mt_tv_seconds  (PROPOSED definition: E1 must confirm the name)")
	if r.MTTV.Samples == 0 {
		fmt.Fprintln(env.Stdout, "  no verification in this window had its trigger event resolvable")
	} else {
		fmt.Fprintf(env.Stdout, "  median %8.1fs over %d row(s)\n", r.MTTV.MedianSeconds, r.MTTV.Samples)
		fmt.Fprintf(env.Stdout, "  p95    %8.1fs\n", r.MTTV.P95Seconds)
	}
	fmt.Fprintln(env.Stdout)

	// Where the system stays quiet: per site and rule class, most silent first.
	//
	// The autonomous-camera surface. A rule class that suppresses 60% of its
	// decisions at one site and 2% at another is a configuration question about
	// that site, and the aggregate below averages the two into a plausible total
	// that describes neither. Sorted by silent share descending, so the cell
	// somebody should look at is the first line rather than the one they have to
	// find.
	fmt.Fprintln(env.Stdout, "where the system stays quiet  (per site and rule class, most silent first)")
	if r.Silence == nil || len(r.Silence.Cells) == 0 {
		fmt.Fprintln(env.Stdout, "  no verification in this window had an incident to belong to")
	} else {
		cells := append([]operatormetrics.StateSlice(nil), r.Silence.Cells...)
		sort.Slice(cells, func(i, j int) bool {
			si, sj := share(cells[i]), share(cells[j])
			if si != sj {
				return si > sj
			}
			return cells[i].Total > cells[j].Total
		})
		fmt.Fprintf(env.Stdout, "  %-22s %-28s %8s %8s %8s\n", "SITE", "RULE", "SILENT", "DECIDED", "SHARE")
		for _, c := range cells {
			note := ""
			if c.Silent > 0 && c.Silent == c.Total {
				note = "  <- this rule class said nothing at all here"
			}
			fmt.Fprintf(env.Stdout, "  %-22s %-28s %8d %8d %7.0f%%%s\n",
				shorten(c.SiteID, 22), shorten(c.RuleID, 28), c.Silent,
				c.Total-c.Silent, 100*share(c), note)
		}
	}
	fmt.Fprintln(env.Stdout)

	// Verification states: the two silent ones are the reason this is here.
	fmt.Fprintln(env.Stdout, "verification states  (duplicate and suppressed are the silent two)")
	if r.Verification.Total == 0 {
		fmt.Fprintln(env.Stdout, "  no verification result in this window")
	} else {
		states := make([]string, 0, len(r.Verification.ByState))
		for s := range r.Verification.ByState {
			states = append(states, s)
		}
		sort.Strings(states)
		for _, s := range states {
			note := ""
			if s == "duplicate" {
				note = "  ingest working: the event repeated one already accounted for"
			}
			if s == "suppressed" {
				note = "  a rule working: a higher-priority rule suppressed this on purpose"
			}
			fmt.Fprintf(env.Stdout, "  %-14s %6d%s\n", s, r.Verification.ByState[s], note)
		}
	}
	fmt.Fprintln(env.Stdout)

	// The evidence proxy, labelled as a proxy.
	fmt.Fprintf(env.Stdout, "evidence_fetch_failed_total (durable proxy, not a measurement)\n")
	fmt.Fprintf(env.Stdout, "  %d dead-lettered evidence-fetch job(s). A fetch that failed twice and then\n", r.EvidenceFailures)
	fmt.Fprintln(env.Stdout, "  succeeded is not counted at all, so this is a floor on evidence trouble.")
}
