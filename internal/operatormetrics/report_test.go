package operatormetrics_test

// The report, and the four rules that make it a report rather than a printout.
//
//   - the North Star is never reported without all four companions;
//   - its denominator is canonical events and never incidents;
//   - there is no target, because M1 is baseline-only and a target set before
//     the variance is known is a number somebody chose;
//   - the built report validates against the PUBLISHED schema, so the contract
//     is a contract and not a description.
//
// The last one is the load-bearing. A schema that nothing validates against is
// a document, and a document drifts from the code that is supposed to match it.
// Validating the report that the reader actually builds is what makes the
// schema a gate.

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Sergey-Bar/vision/internal/contracts/schemasync"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// reportWindow is a bucket-aligned window over the test's `now`.
//
// It is TWO hours either side rather than one, and that is not decoration: the
// window's end is TRUNCATED to the hour, so a window that ended an hour from
// now would end up BEFORE a disposition recorded half an hour from now, and
// the attention row would vanish for an hour of the day. A test whose result
// depends on the hour it runs in is a test that fails for a reason nobody
// read.
func reportWindow(now time.Time) operatormetrics.Window {
	from := now.Add(-2 * time.Hour).Truncate(time.Hour)
	to := now.Add(2 * time.Hour).Truncate(time.Hour)
	return operatormetrics.Window{From: from, To: to}
}

// buildReport writes a rollup and reads a report back.
func buildReport(t *testing.T) operatormetrics.Report {
	t.Helper()
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	h.disposition(t, id, h.now.Add(30*time.Minute))
	h.canonical(t, "evt-canary-1", h.now)

	w := reportWindow(h.now)
	rollup := &operatormetrics.Rollup{Pool: h.pool, SiteID: h.site, Now: func() time.Time { return h.now }}
	if _, err := rollup.Write(ctx(), h.tenant, operatormetrics.Bucket{Start: w.From, End: w.To}); err != nil {
		t.Fatalf("write the rollup: %v", err)
	}
	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}, Now: func() time.Time { return h.now }}
	rep, err := reader.Read(ctx(), h.tenant, "", "", w)
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	return rep
}

// TestTheReportValidatesAgainstThePublishedSchema is the contract, exercised.
//
// The report the reader builds is validated against the schema in
// `contracts/json-schema/metrics-report.schema.json`. A shape the schema does
// not allow is a shape a consumer cannot rely on, and the schema is the
// published contract.
func TestTheReportValidatesAgainstThePublishedSchema(t *testing.T) {
	rep := buildReport(t)
	if err := rep.Validate(); err != nil {
		t.Fatalf("the report does not validate against its own rules: %v", err)
	}

	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal the report: %v", err)
	}
	schemaBytes, err := schemasync.Bytes("metrics-report.schema.json")
	if err != nil {
		t.Fatalf("read the embedded schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		t.Fatalf("parse the schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("metrics-report.schema.json", doc); err != nil {
		t.Fatalf("add the schema: %v", err)
	}
	schema, err := compiler.Compile("metrics-report.schema.json")
	if err != nil {
		t.Fatalf("compile the schema: %v", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse the report: %v", err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Errorf("the report does not validate against the published schema: %v\n\n%s",
			err, raw)
	}
}

// TestTheNorthStarTravelsWithItsFourCompanions is C4.6's rule.
//
// A North Star with no companion is a number that improves when the product
// gets worse, and the report type carries the four as a struct so a report
// with three cannot be built. This asserts the built report has all four and
// that the report's own validation says so.
func TestTheBuiltReportCarriesItsFourCompanions(t *testing.T) {
	rep := buildReport(t)
	if rep.NorthStar == nil {
		t.Fatal("the report carries no North Star, so there is nothing to check the companions against")
	}
	ns := rep.NorthStar
	for name, c := range map[string]operatormetrics.Rate{
		"operator_override_rate":   ns.Companions.OperatorOverrideRate,
		"ai_false_escalation_rate": ns.Companions.AIFalseEscalationRate,
		"evidence_completeness":    ns.Companions.EvidenceCompleteness,
		"missed_incident_rate":     ns.Companions.MissedIncidentRate,
	} {
		if c.Metric != name {
			t.Errorf("the companion %q names itself %q", name, c.Metric)
		}
	}
	if err := rep.Validate(); err != nil {
		t.Errorf("the report does not validate: %v", err)
	}
}

// TestAReportWithAMissingCompanionIsRefused is the rule as a negative.
//
// The struct makes a missing companion hard to express, so this reaches for
// the field itself and empties it: a companion with no metric name is a
// companion that is not there, and the report has to refuse to go out.
func TestAReportWithAMissingCompanionIsRefused(t *testing.T) {
	rep := buildReport(t)
	if rep.NorthStar == nil {
		t.Fatal("the report carries no North Star")
	}
	rep.NorthStar.Companions.MissedIncidentRate = operatormetrics.Rate{}
	if err := rep.Validate(); err == nil {
		t.Error("a report with a missing companion validated. The North Star is never reportable " +
			"without all four")
	}
}

// TestTheNorthStarsDenominatorIsCanonicalEvents is C4.5's rule.
//
// Dividing attention minutes by incidents would improve the metric
// automatically as the rules got more conservative, which is a metric that
// improves when the product gets worse. The denominator is canonical events.
func TestTheNorthStarsDenominatorIsCanonicalEvents(t *testing.T) {
	rep := buildReport(t)
	if rep.NorthStar == nil {
		t.Fatal("the report carries no North Star")
	}
	if got := rep.NorthStar.Denominator.Metric; got != "canonical_events" {
		t.Errorf("the North Star's denominator is %q, want canonical_events. Incidents would "+
			"improve the ratio automatically as the rules got more conservative", got)
	}
	// And the report refuses one whose denominator is not.
	rep.NorthStar.Denominator.Metric = "incidents_raised"
	if err := rep.Validate(); err == nil {
		t.Error("a North Star whose denominator is incidents_raised validated")
	}
}

// TestTheReportCarriesNoTarget is C4.4's rule.
//
// M1 is baseline only. A target set before the baseline variance is known is a
// number somebody chose rather than a number anybody measured, and the M2
// target is set FROM the M1 baseline. So the report has no target field, and
// this asserts that at the wire level, where a target would show up.
func TestTheReportCarriesNoTarget(t *testing.T) {
	rep := buildReport(t)
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for key := range doc {
		if strings.Contains(strings.ToLower(key), "target") {
			t.Errorf("the report carries %q. M1 is baseline only: a target set before the "+
				"variance is known is a number somebody chose", key)
		}
	}
	// And the schema does not offer one either.
	schemaBytes, err := schemasync.Bytes("metrics-report.schema.json")
	if err != nil {
		t.Fatalf("read the embedded schema: %v", err)
	}
	if strings.Contains(strings.ToLower(string(schemaBytes)), "\"target") {
		t.Error("the published schema offers a target field, and M1 is baseline only")
	}
}

// TestTheReaderRefusesAnUnalignedWindow is the bucket rule.
//
// A window that cuts a bucket in half reports a fraction of a bucket as if it
// were a whole one, and the error is invisible: the number looks like any
// other number.
func TestTheReaderRefusesAnUnalignedWindow(t *testing.T) {
	h := setup(t)
	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}, Now: func() time.Time { return h.now }}
	w := operatormetrics.Window{From: h.now.Add(-90 * time.Minute), To: h.now.Add(30 * time.Minute)}
	if _, err := reader.Read(ctx(), h.tenant, "", "", w); err == nil {
		t.Error("the reader accepted a window that is not on bucket boundaries")
	}
}

// TestTheWilsonIntervalIsTheOneTheRatesUse is the arithmetic behind every
// interval in the report.
//
// The Wilson score interval is used rather than the normal approximation
// because a proportion near zero or one has a skewed sampling distribution, and
// the normal approximation is exactly wrong at the ends -- which is where a
// security product's rates live.
//
// A rate of zero out of ten has an upper bound well above zero, and a rate of
// ten out of ten has a lower bound below one. An interval that collapsed to
// the point estimate at either end would claim a precision the sample does not
// have.
func TestTheWilsonIntervalIsTheOneTheRatesUse(t *testing.T) {
	lower, upper := operatormetrics.WilsonInterval(0, 10, 1.959963984540054)
	if lower != 0 {
		t.Errorf("the lower bound of 0 out of 10 is %v, want 0", lower)
	}
	if upper <= 0 || upper >= 1 {
		t.Errorf("the upper bound of 0 out of 10 is %v, and it has to be above zero: a rate of "+
			"zero out of ten is not a rate of zero", upper)
	}

	lower, upper = operatormetrics.WilsonInterval(10, 10, 1.959963984540054)
	if math.Abs(upper-1) > 1e-9 {
		t.Errorf("the upper bound of 10 out of 10 is %v, want 1", upper)
	}
	if lower >= 1 {
		t.Errorf("the lower bound of 10 out of 10 is %v, and it has to be below one: ten out of "+
			"ten is not a proof", lower)
	}

	// No observations is no interval, and it is reported as (0, 0) rather than
	// as a point at zero.
	lower, upper = operatormetrics.WilsonInterval(0, 0, 1.959963984540054)
	if lower != 0 || upper != 0 {
		t.Errorf("the interval of no observations is (%v, %v), want (0, 0)", lower, upper)
	}
}

// TestThePublishedSchemaIsTheOneOnDisk keeps the embedded copy honest.
//
// The schemasync gate checks the bytes; this checks that the copy the report
// is validated against is the copy that was published, which is the same
// statement from the other side.
func TestThePublishedSchemaIsTheOneOnDisk(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = filepath.Dir(root)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "contracts", "json-schema", "metrics-report.schema.json"))
	if err != nil {
		t.Fatalf("read the published schema: %v", err)
	}
	embedded, err := schemasync.Bytes("metrics-report.schema.json")
	if err != nil {
		t.Fatalf("read the embedded schema: %v", err)
	}
	if !bytes.Equal(onDisk, embedded) {
		t.Error("the embedded copy of the report schema is stale:\n" +
			"  cp contracts/json-schema/metrics-report.schema.json internal/contracts/schemasync/")
	}
}
