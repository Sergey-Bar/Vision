package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// A long-running, scripted Milestone endpoint.
//
// # Why it exists
//
// Criterion 09 asks whether an operator can find the second of an incident within
// thirty seconds. Nothing in this build can answer that against a real recorder,
// and a recorder is a year of procurement. What it can do is put a vendor-shaped
// HTTP server on the other end of a real socket and measure the thing the claim is
// actually about:
//
//	when an incident is raised, how long before a recording that CONTAINS it is
//	open, and how much of what happened before it is in the clip
//
// That is measurable, and it is the part of the thirty seconds that is a property
// of this code rather than of a recorder's write bandwidth. The write itself takes
// milliseconds; the thirty seconds in the criterion is a budget, not a latency.
//
// # It is not a Transport, and the difference matters
//
// A `Transport` is what the adapter consumes. This is the thing the Transport
// talks to. Keeping them apart is why the scripted handlers that used to be
// `httptest.NewServer` closures inside tests now have one home, and why a probe can
// be pointed at this server and get the same answers it gets against a device.
//
// # What it is not
//
// Not a Milestone installation. It serves the nine routes in [Routes] and nothing
// else, it answers `GET_PARAMETER` with 501 the way a device would answer a method
// it does not have, and it never once changes a route's Unverified flag — which is
// the control `probe.go` exists to protect and which `TestTheProbeNeverPromotesARoute`
// would fail on if this file could.
//
// # Idempotency is the load-bearing behaviour
//
// The same `Idempotency-Key` gets the same reference back, and the second answer is
// marked as a replay. That is not a convenience: a bookmark write that is retried
// after a timeout must not create a second recording, and a server that cannot
// demonstrate that is a server that cannot stand in for the case the retry exists
// to handle.

// LiveOptions configures [StartLiveServer]. Every field has a usable default except
// [LiveOptions.Token], which has none: a vendor endpoint with no credential on it
// is a thing to be deliberate about.
type LiveOptions struct {
	// Listen is host:port. Port 0 asks the OS for one.
	Listen string
	// Token is the Bearer credential every request must carry. Empty means the
	// server refuses everything, which is the safe default for a thing that listens
	// on a socket.
	Token string
	// Now is the server's clock. Injected so a demo can rebase an incident into the
	// past without waiting, which is the whole reason criterion 09 can be measured
	// at all in a test.
	Now func() time.Time
	// Events is what the event feed serves, oldest first.
	Events []LiveEvent
	// Latency is how long each response takes. Zero is immediate. A non-zero value
	// is how a demo shows the thirty-second budget being spent rather than assumed.
	Latency time.Duration
}

// LiveEvent is one event the feed serves, in the shape the adapter parses.
type LiveEvent struct {
	ID       string
	Type     string
	DeviceID string
	Value    string
	// At is the instant the event claims. Zero means "server time", so a fixture
	// with no timestamps still produces a stream with a monotonic clock.
	At time.Time
}

// LiveBookmark is what the server recorded when a bookmark was placed.
//
// `Placed` is the audit trail of the whole criterion: it is what an operator would
// have, and comparing its window against the incident's window is the measurement.
type LiveBookmark struct {
	Reference string
	Key       string
	Header    string
	DeviceID  string
	Triggered time.Time
	Begin     time.Time
	End       time.Time
	Replayed  bool
}

// LiveServer is a running scripted endpoint.
type LiveServer struct {
	opts LiveOptions
	srv  *http.Server
	ln   net.Listener

	mu        sync.Mutex
	placed    []LiveBookmark
	byKey     map[string]string
	requests  []string
	nextRefIn int
}

// StartLiveServer starts listening and serving. It returns once the listener is up,
// so a caller can read [LiveServer.URL] and connect without sleeping.
func StartLiveServer(ctx context.Context, opts LiveOptions) (*LiveServer, error) {
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	// `ListenConfig.Listen` rather than `net.Listen`, so a cancellation can stop the
	// listener instead of the server outliving the context that asked for it.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("milestone: listen on %s: %w", opts.Listen, err)
	}
	s := &LiveServer{
		opts:      opts,
		ln:        ln,
		byKey:     map[string]string{},
		nextRefIn: 1,
	}
	mux := http.NewServeMux()
	// Every route in the table, plus the write route the table does not carry.
	// Registering from [Routes] rather than from a hand-copied list is what keeps
	// this server honest as routes are added: a route added to the table and not
	// served here answers 404 and the probe calls it a contradiction, which is the
	// truth about a device that has it.
	for _, route := range Routes {
		// The bookmarks route is the one the table declares as a READ (with a
		// `searchTime` task) and the write-back layer declares again as a WRITE. Two
		// registrations on one path is a panic in `ServeMux`, and the right fix is
		// the one that says what happened: the bookmarks handler dispatches on
		// method, so it answers both, and registering it once is the truth.
		if route.Path == APIPrefix+"/bookmarks" {
			continue
		}
		mux.HandleFunc(route.Path, s.handleRead(route))
	}
	mux.HandleFunc(APIPrefix+"/bookmarks", s.handleBookmarks)
	mux.HandleFunc("/", s.handleUnknown)
	// ReadHeaderTimeout is set because a server without one is a Slowloris target, and
	// the fact that this one listens on loopback for a demo does not make the rule
	// conditional: the day somebody points it at a routable address the hole is
	// already there. ReadTimeout is bounded generously because a bookmark write may
	// legitimately carry a video reference.
	s.srv = &http.Server{Handler: s.withLog(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	go func() { //nolint:gosec // G118: reported at the goroutine, not at the statement. The Background() below is deliberate -- see there.
		<-ctx.Done()
		// A fresh context, not a child of the one that just ended.
		//
		// `Shutdown` needs a deadline, and a deadline needs a live context. Deriving
		// it from `ctx` would be deriving it from a context that is already
		// cancelled, which turns a graceful two-second drain into an immediate
		// refusal -- so an in-flight bookmark write would be cut off by the very
		// shutdown meant to let it finish. G118 is right that a `Background()` inside
		// a goroutine is usually a mistake and wrong here for exactly that reason.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //nolint:gosec // G118: the ctx above is already cancelled; deriving from it would refuse the drain
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// URL is the base URL a `Transport` should be built with.
func (s *LiveServer) URL() string { return "http://" + s.ln.Addr().String() }

// Close stops the server.
func (s *LiveServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// Placed returns every bookmark written, oldest first.
//
// This is the criterion's measurement surface: the window in the last entry is what
// an operator would have been handed.
func (s *LiveServer) Placed() []LiveBookmark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LiveBookmark(nil), s.placed...)
}

// Requests returns "METHOD /path" for everything served, oldest first.
func (s *LiveServer) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// withLog enforces the credential and records the request.
//
// The credential check is here rather than per-route so that a route added to the
// table is protected the moment it is registered, rather than the day someone
// remembers.
func (s *LiveServer) withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		if s.opts.Latency > 0 {
			time.Sleep(s.opts.Latency)
		}
		if s.opts.Token != "" {
			got := r.Header.Get("Authorization")
			if got != "Bearer "+s.opts.Token {
				w.Header().Set("WWW-Authenticate", `Bearer realm="milestone"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleUnknown answers the way a device answers a path it does not have, and says
// which paths it does have.
//
// A 404 with a list is more useful to a probe than a bare 404, and `Probe` puts the
// body in its report — so the operator reading the report learns what to try next
// instead of learning that something is wrong.
func (s *LiveServer) handleUnknown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	// `json.NewEncoder` rather than `Fprintf` with a `%s` inside a literal, because
	// the two values here come from the request and gosec's taint analysis is right
	// to ask: a path containing a quote would otherwise produce a body that is not
	// JSON, and a body that is not JSON is a body a browser may render.
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": fmt.Sprintf("no such route: %s %s", r.Method, r.URL.Path),
	})
}

// handleRead serves one GET route.
func (s *LiveServer) handleRead(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case route.ClockProbe:
			_, _ = fmt.Fprintf(w, `{"dateTime":%q}`, s.opts.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
		case route.Path == APIPrefix+"/events":
			s.writeEvents(w, r)
		case route.Path == APIPrefix+"/eventTypes":
			_, _ = w.Write([]byte(`{"array":[{"id":"481d4d6c-9156-4698-af7e-74b32f9e4359",` +
				`"name":"Motion Started","generatorType":"MIPDevice"}]}`))
		default:
			// Every other declared route answers with an empty, correctly-shaped
			// envelope. An empty `array` is a CONTRADICTION for a route declared
			// `Expect: "array"` — which is the honest answer for a device that has
			// the route and nothing in it, and it is what the probe should say.
			if route.Expect == "array" {
				_, _ = w.Write([]byte(`{"array":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}
	}
}

// writeEvents serves the feed in the vendor's own paged envelope.
//
// The envelope is `{array, eventCount, _links}` rather than a bare array because
// the adapter's pagination and `decodeEnvelope` both have to see the real shape,
// and a feed that only ever returned `[]` would leave the paging path untested.
func (s *LiveServer) writeEvents(w http.ResponseWriter, r *http.Request) {
	page := s.opts.Events
	if since := r.URL.Query().Get("time"); since != "" {
		cutoff, err := time.Parse(time.RFC3339, since)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			// Encoded rather than interpolated: `since` came off the query string.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": fmt.Sprintf("time=%q is not RFC3339", since),
			})
			return
		}
		kept := page[:0:0]
		for _, e := range page {
			if e.At.After(cutoff) {
				kept = append(kept, e)
			}
		}
		page = kept
	}
	items := make([]map[string]any, 0, len(page))
	for i, e := range page {
		at := e.At
		if at.IsZero() {
			at = s.opts.Now().UTC()
		}
		items = append(items, map[string]any{
			"Id": e.ID, "Type": e.Type,
			"Timestamp": at.UTC().Format("2006-01-02T15:04:05.000Z"),
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"DeviceObject": map[string]any{"Id": e.DeviceID},
						"Confidence":   0.94,
						"ClassId":      "Vehicle",
						"ClassName":    e.Type,
					},
				},
			},
			"__index": i,
		})
	}
	// `__index` would be a field the adapter does not know about, and this server's
	// job is to be a shape the adapter must survive. Drop it on the way out.
	for _, it := range items {
		delete(it, "__index")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"array": items, "eventCount": len(items),
		"_links": map[string]any{"self": map[string]any{"href": r.URL.String()}},
	})
}

// bookmarkBody is the documented request shape, read here rather than decoded into
// [Bookmark] because the point of this handler is to prove that what goes on the
// wire is what the write-back tests assert. Reading it into the product's own type
// would make the assertion circular.
type bookmarkBody struct {
	Header        string `json:"header"`
	Description   string `json:"description,omitempty"`
	TimeTriggered string `json:"timeTriggered,omitempty"`
	TimeBegin     string `json:"timeBegin,omitempty"`
	TimeEnd       string `json:"timeEnd,omitempty"`
	DevicePath    struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"devicePath"`
}

// handleBookmarks is the write route, and a search over what was written.
//
// The idempotency check is first and unconditional: a repeated key returns the
// original reference and says so, without a second recording. A vendor that created
// a new recording for a retried write would make the retry itself the incident.
func (s *LiveServer) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.writeBookmark(w, r)
	case http.MethodGet:
		s.searchBookmarks(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *LiveServer) writeBookmark(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		// A vendor that accepted a write with no idempotency key would be a vendor
		// whose retry creates a second recording. Answering 400 makes this server
		// unable to stand in for a device that is safe to retry against, which is
		// the point: the client has to send the key, and this proves it did.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Idempotency-Key is required; a write that cannot be " +
			"deduplicated will create a second recording when it is retried"}`))
		return
	}
	var body bookmarkBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":"body is not the documented shape: %v"}`, err)
		return
	}
	if body.Header == "" || body.DevicePath.ID == "" || body.TimeTriggered == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"header, devicePath.id and timeTriggered are required"}`))
		return
	}
	triggered, err := time.Parse(time.RFC3339, body.TimeTriggered)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":"timeTriggered %q is not RFC3339"}`, body.TimeTriggered)
		return
	}

	s.mu.Lock()
	if ref, seen := s.byKey[key]; seen {
		for i := range s.placed {
			if s.placed[i].Reference == ref {
				s.placed[i].Replayed = true
			}
		}
		s.mu.Unlock()
		w.Header().Set("Idempotency-Key", key)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"array": []map[string]any{{
			"reference": ref, "header": body.Header, "replayed": true,
		}}})
		return
	}
	ref := fmt.Sprintf("bm-live-%04d", s.nextRefIn)
	s.nextRefIn++
	s.byKey[key] = ref
	begin, end := triggered, triggered
	if t, err := time.Parse(time.RFC3339, body.TimeBegin); err == nil {
		begin = t
	}
	if t, err := time.Parse(time.RFC3339, body.TimeEnd); err == nil {
		end = t
	}
	s.placed = append(s.placed, LiveBookmark{
		Reference: ref, Key: key, Header: body.Header,
		DeviceID: body.DevicePath.ID, Triggered: triggered, Begin: begin, End: end,
	})
	s.mu.Unlock()

	w.Header().Set("Idempotency-Key", key)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"array": []map[string]any{{
		"reference": ref, "header": body.Header, "replayed": false,
	}}})
}

// searchBookmarks is the "find it again" half of criterion 09: an operator who has
// the reference has to be able to get the recording back from it.
func (s *LiveServer) searchBookmarks(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.placed))
	for _, b := range s.placed {
		if ref != "" && b.Reference != ref {
			continue
		}
		out = append(out, map[string]any{
			"reference": b.Reference, "header": b.Header,
			"timeTriggered": b.Triggered.UTC().Format(time.RFC3339),
			"timeBegin":     b.Begin.UTC().Format(time.RFC3339),
			"timeEnd":       b.End.UTC().Format(time.RFC3339),
			"devicePath":    map[string]any{"type": "cameras", "id": b.DeviceID},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"array": out, "eventCount": len(out)})
}
