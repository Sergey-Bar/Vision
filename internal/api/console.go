package api

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/incidents"
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
// RequestID and Principal are on every page rather than passed per template,
// because a page that forgets the request id is a page whose operator cannot
// join what they are reading to the audit row it produced.
//
// There is no CSRF field, and there is not a token to put in one. The console
// has no session: the operator's identity comes from the boundary in front of
// it, not from a cookie this server issues. A form carrying an empty
// `csrf_token` would be worse than a form carrying none, because a reader
// auditing the markup would conclude the console had a protection it does not
// have.
type consoleData struct {
	Title       string
	Principal   identity.Principal
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
	// Location is the site's own zone, and the times on this surface are
	// rendered in it. Nil means the site has no zone that could be read, and
	// the times are rendered in UTC with the offset saying so.
	Location *time.Location
	// Search is the structured search page: the query the operator typed, the
	// page it answered, and the reason an answer was not available. All three
	// are always present, because a search page that renders a blank result set
	// reads as "nothing happened", and the honest answer when the search is not
	// configured is a sentence that says so.
	Search      []eventsearch.Hit
	SearchQuery eventsearch.Query
	SearchErr   string
	SearchRan   bool
	Error       string
	Message     string
}

// SiteTimezone renders a site's time, never the host's.
//
// Every rendered time carries an explicit offset. A security operator comparing
// an incident timeline with a guard's account of the same minute is doing
// arithmetic, and a timestamp without an offset makes that arithmetic a guess.
//
// The zone is the SITE's, read from the site's own row, and it used not to be
// read at all: every time on this surface was rendered in UTC, which is right
// for a site in UTC and wrong for every other site. The SPA renders the same
// record in the site's zone, so the two halves of the console were showing
// the same incident at two different times.
//
// A site whose zone cannot be loaded is rendered in UTC, and the offset says
// so. A zone that is guessed is a wrong clock, and a wrong clock reads as a
// fact.
func (d consoleData) Time(t time.Time) string {
	if t.IsZero() {
		// An em dash, written as an escape. The literal was once a raw U+2014 in
		// a file that had been through a lossy encoding round-trip, and it reached
		// the console as mojibake. An escape is the same character and cannot be
		// corrupted by whatever touches the file next.
		return "\u2014"
	}
	loc := d.Location
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02 15:04:05Z07:00")
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

func (s *Server) consoleData(r *http.Request, p identity.Principal, title string, d Deps) consoleData {
	return consoleData{
		Title:     title,
		Principal: p,
		RequestID: requestID(r),
		Now:       time.Now().UTC(),
		SiteID:    firstSite(p),
		Location:  s.siteLocation(r, p, d),
	}
}

// siteLocation reads the site's own zone.
//
// It is a separate function because it is the one thing in this file that
// touches the database, and a page that cannot read the site's zone still
// renders: the times are in UTC and the offset says so.
func (s *Server) siteLocation(r *http.Request, p identity.Principal, d Deps) *time.Location {
	if d.SiteTimezone == nil {
		return nil
	}
	site := firstSite(p)
	if site == "" {
		return nil
	}
	zone, err := d.SiteTimezone.Timezone(r.Context(), p.TenantID, site)
	if err != nil || zone == "" {
		return nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("the site timezone could not be loaded", "site", site, "zone", zone, "error", err)
		}
		return nil
	}
	return loc
}

func firstSite(p identity.Principal) string {
	if len(p.SiteScope) == 0 {
		return ""
	}
	return p.SiteScope[0]
}

// handleConsoleRoot sends every request to the incident feed. Login is removed,
// so there is no sign-in page to land on.
func (s *Server) handleConsoleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/incidents", http.StatusFound)
}

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
// # Why a sensitive disposition asks for the factor again
//
// `escalated` and `dismissed` require a step-up, and the step-up is spent on the
// transaction that needs it. The console's own pages carry no token of their
// own, so there is nothing to rotate between one privileged action and the
// next: the second factor is re-presented for the action that requires it.
func (s *Server) handleConsoleDisposition(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incident", d)
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
	// The agreement is REQUIRED.
	//
	// It used to default to "unsure", which is the console inventing an
	// answer about what the operator thought. The JSON route refuses an
	// empty agreement with a 400, and the console refusing it the same way is
	// the point: the two routes are one product and they must not disagree
	// about what a disposition has to say.
	if agreement == "" {
		data.Error = "Say whether you agreed with the decision: the record asks, and a blank is not an answer."
		s.render(w, http.StatusBadRequest, "incident.html", data)
		return
	}

	// The step-up, for the two sensitive outcomes.
	//
	// The factor is the six-digit code, not a proof: the proof is minted and
	// spent inside this one call and never reaches the browser. See
	// internal/api/stepup.go for the interstitial and the wording.
	stepUpVerified := false
	if incidents.OutcomeNeedsStepUp(outcome) {
		factor := strings.TrimSpace(r.PostFormValue("factor"))
		form := StepUpForm{
			IncidentID: id, Outcome: outcome, ReasonCode: reasonCode,
			Notes: notes, Agreement: agreement,
		}
		if factor == "" {
			s.renderStepUpPrompt(w, r, data, form)
			return
		}
		if err := s.stepUpForDisposition(r.Context(), p, d, id, factor); err != nil {
			data.Error = err.Error()
			s.renderStepUpPrompt(w, r, data, form)
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
		// A step-up that is missing is a 403, not a 400.
		//
		// The two sensitive outcomes need a fresh second factor, and an
		// operator who has not enrolled one is being told their account cannot
		// do this -- which is a permission, not a malformed request. The JSON
		// route already says 403 for the same refusal, and a page that answers
		// 400 for a permission and 403 for the same permission on the other
		// route is a page an operator has to learn two things about.
		if strings.Contains(err.Error(), "step-up") {
			s.render(w, http.StatusForbidden, "incident.html", data)
			return
		}
		s.render(w, http.StatusBadRequest, "incident.html", data)
		return
	}

	http.Redirect(w, r, "/incidents/"+url.PathEscape(id), http.StatusFound)
}

// severityClass maps a severity to a CSS class.

func (s *Server) handleConsoleIncidents(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incidents", d)

	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	state := r.URL.Query().Get("state")
	degraded := r.URL.Query().Get("degraded")

	// The principal's scope is decided where the principal is built
	// (`openPrincipal`), from the install's configuration. This handler used to
	// decide it for itself from an environment variable, which is how the
	// JSON API and the HTML console came to disagree about the same site.
	siteID := ""
	if len(p.SiteScope) > 0 {
		siteID = p.SiteScope[0]
	}

	// Incidents list
	items, err := d.Incidents.List(r.Context(), p.TenantID, siteID, limit)
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
		if letters, err := d.DeadLetters.Read(r.Context(), p.TenantID, primarySiteOf(p), "", 1); err == nil {
			data.DeadLetters = letters
		}
	}

	s.render(w, http.StatusOK, "incidents.html", data)
}

// handleConsoleSearch renders the search page.
//
// It only searches when the operator asked something: a governed query (site,
// event type, camera, zone, correlation, incident) or a window. Landing on the
// page with no parameters renders the form and nothing else, because a page
// that answered an empty query on arrival would render "no results" before the
// operator had asked a question — the same class of empty that reads as a fact.
func (s *Server) handleConsoleSearch(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Search", d)
	q := r.URL.Query()
	asked := false
	for _, key := range []string{"siteId", "eventType", "deviceId", "zoneId", "correlationId", "incidentId", "from", "to"} {
		if strings.TrimSpace(q.Get(key)) != "" {
			asked = true
			break
		}
	}
	if !asked {
		s.render(w, http.StatusOK, "search.html", data)
		return
	}
	if d.EventSearch == nil {
		data.SearchErr = "The evidence search is not configured on this deployment."
		s.render(w, http.StatusServiceUnavailable, "search.html", data)
		return
	}
	site := q.Get("siteId")
	if site != "" && !p.CanSeeSite(site) {
		WriteError(w, http.StatusForbidden, "site_not_assigned",
			"You are not assigned to that site.", requestID(r))
		return
	}
	scope := site
	if scope == "" {
		scope = strings.Join(p.SiteScope, ",")
	}
	from, to, err := parseSearchTimes(q.Get("from"), q.Get("to"))
	if err != nil {
		data.SearchErr = err.Error()
		s.render(w, http.StatusBadRequest, "search.html", data)
		return
	}
	query := eventsearch.Query{
		SiteIDs:       splitScope(scope),
		EventType:     q.Get("eventType"),
		DeviceID:      q.Get("deviceId"),
		ZoneID:        q.Get("zoneId"),
		CorrelationID: q.Get("correlationId"),
		IncidentID:    q.Get("incidentId"),
		From:          from, To: to,
		Limit: parseLimit(q.Get("limit"), eventsearch.DefaultLimit, eventsearch.MaxLimit),
	}
	res, err := d.EventSearch.Search(r.Context(), p.TenantID, query)
	if err != nil {
		data.SearchErr = "The search could not be read: " + err.Error()
		s.render(w, http.StatusInternalServerError, "search.html", data)
		return
	}
	data.Search = res.Hits
	data.SearchQuery = query
	data.SearchRan = true
	s.render(w, http.StatusOK, "search.html", data)
}

func (s *Server) handleConsoleIncident(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	data := s.consoleData(r, p, "Incident", d)
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
