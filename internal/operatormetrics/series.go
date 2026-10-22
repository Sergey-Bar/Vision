package operatormetrics

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// The series and the trend.
//
// # A series is the metric over time, and it is read from the rollup
//
// Not re-derived from the raw tables: that is the drill-down's job, and the
// drill-down is a separate route for exactly this reason. A series that
// re-derived every bucket would be a scan of the whole window per request, and
// a dashboard that asked for a year of history would ask for a year of scans.
//
// # A trend is a difference between two windows
//
// The current window and the window before it, of the same length, so the
// comparison is like for like. A trend that compared a week against a month
// would be a trend about the calendar.
//
// # Every number carries its N, its window, when it was computed and where it
// came from
//
// And a drill-down reference that re-derives one bucket from the raw rows. A
// reader who does not believe the rollup has to be able to check it, and
// "check it" has to be a URL they can open rather than a query they have to
// write.

// Granularity is how wide a series point is.
type Granularity string

// The granularities.
//
// The rollup is stored per hour, so an hour is the finest and a day is the
// coarsest that is still a SUM rather than a re-derivation. A week would have
// to be a re-derivation, and a re-derivation is the drill-down.
const (
	// GranularityHour is one bucket.
	GranularityHour Granularity = "hour"
	// GranularityDay is a day of buckets.
	GranularityDay Granularity = "day"
)

// AllGranularities returns the closed set.
func AllGranularities() []Granularity {
	return []Granularity{GranularityHour, GranularityDay}
}

// SeriesPoint is one bucket's value.
type SeriesPoint struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// BucketStart and BucketEnd are the interval the point covers, half-open.
	BucketStart time.Time `json:"bucket_start"`
	BucketEnd   time.Time `json:"bucket_end"`
	// SiteID and RuleID are the scope the point was stored at. They are empty
	// when the point is an aggregate over the scope.
	SiteID string `json:"site_id,omitempty"`
	RuleID string `json:"rule_id,omitempty"`
	// Value is the measured number.
	Value float64 `json:"value"`
	// N is how many things the value counts.
	N int64 `json:"n"`
	// ComputedAt is when the value was derived.
	ComputedAt time.Time `json:"computed_at"`
}

// Series is a metric over time.
type Series struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// Window is the period the series covers.
	Window Window `json:"window"`
	// Granularity is how wide a point is.
	Granularity Granularity `json:"granularity"`
	// Source is where the numbers came from. Always "postgres".
	Source string `json:"source"`
	// ComputedAt is when the series was built.
	ComputedAt time.Time `json:"computed_at"`
	// Points are the buckets, in order.
	Points []SeriesPoint `json:"points"`
	// Drilldown is a reference that re-derives one bucket from the raw rows.
	Drilldown string `json:"drilldown"`
}

// Trend is a comparison of two windows.
type Trend struct {
	// Metric is the wire name.
	Metric string `json:"metric"`
	// Window is the current period.
	Window Window `json:"window"`
	// PriorWindow is the period before it, of the same length.
	PriorWindow Window `json:"prior_window"`
	// Source is where the numbers came from.
	Source string `json:"source"`
	// ComputedAt is when the trend was built.
	ComputedAt time.Time `json:"computed_at"`
	// Current is the current window's value, and nil when the window holds no
	// rollup at all.
	Current *Measure `json:"current"`
	// Prior is the prior window's value.
	Prior *Measure `json:"prior"`
	// Delta is the difference, and nil when either side is absent.
	Delta *float64 `json:"delta"`
	// DeltaPercent is the difference as a share of the prior value, and nil
	// when the prior value is zero: a percentage of zero is not a percentage.
	DeltaPercent *float64 `json:"delta_percent"`
	// Drilldown is a reference that re-derives the current window from the raw
	// rows.
	Drilldown string `json:"drilldown"`
}

// Series reads a metric over the window.
//
// The site and class are FILTERS and never grants: the reader's query names the
// tenant, and the RLS policies decide which sites the caller may see at all.
func (r *Reader) Series(ctx context.Context, tenant, site, class string, m Metric, w Window, g Granularity) (Series, error) {
	if r.Pool == nil {
		return Series{}, ErrNoPool
	}
	if tenant == "" {
		return Series{}, fmt.Errorf("operatormetrics: a series with no tenant is a series about everybody")
	}
	if !w.To.After(w.From) {
		return Series{}, fmt.Errorf("operatormetrics: the window [%s, %s) is empty",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}
	if g != GranularityHour && g != GranularityDay {
		return Series{}, fmt.Errorf("operatormetrics: %q is not a granularity. The closed set is %v",
			g, AllGranularities())
	}

	computedAt := r.now()
	out := Series{
		Metric:      m.String(),
		Window:      w,
		Granularity: g,
		Source:      "postgres",
		ComputedAt:  computedAt,
		Points:      []SeriesPoint{},
		Drilldown:   drilldownRef(m, w, site, class),
	}

	rows, err := r.Pool.WithSites(tenant, "metric_rollups", r.Sites).Query(ctx,
		`SELECT metric, site_id, coalesce(rule_id, ''), bucket_start, bucket_end, value, n, computed_at
		   FROM metric_rollups
		  WHERE tenant_id = $1
		    AND metric = $2
		    AND bucket_start >= $3 AND bucket_start < $4
		    AND ($5 = '' OR site_id = $5)
		    AND ($6 = '' OR coalesce(rule_id, '') = $6)
		  ORDER BY bucket_start, site_id, coalesce(rule_id, '')`,
		tenant, m.String(), w.From.UTC(), w.To.UTC(), site, class)
	if err != nil {
		return out, fmt.Errorf("operatormetrics: read the series: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			metric, siteID, ruleID string
			bucketStart, bucketEnd time.Time
			value                  float64
			n                      int64
			computed               time.Time
		)
		if err := rows.Scan(&metric, &siteID, &ruleID, &bucketStart, &bucketEnd, &value, &n, &computed); err != nil {
			return out, fmt.Errorf("operatormetrics: read the series scan: %w", err)
		}
		out.Points = append(out.Points, SeriesPoint{
			Metric:      metric,
			BucketStart: bucketStart.UTC(),
			BucketEnd:   bucketEnd.UTC(),
			SiteID:      siteID,
			RuleID:      ruleID,
			Value:       value,
			N:           n,
			ComputedAt:  computed.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("operatormetrics: read the series rows: %w", err)
	}

	if g == GranularityDay {
		out.Points = daily(out.Points)
	}
	return out, nil
}

// daily folds hourly points into days.
//
// It is a SUM and not an average, because a day of attention minutes is a
// duration and a day of events is a count: averaging a count would report a
// number that is not a thing anybody counted.
func daily(points []SeriesPoint) []SeriesPoint {
	type key struct {
		day  string
		site string
		rule string
	}
	index := map[key]*SeriesPoint{}
	var order []key
	for _, p := range points {
		day := p.BucketStart.UTC().Truncate(24 * time.Hour)
		k := key{day: day.Format(time.RFC3339), site: p.SiteID, rule: p.RuleID}
		agg, ok := index[k]
		if !ok {
			agg = &SeriesPoint{
				Metric:      p.Metric,
				BucketStart: day,
				BucketEnd:   day.Add(24 * time.Hour),
				SiteID:      p.SiteID,
				RuleID:      p.RuleID,
				ComputedAt:  p.ComputedAt,
			}
			index[k] = agg
			order = append(order, k)
		}
		agg.Value += p.Value
		agg.N += p.N
		if p.ComputedAt.After(agg.ComputedAt) {
			agg.ComputedAt = p.ComputedAt
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].day != order[j].day {
			return order[i].day < order[j].day
		}
		if order[i].site != order[j].site {
			return order[i].site < order[j].site
		}
		return order[i].rule < order[j].rule
	})
	out := make([]SeriesPoint, 0, len(order))
	for _, k := range order {
		out = append(out, *index[k])
	}
	return out
}

// Trend reads a metric over the window and over the window before it.
//
// The prior window is the same LENGTH, immediately before, so the comparison is
// like for like. A week against a month would be a trend about the calendar.
func (r *Reader) Trend(ctx context.Context, tenant, site, class string, m Metric, w Window) (Trend, error) {
	if r.Pool == nil {
		return Trend{}, ErrNoPool
	}
	if !w.To.After(w.From) {
		return Trend{}, fmt.Errorf("operatormetrics: the window [%s, %s) is empty",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	}
	length := w.To.Sub(w.From)
	prior := Window{From: w.From.Add(-length), To: w.From}

	computedAt := r.now()
	out := Trend{
		Metric:      m.String(),
		Window:      w,
		PriorWindow: prior,
		Source:      "postgres",
		ComputedAt:  computedAt,
		Drilldown:   drilldownRef(m, w, site, class),
	}

	current, err := r.windowTotal(ctx, tenant, site, class, m, w)
	if err != nil {
		return out, err
	}
	before, err := r.windowTotal(ctx, tenant, site, class, m, prior)
	if err != nil {
		return out, err
	}
	if current != nil {
		out.Current = current
	}
	if before != nil {
		out.Prior = before
	}
	if current == nil || before == nil {
		// No delta against a side that was not measured. A trend of "the
		// product did not measure this" against "the product measured zero"
		// is not a trend.
		return out, nil
	}
	delta := current.Value - before.Value
	out.Delta = &delta
	if before.Value != 0 {
		pct := (delta / before.Value) * 100
		out.DeltaPercent = &pct
	}
	return out, nil
}

// windowTotal sums one metric over a window.
//
// It returns nil when the window holds no rollup for the metric at all, which
// is different from a window that holds a rollup of zero. The difference is
// the difference between "not measured" and "measured as nothing", and a trend
// that reported the second as the first would be a trend about a product that
// was not installed.
func (r *Reader) windowTotal(ctx context.Context, tenant, site, class string, m Metric, w Window) (*Measure, error) {
	var (
		value float64
		n     int64
		rows  int
	)
	err := r.Pool.WithSites(tenant, "metric_rollups", r.Sites).QueryRow(ctx,
		`SELECT coalesce(sum(value), 0), coalesce(sum(n), 0), count(*)
		   FROM metric_rollups
		  WHERE tenant_id = $1
		    AND metric = $2
		    AND bucket_start >= $3 AND bucket_start < $4
		    AND ($5 = '' OR site_id = $5)
		    AND ($6 = '' OR coalesce(rule_id, '') = $6)`,
		tenant, m.String(), w.From.UTC(), w.To.UTC(), site, class).Scan(&value, &n, &rows)
	if err != nil {
		return nil, fmt.Errorf("operatormetrics: read the window total: %w", err)
	}
	if rows == 0 {
		return nil, nil
	}
	return &Measure{
		Metric:     m.String(),
		Value:      value,
		N:          n,
		Window:     w,
		ComputedAt: r.now(),
	}, nil
}

// drilldownRef is the reference that re-derives a window from the raw rows.
//
// It is a reference and not a number, because the point of it is that a reader
// who does not believe the rollup can OPEN it. A drill-down that returned the
// number would be a second number to disagree with.
func drilldownRef(m Metric, w Window, site, class string) string {
	ref := fmt.Sprintf("/v1/analytics/drilldown?metric=%s&from=%s&to=%s",
		m.String(), w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339))
	if site != "" {
		ref += "&site=" + site
	}
	if class != "" {
		ref += "&class=" + class
	}
	return ref
}
