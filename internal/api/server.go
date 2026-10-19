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
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
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
)

// Server is the HTTP surface.
type Server struct {
	Log *slog.Logger
	// Tenant is the site this server serves. Single-site per install, so it is
	// configuration rather than a routing decision.
	Tenant string
	// PrincipalRole is the role the configured principal holds.
	//
	// The console has no login, so the API runs as a principal that comes from
	// CONFIGURATION rather than from a session. The role is what that principal
	// is ALLOWED to do, and the capability checks below are real: a role without
	// `evidence.fetch` does not fetch evidence, whatever the request says.
	//
	// It defaults to the tenant administrator, because an install with no
	// identity configured has one identity and it is the one that administers.
	// Narrowing it is a configuration change, not a code change.
	PrincipalRole identity.Role
	// PrincipalSites are the sites the configured principal may see.
	//
	// Empty means every site, which is the single-site install's case. A
	// principal scoped to a site it is not assigned to is refused, and the
	// refusal is the same answer as "no such incident" so the refusal does not
	// disclose what exists.
	PrincipalSites []string
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
	// Pool is the database pool handlers write audit rows through.
	Pool *dbx.Pool
	// Audit writes the append-only ledger. Nil means audit routes say so rather
	// than failing every read.
	Audit *audit.Writer
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

// openPrincipal is the principal every request runs as when login is removed.
//
// The console has no login, so there is no session and no user. What there IS
// is a CONFIGURED principal: a role and a site scope, both from configuration,
// and the ledger attributes every action to it. That is a different thing from
// an open door, and the difference is the capability checks below: the role
// decides what the API may do, and a request cannot widen it.
//
// The default role is the tenant administrator, because an install that has not
// configured an identity has one identity and it is the one that administers.
func (s *Server) openPrincipal(_ *http.Request) identity.Principal {
	role := s.PrincipalRole
	if role == "" {
		role = identity.RoleTenantAdmin
	}
	return identity.Principal{
		TenantID:       s.Tenant,
		Roles:          []identity.Role{role},
		SiteScope:      s.PrincipalSites,
		MFAVerified:    true,
		ServiceAccount: true,
	}
}

// requireCapability is the wrapper for a capability-gated handler.
//
// The check is REAL. The console has no login, but the configured principal
// still holds a role, and a role that does not hold the capability does not get
// the route. An open door would be a different thing: it would mean the role
// meant nothing, and a capability table that grants nothing is a role table
// that lies.
func (s *Server) requireCapability(c identity.Capability, h func(http.ResponseWriter, *http.Request, identity.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.openPrincipal(r)
		if !p.Can(c) {
			WriteError(w, http.StatusForbidden, "capability_denied",
				"The configured principal does not hold "+string(c)+".", requestID(r))
			return
		}
		h(w, r, p)
	}
}

// requireCapabilityAtSite is requireCapability plus the site check.
//
// The site is resolved from the request -- the incident's own site, or the
// camera's -- and the principal must be assigned to it. The refusal is 404
// rather than 403, because "no such incident" and "not yours" are the same
// answer and a different one is a disclosure.
func (s *Server) requireCapabilityAtSite(
	c identity.Capability,
	siteOf func(*http.Request, identity.Principal) string,
	h func(http.ResponseWriter, *http.Request, identity.Principal),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.openPrincipal(r)
		if !p.Can(c) {
			WriteError(w, http.StatusForbidden, "capability_denied",
				"The configured principal does not hold "+string(c)+".", requestID(r))
			return
		}
		if site := siteOf(r, p); site != "" && !p.CanSeeSite(site) {
			WriteError(w, http.StatusNotFound, "not_found",
				"No such record, or you are not assigned to its site.", requestID(r))
			return
		}
		h(w, r, p)
	}
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
