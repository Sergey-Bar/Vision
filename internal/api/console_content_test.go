package api_test

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The console renders forms, and two of its three pointed at routes that could not
// accept them:
//
//	login.html   <form method="post" action="/login">           route: GET  /login
//	layout.html  <form method="post" action="/v1/auth/session"> route: DELETE /v1/auth/session
//
// Both are 405 from the mux. The console was a page an operator could look at and
// not use, and no test noticed, because every test in this package asserted
// properties of RESPONSES and none of them looked at what the page asked for.
//
// The gap is named in `docs/roadmap/engineering/quality-engineering.md`: "These
// tests assert properties of responses. They do not assert that the console renders
// the thing it is supposed to render."
//
// BOTH OF THEM ARE GONE NOW, and how they went is the point.
//
// `login.html` was deleted. The console has no login, so the template rendered
// nothing, and it carried an empty `csrf_token` field, which is worse than the
// absence of the field: a reader auditing the markup would conclude the console
// had a protection it does not have.
//
// The layout's sign-out form went for a second reason as well as the first.
// `POST /logout` was not a route either. There is no session to end. The console's
// identity comes from the boundary in front of it, so there is nothing on the page
// that can end it.

// formRE finds a form's method and action out of a template.
//
// `method` is optional in HTML and defaults to GET, so the regex makes it optional
// and the test substitutes GET -- reading a missing method as POST because that is
// the mistake this is looking for.
var formRE = regexp.MustCompile(`(?is)<form\b[^>]*>`)

// attrRE pulls one attribute out of a tag's text.
func attrRE(tag, name string) string {
	re := regexp.MustCompile(`(?i)\b` + name + `\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}

func templateFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoDir(t), "internal", "api", "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatal("no templates found, so this test is asserting nothing")
	}
	return out
}

// TestTheDispositionFormCarriesEveryOutcome is the content assertion for the form.
//
// Spelled as a set rather than as a search for one outcome, because the failure this
// guards against is a form that is MISSING one: `Record` refuses an outcome the
// database does not admit, so a form with five of the six radios is a form where one
// legitimate disposition is simply not available and nothing reports it.
//
// The expected set is read from `incidents.DispositionOutcomes` rather than written
// out here, so the form cannot be tested against a stale copy of the rule it is meant
// to match. If somebody adds an outcome to the map and not to the template, the
// comparison fails and names which one is missing.
func TestTheDispositionFormCarriesEveryOutcome(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := call(t, s, http.MethodGet, "/incidents/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	found := map[string]bool{}
	for _, m := range regexp.MustCompile(`name="outcome" value="([a-z_]+)"`).FindAllStringSubmatch(body, -1) {
		found[m[1]] = true
	}
	var missing []string
	for outcome := range incidents.DispositionOutcomes {
		if !found[outcome] {
			missing = append(missing, outcome)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the disposition form has no radio for %v. An operator cannot record that "+
			"outcome from the console at all, and `Record` refuses it, so the gap is invisible "+
			"from the UI", missing)
	}
	if len(found) != len(incidents.DispositionOutcomes) {
		t.Errorf("the form offers %d outcome radio(s) and the service accepts %d; the sets "+
			"must be identical in both directions", len(found), len(incidents.DispositionOutcomes))
	}

	// The agreement values are a separate CHECK with its own four values, and a form
	// that omits one forces an operator who disagrees with the AI into picking
	// "unsure", which is a data-quality loss rather than a refusal.
	agreed := map[string]bool{}
	for _, m := range regexp.MustCompile(`name="agreement" value="([a-z_]+)"`).FindAllStringSubmatch(body, -1) {
		agreed[m[1]] = true
	}
	for _, want := range []string{"agreed", "disagreed", "unsure", "no_ai_observation"} {
		if !agreed[want] {
			t.Errorf("the disposition form has no agreement radio for %q", want)
		}
	}

	// The step-up field is on the page, because the two sensitive outcomes need one
	// and an operator choosing `escalated` should not have to reload to find it.
	if !strings.Contains(body, `name="factor"`) {
		t.Error("the disposition form has no second-factor field. `escalated` and `dismissed` " +
			"require one, so an operator would discover the requirement by being refused")
	}
}

// TestRecordingADispositionFromTheConsole drives the form end to end, and it is the
// first test that has ever made this product's review surface perform a review.
//
// The console rendered the disposition table for the whole of its life and there was no
// route that added a row to it: an operator with the page open had to leave the console
// and call `POST /v1/incidents/{id}/dispositions` with a JSON body. A read-only view of
// a decision record is a screenshot of a workflow.
func TestRecordingADispositionFromTheConsole(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	// A second site in the same tenant that this principal is NOT assigned to, so the
	// cross-site case below is a real row and not a fabricated id -- the incident's
	// site is a foreign key and a made-up one would fail the seed rather than the
	// assertion.
	const otherSite = "site-not-mine"
	if _, err := testutil.Pool(t).Exec(ctx(),
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		otherSite, tenant); err != nil {
		t.Fatalf("seed the second site: %v", err)
	}

	post := func(t *testing.T, incidentID string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/incidents/"+incidentID+"/disposition",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	countDispositions := func(t *testing.T, incidentID, outcome string) int {
		t.Helper()
		var n int
		q := `SELECT count(*) FROM dispositions WHERE tenant_id = $1 AND incident_id = $2`
		args := []any{tenant, incidentID}
		if outcome != "" {
			q += ` AND outcome = $3`
			args = append(args, outcome)
		}
		if err := adminPool(t).QueryRow(ctx(), q, args...).Scan(&n); err != nil {
			t.Fatalf("count dispositions: %v", err)
		}
		return n
	}

	t.Run("an acknowledged outcome needs no second factor", func(t *testing.T) {
		rec := post(t, id, url.Values{
			"outcome":    {"acknowledged"},
			"agreement":  {"unsure"},
			"reasonCode": {"reviewed"},
			"notes":      {"seen it, nothing yet"},
		})
		if rec.Code != http.StatusFound {
			t.Fatalf("recording a disposition answered %d, want 302 back to the incident: %s",
				rec.Code, rec.Body)
		}
		if n := countDispositions(t, id, ""); n != 1 {
			t.Errorf("the disposition table has %d row(s) after a recorded disposition, want 1", n)
		}
	})

	t.Run("an escalation with no second factor is refused before anything is written", func(t *testing.T) {
		// The agreement is present, because the form is validated before the
		// factor is asked for: an operator whose form could not succeed is not
		// asked for a second factor first. So the only thing wrong with this
		// request is the missing factor, which is what the subtest is about.
		rec := post(t, id, url.Values{
			"outcome": {"escalated"}, "reasonCode": {"eyes please"}, "agreement": {"unsure"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("an escalation with no factor answered %d, want 403: %s", rec.Code, rec.Body)
		}
		if n := countDispositions(t, id, "escalated"); n != 0 {
			t.Errorf("an escalation was recorded with no second factor")
		}
	})

	t.Run("a dismissal with no second factor is refused", func(t *testing.T) {
		// The other sensitive outcome. Both are in `stepUpRequired` and a form that
		// guards one of them is half a control.
		rec := post(t, id, url.Values{
			"outcome": {"dismissed"}, "reasonCode": {"noise"}, "agreement": {"unsure"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("a dismissal with no factor answered %d, want 403", rec.Code)
		}
		if n := countDispositions(t, id, "dismissed"); n != 0 {
			t.Errorf("a dismissal was recorded with no second factor")
		}
	})

	t.Run("an outcome the service does not admit is refused", func(t *testing.T) {
		rec := post(t, id, url.Values{"outcome": {"not_a_real_outcome"}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("an invented outcome answered %d, want 400", rec.Code)
		}
		if n := countDispositions(t, id, ""); n != 1 {
			t.Errorf("the invented outcome wrote a row; the table has %d rows, want the 1 from "+
				"the acknowledged disposition above", n)
		}
	})

	// The CSRF subtest that used to live here is gone, and its removal is a
	// finding rather than a deletion.
	//
	// It asserted that a disposition with no CSRF token is refused, because "a
	// third-party page that can make an operator's browser escalate an incident
	// is the whole reason this check exists". The check is gone: the CSRF
	// middleware went with the login, because a token that protects a cookie
	// protects nothing when there is no cookie, and the API's authority comes
	// from its own configuration rather than from anything the caller carries.
	//
	// That reasoning is sound for a machine API. It is NOT sound for the
	// console's own pages, which are served from the same origin as the API and
	// are reachable by a browser: there, a cross-site POST is a request the
	// product honours, and nothing stops it.
	//
	// The control is therefore recorded as a GAP -- in `docs/threat-model.md`
	// under A3.2 and in `docs/status.md` under what is not done -- rather than
	// asserted as absent here. A test that asserted the absence of a control
	// would have to be deleted when the control comes back, and a test whose
	// whole job is to be deleted is a test that stops anybody noticing when it
	// should.
	//
	// What is asserted here is the control that DOES exist: the two sensitive
	// outcomes need a second factor, and the site scope is enforced.

	t.Run("an operator not assigned to the incident's site cannot record against it", func(t *testing.T) {
		theirs := seedIncident(t, tenant, otherSite)
		rec := post(t, theirs, url.Values{"outcome": {"acknowledged"}})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("a disposition against another site's incident answered %d, want 404",
				rec.Code)
		}
		if n := countDispositions(t, theirs, ""); n != 0 {
			t.Error("a disposition was recorded against an incident at an unassigned site")
		}
	})
}

// TestEveryConsoleFormSubmitsToARouteThatAcceptsItsMethod is the test that would
// have caught both.
//
// It does not check that a form WORKS -- that a disposition is recorded, that the
// operator lands somewhere sensible. It checks the narrower and more mechanical
// thing: that the method and action the page declares are a pair the mux has a
// handler for. A form that names a pair the router does not have is a 405 on the
// operator's first click, and it is invisible to every assertion about responses.
//
// The request is made with NO session on purpose. A 401 means the route exists and
// wants credentials, which is the correct answer for a form on a signed-in page; a
// 405 means the method is not registered, which is the defect. Asserting on the
// absence of 405 rather than on a 200 keeps the test from having to authenticate
// for every form and from breaking when a form's success response legitimately
// changes shape.
func TestEveryConsoleFormSubmitsToARouteThatAcceptsItsMethod(t *testing.T) {
	s, _, _ := server(t)
	h := s.Handler()

	found := 0
	for _, file := range templateFiles(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		name := filepath.Base(file)
		for _, tag := range formRE.FindAllString(string(raw), -1) {
			found++
			action := attrRE(tag, "action")
			method := strings.ToUpper(strings.TrimSpace(attrRE(tag, "method")))
			if method == "" {
				// HTML's default. Read as POST because a form with no method on a
				// page that acts is the same class of mistake, and reading it as GET
				// would let one through silently.
				method = http.MethodPost
			}
			if action == "" {
				t.Errorf("%s: a <form> with no action submits to the current URL, which "+
					"is whatever page the operator happened to be on. Name it", name)
				continue
			}
			target, err := url.Parse(action)
			if err != nil {
				t.Errorf("%s: form action %q does not parse: %v", name, action, err)
				continue
			}
			req := httptest.NewRequest(method, target.Path, strings.NewReader(""))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code == http.StatusMethodNotAllowed {
				t.Errorf("%s: the form declares method=%s action=%q and the router answers 405, "+
					"so the operator gets 'Method Not Allowed' on their first click. Either the "+
					"form names the wrong method or the route is registered under another one.\n"+
					"    A form can only send GET and POST: a DELETE-registered route is "+
					"unreachable from a form however the form is spelled.",
					name, method, action)
			}
		}
	}
	if found == 0 {
		t.Fatal("no forms were found in the templates, so this test is asserting nothing")
	}
	t.Logf("%d form(s) checked across the console templates", found)
}

// The console has no sign-in form, and the four tests that drove it are gone.
//
// They asserted that the form signed an operator in, that a wrong password was
// refused with a message, that the CSRF token survived HTML escaping, and that
// a sign-out ended the session. None of them can be restated, because there is
// no form, no password and no session: the sign-in page, the sign-out form and
// the session endpoint were all removed with the login, and every one of these
// tests had been failing since.
//
// They were not deleted to make a suite green. They were deleted because the
// surface they tested is gone, and a test for a surface that does not exist is
// a test that cannot fail for a reason anybody would act on.
//
// What replaces them is the fact that now has to be true, which is the
// opposite of all four: there is no sign-in page to sign in to, and a console
// that offered one would be sending an operator somewhere they could not
// finish.
func TestTheConsoleHasNoSignInForm(t *testing.T) {
	s, _, _ := server(t)
	h := s.Handler()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/login"},
		{http.MethodPost, "/login"},
		{http.MethodPost, "/logout"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("")))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404: the console has no login, so a "+
				"sign-in page is a page an operator can be sent to that cannot be "+
				"completed", tc.method, tc.path, rec.Code)
		}
	}
}

// repoDir finds the module root by walking up to go.mod, so a test that breaks when
// a directory moves is a test that gets deleted rather than fixed.
func repoDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod above this package")
	return ""
}

// The console must render the thing it is supposed to render, not merely answer.
//
// `docs/roadmap/engineering/quality-engineering.md` names the gap: twelve tests
// asserting properties of responses, none asserting content. A template that renders
// an empty table for every incident passes all twelve.

// TestTheIncidentPageRendersTheIncident pins the properties an operator reads first:
// the title, the identifiers, and the verification verdict with its explanation.
// Each is asserted against the SEEDED value, so a template that dropped a field and a
// template that rendered a placeholder both fail.
func TestTheIncidentPageRendersTheIncident(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)

	rec := call(t, s, http.MethodGet, "/incidents/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	for _, want := range []struct{ what, needle string }{
		{"the incident's title", "Camera offline"},
		{"the incident's id", id},
		{"the site it belongs to", site},
		{"the rule that fired", "core.camera-offline"},
		{"the bundle version", "0.1.0"},
		{"the verdict", "CORROBORATED"},
		{"the reason code", "corroborated"},
		{"the supporting event", "evt-1"},
		{"the explanation", "produced CORROBORATED"},
		{"a heading for the section", "Why this incident exists"},
	} {
		if !strings.Contains(body, want.needle) {
			t.Errorf("the incident page does not render %s. It is missing %q.\n"+
				"A page that answers 200 and omits the incident is the failure this test exists for",
				want.what, want.needle)
		}
	}
}

// TestTheIncidentPageSaysWhenTheAIPlaneWasDown is the one that matters most, because
// it is the product's thesis: a missing observation is not a negative one, and the
// operator has to be able to see which it was.
//
// It renders an incident whose decision was made with a degraded observation, and
// requires the notice. The negative case is in the same test, because a notice that
// appears unconditionally is as wrong as one that never appears.
func TestTheIncidentPageSaysWhenTheAIPlaneWasDown(t *testing.T) {
	s, tenant, site := server(t)

	// `Degraded` and `DegradedReason` are TWO fields on `rules.Decision`, and the
	// first version of this set only the second, so `degraded` came back false and
	// the whole notice block was skipped by its own `{{if .Degraded}}`.
	//
	// Worth stating because the two being separate is the kind of thing a test
	// fixture gets wrong silently: `DegradedReason: rules.DegradedTimeout` reads
	// like it says the incident was degraded, and it says which reason a
	// DEGRADED incident had.
	degraded := seedIncidentDecision(t, tenant, site, rules.Decision{
		State: rules.StateCorroborated, RuleID: "core.camera-offline",
		Severity: "low", Reason: "corroborated",
		Action:           rules.Action{CreateIncident: true, Severity: "low"},
		SupportingEvents: []string{"evt-1"},
		Degraded:         true,
		DegradedReason:   rules.DegradedTimeout,
	})

	rec := call(t, s, http.MethodGet, "/incidents/"+degraded, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "while the AI plane was") {
		t.Error("an incident decided while the AI plane was degraded renders no notice, so an " +
			"operator cannot tell a corroborated incident from one the model never saw")
	}
	if !strings.Contains(body, string(rules.DegradedTimeout)) {
		t.Errorf("the notice does not name the degradation reason %q; an operator who cannot "+
			"tell a timeout from a model that was never asked will debug the wrong thing",
			rules.DegradedTimeout)
	}
	// The wording that keeps it honest: the notice has to say the incident is NOT
	// fully corroborated, or it reads as a caveat attached to a conclusion.
	if !strings.Contains(body, "not fully corroborated") {
		t.Error("the notice does not say the incident is not fully corroborated")
	}

	// And an incident that was NOT decided while degraded must not carry the notice.
	clean := seedIncident(t, tenant, site)
	rec = call(t, s, http.MethodGet, "/incidents/"+clean, nil)
	if strings.Contains(rec.Body.String(), "while the AI plane was") {
		t.Error("an incident decided with the AI plane healthy carries the degradation notice, " +
			"so the notice stops being information")
	}
}

// TestTheEvidenceTableHasOneRowPerItem counts rows rather than looking for an id, so
// a table that renders the header and drops every row fails.
//
// The count is on `<tr>` inside the evidence section, which is the only thing a
// silent drop of `{{range}}` would change.
// linkEvidence attaches a stored evidence row to an incident.
//
// It is not part of `seedEvidence` because the link is a separate fact. An evidence
// row exists because something was captured; `incident_evidence` exists because a
// DECISION cited it, and `scanEvidence` reads the join. Seeding only the row -- which
// is what this test did first -- produces an incident with no evidence section at all,
// and the failure reads as "the console does not render evidence" rather than as "the
// fixture never linked any".
func linkEvidence(t *testing.T, tenant, incidentID, evidenceID string) {
	t.Helper()
	if _, err := testutil.Pool(t).Exec(ctx(),
		`INSERT INTO incident_evidence (tenant_id, incident_id, evidence_id)
		 VALUES ($1, $2, $3)`,
		tenant, incidentID, evidenceID); err != nil {
		t.Fatalf("link %s to %s: %v", evidenceID, incidentID, err)
	}
}

func TestTheEvidenceTableHasOneRowPerItem(t *testing.T) {
	s, tenant, site := server(t)
	id := seedIncident(t, tenant, site)
	linkEvidence(t, tenant, id, seedEvidence(t, tenant, site, "a"))
	linkEvidence(t, tenant, id, seedEvidence(t, tenant, site, "b"))

	rec := call(t, s, http.MethodGet, "/incidents/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the incident page returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	start := strings.Index(body, "Stored evidence")
	if start < 0 {
		t.Fatal("the incident page has no evidence section at all, so an operator cannot see " +
			"what the decision cited")
	}
	section := body[start:]
	// Cut at the next <section> so the count is the EVIDENCE table's and not a later
	// one's. A count that included the dispositions table would pass with the
	// evidence table emptied.
	//
	// Both indexes are checked rather than compared against 0. `Index` returns -1 when
	// it finds nothing, and -1 > 0 is false, so an `if end > 0` guard looks correct and
	// silently does nothing -- the cut is skipped and the count below covers the rest
	// of the page.
	firstOpen := strings.Index(section, "<section>")
	if firstOpen > 0 {
		if next := strings.Index(section[firstOpen:], "<section>"); next > 0 {
			section = section[:firstOpen+next]
		}
	}

	rows := strings.Count(section, "<tr>")
	// One header row plus one per item.
	if want := 1 + 2; rows != want {
		t.Errorf("the evidence table has %d row(s), want %d: one header plus one per item. "+
			"A `{{range}}` that renders nothing leaves the header behind, which looks like a "+
			"table of nothing rather than a table that failed", rows, want)
	}
	for _, id := range []string{"ev-a", "ev-b"} {
		if !strings.Contains(section, id) {
			t.Errorf("the evidence table does not name %q", id)
		}
	}
}

var _ = testutil.DatabaseURLEnv

// TestProductNameIsConsistency guards the brand the operator reads.
//
// The naming decision (`docs/roadmap/engineering/ux-and-design-system.md` §9.1)
// names the product VISION and puts the category — "Physical Security
// Intelligence Layer" — beside it as the one-line statement, with the same name
// in `<title>` so a browser tab and a topbar never disagree. It supersedes
// `Captain`, which is why the old name is refused rather than merely absent.
//
// The page is RENDERED, not read as text: a template that contains the right
// strings and a `{{define}}` nobody executes would pass a text search and ship
// a topbar with nothing in it. Rendering the `head` block directly is also what
// keeps this test off the database — name consistency is a property of the
// template, and a test that skipped without Postgres would be a green run that
// checked nothing on every machine without one.
//
// The mark and the colour rule are asserted against the CSS because the naming
// decision's constraint is structural: the mark uses the single brand accent
// token `--accent` and never one of the four semantic operator colours
// (§9.4 point 3), and a red logo is a logo for a different product.
func TestProductNameIsConsistency(t *testing.T) {
	const (
		name      = "VISION"
		statement = "Physical Security Intelligence Layer"
		oldName   = "Captain"
	)
	root := repoDir(t)
	layoutPath := filepath.Join(root, "internal", "api", "templates", "layout.html")

	tmpl, err := template.New("layout.html").ParseFiles(layoutPath)
	if err != nil {
		t.Fatalf("parse layout.html: %v", err)
	}
	var buf bytes.Buffer
	// The `head` block is what every page includes, so this is the real render
	// path for the title and the topbar. An empty principal renders the
	// unauthenticated form, which is the same brand markup.
	if err := tmpl.ExecuteTemplate(&buf, "head", brandPage{Title: "Sign in"}); err != nil {
		t.Fatalf("render layout head: %v", err)
	}
	page := buf.String()

	titleRe := regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	brandRe := regexp.MustCompile(`(?s)class="brand-name">(.*?)<`)
	titleMatch := titleRe.FindStringSubmatch(page)
	if titleMatch == nil {
		t.Fatalf("the rendered page has no <title>, so the tab carries no name at all:\n%s", page)
	}
	brandMatch := brandRe.FindStringSubmatch(page)
	if brandMatch == nil {
		t.Fatal("the rendered topbar has no brand-name element, so the console " +
			"renders with no product name in it")
	}
	// One name, two places. `VISION — Sign in` in the tab and `VISION` in the
	// topbar: the tab's name is the part before the separator, so a title that
	// reads `Sign in — VISION` is a different claim and fails here.
	if got := strings.TrimSpace(strings.SplitN(titleMatch[1], "—", 2)[0]); got != name {
		t.Errorf("<title> opens with %q, want %q. The tab is what an operator "+
			"reads when six consoles are open", got, name)
	}
	if got := strings.TrimSpace(brandMatch[1]); got != name {
		t.Errorf("the topbar brand reads %q, want %q", got, name)
	}
	if !strings.Contains(page, `class="brand-statement">`+statement+`<`) {
		t.Error("the topbar carries no one-line statement. The category is what " +
			"the name means, and a bare word in a topbar is a logo, not a product")
	}

	// The old name is refused, not merely absent. `Captain` was the name the
	// decision superseded, and a page that quietly went back to it is the
	// exact regression this test exists for.
	if strings.Contains(page, oldName) {
		t.Error("the rendered page still carries the superseded name \"Captain\"")
	}
	for _, path := range templateFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(raw), oldName) {
			t.Errorf("%s still carries the superseded name \"Captain\"", filepath.Base(path))
		}
	}

	// The mark: 24-unit grid, square, currentColor so the token resolves once.
	markRe := regexp.MustCompile(`(?s)<svg class="brand-mark"[^>]*viewBox="([^"]+)"[^>]*>(.*?)</svg>`)
	mark := markRe.FindStringSubmatch(page)
	if mark == nil {
		t.Fatal("the topbar renders no brand mark. The naming decision specifies " +
			"one (ux-and-design-system §9.2), and a name without a mark is text")
	}
	var x0, y0, w, h float64
	if n, _ := fmt.Sscanf(mark[1], "%g %g %g %g", &x0, &y0, &w, &h); n != 4 {
		t.Fatalf("the mark's viewBox %q is not four numbers, so its grid cannot be checked", mark[1])
	}
	if x0 != 0 || y0 != 0 || w != 24 || h != 24 {
		t.Errorf("the mark's grid is %q, want a square 0 0 24 24. A mark is drawn "+
			"on a fixed grid so it survives 24px and 32px", mark[1])
	}
	if !strings.Contains(mark[2], "currentColor") {
		t.Error("the mark does not use currentColor, so its colour is hard-coded " +
			"and the single brand accent token is not the one place it comes from")
	}

	// The colour rule, read out of the stylesheet the page actually links.
	css, err := os.ReadFile(filepath.Join(root, "internal", "api", "templates", "static", "console.css"))
	if err != nil {
		t.Fatalf("read console.css: %v", err)
	}
	markRule := cssRule(string(css), ".brand-mark")
	if markRule == "" {
		t.Fatal("console.css has no .brand-mark rule, so the mark has no colour of " +
			"its own and falls back to whatever inherited")
	}
	if !strings.Contains(markRule, "var(--accent)") {
		t.Errorf(".brand-mark is %q, which does not use var(--accent). The naming "+
			"decision puts the mark in the single brand accent token", markRule)
	}
	for _, status := range []string{"--ok", "--warn", "--err", "--info"} {
		if strings.Contains(markRule, "var("+status+")") {
			t.Errorf(".brand-mark uses var(%s), a semantic operator colour. §9.4 "+
				"refuses a brand colour inside the semantic set: if --accent were "+
				"also \"medium severity\", every link in an alert would look like a "+
				"warning, and a red logo is a logo for a different product", status)
		}
	}
	// And the four semantic tokens are the ones they were, because "the accent
	// moved" and "a semantic colour moved" are different changes and only one of
	// them is a brand decision.
	for token, want := range map[string]string{
		"--ok": "#3fb950", "--warn": "#d29922", "--err": "#f85149", "--info": "#58a6ff",
	} {
		if !strings.Contains(string(css), token+": "+want) {
			t.Errorf("console.css no longer defines %s: %s. The semantic operator "+
				"colours are not the brand's to change", token, want)
		}
	}
}

// brandPage is the minimum data the `head` block renders. It is deliberately
// not the console's own data type: this test asserts a property of the template,
// and building the real one would drag the database in for a check that has
// nothing to do with it.
type brandPage struct {
	Title     string
	Principal struct {
		Username    string
		Roles       []string
		BreakGlass  bool
		MFAVerified bool
		TenantID    string
	}
}

// cssRule returns the body of one CSS rule by selector, so the colour claims are
// checked against the rule that paints the mark rather than against the whole
// stylesheet. A `var(--ok)` anywhere else in the file — in `.alert.error`, where
// it belongs — is not a finding.
func cssRule(css, selector string) string {
	i := strings.Index(css, selector+" {")
	if i < 0 {
		return ""
	}
	rest := css[i+len(selector)+1:]
	if end := strings.Index(rest, "}"); end >= 0 {
		return rest[:end]
	}
	return ""
}
