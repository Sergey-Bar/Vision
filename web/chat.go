package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
	"github.com/Sergey-Bar/vision/internal/investigate"
)

// registerSearchRoutes mounts the free-text search surface.
//
// /api/search/free POST { q, siteId?, from?, to?, limit? }
//
//	ranks canonical events by meaning (pgvector cosine similarity) and falls
//	back to keyword matching over the stored document text when no embedding
//	model or pgvector is available. Every hit resolves through canonical_events
//	and carries its incident id when there is one.
func registerSearchRoutes(_ string, pool *dbx.Pool, s scope) {
	http.HandleFunc("/api/search/free", handleSearchFree(pool, s))
}

// handleSearchFree is the /api/search/free handler, extracted so tests can
// call it directly without registering on the default mux.
func handleSearchFree(pool *dbx.Pool, s scope) http.HandlerFunc {
	svc := &eventsearch.Service{Pool: pool}
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in /api/search/free: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Q      string `json:"q"`
			From   string `json:"from,omitempty"`
			To     string `json:"to,omitempty"`
			Limit  int    `json:"limit,omitempty"`
			SiteID string `json:"siteId,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The scope, last. See the chat handler for why.
		tenant, sites, err := scopeFor(s, pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		q := eventsearch.Query{
			SiteIDs:  sites,
			FreeText: strings.TrimSpace(req.Q),
			Limit:    req.Limit,
		}
		if req.SiteID != "" {
			q.SiteIDs = []string{req.SiteID}
		}
		if req.From != "" {
			if t, err := time.Parse(time.RFC3339, req.From); err == nil {
				q.From = t.UTC()
			}
		}
		if req.To != "" {
			if t, err := time.Parse(time.RFC3339, req.To); err == nil {
				q.To = t.UTC()
			}
		}
		if q.Limit <= 0 || q.Limit > 200 {
			q.Limit = 50
		}
		res, err := svc.Search(context.Background(), tenant, q)
		if err != nil {
			http.Error(w, fmt.Sprintf("search: %v", err), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":      req.Q,
			"hits":       res.Hits,
			"nextCursor": res.NextCursor,
		})
	}
}

// registerChatRoutes mounts the grounded chat surface.
//
// /api/chat POST { question, videoId? }
//
//	routes the question through investigate.Service.Run so every citation
//	resolves to a stored incident or evidence row, and answers "unavailable"
//	when nothing supports an answer. The narrator is optional and never adds
//	hits.
func registerChatRoutes(_ string, pool *dbx.Pool, s scope) {
	http.HandleFunc("/api/chat", handleChat(pool, s))
}

// handleChat is the /api/chat handler, extracted so tests can call it directly.
func handleChat(pool *dbx.Pool, s scope) http.HandlerFunc {
	searchSvc := &eventsearch.Service{Pool: pool}
	investSvc := &investigate.Service{
		Search:   searchSvc,
		Evidence: evidenceResolver{pool: pool},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in /api/chat: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Question string `json:"question"`
			VideoID  string `json:"videoId,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		question := strings.TrimSpace(req.Question)
		if question == "" {
			http.Error(w, "question required", http.StatusBadRequest)
			return
		}
		// The scope, last. A malformed request is a client error whatever
		// the server is configured for, and a server that is not configured
		// cannot make a malformed request valid.
		tenant, sites, err := scopeFor(s, pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		t := investigate.Template{
			TemplateID:    "chat",
			IncidentClass: "chat",
			Version:       "1.0.0",
			Question:      question,
			Steps: []investigate.Step{{
				StepID: "free-text",
				Name:   "Free-text search",
				Reason: "The question names no structured bound, so it searches the record by meaning and by keyword.",
				Query: eventsearch.Query{
					SiteIDs:  sites,
					FreeText: question,
					Limit:    20,
				},
				MaxHits: 20,
			}},
		}
		run, err := investSvc.Run(context.Background(), tenant, t, nil)
		if err != nil {
			http.Error(w, fmt.Sprintf("chat: %v", err), http.StatusBadGateway)
			return
		}
		answer, citations := chatAnswer(run)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"question":   question,
			"answer":     answer,
			"state":      stateOf(run),
			"citations":  citations,
			"videoId":    req.VideoID,
			"narrated":   run.Narrated,
			"unresolved": run.Unresolved,
		})
	}
}

// registerClipsAndCriticalRoutes mounts the clip catalog and critical-stream
// surfaces that live.js polls.
//
// /api/clips GET ?videoId=   stored alert clips, newest first
// /api/critical GET ?limit=   highest-severity open streams
func registerClipsAndCriticalRoutes(_ string, pool *dbx.Pool, s scope) {
	http.HandleFunc("/api/clips", handleClips(pool, s))
	http.HandleFunc("/api/critical", handleCritical(pool, s))
}

// registerCameraRoutes mounts the live camera inventory.
func registerCameraRoutes(_ string, pool *dbx.Pool, s scope) {
	http.HandleFunc("/api/cameras", handleCameras(pool, s))
}

func handleCameras(pool *dbx.Pool, s scope) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in /api/cameras: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		tenant, sites, err := scopeFor(s, pool)
		if err != nil {
			// The console is not configured, or the database is not there.
			//
			// It used to answer an EMPTY camera list here, so the operator
			// surface would load without an error banner. An empty list reads
			// as "there are no cameras", and the console must not say that
			// when it has not asked: an inventory it did not read is an
			// unknown inventory, and the screen has a state for that.
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		rows, err := pool.WithSites(tenant, "cameras", sites).Query(context.Background(), `
			SELECT camera_id, site_id, display_name,
			       CASE state WHEN 'online' THEN 'online' WHEN 'offline' THEN 'offline' WHEN 'degraded' THEN 'degraded' ELSE 'offline' END,
			       source_camera_id
			  FROM cameras
			 WHERE tenant_id = $1
			   AND ($2::text[] IS NULL OR site_id = ANY($2::text[]))
			 ORDER BY display_name`, tenant, sites)
		if err != nil {
			http.Error(w, fmt.Sprintf("cameras: %v", err), http.StatusBadGateway)
			return
		}
		defer rows.Close()
		type camera struct {
			ID     string `json:"id"`
			SiteID string `json:"siteId"`
			Name   string `json:"name"`
			Status string `json:"status"`
			RTSP   string `json:"rtsp,omitempty"`
		}
		var cameras []camera
		for rows.Next() {
			var c camera
			if err := rows.Scan(&c.ID, &c.SiteID, &c.Name, &c.Status, &c.RTSP); err != nil {
				http.Error(w, fmt.Sprintf("cameras: scan: %v", err), http.StatusBadGateway)
				return
			}
			cameras = append(cameras, c)
		}
		if cameras == nil {
			cameras = []camera{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"cameras": cameras})
	}
}

// handleClips is the /api/clips handler, extracted so tests can call it directly.
func handleClips(pool *dbx.Pool, s scope) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in /api/clips: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		tenant, sites, err := scopeFor(s, pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		videoID := r.URL.Query().Get("videoId")
		rows, err := pool.WithSites(tenant, "clips", sites).Query(context.Background(), `
			SELECT e.evidence_id, e.site_id, e.kind, e.observed_at, e.storage_ref,
			       e.media_type, e.size_bytes, e.sha256, e.incident_id
			  FROM evidence e
			 WHERE e.tenant_id = $1
			   AND ($2::text[] IS NULL OR e.site_id = ANY($2::text[]))
			   AND e.kind = 'clip'
			   AND ($3 = '' OR e.storage_ref ILIKE '%' || $3 || '%')
			 ORDER BY e.observed_at DESC
			 LIMIT 20`, tenant, sites, videoID)
		if err != nil {
			http.Error(w, fmt.Sprintf("clips: %v", err), http.StatusBadGateway)
			return
		}
		defer rows.Close()
		type clip struct {
			EvidenceID string    `json:"evidenceId"`
			SiteID     string    `json:"siteId"`
			Kind       string    `json:"kind"`
			ObservedAt time.Time `json:"observedAt"`
			URI        string    `json:"uri"`
			MediaType  string    `json:"mediaType,omitempty"`
			SizeBytes  int64     `json:"sizeBytes,omitempty"`
			SHA256     string    `json:"sha256,omitempty"`
			IncidentID string    `json:"incidentId,omitempty"`
		}
		var clips []clip
		for rows.Next() {
			var c clip
			var incidentID *string
			if err := rows.Scan(&c.EvidenceID, &c.SiteID, &c.Kind, &c.ObservedAt,
				&c.URI, &c.MediaType, &c.SizeBytes, &c.SHA256, &incidentID); err != nil {
				http.Error(w, fmt.Sprintf("clips: scan: %v", err), http.StatusBadGateway)
				return
			}
			if incidentID != nil {
				c.IncidentID = *incidentID
			}
			clips = append(clips, c)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"clips": clips})
	}
}

// handleCritical is the /api/critical handler, extracted so tests can call it directly.
func handleCritical(pool *dbx.Pool, s scope) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in /api/critical: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		tenant, sites, err := scopeFor(s, pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		limit := 5
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
				limit = n
			}
		}
		rows, err := pool.WithSites(tenant, "critical", sites).Query(context.Background(), `
		SELECT i.incident_id, i.primary_site_id, i.severity, i.title, i.state,
		       i.first_event_at, i.last_event_at, i.rule_id,
		       i.threat_score, i.threat_reason_chain
		  FROM incidents i
		 WHERE i.tenant_id = $1
		   AND ($2::text[] IS NULL OR i.primary_site_id = ANY($2::text[]))
		   AND i.state IN ('open', 'unverified')
		 ORDER BY
		       i.threat_score DESC,
		       CASE i.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3
		                        WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END DESC,
		       i.first_event_at DESC
		 LIMIT $3`, tenant, sites, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("critical: %v", err), http.StatusBadGateway)
			return
		}
		defer rows.Close()
		type stream struct {
			IncidentID string    `json:"incidentId"`
			SiteID     string    `json:"siteId"`
			Score      int       `json:"score"`
			Reasons    []string  `json:"reasons"`
			State      string    `json:"state"`
			Severity   string    `json:"severity"`
			Title      string    `json:"title"`
			FirstAt    time.Time `json:"firstAt"`
			LastAt     time.Time `json:"lastAt"`
			RuleID     string    `json:"ruleId,omitempty"`
		}
		var streams []stream
		for rows.Next() {
			var s stream
			var first, last time.Time
			var score int
			var chain []byte
			if err := rows.Scan(&s.IncidentID, &s.SiteID, &s.Severity, &s.Title,
				&s.State, &first, &last, &s.RuleID, &score, &chain); err != nil {
				http.Error(w, fmt.Sprintf("critical: scan: %v", err), http.StatusBadGateway)
				return
			}
			s.FirstAt = first
			s.LastAt = last
			// The score and the reasons are the PRODUCT's: the threat score the
			// autonomy layer computed and the chain that produced it, both
			// stored on the incident. This handler used to derive a score from
			// the severity and write a reason string made of the severity and
			// the rule id -- a number the console made up, wearing the word
			// "score". A score with no chain behind it is a number nobody can
			// check, and the chain is what makes it checkable.
			s.Score = score
			s.Reasons = chainReasons(chain)
			streams = append(streams, s)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"streams": streams})
	}
}

// chainReasons renders the stored reason chain as the short strings the
// legacy alias carried.
//
// The chain is JSONB on the incident row, written by the autonomy layer when
// the incident was raised. It is rendered as "rule: factor" so the reader sees
// WHICH rule moved the score, which is the whole point of carrying a chain: a
// score without its derivation is a number nobody can act on.
//
// A chain that does not parse is not an error here. The row is still an
// incident with a real score; a missing chain means the reader gets the score
// without its reasons, and the page says so rather than inventing reasons.
func chainReasons(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var steps []struct {
		Key      string `json:"Key"`
		Rule     string `json:"Rule"`
		Factor   int    `json:"Factor"`
		Evidence string `json:"Evidence"`
	}
	if err := json.Unmarshal(raw, &steps); err != nil {
		return nil
	}
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		switch {
		case step.Rule != "" && step.Factor != 0:
			out = append(out, fmt.Sprintf("%s: %d", step.Rule, step.Factor))
		case step.Rule != "":
			out = append(out, step.Rule)
		case step.Key != "":
			out = append(out, step.Key)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scopeFor resolves the tenant and the site scope a request is answered
// under.
//
// The scope is CONFIGURED, not requested: see web/scope.go for why the
// X-Tenant-ID header is not read, and what it was doing instead.
//
// A missing tenant or a missing database is a SERVER that is not configured,
// and it answers 503: the request was fine, and 400 would say it was not.
func scopeFor(s scope, pool *dbx.Pool) (string, []string, error) {
	if pool == nil {
		return "", nil, scopeError("the console server has no database configured: set SP_DATABASE_URL")
	}
	return s.resolve(pool)
}

// chatAnswer turns an investigation run into a grounded answer and citations.
//
// Every citation names an incident or an evidence row; nothing in the answer
// is invented. When the run holds no resolvable hits the answer states the
// question is unsupported by stored evidence.
func chatAnswer(run investigate.Run) (string, []string) {
	if len(run.Hits) == 0 {
		return "Unavailable: no stored incident or evidence supports an answer to that question.", nil
	}
	citations := make([]string, 0, len(run.Hits))
	seen := map[string]bool{}
	for _, h := range run.Hits {
		key := h.IncidentID
		if key == "" {
			key = h.EvidenceID
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		citations = append(citations, key)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The record holds %d stored event(s) for that question, each tied to an incident or evidence row you can open.", len(run.Hits))
	if run.Unresolved > 0 {
		fmt.Fprintf(&b, " %d event(s) matched but resolve to nothing stored and are not cited.", run.Unresolved)
	}
	return b.String(), citations
}

// stateOf reports whether the chat run answered, degraded, or found nothing.
func stateOf(run investigate.Run) string {
	switch {
	case len(run.Hits) == 0:
		return "unavailable"
	case run.Unresolved > 0:
		return "partial"
	default:
		return "answered"
	}
}

// evidenceResolver resolves evidence behind an event through the evidence table.
type evidenceResolver struct {
	pool *dbx.Pool
}

func (e evidenceResolver) EvidenceForEvent(ctx context.Context, tenant, eventID string) (string, error) {
	if e.pool == nil {
		return "", nil
	}
	var evID string
	if err := e.pool.WithSites(tenant, "evidenceResolver", nil).QueryRow(ctx, `
		SELECT DISTINCT ie.incident_id
		  FROM incident_events ie
		 WHERE ie.tenant_id = $1 AND ie.event_id = $2 AND ie.incident_id <> ''
		 LIMIT 1`, tenant, eventID).Scan(&evID); err == nil {
		return evID, nil
	}
	return "", nil
}

// Ensure compile-time interface satisfaction.
var _ investigate.EvidenceResolver = evidenceResolver{}
