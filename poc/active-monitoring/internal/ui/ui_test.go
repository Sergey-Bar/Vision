package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

func uiStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id := "run-ui"
	if err := s.StartRun(context.Background(), store.Run{
		ID: id, Label: "ui-test", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Hour), TargetSeconds: 3600,
	}); err != nil {
		t.Fatal(err)
	}
	return s, id
}

func newUI(t *testing.T, s *store.Store, id string) *Server {
	t.Helper()
	u, err := New(s, id, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func getJSON(t *testing.T, h http.Handler, path string) Data {
	t.Helper()
	_, body := get(t, h, path)
	var d Data
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("the JSON feed does not parse: %v\n%s", err, body)
	}
	return d
}

func TestNewRefusesANonLoopbackAddress(t *testing.T) {
	s, id := uiStore(t)
	if _, err := New(s, id, "0.0.0.0:8097"); err == nil {
		t.Fatal("the screen was allowed to bind a wildcard address")
	}
	if _, err := New(s, id, "192.168.1.5:8097"); err == nil {
		t.Fatal("the screen was allowed to bind a routable address")
	}
	if _, err := New(s, id, "127.0.0.1:8097"); err != nil {
		t.Fatalf("loopback was refused: %v", err)
	}
}

func TestNewRefusesToRunWithNoStore(t *testing.T) {
	if _, err := New(nil, "r", "127.0.0.1:8097"); err == nil {
		t.Fatal("the screen was built with no store; every number on it would have to be invented")
	}
}

// TestEmptyRunShowsNoDataAndNotZero is T33's central test. Every count is zero
// because nothing has been recorded, and the page must say so in words. A zero
// would be a measurement, and there was no measurement.
func TestEmptyRunShowsNoDataAndNotZero(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()

	_, live := get(t, h, "/")
	for _, want := range []string{
		"no data", "no frame has been read yet",
		"no detection has been recorded",
		"no fault has been recorded",
		"no detection has been recorded",
		"the absence of",
	} {
		if !strings.Contains(live, want) {
			t.Fatalf("the live page does not say %q:\n%s", want, live)
		}
	}
	// The counts must still be readable and zero: the tables exist.
	d := getJSON(t, h, "/api/summary")
	if len(d.Counts) != 0 {
		t.Fatalf("counts = %v, want an empty map for a run with no detections", d.Counts)
	}
	if d.Heartbeats != 0 {
		t.Fatalf("heartbeats = %d, want 0", d.Heartbeats)
	}

	_, board := get(t, h, "/board")
	for _, want := range []string{
		"no data", "no heartbeat has been recorded, so availability is not computed",
		"no fault has been recorded",
		"no detection has been recorded",
	} {
		if !strings.Contains(board, want) {
			t.Fatalf("the board does not say %q:\n%s", want, board)
		}
	}
}

// TestAMissingTableBreaksThePageRatherThanShowingZero is the red-check the
// plan asks for: delete the faults table and the page must report that it could
// not read it, not render an empty histogram as if nothing had ever been
// recorded.
func TestAMissingTableBreaksThePageRatherThanShowingZero(t *testing.T) {
	s, id := uiStore(t)
	if _, err := s.DB().Exec("DROP TABLE faults"); err != nil {
		t.Fatal(err)
	}
	h := newUI(t, s, id).Handler()

	_, live := get(t, h, "/")
	if !strings.Contains(live, "This page could not read") {
		t.Fatalf("the page did not report that a table is missing:\n%s", live)
	}
	if !strings.Contains(live, "no data") {
		t.Fatal("the page rendered numbers while a table was unreadable")
	}
	d := getJSON(t, h, "/api/summary")
	if len(d.Unavailable) == 0 {
		t.Fatal("the JSON feed reported nothing unavailable while the faults table was dropped")
	}
	joined := strings.Join(d.Unavailable, "; ")
	if !strings.Contains(joined, "faults") {
		t.Fatalf("the unavailable list does not name the faults table: %q", joined)
	}
}

func TestAFaultInjectedNowAppearsWithinTenSeconds(t *testing.T) {
	s, id := uiStore(t)
	u := newUI(t, s, id)
	h := u.Handler()
	_, before := get(t, h, "/")
	if !strings.Contains(before, "no fault has been recorded") {
		t.Fatalf("the starting page already claims a fault: %s", before)
	}
	// A real fault, not a rehearsal: no expectation was declared for it, and an
	// un-rehearsed fault is a FAIL because a fault nobody predicted is exactly
	// what a reliability run exists to find.
	if err := s.RecordFault(context.Background(), id, store.Fault{
		At: time.Now(), Reason: "ai_pipe_backpressure",
		Detail: "the MJPEG pipe was blocked for 5s, past the deadline",
	}); err != nil {
		t.Fatal(err)
	}
	_, after := get(t, h, "/")
	if !strings.Contains(after, "ai_pipe_backpressure") {
		t.Fatalf("an injected fault did not appear on the page:\n%s", after)
	}
	// T33: a fault on the page must render as FAIL. A row that says "recorded"
	// and a row that says FAIL must not look the same, because a reader
	// scanning for a red cell is the reason the row is there.
	if !strings.Contains(after, `data-verdict="FAIL"`) {
		t.Fatalf("an injected fault did not render as FAIL on the page:\n%s", after)
	}
	// The board's histogram must show it too.
	_, board := get(t, h, "/board")
	if !strings.Contains(board, "ai_pipe_backpressure") {
		t.Fatal("the board's fault histogram did not show the injected fault")
	}
}

// TestARehearsedFaultThatBehavedAsExpectedRendersPASS: a rehearsed fault that
// did what it should is not a failure, and the page must not paint it red. The
// two states have to be distinguishable, or the red means nothing.
func TestARehearsedFaultThatBehavedAsExpectedRendersPASS(t *testing.T) {
	s, id := uiStore(t)
	ctx := context.Background()
	if err := s.RecordFault(ctx, id, store.Fault{
		At: time.Now(), Reason: "rehearsal:401", Detail: "a 401 refreshes the token once",
		Expected: "auth_failed", Observed: "auth_failed",
	}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, newUI(t, s, id).Handler(), "/")
	if !strings.Contains(body, `data-verdict="PASS"`) {
		t.Fatalf("a rehearsed fault that matched its expectation did not render as PASS:\n%s", body)
	}
	// And a mismatch is red, with both sides named.
	if err := s.RecordFault(ctx, id, store.Fault{
		At: time.Now(), Reason: "rehearsal:500", Detail: "a 500 is worth another attempt",
		Expected: "auth_failed", Observed: "retry",
	}); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, newUI(t, s, id).Handler(), "/")
	if !strings.Contains(body, "expected auth_failed, observed retry") {
		t.Fatalf("a mismatched rehearsal does not show both sides:\n%s", body)
	}
}

func TestAnEmptyConnectionIsNeverRenderedAsZeroForAVerdict(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()
	d := getJSON(t, h, "/api/summary")
	if d.Verdict == "PASS" {
		t.Fatal("a run with nothing recorded was rendered as a pass")
	}
	if d.Verdict == "" {
		t.Fatal("the verdict is empty; there are three verdicts and one of them must be shown")
	}
}

func TestTheHeaderAlwaysCarriesTheTruthLine(t *testing.T) {
	s, id := uiStore(t)
	u := newUI(t, s, id)
	u.Publish(Live{Source: "recorded loop (no live camera)", Mode: "live", Simulator: true,
		ClockSource: "ntp", Provenance: "synthetic", At: time.Now()})
	h := u.Handler()
	for _, path := range []string{"/", "/board"} {
		_, body := get(t, h, path)
		for _, want := range []string{
			"recorded loop (no live camera)",
			"local simulator (xpsim)",
			"no XProtect server was reachable during this run",
			"live",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s does not carry %q", path, want)
			}
		}
	}
}

func TestAnUnstatedSourceIsShownAsUnstatedNotGuessed(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()
	_, body := get(t, h, "/")
	if !strings.Contains(body, "not stated") {
		t.Fatalf("the header guessed a source or omitted it:\n%s", body)
	}
}

func TestProvenanceIsRenderedOnTheFrameAndThePage(t *testing.T) {
	s, id := uiStore(t)
	u := newUI(t, s, id)
	u.Publish(Live{Source: "recorded loop (no live camera)", Mode: "demo-replay", Simulator: true,
		Provenance: "synthetic", AIState: "no_objects", At: time.Now(), Width: 64, Height: 48,
		Frame:  tinyJPEG(t, 64, 48),
		Boxes:  []OverlayBox{{Class: "bird", Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.5}},
		Lines:  []OverlayLine{{Name: "kerb", A: [2]float64{0, 0.5}, B: [2]float64{1, 0.5}}},
		Tracks: []TrackRow{{ID: 1, Class: "bird", Seen: 3, Age: time.Second}},
	})
	h := u.Handler()
	_, body := get(t, h, "/")
	for _, want := range []string{">synthetic</b>", ">no_objects</b>", "mode:</b> demo-replay"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the page does not show %q:\n%s", want, body)
		}
	}
	code, frame := get(t, h, "/frame.jpg")
	if code != 200 {
		t.Fatalf("the frame endpoint answered %d", code)
	}
	if !strings.HasPrefix(frame, "\xff\xd8") {
		t.Fatal("the frame endpoint did not return a JPEG")
	}
}

func TestNoFrameIsServiceUnavailableNotABlackImage(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()
	code, body := get(t, h, "/frame.jpg")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("with no frame the endpoint answered %d, want 503", code)
	}
	if strings.Contains(body, "\xff\xd8") {
		t.Fatal("with no frame the endpoint returned an image; a black frame would look like a camera pointing at something")
	}
	if !strings.Contains(body, "no frame has been read yet") {
		t.Fatalf("the body must say why: %q", body)
	}
}

func TestTheFrameIsDrawnOnTheServer(t *testing.T) {
	// The overlay must change the bytes. If drawing silently did nothing, the
	// screen would show a clean frame with no boxes and no lines and look fine.
	base := Live{Frame: tinyJPEG(t, 64, 48), Width: 64, Height: 48}
	withOverlay := base
	withOverlay.Boxes = []OverlayBox{{Class: "car", X1: 0.1, Y1: 0.1, X2: 0.9, Y2: 0.9}}
	withOverlay.Lines = []OverlayLine{{Name: "kerb", A: [2]float64{0, 0.5}, B: [2]float64{1, 0.5}}}
	plain, err := DrawOverlay(base)
	if err != nil {
		t.Fatal(err)
	}
	drawn, err := DrawOverlay(withOverlay)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == len(drawn) && string(plain) == string(drawn) {
		t.Fatal("the overlay did not change a single byte: the boxes and lines are not being drawn")
	}
}

// storeSnapshotFixture writes a real file at a real content-derived reference
// and records a detection pointing at it, so the route is tested against the
// shape the pipeline actually produces rather than against a made-up one.
func storeSnapshotFixture(t *testing.T, s *store.Store, runID string) string {
	t.Helper()
	ref := strings.Repeat("ab", 32) // 64 hex characters
	rel := "ab/ab/" + ref + ".jpg"
	p := filepath.Join(filepath.Dir(s.Path()), "evidence", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, tinyJPEG(t, 8, 8), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(context.Background(), store.Detection{
		ID: "det-snap", RunID: runID, At: time.Now(), Class: "car", Family: "vehicle",
		TrackID: 1, BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
		Direction: "left-to-right", AIState: "asked_answered", Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSnapshot(context.Background(), "det-snap", rel, store.EvidenceCrossingSnapshot); err != nil {
		t.Fatal(err)
	}
	return rel
}

// TestTheSnapshotRouteServesTheStoredJPEG: the picture of the decision has to be
// openable, and the screen has to reach it by reference rather than by rebuilding
// a path out of a column and hoping.
func TestTheSnapshotRouteServesTheStoredJPEG(t *testing.T) {
	s, id := uiStore(t)
	rel := storeSnapshotFixture(t, s, id)
	h := newUI(t, s, id).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/snapshot.jpg?ref="+url.QueryEscape(rel), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("a stored snapshot answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content type %q, want image/jpeg", got)
	}
	if !strings.HasPrefix(rec.Body.String(), "\xff\xd8") {
		t.Fatal("the served bytes are not a JPEG")
	}
}

// TestTheSnapshotRouteRefusesTraversal is the negative test the evidence rules
// require on every evidence path, and this one is new.
//
// A reference arrives in a QUERY STRING. A handler that joins a query string onto
// a path is one `?ref=../../poc.db` away from serving the database, so the shape
// is matched against a pattern before the path is built at all. Each case below
// is a way a caller might try to name a file the store does not own.
func TestTheSnapshotRouteRefusesTraversal(t *testing.T) {
	s, id := uiStore(t)
	storeSnapshotFixture(t, s, id)
	// A file the store does NOT own, sitting one level above the evidence root.
	// If any of these escapes, this is what it would read.
	secret := filepath.Join(filepath.Dir(s.Path()), "not-evidence.txt")
	if err := os.WriteFile(secret, []byte("this is not evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newUI(t, s, id).Handler()

	cases := []struct{ name, ref string }{
		{"parent traversal", "../../not-evidence.txt"},
		{"parent traversal with the right shape", "../ab/ab/" + strings.Repeat("ab", 32) + ".jpg"},
		{"absolute path", filepath.ToSlash(secret)},
		{"absolute windows path", `C:/poc-data/not-evidence.txt`},
		{"nul byte", "ab/ab/%00.jpg"},
		{"no separator at all", strings.Repeat("ab", 32) + ".jpg"},
		{"uppercase hex", "AB/AB/" + strings.Repeat("AB", 32) + ".jpg"},
		{"three levels", "ab/ab/cd/" + strings.Repeat("ab", 32) + ".jpg"},
		{"wrong extension", "ab/ab/" + strings.Repeat("ab", 32) + ".exe"},
		{"empty", ""},
		{"backslash separator", `ab\ab\` + strings.Repeat("ab", 32) + `.jpg`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/snapshot.jpg?ref="+url.QueryEscape(c.ref), nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("ref %q was served with 200; a route that reads a query string and builds a path is one traversal away from serving the database", c.ref)
			}
			if strings.Contains(rec.Body.String(), "this is not evidence") {
				t.Fatalf("ref %q returned a file outside the evidence store", c.ref)
			}
			if !strings.Contains(rec.Body.String(), "no data") && !strings.Contains(rec.Body.String(), "not one") {
				t.Fatalf("ref %q was refused with %d but the body does not say why: %q", c.ref, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestAMissingSnapshotIsNotFoundNotAGreyBox: the panel must say the same thing
// the still endpoint says for a missing frame. A grey placeholder reads as "there
// was no object there", which is a different claim from "no picture was
// rendered", and only one of them is true.
func TestAMissingSnapshotIsNotFoundNotAGreyBox(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()
	missing := "cd/cd/" + strings.Repeat("0", 64) + ".jpg"
	code, body := get(t, h, "/snapshot.jpg?ref="+url.QueryEscape(missing))
	if code != http.StatusNotFound {
		t.Fatalf("a snapshot that is not on disk answered %d, want 404", code)
	}
	if !strings.Contains(body, "no data") {
		t.Fatalf("the body must say `no data`: %q", body)
	}
}

func TestTheScreenHasNoJavaScriptAndNoEditing(t *testing.T) {
	s, id := uiStore(t)
	h := newUI(t, s, id).Handler()
	for _, path := range []string{"/", "/board"} {
		_, body := get(t, h, path)
		lower := strings.ToLower(body)
		for _, forbidden := range []string{"<script", "onclick=", "onload=", "fetch(", "xmlhttprequest", "<form", "contenteditable"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains %q; the screen is read-only with no JavaScript", path, forbidden)
			}
		}
	}
	// Every non-GET method on every route is refused, because there is nothing
	// to change.
	for _, path := range []string{"/", "/board", "/api/summary", "/frame.jpg"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("x")))
		if rec.Code == http.StatusOK {
			t.Fatalf("POST %s was accepted; the screen has no editing", path)
		}
	}
}

func TestHumanBytesSaysNoSamplesRatherThanZero(t *testing.T) {
	if got := humanBytes(0); got != "no samples" {
		t.Fatalf("humanBytes(0) = %q; a zero here would be a measurement, and there was none", got)
	}
	if got := humanBytes(1536); got != "1.5 KiB" {
		t.Fatalf("humanBytes(1536) = %q", got)
	}
}

func TestCountOrDistinguishesNoRowsFromZeroRows(t *testing.T) {
	if got := countOr(map[string]int{}); got != "no data" {
		t.Fatalf("countOr(empty) = %q", got)
	}
	if got := countOr(map[string]int{"done": 0}); got != "0" {
		t.Fatalf("countOr(done=0) = %q; zero rows IS a measurement", got)
	}
}
