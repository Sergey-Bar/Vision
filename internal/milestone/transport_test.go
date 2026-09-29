package milestone_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// A redirect is a second host, and Go's default client follows up to ten of them
// while replaying the request headers on each hop. The Authorization bearer goes
// with it, so anything that can make Milestone answer 302 - a load balancer that
// moved, a proxy in the path, a vendor change — receives the site's Milestone
// credential for a host nobody configured.
//
// The test drives it end to end rather than asserting on the client struct: the
// thing that matters is whether the token arrives at the second server, and only
// a request proves that.
func TestTheBearerIsNotReplayedToARedirectTarget(t *testing.T) {
	const token = "milestone-bearer-that-must-not-leak"

	// The place the token must never reach.
	var leaked string
	var leakedMu sync.Mutex
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedMu.Lock()
		leaked = r.Header.Get("Authorization")
		leakedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer elsewhere.Close()

	// The configured Milestone, which redirects everything to elsewhere.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/v1/Events", http.StatusFound)
	}))
	defer redir.Close()

	tr, err := milestone.NewHTTPTransport(redir.URL+"/api", token, 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	defer func() { _ = tr.Close() }()

	_, err = tr.Get(context.Background(), milestone.Route{Path: "/Events"}, milestone.Query{})
	if err == nil {
		t.Fatal("a 302 was followed to completion. The redirect target served a 200 with an " +
			"empty body and the adapter reported success, having sent the site's Milestone " +
			"credential to a host nobody configured")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("the failure should name the redirect, got %q", err)
	}

	leakedMu.Lock()
	defer leakedMu.Unlock()
	if leaked != "" {
		t.Errorf("the bearer token was sent to the redirect target: %q", leaked)
	}
}

// A 3xx is a fact about the endpoint, and the error says so rather than reporting
// "3xx is not 2xx" and leaving the operator to work out what a 302 from their
// vendor means.
func TestARedirectIsReportedAsARedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	tr, err := milestone.NewHTTPTransport(srv.URL+"/api", "token", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	defer func() { _ = tr.Close() }()

	_, err = tr.Get(context.Background(), milestone.Route{Path: "/Events"}, milestone.Query{})
	if err == nil {
		t.Fatal("a 301 was treated as a success")
	}
	for _, want := range []string{"301", "redirect", "base_url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q so the operator knows what to change, got %q",
				want, err)
		}
	}
}
