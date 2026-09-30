// Package xpsim is a local stand-in for a Milestone XProtect VMS.
//
// It is not a test double in memory. It is a separate process listening on
// loopback, reachable over HTTP, exactly as a VMS would be, and it speaks the
// paths and status codes the published specification says. The claim "the system
// talks to XProtect over the API" is therefore tested against something that
// behaves like a server rather than against a struct in the same address space.
//
// It also has to be honest about what it is. Every call to /__control/whoami
// answers with the simulator's own name, and the report prints that name next to
// every push count.
package xpsim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Name is the identity the simulator reports. It is deliberately conspicuous.
const Name = "xpsim"

// faultTimeout is how long the injected timeout holds a connection open. It has
// to exceed any sane client deadline and no more, so a rehearsal does not leave
// a goroutine sleeping for half a minute after the test is over.
const faultTimeout = 8 * time.Second

// WhoAmI is the answer to the question every honest tool must be able to ask of
// its own push target.
type WhoAmI struct {
	Name          string    `json:"name"`
	Simulator     bool      `json:"simulator"`
	Product       string    `json:"product"`
	Version       string    `json:"version"`
	Reachable     bool      `json:"reachable"`
	ServerTime    time.Time `json:"server_time"`
	EventCount    int       `json:"event_count"`
	BookmarkCount int       `json:"bookmark_count"`
}

// Fault is an injected failure. The set is closed: a rehearsal is only
// reproducible if the failure it injects is one of a known list.
type Fault string

const (
	FaultNone            Fault = ""
	FaultUnauthorized    Fault = "401"
	FaultForbidden       Fault = "403"
	FaultBadRequest      Fault = "400"
	FaultServerError     Fault = "500"
	FaultTimeout         Fault = "timeout"
	FaultConnectionReset Fault = "connection-reset"
	FaultTruncatedJSON   Fault = "truncated-json"
)

// Faults is the closed set, in the order the rehearsal runs them.
var Faults = []Fault{FaultUnauthorized, FaultServerError, FaultConnectionReset, FaultTimeout, FaultBadRequest, FaultForbidden, FaultTruncatedJSON}

// ValidFault reports whether a name is one of them.
func ValidFault(f Fault) bool {
	for _, k := range Faults {
		if k == f {
			return true
		}
	}
	return f == FaultNone
}

// Event is one accepted push, kept so the read-back call can find it.
type Event struct {
	ID       string          `json:"id"`
	Tag      string          `json:"tag"`
	Type     string          `json:"type"`
	Source   string          `json:"source"`
	Time     string          `json:"time"`
	Datatype string          `json:"datatype"`
	Data     json.RawMessage `json:"data"`
	Received time.Time       `json:"received"`
	// Raw is the exact body that arrived. It is kept because a contract test
	// that only checks parsed fields would miss a field the server would reject.
	Raw string `json:"-"`
}

// Bookmark is one accepted bookmark push.
type Bookmark struct {
	TimeTriggered string `json:"timeTriggered"`
	DevicePath    string `json:"devicePath"`
	Header        string `json:"header"`
	Received      time.Time
}

// Server is the simulator.
type Server struct {
	mu       sync.Mutex
	events   map[string]Event
	byTag    map[string]string
	bookmark []Bookmark
	// next is a counter, which is fine here: the simulator is not the thing
	// whose idempotency matters, the client is.
	next    int
	offline bool
	fault   Fault
	// faultLeft is how many more calls the armed fault applies to. A fault with
	// no count applies until it is cleared, which is how a rehearsal holds a
	// condition steady.
	faultLeft int
	// faultPath restricts the armed fault to one path, optionally narrowed to
	// one method. A rehearsal that injects "401 on the push" has to leave the
	// token call alone, or every rehearsal becomes a rehearsal of the token
	// call; and it has to leave the read-back GET alone, or the rehearsal
	// measures the read-back instead of the push.
	faultPath   string
	faultMethod string
	token       string
	tokenTTL    time.Duration
	// tokenExpiry is when the current token stops being valid, so the
	// refresh-on-401 rehearsal has something real to refresh.
	tokenExpiry time.Time
	expirySet   bool
}

// New builds a simulator.
func New() *Server {
	return &Server{
		events:   map[string]Event{},
		byTag:    map[string]string{},
		token:    "simulated-access-token",
		tokenTTL: 55 * time.Minute,
	}
}

// Offline is the "VMS is down" mode: every call fails, and the outbox has to
// stay full. It exists so a run can prove it survives a provider outage.
func (s *Server) Offline(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = v
}

// Arm injects a fault for the next n calls. n <= 0 means until it is cleared.
// An empty path arms it on every path; a named path arms it only there.
func (s *Server) Arm(f Fault, n int) error {
	return s.ArmOn(f, n, "")
}

// ArmOn injects a fault restricted to one path and method. An empty path arms
// it on every path; an empty method arms it on every method.
func (s *Server) ArmOn(f Fault, n int, path string, method ...string) error {
	if !ValidFault(f) {
		return fmt.Errorf("xpsim: %q is not one of the rehearsable faults (%s)", f, faultList())
	}
	m := ""
	if len(method) > 0 {
		m = method[0]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
	s.faultLeft = n
	s.faultPath = path
	s.faultMethod = m
	return nil
}

// Clear removes any armed fault.
func (s *Server) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault, s.faultLeft, s.faultPath, s.faultMethod = FaultNone, 0, "", ""
}

// takeFault consumes one unit of the armed fault, if any. A path- or
// method-scoped fault only fires there.
func (s *Server) takeFault(method, path string) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fault == FaultNone {
		return FaultNone, false
	}
	if s.faultPath != "" && !strings.EqualFold(s.faultPath, path) {
		return FaultNone, false
	}
	if s.faultMethod != "" && !strings.EqualFold(s.faultMethod, method) {
		return FaultNone, false
	}
	if s.faultLeft == 0 {
		return s.fault, true
	}
	s.faultLeft--
	f := s.fault
	if s.faultLeft <= 0 {
		s.fault = FaultNone
	}
	return f, true
}

func faultList() string {
	var names []string
	for _, f := range Faults {
		names = append(names, string(f))
	}
	return strings.Join(names, ", ")
}

// Handler returns the HTTP handler. The paths are the published ones, under
// /API/rest/v1, plus the two control paths this simulator adds.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Control surface. Namespaced under /__control so it can never collide
	// with a documented path, and so a report can tell at a glance that a
	// number came from a simulator.
	mux.HandleFunc("/__control/whoami", s.handleWhoami)
	mux.HandleFunc("/__control/fault", s.handleFault)
	mux.HandleFunc("/__control/reset", s.handleReset)
	mux.HandleFunc("/__control/events", s.handlePushed)

	// Authentication, at the documented path and form.
	mux.HandleFunc("/API/IDP/connect/token", s.handleToken)

	// The documented REST surface.
	mux.HandleFunc("/API/rest/v1/events", s.handleEvents)
	mux.HandleFunc("/API/rest/v1/events/bulk", s.handleBulkEvents)
	mux.HandleFunc("/API/rest/v1/bookmarks", s.handleBookmarks)
	mux.HandleFunc("/API/rest/v1/eventTypes", s.handleEventTypes)

	return mux
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	if f, ok := s.takeFault(r.Method, r.URL.Path); ok {
		s.applyFault(w, f, nil)
		return
	}
	s.mu.Lock()
	out := WhoAmI{
		Name: Name, Simulator: true,
		Product: "XProtect simulator (xpsim)", Version: "1.0.0",
		Reachable: true, ServerTime: time.Now().UTC(),
		EventCount: len(s.events), BookmarkCount: len(s.bookmark),
	}
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

func (s *Server) handleFault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "arm a fault with POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Fault string `json:"fault"`
		Count int    `json:"count"`
		// Path scopes the fault to one endpoint, so a rehearsal of "401 on the
		// push" does not turn into a rehearsal of the token call.
		Path string `json:"path"`
		// Method narrows the fault to one HTTP method on that path, so a
		// rehearsal of the POST does not also fault the read-back GET.
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("the fault request is not usable: %v", err), http.StatusBadRequest)
		return
	}
	if err := s.ArmOn(Fault(body.Fault), body.Count, body.Path, body.Method); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, 200, map[string]any{"armed": body.Fault, "count": body.Count, "path": body.Path, "method": body.Method})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.events = map[string]Event{}
	s.byTag = map[string]string{}
	s.bookmark = nil
	s.next = 0
	s.offline = false
	s.fault, s.faultLeft, s.faultPath, s.faultMethod = FaultNone, 0, "", ""
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"reset": true})
}

func (s *Server) handlePushed(w http.ResponseWriter, r *http.Request) {
	// The read-back surface: what the simulator actually holds. A client that
	// wants to prove its event landed can ask, and the report prints the count.
	s.mu.Lock()
	tags := make([]string, 0, len(s.byTag))
	for tag, id := range s.byTag {
		tags = append(tags, tag+"="+id)
	}
	sort.Strings(tags)
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"tags": tags, "count": len(tags)})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if s.isOffline() {
		s.applyFault(w, FaultConnectionReset, nil)
		return
	}
	if f, ok := s.takeFault(r.Method, r.URL.Path); ok && f != FaultUnauthorized && f != FaultForbidden {
		s.applyFault(w, f, nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the token request body is not form-encoded", http.StatusBadRequest)
		return
	}
	grant := r.FormValue("grant_type")
	client := r.FormValue("client_id")
	switch grant {
	case "client_credentials", "password", "windows_credentials":
	case "":
		http.Error(w, "grant_type is required", http.StatusBadRequest)
		return
	default:
		http.Error(w, "grant_type "+grant+" is not supported by the simulator", http.StatusBadRequest)
		return
	}
	if client == "" {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	// A fresh token on every call, so a client that keeps using an old one is
	// visibly wrong rather than accidentally working.
	s.next++
	s.token = "simulated-access-token-" + strconv.Itoa(s.next)
	s.tokenExpiry = time.Now().Add(s.tokenTTL)
	s.expirySet = true
	token := s.token
	ttl := s.tokenTTL
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		// The quickstart shows access_token and nothing else. expires_in is
		// included because the client prefers it when present and falls back to
		// reading exp out of the JWT, and the simulator wants to exercise both
		// paths.
		"expires_in": int(ttl.Seconds()),
		"scope":      "MIPService.AccessToken",
	})
}

func (s *Server) isOffline() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offline
}

func (s *Server) tokenValid(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expirySet && time.Now().After(s.tokenExpiry) {
		return false
	}
	return got == s.token
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.isOffline() {
		s.applyFault(w, FaultConnectionReset, nil)
		return
	}
	if f, ok := s.takeFault(r.Method, r.URL.Path); ok {
		if f == FaultUnauthorized {
			// 401 must not require a token: the client cannot present one it has
			// not got yet.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.applyFault(w, f, nil)
		return
	}
	if !s.tokenValid(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var ev Event
		raw, _ := readAll(r)
		if err := json.Unmarshal(raw, &ev); err != nil {
			http.Error(w, "the event body is not usable JSON", http.StatusBadRequest)
			return
		}
		if ev.Type == "" {
			http.Error(w, "type is required", http.StatusBadRequest)
			return
		}
		if ev.Source == "" {
			http.Error(w, "source is required", http.StatusBadRequest)
			return
		}
		if ev.Datatype != "analytics" {
			http.Error(w, "datatype "+ev.Datatype+" cannot be sent by a client token; only analytics and MIPDevice generator types are allowed", http.StatusForbidden)
			return
		}
		if ev.Tag == "" {
			http.Error(w, "tag is required for an analytics event, because it is the only field the read-back can match on", http.StatusBadRequest)
			return
		}
		ev.Raw = string(raw)
		s.mu.Lock()
		if id, ok := s.byTag[ev.Tag]; ok {
			s.mu.Unlock()
			// The real API would create a second event. The simulator refuses,
			// because a duplicate is exactly what AC4 forbids and a simulator
			// that let duplicates through would make the test vacuous.
			http.Error(w, "an event with tag "+ev.Tag+" already exists as "+id, http.StatusConflict)
			return
		}
		s.next++
		id := "evt-" + strconv.Itoa(s.next) + "-sim"
		ev.ID = id
		ev.Received = time.Now().UTC()
		s.events[id] = ev
		s.byTag[ev.Tag] = id
		s.mu.Unlock()
		writeJSON(w, 202, map[string]any{"data": map[string]any{"id": id}})
	case http.MethodGet:
		tag := tagFilter(r)
		s.mu.Lock()
		defer s.mu.Unlock()
		if tag != "" {
			id, ok := s.byTag[tag]
			if !ok {
				writeJSON(w, 200, map[string]any{"data": []any{}})
				return
			}
			writeJSON(w, 200, map[string]any{"data": []Event{s.events[id]}})
			return
		}
		all := make([]Event, 0, len(s.events))
		ids := make([]string, 0, len(s.events))
		for id := range s.events {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			all = append(all, s.events[id])
		}
		writeJSON(w, 200, map[string]any{"data": all})
	default:
		http.Error(w, "only GET and POST are defined on /events", http.StatusMethodNotAllowed)
	}
}

// tagFilter reads the documented filter shape: tag=equals:'<value>'.
func tagFilter(r *http.Request) string {
	raw := r.URL.Query().Get("tag")
	const prefix = "equals:"
	if !strings.HasPrefix(raw, prefix) {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(raw, prefix), "'")
}

func (s *Server) handleBulkEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "bulk events are POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.isOffline() {
		s.applyFault(w, FaultConnectionReset, nil)
		return
	}
	if !s.tokenValid(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Events []Event `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "the bulk body is not usable", http.StatusBadRequest)
		return
	}
	results := make([]map[string]any, 0, len(body.Events))
	for i, ev := range body.Events {
		if ev.Type == "" || ev.Source == "" || ev.Tag == "" {
			results = append(results, map[string]any{"index": i, "status": 400, "reason": "type, source and tag are all required"})
			continue
		}
		s.mu.Lock()
		s.next++
		id := "evt-" + strconv.Itoa(s.next) + "-sim"
		ev.ID = id
		ev.Received = time.Now().UTC()
		s.events[id] = ev
		s.byTag[ev.Tag] = id
		s.mu.Unlock()
		results = append(results, map[string]any{"index": i, "status": 202, "id": id})
	}
	// 207 is the documented answer: the batch succeeded in part.
	writeJSON(w, 207, map[string]any{"data": results})
}

func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	if s.isOffline() {
		s.applyFault(w, FaultConnectionReset, nil)
		return
	}
	if f, ok := s.takeFault(r.Method, r.URL.Path); ok {
		if f != FaultUnauthorized {
			s.applyFault(w, f, nil)
			return
		}
	}
	if !s.tokenValid(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "bookmarks are POST only in this build", http.StatusMethodNotAllowed)
		return
	}
	raw, _ := readAll(r)
	var bm struct {
		Header     *string `json:"header"`
		DevicePath *struct {
			Type *string `json:"type"`
			ID   *string `json:"id"`
		} `json:"devicePath"`
		TimeTriggered *string `json:"timeTriggered"`
		TimeBegin     *string `json:"timeBegin"`
		TimeEnd       *string `json:"timeEnd"`
		Description   *string `json:"description"`
		Reference     *string `json:"reference"`
	}
	if err := json.Unmarshal(raw, &bm); err != nil {
		http.Error(w, "the bookmark body is not usable JSON", http.StatusBadRequest)
		return
	}
	// The published body marks header and devicePath as required, and
	// timeTriggered is what makes the bookmark point at a moment. A body without
	// them is a 400, and the simulator enforces that so the contract test has
	// something to fail against.
	if bm.Header == nil {
		http.Error(w, "header is required", http.StatusBadRequest)
		return
	}
	if bm.DevicePath == nil || bm.DevicePath.ID == nil {
		http.Error(w, "devicePath is required, with an id", http.StatusBadRequest)
		return
	}
	if bm.TimeTriggered == nil {
		http.Error(w, "timeTriggered is required: a bookmark without a time does not point at anything", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.bookmark = append(s.bookmark, Bookmark{TimeTriggered: *bm.TimeTriggered, DevicePath: *bm.DevicePath.ID, Header: *bm.Header, Received: time.Now().UTC()})
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"data": map[string]any{"id": "bmk-" + strconv.FormatInt(time.Now().UnixNano(), 10)}})
}

func (s *Server) handleEventTypes(w http.ResponseWriter, r *http.Request) {
	if s.isOffline() {
		s.applyFault(w, FaultConnectionReset, nil)
		return
	}
	if !s.tokenValid(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// The simulator answers with a fixed guid. Registering a new event type needs
	// a real VMS, and that is out of scope by the plan's own §9.
	writeJSON(w, 200, map[string]any{"data": map[string]any{"item": []map[string]any{
		{"id": "b4d1e1c2-0000-4000-8000-000000000001", "name": "Analytics data", "datatype": "analytics"},
	}}})
}

func (s *Server) applyFault(w http.ResponseWriter, f Fault, _ []byte) {
	switch f {
	case FaultUnauthorized:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case FaultForbidden:
		http.Error(w, "forbidden: this token may not trigger this event type", http.StatusForbidden)
	case FaultBadRequest:
		http.Error(w, "injected bad request", http.StatusBadRequest)
	case FaultServerError:
		http.Error(w, "injected server error", http.StatusInternalServerError)
	case FaultTruncatedJSON:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		// A truncated body with a success status: the response a proxy produces
		// when it dies mid-write. The client must not read this as an accepted
		// event with no id.
		_, _ = w.Write([]byte(`{"data":{"id":"evt-`))
	case FaultTimeout:
		// Hold the connection open past any sane client deadline. The client must
		// time out and record a timeout, not a refusal.
		time.Sleep(faultTimeout)
	case FaultConnectionReset:
		// Close the connection without a response by hijacking it.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "injected connection reset", http.StatusInternalServerError)
	default:
		http.Error(w, "unknown injected fault", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
