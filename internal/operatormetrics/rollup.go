package operatormetrics

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The rollup writer.
//
// # What this is
//
// A DERIVATION, not a recording of a moment.
//
// Every number it writes is a Postgres computation over the tables the product
// already writes, and every number can be re-derived from those tables at any
// time. That is the property that makes the analytics trustworthy: a report
// signed by somebody survives a restart, survives a rebuild, and can be checked
// by anybody who can run the query.
//
// # Idempotence, and why it is the whole design
//
// Running the writer twice over the same bucket produces the same rows. The
// write is an `INSERT ... ON CONFLICT DO UPDATE` keyed on
// `(tenant_id, metric, bucket_start, site_id, scope_key)`, and the value it
// writes is a function of the bucket and the source tables alone -- never of
// the clock, never of what the writer believed last time, and never of the
// order the metrics happen to be computed in.
//
// That is what makes `securityctl analytics rebuild` a repair path rather than
// a second source of truth. It re-derives a window and the result is the same
// one, or a better one, and the reader cannot tell which happened.
//
// # The scope a row is stored at
//
// The FINEST scope the source tables carry, and the reader aggregates.
//
// `canonical_events` is stored per device, because the M1 gate 5 ratio is per
// camera. `incidents_raised` and the verification states are stored per rule,
// because the M3 and M6 gates need a per-class number. The attention minutes
// are per rule for the same reason.
//
// There is no "all classes" row and no "all devices" row, and there is no
// tenant-wide row either. A tenant-wide number is the SUM of the rows a
// principal may see, computed by the reader, because a row with a NULL site
// would be visible to a principal assigned to one site -- `app_site_visible`
// is TRUE for NULL. That is the reason `metric_rollups.site_id` is NOT NULL,
// and this writer is the other end of it: it never writes a row it could not
// attribute to a site.
//
// # What is NOT rolled up
//
// MTTI and MTTV. A median of a histogram is not a median, so they are computed
// on demand from `incidents` and `dispositions` by `Queries.MTTI` and
// `Queries.MTTV`, and the report says which is which beside the number.

// Bucket is the window one rollup covers.
//
// It is half-open: [Start, End). A bucket that closed at an instant would count
// its own end twice across two buckets, and the count would be wrong by one
// event per bucket rather than obviously wrong.
type Bucket struct {
	Start time.Time
	End   time.Time
}

// Valid reports whether the bucket is a non-empty interval.
func (b Bucket) Valid() bool {
	return !b.Start.IsZero() && b.End.After(b.Start)
}

// Measurement is one row of the rollup, before it is written.
type Measurement struct {
	// Metric is the closed-set member this row is for.
	Metric Metric
	// SiteID is never empty. A row with no site is a tenant-wide row, and the
	// table cannot hold one.
	SiteID string
	// RuleID is empty for "every class".
	RuleID string
	// DeviceID is empty for "every device".
	DeviceID string
	// Value is the measured number.
	Value float64
	// N is how many things the value counts.
	N int64
}

// RollupResult is what one write did.
type RollupResult struct {
	// Metrics is how many distinct metrics were written.
	Metrics int
	// Rows is how many rows were written.
	Rows int
	// Skipped is how many source rows could not be attributed to a site and
	// were therefore not written.
	//
	// It is reported rather than dropped silently. A metric that quietly loses
	// rows is worse than one that says it lost them, because the reader cannot
	// tell a small number from a wrong one.
	Skipped int
}

// Rollup writes the durable metric rollup.
type Rollup struct {
	// Pool is the database handle. Required.
	Pool *dbx.Pool
	// Now is the clock. It is a field so that a test can pin it and so that
	// `computed_at` is the writer's own notion of now rather than the
	// database's.
	Now func() time.Time
	// AttentionCap is the per-incident ceiling on attention minutes. Zero means
	// `DefaultAttentionCap`.
	AttentionCap time.Duration
	// SiteID is the site this derivation is FOR.
	//
	// It is required, and it is bound onto every statement the derivation
	// makes, because `metric_rollups.site_id` is NOT NULL and the
	// `site_isolation` policy is RESTRICTIVE. A derivation that bound no scope
	// would see no rows and write no rows, and it would do so SILENTLY: the
	// test suite connects as a superuser, which bypasses the policy, so a
	// derivation that cannot see anything still passes every test it has.
	//
	// The product is one tenant, one site, one process, so the site is the
	// install's own and it comes from configuration like everything else.
	SiteID string
}

// now is the writer's clock, defaulted.
func (r *Rollup) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// cap is the attention cap, defaulted.
func (r *Rollup) cap() time.Duration {
	if r.AttentionCap <= 0 {
		return DefaultAttentionCap
	}
	return r.AttentionCap
}

// Write computes and stores every rolled-up metric for one bucket.
//
// It is idempotent: the same bucket over the same tables produces the same
// rows, and writing twice leaves one copy of each.
func (r *Rollup) Write(ctx context.Context, tenant string, b Bucket) (RollupResult, error) {
	var out RollupResult
	if r.Pool == nil {
		return out, ErrNoPool
	}
	if !b.Valid() {
		return out, fmt.Errorf("operatormetrics: rollup bucket [%s, %s) is not a non-empty interval",
			b.Start.UTC().Format(time.RFC3339), b.End.UTC().Format(time.RFC3339))
	}
	if tenant == "" {
		return out, fmt.Errorf("operatormetrics: rollup has no tenant, and a metric with no tenant " +
			"is a metric about everybody")
	}
	if r.SiteID == "" {
		return out, fmt.Errorf("operatormetrics: rollup has no site. The rollup table is " +
			"site-scoped, so a derivation with no site would read nothing and write nothing, " +
			"and the report would say the site was never measured")
	}

	computedAt := r.now()
	rows, err := r.compute(ctx, tenant, b)
	if err != nil {
		return out, err
	}
	out.Rows = len(rows)
	seen := map[Metric]bool{}
	for _, m := range rows {
		seen[m.Metric] = true
	}
	out.Metrics = len(seen)

	for _, m := range rows {
		if err := r.upsert(ctx, tenant, b, m, computedAt); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Derive computes the metrics for a window from the RAW rows, without writing.
//
// It is the DRILL-DOWN: the same computation the writer does, run on demand for
// one window, so a reader who does not believe the rollup can check it.
//
// The number it produces and the number the rollup holds are the same
// computation over the same rows, so a reader that finds them different has
// found something: a bucket that was derived before the rows it summarises
// existed, which is the one way a rollup can be wrong.
//
// It is a re-derivation and not a read, and that is the cost: it scans the
// source tables for the window. A drill-down over an hour is cheap; a
// drill-down over a year is a year of scanning, and the route that carries it
// says so.
func (r *Rollup) Derive(ctx context.Context, tenant string, b Bucket) ([]Measurement, error) {
	if r.Pool == nil {
		return nil, ErrNoPool
	}
	if !b.Valid() {
		return nil, fmt.Errorf("operatormetrics: drilldown bucket [%s, %s) is not a non-empty interval",
			b.Start.UTC().Format(time.RFC3339), b.End.UTC().Format(time.RFC3339))
	}
	if r.SiteID == "" {
		return nil, fmt.Errorf("operatormetrics: drilldown has no site. The rollup table is " +
			"site-scoped, so a derivation with no site would read nothing and write nothing")
	}
	return r.compute(ctx, tenant, b)
}

// upsert writes one measurement.
//
// The conflict target is the primary key, which is the whole table's
// uniqueness: `(tenant_id, metric, bucket_start, site_id, scope_key)`. The
// `scope_key` is GENERATED, so it is not written -- writing it would be
// writing a derived column by hand, and the database would refuse.
func (r *Rollup) upsert(ctx context.Context, tenant string, b Bucket, m Measurement, computedAt time.Time) error {
	var ruleID, deviceID *string
	if m.RuleID != "" {
		ruleID = &m.RuleID
	}
	if m.DeviceID != "" {
		deviceID = &m.DeviceID
	}
	_, err := r.Pool.WithSites(tenant, "metric_rollups", []string{m.SiteID}).Exec(ctx, `
		INSERT INTO metric_rollups
			(tenant_id, metric, bucket_start, bucket_end, site_id, rule_id, device_id,
			 value, n, numerator, denominator, computed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULL, NULL, $10)
		ON CONFLICT (tenant_id, metric, bucket_start, site_id, scope_key)
		DO UPDATE SET
			bucket_end = EXCLUDED.bucket_end,
			value = EXCLUDED.value,
			n = EXCLUDED.n,
			computed_at = EXCLUDED.computed_at`,
		tenant, m.Metric.String(), b.Start.UTC(), b.End.UTC(), m.SiteID, ruleID, deviceID,
		m.Value, m.N, computedAt)
	if err != nil {
		return fmt.Errorf("operatormetrics: rollup write %s for site %s: %w", m.Metric, m.SiteID, err)
	}
	return nil
}

// computation is one metric's SQL.
//
// The metric name comes from the SQL, and the Go side looks it up in the closed
// set and REFUSES a name it does not know. That is deliberate: a metric name
// typed into a query is a name the closed set has to catch, and a name the set
// does not hold is a bug that must fail loudly at the write rather than
// produce a row the database's CHECK constraint rejects later.
type computation struct {
	// sql computes the rows. It takes the tenant and the bucket's bounds and
	// returns (metric, site_id, rule_id, device_id, value, n).
	sql string
	// usesCap says the statement takes the attention cap as a fourth argument.
	//
	// It is a flag rather than an argument list because the cap is a property
	// of the WRITER, not of the computation: a statement that took it as a
	// literal would be a statement whose meaning depended on a constant
	// somebody edited.
	usesCap bool
}

// computations is the set of rolled-up metrics.
//
// Every statement names the tenant on both sides of every join, for the same
// reason the rest of this package does: row-level security is the backstop and
// not the filter, and the test suite connects as a superuser, which
// PostgreSQL exempts from every policy.
var computations = []computation{
	// The North Star's denominator, per device, because the M1 gate 5 ratio is
	// per camera.
	{sql: `
		SELECT 'canonical_events' AS metric, ce.site_id, NULL::text AS rule_id, ce.device_id,
		       count(*)::float8 AS value, count(*)::bigint AS n
		  FROM canonical_events ce
		 WHERE ce.tenant_id = $1
		   AND ce.normalized_at >= $2 AND ce.normalized_at < $3
		 GROUP BY ce.site_id, ce.device_id`},

	// The numerator's other half, per rule.
	{sql: `
		WITH opened AS (
		    SELECT i.incident_id, i.primary_site_id AS site_id, i.rule_id,
		           min(l.ts) AS opened_at
		      FROM incidents i
		      JOIN audit_ledger l
		        ON l.tenant_id = i.tenant_id
		       AND l.target_id = i.incident_id
		       AND l.action = 'incident.opened'
		     WHERE i.tenant_id = $1
		       AND i.created_at >= $2 AND i.created_at < $3
		       AND l.ts >= $2 AND l.ts < $3
		     GROUP BY i.incident_id, i.primary_site_id, i.rule_id
		), closed AS (
		    SELECT i.incident_id, min(d.created_at) AS dispositioned_at
		      FROM incidents i
		      JOIN dispositions d
		        ON d.tenant_id = i.tenant_id
		       AND d.incident_id = i.incident_id
		     WHERE i.tenant_id = $1
		       AND i.created_at >= $2 AND i.created_at < $3
		       AND d.created_at >= $2 AND d.created_at < $3
		     GROUP BY i.incident_id
		), per_incident AS (
		    SELECT o.site_id, o.rule_id,
		           GREATEST(EXTRACT(EPOCH FROM (c.dispositioned_at - o.opened_at)), 0) AS seconds
		      FROM opened o
		      JOIN closed c ON c.incident_id = o.incident_id
		)
		SELECT 'attention_minutes_capped' AS metric, site_id, rule_id, NULL::text AS device_id,
		       sum(LEAST(seconds, $4))::float8 / 60.0 AS value, count(*)::bigint AS n
		  FROM per_incident GROUP BY site_id, rule_id
		UNION ALL
		SELECT 'attention_minutes_uncapped', site_id, rule_id, NULL::text,
		       sum(seconds)::float8 / 60.0, count(*)::bigint
		  FROM per_incident GROUP BY site_id, rule_id
		UNION ALL
		SELECT 'attention_incidents', site_id, rule_id, NULL::text,
		       count(*)::float8, count(*)::bigint
		  FROM per_incident GROUP BY site_id, rule_id`,
		usesCap: true},

	// M1 gate 5's numerator: incidents raised, per site and per rule.
	{sql: `
		SELECT 'incidents_raised' AS metric, i.primary_site_id AS site_id, i.rule_id,
		       NULL::text AS device_id, count(*)::float8 AS value, count(*)::bigint AS n
		  FROM incidents i
		 WHERE i.tenant_id = $1
		   AND i.created_at >= $2 AND i.created_at < $3
		 GROUP BY i.primary_site_id, i.rule_id`},

	// All five verification states, not three. The two that are missing from
	// the live counters are DUPLICATE and SUPPRESSED, and they are the two an
	// operator asks about when they suspect the rules are eating incidents.
	//
	// The states are spelled out rather than concatenated, and the ELSE
	// produces a name that is NOT in the vocabulary. That is deliberate: a new
	// verification state would otherwise be counted as one of the five, and
	// the report would be quietly wrong. With the ELSE as it is, the write
	// fails the database's CHECK constraint and the failure says which name
	// is not a metric.
	{sql: `
		SELECT CASE v.state
		         WHEN 'corroborated' THEN 'verification_corroborated'
		         WHEN 'unverified'   THEN 'verification_unverified'
		         WHEN 'contradicted' THEN 'verification_contradicted'
		         WHEN 'duplicate'    THEN 'verification_duplicate'
		         WHEN 'suppressed'   THEN 'verification_suppressed'
		         ELSE 'verification_state_not_in_the_vocabulary'
		       END AS metric,
		       i.primary_site_id AS site_id, v.rule_id,
		       NULL::text AS device_id, count(*)::float8 AS value, count(*)::bigint AS n
		  FROM verification_results v
		  JOIN incidents i
		    ON i.tenant_id = v.tenant_id
		   AND i.incident_id = v.incident_id
		 WHERE v.tenant_id = $1
		   AND v.evaluated_at >= $2 AND v.evaluated_at < $3
		 GROUP BY v.state, i.primary_site_id, v.rule_id`},

	// The override-rate numerator and its siblings, per site and per rule.
	//
	// The agreement vocabulary is spelled out rather than concatenated,
	// because the column's value is `no_ai_observation` and the metric's name
	// is `dispositions_no_ai`, and a concatenation would have to be wrong in a
	// way the CHECK constraint would catch only at the write. The ELSE is the
	// same fail-closed shape as the verification states above: a new agreement
	// value refuses the write rather than being counted as one of the four.
	{sql: `
		SELECT CASE d.agreement
		         WHEN 'no_ai_observation' THEN 'dispositions_no_ai'
		         WHEN 'agreed'            THEN 'dispositions_agreed'
		         WHEN 'disagreed'         THEN 'dispositions_disagreed'
		         WHEN 'unsure'            THEN 'dispositions_unsure'
		         ELSE 'disposition_agreement_not_in_the_vocabulary'
		       END AS metric,
		       i.primary_site_id AS site_id, i.rule_id, NULL::text AS device_id,
		       count(*)::float8 AS value, count(*)::bigint AS n
		  FROM dispositions d
		  JOIN incidents i
		    ON i.tenant_id = d.tenant_id
		   AND i.incident_id = d.incident_id
		 WHERE d.tenant_id = $1
		   AND d.created_at >= $2 AND d.created_at < $3
		 GROUP BY d.agreement, i.primary_site_id, i.rule_id`},

	// The AI plane's own count, and the two exclusions.
	//
	// The degraded and the insufficient counts are REPORTED and never folded
	// into a denominator: a degraded observation is not evidence about the
	// model, and a denominator that includes it turns an outage into a score.
	{sql: `
		SELECT 'ai_observations' AS metric, o.site_id, i.rule_id, NULL::text AS device_id,
		       count(*)::float8 AS value, count(*)::bigint AS n
		  FROM ai_observations o
		  JOIN incidents i
		    ON i.tenant_id = o.tenant_id
		   AND i.incident_id = o.incident_id
		 WHERE o.tenant_id = $1
		   AND o.created_at >= $2 AND o.created_at < $3
		 GROUP BY o.site_id, i.rule_id
		UNION ALL
		SELECT 'ai_observations_degraded', o.site_id, i.rule_id, NULL::text,
		       count(*)::float8, count(*)::bigint
		  FROM ai_observations o
		  JOIN incidents i
		    ON i.tenant_id = o.tenant_id
		   AND i.incident_id = o.incident_id
		 WHERE o.tenant_id = $1
		   AND o.created_at >= $2 AND o.created_at < $3
		   AND o.degraded_reason <> 'none'
		 GROUP BY o.site_id, i.rule_id
		UNION ALL
		SELECT 'ai_observations_insufficient', o.site_id, i.rule_id, NULL::text,
		       count(*)::float8, count(*)::bigint
		  FROM ai_observations o
		  JOIN incidents i
		    ON i.tenant_id = o.tenant_id
		   AND i.incident_id = o.incident_id
		 WHERE o.tenant_id = $1
		   AND o.created_at >= $2 AND o.created_at < $3
		   AND o.insufficient_evidence
		 GROUP BY o.site_id, i.rule_id`},

	// Evidence stored, per site. There is no rule on an evidence item, so
	// there is no per-rule row either, and the site is the whole scope.
	{sql: `
		SELECT 'evidence_items_stored' AS metric, e.site_id, NULL::text AS rule_id,
		       NULL::text AS device_id, count(*)::float8 AS value, count(*)::bigint AS n
		  FROM evidence e
		 WHERE e.tenant_id = $1
		   AND e.stored_at >= $2 AND e.stored_at < $3
		 GROUP BY e.site_id`},

	// Evidence fetches that failed, from the dead-lettered jobs.
	//
	// `jobs.site_id` is NULLABLE, and this table's `site_id` is not, so a
	// site-less job has nowhere to go. The WHERE clause says so explicitly
	// rather than letting a NULL site into a NOT NULL column, and the
	// pipeline's log line reports the gap.
	{sql: `
		SELECT 'evidence_fetch_failed' AS metric, j.site_id, NULL::text AS rule_id,
		       NULL::text AS device_id, count(*)::float8 AS value, count(*)::bigint AS n
		  FROM jobs j
		 WHERE j.tenant_id = $1
		   AND j.queue = 'evidence_fetch'
		   AND j.status = 'dead_letter'
		   AND j.site_id IS NOT NULL
		   AND j.completed_at >= $2 AND j.completed_at < $3
		 GROUP BY j.site_id`},
}

// compute runs every computation and returns the measurements.
func (r *Rollup) compute(ctx context.Context, tenant string, b Bucket) ([]Measurement, error) {
	var out []Measurement
	for _, c := range computations {
		args := []any{tenant, b.Start.UTC(), b.End.UTC()}
		if c.usesCap {
			args = append(args, r.cap().Seconds())
		}

		rows, err := r.Pool.WithSites(tenant, "metric_rollups", []string{r.SiteID}).Query(ctx, c.sql, args...)
		if err != nil {
			return nil, fmt.Errorf("operatormetrics: rollup compute: %w", err)
		}
		for rows.Next() {
			var (
				name     string
				siteID   string
				ruleID   *string
				deviceID *string
				value    float64
				n        int64
			)
			if err := rows.Scan(&name, &siteID, &ruleID, &deviceID, &value, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("operatormetrics: rollup scan: %w", err)
			}
			metric, ok := MetricByName(name)
			if !ok {
				rows.Close()
				return nil, fmt.Errorf("operatormetrics: the rollup query produced %q, which is not in "+
					"the closed metric set. The set is the vocabulary and the database's CHECK "+
					"constraint carries the same list, so a name here that is not there is a "+
					"statement about a metric that does not exist", name)
			}
			if siteID == "" {
				// A row with no site is a tenant-wide row, and the table cannot
				// hold one: `app_site_visible(NULL)` is TRUE, so a site-scoped
				// principal would read another site's aggregate.
				rows.Close()
				return nil, fmt.Errorf("operatormetrics: the rollup query for %s produced a row with "+
					"no site. Every row is site-scoped; a tenant-wide number is the SUM of the rows "+
					"a principal may see", metric)
			}
			out = append(out, Measurement{
				Metric:   metric,
				SiteID:   siteID,
				RuleID:   deref(ruleID),
				DeviceID: deref(deviceID),
				Value:    value,
				N:        n,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("operatormetrics: rollup rows: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// deref is a nullable string as a string, with NULL as the empty string.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// WilsonInterval is the Wilson score interval on a proportion.
//
// It is here because a rate without an interval is a number with an undisclosed
// precision, and the metrics document asks for the interval on every rate. The
// Wilson interval is used rather than the normal approximation because a
// proportion near zero or one has a skewed sampling distribution, and the
// normal approximation is exactly wrong at the ends -- which is where a
// security product's rates live.
//
// It returns (0, 0) for an N of zero, and the caller is expected to report
// that as "not measured" rather than as "a rate of zero with no spread".
func WilsonInterval(successes, n int64, z float64) (lower, upper float64) {
	if n <= 0 {
		return 0, 0
	}
	if z <= 0 {
		z = 1.959963984540054 // the 95% two-sided normal quantile
	}
	p := float64(successes) / float64(n)
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	denom := 1 + z*z/float64(n)
	centre := p + z*z/(2*float64(n))
	spread := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	lower = (centre - spread) / denom
	upper = (centre + spread) / denom
	if lower < 0 {
		lower = 0
	}
	if upper > 1 {
		upper = 1
	}
	return lower, upper
}
