package milestone_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/milestone"
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
		http.Redirect(w, r, elsewhere.URL+"/api/rest/v1/events", http.StatusFound)
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

// A named query parameter reaches the wire, spelled exactly as the caller wrote
// it, and it wins over the value this file would otherwise have derived.
//
// Two things are asserted and they are not the same thing. The first is that a
// documented filter survives into the request: the single quotes around the value
// are part of this API's syntax (`events-rest-v1.reference.md:219-263`) and a
// builder that "helpfully" stripped them would ask a question the server does not
// recognise, and the answer would look like a site with no analytics rather than a
// rejected filter.
//
// The second is the override. A caller that names `size` is asking for a specific
// number of records, and a derived value that silently won would make the request
// something other than the one that was asked for - which on a probe is a report
// about an endpoint nobody questioned.
func TestANamedQueryParameterReachesTheWireAndWinsOverADerivedOne(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	tr, err := milestone.NewHTTPTransport(srv.URL+"/api", "token", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	defer func() { _ = tr.Close() }()

	since := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	_, err = tr.Get(context.Background(),
		milestone.Route{Path: "/events"},
		milestone.Query{
			Since: &since,
			Limit: 200,
			Params: map[string]string{
				"analyticData.object.value": "contains:'ABC123'",
				"size":                      "1",
			},
		})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The vendor's filter syntax, intact.
	if !strings.Contains(got, "analyticData.object.value=contains%3A%27ABC123%27") &&
		!strings.Contains(got, "analyticData.object.value=contains:'ABC123'") {
		t.Errorf("the documented filter did not reach the wire as written: %q", got)
	}
	// The single quotes are load-bearing and are asserted separately from the
	// encoding, because a stripped quote is a request the server rejects for a
	// reason nobody reading the report would guess.
	if !strings.Contains(got, "%27") && !strings.Contains(got, "'") {
		t.Errorf("the value's single quotes were lost: %q. This API quotes string filter "+
			"values (events-rest-v1.reference.md:219-263)", got)
	}
	if !strings.Contains(got, "size=1") {
		t.Errorf("the caller's size did not win over the derived 200: %q", got)
	}
	if strings.Contains(got, "size=200") {
		t.Errorf("the derived page size survived alongside the caller's: %q", got)
	}
	// A derived filter that the caller did not name is still there.
	if !strings.Contains(got, "time=") {
		t.Errorf("the derived time filter was dropped along with the override: %q", got)
	}
}

// A write-side route carries no read parameters, and nothing a caller named can
// put one there.
//
// A bookmark POST is a body-and-task endpoint with no filters
// (`bookmarks-rest-v1.openapi.yaml:167-171`). Sending `analyticData...` or `size`
// to it asks the vendor a question about an endpoint that has no such question,
// and the answer is a 400 that reads like a broken adapter rather than like a
// wrong request.
func TestAWriteRouteCarriesNoReadParameters(t *testing.T) {
	var got string
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, method = r.URL.RawQuery, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"array":[{"reference":"probe-ref"}]}`))
	}))
	defer srv.Close()

	tr, err := milestone.NewHTTPTransport(srv.URL+"/api", "token", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	defer func() { _ = tr.Close() }()

	writer, ok := tr.(milestone.BookmarkWriter)
	if !ok {
		t.Fatal("the HTTP transport is not a BookmarkWriter, so this route cannot be reached at all")
	}
	if _, err := writer.WriteBookmark(context.Background(), milestone.Bookmark{
		IdempotencyKey: "probe-key",
		SiteRef:        "e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		Time:           time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		Label:          "adapter probe",
	}); err != nil {
		t.Fatalf("WriteBookmark: %v", err)
	}
	if method != http.MethodPost {
		t.Fatalf("the write went out as %s", method)
	}
	for _, unwanted := range []string{"size", "time", "analyticData"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the write carried the read parameter %q: %q", unwanted, got)
		}
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
