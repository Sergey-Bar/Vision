package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// theEvidenceStore is the store the most recently built test server was given.
//
// The evidence-content route streams bytes off it, so a test that wants to
// exercise that route has to put real bytes behind a real evidence row.
var theEvidenceStore *evidence.Store

var clock = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// There is no secret store double here.
//
// There used to be one, and it was never called: the API has no login, so
// nothing in this tree wraps or unwraps a factor. The shared double is in
// `internal/testutil` for the trees that DO have a login, and a copy of it
// here is a copy nobody calls.

func server(t *testing.T) (*api.Server, string, string) {
	t.Helper()
	s, tenant, site, _ := serverWithRegistry(t, nil)
	return s, tenant, site
}

// serverWithRegistry is `server` plus the observability registry, so a test can
// drive the metrics route against the same wiring the daemon uses rather than
// against a route registered by a different call site.
func serverWithRegistry(t *testing.T, registry *observability.Registry) (*api.Server, string, string, *observability.Registry) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenant, site, _ := testutil.NewTenant(t, raw)
	writer := &audit.Writer{Pool: pool}
	s := &api.Server{}
	s.Audit = writer
	s.Pool = pool
	s.Tenant = tenant
	s.Secure = true
	s.SetStartTime(clock)

	// A template failure is a silent 500 without this. An operator seeing one has
	// no way to tell which page broke, and a console that renders nothing looks
	// exactly like a console with no incidents.
	var logBuf bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&logBuf, nil))
	t.Cleanup(func() {
		if t.Failed() && logBuf.Len() > 0 {
			t.Logf("server log:\n%s", logBuf.String())
		}
	})

	// The console reads through the same services the API does, so the test
	// wires real ones rather than stubs: a nil service behind a route is exactly
	// the shape of a defect the console would hit in production.
	// The evidence store is real, over a temp root, because the evidence-content
	// route streams bytes off it and a nil service behind that route is a nil
	// dereference rather than a test.
	evStore, err := evidence.New(evidence.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("evidence store: %v", err)
	}
	theEvidenceStore = evStore

	deps := api.Deps{
		Evidence:  evStore,
		Incidents: &incidents.Service{Pool: pool, Audit: writer},
		Bundles:   &bundles.Service{Pool: pool, Compiler: rules.NewCompiler(), Audit: writer},
		Metrics:   registry,
		// The structured search, real, over the same pool. A nil service behind
		// the search route is exactly the shape the comment above refuses.
		EventSearch: &eventsearch.Service{Pool: pool},
	}
	// The routes closure captures s, so it is assigned after construction rather
	// than inside the literal.
	s.Routes = func(mux *http.ServeMux) { s.RegisterRoutes(deps)(mux) }

	return s, tenant, site, registry
}

// adminStore is the evidence store the test server was built with, for the tests
// that need to put bytes behind an evidence row.
func adminStore(t *testing.T) *evidence.Store {
	t.Helper()
	return theEvidenceStore
}

// The security headers are the whole reason the console has no inline script, so
// they are asserted rather than assumed.
func TestSecurityHeadersArePresent(t *testing.T) {
	s, _, _ := server(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
		"X-Robots-Tag":               "noindex, nofollow, noarchive",
		"Strict-Transport-Security":  "max-age=31536000; includeSubDomains",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s is %q, want %q", header, got, value)
		}
	}
}

func TestContentSecurityPolicyForbidsInlineScript(t *testing.T) {
	s, _, _ := server(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy is set")
	}
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy is missing %q:\n%s", want, csp)
		}
	}
	// 'unsafe-inline' in any directive defeats the policy entirely, and one
	// template helper added later is exactly how it would get there.
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("the policy permits unsafe execution:\n%s", csp)
	}
}

func TestHealthExcludesTheAIPlane(t *testing.T) {
	s, _, _ := server(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health returned %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status is %v", body["status"])
	}
	if _, ok := body["aiPlane"]; !ok {
		t.Error("the health response says nothing about the AI plane; an operator reading " +
			"it should be able to tell the AI plane was not consulted")
	}
}

func TestConsoleIsLeftToRight(t *testing.T) {
	s, _, _ := server(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/incidents", nil)
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `dir="ltr"`) {
		t.Error("the console does not declare dir=\"ltr\"")
	}
	if !strings.Contains(body, `lang="en"`) {
		t.Error("the console does not declare lang=\"en\"")
	}
	// No inline script anywhere, because the CSP says there cannot be one and a
	// page that violates its own policy is worse than no policy.
	if strings.Contains(body, "<script") {
		t.Error("the console contains a script element; the CSP would block it anyway")
	}
	if strings.Contains(body, "style=") {
		t.Error("the console contains an inline style attribute; the CSP would block it")
	}
}

func TestJSONIsNeverNegotiatedIntoHTML(t *testing.T) {
	s, _, _ := server(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	req.Header.Set("Accept", "text/html")
	s.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("the API returned %q for a request that asked for HTML; the API is JSON only", ct)
	}
}

// Uptime used to be derived from a `start` field nothing ever assigned, so every
// install reported an uptime of about one second for the life of the process.
// The daemon now sets it; this is what proves the value is real and moves.
func TestHealthUptimeIsRealAndMonotonic(t *testing.T) {
	s, _, _ := server(t)

	base := clock
	// Five seconds of uptime at the first call. Zero would be indistinguishable
	// from a server that has not started, which is not what this asserts.
	at := base.Add(5 * time.Second)
	s.SetStartTime(base)
	// The server's clock is injectable so monotonicity is asserted by advancing
	// it, not by sleeping. A sleep-based test is a slow test that is still only
	// asserting the wall clock agrees with itself.
	s.SetClock(func() time.Time { return at })

	first := healthUptime(t, s)
	if first <= 0 {
		t.Fatalf("uptime is %v; the recorded start time is not being read", first)
	}

	at = base.Add(95 * time.Second)
	second := healthUptime(t, s)
	if second <= first {
		t.Errorf("uptime did not advance: %v then %v after 90s", first, second)
	}
	if second-first < 89 {
		t.Errorf("uptime advanced by %v over an injected 90s, which is not the clock the "+
			"handler is reading", second-first)
	}

	// A server that was never told when it started reports ~1s, not a growing
	// number. That fallback is asserted too, because it is the number that made
	// this defect invisible: a wrong constant reads like a plausible one.
	unset := &api.Server{}
	rec := httptest.NewRecorder()
	unset.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	var body struct {
		Uptime float64 `json:"uptimeSeconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body.Uptime > 2 {
		t.Errorf("a server with no recorded start time reports %vs uptime", body.Uptime)
	}
}

func TestHealthStartTimeIsRecordedByListenAndServe(t *testing.T) {
	s := &api.Server{Secure: true, Tenant: "t-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A listener that is immediately shut down still has to leave a start time,
	// because the start time is what makes uptime mean anything.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx, addr, "", "") }()

	deadline := time.Now().Add(5 * time.Second)
	for s.StartedAt().IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.StartedAt().IsZero() {
		t.Fatal("ListenAndServe returned a server whose start time was never recorded")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown returned but the listener is still serving")
	}
}

// Shutdown before ListenAndServe is a no-op rather than a nil dereference: the
// daemon calls it on a signal that can arrive before the listener is up.
func TestShutdownWithoutAListenerIsANoOp(t *testing.T) {
	s := &api.Server{}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown with no listener: %v", err)
	}
}

// The SLO verdicts are on the health surface, by name.
//
// Two properties are load-bearing and opposite. The breach has to be visible
// without anyone opening a log, because nobody is going to grep a log at 02:00 for
// an objective name. And the status must stay 200: a readiness probe that turns
// the platform unready because it is behind moves the failure to every replica at
// once and removes the capacity that would have drained the backlog.
func TestHealthReportsWhichSLOsAreBreachedAndStaysUp(t *testing.T) {
	s := &api.Server{Metrics: observability.NewRegistry()}
	r := s.Metrics
	r.Gauges.Set(observability.GaugeBacklogDepth, 90000) // above the hard limit
	r.RecordSLOs(observability.Breaches(r, observability.ReadingsFrom(r)))

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("a breached objective answered %d. Liveness must not depend on an SLO: the "+
			"process is running and removing it from the pool removes the drain", rec.Code)
	}
	var body struct {
		SLO struct {
			Status    string   `json:"status"`
			Evaluated int      `json:"evaluated"`
			Breached  []string `json:"breached"`
		} `json:"slo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body.SLO.Status != "breached" || len(body.SLO.Breached) != 1 {
		t.Errorf("the endpoint reports %q for %v", body.SLO.Status, body.SLO.Breached)
	}
	if body.SLO.Evaluated == 0 {
		t.Error("the endpoint reports nothing evaluated")
	}
	if body.SLO.Breached[0] != "ingest_backlog" {
		t.Errorf("reported %q, want ingest_backlog", body.SLO.Breached[0])
	}
	// Names only. This endpoint is unauthenticated, and a measured disk percentage
	// or a dead-letter count is not something to hand to whoever asked.
	raw := rec.Body.String()
	for _, leak := range []string{
		"%",                          // a measured percentage
		"measured",                   // an SLO's reading
		"check the dead-letter list", // a remedy
		"run a retention sweep",      // a remedy
		"a source is falling behind", // a remedy
	} {
		if strings.Contains(raw, leak) {
			t.Errorf("an unauthenticated health response contains %q:\n%s", leak, raw)
		}
	}
}

// An install whose pipeline has not run reports that it has not evaluated. The
// alternative answer is "met", from a process that has measured nothing, and that
// is the only response here that would be a lie.
func TestHealthSaysNotEvaluatedRatherThanMet(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*api.Server)
	}{
		{"no registry", func(*api.Server) {}},
		{"a registry nobody wrote to", func(s *api.Server) {
			s.Metrics = observability.NewRegistry()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &api.Server{}
			tc.set(s)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

			var body struct {
				SLO struct {
					Status string `json:"status"`
				} `json:"slo"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode health: %v", err)
			}
			if body.SLO.Status != "not evaluated" {
				t.Errorf("the endpoint says %q", body.SLO.Status)
			}
		})
	}

	// And the evaluated-and-clean case says met, so the word means something.
	// A bare registry satisfies every objective, which is the clean verdict, and
	// the point is that "met" comes from an evaluation rather than from a
	// constructor.
	r := observability.NewRegistry()
	r.RecordSLOs(observability.Breaches(r, observability.ReadingsFrom(r)))
	s := &api.Server{Metrics: r}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if !strings.Contains(rec.Body.String(), `"status":"met"`) {
		t.Errorf("evaluated objectives with no breach do not read as met:\n%s", rec.Body.String())
	}
}

func healthUptime(t *testing.T, s *api.Server) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	var body struct {
		Uptime float64 `json:"uptimeSeconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	return body.Uptime
}

// The request log must carry tenant_id, site_id and request_id on every line, per
// AGENTS.md. Without them the log cannot answer the question it exists for:
// whose request, and what id joins it to the audit row it produced.
func TestRequestLogCarriesTenantAndRequestID(t *testing.T) {
	s, tenant, _ := server(t)

	var buf bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&buf, nil))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("X-Request-Id", "req-from-the-proxy")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	if line == "" {
		t.Fatal("the request produced no log line")
	}
	for _, key := range []string{"tenant_id=" + tenant, "request_id=req-from-the-proxy", "site_id="} {
		if !strings.Contains(line, key) {
			t.Errorf("the log line is missing %q:\n%s", key, line)
		}
	}

	// A request with no inbound id still gets one, generated once and echoed.
	buf.Reset()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	generated := rec.Header().Get("X-Request-Id")
	if generated == "" {
		t.Fatal("no request id was generated for a request that arrived without one")
	}
	if !strings.Contains(buf.String(), "request_id="+generated) {
		t.Errorf("the generated id %q does not appear in the log line:\n%s", generated, buf.String())
	}
}

// mustSignIn is login for a test that only needs a session. The error path is
// fatal: a test that continued without a session would be testing the
// unauthenticated behaviour and calling it the authenticated one.
func mustSignIn(t *testing.T, s *api.Server) (*http.Cookie, string) {
	t.Helper()
	cookie, csrf, err := signIn(t, s)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	return cookie, csrf
}
