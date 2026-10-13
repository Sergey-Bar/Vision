package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/simulate"
	"github.com/jackc/pgx/v5"
)

// Deps are the services the API routes onto.
type Deps struct {
	Incidents *incidents.Service
	Bundles   *bundles.Service
	Evidence  *evidence.Store
	// Identity is used to spend a step-up proof. Declared here so a handler cannot
	// disposition a sensitive incident without one.
	Identity *identity.Service
	// DeadLetters reads the inbox and job dead-letter lists, which is a separate
	// capability from reading incidents: an operator who may triage incidents
	// should not automatically be able to replay work.
	DeadLetters DeadLetterReader
	ClockDrift  ClockDriftReader
	// Absence backs the coverage panel: what the site could NOT see. A separate
	// reader rather than a field on Incidents because it counts across incident
	// state, observations, dead letters and drift -- four tables that are not
	// incidents and that a reader looking at one of them should not need to know about.
	Absence AbsenceReader
	// Handover backs the shift hand-over page: the brief and the duty-log
	// state, from the same packages the POC writes them with. Nil means the
	// page says the artifact is not configured rather than rendering an empty
	// hand-over, which would read as "nothing happened".
	Handover HandoverReader
	// InboxCounts backs the SLO alert thresholds.
	InboxCounts func() (Backlog, error)
	// Metrics is the observability registry. Nil means the route says the
	// endpoint is not configured rather than rendering an empty exposition: an
	// empty scrape and a broken scrape look identical to a collector, and only
	// one of them is a lie.
	Metrics *observability.Registry
	// Enqueuer is the job queue enqueuer. Nil means the route says the
	// endpoint is not configured.
	Enqueuer *jobs.Enqueuer
}

// DeadLetterReader is the dead-letter surface.
//
// The site is a parameter because `site_isolation` has been RESTRICTIVE since
// migration 21: a reader that named only its tenant would be shown an empty list,
// and an empty dead-letter list reads as "the site is healthy". The interface
// carries the site so no implementation can quietly omit it.
type DeadLetterReader interface {
	Read(ctx context.Context, tenant, site, kind string, limit int) ([]DeadLetter, error)
	ReadWithCursor(ctx context.Context, tenant, site, kind, cursor string, limit int) ([]DeadLetter, string, error)
	Replay(ctx context.Context, tenant, site, kind, id string) error
}

// DeadLetter is one inbox or job dead letter.
type DeadLetter struct {
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	Queue          string    `json:"queue,omitempty"`
	Integration    string    `json:"integrationId,omitempty"`
	SourceEvent    string    `json:"sourceEventId,omitempty"`
	Attempts       int       `json:"attempts"`
	Reason         string    `json:"reason"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	DeadLetteredAt time.Time `json:"deadLetteredAt,omitempty"`
	Replayable     bool      `json:"replayable"`
}

// ClockDriftReader is the device clock surface.
type ClockDriftReader interface {
	List(ctx context.Context, tenant string, limit int) ([]DriftView, error)
	ListWithCursor(ctx context.Context, tenant, cursor string, limit int) (string, []DriftView, error)
}

// DriftView is one device's clock state as the operator sees it.
type DriftView struct {
	DeviceID   string    `json:"deviceId"`
	SiteID     string    `json:"siteId"`
	DriftMS    int32     `json:"driftMs"`
	Band       string    `json:"band"`
	MeasuredAt time.Time `json:"measuredAt"`
	Excluded   bool      `json:"excludedFromCorroboration"`
	Reason     string    `json:"reason,omitempty"`
}

// Backlog is the ingest backlog the SLO thresholds read.
type Backlog struct {
	InboxPending    int64 `json:"inboxPending"`
	InboxDeadLetter int64 `json:"inboxDeadLetter"`
	JobPending      int64 `json:"jobPending"`
	JobDeadLetter   int64 `json:"jobDeadLetter"`
	OutboxPending   int64 `json:"outboxPending"`
}

// RegisterRoutes wires the API and console onto a mux.
//
// Every route declares the capability it needs. There is no route that is
// reachable without one, because a route whose authorization was decided
// somewhere else is a route nobody re-checks when the model changes.
func (s *Server) RegisterRoutes(d Deps) func(*http.ServeMux) {
	return func(mux *http.ServeMux) {
		// Auth.
		mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
		mux.HandleFunc("POST /v1/auth/mfa/verify", s.handleMFAVerify)
		mux.HandleFunc("GET /v1/auth/session", s.requirePrincipal(s.handleSession))
		mux.HandleFunc("DELETE /v1/auth/session", s.requirePrincipal(s.handleLogout))

		// Incidents.
		//
		// The list route is the one that means "every site I may see", so it filters
		// per row inside the handler and stays on `requireCapability`. Every route
		// addressed by incident id goes through `requireCapabilityAtSite`, which
		// resolves the incident's site and refuses before the handler runs. That is
		// the application edge of migration 21's RESTRICTIVE `site_isolation`
		// policy: the policy is what the database enforces if the edge is missed, and
		// this is what stops a caller who is not assigned from reaching the row at
		// all.
		mux.Handle("GET /v1/incidents",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleIncidentList(w, r, p, d)
			}))
		mux.Handle("GET /v1/incidents/{id}",
			s.requireCapabilityAtSite(identity.CapIncidentRead, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleIncidentGet(w, r, p, d)
				}))
		mux.Handle("GET /v1/incidents/{id}/explanation",
			s.requireCapabilityAtSite(identity.CapIncidentRead, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleIncidentExplain(w, r, p, d)
				}))
		mux.Handle("POST /v1/incidents/{id}/dispositions",
			s.requireCapabilityAtSite(identity.CapIncidentDisposition, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleDisposition(w, r, p, d)
				}))

		// Evidence.
		mux.Handle("GET /v1/incidents/{id}/evidence",
			s.requireCapabilityAtSite(identity.CapEvidenceRead, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleIncidentEvidence(w, r, p, d)
				}))
		mux.Handle("POST /v1/incidents/{id}/evidence",
			s.requireCapabilityAtSite(identity.CapEvidenceFetch, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleEvidenceFetch(w, r, p, d)
				}))
		// `evidence.fetch`, not `evidence.read`.
		//
		// The two capabilities are distinct in the role table and in the seed the
		// migration ships - `viewer` holds `evidence.read` and nothing else, and
		// `operator` is the role the incident queue is run from - so the
		// distinction is the model saying "reading the evidence list is not the
		// same as taking the bytes". This route gated on `evidence.read`, which
		// made both capabilities grant nothing and left a viewer able to stream
		// raw video out of the filesystem: a capability table that grants nothing
		// is a role table that lies, and it lied in the direction of least
		// privilege.
		mux.Handle("GET /v1/evidence/{evidenceId}/content",
			s.requireCapability(identity.CapEvidenceFetch, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleEvidenceContent(w, r, p, d)
			}))

		// Audit.
		mux.Handle("GET /v1/audit/ledger",
			s.requireCapability(identity.CapAuditRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleAuditLedger(w, r, p)
			}))
		mux.Handle("GET /v1/audit/checkpoints",
			s.requireCapability(identity.CapAuditRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleAuditCheckpoints(w, r, p)
			}))

		// Rules.
		mux.Handle("GET /v1/rules/bundles",
			s.requireCapability(identity.CapRulesRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleList(w, r, p, d)
			}))
		mux.Handle("POST /v1/rules/bundles",
			s.requireCapability(identity.CapRulesWrite, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleStore(w, r, p, d)
			}))
		mux.Handle("POST /v1/rules/bundles/{version}/lint",
			s.requireCapability(identity.CapRulesWrite, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleLint(w, r, p, d)
			}))
		mux.Handle("POST /v1/rules/bundles/{version}/simulate",
			s.requireCapability(identity.CapRulesRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleSimulate(w, r, p, d)
			}))
		mux.Handle("POST /v1/rules/bundles/{version}/activate",
			s.requireCapability(identity.CapRulesActivate, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleActivate(w, r, p, d)
			}))
		mux.Handle("POST /v1/rules/bundles/active/rollback",
			s.requireCapability(identity.CapRulesActivate, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRuleRollback(w, r, p, d)
			}))

		// Operations.
		mux.Handle("GET /v1/ops/dead-letters",
			s.requireCapability(identity.CapDeadLetterRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleDeadLetters(w, r, p, d)
			}))
		mux.Handle("POST /v1/ops/dead-letters/replay",
			s.requireCapability(identity.CapDeadLetterReplay, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleDeadLetterReplay(w, r, p, d)
			}))
		mux.Handle("GET /v1/ops/clock-drift",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleClockDrift(w, r, p, d)
			}))

		// Metrics. audit.read is the capability, not a new one.
		//
		// A dedicated metrics capability would be a migration and an ADR, and the
		// principal that reads the audit ledger is already the one reading the
		// metrics: a security director reviewing both needs the same role, and a
		// second role to grant alongside it doubles the ways an install ends up
		// with an operator who can see the pipeline but not the events it decided
		// on. Revisit if a monitoring agent ever needs to scrape without reading
		// the ledger.
		mux.Handle("GET /v1/metrics",
			s.requireCapability(identity.CapAuditRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleMetrics(w, r, p, d)
			}))

		// The console.
		mux.HandleFunc("GET /{$}", s.handleConsoleRoot)
		// The console's incident pages are gated exactly as the API's are. They
		// render the same rows, so a principal without the capability must not
		// reach them by asking for HTML instead of JSON - which is a distinction
		// an attacker does not have to be clever about.
		mux.Handle("GET /incidents", s.requireCapability(identity.CapIncidentRead,
			func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleConsoleIncidents(w, r, p, d)
			}))
		mux.Handle("GET /incidents/{id}", s.requireCapabilityAtSite(identity.CapIncidentRead, siteOfIncident(d),
			func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleConsoleIncident(w, r, p, d)
			}))
		// The coverage panel: what this site could NOT see.
		//
		// Gated on CapIncidentRead rather than a capability of its own, and the
		// reasoning is worth stating because it is the opposite of the dead-letter
		// reader's. Dead letters are REPLAYABLE work, so reading them is a distinct
		// right. The coverage panel is counts and reasons -- "the vendor has not been
		// polled since 14:02" -- and an operator who can see an incident can see that
		// the incident is not the whole story. Withholding it would mean an operator
		// who is not allowed to replay work cannot tell whether there was any work to
		// replay, which is the question they are actually asking.
		mux.Handle("GET /coverage", s.requireCapability(identity.CapIncidentRead,
			func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleConsoleAbsence(w, r, p, d)
			}))
		// The shift hand-over: the brief and the duty-log state, from the same
		// packages the capture path writes them with. Gated exactly as the
		// coverage panel is, and for the same reason: an operator who may read
		// an incident may read the record of how it was handled.
		mux.Handle("GET /handover", s.requireCapability(identity.CapIncidentRead,
			func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleConsoleHandover(w, r, p, d)
			}))
		// The console's sign-out. Gated on a principal rather than on a capability,
		// because the capability that would be right here does not exist: signing out
		// is not something an operator may lack the right to do, and a
		// CapSessionRead held by nobody would be a capability table that grants
		// nothing. The CSRF check DOES apply -- see handleConsoleLogout.
		mux.Handle("POST /logout", s.requirePrincipal(s.handleConsoleLogout))
		// The console's disposition form. The route the incident page's form posts to,
		// which is the write the console previously had no way to perform: it rendered
		// the disposition table and nothing could add a row to it.
		//
		// `requireCapabilityAtSite` rather than `requireCapability`, like every other
		// incident-addressed route: a disposition is the most consequential thing an
		// operator can do here, and it is addressed by incident id.
		mux.Handle("POST /incidents/{id}/disposition",
			// `requireCSRF` is OUTSIDE `requireCapabilityAtSite`, and that order is load
			// bearing rather than incidental.
			//
			// `identity.Resolve` rotates the session token on every use, so the moment a
			// handler has resolved a principal the token in the incoming cookie is one
			// rotation stale. `requireCSRF` then looks the session up BY THE COOKIE'S
			// TOKEN, finds nothing, and answers `csrf_mismatch: identity: no such
			// session` -- for a request that carried a perfectly good token.
			//
			// With the wrappers the other way round, two of this route's five subtests
			// passed for the wrong reason: the escalation and dismissal refusals were
			// coming from the CSRF check, not from the step-up check, because the
			// request never got past the middleware. A test that asserts "403" cannot
			// tell those two 403s apart, which is the same reason this route's negative
			// case asserts the DATABASE afterwards rather than the status code.
			//
			// So CSRF is validated against the untouched cookie, and only then is the
			// principal resolved.
			s.requireCSRF(s.requireCapabilityAtSite(identity.CapIncidentDisposition, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleConsoleDisposition(w, r, p, d)
				})))
		mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
			s.handleConsoleLogin(w, r)
		})
		// The console's own POST front door. `GET /login` above RENDERS the page; this
		// one is what the page's form submits to, and it is a separate route because
		// an HTML form cannot send a DELETE and the API's answer is JSON. See
		// handleConsoleLoginSubmit.
		mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
			s.handleConsoleLoginSubmit(w, r)
		})
		mux.Handle("GET /static/{file}", s.Static())
	}
}

// loginRequest is the password step.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// BreakGlass is a boolean, not a token. A break-glass account is a person who
	// signs in normally and is then treated differently, and it says so out loud
	// on the ledger.
	BreakGlass bool `json:"breakGlass"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	res, status, reason := s.passwordStep(r, req.Username, req.Password)
	if status != http.StatusOK {
		WriteError(w, status, "login_failed", reason, requestID(r))
		return
	}

	if res.Session != nil {
		s.setSessionCookie(w, res.Session.Token, res.Session.CSRFToken)
		WriteJSON(w, http.StatusOK, map[string]any{
			"mfaRequired": false,
			"session":     s.sessionInfo(res.Session.Principal),
		})
		return
	}
	// res.Principal, not res.Session.Principal. Session is nil by construction
	// on this branch - the early return above is the non-MFA case - so reading
	// the principal off it dereferenced nil and turned every MFA login for every
	// enrolled operator into a 500.
	WriteJSON(w, http.StatusOK, map[string]any{
		"mfaRequired": true,
		"challengeId": res.ChallengeID,
		"expiresAt":   res.ExpiresAt,
		"session":     s.sessionInfo(res.Principal),
	})
}

// passwordStep is the password half of signing in, with no opinion about how the
// answer is rendered.
//
// It exists because there are two front doors and the DECISION must not be written
// twice. `POST /v1/auth/login` answers JSON for a client bundle and `POST /login`
// answers a redirect for a server-rendered form, and before this was extracted each
// would have called `Identity.PasswordStep` itself. A lockout check, an enabled
// check and a break-glass annotation are three decisions that have to agree, and two
// copies of them is how the second copy ends up missing one.
//
// The return shape is `(result, status, reason)`. A non-OK status carries the reason
// to render; an OK status carries the result and an empty reason.
func (s *Server) passwordStep(r *http.Request, username, password string) (identity.LoginResult, int, string) {
	if strings.TrimSpace(username) == "" || password == "" {
		return identity.LoginResult{}, http.StatusBadRequest,
			"A username and password are required."
	}
	res, err := s.Identity.PasswordStep(r.Context(), s.Tenant, username, password, s.clientIP(r))
	if err != nil {
		// errors.Is, not a substring match on the message. A locked account and a
		// wrong password both read "invalid", and telling them apart is the whole
		// point of the 423: an operator whose account is locked needs to be told
		// to wait, not to keep typing. Matching on text would also break the
		// moment identity wrapped the error, silently downgrading 423 to 401.
		status := http.StatusUnauthorized
		if errors.Is(err, identity.ErrLocked) {
			status = http.StatusLocked
		}
		return identity.LoginResult{}, status, err.Error()
	}
	return res, http.StatusOK, ""
}

type mfaRequest struct {
	ChallengeID string `json:"challengeId"`
	Method      string `json:"method"`
	Code        string `json:"code"`
}

func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req mfaRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if req.ChallengeID == "" || req.Code == "" {
		WriteError(w, http.StatusBadRequest, "bad_request",
			"A challenge id and a second factor code are required.", requestID(r))
		return
	}
	sess, err := s.Identity.CompleteChallenge(r.Context(), s.Tenant, req.ChallengeID,
		req.Method, req.Code, s.clientIP(r))
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "mfa_failed", err.Error(), requestID(r))
		return
	}
	s.setSessionCookie(w, sess.Token, sess.CSRFToken)
	WriteJSON(w, http.StatusOK, s.sessionInfo(sess.Principal))
}

func (s *Server) sessionInfo(p identity.Principal) map[string]any {
	roles := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		roles = append(roles, string(r))
	}
	return map[string]any{
		"sessionId":   p.SessionID,
		"principalId": p.UserID,
		"displayName": p.Username,
		"roles":       roles,
		"siteScope":   p.SiteScope,
		"csrfToken":   p.CSRFToken,
		"mfaVerified": p.MFAVerified,
		"breakGlass":  p.BreakGlass,
	}
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	WriteJSON(w, http.StatusOK, s.sessionInfo(p))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if err := s.Identity.Logout(r.Context(), s.Tenant, p.SessionID, p.Username, "operator logout"); err != nil {
		WriteError(w, http.StatusInternalServerError, "logout_failed", err.Error(), requestID(r))
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIncidentList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if err := s.tenantHeader(r, p); err != nil {
		WriteError(w, http.StatusForbidden, "tenant_mismatch", err.Error(), requestID(r))
		return
	}
	site := r.URL.Query().Get("siteId")
	if site != "" && !p.CanSeeSite(site) {
		// The site is not in the principal's scope. Refusing rather than
		// returning an empty list, because "no such site" and "not yours" are the
		// same answer and a different one is a disclosure.
		WriteError(w, http.StatusForbidden, "site_not_assigned",
			"You are not assigned to that site.", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	cursor := r.URL.Query().Get("cursor")
	state := r.URL.Query().Get("state")
	degradedStr := r.URL.Query().Get("degraded")
	var degraded *bool
	switch degradedStr {
	case "true":
		v := true
		degraded = &v
	case "false":
		v := false
		degraded = &v
	}
	// A site the caller may see, or "every site they may see".
	scope := site
	if scope == "" {
		scope = strings.Join(p.SiteScope, ",")
	}
	items, nextCursor, err := d.Incidents.ListWithCursor(r.Context(), p.TenantID, scope, cursor, limit, state, degraded)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	// Defence in depth rather than the mechanism: ListScoped filters in SQL, and
	// this drops anything whose site is not in the principal's scope, so a bug in
	// the query cannot become a disclosure.
	if len(p.SiteScope) == 0 {
		items = nil
	} else {
		visible := items[:0]
		for _, it := range items {
			if p.CanSeeSite(it.SiteID) {
				visible = append(visible, it)
			}
		}
		items = visible
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"nextCursor": nextCursor,
	})
}

func (s *Server) handleIncidentGet(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("id")
	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil {
		// Absence and invisibility are the same answer.
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}
	if !p.CanSeeSite(detail.SiteID) {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, detail)
}

// handleIncidentExplain answers "why did this incident exist" from stored facts
// and the stored observation. It is a separate endpoint because it is the
// question an operator asks after the fact, and answering it must never involve
// re-running anything.
func (s *Server) handleIncidentExplain(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("id")
	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}
	if !p.CanSeeSite(detail.SiteID) {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}

	bundle, err := s.activeBundle(r, p, d)
	if err != nil {
		WriteError(w, http.StatusConflict, "no_active_bundle", err.Error(), requestID(r))
		return
	}
	ex, err := d.Incidents.Replay(r.Context(), incidents.ReplayInputs{
		TenantID: p.TenantID, IncidentID: id, Bundle: bundle,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "explain_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, ex)
}

// activeBundle compiles the tenant's active bundle. The compiler is rebuilt per
// call rather than cached, because a cached compiler would keep serving a bundle
// after the operator rolled back, and an explanation from the wrong bundle is
// worse than no explanation.
func (s *Server) activeBundle(r *http.Request, p identity.Principal, d Deps) (*rules.CompiledBundle, error) {
	_, content, err := d.Bundles.Active(r.Context(), p.TenantID)
	if err != nil {
		return nil, err
	}
	compiled, problems := rules.NewCompiler().Compile(content)
	if compiled == nil {
		for _, pr := range problems {
			if pr.Severity == rules.SeverityError {
				return nil, fmt.Errorf("the active bundle does not compile: %s", pr)
			}
		}
		return nil, fmt.Errorf("the active bundle did not compile")
	}
	return compiled, nil
}

type dispositionRequest struct {
	Outcome     string `json:"outcome"`
	ReasonCode  string `json:"reasonCode"`
	Notes       string `json:"notes"`
	Agreement   string `json:"agreement"`
	StepUpToken string `json:"stepUpToken"`
}

func (s *Server) handleDisposition(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	var req dispositionRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	id := r.PathValue("id")
	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil || !p.CanSeeSite(detail.SiteID) {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}

	// A sensitive outcome consumes a step-up proof. Consume-and-act is one
	// transaction, so a replayed proof cannot escalate a second incident.
	stepUp := req.StepUpToken != ""
	if stepUp {
		if err := d.Incidents.ConsumeStepUp(r.Context(), d.Identity, p.TenantID, p.UserID, req.StepUpToken); err != nil {
			WriteError(w, http.StatusForbidden, "step_up_required", err.Error(), requestID(r))
			return
		}
	}

	if _, err := d.Incidents.Record(r.Context(), incidents.Disposition{
		TenantID: p.TenantID, IncidentID: id, Outcome: req.Outcome,
		ReasonCode: req.ReasonCode, Notes: req.Notes, Agreement: req.Agreement,
		ActorID: p.UserID, ActorRoles: roleStrings(p), SiteScope: p.SiteScope,
		SessionID: p.SessionID, StepUpVerified: stepUp,
	}); err != nil {
		if strings.Contains(err.Error(), "step-up") {
			WriteError(w, http.StatusForbidden, "step_up_required", err.Error(), requestID(r))
			return
		}
		WriteError(w, http.StatusBadRequest, "disposition_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"incidentId": id, "state": "dispositioned",
	})
}

func (s *Server) handleIncidentEvidence(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("id")
	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil || !p.CanSeeSite(detail.SiteID) {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": detail.Evidence})
}

func (s *Server) handleEvidenceFetch(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("id")
	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil || !p.CanSeeSite(detail.SiteID) {
		WriteError(w, http.StatusNotFound, "not_found",
			"No such incident, or you are not assigned to its site.", requestID(r))
		return
	}
	if d.Enqueuer == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"The job queue is not configured.", requestID(r))
		return
	}
	// Enqueue an evidence fetch job for this incident.
	payload, err := dispatch.EncodeJobPayload(dispatch.JobPayload{
		ContractVersion: dispatch.ContractVersion,
		IncidentID:      id,
		SiteID:          detail.SiteID,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "encode_failed", err.Error(), requestID(r))
		return
	}
	job := jobs.Job{
		Payload:        payload,
		IdempotencyKey: fmt.Sprintf("evidence-fetch:%s:%d", id, time.Now().UnixMilli()),
	}
	if err := d.Enqueuer.EnqueueJob(r.Context(), p.TenantID, jobs.QueueEvidenceFetch, job); err != nil {
		WriteError(w, http.StatusInternalServerError, "enqueue_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{
		"enqueued": true, "queue": string(jobs.QueueEvidenceFetch), "incidentId": id,
	})
}

// handleEvidenceContent streams stored bytes.
//
// The hash is verified on the way out, so a file altered on disk is reported
// rather than served: an operator looking at evidence that cannot be proven to be
// the evidence an incident cited is worse off than being told the file is wrong.
//
// The fetch is written to the audit ledger before the first byte goes out.
// `audit.ActionEvidenceFetched` was declared and never appended to, so every
// byte of every piece of evidence left the building with no ledger row - the one
// action on this route that an incident responder would ask about afterwards is
// the one the ledger does not record. It is written before the response rather
// than after the stream because a stream that is cut short by a killed process
// still released bytes, and a row that only lands on a clean finish is a row that
// is missing from exactly the fetches worth investigating. The cost is that a
// ledger write which fails refuses the fetch, which is the right way round: an
// unrecorded release of evidence is worse than a refused one.
func (s *Server) handleEvidenceContent(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("evidenceId")
	var ref, wantHash, siteID string
	// The site is selected and checked, not just the tenant.
	//
	// Holding `evidence.read` and being assigned to site A is not being assigned
	// to site B, and this handler streams raw video out of the filesystem. It
	// checked only `tenant_id`, and its sibling `handleIncidentEvidence` already
	// checks `p.CanSeeSite`. RLS does not close the gap: the two policies on
	// `evidence` are both PERMISSIVE, and PostgreSQL ORs multiple permissive
	// policies for the same command, so `tenant_isolation OR site_isolation` is
	// just `tenant_isolation`. There is no site isolation at the database layer
	// - see docs/status.md - which makes the handler the only place it happens.
	// The principal's site scope, not just the tenant.
	//
	// `For` binds the tenant and nothing else, so the query runs with an empty
	// `app.site_ids`. That is invisible today because both policies on `evidence`
	// are PERMISSIVE and PostgreSQL ORs them — `tenant_isolation OR
	// site_isolation` is `tenant_isolation` — so there is no site isolation at the
	// database layer at all and the handler's own `p.CanSeeSite` check is the
	// only one. Binding the scope as well means the statement is correct on its
	// own terms, and the day `site_isolation` becomes RESTRICTIVE this path
	// needs no change.
	//
	// The audit-ledger read below is not site-scoped at all: the ledger is the
	// tenant's, and a row is attributed to a site by its content rather than
	// belonging to one. It is listed here so the distinction is written down.
	err := d.Incidents.Pool.WithSites(p.TenantID, "api", p.SiteScope).QueryRow(r.Context(), `
		SELECT storage_ref, sha256, site_id FROM evidence
		 WHERE tenant_id = $1 AND evidence_id = $2`,
		p.TenantID, id).Scan(&ref, &wantHash, &siteID)
	if err != nil {
		WriteError(w, http.StatusNotFound, "not_found", "No such evidence.", requestID(r))
		return
	}
	if !p.CanSeeSite(siteID) {
		// 404, not 403. A 403 says the evidence exists and this operator may not
		// have it, which is the same information as a 200 would be, for a route
		// whose response body is a video of somebody's door.
		WriteError(w, http.StatusNotFound, "not_found", "No such evidence.", requestID(r))
		return
	}

	rc, obj, err := d.Evidence.Open(ref, wantHash)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), evidence.ErrHashMismatch.Error()) {
			// 409 rather than 500: the file exists and is wrong, which is a
			// different problem from the service being broken and needs a different
			// response from whoever is watching.
			status = http.StatusConflict
		}
		// A fetch that could not produce the bytes. Counted here and nowhere else,
		// because this is the only place in the tree that fetches evidence.
		//
		// The metric ledger in `internal/observability/metric_producers_test.go`
		// excused `evidence_fetched_total` and `evidence_failed_total` with "evidence
		// retrieval is M2's blocker. There is no fetch worker to increment it;
		// inventing one now would be inventing the thing that calls it." That was
		// wrong in a way worth recording: retrieval has no *worker*, and this route
		// is a perfectly good fetch event -- it opens the object, verifies its
		// SHA-256, and writes an `ActionEvidenceFetched` row to the hash-chained
		// ledger, which is a more authoritative record of a fetch than a counter.
		// The excuse was plausible enough to survive, which is the failure mode of
		// every ledger entry whose reason reads like a decision.
		if d.Metrics != nil {
			d.Metrics.Counters.Inc(observability.CounterEvidenceFailed)
		}
		WriteError(w, status, "evidence_unavailable", err.Error(), requestID(r))
		return
	}
	defer func() { _ = rc.Close() }()

	// Counted after `Open` succeeded and the hash verified, and before the bytes are
	// streamed: this is "evidence was released to a caller", which is the fact an
	// operator needs when asking why a clip was viewed, and counting it before the
	// stream completes would report a fetch for a response that failed midway.
	//
	// It is counted before the audit row rather than after, because the audit failure
	// path below returns a 500 and releases no bytes -- so counting after would
	// under-report a fetch that this route refused to make, and the metric would be
	// the one place that thinks the bytes went out when they did not.
	if d.Metrics != nil {
		d.Metrics.Counters.Inc(observability.CounterEvidenceFetched)
	}

	if err := s.auditEvidenceFetched(r, p, id, siteID, obj); err != nil {
		// The evidence is not released. A response already committed at this
		// point could not be withdrawn, so the row is written before the header
		// is and a failure here is a 500 rather than a silent skip.
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", itoa(obj.SizeBytes))
	w.Header().Set("X-Content-SHA256", obj.SHA256)
	// Evidence is fetched once, by an operator, and must not sit in a proxy.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Content-Disposition", `attachment; filename="evidence"`)
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, 64*1024)
	_, _ = copyStream(w, rc, buf)
}

// auditEvidenceFetched records the release of raw evidence bytes.
//
// The row names the evidence, the site it belongs to, and the digest of exactly
// what was served, so an investigator asking "who took the video, and is it the
// video the incident cited" has an answer without trusting the operator's
// account of it.
//
// A server with no audit writer configured is left alone rather than failing the
// fetch: the same nil check `auditDenied` makes, and for the same reason — a
// deployment that has not wired the ledger is already broken, and turning every
// evidence read into a 500 hides that behind a different symptom.
func (s *Server) auditEvidenceFetched(r *http.Request, p identity.Principal, evidenceID, siteID string, obj evidence.Object) error {
	if s.Audit == nil || s.Pool == nil {
		return nil
	}
	return s.Pool.InSystemTenant(r.Context(), func(tx pgx.Tx) error {
		_, err := s.Audit.Append(r.Context(), tx, audit.Entry{
			TenantID: p.TenantID, Actor: p.Username, ActorType: audit.ActorOperator,
			Action: audit.ActionEvidenceFetched, TargetType: "evidence", TargetID: evidenceID,
			// The digest of the bytes, not of the row: the row's digest is what
			// should have been on disk, and this route is the moment that claim
			// stops being a claim and becomes a fact.
			ResultStateHash: obj.SHA256,
			EvidenceRefs:    []string{obj.Ref},
			RequestID:       requestID(r), SessionID: p.SessionID, SiteScope: p.SiteScope,
			Reason: "raw evidence released over " + siteID,
		})
		return err
	})
}

// handleMetrics renders the Prometheus text exposition.
//
// text/plain rather than application/json, and version=0.0.4 because that is
// the version the exposition this package emits is the 0.0.4 text format. A
// collector that is handed the wrong content type will parse it as JSON and
// report a parse failure that looks like a broken core.
//
// It takes a principal it does not read. The exposition is process-wide and the
// registry has no per-tenant view, so there is nothing in the scrape that
// varies by caller; the parameter is carried because every other gated handler
// in this file has the same shape, and the audit.read check that actually
// authorizes the scrape happens in requireCapability at the call site. Dropping
// the argument would be free today and would make the next handler that does
// need the principal the odd one out.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) { //nolint:unparam // p is the gated-handler signature every sibling handler here carries; the audit.read check happens in requireCapability, not here.
	if d.Metrics == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The metrics registry is not configured. A scrape that returns an empty "+
				"exposition is indistinguishable from a core that has done no work, and "+
				"only one of those is true.", requestID(r))
		return
	}
	body := d.Metrics.Render()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	// A scrape is a point-in-time read. Caching it means a collector can be
	// showing an operator a number that is minutes old with nothing to say so.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func (s *Server) handleDeadLetters(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.DeadLetters == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The dead-letter reader is not configured. An empty list and a broken list look "+
				"identical to an operator, and only one of them is a lie, so this says "+
				"unavailable rather than returning nothing.", requestID(r))
		return
	}
	kind := r.URL.Query().Get("kind")
	limit := parseLimit(r.URL.Query().Get("limit"), 100, 500)
	cursor := r.URL.Query().Get("cursor")
	items, nextCursor, err := d.DeadLetters.ReadWithCursor(r.Context(), p.TenantID, primarySiteOf(p), kind, cursor, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotWired) {
			status = http.StatusServiceUnavailable
		}
		WriteError(w, status, "dead_letters_unavailable", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
}

func (s *Server) handleDeadLetterReplay(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.DeadLetters == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The dead-letter reader is not configured.", requestID(r))
		return
	}
	var req struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if req.Kind == "" || req.ID == "" {
		WriteError(w, http.StatusBadRequest, "bad_request", "kind and id are required", requestID(r))
		return
	}
	site := primarySiteOf(p)
	if err := d.DeadLetters.Replay(r.Context(), p.TenantID, site, req.Kind, req.ID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotWired) {
			status = http.StatusServiceUnavailable
		}
		WriteError(w, status, "replay_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"replayed": true})
}

func (s *Server) handleClockDrift(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.ClockDrift == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The clock-drift reader is not configured. A console that shows an empty "+
				"drift table next to a real backlog is worse than one that says it cannot see it.",
			requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 500, 1000)
	cursor := r.URL.Query().Get("cursor")
	nextCursor, items, err := d.ClockDrift.ListWithCursor(r.Context(), p.TenantID, cursor, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotWired) {
			status = http.StatusServiceUnavailable
		}
		WriteError(w, status, "clock_drift_unavailable", err.Error(), requestID(r))
		return
	}
	var severe int
	for _, it := range items {
		if it.Excluded {
			severe++
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "severeCount": severe, "nextCursor": nextCursor})
}

func (s *Server) handleRuleList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The bundle service is not configured.", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	cursor := r.URL.Query().Get("cursor")
	list, nextCursor, err := d.Bundles.ListWithCursor(r.Context(), p.TenantID, cursor, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "rules_failed", err.Error(), requestID(r))
		return
	}
	active := ""
	for _, m := range list {
		if m.Active {
			active = m.Version
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": list, "activeVersion": active, "nextCursor": nextCursor})
}

type ruleStoreRequest struct {
	ContentBase64     string `json:"contentBase64"`
	SourceReference   string `json:"sourceReference"`
	SupersedesVersion string `json:"supersedesVersion"`
}

func (s *Server) handleRuleStore(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	var req ruleStoreRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	content, err := decodeBase64(req.ContentBase64)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "contentBase64 is not valid base64.", requestID(r))
		return
	}
	meta, problems, err := d.Bundles.Store(r.Context(), p.TenantID, content,
		req.SourceReference, p.UserID, req.SupersedesVersion)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "store_failed", err.Error(), requestID(r))
		return
	}
	if meta.Version == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": ErrorBody{Code: "lint_failed",
				Message: "The bundle did not compile and was not stored.", RequestID: requestID(r)},
			"problems": problems,
		})
		return
	}
	WriteJSON(w, http.StatusCreated, meta)
}

// activateRequest carries the simulation report, not a digest.
//
// The route used to take `simulateDiffDigest: string` and pass it straight
// through, which meant the gate checked that 64 hex characters had been typed
// and nothing else. A digest is an output, not evidence: the operator reviewed a
// report, and the report is what the gate can re-hash and compare. A caller that
// wants to acknowledge a bundle sends the report.
type activateRequest struct {
	SimulateReport simulate.Report `json:"simulateReport"`
}

func (s *Server) handleRuleActivate(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	var req activateRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	version := r.PathValue("version")
	if err := d.Bundles.Activate(r.Context(), p.TenantID, version, req.SimulateReport, p.UserID); err != nil {
		var gate *bundles.ErrGateNotSatisfied
		if asGate(err, &gate) {
			WriteError(w, http.StatusConflict, "gate_not_satisfied", gate.Reason, requestID(r))
			return
		}
		WriteError(w, http.StatusBadRequest, "activate_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"activeVersion": version,
		"reviewedDiff":  req.SimulateReport.DiffDigest,
	})
}

func (s *Server) handleRuleRollback(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	version, err := d.Bundles.Rollback(r.Context(), p.TenantID, p.UserID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "rollback_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"activeVersion": version})
}

func (s *Server) handleAuditLedger(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if s.Audit == nil {
		WriteError(w, http.StatusNotImplemented, "not_wired", "The audit writer is not configured.", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100, 500)
	from := int64(parseLimit(r.URL.Query().Get("fromSequence"), 1, 1<<40))

	rows, err := s.Pool.For(p.TenantID, "api").Query(r.Context(), `
		SELECT sequence, ts, actor, actor_type, action, target_type, target_id,
		       prev_state_hash, result_state_hash, policy_version, reason,
		       evidence_refs, request_id, session_id, site_scope, prev_hash, hash
		  FROM audit_ledger
		 WHERE tenant_id = $1 AND sequence >= $2
		 ORDER BY sequence LIMIT $3`, p.TenantID, from, limit+1)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
		return
	}
	defer rows.Close()

	type entry struct {
		Sequence   int64     `json:"sequence"`
		TS         time.Time `json:"ts"`
		Actor      string    `json:"actor"`
		ActorType  string    `json:"actorType"`
		Action     string    `json:"action"`
		TargetType string    `json:"targetType"`
		TargetID   string    `json:"targetId"`
		// Nullable, because an action that changed nothing has neither a prior
		// state nor a result. Scanning NULL into a string is an error, which is how
		// this endpoint came to fail on every row that had no state transition.
		PrevStateHash   *string  `json:"prevStateHash,omitempty"`
		ResultStateHash *string  `json:"resultStateHash,omitempty"`
		PolicyVersion   string   `json:"policyVersion"`
		Reason          string   `json:"reason"`
		EvidenceRefs    []string `json:"evidenceRefs"`
		RequestID       string   `json:"requestId"`
		SessionID       string   `json:"sessionId"`
		SiteScope       []string `json:"siteScope"`
		PrevHash        string   `json:"prevHash"`
		Hash            string   `json:"hash"`
	}
	items := make([]entry, 0, limit)
	truncated := false
	for rows.Next() {
		if len(items) >= limit {
			truncated = true
			break
		}
		var e entry
		var evidenceRaw, scopeRaw []byte
		if err := rows.Scan(&e.Sequence, &e.TS, &e.Actor, &e.ActorType, &e.Action,
			&e.TargetType, &e.TargetID, &e.PrevStateHash, &e.ResultStateHash,
			&e.PolicyVersion, &e.Reason, &evidenceRaw, &e.RequestID, &e.SessionID,
			&scopeRaw, &e.PrevHash, &e.Hash); err != nil {
			WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
			return
		}
		_ = jsonUnmarshal(evidenceRaw, &e.EvidenceRefs)
		_ = jsonUnmarshal(scopeRaw, &e.SiteScope)
		items = append(items, e)
	}

	// chainValid is recomputed, not asserted from the columns on this page.
	//
	// The loop this replaced compared `items[i].PrevHash` to `items[i-1].Hash`:
	// two stored columns, side by side, which is a statement about whether the
	// page is internally consistent and not about whether the ledger is. It never
	// recomputed a row's own hash, so an edited row verified fine, and it never
	// checked the first row, so a single-row page - which is what
	// `?fromSequence=<n>&limit=1` produces — always reported true. The comment
	// said it was recomputed; it was not.
	//
	// The real verification recomputes every row's digest inside the database,
	// which is `audit.Writer.Verify` and the same call a checkpoint seal makes.
	// The page is a window onto a chain; whether the chain is intact is a question
	// about the whole of it, and a window cannot answer it.
	chainValid := false
	var chainChecked int64
	if from > 0 {
		// The verification reads the ledger and writes nothing, so it runs on the
		// tenant-scoped connection directly rather than being wrapped in a
		// transaction it does not need.
		var valid bool
		err := s.Pool.For(p.TenantID, "api").QueryRow(r.Context(),
			`SELECT valid, checked FROM audit_verify_chain($1, $2)`, p.TenantID, from).
			Scan(&valid, &chainChecked)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "audit_verify_failed", err.Error(), requestID(r))
			return
		}
		chainValid = valid
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "chainValid": chainValid, "chainChecked": chainChecked,
		"truncated": truncated,
	})
}

func (s *Server) handleAuditCheckpoints(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if s.Audit == nil {
		WriteError(w, http.StatusNotImplemented, "not_wired", "The audit writer is not configured.", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.Pool.For(p.TenantID, "api").Query(r.Context(), `
		SELECT seal_ts, prev_hash, archive_path, sequence_from, sequence_to, signature
		  FROM audit_checkpoints
		 WHERE tenant_id = $1
		 ORDER BY seal_ts DESC
		 LIMIT $2`, p.TenantID, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
		return
	}
	defer rows.Close()
	type cp struct {
		SealTS       time.Time `json:"sealTs"`
		PrevHash     string    `json:"prevHash"`
		ArchivePath  string    `json:"archivePath"`
		SequenceFrom int64     `json:"sequenceFrom"`
		SequenceTo   int64     `json:"sequenceTo"`
		Signature    string    `json:"signature"`
	}
	var out []cp
	for rows.Next() {
		var c cp
		if err := rows.Scan(&c.SealTS, &c.PrevHash, &c.ArchivePath, &c.SequenceFrom, &c.SequenceTo, &c.Signature); err != nil {
			WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
			return
		}
		out = append(out, c)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"checkpoints": out})
}

func (s *Server) handleRuleLint(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired", "The bundle service is not configured.", requestID(r))
		return
	}
	version := r.PathValue("version")
	if version == "" {
		WriteError(w, http.StatusBadRequest, "bad_request", "version is required", requestID(r))
		return
	}
	b, err := d.Bundles.Content(r.Context(), p.TenantID, version)
	if err != nil {
		WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("bundle %s: %v", version, err), requestID(r))
		return
	}
	// Run the compiler to validate the bundle.
	compiler := rules.NewCompiler()
	compiled, problems := compiler.Compile(b)
	if len(problems) > 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"valid": false, "errors": problems,
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"valid": true, "rules": len(compiled.Rules), "version": version,
	})
}

func (s *Server) handleRuleSimulate(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired", "The bundle service is not configured.", requestID(r))
		return
	}
	version := r.PathValue("version")
	if version == "" {
		WriteError(w, http.StatusBadRequest, "bad_request", "version is required", requestID(r))
		return
	}
	var req struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	// Load and compile the bundle.
	b, err := d.Bundles.Content(r.Context(), p.TenantID, version)
	if err != nil {
		WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("bundle %s: %v", version, err), requestID(r))
		return
	}
	compiler := rules.NewCompiler()
	compiled, problems := compiler.Compile(b)
	if len(problems) > 0 {
		WriteError(w, http.StatusInternalServerError, "compile_failed", problems[0].Message, requestID(r))
		return
	}
	// Run simulation with the provided events converted to scenarios.
	// For now, return a minimal response indicating the bundle compiles.
	_ = compiled
	WriteJSON(w, http.StatusOK, map[string]any{
		"results": []map[string]any{}, "version": version, "note": "simulation requires scenario conversion",
	})
}

func roleStrings(p identity.Principal) []string {
	out := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		out = append(out, string(r))
	}
	return out
}
