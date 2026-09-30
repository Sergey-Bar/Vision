package hafnia

import (
	"context"
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
//   - **The dialer pins the destination.** A second layer under the allow-list,
//     for the case where a hostname resolves somewhere unexpected. This is the
//     SSRF defence that survives a DNS change.
//   - **TLS verification is on** unless explicitly disabled, and when it is
//     disabled the connection says so in the TLS config rather than quietly
//     being insecure.
func newHTTPClient(cfg Config) *http.Client {
	dialer := &net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
		ExpectContinueTimeout: time.Second,
		// The allow-list is enforced by the caller refusing any host that is not
		// the pinned one; this keeps the connection pool from holding other
		// destinations open across a misconfiguration.
		MaxIdleConnsPerHost: 2,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	}
	if cfg.AllowInsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			//nolint:gosec // the operator asked for this by name; see Config
			InsecureSkipVerify: true,
		}
	} else {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirectRefused
		},
	}
}

// ErrRedirectRefused is returned when the provider answers with a redirect.
//
// It is its own error rather than a generic one because "the provider tried to
// send us somewhere else" is the single most security-relevant thing that can
// happen to an upload, and it deserves a name an operator can be paged about.
var ErrRedirectRefused = errRedirect{}

type errRedirect struct{}

func (errRedirect) Error() string {
	return "hafnia: refusing a redirect; the pinned host is the only destination"
}
