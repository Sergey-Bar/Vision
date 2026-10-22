package main

// The console server's posture.
//
// The console is the surface a judge opens and the surface a browser points
// at, and it used to be the least careful surface in the repository: it read
// its tenant out of a request header, it guessed when the header was absent,
// it sent no security headers at all, and it refused nothing that came from
// another origin.
//
// These are the gates for the four of those.

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheConsoleNeverReadsATenantFromAHeader
//
// The console's code does not read X-Tenant-ID or X-Site-IDs.
//
// The console has no login, so there is no session for a header to match
// against: ANY value a caller sends is attacker-controlled. The header was
// not an authorisation, it was an instruction, and a request that said
// "X-Tenant-ID: someone-else" read someone else's records.
//
// The scan is over the SOURCE rather than over the behaviour, because the
// behaviour is "read somebody else's rows" and a test that asserts on that
// needs a second tenant in a database.
func TestTheConsoleNeverReadsATenantFromAHeader(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the console server's source: %v", err)
	}
	forbidden := []string{"X-Tenant-ID", "X-Site-IDs", "x-tenant-id", "x-site-ids"}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, f := range forbidden {
			if !strings.Contains(body, f) {
				continue
			}
			// A test may NAME the header, in order to assert that it is
			// not read. A comment may mention it. Neither is a read.
			for _, line := range strings.Split(body, "\n") {
				if !strings.Contains(line, f) {
					continue
				}
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
					continue
				}
				if strings.Contains(path, "_test.go") {
					continue
				}
				t.Errorf("%s reads %q. The console's tenant is CONFIGURED, and a header "+
					"from a caller with no session to match it against is an instruction "+
					"rather than an authorisation", path, f)
			}
		}
	}
}

// TestTheConsoleNeverGuessesItsTenant
//
// With no configured tenant, the console refuses rather than guessing.
//
// The guess was `SELECT tenant_id FROM sites LIMIT 1`, which is "whichever
// tenant happens to be first". On a multi-tenant database that is a
// cross-tenant read; on a single-tenant one it is an arbitrary one, and it
// is a guess the operator never made.
func TestTheConsoleNeverGuessesItsTenant(t *testing.T) {
	t.Setenv("SP_TENANT_ID", "")
	t.Setenv("SP_PRINCIPAL_SITES", "")
	t.Setenv("SP_SITE_ID", "")

	s := configuredScope()
	if s.tenant != "" {
		t.Fatalf("the scope picked up a tenant with nothing configured: %q", s.tenant)
	}
	_, _, err := s.resolve(nil)
	if err == nil {
		t.Fatal("the scope resolved a tenant with nothing configured. A missing tenant is " +
			"an error, not a guess")
	}
	if !strings.Contains(err.Error(), "SP_TENANT_ID") {
		t.Errorf("the refusal does not say how to fix it: %q", err.Error())
	}
}

// TestTheConsoleTakesItsTenantFromTheConfiguration
//
// The configured tenant is the one that is used.
func TestTheConsoleTakesItsTenantFromTheConfiguration(t *testing.T) {
	t.Setenv("SP_TENANT_ID", "tenant-local")
	t.Setenv("SP_PRINCIPAL_SITES", "site-a, site-b")
	t.Setenv("SP_SITE_ID", "")

	s := configuredScope()
	tenant, sites, err := s.resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tenant != "tenant-local" {
		t.Errorf("the tenant is %q, want tenant-local", tenant)
	}
	if len(sites) != 2 || sites[0] != "site-a" || sites[1] != "site-b" {
		t.Errorf("the site scope is %v, want [site-a site-b]", sites)
	}
}

// TestTheConsoleSendsTheSecurityHeaders
//
// Every response carries the headers, including the ones that answer with an
// error.
func TestTheConsoleSendsTheSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("the response carries no Content-Security-Policy")
	}
	for _, want := range []string{
		"default-src 'self'",
		"style-src 'self'",
		"script-src 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy does not say %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("the policy allows unsafe-inline: %s", csp)
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("the policy allows unsafe-eval: %s", csp)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options is %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options is %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got == "" {
		t.Error("the response carries no Referrer-Policy")
	}
}

// TestTheConsoleRefusesACrossOriginWrite
//
// A state-changing request from another origin is refused.
//
// The console is a browser application served by this server and it has no
// session, so the browser is the only thing standing between a page on
// another origin and the operator's session-free console. Without this, any
// page could make the operator's browser drive the console.
func TestTheConsoleRefusesACrossOriginWrite(t *testing.T) {
	reached := false
	h := refuseCrossOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	// A write from another origin.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a cross-origin POST answered %d, want 403", rec.Code)
	}
	if reached {
		t.Error("the cross-origin POST reached the handler")
	}

	// A write from this origin.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	req.Host = "localhost:8080"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("a same-origin POST answered %d, want 200", rec.Code)
	}

	// A read from another origin is not refused: a page may read, and the
	// answer is the same either way.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("a cross-origin GET answered %d, want 200", rec.Code)
	}
}

// TestTheAccessLogCarriesTheTenantAndTheRequestID
//
// The log line carries the tenant, the site and the request id.
//
// A console log without a tenant is a log line nobody can audit: the whole
// point of the record is that a reader can tie an action to the tenant it
// happened in.
func TestTheAccessLogCarriesTheTenantAndTheRequestID(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	setPrincipal("tenant-local")
	defer setPrincipal("")

	h := withRequestID(accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/cameras?siteId=site-a", nil)
	h.ServeHTTP(rec, req)

	line := buf.String()
	for _, want := range []string{`tenant="tenant-local"`, `site="site-a"`, "request_id=", "418"} {
		if !strings.Contains(line, want) {
			t.Errorf("the access log does not carry %q: %s", want, line)
		}
	}
	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Error("the response carries no request id, so a reader cannot find the log line")
	}
}

// TestTheRequestIDIsUnique
//
// Two requests get two ids.
func TestTheRequestIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)
		id := rec.Header().Get("X-Request-Id")
		if id == "" {
			t.Fatal("a request got no id")
		}
		if seen[id] {
			t.Fatalf("the request id %q was handed out twice", id)
		}
		seen[id] = true
	}
}

// The console's own pages and the console's own API must answer with the
// security headers, and the streaming route must still stream.
//
// The MJPEG stream is a ResponseWriter wrapper away from not streaming at
// all, and a console whose live view buffers is a console whose live view
// hangs.
func TestTheStreamingRouteStillStreams(t *testing.T) {
	rec := httptest.NewRecorder()
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("the response writer cannot flush, so the live view would buffer")
		}
		f.Flush()
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/vms/frame", nil))
	if _, err := io.ReadAll(rec.Body); err != nil {
		t.Fatalf("read the body: %v", err)
	}
}

// The middleware must not break the WebSocket upgrade.
//
// A wrapper that does not implement http.Hijacker makes every upgrade fail
// with "response does not implement http.Hijacker", the browser reports a
// handshake error of 500, and the live view never starts. The rehearsal
// found it: the access log was the wrapper, and the log is worth more than
// the live view is convenient.
func TestTheMiddlewareDoesNotBreakTheWebSocketUpgrade(t *testing.T) {
	rec := httptest.NewRecorder()
	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("the response writer cannot be hijacked, so the WebSocket upgrade " +
				"fails with a handshake error and the live view never starts")
		}
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/live", nil))
}

// testScope is the scope the console's tests run under.
//
// It is a CONFIGURED scope, which is the only kind there is now: the tests
// that used to send an X-Tenant-ID header were testing a path that no
// longer exists.
func testScope() scope {
	return scope{tenant: "tenant-local", sites: []string{"site-1"}, sitesDeclared: true}
}
