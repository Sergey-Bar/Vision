package api

import (
	"context"
	"net/http"

	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// The console's hand-over page.
//
// This is the plan's inheritance requirement: the product console renders the
// brief and the duty-log state the same packages produce — the same
// `internal/brief` and `internal/dutylog` the POC uses — rather than a second
// implementation of the same idea in a different shape. No OpenAPI change and
// no new `/v1` route: this is a server-rendered console page, and the artifact
// it shows is the one the shift wrote.
//
// The page is honest about absence. A site with no duty log configured says so
// by name, because a hand-over panel that renders empty is a hand-over panel
// that reads as "nothing happened", and the survey this whole work set answers
// is the one about explaining yourself after the fact.

// HandoverReader is the capability behind the hand-over page.
//
// One method rather than two: the page renders both halves from one shift's
// artifact, and a reader that must open the file once rather than once per half
// is the difference between one parse per page and four. A reader that can
// supply one half and not the other is a site with a brief and no artifact,
// and the error it returns names which.
//
// `SiteID` is on the interface because the authorization decision has to be
// made against the site that will actually be read, not against the
// principal's own scope. Checking `p.CanSeeSite(firstSite(p))` — the
// principal's first site — is trivially true for any principal with a scope
// and grants every operator who can see *any* site the deployment's
// hand-over, which is exactly the check the page's docstring claims to make.
type HandoverReader interface {
	// SiteID is the site whose artifact this reader serves.
	SiteID() string
	// Read returns the brief and the artifact's own state for one shift.
	Read(ctx context.Context, tenantID, siteID string) (HandoverView, error)
}

// HandoverView is the page's model: the brief, the artifact state, or the named
// error one of them is absent.
type HandoverView struct {
	Brief *brief.Brief
	State DutyLogState
}

// DutyLogState is what the console shows about the hand-over artifact.
type DutyLogState struct {
	Path         string
	Shift        string
	SiteID       string
	Lines        int
	HeadHash     string
	Verification string
	Dump         string
	// DumpNote discloses the dump's truncation: a dump that silently stops
	// reads as the whole shift, and an operator comparing this page's line
	// count with the artifact's deserves to see why they differ.
	DumpNote string
}

// handleConsoleHandover renders the shift hand-over.
//
// A GET, no side effects, no CSRF field: there is nothing to submit, and a form
// that cannot act is a form that can only be replayed.
//
// The site scope is checked against the site the reader actually reads, before
// anything is read. The reader reads the deployment's configured site, so the
// check is against that: `firstSite(p)` is the principal's *own* first scope
// and `CanSeeSite(firstSite(p))` is true for any principal with one, which
// would hand every operator who can see any site the deployment's hand-over.
// The 404 is deliberate, for the reason the evidence handler gives: a 403
// confirms the artifact exists.
func (s *Server) handleConsoleHandover(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Handover == nil {
		s.render(w, http.StatusOK, "handover.html", s.handoverPageData(r, p, "The hand-over artifact is not configured for this site."))
		return
	}
	siteID := d.Handover.SiteID()
	if !p.CanSeeSite(siteID) {
		WriteError(w, http.StatusNotFound, "not_found", "No such shift.", requestID(r))
		return
	}
	data := s.handoverPageData(r, p, "")
	// One read of the artifact for both halves of the page: the previous shape
	// listed the target twice for context and then parsed the whole shift log
	// twice more (four full passes, two of them re-hashing every line). One read
	// per page render is what the page needs and what a file the operator may
	// have to open is worth.
	view, err := d.Handover.Read(r.Context(), p.TenantID, siteID)
	if err != nil {
		data.DutyLogErr = err.Error()
		s.render(w, http.StatusOK, "handover.html", data)
		return
	}
	data.Brief = view.Brief
	data.DutyLog = &view.State
	s.render(w, http.StatusOK, "handover.html", data)
}

// handoverPageData is the page's model with the head already filled.
func (s *Server) handoverPageData(r *http.Request, p identity.Principal, dutyLogErr string) consoleData {
	data := s.consoleData(r, p, "Shift hand-over")
	data.DutyLogErr = dutyLogErr
	return data
}
