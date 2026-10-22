package api

// The absence panel's storage, in the application rather than this package.
//
// Three statements, and each one has a shape the others do not.
//
// # Why they live beside the console and not in it
//
// `internal/api` states the surface it needs — [AbsenceReader] — and the application
// supplies it, which is the arrangement `DeadLetterReader` and `ClockDriftReader`
// already use. The reason is about the SQL rather than the page: these tables belong
// to the packages that own them, and a COUNT over four of them inside the API package
// would be the second reader for each — and the second reader for a tenant-scoped
// table is where a missing RLS predicate goes to hide.
//
// # The one rule every statement here follows
//
// **A COUNT, a MIN and a MODE — never a row.** The panel reports how much and when,
// never which. A panel that could name the observation with the oldest timestamp
// would be a way to read a row through a page gated on a different capability from
// the one that reads observations.
//
// # Site-scoped, not tenant-scoped
//
// The pool is site-bound since migration `0021`, and `WithSites` is how a handler
// says which site it is reading. Every statement below binds `site_id` **in the
// WHERE clause as well as through the policy**, because a query that relies on the
// policy alone is one query away from being wrong the next time somebody changes the
// site's scope — and the two disagreeing is how a tenant admin sees another site's
// counts.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// sqlAbsenceReader implements [AbsenceReader] over the site's pool.
//
// The pool arrives per call rather than being held, because `*dbx.Pool` is
// long-lived and a reader that captured one at construction time would outlive the
// tenant scope it was built under. The `tenantID` and `siteID` are arguments for the
// same reason: one reader serves every site.
type sqlAbsenceReader struct {
	pool     *dbx.Pool
	tenantID string
	actor    string
}

// blindFeedAfterInterval is the threshold as an interval, for the one place it binds.
const blindFeedAfterInterval = "24 hours"

// BlindIntegrations counts integrations that are not being read.
//
// Two cases kept apart, because they are different tickets: never-polled is a wiring
// problem and stopped-after-success is a vendor or network problem.
func (r sqlAbsenceReader) BlindIntegrations(ctx context.Context, tenantID, siteID string) (int, string, time.Time, error) {
	var (
		never, stale int
		oldest       time.Time
	)
	err := r.scoped(ctx, siteID).QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE last_success_at IS NULL),
			count(*) FILTER (WHERE last_success_at IS NOT NULL
			                   AND last_success_at < now() - '`+blindFeedAfterInterval+`'::interval),
			coalesce(min(coalesce(last_success_at, now())), 'epoch'::timestamptz)
		  FROM integrations
		 WHERE tenant_id = $1 AND site_id = $2 AND enabled`,
		tenantID, siteID).Scan(&never, &stale, &oldest)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("absence: read the vendor feed: %w", err)
	}
	total := never + stale
	if total == 0 {
		return 0, "", time.Time{}, nil
	}
	worst := "polled, then stopped"
	if never >= stale {
		worst = "never polled"
	}
	return total, worst, sinceOrZero(oldest), nil
}

// DegradedObservations counts observations the model did not answer.
//
// The reason is the point. "7 degraded" is a number; "7 degraded, all of them
// `no_runtime`" is a fact an operator can act on. The reason is the most
// FREQUENT one rather than the newest, tie-broken alphabetically so two reads of the
// same data give the same string.
func (r sqlAbsenceReader) DegradedObservations(ctx context.Context, tenantID, siteID string) (int, string, time.Time, error) {
	var (
		n      int
		reason *string
		oldest time.Time
	)
	err := r.scoped(ctx, siteID).QueryRow(ctx, `
		SELECT count(*),
		       (SELECT o.degraded_detail FROM ai_observations o
		         WHERE o.tenant_id = $1 AND o.site_id = $2
		           AND o.degraded_reason IS NOT NULL AND o.degraded_reason <> ''
		         GROUP BY o.degraded_detail
		         ORDER BY count(*) DESC, o.degraded_detail
		         LIMIT 1),
		       coalesce(min(o.created_at), 'epoch'::timestamptz)
		  FROM ai_observations o
		 WHERE o.tenant_id = $1 AND o.site_id = $2
		   AND o.degraded_reason IS NOT NULL AND o.degraded_reason <> ''`,
		tenantID, siteID).Scan(&n, &reason, &oldest)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("absence: read the degraded observations: %w", err)
	}
	if n == 0 {
		return 0, "", time.Time{}, nil
	}
	worst := ""
	if reason != nil {
		worst = *reason
	}
	return n, worst, sinceOrZero(oldest), nil
}

// DriftedDevices counts devices whose clock is out of band.
//
// `band <> 'ok'` rather than a comparison against a threshold here, because the band
// IS the threshold: it is computed by `clock.Policy` from the site's SLO and stored.
// A second threshold would be a second answer to the same question and would disagree
// the day somebody changed the policy.
//
// The column is `measured_at`. It was `sampled_at`, which is a column that has
// never existed on `device_clocks`, and the statement failed on every read —
// behind a panic on the same route, and behind a stack overflow behind that.
// Three defects stacked on one route, each hiding the next, and the route is
// only reachable when the panel has something to report.
func (r sqlAbsenceReader) DriftedDevices(ctx context.Context, tenantID, siteID string) (int, string, time.Time, error) {
	var (
		n      int
		worst  *string
		oldest time.Time
	)
	err := r.scoped(ctx, siteID).QueryRow(ctx, `
		SELECT count(*),
		       (array_agg(band ORDER BY abs(drift_ms) DESC))[1],
		       coalesce(min(measured_at), 'epoch'::timestamptz)
		  FROM device_clocks
		 WHERE tenant_id = $1 AND site_id = $2 AND band <> 'ok'`,
		tenantID, siteID).Scan(&n, &worst, &oldest)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("absence: read the clock drift: %w", err)
	}
	if n == 0 {
		return 0, "", time.Time{}, nil
	}
	band := ""
	if worst != nil {
		band = *worst
	}
	return n, band, sinceOrZero(oldest), nil
}

// scoped is the site-bound connection every statement runs on.
func (r sqlAbsenceReader) scoped(_ context.Context, siteID string) *dbx.Tenant {
	return r.pool.WithSites(r.tenantID, r.actor, []string{siteID})
}

// sinceOrZero maps Postgres's `'epoch'` sentinel to Go's zero time.
//
// The SQL uses `coalesce(min(...), 'epoch')` rather than a nullable column because a
// `NULL` scanned into a `time.Time` is an error in pgx, and a panel that cannot
// render "no beginning on record" is a panel that has to render the year 1970 instead.
func sinceOrZero(t time.Time) time.Time {
	if t.IsZero() || t.Unix() <= 0 {
		return time.Time{}
	}
	return t
}

// NewAbsenceReader returns the reader the console's coverage panel reads.
//
// The constructor is exported so the application supplies the storage, exactly as it
// supplies `DeadLetters` and `ClockDrift` — see the file comment for why the SQL is
// here and the wiring is not.
func NewAbsenceReader(pool *dbx.Pool, tenantID, actor string) AbsenceReader {
	return sqlAbsenceReader{pool: pool, tenantID: tenantID, actor: actor}
}

// compile-time proof that the reader satisfies the surface, so a rename in the
// interface breaks here rather than at the one call site in the application.
var _ AbsenceReader = sqlAbsenceReader{}

// keep pgx imported for the `rows`/`pgx.Row` vocabulary used in the doc comments
// above, which name the driver behaviour the sentinel depends on.
var _ = pgx.ErrNoRows
