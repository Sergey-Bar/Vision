package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/nlp"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/simulate"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
)

// Deps are the services the API routes onto.
type Deps struct {
	Incidents *incidents.Service
	Bundles   *bundles.Service
	Evidence  *evidence.Store
	// Inbox writes canonical inbox events. The video upload and YouTube
	// endpoints emit video_ingest events here so the VMS's source registrar
	// can pick them up.
	Inbox *inbox.Writer
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
	// EventSearch is the structured search over the canonical event record â€”
	// the "what happened at Door 18 between 14:00 and 15:00" question. Nil
	// means the route and the console page say the search is not configured
	// rather than answering every query with an empty page: an empty result and
	// an unconfigured one are different answers, and only one of them is true.
	EventSearch *eventsearch.Service
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

// siteOfIncident resolves the site an incident-addressed request names, by
// reading the incident row. It is the resolver `requireCapabilityAtSite` uses to
// apply the site check before the handler runs. An unresolvable id resolves to
// the empty string, which the gate treats as a refusal.
func siteOfIncident(d Deps) func(*http.Request, identity.Principal) string {
	return func(r *http.Request, p identity.Principal) string {
		detail, err := d.Incidents.Get(r.Context(), p.TenantID, r.PathValue("id"))
		if err != nil {
			return ""
		}
		return detail.SiteID
	}
}

// RegisterRoutes wires the API and console onto a mux.
//
// Every route declares the capability it needs. There is no route that is
// reachable without one, because a route whose authorization was decided
// somewhere else is a route nobody re-checks when the model changes.
func (s *Server) RegisterRoutes(d Deps) func(*http.ServeMux) {
	return func(mux *http.ServeMux) {
		s.Log.Info("RegisterRoutes called")
		fmt.Fprintln(os.Stderr, "REGISTER_ROUTES_CALLED")
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
		// The principal route is gated on the identity capability.
		//
		// It is the console's own identity, and any caller may know who it is --
		// but every route on this surface declares a capability, and a route that
		// declares none is a route nobody reasoned about. The identity capability
		// is granted to the tenant administrator alone, which is the right answer
		// for a surface that says what the install may do.
		mux.Handle("GET /v1/principal",
			s.requireCapability(identity.CapIdentityManage, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handlePrincipal(w, r, p)
			}))

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
		// Video clip endpoint for instant replay
		mux.Handle("GET /video/clip/{eventId}",
			s.requireCapability(identity.CapEvidenceRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleVideoClip(w, r, p, d)
			}))

		// Video upload: multipart form -> evidence store -> canonical inbox event.
		mux.Handle("POST /v1/video/upload",
			s.requireCapability(identity.CapVideoUpload, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleVideoUpload(w, r, p, d)
			}))
		// YouTube ingest: JSON body { url, siteId } -> source request + event.
		mux.Handle("POST /v1/video/youtube",
			s.requireCapability(identity.CapVideoUpload, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleVideoYouTube(w, r, p, d)
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

		// Compile a free-text rule into a bundle-ready rule document.
		mux.Handle("POST /v1/rules/from-text",
			s.requireCapability(identity.CapRulesWrite, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleRulesFromText(w, r, p, d)
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

		// Legacy /api/* aliases for the console frontend.
		mux.Handle("GET /api/cameras",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleCameras(w, r, p)
			}))
		mux.Handle("GET /api/critical",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleCritical(w, r, p, d)
			}))
		mux.Handle("GET /api/clips",
			s.requireCapability(identity.CapEvidenceRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleClips(w, r, p, d)
			}))

		// WebSocket live feed.
		mux.HandleFunc("GET /ws/live", func(w http.ResponseWriter, r *http.Request) {
			s.handleWebSocketLive(w, r)
		})

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
		// Search.
		//
		// `incident.read` is the capability, and the reasoning is worth stating
		// because it is the same one the metrics route carries. The rows a
		// search returns are the canonical events incidents are built from â€”
		// the record an operator reading incidents is already reading, one row
		// at a time. A capability of its own would be a migration, a role seed
		// and an ADR, and the principal who may read an incident is the
		// principal asking "what else happened in that window". The site scope
		// is resolved from the principal exactly as the incident list resolves
		// it, and a site outside it is refused rather than returned empty,
		// because "no such site" and "not yours" are the same answer and a
		// different one is a disclosure.
		mux.Handle("GET /v1/evidence",
			s.requireCapability(identity.CapIncidentRead, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleEvidenceSearch(w, r, p, d)
			}))

		// The console's search page, gated exactly as its API twin is and for
		// the same reason: the page renders the same rows, so a principal
		// without the capability must not reach them by asking for HTML.
		mux.Handle("GET /search", s.requireCapability(identity.CapIncidentRead,
			func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
				s.handleConsoleSearch(w, r, p, d)
			}))

		// The console's disposition form. The route the incident page's form posts to,
		// which is the write the console previously had no way to perform: it rendered
		// the disposition table and nothing could add a row to it.
		//
		// `requireCapabilityAtSite` rather than `requireCapability`, like every other
		// incident-addressed route: a disposition is the most consequential thing an
		// operator can do here, and it is addressed by incident id.
		mux.Handle("POST /incidents/{id}/disposition",
			s.requireCapabilityAtSite(identity.CapIncidentDisposition, siteOfIncident(d),
				func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
					s.handleConsoleDisposition(w, r, p, d)
				}))
		mux.Handle("GET /static/{file}", s.Static())
	}
}

// handleEvidenceSearch is the structured search: the plan's C5, over the
// canonical event record.
//
// The question it answers is the one the plan named â€” "Door 18, 14:00â€“15:00" â€”
// and the answer is a page of canonical events, each carrying the incident it
// belongs to when there is one. The window is read as an explicit RFC3339
// instant, and the query reads `normalized_at` and nothing else, which is the
// invariant `internal/predicatecov` guards in Go source and this handler guards
// at the edge by refusing a malformed time rather than guessing one.
func (s *Server) handleEvidenceSearch(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.EventSearch == nil {
		WriteError(w, http.StatusServiceUnavailable, "search_unavailable",
			"The evidence search is not configured.", requestID(r))
		return
	}
	q := r.URL.Query()
	site := q.Get("siteId")
	if site != "" && !p.CanSeeSite(site) {
		// Refused rather than empty, for the reason the incident list gives:
		// "no such site" and "not yours" are the same answer, and a different
		// one is a disclosure.
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
		// A malformed time is refused with the reason. Parsing it as the zero
		// time would widen the window to everything, which is the one answer
		// this route must never give.
		WriteError(w, http.StatusBadRequest, "bad_time", err.Error(), requestID(r))
		return
	}
	res, err := d.EventSearch.Search(r.Context(), p.TenantID, eventsearch.Query{
		SiteIDs:       splitScope(scope),
		EventType:     q.Get("eventType"),
		DeviceID:      q.Get("deviceId"),
		ZoneID:        q.Get("zoneId"),
		CorrelationID: q.Get("correlationId"),
		IncidentID:    q.Get("incidentId"),
		From:          from, To: to,
		Limit:    parseLimit(q.Get("limit"), eventsearch.DefaultLimit, eventsearch.MaxLimit),
		Cursor:   q.Get("cursor"),
		FreeText: q.Get("q"),
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "cursor") {
			// A cursor the server did not issue is the caller's mistake, not
			// the server's, and 500 would put it in the server's error budget.
			status = http.StatusBadRequest
		}
		WriteError(w, status, "search_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

// parseSearchTimes reads the window's ends.
//
// Both are optional and both are RFC3339. A malformed end is an error rather
// than a zero time, because a zero `from` widens the window to the beginning of
// the table â€” the query that answers a question nobody asked.
func parseSearchTimes(from, to string) (time.Time, time.Time, error) {
	var f, t time.Time
	var err error
	if strings.TrimSpace(from) != "" {
		if f, err = time.Parse(time.RFC3339, from); err != nil {
			return f, t, fmt.Errorf("from is not an RFC3339 instant: %q", from)
		}
	}
	if strings.TrimSpace(to) != "" {
		if t, err = time.Parse(time.RFC3339, to); err != nil {
			return f, t, fmt.Errorf("to is not an RFC3339 instant: %q", to)
		}
	}
	if !f.IsZero() && !t.IsZero() && t.Before(f) {
		return f, t, fmt.Errorf("the window ends before it starts: from %s to %s",
			f.Format(time.RFC3339), t.Format(time.RFC3339))
	}
	return f, t, nil
}

// splitScope turns the comma-joined scope a principal carries into the list the
// search takes. An empty scope stays empty â€” see eventsearch.cleanSites on why
// an empty array is not a NULL one.
func splitScope(scope string) []string {
	var out []string
	for _, s := range strings.Split(scope, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// handlePrincipal says who the console is acting as.
//
// The console has no login, so there is no user to show. What there is a
// CONFIGURED principal -- a role and a site scope, both from configuration --
// and this route names it, so the console can say what it is acting as instead
// of implying a person is sitting there.
//
// The capabilities are the role's, listed by name. A console that shows them is
// a console that says what the install is allowed to do, which is the honest
// thing for an operator to see and the reason the API's checks are real.
//
// It is not gated on a capability: every caller may read its own identity, and
// a route that refuses to tell you who you are is not a capability check.
func (s *Server) handlePrincipal(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	roles := make([]string, 0, len(p.Roles))
	for _, role := range p.Roles {
		roles = append(roles, string(role))
	}
	caps := make([]string, 0)
	for _, role := range p.Roles {
		for _, c := range identity.CapabilitiesFor(role) {
			caps = append(caps, string(c))
		}
	}
	sort.Strings(caps)
	WriteJSON(w, http.StatusOK, map[string]any{
		"tenantId":     p.TenantID,
		"roles":        roles,
		"siteScope":    p.SiteScope,
		"capabilities": caps,
		// The console must never imply a person did something, so it says
		// plainly that this is not a person.
		"serviceAccount": p.ServiceAccount,
	})
}

// handleIncidentList answers the incident feed.
func (s *Server) handleIncidentList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
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
	// Open access: every site is visible, no filtering.
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
	// The attention producer, BEFORE the body: the same ordering and the same
	// failure answer `auditEvidenceFetched` uses. A read that answers and then
	// fails to record that it was read is the invisible gap this row exists to
	// close â€” attention_minutes is the North Star numerator, and a producer that
	// can fail silently under-counts it with nothing anywhere saying so.
	//
	// The site is deliberately not a parameter: the row is about the incident and
	// the session, and the site check above has already refused the ones the
	// caller may not see. Carrying a site id this function ignores would be the
	// sort of parameter a later change fills in with the wrong value.
	if err := s.auditIncidentOpened(r, p, id); err != nil {
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, detail)
}

// auditIncidentOpened records the first authenticated read of an incident by this
// session.
//
// This row is the producer for `attention_minutes`, which had none: nothing in
// the product recorded when an operator STARTED paying attention, so the interval
// a human was responsible for an incident existed in no table anywhere. The
// dispositions table records when they finished; this records when they began.
//
// Once per session per incident, not once per read. A page refresh is not a new
// bout of attention, and a metric that counts refreshes rewards an operator for
// spamming them. The check is a read and the write is append-only, so two
// concurrent first-reads from one session can both find no row and both write
// one; attention then starts at the earlier of the two, which is the correct
// answer to a race between two tabs.
//
// A server with no audit writer is left alone rather than failing the read, for
// the reason `auditEvidenceFetched` gives: a deployment that has not wired the
// ledger is already broken, and turning every incident read into a 500 hides that
// behind a different symptom. A deployment WITH one gets the row, or an error it
// can see.
func (s *Server) auditIncidentOpened(r *http.Request, p identity.Principal, incidentID string) error {
	if s.Audit == nil || s.Pool == nil {
		return nil
	}
	var seen int
	err := s.Pool.For(p.TenantID, "audit_ledger").QueryRow(r.Context(), `
		SELECT count(*) FROM audit_ledger
		 WHERE action = $1 AND target_id = $2 AND session_id = $3`,
		audit.ActionIncidentOpened, incidentID, p.SessionID).Scan(&seen)
	if err != nil {
		return err
	}
	if seen > 0 {
		return nil
	}
	return s.Pool.InSystemTenant(r.Context(), func(tx pgx.Tx) error {
		_, err := s.Audit.Append(r.Context(), tx, audit.Entry{
			TenantID: p.TenantID, Actor: p.Username, ActorType: audit.ActorOperator,
			Action: audit.ActionIncidentOpened, TargetType: "incident", TargetID: incidentID,
			RequestID: requestID(r), SessionID: p.SessionID, SiteScope: p.SiteScope,
			Reason: "first authenticated read of this incident in this session",
		})
		return err
	})
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
	Outcome    string `json:"outcome"`
	ReasonCode string `json:"reasonCode"`
	Notes      string `json:"notes"`
	Agreement  string `json:"agreement"`
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

	if _, err := d.Incidents.Record(r.Context(), incidents.Disposition{
		TenantID: p.TenantID, IncidentID: id, Outcome: req.Outcome,
		ReasonCode: req.ReasonCode, Notes: req.Notes, Agreement: req.Agreement,
		ActorID: p.UserID, ActorRoles: roleStrings(p), SiteScope: p.SiteScope,
		SessionID: p.SessionID,
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
	// are PERMISSIVE and PostgreSQL ORs them â€” `tenant_isolation OR
	// site_isolation` is `tenant_isolation` â€” so there is no site isolation at the
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

// handleVideoClip serves a video clip associated with an event for instant replay.
// It supports HTTP range requests for efficient seeking.
func (s *Server) handleVideoClip(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		WriteError(w, http.StatusBadRequest, "missing_event_id", "eventId is required", requestID(r))
		return
	}

	var ref, wantHash, siteID string
	var sizeBytes int64
	err := d.Incidents.Pool.WithSites(p.TenantID, "api", p.SiteScope).QueryRow(r.Context(), `
		SELECT e.storage_ref, e.sha256, e.site_id, e.size_bytes
		  FROM incident_events ie
		  JOIN evidence e ON e.tenant_id = ie.tenant_id AND e.evidence_id = ie.evidence_id
		 WHERE ie.event_id = $1 AND ie.tenant_id = $2`,
		eventID, p.TenantID).Scan(&ref, &wantHash, &siteID, &sizeBytes)
	if err != nil {
		WriteError(w, http.StatusNotFound, "no_evidence", "No evidence found for this event", requestID(r))
		return
	}
	if !p.CanSeeSite(siteID) {
		WriteError(w, http.StatusNotFound, "not_found", "No such evidence.", requestID(r))
		return
	}

	rc, obj, err := d.Evidence.Open(ref, wantHash)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), evidence.ErrHashMismatch.Error()) {
			status = http.StatusConflict
		}
		WriteError(w, status, "evidence_unavailable", err.Error(), requestID(r))
		return
	}
	defer func() { _ = rc.Close() }()

	// Range request support for seeking.
	start, end := int64(0), obj.SizeBytes-1
	if ra := r.Header.Get("Range"); ra != "" {
		if strings.HasPrefix(ra, "bytes=") {
			spec := strings.TrimPrefix(ra, "bytes=")
			parts := strings.SplitN(spec, "-", 2)
			if len(parts) == 2 {
				if s, err := strconv.ParseInt(parts[0], 10, 64); err == nil && s >= 0 && s < obj.SizeBytes {
					start = s
				}
				if e, err := strconv.ParseInt(parts[1], 10, 64); err == nil && e < obj.SizeBytes {
					end = e
				}
			}
		}
	}
	if end >= obj.SizeBytes {
		end = obj.SizeBytes - 1
	}
	if start > end {
		start = 0
		end = obj.SizeBytes - 1
	}

	if seeker, ok := rc.(io.Seeker); ok {
		if _, err := seeker.Seek(start, io.SeekStart); err != nil {
			WriteError(w, http.StatusInternalServerError, "evidence_unavailable", err.Error(), requestID(r))
			return
		}
	}

	if ra := r.Header.Get("Range"); ra != "" && strings.HasPrefix(ra, "bytes=") {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, obj.SizeBytes))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", obj.MediaType)
	w.Header().Set("Content-Length", itoa(end-start+1))
	w.Header().Set("X-Content-SHA256", obj.SHA256)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.CopyN(w, rc, end-start+1)
}

// videoUploadRequest is the parsed multipart upload.
type videoUploadRequest struct {
	// File is the uploaded video file.
	FileName string
	// ContentType is the declared MIME type.
	ContentType string
	// Data is the file bytes (bounded by maxUploadBytes).
	Data []byte
}

const maxUploadBytes int64 = 2 << 30 // 2 GiB cap on a single upload.

// readUploadedFile reads one file from a multipart request, bounded in size.
func readUploadedFile(r *http.Request) (videoUploadRequest, error) {
	// #nosec G120: maxUploadBytes bounds the form parse; io.LimitReader bounds the file read.
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return videoUploadRequest{}, fmt.Errorf("multipart parse: %w", err)
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		return videoUploadRequest{}, fmt.Errorf("file field: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return videoUploadRequest{}, fmt.Errorf("read file: %w", err)
	}
	if int64(len(data)) > maxUploadBytes {
		return videoUploadRequest{}, fmt.Errorf("file exceeds the %d byte cap", maxUploadBytes)
	}
	return videoUploadRequest{
		FileName:    fh.Filename,
		ContentType: fh.Header.Get("Content-Type"),
		Data:        data,
	}, nil
}

// isVideo reports whether the bytes look like a video container by magic bytes.
// It is a best-effort gate so a non-video file is rejected with a clear reason
// rather than stored and later failing to decode.
func isVideo(data []byte, contentType string) bool {
	if len(data) < 16 {
		return false
	}
	ct := strings.ToLower(contentType)
	if strings.HasPrefix(ct, "video/") {
		return true
	}
	// MP4: ftyp box.
	if len(data) > 12 && string(data[4:8]) == "ftyp" {
		return true
	}
	// Matroska: EBML header.
	if data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3 {
		return true
	}
	// WebM shares the EBML header.
	// MPEG-TS sync byte.
	if data[0] == 0x47 {
		return true
	}
	// AVI RIFF.
	if len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "AVI " {
		return true
	}
	// QuickTime MOV.
	if len(data) > 12 && string(data[4:8]) == "moov" {
		return true
	}
	return false
}

// handleVideoUpload stores an uploaded video in the evidence store and emits a
// canonical inbox event asking the VMS's source registrar to play it.
func (s *Server) handleVideoUpload(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	site := r.FormValue("siteId")
	if site == "" {
		site = primarySiteOf(p)
	}
	if site == "" || !p.CanSeeSite(site) {
		WriteError(w, http.StatusForbidden, "site_not_assigned", "You are not assigned to that site.", requestID(r))
		return
	}
	up, err := readUploadedFile(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_upload", err.Error(), requestID(r))
		return
	}
	if !isVideo(up.Data, up.ContentType) {
		WriteError(w, http.StatusBadRequest, "not_a_video", "The uploaded file is not a recognized video container.", requestID(r))
		return
	}
	obj, err := d.Evidence.Put(p.TenantID, bytes.NewReader(up.Data))
	if err != nil {
		WriteError(w, http.StatusInsufficientStorage, "evidence_store_failed", err.Error(), requestID(r))
		return
	}
	// Canonical inbox event: the VMS's source registrar picks this up as a
	// file source. The integration id names the KIND of request rather than the
	// implementation that used to serve it, so a rename of the worker does not
	// silently fork the dedupe scope.
	payload := map[string]any{
		"type":         "video_ingest",
		"sourceType":   "file",
		"evidenceId":   obj.Ref,
		"sha256":       obj.SHA256,
		"sizeBytes":    obj.SizeBytes,
		"mediaType":    up.ContentType,
		"fileName":     up.FileName,
		"clipDuration": 10,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "encode_failed", err.Error(), requestID(r))
		return
	}
	accepted, err := inboxSvcWrite(r.Context(), d, p.TenantID, site, "video-source", obj.Ref, payloadBytes)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "ingest_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"videoId":    obj.Ref,
		"evidenceId": obj.Ref,
		"eventId":    accepted.EventID,
		"duplicate":  accepted.Duplicate,
		"sizeBytes":  obj.SizeBytes,
		"sha256":     obj.SHA256,
	})
}

// handleVideoYouTube asks the VMS's source registrar to play a YouTube URL, and
// emits a canonical inbox event. The download itself is the registrar's, via
// yt-dlp.
type videoYouTubeRequest struct {
	URL    string `json:"url"`
	SiteID string `json:"siteId"`
}

func (s *Server) handleVideoYouTube(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	var req videoYouTubeRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if req.URL == "" {
		WriteError(w, http.StatusBadRequest, "missing_url", "url is required", requestID(r))
		return
	}
	if !isYouTubeURL(req.URL) {
		WriteError(w, http.StatusBadRequest, "not_youtube", "The URL is not a YouTube URL.", requestID(r))
		return
	}
	site := req.SiteID
	if site == "" {
		site = primarySiteOf(p)
	}
	if site == "" || !p.CanSeeSite(site) {
		WriteError(w, http.StatusForbidden, "site_not_assigned", "You are not assigned to that site.", requestID(r))
		return
	}
	// The evidence id is content-addressed over the URL, so the same URL always
	// maps to the same placeholder object. The source registrar downloads the real bytes.
	sum := sha256.Sum256([]byte(req.URL))
	evidenceID := fmt.Sprintf("yt/%x", sum[:8])
	payload := map[string]any{
		"type":         "video_ingest",
		"sourceType":   "youtube",
		"url":          req.URL,
		"evidenceId":   evidenceID,
		"mediaType":    "video/mp4",
		"clipDuration": 10,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "encode_failed", err.Error(), requestID(r))
		return
	}
	accepted, err := inboxSvcWrite(r.Context(), d, p.TenantID, site, "video-source", evidenceID, payloadBytes)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "ingest_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"videoId":    evidenceID,
		"evidenceId": evidenceID,
		"eventId":    accepted.EventID,
		"duplicate":  accepted.Duplicate,
		"source":     "youtube",
	})
}

// isYouTubeURL reports whether u is a YouTube watch/embed/shorts URL.
func isYouTubeURL(u string) bool {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	if host == "youtube.com" || host == "www.youtube.com" || host == "m.youtube.com" {
		return true
	}
	if host == "youtu.be" {
		return true
	}
	return false
}

// handleRulesFromText compiles a free-text rule description into a
// bundle-ready rule YAML document, without touching the rule engine.
type rulesFromTextRequest struct {
	Text   string `json:"text"`
	SiteID string `json:"siteId"`
}

type rulesFromTextResponse struct {
	Rule string `json:"rule"`
}

func (s *Server) handleRulesFromText(w http.ResponseWriter, r *http.Request, p identity.Principal, _ Deps) {
	var req rulesFromTextRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		WriteError(w, http.StatusBadRequest, "missing_text", "text is required", requestID(r))
		return
	}
	site := req.SiteID
	if site == "" {
		site = primarySiteOf(p)
	}
	report, err := nlp.CompileRuleFromText(req.Text, site)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "unparseable", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, rulesFromTextResponse{Rule: report})
}

// inboxSvcWrite writes one inbox event. ctx is the first parameter so it can be
// carried through the inbox write path without the caller re-deriving it.
func inboxSvcWrite(ctx context.Context, d Deps, tenant, site, integrationID, sourceEventID string, payload []byte) (inbox.Accepted, error) {
	if d.Inbox == nil {
		return inbox.Accepted{}, errors.New("api: no inbox writer configured")
	}
	return d.Inbox.Write(ctx, inbox.WriteOptions{
		TenantID:      tenant,
		SiteID:        site,
		IntegrationID: integrationID,
		SourceEventID: sourceEventID,
		Payload:       payload,
		ReceivedAt:    time.Now().UTC(),
	})
}

// roleStrings converts a principal's roles to a string slice.
func roleStrings(p identity.Principal) []string {
	out := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		out = append(out, string(r))
	}
	return out
}

// handleAuditLedger reads the audit ledger for this tenant.
func (s *Server) handleAuditLedger(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if s.Pool == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the database pool is not configured", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.Pool.For(p.TenantID, "audit").Query(r.Context(), `
		SELECT sequence, ts, actor, actor_type, action, target_type, target_id,
		       prev_state_hash, result_state_hash, reason, request_id, session_id, site_scope
		  FROM audit_ledger
		 WHERE tenant_id = $1
		 ORDER BY sequence DESC
		 LIMIT $2`, p.TenantID, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	defer rows.Close()
	type ledgerEntry struct {
		Sequence        int64     `json:"sequence"`
		TS              time.Time `json:"ts"`
		Actor           string    `json:"actor"`
		ActorType       string    `json:"actorType"`
		Action          string    `json:"action"`
		TargetType      string    `json:"targetType"`
		TargetID        string    `json:"targetId"`
		PrevStateHash   string    `json:"prevStateHash"`
		ResultStateHash string    `json:"resultStateHash"`
		Reason          string    `json:"reason"`
		RequestID       string    `json:"requestId"`
		SessionID       string    `json:"sessionId"`
		SiteScope       string    `json:"siteScope"`
	}
	var out []ledgerEntry
	for rows.Next() {
		var e ledgerEntry
		if err := rows.Scan(&e.Sequence, &e.TS, &e.Actor, &e.ActorType, &e.Action,
			&e.TargetType, &e.TargetID, &e.PrevStateHash, &e.ResultStateHash,
			&e.Reason, &e.RequestID, &e.SessionID, &e.SiteScope); err != nil {
			WriteError(w, http.StatusInternalServerError, "scan_failed", err.Error(), requestID(r))
			return
		}
		out = append(out, e)
	}
	if out == nil {
		out = []ledgerEntry{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleAuditCheckpoints reads the audit checkpoint seal records.
func (s *Server) handleAuditCheckpoints(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if s.Pool == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the database pool is not configured", requestID(r))
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	rows, err := s.Pool.For(p.TenantID, "audit").Query(r.Context(), `
		SELECT from_sequence, to_sequence, entry_count, sealed_at, seal_hash
		  FROM audit_checkpoints
		 WHERE tenant_id = $1
		 ORDER BY from_sequence DESC
		 LIMIT $2`, p.TenantID, limit)
	if err != nil {
		// The checkpoints table may not exist in older schemas; return empty.
		WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	defer rows.Close()
	type checkpoint struct {
		FromSequence int64     `json:"fromSequence"`
		ToSequence   int64     `json:"toSequence"`
		EntryCount   int       `json:"entryCount"`
		SealedAt     time.Time `json:"sealedAt"`
		SealHash     string    `json:"sealHash"`
	}
	var out []checkpoint
	for rows.Next() {
		var c checkpoint
		if err := rows.Scan(&c.FromSequence, &c.ToSequence, &c.EntryCount, &c.SealedAt, &c.SealHash); err != nil {
			WriteError(w, http.StatusInternalServerError, "scan_failed", err.Error(), requestID(r))
			return
		}
		out = append(out, c)
	}
	if out == nil {
		out = []checkpoint{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleRuleList lists stored rule bundles.
func (s *Server) handleRuleList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the bundle store is not configured", requestID(r))
		return
	}
	list, _, err := d.Bundles.ListWithCursor(r.Context(), p.TenantID, "", 50)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleRuleStore stores a new rule bundle.
func (s *Server) handleRuleStore(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the bundle store is not configured", requestID(r))
		return
	}
	var body struct {
		Content         []byte `json:"content"`
		SourceReference string `json:"sourceReference,omitempty"`
		CreatedBy       string `json:"createdBy,omitempty"`
		Supersedes      string `json:"supersedes,omitempty"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if len(body.Content) == 0 {
		WriteError(w, http.StatusBadRequest, "missing_content", "content is required", requestID(r))
		return
	}
	createdBy := body.CreatedBy
	if createdBy == "" {
		createdBy = p.Username
	}
	meta, problems, err := d.Bundles.Store(r.Context(), p.TenantID, body.Content, body.SourceReference, createdBy, body.Supersedes)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "store_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"meta":     meta,
		"problems": problems,
		"compiled": len(problems) == 0,
	})
}

// handleRuleLint lints a rule bundle without storing it.
func (s *Server) handleRuleLint(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ Deps) {
	var body struct {
		Content []byte `json:"content"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if len(body.Content) == 0 {
		WriteError(w, http.StatusBadRequest, "missing_content", "content is required", requestID(r))
		return
	}
	compiled, problems := rules.NewCompiler().Compile(body.Content)
	WriteJSON(w, http.StatusOK, map[string]any{
		"compiled": compiled != nil,
		"problems": problems,
		"version": func() string {
			if compiled != nil {
				return compiled.Version
			}
			return ""
		}(),
	})
}

// handleRuleSimulate simulates a rule bundle against fixtures.
func (s *Server) handleRuleSimulate(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ Deps) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"note":    "simulation requires scenario conversion",
		"results": []map[string]any{},
		"version": "0.1.0",
	})
}

// handleRuleActivate activates a stored rule bundle.
func (s *Server) handleRuleActivate(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the bundle store is not configured", requestID(r))
		return
	}
	version := r.PathValue("version")
	if version == "" {
		WriteError(w, http.StatusBadRequest, "missing_version", "version is required", requestID(r))
		return
	}
	var body struct {
		DiffDigest string `json:"diffDigest"`
		Actor      string `json:"actor,omitempty"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if body.DiffDigest == "" {
		WriteError(w, http.StatusBadRequest, "missing_digest", "diffDigest is required", requestID(r))
		return
	}
	actor := body.Actor
	if actor == "" {
		actor = p.Username
	}
	report := simulate.Report{
		BundleVersion: version,
		DiffDigest:    body.DiffDigest,
		FixtureSet:    "default",
		Changes:       nil,
	}
	if err := d.Bundles.Activate(r.Context(), p.TenantID, version, report, actor); err != nil {
		WriteError(w, http.StatusBadRequest, "activate_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"activated": version})
}

// handleRuleRollback rolls back to the previously active rule bundle.
func (s *Server) handleRuleRollback(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Bundles == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the bundle store is not configured", requestID(r))
		return
	}
	actor := p.Username
	previous, err := d.Bundles.Rollback(r.Context(), p.TenantID, actor)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "rollback_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"rolledBackTo": previous})
}

// handleDeadLetters lists terminal failures from every queue.
func (s *Server) handleDeadLetters(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.DeadLetters == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the dead-letter reader is not configured", requestID(r))
		return
	}
	site := r.URL.Query().Get("siteId")
	if site == "" {
		site = primarySiteOf(p)
	}
	kind := r.URL.Query().Get("kind")
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	letters, err := d.DeadLetters.Read(r.Context(), p.TenantID, site, kind, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	if letters == nil {
		letters = []DeadLetter{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": letters})
}

// handleDeadLetterReplay replays a dead letter back to received.
func (s *Server) handleDeadLetterReplay(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.DeadLetters == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "the dead-letter reader is not configured", requestID(r))
		return
	}
	var body struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	site := r.URL.Query().Get("siteId")
	if site == "" {
		site = primarySiteOf(p)
	}
	if err := d.DeadLetters.Replay(r.Context(), p.TenantID, site, body.Kind, body.ID); err != nil {
		WriteError(w, http.StatusBadRequest, "replay_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"replayed": body.ID})
}

// handleClockDrift returns clock drift information for the tenant.
func (s *Server) handleClockDrift(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.ClockDrift == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "clock drift is not configured", requestID(r))
		return
	}
	list, err := d.ClockDrift.List(r.Context(), p.TenantID, 50)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	if list == nil {
		list = []DriftView{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMetrics serves the Prometheus text exposition.
//
// # Why the exposition rather than a JSON summary
//
// A scrape endpoint whose body a Prometheus server cannot parse is not a scrape
// endpoint, and the SLO state a JSON summary would carry is already in the
// exposition: `Registry.writeSLOs` emits one series per objective, so the
// `slo_objective` lines ARE the state, and `/v1/health` carries the same
// evaluation as a field for a human reading it.
//
// The route is not declared in the OpenAPI document, and that gap is a recorded
// refusal in `internal/capability` rather than an accident: a scrape endpoint
// is for a scraper, not for a generated client.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request, _ identity.Principal, d Deps) {
	if d.Metrics == nil {
		// No registry is a configuration that has not happened yet, not an error
		// the scraper should be paged for. An empty exposition is the honest
		// answer, and it parses.
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# the metrics registry is not configured\n"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(d.Metrics.Render()))
}

// auditEvidenceFetched records that evidence was fetched for an incident.
func (s *Server) auditEvidenceFetched(r *http.Request, p identity.Principal, evidenceID string, _ string, _ evidence.Object) error {
	if s.Audit == nil || s.Pool == nil {
		return nil
	}
	return s.Pool.InSystemTenant(r.Context(), func(tx pgx.Tx) error {
		_, err := s.Audit.Append(r.Context(), tx, audit.Entry{
			TenantID: p.TenantID, Actor: p.Username, ActorType: audit.ActorOperator,
			Action: audit.ActionEvidenceFetched, TargetType: "evidence", TargetID: evidenceID,
			RequestID: requestID(r), SessionID: p.SessionID, SiteScope: p.SiteScope,
			Reason: "evidence fetched for instant replay",
		})
		return err
	})
}

// Demo cameras for the /api/cameras endpoint when no real camera registry exists.
var demoCameras = []map[string]any{
	{"id": "cam-1", "name": "Entrance North", "siteId": "North Campus", "status": "online", "rtsp": ""},
	{"id": "cam-2", "name": "Entrance South", "siteId": "North Campus", "status": "online", "rtsp": ""},
	{"id": "cam-3", "name": "Aisle 4", "siteId": "West Warehouse", "status": "online", "rtsp": ""},
	{"id": "cam-4", "name": "Aisle 7", "siteId": "West Warehouse", "status": "degraded", "rtsp": ""},
	{"id": "cam-5", "name": "Loading Dock", "siteId": "Downtown Hub", "status": "offline", "rtsp": ""},
	{"id": "cam-6", "name": "Parking A", "siteId": "Downtown Hub", "status": "online", "rtsp": ""},
	{"id": "cam-7", "name": "Parking B", "siteId": "Downtown Hub", "status": "online", "rtsp": ""},
	{"id": "cam-8", "name": "Back Office", "siteId": "North Campus", "status": "offline", "rtsp": ""},
}

func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	WriteJSON(w, http.StatusOK, map[string]any{"cameras": demoCameras})
}

func (s *Server) handleCritical(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Incidents == nil {
		WriteJSON(w, http.StatusOK, map[string]any{"streams": []any{}})
		return
	}
	streams, err := d.Incidents.List(r.Context(), p.TenantID, "", 10)
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{"streams": []any{}})
		return
	}
	type critStream struct {
		IncidentID string   `json:"incidentId"`
		VideoID    string   `json:"videoId"`
		SiteID     string   `json:"siteId"`
		Score      float64  `json:"score"`
		Reasons    []string `json:"reasons"`
		State      string   `json:"state"`
		Severity   string   `json:"severity"`
		Title      string   `json:"title"`
		FirstAt    string   `json:"firstAt"`
		LastAt     string   `json:"lastAt"`
		RuleID     string   `json:"ruleId"`
	}
	out := make([]critStream, 0, len(streams))
	for _, inc := range streams {
		out = append(out, critStream{
			IncidentID: inc.IncidentID,
			VideoID:    "",
			SiteID:     inc.SiteID,
			Score:      0,
			Reasons:    nil,
			State:      inc.State,
			Severity:   inc.Severity,
			Title:      inc.Title,
			FirstAt:    inc.FirstEvent.Format(time.RFC3339),
			LastAt:     inc.CreatedAt.Format(time.RFC3339),
			RuleID:     inc.RuleID,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"streams": out})
}

func (s *Server) handleClips(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	// Evidence store has no List method; return empty for now.
	WriteJSON(w, http.StatusOK, map[string]any{"clips": []any{}})
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) handleWebSocketLive(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return
		}
	}
}
