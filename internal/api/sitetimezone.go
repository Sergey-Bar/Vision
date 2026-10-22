package api

// The site's clock.
//
// # Why it is read from the database
//
// The zone is a column on the `sites` row, and migration 0001 says out loud what
// it is for: "Rendering law: dates cross the site timezone, never the host's."
// The column existed from the first migration and no surface read it, so the
// site's clock was unreachable by every surface at once: the console's own
// renderer pinned UTC, the browser's clock was the browser's, and the same
// record was read in three zones.
//
// # What it is NOT
//
// It is not a fallback and it is not a default. There is no "UTC when we cannot
// tell" here, because a zone that is guessed is a wrong clock, and a wrong
// clock is worse than an absent one: an absent one can be noticed.
//
// # Where the storage lives
//
// The reader is a `Deps` field and the application supplies it, exactly as it
// supplies the absence reader, the dead-letter reader and the clock-drift
// reader. The reason is the same in every case: the console states the surface
// it needs, and the application supplies the storage that owns those tables.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// SiteTimezoneReader reads the timezone a site's times are rendered in.
//
// The interface is one method because the answer is one fact. A reader that
// returned the site's whole record would be a second reader for the `sites`
// table, and the second reader for a tenant-scoped table is where a missing
// RLS predicate goes to hide.
type SiteTimezoneReader interface {
	// Timezone returns the site's zone, as the site's row records it.
	//
	// An empty zone with a nil error means the site has no row: there is no
	// clock to report, and the caller omits the field rather than inventing
	// one. An error means the read itself failed, and the caller says so.
	Timezone(ctx context.Context, tenantID, siteID string) (string, error)
}

// sqlSiteTimezoneReader reads the zone from the `sites` table.
//
// The tenant arrives per call rather than being held, because `*dbx.Pool` is
// long-lived and a reader that captured a tenant at construction time would
// outlive the scope it was built under. One reader serves every site and every
// tenant the API is configured for.
type sqlSiteTimezoneReader struct {
	pool  *dbx.Pool
	actor string
}

// siteTimezoneSQL reads one site's zone.
//
// The site is named in the WHERE clause as well as through the site scope,
// because `sites` carries a RESTRICTIVE `site_isolation` policy since
// migration 0023: a tenant-scoped, site-unscoped read of `sites` returns
// nothing at all, and a reader that read nothing would report "this site has
// no timezone" for a site that has one.
const siteTimezoneSQL = `
	SELECT timezone
	  FROM sites
	 WHERE tenant_id = $1 AND site_id = $2`

// Timezone reads the site's zone.
func (r sqlSiteTimezoneReader) Timezone(ctx context.Context, tenantID, siteID string) (string, error) {
	if r.pool == nil {
		return "", errors.New("api: the site timezone reader has no pool")
	}
	// No tenant and no site is not a read that can succeed, and it is not an
	// error either: there is no site to have a clock.
	if tenantID == "" || siteID == "" {
		return "", nil
	}
	var zone string
	err := r.pool.WithSites(tenantID, r.actor, []string{siteID}).
		QueryRow(ctx, siteTimezoneSQL, tenantID, siteID).Scan(&zone)
	switch {
	case err == nil:
		return zone, nil
	case errors.Is(err, pgx.ErrNoRows):
		// The site has no row, so there is no clock to report. This is not a
		// failure: the caller says the clock is unknown rather than failing a
		// read over a missing fact.
		return "", nil
	default:
		return "", fmt.Errorf("api: read the timezone of site %s: %w", siteID, err)
	}
}

// NewSiteTimezoneReader returns the reader the API reads a site's clock with.
//
// The constructor is exported so the application supplies the storage, exactly
// as it supplies the absence reader and the dead-letter reader — see the file
// comment for why the SQL is here and the wiring is not.
func NewSiteTimezoneReader(pool *dbx.Pool, actor string) SiteTimezoneReader {
	return sqlSiteTimezoneReader{pool: pool, actor: actor}
}

// compile-time proof that the reader satisfies the surface, so a rename in the
// interface breaks here rather than at the one call site in the application.
var _ SiteTimezoneReader = sqlSiteTimezoneReader{}
