package api

// The analytics routes.
//
// # What they are for
//
// The durable report: the North Star with its four companions, the series
// behind them, the trend against the window before, and a drill-down that
// re-derives a number from the raw rows.
//
// # The capability
//
// `analytics.read`, and NOT `audit.read`. The metrics scrape at `/v1/metrics`
// reuses `audit.read` because it is a Prometheus exposition; these routes are
// not a scrape, they are a report about PEOPLE -- how long a shift took to
// clear a site -- and somebody who may read every operator's disposition row is
// not automatically somebody who may read the aggregate of them.
//
// # The site filter is a FILTER, and the scope is the GRANT
//
// Every query binds the PRINCIPAL's site scope onto the connection, and the
// `site_isolation` policy is RESTRICTIVE. A request for a site the principal
// is not assigned to therefore sees that site's numbers as ABSENT rather than
// as zero: the query is scoped, so the rows are not there to sum.
//
// # Every response says what it is
//
// `window`, `n`, `computed_at` and `source`, and a drill-down reference. A
// number without its window is a number nobody can compare, and a number
// without a drill-down is a number nobody can check.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// handleAnalyticsIncidentRate answers M1 gate 5's ratio.
//
// The ratio is incidents raised per 1,000 canonical events, per camera, and it
// is the number a release note quotes. It is a Postgres computation over the
// source tables rather than a read of the rollup, because the rollup carries
// canonical events per DEVICE and incidents per RULE, and the ratio is per
// camera.
//
// The response says which cameras have no canonical events at all, because a
// camera with no events is a camera with no rate and NOT a camera with a rate
// of zero.
func (s *Server) handleAnalyticsIncidentRate(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AnalyticsPool == nil {
		WriteError(w, http.StatusNotImplemented, "not_configured",
			"The durable analytics are not configured on this install.", requestID(r))
		return
	}
	win, err := analyticsWindow(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
		return
	}
	reader := &operatormetrics.Reader{Pool: d.AnalyticsPool, Sites: p.SiteScope}
	report, err := reader.IncidentRate(r.Context(), p.TenantID, win)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "incident_rate_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, report)
}

// handleAnalyticsSummary answers the report.
//
// The North Star, its four companions and the rolled-up numbers behind them,
// over one window.
func (s *Server) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AnalyticsPool == nil {
		WriteError(w, http.StatusNotImplemented, "not_configured",
			"The durable analytics are not configured on this install.", requestID(r))
		return
	}
	win, err := analyticsWindow(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
		return
	}
	site, class, err := analyticsFilters(r, p)
	if err != nil {
		WriteError(w, http.StatusForbidden, "site_not_assigned", err.Error(), requestID(r))
		return
	}

	reader := &operatormetrics.Reader{Pool: d.AnalyticsPool, Sites: p.SiteScope}
	report, err := reader.Read(r.Context(), p.TenantID, site, class, win)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "report_failed", err.Error(), requestID(r))
		return
	}
	if err := report.Validate(); err != nil {
		// The report does not validate against its own rules, so it does not go
		// out. A report that fails its own check is a report nobody signed.
		WriteError(w, http.StatusInternalServerError, "report_invalid", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, report)
}

// handleAnalyticsSeries answers one metric over the window.
func (s *Server) handleAnalyticsSeries(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AnalyticsPool == nil {
		WriteError(w, http.StatusNotImplemented, "not_configured",
			"The durable analytics are not configured on this install.", requestID(r))
		return
	}
	metric, err := analyticsMetric(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_metric", err.Error(), requestID(r))
		return
	}
	win, err := analyticsWindow(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
		return
	}
	granularity := operatormetrics.GranularityHour
	if v := strings.TrimSpace(r.URL.Query().Get("granularity")); v != "" {
		granularity = operatormetrics.Granularity(v)
	}
	site, class, err := analyticsFilters(r, p)
	if err != nil {
		WriteError(w, http.StatusForbidden, "site_not_assigned", err.Error(), requestID(r))
		return
	}

	reader := &operatormetrics.Reader{Pool: d.AnalyticsPool, Sites: p.SiteScope}
	series, err := reader.Series(r.Context(), p.TenantID, site, class, metric, win, granularity)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "series_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, series)
}

// handleAnalyticsTrend answers one metric over the window and over the window
// before it.
func (s *Server) handleAnalyticsTrend(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AnalyticsPool == nil {
		WriteError(w, http.StatusNotImplemented, "not_configured",
			"The durable analytics are not configured on this install.", requestID(r))
		return
	}
	metric, err := analyticsMetric(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_metric", err.Error(), requestID(r))
		return
	}
	win, err := analyticsWindow(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
		return
	}
	site, class, err := analyticsFilters(r, p)
	if err != nil {
		WriteError(w, http.StatusForbidden, "site_not_assigned", err.Error(), requestID(r))
		return
	}

	reader := &operatormetrics.Reader{Pool: d.AnalyticsPool, Sites: p.SiteScope}
	trend, err := reader.Trend(r.Context(), p.TenantID, site, class, metric, win)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "trend_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, trend)
}

// handleAnalyticsDrilldown re-derives the numbers for a window from the RAW
// rows, without reading the rollup.
//
// It is the check on the rollup. A reader who does not believe the number can
// open this and see the rows it came from -- and if the two disagree, the
// reader has found something, because they are the same computation over the
// same rows.
//
// The response says it is a re-derivation and how much it cost, because a
// drill-down over a year is a year of scanning and a reader deserves to know
// that before they ask for it.
func (s *Server) handleAnalyticsDrilldown(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AnalyticsPool == nil {
		WriteError(w, http.StatusNotImplemented, "not_configured",
			"The durable analytics are not configured on this install.", requestID(r))
		return
	}
	metric, err := analyticsMetric(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_metric", err.Error(), requestID(r))
		return
	}
	win, err := analyticsWindow(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
		return
	}
	site, class, err := analyticsFilters(r, p)
	if err != nil {
		WriteError(w, http.StatusForbidden, "site_not_assigned", err.Error(), requestID(r))
		return
	}
	// The drill-down is for ONE site, because the derivation binds a site scope
	// and a row with no site is a row about everybody.
	if site == "" {
		WriteError(w, http.StatusBadRequest, "site_required",
			"The drill-down needs a site: the rollup is site-scoped, and a derivation with no "+
				"site would read nothing and write nothing.", requestID(r))
		return
	}

	rollup := &operatormetrics.Rollup{Pool: d.AnalyticsPool, SiteID: site}
	measurements, err := rollup.Derive(r.Context(), p.TenantID,
		operatormetrics.Bucket{Start: win.From, End: win.To})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "drilldown_failed", err.Error(), requestID(r))
		return
	}

	// Only the metric that was asked for, and only the rows in scope.
	type row struct {
		Metric   string  `json:"metric"`
		SiteID   string  `json:"site_id"`
		RuleID   string  `json:"rule_id,omitempty"`
		DeviceID string  `json:"device_id,omitempty"`
		Value    float64 `json:"value"`
		N        int64   `json:"n"`
	}
	rows := []row{}
	for _, m := range measurements {
		if m.Metric != metric {
			continue
		}
		if m.SiteID != site {
			continue
		}
		if class != "" && m.RuleID != class {
			continue
		}
		rows = append(rows, row{
			Metric: m.Metric.String(), SiteID: m.SiteID, RuleID: m.RuleID,
			DeviceID: m.DeviceID, Value: m.Value, N: m.N,
		})
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"report_version": operatormetrics.ReportVersion,
		"metric":         metric.String(),
		"window":         win,
		"source":         "raw rows",
		"computed_at":    time.Now().UTC(),
		"note": "Re-derived from the source tables, not read out of the rollup. The two are " +
			"the same computation over the same rows, so a reader that finds them " +
			"different has found a bucket that was derived before the rows it summarises existed.",
		"rows": rows,
	})
}

// analyticsWindow parses the window and ALIGNS it to the rollup's buckets.
//
// The alignment is not a convenience. The rollup is stored per hour, and a
// window that starts at 10:17 would include the 10:00 bucket -- an hour that
// is three quarters outside the window -- and the report would be summing a
// bucket the reader did not ask for.
//
// So the window is floored to the hour and the response carries the window it
// ACTUALLY covers. A caller that asked for "the last 24 hours" gets the last
// 24 whole hours, and the response says which they were.
func analyticsWindow(r *http.Request) (operatormetrics.Window, error) {
	q := r.URL.Query()
	now := time.Now().UTC()

	to := now
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return operatormetrics.Window{}, fmt.Errorf(
				"to=%q is not an RFC3339 instant (2026-10-21T00:00:00Z)", v)
		}
		to = parsed.UTC()
	}
	from := to.Add(-24 * time.Hour)
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			// A DURATION is accepted too, because "the last 24 hours" is how
			// people say it.
			d, derr := time.ParseDuration(v)
			if derr != nil {
				return operatormetrics.Window{}, fmt.Errorf(
					"from=%q is neither an RFC3339 instant nor a duration (24h)", v)
			}
			from = to.Add(-d)
		} else {
			from = parsed.UTC()
		}
	}
	if !to.After(from) {
		return operatormetrics.Window{}, fmt.Errorf(
			"the window [%s, %s) is empty", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	aligned := operatormetrics.Window{
		From: from.Truncate(time.Hour),
		To:   to.Truncate(time.Hour),
	}
	if !aligned.To.After(aligned.From) {
		return operatormetrics.Window{}, fmt.Errorf(
			"the window [%s, %s) covers no whole hour", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	return aligned, nil
}

// analyticsMetric parses the metric name, and refuses one that is not in the
// closed set.
//
// The refusal names the set, because a caller that guessed wrong deserves to
// know what the vocabulary is rather than a 400 that says "bad request".
func analyticsMetric(r *http.Request) (operatormetrics.Metric, error) {
	name := strings.TrimSpace(r.URL.Query().Get("metric"))
	if name == "" {
		return 0, fmt.Errorf("metric is required. The vocabulary is %s",
			strings.Join(operatormetrics.MetricNames(), ", "))
	}
	m, ok := operatormetrics.MetricByName(name)
	if !ok {
		return 0, fmt.Errorf("%q is not a metric. The vocabulary is %s",
			name, strings.Join(operatormetrics.MetricNames(), ", "))
	}
	return m, nil
}

// analyticsFilters reads the site and class filters, and refuses a site the
// principal may not see.
//
// The refusal is the same shape as the incident list's: "no such site" and
// "not yours" are the same answer, and a different one is a disclosure.
//
// The refusal is also not what protects the data. The SCOPE bound onto the
// connection is: a request for a site the principal is not assigned to sees no
// rows for it, so the number is ABSENT rather than zero. The refusal here is
// the better answer -- it says why -- and the scope is the one that holds when
// somebody asks for a number they were never going to get.
func analyticsFilters(r *http.Request, p identity.Principal) (site, class string, err error) {
	q := r.URL.Query()
	site = strings.TrimSpace(q.Get("site"))
	class = strings.TrimSpace(q.Get("class"))
	if site != "" && !p.CanSeeSite(site) {
		return "", "", fmt.Errorf("you are not assigned to site %q", site)
	}
	return site, class, nil
}
