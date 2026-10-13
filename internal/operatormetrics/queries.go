package operatormetrics

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// Queries runs the durable metric computations. It holds a pool and nothing
// else: every method takes the tenant explicitly, because a metric that
// defaults to a tenant is a metric that eventually reads somebody else's.
type Queries struct {
	Pool *dbx.Pool
}

// DefaultAttentionCap is the per-incident ceiling on attention minutes.
//
// The metrics doc is explicit that this number is PROPOSED and "to be set from
// the M1 distribution, not before": four hours is the placeholder that makes the
// cap real enough to compute, and the comment is here so that nobody mistakes a
// declared placeholder for a measured one. Both the capped and the uncapped
// number are reported, so replacing this constant changes a number an operator
// can already see without it.
const DefaultAttentionCap = 4 * time.Hour

// ErrNoPool is returned when a Queries value was built without a pool, rather
// than panicking on the first call. A metrics caller that forgot its dependency
// is a configuration error and should be reported as one.
var ErrNoPool = errors.New("operatormetrics: no pool")

// ---------------------------------------------------------------------------
// attention_minutes ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â the North Star numerator
// ---------------------------------------------------------------------------

// IncidentAttention is one incident's human attention, in seconds.
type IncidentAttention struct {
	IncidentID string `json:"incident_id"`
	RuleID     string `json:"rule_id"`
	// UncappedSeconds is elapsed time from the first authenticated read to the
	// disposition. It is an over-count on purpose: it includes the operator who
	// opened an incident before going to lunch.
	UncappedSeconds float64 `json:"uncapped_seconds"`
	// CappedSeconds is the same number with DefaultAttentionCap applied.
	CappedSeconds float64 `json:"capped_seconds"`
	// Capped reports whether the cap actually bit. A capped incident is one
	// where the uncapped number is known to be wrong, which is different from
	// one where it is right.
	Capped bool `json:"capped"`
	// Dispositioned is false for an incident still holding attention with no
	// verdict yet. It is reported rather than dropped, because "the operator is
	// still looking at it" is a different fact from "nobody looked".
	Dispositioned bool `json:"dispositioned"`
}

// AttentionReport is the window's total.
type AttentionReport struct {
	From            time.Time           `json:"from,omitempty"`
	To              time.Time           `json:"to,omitempty"`
	Incidents       []IncidentAttention `json:"incidents"`
	UncappedSeconds float64             `json:"uncapped_seconds"`
	CappedSeconds   float64             `json:"capped_seconds"`
	CappedCount     int                 `json:"capped_count"`
	OpenCount       int                 `json:"open_count"`
	Samples         int                 `json:"samples"`
}

// Attention computes attention minutes per incident over the window.
//
// attention_minutes(incident) = dispositions.created_at ÃƒÆ’Ã‚Â¢Ãƒâ€¹Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ min(audit_ledger.ts
// WHERE action = 'incident.opened' AND target_id = incident_id), capped at the
// declared maximum, with BOTH the capped and the uncapped value reported.
//
// The two numbers are the point of the metric rather than a convenience. The
// uncapped number is an over-count and says so; the capped one is a floor-ish
// approximation of human time and is right more often than it is wrong. A single
// number here would be the one that flatters whichever choice its author made.
//
// An incident with no `incident.opened` row is not counted at all ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â not as zero.
// A zero would say "nobody opened this incident", and the truth is that this
// build predates the producer or the session is older than the ledger. Zero and
// unknown are different answers, and an unmeasured incident here is the second.
func (q *Queries) Attention(ctx context.Context, tenant string, from, to time.Time, capAt time.Duration) (AttentionReport, error) {
	if q.Pool == nil {
		return AttentionReport{}, ErrNoPool
	}
	if capAt <= 0 {
		capAt = DefaultAttentionCap
	}
	report := AttentionReport{From: from, To: to}

	// Every predicate names the tenant, and that is not belt-and-braces. Row-level
	// security is the backstop and not the filter: the test suite connects as a
	// superuser, which PostgreSQL exempts from every policy, so a query that
	// relies on the session's tenant setting alone returns every tenant's rows the
	// moment anybody runs it outside the application's own role. A report that can
	// leak another tenant's attention minutes because of who ran the query is a
	// report that will.
	rows, err := q.Pool.For(tenant, "incidents").Query(ctx, `
		SELECT i.incident_id,
		       i.rule_id,
		       (SELECT min(l.ts) FROM audit_ledger l
		         WHERE l.tenant_id = i.tenant_id
		           AND l.target_id = i.incident_id
		           AND l.action = 'incident.opened'
		           AND l.ts >= $2 AND l.ts <= $3) AS opened_at,
		       (SELECT min(d.created_at) FROM dispositions d
		         WHERE d.tenant_id = i.tenant_id
		           AND d.incident_id = i.incident_id
		           AND d.created_at >= $2 AND d.created_at <= $3) AS dispositioned_at
		  FROM incidents i
		 WHERE i.tenant_id = $1
		   AND i.created_at >= $2 AND i.created_at <= $3
		 ORDER BY i.incident_id`, tenant, from, to)
	if err != nil {
		return report, fmt.Errorf("operatormetrics: attention: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			item        IncidentAttention
			openedAt    *time.Time
			disposition *time.Time
		)
		if err := rows.Scan(&item.IncidentID, &item.RuleID, &openedAt, &disposition); err != nil {
			return report, fmt.Errorf("operatormetrics: attention scan: %w", err)
		}
		if openedAt == nil {
			// No producer row: this incident's attention was never recorded.
			// Counted in no total, and named in the report's comment rather
			// than folded in as a zero.
			continue
		}
		if disposition == nil {
			// The operator opened it and has not answered yet. Counted as open
			// and excluded from every total: a window's attention minutes do not
			// include an incident still in progress, and reporting it as 0 would
			// say "this cost nothing" about an incident still costing.
			report.OpenCount++
			report.Incidents = append(report.Incidents, item)
			continue
		}
		item.Dispositioned = true
		item.UncappedSeconds = disposition.Sub(*openedAt).Seconds()
		if item.UncappedSeconds < 0 {
			// A disposition timestamped before the read that opened the
			// incident is a clock event, not an attention measurement.
			item.UncappedSeconds = 0
		}
		if item.UncappedSeconds > capAt.Seconds() {
			item.Capped = true
			item.CappedSeconds = capAt.Seconds()
			report.CappedCount++
		} else {
			item.CappedSeconds = item.UncappedSeconds
		}
		report.Incidents = append(report.Incidents, item)
		report.Samples++
		report.UncappedSeconds += item.UncappedSeconds
		report.CappedSeconds += item.CappedSeconds
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("operatormetrics: attention rows: %w", err)
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// mt_ti_seconds ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â reported as three numbers, never one
// ---------------------------------------------------------------------------

// LatencyClass is one rule class's MTTI, in the three parts the metrics doc
// defines.
type LatencyClass struct {
	RuleID string
	// DetectionSeconds is (a): incidents.created_at ÃƒÆ’Ã‚Â¢Ãƒâ€¹Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ incidents.first_event_at.
	DetectionSeconds float64
	// InvestigationSeconds is (b): min(dispositions.created_at) ÃƒÆ’Ã‚Â¢Ãƒâ€¹Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ incidents.created_at.
	InvestigationSeconds float64
	// MTTISeconds is (c) = (a) + (b). Reported, but never alone.
	MTTISeconds float64
}

// LatencyReport aggregates the three parts per class, as mean and median.
type LatencyReport struct {
	From    time.Time               `json:"from,omitempty"`
	To      time.Time               `json:"to,omitempty"`
	Classes []LatencyClassAggregate `json:"classes"`
	Samples int                     `json:"samples"`
}

// LatencyClassAggregate is one class's aggregate. Median is the headline; the
// mean sits beside it, not above it.
type LatencyClassAggregate struct {
	RuleID              string  `json:"rule_id"`
	Samples             int     `json:"samples"`
	MeanMTTI            float64 `json:"mean_mtti_seconds"`
	MedianMTTI          float64 `json:"median_mtti_seconds"`
	MeanDetection       float64 `json:"mean_detection_seconds"`
	MedianDetection     float64 `json:"median_detection_seconds"`
	MeanInvestigation   float64 `json:"mean_investigation_seconds"`
	MedianInvestigation float64 `json:"median_investigation_seconds"`
}

// MTTI computes time-to-investigate per rule class over incidents dispositioned
// in the window.
//
// The three parts are the whole definition: a single number that collapsed
// detection latency and investigation latency into "how long did this take"
// would hide the only thing an operator can act on. Long detection latency is a
// pipeline problem; long investigation latency is an interface problem. They
// have different remedies, and a mean of the two has neither.
//
// The median is the headline because a p95 of a handful of incidents is a number
// with no confidence in it, and the mean is reported beside it so a reader can
// see the skew rather than trust whichever one is quoted.
func (q *Queries) MTTI(ctx context.Context, tenant string, from, to time.Time) (LatencyReport, error) {
	if q.Pool == nil {
		return LatencyReport{}, ErrNoPool
	}
	report := LatencyReport{From: from, To: to}

	// The tenant is a predicate on both sides of the join, not just the driving
	// table: a subquery without it would join a disposition from any tenant
	// against this tenant's incident on the incident_id alone, and the two ids
	// are unique per tenant rather than globally.
	rows, err := q.Pool.For(tenant, "incidents").Query(ctx, `
		SELECT i.rule_id,
		       extract(epoch FROM (i.created_at - i.first_event_at)),
		       extract(epoch FROM (d.first_disposition - i.created_at))
		  FROM incidents i
		  JOIN (SELECT dd.tenant_id, dd.incident_id, min(dd.created_at) AS first_disposition
		          FROM dispositions dd GROUP BY dd.tenant_id, dd.incident_id) d
		    ON d.tenant_id = i.tenant_id
		   AND d.incident_id = i.incident_id
		 WHERE i.tenant_id = $1
		   AND i.created_at >= $2 AND i.created_at <= $3
		 ORDER BY i.rule_id, i.incident_id`, tenant, from, to)
	if err != nil {
		return report, fmt.Errorf("operatormetrics: mtti: %w", err)
	}
	defer rows.Close()

	byClass := map[string][]LatencyClass{}
	for rows.Next() {
		var c LatencyClass
		if err := rows.Scan(&c.RuleID, &c.DetectionSeconds, &c.InvestigationSeconds); err != nil {
			return report, fmt.Errorf("operatormetrics: mtti scan: %w", err)
		}
		if c.DetectionSeconds < 0 {
			c.DetectionSeconds = 0
		}
		if c.InvestigationSeconds < 0 {
			c.InvestigationSeconds = 0
		}
		c.MTTISeconds = c.DetectionSeconds + c.InvestigationSeconds
		byClass[c.RuleID] = append(byClass[c.RuleID], c)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("operatormetrics: mtti rows: %w", err)
	}

	ids := make([]string, 0, len(byClass))
	for id := range byClass {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		items := byClass[id]
		detection := make([]float64, 0, len(items))
		investigation := make([]float64, 0, len(items))
		total := make([]float64, 0, len(items))
		for _, it := range items {
			detection = append(detection, it.DetectionSeconds)
			investigation = append(investigation, it.InvestigationSeconds)
			total = append(total, it.MTTISeconds)
		}
		agg := LatencyClassAggregate{
			RuleID:              id,
			Samples:             len(items),
			MeanMTTI:            Mean(total),
			MedianMTTI:          Median(total),
			MeanDetection:       Mean(detection),
			MedianDetection:     Median(detection),
			MeanInvestigation:   Mean(investigation),
			MedianInvestigation: Median(investigation),
		}
		report.Classes = append(report.Classes, agg)
		report.Samples += len(items)
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// mt_tv_seconds ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â the proposed definition, marked as proposed
// ---------------------------------------------------------------------------

// MTTVReport is time-to-verify, from the fact existing to the verdict on it.
type MTTVReport struct {
	From          time.Time `json:"from,omitempty"`
	To            time.Time `json:"to,omitempty"`
	MedianSeconds float64   `json:"median_seconds"`
	P95Seconds    float64   `json:"p95_seconds"`
	Samples       int       `json:"samples"`
}

// MTTV computes verification latency per window: verification_results.evaluated_at
// ÃƒÆ’Ã‚Â¢Ãƒâ€¹Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ canonical_events.normalized_at, for the trigger event.
//
// THE NAME IS A PROPOSAL, NOT A DECISION. The metrics doc says so in as many
// words: "`mt_tv_seconds` ... PROPOSED definition, and a name the roadmap never
// expands ... **E1 must confirm or correct this before M2 opens.**" This
// implements the proposal ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â Mean Time To Verify ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â because computing something is
// the only way to find out what the number looks like, and the comment is the
// record that a human still owns the name. If it turns out to mean Mean Time To
// Respond, only the source column changes and nothing here does.
func (q *Queries) MTTV(ctx context.Context, tenant string, from, to time.Time) (MTTVReport, error) {
	if q.Pool == nil {
		return MTTVReport{}, ErrNoPool
	}
	report := MTTVReport{From: from, To: to}

	rows, err := q.Pool.For(tenant, "verification_results").Query(ctx, `
		SELECT extract(epoch FROM (v.evaluated_at - e.normalized_at))
		  FROM verification_results v
		  JOIN canonical_events e
		    ON e.tenant_id = v.tenant_id
		   AND e.event_id = v.event_id
		 WHERE v.tenant_id = $1
		   AND v.event_id IS NOT NULL
		   AND v.evaluated_at >= $2 AND v.evaluated_at <= $3`, tenant, from, to)
	if err != nil {
		return report, fmt.Errorf("operatormetrics: mttv: %w", err)
	}
	defer rows.Close()

	values := []float64{}
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return report, fmt.Errorf("operatormetrics: mttv scan: %w", err)
		}
		if v >= 0 {
			values = append(values, v)
		}
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("operatormetrics: mttv rows: %w", err)
	}
	if len(values) == 0 {
		return report, nil
	}
	report.Samples = len(values)
	report.MedianSeconds = Median(values)
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	report.P95Seconds = sorted[int(0.95*float64(len(sorted)-1))]
	return report, nil
}

// ---------------------------------------------------------------------------
// the two silent verification states
// ---------------------------------------------------------------------------

// StateCounts is the verification-state distribution over a window.
type StateCounts struct {
	From    time.Time        `json:"from,omitempty"`
	To      time.Time        `json:"to,omitempty"`
	ByState map[string]int64 `json:"by_state"`
	Total   int64            `json:"total"`
}

// VerificationStates counts verification_results by state.
//
// It exists because the live counters covered three of the five states and the
// two it omitted are the ones an operator asks about when they suspect the rules
// are eating incidents: `duplicate` is ingest working correctly and `suppressed`
// is a rule working exactly as its author intended. Neither had a durable number,
// so "how often does this system deliberately stay quiet" was unanswerable over
// any window longer than the process's lifetime.
func (q *Queries) VerificationStates(ctx context.Context, tenant string, from, to time.Time) (StateCounts, error) {
	if q.Pool == nil {
		return StateCounts{}, ErrNoPool
	}
	counts := StateCounts{From: from, To: to, ByState: map[string]int64{}}
	rows, err := q.Pool.For(tenant, "verification_results").Query(ctx, `
		SELECT state, count(*) FROM verification_results
		 WHERE tenant_id = $1
		   AND evaluated_at >= $2 AND evaluated_at <= $3
		 GROUP BY state ORDER BY state`, tenant, from, to)
	if err != nil {
		return counts, fmt.Errorf("operatormetrics: verification states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return counts, fmt.Errorf("operatormetrics: verification states scan: %w", err)
		}
		counts.ByState[state] = n
		counts.Total += n
	}
	if err := rows.Err(); err != nil {
		return counts, fmt.Errorf("operatormetrics: verification states rows: %w", err)
	}
	return counts, nil
}

// EvidenceFetchFailures counts dead-lettered evidence-fetch jobs in the window.
//
// This is the durable source of `evidence_fetch_failed_total`, and it is a
// PROXY, which the metrics doc says in the same breath: there is no per-attempt
// failure table, and ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â§7.2 proposes one as a jobs-derived query rather than a new
// table. This is that query.
//
// A proxy has to be labelled one. It counts attempts that reached the dead letter
// ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â a fetch that failed four times and then succeeded is not counted at all, so
// this number is a floor on evidence trouble, never a measurement of it. The
// live counter is the other half: see `internal/observability.CounterEvidenceFailed`.
func (q *Queries) EvidenceFetchFailures(ctx context.Context, tenant string, from, to time.Time) (int64, error) {
	if q.Pool == nil {
		return 0, ErrNoPool
	}
	// completed_at, not created_at, and not dead_lettered_at: the jobs table
	// stamps completed_at on the transition INTO dead-letter
	// (`internal/jobs/jobs.go:417`), which is the moment the question "did this
	// fetch fail" was answered. Counting created_at would window the attempt and
	// not the failure.
	var n int64
	err := q.Pool.For(tenant, "jobs").QueryRow(ctx, `
		SELECT count(*) FROM jobs
		 WHERE tenant_id = $1
		   AND queue = 'evidence_fetch'
		   AND status = 'dead_letter'
		   AND completed_at >= $2 AND completed_at <= $3`, tenant, from, to).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("operatormetrics: evidence fetch failures: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// the per-class, per-site slice: WHERE does the system stay quiet
// ---------------------------------------------------------------------------

// StateSlice is one (site, rule class) cell's state distribution.
type StateSlice struct {
	SiteID  string           `json:"site_id"`
	RuleID  string           `json:"rule_id"`
	ByState map[string]int64 `json:"by_state"`
	Total   int64            `json:"total"`
	// Silent is duplicate+suppressed: the decisions that deliberately produced
	// no incident. Reported beside Total rather than inside it, because a rule
	// with a high silent share is either suppressing well or misconfigured, and
	// the number does not say which.
	Silent int64 `json:"silent"`
}

// StateSliceReport is the window's per-cell distribution, ordered.
type StateSliceReport struct {
	From  time.Time    `json:"from,omitempty"`
	To    time.Time    `json:"to,omitempty"`
	Cells []StateSlice `json:"cells"`
	// TotalSilent and TotalDecided are the window's own halves, so a reader can
	// see the aggregate before reading any cell.
	TotalSilent  int64 `json:"total_silent"`
	TotalDecided int64 `json:"total_decided"`
}

// VerificationStatesBySiteAndClass breaks the distribution down to the level a
// reader acts on.
//
// The aggregate from `VerificationStates` answers "how often does this system
// stay quiet". This answers the only follow-up that matters: "WHERE". A rule
// suppressing 60% of its decisions at one site and 2% at another is a
// configuration question about that site, and an aggregate hides it completely —
// the two figures average into a plausible total that describes neither.
//
// The cell's rule class comes from verification_results and its site from the
// incident the verification belongs to, joined on the tenant and the incident id.
// A verification row with no incident is not a cell: it belongs to no site, and
// inventing a site for it would put a number somewhere an operator would go
// looking.
func (q *Queries) VerificationStatesBySiteAndClass(ctx context.Context, tenant string, from, to time.Time) (StateSliceReport, error) {
	if q.Pool == nil {
		return StateSliceReport{}, ErrNoPool
	}
	report := StateSliceReport{From: from, To: to}
	rows, err := q.Pool.For(tenant, "verification_results").Query(ctx, `
		SELECT i.primary_site_id,
		       v.rule_id,
		       v.state,
		       count(*)
		  FROM verification_results v
		  JOIN incidents i
		    ON i.tenant_id = v.tenant_id
		   AND i.incident_id = v.incident_id
		 WHERE v.tenant_id = $1
		   AND v.evaluated_at >= $2 AND v.evaluated_at <= $3
		 GROUP BY i.primary_site_id, v.rule_id, v.state
		 ORDER BY i.primary_site_id, v.rule_id, v.state`, tenant, from, to)
	if err != nil {
		return report, fmt.Errorf("operatormetrics: verification states by site and class: %w", err)
	}
	defer rows.Close()

	byCell := map[string]*StateSlice{}
	order := []string{}
	for rows.Next() {
		var site, ruleID, state string
		var n int64
		if err := rows.Scan(&site, &ruleID, &state, &n); err != nil {
			return report, fmt.Errorf("operatormetrics: verification states by site and class scan: %w", err)
		}
		key := site + "\x00" + ruleID
		cell, ok := byCell[key]
		if !ok {
			cell = &StateSlice{SiteID: site, RuleID: ruleID, ByState: map[string]int64{}}
			byCell[key] = cell
			order = append(order, key)
		}
		cell.ByState[state] += n
		cell.Total += n
		if state == "duplicate" || state == "suppressed" {
			cell.Silent += n
			report.TotalSilent += n
		} else {
			report.TotalDecided += n
		}
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("operatormetrics: verification states by site and class rows: %w", err)
	}
	for _, key := range order {
		report.Cells = append(report.Cells, *byCell[key])
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// the helpers
// ---------------------------------------------------------------------------

// Median returns the median, or 0 for an empty slice.
//
// The 0 and the doc comment are deliberate. A median over nothing is unknown, and
// an unknown is not a zero ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â but this helper returns a number to a caller that
// has already decided the population is non-empty, and every caller here checks
// its own sample count first. The alternative, an error or a -1, would push the
// "did we measure anything" decision into the arithmetic where nobody reads it.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Mean returns the arithmetic mean, or 0 for an empty slice. See Median for why
// the empty case is a zero and not an error.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
