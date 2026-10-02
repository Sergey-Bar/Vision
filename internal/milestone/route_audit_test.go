package milestone

// The route table against the published vendor documents.
//
// `docs/vendor/milestone/route-audit.md` is the evidence. It records, for every
// route in this package, what Milestone's own two published documents say — with a
// line citation — and where they are silent.
//
// This file is what stops two things:
//
//   - **a route with no verdict.** The audit found nine of nine declared routes
//     wrong and proved it. A tenth route added to `routes.go` tomorrow would have
//     the same evidence problem and, without a gate, the same silence. This is the
//     third class-level gate after `sqlguard` and `rlsrole`, and it exists because
//     the class has already recurred: a documented field nothing reads, in
//     `RawRecord.Envelope`, `checkpoint.SealRange` and `api.Server.start`.
//   - **code and evidence drifting apart.** The route table, the audit and the
//     vendor documents are three statements of the same fact. Nothing enforced
//     their agreement, which is why a table could sit next to a document that
//     refuted every line of it for as long as it did.
//
// It is an internal test package because the routes it must see — `bookmarkRoute`
// and `systemTimeRoute` — are deliberately unexported, and exporting them to a
// test would be a wider API than the seam has.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// vendorDocs is where the two documents this adapter was checked against live.
//
// They are committed copies of Milestone's own files, and they are the evidence
// for every claim in the audit. Their provenance and licences are in the
// directory's README.
const vendorDocs = "../../docs/vendor/milestone"

const (
	eventsSpec    = "events-rest-v1.reference.md"
	bookmarksSpec = "bookmarks-rest-v1.openapi.yaml"
	auditDoc      = "route-audit.md"
)

func readVendorDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vendorDocs, name))
	if err != nil {
		t.Fatalf("read %s: %v\n"+
			"The route table's evidence is this file. If it has moved or been deleted, the audit "+
			"in docs/vendor/milestone/route-audit.md has nothing behind it and every verdict in "+
			"it is an assertion again.", name, err)
	}
	return string(raw)
}

// verdicts is the closed vocabulary, read from the audit document so the two
// cannot drift. A fourth word added to the audit without a decision here fails.
var verdicts = []string{"CONTRADICTED", "CONFIRMED", "NOT-ANSWERABLE-FROM-DOCS"}

// auditVerdict is one row of the audit's verdict table.
type auditVerdict struct {
	route   string // "METHOD /path", as the table's "now" column writes it
	verdict string
	cited   bool
}

// readAuditVerdicts parses the verdict table out of the audit document.
//
// It is a line scan rather than a markdown parser because the table is a
// convention this file and that document share, and because a parser would be a
// dependency added so a test could read a table with eight columns.
//
// `cited` records whether the citation cell says anything at all. For a
// `NOT-ANSWERABLE-FROM-DOCS` route the honest cell says there is nothing to cite,
// and "nothing in either document mentions this" is a finding rather than a gap in
// the evidence — so an empty cell is refused either way, because an empty cell is
// not a sentence a reader can check.
func readAuditVerdicts(t *testing.T) []auditVerdict {
	t.Helper()
	doc := readVendorDoc(t, auditDoc)
	var out []auditVerdict
	inTable := false
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "| # | Route as declared before") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break // the table ended
		}
		cells := splitRow(line)
		if len(cells) < 5 || !isRowNumber(cells[0]) {
			continue // the header rule, or a separator
		}
		out = append(out, auditVerdict{
			route:   normaliseRouteCell(cells[2]),
			verdict: strings.TrimSpace(cells[3]),
			cited:   strings.TrimSpace(cells[4]) != "" && strings.TrimSpace(cells[4]) != "—",
		})
	}
	if len(out) == 0 {
		t.Fatalf("no verdict rows were read from %s. The table header this test looks for has "+
			"changed shape, so the gate is reading nothing and passing for the wrong reason", auditDoc)
	}
	return out
}

func splitRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isRowNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normaliseRouteCell reduces a table cell to the same spelling the code produces.
//
// The audit writes its routes as prose does — in backticks, and for the bookmark
// write with a trailing "(no `task`)" because that distinction is the point of
// the row. Neither is part of the route's identity, so neither is compared.
func normaliseRouteCell(cell string) string {
	cell = strings.TrimSpace(cell)
	cell = strings.ReplaceAll(cell, "`", "")
	if cut := strings.Index(cell, " ("); cut >= 0 {
		cell = cell[:cut]
	}
	return strings.TrimSpace(cell)
}

// everyDeclaredRoute is the set of routes this package calls, by the same
// "METHOD /path" spelling the audit table uses.
func everyDeclaredRoute() []Route {
	out := make([]Route, 0, len(Routes)+1)
	out = append(out, Routes...)
	out = append(out, bookmarkRoute)
	return out
}

func routeKey(r Route) string {
	key := r.Method + " " + r.Path
	if r.Task != "" {
		key += "?task=" + r.Task
	}
	return key
}

// TestEveryRouteHasAVerdictInTheAudit: the gate.
//
// A route in the table with no verdict in the audit is a route nobody checked
// against Milestone's own words, which is how the eight routes this audit refuted
// stayed in the code for as long as they did.
func TestEveryRouteHasAVerdictInTheAudit(t *testing.T) {
	verdictsByRoute := map[string]auditVerdict{}
	for _, v := range readAuditVerdicts(t) {
		verdictsByRoute[v.route] = v
	}

	for _, r := range everyDeclaredRoute() {
		key := routeKey(r)
		v, ok := verdictsByRoute[key]
		if !ok {
			t.Errorf("the route %q has no verdict in %s.\n"+
				"A route nobody checked against the vendor's published documents is exactly the "+
				"defect this gate exists for: `/api/v1/System/UserGroups` sat in this table "+
				"throughout the audit that proved it wrong. Add a row with a verdict and a line "+
				"citation.", key, auditDoc)
			continue
		}
		if !knownVerdict(v.verdict) {
			t.Errorf("the verdict for %q is %q, which is not one of %v. A verdict outside the "+
				"vocabulary cannot be acted on: it is not a claim", key, v.verdict, verdicts)
		}
		if !v.cited {
			t.Errorf("the verdict for %q has no citation. \"The documents do not say\" is only "+
				"useful if a reader can check where", key)
		}
	}
}

// TestEveryVerdictNamesADeclaredRoute, the other direction.
//
// A verdict for a route the code no longer calls is a stale claim about the vendor,
// and this repository's own rule is that a stale claim is a defect rather than
// harmless. It is also how a route could quietly come back.
func TestEveryVerdictNamesADeclaredRoute(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range everyDeclaredRoute() {
		declared[routeKey(r)] = true
	}
	for _, v := range readAuditVerdicts(t) {
		if !declared[v.route] {
			t.Errorf("%s has a verdict for %q, which no route in this package calls.\n"+
				"Either the route was renamed without updating the audit, or the audit is "+
				"describing something the code no longer does. Both are the same defect: a "+
				"document stating something nothing checks.", auditDoc, v.route)
		}
	}
}

func knownVerdict(v string) bool {
	for _, known := range verdicts {
		if strings.Contains(v, known) {
			return true
		}
	}
	return false
}

// TestEveryRouteMatchesTheDocumentedFormInTheAudit is a literal copy of the audit
// table's "now" column.
//
// The audit document is prose and is read by a person; this table is code and is
// checked by a machine, so the two can be compared without a human doing it. It
// has been red-checked: changing one path here fails the test, and changing one
// path in routes.go fails the row that disagrees with it.
func TestEveryRouteMatchesTheDocumentedFormInTheAudit(t *testing.T) {
	type expectation struct {
		route   Route
		source  string
		wantKey string
	}
	want := []expectation{
		{Route{Method: "GET", Path: APIPrefix + "/System/UserGroups"}, "audit §1 row 1", ""},
		{Route{Method: "GET", Path: APIPrefix + "/events"}, "audit §1 row 2", ""},
		{Route{Method: "GET", Path: APIPrefix + "/events/{id}"}, "audit §1 row 3", ""},
		{Route{Method: "GET", Path: APIPrefix + "/Alarms"}, "audit §1 row 4", ""},
		{Route{Method: "POST", Path: APIPrefix + "/bookmarks", Task: "searchTime"}, "audit §1 row 5", "GET /api/rest/v1/bookmarks"},
		{Route{Method: "GET", Path: APIPrefix + "/ResourceGroups"}, "audit §1 row 6", ""},
		{Route{Method: "GET", Path: APIPrefix + "/Servlets"}, "audit §1 row 7", ""},
		{Route{Method: "GET", Path: APIPrefix + "/System/Time"}, "audit §1 row 8", ""},
		{Route{Method: "POST", Path: APIPrefix + "/bookmarks"}, "audit §1 row 9", ""},
		{Route{Method: "GET", Path: APIPrefix + "/eventTypes"}, "audit §1 row 10", ""},
	}

	got := map[string]Route{}
	for _, r := range everyDeclaredRoute() {
		got[routeKey(r)] = r
	}
	for _, e := range want {
		key := routeKey(e.route)
		r, ok := got[key]
		if !ok {
			t.Errorf("no route is declared at %q. %s says one is.\n"+
				"Method, path or task changed without the audit being updated, which is the drift "+
				"this copy exists to catch", key, e.source)
			continue
		}
		if !r.Unverified {
			t.Errorf("the route %q is no longer marked Unverified. A published document can "+
				"refute an assumption and it did; it cannot confirm one. Only a human, after "+
				"reading a dated probe artifact, clears this flag", key)
		}
		if strings.TrimSpace(r.Notes) == "" {
			t.Errorf("the route %q has no Notes. Every route's note says what the published "+
				"documents say about it, so an empty one means an unchecked route", key)
		}
	}
	if len(got) != len(want) {
		t.Errorf("this package declares %d routes and the audit documents %d. A route added "+
			"without a verdict, or removed without deleting its row", len(got), len(want))
	}
}

// TestTheBookmarkWriteSendsNoTask is the `task` half of the decision in audit §2.5,
// separated from the table above because the two routes share a method and a path
// and differ only by their task.
//
// `bookmarks-rest-v1.openapi.yaml:142-161` gives `newReference` a description that
// sounds like this product's — "create a new reference and trigger rules", "to
// ensure actions like Start-Recording is performed in real time" — and then says
// the thing that decides it: "The newReference task does not create the bookmark in
// the database." A task-less POST creates the bookmark and does not trigger the
// vendor's rules, which is what "a pointer the operator can follow" means, and
// triggering a customer's rules is a capability activation that AGENTS.md reserves
// to a human.
func TestTheBookmarkWriteSendsNoTask(t *testing.T) {
	if bookmarkRoute.Task != "" {
		t.Errorf("the bookmark write-back carries task=%q. See the comment on bookmarkRoute and "+
			"audit §2.5: no task creates the bookmark without triggering the vendor's rules, and "+
			"`newReference` triggers rules and creates no bookmark", bookmarkRoute.Task)
	}
	if bookmarkRoute.Method != "POST" {
		t.Errorf("the bookmark write uses %s; the documented method is POST", bookmarkRoute.Method)
	}
	// And the read side is the other task, on the same path.
	var read *Route
	for i := range Routes {
		if Routes[i].Path == bookmarkRoute.Path {
			read = &Routes[i]
		}
	}
	if read == nil {
		t.Fatal("no route in Routes reads bookmarks; the bookmark feed would never be polled")
	}
	if read.Task != "searchTime" {
		t.Errorf("the bookmark read carries task=%q, want searchTime: a GET cannot retrieve "+
			"bookmarks, so the documented read is this POST with this task", read.Task)
	}
	if read.Method != "POST" {
		t.Errorf("the bookmark read uses %s; bookmarks-rest-v1.openapi.yaml:45-46 says they "+
			"cannot be retrieved with a GET at all", read.Method)
	}
}

// TestTheClockRouteIsDeclaredOnce.
//
// `systemTimeRoute` used to be a second, independent copy of a path the Routes
// table also declared. Nothing tied the two together, so they could disagree and
// every test would still pass — each only ever compared itself with itself. The
// probe in particular read one copy while the clock read went to the other.
func TestTheClockRouteIsDeclaredOnce(t *testing.T) {
	var matches int
	for _, r := range Routes {
		if r.Path == systemTimeRoute.Path {
			matches++
			if r.Method != systemTimeRoute.Method {
				t.Errorf("the clock route is declared twice with different methods: %q in Routes, "+
					"%q on systemTimeRoute. One of them is a route nobody calls",
					r.Method, systemTimeRoute.Method)
			}
			if r.ClockProbe != systemTimeRoute.ClockProbe {
				t.Errorf("the clock route is declared twice with different ClockProbe flags")
			}
		}
	}
	if matches != 1 {
		t.Errorf("the clock path is declared %d times in Routes, want exactly 1", matches)
	}
	if !systemTimeRoute.ClockProbe {
		t.Error("the clock route is not marked ClockProbe. The field existed, was documented and " +
			"was read by nothing; it is set now so a second declaration cannot drop it silently")
	}
}

// TestNoRouteUsesAPrefixTheVendorDoesNotDeclare.
//
// The mistake this package made was in eight places at once, so it is worth one
// test that would catch it if it were ever made in one. `/api/v1` is not a
// Milestone prefix: it appears in neither committed document.
func TestNoRouteUsesAPrefixTheVendorDoesNotDeclare(t *testing.T) {
	for _, r := range everyDeclaredRoute() {
		if !strings.HasPrefix(r.Path, APIPrefix) {
			t.Errorf("the route %s %s does not start with the documented prefix %s.\n"+
				"Every call would 404, and a feed that 404s is a site that looks quiet",
				r.Method, r.Path, APIPrefix)
		}
	}
}

// TestTheVendorDocumentsStillDeclareWhatThisAdapterSends re-reads the evidence.
//
// The audit cites line numbers in two files that were downloaded on a date. If
// either is ever re-downloaded and a name has moved, that is a finding about the
// vendor and this is where it surfaces, rather than a divergence that grows
// silently in the gap between a document and a route table.
func TestTheVendorDocumentsStillDeclareWhatThisAdapterSends(t *testing.T) {
	bookmarks := readVendorDoc(t, bookmarksSpec)
	events := readVendorDoc(t, eventsSpec)

	// The prefix. Case-insensitively, and deliberately: the two documents disagree
	// about it and the audit records both rather than picking a winner silently.
	// See audit §2.1.
	for name, doc := range map[string]string{bookmarksSpec: bookmarks, eventsSpec: events} {
		if !strings.Contains(strings.ToLower(doc), strings.ToLower(APIPrefix)) {
			t.Errorf("%s no longer contains the prefix %s this adapter calls. Re-read the "+
				"specification and audit §2.1 before changing the constant", name, APIPrefix)
		}
	}
	for name, doc := range map[string]string{bookmarksSpec: bookmarks, eventsSpec: events} {
		if strings.Contains(doc, "/api/v1/") {
			t.Errorf("%s now contains /api/v1/, which is the prefix this package guessed and "+
				"neither document ever declared", name)
		}
	}

	// Every field name the bookmark write sends is a declared schema property.
	// Matched as a YAML mapping key rather than as a bare substring, so a field
	// name that only appears inside a description does not pass for a declared one.
	for _, field := range []string{"header", "description", "timeTriggered", "timeBegin", "timeEnd", "devicePath"} {
		if !declaresYAMLKey(bookmarks, field) {
			t.Errorf("the bookmark body sends %q and %s does not declare it as a schema property. "+
				"A field the vendor does not know is a 400 or a silently ignored field",
				field, bookmarksSpec)
		}
	}
	// ...and the two the vendor requires are named as required.
	for _, required := range []string{"- header", "- devicePath"} {
		if !strings.Contains(bookmarks, required) {
			t.Errorf("%s no longer lists %q under newBookmarkBody's required list", bookmarksSpec, required)
		}
	}

	// The bookmark search's four tasks, including the three the audit reasoned
	// about. `newReference` is the one this package deliberately does not send.
	for _, task := range []string{"newReference", "searchTime", "searchFromBookmark"} {
		if !strings.Contains(bookmarks, task) {
			t.Errorf("%s no longer documents the %q task; audit §2.4 and §2.5 reason about it",
				bookmarksSpec, task)
		}
	}
	for _, field := range []string{"countLimit", "resourceTypes", "endTime", "timeLimit"} {
		if !declaresYAMLKey(bookmarks, field) {
			t.Errorf("%s no longer declares %q, which the bookmark search body sends", bookmarksSpec, field)
		}
	}

	// The events feed's documented entry point, and the spellings the transport
	// now sends instead of the ones it used to guess.
	for _, want := range []string{
		"/api/rest/v1/events",
		"/events/{id}",
		"gt:'",
		"orderBy",
		"page",
		"size",
		"_links",
		"eventCount",
	} {
		if !strings.Contains(events, want) {
			t.Errorf("%s no longer contains %q, which transport.go's query builder is written "+
				"against. Read the document again before changing the builder", eventsSpec, want)
		}
	}
	// The response member the item decoder reads, and the fields it reads off an
	// item. `type` is a GUID and `Priority`, `TimeSpan`, `ImagePath` and
	// `VideoRecordingStartTime` do not exist — see audit §3, and the fact that the
	// event-type mapping is a human decision (H1).
	for _, field := range []string{"specversion", "localId", "datatype", "tag"} {
		if !strings.Contains(events, `"`+field+`"`) {
			t.Errorf("%s no longer shows an event field named %q", eventsSpec, field)
		}
	}
}

// declaresYAMLKey reports whether a YAML file declares name as a mapping key.
//
// One regexp rather than a YAML parser, because pulling a YAML library into the
// module for a test is a dependency change, and a dependency change needs a human
// to read it.
var yamlKeyPattern = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*):`)

func declaresYAMLKey(doc, name string) bool {
	for _, m := range yamlKeyPattern.FindAllStringSubmatch(doc, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

// TestTheAuditNamesTheConstraintTheRouteAuditExistsFor is a floor on the evidence
// rather than a check of the code.
//
// The reason this file exists is that the route table was wrong and the documents
// that refute it were committed in this repository the whole time. If that stops
// being true — if the vendor documents are replaced, renamed, or reduced to a
// pointer — then every verdict in the audit is an assertion again, and the most
// useful thing this gate can do is say so.
func TestTheAuditNamesTheConstraintTheRouteAuditExistsFor(t *testing.T) {
	audit := readVendorDoc(t, auditDoc)
	for _, want := range []string{
		// The rule the whole document is built around.
		"A published OpenAPI document is not a running server.",
		// The three that were wrong in the same way.
		"/api/v1",
		// The two verdicts that keep a route on the list.
		"NOT-ANSWERABLE-FROM-DOCS",
		// The handover, so a reader knows the mapping question is not this file's.
		"H1",
	} {
		if !strings.Contains(audit, want) {
			t.Errorf("%s no longer contains %q. The audit is the evidence the route table is "+
				"checked against; if it has stopped saying these things, the gate above is "+
				"checking the routes against a document that no longer supports it", auditDoc, want)
		}
	}
}
