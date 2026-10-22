package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// totpCode computes an RFC 6238 code.
//
// Written here rather than reached for in the identity package's tests,
// because a test that calls the implementation it is testing asserts that the
// implementation agrees with itself -- which is the property that was broken
// once already in that package, where the code hashed the base32 text instead
// of the bytes it encodes.
func totpCode(t *testing.T, base32Secret string, at time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(base32Secret)
	if err != nil {
		t.Fatalf("the enrolled secret is not base32: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha256.New, raw)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[off])&0x7f)<<24 |
		(uint32(sum[off+1])&0xff)<<16 |
		(uint32(sum[off+2])&0xff)<<8 |
		(uint32(sum[off+3]) & 0xff)
	return fmt.Sprintf("%06d", value%1000000)
}

// theEvidenceStore is the store the most recently built test server was given.
//
// The evidence-content route streams bytes off it, so a test that wants to
// exercise that route has to put real bytes behind a real evidence row.
var theEvidenceStore *evidence.Store

// theOperatorSecret is the TOTP secret of the principal the most recently
// built test server was given.
//
// The secret is returned exactly once, by the enrolment, and the test needs it
// to produce the code the disposition route verifies. It is a secret for a
// throwaway tenant in a test database and it never leaves the process.
var theOperatorSecret string

var clock = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// apiSecretStore is the reversible wrapper the API tests use.
//
// It used to be absent from this file, with the reason written down: the API
// has no login, so nothing in this tree wrapped or unwrapped a factor, and a
// copy of the double would be a copy nobody calls.
//
// That reason expired when the disposition route started verifying a second
// factor. The route calls the identity service, and the identity service
// unwraps the secret the enrolment sealed, so a test that drives the route
// needs a store that can open what the enrolment wrote.
type apiSecretStore struct{}

func (apiSecretStore) WrapFor(tenant, userID, factorID string, b []byte) ([]byte, error) {
	return append([]byte("B:"+tenant+"\x00"+userID+"\x00"+factorID+":"), b...), nil
}

func (apiSecretStore) UnwrapFor(tenant, userID, factorID string, b []byte) ([]byte, error) {
	tag := "B:" + tenant + "\x00" + userID + "\x00" + factorID + ":"
	if len(b) < len(tag) || string(b[:len(tag)]) != tag {
		return nil, errors.New("not wrapped by this store, or bound to a different row")
	}
	return b[len(tag):], nil
}

func server(t *testing.T) (*api.Server, string, string) {
	t.Helper()
	s, tenant, site, _ := serverWithRegistry(t, nil)
	return s, tenant, site
}

// serverWithRegistry is `server` plus the observability registry, so a test can
// drive the metrics route against the same wiring the daemon uses rather than
// against a route registered by a different call site.
//
// The opts are mutators over the dependency set. They exist because some
// routes are only reachable with a dependency the default wiring leaves nil —
// the Chief's narrator and its prior reader are two of them — and a test that
// could not install one would be asserting against a route the product never
// runs.
func serverWithRegistry(t *testing.T, registry *observability.Registry, opts ...func(*api.Deps)) (*api.Server, string, string, *observability.Registry) {
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

	// The CONFIGURED principal, which is what every request now runs as.
	//
	// The site scope is the tenant's own site, because that is what an install
	// configured for one site has: a principal with an empty scope sees no
	// site at all, and a test that seeded a site and then read an incident at
	// it would be testing a refusal rather than a read.
	//
	// The actor and the session are named rather than left empty, because the
	// ledger's actor column cannot be empty and the attention metric
	// deduplicates on the session. An empty session id would make every read a
	// new bout of attention, which is the refresh counter the metric exists not
	// to be.
	s.PrincipalSites = []string{site}
	s.PrincipalName = "operator"
	s.PrincipalSession = "test-session"

	// The identity service, real, over the same pool.
	//
	// The two sensitive disposition outcomes need a second factor, and the
	// route verifies it by calling this service. A nil service behind that
	// route is exactly the shape the comment further down refuses: the route
	// would refuse every escalation and say the install has no identity
	// service, which is a sentence about the INSTALL when the truth is about
	// the test.
	//
	// The clock is the frozen one, so a TOTP code is a fixed value rather
	// than a race against a wall clock.
	identities := &identity.Service{
		Pool: pool, Audit: writer, SecretStore: apiSecretStore{},
		Now: func() time.Time { return clock },
	}

	// The configured principal is a USER, because a second factor belongs to a
	// user and the sensitive dispositions are recorded against one.
	operatorID, err := identities.BootstrapAdmin(context.Background(), tenant, "operator",
		"a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("bootstrap the operator: %v", err)
	}
	s.PrincipalUserID = operatorID

	// The operator's second factor, enrolled and confirmed.
	//
	// The confirmation does not spend the TOTP step -- enrolment is not an
	// authentication -- so the code the test produces for the frozen clock is
	// still the code the route will accept.
	secret, _, err := identities.EnrollTOTP(context.Background(), tenant, operatorID, "operator")
	if err != nil {
		t.Fatalf("enrol the operator's factor: %v", err)
	}
	if err := identities.VerifyTOTPEnrollment(context.Background(), tenant, operatorID,
		totpCode(t, secret, clock)); err != nil {
		t.Fatalf("confirm the operator's factor: %v", err)
	}
	theOperatorSecret = secret

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
		// The absence panel's reader, over the same pool.
		//
		// It used NOT to be wired here, and that is why three defects on the
		// coverage route survived a green suite: with no reader the panel
		// reads nothing, so it never reaches the statement that names a
		// column that does not exist, the dead-letter read that recursed, or
		// the clock it read as a nil field.
		//
		// A dependency the tests leave nil is a dependency the tests cannot
		// speak about, and the route that uses it is a route that is only
		// right when it has nothing to say.
		Absence: api.NewAbsenceReader(pool, tenant, "coverage-panel"),
		// The dead-letter reader, over the same pool.
		//
		// It was not wired here either, and the coverage panel reads the dead
		// letters through it. With no reader the panel never reached the
		// branch that recursed for ever — so the test harness could not
		// speak about the one reader that kills the process.
		DeadLetters: api.DeadLetters{
			Inbox: &inbox.Writer{Pool: pool},
			Jobs:  &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM},
		},
		// The identity service, for the same reason: the sensitive dispositions
		// verify a factor through it.
		Identities: identities,
		// The durable analytics read the rollup through this pool, and the
		// rollup is site-scoped, so the reader binds the principal's scope onto
		// the connection.
		AnalyticsPool: pool,
		// The recording index, over the same pool, for the same reason.
		//
		// It is wired HERE rather than through an option because a recording
		// is the first thing an operator looks at and the last thing a test
		// harness leaves nil: a route with no index behind it answers "not
		// configured" for every camera, and a test suite that never sees the
		// route with an index behind it cannot tell a working route from a
		// route that is only right when it has nothing to say.
		Recordings: recording.NewService(pool),
	}
	for _, opt := range opts {
		opt(&deps)
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

// mustSignIn is gone.
//
// Login is removed, so there is no session to mint. It used to drive the login
// route and hand back the session cookie and the CSRF value, and every test
// that called it had been failing since the route was removed -- a suite that
// is red for a reason nobody wrote down is a suite that stops being read.
//
// The tests that needed a second operator now vary the CONFIGURED principal's
// session instead, which is the same fact expressed in the model the product
// actually has: two operators are two sessions, and the attention metric
// counts one bout per session.
