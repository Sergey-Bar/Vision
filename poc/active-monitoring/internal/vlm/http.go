package vlm

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// newHTTPClient builds the client the four endpoints share.
//
// Every egress decision is made here, once, so it cannot be made four times with
// three different answers:
//
//   - **Redirects are refused.** A 302 from the pinned host to somewhere else is
//     the cheapest way to send this site's footage to a host nobody pinned. The
//     Go default follows up to ten of them.
//   - **The TLS minimum is 1.2.** Not a preference; a 1.0 client is a client
//     that will negotiate with whatever answers.
//   - **The connection pool is not shared with other destinations.** A
//     misconfiguration should not leave sockets open to hosts this build
//     refuses to talk to.
func newHTTPClient(cfg Config) *http.Client {
	dialer := &net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConnsPerHost:   2,
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tlsFloor},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirectRefused
		},
	}
}

// tlsFloor is the TLS minimum this build accepts, named so the value is one
// line rather than two literals in a struct.
const tlsFloor = tls.VersionTLS12
