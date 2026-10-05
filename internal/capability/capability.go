// Package capability is the gate for the "declared but does not happen" class.
//
// # Why this exists
//
// The compiler and the linter cannot see this defect, and neither can a unit test.
// `then.dispatch: [{type: notify_operator, target: on-call}]` in a rule bundle parses,
// compiles, validates against a closed set, and is discarded: `rules.Outcome` had no
// field to carry it, `outbox.Enqueue` had no production caller, and the relay drained an
// empty table. Every one of the tests passed. So did `go build`, `go vet`, `gofmt` and
// `golangci-lint`, which report zero issues on this repository today.
//
// The shape is always the same: something the product declares - a rule's dispatch, a
// work queue, a metric name, a capability, a route in the published contract, a table's
// partitions, a table's write isolation - has a *reader* or an *executor* that nothing in
// the tree reaches. `internal/deadexport` was written for the same class at the symbol
// level and found thirty-three genuinely dead exported functions; this is the
// declaration level, and it is the version that reaches the things an operator is told
// will happen.
//
// # The seven tables
//
// Each table is a list of declarations derived from the tree, and each entry is either
// covered or carries a refusal naming why. The tables are:
//
//	DispatchExecutors       every rules.DispatchTypes key has an executor
//	QueueEndpoints          every jobs.Queue* has an enqueue caller and a worker
//	MetricProducers         every name in observability/metrics.go is written
//	CapabilityEnforcement   every identity.Capability gates a route, or is refused
//	ContractRouting         every openapi operation is on the mux, and the reverse
//	PartitionHorizon        every RANGE parent covers >= 18 months from today
//	SiteScopedWriteCanaries every site-bearing table has a write canary
//
// Every one of them derives its list rather than carrying one. A hand-written list is a
// list that is wrong the moment somebody adds a row, and `internal/statuscheck` and
// `internal/sqlguard` both exist because this repository has learned that twice.
//
// # The two rules that make it trustworthy
//
// A coverage table with no falsification rule is a table of plausible entries nobody can
// check, which is how thirty unrun controls once looked like thirty guarded ones. So:
//
//  1. **Closing a refusal requires deleting the entry.** A refused item that has become
//     covered is a FAILURE, and the message says so. The alternative is a stale
//     exemption sitting in a document about work that is already done, which reads as
//     remaining work and hides the work that is not.
//
//  2. **A refusal requires a non-empty reason**, and a reason that does not say what
//     closing it takes is rejected. "Not implemented yet" is not a reason; "the
//     dispatcher lands later in this branch" is.
//
// Both directions are asserted against a fixture in this package's own tests, so the
// gate is proved able to fail before it is trusted to pass.
//
// # The idiom this reuses
//
// `internal/rlsrole`'s `unprotectedTables` and `internal/deadexport`'s allow-list, and
// `TestTheUnprotectedLedgerIsConsulted`'s trick of failing when an entry is stale. This
// is the third such ledger in this repository and the second built to be emptied, which
// is the intended terminal state: an empty ledger with the check still running. "Passing
// because there is nothing to check" is a state somebody can reach by deleting entries
// instead of closing defects, and rule 1 forbids it.
package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Table is one coverage table: a named assertion over a derived list.
type Table struct {
	// Name is the table's identifier and the first half of every ledger key.
	Name string
	// Asserts is the claim the table makes, in one sentence, carried into the failure
	// message. It is prose on purpose: the person who hits this needs to know what
	// property was violated, not which function returned false.
	Asserts string
	// Items are the declarations the table covers, sorted by name.
	Items []Item
}

// Item is one declaration and whether the tree gives it an executor.
type Item struct {
	// Name identifies the declaration: a dispatch type, a queue, a metric, a
	// capability, a `METHOD /path`, a partitioned parent, a table.
	Name string
	// Covered is what the derivation found. It is computed, never declared.
	Covered bool
	// Evidence names where the coverage was found, or what it would take to get it.
	// It is what makes a finding actionable rather than merely true.
	Evidence string
}

// String renders one item.
func (i Item) String() string {
	if i.Covered {
		return i.Name + " (covered by " + i.Evidence + ")"
	}
	return i.Name + " (needs " + i.Evidence + ")"
}

// Report is every table's result.
type Report struct {
	Tables []Table
}

// Table returns one table by name.
func (r Report) Table(name string) (Table, bool) {
	for _, t := range r.Tables {
		if t.Name == name {
			return t, true
		}
	}
	return Table{}, false
}

// Uncovered returns the items in one table that the tree gives no executor to.
func (r Report) Uncovered(table string) []string {
	t, ok := r.Table(table)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range t.Items {
		if !item.Covered {
			out = append(out, item.Name)
		}
	}
	return out
}

// Covered returns the items in one table that the tree gives an executor to.
func (r Report) Covered(table string) []string {
	t, ok := r.Table(table)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range t.Items {
		if item.Covered {
			out = append(out, item.Name)
		}
	}
	return out
}

// Ledger is the recorded refusals, by table then by item.
//
// A `map[string]map[string]string` rather than a flat map keyed by a joined string,
// because the item names contain slashes: the ContractRouting items are
// `GET /v1/audit/ledger`, and a `/`-joined key cannot be split back apart.
type Ledger map[string]map[string]string

// Refuse records one gap and what closing it takes.
//
// Refusing an item that is already refused replaces the reason, which is how a closing
// commit and its ledger edit travel together: the new reason describes what is left, not
// what was left.
func (l Ledger) Refuse(table, item, why string) {
	if l[table] == nil {
		l[table] = map[string]string{}
	}
	l[table][item] = why
}

// Why returns the reason a table's item is refused.
func (l Ledger) Why(table, item string) (string, bool) {
	items, ok := l[table]
	if !ok {
		return "", false
	}
	why, ok := items[item]
	return why, ok
}

// Tables lists the ledger's tables, sorted.
func (l Ledger) Tables() []string {
	out := make([]string, 0, len(l))
	for t := range l {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Items lists one table's refused items, sorted.
func (l Ledger) Items(table string) []string {
	items := l[table]
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

// Reasonable reports whether a reason says what it has to.
//
// A refusal whose reason is empty is a suppression with a name. A reason that only
// restates the item - "notify_operator is not wired up" - is the same thing wearing a
// sentence, so a reason has to mention what closing it takes.
//
// This is deliberately a crude test on prose and it is stated as one. A rule about
// reasons that a determined contributor learns to satisfy without the reason becoming
// true is a rule with no force. The mechanism that actually prevents an unfounded
// exemption is `Verify`'s stale check: an entry cannot survive the defect closing. This
// only stops an entry being empty, which is the half that is cheap to do by accident.
func Reasonable(why string) bool {
	w := strings.ToLower(strings.TrimSpace(why))
	if w == "" {
		return false
	}
	for _, needs := range []string{
		// What closing it takes.
		"code", "migrat", "endpoint", "milestone", "delete", "wire", "implement",
		"contract", "phase", "owner", "land", "later", "closes", "canary", "insert",
		"policy", "surface", "executor", "worker", "statement",
		// A statement of the gap that also says where it stands.
		"no route", "no such route", "not in", "nothing ", "gates on", "declared",
		"not an m1", "never used", "excluded",
	} {
		if strings.Contains(w, needs) {
			return true
		}
	}
	return false
}

// Verify checks a report against a ledger and returns every problem.
//
// It is the whole gate, and it returns a list rather than the first failure because the
// person fixing this needs the entire list: the seven tables are independent defects, and
// a gate that stops at the first is a gate that takes seven runs to clear.
func (r Report) Verify(l Ledger) []string {
	var problems []string

	known := map[string]bool{}
	for _, t := range r.Tables {
		known[t.Name] = true
	}

	// Every uncovered item is refused, and every refusal says why.
	for _, t := range r.Tables {
		for _, item := range t.Items {
			if item.Covered {
				continue
			}
			why, refused := l.Why(t.Name, item.Name)
			if !refused {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is declared and nothing implements it.\n  what it needs: %s\n"+
						"  Either close it, or record it in the capability ledger with the "+
						"reason and what closing it takes. An unrecorded gap is a silent "+
						"no-op, which is the one class this gate exists for",
					t.Name, item.Name, item.Evidence))
				continue
			}
			if !Reasonable(why) {
				problems = append(problems, fmt.Sprintf(
					"%s: the refusal for %s does not say why.\n  got: %q\n"+
						"  A refusal with no reason is a suppression with a name. The reason "+
						"has to say what closing it takes, so a reader can tell work that "+
						"remains from an exemption nobody will ever revisit",
					t.Name, item.Name, why))
			}
		}
	}

	// Every refusal is currently warranted.
	for _, table := range l.Tables() {
		if !known[table] {
			problems = append(problems, fmt.Sprintf(
				"the ledger names a table this report does not have: %q. Either the table "+
					"was renamed or deleted, and every refusal under it is now carrying no "+
					"weight, or the ledger was written for a table that was never built",
				table))
			continue
		}
		t, _ := r.Table(table)
		present := map[string]bool{}
		for _, item := range t.Items {
			present[item.Name] = true
			if !item.Covered {
				continue
			}
			// Only an item that IS in the ledger can be stale. Checking coverage alone
			// would report every covered item in every table, which is how this gate
			// reported fifteen false stale entries on its first run.
			if _, refused := l.Why(table, item.Name); !refused {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s is in the refusal ledger and IS now covered (%s).\n"+
					"  Delete the entry. Leaving it is an exemption for a defect that has "+
					"been fixed, and it reads as work that remains while hiding the work "+
					"that does not", table, item.Name, item.Evidence))
		}
		for _, item := range l.Items(table) {
			if !present[item] {
				problems = append(problems, fmt.Sprintf(
					"%s: the refusal ledger names %q, which the table does not derive any "+
						"more.\n  It was renamed, removed, or it never existed. Delete the "+
						"entry: a refusal for a thing that is not there is a refusal that can "+
						"never be closed", table, item))
			}
		}
	}

	sort.Strings(problems)
	return problems
}

// Summary renders one line per table, for the gate's output.
//
// The refusals are printed in full on every run, not only when one is exercised. A gate
// whose exemptions are visible only on the day they are used is a gate nobody audits in
// advance, and that is where dead entries accumulate.
func (r Report) Summary(l Ledger) string {
	var b strings.Builder
	for _, t := range r.Tables {
		covered := 0
		for _, item := range t.Items {
			if item.Covered {
				covered++
			}
		}
		fmt.Fprintf(&b, "%-22s %d/%d covered, %d refused\n",
			t.Name, covered, len(t.Items), len(l.Items(t.Name)))
		for _, item := range l.Items(t.Name) {
			fmt.Fprintf(&b, "    REFUSED  %s\n      %s\n", item, l[t.Name][item])
		}
	}
	return b.String()
}
