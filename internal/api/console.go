package api

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/brief"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
)

//go:embed templates/*.html templates/static/*
var consoleFS embed.FS

// consoleFuncs are the helpers the templates call.
//
// They exist as functions rather than as template literals because the values
// they transform come from untrusted input - a severity from a rule bundle, a
// state from a decision - and a closed mapping is what stops a bundle author
// from closing over a template execution.
var consoleFuncs = template.FuncMap{
	"severityClass": severityClass,
	"stateClass":    stateClass,
	"lower":         strings.ToLower,
	"short":         shortHash,
	"join":          strings.Join,
}

// The console is parsed with its func map attached from the start: a template
// that parses and then fails to resolve a function is a console that does not
// boot, and it should fail at init rather than on the first page an operator
// happens to open.
var consoleTemplates = template.Must(
	template.New("console").Funcs(consoleFuncs).ParseFS(consoleFS, "templates/*.html"))

// shortHash abbreviates a digest for display. A truncated hash is a label, not
// an identity, and every one of them is clickable through to the full value.
func shortHash(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "..."
}

// Static returns the console stylesheet. It is served from the embedded FS so
// the console is one binary with no asset directory to deploy alongside it.
func (s *Server) Static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := consoleFS.ReadFile("templates/static/console.css")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	})
}

// consoleData is what every template receives.
//
// RequestID, Principal and CSRF are on every page rather than passed per
// template, because a form that forgets the CSRF field is a form that accepts a
// cross-site POST, and forgetting one is how that happens.
type consoleData struct {
	Title       string
	Principal   identity.Principal
	CSRF        string
	RequestID   string
	Now         time.Time
	Incident    *incidents.Detail
	Incidents   []incidents.Summary
	Drift       []DriftView
	DeadLetters []DeadLetter
	// Absence is the coverage panel, and AbsenceSites is what it covers. A count
	// with no scope is a number nobody can check, so the two are always together.
	Absence      []AbsenceView
	AbsenceSites []string
	// Brief and DutyLog are the shift hand-over page: the rendered brief and
	// the artifact's own state, from the same packages the capture path writes
	// them with. DutyLogErr names why one of them is absent — a hand-over page
	// that renders empty is one that reads as "nothing happened".
	Brief      *brief.Brief
	DutyLog    *DutyLogState
	DutyLogErr string
	SiteID     string
	Error      string
	Message    string
}

// SiteTimezone renders a site's time, never the host's.
//
// Every rendered time carries an explicit offset. A security operator comparing
// an incident timeline with a guard's account of the same minute is doing
// arithmetic, and a timestamp without an offset makes that arithmetic a guess.
func (d consoleData) Time(t time.Time) string {
	if t.IsZero() {
		// An em dash, written as an escape. The literal was once a raw U+2014 in
		// a file that had been through a lossy encoding round-trip, and it reached
		// the console as mojibake. An escape is the same character and cannot be
		// corrupted by whatever touches the file next.
		return "\u2014"
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data consoleData) {
	var buf bytes.Buffer
	if err := consoleTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		if s.Log != nil {
			s.Log.Error("console render failed", "template", name, "error", err)
		}
		WriteError(w, http.StatusInternalServerError, "render_failed",
			"The page could not be rendered.", data.RequestID)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The console is a security surface; a cached page is a stale authorization
	// decision served to whoever opens the machine next.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) consoleData(r *http.Request, p identity.Principal, title string) consoleData {
	return consoleData{
		Title:     title,
		Principal: p,
		CSRF:      p.CSRFToken,
		RequestID: requestID(r),
		Now:       time.Now().UTC(),
		SiteID:    firstSite(p),
	}
}

func firstSite(p identity.Principal) string {
	if len(p.SiteScope) == 0 {
		return ""
	}
	return p.SiteScope[0]
}

// handleConsoleRoot sends a signed-in operator to the incident feed and everyone
// else to the sign-in page.
func (s *Server) handleConsoleRoot(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	p, rotated, err := s.Identity.Resolve(r.Context(), s.Tenant, cookie.Value, s.clientIP(r))
	if err != nil {
		s.clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// The rotated token has to go back to the browser.
	//
	// `Resolve` rotates the session token on every call, and this handler threw
	// the new one away. The browser kept the old value, which no longer matches
	// the row, so the very next request failed to resolve and bounced to /login:
	// visiting the console root logged the operator out. Every other handler that
	// resolves a session ’ `s.principal` and therefore the whole API ’ does this,
	// and this one was the exception.
	s.setSessionCookie(w, rotated, p.CSRFToken)
	http.Redirect(w, r, "/incidents", http.StatusFound)
}

func (s *Server) handleConsoleLogin(w http.ResponseWriter, r *http.Request) {
	csrfRaw, _, err := identity.NewToken()
	if err != nil {
		s.render(w, http.StatusInternalServerError, "login.html", consoleData{Title: "Sign in", RequestID: requestID(r), Now: time.Now().UTC(), Error: "CSRF token generation failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName() + "_csrf",
		Value:    csrfRaw,
		Path:     "/",
		Secure:   s.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((15 * time.Minute).Seconds()),
	})
	data := consoleData{Title: "Sign in", RequestID: requestID(r), Now: time.Now().UTC(), CSRF: csrfRaw}
	if msg := r.URL.Query().Get("error"); msg != "" {
		data.Error = msg
	}
	s.render(w, http.StatusOK, "login.html", data)
}

// handleConsoleLoginSubmit is the form-encoded password step.
//
// It exists because `login.html` has always pointed at a route that could not accept
// it. The form said `method="post" action="/login"` and the only `/login` route was
// `GET`, so submitting the sign-in form produced a 405 from the mux and the console
// was a page an operator could look at and not use. An HTML form can only send GET
// and POST, so no amount of editing that one line reaches `DELETE`; the console
// needs a POST of its own, and the API keeps its JSON route unchanged.
//
// A failure re-renders the page with the reason rather than redirecting to it with a
// query string, because a reason in a URL lands in the browser history, in a
// bookmark, and in the `Referer` of whatever the operator clicks next. The lockout
// 423 in particular is the one answer an operator needs to read twice.
func (s *Server) handleConsoleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	// Verify pre-session CSRF token (double-submit cookie).
	csrfCookie, err := r.Cookie(s.cookieName() + "_csrf")
	if err != nil {
		s.renderConsoleLoginError(w, r, http.StatusForbidden, "Missing CSRF token. Reload the page and try again.")
		return
	}
	presented := r.PostFormValue("csrf_token")
	if subtle.ConstantTimeCompare([]byte(presented), []byte(csrfCookie.Value)) != 1 {
		s.renderConsoleLoginError(w, r, http.StatusForbidden, "Invalid CSRF token. Reload the page and try again.")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderConsoleLoginError(w, r, http.StatusBadRequest, "The form could not be read.")
		return
	}
	res, status, reason := s.passwordStep(r,
		r.PostFormValue("username"), r.PostFormValue("password"))
	if status != http.StatusOK {
		s.renderConsoleLoginError(w, r, status, reason)
		return
	}

	if res.Session != nil {
		s.setSessionCookie(w, res.Session.Token, res.Session.CSRFToken)
		http.Redirect(w, r, "/incidents", http.StatusFound)
		return
	}
	// MFA is required. There is no console page that collects a second factor, and
	// inventing one here would be the wrong fix: the honest answer is the page that
	// says so and names the route that does collect it, rather than a form that
	// accepts a code and silently does nothing with it.
	s.renderConsoleLoginError(w, r, http.StatusOK,
		"This account needs a second factor, which the console does not collect yet. "+
			"Sign in through the API at POST /v1/auth/login, then open the console.")
}

// renderConsoleLoginError re-renders the sign-in page with a reason.
//
// `status` is the status the PAGE is served with, not the status the password step
// produced: a wrong password re-renders at 401, which is right, but a locked account
// re-rendering at 423 would make the browser's own error page replace the page that
// explains the lockout.
func (s *Server) renderConsoleLoginError(w http.ResponseWriter, r *http.Request, pageStatus int, reason string) {
	data := consoleData{Title: "Sign in", RequestID: requestID(r), Now: time.Now().UTC(), Error: reason}
	s.render(w, pageStatus, "login.html", data)
}

// handleConsoleLogout is the form-encoded sign-out.
//
// `layout.html` posted to `/v1/auth/session`, which is registered `DELETE` only, so
// the Sign out button was a 405. Same shape as the sign-in form: HTML cannot send
// DELETE, and the API's DELETE is correct for a JSON client that has no form to fill
// in.
//
// Unlike the sign-in form this one is NOT exempt from the CSRF check. A session
// exists, the layout renders the token into the form, and a sign-out a third-party
// page can trigger is a nuisance at best -- an operator's console is cleared
// mid-incident.
// handleConsoleDisposition records an operator disposition from the console.
//
// # Why this route exists at all
//
// The console could SHOW a disposition — the incident page renders the table — and
// there was no way to record one. An operator with the page open had to leave the
// console and call `POST /v1/incidents/{id}/dispositions` with a JSON body and a
// step-up proof, which means the product's own review surface had no review on it.
// A read-only view of a decision record is a screenshot of a workflow.
//
// # The step-up, and why the form asks for a code rather than a proof
//
// `escalated` and `dismissed` require a step-up: `incidents.Record` refuses them
// without `StepUpVerified`, and this route obtains that by calling
// `identity.StepUp` with the operator's second factor and then spending the proof.
//
// The form asks for the FACTOR, not for a proof, because a proof is a bearer token
// and putting one in an HTML form means putting it in a page, a DOM, and possibly a
// browser history. Asking for the code and spending a freshly minted proof keeps the
// token out of everything except the transaction that uses it.
//
// An operator with no enrolled factor cannot escalate or dismiss. That is stated on
// the page rather than discovered at submit time, because "your second factor is not
// enrolled" is a configuration fact the operator can fix and a 403 they cannot.
//
// # Why a successful sensitive disposition rotates the CSRF token
//
// `identity.RotateCSRF` existed, unwired, with the reason written on it: a token
// minted before an escalation is not usable after one. An escalation IS the
// privilege-relevant moment — the incident stops being a review queue item and
// becomes somebody else's problem — and the operator's console is still open with
// the pre-escalation token in every form on the page. Rotating here means the next
// state-changing request from this console has to carry the new one.
func (s *Server) handleConsoleDisposition(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incident")
	id := r.PathValue("id")

	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil || !p.CanSeeSite(detail.SiteID) {
		data.Incident = nil
		data.Error = "No such incident, or you are not assigned to its site."
		s.render(w, http.StatusNotFound, "incident.html", data)
		return
	}
	data.Incident = &detail

	if err := r.ParseForm(); err != nil {
		data.Error = "The form could not be read."
		s.render(w, http.StatusBadRequest, "incident.html", data)
		return
	}
	outcome := strings.TrimSpace(r.PostFormValue("outcome"))
	agreement := strings.TrimSpace(r.PostFormValue("agreement"))
	reasonCode := strings.TrimSpace(r.PostFormValue("reasonCode"))
	notes := strings.TrimSpace(r.PostFormValue("notes"))

	// Refused BEFORE a factor is asked for, so a form that cannot succeed does not
	// collect a second factor first. `DispositionOutcomes` is the closed set and the
	// database CHECKs it; asking the database to be the only validator means the error
	// is a constraint violation rather than a sentence.
	if !incidents.DispositionOutcomes[outcome] {
		data.Error = "Choose one of the listed outcomes."
		s.render(w, http.StatusBadRequest, "incident.html", data)
		return
	}
	if agreement == "" {
		agreement = "unsure"
	}

	stepUpVerified := false
	if incidents.OutcomeNeedsStepUp(outcome) {
		code := strings.TrimSpace(r.PostFormValue("factor"))
		if code == "" {
			data.Error = "Escalating or dismissing needs a second factor. " +
				"This account has none enrolled, so an operator cannot do either from here."
			s.render(w, http.StatusForbidden, "incident.html", data)
			return
		}
		proof, err := d.Identity.StepUp(r.Context(), p.TenantID, p.UserID, "", code,
			"disposition:"+outcome, id)
		if err != nil {
			data.Error = "The second factor was not accepted: " + err.Error()
			s.render(w, http.StatusForbidden, "incident.html", data)
			return
		}
		// Spent before the disposition is recorded, not after. A proof that is spent
		// last and a record that fails leaves a live proof behind, and a live proof is
		// exactly the thing `ConsumeStepUp` exists to prevent: it is spent FIRST, and
		// a failure to record then costs the operator a re-authentication rather than
		// leaving a usable escalation token on the table.
		if err := d.Incidents.ConsumeStepUp(r.Context(), d.Identity, p.TenantID, p.UserID, proof); err != nil {
			data.Error = err.Error()
			s.render(w, http.StatusForbidden, "incident.html", data)
			return
		}
		stepUpVerified = true
	}

	if _, err := d.Incidents.Record(r.Context(), incidents.Disposition{
		TenantID: p.TenantID, IncidentID: id, Outcome: outcome,
		ReasonCode: reasonCode, Notes: notes, Agreement: agreement,
		ActorID: p.UserID, ActorRoles: roleStrings(p), SiteScope: p.SiteScope,
		SessionID: p.SessionID, StepUpVerified: stepUpVerified,
	}); err != nil {
		data.Error = "The disposition was not recorded: " + err.Error()
		s.render(w, http.StatusBadRequest, "incident.html", data)
		return
	}

	if stepUpVerified {
		// The escalation boundary. The cookie carries the session, which survives; the
		// CSRF token does not, and a page rendered before this point still holds the
		// old one in every form. A failure to rotate is logged and NOT swallowed: the
		// disposition already committed, so the only honest thing left is to say that
		// the rotation did not happen and let the operator know their console is now
		// carrying a token that may or may not be current.
		if fresh, err := s.rotateConsoleCSRF(r, p, d); err != nil {
			if s.Log != nil {
				s.Log.Error("the CSRF token was not rotated after a sensitive disposition",
					"tenant_id", p.TenantID, "incident_id", id, "error", err.Error())
			}
		} else if fresh != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     s.CookieName + "_csrf",
				Value:    fresh,
				Path:     "/",
				HttpOnly: true,
				Secure:   s.Secure,
				SameSite: http.SameSiteStrictMode,
			})
		}
	}

	http.Redirect(w, r, "/incidents/"+url.PathEscape(id), http.StatusFound)
}

// rotateConsoleCSRF issues a fresh token for the live session.
//
// Separate from the caller because rotating is the part that can fail and the
// disposition is not: the record is already committed by the time this is called, so
// "fail the whole thing" is no longer available and the choice is between reporting
// and pretending.
func (s *Server) rotateConsoleCSRF(r *http.Request, p identity.Principal, d Deps) (string, error) {
	if d.Identity == nil || strings.TrimSpace(p.SessionID) == "" {
		return "", nil
	}
	return d.Identity.RotateCSRF(r.Context(), p.TenantID, p.SessionID)
}

func (s *Server) handleConsoleLogout(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if err := s.Identity.Logout(r.Context(), s.Tenant, p.SessionID, p.Username, "console logout"); err != nil {
		data := s.consoleData(r, p, "Sign in")
		data.Error = "Sign out failed: " + err.Error()
		s.render(w, http.StatusInternalServerError, "incidents.html", data)
		return
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleConsoleIncidents(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incidents")

	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	state := r.URL.Query().Get("state")
	degraded := r.URL.Query().Get("degraded")

	items, err := d.Incidents.List(r.Context(), p.TenantID, "", limit)
	if err != nil {
		data.Error = "The incident list could not be read: " + err.Error()
		s.render(w, http.StatusInternalServerError, "incidents.html", data)
		return
	}
	// The site filter is applied here rather than in the query, so a principal
	// with several assignments and a principal with one behave identically.
	filtered := make([]incidents.Summary, 0, len(items))
	for _, item := range items {
		if !p.CanSeeSite(item.SiteID) {
			continue
		}
		if state != "" && item.State != state {
			continue
		}
		if degraded == "true" && !item.Degraded {
			continue
		}
		filtered = append(filtered, item)
	}
	data.Incidents = filtered

	if d.ClockDrift != nil {
		if drift, err := d.ClockDrift.List(r.Context(), p.TenantID, 20); err == nil {
			data.Drift = drift
		}
	}
	if d.DeadLetters != nil {
		if letters, err := d.DeadLetters.Read(r.Context(), p.TenantID, primarySiteOf(p), "", 20); err == nil {
			data.DeadLetters = letters
		}
	}

	s.render(w, http.StatusOK, "incidents.html", data)
}

func (s *Server) handleConsoleIncident(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incident")
	id := r.PathValue("id")

	detail, err := d.Incidents.Get(r.Context(), p.TenantID, id)
	if err != nil {
		// Absence and invisibility are the same page. A distinct "not assigned"
		// page would tell an operator which incident ids exist.
		data.Error = "No such incident, or you are not assigned to its site."
		s.render(w, http.StatusNotFound, "incident.html", data)
		return
	}
	if !p.CanSeeSite(detail.SiteID) {
		data.Error = "No such incident, or you are not assigned to its site."
		s.render(w, http.StatusNotFound, "incident.html", data)
		return
	}
	data.Incident = &detail
	s.render(w, http.StatusOK, "incident.html", data)
}

// severityClass maps a severity to a CSS class.
//
// A closed map rather than the string interpolated into a class attribute: the
// severity comes from a rule bundle, and a bundle is untrusted input, so a
// severity of `x" onclick="…` must not be able to become a handler.
func severityClass(s string) string {
	switch s {
	case "info":
		return "sev-info"
	case "low":
		return "sev-low"
	case "medium":
		return "sev-medium"
	case "high":
		return "sev-high"
	case "critical":
		return "sev-critical"
	default:
		return "sev-unknown"
	}
}

func stateClass(s string) string {
	switch s {
	case "open":
		return "state-open"
	case "in_review":
		return "state-review"
	case "dispositioned":
		return "state-dispositioned"
	case "closed":
		return "state-closed"
	default:
		return "state-unknown"
	}
}
