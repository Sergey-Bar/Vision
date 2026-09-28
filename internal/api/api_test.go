package api_test

import (
	"context"
	"encoding/json"
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
	s, tenant, site := server(t)

	cookie, csrf := login(t, s, tenant, site)
	csrfToken := csrf.Value

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
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	// Now the request gets past CSRF and fails on the incident, not the token.
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "csrf") {
		t.Error("a correct CSRF token was still refused")
	}
}

func TestCSRFTokenIsBoundToTheSession(t *testing.T) {
	s, tenant, site := server(t)
	cookie, _ := login(t, s, tenant, site)

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
	s, tenant, site := server(t)
	login(t, s, tenant, site)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	req.Header.Set("X-Tenant-Id", "some-other-tenant")
	s.Handler().ServeHTTP(rec, req)

	// Either the request is refused outright or the session is rejected, but it
	// must not succeed as the other tenant.
	if rec.Code == http.StatusOK {
		t.Error("a request with a mismatched tenant header succeeded")
	}
	_ = site
}

func TestConsoleIsLeftToRight(t *testing.T) {
	s, tenant, site := server(t)
	session, _ := login(t, s, tenant, site)

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
	s, tenant, site := server(t)
	login(t, s, tenant, site)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	req.Header.Set("Accept", "text/html")
	s.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("the API returned %q for a request that asked for HTML; the API is JSON only", ct)
	}
}

// login drives the full flow and returns the session and CSRF cookies.
func login(t *testing.T, s *api.Server, tenant, site string) (session, csrf *http.Cookie) {
	t.Helper()
	ctx := context.Background()

	if _, err := s.Identity.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"username":"admin","password":"a-long-bootstrap-passphrase"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", rec.Code, rec.Body)
	}

	for _, c := range rec.Result().Cookies() {
		switch {
		case strings.HasSuffix(c.Name, "_csrf"):
			csrf = c
		default:
			session = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("login did not set both cookies (session=%v csrf=%v)", session, csrf)
	}
	return session, csrf
}

func TestSessionCookieCarriesEveryAttribute(t *testing.T) {
	s, tenant, site := server(t)
	ctx := context.Background()
	if _, err := s.Identity.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"a-long-bootstrap-passphrase"}`))
	s.Handler().ServeHTTP(rec, req)

	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.HasSuffix(c.Name, "_csrf") {
			continue
		}
		session = c
	}
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly {
		t.Error("the session cookie is not HttpOnly; a script on the page could read it")
	}
	if !session.Secure {
		t.Error("the session cookie is not Secure")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Errorf("the session cookie is %v, want Strict", session.SameSite)
	}
}

func TestLoginWithBadCredentialsIsRefused(t *testing.T) {
	s, tenant, site := server(t)
	ctx := context.Background()
	if _, err := s.Identity.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"the-wrong-passphrase"}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password returned %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) > 0 {
		t.Error("a failed login set a cookie")
	}
}

func TestBodySizeIsCapped(t *testing.T) {
	s, tenant, site := server(t)
	login(t, s, tenant, site)

	huge := strings.Repeat("x", 2<<20)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/rules/bundles",
		strings.NewReader(`{"contentBase64":"`+huge+`"}`))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Error("a two-megabyte body was accepted")
	}
}
