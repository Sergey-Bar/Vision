package xprotect

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// probeServer answers the four probe routes with fixed responses, so the probe's
// classification can be tested without a VMS.
type probeServer struct {
	// responses is keyed by "METHOD path".
	responses map[string]probeReply
	// writes records every non-GET request. The probe must produce none of them.
	writes []string
}

type probeReply struct {
	status int
	body   string
}

func (p *probeServer) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Path
	if req.Method != http.MethodGet {
		p.writes = append(p.writes, key)
	}
	if r, ok := p.responses[key]; ok {
		return &http.Response{StatusCode: r.status, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(r.body))}, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"message":"not found"}`))}, nil
}

func probeClientFor(t *testing.T, srv *probeServer) *Client {
	t.Helper()
	// The token route always answers, so the tests are about the READ routes. The
	// failed-authentication case is its own test.
	if _, ok := srv.responses["POST "+PathToken]; !ok {
		if srv.responses == nil {
			srv.responses = map[string]probeReply{}
		}
		srv.responses["POST "+PathToken] = probeReply{200, `{"access_token":"t","expires_in":3600}`}
	}
	c, err := New(Config{
		BaseURL:     "https://x.example.com",
		Timeout:     2 * time.Second,
		Credentials: Credentials{ClientID: "c", ClientSecret: "s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The transport is swapped AFTER New rather than being a Config field. A
	// configurable transport on the production type is a configurable egress, and
	// this package's allow-list is the reason that does not exist.
	c.http = &http.Client{Timeout: 2 * time.Second, Transport: srv}
	return c
}

// TestTheProbeNeverWrites is the property that makes a probe safe to point at a
// live instance during a demo: there is no route it can reach that POSTs.
func TestTheProbeNeverWrites(t *testing.T) {
	srv := &probeServer{responses: map[string]probeReply{
		"POST " + PathToken:     {200, `{"access_token":"t","expires_in":3600}`},
		"GET " + PathEventTypes: {200, `{"data":[{"id":"a","name":"Analytics"}]}`},
		"GET " + PathEvents:     {200, `{"data":[]}`},
		"GET " + PathBookmarks:  {200, `{"data":[]}`},
	}}
	c := probeClientFor(t, srv)
	if _, err := c.Probe(context.Background(), ProbeInput{Tag: "aicap-bird-abc123"}); err != nil {
		t.Fatal(err)
	}
	// The token call is the only POST, and it is a credential exchange, not a write.
	for _, w := range srv.writes {
		if w != "POST "+PathToken {
			t.Fatalf("the probe issued a write: %s", w)
		}
	}
}

// TestTheFourStatesAreDistinguished: the four outcomes an operator can act on are
// four, not two.
func TestTheFourStatesAreDistinguished(t *testing.T) {
	cases := []struct {
		name    string
		replies map[string]probeReply
		path    string
		want    RouteState
	}{
		{"reachable", map[string]probeReply{"GET " + PathEventTypes: {200, `{"data":[{"id":"a"}]}`}}, PathEventTypes, RouteReachable},
		{"contradicted", map[string]probeReply{"GET " + PathEventTypes: {200, `{"results":[]}`}}, PathEventTypes, RouteContradicted},
		{"unauthenticated", map[string]probeReply{"GET " + PathEventTypes: {401, `{"message":"no"}`}}, PathEventTypes, RouteUnauthenticated},
		{"absent", map[string]probeReply{}, PathEventTypes, RouteAbsent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &probeServer{responses: tc.replies}
			c := probeClientFor(t, srv)
			rows, err := c.Probe(context.Background(), ProbeInput{Tag: "t"})
			if err != nil {
				t.Fatal(err)
			}
			var got RouteState
			for _, r := range rows {
				if r.Path == tc.path {
					got = r.State
				}
			}
			if got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAFailedAuthenticationIsOneAnswerAndNotFourAbsent: fourteen `absent` rows when
// the credentials are wrong would be fourteen wrong claims.
func TestAFailedAuthenticationIsOneAnswerAndNotFourAbsent(t *testing.T) {
	srv := &probeServer{responses: map[string]probeReply{
		"POST " + PathToken: {401, `{"message":"bad credentials"}`},
	}}
	c := probeClientFor(t, srv)
	rows, err := c.Probe(context.Background(), ProbeInput{Tag: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows; a failed authentication is ONE answer", len(rows))
	}
	if rows[0].State != RouteUnknown {
		t.Fatalf("state = %q; an unreachable host is unknown, and `absent` would be a claim about the ROUTES", rows[0].State)
	}
	if rows[0].Detail == "" {
		t.Fatal("the single row carries no detail, so the operator cannot act on it")
	}
}

// TestAProbeNeverSaysVerified: the state set has no word meaning verified, and this
// is the assertion that keeps it that way.
func TestAProbeNeverSaysVerified(t *testing.T) {
	for _, s := range []RouteState{RouteReachable, RouteContradicted, RouteUnauthenticated, RouteAbsent, RouteUnknown} {
		for _, forbidden := range []string{"verified", "confirmed"} {
			if strings.Contains(string(s), forbidden) {
				t.Fatalf("the state %q contains %q: verification is a human decision with a date on it", s, forbidden)
			}
		}
	}
}

// TestStateFromMapsTransportFailuresToUnknown: a network failure says nothing about
// the routes.
func TestStateFromMapsTransportFailuresToUnknown(t *testing.T) {
	state, detail := stateFrom(&RetryableError{Detail: "dial tcp: connection refused"}, 0)
	if state != RouteUnknown {
		t.Fatalf("state = %q, want %q", state, RouteUnknown)
	}
	if detail == "" {
		t.Fatal("a transport failure with no detail is a shrug")
	}
}
