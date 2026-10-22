package operatormetrics

// Every metric in the vocabulary has a producer, and a reader.
//
// # Why this gate exists
//
// The live registry has the same problem and solved it: `internal/observability`
// declared twenty-seven metric names and ten of them were written by nothing.
// A dashboard built against one of those shows a flat line, and a flat line
// reads as "this has never happened" rather than as "nothing is measuring
// this".
//
// The durable vocabulary has the same failure mode, and it is worse here,
// because a report that carries a metric nobody produces is a report that
// carries a ZERO for it. A zero in a signed report is a statement.
//
// So: every metric is produced by a query in `rollup.go`, or it is computed on
// demand by the report, or it is in the ledger below with the reason.
//
// # The ledger is not empty
//
// Three of the four companions need a corpus and a replay, which are the
// evaluation and replay-audit work, and until they land those companions are
// reported as NOT MEASURED.
//
// A companion with no value and a stated reason is honest. A companion with a
// value somebody made up is not, and the difference is the whole point of
// writing the ledger rather than leaving the gap to be discovered.

import (
	"os"
	"strings"
	"testing"
)

// unproducedMetrics is every metric nothing produces yet, and why.
//
// It is a map of metric name to the reason it has no producer, and every entry
// has to say what would close it.
//
// It is NOT empty: two of the four companions need the evidence store's own
// retention state and a replay corpus, and until they exist those companions
// are reported as NOT MEASURED. A companion with a value somebody made up is
// not that.
var unproducedMetrics = map[string]string{
	"evidence_completeness": "the share of closed incidents whose evidence is retrievable AND " +
		"passes its hash. It needs the evidence store's own retention state -- a clip that was " +
		"swept is not a clip that was never fetched, and the two read the same in the table -- " +
		"so the number is not yet computable and the companion is reported as not measured",
	"missed_incident_rate": "the share of expected incidents that were not raised. It needs the " +
		"replay corpus and the expected set it is compared against, and a corpus that does not " +
		"cover the required behaviours reports its gaps rather than a rate, so the companion " +
		"is reported as not measured",
}

// TestEveryMetricHasAProducerOrAnEntryInTheLedger is the gate.
//
// Red-checked: add a metric to the closed set, do not write it, and this fails
// with the name and no reason attached.
func TestEveryMetricHasAProducerOrAnEntryInTheLedger(t *testing.T) {
	metrics := AllMetrics()
	if len(metrics) < 10 {
		t.Fatalf("the closed set holds %d metrics, so this test is checking almost nothing", len(metrics))
	}

	produced := producedMetricNames()
	computed := computedOnDemandMetricNames()

	for _, m := range metrics {
		name := m.String()
		_, excused := unproducedMetrics[name]
		switch {
		case produced[name] && excused:
			t.Errorf("%s now has a producer but is still in the ledger as unproduced. Delete "+
				"the entry -- a ledger that cannot shrink is not a ledger", name)
		case produced[name] || computed[name]:
			// The ordinary case.
		case excused:
			// The ordinary excuse.
		default:
			t.Errorf("%s is in the closed metric set and nothing produces it.\n"+
				"Either add the query that computes it, mark it on-demand and compute it in the "+
				"report, or add a line to unproducedMetrics in %s saying why there is none. A "+
				"report that carries a metric nobody produces carries a ZERO for it, and a zero "+
				"in a signed report is a statement.",
				name, "internal/operatormetrics/producer_test.go")
		}
	}

	declared := map[string]bool{}
	for _, m := range metrics {
		declared[m.String()] = true
	}
	for name := range unproducedMetrics {
		if !declared[name] {
			t.Errorf("the ledger names %s, which is no longer in the closed set. Delete the "+
				"entry rather than leaving it to look like work", name)
		}
	}
}

// TestTheLedgerIsNotEmpty is the floor on the ledger's own honesty.
//
// While the ledger is non-empty, a metric added without a producer fails this
// gate with a name and no reason attached -- which is the whole value of the
// ledger. Keeping a test that asserts "the map I emptied is empty" would leave
// the gate running with nothing for it to do.
//
// So the floor is removed in the same commit as the last entry, which is what
// the floor says to do.
func TestTheLedgerIsNotEmpty(t *testing.T) {
	if len(unproducedMetrics) == 0 {
		t.Skip("the ledger is empty: delete this test in the same commit as the last entry, " +
			"the same way the observability package's floor was deleted")
	}
	if len(unproducedMetrics) < 2 {
		t.Errorf("the ledger holds %d entries. Two of the four companions have no producer yet, "+
			"and a ledger with fewer entries than that is a ledger that has lost one",
			len(unproducedMetrics))
	}
	for name, reason := range unproducedMetrics {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("the ledger names %s without saying why, which is not a ledger entry", name)
		}
	}
}

// TestEveryRolledUpMetricIsProducedByAQuery is the narrower, sharper half.
//
// A metric the specification calls rolled-up MUST be produced by one of the
// queries. It is not enough that something produces it: the rollup table is
// where a rolled-up metric lives, and a metric that is marked rolled-up and
// produced somewhere else is a metric whose reader and whose writer disagree
// about where the number is.
func TestEveryRolledUpMetricIsProducedByAQuery(t *testing.T) {
	produced := producedMetricNames()
	for _, m := range AllMetrics() {
		if !m.RolledUp() {
			continue
		}
		if !produced[m.String()] {
			t.Errorf("%s is marked rolled up and no query produces it. A rolled-up metric lives "+
				"in metric_rollups, and a reader that looks there for a number nobody wrote "+
				"finds nothing", m)
		}
	}
}

// TestNoMetricIsBothRolledUpAndComputedOnDemand is the same idea from the
// other side.
//
// A metric cannot be in two places. The North Star is computed on demand from
// the rollup; a metric the rollup also produces is a metric with two numbers
// and one name, and a reader cannot tell which one it is looking at.
func TestNoMetricIsBothRolledUpAndComputedOnDemand(t *testing.T) {
	produced := producedMetricNames()
	for _, m := range AllMetrics() {
		if !m.Spec().OnDemand {
			continue
		}
		if produced[m.String()] {
			t.Errorf("%s is marked on-demand AND a rollup query produces it. A metric with two "+
				"numbers and one name is a number nobody can check", m)
		}
	}
}

// TestEveryOnDemandMetricIsComputedInTheReport is the other half.
//
// A metric marked on-demand is one the report reader COMPUTES, and the
// computation has to be in the reader's source. A metric that is marked
// on-demand and computed nowhere is a metric the report claims to have and does
// not.
func TestEveryOnDemandMetricIsComputedInTheReport(t *testing.T) {
	computed := computedOnDemandMetricNames()
	for _, m := range AllMetrics() {
		if !m.Spec().OnDemand {
			continue
		}
		if !computed[m.String()] {
			t.Errorf("%s is marked on-demand and the report does not compute it. A metric the "+
				"report claims to have and does not is a metric a reader will believe", m)
		}
	}
}

// TestTheMetricsFileAndTheLedgerAgreeOnTheVocabulary is the last copy.
//
// The schema's enum and the migration's CHECK list are checked against the Go
// set by the external test. This checks the LEDGER against the Go set, which is
// the third list and the one nobody looks at.
func TestTheMetricsFileAndTheLedgerAgreeOnTheVocabulary(t *testing.T) {
	declared := map[string]bool{}
	for _, m := range AllMetrics() {
		declared[m.String()] = true
	}
	for name := range unproducedMetrics {
		if !declared[name] {
			t.Errorf("the ledger names %s, which is not in the closed metric set", name)
		}
	}
}

// producedMetricNames is every metric name a query in the rollup writer
// produces.
//
// It is read out of the SQL rather than out of a registry, because a registry
// call would make this a statement that the package agrees with itself: it
// would find exactly the metrics the writer chose to expose, and the closed set
// is the thing that must not grow without one.
//
// The metric name in a query is a quoted literal, so the search is for the
// literal. A name built by concatenation would not be found, and the queries
// are written to spell their names out for exactly that reason: a name the
// vocabulary does not hold has to be visible in the source, not inferred.
func producedMetricNames() map[string]bool {
	out := map[string]bool{}
	for _, c := range computations {
		for _, name := range MetricNames() {
			if strings.Contains(c.sql, "'"+name+"'") {
				out[name] = true
			}
		}
	}
	return out
}

// computedOnDemandMetricNames is every metric the report reader computes from
// the rollup rather than reading a rolled-up row.
//
// The list is read out of the reader's own source, for the same reason the
// producers are: a registry would make the gate a statement that the package
// agrees with itself.
//
// The search is for the Go CONSTANT's name rather than the wire name, because
// the reader names the metric by its constant: `metricNorthStar`, not
// `north_star_attention_minutes_per_1000_events`. The constant is in the
// specification table for exactly this.
//
// And it is a SEARCH rather than a registry, which is the point: a metric that
// is mentioned in the reader's source is one the reader knows about, and the
// specification's `OnDemand` flag is what says the reader is supposed to
// compute it.
func computedOnDemandMetricNames() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile("report.go")
	if err != nil {
		return out
	}
	source := string(raw)
	for _, m := range AllMetrics() {
		if strings.Contains(source, m.Spec().Const) {
			out[m.String()] = true
		}
	}
	return out
}
