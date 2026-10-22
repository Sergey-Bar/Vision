package main

// The console server's scope.
//
// It is CONFIGURED, and it is the only thing that decides which tenant a
// request may read.
//
// It used not to be. The console server read `X-Tenant-ID` and `X-Site-IDs`
// out of the request and used them as the tenant scope, and when the headers
// were absent it guessed with `SELECT tenant_id FROM sites LIMIT 1`.
//
// Three things were wrong with that, and each of them is a defect on its own:
//
//   1. There is no session to match a header against. The console has no
//      login, so ANY header value is attacker-controlled: a request that said
//      `X-Tenant-ID: someone-else` read someone else's records. The header
//      was not an authorisation, it was an instruction.
//   2. The fallback guessed. `LIMIT 1` on the sites table is "whichever
//      tenant happens to be first", which is a cross-tenant read on a
//      multi-tenant database and an arbitrary one on a single-tenant one.
//   3. The pool it used was opened with whatever `SP_DATABASE_URL` said, and
//      the demo scripts set that to a SUPERUSER connection, so the row-level
//      security policies did not apply to the console's own queries at all.
//      The intent in migration 0008 is explicit: RLS constrains "the
//      platform's own connections, including the ones serving a console
//      request".
//
// So the scope comes from the same configuration the product uses -
// `SP_TENANT_ID` and `SP_PRINCIPAL_SITES` - and a missing tenant is an
// ERROR, not a guess. The headers are not read at all: there is no code path
// that consults them, and `TestTheConsoleNeverReadsATenantFromAHeader` says
// so.

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// scope is the tenant and the site scope the console server acts as.
type scope struct {
	// tenant is the tenant every request is scoped to.
	tenant string
	// sites is the site scope. An empty list means EVERY site in the
	// tenant, which is a scope the operator configured deliberately.
	sites []string
	// sitesDeclared says whether the operator named the sites at all, as
	// opposed to the list being empty because nothing was configured.
	sitesDeclared bool
}

// configuredScope reads the scope from the environment.
//
// The keys are the product's own: the console server and the product act as
// the same principal, and two sets of keys for one principal is two answers
// to the question of who the console is.
func configuredScope() scope {
	s := scope{tenant: strings.TrimSpace(os.Getenv("SP_TENANT_ID"))}
	raw := strings.TrimSpace(os.Getenv("SP_PRINCIPAL_SITES"))
	if raw != "" {
		s.sitesDeclared = true
		for _, site := range strings.Split(raw, ",") {
			if site = strings.TrimSpace(site); site != "" {
				s.sites = append(s.sites, site)
			}
		}
	}
	// A single-site install names one site in SP_SITE_ID, and the principal
	// sites are the same list. The product's own configuration treats the
	// two the same way.
	if !s.sitesDeclared {
		if one := strings.TrimSpace(os.Getenv("SP_SITE_ID")); one != "" {
			s.sitesDeclared = true
			s.sites = []string{one}
		}
	}
	return s
}

// errNoScope is what a request gets when the console server has not been told
// which tenant it serves.
var errNoScope = scopeError("the console server is not configured with a tenant: set SP_TENANT_ID")

type scopeError string

func (e scopeError) Error() string { return string(e) }

// resolve returns the tenant and the site scope for a request.
//
// The tenant is the configured one. There is no other answer.
//
// The site list, when the operator declared none, is the tenant's own sites:
// that is a query WITHIN the tenant, which is not a disclosure. When there
// is no pool there is no list, and the caller gets an empty scope, which the
// queries treat as "every site in the tenant".
func (s scope) resolve(pool *dbx.Pool) (string, []string, error) {
	if s.tenant == "" {
		return "", nil, errNoScope
	}
	sites := s.sites
	if !s.sitesDeclared && pool != nil {
		rows := make([]string, 0, 4)
		rr, err := pool.WithSites(s.tenant, "consoleScope", nil).Query(context.Background(),
			`SELECT site_id FROM sites WHERE tenant_id = $1 ORDER BY site_id`, s.tenant)
		if err == nil {
			defer rr.Close()
			for rr.Next() {
				var site string
				if err := rr.Scan(&site); err == nil {
					rows = append(rows, site)
				}
			}
		}
		sites = rows
	}
	return s.tenant, sites, nil
}

// describeScope prints the scope, for the startup log.
//
// It is printed so that an operator can see WHICH tenant the console is
// looking at without reading the configuration, and it is printed once.
func (s scope) describeScope() {
	if s.tenant == "" {
		log.Printf("console: no tenant is configured; set SP_TENANT_ID or the console will refuse every request")
		return
	}
	if s.sitesDeclared {
		log.Printf("console: acting as tenant %q, sites %s", s.tenant, strings.Join(s.sites, ", "))
		return
	}
	log.Printf("console: acting as tenant %q, every site in it", s.tenant)
}
