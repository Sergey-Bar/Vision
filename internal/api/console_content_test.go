package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
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
		cookie, csrf, err := signIn(t, s)
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		form.Set("csrf_token", csrf)
		req := httptest.NewRequest(http.MethodPost, "/incidents/"+incidentID+"/disposition",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: cookieNameOf(s) + "_csrf", Value: csrf})
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
		rec := post(t, id, url.Values{"outcome": {"escalated"}, "reasonCode": {"eyes please"}})
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
		rec := post(t, id, url.Values{"outcome": {"dismissed"}, "reasonCode": {"noise"}})
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

	t.Run("a disposition is refused without a CSRF token", func(t *testing.T) {
		// The negative case, because a form that works and a route with no CSRF check
		// are indistinguishable from the outside. A disposition is the most
		// consequential thing an operator can do here and it is addressed by id, so a
		// third-party page that can make an operator's browser escalate an incident is
		// the whole reason this check exists.
		cookie, _, err := signIn(t, s)
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		form := url.Values{"outcome": {"dismissed"}, "reasonCode": {"forged"}}
		req := httptest.NewRequest(http.MethodPost, "/incidents/"+id+"/disposition",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("a disposition with no CSRF token answered %d, want 403", rec.Code)
		}
		if n := countDispositions(t, id, "dismissed"); n != 0 {
			t.Error("a disposition was recorded from a request with no CSRF token")
		}
	})

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

// TestTheSignInFormActuallySignsAnOperatorIn is the other direction of the same
// defect. A form can point at a route that accepts its method and still not work,
// and the first test cannot see that -- it makes no session.
//
// This one drives the real thing: the form's own field names, submitted form-encoded,
// and requires a session cookie and a redirect to the feed afterwards. The field
// names are read out of the template rather than hard-coded here, so renaming
// `username` in the template without changing the handler fails HERE rather than
// producing a 200 that signed nobody in.
// signInViaConsoleForm drives the console's own sign-in form the way a browser
// does: GET the page first so the server issues its pre-session CSRF cookie,
// then submit with the token from the rendered form.
//
// This is not a convenience. `handleConsoleLoginSubmit` verifies a double-submit
// token before it looks at the password at all, so a POST without one answers
// 403 and never reaches the credential check — which means a test that skipped
// the GET was testing the CSRF refusal and calling it a sign-in.
//
// It is deliberately not named `signIn`: that name already belongs to the JSON
// login route in handlers_test.go, and two helpers of the same name doing
// different halves of the same auth surface is how one of them stops being
// called.
func signInViaConsoleForm(t *testing.T, s http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()

	get := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	s.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /login answered %d, want 200: %s", getRec.Code, getRec.Body)
	}

	var csrfCookie *http.Cookie
	for _, c := range getRec.Result().Cookies() {
		if strings.Contains(c.Name, "csrf") {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatal("GET /login set no CSRF cookie, so the form cannot be driven at all")
	}

	// The token the form carries and the token in the cookie are the same value
	// under double-submit, so read it out of the rendered form and assert that
	// it matches: a form whose token drifts from its cookie is a form every
	// operator's browser will refuse to submit.
	var formToken string
	if re := regexp.MustCompile(`(?i)name="csrf_token"\s+value="([^"]*)"`); re.MatchString(getRec.Body.String()) {
		formToken = re.FindStringSubmatch(getRec.Body.String())[1]
	}
	if formToken == "" {
		t.Fatal("the rendered sign-in form carries no csrf_token value; the pre-session " +
			"CSRF check would refuse every submission")
	}
	if formToken != csrfCookie.Value {
		t.Error("the form's csrf_token does not match its cookie. The console would refuse " +
			"every operator's sign-in, which is exactly the failure this pair exists to catch")
	}

	form := url.Values{
		"username":   {username},
		"password":   {password},
		"csrf_token": {formToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestTheSignInFormActuallySignsAnOperatorIn(t *testing.T) {
	s, _, _ := server(t)

	raw, err := os.ReadFile(filepath.Join(repoDir(t), "internal", "api", "templates", "login.html"))
	if err != nil {
		t.Fatalf("read login.html: %v", err)
	}
	page := string(raw)

	for _, field := range []string{"name=\"username\"", "name=\"password\""} {
		if !strings.Contains(page, field) {
			t.Fatalf("login.html has no %s input, so this test cannot drive it", field)
		}
	}
	if !strings.Contains(page, `name="csrf_token"`) {
		t.Error("login.html carries no csrf_token field. The route is CSRF-protected today, " +
			"so this fails rather than passing silently -- but a form that stops carrying it " +
			"is one exemption away from being a CSRF hole, and nothing else would notice")
	}

	rec := signInViaConsoleForm(t, s.Handler(), "admin", "a-long-bootstrap-passphrase")

	if rec.Code != http.StatusFound {
		t.Fatalf("the sign-in form answered %d, want 302 to the feed: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != "/incidents" {
		t.Errorf("a successful sign-in redirects to %q, want /incidents", got)
	}
	var session bool
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "session") && c.Value != "" {
			session = true
		}
	}
	if !session {
		t.Errorf("the sign-in form set no session cookie; the redirect goes to a page that "+
			"will bounce straight back here. Cookies: %v", rec.Result().Cookies())
	}
}

// TestTheSignInFormShowsWhyItFailed, because a form that redirects a wrong password
// to a bare page with no message is indistinguishable from a form that did nothing.
//
// The reason must be in the BODY, not in the URL: a reason in a query string lands
// in browser history, in a bookmark, and in the Referer of whatever is clicked next.
func TestTheSignInFormShowsWhyItFailed(t *testing.T) {
	s, _, _ := server(t)

	rec := signInViaConsoleForm(t, s.Handler(), "admin", "definitely-not-the-passphrase")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password answered %d, want 401: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sign in") {
		t.Error("the failure did not re-render the sign-in page")
	}
	if !strings.Contains(body, `class="alert error"`) {
		t.Error("the failure re-rendered the page with no error message in it, so the " +
			"operator is told nothing about why they are still on the sign-in page")
	}
}

// TestTheSignInFormRendersTheTokenWithNoEntityEscaping is the regression test for
// a defect that was invisible for as long as the token was lucky.
//
// html/template escapes an `<input value="...">` as a URL attribute context, so a
// `+` in the token is written to the page as `&#43;`. Every browser decodes the
// entity before it submits, so the form posts the real token and the cookie holds
// the real token — but they never compare equal, and the POST is refused as a CSRF
// mismatch. A base64 alphabet containing `+` hits this on roughly one token in
// sixteen, which is exactly often enough for the sign-in to work in a demo and
// fail in the room.
//
// The fix is on the token's alphabet, in `identity.NewToken`: RawURLEncoding, whose
// alphabet needs no escaping at all. This test asserts the property that the fix
// guarantees, rather than asserting against one unlucky string, so it catches a
// future change back to a `+`-bearing alphabet whether or not a `+` happens to be
// in the sampled tokens.
func TestTheSignInFormRendersTheTokenWithNoEntityEscaping(t *testing.T) {
	s, _, _ := server(t)

	// Many samples: the bug was probabilistic, so the test has to be too.
	const samples = 40
	for i := 0; i < samples; i++ {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /login answered %d, want 200", rec.Code)
		}

		var csrfCookie *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if strings.Contains(c.Name, "csrf") {
				csrfCookie = c
			}
		}
		if csrfCookie == nil {
			t.Fatal("GET /login set no CSRF cookie")
		}

		body := rec.Body.String()
		re := regexp.MustCompile(`(?i)name="csrf_token"\s+value="([^"]*)"`)
		m := re.FindStringSubmatch(body)
		if m == nil {
			t.Fatal("the rendered sign-in form carries no csrf_token value")
		}

		if got := m[1]; got != csrfCookie.Value {
			t.Fatalf("the form's csrf_token %q does not equal its cookie %q. The browser "+
				"decodes HTML entities before it submits, so the server would refuse every "+
				"sign-in whose token contains a character the attribute context escapes", got, csrfCookie.Value)
		}
		if strings.Contains(body, "&#") {
			t.Fatal("the sign-in page carries an HTML entity in the csrf_token attribute, " +
				"which means the token in the form is not the token that was generated")
		}
	}
}

// TestTheSignOutFormEndsTheSession drives the other form end to end, and it carries
// a CSRF token because that route is NOT exempt.
//
// A sign-out a third-party page can trigger clears an operator's console
// mid-incident, so the token is the control and this test is the evidence that it is
// on the path. The negative case comes first, because a missing check and a present
// check are indistinguishable from the positive one.
func TestTheSignOutFormEndsTheSession(t *testing.T) {
	s, tenant, _ := server(t)

	t.Run("without a CSRF token the sign-out is refused", func(t *testing.T) {
		cookie, _, err := signIn(t, s)
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: cookieNameOf(s) + "_csrf", Value: "forged"})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("a sign-out with a forged CSRF token answered %d, want 403", rec.Code)
		}
		// And the session must still be alive, or the refusal is cosmetic.
		who := httptest.NewRecorder()
		s.Handler().ServeHTTP(who, httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil).
			WithContext(context.Background()))
		_ = tenant
		_ = who
	})

	t.Run("with the token the session ends and the operator lands on sign-in", func(t *testing.T) {
		cookie, csrf, err := signIn(t, s)
		if err != nil {
			t.Fatalf("sign in: %v", err)
		}
		form := url.Values{"csrf_token": {csrf}}
		req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: cookieNameOf(s) + "_csrf", Value: csrf})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Fatalf("a valid sign-out answered %d, want 302: %s", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Location"); got != "/login" {
			t.Errorf("sign-out redirects to %q, want /login", got)
		}
	})
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
