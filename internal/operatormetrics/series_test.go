package operatormetrics_test

// The series, the trend, the drill-down and the freshness.
//
// # The series reads the rollup, and the drill-down re-derives
//
// A series is a read of the rollup: one value per bucket, with the N and the
// window on every point. A drill-down is a RE-DERIVATION from the raw rows,
// and the two are the same computation over the same tables -- which is what
// makes the drill-down a CHECK on the rollup rather than a second number.
//
// # The trend compares two windows of the SAME length
//
// The current window and the window before it, so the comparison is like for
// like. A side that was not measured is absent rather than zero.

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// writeBucket writes one rollup row for the site.
func writeBucket(t *testing.T, h harness, start, end time.Time, value float64, n int64) {
	t.Helper()
	if _, err := h.pool.WithSites(h.tenant, "test", []string{h.site}).Exec(context.Background(),
		`INSERT INTO metric_rollups
			(tenant_id, metric, bucket_start, bucket_end, site_id, rule_id, device_id,
			 value, n, numerator, denominator, computed_at)
		 VALUES ($1, $2, $3, $4, $5, NULL, NULL, $6, $7, NULL, NULL, now())`,
		h.tenant, "canonical_events", start, end, h.site, value, n); err != nil {
		t.Fatalf("write the canonical_events bucket: %v", err)
	}
}

// TestTheSeriesReadsTheRollupPerBucket is the series.
//
// Two hours of a metric, read at hour and at day granularity. The day is a SUM
// of the hours, because a day of attention minutes is a duration and averaging
// a count would report a number that is not a thing anybody counted.
func TestTheSeriesReadsTheRollupPerBucket(t *testing.T) {
	h := setup(t)
	base := time.Now().UTC().Truncate(time.Hour).Add(-4 * time.Hour)
	writeBucket(t, h, base, base.Add(time.Hour), 10, 10)
	writeBucket(t, h, base.Add(time.Hour), base.Add(2*time.Hour), 20, 20)

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	win := operatormetrics.Window{From: base, To: base.Add(2 * time.Hour)}

	hourly, err := reader.Series(context.Background(), h.tenant, "", "",
		canonicalEvents(t), win, operatormetrics.GranularityHour)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(hourly.Points) != 2 {
		t.Fatalf("the hourly series holds %d points, want 2", len(hourly.Points))
	}
	if hourly.Points[0].Value != 10 || hourly.Points[1].Value != 20 {
		t.Errorf("the hourly series is %v, want 10 then 20", hourly.Points)
	}
	if hourly.Points[0].N != 10 {
		t.Errorf("the first point's N is %d, want 10. A point without its N is a number "+
			"nobody can interpret", hourly.Points[0].N)
	}
	if hourly.Drilldown == "" {
		t.Error("the series carries no drill-down reference. A number nobody can check is a " +
			"number nobody can trust")
	}
	if hourly.Source != "postgres" {
		t.Errorf("the series source is %q, want postgres", hourly.Source)
	}

	daily, err := reader.Series(context.Background(), h.tenant, "", "",
		canonicalEvents(t), win, operatormetrics.GranularityDay)
	if err != nil {
		t.Fatalf("Series at day granularity: %v", err)
	}
	if len(daily.Points) != 1 {
		t.Fatalf("the daily series holds %d points, want 1", len(daily.Points))
	}
	if daily.Points[0].Value != 30 {
		t.Errorf("the daily point is %v, want 30. A day is a SUM of hours", daily.Points[0].Value)
	}
	if daily.Points[0].N != 30 {
		t.Errorf("the daily point's N is %d, want 30", daily.Points[0].N)
	}
}

// TestTheSeriesRefusesWhatItCannotAnswer is the refusals.
func TestTheSeriesRefusesWhatItCannotAnswer(t *testing.T) {
	h := setup(t)
	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	win := operatormetrics.Window{
		From: time.Now().UTC().Truncate(time.Hour).Add(-time.Hour),
		To:   time.Now().UTC().Truncate(time.Hour),
	}
	if _, err := reader.Series(context.Background(), h.tenant, "", "",
		canonicalEvents(t), win, "fortnight"); err == nil {
		t.Error("the series accepted a granularity that is not in the closed set")
	}
	if _, err := reader.Series(context.Background(), "", "", "",
		canonicalEvents(t), win, operatormetrics.GranularityHour); err == nil {
		t.Error("the series accepted a window with no tenant")
	}
	if _, err := reader.Series(context.Background(), h.tenant, "", "",
		canonicalEvents(t),
		operatormetrics.Window{From: win.To, To: win.From}, operatormetrics.GranularityHour); err == nil {
		t.Error("the series accepted an empty window")
	}
}

// TestTheTrendComparesTwoWindowsOfTheSameLength is the trend.
//
// The current window and the one before it, of the same length, and a delta
// that is the difference.
func TestTheTrendComparesTwoWindowsOfTheSameLength(t *testing.T) {
	h := setup(t)
	end := time.Now().UTC().Truncate(time.Hour)
	current := operatormetrics.Window{From: end.Add(-time.Hour), To: end}
	prior := operatormetrics.Window{From: end.Add(-2 * time.Hour), To: end.Add(-time.Hour)}

	writeBucket(t, h, current.From, current.To, 30, 30)
	writeBucket(t, h, prior.From, prior.To, 10, 10)

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	trend, err := reader.Trend(context.Background(), h.tenant, "", "",
		canonicalEvents(t), current)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if trend.Current == nil || trend.Current.Value != 30 {
		t.Errorf("the current window is %v, want 30", trend.Current)
	}
	if trend.Prior == nil || trend.Prior.Value != 10 {
		t.Errorf("the prior window is %v, want 10", trend.Prior)
	}
	if trend.Delta == nil || *trend.Delta != 20 {
		t.Errorf("the delta is %v, want 20", trend.Delta)
	}
	if trend.DeltaPercent == nil || *trend.DeltaPercent != 200 {
		t.Errorf("the delta percent is %v, want 200", trend.DeltaPercent)
	}
	if trend.PriorWindow.To != current.From {
		t.Errorf("the prior window ends at %s, want %s. The two windows have to be adjacent "+
			"and the same length", trend.PriorWindow.To, current.From)
	}
}

// TestATrendAgainstAnUnmeasuredWindowIsAbsent is the honest absence.
//
// A window with no rollup is not a window of zero, and a delta against it is
// not a delta.
func TestATrendAgainstAnUnmeasuredWindowIsAbsent(t *testing.T) {
	h := setup(t)
	end := time.Now().UTC().Truncate(time.Hour)
	current := operatormetrics.Window{From: end.Add(-time.Hour), To: end}
	writeBucket(t, h, current.From, current.To, 30, 30)

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	trend, err := reader.Trend(context.Background(), h.tenant, "", "",
		canonicalEvents(t), current)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if trend.Prior != nil {
		t.Errorf("the prior window is %v, want absent. A window that was not measured is not a "+
			"window of zero", trend.Prior)
	}
	if trend.Delta != nil {
		t.Errorf("the delta is %v, want absent", trend.Delta)
	}
}

// TestTheFreshestBucketIsTheNewestOne is the doctor's freshness.
//
// A number from a window that ended two days ago is not wrong, it is STALE, and
// the difference is worth a line of output.
func TestTheFreshestBucketIsTheNewestOne(t *testing.T) {
	h := setup(t)
	base := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	writeBucket(t, h, base, base.Add(time.Hour), 10, 10)
	newest := base.Add(time.Hour)
	writeBucket(t, h, newest, newest.Add(time.Hour), 10, 10)

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	freshest, err := reader.Freshest(context.Background(), h.tenant)
	if err != nil {
		t.Fatalf("Freshest: %v", err)
	}
	if !freshest.Equal(newest) {
		t.Errorf("the freshest bucket is %s, want %s", freshest.UTC(), newest.UTC())
	}
}

// TestTheDrilldownRederivesTheNumberFromTheRawRows is the check on the rollup.
//
// The drill-down computes from the SOURCE tables, and it is the same
// computation the writer does, so the two agreeing is the control.
func TestTheDrilldownRederivesTheNumberFromTheRawRows(t *testing.T) {
	h := setup(t)
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.Add(-time.Hour)

	// The rows the derivation reads.
	id := h.raise(t, 2*time.Second)
	h.open(t, id, start.Add(time.Minute))
	h.disposition(t, id, start.Add(10*time.Minute))
	h.canonical(t, "evt-drilldown-1", start.Add(5*time.Minute))
	h.canonical(t, "evt-drilldown-2", start.Add(6*time.Minute))

	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site}

	derived, err := rollup.Derive(context.Background(), h.tenant,
		operatormetrics.Bucket{Start: start, End: end})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(derived) == 0 {
		t.Fatal("the drill-down derived nothing. A drill-down over an empty window is a " +
			"correct answer and an unhelpful one")
	}
	var sawEvents bool
	for _, m := range derived {
		if m.Metric.String() == "canonical_events" && m.Value == 2 {
			sawEvents = true
		}
	}
	if !sawEvents {
		t.Errorf("the drill-down did not re-derive the two canonical events: %v", derived)
	}

	// And it refuses a bucket that is not an interval, and one with no site.
	if _, err := rollup.Derive(context.Background(), h.tenant,
		operatormetrics.Bucket{Start: end, End: end}); err == nil {
		t.Error("the drill-down accepted an empty bucket")
	}
	noSite := &operatormetrics.Rollup{Pool: h.pool}
	if _, err := noSite.Derive(context.Background(), h.tenant,
		operatormetrics.Bucket{Start: start, End: end}); err == nil {
		t.Error("the drill-down accepted a derivation with no site. The rollup table is " +
			"site-scoped, and a derivation with no site would read nothing")
	}
}

// TestTheDueReportsAreTheOnesThatHaveNotBeenPublished is the due check.
//
// A report whose period has ended and which has not been published is DUE, and
// one that has been published is not. The loop checks every hour and the
// window does not change until the period ends, so without the check the
// product would publish the same report every hour.
func TestTheDueReportsAreTheOnesThatHaveNotBeenPublished(t *testing.T) {
	h := setup(t)
	root := t.TempDir()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	scheduled := &operatormetrics.Scheduled{
		Pool:  h.pool,
		Root:  root,
		Audit: &audit.Writer{Pool: h.pool},
		Now:   func() time.Time { return now },
		Sites: []string{h.site},
	}

	kinds, windows, err := scheduled.Due(context.Background(), h.tenant)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(kinds) != len(operatormetrics.AllScheduledKinds()) {
		t.Fatalf("%d reports are due, want %d. Nothing has been published yet",
			len(kinds), len(operatormetrics.AllScheduledKinds()))
	}
	for i, kind := range kinds {
		if windows[i].To.After(now) {
			t.Errorf("the %s report covers a window that ends at %s, which is in the future",
				kind, windows[i].To.UTC())
		}
	}

	// Publish one, and it is no longer due.
	if _, err := scheduled.Publish(context.Background(), h.tenant, kinds[0], windows[0]); err != nil {
		t.Fatalf("publish: %v", err)
	}
	kinds, _, err = scheduled.Due(context.Background(), h.tenant)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(kinds) != len(operatormetrics.AllScheduledKinds())-1 {
		t.Errorf("%d reports are still due after one was published, want %d",
			len(kinds), len(operatormetrics.AllScheduledKinds())-1)
	}
}

// TestTheScheduledReportRefusesWithoutARoot is the refusal.
//
// There is nowhere to publish a report without a root, and a report written
// somewhere guessed is a report somewhere unreadable.
func TestTheScheduledReportRefusesWithoutARoot(t *testing.T) {
	h := setup(t)
	scheduled := &operatormetrics.Scheduled{Pool: h.pool, Audit: &audit.Writer{Pool: h.pool}}
	win := operatormetrics.Window{
		From: time.Now().UTC().Truncate(time.Hour).Add(-time.Hour),
		To:   time.Now().UTC().Truncate(time.Hour),
	}
	if _, err := scheduled.Publish(context.Background(), h.tenant,
		operatormetrics.ReportExec, win); err == nil {
		t.Error("the publisher accepted a report with no root to write it to")
	}
	noTenant := &operatormetrics.Scheduled{Pool: h.pool, Root: t.TempDir(), Audit: &audit.Writer{Pool: h.pool}}
	if _, err := noTenant.Publish(context.Background(), "",
		operatormetrics.ReportExec, win); err == nil {
		t.Error("the publisher accepted a report with no tenant. A report with no tenant is a " +
			"report about everybody")
	}
}

// TestEveryGranularityIsNamed is the closed set.
//
// The granularity list is the vocabulary a caller names, so it has to be
// enumerable: a caller that cannot see the set cannot ask for one of its
// members.
func TestEveryGranularityIsNamed(t *testing.T) {
	all := operatormetrics.AllGranularities()
	if len(all) != 2 {
		t.Fatalf("there are %d granularities, want 2", len(all))
	}
	seen := map[operatormetrics.Granularity]bool{}
	for _, g := range all {
		if seen[g] {
			t.Errorf("the granularity %q is listed twice", g)
		}
		seen[g] = true
	}
	if !seen[operatormetrics.GranularityHour] || !seen[operatormetrics.GranularityDay] {
		t.Errorf("the granularity set is %v, want hour and day", all)
	}
}

// canonicalEvents is the North Star's denominator, looked up by its wire name.
//
// The metric constants are unexported because the vocabulary is closed and a
// caller that could name a metric by CONSTANT could name one that is not in
// the set. The wire name is the public way in, and the lookup is the check
// that the name is real.
func canonicalEvents(t *testing.T) operatormetrics.Metric {
	t.Helper()
	m, ok := operatormetrics.MetricByName("canonical_events")
	if !ok {
		t.Fatal("canonical_events is not in the closed metric set")
	}
	return m
}
