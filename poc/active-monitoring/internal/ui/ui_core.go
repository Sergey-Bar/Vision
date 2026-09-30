// Package ui is the operator screen: two read-only pages on loopback, no
// framework, no JavaScript, no editing.
//
// The screen's one rule is that every number on it came from a row. There is no
// "demo mode" that computes something else and no editing through the browser,
// and the honesty tests in this package delete a table and assert that the page
// says so rather than showing a zero.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// Live is the per-frame state the pipeline hands the screen. It is drawn by the
// server, in Go, because the pixels are already here and sending them to a
// browser only to have JavaScript draw them back would be a second reader.
type Live struct {
	At     time.Time
	Frame  []byte // JPEG, the last frame as it came off the pipe
	Width  int
	Height int
	// Boxes are normalised, and the lines are drawn in normalised coordinates
	// too, so the overlay matches the pixels at any resolution.
	Boxes []OverlayBox
	Lines []OverlayLine
	// Tracks is the pulse list: one row per live object.
	Tracks []TrackRow
	// AIState is the last sidecar state, printed so the reader can see when the
	// model was not answering.
	// Seq is the frame counter, echoed in the stream so a viewer could tell two
	// identical-looking pictures apart.
	Seq     uint64
	AIState string
	// Provenance is printed on the frame itself.
	Provenance string
	// Simulator is whether the push target is a simulator. It is rendered in the
	// header, in words.
	Simulator   bool
	Source      string
	Mode        string
	ClockSource string
}

// OverlayBox is one detection, normalised.
type OverlayBox struct {
	Class string
	Score float64
	X1    float64
	Y1    float64
	X2    float64
	Y2    float64
}

// OverlayLine is a crossing line, normalised.
type OverlayLine struct {
	Name string
	A    [2]float64
	B    [2]float64
	// LastFired is when a crossing was last seen on this line, for the pulse.
	LastFired time.Time
	Count     int
}

// TrackRow is one live object.
type TrackRow struct {
	ID    int
	Class string
	Seen  int
	Age   time.Duration
}

// Server is the screen.
type Server struct {
	Store *store.Store
	RunID string
	Addr  string

	mu   sync.RWMutex
	live Live
	// started is when this process started, for the uptime in the header.
	started time.Time
	// lastFaultAge is how long ago the last fault was recorded, so a fault
	// injected now shows on the page within ten seconds.
	lastFaultRefresh time.Time
	faultCount       int
}

// New builds a server. It refuses a non-loopback address: the screen carries
// detections and evidence hashes and is unauthenticated, so it is a loopback
// service and nothing else.
func New(store *store.Store, runID, addr string) (*Server, error) {
	if store == nil {
		return nil, fmt.Errorf("ui: no store, so every number on the screen would have to be invented; the screen shows what the database says or it does not run")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ui: %q is not host:port, so the screen has no address and there is nothing to open", addr)
	}
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return nil, fmt.Errorf("ui: %q is not loopback; the screen is read-only but unauthenticated, and binding it to a routable address would expose detections and evidence hashes to the network", addr)
	}
	return &Server{Store: store, RunID: runID, Addr: addr, started: time.Now()}, nil
}

// Publish hands the screen the newest frame. It copies under a lock, because the
// pipeline goroutine writes it and the HTTP goroutine reads it.
func (s *Server) Publish(l Live) {
	s.mu.Lock()
	s.live = l
	s.mu.Unlock()
}

func (s *Server) current() Live {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live
}

// readOnly wraps a handler so that only GET and HEAD are served. The screen has
// no editing, so any other method is a 405 rather than a quiet 200 that happens
// to ignore the body: an endpoint that answers 200 to a POST looks like it took
// the change.
func readOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "the screen is read-only; there is nothing to change here and no number on it may be edited through a browser", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

// Handler builds the routes.
//
// Three views of the same data, and no JavaScript anywhere:
//
//	/            the operator's live view
//	/board       the seven-day board
//	/live.mjpeg   the frame stream, which the page renders inside a plain <img>
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", readOnly(handleLive(s)))
	mux.HandleFunc("/board", readOnly(handleBoard(s)))
	mux.HandleFunc("/frame.jpg", readOnly(s.handleFrame))
	mux.HandleFunc("/still.jpg", readOnly(s.stillHandler))
	mux.HandleFunc("/live.mjpeg", readOnly(s.streamHandler))
	mux.HandleFunc("/api/summary", readOnly(s.handleJSON))
	return mux
}

// Serve runs until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("ui: the screen at %s could not be served: %w", s.Addr, err)
	}
	return nil
}

// handleFrame serves the last frame with the overlay drawn on it, in Go. The
// pixels already exist here; sending them to a browser to be drawn back by
// JavaScript would be a second reader of the video, which is the bug this whole
// architecture exists to prevent.
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	l := s.current()
	if len(l.Frame) == 0 {
		// "no data" is a different thing from "a black frame", and a black
		// frame would look like the camera is pointing at something.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no frame has been read yet"))
		return
	}
	drawn, err := DrawOverlay(l)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("the overlay could not be drawn: " + err.Error()))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(drawn)
}

// Data is everything the pages render, gathered from rows. Every field is a
// count from the database or a value the pipeline measured; none is computed by
// the UI.
type Data struct {
	Run         *store.Run          `json:"run"`
	Elapsed     string              `json:"elapsed"`
	Verdict     string              `json:"verdict"`
	Provisional bool                `json:"provisional"`
	Counts      map[string]int      `json:"counts"`
	FaultCounts map[string]int      `json:"fault_counts"`
	Faults      []store.Fault       `json:"faults"`
	Recent      []store.Detection   `json:"recent"`
	Heartbeats  int                 `json:"heartbeats"`
	Gaps        []store.Gap         `json:"heartbeat_gaps"`
	Stalled     []store.Detection   `json:"stalled"`
	Peaks       store.Peaks         `json:"peaks"`
	Evidence    store.EvidenceStats `json:"evidence"`
	Push        store.AttemptStats  `json:"push"`
	Unavailable []string            `json:"unavailable"`
	Live        Live                `json:"live"`
	Truth       [2]string           `json:"truth"`
	DBPath      string              `json:"db_path"`
	DBError     string              `json:"db_error,omitempty"`
}

// Gather reads the store. A read failure becomes an explicit "unavailable" entry
// rather than a zero: a zero would be a measurement, and there was no
// measurement.
func (s *Server) Gather(ctx context.Context) Data {
	d := Data{Counts: map[string]int{}, FaultCounts: map[string]int{}, DBPath: s.Store.Path()}
	run, err := s.Store.RunByID(ctx, s.RunID)
	if err != nil {
		d.Unavailable = append(d.Unavailable, "runs: "+err.Error())
	}
	d.Run = run
	if run != nil {
		end := time.Now().UTC()
		if run.EndedAt != nil {
			end = *run.EndedAt
		}
		d.Elapsed = end.Sub(run.StartedAt).Round(time.Second).String()
	}
	if c, err := s.Store.CountByStatus(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "detections: "+err.Error())
	} else {
		d.Counts = c
	}
	if c, err := s.Store.FaultCounts(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "faults: "+err.Error())
	} else {
		d.FaultCounts = c
	}
	if f, err := s.Store.Faults(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "fault rows: "+err.Error())
	} else {
		d.Faults = f
	}
	if r, err := s.Store.Recent(ctx, s.RunID, 50); err != nil {
		d.Unavailable = append(d.Unavailable, "recent detections: "+err.Error())
	} else {
		d.Recent = r
	}
	if n, err := s.Store.HeartbeatCount(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "heartbeats: "+err.Error())
	} else {
		d.Heartbeats = n
	}
	if g, err := s.Store.HeartbeatGaps(ctx, s.RunID, 30*time.Second); err != nil {
		d.Unavailable = append(d.Unavailable, "heartbeat gaps: "+err.Error())
	} else {
		d.Gaps = g
	}
	if st, err := s.Store.Stalled(ctx, s.RunID, 5*time.Minute); err != nil {
		d.Unavailable = append(d.Unavailable, "stalled detections: "+err.Error())
	} else {
		d.Stalled = st
	}
	if p, err := s.Store.SamplePeaks(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "resource samples: "+err.Error())
	} else {
		d.Peaks = p
	}
	if e, err := s.Store.VerifyStats(ctx, s.RunID, nil); err != nil {
		d.Unavailable = append(d.Unavailable, "evidence: "+err.Error())
	} else {
		d.Evidence = e
	}
	if p, err := s.Store.Stats(ctx, s.RunID); err != nil {
		d.Unavailable = append(d.Unavailable, "outbox: "+err.Error())
	} else {
		d.Push = p
	}
	l := s.current()
	d.Live = l
	return d
}

// VerdictFor computes the three-state verdict from the gathered rows. It is the
// same function the report uses, so the screen and the report cannot disagree
// about whether a run passed.
func (s *Server) verdictFor(ctx context.Context, d Data) (string, bool) {
	if d.Run == nil {
		return "NOT RUN", false
	}
	in := inputsFrom(d)
	return string(verdictOf(in)), in.Elapsed < in.Target
}

func esc(s string) string { return html.EscapeString(s) }

// noData renders the "no data" line. It is deliberately different from a zero:
// a reader must not be able to mistake "nothing was recorded" for "nothing
// happened".
func noData(what string) string {
	return `<p class="nodata">no data &mdash; ` + esc(what) + ` was not recorded. This is not a zero.</p>`
}

// handleJSON is what the honesty tests read. The tests assert on the JSON rather
// than on the HTML, because a change to a class name must not be able to make an
// honesty test pass or fail.
func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := s.Gather(ctx)
	verdict, provisional := s.verdictFor(ctx, d)
	d.Verdict = verdict
	d.Provisional = provisional
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d)
}

func pushSentence(l Live, d Data) string {
	// The run row is the authority, not the live frame. A zero-valued Live has
	// Simulator=false, and rendering that as "XProtect (declared live)" would
	// claim a VMS landing that never happened, which is the one thing this screen
	// must never do. The run row says what the run was configured against, and a
	// run row that is absent says "not stated".
	if d.Run == nil {
		return "push target: not stated (no run row)"
	}
	if d.Run.PushIsVMS {
		return "XProtect at " + d.Run.PushBaseURL + " (declared live)"
	}
	return "local simulator (xpsim) — no XProtect server was reachable during this run"
}

func orNone(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}
