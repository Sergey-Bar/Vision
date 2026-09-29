package api

import (
	"net"
	"net/http"
	"testing"
)

// This test is internal because clientIP is unexported, and unexported is the
// only correct visibility for it: the client address is derived once, here, and
// the rule below is the one the rate limiter and the audit ledger both key on.

func mustCIDRs(t *testing.T, raw ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(raw))
	for _, r := range raw {
		_, n, err := net.ParseCIDR(r)
		if err != nil {
			t.Fatalf("test setup: %s is not a CIDR: %v", r, err)
		}
		out = append(out, n)
	}
	return out
}

func request(peer, fwd string) *http.Request {
	r := &http.Request{RemoteAddr: peer, Header: http.Header{}}
	if fwd != "" {
		r.Header.Set("X-Forwarded-For", fwd)
	}
	return r
}

// The header is ignored unless the direct peer is inside a configured trusted
// CIDR. This is the case that made the old leftmost-read spoofable: any client
// that could reach the listener wrote its own address into the first entry.
func TestUntrustedPeerHeaderIsIgnored(t *testing.T) {
	s := &Server{BehindProxy: true, TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}

	r := request("203.0.113.9:5555", "1.2.3.4")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("an untrusted peer's X-Forwarded-For was believed: got %q, want 203.0.113.9", got)
	}

	// BehindProxy false, whatever the list says: there is no proxy to believe.
	s = &Server{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}
	if got := s.clientIP(request("10.1.1.1:5555", "1.2.3.4")); got != "10.1.1.1" {
		t.Errorf("with BehindProxy off, the header was believed: got %q, want 10.1.1.1", got)
	}
}

// A trusted peer gets the rightmost entry that is not itself a trusted proxy.
// The leftmost entry is written by the client; the entries to its right are
// appended by hops the operator named.
func TestTrustedPeerUsesRightmostUntrustedEntry(t *testing.T) {
	s := &Server{BehindProxy: true, TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}

	// The client forged 1.1.1.1 to the left of the real address. The real one is
	// rightmost and untrusted, so it wins.
	r := request("10.0.0.5:4444", "1.1.1.1, 203.0.113.9")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("got %q, want 203.0.113.9 (the rightmost untrusted entry)", got)
	}

	// Two trusted proxies in the chain: both are skipped, the client survives.
	r = request("10.0.0.5:4444", "203.0.113.9, 10.1.1.1, 10.2.2.2")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("got %q, want 203.0.113.9 (trusted hops skipped)", got)
	}

	// No header at all from a trusted peer: the peer is the best answer there is.
	if got := s.clientIP(request("10.0.0.5:4444", "")); got != "10.0.0.5" {
		t.Errorf("got %q, want the peer address 10.0.0.5", got)
	}
}

// A header made entirely of addresses the operator has named as proxies carries
// no client information. Falling back to the leftmost entry there would put a
// value the caller chose into an audit row, which is the defect this whole path
// exists to remove.
func TestAllTrustedHeaderFallsBackToThePeer(t *testing.T) {
	s := &Server{BehindProxy: true, TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}
	if got := s.clientIP(request("10.0.0.5:4444", "10.9.9.9, 10.8.8.8")); got != "10.0.0.5" {
		t.Errorf("got %q, want the peer address 10.0.0.5", got)
	}
}

// Whitespace around entries is what proxies actually emit, and a malformed
// entry tells us nothing rather than ending the walk.
func TestForwardedParsingTolerance(t *testing.T) {
	s := &Server{BehindProxy: true, TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}

	r := request("10.0.0.5:4444", "  203.0.113.9 , 10.1.1.1  ")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("got %q, want 203.0.113.9", got)
	}

	r = request("10.0.0.5:4444", "not-an-ip, 10.1.1.1, 203.0.113.9")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("a malformed entry ended the walk: got %q, want 203.0.113.9", got)
	}
}

// An unparsable RemoteAddr must not panic and must not fall through to the
// header.
func TestUnparsableRemoteAddr(t *testing.T) {
	s := &Server{BehindProxy: true, TrustedProxies: mustCIDRs(t, "10.0.0.0/8")}
	if got := s.clientIP(request("not-an-address", "1.2.3.4")); got != "not-an-address" {
		t.Errorf("got %q, want the raw RemoteAddr", got)
	}
}
