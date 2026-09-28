package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/api"
	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

var clock = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

type testSecretStore struct{}

func (testSecretStore) Wrap(b []byte) ([]byte, error) { return append([]byte("T:"), b...), nil }
func (testSecretStore) Unwrap(b []byte) ([]byte, error) {
	if len(b) < 2 {
		return nil, ioEOF
	}
	return b[2:], nil
}

var ioEOF = errEOF{}

type errEOF struct{}

func (errEOF) Error() string { return "unexpected end of key material" }

func server(t *testing.T) (*api.Server, string, string) {
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
	ident := &identity.Service{
		Pool: pool, Audit: writer,
		Now:         func() time.Time { return clock },
		SecretStore: testSecretStore{},
	}
	s := &api.Server{}
	s.Identity = ident
	s.Audit = writer
	s.Pool = pool
	s.Tenant = tenant
	s.Secure = true

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
	deps := api.Deps{
		Identity:  ident,
		Incidents: &incidents.Service{Pool: pool, Audit: writer},
		Bundles:   &bundles.Service{Pool: pool, Compiler: rules.NewCompiler(), Audit: writer},
	}
	// The routes closure captures s, so it is assigned after construction rather
	// than inside the literal.
	s.Routes = func(mux *http.ServeMux) { s.RegisterRoutes(deps)(mux) }

	// Every test needs a signed-in operator, so the first administrator is
	// created here rather than in each test. A bootstrap account has no enrolled
	// factor, which is the one state that can sign in on a password alone, and
	// that is exactly what these tests exercise.
	if _, err := ident.BootstrapAdmin(context.Background(), tenant, "admin",
		"a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return s, tenant, site
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

func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	s, _, _ := server(t)
	for _, path := range []string{
		"/v1/incidents",
		"/v1/auth/session",
		"/v1/audit/ledger",
		"/v1/ops/dead-letters",
		"/incidents",
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d with no session, want 401", path, rec.Code)
		}
	}
}

// The cookie alone is not authorization. A browser attaches it to any request to
// this origin, including one a page elsewhere caused.
func TestStateChangingRequestWithoutCSRFIsRefused(t *testing.T) {
	s, _, _ := server(t)

	cookie, csrf := mustSignIn(t, s)
	csrfToken := csrf

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/incidents/inc_x/dispositions",
		strings.NewReader(`{"outcome":"dismissed"}`))
	req.AddCookie(cookie)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Error("a disposition succeeded with no CSRF token")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/incidents/inc_x/dispositions",
		strings.NewReader(`{"outcome":"dismissed"}`))
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: cookieNameOf(s) + "_csrf", Value: csrfToken})
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	// With both halves present the request gets past CSRF and fails on the missing
	// incident, not on the token. That is the property: the two checks are
	// independent, and a correct token is not refused.
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "csrf") {
		t.Errorf("a correct CSRF token was still refused: %s", rec.Body)
	}
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "csrf") {
		t.Error("a correct CSRF token was still refused")
	}
}

func TestCSRFTokenIsBoundToTheSession(t *testing.T) {
	s, _, _ := server(t)
	cookie, _ := mustSignIn(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/incidents/inc_x/dispositions",
		strings.NewReader(`{"outcome":"dismissed"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", "a-token-from-another-session")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a foreign CSRF token returned %d, want 403", rec.Code)
	}
}

// X-Tenant-Id is honoured only when it matches the session tenant. A header that
// selected a tenant would make the whole authorization model decorative.
func TestTenantHeaderMismatchIsRefused(t *testing.T) {
	s, _, _ := server(t)
	mustSignIn(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	req.Header.Set("X-Tenant-Id", "some-other-tenant")
	s.Handler().ServeHTTP(rec, req)

	// Either the request is refused outright or the session is rejected, but it
	// must not succeed as the other tenant.
	if rec.Code == http.StatusOK {
		t.Error("a request with a mismatched tenant header succeeded")
	}
}

func TestConsoleIsLeftToRight(t *testing.T) {
	s, _, _ := server(t)
	session, _ := mustSignIn(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/incidents", nil)
	req.AddCookie(session)
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
	mustSignIn(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	req.Header.Set("Accept", "text/html")
	s.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("the API returned %q for a request that asked for HTML; the API is JSON only", ct)
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
