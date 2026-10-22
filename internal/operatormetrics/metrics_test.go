package operatormetrics_test

// The metric vocabulary, and the three copies of it.
//
// The vocabulary is closed and it lives in Go. But a closed set that exists in
// one place and is copied into two others is a closed set with three doors, and
// the copies drift. So there are three:
//
//   - the Go set, in `internal/operatormetrics/metrics.go`;
//   - the JSON Schema's enum, in `contracts/json-schema/metrics-report.schema.json`,
//     which is what a report is validated against;
//   - the database's CHECK constraint, in `migrations/0029_metric_rollups.expand.sql`,
//     which is what refuses a row naming a metric that does not exist.
//
// The two tests below are the reason they do not drift: each one reads a copy
// off disk and compares it with the Go set, so adding a metric to one and not
// the others is a failing test rather than a report that validates against a
// vocabulary the product does not have, or a rollup row the database refuses.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// TestEveryMetricHasASpecification is the floor on the closed set.
//
// The compile-time assertion in metrics.go catches a metric added without a
// specification, but it cannot catch a specification MISSING IN THE MIDDLE:
// the array's length would still be right and the gap would be a zero value.
//
// A zero specification has no name, and a metric with no name is not a metric.
// So the gap is caught here.
func TestEveryMetricHasASpecification(t *testing.T) {
	metrics := operatormetrics.AllMetrics()
	if len(metrics) < 10 {
		t.Fatalf("the closed set holds %d metrics, so this test is checking almost nothing", len(metrics))
	}
	for _, m := range metrics {
		spec := m.Spec()
		if spec.Name == "" {
			t.Errorf("metric %d has no specification, so it has no name. The compile-time "+
				"assertion catches a metric added to the set without a specification, but it "+
				"cannot catch a specification missing in the middle", m)
			continue
		}
		if spec.Metric != m {
			t.Errorf("the specification for %s names a different metric (%d). The table is "+
				"keyed by the constant, so this is a copy-paste", spec.Name, spec.Metric)
		}
		if spec.Description == "" {
			t.Errorf("%s has no description. A metric nobody can put into a sentence is a "+
				"number nobody can act on", spec.Name)
		}
		if spec.Kind == "" {
			t.Errorf("%s has no kind. A count and a rate read the same in a table and mean "+
				"different things", spec.Name)
		}
		if spec.IsRate && spec.Denominator == m {
			t.Errorf("%s is a rate whose denominator is itself", spec.Name)
		}
	}
}

// TestTheSchemaEnumIsTheGoSet keeps the report contract and the code in step.
//
// A report validated against a schema whose enum names a metric the product
// does not have is a report that refuses to describe the product, and a schema
// that omits a metric is a report that cannot carry it.
func TestTheSchemaEnumIsTheGoSet(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "json-schema", "metrics-report.schema.json"))
	if err != nil {
		t.Fatalf("read the report schema: %v", err)
	}
	var doc struct {
		Defs struct {
			MetricName struct {
				Enum []string `json:"enum"`
			} `json:"metricName"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the report schema: %v", err)
	}
	got := append([]string(nil), doc.Defs.MetricName.Enum...)
	if len(got) == 0 {
		t.Fatal("the report schema has no metric enum, so the gate is checking nothing")
	}
	want := operatormetrics.MetricNames()
	if diff := difference(got, want); diff != "" {
		t.Errorf("the report schema's metric enum and the Go set disagree:\n%s", diff)
	}
}

// TestTheMigrationCheckListIsTheGoSet keeps the database and the code in step.
//
// The CHECK constraint is the last thing between a typo in a rollup query and
// a row in the table, and a constraint that names a metric the code does not
// have is a constraint that refuses a legitimate metric.
func TestTheMigrationCheckListIsTheGoSet(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "migrations", "0029_metric_rollups.expand.sql"))
	if err != nil {
		t.Fatalf("read the rollup migration: %v", err)
	}
	got := checkList(string(raw))
	if len(got) == 0 {
		t.Fatal("the migration's metric CHECK list could not be read, so the gate is checking nothing")
	}
	want := operatormetrics.MetricNames()
	if diff := difference(got, want); diff != "" {
		t.Errorf("the migration's CHECK list and the Go set disagree:\n%s", diff)
	}
}

// TestTheNorthStarTravelsWithItsFourCompanions is C4.6's rule, stated as a
// test on the vocabulary.
//
// A North Star without its companions is a number with nothing to check it
// against, and the roadmap is explicit that the report must not show one. The
// companions are four, they are distinct, and none of them is the North Star
// itself.
func TestTheNorthStarTravelsWithItsFourCompanions(t *testing.T) {
	companions := operatormetrics.CompanionMetrics()
	if len(companions) != 4 {
		t.Fatalf("the North Star has %d companions, want 4", len(companions))
	}
	seen := map[operatormetrics.Metric]bool{}
	for _, c := range companions {
		if c == operatormetrics.NorthStarMetric() {
			t.Error("the North Star is listed as its own companion")
		}
		if seen[c] {
			t.Errorf("%s is listed twice among the companions", c)
		}
		seen[c] = true
		if !c.IsRate() {
			t.Errorf("%s is a companion and is not a rate. Every companion is a rate, and a "+
				"rate carries its N and its interval", c)
		}
	}
	if !operatormetrics.NorthStarMetric().IsRate() {
		t.Error("the North Star is not a rate, so it would be reported without an N or an interval")
	}
}

// TestNoQuantileIsRolledUp is C1.4's rule, stated as a test.
//
// "A median of a histogram is not a median." MTTI and MTTV are computed on
// demand from the incidents and the dispositions, and the vocabulary must not
// contain a rolled-up quantile: a member named for one would be a number the
// product publishes and nobody can defend.
func TestNoQuantileIsRolledUp(t *testing.T) {
	for _, m := range operatormetrics.AllMetrics() {
		name := strings.ToLower(m.String())
		for _, forbidden := range []string{"mtti", "mttv", "median", "p95", "percentile"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("%s is a rolled-up %s. A quantile is computed on demand from the "+
					"source tables, because a median of a histogram is not a median", m, forbidden)
			}
		}
	}
}

// TestEveryMetricNameIsSnakeCase is the wire-shape discipline applied to the
// NAMES rather than to the keys.
//
// The package's own wire-shape test walks the decoded document and fails on
// the first non-snake_case key at any depth. A metric name is a value rather
// than a key, and the same rule applies to it: a name with a capital in it is
// a name the report and the database and the dashboard will spell three ways.
func TestEveryMetricNameIsSnakeCase(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	for _, m := range operatormetrics.AllMetrics() {
		if !re.MatchString(m.String()) {
			t.Errorf("the metric name %q is not snake_case", m)
		}
	}
}

// TestTheVocabularyRoundTripsThroughItsNames is the vocabulary's own
// round-trip.
//
// A name resolves to the metric it names, and a name that is not in the
// vocabulary resolves to nothing. The second half matters more than the first:
// a lookup that answered for a name it does not hold would let a typo into the
// rollup reach the table, and the table's CHECK constraint is the last thing
// between that and a row nobody can read.
func TestTheVocabularyRoundTripsThroughItsNames(t *testing.T) {
	for _, m := range operatormetrics.AllMetrics() {
		got, ok := operatormetrics.MetricByName(m.String())
		if !ok {
			t.Errorf("%s does not resolve to a metric", m)
			continue
		}
		if got != m {
			t.Errorf("%s resolves to %s", m, got)
		}
	}
	if _, ok := operatormetrics.MetricByName("not_a_metric"); ok {
		t.Error("a name that is not in the vocabulary resolved to a metric")
	}
}

// difference is the two sets' difference, in both directions.
func difference(got, want []string) string {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	var b strings.Builder
	for _, n := range g {
		if !contains(w, n) {
			b.WriteString("  only in the file: " + n + "\n")
		}
	}
	for _, n := range w {
		if !contains(g, n) {
			b.WriteString("  only in the Go set: " + n + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// checkList pulls the metric names out of the migration's CHECK constraint.
//
// It reads the SQL rather than importing the migration, because the migration
// is a FILE and the point of this test is that the file and the code agree. A
// test that ran the migration and read the constraint out of the catalogue
// would be checking the file against itself.
func checkList(sql string) []string {
	start := strings.Index(sql, "CHECK (metric IN (")
	if start < 0 {
		return nil
	}
	rest := sql[start+len("CHECK (metric IN ("):]
	end := strings.Index(rest, "))")
	if end < 0 {
		return nil
	}
	re := regexp.MustCompile(`'([a-z0-9_]+)'`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(rest[:end], -1) {
		out = append(out, m[1])
	}
	return out
}
