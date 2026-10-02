package milestone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// The probe's job is to turn a list of guesses into confirmed-or-contradicted.
//
// The control that matters most is the one it must *not* do: it never changes a
// route's Unverified flag. A probe that promoted its own findings would make
// that flag a claim the tool makes about itself, and a tool that marks its own
// homework is worth nothing at all.

func aProbeServer(t *testing.T, answers map[string]string) milestone.Transport {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(body, "STATUS:") {
			w.WriteHeader(statusFrom(body))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	tr, err := milestone.NewHTTPTransport(srv.URL, "probe-token", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	return tr
}

func probeRoutes() []milestone.Route {
	return []milestone.Route{
		{Method: "GET", Path: "/api/rest/v1/events", Expect: "array", Unverified: true},
		{Method: "GET", Path: "/api/rest/v1/bookmarks", Expect: "object", WriteBack: true, Unverified: true},
	}
}

func TestTheProbeConfirmsWhatAnswersAndContradictsWhatDoesNot(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/rest/v1/events": `[{"Id":"1","Type":"Motion","Timestamp":"2026-07-01T12:00:00Z"}]`,
	})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})

	if len(report.Results) == 0 {
		t.Fatal("the probe reached nothing")
	}
	byPath := map[string]milestone.ProbeResult{}
	for _, r := range report.Results {
		byPath[r.Route.Path] = r
	}

	ev, ok := byPath["/api/rest/v1/events"]
	if !ok {
		t.Fatal("the events route was not probed")
	}
	if ev.Verdict != milestone.VerdictConfirmed {
		t.Errorf("the events route came back %q: %s", ev.Verdict, ev.Detail)
	}
	if ev.Items != 1 {
		t.Errorf("the probe saw %d items", ev.Items)
	}
	if len(ev.Keys) == 0 {
		t.Error("the probe reported no field names; comparing them with the adapter's " +
			"expectations is the entire job")
	}
	if ev.NeedsHuman == "" {
		t.Error("a confirmed route carries no caveat; a reader would take shape-confirmed " +
			"for meaning-understood")
	}

	// A write-side route must be skipped, not attempted. A probe that placed a
	// bookmark on every run would be an instrument nobody ran twice.
	bm, ok := byPath["/api/rest/v1/bookmarks"]
	if !ok {
		t.Fatal("the bookmark route was not reported at all")
	}
	if bm.Verdict != milestone.VerdictSkipped {
		t.Errorf("the probe attempted a write-side route: verdict %q", bm.Verdict)
	}
}

func TestTheProbeNamesAContradictionWithTheBody(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/rest/v1/events": "STATUS:500",
	})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
	if report.Summary.Contradicted != 1 {
		t.Errorf("a 500 was recorded as %d contradictions, want 1", report.Summary.Contradicted)
	}
	if report.AllConfirmed() {
		t.Error("the report claims everything is confirmed while a route contradicted it")
	}
	for _, r := range report.Results {
		if r.Route.Path == "/api/rest/v1/events" && r.Detail == "" {
			t.Error("a contradiction with no detail; the operator needs the body to file a bug")
		}
	}
}

func TestAnUnreachableRouteIsNotTheSameAsAContradictedOne(t *testing.T) {
	// Three cases, and the reason they must stay apart: each sends an operator to
	// a different place, and a probe that collapses them turns a wrong base URL
	// into a vendor bug report.
	t.Run("a route the vendor does not have is a contradiction", func(t *testing.T) {
		tr := aProbeServer(t, map[string]string{}) // 404s everything
		report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
		if report.Summary.Contradicted == 0 {
			t.Error("a 404 was not recorded as a contradiction; this is the single most useful " +
				"thing the probe can tell somebody standing in front of a real server")
		}
	})

	t.Run("an endpoint that is not there is unreachable", func(t *testing.T) {
		// A port nothing is listening on, rather than a server that answers 404.
		// "We did not get there" and "we got there and it is different" are
		// different facts.
		tr, err := milestone.NewHTTPTransport("http://127.0.0.1:1", "t", 500*time.Millisecond)
		if err != nil {
			t.Fatalf("NewHTTPTransport: %v", err)
		}
		report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
		if report.Summary.Unreachable == 0 {
			t.Error("a refused connection was not recorded as unreachable")
		}
		if report.Summary.Contradicted != 0 {
			t.Error("a refused connection was recorded as a contradiction; that sends an " +
				"operator to file a vendor bug for a typo in a config file")
		}
		if report.AllConfirmed() {
			t.Error("the report claims everything is confirmed when nothing was reachable")
		}
	})

	t.Run("bad credentials are unreachable, not a broken route", func(t *testing.T) {
		// Twenty routes would otherwise report twenty identical 401 rows, which
		// is a report nobody reads and a bug report nobody files.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		tr, err := milestone.NewHTTPTransport(srv.URL, "wrong-token", 2*time.Second)
		if err != nil {
			t.Fatalf("NewHTTPTransport: %v", err)
		}
		report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
		if report.Summary.Contradicted != 0 {
			t.Errorf("a 401 was recorded as %d contradictions; the credentials are the problem, "+
				"not the route", report.Summary.Contradicted)
		}
	})
}

// TestTheProbeNeverPromotesARoute is the control. Remove the claim and this is
// the only assertion in the tree that fails - and the failure is silent
// everywhere else, because a self-promoting probe still produces a report that
// looks right.
func TestTheProbeNeverPromotesARoute(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/rest/v1/events": `[{"Id":"1","Type":"Motion"}]`,
	})
	before := unverifiedCount(t)
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: milestone.Routes})
	if report.Summary.Confirmed == 0 {
		t.Skip("no route was confirmed; the fixture server answers nothing this probe expects")
	}
	if after := unverifiedCount(t); after != before {
		t.Errorf("the probe changed the Unverified count from %d to %d; a tool that promotes its own "+
			"findings makes the flag a claim it makes about itself", before, after)
	}
	// And the report says how many a human could promote, rather than doing it.
	if report.Summary.Flaggable == 0 && report.Summary.Confirmed > 0 {
		t.Log("no confirmed route was marked Unverified, so nothing is flaggable")
	}
}

func unverifiedCount(t *testing.T) int {
	t.Helper()
	n := 0
	for _, r := range milestone.Routes {
		if r.Unverified {
			n++
		}
	}
	return n
}

func TestTheProbeReachesTheClockRouteWhenAsked(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/rest/v1/System/Time": `{"dateTime":"2026-07-01T12:00:00.000Z"}`,
	})
	off := milestone.Probe(context.Background(), tr,
		milestone.ProbeOptions{Routes: probeRoutes(), IncludeClock: true})
	found := false
	for _, r := range off.Results {
		if r.Route.Path == "/api/rest/v1/System/Time" {
			found = true
			if r.Verdict != milestone.VerdictConfirmed {
				t.Errorf("the clock route came back %q: %s", r.Verdict, r.Detail)
			}
		}
	}
	if !found {
		t.Error("the clock route was not probed even though it was asked for")
	}

	// And it is not probed by default, because sampling a site's clock without
	// being asked is not something an instrument should do unprompted.
	without := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
	for _, r := range without.Results {
		if r.Route.Path == "/api/rest/v1/System/Time" {
			t.Error("the clock route was probed without being asked for")
		}
	}
}

func TestAClockAnswerWithNoTimeIsContradictedNotZero(t *testing.T) {
	// A zero clock is worse than no clock, because it looks like a measurement
	// of 1970 and every window selected against it is a window with timestamps
	// on it and no meaning.
	tr := aProbeServer(t, map[string]string{"/api/rest/v1/System/Time": `{}`})
	report := milestone.Probe(context.Background(), tr,
		milestone.ProbeOptions{Routes: probeRoutes(), IncludeClock: true})
	for _, r := range report.Results {
		if r.Route.Path == "/api/rest/v1/System/Time" && r.Verdict != milestone.VerdictContradicted {
			t.Errorf("an empty clock answer was recorded as %q", r.Verdict)
		}
	}
}

func TestTheReportSerialisesForATicket(t *testing.T) {
	tr := aProbeServer(t, map[string]string{"/api/rest/v1/events": `[{"Id":"1"}]`})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"verdict"`) {
		t.Error("the report does not serialise its verdicts; a probe nobody can paste into a ticket is an instrument nobody runs")
	}
	for _, r := range report.Results {
		if r.String() == "" {
			t.Error("a result renders as an empty line")
		}
	}
}

// The probe has to report the *body* for the routes whose purpose is to be
// learned from a live site, and not only how many items arrived.
//
// `/eventTypes` is the one that needs it: a field *name* tells a human what to
// call a column, and what the human needs is which GUIDs and names this
// customer's installation actually uses. Neither published document shows a
// response body, so the probe is the only evidence there will be until somebody
// stands in front of a real server.
func TestTheProbeReportsTheBodyAndNotOnlyAnItemCount(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/rest/v1/eventTypes": `[{"id":"481d4d6c-9156-4698-af7e-74b32f9e4359",
			"name":"Motion Started","generatorType":"MIPDevice"}]`,
	})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{
		Routes: []milestone.Route{{Method: "GET", Path: "/api/rest/v1/eventTypes", Unverified: true}},
	})
	if len(report.Results) != 1 {
		t.Fatalf("probed %d routes, want 1", len(report.Results))
	}
	got := report.Results[0]
	if got.Verdict != milestone.VerdictConfirmed {
		t.Fatalf("the vocabulary route came back %q: %s", got.Verdict, got.Detail)
	}
	if got.Sample == nil {
		t.Fatal("the probe reported an item count and no body. The counts were never the " +
			"question: the schema is documented nowhere, and the probe is the only evidence")
	}
	if got.Sample["name"] != "Motion Started" || got.Sample["id"] != "481d4d6c-9156-4698-af7e-74b32f9e4359" {
		t.Errorf("the sample is %v; it has to be the vendor's own bytes for a human to read "+
			"the identity and name fields off it", got.Sample)
	}
	// And it survives into the rendered line, because an operator running the
	// probe in a terminal is the person who has to act on it.
	if !strings.Contains(got.String(), "Motion Started") {
		t.Errorf("the rendered result does not carry the body: %q", got.String())
	}
	// The serialised artifact is the thing pasted into a ticket.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"sample"`) {
		t.Errorf("the serialised result has no sample field: %s", raw)
	}
}

// A documented filter that this build cannot otherwise ask about is asked through
// the probe, and the answer lands in the same artifact as the schema.
//
// This API has no machine-readable specification (route-audit.md §4), so whether
// `analyticData.object.value=contains:'ABC123'` is honoured is a fact about a
// server and not about a document. The alternative — hand-typing a curl at a
// customer's box — is the thing `adapter-probe` exists to replace: a person
// clicking through endpoints is a person who reaches some of them and stops, and
// what they skipped would not be written down.
func TestTheProbeSendsNamedParametersOnEveryRead(t *testing.T) {
	var mu sync.Mutex
	asked := map[string][]url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Path] = append(asked[r.URL.Path], r.URL.Query())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"array":[{"id":"e1","type":"Analytics"}],"eventCount":1}`))
	}))
	t.Cleanup(srv.Close)

	tr, err := milestone.NewHTTPTransport(srv.URL, "probe-token", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{
		Routes:    []milestone.Route{{Method: "GET", Path: "/api/rest/v1/events", Expect: "array", Unverified: true}},
		ItemLimit: 1,
		Params:    map[string]string{"analyticData.object.value": "contains:'ABC123'"},
	})
	if len(report.Results) != 1 {
		t.Fatalf("probed %d routes, want 1", len(report.Results))
	}
	if report.Results[0].Verdict != milestone.VerdictConfirmed {
		t.Fatalf("verdict %q: %s", report.Results[0].Verdict, report.Results[0].Detail)
	}

	mu.Lock()
	defer mu.Unlock()
	got := asked["/api/rest/v1/events"]
	if len(got) != 1 {
		t.Fatalf("the events route was probed %d times", len(got))
	}
	if !strings.Contains(got[0].Get("analyticData.object.value"), "ABC123") {
		t.Errorf("the named parameter did not reach the query: %v", got[0])
	}
}

// statusFrom reads the STATUS:404 marker a test server uses to answer with a
// status code and no body.
func statusFrom(s string) int {
	n := 0
	for _, c := range s[len("STATUS:"):] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
