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
	CounterInboxAccepted       = "inbox_accepted_total"
	CounterInboxDeadLettered   = "inbox_dead_lettered_total"
	CounterCanonicalEvents     = "canonical_events_total"
	CounterCorroborated        = "corroborated_total"
	CounterContradicted        = "contradicted_total"
	CounterUnverified          = "unverified_total"
	CounterVLMInvocations      = "vlm_invocations_total"
	CounterVLMErrors           = "vlm_errors_total"
	CounterEvidenceFetched     = "evidence_fetched_total"
	CounterEvidenceFailed      = "evidence_fetch_failed_total"
	CounterActionsDispatched   = "actions_dispatched_total"
	CounterDeadLettersVisible  = "dead_letter_visible_total"
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
)

// knownMetrics is the closed set, so a typo in a call site fails a test rather
// than creating a metric no dashboard will ever read.
var knownMetrics = map[string]bool{
	CounterInboxAccepted: true, CounterInboxDeadLettered: true,
	CounterCanonicalEvents: true, CounterCorroborated: true,
	CounterContradicted: true, CounterUnverified: true,
	CounterVLMInvocations: true, CounterVLMErrors: true,
	CounterEvidenceFetched: true, CounterEvidenceFailed: true,
	CounterActionsDispatched: true, CounterDeadLettersVisible: true,
	CounterRuleBundleActivated: true, CounterCheckpointsSealed: true,
	CounterAuditAppends: true,
	GaugeBacklogDepth:   true, GaugeIngestLagSeconds: true, GaugeOutboxPending: true,
	GaugeJobQueueDepth: true, GaugeClockDriftMS: true, GaugeDeadLetterCount: true,
	GaugeEvidenceDiskFree: true, GaugeIncidentOpen: true, GaugeIncidentDegraded: true,
	GaugeAIUnavailable: true, GaugeClockSevereDevices: true, GaugePostgresConns: true,
}

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

// NewCounters builds a counter set seeded from the known names, so a scrape
// before the first event still reports every metric as zero rather than
// omitting it. A metric that appears only after something goes wrong is a metric
// nobody has a baseline for.
func NewCounters() *Counters {
	c := &Counters{
		values: make(map[string]int64, len(knownMetrics)),
		labels: make(map[string]map[string]int64),
	}
	for name := range knownMetrics {
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
func (g *Gauges) Get(name string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.values[name]
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
}

// NewRegistry builds a registry.
func NewRegistry() *Registry {
	return &Registry{
		Counters:  NewCounters(),
		Gauges:    NewGauges(),
		IngestLag: NewReservoir(1024),
	}
}

// Render writes the exposition format.
func (r *Registry) Render() string {
	var b strings.Builder
	writeSorted(&b, r.Gauges.Snapshot(), func(name string, value float64) {
		fmtGauge(&b, name, value)
	})
	writeSorted(&b, map[string]int64(r.Counters.Snapshot()), func(name string, value int64) {
		fmtCounter(&b, name, value)
	})
	for name, byLabel := range r.Counters.LabeledSnapshot() {
		for label, value := range byLabel {
			fmt.Fprintf(&b, "%s{label=%q} %d\n", name, label, value)
		}
	}
	if lag := r.IngestLag.QuantileSeconds(0.95); lag >= 0 {
		fmtGauge(&b, GaugeIngestLagSeconds, lag)
	}
	return b.String()
}

func fmtGauge(b *strings.Builder, name string, value float64) {
	fmt.Fprintf(b, "# TYPE %s gauge\n%s %g\n", name, name, value)
}

func fmtCounter(b *strings.Builder, name string, value int64) {
	fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writeSorted[T any](b *strings.Builder, m map[string]T, write func(string, T)) {
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
// A fixed window is what the SLO means by "recent", and a bounded reservoir is
// how a long-running process on a site that never restarts can keep one.
type Reservoir struct {
	mu     sync.Mutex
	values []float64
	next   int
	filled bool
}

// NewReservoir builds a reservoir of the given size.
func NewReservoir(size int) *Reservoir {
	if size <= 0 {
		size = 256
	}
	return &Reservoir{values: make([]float64, 0, size)}
}

// Observe adds a sample, overwriting the oldest once full.
func (r *Reservoir) Observe(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) < cap(r.values) {
		r.values = append(r.values, v)
		return
	}
	r.values[r.next] = v
	r.next = (r.next + 1) % len(r.values)
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
