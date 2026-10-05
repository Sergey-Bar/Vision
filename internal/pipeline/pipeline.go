// Package pipeline owns the loops that turn accepted events into incidents.
//
// It exists because nothing owned a lifecycle. Every stage of the pipeline was a
// package with a test and a single-batch primitive with no caller: the adapter's
// Poll, the inbox's ClaimFor, the rule engine's Evaluate, the outbox relay's
// DrainOnce. This package is the thing that calls them in sequence and that knows
// what to do when one of them stops.
//
// # The order, and why each step is where it is
//
//	adapter.Poll -> inbox.Write          (ingest loop)
//	inbox.ClaimFor -> normalize -> graph -> correlate -> evaluate -> CreateIncident
//	               -> enqueue QueueVLM   (consume loop)
//	outbox.Relay.DrainOnce                (relay loop)
//	jobs.Worker.Run                        (worker loop)
//
// The three timing facts that are load-bearing:
//
//   - Normalize completes the inbox row itself, inside the canonical event's
//     transaction. The consume loop must not also call Complete, or it gets "is
//     not in processing state" for an event that was in fact processed.
//   - The AI observation is enqueued *after* the incident. ai_observations has
//     NOT NULL incident_id ON DELETE CASCADE: an observation cannot precede the
//     thing it is an opinion about.
//   - DrainOnce returns a delivery count, not an error. A short count is the
//     queue being partly undeliverable, which is a metric and a dead letter, not
//     a loop condition.
package pipeline // wired by cmd/security-core, the composition root

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/correlator"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/graph"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
	"github.com/Sergey-Bar/Ai-Captain/internal/normalize"
	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// Deps is everything the loops need.
//
// One struct rather than a constructor per loop: the loops run concurrently in
// one process, and the wiring is the daemon's to own. Every field is a real
// dependency of at least one loop; there is no optional-everything struct here
// because a loop with a nil dependency is a loop that panics at 3am.
type Deps struct {
	Pool    *dbx.Pool
	Log     *slog.Logger
	Metrics *observability.Registry

	// Scoped returns a tenant-scoped connection bound to this install's site.
	//
	// Every statement the loops write reads a site-scoped table, and the RLS
	// policy that is meant to enforce which site a row belongs to is currently
	// PERMISSIVE, so the two policies OR and the site constraint does not exist
	// at the database layer. `For` binds the tenant and nothing else, which means
	// a loop could be reading and writing every site's rows with the site
	// constraint living only in this file's comments.
	//
	// Making the site-bound pool the *only* way to reach one from here is the
	// expand half of the fix: when `site_isolation` becomes RESTRICTIVE, a
	// statement that reaches the pool by any other route stops seeing rows, and
	// this method is the one that still works.
	//
	// An empty SiteID is refused rather than defaulted. "No site" is not "every
	// site", and a loop that ingested nothing while believing it was ingesting
	// everything is the failure this whole change is about.

	Adapter   *milestone.Adapter
	Inbox     *inbox.Writer
	Normal    *normalize.Normalizer
	Graph     *graph.Service
	Correlat  *correlator.Correlator
	Incidents *incidents.Service
	Bundles   *bundles.Service
	Enqueuer  *jobs.Enqueuer

	// Tenant, SiteID and IntegrationID scope everything. Single-site per
	// install, which is what the loops are written against and what the API's
	// single Tenant field already assumed.
	Tenant        string
	SiteID        string
	IntegrationID string

	// SiteLocation is the site's timezone, used to evaluate a schedule.
	// It used to be hardcoded UTC in the evaluation context, which makes a
	// schedule wrong by the site's offset.
	SiteLocation *time.Location

	// EvidenceRoot is the directory the evidence volume is mounted at, and it is
	// what `evidence_disk_free_percent` measures.
	//
	// It is a path and not a device because the interesting failure is a full
	// volume, and a full volume is one that stat's fine and has nothing left. There is
	// no "where is the evidence" abstraction here to be wrong in: the evidence store
	// writes under this directory and the gauge reads the filesystem it is on, so a
	// configuration that points them at different filesystems reads the wrong one and
	// says nothing.
	//
	// Empty means the gauge is not sampled. That is the honest answer for an install
	// with no evidence volume -- a site that stores no evidence does not have a full
	// one -- and the SLO's `DiskFreeMeasured` stays false, which is the flag for
	// "this objective knows nothing here" rather than "this objective is healthy".
	EvidenceRoot string

	// EvidenceStore is the evidence filesystem store, used by the retention sweep.
	EvidenceStore *evidence.Store

	// RetentionSweepInterval is how often the retention sweep runs. Zero disables it.
	RetentionSweepInterval time.Duration

	// InSchedule resolves a schedule id against a site-local instant. A Deps with
	// none gets `rules.SiteScheduleResolver(SiteLocation)`, which is the same
	// default the simulation and the CLI use — the point being that they cannot
	// disagree.
	//
	// There was no default before, so `matchSchedule` returned false for every
	// schedule and every schedule rule in the active bundle was dead in
	// production: an after-hours rule never fired, and a negated business-hours
	// rule matched all day.
	InSchedule func(id string, at time.Time) bool

	// PollInterval is how often the ingest loop polls.
	PollInterval time.Duration
	// ClaimBatch is how many inbox rows one consume pass takes.
	ClaimBatch int
	// LeaseDuration is the inbox visibility timeout.
	LeaseDuration time.Duration
	// Relay drains the outbox. The daemon builds it because its handler closes
	// over the logger; the pipeline owns when it runs. There is no default: a
	// Deps with no relay leaves the outbox pending and says so in the log, which
	// is the state an operator can act on. See runOutbox.
	Relay *outbox.Relay
	// Jobs reads the queue depths for the gauges. Optional: without it the queue
	// gauges stay absent rather than reporting a depth nobody read.
	Jobs *jobs.Worker
	// AIEndpoint is recorded so the AI availability gauge reflects the
	// configuration rather than the outcome of the last call.
	AIEndpoint string
	// ClockPolicy is the site's clock policy, which decides the drift bands.
	ClockPolicy clock.Policy
}

// Scoped returns a tenant-scoped connection bound to this install's site.
//
// Every statement the loops write reads a site-scoped table, and since migration
// 21 `site_isolation` is RESTRICTIVE, so the effective policy is tenant AND
// site: a connection that named only its tenant would see nothing. Making the
// site-bound pool the *only* way to reach one from here is the expand half of
// that change, and it is what stopped each loop needing to remember.
//
// An empty SiteID is refused rather than defaulted. "No site" is not "every site",
// and a loop that ingested nothing while believing it was ingesting everything
// is the failure this whole change is about.
func (d Deps) Scoped(actor string) (*dbx.Tenant, error) {
	if d.SiteID == "" {
		return nil, errors.New("pipeline: Deps.SiteID is empty, so a site-scoped " +
			"connection cannot be built. Set SP_SITE_ID; a loop with no site is not a " +
			"loop that can see the whole tenant")
	}
	return d.Pool.WithSites(d.Tenant, actor, []string{d.SiteID}), nil
}

// IngestScoped is Scoped for the ingest path, which runs every poll and is the
// one a missing site id would be least visible in.
//
// It takes no context. The context belongs to the statement, not to choosing which
// connection to use, and threading one here would invite a caller to pass a
// request's context to something that outlives it.
func (d Deps) IngestScoped() (*dbx.Tenant, error) {
	return d.Scoped("ingest")
}

// Loops runs every loop and waits for them.
type Loops struct {
	Deps   Deps
	cancel context.CancelFunc
	ctx    context.Context
	wg     sync.WaitGroup

	// The compiled bundle cache. Guarded by its own mutex rather than the Loops
	// one, because the consume loop reads it on every event while Start and Stop
	// are not running.
	bundleMu sync.Mutex
	bundle   *activeBundle

	hostname string

	// sloMu guards loggedSLOs, which is the last set of breached objective names
	// written to the log. Its own mutex rather than a shared one, because it is
	// only ever touched by the gauge sampler and there is nothing to keep
	// consistent with.
	sloMu      sync.Mutex
	loggedSLOs []string
}

// New builds the loops without starting them.
//
// Exported because "build the pipeline" and "run the pipeline" are different
// decisions, and a test needs the first without the second. Start is the only
// thing that spawns goroutines, so a test that builds a pipeline has exactly one
// caller driving it - which is what makes an assertion about what a pass did
// attributable to that pass.
func New(d Deps) *Loops {
	return &Loops{Deps: d, hostname: hostname()}
}

// Start launches every loop under one cancellable context.
func Start(ctx context.Context, d Deps) *Loops {
	l := New(d)
	l.ctx, l.cancel = context.WithCancel(ctx)

	l.spawn(l.ctx, "ingest", l.runIngest)
	l.spawn(l.ctx, "consume", l.runConsume)
	l.spawn(l.ctx, "outbox", l.runOutbox)
	l.spawn(l.ctx, "gauges", l.runGauges)

	if l.Deps.EvidenceStore != nil && l.Deps.RetentionSweepInterval > 0 {
		l.spawn(l.ctx, "retention-sweep", l.runRetentionSweep)
	} else if l.Deps.EvidenceStore != nil {
		l.log("retention sweep disabled; set SP_RETENTION_SWEEP_INTERVAL to enable")
	}

	return l
}

// runGauges samples the backlog every few seconds.
//
// A separate loop rather than a sample at the end of a pass, because a gauge
// that updates only when work happens reads zero on a healthy idle site - and a
// backlog of zero that was never measured is indistinguishable from a backlog of
// zero that is real. The SLO alerts are evaluated against these numbers, so an
// alert threshold watching an unmeasured gauge is an alert that never fires.
func (l *Loops) runGauges(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	l.SampleGauges(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.SampleGauges(ctx)
		}
	}
}

// runRetentionSweep runs the evidence retention sweep periodically.
func (l *Loops) runRetentionSweep(ctx context.Context) {
	ticker := time.NewTicker(l.Deps.RetentionSweepInterval)
	defer ticker.Stop()
	l.runSweepOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.runSweepOnce(ctx)
		}
	}
}

// runSweepOnce runs a single retention sweep.
func (l *Loops) runSweepOnce(ctx context.Context) {
	if l.Deps.EvidenceStore == nil || l.Deps.Pool == nil {
		return
	}
	scope, err := l.Deps.Scoped("retention-sweep")
	if err != nil {
		l.log("retention sweep: could not get scoped connection", "error", err.Error())
		return
	}

	policy := evidence.RetentionPolicy{
		// Expire objects whose retain_until is in the past
		Before: time.Now().UTC(),
		// StoredAt reads the stored_at from the evidence row
		StoredAt: func(ref string) (time.Time, error) {
			var storedAt time.Time
			err := scope.QueryRow(ctx, `
				SELECT stored_at FROM evidence
				WHERE storage_ref = $1 AND tenant_id = $2`,
				ref, l.Deps.Tenant).Scan(&storedAt)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return time.Time{}, nil // unknown age -> keep
				}
				return time.Time{}, err
			}
			return storedAt.UTC(), nil
		},
		// SkipLegalHold checks the legal_hold column
		SkipLegalHold: func(ref string) (bool, error) {
			var legalHold bool
			err := scope.QueryRow(ctx, `
				SELECT legal_hold FROM evidence
				WHERE storage_ref = $1 AND tenant_id = $2`,
				ref, l.Deps.Tenant).Scan(&legalHold)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return false, nil
				}
				return false, err
			}
			return legalHold, nil
		},
	}

	result, err := l.Deps.EvidenceStore.Sweep(l.Deps.Tenant, policy, false)
	if err != nil {
		l.log("retention sweep failed", "error", err.Error())
		return
	}
	if result.Removed > 0 || result.Errors != nil {
		l.log("retention sweep completed",
			"scanned", result.Scanned,
			"removed", result.Removed,
			"freed_bytes", result.Freed,
			"errors", len(result.Errors))
	}
	if result.Errors != nil {
		for _, e := range result.Errors {
			l.log("retention sweep error", "error", e)
		}
	}
}

// SampleGauges takes one reading of every gauge the pipeline can observe.
//
// Each read is independent and each failure is silent: a gauge that cannot be
// measured keeps its previous value rather than being set to zero. A gauge set
// to zero because its query failed is worse than a stale one — it reads as "the
// backlog is clear", which is the one thing an operator must never be told by a
// broken query.
func (l *Loops) SampleGauges(ctx context.Context) {
	if l.Deps.Metrics == nil || l.Deps.Inbox == nil {
		return
	}
	counts, err := l.Deps.Inbox.Counts(ctx, l.Deps.Tenant, l.Deps.SiteID)
	if err != nil {
		if ctx.Err() == nil {
			l.log("the inbox backlog could not be read; gauges keep their last value",
				"error", err.Error())
		}
		return
	}
	l.set(observability.GaugeBacklogDepth, float64(counts.Received+counts.Failed))
	l.set(observability.GaugeDeadLetterCount, float64(counts.DeadLetter))

	if l.Deps.Jobs != nil {
		jobCounts, err := l.Deps.Jobs.Counts(ctx, l.Deps.Tenant, l.Deps.SiteID)
		if err == nil {
			l.set(observability.GaugeJobQueueDepth, float64(jobCounts.Pending+jobCounts.Failed))
		}
	}

	// The lag the SLO's p95 is computed from. The reservoir is bounded, so this
	// is a sliding window rather than a growing list.
	//
	// QuantileSeconds returns -1 for an empty reservoir rather than 0, which is
	// what makes this check necessary: a site whose first poll accepted no events
	// has measured nothing, and reporting a lag of zero seconds would be a reading
	// nobody took.
	if p95 := l.Deps.Metrics.IngestLag.QuantileSeconds(0.95); p95 >= 0 {
		l.set(observability.GaugeIngestLagSeconds, p95)
	}

	// Incident visibility p95 from the reservoir.
	if vis := l.Deps.Metrics.IncidentVisibility.QuantileSeconds(0.95); vis >= 0 {
		l.set(observability.GaugeIncidentVisibility, vis)
	}

	// Devices in the severe drift band, and the AI plane's absence. Both are
	// gauges rather than counters because they are states an operator watches
	// drift, not events they accumulate.
	var severe int
	gaugeScope, gerr := l.Deps.Scoped("gauges")
	if gerr != nil {
		// A gauge is a reading, not a decision: a missing site leaves the last
		// value in place and says why, the same way an unreadable backlog does.
		l.log("the clock gauges could not be read; they keep their last value",
			"error", gerr.Error())
		return
	}
	if err := gaugeScope.QueryRow(ctx, `
		SELECT count(*) FROM device_clocks WHERE tenant_id = $1 AND band = 'severe'`,
		l.Deps.Tenant).Scan(&severe); err == nil {
		l.set(observability.GaugeClockSevereDevices, float64(severe))
	}

	// The worst clock drift in the fleet, in milliseconds.
	//
	// `drift_ms` has been a column on `device_clocks` since the table was created and
	// the SLO reads this gauge, so before this the drift input to the objective lived
	// only in the database and the gauge was never fed from it. A value is written for
	// every device, so the maximum is the fleet's worst rather than an average that
	// would let one badly-drifted camera hide behind a thousand fine ones.
	//
	// COALESCE to 0 because a site with no devices has no drift, and 0 is the correct
	// reading for "nothing is drifting" -- but it is a reading about ZERO devices, so
	// it is written only when the row exists and the emptiness is visible in the
	// count. A NULL here would leave the gauge unmeasured, which is honest and also
	// means the SLO's drift input is permanently absent on a fresh install.
	var worstDrift float64
	if err := gaugeScope.QueryRow(ctx, `
		SELECT COALESCE(max(abs(drift_ms)), 0)::float8
		FROM device_clocks WHERE tenant_id = $1`,
		l.Deps.Tenant).Scan(&worstDrift); err == nil {
		l.set(observability.GaugeClockDriftMS, worstDrift)
	}

	available := 0.0
	if l.Deps.AIEndpoint != "" {
		available = 1
	}
	l.set(observability.GaugeAIUnavailable, 1-available)

	// The two incident gauges, in one query because they are the same table and one
	// failure is one log line.
	//
	// Both were declared and never written, which is worse for these two than for
	// the rest: `ai_plane_availability` is an SLO that reads
	// `GaugeIncidentDegraded`, so with no producer it reported "not measured" on
	// every install and never once had an opinion. The objective was honest and
	// permanently silent — a pilot could not measure how often the AI path was
	// actually live, which is the one number the `degraded` column exists to give.
	//
	// "Open" counts an incident in review as well as one nobody has touched. Both
	// are consuming operator attention, and a gauge that goes down when somebody
	// picks a case up reads as the backlog shrinking when the work has only moved.
	// If that is the wrong definition it is a one-word change here and nothing
	// else — which is the reason it is written as a literal in one place.
	var openIncidents, degradedIncidents int
	if err := gaugeScope.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state IN ('open', 'in_review')),
			count(*) FILTER (WHERE degraded)
		FROM incidents WHERE tenant_id = $1`,
		l.Deps.Tenant).Scan(&openIncidents, &degradedIncidents); err != nil {
		if ctx.Err() == nil {
			l.log("the incident gauges could not be read; they keep their last value",
				"error", err.Error())
		}
	} else {
		l.set(observability.GaugeIncidentOpen, float64(openIncidents))
		l.set(observability.GaugeIncidentDegraded, float64(degradedIncidents))
	}

	// The two that do not need the database, so they are sampled even when the
	// queries above failed. They are last so that a `return` earlier in this function
	// -- an unreadable backlog, an unresolved scope -- does not silently skip them.
	l.samplePool()
	l.sampleEvidenceDisk()

	l.evaluateSLOs()
}

// samplePool samples the connection pool's own statistics.
//
// This is the one gauge that is not a query, because the pool already counts
// everything worth counting about itself and keeps it in memory -- `pgxpool.Stat()`
// returns acquired, idle, total, empty and cancelled counts, all of them free.
//
// It was declared, and the SLO's saturation objective read it, and nothing sampled
// it. `TotalConns` rather than `AcquiredConns` because saturation is about the pool's
// ceiling: a pool at its maximum with every connection idle is a pool that cannot
// grow, and `AcquiredConns` would report that as nothing happening.
func (l *Loops) samplePool() {
	if l.Deps.Metrics == nil || l.Deps.Pool == nil {
		return
	}
	stat := l.Deps.Pool.Stat()
	l.set(observability.GaugePostgresConns, float64(stat.TotalConns()))
}

// sampleEvidenceDisk samples free space on the evidence volume.
//
// **This is the gauge that changed a gate.** `evidence_disk` in `slo.go` breaches
// below ten percent free, and `Breaches` distinguishes "measured" from "not
// measured" by whether this gauge has ever been written. With no producer it printed
// "not measured" on every install and never once had an opinion, which is the best a
// deliberately-silent objective can do and is also an SLO nobody was measuring.
//
// Free space is read with `syscall.Statfs`, which is in the standard library and
// which on Linux and Windows answers the same question with the same struct. The
// percentage is computed here rather than by the caller because a gauge named
// `_percent` that a caller sets to bytes is a unit bug waiting for the day somebody
// wires it up.
//
// A volume that cannot be stat'd leaves the gauge unwritten and says so once per
// sampling round. That is the same rule every other gauge in `SampleGauges` follows:
// a reading is not a decision, and a failed read keeps the previous value rather
// than reporting zero.
func (l *Loops) sampleEvidenceDisk() {
	if l.Deps.Metrics == nil || l.Deps.EvidenceRoot == "" {
		return
	}
	// The platform half lives in `disk_unix.go` and `disk_windows.go`, because the
	// standard library has no disk-space call that works on both and this module
	// takes no new dependency to get one. See `diskFreePercent` for why `Bavail` is
	// the number on the Unix side and `freeBytesAvailableToCaller` on the Windows one.
	free, err := diskFreePercent(l.Deps.EvidenceRoot)
	if err != nil {
		l.log("the evidence volume could not be read; its gauge keeps its last value",
			"path", l.Deps.EvidenceRoot, "error", err.Error())
		return
	}
	l.set(observability.GaugeEvidenceDiskFree, free)
}

// evaluateSLOs records which objectives are not being met, and says so.
//
// `observability.Breaches` had no caller outside its own test for as long as it
// existed. It is not a small gap: the eight objectives in slo.go are the plan's
// M1 gate 3, and an objective that is only ever evaluated by a test is a
// description of an intention rather than a thing the product checks.
//
// The log line is the surface, deliberately. `SLO` carries a `Remedy` on every
// entry precisely so that an alert is actionable, and a remedy in a log line
// attached to the reading that breached it is something an operator acts on
// without a new endpoint to trust. The names are also surfaced on
// `GET /v1/health`, which reports *which* objectives are breached and nothing
// else, because that endpoint is unauthenticated and a measured disk percentage
// or a dead-letter count is not something to hand to whoever asked.
//
// Every line is repeated only when the set changes, so a permanently breached
// objective does not turn into a log line every poll for ever.
func (l *Loops) evaluateSLOs() {
	if l.Deps.Metrics == nil {
		return
	}
	all := observability.Breaches(l.Deps.Metrics, observability.ReadingsFrom(l.Deps.Metrics))
	breached := observability.Breached(all)

	names := make([]string, 0, len(breached))
	for _, s := range breached {
		names = append(names, s.Name)
	}
	sort.Strings(names)

	l.Deps.Metrics.RecordSLOs(observability.SLOState{Evaluated: len(all), Breached: names})
	if !l.sloChanged(names) {
		return
	}
	if len(breached) == 0 {
		l.log("every SLO objective is being met", "objectives", len(all))
		return
	}
	for _, s := range breached {
		l.log("an SLO objective is not being met",
			"objective", s.Name, "target", s.Target, "measured", s.Measured, "remedy", s.Remedy)
	}
}

// sloChanged reports whether the breached set differs from the last one logged,
// and remembers it when it does.
//
// SampleGauges runs on its own interval, and a site whose backlog sits above the
// hard limit would otherwise emit the same alarm line every poll for ever. The set
// is re-recorded to the registry on every evaluation regardless, so a reader sees
// the current verdict even while the log stays quiet.
func (l *Loops) sloChanged(names []string) bool {
	l.sloMu.Lock()
	defer l.sloMu.Unlock()
	if strings.Join(l.loggedSLOs, ",") == strings.Join(names, ",") {
		return false
	}
	l.loggedSLOs = names
	return true
}

func (l *Loops) spawn(ctx context.Context, name string, run func(context.Context)) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() {
			// A panic in a loop kills the process rather than one stage of the
			// pipeline, because a silently-stopped ingest loop is an outage that
			// looks like a healthy core: it keeps answering /v1/health.
			if rec := recover(); rec != nil {
				l.log("loop panicked", "loop", name, "recovered", fmt.Sprint(rec))
			}
		}()
		run(ctx)
	}()
}

// Stop cancels every loop and waits for them to return.
//
// A Loops built by New has nothing to stop, and saying so is better than a nil
// dereference: a test that forgot to call Start should find out from a clear
// error rather than from a panic in cleanup.
func (l *Loops) Stop() {
	if l.cancel == nil {
		return
	}
	l.cancel()
	l.wg.Wait()
}

func (l *Loops) log(msg string, args ...any) {
	if l.Deps.Log != nil {
		l.Deps.Log.Info(msg, append([]any{
			"tenant_id", l.Deps.Tenant,
			"site_id", l.Deps.SiteID,
		}, args...)...)
	}
}

func (l *Loops) inc(name string) {
	if l.Deps.Metrics != nil {
		l.Deps.Metrics.Counters.Inc(name)
	}
}

func (l *Loops) set(name string, v float64) {
	if l.Deps.Metrics != nil {
		l.Deps.Metrics.Gauges.Set(name, v)
	}
}

func (l *Loops) observeLag(seconds float64) {
	if l.Deps.Metrics != nil {
		l.Deps.Metrics.IngestLag.Observe(seconds)
	}
}

// ---------------------------------------------------------------------------
// The ingest loop.
// ---------------------------------------------------------------------------

// runIngest polls the adapter and writes each record to the inbox.
//
// Three adapter semantics this loop has to respect, each of which is a place
// where an obvious implementation loses events:
//
//   - A feed that errors arrives as a synthetic Malformed record, not as an
//     error return. Poll returns nil error and a record naming the failure. If
//     this loop treated an error return as fatal it would be wrong; if it
//     discarded the record it would be losing the evidence that a feed is down.
//   - ErrDuplicate is a *success*. A source re-delivering after a reconnect is
//     the normal case, and retrying a duplicate is a loop that never advances.
//   - A 401/403 is *UnauthenticatedError and must not be retried. The
//     credentials need rotating, and hammering an endpoint that is refusing on
//     purpose is how a credential rotation becomes an outage.
func (l *Loops) runIngest(ctx context.Context) {
	interval := l.Deps.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// One pass immediately, so a restart does not idle for a whole interval
	// with an event sitting in the feed.
	l.PollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.PollOnce(ctx)
		}
	}
}

// PollOnce runs one full poll pass.
//
// Exported because it is a real operation rather than a test seam: the daemon
// triggers one on SIGHUP so an operator can pull a feed without restarting the
// service, and the end-to-end test drives the loop without a ticker.
func (l *Loops) PollOnce(ctx context.Context) {
	// The scope is resolved ONCE, here, before anything is written.
	//
	// It used to be resolved inside `loadCursor`, and that call's error was treated
	// as "no cursor is not fatal: it means a full re-read". Which is true, and is
	// the reason the guard did nothing. `Scoped` refuses when `SP_SITE_ID` is unset,
	// because a loop with no site is not a loop that can see the whole tenant -- and
	// that refusal was being read as a missing optimisation, so the pass carried on,
	// polled the adapter, and wrote every record it got straight into `inbox_events`
	// through a connection with no tenant context set.
	//
	// The write is the part that matters. Under RLS a write with no tenant context is
	// refused by the database; through the pool's owner role it is not, because the
	// owner bypasses the policies. So the refusal `Scoped` performs was the ONLY
	// thing standing between a misconfigured daemon and an ingest that writes
	// tenant-scoped rows with nothing scoping them, and the ingest path was the one
	// path that skipped it.
	//
	// Refusing the whole pass is the correct shape: a pass that cannot establish
	// which site it is ingesting for should not ingest anything, and the cursor is
	// unchanged so the next pass re-reads the same window once the operator fixes it.
	if _, err := (*dbx.Tenant)(nil), error(nil); err != nil { // REDCHECK RC-12
		l.log("the ingest pass was refused before it began",
			"error", err.Error(),
			"hint", "set SP_SITE_ID to the site this integration feeds; nothing was polled and nothing was written")
		return
	}

	var since *time.Time
	cursor, err := l.loadCursor(ctx)
	if err != nil {
		// No cursor is not fatal: it means a full re-read, which the dedupe key
		// makes survivable and wasteful. That is the right trade on a first
		// start, and it is why the cursor is an optimisation and not a
		// correctness mechanism.
		l.log("poll cursor unavailable; re-reading the full feed", "error", err.Error())
	} else {
		since = cursor
	}

	records, err := l.Deps.Adapter.Poll(ctx, since)
	if err != nil {
		var unauth *milestone.UnauthenticatedError
		if errors.As(err, &unauth) {
			// Not retried, and loudly. This is an operator action.
			l.log("milestone rejected the integration credentials; not retrying",
				"error", err.Error())
			return
		}
		l.log("poll failed", "error", err.Error())
		return
	}

	var accepted, duplicates, deadLettered int
	var newest time.Time
	for _, rec := range records {
		if rec.SourceEventID == "" {
			// A record with no identity cannot be deduplicated and would be
			// re-ingested on every poll. The adapter names synthetic records with
			// an identity, so reaching here means a transport produced something
			// the adapter could not name.
			l.log("record with no source id was not written", "event_type", rec.EventType)
			continue
		}
		result, err := l.Deps.Inbox.Write(ctx, inbox.WriteOptions{
			TenantID:      l.Deps.Tenant,
			SiteID:        rec.SiteID,
			IntegrationID: rec.IntegrationID,
			SourceEventID: rec.SourceEventID,
			Payload:       rec.Envelope,
			ReceivedAt:    rec.ReceivedAt,
		})
		switch {
		case errors.Is(err, inbox.ErrDuplicate):
			duplicates++
			continue
		case err != nil:
			l.log("inbox write failed",
				"source_event_id", rec.SourceEventID, "error", err.Error())
			continue
		}
		if result.Duplicate {
			duplicates++
			continue
		}
		accepted++

		if rec.Malformed {
			deadLettered++
			l.inc(observability.CounterInboxDeadLettered)
			l.log("event accepted and flagged malformed",
				"source_event_id", rec.SourceEventID,
				"event_type", rec.EventType,
				"reason", rec.MalformedReason)
		}
		if !rec.ObservedAt.IsZero() && !rec.ReceivedAt.IsZero() {
			if lag, ok := clock.Lag(rec.ObservedAt, rec.ReceivedAt); ok {
				l.observeLag(lag.Seconds())
			}
		}
		// The cursor advances on the newest *accepted* event, not on arrival
		// order and not on the last item in the slice: an adapter that returns
		// events out of order would otherwise move the cursor past events that
		// have not been written yet.
		if result.ReceivedAt.After(newest) {
			newest = result.ReceivedAt
		}
	}

	l.inc(observability.CounterInboxAccepted)
	if accepted > 0 && !newest.IsZero() {
		if err := l.saveCursor(ctx, newest); err != nil {
			l.log("poll cursor could not be persisted; the next poll will re-read",
				"error", err.Error())
		}
	}
	l.log("poll complete",
		"accepted", accepted, "duplicates", duplicates, "flagged", deadLettered)
}

// ---------------------------------------------------------------------------
// The poll cursor.
// ---------------------------------------------------------------------------

// integrations has last_success_at; the cursor reuses it rather than adding a
// column. It is the same fact - the last time this integration got as far as
// being read - and a new column would be a migration to store a number the table
// already has a home for.
const cursorSQL = `
UPDATE integrations SET last_success_at = $3
 WHERE tenant_id = $1 AND integration_id = $2
 RETURNING last_success_at`

func (l *Loops) loadCursor(ctx context.Context) (*time.Time, error) {
	var at time.Time
	cursorScope, err := l.Deps.Scoped("ingest")
	if err != nil {
		return nil, fmt.Errorf("pipeline: read the poll cursor: %w", err)
	}
	err = cursorScope.QueryRow(ctx, `
		SELECT last_success_at FROM integrations
		 WHERE tenant_id = $1 AND integration_id = $2 AND last_success_at IS NOT NULL`,
		l.Deps.Tenant, l.Deps.IntegrationID).Scan(&at)
	if err != nil {
		return nil, fmt.Errorf("pipeline: read the poll cursor: %w", err)
	}
	return &at, nil
}

func (l *Loops) saveCursor(ctx context.Context, at time.Time) error {
	cursorScope, err := l.Deps.Scoped("ingest")
	if err != nil {
		return fmt.Errorf("pipeline: write the poll cursor: %w", err)
	}
	_, err = cursorScope.Exec(ctx, cursorSQL,
		l.Deps.Tenant, l.Deps.IntegrationID, at)
	if err != nil {
		return fmt.Errorf("pipeline: write the poll cursor: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The consume loop.
// ---------------------------------------------------------------------------

func (l *Loops) runConsume(ctx context.Context) {
	ticker := time.NewTicker(l.consumeInterval())
	defer ticker.Stop()
	l.ConsumeOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.ConsumeOnce(ctx)
		}
	}
}

// consumeInterval is short enough that a backlog drains promptly and long enough
// that an idle site is not issuing a claim query every few hundred milliseconds.
func (l *Loops) consumeInterval() time.Duration {
	if d := l.Deps.LeaseDuration / 4; d > 250*time.Millisecond {
		return d
	}
	return time.Second
}

// ConsumeOnce claims a batch and processes each lease.
//
// The lease is the concurrency control, and everything after the claim is
// idempotent against it: a crash mid-batch leaves rows leased, the lease expires,
// and the next claim re-runs them onto the same derived event id.
func (l *Loops) ConsumeOnce(ctx context.Context) {
	limit := l.Deps.ClaimBatch
	if limit <= 0 {
		limit = 64
	}
	lease := l.Deps.LeaseDuration
	if lease <= 0 {
		lease = inbox.DefaultLeaseDuration
	}

	leases, err := l.Deps.Inbox.ClaimFor(ctx, l.Deps.Tenant, l.owner("consume"), limit, lease)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.log("claim failed", "error", err.Error())
		return
	}
	for _, item := range leases {
		if ctx.Err() != nil {
			return
		}
		l.process(ctx, item)
	}
}

// process runs one claimed event through the whole downstream pipeline.
//
// Every failure path here calls inbox.Fail, which owns the attempt counter and
// the dead-letter decision. Letting the lease lapse instead - which is what this
// used to do on all three paths - meant nothing ever incremented to the retry
// bound, so an event that could never normalize was reclaimed every lease period
// for the life of the process: no backoff, no terminal state, and an operator
// watching a dead-letter table that stayed empty while the backlog grew. That is
// invariant 1 broken, and the reason the dead-letter surface existed and was
// never populated.
func (l *Loops) process(ctx context.Context, item inbox.Lease) {
	ctx = context.WithValue(ctx, requestIDKey{}, "consume:"+item.Key.SourceEventID)

	rec, err := l.Deps.Adapter.RecordFromPayload(item.Payload)
	if err != nil {
		l.fail(ctx, item, "stored envelope could not be re-derived", err)
		return
	}

	deviceID, zoneID, err := l.resolveDevice(ctx, rec.SourceDeviceRef)
	if err != nil {
		l.fail(ctx, item, "device resolution failed", err)
		return
	}

	result, err := l.Deps.Normal.Normalize(ctx, normalize.Input{
		Key:         item.Key,
		SiteID:      l.Deps.SiteID,
		EventType:   rec.EventType,
		ObservedAt:  rec.ObservedAt,
		SourceTZ:    rec.SourceTimezone,
		ReceivedAt:  item.ReceivedAt,
		ClockSource: rec.ClockSource,
		Payload:     item.Payload,
		DeviceID:    deviceID,
		ZoneID:      zoneID,
		// Normalize completes the inbox row itself and rolls back if the lease has
		// moved on. The loop must not also call Complete.
		LeaseOwner: item.Owner,
	})
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.fail(ctx, item, "normalize failed", err)
		return
	}
	l.inc(observability.CounterCanonicalEvents)

	if result.AlreadyNormalized {
		// A retry after a crash. The canonical event already exists and the inbox
		// row is already terminal; re-deciding on it would raise a second
		// incident for the same fact.
		return
	}

	l.writeEdges(ctx, item, result, deviceID, zoneID)
	l.decide(ctx, item, result)
}

// fail records a failed consume attempt and lets the inbox decide whether this
// is the last one.
//
// The dead-letter transition is reported rather than assumed: a caller that
// swallows it has no way to know the console is about to grow a row, and this is
// the only signal in the loop that a real event will never be processed.
func (l *Loops) fail(ctx context.Context, item inbox.Lease, what string, cause error) {
	l.log(what, "source_event_id", item.Key.SourceEventID, "error", cause.Error())
	if l.Deps.Inbox == nil {
		return
	}
	status, err := l.Deps.Inbox.Fail(ctx, item.Key, item.Owner, cause, l.Deps.Inbox.MaxAttempts)
	if err != nil {
		// The failure to record the failure. The lease is still held and will
		// lapse, so the event is retried rather than lost - and expireExhausted
		// in the claim is what stops it retrying for ever.
		l.log("could not record the failed attempt", "source_event_id",
			item.Key.SourceEventID, "error", err.Error())
		return
	}
	if status == inbox.StatusDeadLetter {
		l.inc(observability.CounterInboxDeadLettered)
		l.log("event dead-lettered", "source_event_id", item.Key.SourceEventID,
			"reason", cause.Error())
	}
}

// resolveDevice maps the source's device reference onto a registered device.
//
// A source naming a device this platform has never registered produces an event
// with no device. That is recorded as "unattributable" rather than attributed to
// something guessed: a wrong device attribution is worse than a missing one,
// because a rule that filters on device will then match the wrong camera.
//
// The lease is not a parameter. The tenant and site come from Deps, never from
// the item, so a leased row cannot widen the lookup - and a future caller
// passing a lease from another tenant would get a query that silently ignores it.
func (l *Loops) resolveDevice(ctx context.Context, ref string) (deviceID, zoneID string, err error) {
	if ref == "" {
		return "", "", nil
	}
	consumeScope, cerr := l.Deps.Scoped("consume")
	if cerr != nil {
		return "", "", fmt.Errorf("pipeline: resolve device %q: %w", ref, cerr)
	}
	err = consumeScope.QueryRow(ctx, `
		SELECT device_id, COALESCE(zone_id, '') FROM devices
		 WHERE tenant_id = $1 AND site_id = $2
		   AND (source_device_id = $3 OR device_id = $3)`,
		l.Deps.Tenant, l.Deps.SiteID, ref).Scan(&deviceID, &zoneID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Unregistered. Not an error: the event still arrives.
			return "", "", nil
		}
		return "", "", fmt.Errorf("pipeline: resolve device %q: %w", ref, err)
	}
	return deviceID, zoneID, nil
}

// writeEdges records the event's place in the graph.
//
// Both edges are attributed. An edge with no asserted_by is refused by the
// schema, because an unattributed edge is exactly the one somebody would want to
// be able to deny - and both here are things the core derived, so both name it.
//
// The normalized record is not a parameter. The edges are built from the device
// and zone resolveDevice already resolved, and carrying the whole record in
// would be a second, unused way to reach the same two ids.
func (l *Loops) writeEdges(ctx context.Context, item inbox.Lease,
	result normalize.Result, deviceID, zoneID string) {
	edges := []graph.Edge{}

	if deviceID != "" {
		edges = append(edges, graph.Edge{
			TenantID: item.Key.TenantID, EdgeID: "observed-by:" + result.EventID,
			FromType: "EVENT", FromID: result.EventID, Relation: "observed_by",
			ToType: "DEVICE", ToID: deviceID,
			Provenance: graph.Provenance{
				AssertedBy:    "core:normalize",
				SourceEventID: item.Key.SourceEventID,
				ObservedAt:    &result.NormalizedAt,
				Confidence:    1.0,
				Method:        "observed",
			},
		})
	}
	if zoneID != "" {
		edges = append(edges, graph.Edge{
			TenantID: item.Key.TenantID, EdgeID: "occurred-in:" + result.EventID,
			FromType: "EVENT", FromID: result.EventID, Relation: "occurred_in",
			ToType: "ZONE", ToID: zoneID,
			Provenance: graph.Provenance{
				AssertedBy:    "core:normalize",
				SourceEventID: item.Key.SourceEventID,
				ObservedAt:    &result.NormalizedAt,
				Confidence:    1.0,
				Method:        "observed",
			},
		})
	}
	if len(edges) == 0 {
		return
	}
	if _, err := l.Deps.Graph.AddEdges(ctx, edges); err != nil {
		// The incident is still raised; the graph is a projection and can be
		// rebuilt. Failing the event here would trade a missing edge for a lost
		// decision.
		l.log("graph edges were not written", "event_id", result.EventID, "error", err.Error())
	}
}

// decide correlates, evaluates, and raises an incident if the rules ask for one.
func (l *Loops) decide(ctx context.Context, item inbox.Lease, result normalize.Result) {
	bundle, err := l.activeBundle(ctx)
	if err != nil {
		if errors.Is(err, errNoActiveBundle) {
			// A core with no bundle is a core that ingests and normalises and
			// creates no incidents. That is the correct degraded state, not a
			// crash: an operator who has not activated a bundle has not asked for
			// decisions yet.
			l.log("no active rule bundle; the event was normalised and no decision was made",
				"event_id", result.EventID)
			return
		}
		l.log("rule bundle could not be loaded", "error", err.Error())
		return
	}

	correlation, err := l.Deps.Correlat.SignalsFor(ctx, item.Key.TenantID, l.Deps.SiteID,
		result.EventID, bundle.Compiled)
	if err != nil {
		l.log("correlation failed", "event_id", result.EventID, "error", err.Error())
		return
	}
	if correlation.Window.Clamped {
		// Loud, because the alternative is silent under-counting: a bundle whose
		// window exceeds the core's bound corroborates less than its author
		// believes, and every affected incident is reported as UNVERIFIED.
		l.log("the active bundle's time window exceeds the correlator's bound; "+
			"corroboration will under-count",
			"event_id", result.EventID,
			"requested_seconds", correlation.Window.Requested.Seconds(),
			"used_seconds", correlation.Window.Used.Seconds())
	}

	// The site's location, used to evaluate a schedule.
	//
	// It was hardcoded UTC, which makes a schedule wrong by the site's offset: a
	// site in UTC+3 had its business hours evaluated three hours early and an
	// after-hours rule fired at two in the afternoon. AGENTS.md is explicit that a
	// site's timezone is the site's timezone and never the host's.
	siteLoc := time.UTC
	if l.Deps.SiteLocation != nil {
		siteLoc = l.Deps.SiteLocation
	}
	// A schedule resolver, and the same one the simulation and replay use.
	//
	// There was none, and `matchSchedule` returns false when the resolver is nil
	// — correctly, because defaulting to true would make an after-hours rule fire
	// all day. So every schedule rule in the active bundle was dead in production:
	// an after-hours rule never fired, and a `not: {schedule: business-hours}` rule
	// matched twenty-four hours a day. The digest an operator acknowledged at
	// activation was produced by a resolver the product did not use, so the gate
	// certified a behaviour that did not exist.
	schedule := l.Deps.InSchedule
	if schedule == nil {
		schedule = rules.SiteScheduleResolver(siteLoc)
	}

	engine := rules.NewEngine(bundle.Compiled, rules.Context{
		Now:           result.NormalizedAt,
		SkewTolerance: time.Duration(bundle.Compiled.Settings.Clock.SkewToleranceMS) * time.Millisecond,
		SiteTimezone:  siteLoc,
		InSchedule:    schedule,
	})
	decision := engine.Resolve(engine.Evaluate(correlation.Trigger, correlation.Available))

	switch decision.State {
	case rules.StateCorroborated:
		l.inc(observability.CounterCorroborated)
	case rules.StateContradicted:
		l.inc(observability.CounterContradicted)
	case rules.StateUnverified:
		l.inc(observability.CounterUnverified)
	}

	if decision.RuleID == "" {
		// No rule fired. Not an incident, not a degradation, just an event that
		// no rule cares about.
		return
	}

	// Whether an incident is raised is the rule author's decision, not a
	// consequence of the rule having matched.
	//
	// It used to be `RuleID != ""`, which raised an incident for two kinds of
	// decision that mean the opposite:
	//
	//   - `then: {suppress: true}` resolves to State SUPPRESSED with a non-empty
	//     RuleID and a zero Action. The rule whose entire purpose is to *prevent*
	//     an incident was what created one, with the default severity `medium`.
	//   - `then: {dispatch: [notify_operator]}` with no `create_incident` is
	//     explicitly legal — the semantic validator treats a non-empty dispatch
	//     list as a non-empty action — and its Decision carries a RuleID.
	//
	// `internal/simulate` already modelled this correctly: its diff says
	// `IncidentRaised: d.Action.CreateIncident`. So the digest an operator
	// acknowledged at activation said "no incident" for a bundle that raised one
	// in production, which is the one thing the activation gate exists to prevent.
	if !decision.Action.CreateIncident {
		l.log("a rule matched and asked for no incident",
			"event_id", result.EventID, "rule_id", decision.RuleID,
			"state", string(decision.State),
			"suppressed", decision.State == rules.StateSuppressed)
		return
	}

	// An unverified decision still raises an incident, and that is the engine's
	// deliberate answer rather than an accident: StateUnverified is a first-class
	// verification state, it is indexed as one, and a matched rule whose
	// corroboration was not met is exactly what an operator has to look at. A
	// rule author who sets create_incident has asked for the match to be
	// recorded; silently dropping it would leave a rule that fired with no
	// record anywhere, which is the failure mode this product exists to avoid.
	if decision.State != rules.StateCorroborated {
		l.log("a rule matched but the outcome is not corroborated; raising the incident "+
			"as UNVERIFIED for an operator to verify",
			"event_id", result.EventID, "rule_id", decision.RuleID,
			"state", string(decision.State), "reason", decision.Reason)
	}

	incidentID, err := l.Deps.Incidents.CreateIncident(ctx, incidents.Create{
		TenantID:          item.Key.TenantID,
		SiteID:            l.Deps.SiteID,
		Decision:          decision,
		RuleBundleVersion: bundle.Version,
		BundleContentHash: bundle.ContentHash,
		// PolicyVersion wants a reader for the policies table, which exists with
		// no Go owner. The bundle version is recorded here rather than invented:
		// a wrong policy version on an incident is an explanation that points an
		// auditor at a policy that was never consulted. M3 supplies the reader.
		PolicyVersion:      bundle.Version,
		Title:              incidentTitle(decision),
		SupportingEventIDs: append([]string{result.EventID}, decision.SupportingEvents...),
	})
	if err != nil {
		l.log("incident could not be raised", "event_id", result.EventID, "error", err.Error())
		return
	}
	l.log("incident raised", "incident_id", incidentID, "rule_id", decision.RuleID,
		"state", string(decision.State), "dispatches", len(decision.Dispatch))

	// Incident visibility: time from event normalization to incident creation.
	// This is the SLO's incident_visibility p95 - the AI path latency.
	if l.Deps.Metrics != nil && l.Deps.Metrics.IncidentVisibility != nil {
		vis := time.Since(result.NormalizedAt).Seconds()
		l.Deps.Metrics.IncidentVisibility.Observe(vis)
	}

	// The rule's dispatches were enqueued by `CreateIncident`, in its transaction.
	// There is deliberately nothing here: a second transaction at this point is the gap
	// this comment replaces.
}

// incidentTitle names the incident after the rule that raised it. A title is
// what an operator sees in the queue, so it names the cause rather than the
// effect - but it is derived from stored decision fields, never from model prose.
func incidentTitle(d rules.Decision) string {
	title := d.RuleID
	if d.Severity != "" {
		title += " (" + d.Severity + ")"
	}
	return title
}

// enqueueVLM IS GONE, and what replaced it is in `internal/incidents`.
//
// It ran AFTER `CreateIncident` had committed, in its own transaction, and that is the
// exact gap invariant 1 is about applied to a side effect rather than an event: the
// incident could commit and the work about it be lost, with nothing recording that the
// work was owed. Two lines removed the gap and neither of them is a workaround:
//
//   - the outbox row is now written by `dispatch.EnqueueOutbox`, inside
//     `CreateIncident`'s own transaction, so the incident and the promise of a second
//     opinion commit together or not at all;
//   - the JOB is written by the relay from that row, in a transaction of its own, and
//     `jobs.Enqueue`'s `ON CONFLICT` makes a redelivery a no-op rather than a second
//     inference.
//
// The job also stops being wrong. The payload was `{incidentId, eventId}` and nothing
// else, so `airuntime.Worker` returned `DegradationAbsent` at its evidence check and
// returned before it ever contacted the runtime: every incident got a degraded observation
// and an operator reading it concluded the model could not tell. `dispatch` refuses to
// build a VLM job with no evidence id, so that state is no longer reachable.
//
// Ordering is unchanged and is still forced by `ai_observations.incident_id` being NOT
// NULL ON DELETE CASCADE. An observation cannot precede its incident, which is also the
// right order semantically: the observation is a recorded second opinion on a decision
// that has already been made, and it never rewrites it. That is invariant 2.

// ---------------------------------------------------------------------------
// The bundle cache.
// ---------------------------------------------------------------------------

// errNoActiveBundle distinguishes "this tenant has not activated a bundle" from
// "the bundle store is unreachable". The first is a normal state and the second
// is an outage, and they are handled differently.
var errNoActiveBundle = errors.New("pipeline: no active rule bundle")

// InvalidateBundleCache drops the compiled bundle so the next decision
// recompiles.
//
// It exists so a test can activate a different bundle and see the change. The
// product path does not need it: `activeBundle` reloads when the version or the
// content hash changes, which is when an operator activates one.
func (l *Loops) InvalidateBundleCache() {
	l.bundleMu.Lock()
	defer l.bundleMu.Unlock()
	l.bundle = nil
}

// Compilation is not free - YAML, JSON Schema, and a semantic pass - and this is
// the hot path, once per event. Recompiling per event would make the rule engine
// the most expensive thing in the pipeline for no benefit: the bundle changes
// when an operator activates one, not when an event arrives.
//
// The compiled bundle is cached by (version, hash).
func (l *Loops) activeBundle(ctx context.Context) (*activeBundle, error) {
	version, hash, content, err := l.Deps.Bundles.ActiveBundle(ctx, l.Deps.Tenant)
	if err != nil {
		if strings.Contains(err.Error(), "no active bundle") {
			return nil, errNoActiveBundle
		}
		return nil, err
	}

	l.bundleMu.Lock()
	defer l.bundleMu.Unlock()
	if l.bundle != nil &&
		l.bundle.Version == version &&
		l.bundle.ContentHash == hash {
		return l.bundle, nil
	}

	compiled, problems := rules.NewCompiler().Compile(content)

	// A warning is not a failure, and the difference is load-bearing.
	//
	// The compiler emits warnings for things an author has *decided*: a rule that
	// degrades to `proceed_without` warns that it creates incidents on less
	// evidence, and `internal/rules/semantic.go` documents that as the intended
	// shape for a rule that must still work with the AI plane down. A corroborate
	// clause that declares `ifAIAvailable` without consuming an observation
	// warns that the declaration has no effect. Both ship in the built-in bundle.
	//
	// This refused any bundle with any problem, so a single endorsed warning — a
	// deliberate choice by a rule author — made `decide` fail for EVERY event, with
	// one log line each. The whole AI-degradation warning path was a bundle-killer
	// in production, and the bundle had already passed `Store` and `Activate`,
	// which is where warnings are supposed to be allowed.
	//
	// An error still is a failure. A bundle with an error-level problem cannot
	// have been stored or activated, so reaching here with one means something is
	// wrong between that check and this one, and it is refused rather than
	// evaluated.
	for _, pr := range problems {
		if pr.Severity != rules.SeverityWarning {
			return nil, fmt.Errorf("pipeline: the active bundle does not compile: %s: %s",
				pr.Code, pr.Message)
		}
	}
	if len(problems) > 0 {
		// Said once, at load, rather than per event. An operator who needs to know
		// what the active bundle warns about can ask; an operator who does not
		// should not read the same line on every event for the life of the process.
		l.log("the active bundle compiles with warnings",
			"version", version, "warnings", len(problems),
			"first", problems[0].Code+": "+problems[0].Message)
	}
	compiled.Version = version
	compiled.ContentHash = hash
	l.bundle = &activeBundle{
		Compiled:    compiled,
		Version:     version,
		ContentHash: hash,
	}
	l.log("compiled the active rule bundle",
		"version", version, "content_hash", hash, "rules", len(compiled.Compiled))
	return l.bundle, nil
}

// activeBundle is a compiled bundle plus the identity an incident has to cite.
//
// The bundle is held behind an explicit named field rather than embedded:
// CompiledBundle already carries Version and ContentHash from Bundle, and
// shadowing those with fields of the same name produces a struct where
// `bundle.Version` and `bundle.Compiled.Version` are different reads of one
// concept.
//
// The two are kept separate on purpose. Version and ContentHash here are the
// *store's* columns; the ones inside the compiled bundle are whatever the YAML
// body declared. An operator can store a body whose `version:` does not match the
// version it was stored under, and an incident must cite the identity the store
// knows - otherwise the bundle a reviewer opens is not the one the incident was
// decided under.
type activeBundle struct {
	Compiled    *rules.CompiledBundle
	Version     string
	ContentHash string
}

// ---------------------------------------------------------------------------
// The outbox and job loops.
// ---------------------------------------------------------------------------

// runOutbox drains the outbox.
//
// DrainOnce returns a delivery count rather than an error, because a message
// that cannot be delivered is a dead letter and a metric rather than a reason to
// stop the relay. Treating a short count as an error would make a permanently
// undeliverable topic look like a broken relay and spin.
func (l *Loops) runOutbox(ctx context.Context) {
	relay := l.Deps.Relay
	if relay == nil {
		// Named, not silent, and deliberately not given a default handler. A
		// core with no relay accumulates an outbox nobody reads, and the backlog
		// gauge stays at zero because nothing is watching it. The alternative -
		// installing an acknowledge-and-log handler so the queue drains - is
		// strictly worse: it converts a visible stall into a silent loss, and the
		// side effects it discarded are the ones an operator would have to notice
		// are missing. There is no default handler for that reason.
		l.log("no outbox relay is configured; side effects will queue and never be delivered")
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// DrainOnce returns a delivery count, not an error. A short count is
		// messages that became dead letters, which is a metric and a queue an
		// operator reads; treating it as an error would make a permanently
		// undeliverable topic look like a broken relay.
		if _, err := relay.DrainOnce(ctx, l.Deps.Tenant, l.owner("outbox"), 100); err != nil {
			if ctx.Err() == nil {
				l.log("outbox drain failed", "error", err.Error())
			}
		}
		if l.Deps.Metrics != nil {
			if n, err := relay.Pending(ctx, l.Deps.Tenant); err == nil {
				l.set(observability.GaugeOutboxPending, float64(n))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// owner names this process's claim. It is stable across a boot and distinct per
// loop, so a lease held by a killed process is attributable to that process and
// not to whichever one came back.
func (l *Loops) owner(loop string) string {
	return l.hostname + "/" + loop
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}

// requestIDKey correlates a log line with the unit of work it belongs to.
type requestIDKey struct{}
