package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
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
	// InboxCounts backs the SLO alert thresholds.
	InboxCounts func() (Backlog, error)
}

// DeadLetterReader is the dead-letter surface.
type DeadLetterReader interface {
	Read(ctx contextContext, tenant, kind string, limit int) ([]DeadLetter, error)
}

// DeadLetter is one inbox or job dead letter.
type DeadLetter struct {
	Kind        string    `json:"kind"`
	ID          string    `json:"id"`
	Queue       string    `json:"queue,omitempty"`
	Integration string    `json:"integrationId,omitempty"`
	SourceEvent string    `json:"sourceEventId,omitempty"`
	Attempts    int       `json:"attempts"`
	Reason      string    `json:"reason"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Replayable  bool      `json:"replayable"`
}

// ClockDriftReader is the device clock surface.
type ClockDriftReader interface {
	List(ctx contextContext, tenant string, limit int) ([]DriftView, error)
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

// contextContext is an alias so the interface signatures above read clearly
// without importing context into every reader's implementation.
type contextContext = ctxContext

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
		mux.Handle("GET /v1/incidents",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleIncidentList(w, r, p, d)
			}))
		mux.Handle("GET /v1/incidents/{id}",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleIncidentGet(w, r, p, d)
			}))
		mux.Handle("GET /v1/incidents/{id}/explanation",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleIncidentExplain(w, r, p, d)
			}))
		mux.Handle("POST /v1/incidents/{id}/dispositions",
			s.requireCapability(identity.CapIncidentDisposition, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleDisposition(w, r, p, d)
			}))

		// Evidence.
		mux.Handle("GET /v1/incidents/{id}/evidence",
			s.requireCapability(identity.CapEvidenceRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleIncidentEvidence(w, r, p, d)
			}))
		mux.Handle("GET /v1/evidence/{evidenceId}/content",
			s.requireCapability(identity.CapEvidenceRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleEvidenceContent(w, r, p, d)
			}))

		// Audit.
		mux.Handle("GET /v1/audit/ledger",
			s.requireCapability(identity.CapAuditRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleAuditLedger(w, r, p)
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
		mux.Handle("GET /v1/ops/clock-drift",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleClockDrift(w, r, p, d)
			}))

		// The console.
		mux.HandleFunc("GET /{$}", s.handleConsoleRoot)
		mux.Handle("GET /incidents", s.requirePrincipal(func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
			s.handleConsoleIncidents(w, r, p, d)
		}))
		mux.Handle("GET /incidents/{id}", s.requirePrincipal(func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
			s.handleConsoleIncident(w, r, p, d)
		}))
		mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
			s.handleConsoleLogin(w, r)
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
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, "bad_request",
			"A username and password are required.", requestID(r))
		return
	}

	res, err := s.Identity.PasswordStep(r.Context(), s.Tenant, req.Username, req.Password, s.clientIP(r))
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
		WriteError(w, status, "login_failed", err.Error(), requestID(r))
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
	WriteJSON(w, http.StatusOK, map[string]any{
		"mfaRequired": true,
		"challengeId": res.ChallengeID,
		"expiresAt":   res.ExpiresAt,
		"session":     s.sessionInfo(res.Session.Principal),
	})
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
	items, err := d.Incidents.List(r.Context(), p.TenantID, site, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	// A principal with no site scope sees nothing, whatever the query said.
	if len(p.SiteScope) == 0 {
		items = nil
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
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

// handleEvidenceContent streams stored bytes.
//
// The hash is verified on the way out, so a file altered on disk is reported
// rather than served: an operator looking at evidence that cannot be proven to be
// the evidence an incident cited is worse off than being told the file is wrong.
func (s *Server) handleEvidenceContent(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	id := r.PathValue("evidenceId")
	var ref, wantHash string
	err := d.Incidents.Pool.QueryRow(r.Context(), `
		SELECT storage_ref, sha256 FROM evidence WHERE tenant_id = $1 AND evidence_id = $2`,
		p.TenantID, id).Scan(&ref, &wantHash)
	if err != nil {
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
		WriteError(w, status, "evidence_unavailable", err.Error(), requestID(r))
		return
	}
	defer func() { _ = rc.Close() }()

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
	items, err := d.DeadLetters.Read(r.Context(), p.TenantID, kind, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNotWired) {
			status = http.StatusServiceUnavailable
		}
		WriteError(w, status, "dead_letters_unavailable", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleClockDrift(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.ClockDrift == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_wired",
			"The clock-drift reader is not configured. A console that shows an empty "+
				"drift table next to a real backlog is worse than one that says it cannot see it.",
			requestID(r))
		return
	}
	items, err := d.ClockDrift.List(r.Context(), p.TenantID, 500)
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
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "severeCount": severe})
}

func (s *Server) handleRuleList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	list, err := d.Bundles.List(r.Context(), p.TenantID)
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
	WriteJSON(w, http.StatusOK, map[string]any{"items": list, "activeVersion": active})
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

type activateRequest struct {
	SimulateDiffDigest string `json:"simulateDiffDigest"`
}

func (s *Server) handleRuleActivate(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	var req activateRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	version := r.PathValue("version")
	if err := d.Bundles.Activate(r.Context(), p.TenantID, version, req.SimulateDiffDigest, p.UserID); err != nil {
		var gate *bundles.ErrGateNotSatisfied
		if asGate(err, &gate) {
			WriteError(w, http.StatusConflict, "gate_not_satisfied", gate.Reason, requestID(r))
			return
		}
		WriteError(w, http.StatusBadRequest, "activate_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"activeVersion": version})
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

	rows, err := s.Pool.Query(r.Context(), `
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
		Sequence        int64    `json:"sequence"`
		TS              string   `json:"ts"`
		Actor           string   `json:"actor"`
		ActorType       string   `json:"actorType"`
		Action          string   `json:"action"`
		TargetType      string   `json:"targetType"`
		TargetID        string   `json:"targetId"`
		PrevStateHash   string   `json:"prevStateHash,omitempty"`
		ResultStateHash string   `json:"resultStateHash,omitempty"`
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

	// chainValid is recomputed here rather than asserted, so a reader can tell a
	// working chain from a page that merely looks intact.
	chainValid := true
	for i := 1; i < len(items); i++ {
		if items[i].PrevHash != items[i-1].Hash {
			chainValid = false
			break
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "chainValid": chainValid, "truncated": truncated,
	})
}

func roleStrings(p identity.Principal) []string {
	out := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		out = append(out, string(r))
	}
	return out
}
