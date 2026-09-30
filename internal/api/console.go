package api

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"time"

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
	SiteID      string
	Error       string
	Message     string
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
	data := consoleData{Title: "Sign in", RequestID: requestID(r), Now: time.Now().UTC()}
	if msg := r.URL.Query().Get("error"); msg != "" {
		data.Error = msg
	}
	s.render(w, http.StatusOK, "login.html", data)
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
