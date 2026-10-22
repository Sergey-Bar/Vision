package operatormetrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The analytics report.
//
// # What a report is
//
// A DERIVATION over the durable rollup, rendered as a document somebody can
// sign. Every number in it is a Postgres computation over the tables the
// product already writes, and the report says so: `source` is `postgres` and
// there is no other value, because a report whose numbers came from a counter
// is a report that restarts with the process.
//
// # The North Star and its four companions
//
// The North Star is operator attention minutes per 1000 canonical events, and
// it is never reported alone.
//
// The four companions are `operator_override_rate`, `ai_false_escalation_rate`,
// `evidence_completeness` and `missed_incident_rate`, and the report type
// carries them as a STRUCT rather than as a map, so a report with three
// companions cannot be constructed at all.
//
// The reason is not tidiness. A North Star with no companion is a number that
// improves when the product gets worse: a rule set that fires less produces
// fewer incidents, and if the denominator were incidents the ratio would
// improve without anybody doing anything. The denominator is canonical events,
// which is the one quantity that cannot be gamed by being quiet, and the
// companions are the four ways the product can be quietly bad.
//
// # No targets
//
// There is no target in this report.
//
// M1 is "baseline only, no target" by roadmap decision: a target set before
// the baseline variance is known is a number somebody chose rather than a
// number anybody measured, and the M2 target is set FROM the M1 baseline's
// variance. So a report that carried a target would be carrying a decision
// that had not been made, and `Targets` does not exist as a field for exactly
// that reason.
//
// # A rate without an N is not a rate
//
// Every rate carries its N, its window, when it was computed and its
// confidence interval, and a rate whose N is zero has NO value rather than a
// value of zero. Zero out of zero observations is not a rate; it is the
// absence of one, and the report says which it is.

// ReportVersion is this report's own contract.
//
// A reader refuses a version it does not understand rather than reading a
// field as something it is not.
const ReportVersion = "security.io/metrics-report/v1"

// Window is the period a number covers.
//
// It is part of every number rather than a property of the report, because a
// rate without its window is a rate without a denominator.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Interval is a confidence interval on a rate.
type Interval struct {
	// Method is how the interval was computed. It is always `wilson` today, and
	// it is named so a reader knows what the numbers mean.
	Method string `json:"method"`
	// Lower and Upper are the bounds.
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

// Measure is one rolled-up number.
type Measure struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// Value is the measured number.
	Value float64 `json:"value"`
	// N is how many things the value counts.
	N int64 `json:"n"`
	// Window is the period the value covers.
	Window Window `json:"window"`
	// ComputedAt is when the value was derived.
	ComputedAt time.Time `json:"computed_at"`
	// SiteID and RuleID are the scope the row was stored at, and they are
	// reported so a reader can see the granularity rather than having to
	// assume it.
	SiteID string `json:"site_id,omitempty"`
	RuleID string `json:"rule_id,omitempty"`
}

// Rate is a proportion, with everything a proportion needs to be acted on.
type Rate struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// Value is the proportion, or nil when the N is zero.
	//
	// Nil and zero are different answers. A rate of zero out of zero
	// observations is not a rate, and a report that cannot tell them apart
	// reports "no disagreements" about a site that has never been observed.
	Value *float64 `json:"value"`
	// N is the denominator: how many observations the rate is over.
	N int64 `json:"n"`
	// Window is the period the rate covers.
	Window Window `json:"window"`
	// ComputedAt is when the rate was derived.
	ComputedAt time.Time `json:"computed_at"`
	// ConfidenceInterval is the interval on the rate, or nil when the N is too
	// small for one.
	ConfidenceInterval *Interval `json:"confidence_interval"`
}

// Companions are the four metrics that travel with the North Star.
//
// A struct, not a map: a report with three companions cannot be built, which
// is the point. The order of the fields is the report's order and it is fixed,
// because a report that showed them in a different order each run would be a
// report nobody could diff.
type Companions struct {
	// OperatorOverrideRate is the share of AI-backed dispositions where the
	// operator disagreed with the model.
	OperatorOverrideRate Rate `json:"operator_override_rate"`
	// AIFalseEscalationRate is the share of escalations that were false
	// positives.
	AIFalseEscalationRate Rate `json:"ai_false_escalation_rate"`
	// EvidenceCompleteness is the share of closed incidents whose evidence is
	// retrievable AND passes its hash.
	EvidenceCompleteness Rate `json:"evidence_completeness"`
	// MissedIncidentRate is the share of expected incidents that were not
	// raised.
	MissedIncidentRate Rate `json:"missed_incident_rate"`
}

// NorthStar is the headline number and the four things that check it.
type NorthStar struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// Value is attention minutes per 1000 canonical events, or nil when the
	// denominator is zero.
	Value *float64 `json:"value"`
	// Numerator is the attention minutes.
	Numerator Measure `json:"numerator"`
	// Denominator is the canonical events.
	//
	// It is the denominator and not the incident count on purpose, and
	// `Validate` refuses a report whose denominator is anything else.
	Denominator Measure `json:"denominator"`
	// CapSeconds is the per-incident cap applied to the numerator, reported
	// because a capped number and an uncapped number are different numbers.
	CapSeconds float64 `json:"cap_seconds"`
	// N is the number of incidents behind the numerator.
	N int64 `json:"n"`
	// Window is the period the number covers.
	Window Window `json:"window"`
	// ComputedAt is when the number was derived.
	ComputedAt time.Time `json:"computed_at"`
	// ConfidenceInterval is the interval on the rate, or nil.
	ConfidenceInterval *Interval `json:"confidence_interval"`
	// Companions are the four metrics that check it.
	Companions Companions `json:"companions"`
}

// Report is one analytics report.
type Report struct {
	// ReportVersion is the report's own contract.
	ReportVersion string `json:"report_version"`
	// TenantID is the tenant the report is about.
	TenantID string `json:"tenant_id"`
	// GeneratedAt is when the report was built.
	GeneratedAt time.Time `json:"generated_at"`
	// Window is the period the report covers.
	Window Window `json:"window"`
	// Site is the site the report is about, or empty for every site the
	// principal may see.
	Site string `json:"site,omitempty"`
	// Class is the rule class the report is about, or empty for every class.
	Class string `json:"class,omitempty"`
	// Source is where the numbers came from. It is always `postgres`.
	Source string `json:"source"`
	// Metrics are the rolled-up numbers.
	Metrics []Measure `json:"metrics"`
	// NorthStar is the headline number, or nil when the report does not carry
	// one.
	NorthStar *NorthStar `json:"north_star,omitempty"`
}

// Reader builds reports out of the durable rollup.
type Reader struct {
	// Pool is the database handle. Required.
	Pool *dbx.Pool
	// Now is the clock. It is a field so that a test can pin it.
	Now func() time.Time
	// AttentionCap is the per-incident ceiling on attention minutes. Zero means
	// `DefaultAttentionCap`.
	AttentionCap time.Duration
	// Sites is the scope the reader may see, and it is bound onto every
	// statement it makes.
	//
	// This is the GRANT, and the site filter in a query is only a FILTER. The
	// `site_isolation` policy is RESTRICTIVE, so a reader that bound no scope
	// would see NO rows, and a reader that bound the wrong scope would see
	// another site's rows. The scope comes from the principal, and the
	// principal comes from configuration.
	//
	// An empty scope therefore reads nothing, which is the right answer: a
	// principal with no sites has no site access at all, and a misconfigured
	// assignment denies rather than opens.
	Sites []string
}

// now is the reader's clock, defaulted.
func (r *Reader) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// cap is the attention cap, defaulted.
func (r *Reader) cap() time.Duration {
	if r.AttentionCap <= 0 {
		return DefaultAttentionCap
	}
	return r.AttentionCap
}

// Read builds a report over the window.
//
// The window must be BUCKET-ALIGNED, because the rollup is stored per bucket
// and a window that cuts a bucket in half would report a fraction of a bucket
// as if it were a whole one. The reader refuses an unaligned window rather
// than quietly reporting something that is not true.
//
// The site and class are FILTERS and never grants: the query names the tenant
// on every side, and the RLS policies decide which sites the caller may see
// at all. A principal that asks for a site it is not assigned to sees nothing,
// and sees it as ABSENT rather than as zero.
func (r *Reader) Read(ctx context.Context, tenant, site, class string, w Window) (Report, error) {
	if r.Pool == nil {
		return Report{}, ErrNoPool
	}
	if tenant == "" {
		return Report{}, fmt.Errorf("operatormetrics: a report with no tenant is a report about everybody")
	}
	if !w.To.After(w.From) {
		return Report{}, fmt.Errorf("operatormetrics: the window [%s, %s) is empty",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}
	if err := r.checkAligned(w); err != nil {
		return Report{}, err
	}

	computedAt := r.now()
	report := Report{
		ReportVersion: ReportVersion,
		TenantID:      tenant,
		GeneratedAt:   computedAt,
		Window:        w,
		Site:          site,
		Class:         class,
		Source:        "postgres",
	}

	measures, err := r.readMetrics(ctx, tenant, site, class, w)
	if err != nil {
		return report, err
	}
	report.Metrics = measures
	report.NorthStar = r.northStar(measures, w, computedAt)

	// The AI false-escalation companion, measured from the observations and the
	// dispositions.
	//
	// It is measured here rather than being filled in by a caller, because a
	// companion that has to be supplied is a companion that can be forgotten, and
	// a North Star with a missing companion is a defective report.
	if report.NorthStar != nil {
		ai, err := r.AIFalseEscalationRate(ctx, tenant, w)
		if err != nil {
			return report, err
		}
		report.NorthStar.Companions.AIFalseEscalationRate = Rate{
			Metric:             metricAIFalseEscalationRate.String(),
			Value:              ai.Value,
			N:                  ai.Judged,
			Window:             w,
			ComputedAt:         computedAt,
			ConfidenceInterval: ai.CI,
		}
	}
	return report, nil
}

// bucketSize is the rollup's bucket width.
//
// It has to be a declared constant rather than a convention, because the
// reader refuses an unaligned window and a reader that guesses the width
// refuses the wrong windows.
const bucketSize = time.Hour

// checkAligned refuses a window that does not fall on bucket boundaries.
//
// A window that cuts a bucket in half would report a fraction of a bucket as
// if it were a whole one, and the error would be invisible: the number would
// look like any other number.
func (r *Reader) checkAligned(w Window) error {
	if w.From.Truncate(bucketSize).Equal(w.From) && w.To.Truncate(bucketSize).Equal(w.To) {
		return nil
	}
	return fmt.Errorf("operatormetrics: the window [%s, %s) is not on %s bucket boundaries, and a "+
		"window that cuts a bucket in half reports a fraction of a bucket as if it were a whole one",
		w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339), bucketSize)
}

// Freshest returns the start of the newest bucket the rollup holds.
//
// It answers "how old is the report", which is the question an operator asks
// when a number looks wrong: a number from a window that ended two days ago
// is not wrong, it is STALE, and the difference is worth a line of output.
//
// The zero time means the rollup holds nothing, which is a different answer
// from "the newest bucket is old" and is reported as such.
func (r *Reader) Freshest(ctx context.Context, tenant string) (time.Time, error) {
	if r.Pool == nil {
		return time.Time{}, ErrNoPool
	}
	var freshest *time.Time
	if err := r.Pool.WithSites(tenant, "metric_rollups", r.Sites).QueryRow(ctx,
		`SELECT max(bucket_start) FROM metric_rollups WHERE tenant_id = $1`,
		tenant).Scan(&freshest); err != nil {
		return time.Time{}, fmt.Errorf("operatormetrics: read the rollup's freshest bucket: %w", err)
	}
	if freshest == nil {
		return time.Time{}, nil
	}
	return freshest.UTC(), nil
}

// readMetrics reads the rolled-up numbers out of the table.
func (r *Reader) readMetrics(ctx context.Context, tenant, site, class string, w Window) ([]Measure, error) {
	rows, err := r.Pool.WithSites(tenant, "metric_rollups", r.Sites).Query(ctx, `
		SELECT metric, site_id, coalesce(rule_id, ''), value, n, computed_at
		  FROM metric_rollups
		 WHERE tenant_id = $1
		   AND bucket_start >= $2 AND bucket_start < $3
		   AND ($4 = '' OR site_id = $4)
		   AND ($5 = '' OR coalesce(rule_id, '') = $5)
		 ORDER BY metric, site_id, coalesce(rule_id, '')`,
		tenant, w.From.UTC(), w.To.UTC(), site, class)
	if err != nil {
		return nil, fmt.Errorf("operatormetrics: read the rollup: %w", err)
	}
	defer rows.Close()

	// The rows are summed per metric, because the rollup is stored at the
	// finest scope the source tables carry and the reader aggregates. A
	// tenant-wide number is the SUM of the rows a principal may see.
	sums := map[string]*Measure{}
	var order []string
	for rows.Next() {
		var (
			metric, siteID, ruleID string
			value                  float64
			n                      int64
			computed               time.Time
		)
		if err := rows.Scan(&metric, &siteID, &ruleID, &value, &n, &computed); err != nil {
			return nil, fmt.Errorf("operatormetrics: read the rollup scan: %w", err)
		}
		m, ok := sums[metric]
		if !ok {
			m = &Measure{Metric: metric, Window: w, ComputedAt: computed}
			sums[metric] = m
			order = append(order, metric)
		}
		m.Value += value
		m.N += n
		if computed.After(m.ComputedAt) {
			m.ComputedAt = computed
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("operatormetrics: read the rollup rows: %w", err)
	}

	sort.Strings(order)
	out := make([]Measure, 0, len(order))
	for _, name := range order {
		out = append(out, *sums[name])
	}
	return out, nil
}

// northStar assembles the headline number and its four companions.
//
// It returns nil when the window holds no rollup at all: a report without a
// North Star is a report about the rollup, and a report that INVENTED one would be
// worse.
func (r *Reader) northStar(measures []Measure, w Window, computedAt time.Time) *NorthStar {
	byMetric := map[string]Measure{}
	for _, m := range measures {
		byMetric[m.Metric] = m
	}

	numerator, hasNumerator := byMetric[metricAttentionMinutesCapped.String()]
	denominator, hasDenominator := byMetric[metricCanonicalEvents.String()]
	if !hasNumerator || !hasDenominator {
		// No rollup, no North Star. A report without one is a report about the
		// rollup, and a report that INVENTED one would be worse.
		return nil
	}

	ns := &NorthStar{
		Metric:      metricNorthStar.String(),
		Numerator:   numerator,
		Denominator: denominator,
		CapSeconds:  r.cap().Seconds(),
		N:           numerator.N,
		Window:      w,
		ComputedAt:  computedAt,
		Companions:  r.companions(byMetric, w, computedAt),
	}

	// The value is minutes per 1000 events, and it is nil when the
	// denominator is zero: a rate of zero out of zero events is not a rate.
	if denominator.Value > 0 {
		v := (numerator.Value / denominator.Value) * 1000
		ns.Value = &v
		lower, upper := WilsonInterval(numerator.N, int64(math.Round(denominator.Value)), 1.959963984540054)
		ns.ConfidenceInterval = &Interval{Method: "wilson", Lower: lower * 1000, Upper: upper * 1000}
	}

	return ns
}

// companions builds the four metrics that travel with the North Star.
//
// The two that are computed from the rollup are computed here. The two that
// are computed on demand -- the false-escalation rate and the missed-incident
// rate -- need a corpus and a replay, and until those exist they are
// reported as NOT MEASURED: a rate with no N and no value, which is a
// different answer from a rate of zero.
//
// A companion that is not measured is still PRESENT. It is a companion with a
// null value, and a reader can see that the product has not measured it.
func (r *Reader) companions(byMetric map[string]Measure, w Window, computedAt time.Time) Companions {
	out := Companions{
		OperatorOverrideRate:  r.overrideRate(byMetric, w, computedAt),
		AIFalseEscalationRate: unmeasuredRate(metricAIFalseEscalationRate, w, computedAt),
		EvidenceCompleteness:  unmeasuredRate(metricEvidenceCompleteness, w, computedAt),
		MissedIncidentRate:    unmeasuredRate(metricMissedIncidentRate, w, computedAt),
	}
	// The missed-incident rate, if a replay wrote one into the rollup over this
	// window.
	//
	// It is READ rather than computed here, because it is a measurement of a
	// REPLAY and not of the window: the replay ran once, over its own period,
	// and the rate belongs to that period. Asking for a window that does not
	// contain it gives no rate, and the companion says so.
	if m, ok := byMetric[metricMissedIncidentRate.String()]; ok && m.N > 0 {
		value := m.Value
		out.MissedIncidentRate = Rate{
			Metric:             metricMissedIncidentRate.String(),
			Value:              &value,
			N:                  m.N,
			Window:             w,
			ComputedAt:         computedAt,
			ConfidenceInterval: wilsonOn(value, m.N),
		}
	}
	return out
}

// wilsonOn is the interval on a proportion a rollup row carries.
//
// The row holds the VALUE and the N, and the successes are the value times
// the N, so the interval is recomputed from the pair the row actually holds.
func wilsonOn(value float64, n int64) *Interval {
	if n <= 0 {
		return nil
	}
	successes := int64(math.Round(value * float64(n)))
	lower, upper := WilsonInterval(successes, n, 1.959963984540054)
	return &Interval{Method: "wilson", Lower: lower, Upper: upper}
}

// overrideRate is the share of AI-backed dispositions where the operator
// disagreed with the model.
//
// The denominator is the dispositions that HAD an AI observation behind them:
// agreed and disagreed. `unsure` is not in it, because "unsure" is not a
// disagreement and folding it in would make the rate look better than it is.
// `no_ai_observation` is not in it either, and that is the important one: a
// site with no AI at all would otherwise report a rate of zero disagreement,
// which reads as a perfect score.
func (r *Reader) overrideRate(byMetric map[string]Measure, w Window, computedAt time.Time) Rate {
	agreed := byMetric[metricDispositionsAgreed.String()]
	disagreed := byMetric[metricDispositionsDisagreed.String()]
	total := agreed.N + disagreed.N
	rate := Rate{
		Metric:     metricOperatorOverrideRate.String(),
		N:          total,
		Window:     w,
		ComputedAt: computedAt,
	}
	if total > 0 {
		v := float64(disagreed.Value) / float64(total)
		rate.Value = &v
		lower, upper := WilsonInterval(disagreed.N, total, 1.959963984540054)
		rate.ConfidenceInterval = &Interval{Method: "wilson", Lower: lower, Upper: upper}
	}
	return rate
}

// unmeasuredRate is a companion that has not been measured.
//
// It carries the metric's name and the window, and no value. That is the
// honest shape: the product knows which companion it is and knows it has not
// measured it, and a reader can tell the difference between "not measured"
// and "measured as zero".
func unmeasuredRate(m Metric, w Window, computedAt time.Time) Rate {
	return Rate{
		Metric:     m.String(),
		N:          0,
		Window:     w,
		ComputedAt: computedAt,
	}
}

// Validate checks a report's internal consistency.
//
// It is the rule "the North Star is never reportable without all four
// companions", stated as a check on a built report rather than as a
// convention: a report that fails it does not go out.
//
// The checks are:
//   - the North Star's denominator is canonical events, and not incidents;
//   - every companion is present and names itself;
//   - a rate with a value has an N, and a rate without a value has no
//     interval;
//   - every measure names a metric that is in the closed set.
func (rep Report) Validate() error {
	for _, m := range rep.Metrics {
		if _, ok := MetricByName(m.Metric); !ok {
			return fmt.Errorf("operatormetrics: the report carries %q, which is not in the closed "+
				"metric set", m.Metric)
		}
	}
	if rep.NorthStar == nil {
		return nil
	}
	ns := rep.NorthStar
	if ns.Metric != metricNorthStar.String() {
		return fmt.Errorf("operatormetrics: the headline metric is %q, want %q",
			ns.Metric, metricNorthStar.String())
	}
	if ns.Denominator.Metric != metricCanonicalEvents.String() {
		return fmt.Errorf("operatormetrics: the North Star's denominator is %q, and it must be %q. "+
			"Incidents would improve the ratio automatically as the rules got more conservative, "+
			"which is a metric that improves when the product gets worse",
			ns.Denominator.Metric, metricCanonicalEvents.String())
	}
	if ns.Numerator.Metric != metricAttentionMinutesCapped.String() {
		return fmt.Errorf("operatormetrics: the North Star's numerator is %q, want %q",
			ns.Numerator.Metric, metricAttentionMinutesCapped.String())
	}
	for name, c := range map[string]Rate{
		"operator_override_rate":   ns.Companions.OperatorOverrideRate,
		"ai_false_escalation_rate": ns.Companions.AIFalseEscalationRate,
		"evidence_completeness":    ns.Companions.EvidenceCompleteness,
		"missed_incident_rate":     ns.Companions.MissedIncidentRate,
	} {
		if c.Metric == "" {
			return fmt.Errorf("operatormetrics: the companion %q is missing from the report. The "+
				"North Star is never reportable without all four", name)
		}
		if c.Metric != name {
			return fmt.Errorf("operatormetrics: the companion %q names itself %q", name, c.Metric)
		}
		if c.Value != nil && c.N <= 0 {
			return fmt.Errorf("operatormetrics: the companion %q has a value and no N. A rate "+
				"without its denominator is a number nobody can check", name)
		}
		if c.Value == nil && c.ConfidenceInterval != nil {
			return fmt.Errorf("operatormetrics: the companion %q has an interval and no value", name)
		}
	}
	return nil
}
