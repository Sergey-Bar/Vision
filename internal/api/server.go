// Package api is the strict JSON API and the server-rendered console.
//
// Two surfaces, one binary, because they enforce the same things. The JSON API
// has no content negotiation: it is JSON or it is a 406. The console is HTML with
// no inline script, which is what makes a strict Content-Security-Policy
// achievable at all — an inline <script> anywhere would mean a CSP with 'unsafe-
// inline' and no benefit.
//
// # There is no login, and this package does not pretend there is
//
// The console has no sign-in route, no session, no cookie and no token. Every
// request runs as a CONFIGURED principal: a role and a site scope, both from
// configuration. The capability checks are real, and they are the only
// authorisation this package performs: a role that does not hold
// `evidence.fetch` does not fetch evidence, whatever the request says.
//
// `tenant_id` is never read from a request body, and it is never read from a
// request header either. The tenant is configuration, and `requestTenant` says
// so on every log line.
//
// The operator's identity is established at the network boundary, by a trusted
// reverse proxy in front of the console, and not by this package. That posture
// is D-16 in `docs/decisions.md`; until it is signed, the honest statement is
// the one above, and `docs/threat-model.md` A3 records what is NOT claimed.
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
	// Loops reports the pipeline's loop liveness, for the health endpoint.
	//
	// It is a function and not a service because the health endpoint is
	// registered on the mux directly and has no Deps, and because the state
	// lives in the pipeline rather than here.
	//
	// Nil means the health endpoint cannot say, and it says so by leaving the
	// member out rather than by reporting an empty set: "no loops" and "nobody
	// told me about the loops" are different answers.
	Loops func() []LoopLiveness
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
	// It defaults to the tenant administrator, because an install that has not
	// configured an identity has one identity and it is the one that administers.
	// Narrowing it is a configuration change, not a code change.
	PrincipalRole identity.Role
	// TrustProxyAuthHeader names the header a trusted reverse proxy sets with
	// the OPERATOR'S identity.
	//
	// It is empty on a single-operator install, and the console then runs as
	// the configured principal, which is the behaviour this product shipped
	// with. It is set on an install that fronts the console with a proxy that
	// terminates TLS and authenticates the operator.
	//
	// The header carries the IDENTITY and not the AUTHORITY: the role and the
	// site scope stay configured, so a proxy that asserts an operator's name
	// cannot grant that operator more than the product's capability set
	// allows. A proxy that could grant capabilities would be a second
	// authorisation system, and the product would have two.
	//
	// The header is only believed when the request arrives from a TRUSTED
	// PROXY, because a header is forgeable by anybody who can reach the port.
	// The proxy's obligations -- strip the header from client requests, bind
	// the console to the proxy's socket -- are in `docs/decisions.md` D-16 and
	// in `deploy/`.
	TrustProxyAuthHeader string
	// PrincipalSites are the sites the configured principal may see.
	//
	// Empty means the principal sees NO site, unless PrincipalSite names one:
	// a principal with an empty scope is a misconfigured assignment, and a
	// misconfigured assignment denies rather than opens. A principal scoped to
	// a site it is not assigned to is refused, and the refusal is the same
	// answer as "no such incident" so the refusal does not disclose what
	// exists.
	PrincipalSites []string
	// PrincipalSite is the install's own site, used as the configured
	// principal's scope when PrincipalSites is empty.
	//
	// A single-site install has exactly one site, and an install that NAMES
	// its site and then refuses to show that site to its own principal is a
	// configuration that reads as "nothing to see" on every screen: the site
	// view 404s, the incident detail 404s, and the clip content 404s, while
	// the incident LIST -- which reads through a different path -- keeps
	// answering. The list and the detail disagreeing is the symptom, and it
	// is a symptom of the scope never having been assigned.
	//
	// It is configuration, not a request: nothing the caller sends can widen
	// it, and an install that does not name a site still has no scope.
	PrincipalSite string
	// PrincipalName is the actor every audited action is attributed to.
	//
	// The ledger's `actor` column cannot be empty and cannot be a constant
	// nobody chose: an audit row that says the actor was "" cannot be joined
	// to anything, and an audit row that says the actor was "console" on
	// every install in every deployment is a row that says nothing about
	// WHO. The default is the console, because that is what the configured
	// principal is: the install's own operator surface.
	PrincipalName string
	// PrincipalUserID is the identifier the decision records name.
	//
	// A disposition's `actor_id` is NOT NULL, and the incidents service
	// refuses a disposition with no actor. The configured principal has no
	// row in `users` -- there is no login, so there is no user to have one --
	// so the identifier is the configured principal's own name.
	PrincipalUserID string
	// PrincipalSession is the session the configured principal acts in.
	//
	// It is the key the attention metric deduplicates on: one
	// `incident.opened` row per incident per session, so a refresh is not a
	// new bout of attention. With a configured principal there is no login
	// and therefore no session cookie, and an EMPTY session id would make
	// every read a new bout -- a refresh counter, which rewards an operator
	// for pressing F5 and makes attention_minutes a measure of curiosity
	// rather than of attention.
	//
	// Two principals that act in different sessions are two operators as far
	// as the metric is concerned, which is what the metric has always meant.
	PrincipalSession string
	// Handlers registers the API and console routes. Kept as a hook so this
	// package owns the security properties and the application owns the routes.
	Routes func(*http.ServeMux)

	// Secure requires TLS. There is no plaintext fallback, because a console
	// that talks to a browser over plain HTTP hands its operator's traffic to
	// the network.
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
	// clockFn is the server's INJECTABLE time source. Nil means the wall
	// clock, and it is nil in every production install, because the daemon
	// never calls SetClock.
	//
	// It is named `clockFn` and not `now` because the accessor is a METHOD
	// called `now()`, and a field and a method with one name is a call site
	// that compiles and panics: the coverage panel reached past the accessor
	// and called `s.now()` on a nil field, so the live product returned 500
	// for every request the panel had something to report — which is to say,
	// the panel crashed exactly when it found something.
	clockFn func() time.Time
}

// EnforcedSecuritySchemes names the OpenAPI security schemes this server
// enforces, spelled the way the published document spells them.
//
// There is one, and what it stands for is a statement about AUTHORIZATION
// rather than about a credential this server checks: there is no login, so
// there is no cookie and no token to present, and every route on this surface
// runs behind a capability check against the CONFIGURED principal. An
// operation that names no scheme is therefore an operation with no check on
// it, and the two health routes are the only ones.
//
// The list lives here, next to the code that does the enforcing, because a
// published security scheme that nothing enforces is a claim an auditor reads
// as a protection. The gate in openapi_contract_test.go compares the document
// with this list in both directions: a scheme the document declares and this
// list does not name is a protection nobody provides, and a scheme this list
// names and the document does not declare is a protection the document hides.
//
// It is a list of one, and it is meant to stay a short list. A scheme that
// needs a credential of its own is a scheme somebody has to spend, and the
// product has nothing to spend.
var EnforcedSecuritySchemes = []string{"serviceBearer"}

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
	s.clockFn = now
}

// now is the server's time source, and it CANNOT be nil.
//
// It is a method and not a field because a field can be nil and a nil clock
// called on a live route is a panic. The coverage panel reached past the
// accessor and called the field directly, and the daemon — which never
// installs a clock — answered 500 for every request the panel had something
// to report. The panel crashed exactly when it found something, which is the
// one moment it exists for.
//
// Every caller goes through here. The injectable source is `clockFn`, and
// `SetClock` is the only way to set it.
func (s *Server) now() time.Time {
	s.mu.Lock()
	fn := s.clockFn
	s.mu.Unlock()
	if fn == nil {
		return time.Now().UTC()
	}
	return fn()
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
// benchmark, or a future embedder silently does without.
//
// The wrapping order is load-bearing. Middleware runs outermost-first, so
// every layer that can refuse a request is applied *after* the layers that
// decorate a response: a refusal is an error response like any other and must
// carry the same security headers and the same X-Request-Id as a success, and
// must appear in the access log.
//
// There is no CSRF layer and no tenant-matching layer here, because this
// product has neither: there is no session, no cookie and no token to protect,
// and the tenant is configuration rather than something a request carries.
//
// The layer that IS here for the identity is `requireProxyIdentity`, and it is
// a refusal rather than a decoration: in proxy mode a request that did not
// come from the trusted proxy never reaches a handler.
//
// The layers that hold without a session are the ones that are here: the CSP,
// the security headers, the panic recovery, the access log and the request id.
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
	// The proxy-identity refusal is INSIDE the layers that decorate a response,
	// so a refusal carries the same security headers and the same request id as
	// a success, and appears in the access log.
	h = s.requireProxyIdentity(h)
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
		holder := &siteHolder{}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), siteHolderKey{}, holder)))
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
			"site_id", s.requestSite(r, holder),
			"request_id", reqID,
		)
	})
}

// siteHolder is where a route records the site it resolved.
//
// A pointer, because a context is immutable downward and the access log reads
// upward: the line is written after the handler returns, and the site the
// request actually touched is something only the route knows.
//
// It holds a string rather than being one, because "the route resolved no site"
// and "the route resolved the empty site" are the same answer. Neither is a
// site, and a log line that says which of the two it is would be a log line
// about the absence of a log line.
type siteHolder struct{ site string }

type siteHolderKey struct{}

// recordSite notes the site a request was about, for the access log.
//
// It is called by the route wrappers AFTER the capability check has accepted
// the site, so the value in the log is a site this principal was allowed to
// reach rather than one a caller named.
func recordSite(r *http.Request, site string) {
	if holder, ok := r.Context().Value(siteHolderKey{}).(*siteHolder); ok {
		holder.site = site
	}
}

// requestSite is the site this request is scoped to.
//
// It is NOT a request header. The line used to carry the raw `X-Site-Id` header
// beside the VALIDATED tenant, and a reader had no way to tell which of the
// two fields to believe: one was configuration the server checked and the other
// was a string a caller sent. A log line that can be made to say anything is
// not a log line.
//
// The value is the site the request actually touched, when a route resolved one
// and the capability check accepted it, and otherwise the principal's own scope
// when that scope is exactly one site. A principal scoped to several sites has
// no single site, and an empty field is the honest answer for "not scoped to
// one site" rather than a guess about which one.
//
// It is bounded, because it goes into a log line and a log line that can be
// made arbitrarily long is a log line that can cost a disk.
func (s *Server) requestSite(r *http.Request, holder *siteHolder) string {
	if holder != nil && holder.site != "" {
		return boundLogField(holder.site)
	}
	p := s.OpenPrincipal(r)
	if len(p.SiteScope) == 1 {
		return boundLogField(p.SiteScope[0])
	}
	return ""
}

// maxLogField bounds a value that goes into a log line.
const maxLogField = 128

func boundLogField(v string) string {
	if len(v) <= maxLogField {
		return v
	}
	return v[:maxLogField] + "..."
}

// requestTenant is the tenant this request is running as.
//
// The server is single-tenant, so the configured tenant is the honest answer
// and the ONLY answer. It used to fall back to a request header when the
// configuration carried no tenant, and the comment above it said a header was
// never promoted to the tenant - which is exactly what that fallback did, on
// exactly the install where the promotion was least wanted: one with no tenant
// configured at all.
//
// A header is attacker-controlled, and this product has no session for it to be
// matched against, so a header is an instruction rather than an authorisation.
// The tenant is configuration, and an install with no tenant configured is a
// misconfigured install that says so in its logs rather than a tenant that
// anybody can name.
func (s *Server) requestTenant(_ *http.Request) string {
	return s.Tenant
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

// OpenPrincipal is the principal every request runs as.
//
// The console has no login, so there is no session and no user. What there IS
// is a CONFIGURED principal: a role and a site scope, both from configuration,
// and the ledger attributes every action to it. That is a different thing from
// an open door, and the difference is the capability checks below: the role
// decides what the API may do, and a request cannot widen it.
//
// The default role is the tenant administrator, because an install that has not
// configured an identity has one identity and it is the one that administers.
//
// The SITE SCOPE is the install's, for the same reason: it comes from
// configuration and not from the request, so the principal's scope is decided
// once, here, and every route reads the same answer. A route that decided the
// scope for itself is a route that can disagree with its siblings, and the
// symptom of that disagreement is a list route answering while a detail route
// 404s on the same record.
//
// The ACTOR and the SESSION are the install's too. The actor is who the
// ledger says did it, and the session is what makes a second read of an
// incident the same operator looking again rather than a second operator
// looking for the first time. Both are decided here, from configuration, and
// neither can be widened or narrowed by anything the caller sends.
//
// # The one thing that comes from the request
//
// In PROXY MODE -- `TrustProxyAuthHeader` set -- the operator's NAME comes
// from the header the proxy set, and nothing else does. The role, the scope,
// the tenant and the session are still configuration.
//
// That asymmetry is the control. The proxy says WHO; the product says WHAT.
// A proxy that could also say what would be a second authorisation system, and
// the product would then have two, and the stricter one would be the one
// nobody reads.
func (s *Server) OpenPrincipal(r *http.Request) identity.Principal {
	role := s.PrincipalRole
	if role == "" {
		role = identity.RoleTenantAdmin
	}
	sites := s.PrincipalSites
	if len(sites) == 0 && s.PrincipalSite != "" {
		sites = []string{s.PrincipalSite}
	}
	name := s.PrincipalName
	if name == "" {
		name = "console"
	}
	// The proxy's assertion about WHO, when there is one.
	//
	// The header is read here and nowhere else, and it is read only after
	// `requireProxyIdentity` has established that the request came from a
	// trusted proxy. A request that arrived without the header, or from an
	// untrusted peer, never reaches a handler at all.
	if s.TrustProxyAuthHeader != "" && r != nil {
		if asserted := strings.TrimSpace(r.Header.Get(s.TrustProxyAuthHeader)); asserted != "" {
			name = asserted
		}
	}
	userID := s.PrincipalUserID
	if userID == "" {
		userID = name
	}
	session := s.PrincipalSession
	if session == "" {
		session = name
	}
	return identity.Principal{
		TenantID:       s.Tenant,
		UserID:         userID,
		Username:       name,
		Roles:          []identity.Role{role},
		SiteScope:      sites,
		SessionID:      session,
		MFAVerified:    true,
		ServiceAccount: s.TrustProxyAuthHeader == "",
	}
}

// requireProxyIdentity is the proxy-mode layer.
//
// It is the D-16 posture in code: the console's identity is the network
// boundary's assertion, and the assertion is only believed when it arrives from
// a trusted proxy.
//
// Three refusals, and they are three different answers:
//
//   - the request did not come from a trusted proxy: 403. The peer's POSITION
//     is checked FIRST, and it is checked whatever the request carries, so the
//     refusal does not depend on the header and therefore does not tell a
//     stranger whether they guessed the header's name right.
//   - the header is absent in proxy mode: 401. The proxy was supposed to
//     authenticate and did not, or the request did not come through it.
//   - the header is present and the request came from a trusted proxy: the
//     request is allowed, and the principal is resolved from the header.
//
// The peer check is first because the header cannot be believed from a peer
// whose position is not trusted, and because a refusal that varies with the
// header is a refusal that leaks.
//
// The second refusal is the whole control, and it is the one a deployment
// forgets: a proxy that does not STRIP the header from client requests turns
// the header into something a stranger can set. The proxy's obligations are
// in `docs/decisions.md` D-16 and in `deploy/`, and the second refusal is what
// catches a deployment that skipped them.
//
// A request from an untrusted peer is refused EVEN IF it carries no header,
// because in proxy mode there is no identity to serve it under.
func (s *Server) requireProxyIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.TrustProxyAuthHeader == "" {
			// Not proxy mode: the configured principal, which is the behaviour
			// this product shipped with.
			next.ServeHTTP(w, r)
			return
		}
		if !s.fromTrustedProxy(r) {
			WriteError(w, http.StatusForbidden, "forbidden",
				"This request did not arrive from the trusted proxy, so its identity header "+
					"is not believed.", requestID(r))
			return
		}
		asserted := strings.TrimSpace(r.Header.Get(s.TrustProxyAuthHeader))
		if asserted == "" {
			WriteError(w, http.StatusUnauthorized, "unauthenticated",
				"This console is behind an authenticating proxy and the request did not "+
					"carry the identity header. Go through the proxy.", requestID(r))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fromTrustedProxy reports whether the request arrived from a configured proxy.
//
// It is the PEER's address and not a forwarded header, because a forwarded
// header is chosen by the caller. The peer is the socket, and the socket is
// the one thing about a request the caller does not choose.
func (s *Server) fromTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return false
	}
	return s.isTrustedProxy(peer)
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
		p := s.OpenPrincipal(r)
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
		p := s.OpenPrincipal(r)
		if !p.Can(c) {
			WriteError(w, http.StatusForbidden, "capability_denied",
				"The configured principal does not hold "+string(c)+".", requestID(r))
			return
		}
		if site := siteOf(r, p); site != "" && !p.CanSeeSite(site) {
			WriteError(w, http.StatusNotFound, "not_found",
				"No such record, or you are not assigned to its site.", requestID(r))
			return
		} else if site != "" {
			// The site was accepted, so it is the site the access log names.
			// Recorded after the check and not before, so a refused request
			// does not write a site the principal was not allowed to reach
			// into the log.
			recordSite(r, site)
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

	// The pipeline's loops.
	//
	// A loop that has stopped is an outage that looks like a healthy core: the
	// process keeps answering this endpoint, the backlog grows, and nothing says
	// so. The loops are named here, and the ones that are not running are named
	// separately, because "the product is up" and "the product is doing its
	// work" are different answers and an operator needs the second one.
	//
	// The status stays "ok" either way. A readiness probe that turns the
	// platform unready because a loop is down moves the failure to every
	// replica at once, and the same reasoning that keeps a breached objective
	// out of the status keeps a stopped loop out of it: the operator is told,
	// and the orchestrator is not lied to.
	if s.Loops != nil {
		loops := s.Loops()
		stopped := []string{}
		panicked := []string{}
		for _, l := range loops {
			if !l.Running {
				stopped = append(stopped, l.Name)
			}
			if l.Panics > 0 {
				panicked = append(panicked, l.Name)
			}
		}
		body["loops"] = map[string]any{
			"note": "which pipeline loops are running. A loop that has stopped is an outage " +
				"that looks like a healthy core, so the stopped ones are named here.",
			"running":  len(loops) - len(stopped),
			"stopped":  stopped,
			"panicked": panicked,
		}
	}

	WriteJSON(w, http.StatusOK, body)
}

// LoopLiveness is one pipeline loop's liveness, as the health endpoint needs
// it.
//
// It is the API's own shape rather than the pipeline's, and the wiring
// translates between them: two packages that both knew both types would be one
// package, and the pipeline would depend on the API's vocabulary for no reason
// but convenience.
type LoopLiveness struct {
	// Name is the loop's name.
	Name string
	// Running says whether the loop's goroutine is alive.
	Running bool
	// LastBeat is when the loop last reported in.
	LastBeat time.Time
	// Panics is how many times the loop has panicked.
	//
	// A loop that panicked once and came back is a different thing from a loop
	// that panics every pass, and the count is what tells them apart.
	Panics int
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
	fn := s.clockFn
	s.mu.Unlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
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
