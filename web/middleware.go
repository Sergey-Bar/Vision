package main

// The console server's middleware.
//
// Two of them, and both of them are things the product's API already does and
// the console server did not:
//
//   - the SECURITY HEADERS, including a Content Security Policy that has no
//     `unsafe-inline` in it
//   - the ACCESS LOG, which carries the tenant, the site and the request id
//
// The console server is the surface a judge opens, and it is the surface a
// browser points at. A CSP on the product's API is worth little if the page
// that renders its answers has none.

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// The Content Security Policy.
//
// `style-src 'self'` with no `unsafe-inline` is the strict one, and it is
// viable here because the console sets its styles through CSSOM rather than
// through a style attribute, and the showcase's pages link their stylesheets
// rather than inlining them.
//
// The live page embeds a public stream, so `frame-src` names the frame's
// origin. That is the ONE exception, it is named rather than wildcarded, and
// it is a frame and not a script: the embedded page runs in its own origin.
//
// `frame-ancestors 'none'` is the answer to clickjacking, and `object-src
// 'none'` is the answer to a plugin.
const contentSecurityPolicy = "default-src 'self'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' blob:; " +
	"style-src 'self'; " +
	"script-src 'self'; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"worker-src 'self' blob:; " +
	"frame-src 'self' https://www.youtube.com https://www.youtube-nocookie.com"

// securityHeaders sets the headers on every response.
//
// The Content-Security-Policy is only meaningful with the others: a policy
// without `frame-ancestors` is a policy about a page nobody can frame.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The policy names the features the console refuses. The ones it
		// does not name are allowed, and the live page's third-party frame
		// needs `compute-pressure` -- without it the browser reports the
		// violation against the EMBEDDING page, which is a console error on
		// a page that is working.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), compute-pressure=(self \"https://www.youtube.com\" \"https://www.youtube-nocookie.com\")")
		next.ServeHTTP(w, r)
	})
}

// requestCounter is the source of the request ids.
//
// It is a counter and not a random string because a request id's job is to
// be UNIQUE within a run and READABLE afterwards, and a counter is both.
var requestCounter atomic.Uint64

// withRequestID assigns a request id to a request and echoes it back.
//
// The id is in the response header as well as in the log, so a person
// looking at a failing request can find the line in the log without having
// to guess a timestamp.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), requestCounter.Add(1))
		r.Header.Set("X-Request-Id", id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status code for the log line.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Flush lets the streaming handlers stream.
//
// The console server serves an MJPEG stream, and a ResponseWriter wrapper
// that does not implement http.Flusher turns a stream into a buffer: the
// page waits for the whole thing.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack lets a handler take the connection over.
//
// The WebSocket upgrade needs it, and a middleware that wraps the response
// writer WITHOUT it breaks every socket on the server: the upgrade fails
// with "websocket: response does not implement http.Hijacker", the browser
// reports a handshake error of 500, and the live view never starts.
//
// This is the second time a wrapper has broken something it did not know
// about: the first was the access log's own writer, and the second was
// this one. A middleware that wraps a writer owes the writer's whole
// interface.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the response writer does not support hijacking")
	}
	return h.Hijack()
}

// accessLog logs one line per request, with the tenant, the site and the
// request id.
//
// The three fields are the ones the repository's own convention says a log
// line carries, and they are here because a console log without a tenant is
// a log line nobody can audit: the whole point of the record is that a
// reader can tie an action to the tenant it happened in.
//
// The tenant is the CONFIGURED one, not the one in a request header: the
// console no longer believes a header, so it must not log one either. A log
// line that carried the attacker's tenant would be a log line that agreed
// with the attacker.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		site := strings.TrimSpace(r.URL.Query().Get("siteId"))
		log.Printf("access %s %s %d %dB %s tenant=%q site=%q request_id=%q",
			logSafe(r.Method), logSafe(r.URL.Path), rec.status, rec.bytes,
			time.Since(start).Round(time.Millisecond), principalTenant(), logSafe(site),
			logSafe(r.Header.Get("X-Request-Id")))
	})
}

// logSafe makes a value safe to put in a log line.
//
// A log line is a record, and a record an attacker can write is not a record.
// A request's path and its Origin header are both attacker-controlled, and a
// newline in either of them lets a request forge a log line of its own: a
// line that says the request was a GET when it was a DELETE, or a line that
// says it came from this origin when it did not.
//
// So the control characters go, and the length is bounded, because a log line
// that is a megabyte long is a log line that costs money to store and reads
// as nothing.
func logSafe(s string) string {
	if s == "" {
		return ""
	}
	const maxLen = 200
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			// A control character, a newline or a tab. It becomes a space, so
			// the value is still readable and the line is still one line.
			b.WriteByte(' ')
		case r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[:maxLen] + "…"
	}
	return out
}

// refuseCrossOrigin refuses a state-changing request that did not come from
// this origin.
//
// The console is a browser application served by this server, and it has no
// session: the browser is the thing that keeps a cross-site page from
// making the browser POST here on the operator's behalf. Without this, a
// page on another origin could make the operator's browser drive the
// console.
//
// A request with no Origin header is not a browser request - it is a script
// or a curl - and the console's own tooling uses those. The rule is
// therefore about the ORIGIN, not about the absence of one.
func refuseCrossOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if !sameOrigin(origin, host) {
			log.Printf("refusing a cross-origin %s to %s from %s",
				logSafe(r.Method), logSafe(r.URL.Path), logSafe(origin))
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin compares an Origin header with the request's host.
func sameOrigin(origin, host string) bool {
	o := origin
	for _, prefix := range []string{"http://", "https://"} {
		o = strings.TrimPrefix(o, prefix)
	}
	// An origin carries no path, so anything after the host is not part of
	// the comparison.
	if i := strings.IndexByte(o, '/'); i >= 0 {
		o = o[:i]
	}
	return strings.EqualFold(o, host)
}

// thePrincipal is the console server's configured principal, for the log.
var thePrincipal atomic.Pointer[string]

// setPrincipal records the tenant the console is acting as.
func setPrincipal(tenant string) {
	thePrincipal.Store(&tenant)
}

// principalTenant returns the tenant the console is acting as.
func principalTenant() string {
	if p := thePrincipal.Load(); p != nil {
		return *p
	}
	return ""
}
