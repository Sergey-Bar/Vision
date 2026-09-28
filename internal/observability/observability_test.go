package observability_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
)

// The metric names are the plan's contract. A dashboard somebody hand-wrote
// against a name this package invents is a dashboard that quietly stops updating.
func TestEveryPlannedMetricExists(t *testing.T) {
	planned := []string{
		observability.CounterInboxAccepted,
		observability.CounterInboxDeadLettered,
		observability.CounterCanonicalEvents,
		observability.CounterCorroborated,
		observability.CounterContradicted,
		observability.CounterUnverified,
		observability.CounterVLMInvocations,
		observability.CounterVLMErrors,
		observability.CounterEvidenceFetched,
		observability.CounterEvidenceFailed,
		observability.CounterActionsDispatched,
		observability.CounterDeadLettersVisible,
		observability.CounterRuleBundleActivated,
		observability.CounterCheckpointsSealed,
		observability.CounterAuditAppends,
		observability.GaugeBacklogDepth,
		observability.GaugeIngestLagSeconds,
		observability.GaugeOutboxPending,
		observability.GaugeJobQueueDepth,
		observability.GaugeClockDriftMS,
		observability.GaugeDeadLetterCount,
		observability.GaugeEvidenceDiskFree,
		observability.GaugeIncidentOpen,
		observability.GaugeIncidentDegraded,
		observability.GaugeAIUnavailable,
		observability.GaugeClockSevereDevices,
		observability.GaugePostgresConns,
	}
	for _, name := range planned {
		if name == "" {
			t.Error("a planned metric name is empty")
		}
	}
	// All of them must be seedable, which is the closed-set check.
	c := observability.NewCounters()
	snapshot := c.Snapshot()
	for _, name := range planned {
		if _, ok := snapshot[name]; !ok {
			t.Errorf("metric %q is not in the registry", name)
		}
	}
}

// A scrape before the first event still reports every counter, so a dashboard
// has a baseline rather than a gap that only fills in when something goes wrong.
func TestCountersAreSeededAtZero(t *testing.T) {
	c := observability.NewCounters()
	snapshot := c.Snapshot()
	if len(snapshot) == 0 {
		t.Fatal("the registry is empty")
	}
	for name, value := range snapshot {
		if value != 0 {
			t.Errorf("counter %q starts at %d, want 0", name, value)
		}
	}
}

func TestUnknownMetricIsIgnored(t *testing.T) {
	c := observability.NewCounters()
	c.Inc("a_metric_nobody_declared")
	if _, ok := c.Snapshot()["a_metric_nobody_declared"]; ok {
		t.Error("an undeclared metric was created; a typo in a call site would become " +
			"a metric no dashboard reads and nothing would fail")
	}
}

// An untrusted label value is a way to allocate without bound.
func TestLabelCardinalityIsCapped(t *testing.T) {
	c := observability.NewCounters()
	for i := 0; i < 500; i++ {
		c.IncLabeled(observability.CounterVLMErrors, "error-"+itoa(i))
	}
	labels := c.LabeledSnapshot()[observability.CounterVLMErrors]
	if len(labels) > 100 {
		t.Errorf("%d label values for one counter; past the cap values fold into a bucket", len(labels))
	}
	if labels["other"] == 0 {
		t.Error("the overflow bucket is empty, so the overflow was not recorded anywhere")
	}
}

func TestRenderProducesValidExposition(t *testing.T) {
	r := observability.NewRegistry()
	r.Counters.Inc(observability.CounterInboxAccepted)
	r.Counters.Inc(observability.CounterInboxAccepted)
	r.Counters.IncLabeled(observability.CounterVLMErrors, "timeout")
	r.Gauges.Set(observability.GaugeBacklogDepth, 12)
	r.Gauges.Set(observability.GaugeEvidenceDiskFree, 88.5)
	r.IngestLag.Observe(1.5)
	r.IngestLag.Observe(2.5)

	out := r.Render()
	for _, want := range []string{
		"# TYPE inbox_accepted_total counter",
		"inbox_accepted_total 2",
		"# TYPE backlog_depth gauge",
		"backlog_depth 12",
		"evidence_disk_free_percent 88.5",
		`vlm_errors_total{label="timeout"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, out)
		}
	}
}

// A collector that stopped running must be distinguishable from a real value.
func TestGaugesKnowWhenTheyAreStale(t *testing.T) {
	g := observability.NewGauges()
	if !g.Stale(observability.GaugeBacklogDepth, time.Minute) {
		t.Error("a gauge that was never set is not reported stale")
	}
	g.Set(observability.GaugeBacklogDepth, 5)
	if g.Stale(observability.GaugeBacklogDepth, time.Minute) {
		t.Error("a gauge set just now is reported stale")
	}
	if g.Stale(observability.GaugeBacklogDepth, 0) == false {
		t.Error("a zero maxAge should report every gauge as stale")
	}
}

func TestReservoirQuantiles(t *testing.T) {
	r := observability.NewReservoir(8)
	if got := r.QuantileSeconds(0.95); got != -1 {
		t.Errorf("an empty reservoir returned %v, want -1", got)
	}
	for i := 1; i <= 100; i++ {
		r.Observe(float64(i))
	}
	// Bounded, so a process that runs for a year keeps one. The retained
	// window is the most recent samples, which is what a "recent p95" means — a
	// reservoir that averaged over everything would report a year-old outage as
	// the current state.
	if r.Len() != 8 {
		t.Errorf("the reservoir holds %d samples, cap is 8", r.Len())
	}
	median := r.QuantileSeconds(0.5)
	if median < 96 || median > 97 {
		t.Errorf("the median of the retained window is %.1f, want about 96.5 (values 93 to 100)",
			median)
	}
	if max := r.QuantileSeconds(1); max != 100 {
		t.Errorf("the maximum of the retained window is %.1f, want 100", max)
	}
}

// Every SLO carries a remedy. An alert with no remedy is an alert everybody
// learns to ignore, and this is the difference between an SLO and a decoration.
func TestEverySLOBriefCarriesARemedy(t *testing.T) {
	r := observability.NewRegistry()
	// A deliberately bad state: everything breached.
	all := observability.Breaches(r, 3, 2, 1, 10, 5, 7, 3.2,
		[]time.Duration{30 * time.Second, 40 * time.Second})
	if len(all) < 8 {
		t.Fatalf("%d SLOs evaluated, want at least 8", len(all))
	}
	for _, slo := range all {
		if slo.Remedy == "" {
			t.Errorf("SLO %q is breached with no remedy", slo.Name)
		}
		if slo.Target == "" {
			t.Errorf("SLO %q states no target", slo.Name)
		}
		if slo.Measured == "" {
			t.Errorf("SLO %q is breached with no measurement", slo.Name)
		}
	}
}

// The AI plane is never an SLO. It is reported so a pilot can measure it, and it
// never makes the core unhealthy.
func TestAIPlaneIsReportedButNotRequired(t *testing.T) {
	r := observability.NewRegistry()
	// Everything else healthy, the AI plane fully down.
	all := observability.Breaches(r, 0, 0, 0, 100, 0, 250, 90,
		[]time.Duration{time.Second, 2 * time.Second})

	breached := observability.Breached(all)
	for _, s := range breached {
		if s.Name == "ai_plane_availability" {
			return // the point of the test
		}
	}
	t.Fatal("an AI-plane outage is not reported; a pilot cannot measure what the AI contributed")
}

func TestHealthyStateBreachesNothing(t *testing.T) {
	r := observability.NewRegistry()
	r.Gauges.Set(observability.GaugeBacklogDepth, 10)
	r.Gauges.Set(observability.GaugeOutboxPending, 2)
	r.Gauges.Set(observability.GaugeJobQueueDepth, 1)
	r.Gauges.Set(observability.GaugeDeadLetterCount, 0)
	r.Gauges.Set(observability.GaugeClockSevereDevices, 0)
	r.IngestLag.Observe(0.8)
	r.IngestLag.Observe(1.1)

	all := observability.Breaches(r, 0, 0, 0, 100, 1, 0, 55,
		[]time.Duration{2 * time.Second, 3 * time.Second})
	breached := observability.Breached(all)
	if len(breached) != 0 {
		for _, s := range breached {
			t.Errorf("a healthy system breached %q: %s", s.Name, s.Measured)
		}
	}
}

// Any accepted event lost is a breach, at any percentage.
func TestAnyLossIsABreach(t *testing.T) {
	r := observability.NewRegistry()
	all := observability.Breaches(r, 1, 0, 0, 0, 0, 0, 50, nil)
	breached := observability.Breached(all)
	for _, s := range breached {
		if s.Name == "ingest_availability" {
			return
		}
	}
	t.Error("one lost event is not a breach; the SLO is zero, not a percentage")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
