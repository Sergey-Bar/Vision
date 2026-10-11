package observability_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
)

// The metric names are the plan's contract. A dashboard somebody hand-wrote
// against a name this package invents is a dashboard that quietly stops updating.
func TestEveryPlannedMetricExists(t *testing.T) {
	counters := []string{
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
		observability.CounterOutboxDelivered,
		observability.CounterRuleBundleActivated,
		observability.CounterCheckpointsSealed,
		observability.CounterAuditAppends,
	}
	gauges := []string{
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
	planned := append(append([]string{}, counters...), gauges...)
	for _, name := range planned {
		if name == "" {
			t.Error("a planned metric name is empty")
		}
	}

	// A metric name is a claim about what is being measured, published to a
	// scraper. "actions_dispatched_total" named a class of thing that does not
	// exist in M1 - the counter is the relay acknowledging an outbox message it
	// then logged and discarded - and a dashboard built on it would report a site
	// taking physical action when it was counting its own log writes. The name
	// has to survive being read by somebody who is not looking at the code.
	for _, name := range planned {
		if name == "actions_dispatched_total" {
			t.Error("actions_dispatched_total is back. It is the name this counter " +
				"used to have and it describes physical dispatch, which M1 does not do")
		}
	}
	if observability.CounterOutboxDelivered != "outbox_messages_delivered_total" {
		t.Errorf("the outbox counter is %q; the name must say what is counted, which is "+
			"outbox messages delivered, not actions dispatched",
			observability.CounterOutboxDelivered)
	}

	// Counters must be seeded, which is the closed-set check: an unknown name is
	// silently dropped, and a dropped metric is a dashboard that quietly stops
	// updating.
	snapshot := observability.NewCounters().Snapshot()
	for _, name := range counters {
		if _, ok := snapshot[name]; !ok {
			t.Errorf("counter %q is not in the registry", name)
		}
	}

	// A gauge must NOT be in the counter set. It used to be, because the closed
	// set was one map and NewCounters seeded every name into it - so Render
	// emitted each gauge twice, once with `# TYPE x gauge` and once with
	// `# TYPE x counter`, and a metric name carrying two type declarations is a
	// scrape the parser rejects. The collector reports that as a broken core.
	for _, name := range gauges {
		if _, ok := snapshot[name]; ok {
			t.Errorf("gauge %q is present in the counter set; it would be emitted with two "+
				"conflicting type declarations and the whole scrape would fail to parse", name)
		}
		if !observability.IsGauge(name) {
			t.Errorf("%q is planned as a gauge but IsGauge says otherwise", name)
		}
		if _, ok := observability.NewCounters().Snapshot()[name]; ok {
			t.Errorf("gauge %q survived into the counter snapshot", name)
		}
	}

	// And the render itself must declare each name exactly once.
	r := observability.NewRegistry()
	for _, name := range gauges {
		r.Gauges.Set(name, 1)
	}
	body := r.Render()
	declarations := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "# TYPE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		declarations[fields[2]]++
	}
	for name, n := range declarations {
		if n > 1 {
			t.Errorf("the exposition declares %q %d times:\n%s", name, n, body)
		}
	}
	for _, name := range planned {
		if declarations[name] == 0 {
			t.Errorf("metric %q is absent from the exposition:\n%s", name, body)
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

// The objectives are exported from the registry rather than from a gauge anybody
// sets, so the series is the evaluator's own verdict and cannot be written by a
// second hand.
//
// Three properties. Every objective is exported whether or not it is breached, so
// a dashboard has a baseline before it can say what a 1 means. The breached one
// carries its reading and its remedy, because a scrape is where an operator looks
// and nobody greps a log at 02:00 for an objective name. And an unset verdict
// exports nothing rather than a flat zero — "0 breached" from a process that has
// measured nothing is the one lie this surface could tell.
func TestRenderExportsTheObjectivesWithTheirReadingAndRemedy(t *testing.T) {
	r := observability.NewRegistry()
	r.Gauges.Set(observability.GaugeBacklogDepth, 90000) // above the hard limit
	r.RecordSLOs(observability.Breaches(r, observability.ReadingsFrom(r)))

	out := r.Render()

	want := `slo_objective{objective="ingest_backlog",measured="inbox 90000, outbox 0, jobs 0",` +
		`remedy="above the hard limit the platform is refusing to accept rather than dropping. ` +
		`Check the source feed first, then the normalizer.",target="soft 5000, hard 50000"} 1`
	if !strings.Contains(out, want) {
		t.Errorf("the breached objective is not exported with its reading and remedy:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE slo_objective gauge") {
		t.Errorf("the objectivies are exported with no type declaration:\n%s", out)
	}

	// One that is being met is exported at 0, so the series has a baseline.
	met := []string{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "slo_objective{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		value := fields[len(fields)-1]
		switch value {
		case "0":
			met = append(met, fields[0])
		case "1":
		default:
			t.Errorf("an objective series is valued %q, which is neither 1 nor 0:\n%s", value, line)
		}
	}
	if len(met) == 0 {
		t.Error("no objective is being met, so nothing establishes the baseline a 1 is read against")
	}

	// Deterministic: two evaluations of the same readings are byte-identical, and
	// a scrape is a golden artefact only if it is.
	again := observability.NewRegistry()
	again.Gauges.Set(observability.GaugeBacklogDepth, 90000)
	again.RecordSLOs(observability.Breaches(again, observability.ReadingsFrom(again)))
	if again.Render() != out {
		t.Errorf("two identical evaluations rendered differently:\n%s\n---\n%s", out, again.Render())
	}

	// And the type is declared exactly once. A duplicate declaration is a scrape
	// the parser rejects, which is how the whole page stopped parsing once before.
	if n := strings.Count(out, "# TYPE slo_objective gauge"); n != 1 {
		t.Errorf("the exposition declares slo_objective %d times:\n%s", n, out)
	}
}

// An install that has not run its pipeline yet exports nothing for the objectives.
// "0 breached" from a process that has measured nothing would read as a clean
// bill of health, and it is the one response a fresh install gets most often.
func TestRenderExportsNoObjectiveBeforeTheFirstEvaluation(t *testing.T) {
	r := observability.NewRegistry()
	if out := r.Render(); strings.Contains(out, "slo_objective") {
		t.Errorf("an unevaluated registry exported an objective series:\n%s", out)
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
	if highest := r.QuantileSeconds(1); highest != 100 {
		t.Errorf("the maximum of the retained window is %.1f, want 100", highest)
	}
}

// Every SLO carries a remedy. An alert with no remedy is an alert everybody
// learns to ignore, and this is the difference between an SLO and a decoration.
func TestEverySLOBriefCarriesARemedy(t *testing.T) {
	r := observability.NewRegistry()
	// A deliberately bad state: everything breached.
	all := observability.Breaches(r, observability.Readings{
		LostEvents:           3,
		DeadLettered:         2,
		DiskFreePercent:      3.2,
		IncidentVisibilities: []time.Duration{30 * time.Second, 40 * time.Second},
	})
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
	all := observability.Breaches(r, observability.Readings{
		AIDegradedDecisions: 250,
		// Measured, because otherwise the objective correctly declines to answer.
		// Nothing in this build writes the degraded-incident gauge yet — see
		// TestAnEmptyRegistryIsNotAHealthyRegistryAndNotAFailedOne — so a test that
		// wants the objective to speak has to say it was measured.
		AIDegradedMeasured:   true,
		DiskFreePercent:      90,
		DiskFreeMeasured:     true,
		IncidentVisibilities: []time.Duration{time.Second, 2 * time.Second},
	})

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

	all := observability.Breaches(r, observability.Readings{
		DiskFreePercent:      55,
		IncidentVisibilities: []time.Duration{2 * time.Second, 3 * time.Second},
	})
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
	all := observability.Breaches(r, observability.Readings{LostEvents: 1, DiskFreePercent: 50})
	breached := observability.Breached(all)
	for _, s := range breached {
		if s.Name == "ingest_availability" {
			return
		}
	}
	t.Error("one lost event is not a breach; the SLO is zero, not a percentage")
}

// TestReadingsFromCountsWhatWasAcceptedButNotTerminal, not what was lost in the
// abstract.
//
// `pipeline.Loops` increments `inbox_accepted_total` for every event the inbox
// accepts and `inbox_dead_lettered_total` for the ones it terminates as dead
// letters. A dead letter is an accepted event the platform could not process —
// invariant 1's requirement is that it reaches *a* terminal state, and this is
// that state. So the loss is the difference between the two, not either counter
// alone, and getting that arithmetic wrong in either direction is either a
// permanently breached objective or a permanently silent one.
func TestReadingsFromCountsWhatWasAcceptedButNotTerminal(t *testing.T) {
	r := observability.NewRegistry()
	r.Counters.Inc(observability.CounterInboxAccepted)
	r.Counters.Inc(observability.CounterInboxAccepted)
	r.Counters.Inc(observability.CounterInboxAccepted)
	r.Counters.Inc(observability.CounterInboxDeadLettered)
	r.Gauges.Set(observability.GaugeIncidentDegraded, 4)
	r.Gauges.Set(observability.GaugeEvidenceDiskFree, 61)

	got := observability.ReadingsFrom(r)
	if got.LostEvents != 2 {
		t.Errorf("LostEvents is %.0f; 3 accepted with 1 dead-lettered is 2 that reached no "+
			"terminal state", got.LostEvents)
	}
	if got.DeadLettered != 1 {
		t.Errorf("DeadLettered is %.0f, want 1", got.DeadLettered)
	}
	if got.AIDegradedDecisions != 4 {
		t.Errorf("AIDegradedDecisions is %.0f, want the degraded-incident gauge", got.AIDegradedDecisions)
	}
	if !got.AIDegradedMeasured || !got.DiskFreeMeasured {
		t.Errorf("both gauges were written and ReadingsFrom says they were not measured: %+v", got)
	}
	if got.DiskFreePercent != 61 {
		t.Errorf("DiskFreePercent is %.0f, want the evidence-disk gauge", got.DiskFreePercent)
	}

	// A healthy site: everything accepted reached a terminal state.
	healthy := observability.NewRegistry()
	healthy.Counters.Inc(observability.CounterInboxAccepted)
	healthy.Counters.Inc(observability.CounterInboxDeadLettered)
	if lost := observability.ReadingsFrom(healthy).LostEvents; lost != 0 {
		t.Errorf("a site whose only accepted events are dead letters reports %.0f lost", lost)
	}
}

// A registry nobody has written must read as unmeasured rather than as healthy,
// and neither must it read as breached.
//
// Both directions have been real. `evidence_disk` breaches below 10% free, and no
// code in this build samples the evidence volume, so an unwritten gauge reads as
// zero — a permanently, universally full disk. `ai_plane_availability` reports
// how often the AI plane was absent, and its gauge is equally unwritten, so zero
// reads as "never degraded". An objective that asserts either of those about a
// system that has reported nothing is not an SLO, it is a story.
func TestAnEmptyRegistryIsNotAHealthyRegistryAndNotAFailedOne(t *testing.T) {
	r := observability.NewRegistry()
	if state := r.SLOs(); state.Evaluated != 0 || len(state.Breached) != 0 {
		t.Errorf("a registry nobody evaluated reports %+v", state)
	}
	if got := observability.ReadingsFrom(nil); got.LostEvents != 0 {
		t.Errorf("ReadingsFrom(nil) is %+v; a nil registry has no readings", got)
	}

	readings := observability.ReadingsFrom(r)
	if readings.DiskFreeMeasured || readings.AIDegradedMeasured {
		t.Errorf("an unwritten registry reports a measurement: %+v", readings)
	}
	for _, slo := range observability.Breaches(r, readings) {
		switch slo.Name {
		case "evidence_disk":
			if slo.Breached {
				t.Errorf("evidence_disk is breached on an unwritten gauge: %q", slo.Measured)
			}
			if !strings.Contains(slo.Measured, "not measured") {
				t.Errorf("evidence_disk reports %q; a gauge nobody wrote has no reading to report",
					slo.Measured)
			}
		case "ai_plane_availability":
			if slo.Breached {
				t.Errorf("ai_plane_availability is breached on an unwritten gauge: %q", slo.Measured)
			}
		}
	}

	// And once something *is* measured, the objective judges it. Otherwise the
	// guard above would pass for an objective that can never breach at all.
	r.Gauges.Set(observability.GaugeEvidenceDiskFree, 3.5)
	measured := observability.ReadingsFrom(r)
	for _, slo := range observability.Breaches(r, measured) {
		if slo.Name == "evidence_disk" && !slo.Breached {
			t.Errorf("a volume with 3.5 percent free is not a breach: %q", slo.Measured)
		}
	}
}

// The recorded verdict is the contract between the loop that evaluates the
// objectives and the endpoint an operator reads them on, so it is copied out
// rather than shared by reference: a caller that sorted the slice in place would
// otherwise be editing the record.
func TestTheRecordedVerdictIsACopy(t *testing.T) {
	r := observability.NewRegistry()
	all := observability.Breaches(r, observability.ReadingsFrom(r))
	if len(all) < 8 {
		t.Fatalf("only %d objectives evaluated", len(all))
	}
	// The first one is the zero-loss objective, which a bare registry satisfies.
	// Breached it is not, so this records a set whose names-only summary is empty
	// and whose detail is not — which is the pair that has to agree.
	r.RecordSLOs(all)

	read := r.SLOs()
	if read.Evaluated != len(all) {
		t.Fatalf("recorded %+v", read)
	}
	detail := r.SLOsDetail()
	if len(detail) != len(all) {
		t.Fatalf("recorded %d of %d objectives", len(detail), len(all))
	}
	detail[0].Name = "evidence_disk"
	if again := r.SLOsDetail(); again[0].Name == "evidence_disk" {
		t.Error("mutating what a reader was handed changed the recorded detail")
	}
	if names := r.SLOs(); names.Evaluated != len(all) {
		t.Error("the names-only summary moved when the detail was read")
	}
}

// Every objective the evaluator produced must reach the detail, and the names-only
// summary must be exactly the subset that is breached. One writer feeds both, and
// this is the test that says they cannot disagree.
func TestTheNamesSummaryIsTheBreachedSubsetOfTheDetail(t *testing.T) {
	r := observability.NewRegistry()
	r.Gauges.Set(observability.GaugeBacklogDepth, 90000) // above the hard limit
	r.Gauges.Set(observability.GaugeEvidenceDiskFree, 3) // below 10%

	all := observability.Breaches(r, observability.ReadingsFrom(r))
	if len(all) == 0 {
		t.Fatal("no objectives were evaluated")
	}
	r.RecordSLOs(all)

	want := map[string]bool{}
	for _, s := range all {
		if s.Breached {
			want[s.Name] = true
		}
	}
	got := map[string]bool{}
	for _, n := range r.SLOs().Breached {
		got[n] = true
	}
	if len(want) == 0 {
		t.Fatal("a registry above the backlog hard limit and under 10% free breached nothing, " +
			"so this test is not testing anything")
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Errorf("names %v, breached in the detail %v", got, want)
	}
	// Sorted, so a scrape's output is byte-stable between two identical evaluations.
	names := r.SLOs().Breached
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("names are unsorted: %v", names)
		}
	}
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
