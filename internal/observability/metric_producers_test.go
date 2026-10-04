package observability_test

// A declared metric with no producer.
//
// `metrics.go` says its names "are the plan's contract, spelled the same way here
// as in docs, because a dashboard somebody hand-writes against a name this package
// invents is a dashboard that quietly stops updating." That is a promise in the
// direction of dashboards. It says nothing about whether anything ever writes the
// value — and ten of the twenty-seven did not, anywhere in the tree.
//
// Ten out of twenty-seven is a third of the published metric surface promising a
// number that no code path produces. A dashboard built against one of them shows a
// flat line, which reads as "this has never happened" rather than as "nothing is
// measuring this". The third one is worse: `evidence_disk` breaches on a gauge
// nothing writes, and wiring up the SLOs surfaced that as a permanently red
// objective on every install.
//
// Nothing checks this. `metrics.go` has a table of names, `pipeline.go` increments
// about a dozen of them, and the difference is invisible in a diff. The
// replacement-character ledger in `internal/testutil/encoding_test.go` is the same
// shape of fix for the same reason.
//
// So: every declared name must have a producer, or a line here saying why it does
// not and what would have to change. The list cannot grow silently, and a line can
// only be removed by fixing the metric.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the module root, from this package's directory.
const repoRoot = "../.."

// metricsPackage is the one package that declares names without producing them;
// counting its own uses would make every name look produced.
const metricsPackage = "internal/observability"

// unproducedMetrics is every declared metric that nothing in the tree writes, and
// why.
//
// It is EMPTY, and the map is deleted rather than left as `{}`.
//
// `TestTheLedgerIsNotEmpty` is the floor on the ledger's own honesty: while it is
// non-empty, a metric added without a producer fails with a name and no reason. So
// leaving an empty map would keep the gate running and make the one thing that can
// happen to it -- passing because there is nothing to check -- a state a future
// contributor could reach by deleting every entry instead of every metric. The floor
// is removed in the same commit as the last entry, which is what the floor says to do.
//
// # What the eight entries were, and why each is gone
//
// Wired, with the number already computed somewhere nobody looked:
//
//	CounterAuditAppends          audit.Writer.Append returns the row's hash on the only
//	                             path a row reaches the ledger
//	CounterOutboxDelivered       outbox.Relay.DrainOnce returns the delivered count
//	                             as its result
//	CounterRuleBundleActivated   bundles.Service.Activate, one event on an
//	                             append-only path
//	GaugePostgresConns           pgxpool.Stat() has counted this in memory since
//	                             pgxpool existed
//	GaugeEvidenceDiskFree        **the one that changed a gate.** evidence_disk
//	                             breaches below 10% free, and `DiskFreeMeasured` is
//	                             false while nothing writes the gauge -- so the
//	                             objective printed "not measured" on every install
//	                             and never once had an opinion. Wiring it needed a
//	                             platform split, because the standard library has no
//	                             disk-space call that works on both Linux and Windows
//	                             (`disk_unix.go`, `disk_windows.go`)
//	GaugeClockDriftMS            `drift_ms` has been a column on `device_clocks`
//	                             since the table was created and the SLO's drift
//	                             input lived only in the database
//
// Two entries whose REASON was wrong, which is the more interesting half:
//
//	CounterEvidenceFetched       excused with "evidence retrieval is M2's blocker.
//	CounterEvidenceFailed        There is no fetch worker to increment it; inventing
//	                             one now would be inventing the thing that calls it."
//	                             Retrieval has no *worker*, and
//	                             `GET /v1/evidence/{id}/content` is a perfectly good
//	                             fetch event: it opens the object, verifies its
//	                             SHA-256, and writes an `ActionEvidenceFetched` row
//	                             to the hash-chained ledger, which is a more
//	                             authoritative record of a fetch than a counter is.
//	                             The excuse read like a decision, which is exactly
//	                             why it survived
//
// One entry that was a duplicate asking to be guessed at:
//
//	CounterDeadLettersVisible    "sits beside CounterInboxDeadLettered with no stated
//	                             difference, and nothing increments it. Either it
//	                             means a distinct thing an operator would want -- a
//	                             dead letter that has been *seen* -- or it is a
//	                             duplicate that should be deleted."
//	                             It is deleted. `CounterInboxDeadLettered` counts the
//	                             fact; a "seen" counter would have to be incremented
//	                             by whoever renders the dead-letter list, which means
//	                             it counts operator attention rather than a system
//	                             state -- and an operator refreshes a page. Every
//	                             value such a series could take would be a function of
//	                             how often somebody looked. The operator-attention
//	                             number this repository cares about is
//	                             `GaugeIncidentOpen`, and it is defined as states an
//	                             operator watches, not events they accumulate
var unproducedMetrics = map[string]string{}

// TestEveryDeclaredMetricHasAProducerOrAnEntryInTheLedger.
//
// Red-checked: add a metric to metrics.go, do not write it, and this fails with the
// name and no reason attached.
func TestEveryDeclaredMetricHasAProducerOrAnEntryInTheLedger(t *testing.T) {
	declared := declaredMetricNames(t)
	if len(declared) < 20 {
		t.Fatalf("only %d metric names were read from metrics.go; the parse found less than the "+
			"file declares and this test is checking almost nothing", len(declared))
	}

	producers := metricIdentifiersUsedInTheTree(t)

	for _, name := range declared {
		used := producers[strings.ToLower(name)]
		_, excused := unproducedMetrics[name]
		switch {
		case used && excused:
			t.Errorf("%s now has a producer but is still in the ledger as unproduced. Delete the "+
				"entry — a ledger that cannot shrink is not a ledger", name)
		case used:
			// The ordinary case.
		case excused:
			// The ordinary excuse.
		default:
			t.Errorf("%s is declared in metrics.go and nothing in the tree writes it.\n"+
				"Either add the producer, or add a line to unproducedMetrics in %s saying why "+
				"there is none — a dashboard built against this name shows a flat line that "+
				"reads as \"this has never happened\".", name, "internal/observability/metric_producers_test.go")
		}
	}

	declaredSet := make(map[string]bool, len(declared))
	for _, name := range declared {
		declaredSet[name] = true
	}
	for name := range unproducedMetrics {
		if !declaredSet[name] {
			t.Errorf("the ledger names %s, which metrics.go no longer declares. Delete the entry "+
				"rather than leaving it to look like work", name)
		}
	}
}

// TestTheLedgerIsNotEmpty is the floor on the ledger's own honesty, and it is
// DELETED with the last entry rather than kept as an empty check.
//
// While the ledger was non-empty, a metric added without a producer failed this gate
// with a name and no reason attached -- which is the whole value of the ledger.
// Keeping a test that asserts "the map I emptied is empty" would leave the gate running
// with nothing for it to do, and the one thing that can then happen to it -- passing
// because there is nothing to check -- would be a state a future contributor could
// reach by deleting every entry instead of every metric.
//
// So the honest shape of "every declared metric has a producer" is that the ledger is
// gone and the assertion above is the gate on its own. Deleting the entry
// `CounterDeadLettersVisible` is what made the map empty, and this test is what said
// to delete this test in the same commit.

// declaredMetricNames reads the exported name constants out of metrics.go by parsing
// it, rather than by matching lines: a regexp that stops matching when somebody
// reformats a const block is a gate that stops working without failing.
func declaredMetricNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(repoRoot, metricsPackage, "metrics.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse metrics.go: %v", err)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, ident := range vs.Names {
				// Only the metric names, which is the family this gate is about: the
				// package also declares a histogram and a reservoir.
				if strings.HasPrefix(ident.Name, "Gauge") ||
					strings.HasPrefix(ident.Name, "Counter") {
					out = append(out, ident.Name)
				}
			}
		}
	}
	return out
}

// metricIdentifiersUsedInTheTree is every metric identifier mentioned outside the
// package that declares them, keyed lower-case for comparison.
//
// Parsed rather than grepped, for the same reason, and because a Go file can spell a
// name in a build-tagged file this test must still see: an identifier behind a
// `//go:build` tag is a producer, and a text search that ignored tags would either
// miss it or miss something else.
func metricIdentifiersUsedInTheTree(t *testing.T) map[string]bool {
	t.Helper()
	used := map[string]bool{}
	metricsDir := filepath.Join(repoRoot, metricsPackage)

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// `poc` is the other module; it does not import this package, so a match
			// there would be a coincidental name, not a producer. `.kilo` holds other
			// checkouts of this repository.
			switch info.Name() {
			case ".git", ".kilo", "poc", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			// Test files are excluded: a metric that only a test writes has no
			// producer, and `metrics_test.go` names every single one of them.
			return nil
		}
		if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(metricsDir)) {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err // a file that does not parse fails the build anyway
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			used[strings.ToLower(sel.Sel.Name)] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	if len(used) < 5 {
		t.Errorf("only %d identifiers were seen outside the metrics package; the walk is not "+
			"reaching the tree", len(used))
	}
	return used
}
