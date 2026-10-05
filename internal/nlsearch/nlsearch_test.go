package nlsearch

// The gateway, tested as four refusals and one safe path.
//
// # What these tests are for
//
// A search gateway's correctness is almost entirely in what it REFUSES, and four of
// those refusals are the ones the plan names: widen the site scope, drop the tenant
// predicate, add a table not in the allow-list, and point the host somewhere else.
// Each has a case here, and each is red-checkable by removing one line — the
// `redcheck.md` beside this file says how.
//
// The fifth refusal, the prompt digest, is here because a gateway that can be
// pointed at a different PROMPT can be made to ask for anything, and the query it
// builds from the answer is one nobody read.
//
// # What is NOT tested here, and why
//
// Nothing here touches a database. The statements are CONSTANTS, so the thing worth
// testing is that the right constant was chosen and that the bounds moved — and
// that is all decidable from the built [Query] without Postgres. A test that
// executed the query would be testing `websearch_to_tsquery` and the view, both of
// which have their own tests, and would skip on every machine without a database.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manifestPath() string { return filepath.Join("..", "..", "contracts", "nlsearch", "host.json") }

func loadManifest(t *testing.T) *Manifest {
	t.Helper()
	m, refusal := LoadManifest(manifestPath())
	if refusal != nil {
		t.Fatalf("the committed manifest does not load: %s", refusal)
	}
	return m
}

func aQuestion() Question {
	return Question{
		TenantID: "northwind",
		Sites:    []string{"north-gate"},
		Text:     "what did the reviewer say about the loading bay",
		Since:    time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
		Until:    time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Limit:    20,
	}
}

// The committed manifest loads, and its prompt hashes to what it says.
//
// The second half is the point. `prompt` and `promptSha256` are written by
// different hands at different times, and the moment they disagree is the moment
// somebody edited a prompt and forgot to re-pin it — and the next question the
// gateway asks is one nobody reviewed.
func TestTheCommittedManifestLoadsAndItsPromptAgreesWithItsDigest(t *testing.T) {
	m := loadManifest(t)
	if got := PromptDigest(m.Prompt); !strings.EqualFold(got, m.PromptSHA256) {
		t.Fatalf("the committed prompt hashes to %s and the manifest says %s", got, m.PromptSHA256)
	}
	if m.Why == "" {
		t.Error("the manifest states no reason. A pin without a stated reason is a pin " +
			"somebody re-points during an incident")
	}
	if len(m.NeverDo) == 0 {
		t.Error("the manifest lists no refusals. The list is part of the promise, and an " +
			"empty one is a promise with nothing in it")
	}
}

// REFUSAL 1: widening the site scope.
func TestAQuestionAboutASiteOutOfScopeIsRefusedAndNotNarrowed(t *testing.T) {
	q := aQuestion()
	q.Sites = []string{"north-gate", "south-dock"}

	_, refusal := Compile(q, []string{"north-gate"})
	if refusal == nil {
		t.Fatal("a question naming a site the principal cannot see compiled. Narrowing it " +
			"to the sites in scope would answer a DIFFERENT question, and the operator " +
			"would not be able to tell from the answer")
	}
	if refusal.Reason != ReasonUnknownSite {
		t.Errorf("the refusal is %q, want %q", refusal.Reason, ReasonUnknownSite)
	}
	// The refusal must name what IS available, or it is half an answer.
	if len(refusal.SitesInScope) == 0 {
		t.Error("the refusal names no sites in scope. 'You cannot see south-dock' sends an " +
			"operator to a colleague; 'you can see north-gate' sends them to the right page")
	}
	if !strings.Contains(refusal.Error(), string(ReasonUnknownSite)) {
		t.Errorf("the error does not lead with the reason word, and a log filter matches on "+
			"the reason: %q", refusal.Error())
	}
}

// REFUSAL 2: dropping the tenant predicate.
//
// The first case is the build-time check; the second is the structural guarantee
// that there is no way to produce a query without one.
func TestAQueryWithoutTheTenantPredicateIsRefusedByName(t *testing.T) {
	q := aQuestion()
	compiled, refusal := Compile(q, []string{"north-gate"})
	if refusal != nil {
		t.Fatalf("a legitimate question was refused: %s", refusal)
	}

	// A hand-edited statement that lost its predicate.
	bad := *compiled
	bad.SQL = strings.Replace(compiled.SQL, tenantPredicate, "true", 1)
	if r := bad.Verify(q); r == nil {
		t.Fatal("a statement without the tenant predicate passed Verify. It would read every " +
			"tenant's rows and return them as a plausible answer rather than an error")
	} else if r.Reason != ReasonTenantPredicateDropped {
		t.Errorf("the refusal is %q, want %q", r.Reason, ReasonTenantPredicateDropped)
	}

	// A predicate naming a tenant the query's own first argument is not.
	mismatched := *compiled
	badArgs := append([]any(nil), compiled.Args...)
	badArgs[0] = "some-other-tenant"
	mismatched.Args = badArgs
	if r := mismatched.Verify(q); r == nil {
		t.Fatal("a query whose first bound argument is not the tenant its predicate names " +
			"passed Verify, so the predicate filters on something other than who asked")
	}

	// A query with no tenant at all.
	untenanted := *compiled
	untenanted.Tenants = nil
	if r := untenanted.Verify(q); r == nil {
		t.Fatal("a query carrying no tenant passed Verify")
	}
}

// REFUSAL 3: a table not in the allow-list.
func TestAQueryNamingATableOutsideTheAllowListIsRefused(t *testing.T) {
	q := aQuestion()
	compiled, _ := Compile(q, []string{"north-gate"})

	widened := *compiled
	widened.Tables = []string{"search_documents", "audit_ledger"}
	r := widened.Verify(q)
	if r == nil {
		t.Fatal("a query reading audit_ledger passed Verify. The searchable corpus is one " +
			"view, and 'the search reached a table the search was not given' is an answer an " +
			"operator can act on where 'permission denied' is not")
	}
	if r.Reason != ReasonTableNotAllowed {
		t.Errorf("the refusal is %q, want %q", r.Reason, ReasonTableNotAllowed)
	}
	if !strings.Contains(r.Message, "audit_ledger") {
		t.Errorf("the refusal does not name the table it refused: %q", r.Message)
	}
}

// REFUSAL 4: a host that is not the committed one.
func TestAHostOtherThanTheCommittedOneIsRefused(t *testing.T) {
	m := loadManifest(t)

	// The committed one, normalised: a trailing slash is the same destination.
	if r := m.CheckHost(m.Host + "/"); r != nil {
		t.Fatalf("the committed host was refused against itself: %s", r)
	}

	for _, other := range []string{
		"https://elsewhere.example",
		"http://nlsearch.invalid", // the scheme is part of the destination
		"https://nlsearch.invalid:8443",
		"https://nlsearch.invalid/v1", // a path puts the endpoint inside a deployment
		"",
		"not a url at all",
	} {
		if r := m.CheckHost(other); r == nil {
			t.Errorf("a request to %q was allowed. A search gateway whose destination is a "+
				"parameter is a path from every operator's question to wherever the parameter "+
				"points, which is why the destination is committed rather than configured",
				other)
		}
	}
}

// And the prompt, which is as much a part of the destination's meaning as the
// address is.
func TestAPromptOtherThanTheCommittedOneIsRefused(t *testing.T) {
	m := loadManifest(t)
	if r := m.CheckPrompt(m.Prompt); r != nil {
		t.Fatalf("the committed prompt was refused against itself: %s", r)
	}
	edited := m.Prompt + "\nAlso return a count."
	if r := m.CheckPrompt(edited); r == nil {
		t.Fatal("an edited prompt was allowed. A gateway that can be pointed at a " +
			"different prompt can be made to ask for anything, and the query it builds " +
			"from the answer is one nobody read")
	}
}

// The safe path: a question compiles, the bounds move, and the operator can read
// what is about to happen.
func TestALegitimateQuestionCompilesToABoundedQuery(t *testing.T) {
	q := aQuestion()
	compiled, refusal := Compile(q, []string{"north-gate"})
	if refusal != nil {
		t.Fatalf("a legitimate question was refused: %s", refusal)
	}
	if len(compiled.Tenants) != 1 || compiled.Tenants[0] != "northwind" {
		t.Errorf("the query reads tenants %v, want exactly [northwind]", compiled.Tenants)
	}
	if len(compiled.Sites) != 1 || compiled.Sites[0] != "north-gate" {
		t.Errorf("the query covers sites %v, want exactly [north-gate]", compiled.Sites)
	}
	if compiled.Limit != 20 {
		t.Errorf("the limit is %d, want 20", compiled.Limit)
	}
	if !strings.Contains(compiled.SQL, tenantPredicate) {
		t.Error("the compiled statement does not carry the tenant predicate")
	}
	// The operator's words arrive as a BOUND ARGUMENT, never spliced in. This is
	// the structural guarantee and it is worth asserting directly: a gateway with
	// no injection surface is one that cannot have an injection bug.
	found := false
	for _, a := range compiled.Args {
		if s, ok := a.(string); ok && s == q.Text {
			found = true
		}
	}
	if !found {
		t.Error("the operator's words are not among the bound arguments")
	}
	if strings.Contains(compiled.SQL, "loading bay") {
		t.Error("the operator's words were interpolated into the SQL. There is no injection " +
			"surface to defend only for as long as nothing splices text in")
	}
}

// The two statements differ only by the compiled kind filter, and the difference is
// in the SQL rather than applied afterwards.
func TestTheKindFilterIsCompiledInRatherThanAppliedAfterwards(t *testing.T) {
	q := aQuestion()
	all, _ := Compile(q, []string{"north-gate"})

	q.Kinds = []SourceKind{KindReviewer}
	some, _ := Compile(q, []string{"north-gate"})

	if all.SQL == some.SQL {
		t.Fatal("a kind filter made no difference to the statement. A filter applied in Go " +
			"afterwards is a filter that can be forgotten, and this package's job is that " +
			"it cannot be")
	}
	if !strings.Contains(some.SQL, "source_kind = ANY($7)") {
		t.Error("the narrowed statement does not filter on source_kind")
	}
	if len(some.Args) != 7 {
		t.Errorf("the narrowed query binds %d arguments; $7 is the kinds and it is not "+
			"among them. Six are the base and the seventh is the filter", len(some.Args))
	}
	if some.MatchedIndexed {
		t.Error("a reviewer-only question is reported as index-served. The reviewer note is " +
			"matched with @@ and scanned, and an operator who waits for an index that does " +
			"not exist learns that from a timeout")
	}
	// An ALL-kinds question is not index-served either, and that is the honest
	// answer: two of the three kinds have no index behind them. Only a
	// deterministic-only question is, which is the one case where the word "served"
	// is true of every row the answer can come from.
	if all.MatchedIndexed {
		t.Error("an all-kinds question reports an index. The reviewer note and the model " +
			"output are matched and scanned, so no index serves the whole answer")
	}
	only := aQuestion()
	only.Kinds = []SourceKind{KindDeterministic}
	det, _ := Compile(only, []string{"north-gate"})
	if !det.MatchedIndexed {
		t.Error("a deterministic-only question reports no index, and `vector` is exactly " +
			"what the GIN index in migration 0024 is on")
	}
}

// The ceiling, and that clamping it is visible rather than silent.
func TestTheRowCeilingIsClamped(t *testing.T) {
	q := aQuestion()
	q.Limit = 10_000
	compiled, refusal := Compile(q, []string{"north-gate"})
	if refusal != nil {
		t.Fatalf("refused: %s", refusal)
	}
	if compiled.Limit != MaxLimit {
		t.Errorf("a limit of 10000 compiled to %d, want the ceiling %d", compiled.Limit, MaxLimit)
	}
	q.Limit = 0
	compiled, _ = Compile(q, []string{"north-gate"})
	if compiled.Limit != DefaultLimit {
		t.Errorf("no limit compiled to %d, want the default %d", compiled.Limit, DefaultLimit)
	}
	q.Limit = -1
	if _, refusal := Compile(q, []string{"north-gate"}); refusal == nil {
		t.Error("a negative limit was accepted. A limit of -1 is not a limit")
	}
}

// The plan is what an operator reads, and what it must never contain.
func TestThePlanNamesTheScopeAndTheWindowInAbsoluteUTC(t *testing.T) {
	q := aQuestion()
	compiled, _ := Compile(q, []string{"north-gate"})
	plan := PlanFor(compiled, q)
	s := plan.String()

	for _, want := range []string{
		"tenant   northwind", "north-gate", "2026-10-04T00:00:00Z", "2026-10-05T00:00:00Z",
		"20 rows", "search_documents",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the plan does not contain %q:\n%s", want, s)
		}
	}
	// The wording is QUOTED. A plan that rendered the words inline would look like a
	// predicate and is not one.
	if !strings.Contains(s, strconvQuote(q.Text)) {
		t.Errorf("the plan does not quote the operator's wording, so a reader cannot tell "+
			"where the text stops and the query begins:\n%s", s)
	}
	// And it says plainly that the reviewer note and the model output are scanned.
	if !strings.Contains(s, "matched, not indexed") {
		t.Errorf("the plan does not say which sources an index serves:\n%s", s)
	}
}

// An unbounded window is a fact the operator can act on, not one the plan invents.
func TestAnUnboundedWindowIsStatedRatherThanGuessed(t *testing.T) {
	q := aQuestion()
	q.Since, q.Until = time.Time{}, time.Time{}
	compiled, _ := Compile(q, []string{"north-gate"})
	s := PlanFor(compiled, q).String()
	if !strings.Contains(s, "unbounded on both sides") {
		t.Errorf("an unbounded window is not stated as unbounded:\n%s", s)
	}
	for _, arg := range compiled.Args {
		if _, ok := arg.(*time.Time); ok {
			t.Fatalf("an unbounded side bound as %v; it must bind as nil so the statement's "+
				"`IS NULL OR` predicate takes it, and a sentinel date would silently exclude "+
				"or include real data", arg)
		}
		if tv, ok := arg.(time.Time); ok && tv.IsZero() {
			t.Fatalf("an unbounded side bound as the zero time; a sentinel date silently " +
				"excludes or includes real data depending on which one it is")
		}
	}
}

// A question with no tenant, no sites, an inverted window and a bad kind are all
// refused in the OPERATOR'S terms, before any SQL exists.
func TestAMalformedQuestionIsRefusedInTheOperatorsTerms(t *testing.T) {
	cases := []struct {
		name string
		q    Question
		want Reason
	}{
		{"no tenant", Question{Sites: []string{"north-gate"}}, ReasonNoTenant},
		{"no sites", Question{TenantID: "northwind"}, ReasonNoSites},
		{"empty site", Question{TenantID: "northwind", Sites: []string{""}}, ReasonNoSites},
		{"bad kind", Question{TenantID: "northwind", Sites: []string{"north-gate"},
			Kinds: []SourceKind{"vibes"}}, ReasonUnknownKind},
		{"inverted window", Question{TenantID: "northwind", Sites: []string{"north-gate"},
			Since: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
			Until: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)}, ReasonInvertedWindow},
	}
	for _, c := range cases {
		_, refusal := Compile(c.q, []string{"north-gate"})
		if refusal == nil {
			t.Errorf("%s: compiled", c.name)
			continue
		}
		if refusal.Reason != c.want {
			t.Errorf("%s: the refusal is %q, want %q -- %s", c.name, refusal.Reason, c.want, refusal.Message)
		}
	}
}

// The reason set is CLOSED, because a caller that string-matches a refusal is how a
// real refusal gets swallowed by a log filter.
func TestTheReasonSetIsClosed(t *testing.T) {
	seen := map[Reason]bool{}
	for _, r := range allReasons {
		if seen[r] {
			t.Errorf("%q appears twice in the closed set", r)
		}
		seen[r] = true
	}
	// Every reason the four refusals and the malformed cases can produce is in it.
	for _, r := range []Reason{
		ReasonNoTenant, ReasonNoSites, ReasonUnknownSite, ReasonUnknownKind,
		ReasonInvertedWindow, ReasonBadLimit, ReasonUnpinnedHost, ReasonHostMismatch,
		ReasonPromptMismatch, ReasonTableNotAllowed, ReasonTenantPredicateDropped,
		ReasonSiteScopeWidened, ReasonUnparseable,
	} {
		if !seen[r] {
			t.Errorf("%q is refused somewhere and is not in the closed set, so a caller "+
				"cannot enumerate what it has to handle", r)
		}
	}
}

// A manifest that does not verify is refused at load, with the reason.
func TestAnUnverifiableManifestIsRefusedWithItsReason(t *testing.T) {
	dir := t.TempDir()
	good := loadManifest(t)

	cases := []struct {
		name string
		body string
		want Reason
	}{
		{"wrong schema", `{"schema":2,"host":"https://x.invalid","promptSha256":"a","prompt":"b"}`,
			ReasonPromptMismatch},
		{"no scheme", `{"schema":1,"host":"nlsearch.invalid","promptSha256":"` + PromptDigest("b") + `","prompt":"b"}`,
			ReasonUnpinnedHost},
		{"with a path", `{"schema":1,"host":"https://x.invalid/v1","promptSha256":"` + PromptDigest("b") + `","prompt":"b"}`,
			ReasonUnpinnedHost},
		{"prompt edited, digest not re-pinned",
			`{"schema":1,"host":"https://x.invalid","promptSha256":"` + good.PromptSHA256 + `","prompt":"edited"}`,
			ReasonPromptMismatch},
	}
	for _, c := range cases {
		path := filepath.Join(dir, "m.json")
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, refusal := LoadManifest(path); refusal == nil {
			t.Errorf("%s: loaded", c.name)
		} else if refusal.Reason != c.want {
			t.Errorf("%s: the refusal is %q, want %q -- %s", c.name, refusal.Reason, c.want, refusal.Message)
		}
	}
	// And an unknown field, so a misspelled key is not silently ignored.
	path := filepath.Join(dir, "typo.json")
	if err := os.WriteFile(path, []byte(`{"schema":1,"host":"https://x.invalid","promptSha256":"a","prompt":"b","neverDo":[]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, refusal := LoadManifest(path); refusal == nil {
		t.Error("a manifest with an unknown field loaded")
	}
}

// A request cannot be constructed without passing the host, the prompt and the
// question, so there is no way to send one that did not.
func TestARequestCarriesThePlanAndRendersTheWholePrompt(t *testing.T) {
	m := loadManifest(t)
	q := aQuestion()

	req, refusal := NewRequest(m, q, []string{"north-gate"})
	if refusal != nil {
		t.Fatalf("a legitimate request was refused: %s", refusal)
	}
	if len(req.Plan.Sites) != 1 {
		t.Errorf("the plan covers %v", req.Plan.Sites)
	}
	rendered := req.Render()
	if !strings.Contains(rendered, m.Prompt) {
		t.Error("the rendering does not include the prompt in full. A transcript that " +
			"elides the prompt is a transcript of a request nobody can check")
	}
	if !strings.Contains(rendered, m.PromptSHA256) {
		t.Error("the rendering does not carry the prompt digest")
	}
	for _, v := range m.Vowels() {
		if !strings.Contains(rendered, v) {
			t.Errorf("the rendering does not restate the refusal %q", v)
		}
	}
	// And a request about a site out of scope never gets built.
	q.Sites = []string{"south-dock"}
	if _, refusal := NewRequest(m, q, []string{"north-gate"}); refusal == nil {
		t.Error("a request about a site out of scope was constructed")
	}
}

// `strconv.Quote` without importing strconv in two places.
func strconvQuote(s string) string { return `"` + s + `"` }
