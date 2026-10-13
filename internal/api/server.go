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
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/observability"
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
	// TrustedProxies are the proxy networks whose X-Forwarded-For this server
	// will believe. It is empty unless the install is behind a proxy, and a
	// non-empty list with BehindProxy=false is inert: without a proxy in front
	// there is nothing to forward anything.
	//
	// It is the same list the rate limiter keys on, so it is not a
	// convenience: a client address the caller can choose is a limiter key the
	// caller can rotate.
	TrustedProxies []*net.IPNet
	// Pool is used for the audit writes a denial produces. A refusal nobody can
	// see is a refusal nobody can investigate.
	Pool *dbx.Pool
	// HealthCheck is the readiness set. A check named "aiRuntime" is informational
	// and never makes the core unready.
	HealthCheck map[string]func(context.Context) error
	// Metrics is the observability registry, read for the SLO verdicts.
	//
	// Nil means this install has not wired one and the endpoint says so rather
	// than reporting every objective as met — "zero breached" from a process that
	// never evaluated anything is the one answer here that would be a lie.
	Metrics *observability.Registry

	mu sync.Mutex
	// httpServer is the running listener, retained so Shutdown can reach it.
	// ListenAndServe used ctx only as BaseContext and never called Shutdown, so
	// a signal stopped nothing: in-flight requests ran to completion against a
	// pool the process was about to close.
	httpServer *http.Server
	start      time.Time
	now        func() time.Time
}

// SetStartTime records when the process began serving.
//
// /v1/health reports uptime from it. Without this the field is derived from a
// zero time and every install reports an uptime of about one second for ever,
// which is the sort of wrong number that stops being noticed.
func (s *Server) SetStartTime(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = t
}

// StartedAt reports the recorded start time.
func (s *Server) StartedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.start
}

// SetClock replaces the server's time source. It exists so uptime is testable
// without a sleep; production never calls it and gets time.Now.
func (s *Server) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Shutdown stops accepting connections and waits for in-flight requests, bounded
// by ctx.
//
// The order matters and is the daemon's to enforce, not this method's: stop
// accepting, then drain the loops, then drain the outbox, then close the pool.
// This is the first step.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpServer
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("api: shutdown: %w", err)
	}
	return nil
}

// Handler builds the HTTP handler with every security layer applied.
//
// The layers live here rather than in ListenAndServe, because a security
// property that only exists on one call path is a property that a test, a
// benchmark, or a future embedder silently does without. The CSRF check in
// particular was on the listener only, which meant Handler served every
// state-changing route with no token at all.
//
// The wrapping order is load-bearing. Middleware runs outermost-first, so
// requireCSRF is applied *after* the layers that decorate a response: a CSRF
// rejection is an error response like any other and must carry the same
// security headers and the same X-Request-Id as a success, and must appear in
// the access log. With requireCSRF outermost it was none of those - four
// WriteError calls emitting bare 401/403 with no CSP, no nosniff, no
// frame-deny, no HSTS, no request id, and nothing in the log. A rejected
// forgery was the one response an attacker could read the shape of for free.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.Routes != nil {
		s.Routes(mux)
	}
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/health/ready", s.ready)

	var h http.Handler = mux
	h = s.requireCSRF(h)
	h = s.contentSecurityPolicy(h)
	h = s.securityHeaders(h)
	h = s.recoverPanics(h)
	h = s.logRequests(h)
	h = s.assignRequestID(h)
	return h
}

// assignRequestID resolves the correlation id once, at the outermost layer, and
// makes it available to everything inside.
//
// An inbound X-Request-Id is honoured so a proxy's id and ours are the same id;
// otherwise one is generated. Either way exactly one id exists per request, and
// it is echoed to the client, because an operator reading an audit row and a log
// line has to be able to join them.
func (s *Server) assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if id == "" || len(id) > 128 {
			// An unbounded or absurd inbound id is echoed straight into every log
			// line and every audit row, so it is replaced rather than trusted.
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

type requestIDKey struct{}

// RequestIDFromContext returns the correlation id assigned to this request, or
// the inbound header when the context was never populated - which is the case
// for a handler mounted outside the server's own middleware stack.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok && v != "" {
		return v
	}
	return ""
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

// logRequests emits one line per request.
//
// AGENTS.md requires tenant_id, site_id and request_id on every line. They were
// absent, which makes the log useless for exactly the question it exists to
// answer: whose request was refused, and what was the correlation id on the
// audit row it produced.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.clock()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if s.Log == nil {
			return
		}
		reqID := RequestIDFromContext(r.Context())
		// No query string: the API takes nothing secret there, but a habit is a
		// habit and a future endpoint that does would leak into every log line.
		s.Log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", s.clock().Sub(start).Milliseconds(),
			"remote", s.clientIP(r),
			"tenant_id", s.requestTenant(r),
			"site_id", r.Header.Get("X-Site-Id"),
			"request_id", reqID,
		)
	})
}

// requestTenant is the tenant this request is running as.
//
// The server is single-tenant, so the configured tenant is the honest answer
// and the header is only reported. A header is never *promoted* to the tenant:
// requireTenant refuses a mismatch, and a log line that echoed an unvalidated
// header would be a line that can be made to say anything.
func (s *Server) requestTenant(r *http.Request) string {
	if s.Tenant != "" {
		return s.Tenant
	}
	return r.Header.Get("X-Tenant-Id")
}

// newRequestID is the correlation id for a request that arrived without one.
// Cryptographic randomness, hex, no dashes: it goes in a log line and in an
// audit row and is never parsed back.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unidentified"
	}
	return hex.EncodeToString(b[:])
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
						"tenant_id", s.requestTenant(r),
						"request_id", RequestIDFromContext(r.Context()),
					)
				}
				WriteError(w, http.StatusInternalServerError, "internal_error",
					"The request could not be completed.", RequestIDFromContext(r.Context()))
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

// requireCapabilityAtSite is requireCapability plus the site check, in one call.
//
// # Why the site check belongs HERE rather than in each handler
//
// `p.CanSeeSite` was called by hand, inside each handler that knew a site, after
// that handler had fetched the thing the site belonged to. Two consequences, and
// the second is the one that mattered:
//
//   - It was a SECOND thing to remember. A handler added tomorrow that took a site
//     from the path or the query would be written with `requireCapability`, because
//     that is what every sibling looks like, and it would hand another site's rows
//     to a principal not assigned to them. Nothing in the router could notice: the
//     capability check passed. The RLS canary connects as the PRODUCTION role,
//     which is assigned per site and so does catch this -- but only for the one
//     route the canary names, and only once somebody writes that row.
//   - It ran AFTER the handler's own work, so a caller who is not assigned could
//     make the server read a row and only then learn no. Migration 21's RESTRICTIVE
//     `site_isolation` policy is the database side of this rule and is real; this
//     is the application edge of the same rule, and it belongs beside the capability
//     check rather than scattered through the handlers that remembered.
//
// # What `resolve` is, and why failing to resolve is a refusal
//
// `resolve` maps the request, for the principal it will run as, to the site the
// addressed resource belongs to. It returns an empty string when the site cannot be
// established: an unknown id, a failed lookup, a request that names no site.
//
// An unresolvable site is a REFUSAL, not a pass. The alternative is to fall through
// to `Authorize(cap)` alone, which is exactly the behaviour this function exists to
// remove -- a route whose site cannot be determined becomes a route with no site
// check at all, and it fails silently.
//
// The answer it gives is a 404 carrying the SAME text as the existing per-handler
// refusal, rather than a 403 naming the site. "That incident does not exist" and
// "that incident is not yours" must not be distinguishable, or the 403 is a
// confirmation that the row exists.
//
// `GET /v1/incidents` is the one route that legitimately means "every site I may
// see", so it stays on `requireCapability` and filters per row. That is the correct
// answer for a scope-wide read, not an exemption from this one.
func (s *Server) requireCapabilityAtSite(
	needed identity.Capability,
	resolve func(*http.Request, identity.Principal) string,
	h func(http.ResponseWriter, *http.Request, identity.Principal),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.principal(w, r)
		if !ok {
			return
		}
		if err := s.authorizeAtSite(p, needed, resolve(r, p)); err != nil {
			s.auditDenied(r, p, needed)
			WriteError(w, http.StatusNotFound, "not_found", err.Error(), requestID(r))
			return
		}
		h(w, r, p)
	}
}

// errNotYours is the answer for both "no such row" and "not your site", and it is
// one constant rather than two strings written out twice, because the two must stay
// identical: a caller who can tell them apart has an oracle for which incident ids
// exist at another site.
var errNotYours = errors.New("no such incident, or you are not assigned to its site")

// authorizeAtSite is the check itself, so a handler that already knows its site can
// ask the same question the router asks.
//
// It is here rather than inline in the wrapper because the two must never be able to
// disagree. A handler that re-implemented the check could return a different answer
// for the same request, and the one that is weaker would be the one that ships.
func (s *Server) authorizeAtSite(p identity.Principal, needed identity.Capability, site string) error {
	if strings.TrimSpace(site) == "" {
		return errNotYours
	}
	if err := p.AuthorizeSite(needed, site); err != nil {
		return errNotYours
	}
	return nil
}

// siteOfIncident is `resolve` for every route addressed by incident id.
//
// It is the one lookup all of them were doing anyway, hoisted into the router so the
// authorisation decision happens before the handler rather than inside it. The
// handler still re-reads the incident; that read is now redundant, and removing it
// is a follow-up rather than something to do in the same commit as the gate that
// makes it safe to remove.
func siteOfIncident(d Deps) func(*http.Request, identity.Principal) string {
	return func(r *http.Request, p identity.Principal) string {
		detail, err := d.Incidents.Get(r.Context(), p.TenantID, r.PathValue("id"))
		if err != nil {
			return ""
		}
		return detail.SiteID
	}
}

// csrfExempt lists the routes a CSRF token cannot protect.
//
// Login and its second step are exempt because a caller has no session yet, and
// therefore no token to present - requiring one would make authentication
// impossible. A forged login request can only log the attacker in as themselves,
// which costs them nothing.
//
// What protects these two routes is the per-account exponential lockout on the
// password step and the per-challenge attempt counter on the second factor, both
// in internal/identity. Per-source rate limiting is a separate control, keyed on
// the client address, and is documented separately: this comment used to claim
// the routes were "rate limited" when no source-keyed limiter existed anywhere in
// the tree, and the only thing bounding guessing was the six digits in a TOTP
// code.
//
// Everything that acts on an existing session is covered, logout included --
// `POST /logout` included, which is the console's form-encoded sign-out.
var csrfExempt = map[string]bool{
	"POST /v1/auth/login":      true,
	"POST /v1/auth/mfa/verify": true,
	// The console's sign-in page, for the same reason as `POST /v1/auth/login` and
	// not as a concession to having a form: there is no session yet, so there is no
	// token to present, and requiring one would make authentication impossible. What
	// bounds guessing is the per-account exponential lockout in internal/identity,
	// which both front doors go through -- which is why `passwordStep` is shared
	// rather than written twice.
	"POST /login": true,
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
		// token that just arrived is always one rotation stale - and a check
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

	s.mu.Lock()
	if s.start.IsZero() {
		s.start = time.Now()
	}
	s.httpServer = srv
	s.mu.Unlock()

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
	//
	// The same reasoning keeps a breached objective from changing the status. A
	// backlog above the hard limit is a real problem and the process is still
	// running; a readiness probe that turns the platform unready because it is
	// behind moves the failure to every replica at once and removes the capacity
	// that would have drained the backlog. So the objectives are reported, not
	// enforced, and what is reported is the *names* only.
	body := map[string]any{
		"status":        "ok",
		"uptimeSeconds": s.clock().Sub(s.startedAt()).Seconds(),
		"aiPlane": map[string]any{
			"note": "AI availability is not an SLO and never affects this status.",
		},
	}

	slo := map[string]any{
		"note": "which SLO objectives are not being met. Names only: this endpoint is " +
			"unauthenticated, and the reading and the remedy are in the log.",
	}
	if s.Metrics == nil {
		slo["evaluated"] = 0
		slo["status"] = sloNotEvaluated
	} else {
		state := s.Metrics.SLOs()
		slo["evaluated"] = state.Evaluated
		switch {
		case state.Evaluated == 0:
			slo["status"] = sloNotEvaluated
		case len(state.Breached) == 0:
			slo["status"] = "met"
		default:
			slo["status"] = "breached"
		}
		slo["breached"] = state.Breached
	}
	body["slo"] = slo

	WriteJSON(w, http.StatusOK, body)
}

// sloNotEvaluated is the one status this endpoint must not get wrong. "met" from
// a process that has measured nothing is the only response here that would be a
// lie, and it is the one an operator reads most often — on a site that has just
// been installed, and every few seconds after that.
const sloNotEvaluated = "not evaluated"

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
	now := s.clock()
	s.mu.Lock()
	start := s.start
	s.mu.Unlock()
	if start.IsZero() {
		return now.Add(-time.Second)
	}
	return start
}

// clock is the Server's time source, so uptime is testable without a sleep.
func (s *Server) clock() time.Time {
	s.mu.Lock()
	now := s.now
	s.mu.Unlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

// clientIP reads the client address.
//
// The rule is two-branch and there is no fallback between them:
//
//   - The direct peer (r.RemoteAddr) is inside a trusted proxy CIDR: walk
//     X-Forwarded-For from the right and take the first entry that is *not* a
//     trusted proxy. That is the last hop that was not somebody we already
//     trust to have appended honestly.
//   - The direct peer is not trusted: X-Forwarded-For is ignored outright and
//     the peer address is the client address.
//
// Why rightmost-untrusted and not leftmost. X-Forwarded-For is an
// append-and-forward list, so the leftmost entry is the one the *original
// client* (or whoever forged it) wrote, and every proxy after it appends to
// the right. Reading leftmost means the value is asserted by the party we are
// trying to constrain. Reading rightmost-untrusted means we trust only the
// suffixes appended by hops we have explicitly named, and stop at the first
// entry we did not - which is exactly the boundary between what the trusted
// chain can vouch for and what the client chose.
//
// The second branch is what makes the first one safe: without it, any client
// that can reach the listener directly, bypassing the proxy, would have its
// own header believed. It is also why TrustedProxies must be a list of real
// proxy addresses and not a boolean: "behind a proxy" says who is in front of
// us, not who may speak for the client.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)

	if s.BehindProxy && peer != nil && s.isTrustedProxy(peer) {
		if client := s.rightmostUntrusted(r.Header.Get("X-Forwarded-For")); client != "" {
			return client
		}
	}
	return host
}

// isTrustedProxy reports whether ip is inside one of the configured proxy
// networks. An unparsable address is not trusted, which is the safe direction:
// the only way to be believed is to be named.
func (s *Server) isTrustedProxy(ip net.IP) bool {
	for _, n := range s.TrustedProxies {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// rightmostUntrusted returns the last X-Forwarded-For entry that is not a
// trusted proxy.
//
// If every entry is trusted - a header that is entirely made of our own proxy
// addresses, which is what a client sends when it wants to be unidentifiable
// and has to guess addresses - there is no untrusted entry and the answer is
// empty, which the caller treats as "use the peer". Guessing there would put
// an attacker-chosen value into an audit row.
func (s *Server) rightmostUntrusted(fwd string) string {
	if strings.TrimSpace(fwd) == "" {
		return ""
	}
	parts := strings.Split(fwd, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(parts[i])
		if entry == "" {
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			// Not an address at all. Keep walking left: a malformed entry tells
			// us nothing, and the entries to its right are still the ones a
			// trusted proxy wrote.
			continue
		}
		if !s.isTrustedProxy(ip) {
			return entry
		}
	}
	return ""
}

// requestID is the correlation id for a request. The context wins, because
// assignRequestID has already normalised the header; the header is the fallback
// for a handler mounted without that middleware.
func requestID(r *http.Request) string {
	if id := RequestIDFromContext(r.Context()); id != "" {
		return id
	}
	return strings.TrimSpace(r.Header.Get("X-Request-Id"))
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
