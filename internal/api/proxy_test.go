package api_test

// The proxy mode: the console's identity is the network boundary's assertion.
//
// # The three refusals
//
//   - the header is absent in proxy mode: 401. The proxy was supposed to
//     authenticate and did not, or the request did not come through it.
//   - the header is present but the request came from an UNTRUSTED peer: 403.
//     A header is forgeable by anybody who can reach the port, so believing it
//     from an untrusted peer would make the header a way IN.
//   - the header is present and the peer is trusted: allowed, and the
//     operator's name comes from the header.
//
// The second refusal is the one a deployment gets wrong, and it is the one the
// reference proxy in `deploy/proxy/` exists for: a proxy that does not STRIP
// the header from client requests lets anybody claim to be anybody, and the
// console cannot tell, because the peer it saw IS the trusted proxy.
//
// # What the header may NOT do
//
// The header carries the operator's NAME and nothing else. The role and the
// site scope stay configured, so a proxy that asserts an operator's identity
// cannot grant that operator more than the product's capability set allows. A
// proxy that could grant capabilities would be a second authorisation system,
// and the product would have two.

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// The header the tests use. The name is configuration, so the tests use a name
// of their own rather than the one the reference proxy happens to use.
const proxyAuthHeader = "X-Auth-Request-User"

// proxyServer is a server in proxy mode, with the trusted proxy at 10.0.0.0/8.
func proxyServer(t *testing.T) *api.Server {
	t.Helper()
	s, _, _ := server(t)
	s.TrustProxyAuthHeader = proxyAuthHeader
	s.BehindProxy = true
	_, block, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse the trusted proxy block: %v", err)
	}
	s.TrustedProxies = []*net.IPNet{block}
	return s
}

// TestInProxyModeARequestWithoutTheIdentityHeaderIsRefused is the first
// refusal.
//
// A request that arrives with no identity has no identity to serve, and
// serving it under the configured principal would be the exact thing proxy mode
// exists to prevent.
func TestInProxyModeARequestWithoutTheIdentityHeaderIsRefused(t *testing.T) {
	s := proxyServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:1234" // a trusted peer
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a request with no identity header got %d, want 401. In proxy mode there is "+
			"no principal to serve it under", rec.Code)
	}
}

// TestInProxyModeARequestFromAnUntrustedPeerIsRefused is the second refusal,
// and the one the reference proxy exists for.
//
// The request carries the header and the peer is NOT the proxy. The header is
// forgeable by anybody who can reach the port, so it is not believed.
func TestInProxyModeARequestFromAnUntrustedPeerIsRefused(t *testing.T) {
	s := proxyServer(t)

	for _, header := range []string{"", proxyAuthHeader} {
		req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		req.RemoteAddr = "203.0.113.9:1234" // NOT a trusted peer
		if header != "" {
			req.Header.Set(header, "attacker")
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("a request from an untrusted peer (header present: %t) got %d, want 403. "+
				"The identity header is only believed from the proxy", header != "", rec.Code)
		}
	}
}

// TestInProxyModeTheIdentityHeaderNamesTheOperator is the third case: the
// request is allowed, and the operator is who the proxy said.
func TestInProxyModeTheIdentityHeaderNamesTheOperator(t *testing.T) {
	s := proxyServer(t)
	s.PrincipalName = "configured-name"

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set(proxyAuthHeader, "operator@example.internal")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("a request through the proxy got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestTheProxyHeaderCannotWidenThePrincipal is the asymmetry, asserted.
//
// The header says WHO. The role and the site scope are CONFIGURATION, so a
// proxy that asserts an operator's identity cannot grant that operator more
// than the product's capability set allows.
func TestTheProxyHeaderCannotWidenThePrincipal(t *testing.T) {
	s := proxyServer(t)
	s.PrincipalRole = identity.RoleOperator
	s.PrincipalSites = []string{"site-allowed"}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set(proxyAuthHeader, "operator@example.internal")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("the request was refused: %d %s", rec.Code, rec.Body.String())
	}

	// The principal the request ran as.
	p := s.OpenPrincipal(req)
	if p.Username != "operator@example.internal" {
		t.Errorf("the operator is named %q, want the proxy's assertion", p.Username)
	}
	if len(p.Roles) != 1 || p.Roles[0] != identity.RoleOperator {
		t.Errorf("the roles are %v, want the CONFIGURED role %v. A proxy that could grant "+
			"capabilities would be a second authorisation system", p.Roles, identity.RoleOperator)
	}
	if len(p.SiteScope) != 1 || p.SiteScope[0] != "site-allowed" {
		t.Errorf("the site scope is %v, want the CONFIGURED scope. A header cannot widen the scope",
			p.SiteScope)
	}
}

// TestWithoutProxyModeTheConfiguredPrincipalIsUsed is the other half.
//
// An install with no proxy is the appliance the product shipped with, and it
// must keep working: the configured principal, and no refusals.
func TestWithoutProxyModeTheConfiguredPrincipalIsUsed(t *testing.T) {
	s, _, _ := server(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set(proxyAuthHeader, "operator@example.internal")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("a request to a console with no proxy configured got %d, want 200",
			rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("the health body does not say ok: %s", rec.Body.String())
	}
}
