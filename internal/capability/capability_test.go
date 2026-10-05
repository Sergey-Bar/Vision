// Package capability_test holds the ledger and the gate.
//
// # The ledger is the mechanism, and it is the part under test
//
// Seven tables, thirty-odd derivations, and one place where the gate can be made to
// pass without anything being implemented: the refusal ledger. So the ledger gets the
// same treatment `internal/rlsrole` gave `unprotectedTables` and `internal/deadexport`
// gave its allow-list - it is asserted to be *accurate*, in both directions, before the
// gate is trusted to be accurate.
//
// Four assertions, and each one exists because its absence is a way this gate could
// report coverage it does not have:
//
//	TestEveryUncoveredItemIsRefused      a gap with no entry is a failure
//	TestTheLedgerIsAccurate             a stale entry is a failure, because a refusal for
//	                                    a closed defect reads as work that remains
//	TestEveryRefusalSaysWhy             an empty reason is a failure, because a refusal
//	                                    with no reason is a suppression with a name
//	TestTheGateFailsWhenItShould        the gate is run against fixtures with a
//	                                    deliberately-removed executor and a
//	                                    deliberately-stale entry, and MUST fail
//
// The fourth is the one that matters. A coverage gate that has never been observed red
// is a gate that has only been observed passing, and `scripts/verify.ps1`'s red-check
// table exists because this repository has thirty such gates.
package capability_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/capability"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// repoRoot is two levels up from this package, and is resolved by walking to the
// `go.mod` rather than assumed: a gate pointed at a directory with no migrations in it
// derives an empty list and reports everything uncovered, which is indistinguishable
// from a gate reporting a broken tree.
const repoRoot = "../.."

// readRepoFile reads one file from the repository root.
//
// Walked rather than hardcoded, because `internal/rlsrole` does the same and a hardcoded
// depth is correct only until this package moves.
func readRepoFile(rel string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			raw, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if readErr != nil {
				return "", readErr
			}
			return string(raw), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errRepoRootNotFound
		}
		dir = parent
	}
	return "", errRepoRootNotFound
}

var errRepoRootNotFound = errors.New("could not find the repository root")

// refusal is one ledger entry, read as a struct so the compiler refuses a call with
// the arguments the wrong way round. `Refuse(table, item, why)` has three same-shaped
// string arguments and the map form does not.
type refusal struct {
	table string
	item  string
	why   string
}

// The ledger.
//
// Every entry answers two questions a reader has to be able to answer without asking:
// what is not working, and what would make it work. An entry that answers only the
// first is a backlog item with extra steps.
//
// The DispatchExecutors group is GONE, and its absence is the point.
//
// It landed with four refusals and `internal/dispatch` closed all four. The gate then
// failed on its own stale-refusal check - "fetch_evidence is in the refusal ledger and IS
// now covered" - which is exactly the friction the two-direction rule exists to create. An
// exemption that survives its defect closing is an exemption nobody is maintaining.
//
// The remaining two groups:
//
//   - Queues. Three of the four declared queues have no producer at all. These are scope
//     decisions, not defects waiting to be fixed by the next person to look:
//     `rule_bulk` when bulk compile is scheduled, `checkpoint_seal` when sealing becomes incremental.
//   - Contract. Declared routes nothing implements, and one implemented route the contract
//     does not declare.
var refusals = []refusal{
	// --- QueueEndpoints.
	{table: "QueueEndpoints", item: "rule_bulk",
		why: "bulk compile is not an M1 capability and no milestone owns it; the constant " +
			"is declared so the queue vocabulary is complete"},
	{table: "QueueEndpoints", item: "checkpoint_seal",
		why: "sealing is a scheduler with no work queue; the constant is declared for the " +
			"vocabulary and the incremental version is a later milestone"},

	// --- CapabilityEnforcement: the four identity carries as recorded debt.
	//
	// These duplicate `internal/identity/capability_coverage_test.go` on purpose. That
	// file is the gate for the capability vocabulary alone; this is the same fact
	// counted in a coverage table that also counts six other things. The two are keyed
	// by the same CONSTANT names and `TestTheLedgerAgreesWithIdentityOnCapabilityDebt`
	// fails if either is updated without the other, so they cannot drift apart - which
	// two hand-maintained lists always do.
	{table: "CapabilityEnforcement", item: "CapIncidentEscalate",
		why: "no route gates an escalation, so the capability is declared and granted and " +
			"nothing reads it"},
	{table: "CapabilityEnforcement", item: "CapIdentityManage",
		why: "the identity management surface is not built; the capability is granted by " +
			"tenant_admin and enforced by no route"},
	{table: "CapabilityEnforcement", item: "CapSiteConfigure",
		why: "site configuration has no surface yet and no route names this capability"},
	{table: "CapabilityEnforcement", item: "CapDeadLetterReplay",
		why: "the replay control gates on dead_letter.read, so a separate replay " +
			"capability was declared and never used"},

	// --- ContractRouting: declared-and-absent, then present-and-undeclared.
	//
	// `GET /v1/health` and `GET /v1/health/ready` are NOT refusals even though the
	// document declares them and the mux registers them method-less. Go's mux treats a
	// method-less pattern as answering every method, so they ARE routed; the derivation
	// says so in its own comment. Calling them a gap here would be the gate reporting a
	// defect that is not one.
	{table: "ContractRouting", item: "POST /v1/incidents/{incidentId}/evidence",
		why: "the document declares a POST to fetch evidence on demand; there is no such " +
			"route, so a generated client gets a 404"},
	{table: "ContractRouting", item: "GET /v1/audit/checkpoints",
		why: "the document declares the checkpoint chain; the sealer writes sealed " +
			"archives to disk and nothing serves them over an endpoint"},
	{table: "ContractRouting", item: "POST /v1/rules/bundles/{version}/lint",
		why: "the document declares a lint endpoint; the same lint runs from " +
			"securityctl and over the verify gate, so the rule is checked and the route " +
			"is not"},
	{table: "ContractRouting", item: "POST /v1/rules/bundles/{version}/simulate",
		why: "the document declares a simulate endpoint; simulate runs at activation time " +
			"on the server and no operator can request one"},
	{table: "ContractRouting", item: "GET /v1/metrics",
		why: "the route is implemented and undeclared, so nothing a client generates from " +
			"the contract can reach it. The fix is a document change, not code"},

	// --- SiteScopedWriteCanaries.
	//
	// Three partitioned parents (canonical_events, inbox_events) are excluded by the
	// derivation because they hold no rows. `user_site_assignments` is deliberately
	// excluded from site_isolation. All other site-scoped tables now have write canaries
	// in internal/rlsrole/write_canary_test.go.
	{table: "SiteScopedWriteCanaries", item: "canonical_events",
		why: "partitioned parent excluded from canary derivation; RLS applies to the monthly partitions instead"},
	{table: "SiteScopedWriteCanaries", item: "inbox_events",
		why: "partitioned parent excluded from canary derivation; RLS applies to the monthly partitions instead"},
	{table: "SiteScopedWriteCanaries", item: "user_site_assignments",
		why: "deliberately excluded from site_isolation: scoping the table that defines the scope by that scope is circular, and the login path cannot get past the policy it needs in order to have it"},
}

// ledger is the refusals above in the form the gate reads.
func ledger() capability.Ledger {
	l := capability.Ledger{}
	for _, r := range refusals {
		if existing, already := l.Why(r.table, r.item); already {
			panic("duplicate refusal for " + r.table + "/" + r.item +
				", already carried with the reason: " + existing)
		}
		l.Refuse(r.table, r.item, r.why)
	}
	return l
}

// build is the whole report over the live tree.
//
// The vocabularies are passed in from their owning packages rather than derived from
// source text: `rules.DispatchTypes`, `identity.AllCapabilities` and the queue
// constants are closed sets that those packages publish, and a gate that re-derived them
// by reading the source would be a second list to keep in step - which is the defect this
// whole repository keeps finding in its own documents.
func build(t *testing.T) capability.Report {
	t.Helper()

	corpus, err := capability.LoadCorpus(repoRoot)
	if err != nil {
		t.Fatalf("load the tree's production source: %v", err)
	}

	dispatchTypes := map[string]bool{}
	for k, v := range rules.DispatchTypes {
		dispatchTypes[k] = v
	}

	dispatchTable, err := capability.DeriveDispatchExecutors(repoRoot, dispatchTypes)
	if err != nil {
		t.Fatalf("derive DispatchExecutors: %v", err)
	}

	queues := []capability.Queue{
		{Name: string(jobs.QueueVLM), Const: "QueueVLM"},
		{Name: string(jobs.QueueEvidenceFetch), Const: "QueueEvidenceFetch"},
		{Name: string(jobs.QueueRuleBulk), Const: "QueueRuleBulk"},
		{Name: string(jobs.QueueCheckpointSeal), Const: "QueueCheckpointSeal"},
	}

	// The capability input is the Go CONSTANT, because that is what a route names.
	// `incident.read` appears in a role table and in a database CHECK; `CapIncidentRead`
	// is what `handlers.go` writes.
	constants := capabilityConstants(t)

	contractTable, err := capability.DeriveContractRouting(repoRoot)
	if err != nil {
		t.Fatalf("derive ContractRouting: %v", err)
	}

	partitionTable, err := capability.DerivePartitionHorizon(repoRoot,
		time.Now().UTC(), capability.DefaultPartitionHorizonMonths)
	if err != nil {
		t.Fatalf("derive PartitionHorizon: %v", err)
	}

	canaryTable, err := capability.DeriveSiteScopedWriteCanaries(repoRoot)
	if err != nil {
		t.Fatalf("derive SiteScopedWriteCanaries: %v", err)
	}

	return capability.Report{Tables: []capability.Table{
		dispatchTable,
		capability.DeriveQueueEndpoints(corpus, queues),
		capability.DeriveMetricProducers(corpus, metricNames(t)),
		capability.DeriveCapabilityEnforcement(corpus, constants),
		contractTable,
		partitionTable,
		canaryTable,
	}}
}

// capabilityConstants maps every capability in the closed set back to the Go constant
// that declares it.
//
// `identity.AllCapabilities` is the authority on WHICH capabilities exist - it is closed
// and this package must not be a second list to keep in step. The constant NAME is the
// part identity does not export, and this reads it out of the declaration block for the
// same reason `internal/identity/capability_coverage_test.go` reads it out of the same
// block: exporting it would be an API for a test.
//
// It is worth a helper rather than being open-coded at the call site because a derivation
// fed the wire values instead of the constants finds nothing at all, and a table that
// derives nothing reports nothing.
func capabilityConstants(t *testing.T) []string {
	t.Helper()
	body, err := readRepoFile("internal/identity/identity.go")
	if err != nil {
		t.Fatalf("read the capability declarations: %v", err)
	}
	declared := map[string]bool{}
	for _, c := range identity.AllCapabilities() {
		declared[string(c)] = true
	}

	byValue := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		marker := `Capability = "`
		at := strings.Index(trimmed, marker)
		if at < 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[:at])
		rest := trimmed[at+len(marker):]
		value, _, _ := strings.Cut(rest, `"`)
		byValue[value] = name
	}

	out := make([]string, 0, len(declared))
	for _, c := range identity.AllCapabilities() {
		constant, ok := byValue[string(c)]
		if !ok {
			t.Fatalf("capability %q is in AllCapabilities but no `Capability = \"...\"` line "+
				"declares it, so this gate cannot see whether anything enforces it. That is "+
				"reported rather than skipped: a gate that silently covers less than it "+
				"claims is the defect this gate exists for", c)
		}
		out = append(out, constant)
	}
	if len(out) == 0 {
		t.Fatal("no capability constants read from identity, so this gate would pass on an " +
			"empty list")
	}
	return out
}

// metricNames reads the declared names out of the registry's const block.
//
// Read from source rather than imported, because `observability` publishes the
// constants and deliberately does not publish the map: `counterMetrics` and
// `gaugeMetrics` are unexported, and the registry seeds every one of them to zero on
// construction, so "the registry says this name exists" and "the registry says nothing
// wrote it" are the same fact.
func metricNames(t *testing.T) []string {
	t.Helper()
	body, err := readRepoFile("internal/observability/metrics.go")
	if err != nil {
		t.Fatalf("read the metric registry: %v", err)
	}
	var out []string
	inBlock := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "const ("):
			inBlock = true
			continue
		case inBlock && trimmed == ")":
			inBlock = false
			continue
		}
		if !inBlock {
			continue
		}
		_, value, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		name := strings.TrimSpace(value)
		if len(name) < 2 || !strings.HasPrefix(name, `"`) || !strings.HasSuffix(name, `"`) {
			continue
		}
		out = append(out, strings.Trim(name, `"`))
	}
	if len(out) == 0 {
		t.Fatal("no metric names read from the registry's const block, so this gate would " +
			"pass on an empty list. That is the failure a derivation that stopped working " +
			"looks like")
	}
	return out
}

// TestEveryDeclaredThingIsCoveredOrRefused is the gate.
//
// One test rather than seven, because the seven answer one question and a reader
// should not have to run seven to find out whether the answer is yes. The per-table
// detail is in the failure message and in the summary the gate prints.
func TestEveryDeclaredThingIsCoveredOrRefused(t *testing.T) {
	report := build(t)
	ledger := ledger()

	problems := report.Verify(ledger)
	for _, p := range problems {
		t.Errorf("%s", p)
	}

	// The summary is printed on every run, refusals included and in full. A gate whose
	// exemptions are visible only on the day they are exercised is a gate nobody audits
	// in advance, and that is where dead entries accumulate.
	t.Log("\n" + report.Summary(ledger))
}

// TestEveryUncoveredItemIsRefused is the first half of the gate's trustworthiness,
// stated separately so that a failure names which half broke.
//
// It is also the assertion that stops the ledger from becoming the whole gate: delete
// an entry and this fails, which means an entry cannot quietly become the only thing
// keeping a known gap from being a failure.
func TestEveryUncoveredItemIsRefused(t *testing.T) {
	report := build(t)
	ledger := ledger()

	for _, table := range report.Tables {
		for _, item := range table.Items {
			if item.Covered {
				continue
			}
			if _, refused := ledger.Why(table.Name, item.Name); !refused {
				t.Errorf("%s/%s has no executor and no refusal entry.\n"+
					"  what it needs: %s\n"+
					"  This is the defect class this gate exists for: something the product "+
					"declares that nothing does.", table.Name, item.Name, item.Evidence)
			}
		}
	}
}

// TestTheLedgerIsAccurate is the half that makes it a ledger rather than a comment.
//
// It fails when a refused item IS now covered, and the failure says "delete the entry"
// rather than leaving a stale exemption sitting in a document about work that is
// already done - which reads as remaining work while hiding the work that is not.
//
// It also fails when the ledger names an item the table no longer derives. A refusal
// for something that does not exist is a refusal that can never be closed, and it is
// how a ledger grows entries nobody is maintaining.
func TestTheLedgerIsAccurate(t *testing.T) {
	report := build(t)
	ledger := ledger()

	known := map[string]bool{}
	for _, table := range report.Tables {
		known[table.Name] = true
		present := map[string]bool{}
		for _, item := range table.Items {
			present[item.Name] = true
			if !item.Covered {
				continue
			}
			if _, refused := ledger.Why(table.Name, item.Name); refused {
				t.Errorf("%s/%s is refused AND covered by %s.\n"+
					"  Delete the entry. Leaving it is an exemption for a defect that has "+
					"been fixed, and it reads as work that remains while hiding the work "+
					"that does not.", table.Name, item.Name, item.Evidence)
			}
		}
		for _, item := range ledger.Items(table.Name) {
			if !present[item] {
				t.Errorf("%s/%s is in the ledger and the table does not derive it any "+
					"more. It was renamed, removed, or never existed - delete the entry",
					table.Name, item)
			}
		}
	}

	for _, table := range ledger.Tables() {
		if !known[table] {
			t.Errorf("the ledger names the table %q, which this report does not have. Either "+
				"it was renamed or deleted and every refusal under it is now carrying no "+
				"weight, or the ledger was hand-written for a table that was never built",
				table)
		}
	}
}

// TestEveryRefusalSaysWhy is the anti-decorative rule.
//
// A refusal with no reason is a suppression with a name, and a reason that only
// restates the item is the same thing wearing a sentence. `capability.Reasonable`
// requires the reason to say what closing it takes - code, a migration, an endpoint, a
// milestone, or the instruction to delete the thing.
//
// This is deliberately a crude test on prose and it is stated as one. A rule about
// reasons that a determined contributor can satisfy without the reason becoming true is
// a rule with no force; the mechanism is what stops the ENTRY existing, and this only
// stops the entry being empty.
func TestEveryRefusalSaysWhy(t *testing.T) {
	for _, r := range refusals {
		if !capability.Reasonable(r.why) {
			t.Errorf("the refusal for %s/%s does not say why.\n  got: %q\n"+
				"  A refusal has to name what closing it takes, so a reader can tell work "+
				"that remains from an exemption nobody will revisit.", r.table, r.item, r.why)
		}
	}
}

// TestTheLedgerAgreesWithIdentityOnCapabilityDebt keeps the two capability ledgers in
// step.
//
// `internal/identity/capability_coverage_test.go` carries the same capabilities as
// `capabilitiesDeclaredButNotYetEnforced`, and this table carries them again by their
// CONSTANT name. Two ledgers for one fact drift the moment one is updated, and then this
// gate reports a capability as covered while identity's own gate reports it as carried
// debt - or the reverse, which is worse, because a reader has two sources disagreeing and
// no way to tell which is current.
//
// So the two are compared. This is not the two gates checking each other: this one reads
// identity's file as text, because identity does not export its ledger and exporting a
// test's ledger to satisfy another test's gate would be an API for a test.
func TestTheLedgerAgreesWithIdentityOnCapabilityDebt(t *testing.T) {
	body, err := readRepoFile("internal/identity/capability_coverage_test.go")
	if err != nil {
		t.Fatalf("read identity's capability ledger: %v", err)
	}

	// `capabilitiesDeclaredButNotYetEnforced`'s keys, which are constant names already -
	// the same shape this table's ledger uses, which is why the comparison needs no
	// mapping between the two files.
	carried := map[string]string{}
	inMap := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "var capabilitiesDeclaredButNotYetEnforced") {
			inMap = true
			continue
		}
		if !inMap {
			continue
		}
		if trimmed == "}" {
			break
		}
		constant, rest, found := strings.Cut(trimmed, `":`)
		if !found {
			continue
		}
		constant = strings.Trim(strings.TrimSpace(constant), `"`)
		if !strings.HasPrefix(constant, "Cap") {
			continue
		}
		reason, _, _ := strings.Cut(rest, `",`)
		carried[constant] = strings.TrimSpace(reason)
	}
	if len(carried) == 0 {
		t.Fatal("no entries read from identity's carried-debt map. This comparison would " +
			"silently agree with nothing, which is the failure a parser that stopped " +
			"matching looks like")
	}

	declaredHere := map[string]bool{}
	for _, constant := range capabilityConstants(t) {
		declaredHere[constant] = true
	}

	ledger := ledger()
	for _, item := range ledger.Items("CapabilityEnforcement") {
		if _, known := carried[item]; !known {
			t.Errorf("this ledger refuses %s, which identity's own ledger does not carry. "+
				"One of the two has been updated and the other has not - the two ledgers "+
				"describe the same fact and a reader has no way to tell which is current", item)
		}
	}
	for constant := range carried {
		if !declaredHere[constant] {
			t.Errorf("identity carries %s, which is not a declared capability any more. "+
				"That entry is dead weight in identity's own ledger and should be deleted "+
				"there", constant)
			continue
		}
		if _, here := ledger.Why("CapabilityEnforcement", constant); !here {
			t.Errorf("identity carries %s as unenforced debt and this table has no refusal "+
				"for it. If identity's entry was the stale one it should be deleted there; "+
				"if it was correct, this table is about to report a capability as covered "+
				"that nothing enforces", constant)
		}
	}
}
