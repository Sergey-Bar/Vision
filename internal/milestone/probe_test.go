package milestone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{Method: "GET", Path: "/api/v1/Events", Expect: "array", Unverified: true},
		{Method: "GET", Path: "/api/v1/Bookmarks", Expect: "object", WriteBack: true, Unverified: true},
	}
}

func TestTheProbeConfirmsWhatAnswersAndContradictsWhatDoesNot(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/v1/Events": `[{"Id":"1","Type":"Motion","Timestamp":"2026-07-01T12:00:00Z"}]`,
	})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})

	if len(report.Results) == 0 {
		t.Fatal("the probe reached nothing")
	}
	byPath := map[string]milestone.ProbeResult{}
	for _, r := range report.Results {
		byPath[r.Route.Path] = r
	}

	ev, ok := byPath["/api/v1/Events"]
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
	bm, ok := byPath["/api/v1/Bookmarks"]
	if !ok {
		t.Fatal("the bookmark route was not reported at all")
	}
	if bm.Verdict != milestone.VerdictSkipped {
		t.Errorf("the probe attempted a write-side route: verdict %q", bm.Verdict)
	}
}

func TestTheProbeNamesAContradictionWithTheBody(t *testing.T) {
	tr := aProbeServer(t, map[string]string{
		"/api/v1/Events": "STATUS:500",
	})
	report := milestone.Probe(context.Background(), tr, milestone.ProbeOptions{Routes: probeRoutes()})
	if report.Summary.Contradicted != 1 {
		t.Errorf("a 500 was recorded as %d contradictions, want 1", report.Summary.Contradicted)
	}
	if report.AllConfirmed() {
		t.Error("the report claims everything is confirmed while a route contradicted it")
	}
	for _, r := range report.Results {
		if r.Route.Path == "/api/v1/Events" && r.Detail == "" {
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
		"/api/v1/Events": `[{"Id":"1","Type":"Motion"}]`,
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
		"/api/v1/System/Time": `{"dateTime":"2026-07-01T12:00:00.000Z"}`,
	})
	off := milestone.Probe(context.Background(), tr,
		milestone.ProbeOptions{Routes: probeRoutes(), IncludeClock: true})
	found := false
	for _, r := range off.Results {
		if r.Route.Path == "/api/v1/System/Time" {
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
		if r.Route.Path == "/api/v1/System/Time" {
			t.Error("the clock route was probed without being asked for")
		}
	}
}

func TestAClockAnswerWithNoTimeIsContradictedNotZero(t *testing.T) {
	// A zero clock is worse than no clock, because it looks like a measurement
	// of 1970 and every window selected against it is a window with timestamps
	// on it and no meaning.
	tr := aProbeServer(t, map[string]string{"/api/v1/System/Time": `{}`})
	report := milestone.Probe(context.Background(), tr,
		milestone.ProbeOptions{Routes: probeRoutes(), IncludeClock: true})
	for _, r := range report.Results {
		if r.Route.Path == "/api/v1/System/Time" && r.Verdict != milestone.VerdictContradicted {
			t.Errorf("an empty clock answer was recorded as %q", r.Verdict)
		}
	}
}

func TestTheReportSerialisesForATicket(t *testing.T) {
	tr := aProbeServer(t, map[string]string{"/api/v1/Events": `[{"Id":"1"}]`})
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
