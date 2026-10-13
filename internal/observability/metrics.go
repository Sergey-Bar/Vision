// Package observability is the metric registry and the SLO evaluation.
//
// The metric names are the plan's contract, spelled the same way here as in
// docs, because a dashboard somebody hand-writes against a name this package
// invents is a dashboard that quietly stops updating.
//
// Two rules about what a metric may be:
//
//   - Counters are monotonic. A gauge that goes down is a gauge, and calling it
//     a counter means the alert fires on a reset and nobody investigates.
//   - Nothing here is on the availability critical path. Observability that can
//     take the product down is a worse outage than the one it was watching, so
//     recording is best-effort and never blocks the pipeline.
package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Metric names, exactly as the plan states them.
const (
	CounterInboxAccepted     = "inbox_accepted_total"
	CounterInboxDeadLettered = "inbox_dead_lettered_total"
	CounterCanonicalEvents   = "canonical_events_total"
	CounterCorroborated      = "corroborated_total"
	CounterContradicted      = "contradicted_total"
	CounterUnverified        = "unverified_total"
	CounterVLMInvocations    = "vlm_invocations_total"
	CounterVLMErrors         = "vlm_errors_total"
	CounterEvidenceFetched   = "evidence_fetched_total"
	CounterEvidenceFailed    = "evidence_fetch_failed_total"
	// CounterDuplicate and CounterSuppressed are the two verification states that
	// mean "this event produced no incident on purpose".
	//
	// They were declared in the metrics doc since July and produced by nothing, so
	// "how often does the system deliberately stay quiet" had no live number at
	// all - and the two states are the ones an operator asks about when they
	// suspect the rules are eating incidents. A suppressed event is a rule working;
	// a duplicate is ingest working. Both need a number, and both need it to be
	// monotonic because both are counted over a window on a dashboard.
	CounterDuplicate  = "duplicate_total"
	CounterSuppressed = "suppressed_total"
	// OutboxMessagesDelivered, not actions_dispatched_total.
	//
	// The old name said "actions dispatched", and the thing being counted is the
	// relay acknowledging an outbox message it then logged and discarded. In M1
	// no action has physical impact, so the name described a class of thing that
	// does not exist - and a dashboard built on it would show a site taking
	// physical action when it was doing nothing but counting its own log writes.
	// The name has to say what is measured; the roadmap's metric table records
	// what a future real dispatch will be called, and it will be a new name.
	CounterOutboxDelivered     = "outbox_messages_delivered_total"
	CounterRuleBundleActivated = "rule_bundle_activated_total"
	CounterCheckpointsSealed   = "checkpoints_sealed_total"
	CounterAuditAppends        = "audit_appends_total"

	GaugeBacklogDepth       = "backlog_depth"
	GaugeIngestLagSeconds   = "ingest_lag_seconds"
	GaugeOutboxPending      = "outbox_pending"
	GaugeJobQueueDepth      = "job_queue_depth"
	GaugeClockDriftMS       = "clock_drift_ms"
	GaugeDeadLetterCount    = "dead_letter_count"
	GaugeEvidenceDiskFree   = "evidence_disk_free_percent"
	GaugeIncidentOpen       = "incident_open_count"
	GaugeIncidentDegraded   = "incident_degraded_count"
	GaugeAIUnavailable      = "ai_unavailable"
	GaugeClockSevereDevices = "clock_severe_device_count"
	GaugePostgresConns      = "postgres_conns_in_use"
	GaugeIncidentVisibility = "incident_visibility_seconds"
)

// The closed set, split by kind.
//
// Splitting it is not tidiness. The two sets were one map, and NewCounters
// seeded *every* name into the counter map - including the gauges. Render then
// emitted each gauge twice: once from Gauges with `# TYPE x gauge` and once from
// Counters with `# TYPE x counter`. Prometheus rejects a metric name carrying
// two conflicting type declarations, so the whole scrape failed to parse and a
// collector reported a broken core.
//
// A gauge can also legitimately hold a value the counter map does not, and the
// counter copy is always zero, so the number a dashboard reads would have been
// whichever declaration the parser happened to keep.
var counterMetrics = map[string]bool{
	CounterInboxAccepted: true, CounterInboxDeadLettered: true,
	CounterCanonicalEvents: true, CounterCorroborated: true,
	CounterContradicted: true, CounterUnverified: true,
	CounterVLMInvocations: true, CounterVLMErrors: true,
	CounterEvidenceFetched: true, CounterEvidenceFailed: true,
	CounterDuplicate: true, CounterSuppressed: true,
	CounterOutboxDelivered:     true,
	CounterRuleBundleActivated: true, CounterCheckpointsSealed: true,
	CounterAuditAppends: true,
}

var gaugeMetrics = map[string]bool{
	GaugeBacklogDepth: true, GaugeIngestLagSeconds: true, GaugeOutboxPending: true,
	GaugeJobQueueDepth: true, GaugeClockDriftMS: true, GaugeDeadLetterCount: true,
	GaugeEvidenceDiskFree: true, GaugeIncidentOpen: true, GaugeIncidentDegraded: true,
	GaugeAIUnavailable: true, GaugeClockSevereDevices: true, GaugePostgresConns: true,
	GaugeIncidentVisibility: true,
}

// knownMetrics is the union, so a call site can ask whether a name is legal at
// all without caring which kind it is.
var knownMetrics = func() map[string]bool {
	out := make(map[string]bool, len(counterMetrics)+len(gaugeMetrics))
	for k := range counterMetrics {
		out[k] = true
	}
	for k := range gaugeMetrics {
		out[k] = true
	}
	return out
}()

// IsGauge reports whether a name is a gauge. Exported so a caller that records
// against the registry from a background sampler does not have to keep its own
// copy of the split.
func IsGauge(name string) bool { return gaugeMetrics[name] }

// Counters is a monotonic counter set.
//
// The lock is a plain mutex rather than an atomic: the values are read in bulk
// for the scrape, and a bulk read of atomics would still need a lock to be
// consistent.
type Counters struct {
	mu     sync.RWMutex
	values map[string]int64
	// labels is a secondary dimension. Kept as a fixed set of string pairs per
	// counter rather than an open label set, because an open label set on an
	// untrusted input is a cardinality bomb.
	labels map[string]map[string]int64
}

// NewCounters builds a counter set seeded from the counter names, so a scrape
// before the first event still reports every counter as zero rather than
// omitting it. A metric that appears only after something goes wrong is a metric
// nobody has a baseline for.
//
// Only counters are seeded. A gauge seeded here would be emitted twice, once as
// each kind, and a metric name with two type declarations is a scrape the parser
// rejects.
func NewCounters() *Counters {
	c := &Counters{
		values: make(map[string]int64, len(counterMetrics)),
		labels: make(map[string]map[string]int64),
	}
	for name := range counterMetrics {
		c.values[name] = 0
	}
	return c
}

// Inc adds one to a counter. An unknown metric name panics in a test and is
// ignored in a build without one, because a silently-dropped metric is how a
// dashboard goes stale.
func (c *Counters) Inc(name string) { c.Add(name, 1) }

// Add adds n to a counter.
func (c *Counters) Add(name string, n int64) {
	if !knownMetrics[name] {
		return
	}
	c.mu.Lock()
	c.values[name] += n
	c.mu.Unlock()
}

// IncLabeled adds one to a counter for a label value, with a cardinality cap.
func (c *Counters) IncLabeled(name, label string) {
	if !knownMetrics[name] {
		return
	}
	const maxLabelValues = 64
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.labels[name] == nil {
		c.labels[name] = map[string]int64{}
	}
	if _, known := c.labels[name][label]; !known && len(c.labels[name]) >= maxLabelValues {
		// A label value arriving from an untrusted source is a way to allocate
		// without bound. Past the cap the value is folded into a single bucket,
		// which still tells an operator something is being generated.
		c.labels[name]["other"]++
		return
	}
	c.labels[name][label]++
}

// Get reads one counter.
func (c *Counters) Get(name string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.values[name]
}

// Snapshot reads every counter, sorted, for a scrape.
func (c *Counters) Snapshot() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int64, len(c.values))
	for k, v := range c.values {
		out[k] = v
	}
	return out
}

// LabeledSnapshot reads the labelled counters, sorted.
func (c *Counters) LabeledSnapshot() map[string]map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]map[string]int64, len(c.labels))
	for k, v := range c.labels {
		copied := make(map[string]int64, len(v))
		for lk, lv := range v {
			copied[lk] = lv
		}
		out[k] = copied
	}
	return out
}

// Gauges is a current-value set.
type Gauges struct {
	mu     sync.RWMutex
	values map[string]float64
	// measuredAt is when each gauge was last set, so a stale gauge is visible as
	// stale rather than as a real reading.
	measuredAt map[string]time.Time
}

// NewGauges builds a gauge set.
func NewGauges() *Gauges {
	return &Gauges{
		values:     map[string]float64{},
		measuredAt: map[string]time.Time{},
	}
}

// Set records a gauge value.
func (g *Gauges) Set(name string, value float64) {
	if !knownMetrics[name] {
		return
	}
	g.mu.Lock()
	g.values[name] = value
	g.measuredAt[name] = time.Now().UTC()
	g.mu.Unlock()
}

// Get reads a gauge.
//
// A gauge nobody has ever set reads as zero, and for a threshold that is often
// the safe direction — a backlog of zero is a healthy backlog. For an objective
// whose threshold is *below* zero's side, it is not: a free-disk gauge nobody
// writes reads as a full disk. `Measured` is the way to tell those apart, and any
// objective that breaches on a low reading has to ask it.
func (g *Gauges) Get(name string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.values[name]
}

// Measured reports whether a gauge has ever been written.
//
// It is a question the zero value cannot answer on its own, and the pipeline
// already states the rule it exists for: "a gauge set to zero because its query
// failed is worse than a stale one — it reads as 'the backlog is clear', which is
// the one thing an operator must never be told by a broken query." That rule was
// applied to keeping a stale value; this is the same rule applied to *whether a
// value exists*, and it is the difference between an objective that reports a
// breach and one that reports the truth.
func (g *Gauges) Measured(name string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.measuredAt[name]
	return ok
}

// Stale reports a gauge not refreshed within maxAge, which is how a collector
// that stopped running is told apart from a real value.
//
// A non-positive maxAge means "always stale", stated explicitly rather than
// falling out of a comparison against a clock whose granularity on some
// platforms is coarse enough to report two adjacent calls as simultaneous.
func (g *Gauges) Stale(name string, maxAge time.Duration) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	at, ok := g.measuredAt[name]
	if !ok || maxAge <= 0 {
		return true
	}
	return time.Since(at) > maxAge
}

// Snapshot reads every gauge, sorted.
func (g *Gauges) Snapshot() map[string]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]float64, len(g.values))
	for k, v := range g.values {
		out[k] = v
	}
	return out
}

// Registry holds both sets and renders them in the Prometheus text exposition
// format.
//
// Prometheus is not a dependency: the format is twelve lines of text, and a
// metrics client on an air-gapped site is a supply-chain decision somebody has
// to sign off on.
type Registry struct {
	Counters *Counters
	Gauges   *Gauges
	// IngestLag keeps a reservoir of recent lag samples so the SLO's p95 can be
	// evaluated without keeping every observation for ever.
	IngestLag *Reservoir

	// IncidentVisibility keeps a reservoir of incident visibility samples so the
	// SLO's p95 can be evaluated without keeping every observation for ever.
	IncidentVisibility *Reservoir

	// slo is the last evaluation's verdict, so the process that runs the
	// objectives and the surface an operator reads them on share one record
	// without either knowing about the other.
	//
	// Neither half is a gauge. An objective's name is not a number, and eight
	// gauge names for eight objectives would be eight series nobody soft of
	// matched against anything. What the scrape gets is one series carrying the
	// name as a label, which is the same statement eight gauges would make in a
	// form a dashboard can actually select on — see writeSLOs.
	//
	// The two halves are one writer and two projections, not two writers of one
	// fact: sloDetail is what the evaluator produced, and slo is the names-only
	// summary derived from it, so the unauthenticated endpoint cannot be handed
	// more than the names even by mistake.
	sloMu     sync.Mutex
	slo       SLOState
	sloDetail []SLO
}

// SLOState is which objectives were last evaluated as not being met.
//
// Names only, and that is a disclosure boundary rather than a detail: the endpoint
// that reads this is /v1/health, which is unauthenticated, and a measured disk
// percentage or a dead-letter count is not something to hand to whoever asked.
// The reading and the remedy live on the objectives themselves, reachable through
// SLOsDetail, and the only route that serves those is /v1/metrics, which requires
// audit.read.
type SLOState struct {
	Evaluated int
	Breached  []string
}

// RecordSLOs stores the verdict of one evaluation, which is the whole evaluated
// set rather than a summary of it.
//
// The argument is the set and not a SLOState because the two would drift: a
// summary recorded beside its own detail is two writers of one fact, and the one
// that goes stale is whichever the last caller forgot.
func (r *Registry) RecordSLOs(all []SLO) {
	r.sloMu.Lock()
	defer r.sloMu.Unlock()
	r.slo.Evaluated = len(all)
	r.slo.Breached = breachedNames(all)
	r.sloDetail = copySLOs(all)
}

// SLOs reports the last evaluation's names-only verdict, as a copy so a caller
// cannot mutate what the next reader sees.
func (r *Registry) SLOs() SLOState {
	r.sloMu.Lock()
	defer r.sloMu.Unlock()
	return SLOState{
		Evaluated: r.slo.Evaluated,
		Breached:  append([]string(nil), r.slo.Breached...),
	}
}

// SLOsDetail reports the last evaluation in full: every objective with its
// target, its reading and its remedy, sorted by name.
//
// This is what a scrape reads. /v1/metrics is capability-gated, so unlike
// /v1/health it can carry the number without it becoming public, and a scrape is
// exactly where an operator wants it — nobody greps a log at 02:00 for an
// objective name. An empty result means no evaluation has run yet, which is a
// different statement from "nothing is breached" and is deliberately not rendered
// as a flat zero.
func (r *Registry) SLOsDetail() []SLO {
	r.sloMu.Lock()
	defer r.sloMu.Unlock()
	return copySLOs(r.sloDetail)
}

// breachedNames is the sorted subset that is not being met, so the names-only
// projection and the detail can never disagree about what is breached.
func breachedNames(all []SLO) []string {
	names := make([]string, 0, len(all))
	for _, s := range all {
		if s.Breached {
			names = append(names, s.Name)
		}
	}
	sort.Strings(names)
	return names
}

// copySLOs detaches a set from the caller's slice, so a reader cannot mutate what
// the next reader sees. Breached is the last field the evaluator fills in and the
// one a copy is most likely to drop.
func copySLOs(in []SLO) []SLO {
	if len(in) == 0 {
		return nil
	}
	out := make([]SLO, len(in))
	copy(out, in)
	return out
}

// The declared reservoir windows, per objective.
//
// Both are the plan's own numbers: SLO 2 is a 5-second ingest-lag target and SLO 3
// is a 15-second incident-visibility target. The reservoir window is the span a
// p95 covers, so it is derived from the objective rather than invented, and it is
// a named constant so a dashboard and the code cannot disagree about what
// "recent" meant.
//
// Fifteen minutes, not fifteen seconds. A p95 over fewer than a few hundred
// samples is a number with no confidence in it, and at a quiet site 15 seconds of
// incident visibility is one sample. The objective is what the span must be at
// least long enough to evaluate; the reported span says how long it really was.
const (
	reservoirIngestLagWindow  = 15 * time.Minute
	reservoirVisibilityWindow = 15 * time.Minute
)

// NewRegistry builds a registry.
func NewRegistry() *Registry {
	return &Registry{
		Counters:           NewCounters(),
		Gauges:             NewGauges(),
		IngestLag:          NewWindowedReservoir(1024, reservoirIngestLagWindow),
		IncidentVisibility: NewWindowedReservoir(1024, reservoirVisibilityWindow),
	}
}

// PruneReservoirs expires samples outside each reservoir's window and returns
// how many were dropped.
//
// Called from the SLO evaluation loop, which is the one place in the product that
// runs on a timer already. It is not called from Render: a scrape is a read, and
// a read that mutates what it reads turns two collectors scraping concurrently
// into two different windows.
func (r *Registry) PruneReservoirs(now time.Time) (ingestLagDropped, visibilityDropped int) {
	return r.IngestLag.Prune(now), r.IncidentVisibility.Prune(now)
}

// fmtGaugeSpan writes a gauge with the wall-clock span of the reservoir it came
// from, as labels.
//
// The labels are the point of the whole change. A p95 on its own is a number
// whose window nobody can see; with span_start and span_end beside it, the same
// number says what it covered, and a p95 measured over three samples is
// distinguishable from one measured over three thousand without asking anybody.
func fmtGaugeSpan(b *strings.Builder, name string, value float64, oldest, newest time.Time, samples int) {
	if samples == 0 || oldest.IsZero() || newest.IsZero() {
		fmtGauge(b, name, value)
		return
	}
	fmt.Fprintf(b, "# TYPE %s gauge\n%s %g span_start=%q span_end=%q span_samples=%d\n",
		name, name, value,
		oldest.UTC().Format(time.RFC3339), newest.UTC().Format(time.RFC3339), samples)
}

// Render writes the exposition format.
func (r *Registry) Render() string {
	var b strings.Builder
	writeSorted(r.Gauges.Snapshot(), func(name string, value float64) {
		fmtGauge(&b, name, value)
	})
	writeSorted(r.Counters.Snapshot(), func(name string, value int64) {
		fmtCounter(&b, name, value)
	})
	for name, byLabel := range r.Counters.LabeledSnapshot() {
		for label, value := range byLabel {
			fmt.Fprintf(&b, "%s{label=%q} %d\n", name, label, value)
		}
	}
	if lag := r.IngestLag.QuantileSeconds(0.95); lag >= 0 {
		l, n, s := r.IngestLag.Span()
		fmtGaugeSpan(&b, GaugeIngestLagSeconds, lag, l, n, s)
	}
	if vis := r.IncidentVisibility.QuantileSeconds(0.95); vis >= 0 {
		l, n, s := r.IncidentVisibility.Span()
		fmtGaugeSpan(&b, GaugeIncidentVisibility, vis, l, n, s)
	}
	r.writeSLOs(&b)
	return b.String()
}

// sloObjectiveSeries is the name the objectives are exported under.
//
// Unexported because it is an exposition detail and not a name a call site records
// against: the values come from the evaluator's verdict, not from a gauge anybody
// sets. Putting it in counterMetrics or gaugeMetrics would invite a second writer
// for a fact that already has one.
const sloObjectiveSeries = "slo_objective"

// writeSLOs exports the objectives, one series each, valued 1 while the objective
// is not being met.
//
// Every objective is exported whether it is breached or not, and that is the
// whole reason this is a series and not an alarm: a metric that appears only when
// something is wrong reads, on the morning after a quiet night, as "this has never
// happened". A series that has been 0 since the install is the baseline a
// dashboard needs before it can say "3" means anything.
//
// The labels carry the target, the reading and the remedy, which is the part an
// operator acts on. They are safe here and unsafe on /v1/health only because this
// exposition is served from the audit.read-gated route.
//
// An unset verdict exports nothing rather than a flat zero. A core that has never
// evaluated its objectives has measured nothing, and "0 breached" from it would be
// the one lie this could tell.
func (r *Registry) writeSLOs(b *strings.Builder) {
	all := r.SLOsDetail()
	if len(all) == 0 {
		return
	}
	byName := make(map[string]SLO, len(all))
	names := make([]string, 0, len(all))
	for _, s := range all {
		byName[s.Name] = s
		names = append(names, s.Name)
	}
	sort.Strings(names)

	fmt.Fprintf(b, "# TYPE %s gauge\n", sloObjectiveSeries)
	fmt.Fprintf(b, "# HELP %s 1 while the objective is not being met. The labels carry "+
		"its target, the reading that breached it and the remedy.\n", sloObjectiveSeries)
	for _, name := range names {
		s := byName[name]
		value := 0
		if s.Breached {
			value = 1
		}
		fmt.Fprintf(b, "%s{objective=%q,measured=%q,remedy=%q,target=%q} %d\n",
			sloObjectiveSeries, s.Name, s.Measured, s.Remedy, s.Target, value)
	}
}

func fmtGauge(b *strings.Builder, name string, value float64) {
	fmt.Fprintf(b, "# TYPE %s gauge\n%s %g\n", name, name, value)
}

func fmtCounter(b *strings.Builder, name string, value int64) {
	fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writeSorted[T any](m map[string]T, write func(string, T)) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		write(k, m[k])
	}
}

// Reservoir is a bounded sample of recent values, for a quantile without
// unbounded memory.
//
// It is bounded TWICE, and the second bound is the one that was missing.
//
// A size bound alone makes the window a count: 1024 samples of ingest lag is 3.4
// minutes at 5 events a second and 17 hours at 1 event a minute, so the same
// p95 target is evaluated against a window whose length depends on the load. A
// quiet site is then measured against yesterday's lag and looks fine; a busy one
// against three minutes and looks bad. Neither number is wrong on its own terms
// and both are wrong as "recent".
//
// The plan's finding was exactly this, and the fix has two halves, because
// "make the window 15 minutes" silently changes every existing p95 reading:
//
//   - the window is DECLARED, so a p95 means a stated span of time;
//   - the span is REPORTED next to every p95, so a reader who cannot see the
//     window can still see what the number covered.
//
// A window of zero keeps the size-bounded behaviour, which is what the unit
// tests that assert specific quantiles want and what a caller that has no clock
// preference should get.
type Reservoir struct {
	mu     sync.Mutex
	values []float64
	at     []time.Time
	next   int
	window time.Duration
}

// NewReservoir builds a reservoir of the given size, bounded by count only.
func NewReservoir(size int) *Reservoir {
	return newReservoir(size, 0)
}

// NewWindowedReservoir builds a reservoir bounded by count AND by wall-clock
// span: samples older than the window are dropped by Prune, and Span reports
// what remained.
//
// The window is per-reservoir rather than a registry-wide constant because the
// two reservoirs answer different questions: ingest lag is SLO 2's 5-second
// target and incident visibility is SLO 3's 15-second target, and a single
// window would make one of them a lie.
func NewWindowedReservoir(size int, window time.Duration) *Reservoir {
	return newReservoir(size, window)
}

func newReservoir(size int, window time.Duration) *Reservoir {
	if size <= 0 {
		size = 256
	}
	return &Reservoir{
		values: make([]float64, 0, size),
		at:     make([]time.Time, 0, size),
		window: window,
	}
}

// Observe adds a sample, overwriting the oldest once full. The timestamp is
// taken here rather than passed in, so a caller cannot backdate a sample into a
// p95 it wants to influence.
func (r *Reservoir) Observe(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) < cap(r.values) {
		r.values = append(r.values, v)
		r.at = append(r.at, time.Now().UTC())
		return
	}
	r.values[r.next] = v
	r.at[r.next] = time.Now().UTC()
	r.next = (r.next + 1) % len(r.values)
}

// Prune drops samples older than the window. It is a separate call rather than
// part of Observe because a quantile is read far more often than it is written,
// and doing the expiry work on the write path would put the window's arithmetic
// between an event and its measurement.
//
// It returns how many samples were dropped, which is zero for an unbounded
// reservoir and is a useful number on its own: a reservoir that drops every
// sample on every prune has a window nobody is writing to.
func (r *Reservoir) Prune(now time.Time) int {
	if r.window <= 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := now.Add(-r.window)
	kept := 0
	for i, at := range r.at {
		if i < len(r.values) && !at.Before(cutoff) {
			kept++
		}
	}
	if kept == len(r.values) {
		return 0
	}
	values := make([]float64, 0, kept)
	at := make([]time.Time, 0, kept)
	for i := range r.values {
		if !r.at[i].Before(cutoff) {
			values = append(values, r.values[i])
			at = append(at, r.at[i])
		}
	}
	dropped := len(r.values) - len(values)
	r.values = values
	r.at = at
	r.next = 0
	return dropped
}

// Span is the wall-clock window the samples in this reservoir actually cover:
// the oldest and newest sample held, and how many.
//
// It exists because the plan's finding was not only that the window was a count
// rather than a time, but that a reader of a p95 could not tell which it was.
// An empty reservoir reports a zero span and zero samples, which is the honest
// answer and not a span of the current instant: a p95 over nothing is not 0
// seconds, it is unknown, and QuantileSeconds already returns -1 for that.
func (r *Reservoir) Span() (oldest, newest time.Time, samples int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return time.Time{}, time.Time{}, 0
	}
	oldest, newest = r.at[0], r.at[0]
	for _, at := range r.at[:len(r.values)] {
		if at.Before(oldest) {
			oldest = at
		}
		if at.After(newest) {
			newest = at
		}
	}
	return oldest, newest, len(r.values)
}

// QuantileSeconds returns an approximate quantile in seconds, or -1 when nothing
// was observed. The reservoir holds seconds because that is the unit the
// exposition format and the SLO both read, and converting twice invites one of
// the two to disagree.
func (r *Reservoir) QuantileSeconds(q float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return -1
	}
	sorted := append([]float64(nil), r.values...)
	sort.Float64s(sorted)
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx]
}

// Len is the number of samples held.
func (r *Reservoir) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.values)
}

// Samples returns a copy of all samples in the reservoir.
func (r *Reservoir) Samples() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]float64(nil), r.values...)
}
