package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures. A coverage gate proved only against the live tree has never been
// observed to fail, and a gate nobody has watched go red is a gate whose failure mode is
// unknown.
//
// These write a miniature tree into t.TempDir() and point the derivations at it, so each
// case below removes exactly one thing and asserts the gate names it.

// fixture writes a tree with the seven inputs each derivation reads, and returns its
// root.
//
// The inputs are minimal but not fake: a real `internal/dispatch/dispatch.go` shape, a
// real `metrics.go` const block, a real `openapi.yaml` two-level mapping, real migration
// DDL with RANGE partitions, and a real `write_canary_test.go`. A fixture that used a
// shape no real file has would test the parser rather than the gate.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// TestTheGateNamesAMissingExecutor is the failure this whole package is justified by,
// run against a fixture rather than against the repository.
//
// The mutation is one line in one file: a dispatch type is removed from the executor
// switch. The fixture's ledger does NOT list it as refused. The gate must therefore
// report it, and the message has to name what closing it takes.
//
// The mutation is deliberately the SAME SHAPE as the sixth control this repository tried
// by hand - which turned out nothing tested. A gate whose mutation nobody has run is a
// gate with an unknown failure mode.
func TestTheGateNamesAMissingExecutor(t *testing.T) {
	root := fixture(t, baseFixture())

	// One case label removed. Everything else is intact.
	const withAllExecutors = `func registeredExecutors(d Dispatch) bool {
	switch d.Type {
	case "notify_operator":
		return true
	case "notify_supervisor":
		return true
	case "request_vlm":
		return true
	case "fetch_evidence":
		return true
	}
	return false
}`
	const missingOne = `func registeredExecutors(d Dispatch) bool {
	switch d.Type {
	case "notify_operator":
		return true
	case "notify_supervisor":
		return true
	case "request_vlm":
		return true
	}
	return false
}`
	replaceIn(t, root, "internal/dispatch/dispatch.go", withAllExecutors, missingOne)

	// The ledger is the baseline: every type covered, nothing refused. So the mutation
	// is the ONLY thing that can produce a finding.
	l := Ledger{}
	report := reportFor(t, root, allDispatchTypes, nil)
	problems := report.Verify(l)

	found := false
	for _, p := range problems {
		if strings.Contains(p, "fetch_evidence") &&
			strings.Contains(p, "DispatchExecutors") {
			found = true
		}
	}
	if !found {
		t.Fatalf("removing one executor case from internal/dispatch produced no finding.\n"+
			"Problems reported: %v\n\n"+
			"A gate that cannot be made to fail by removing the thing it checks is not a "+
			"gate. This is the mutation that must go red, and it is here so that it is "+
			"known to go red rather than assumed to.", problems)
	}
}

// TestTheGateNamesAMissingWorker is the second half of the queue assertion, and it is a
// separate test because a producer and a worker are separate facts.
//
// The fixture's producer satisfies the producer half. Removing the worker registration
// has to fail, or the table is really only asserting "something enqueues somewhere".
func TestTheGateNamesAMissingWorker(t *testing.T) {
	root := fixture(t, baseFixture())

	const withWorker = `	jobWorker := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM}`
	const withoutWorker = `	jobWorker := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM[:0]}`
	// The mutation removes the `Name: jobs.QueueVLM` line entirely, which is the shape
	// the real defect has: a worker constructed without naming its queue.
	replaceIn(t, root, "cmd/security-core/main.go", withWorker,
		`	jobWorker := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}}`)
	_ = withoutWorker

	l := Ledger{}
	report := reportFor(t, root, nil, []Queue{{Name: "vlm", Const: "QueueVLM"}})
	problems := report.Verify(l)

	if !anyMentions(problems, "QueueEndpoints", "vlm") {
		t.Fatalf("removing the worker registration produced no finding.\nProblems: %v\n\n"+
			"Either half of the queue assertion alone would pass here, and a queue with a "+
			"producer and no worker accumulates work until a threshold fires on a backlog "+
			"that will never drain", problems)
	}
}

// TestTheGateNamesAnUncoveredTable is the site-canary table, whose whole subject is a
// sample rather than a census.
//
// The fixture derives three site-bearing tables and canaries one of them. The two
// uncovered ones must both be named, and neither may pass because it is "probably fine".
func TestTheGateNamesAnUncoveredTable(t *testing.T) {
	root := fixture(t, baseFixture())

	l := Ledger{}
	table, err := DeriveSiteScopedWriteCanaries(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	problems := (&Report{Tables: []Table{table}}).Verify(l)

	for _, want := range []string{"cameras", "zones"} {
		if !anyMentions(problems, "SiteScopedWriteCanaries", want) {
			t.Errorf("an uncovered site-bearing table was not named: %s\nProblems: %v",
				want, problems)
		}
	}
	if anyMentions(problems, "SiteScopedWriteCanaries", "incidents") {
		t.Error("incidents carries primary_site_id rather than site_id and is not derived " +
			"as site-bearing, so it cannot appear in this table at all. Its appearing here " +
			"means the derivation matched something it should not have")
	}
}

// TestTheGateNamesAnUnwrittenMetric is the metric table.
//
// The fixture's production code writes one metric and declares another. The unwritten one
// is the defect this table exists for: a counter seeded to zero on every install, reported
// by a scrape, read by a dashboard, and never once incremented.
//
// The input is the CONSTANT rather than the exposition name, because the constant is what
// a call site writes and the exposition name is what a dashboard queries. Feeding the
// exposition name found nothing at all on the first run, which reported all 26 metrics as
// unwritten - a table with a false green in the direction that hides nothing.
func TestTheGateNamesAnUnwrittenMetric(t *testing.T) {
	root := fixture(t, baseFixture())

	l := Ledger{}
	table := DeriveMetricProducers(mustCorpus(t, root), []string{
		"CounterInboxAccepted", "CounterEvidenceFetched",
	})
	problems := (&Report{Tables: []Table{table}}).Verify(l)

	if !anyMentions(problems, "MetricProducers", "CounterEvidenceFetched") {
		t.Fatalf("a metric nothing writes was not named.\nProblems: %v\n\n"+
			"`observability.NewCounters` seeds every declared name to zero and `Render` "+
			"emits it on every scrape, so an unwritten metric is a dashboard reading 0 "+
			"forever and calling the site healthy", problems)
	}
	if anyMentions(problems, "MetricProducers", "CounterInboxAccepted") {
		t.Error("the metric that IS written was reported as uncovered, which means the " +
			"write-shape patterns are too tight")
	}
}

// TestTheGateNamesAMissingHandler proves the comment filter works, which is what stops
// the metric table from passing on a mention.
//
// The mutation moves the write into a comment. A gate that counted mentions would pass;
// this one must not.
func TestTheGateNamesAMovedIntoAComment(t *testing.T) {
	root := fixture(t, baseFixture())
	replaceIn(t, root, "internal/pipeline/pipeline.go",
		"l.inc(observability.CounterInboxAccepted)",
		"// l.inc(observability.CounterInboxAccepted)")

	l := Ledger{}
	table := DeriveMetricProducers(mustCorpus(t, root), []string{"CounterInboxAccepted"})
	problems := (&Report{Tables: []Table{table}}).Verify(l)

	if !anyMentions(problems, "MetricProducers", "CounterInboxAccepted") {
		t.Fatalf("commenting out the only write left the metric reported as covered.\n"+
			"Problems: %v\n\n"+
			"A name mentioned in a comment is not a producer, and a gate that cannot tell "+
			"the difference reports coverage it does not have", problems)
	}
}

// TestTheGateNamesAMissingPartition proves the horizon table is not vacuous.
//
// The fixture's partitions reach three months past the fixed `today`, against an
// eighteen-month horizon. It has to fail, and it has to say which date it needed - a
// failure that says "not enough partitions" without saying which month sends ingest into
// the DEFAULT partition is a failure an operator cannot act on.
func TestTheGateNamesAMissingPartition(t *testing.T) {
	root := fixture(t, baseFixture())

	today := mustDate(t, "2026-10-05T00:00:00Z")
	table, err := DerivePartitionHorizon(root, today, 18)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	problems := (&Report{Tables: []Table{table}}).Verify(Ledger{})

	if !anyMentions(problems, "PartitionHorizon", "inbox_events") {
		t.Fatalf("a table short of the horizon was not named.\nProblems: %v", problems)
	}
	// The message has to carry the date, or the finding is not actionable.
	carriesDate := false
	for _, p := range problems {
		if strings.Contains(p, "2028-04-05") {
			carriesDate = true
		}
	}
	if !carriesDate {
		t.Errorf("the finding does not name the month the table runs out.\nProblems: %v\n\n"+
			"An operator reading this has to be told which date sends every accepted event "+
			"into one unpartitioned relation", problems)
	}
}

// TestTheGateNamesARouteTheContractDeclaresAndTheRouterDoesNotHave is the contract table,
// in the direction the plan names: a declared operation with no route is a 404 a
// generated client will hit.
func TestTheGateNamesAnUnimplementedRoute(t *testing.T) {
	root := fixture(t, baseFixture())

	l := Ledger{}
	table, err := DeriveContractRouting(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	problems := (&Report{Tables: []Table{table}}).Verify(l)

	if !anyMentions(problems, "ContractRouting", "POST /v1/rules/bundles/9/lint") {
		t.Fatalf("a declared-but-unrouted operation was not named.\nProblems: %v", problems)
	}
}

// TestTheGateNamesARouteTheRouterHasAndTheContractDoesNot is the OTHER direction, which
// is the one a one-directional gate misses and the reason this table asserts both.
//
// `GET /v1/metrics` is implemented, works, is in no contract, and is therefore
// unreachable by anything generated from the contract. A client author reading the
// document has no way to learn the surface exists.
func TestTheGateNamesAnUndeclaredRoute(t *testing.T) {
	root := fixture(t, baseFixture())

	l := Ledger{}
	table, err := DeriveContractRouting(root)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	problems := (&Report{Tables: []Table{table}}).Verify(l)

	if !anyMentions(problems, "ContractRouting", "GET /v1/metrics") {
		t.Fatalf("an implemented-but-undeclared route was not named.\nProblems: %v\n\n"+
			"This is the direction a one-directional parity check never reports, and it is "+
			"the reason DeriveContractRouting walks both ways", problems)
	}
}

// TestTheGateNamesACapabilityNamedOnlyInItsOwnPackage proves the capability table
// excludes the declaring package.
//
// The fixture's `internal/api/handlers.go` mentions the constant inside a COMMENT that
// records it as unenforced, and `internal/identity/identity.go` declares it. Neither
// counts. If the declaring package counted as a use, the table would report it covered -
// which is exactly the false green the exclusion exists to prevent, and it is the one this
// repository's own gates walk into constantly, because explaining why a capability is NOT
// enforced means writing the constant down.
func TestTheGateNamesACapabilityNamedOnlyInItsOwnPackage(t *testing.T) {
	root := fixture(t, baseFixture())

	c := mustCorpus(t, root)
	table := DeriveCapabilityEnforcement(c, []string{"CapSiteConfigure"})
	problems := (&Report{Tables: []Table{table}}).Verify(Ledger{})

	if !anyMentions(problems, "CapabilityEnforcement", "CapSiteConfigure") {
		t.Fatalf("a capability named only by its own declaration and a comment was reported "+
			"as enforced.\nProblems: %v\n\n"+
			"The declaration is not a use, and a comment explaining that a capability is not "+
			"enforced is not a use either. A capability nothing enforces grants nothing",
			problems)
	}
}

// TestACapabilityEnforcedElsewhereIsCovered is the other half: a reference in non-test
// code OUTSIDE the declaring package counts.
//
// It is the test that stops the exclusion from being over-broad. A prefix that excluded
// too much would report every capability as unenforced, which reads as a tree full of
// debt rather than as a broken rule.
func TestACapabilityEnforcedElsewhereIsCovered(t *testing.T) {
	root := fixture(t, baseFixture())
	c := mustCorpus(t, root)
	table := DeriveCapabilityEnforcement(c, []string{"CapIncidentRead"})
	for _, item := range table.Items {
		if !item.Covered {
			t.Errorf("CapIncidentRead is named by a route outside internal/identity and was "+
				"reported as uncovered, so the exclusion prefix is too wide: %v", item)
		}
	}
}

// TestAGeneratedFileDoesNotCountAsAProducer keeps the corpus honest about
// `internal/contracts`.
//
// The embedded copies of the published schemas are generated and they name every
// dispatch type. If they counted, DispatchExecutors would pass on a file nothing executes
// - which is the same false green as counting a comment, reached by a different route.
func TestAGeneratedFileDoesNotCountAsAProducer(t *testing.T) {
	root := fixture(t, baseFixture())
	body, err := readFixtureFile(root, "internal/contracts/embedded.go")
	if err != nil {
		t.Fatalf("read the embedded copy: %v", err)
	}
	if !strings.HasPrefix(body, "// Code generated by protoc-gen-go. DO NOT EDIT.") {
		t.Fatalf("the fixture's embedded file does not carry the generated marker, so this "+
			"test would be testing a file the corpus does not skip. First line was: %q",
			strings.SplitN(body, "\n", 2)[0])
	}

	c := mustCorpus(t, root)
	table := DeriveMetricProducers(c, []string{"fetch_evidence_total"})
	problems := (&Report{Tables: []Table{table}}).Verify(Ledger{})
	if !anyMentions(problems, "MetricProducers", "fetch_evidence_total") {
		t.Fatalf("a metric named only by a generated file was reported as covered.\n"+
			"Problems: %v", problems)
	}
}

// TestAddMonthsClampsRatherThanOverflows is the small arithmetic the horizon table rests
// on, and it has its own test because `time.AddDate` gets it wrong in a way that makes
// the gate's answer depend on the day it ran.
func TestAddMonthsClampsRatherThanOverflows(t *testing.T) {
	// 31 January plus one month. `AddDate(0, 1, 0)` gives 2 or 3 March depending on the
	// month's length, so a table that passed on the 30th would fail on the 31st with no
	// change to the tree.
	got := addMonths(mustDate(t, "2026-01-31T12:00:00Z"), 1)
	want := "2026-02-28T12:00:00Z"
	if got.Format("2006-01-02T15:04:05Z") != want {
		t.Errorf("addMonths(2026-01-31, 1) = %s, want %s", got.Format("2006-01-02T15:04:05Z"), want)
	}
}

// TestAddMonthsRollsTheYear pins the other direction: the horizon is measured in months,
// so a December boundary has to cross into the next year rather than wrapping.
func TestAddMonthsRollsTheYear(t *testing.T) {
	got := addMonths(mustDate(t, "2026-12-15T00:00:00Z"), 18)
	if y, m, _ := got.Date(); y != 2028 || m != 6 {
		t.Errorf("addMonths(2026-12-15, 18) = %s, want 2028-06", got.Format("2006-01-02"))
	}
}

// ---------------------------------------------------------------------------
// Fixture plumbing
// ---------------------------------------------------------------------------

var allDispatchTypes = map[string]bool{
	"notify_operator": true, "notify_supervisor": true,
	"request_vlm": true, "fetch_evidence": true,
}

// reportFor runs the two derivations the fixture tree can answer.
//
// Only DispatchExecutors and QueueEndpoints are derived, because those are the two the
// fixture mutations touch, and running the other five against a partial tree would report
// findings about files the fixture deliberately does not have.
func reportFor(t *testing.T, root string, dispatchTypes map[string]bool, queues []Queue) Report {
	t.Helper()
	var tables []Table
	if dispatchTypes != nil {
		table, err := DeriveDispatchExecutors(root, dispatchTypes)
		if err != nil {
			t.Fatalf("derive DispatchExecutors: %v", err)
		}
		tables = append(tables, table)
	}
	if queues != nil {
		tables = append(tables, DeriveQueueEndpoints(mustCorpus(t, root), queues))
	}
	return Report{Tables: tables}
}

func mustCorpus(t *testing.T, root string) *Corpus {
	t.Helper()
	c, err := LoadCorpus(root)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	return c
}

func anyMentions(problems []string, table, item string) bool {
	for _, p := range problems {
		if strings.Contains(p, table) && strings.Contains(p, item) {
			return true
		}
	}
	return false
}

// replaceIn applies a single mutation and fails loudly if the anchor is not there.
//
// A mutation that does not apply is the most dangerous outcome of a red-check: the gate
// passes, the mutation did nothing, and the transcript records a control that was
// exercised and proved nothing. This is the guard against that.
func replaceIn(t *testing.T, root, rel, from, to string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	body := string(raw)
	if !strings.Contains(body, from) {
		t.Fatalf("the mutation does not apply: %s does not contain the anchor.\n"+
			"  anchor: %q\n"+
			"Without this check a mutation that silently did nothing would leave the gate "+
			"green and the transcript would record a control that was never exercised.",
			rel, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(body, from, to, 1)), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func readFixtureFile(root, rel string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// baseFixture is the tree every mutation above starts from.
//
// The Go files carry the shapes the real ones carry and nothing else: the point is that
// the derivations are exercised against syntax that exists in this repository, so a
// derivation that breaks on a real file breaks here too.
func baseFixture() map[string]string {
	return map[string]string{
		"internal/dispatch/dispatch.go": `package dispatch

// Dispatch is one side effect a rule asked for.
type Dispatch struct {
	Type   string
	Target string
}

// registeredExecutors is the closed set of dispatch types this package performs.
func registeredExecutors(d Dispatch) bool {
	switch d.Type {
	case "notify_operator":
		return true
	case "notify_supervisor":
		return true
	case "request_vlm":
		return true
	case "fetch_evidence":
		return true
	}
	return false
}
`,
		"internal/pipeline/pipeline.go": `package pipeline

import "github.com/Sergey-Bar/vision/internal/observability"

func Consume() {
	l.inc(observability.CounterInboxAccepted)
}
`,
		"cmd/security-core/main.go": `package main

import (
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/observability"
)

func run() {
	jobWorker := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM}
	_ = jobWorker
	registry.Counters.Inc(observability.CounterAuditAppends)

	// CapIncidentRead gates the incident routes and is a real enforcement site.
	mux.Handle("GET /v1/incidents", requireCapability(identity.CapIncidentRead))

	// CapSiteConfigure is named here in a COMMENT ONLY, on purpose. This file exists to
	// record that the capability is declared and granted and enforced by nothing, and
	// naming the constant is how you write that down. A gate that counts comment
	// mentions as enforcement reports this site's own warning as its coverage.
}
`,
		"internal/identity/identity.go": `package identity

// CapSiteConfigure is declared and granted and enforced by no route.
const CapSiteConfigure = "site.configure"

// CapIncidentRead gates the incident routes.
const CapIncidentRead = "incident.read"
`,
		"internal/api/handlers.go": `package api

func RegisterRoutes(s *Server, mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/incidents", s.list)
	mux.HandleFunc("GET /v1/metrics", s.metrics)
	// The console: HTML for a browser, and deliberately not in the contract.
	mux.HandleFunc("GET /incidents", s.console)
}
`,
		"internal/rlsrole/write_canary_test.go": `package rlsrole

var writeCanaries = []writeCanary{
	{table: "incidents"},
}
`,
		"internal/contracts/embedded.go": `// Code generated by protoc-gen-go. DO NOT EDIT.

package contracts

// A generated copy of the published schema, naming every metric and dispatch type.
const Schema = "notify_operator notify_supervisor request_vlm fetch_evidence " +
	"fetch_evidence_total incident_visibility_seconds"
`,
		"contracts/openapi/v1/openapi.yaml": `openapi: 3.1.0
paths:
  /v1/health:
    get:
      responses:
        "200":
          description: ok
  /v1/incidents:
    get:
      responses:
        "200":
          description: ok
  /v1/rules/bundles/9/lint:
    post:
      responses:
        "200":
          description: ok
`,
		"migrations/0001_partitions.expand.sql": `-- Partitioned parents. Note the trailing clause: PARTITION BY RANGE sits AFTER the
-- column list's closing paren, which is why the derivation matches it against the whole
-- file rather than against a semicolon-delimited statement. Splitting first loses
-- exactly the clause being looked for.
CREATE TABLE inbox_events (
    event_id     TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL
) PARTITION BY RANGE (received_at);

CREATE TABLE canonical_events (
    event_id     TEXT NOT NULL,
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL
) PARTITION BY RANGE (observed_at);

CREATE TABLE inbox_events_2026_01 PARTITION OF inbox_events
  FOR VALUES FROM ('2026-01-01T00:00:00Z') TO ('2026-02-01T00:00:00Z');
CREATE TABLE inbox_events_2026_11 PARTITION OF inbox_events
  FOR VALUES FROM ('2026-11-01T00:00:00Z') TO ('2026-12-01T00:00:00Z');
CREATE TABLE inbox_events_default PARTITION OF inbox_events DEFAULT;

CREATE TABLE canonical_events_2026_01 PARTITION OF canonical_events
  FOR VALUES FROM ('2026-01-01T00:00:00Z') TO ('2026-02-01T00:00:00Z');
CREATE TABLE canonical_events_2026_11 PARTITION OF canonical_events
  FOR VALUES FROM ('2026-11-01T00:00:00Z') TO ('2026-12-01T00:00:00Z');
CREATE TABLE canonical_events_default PARTITION OF canonical_events DEFAULT;
`,
		"migrations/0002_site_tables.expand.sql": `-- Site-bearing tables, three of them.
CREATE TABLE cameras (
    camera_id  TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    site_id    TEXT NOT NULL
);

CREATE TABLE zones (
    zone_id    TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    site_id    TEXT NOT NULL
);

CREATE TABLE incidents (
    incident_id      TEXT NOT NULL,
    tenant_id        TEXT NOT NULL,
    primary_site_id  TEXT NOT NULL
);
`,
	}
}
