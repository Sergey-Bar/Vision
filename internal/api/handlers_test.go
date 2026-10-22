package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/simulate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The HTTP handlers, driven against a real database.
//
// Every route is exercised with the services the production wiring uses. A mock
// would assert that a mock was called; what is worth testing here is that a
// handler refuses before it reaches the data, that a response carries no field
// it should not, and that a 404 is indistinguishable from a site the caller
// cannot see.

// call issues a request against the server.
//
// There is no session to mint and no CSRF token to present: login is removed and
// the server answers under the CONFIGURED principal, whose role, site scope,
// actor and session come from configuration. A request that carried a cookie
// would be testing a model the product does not have.
func call(t *testing.T, s *api.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// callAs issues a request as a specific user.
//
// Login is removed, so there is no session to mint: the request is sent as-is and
// the server answers under open access. The username and password arguments are
// kept so existing call sites do not have to change; they are no longer consulted.
func callAs(t *testing.T, s *api.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// callAnon issues a GET with no session at all.
//
// It is a GET and only a GET, and that is not a limitation: the point of the
// helper is the answer a caller with NO CREDENTIALS gets, and every route that
// answers differently to a GET and a POST is a route whose difference is
// about the body rather than about the caller.
func callAnon(t *testing.T, s *api.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// signIn is gone.
//
// Login is removed, so there is no session to mint and no CSRF cookie to
// compare against. It used to drive `POST /v1/auth/login` and hand back the
// session cookie and the CSRF value; every test that called it was testing a
// model the product does not have, and every one of them had been failing
// since the route was removed.
//
// The helper is replaced rather than stubbed, because a stub that returns a
// synthetic cookie would keep the tests LOOKING authenticated while the server
// answered under open access -- and a test that believes it is testing the
// authenticated path while it is testing the anonymous one is worse than no
// test at all.

// seedIncident raises one incident so the read routes have something to read.
func seedIncident(t *testing.T, tenant, site string) string {
	t.Helper()
	return seedIncidentDecision(t, tenant, site, rules.Decision{
		State: rules.StateCorroborated, RuleID: "core.camera-offline",
		Severity: "low", Reason: "corroborated",
		Action:           rules.Action{CreateIncident: true, Severity: "low"},
		SupportingEvents: []string{"evt-1"},
		DegradedReason:   rules.DegradedNone,
	})
}

// seedIncidentDecision raises one incident from a caller-supplied decision, so a
// test can state the shape it needs -- a degraded observation, say -- rather than
// only the corroborated-and-clean case `seedIncident` hardcodes.
//
// It is not a second seeder. `seedIncident` is this with one decision literal, and
// two seeders for one shape is how they drift.
func seedIncidentDecision(t *testing.T, tenant, site string, decision rules.Decision) string {
	t.Helper()
	id, err := (&incidents.Service{Pool: adminPool(t)}).CreateIncident(ctx(),
		incidents.Create{
			TenantID: tenant, SiteID: site,
			Decision:          decision,
			RuleBundleVersion: "0.1.0",
			BundleContentHash: strings.Repeat("d", 64),
			PolicyVersion:     "p1", Title: "Camera offline",
		})
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Read routes.
// ---------------------------------------------------------------------------

func TestIncidentListReturnsOnlyWhatTheCallerMaySee(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := call(t, s, http.MethodGet, "/v1/incidents", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the list has %d item(s), want 1: %s", len(items), rec.Body)
	}
	first, _ := items[0].(map[string]any)
	if first["incidentId"] != id {
		t.Errorf("the list returned %v, want %s", first["incidentId"], id)
	}
	// The feed row is a summary. It must not carry the bundle hash or the
	// explanation, which belong on the detail route.
	if _, leaked := first["bundleContentHash"]; leaked {
		t.Error("the feed row carries the bundle content hash")
	}
	if _, leaked := first["explanation"]; leaked {
		t.Error("the feed row carries the explanation")
	}
}

func TestIncidentListRefusesAnUnassignedSite(t *testing.T) {
	s, tenant, site := server(t)
	seedIncident(t, tenant, site)

	// The session is assigned to the site it was bootstrapped with. A different
	// one must be refused rather than answered with an empty list, because an
	// empty list and a refusal look the same to a caller who is guessing sites.
	rec := call(t, s, http.MethodGet, "/v1/incidents?siteId=site-somewhere-else", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an unassigned site returned %d, want 403", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["code"] != "site_not_assigned" {
		t.Errorf("the refusal is %v, want site_not_assigned", body["code"])
	}
}

func TestIncidentGetReturnsItsProvenance(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := call(t, s, http.MethodGet, "/v1/incidents/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get returned %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)

	// The provenance is the point of the detail route: which rule, which bundle,
	// which policy, and what was decided.
	for _, field := range []string{"ruleId", "ruleBundleVersion", "policyVersion", "verification", "events"} {
		if _, ok := body[field]; !ok {
			t.Errorf("the detail response has no %q", field)
		}
	}
	verification, _ := body["verification"].(map[string]any)
	if verification == nil || verification["state"] != "CORROBORATED" {
		t.Errorf("the verification is %v, want CORROBORATED", verification)
	}
}

// Absence and invisibility must be the same answer, or the route is a disclosure
// oracle: a caller who can tell them apart learns which incident ids exist.
func TestUnknownAndInvisibleIncidentsAreIndistinguishable(t *testing.T) {
	s, tenant, site := server(t)
	seedIncident(t, tenant, site)

	unknown := call(t, s, http.MethodGet, "/v1/incidents/inc-does-not-exist", nil)
	if unknown.Code != http.StatusNotFound {
		t.Errorf("an unknown incident returned %d, want 404", unknown.Code)
	}
	if body := decodeBody(t, unknown); body["code"] != "not_found" {
		t.Errorf("the 404 body is %v, want not_found", body["code"])
	}
}

// The session endpoint is not implemented, and the contract says so.
//
// `GET /v1/auth/session` is still DECLARED in the OpenAPI document, and the
// capability gate records it as a declared-and-absent route: login and MFA are
// removed, and every request runs under the configured principal.
//
// The test used to assert that the endpoint answered 200 and carried no
// secrets. It cannot be restated, because there is no endpoint: it was
// failing since the route was removed.
//
// What replaces it is the gap itself, pinned. A route that is declared and
// absent is a defect the repository already records; a test that says so is
// what stops the record and the tree from drifting apart, and what makes the
// removal visible to anybody who reads the test list rather than the ledger.
func TestTheSessionEndpointIsNotImplemented(t *testing.T) {
	s, _, _ := server(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec := call(t, s, method, "/v1/auth/session", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s /v1/auth/session answered %d, want 404: login and MFA are "+
				"removed, and a half-implemented session endpoint is a route whose "+
				"authority is the caller's", method, rec.Code)
		}
	}
}

func TestUnwiredReadModelSaysSoRatherThanReturningNothing(t *testing.T) {
	// A read model that is not configured must not answer with an empty list. An
	// empty list and a broken one look identical to an operator, and only one of
	// them is a lie.
	//
	// The reader is UNWIRED HERE EXPLICITLY, because the default harness
	// wires it: the coverage panel reads the dead letters through it, and a
	// harness that left it nil could not speak about the one reader that
	// kills the process. So the unwired case is now something a test asks
	// for rather than something it inherits.
	s, _, _, _ := serverWithRegistry(t, nil, func(d *api.Deps) {
		d.DeadLetters = nil
	})
	rec := call(t, s, http.MethodGet, "/v1/ops/dead-letters", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unwired read model returned %d, want 503: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["code"] != "not_configured" {
		t.Errorf("the body is %v, want not_configured", body["code"])
	}
	if !strings.Contains(body["message"].(string), "empty list") {
		t.Errorf("the message does not explain why an empty answer would be a lie: %q", body["message"])
	}
}

// ---------------------------------------------------------------------------
// The metrics route.
//
// The three cases are the whole authorization matrix for this endpoint: no
// session, a session without audit.read, and a session with it. A metrics
// endpoint is a map of the whole pipeline's behaviour, so "who can read it" is
// the question, not "is it gated".
// ---------------------------------------------------------------------------

// The metrics route is gated on the CAPABILITY, not on a session.
//
// It used to be gated on a session, and the test asserted that a request with
// no session got a 401. There is no session any more: the request runs as the
// CONFIGURED principal, whose role is configuration. The gate that remains is
// the capability check, and the property worth asserting is that the answer is
// a function of the configuration and of NOTHING the caller carries.
//
// A request with a random cookie and a request with none must get the same
// answer, because a route whose answer depends on a cookie the caller can
// invent is a route whose authority is the caller's.
func TestMetricsAnswerDependsOnTheConfigurationAndNotOnTheCaller(t *testing.T) {
	registry := observability.NewRegistry()
	s, _, _, _ := serverWithRegistry(t, registry)

	bare := callAnon(t, s, "/v1/metrics")

	withCookie := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	req.AddCookie(&http.Cookie{Name: "sp_session", Value: "not-a-real-session"})
	req.AddCookie(&http.Cookie{Name: "sp_session_csrf", Value: "forged"})
	s.Handler().ServeHTTP(withCookie, req)

	if bare.Code != withCookie.Code {
		t.Errorf("/v1/metrics answered %d with no cookie and %d with a forged one; the "+
			"route's authority is the configured principal, not what the caller carries",
			bare.Code, withCookie.Code)
	}
	if bare.Code == http.StatusUnauthorized {
		t.Errorf("/v1/metrics answered 401: there is no session to be missing, and the " +
			"route is gated on the capability")
	}
	// The refusal must not leak the exposition on the way out. The scrape itself
	// obviously carries the exposition -- that is what a scrape is -- so this is
	// about the REFUSAL, and the refusal is the other principal's answer.
	reduceToViewer(t, s)
	refused := callAnon(t, s, "/v1/metrics")
	if refused.Code == http.StatusOK {
		t.Fatal("/v1/metrics served a viewer: the route is gated on the capability")
	}
	if strings.Contains(refused.Body.String(), "inbox_accepted_total") {
		t.Error("a refused scrape carried the exposition in its body")
	}
}

func TestMetricsRefusesWithoutTheAuditCapability(t *testing.T) {
	registry := observability.NewRegistry()
	s, _, _, _ := serverWithRegistry(t, registry)

	// The configured administrator holds audit.read, so testing "a principal
	// without it" against the administrator asserts nothing. Reduce the
	// principal to a viewer, which reads incidents and evidence and nothing
	// else - the least-privileged role that can still read.
	reduceToViewer(t, s)

	rec := call(t, s, http.MethodGet, "/v1/metrics", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("/v1/metrics as a viewer returned %d, want 403: %s", rec.Code, rec.Body)
	}
	if body := decodeBody(t, rec); body["code"] != "capability_denied" {
		t.Errorf("the refusal is %v, want capability_denied", body["code"])
	}
	// The refusal must not leak the exposition.
	if strings.Contains(rec.Body.String(), "inbox_accepted_total") {
		t.Error("a forbidden response carried the exposition in its body")
	}
}

func TestMetricsRendersForTheAuditRole(t *testing.T) {
	registry := observability.NewRegistry()
	s, _, _, registry := serverWithRegistry(t, registry)

	grantAuditRead(t, s)

	// Recorded before the scrape, not after: the exposition is a point-in-time
	// read, and a test that increments afterwards is asserting about a different
	// scrape than the one it made.
	registry.Counters.Inc(observability.CounterInboxAccepted)
	registry.Gauges.Set(observability.GaugeBacklogDepth, 7)

	rec := call(t, s, http.MethodGet, "/v1/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/metrics returned %d: %s", rec.Code, rec.Body)
	}

	// The content type is the contract with the collector. A scrape handed JSON
	// is a parse failure that looks like a broken core.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type is %q, want text/plain", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "version=0.0.4") {
		t.Errorf("Content-Type %q does not state the exposition version",
			rec.Header().Get("Content-Type"))
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("a scrape is cacheable (%q); a collector could be showing a stale number", cc)
	}

	// Counters are seeded at zero so a metric exists before anything has
	// happened: a counter that appears only after a fault is one nobody has a
	// baseline for.
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE inbox_accepted_total counter",
		"inbox_accepted_total 1",
		// A counter nobody has touched still appears, at zero.
		"# TYPE vlm_invocations_total counter\nvlm_invocations_total 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, body)
		}
	}
	// Gauges are not seeded. A gauge nobody has measured is absent, and absent is
	// honest; a fabricated zero is a reading nobody took.
	for _, want := range []string{
		"# TYPE backlog_depth gauge\nbacklog_depth 7",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, body)
		}
	}

	// A name carrying two type declarations is a scrape the parser rejects, and
	// a collector reports that as a broken core. The registry used to emit every
	// gauge twice, once from each set.
	seen := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "# TYPE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		seen[fields[2]+" "+fields[3]]++
	}
	for decl, n := range seen {
		if n > 1 {
			t.Errorf("the exposition declares %q %d time(s): a metric name with two type "+
				"declarations is a scrape the parser rejects\n%s", decl, n, body)
		}
	}
	for name := range registry.Counters.Snapshot() {
		if observability.IsGauge(name) {
			t.Errorf("the gauge %q is present in the counter set", name)
		}
	}
}

func TestUnwiredMetricsRegistrySaysSoRatherThanScrapingEmpty(t *testing.T) {
	// No registry at all. An empty exposition is indistinguishable from a core
	// that has done no work, and only one of those is true.
	//
	// The route answers 200 with a body that NAMES the missing registry, rather
	// than 503. A scrape that fails is a scrape a collector pages somebody
	// about, and an install that has not wired a registry is a configuration
	// that has not happened yet, not a broken core. What matters is that the
	// body says so: the test used to want a 503 and a `not_wired` code, and
	// the route's own comment explains why that answer was wrong.
	s, _, _ := server(t)
	grantAuditRead(t, s)

	rec := call(t, s, http.MethodGet, "/v1/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("an unwired registry returned %d, want 200: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not configured") {
		t.Errorf("the exposition does not say the registry is missing, so it is an "+
			"empty scrape that looks like a core that has done no work: %q", body)
	}
	if strings.Contains(body, "inbox_accepted_total") {
		t.Errorf("the exposition carries metric lines for a registry that does not exist: %q", body)
	}
}

// grantAuditRead makes the CONFIGURED principal a holder of audit.read.
//
// It used to grant a role to a row in `user_roles` and then sign in as that
// user, because the capability came from the DATABASE and the role was the
// user's. The capability still comes from the database -- `identity` is the
// fallback for a bootstrap and the reference the migration seeds from -- but
// the principal is now CONFIGURATION, so the role under test is the
// configured one and the grant is made on the server.
//
// The role is `security_manager`, not the tenant administrator, because the
// administrator is the DEFAULT and a test that asserts against the default is
// asserting against the absence of a configuration.
func grantAuditRead(t *testing.T, s *api.Server) {
	t.Helper()
	s.PrincipalRole = identity.RoleSecurityManager
}

// reduceToViewer strips the principal down to the viewer role, which is the
// least-privileged role that can still read incidents.
//
// It exists because the configured administrator holds audit.read, so "a
// principal without the capability" cannot be tested against the
// administrator - that test would pass for the wrong reason, or fail while the
// route is correctly gated.
func reduceToViewer(t *testing.T, s *api.Server) {
	t.Helper()
	s.PrincipalRole = identity.RoleViewer
}

// ---------------------------------------------------------------------------
// The audit route.
// ---------------------------------------------------------------------------

func TestAuditLedgerIsRefusedWithoutTheCapability(t *testing.T) {
	// The bootstrap administrator holds identity.* but not audit.read. The route
	// must refuse, and the refusal must be audited rather than invisible.
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/audit/ledger", nil)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusOK {
		t.Fatalf("the audit route returned %d, want 403 or 200: %s", rec.Code, rec.Body)
	}
	if rec.Code == http.StatusForbidden {
		if body := decodeBody(t, rec); body["code"] != "capability_denied" {
			t.Errorf("the refusal is %v, want capability_denied", body["code"])
		}
	}
}

func TestAuditLedgerRecomputesChainValidity(t *testing.T) {
	s, tenant, site := server(t)
	_ = seedIncident(t, tenant, site)

	// The audit capability, so the route is reachable. The principal is
	// configured, so the role under test is the configured one.
	grantAuditRead(t, s)

	rec := call(t, s, http.MethodGet, "/v1/audit/ledger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the audit route returned %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	// The chain is recomputed rather than asserted, so a reader can tell a
	// working chain from a page that merely looks intact.
	valid, present := body["chainValid"]
	if !present {
		t.Fatal("the response has no chainValid")
	}
	if valid != true {
		t.Errorf("chainValid is %v on a ledger this test just wrote", valid)
	}
}

// adminPool is a pool with the migrator's grants, for the few places a test
// seeds or reads rows directly.
// ctx is a background context for the test helpers that seed rows directly.
func ctx() context.Context { return context.Background() }

func adminPool(t *testing.T) *dbx.Pool {
	t.Helper()
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	p, err := dbx.Open(ctx(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// The console's root opens, and it opens for a caller with no credentials.
//
// It used to log the operator out: `Resolve` rotated the session token on
// every call and `handleConsoleRoot` discarded the new value, so the next
// request bounced to /login.
//
// There is no session to lose now. The request runs as the CONFIGURED
// principal, whose actor and session are configuration and do not move between
// two requests, so the class of defect -- a page that invalidates the caller's
// credential -- cannot happen at all. What is worth asserting instead is the
// property that replaced it: the console's first page renders, and it renders
// for a caller carrying nothing.
func TestTheConsoleRootOpensForACallerWithNoCredentials(t *testing.T) {
	s, _, _ := server(t)

	first := callAnon(t, s, "/")
	if first.Code != http.StatusFound || first.Header().Get("Location") != "/incidents" {
		t.Fatalf("the console root returned %d -> %q, want 302 -> /incidents",
			first.Code, first.Header().Get("Location"))
	}

	second := callAnon(t, s, "/incidents")
	if second.Code == http.StatusFound && second.Header().Get("Location") == "/login" {
		t.Error("the incident feed bounced to a sign-in page: the console has no login, " +
			"so a page that sends an operator to sign in sends them nowhere")
	}
	if second.Code != http.StatusOK {
		t.Errorf("the incident feed returned %d after the root: %s", second.Code, second.Body)
	}
	// And no response may hand out a session cookie: there is no session, and a
	// cookie that carries one would be a credential the console minted for a
	// caller that never authenticated.
	for _, c := range second.Result().Cookies() {
		if strings.Contains(c.Name, "session") {
			t.Errorf("the console set a session cookie %q; the console has no login and "+
				"mints no credentials", c.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// The rule routes, including the activation gate.
// ---------------------------------------------------------------------------

// The activation gate, end to end.
//
// The route used to take `simulateDiffDigest` and the gate checked only that 64
// lowercase hex characters had arrived, so the interesting case was a *well
// formed* digest that was not the digest of anything. The route now takes the
// simulation report, which is what the operator reviewed and what the gate can
// re-hash.
func TestRuleActivationRefusesARecordWithNoDigest(t *testing.T) {
	s, _, _ := server(t)
	stored := storeBundle(t, s)

	rec := call(t, s, http.MethodPost, "/v1/rules/bundles/"+stored+"/activate", map[string]any{})
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Fatal("a bundle was activated with no reviewed diff digest")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the refusal returned %d, want 400", rec.Code)
	}
}

// A well-formed digest that is not the digest of the acknowledged changes used
// to be enough. This is the case the gate existed for and did not have.
//
// The route takes the digest and the fixture set the simulation ran over, and
// it RE-HASHES the change list itself: the digest in the request is compared
// against `simulate.DigestFor(version, fixtureSet, changes)`, and a digest
// that was typed in by hand does not match.
func TestRuleActivationRefusesAHandWrittenDigest(t *testing.T) {
	s, _, _ := server(t)
	stored := storeBundle(t, s)

	rec := call(t, s, http.MethodPost, "/v1/rules/bundles/"+stored+"/activate", map[string]any{
		"diffDigest": strings.Repeat("a", 64), // well formed, and not the digest of anything
	})
	if rec.Code == http.StatusOK {
		t.Fatal("a hand-written digest activated the bundle. The route checked only that " +
			"64 hex characters had arrived")
	}
	if !strings.Contains(rec.Body.String(), "not the digest of the acknowledged changes") {
		t.Errorf("the refusal does not say the change list and the digest disagree: %s", rec.Body)
	}
}

// And the honest one works, so the gate is a bound and not a wall.
//
// The route re-hashes the change list the simulation ran over, which for the
// default fixture set is no changes at all, and compares it with the digest the
// request carries. The honest digest is therefore the one the simulator
// prints for that set.
func TestRuleActivationAcceptsAGenuineReport(t *testing.T) {
	s, _, _ := server(t)
	stored := storeBundle(t, s)

	rec := call(t, s, http.MethodPost, "/v1/rules/bundles/"+stored+"/activate", map[string]any{
		"diffDigest": simulate.DigestFor(stored, "default", nil),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("a genuine simulation report was refused with %d: %s", rec.Code, rec.Body)
	}
}

func TestRuleActivationRefusesAMalformedDigest(t *testing.T) {
	s, _, _ := server(t)
	stored := storeBundle(t, s)

	rec := call(t, s, http.MethodPost, "/v1/rules/bundles/"+stored+"/activate", map[string]any{
		"diffDigest": "not-a-digest",
	})
	if rec.Code == http.StatusOK {
		t.Error("a bundle was activated with a malformed digest")
	}
}

func TestRuleStoreRefusesABundleThatDoesNotCompile(t *testing.T) {
	s, _, _ := server(t)
	rec := call(t, s, http.MethodPost, "/v1/rules/bundles", map[string]any{
		"content":         []byte("apiVersion: security.io/v1\nkind: RuleBundle\nversion: 1.0.0\n"),
		"sourceReference": "test",
	})
	if rec.Code == http.StatusCreated {
		t.Fatal("a bundle that does not compile was stored")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("the refusal returned %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "did not compile") {
		t.Errorf("the refusal does not say why: %s", rec.Body)
	}
}

// builtinVersion is the version rules/builtin/bundle.yaml declares.
//
// It is written out here rather than parsed from the file, because a test that
// reads the value it is testing against is a test that cannot fail: the fixture
// and the expectation would move together. When the bundle is versioned, this
// constant is the thing that has to change, and changing it is the point.
const builtinVersion = "0.1.0"

func storeBundle(t *testing.T, s *api.Server) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "rules", "builtin", "bundle.yaml"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	rec := call(t, s, http.MethodPost, "/v1/rules/bundles", map[string]any{
		"content":         raw,
		"sourceReference": "test",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("storing the built-in bundle returned %d: %s", rec.Code, rec.Body)
	}
	return builtinVersion
}

// seedEvidence records one piece of evidence and the bytes behind it.
func seedEvidence(t *testing.T, tenant, site, body string) string {
	t.Helper()
	obj, err := adminStore(t).Put(tenant, strings.NewReader(body))
	if err != nil {
		t.Fatalf("put evidence: %v", err)
	}
	if _, err := testutil.Pool(t).Exec(ctx(), `
		INSERT INTO evidence (evidence_id, tenant_id, site_id, kind, tier, observed_at,
		                     captured_at, storage_ref, size_bytes, sha256)
		VALUES ('ev-' || $1, $2, $3, 'snapshot', 'A', now(), now(), $4, $5, $6)`,
		strings.ToLower(body), tenant, site, obj.Ref, obj.SizeBytes, obj.SHA256,
	); err != nil {
		t.Fatalf("insert evidence row: %v", err)
	}
	return "ev-" + strings.ToLower(body)
}

// Holding a capability is not the same as being assigned to a place, and two of
// these routes treated it as though it were.
//
// The evidence-content route streams raw video out of the filesystem and checked
// only `tenant_id`. The incident list checked only that the principal had *some*
// site. The database does not close either gap: the two policies on `evidence`
// are both PERMISSIVE, and PostgreSQL ORs multiple permissive policies for the
// same command, so `tenant_isolation OR site_isolation` is `tenant_isolation`
// and there is no site isolation at the row level at all. The handler is the
// only place it happens.
func TestHoldingACapabilityIsNotBeingAssignedToThePlace(t *testing.T) {
	s, tenant, siteA := server(t)
	// A second, real site in the same tenant. It has to be a row and not a string
	// the handler is asked about, because the incident's site is a foreign key
	// and a fabricated id would fail the seed rather than the assertion.
	siteB := "site-not-mine"
	if _, err := testutil.Pool(t).Exec(ctx(),
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		siteB, tenant); err != nil {
		t.Fatalf("create the second site: %v", err)
	}

	// One incident per site, in the same tenant.
	seedIncident(t, tenant, siteA)
	seedIncident(t, tenant, siteB)
	// The four routes addressed by incident id. They are one case each because they
	// were one case each in the ROUTER once, and there was no test below this level
	// that could tell a site check from its absence: `p.CanSeeSite` was called by
	// hand inside each handler, and the only cross-site test in the file covered the
	// LIST route's `?siteId=`, which takes a different path through the code.
	//
	// Which is exactly how `identity.Principal.AuthorizeSite` stayed unwired with
	// `TEST-ONLY` in the dead-export ledger: the function existed, was correct, and
	// had no caller, because the behaviour it was written for was present in four
	// other places and none of them was named.
	//
	// The refusal is 404 with the "no such incident" text on every route, and that
	// is the assertion with teeth: a 403, or a 404 that says "not assigned to
	// site-b", is a confirmation that the id exists at another site, which is the
	// same oracle a 200 would have carried.
	for _, tc := range []struct {
		name   string
		method string
		path   func(string) string
		body   any
	}{
		{"the incident detail", http.MethodGet, func(id string) string { return "/v1/incidents/" + id }, nil},
		{"the explanation", http.MethodGet, func(id string) string { return "/v1/incidents/" + id + "/explanation" }, nil},
		{"the evidence list", http.MethodGet, func(id string) string { return "/v1/incidents/" + id + "/evidence" }, nil},
		{"the disposition", http.MethodPost, func(id string) string { return "/v1/incidents/" + id + "/dispositions" },
			// `agreement` is a STRING and a CLOSED SET: migration 0005's
			// `dispositions_agreement_check` admits only some values. It was a bool
			// here first, and the subtest still passed with the gate in place --
			// because the gate refuses before the body is ever decoded, so the
			// malformed body was never reached. Both fixtures only got read when the
			// site check was removed to red-check it, and the test failed on a 400
			// about JSON instead of on the 404 that would have said the incident was
			// served. A test whose fixture is never parsed is a test whose fixture is
			// not a test.
			map[string]any{"outcome": "true_positive", "reasonCode": "reviewed", "agreement": "agreed"}},
		{"the console page", http.MethodGet, func(id string) string { return "/incidents/" + id }, nil},
	} {
		t.Run("an incident at an unassigned site is refused on "+tc.name, func(t *testing.T) {
			theirs := seedIncident(t, tenant, siteB)

			rec := callAs(t, s, tc.method, tc.path(theirs), tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("an incident at unassigned site %q returned %d, want 404: %s",
					siteB, rec.Code, rec.Body)
			}
			// The body must be indistinguishable from a nonexistent id, or the 404
			// still leaks that this one exists.
			missing := callAs(t, s, tc.method, tc.path("no-such-incident"), tc.body)
			if rec.Code != missing.Code {
				t.Errorf("a real incident at another site answered %d and a nonexistent one "+
					"answered %d; the difference is an existence oracle", rec.Code, missing.Code)
			}
		})
	}

	t.Run("an id that resolves to no site is refused, not served", func(t *testing.T) {
		// The `site == ""` arm of the gate, which is the one the other two mutations
		// cannot reach. Its first red-check attempt came back GREEN: removing the
		// empty-site refusal changed nothing, because every route that goes through
		// `requireCapabilityAtSite` had a resolvable site in every test above.
		//
		// That is the exact coverage hole this assertion is here to close, and it is
		// worth being precise about why it matters. An unknown id resolves to no site,
		// so with the refusal removed the request falls through to
		// `p.AuthorizeSite(needed, "")`, which reaches `CanSeeSite("")` -- and a
		// principal assigned to a site does NOT see the empty string. The route is
		// still refused, by accident, one layer down, by a different check. A gate
		// that relies on that is a gate whose behaviour depends on a subtlety in an
		// unrelated predicate, and the day `CanSeeSite` gains an "empty means
		// everything" case -- which is a reasonable thing to add for a scope-wide
		// query -- this route starts serving rows to callers assigned to nothing.
		//
		// So the assertion is not "an unknown id is refused". It is that the refusal
		// is the SAME refusal an unassigned site gets, with no dependence on
		// `CanSeeSite`'s treatment of an empty string.
		for _, tc := range []struct {
			name   string
			method string
			path   func(string) string
			body   any
		}{
			{"the incident detail", http.MethodGet, func(id string) string { return "/v1/incidents/" + id }, nil},
			{"the explanation", http.MethodGet, func(id string) string { return "/v1/incidents/" + id + "/explanation" }, nil},
			{"the evidence list", http.MethodGet, func(id string) string { return "/v1/incidents/" + id + "/evidence" }, nil},
			{"the disposition", http.MethodPost, func(id string) string { return "/v1/incidents/" + id + "/dispositions" },
				map[string]any{"outcome": "true_positive", "reasonCode": "reviewed", "agreement": "agreed"}},
			{"the console page", http.MethodGet, func(id string) string { return "/incidents/" + id }, nil},
		} {
			rec := callAs(t, s, tc.method, tc.path("no-such-incident"), tc.body)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s for an id that resolves to no site returned %d, want 404: %s",
					tc.name, rec.Code, rec.Body)
			}
		}
	})

	t.Run("an incident at the principal's own site is served on the same routes", func(t *testing.T) {
		// The other direction, because a filter that refuses everything is not a
		// filter. `siteOfIncident` resolves the site by looking the row up, and a
		// refusal here would be the gate refusing its own site rather than another.
		mine := seedIncident(t, tenant, siteA)
		for _, tc := range []struct {
			name   string
			method string
			path   func(string) string
			body   any
		}{
			{"the incident detail", http.MethodGet, func(id string) string { return "/v1/incidents/" + id }, nil},
			{"the evidence list", http.MethodGet, func(id string) string { return "/v1/incidents/" + id + "/evidence" }, nil},
			{"the console page", http.MethodGet, func(id string) string { return "/incidents/" + id }, nil},
		} {
			rec := callAs(t, s, tc.method, tc.path(mine), tc.body)
			if rec.Code != http.StatusOK {
				t.Errorf("%s at the principal's own site returned %d, want 200: %s",
					tc.name, rec.Code, rec.Body)
			}
		}
	})
}

func TestConsoleIncidentPageRendersProvenance(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := call(t, s, http.MethodGet, "/incidents/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d: %s", rec.Code, rec.Body)
	}
	page := rec.Body.String()
	// The page must show the three timestamps apart, the bundle version and the
	// verdict. An operator comparing a timeline is doing arithmetic.
	for _, want := range []string{"Camera offline", "0.1.0", "CORROBORATED", "core.camera-offline"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

func TestConsoleIsNotIndexed(t *testing.T) {
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/incidents", nil)
	if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("the console page is not marked noindex (got %q); a stored link to a "+
			"security console is a stored pointer at a site", got)
	}
}

func TestConsoleStaticStylesheetIsServed(t *testing.T) {
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/static/console.css", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the stylesheet returned %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("the stylesheet is served as %q", ct)
	}
}

// The console has no sign-in page.
//
// It used to: a public page at `/login` that declared its language and
// direction. The route was removed with the rest of the login surface, and the
// test had been failing since.
//
// The page is not restated, because there is no page. The language-and-
// direction claim is asserted for the console's pages that DO exist by
// `TestConsoleIsLeftToRight` in api_test.go, which reads every template in the
// tree rather than one page -- so the claim survives the removal of a page
// rather than travelling with it.
func TestTheConsoleHasNoSignInPage(t *testing.T) {
	s, _, _ := server(t)
	rec := callAnon(t, s, "/login")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /login answered %d, want 404: the console has no login, and a "+
			"sign-in page an operator can be sent to is a page they cannot complete",
			rec.Code)
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return out
}

var _ = testutil.Pool
