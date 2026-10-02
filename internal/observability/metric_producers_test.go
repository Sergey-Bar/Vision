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
// why. The reason is the part that matters: a bare name here would be a backlog
// item nobody can act on, and the whole point of writing it down is that the next
// person can pick one up in a minute.
//
// Two families, and the difference matters:
//
//   - **A subsystem that does not exist yet.** Evidence retrieval is M2's blocker
//     and `internal/evidence` has no fetch worker, so its two counters are
//     correctly absent. Wiring them now would mean inventing the thing that
//     increments them.
//   - **A subsystem that exists and does the work**, where the number is free and
//     the only thing missing is that nobody asked. The audit ledger is append-only
//     and hash-chained; the outbox relay returns a delivery count; the bundle
//     service has an `Activate` that is a single event. Each of those is one line
//     and a `*Registry` on a constructor. They are listed separately because they
//     are the ones a human can close this week.
var unproducedMetrics = map[string]string{
	// Exists, and the number is free. See the note above.
	"CounterAuditAppends":        "audit.Writer appends every row of the hash-chained ledger and counts none of them. One line in the writer, plus a *Registry on its constructor.",
	"CounterOutboxDelivered":     "the outbox relay knows exactly how many messages it delivered — DrainOnce returns that count — and the metric is never incremented. One line where the count is already computed.",
	"CounterRuleBundleActivated": "bundles.Service.Activate is a single event on an append-only path. One line in the activation.",

	// The disk and the connection pool have a measurement available and nobody
	// takes it.
	"GaugePostgresConns":    "pgxpool exposes its own connection statistics; nothing samples them into this gauge.",
	"GaugeEvidenceDiskFree": "no evidence-volume sampler exists. This is the one that mattered: evidence_disk breaches below 10 percent free, so an unwritten gauge reads as a permanently full disk.",
	"GaugeClockDriftMS":     "clocksub measures per-device drift and records it in device_clocks; the gauge is never fed from it, so the SLO's drift input is only in the database.",

	// A subsystem that does not exist yet.
	"CounterEvidenceFetched": "evidence retrieval is M2's blocker. There is no fetch worker to increment it; inventing one now would be inventing the thing that calls it.",
	"CounterEvidenceFailed":  "as CounterEvidenceFetched.",

	// A name whose meaning is not settled.
	"CounterDeadLettersVisible": "sits beside CounterInboxDeadLettered with no stated difference, and nothing increments it. Either it means a distinct thing an operator would want — a dead letter that has been *seen* — or it is a duplicate that should be deleted. Guessing which would put a meaningless series on a dashboard.",
}

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

// TestTheLedgerIsNotEmpty is the floor on the ledger's own honesty, for the reason
// given in `TestTheLedgerIsNotEmpty` in the encoding test: deleting the map turns
// the test above into a gate over nothing, with no failure anywhere.
func TestTheLedgerIsNotEmpty(t *testing.T) {
	if len(unproducedMetrics) == 0 {
		t.Fatal("the unproduced-metric ledger is empty. If every one has been wired, delete the " +
			"map and this test in the same commit, so nobody can empty it and keep the gate")
	}
	for name, reason := range unproducedMetrics {
		if reason == "" {
			t.Errorf("the ledger excuses %s without saying why, which is not an excuse", name)
		}
	}
}

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
