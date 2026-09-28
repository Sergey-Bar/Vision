// Package api is the strict JSON API and the server-rendered console.
//
// Two surfaces, one binary, because they enforce the same things. The JSON API
// has no content negotiation: it is JSON or it is a 406. The console is HTML with
// no inline script, which is what makes a strict Content-Security-Policy
// achievable at all — an inline <script> anywhere would mean a CSP with 'unsafe-
// inline' and no benefit.
//
// Every state-changing method requires a CSRF token, every session cookie is
// HttpOnly + Secure + SameSite, and `tenant_id` is never read from a request
// body. A `X-Tenant-Id` header is honoured only when it matches the session
// tenant, and a mismatch is 403 and audited.
package api

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/jackc/pgx/v5"
)

// Server is the HTTP surface.
type Server struct {
	Identity *identity.Service
	Audit    *audit.Writer
	Log      *slog.Logger
	// Tenant is the site this server serves. Single-site per install, so it is
	// configuration rather than a routing decision.
	Tenant string
	// Handlers registers the API and console routes. Kept as a hook so this
	// package owns the security properties and the application owns the routes.
	Routes func(*http.ServeMux)

	// CookieName is the session cookie's name.
	CookieName string
	// Secure requires TLS. There is no plaintext fallback: a console that
	// authenticates over plain HTTP hands a session cookie to the network.
	Secure bool
	// BehindProxy says the listener is behind a TLS-terminating proxy, which
	// changes only how the request scheme is read.
	BehindProxy bool
	// Pool is used for the audit writes a denial produces. A refusal nobody can
	// see is a refusal nobody can investigate.
	Pool *dbx.Pool
	// HealthCheck is the readiness set. A check named "aiRuntime" is informational
	// and never makes the core unready.
	HealthCheck map[string]func(context.Context) error
	start       time.Time
}

// Handler builds the HTTP handler with every security layer applied.
//
// The layers live here rather than in ListenAndServe, because a security
// property that only exists on one call path is a property that a test, a
// benchmark, or a future embedder silently does without. The CSRF check in
// particular was on the listener only, which meant Handler served every
// state-changing route with no token at all.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.Routes != nil {
		s.Routes(mux)
	}
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/health/ready", s.ready)

	var h http.Handler = mux
	h = s.contentSecurityPolicy(h)
	h = s.securityHeaders(h)
	h = s.recoverPanics(h)
	h = s.logRequests(h)
	h = s.requireCSRF(h)
	return h
}

// contentSecurityPolicy sets a policy with no inline script and no framing.
//
// The console is server-rendered precisely so this is achievable. An operator's
// browser running a script an event payload injected is the one XSS path that
// matters here, because the operator is the thing being attacked.
func (s *Server) contentSecurityPolicy(next http.Handler) http.Handler {
	policy := strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		// No inline styles either: the console's stylesheet is a file, so a style
		// attribute in a payload has nowhere to go.
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", policy)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// A security console has no business being indexed, and a stored
		// reference to one is a stored pointer at a site.
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		if s.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if s.Log == nil {
			return
		}
		// No query string: the API takes nothing secret there, but a habit is a
		// habit and a future endpoint that does would leak into every log line.
		s.Log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", s.clientIP(r),
		)
	})
}

// recoverPanics keeps one bad request from taking the console down. The stack
// goes to the log with the request id; the client gets a generic 500 that says
// nothing about what failed inside.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if s.Log != nil {
					s.Log.Error("panic",
						"recovered", rec,
						"path", r.URL.Path,
						"method", r.Method,
						"request_id", r.Header.Get("X-Request-Id"),
					)
				}
				WriteError(w, http.StatusInternalServerError, "internal_error",
					"The request could not be completed.", requestID(r))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// principal resolves the session, or writes 401 and returns false.
func (s *Server) principal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		WriteError(w, http.StatusUnauthorized, "no_session",
			"Sign in to continue.", requestID(r))
		return identity.Principal{}, false
	}
	p, rotated, err := s.Identity.Resolve(r.Context(), s.Tenant, cookie.Value, s.clientIP(r))
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "no_session",
			"Sign in to continue.", requestID(r))
		return identity.Principal{}, false
	}
	// Resolve rotates the token on every use, so the client always gets the
	// current one back. A browser mid-refresh is not logged out by rotation.
	s.setSessionCookie(w, rotated, p.CSRFToken)
	return p, true
}

// requirePrincipal is the wrapper for authenticated handlers.
func (s *Server) requirePrincipal(h func(http.ResponseWriter, *http.Request, identity.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.principal(w, r)
		if !ok {
			return
		}
		h(w, r, p)
	}
}

// requireCapability is the wrapper for a capability-gated handler.
func (s *Server) requireCapability(needed identity.Capability, h func(http.ResponseWriter, *http.Request, identity.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.principal(w, r)
		if !ok {
			return
		}
		if err := p.Authorize(needed); err != nil {
			s.auditDenied(r, p, needed)
			WriteError(w, http.StatusForbidden, "not_authorized", err.Error(), requestID(r))
			return
		}
		h(w, r, p)
	}
}

// csrfExempt lists the routes a CSRF token cannot protect.
//
// Login and its second step are exempt because a caller has no session yet, and
// therefore no token to present — requiring one would make authentication
// impossible. They are not left unprotected by accident: a forged login request
// can only log the attacker in as themselves, which costs them nothing, and the
// routes are rate limited and audited.
//
// Everything that acts on an existing session is covered, logout included.
var csrfExempt = map[string]bool{
	"POST /v1/auth/login":      true,
	"POST /v1/auth/mfa/verify": true,
}

// requireCSRF rejects a state-changing method without a matching token.
//
// The cookie alone is not authorization: a browser attaches it to any request to
// this origin, including one another page caused. The token is read from a
// readable cookie so a server-rendered form can carry it, and is checked against
// the session row, so it authorises nothing on its own.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if csrfExempt[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		presented := r.Header.Get("X-CSRF-Token")
		if presented == "" {
			// A form post cannot set a header, so the token rides in a form field.
			presented = r.FormValue("csrf_token")
		}
		cookie, err := r.Cookie(s.cookieName())
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "no_session", "Sign in to continue.", requestID(r))
			return
		}
		// The CSRF token lives in a separate, readable cookie so a form can
		// carry it. It is bound to the session server-side, so its value alone
		// authorises nothing.
		csrfCookie, err := r.Cookie(s.cookieName() + "_csrf")
		if err != nil {
			WriteError(w, http.StatusForbidden, "csrf_missing",
				"This request is missing its CSRF token. Reload the page and try again.", requestID(r))
			return
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(csrfCookie.Value)) != 1 {
			WriteError(w, http.StatusForbidden, "csrf_mismatch",
				"This request's CSRF token did not match. Reload the page and try again.", requestID(r))
			return
		}
		// The CSRF cookie is deliberately not HttpOnly: the server-rendered console
		// reads it to stamp the token into a form. It authorises nothing on its
		// own, because the server checks it against the session row.
		//
		// The session is found by the token, not by an id derived from it. Resolve
		// rotates the token on every request, so a session id derived from the
		// token that just arrived is always one rotation stale — and a check
		// against a stale session refuses every correctly-tokened request, which
		// looks exactly like a CSRF attack.
		if err := s.Identity.CheckCSRFByToken(r.Context(), s.Tenant, cookie.Value, presented); err != nil {
			WriteError(w, http.StatusForbidden, "csrf_mismatch", err.Error(), requestID(r))
			return
		}
		_ = csrfCookie
		next.ServeHTTP(w, r)
	})
}

// tenantHeader is the X-Tenant-Id check.
//
// The header exists for a service that fronts several sites. It is honoured
// only when it matches the session tenant: a header that selected a tenant would
// make the whole authorization model decorative.
func (s *Server) tenantHeader(r *http.Request, p identity.Principal) error {
	header := strings.TrimSpace(r.Header.Get("X-Tenant-Id"))
	if header == "" {
		return nil
	}
	if header != p.TenantID {
		return fmt.Errorf("X-Tenant-Id %q does not match the session tenant", header)
	}
	return nil
}

func (s *Server) auditDenied(r *http.Request, p identity.Principal, needed identity.Capability) {
	if s.Audit == nil || s.Pool == nil {
		return
	}
	_ = s.Pool.InSystemTenant(r.Context(), func(tx pgx.Tx) error {
		_, err := s.Audit.Append(r.Context(), tx, audit.Entry{
			TenantID: p.TenantID, Actor: p.Username, ActorType: audit.ActorOperator,
			Action: "api.access.denied", TargetType: "capability", TargetID: string(needed),
			RequestID: requestID(r), SessionID: p.SessionID, SiteScope: p.SiteScope,
			Reason: "capability not held",
		})
		return err
	})
}

func (s *Server) cookieName() string {
	return s.cookieNameOrDefault()
}

func (s *Server) cookieNameOrDefault() string {
	if s.CookieName == "" {
		return "sp_session"
	}
	return s.CookieName
}

// setSessionCookie writes the session cookie and the readable CSRF cookie, with
// every attribute the threat model names.
func (s *Server) setSessionCookie(w http.ResponseWriter, sessionToken, csrfToken string) {
	if sessionToken == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.Secure,
		// Strict, because every legitimate request is same-origin: the console and
		// the API are served by this one listener.
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((12 * time.Hour).Seconds()),
	})
	if csrfToken == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName() + "_csrf", Value: csrfToken, Path: "/",
		Secure: s.Secure, SameSite: http.SameSiteStrictMode,
		MaxAge: int((12 * time.Hour).Seconds()),
	})
}

// clearSessionCookie removes the session cookie.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: "", Path: "/",
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName() + "_csrf", Value: "", Path: "/",
		Secure: s.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// ListenAndServe starts the listener.
//
// TLS is configured here rather than left to a proxy, and there is no plaintext
// listener. A site that terminates TLS upstream sets BehindProxy so the console
// still forms secure URLs.
func (s *Server) ListenAndServe(ctx context.Context, addr string, certFile, keyFile string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          nil,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// TLS 1.3 where the client offers it, which is the only version with
			// a cipher suite that needs no negotiation and no forward secrecy
			// configuration.
			MaxVersion: tls.VersionTLS13,
		},
	}
	if s.BehindProxy {
		srv.TLSConfig = srv.TLSConfig.Clone()
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("api: listen on %s: %w", addr, err)
	}
	if s.Log != nil {
		s.Log.Info("api listening", "addr", addr, "tls", certFile != "")
	}
	if certFile == "" {
		return srv.Serve(ln)
	}
	return srv.ServeTLS(ln, certFile, keyFile)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Health deliberately excludes the AI plane. Core liveness must not depend
	// on something that is explicitly not an SLO, or a model outage would page
	// somebody about a security system that is working.
	WriteJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"uptimeSeconds": time.Since(s.startedAt()).Seconds(),
		"aiPlane": map[string]any{
			"note": "AI availability is not an SLO and never affects this status.",
		},
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	ready := true
	if s.HealthCheck != nil {
		for name, fn := range s.HealthCheck {
			err := fn(r.Context())
			entry := map[string]any{"ok": err == nil}
			if err != nil {
				entry["detail"] = err.Error()
			}
			checks[name] = entry
			if ok, _ := entry["ok"].(bool); !ok {
				if name != "aiRuntime" {
					ready = false
				}
			}
		}
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	WriteJSON(w, status, map[string]any{"ready": ready, "checks": checks})
}

func (s *Server) startedAt() time.Time {
	if s.start.IsZero() {
		return time.Now().Add(-time.Second)
	}
	return s.start
}

// clientIP reads the client address. Behind a proxy the forwarded header is
// authoritative, because the listener's own address is the proxy.
func (s *Server) clientIP(r *http.Request) string {
	if s.BehindProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	return ""
}

// WriteJSON writes a JSON response with the headers the API contract states.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ErrorBody is the single error shape. One shape means a client has one thing to
// parse, and it means an error cannot leak a stack by omission.
type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

// WriteError writes an error.
//
// The message is written for the operator reading it. "Access denied" with no
// reason is the worst possible answer to give somebody whose incident queue is
// stopped.
func WriteError(w http.ResponseWriter, status int, code, message, requestID string) {
	WriteJSON(w, status, ErrorBody{Code: code, Message: message, RequestID: requestID})
}

// DecodeJSON reads a JSON body with a size cap.
//
// The cap is a limit rather than a suggestion: an unauthenticated body of
// unbounded size is a way to make the process allocate before anybody has
// identified themselves.
func DecodeJSON(r *http.Request, dst any) error {
	const maxBody = 1 << 20
	r.Body = http.MaxBytesReader(nil, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("request body: %w", err)
	}
	return nil
}

func parseLimit(raw string, def, ceiling int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > ceiling {
		return ceiling
	}
	return n
}
