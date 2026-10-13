package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/brief"
	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/poc/search"
	"github.com/Sergey-Bar/vision/poc/ws"
)

// Server serves the POC frontend, the JSON API and the analytics WebSocket
// from a single origin so the browser needs no CORS dance.
type Server struct {
	cfg     *Config
	ingest  *StreamIngest
	http    *http.Server
	rootCtx context.Context

	mu     sync.RWMutex
	stream StreamInfo
}

// StreamInfo is the ingested metadata of the stream currently being played.
type StreamInfo struct {
	URL       string `json:"url"`
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Source    string `json:"source"` // "metadata" | "none"
	SetAt     string `json:"set_at"`
}

// NewServer builds the POC server around an existing ingest pipeline.
func NewServer(cfg *Config, ingest *StreamIngest) *Server {
	return &Server{cfg: cfg, ingest: ingest}
}

// Start mounts every route, attaches the WebSocket hub and begins listening.
// It returns an error only if the listener cannot be bound, so a port conflict
// fails the process at startup instead of surfacing as an async log line.
func (s *Server) Start(ctx context.Context) error {
	s.rootCtx = ctx
	staticDir := s.cfg.Web.StaticDir
	if staticDir == "" {
		staticDir = "./poc/static"
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	mux.HandleFunc("/api/healthz", s.handleHealthz)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/zones", s.handleZones)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/stream", s.handleStream)
	// Autonomy layer endpoints
	mux.HandleFunc("/api/site", s.handleSite)
	mux.HandleFunc("/api/incidents/active", s.handleActiveIncidents)
	mux.HandleFunc("/api/incidents/", s.handleIncidentByID)
	mux.HandleFunc("/api/telemetry/live", s.handleTelemetryLive)
	mux.HandleFunc("/api/briefs/latest", s.handleBriefLatest)
	mux.HandleFunc("/api/dutylog", s.handleDutyLog)
	mux.HandleFunc("/api/notes", s.handleNote)
	mux.HandleFunc("/api/site-config", s.handleSiteConfig)
	mux.HandleFunc("/api/investigate", s.handleInvestigate)
	mux.HandleFunc("/api/patrol", s.handlePatrol)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, staticDir+"/index.html")
	})

	// One origin: the hub shares this listener instead of opening :8081.
	s.ingest.wsHub.Attach(ctx, mux)

	port := s.cfg.Web.Port
	if port == 0 {
		port = 8082
	}
	host := s.cfg.Web.Host
	if host == "" {
		host = "127.0.0.1"
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return fmt.Errorf("listen on %s:%d: %w", host, port, err)
	}
	s.http = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("POC server listening on http://%s:%d (UI + /api + /ws)", host, port)
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("POC server error: %v", err)
		}
	}()
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.ingest.statusSnapshot())
}

func (s *Server) handleZones(w http.ResponseWriter, _ *http.Request) {
	zones := s.ingest.spatialEngine.GetZones()
	out := make([]map[string]any, 0, len(zones))
	for _, z := range zones {
		poly := make([]map[string]float64, 0, len(z.Polygon))
		for _, p := range z.Polygon {
			poly = append(poly, map[string]float64{"x": p.X, "y": p.Y})
		}
		out = append(out, map[string]any{
			"id":              z.ID,
			"name":            z.Name,
			"type":            z.Type,
			"polygon":         poly,
			"alert_on_entry":  z.AlertOnEntry,
			"alert_on_dwell":  z.AlertOnDwell,
			"dwell_threshold": z.DwellThreshold,
		})
	}
	writeJSON(w, map[string]any{
		"zones": out,
		"rules": s.cfg.Spatial.Rules,
		"frame": map[string]float64{"width": frameW, "height": frameH},
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Text  string `json:"text"`
		Limit int    `json:"limit"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result := s.ingest.searchIndex.Search(search.Query{Text: req.Text, Limit: req.Limit})
	writeJSON(w, result)
}

// handleStream ingests metadata for the stream the frontend is about to play:
// it normalizes the URL to a video id and fetches YouTube oEmbed title/author
// (best effort — offline degrades to source=none, never to an error).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, s.stream)
		return
	case http.MethodPost:
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	videoID := extractVideoID(req.URL)
	if videoID == "" {
		http.Error(w, "could not extract a YouTube video id from the URL", http.StatusBadRequest)
		return
	}

	info := StreamInfo{
		URL:     req.URL,
		VideoID: videoID,
		Source:  "none",
		SetAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if title, author, thumb, ok := fetchOEmbed(r.Context(), req.URL); ok {
		info.Title = title
		info.Author = author
		info.Thumbnail = thumb
		info.Source = "metadata"
	}

	s.mu.Lock()
	s.stream = info
	s.mu.Unlock()

	// The player is about to show this stream; the pipeline now samples its
	// pixels too. In demo policy this is a no-op — the sim keeps running. In
	// a fleet, the manually loaded stream becomes its own camera: it starts
	// or fails by name, and the other cameras are untouched either way.
	if s.ingest.mode != "demo" {
		name := "Manual stream"
		if info.Title != "" {
			name = info.Title
		}
		if err := s.ingest.startManualCamera(s.rootCtx, req.URL, name); err != nil {
			log.Printf("manual stream %s unavailable: %v", videoID, err)
		}
	}

	// Make the stream's own metadata searchable alongside frame detections.
	s.ingest.searchIndex.Add(search.Entry{
		ID:        "stream_" + videoID,
		Timestamp: time.Now().UTC(),
		Categories: []search.CategoryInfo{{
			Category: "stream",
			Label:    strings.TrimSpace(info.Title + " " + info.Author),
		}},
	})
	s.ingest.wsHub.Broadcast(ws.Message{Type: "stream", Data: info, Timestamp: time.Now().UTC()})
	writeJSON(w, info)
}

// extractVideoID reads the YouTube id out of a URL, a /live/ or /embed/
// path, or a bare id.
//
// The return value is CLEANED before it is returned: every consumer of an id is
// a consumer of request data, and an id carrying a control character is a line
// injection into the operational log at the call site below, a query-injection
// into the oEmbed request, and a mangled search index key. `url.Parse`
// percent-decodes, so `https://host/live/x%0aforged` really does yield an id
// with a newline in it. Allow-listing the id is cheaper than defending every
// consumer against every character, and an id that is not one of these is not
// a YouTube id anyway.
// maxVideoIDLen is the YouTube id's length, and the only length this endpoint
// accepts. The alphabet allow-list alone is not a bound: `url.Parse` accepts an
// arbitrarily long `v` parameter or path segment, and an id of half a million
// characters would be stored, broadcast over the websocket, used as a search
// index key and written into a log line — memory amplification and a log line
// nobody can read, from one unauthenticated POST with a large body.
const maxVideoIDLen = 11

// maxBodyBytes bounds a JSON request body. None of this API's payloads is
// anywhere near a kilobyte, so a body that is has already said something about
// the client, and an unbounded io.ReadAll is the shape a request can use to
// make the service do its work for it.
const maxBodyBytes = 1 << 20 // 1 MiB

func extractVideoID(raw string) string {
	id := youtubeVideoID(raw)
	if id == "" || len(id) > maxVideoIDLen {
		return ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ""
		}
	}
	return id
}

// youtubeVideoID is the extraction itself, before the id is trusted. It is a
// separate function because a test needs to show both halves: what the parse
// finds, and what the caller may keep.
func youtubeVideoID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		q := u.Query()
		if v := q.Get("v"); v != "" {
			return v
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if (p == "live" || p == "embed" || p == "shorts" || p == "v") && i+1 < len(parts) {
				return parts[i+1]
			}
		}
		if strings.Contains(u.Host, "youtu.be") && len(parts) > 0 {
			return parts[0]
		}
	}
	if len(raw) == 11 {
		return raw
	}
	return ""
}

func fetchOEmbed(ctx context.Context, rawURL string) (title, author, thumb string, ok bool) {
	endpoint := "https://www.youtube.com/oembed?format=json&url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", "", false
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", false
	}
	var payload struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "", false
	}
	return payload.Title, payload.AuthorName, payload.ThumbnailURL, true
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func (s *Server) handleSite(w http.ResponseWriter, _ *http.Request) {
	if s.ingest.siteModel == nil {
		http.Error(w, "site model not enabled", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, s.ingest.siteModel)
}

func (s *Server) handleActiveIncidents(w http.ResponseWriter, _ *http.Request) {
	if s.ingest.linker == nil {
		writeJSON(w, []correlation.CorrelatedIncident{})
		return
	}
	// The linker's maps are written under passMu by the autonomy pass, so this
	// read takes it too: an unsynchronized read beside a concurrent write is a
	// fatal "concurrent map read and map write", not a rare race.
	s.ingest.passMu.Lock()
	incidents := s.ingest.linker.AllIncidents()
	s.ingest.passMu.Unlock()
	// Filter active only
	active := make([]*correlation.CorrelatedIncident, 0)
	for _, inc := range incidents {
		if inc.State == "ACTIVE" || inc.State == "VERIFYING" || inc.State == "ESCALATING" {
			active = append(active, inc)
		}
	}
	writeJSON(w, active)
}

func (s *Server) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	if s.ingest.linker == nil {
		http.NotFound(w, r)
		return
	}
	// Extract incident ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/incidents/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	// Handle /proof subpath
	if strings.HasSuffix(path, "/proof") {
		incidentID := strings.TrimSuffix(path, "/proof")
		s.handleIncidentProof(w, incidentID)
		return
	}
	// Handle /override subpath
	if strings.HasSuffix(path, "/override") && r.Method == http.MethodPost {
		incidentID := strings.TrimSuffix(path, "/override")
		s.handleIncidentOverride(w, r, incidentID)
		return
	}
	// Handle GET /api/incidents/:id
	if r.Method == http.MethodGet {
		inc := s.ingest.linker.GetIncident(path)
		if inc == nil {
			http.NotFound(w, r)
			return
		}
		// The answer is assembled only from the stored incident: its stored
		// detections, its stored cameras, its stored reason chain. Nothing
		// here re-runs the model or re-walks the stream, because the claim
		// this endpoint exists to keep is that a decision replays from
		// stored facts — a response that re-inferred anything would be the
		// bug, not the answer.
		out := map[string]any{
			"incident":                   inc,
			"provenance":                 inc.Provenance,
			"reason_chain":               inc.ReasonChain,
			"evidence_refs":              inc.DetectionIDs,
			"replayed_from_stored_facts": true,
			"stored_facts": map[string]any{
				"detection_ids":  inc.DetectionIDs,
				"camera_ids":     inc.CameraIDs,
				"anomalies":      inc.Anomalies,
				"first_seen":     inc.FirstSeen,
				"last_seen":      inc.LastSeen,
				"threat_score":   inc.ThreatScore,
				"action":         inc.Action,
				"autonomy_state": inc.AutonomyState,
				"state":          inc.State,
			},
		}
		if sight, ok := s.ingest.lastSight.Get(inc.ID); ok {
			out["second_sight"] = sight
		}
		// The under-observed anomaly type, named where the operator reads the
		// incident's anomalies: a zone whose every declared camera is
		// degraded is a finding about this site's visibility, not about the
		// intrusion, and it must be possible to tell the two apart.
		out["under_observed_type"] = anomaly.AnomalyUnderObserved
		// The incident's own lines in the duty log: the audit question about
		// one incident answered from the shift's single artifact.
		if lines := s.ingest.incidentDutyLog(inc.ID); len(lines) > 0 {
			out["duty_log"] = lines
		}
		writeJSON(w, out)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleIncidentProof(w http.ResponseWriter, incidentID string) {
	if s.ingest.linker == nil {
		http.NotFound(w, nil)
		return
	}
	inc := s.ingest.linker.GetIncident(incidentID)
	if inc == nil {
		http.NotFound(w, nil)
		return
	}
	// Build proof bundle
	proof := map[string]any{
		"incident":      inc,
		"provenance":    inc.Provenance,
		"reason_chain":  inc.ReasonChain,
		"evidence_refs": inc.DetectionIDs,
	}
	writeJSON(w, proof)
}

func (s *Server) handleIncidentOverride(w http.ResponseWriter, r *http.Request, incidentID string) {
	if s.ingest.linker == nil {
		http.NotFound(w, r)
		return
	}
	inc := s.ingest.linker.GetIncident(incidentID)
	if inc == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Reason) < 10 {
		http.Error(w, "reason must be at least 10 characters", http.StatusBadRequest)
		return
	}
	// Update incident state to overridden
	s.ingest.linker.UpdateIncidentState(incidentID, "OVERRIDDEN")
	s.ingest.linker.UpdateIncidentAction(incidentID, "HOLD", "OVERRIDDEN")
	// Record override in telemetry. An operator taking back an ESCALATION is
	// the pair of facts the false-escalation rate is made of; taking back an
	// OBSERVE is a routine correction and counts only as an override.
	if s.ingest.telemetry != nil {
		if inc.AutonomyState == "ESCALATING" || inc.Action == "ESCALATE" {
			s.ingest.telemetry.RecordFalseEscalation()
		} else {
			s.ingest.telemetry.RecordOverride()
		}
	}
	// The override is a hand-over line before it is broadcast: the reason the
	// operator gave is the artifact, the broadcast is the notification.
	if !s.ingest.logOverride(inc, inc.Action, "HOLD", req.Reason) {
		// The artifact, not the acknowledgement, is the record. The override
		// itself has already taken effect on the incident, so the operator is
		// told that and told why the hand-over cannot show it — a 200 here
		// would be the lost-record-with-acknowledgement failure the duty log
		// exists to prevent.
		http.Error(w, "the override took effect, but the shift's hand-over artifact could not be written", http.StatusServiceUnavailable)
		return
	}
	s.ingest.rebuildBrief()
	// Broadcast override event
	s.ingest.wsHub.Broadcast(ws.Message{
		Type: "override",
		Data: map[string]any{
			"incident_id": incidentID,
			"reason":      req.Reason,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		},
		Timestamp: time.Now().UTC(),
	})
	writeJSON(w, map[string]any{"status": "overridden", "incident_id": incidentID})
}

// handleBriefLatest returns the latest rendered shift brief: the same bytes
// the ws `brief` message carries, for a client that was not connected when
// it was broadcast. It is rendered from the duty log and the fleet, so it
// never disagrees with the artifact.
func (s *Server) handleBriefLatest(w http.ResponseWriter, _ *http.Request) {
	b := s.ingest.latestBrief()
	if b == nil {
		// The brief's own constant, not a second spelling of it: a payload that
		// says v2 while this error says v1 is one endpoint reporting two
		// schemas, and a client cannot tell which one the body is.
		writeJSON(w, map[string]any{
			"schema": brief.Schema,
			"error":  "no brief rendered yet",
			"reason": "the duty log is not open, or no decision has been made this shift",
		})
		return
	}
	// The brief's own wire shape, so the endpoint and the ws message cannot
	// render it differently.
	data, err := b.JSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// handleSiteConfig is the autonomous configurator's input page: the artifact
// generated from this fleet's own inventory, declarations, measurements and
// learned occupancy. It reads nothing and writes nothing; `securityctl
// site-config plan` is what acts on the same inputs.
func (s *Server) handleSiteConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.ingest.siteConfigView(time.Now().UTC()))
}

// handleDutyLog is the hand-over artifact itself: the shift's log path, its
// chain head and verification, its lines, and its terminal rendering.
func (s *Server) handleDutyLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.ingest.dutyLogView())
}

// handleInvestigate answers one question from the facts the pipeline stored.
// It is the investigator role: a reading of stored facts, with the citations
// it used. It never decides anything, and with no GPU bound it returns the
// named degradation rather than an empty answer.
func (s *Server) handleInvestigate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Question string `json:"question"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.ingest.Investigate(r.Context(), req.Question))
}

// handleNote records one operator's patrol note against a camera or an
// incident. It is the generic form the patrol check routes through
// (POST /api/patrol with a route id): both write one duty-log line, and this is
// the shape that takes a free-text subject.
//
// A note is the one place a human writes into the artifact in their own words.
// It is the hand-over's whole point: the record an incoming operator reads is
// not only what the autonomy layer decided, it is what the operator who was
// there knew. The note is rendered by brief as a `brief.Note`, so it lands in
// the summary and not only in the JSONL.
func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Subject string `json:"subject"`
		Note    string `json:"note"`
	}
	if err := decodeBody(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.Note) == "" {
		http.Error(w, "subject and note are both required", http.StatusBadRequest)
		return
	}
	// The note is sanitised inside logNote, at the writer, so one control
	// covers every path into the artifact rather than each caller remembering
	// to.
	if !s.ingest.logNote("operator", req.Subject, req.Note) {
		// The artifact, not the acknowledgement, is the record. A 200 on a
		// line that was never written is the lost-record-with-acknowledgement
		// failure the hand-over exists to prevent, so this says 503 and names
		// what could not be written.
		http.Error(w, "the shifts hand-over artifact could not be written", http.StatusServiceUnavailable)
		return
	}
	s.ingest.rebuildBrief()
	s.ingest.wsHub.Broadcast(ws.Message{
		Type: "patrol",
		Data: map[string]any{
			"kind":    "checked",
			"subject": req.Subject,
			"note":    req.Note,
			"at":      time.Now().UTC().Format(time.RFC3339),
		},
		Timestamp: time.Now().UTC(),
	})
	writeJSON(w, map[string]any{"status": "recorded", "subject": req.Subject})
}

// decodeBody reads a JSON body under the one size bound this API declares.
func decodeBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("no request body")
	}
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes)).Decode(dst)
}

// sanitizeText strips the characters that make a rendered hand-over line lie.
//
// What it protects is the RENDERING, not the chain: `json.Marshal` escapes a
// newline, so a control character in a free-text field cannot break a JSONL
// line or invalidate the hash. What it can do is print as a second,
// well-formed-looking line in `dutylog.Dump` and `brief.Text`, which the hash
// chain does not cover - an operator reading the hand-over sees a note that
// was never written. So the control runs at the writer, in one place, for
// every free-text path into the artifact.
func sanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n', r == '\r', r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// A control character is not a word. Dropped rather than mapped,
			// so the sanitizer is idempotent and adds nothing of its own.
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// handlePatrol serves the route-walk records: GET lists the site's routes
// (with the cameras declared to cover them), POST records that an operator
// checked a camera on a route. The check is a duty-log line, because a patrol
// that leaves no trace is a patrol nobody did.
func (s *Server) handlePatrol(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.ingest.siteModel == nil {
			writeJSON(w, map[string]any{"routes": []string{}, "reason": "site model not enabled"})
			return
		}
		out := make([]map[string]any, 0, len(s.ingest.siteModel.Routes))
		for _, route := range s.ingest.siteModel.Routes {
			out = append(out, map[string]any{
				"id":      route.ID,
				"points":  len(route.Points),
				"cameras": camerasForRoute(s.ingest.siteModel, route.ID),
			})
		}
		writeJSON(w, map[string]any{
			"routes":   out,
			"interval": s.ingest.patrolInterval().String(),
		})
	case http.MethodPost:
		var req struct {
			RouteID  string `json:"route_id"`
			CameraID string `json:"camera_id"`
			Note     string `json:"note"`
		}
		if err := decodeBody(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.RouteID == "" {
			http.Error(w, "route_id is required", http.StatusBadRequest)
			return
		}
		subject := req.RouteID
		text := "checked " + orEmpty(req.CameraID, "the route")
		if req.Note != "" {
			text += ": " + req.Note
		}
		if !s.ingest.logPatrolNote(text, subject) {
			http.Error(w, "the shifts hand-over artifact could not be written", http.StatusServiceUnavailable)
			return
		}
		s.ingest.rebuildBrief()
		s.ingest.wsHub.Broadcast(ws.Message{
			Type: "patrol",
			Data: map[string]any{
				"kind":      "checked",
				"route_id":  req.RouteID,
				"camera_id": req.CameraID,
				"note":      req.Note,
				"at":        time.Now().UTC().Format(time.RFC3339),
			},
			Timestamp: time.Now().UTC(),
		})
		writeJSON(w, map[string]any{"status": "recorded", "route_id": req.RouteID, "camera_id": req.CameraID})
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func orEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (s *Server) handleTelemetryLive(w http.ResponseWriter, _ *http.Request) {
	if s.ingest.telemetry == nil {
		writeJSON(w, map[string]any{"error": "telemetry not enabled"})
		return
	}
	live := s.ingest.telemetry.GetLiveTelemetry()
	writeJSON(w, live)
}
