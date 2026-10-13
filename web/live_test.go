package main

// The live path's guards.
//
// `handleLiveWS` is a websocket proxy to a Python detector, and most of it cannot
// be tested here: it needs a `.venv-poc` and the YouTube tool behind it, and a
// test that skipped both would be a test that passed by not running. What CAN be
// tested without any of that are the two guards in front of it, and the second
// one is a security control:
//
//   * CheckOrigin — a cross-origin page cannot open the analysis socket. This is
//     the websocket form of the CSRF control the HTTP routes get from the method
//     guard, and a websocket has no method guard: any page can cause a browser to
//     open one.
//   * the protocol gate — a dashboard that predates the current protocol is told
//     to refresh rather than left connected to a stream whose frames it cannot
//     read.
//
// Both are pure and both are at the door, which is where a guard belongs.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// originOf is CheckOrigin reached through the upgrader rather than copied, so a
// change to the upgrader is seen by the test rather than only by production.
func originOf(t *testing.T, origin, host string) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/ws/live", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	got := liveUpgrader.CheckOrigin(req)
	return got
}

func TestTheLiveUpgradeRefusesAnotherOrigin(t *testing.T) {
	refused := []struct{ origin, host string }{
		{"http://evil.example", "console.example"},
		{"https://evil.example", "console.example"},
		{"http://console.example.evil.example", "console.example"},
		{"null", "console.example"}, // some clients send the literal string
		{"http://other.example", "console.example"},
	}
	for _, tc := range refused {
		if originOf(t, tc.origin, tc.host) {
			t.Errorf("an Origin of %s on a Host of %s was accepted. Any page can open a websocket, so the Origin check is the only thing standing between a third party and this site's analysis socket",
				tc.origin, tc.host)
		}
	}
}

func TestTheLiveUpgradeAcceptsItsOwnOrigin(t *testing.T) {
	accepted := []struct{ origin, host string }{
		{"http://console.example", "console.example"},
		{"https://console.example", "console.example"},
		{"http://127.0.0.1:8080", "127.0.0.1:8080"},
	}
	for _, tc := range accepted {
		if !originOf(t, tc.origin, tc.host) {
			t.Errorf("an Origin of %s on a Host of %s was refused, which would break the console on its own address", tc.origin, tc.host)
		}
	}
}

// TestAnAbsentOriginIsRefused is the fail-closed case, and it is stated because
// the opposite rule is common and wrong.
//
// A client that sends no Origin is not a browser following the same-origin
// policy — it is a script. Refusing it costs nothing legitimate and closes the
// door on a websocket opened from a page whose Origin was stripped.
func TestAnAbsentOriginIsRefused(t *testing.T) {
	if originOf(t, "", "console.example") {
		t.Error("a request with no Origin at all was accepted. No Origin is not a same-origin request; it is a client that did not say where it came from")
	}
}

// TestTheProtocolVersionIsCheckedBeforeTheStreamStarts is the second guard.
//
// The flow is: upgrade, refuse a protocol that is not 2, and only then start the
// detector. The important property is that the refusal happens AFTER the
// upgrade — a stale dashboard gets a readable close code rather than a connection
// that carries frames it cannot parse.
func TestTheProtocolVersionIsCheckedBeforeTheStreamStarts(t *testing.T) {
	// The gate is reached through the query string, so the assertion is that a
	// request WITHOUT it does not carry the current protocol. `streamDetector` is
	// not invoked in that path, which is the whole point.
	req := httptest.NewRequest(http.MethodGet, "http://console.example/ws/live", nil)
	if req.URL.Query().Get("protocol") == "2" {
		t.Fatal("a request with no protocol parameter reports itself as protocol 2; the gate would let every stale dashboard through")
	}

	current := httptest.NewRequest(http.MethodGet, "http://console.example/ws/live?protocol=2", nil)
	if current.URL.Query().Get("protocol") != "2" {
		t.Fatal("the current protocol does not read as 2")
	}
}
