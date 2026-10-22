package operatormetrics_test

// The rollup writer, and the two properties it has to have.
//
// # Idempotence
//
// Writing the same bucket twice produces the same rows. That is what makes
// `securityctl analytics rebuild` a REPAIR path rather than a second source of
// truth: the rollup is a derivation from the source tables, and running the
// derivation again produces the same answer.
//
// A writer that appended instead of replacing would make the rollup a
// recording of a moment, and a recording of a moment cannot be re-derived --
// which means a reader who disagrees with the rollup has no way to find out
// which of them is right.
//
// # Recomputability
//
// The value written is a function of the bucket and the source tables alone.
// Not of the clock, not of what the writer believed last time, and not of the
// order the metrics happen to be computed in.
//
// # The site rule
//
// Every row is site-scoped. `metric_rollups.site_id` is NOT NULL because
// `app_site_visible(NULL)` is TRUE, and a tenant-wide row would put one site's
// aggregate attention minutes inside another site's scope. The tenant-wide view
// is the SUM of the rows a principal may see, and it is the READER's job to
// sum them.

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// canonical inserts one canonical event, which is the North Star's
// denominator.
//
// It is inserted directly rather than through the ingest path, because the
// test is about the arithmetic and the ingest path stamps its own timestamps.
func (h harness) canonical(t *testing.T, eventID string, at time.Time) {
	t.Helper()
	if _, err := h.pool.For(h.tenant, "canonical_events").Exec(ctx(), `
		INSERT INTO canonical_events
			(tenant_id, event_id, site_id, source_system_id, source_event_id, integration_id,
			 event_type, schema_version, observed_at, received_at, normalized_at, device_id,
			 integrity_hash)
		VALUES ($1, $2, $3, 'sys-canary', $2, 'int-canary', 'motion', '1', $4, $4, $4, $5,
		        repeat('a', 64))`,
		h.tenant, eventID, h.site, at, "dev-canary"); err != nil {
		t.Fatalf("insert the canonical event: %v", err)
	}
}

// rollupRows reads the rollup back, ordered so a comparison is stable.
func (h harness) rollupRows(t *testing.T) []string {
	t.Helper()
	rows, err := h.pool.For(h.tenant, "metric_rollups").Query(ctx(),
		`SELECT metric, site_id, coalesce(rule_id, ''), coalesce(device_id, ''),
		        value::text, n::text
		   FROM metric_rollups
		  WHERE tenant_id = $1
		  ORDER BY metric, site_id, coalesce(rule_id, ''), coalesce(device_id, '')`,
		h.tenant)
	if err != nil {
		t.Fatalf("read the rollup: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var metric, site, rule, device, value, n string
		if err := rows.Scan(&metric, &site, &rule, &device, &value, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, metric+"|"+site+"|"+rule+"|"+device+"|"+value+"|"+n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return out
}

// TestTheRollupIsIdempotent is the first property.
//
// The same bucket, written twice, leaves one copy of each row and the same
// values. The values matter as much as the count: a writer that doubled the
// count would also fail this, but a writer that kept the count and doubled the
// value would pass a count-only check.
func TestTheRollupIsIdempotent(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	h.disposition(t, id, h.now.Add(30*time.Minute))
	h.canonical(t, "evt-canary-1", h.now)

	bucket := operatormetrics.Bucket{Start: h.now.Add(-time.Hour), End: h.now.Add(time.Hour)}
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}

	first, err := rollup.Write(ctx(), h.tenant, bucket)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if first.Rows == 0 {
		t.Fatal("the rollup wrote no rows for a bucket with an incident, a disposition and a canonical event")
	}
	before := h.rollupRows(t)

	second, err := rollup.Write(ctx(), h.tenant, bucket)
	if err != nil {
		t.Fatalf("write again: %v", err)
	}
	if second.Rows != first.Rows {
		t.Errorf("the second write produced %d rows and the first produced %d. A derivation "+
			"that changes its own answer is a recording of a moment", second.Rows, first.Rows)
	}
	after := h.rollupRows(t)
	if len(before) != len(after) {
		t.Fatalf("the rollup holds %d rows after the first write and %d after the second",
			len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("row %d changed between writes:\n  before %s\n  after  %s", i, before[i], after[i])
		}
	}
}

// TestTheRollupIsRecomputable is the second property.
//
// The value is a function of the source tables, so a source that changes
// changes the rollup, and a rollup that is deleted and re-derived comes back
// the same.
func TestTheRollupIsRecomputable(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	h.disposition(t, id, h.now.Add(30*time.Minute))

	bucket := operatormetrics.Bucket{Start: h.now.Add(-time.Hour), End: h.now.Add(time.Hour)}
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	if _, err := rollup.Write(ctx(), h.tenant, bucket); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := h.rollupRows(t)

	// Delete everything the writer wrote and write it again from the same
	// source. The result must be identical: the rollup is derived, not
	// remembered.
	if _, err := h.pool.For(h.tenant, "metric_rollups").Exec(ctx(),
		`DELETE FROM metric_rollups WHERE tenant_id = $1`, h.tenant); err != nil {
		t.Fatalf("clear the rollup: %v", err)
	}
	if _, err := rollup.Write(ctx(), h.tenant, bucket); err != nil {
		t.Fatalf("re-derive: %v", err)
	}
	after := h.rollupRows(t)
	if len(before) != len(after) {
		t.Fatalf("the re-derived rollup holds %d rows and the original held %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("row %d differs after a re-derivation:\n  before %s\n  after  %s", i, before[i], after[i])
		}
	}
}

// TestTheRollupCarriesTheNorthStarsDenominator is the metric that matters.
//
// The North Star is attention minutes per 1000 canonical events, and the
// denominator is the half that does not exist anywhere else. A rollup with a
// numerator and no denominator is a report that cannot be computed.
func TestTheRollupCarriesTheNorthStarsDenominator(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	h.disposition(t, id, h.now.Add(30*time.Minute))
	h.canonical(t, "evt-canary-1", h.now)
	h.canonical(t, "evt-canary-2", h.now)

	bucket := operatormetrics.Bucket{Start: h.now.Add(-time.Hour), End: h.now.Add(time.Hour)}
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	if _, err := rollup.Write(ctx(), h.tenant, bucket); err != nil {
		t.Fatalf("write: %v", err)
	}

	rows := h.rollupRows(t)
	var found bool
	for _, r := range rows {
		if len(r) > len("canonical_events|") && r[:len("canonical_events|")] == "canonical_events|" {
			found = true
			if !strings.Contains(r, "|2|") {
				t.Errorf("the canonical_events row does not count two events: %s", r)
			}
		}
	}
	if !found {
		t.Errorf("the rollup has no canonical_events row, so the North Star has no denominator:\n%v", rows)
	}
}

// TestEveryRollupRowIsSiteScoped is the site rule.
//
// The table's column is NOT NULL, so this is a belt-and-braces check on the
// WRITER: a row with no site is a tenant-wide row, and the writer refuses to
// write one rather than letting the database's constraint catch it.
func TestEveryRollupRowIsSiteScoped(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	h.disposition(t, id, h.now.Add(30*time.Minute))

	bucket := operatormetrics.Bucket{Start: h.now.Add(-time.Hour), End: h.now.Add(time.Hour)}
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	if _, err := rollup.Write(ctx(), h.tenant, bucket); err != nil {
		t.Fatalf("write: %v", err)
	}

	var siteLess int
	if err := h.pool.For(h.tenant, "metric_rollups").QueryRow(ctx(),
		`SELECT count(*) FROM metric_rollups WHERE tenant_id = $1 AND (site_id IS NULL OR site_id = '')`,
		h.tenant).Scan(&siteLess); err != nil {
		t.Fatalf("count the site-less rows: %v", err)
	}
	if siteLess != 0 {
		t.Errorf("%d rollup rows have no site. A site-less row is a tenant-wide row, and "+
			"`app_site_visible(NULL)` is TRUE, so a principal assigned to one site would read "+
			"every other site's aggregate", siteLess)
	}
}

// TestTheRollupRefusesAnEmptyBucket is the shape check.
//
// A bucket that is not a non-empty interval would count its own end twice
// across two buckets, and the error would be one event per bucket rather than
// something obviously wrong.
func TestTheRollupRefusesAnEmptyBucket(t *testing.T) {
	h := setup(t)
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	for _, b := range []operatormetrics.Bucket{
		{},
		{Start: h.now, End: h.now},
		{Start: h.now.Add(time.Hour), End: h.now},
	} {
		if _, err := rollup.Write(ctx(), h.tenant, b); err == nil {
			t.Errorf("the writer accepted the bucket [%s, %s), which is not a non-empty interval",
				b.Start.UTC().Format(time.RFC3339), b.End.UTC().Format(time.RFC3339))
		}
	}
}

// TestTheRollupRefusesNoTenant is the shape check that matters most.
//
// A rollup with no tenant is a metric about everybody, and the table's
// primary key has no room for it.
func TestTheRollupRefusesNoTenant(t *testing.T) {
	h := setup(t)
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	bucket := operatormetrics.Bucket{Start: h.now.Add(-time.Hour), End: h.now.Add(time.Hour)}
	if _, err := rollup.Write(ctx(), "", bucket); err == nil {
		t.Error("the writer accepted a bucket with no tenant")
	}
}
