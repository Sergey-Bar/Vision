package main

import (
	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The site's clock reader, wired to the site's pool.
//
// It is a one-line constructor because everything that matters about it is in
// `internal/api/sitetimezone.go`: the statement, and the rule that the zone is
// the one the `sites` row records — never the host's, never a guess.
//
// # Why the statement lives in `internal/api` and this constructor lives here
//
// `internal/api` owns the SQL and states the surface as `api.SiteTimezoneReader`;
// the application supplies it. That is the arrangement the absence reader and
// the dead-letter reader already use, and the reason is the same: `sites` is
// site-scoped under a RESTRICTIVE policy, and a second reader for it is where a
// missing scope goes to hide.

// NewSiteTimezoneReader returns the reader the API reads a site's clock with.
//
// `actor` is the identity the queries run under, and it is named rather than
// taken from the request: the site's zone is a fact about the site, not about
// the caller, and every caller that may read the site may read its clock. The
// tenant and the site come from the request's principal, because they are the
// scope the reader is allowed to read.
func NewSiteTimezoneReader(pool *dbx.Pool) api.SiteTimezoneReader {
	return api.NewSiteTimezoneReader(pool, "site-clock")
}
