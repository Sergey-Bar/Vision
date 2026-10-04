package main

import (
	"github.com/Sergey-Bar/Ai-Captain/internal/api"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// The coverage panel's reader, wired to the site's pool.
//
// It is a one-line constructor because everything that matters about it is in
// `internal/api/absence_sql.go`: the four statements, and the rule that each is a
// COUNT, a MIN or a MODE and never a row. The panel reports how much and when, never
// which, because a panel that could name the observation with the oldest timestamp
// would be a way to read a row through a page gated on a different capability from the
// one that reads observations.
//
// # Why the statements live in `internal/api` and this constructor lives here
//
// `internal/api` owns the SQL and states the surface as `api.AbsenceReader`; the
// application supplies it. That is the arrangement the dead-letter and clock-drift
// readers already use, and the reason is the tables: `integrations`,
// `ai_observations` and `device_clocks` belong to the packages that own them, and a
// second reader for a tenant-scoped table is where a missing RLS predicate goes to
// hide.

// NewAbsenceReader returns the reader the console's coverage panel reads.
//
// `actor` is the identity the queries run under, and it is named rather than taken
// from the request: this is a read-only count and a principal who may read incidents
// may read coverage, so there is nothing per-principal about it. The site scope comes
// from `WithSites` inside the reader, once per statement, so a tenant admin and a
// site operator reading the same page at the same moment get the same rows for their
// own site and neither gets the other's.
func NewAbsenceReader(pool *dbx.Pool, tenantID string) api.AbsenceReader {
	return api.NewAbsenceReader(pool, tenantID, "coverage-panel")
}
