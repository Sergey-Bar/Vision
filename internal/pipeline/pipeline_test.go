package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/correlator"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/graph"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/normalize"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/outbox"
	"github.com/Sergey-Bar/vision/internal/pipeline"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/simulate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The end-to-end pipeline test.
//
// Every stage below is the production stage: the real adapter, the real inbox,
// the real normalizer, the real correlator, the real engine, the real incident
// service. Only the Milestone transport is stubbed, because it is the one
// component outside the process.
//
// The assertion is invariant 1 in one sentence: an event that reaches the inbox
// reaches a terminal state. Everything else this test checks - the canonical
// event, the graph edges, the incident, the enqueued second opinion - is detail,
// and detail is only interesting once the invariant holds.

// epoch is the fixture's time. Fixed, so the events fall inside the canonical
// event table's 2026 partitions.
var epoch = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// The adapter's real event route.
//
// It used to be "/api/v1/Events", which is not a Milestone prefix at all and
// appears in neither published document — see docs/vendor/milestone/route-audit.md
// §2.1. Every poll test here matched on it and passed, because the stub matches on
// the same wrong string: a test double agreeing with the code under test about a
// value the vendor contradicts is the definition of testing nothing.
const eventsRoute = "/api/rest/v1/events"

// stubTransport serves one feed page and nothing else, which is what a site with
// one deployed feed looks like.
type stubTransport struct {
	body json.RawMessage
	// calls counts the reads, so a test can assert the cursor actually narrowed
	// the second poll rather than both polls being full re-reads.
	calls int
	// queries records the query each call carried.
	queries []milestone.Query
}

func (s *stubTransport) Get(_ context.Context, route milestone.Route, q milestone.Query) (*milestone.Response, error) {
	s.calls++
	s.queries = append(s.queries, q)
	if route.Path != eventsRoute {
		// Not deployed on this site, which must not look like a fault.
		return &milestone.Response{Status: 404, Route: route}, nil
	}
	return &milestone.Response{Status: 200, Body: s.body, Route: route}, nil
}

func (s *stubTransport) Close() error { return nil }

type harness struct {
	loops       *pipeline.Loops
	transport   *stubTransport
	registry    *observability.Registry
	tenant      string
	site        string
	integration string
	// log is the buffer the loops' logger writes to. Exposed so a test can assert
	// on what an operator would read, which for the SLO objectives is a log line
	// and for everything else is a return value nothing returns.
	log *bytes.Buffer
}

func setup(t *testing.T, bundleYAML string, feed string) harness {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenant, site, _ := testutil.NewTenant(t, raw)
	const integration = "int-milestone"

	// The topology the rules refer to. Without a registered zone and device the
	// events would normalize as unattributable, which is correct behaviour and
	// would make this test assert nothing.
	// The integrations row is not decoration: the pipeline reads last_success_at
	// from it as the poll cursor and updates it after each accepted poll. Without
	// it the cursor read fails on every pass and every poll is a full re-read.
	testutil.Exec(t, raw, `
		INSERT INTO integrations (tenant_id, integration_id, site_id, type, enabled, base_url)
		VALUES ($1, $2, $3, 'xprotect_milestone', true, 'http://127.0.0.1:8000/api')
		ON CONFLICT (tenant_id, integration_id) DO NOTHING`, tenant, integration, site)
	testutil.Exec(t, raw, `
		INSERT INTO zones (tenant_id, zone_id, site_id, name, path, depth)
		VALUES ($1, 'zone-restricted', $2, 'Restricted', '/site/restricted/door', 2)`,
		tenant, site)
	testutil.Exec(t, raw, `
		INSERT INTO devices (tenant_id, device_id, site_id, zone_id, kind, attributes, source_device_id)
		VALUES ($1, 'cam-1', $2, 'zone-restricted', 'camera', '{"tags":["perimeter"]}', 'cam-1')`,
		tenant, site)
	testutil.Exec(t, raw, `
		INSERT INTO device_clocks (tenant_id, device_id, site_id, clock_source, drift_ms, band)
		VALUES ($1, 'cam-1', $2, 'ntp', 0, 'ok')`, tenant, site)

	if bundleYAML != "" {
		svc := &bundles.Service{Pool: pool, Compiler: rules.NewCompiler(),
			Audit: &audit.Writer{Pool: pool}}
		meta, problems, err := svc.Store(context.Background(), tenant,
			[]byte(bundleYAML), "test", "tester", "")
		if err != nil {
			t.Fatalf("store the bundle: %v", err)
		}
		// A warning is logged, not fatal.
		//
		// The compiler warns about things a rule author has decided — a
		// `proceed_without` fallback, an `ifAIAvailable` that has no AI to apply
		// to. Treating any problem as fatal here would have made this harness
		// unable to express the very case it needs to test, which is how a
		// harness can quietly decide what a test is allowed to ask.
		for _, p := range problems {
			t.Logf("bundle problem: %s %s %s", p.Severity, p.Code, p.Message)
			if p.Severity == rules.SeverityError {
				t.Fatalf("the bundle has an error-level problem and was not stored: %s", p.Message)
			}
		}
		if meta.Version == "" {
			t.Fatal("the bundle was not stored, and no problem said why")
		}
		// Activation goes through the gate, as it must: a bundle that has not been
		// simulated cannot be activated. The test acknowledges a digest rather than
		// bypassing the check, because a test that bypasses it would still pass the
		// day the gate stopped existing.
		rep := simulate.Report{BundleVersion: meta.Version, FixtureSet: "fixtures-1"}
		rep.DiffDigest = simulate.DigestFor(rep.BundleVersion, rep.FixtureSet, rep.Changes)
		if err := svc.Activate(context.Background(), tenant, meta.Version, rep, "tester"); err != nil {
			t.Fatalf("activate the bundle: %v", err)
		}
	}

	transport := &stubTransport{body: json.RawMessage(feed)}
	adapter := milestone.New(transport, site, integration).
		WithClock(func() time.Time { return epoch })

	registry := observability.NewRegistry()
	writer := &audit.Writer{Pool: pool}

	// Create a dispatcher and outbox relay to process VLM jobs enqueued by the incident service.
	dispatcher := &dispatch.Dispatcher{
		Dests:    &dispatch.Destinations{},
		Enqueuer: jobs.New(pool),
		Pool:     pool,
	}
	relay := &outbox.Relay{
		Pool:          pool,
		Handler:       dispatcher.Handler(),
		MaxAttempts:   8,
		LeaseDuration: 30 * time.Second,
		Metrics:       registry,
		Logf:          func(format string, args ...any) { slog.Debug(fmt.Sprintf(format, args...)) },
	}

	// A logger, because every failure inside the loops is logged and returned
	// nowhere. Without one, a pipeline that is failing on every event is a test
	// that fails on an assertion about a database it never wrote to, which reads
	// as a broken fixture rather than as a broken loop.
	var logBuf bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() && logBuf.Len() > 0 {
			t.Logf("pipeline log:\n%s", logBuf.String())
		}
	})

	loops := pipeline.New(pipeline.Deps{
		Pool: pool, Metrics: registry,
		Log:       slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Adapter:   adapter,
		Inbox:     inbox.NewWriter(pool),
		Normal:    normalize.New(pool),
		Graph:     &graph.Service{Pool: pool},
		Correlat:  &correlator.Correlator{Pool: pool},
		Incidents: &incidents.Service{Pool: pool, Audit: writer},
		Bundles:   &bundles.Service{Pool: pool, Compiler: rules.NewCompiler(), Audit: writer},
		Enqueuer:  jobs.New(pool),
		Tenant:    tenant, SiteID: site, IntegrationID: integration,
		LeaseDuration: 30 * time.Second,
		ClockPolicy:   clock.DefaultPolicy(),
		Relay:         relay,
	})

	return harness{
		loops: loops, transport: transport, registry: registry,
		tenant: tenant, site: site, integration: integration,
		log: &logBuf,
	}
}

// bundle is one rule: motion in a restricted zone, corroborated by a second
// independent signal inside the window, with an action that raises an incident.
//
// It reads the fields the correlator populates - event type, zone path prefix,
// signal tier - which is the point of asserting through it: if the correlator
// stopped filling ZonePath, the rule would stop matching and this test would say
// so rather than quietly passing on a bundle that matched nothing.
//
// No AI observation is required. `requiresAIObservation` with
// `degradeTo: UNVERIFIED` is the shape a rule that *does* consume a Tier C signal
// must take, and requiring one here would make the test depend on a model being
// present - which is a site with no AI plane, a supported configuration.
const bundle = `apiVersion: security.io/v1
kind: RuleBundle
version: 0.1.0
description: One rule for the end-to-end test.

site:
  siteId: site-default
  timezones:
    - UTC

settings:
  clock:
    referenceSource: ntp
    driftWarnMs: 2000
    driftSevereMs: 10000
    skewTolerance: 5000

rules:
  - id: test.motion-in-restricted-zone
    description: Motion in a restricted zone with a second independent signal.
    enabled: true
    priority: 500
    tags: [test]
    when:
      all:
        - eventType: [motion, analytics]
        - zone:
            pathPrefix: /site/restricted
        - timeWindow:
            field: normalized_at
            durationSeconds: 30
        - corroborate:
            eventTypes: [analytics, motion]
            minSignals: 1
            minEvidenceTier: B
    then:
      createIncident: true
      severity: high
      title: "Motion in a restricted zone"
`

// TestAnEventReachesATerminalState is the invariant-1 test.
//
// The event is offered once. Everything after that is the core's own work, and
// the only thing asserted is that the inbox row ends in a terminal state and
// that the downstream facts it produced exist.
func TestAnEventReachesATerminalState(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"},
		{"Id":"e2","Type":"Analytics","Timestamp":"2026-03-01T09:00:02Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()

	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)

	// Drain the outbox relay so VLM jobs enqueued by the incident service are processed.
	if _, err := h.loops.Deps.Relay.DrainOnce(ctx, h.tenant, "test-relay", 10); err != nil {
		t.Fatalf("drain outbox relay: %v", err)
	}

	// The inbox rows are the invariant. Anything accepted must leave 'received'.
	var received, processed int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'received'),
		        count(*) FILTER (WHERE status = 'processed')
		   FROM inbox_events WHERE tenant_id = $1`, h.tenant).Scan(&received, &processed); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if received != 0 {
		t.Errorf("%d accepted event(s) are still in 'received'; an event that reaches "+
			"inbox_events must reach a terminal state unless it is explicitly dead-lettered",
			received)
	}
	if processed != 2 {
		t.Fatalf("%d event(s) reached 'processed', want 2", processed)
	}

	// The canonical event, with the clock correction applied and the derived
	// event type stored under its normalized name.
	var eventID, eventType string
	var normalized time.Time
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT event_id, event_type, normalized_at FROM canonical_events
		  WHERE tenant_id = $1 AND event_type = 'motion'`, h.tenant).
		Scan(&eventID, &eventType, &normalized); err != nil {
		t.Fatalf("read the canonical event: %v", err)
	}
	if eventType != "motion" {
		t.Errorf("the canonical event type is %q, want the normalized name 'motion'", eventType)
	}
	if !normalized.Equal(epoch) {
		t.Errorf("normalized_at is %s, want the source's instant with no drift to correct", normalized)
	}

	// The graph edge attributing the event to its device. Both halves of the
	// provenance are named, because an unattributed edge is exactly the one
	// somebody would want to be able to deny.
	var assertedBy, deviceID string
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT asserted_by, to_id FROM edges_active
		  WHERE tenant_id = $1 AND from_id = $2 AND relation = 'observed_by'`,
		h.tenant, eventID).Scan(&assertedBy, &deviceID); err != nil {
		t.Fatalf("read the graph edge: %v", err)
	}
	if assertedBy != "core:normalize" {
		t.Errorf("the edge is attributed to %q", assertedBy)
	}
	if deviceID != "cam-1" {
		t.Errorf("the edge points at device %q, want cam-1", deviceID)
	}

	// The incident's lifecycle state, and the decision's verification state.
	//
	// Two different records, in two different tables. `incidents.state` is the
	// workflow (open / in_review / dispositioned / closed) and says nothing about
	// whether the decision was corroborated; `verification_results.state` is the
	// decision's outcome. Reading the wrong one is how a corroborated incident
	// gets reported as unverified in an audit.
	var incidentID, ruleID, lifecycle string
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT incident_id, rule_id, state FROM incidents WHERE tenant_id = $1`,
		h.tenant).Scan(&incidentID, &ruleID, &lifecycle); err != nil {
		t.Fatalf("read the incident: %v", err)
	}
	if ruleID != "test.motion-in-restricted-zone" {
		t.Errorf("the incident cites rule %q", ruleID)
	}
	if lifecycle != "open" {
		t.Errorf("a newly raised incident is in lifecycle state %q, want open", lifecycle)
	}

	var verification, reasonCode string
	var deterministic bool
	var supporting string
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT state, reason_code, deterministic, supporting_event_ids::text
		   FROM verification_results WHERE tenant_id = $1 AND incident_id = $2`,
		h.tenant, incidentID).Scan(&verification, &reasonCode, &deterministic, &supporting); err != nil {
		t.Fatalf("read the verification result: %v", err)
	}
	// The stored form is lowercase by design: the engine's constants are
	// uppercase and the incidents service lowercases them before writing, because
	// one canonical storage form beats two. Asserting the stored value means the
	// test follows the database rather than the Go constant, which is what a
	// replay reading these rows sees.
	if !strings.EqualFold(verification, string(rules.StateCorroborated)) {
		t.Errorf("the decision is %q (reason %q); two in-window signals in the same "+
			"restricted zone should have corroborated", verification, reasonCode)
	}
	// Replayed-from-stored-facts is invariant 2, and `deterministic` is the stored
	// claim. A decision that re-inferred anything would set it false.
	if !deterministic {
		t.Error("the verification is marked non-deterministic; the decision must replay " +
			"from stored facts")
	}
	if !strings.Contains(supporting, "evt") {
		t.Errorf("the verification cites no supporting events: %s", supporting)
	}

	// The incident cites the versions that explain it. An incident without them
	// is one nobody can re-evaluate.
	var version, hash string
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT rule_bundle_version, bundle_content_hash FROM incidents WHERE tenant_id = $1`,
		h.tenant).Scan(&version, &hash); err != nil {
		t.Fatalf("read the incident's bundle identity: %v", err)
	}
	if version != "0.1.0" || len(hash) != 64 {
		t.Errorf("the incident cites bundle %q hash %q; an incident nobody can re-evaluate "+
			"is an incident nobody can trust", version, hash)
	}
}

// A second poll of the same feed must produce no new rows. This is what makes
// the cursor an optimisation rather than a correctness mechanism - and the reason
// a broken cursor cannot lose an event is that the dedupe key, not the cursor, is
// what enforces it.
func TestPollingTwiceIngestsEachEventOnce(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()

	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)
	first := h.countCanonical(ctx, t)

	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)
	second := h.countCanonical(ctx, t)

	if first != 1 {
		t.Fatalf("the first pass produced %d canonical events, want 1", first)
	}
	if second != first {
		t.Errorf("the second pass changed the canonical event count from %d to %d; a "+
			"re-read of the same feed must not produce a second event", first, second)
	}
}

// A crash between the claim and the normalization is recovered by the lease. The
// row is left processing with an expired lease, and the next claim takes it and
// produces the same derived event id - a retry, not a duplicate and not a loss.
func TestALapsedLeaseIsRecoveredWithoutDuplicating(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()

	h.loops.PollOnce(ctx)

	// Simulate the crash: the row was claimed and the worker died.
	raw := testutil.Pool(t)
	testutil.Exec(t, raw, `
		UPDATE inbox_events
		   SET status = 'processing', lease_owner = 'dead-worker', lease_expires_at = now() - interval '1 minute'
		 WHERE tenant_id = $1`, h.tenant)

	if got := h.countCanonical(ctx, t); got != 0 {
		t.Fatalf("a canonical event exists before any consume pass (%d)", got)
	}

	h.loops.ConsumeOnce(ctx)

	if got := h.countCanonical(ctx, t); got != 1 {
		t.Errorf("after recovering the lapsed lease there are %d canonical events, want 1", got)
	}
	var processed int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1 AND status = 'processed'`,
		h.tenant).Scan(&processed); err != nil {
		t.Fatalf("count processed rows: %v", err)
	}
	if processed != 1 {
		t.Errorf("%d row(s) are processed after recovery, want 1", processed)
	}
}

// With no active bundle the core still ingests, still normalizes, and raises
// nothing. An operator who has not activated a bundle has not asked for
// decisions yet, and the alternative is a core that refuses events it cannot
// decide on - which is a loss of the events themselves.
func TestWithNoActiveBundleEventsAreStillIngestedAndNormalized(t *testing.T) {
	h := setup(t, "", `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()

	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)

	if got := h.countCanonical(ctx, t); got != 1 {
		t.Errorf("with no bundle there are %d canonical events, want 1; refusing to ingest "+
			"an event because no rule is active loses the event", got)
	}
	var incidents int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM incidents WHERE tenant_id = $1`, h.tenant).Scan(&incidents); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	if incidents != 0 {
		t.Errorf("%d incident(s) were raised with no active bundle", incidents)
	}
}

// A feed that is not deployed is not a fault, and must not be reported as one.
func TestAnUndeployedFeedDoesNotLookLikeAFault(t *testing.T) {
	// An empty body: every route 404s except the one the adapter asked for.
	h := setup(t, bundle, `[]`)
	ctx := context.Background()

	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)

	// No rows at all. A site with nothing deployed must not manufacture events,
	// and an adapter that turns "404 on every route" into a stream of malformed
	// records would fill the dead-letter queue on every poll of a site that has
	// simply not bought the integration.
	var rows int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1`, h.tenant).Scan(&rows); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("a site with no deployed feed produced %d inbox row(s)", rows)
	}
}

// A pass that cannot establish which site it is ingesting for must ingest NOTHING.
//
// The defect this pins: `PollOnce` used to resolve its scope inside `loadCursor`,
// and `loadCursor`'s failure was read as "no cursor is not fatal, it means a full
// re-read". Which is true of a cursor and was applied to a site binding. So with
// `Deps.SiteID` unset the pass logged the line, polled the adapter anyway, and
// wrote every record it got into `inbox_events` through a connection with no tenant
// context bound.
//
// Under RLS a write with no tenant context is refused by the database. Through the
// pool's owner role it is not, because the owner bypasses the policies -- and that
// is the role the tests and the daemon's own pool run as. So the refusal `Scoped`
// performs was the only thing between a misconfigured daemon and tenant-scoped rows
// written with nothing scoping them, and the one path that wrote rows was the one
// path that skipped it.
func TestAnIngestPassWithNoSiteIngestsNothing(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Cam [cam-1]"}
	]`)
	ctx := context.Background()

	// The harness's own site, so the only thing wrong is the binding.
	h.loops.Deps.SiteID = ""

	before := h.registry.Counters.Snapshot()[observability.CounterInboxAccepted]
	h.loops.PollOnce(ctx)

	var rows int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1`, h.tenant).Scan(&rows); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if rows != 0 {
		t.Errorf("a pass with no site id wrote %d inbox row(s). The pass must be refused "+
			"before the adapter is polled, not after the first query that happens to need "+
			"a connection", rows)
	}
	if after := h.registry.Counters.Snapshot()[observability.CounterInboxAccepted]; after != before {
		t.Errorf("the accepted counter moved from %d to %d on a refused pass, so the "+
			"refusal happened after the work rather than before it", before, after)
	}
}

// The other direction, because a pass that always refuses is not a pass. The
// harness sets a site, and this is what the wiring has to leave working.
func TestAnIngestPassWithASiteIngests(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Cam [cam-1]"}
	]`)
	ctx := context.Background()

	if h.loops.Deps.SiteID == "" {
		t.Fatal("the harness has no site, so this test would assert nothing about the " +
			"wired path")
	}
	h.loops.PollOnce(ctx)

	var rows int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1`, h.tenant).Scan(&rows); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if rows == 0 {
		t.Error("a pass with a site id ingested nothing; the scope is now resolved up front " +
			"and something upstream of the write is refusing")
	}
}

// The counter that was supposed to be wired. A metrics assertion that passes on
// an empty wiring proves nothing, so this one requires a non-zero reading after
// real work.
func TestTheIngestCounterMovesAfterARealPoll(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Cam [cam-1]"}
	]`)
	ctx := context.Background()

	if got := h.registry.Counters.Snapshot()[observability.CounterInboxAccepted]; got != 0 {
		t.Fatalf("the accepted counter starts at %d, want 0", got)
	}
	h.loops.PollOnce(ctx)

	if got := h.registry.Counters.Snapshot()[observability.CounterInboxAccepted]; got < 1 {
		t.Errorf("the accepted counter is %d after a poll that accepted an event; the "+
			"counter is not wired to the loop", got)
	}
}

// The gauges are sampled by their own loop, not at the end of a pass. The
// backlog gauge after the work is drained must read zero, and it must be a
// measured zero: a gauge that only updates when work arrives reports zero on a
// healthy idle site by never having been written, which an SLO threshold cannot
// tell apart from a real clear.
func TestBacklogGaugeIsMeasuredNotLeftAtZero(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Cam [cam-1]"}
	]`)
	ctx := context.Background()

	// A row that is waiting to be consumed. The gauge must report it, not zero.
	h.loops.PollOnce(ctx)
	h.loops.SampleGauges(ctx)

	if got := h.gauge(observability.GaugeBacklogDepth); got != 1 {
		t.Errorf("the backlog gauge reads %v with one event waiting to be consumed; a gauge "+
			"that reports zero while work is queued is the failure an SLO threshold cannot "+
			"detect", got)
	}

	// After the work drains, the same measurement reads zero - and it is a
	// measurement, because it was read again.
	h.loops.ConsumeOnce(ctx)
	h.loops.SampleGauges(ctx)

	if got := h.gauge(observability.GaugeBacklogDepth); got != 0 {
		t.Errorf("the backlog gauge reads %v after the event was consumed, want 0", got)
	}
}

// The two incident gauges are read, not assumed.
//
// `GaugeIncidentOpen` and `GaugeIncidentDegraded` were declared in metrics.go and
// written nowhere, and `ai_plane_availability` is an SLO that reads the second one —
// so the objective reported "not measured" on every install and never once had an
// opinion. A pilot could not measure how often the AI path was actually live, which
// is the single number the `degraded` column exists to provide.
func TestTheIncidentGaugesAreMeasuredNotLeftAtUnset(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Cam [cam-1]"}
	]`)
	ctx := context.Background()

	// Before anything has happened, the gauges are read and read zero. That is a
	// measurement, and it is distinguishable from "never written" — which is the
	// whole point, because a gauge that only updates when work arrives reports
	// zero on a healthy idle site by never having been set.
	h.loops.SampleGauges(ctx)
	if !h.registry.Gauges.Measured(observability.GaugeIncidentOpen) {
		t.Error("the open-incident gauge was never written, so a dashboard shows a flat line " +
			"that reads as \"this has never happened\"")
	}
	if !h.registry.Gauges.Measured(observability.GaugeIncidentDegraded) {
		t.Error("the degraded-incident gauge was never written, which leaves the AI-plane SLO " +
			"permanently unmeasured")
	}

	// The rule raises an incident, so the open count becomes one and the SLO has a
	// real input.
	h.loops.PollOnce(ctx)
	h.loops.ConsumeOnce(ctx)
	h.loops.SampleGauges(ctx)

	if got := h.gauge(observability.GaugeIncidentOpen); got < 1 {
		t.Errorf("the open-incident gauge reads %v with an incident raised; a rule that raises "+
			"nothing measurable is a rule whose effect cannot be told from a rule that raised "+
			"nothing", got)
	}
	// This bundle's `proceed_without` means the AI plane was never invoked, so the
	// degraded count is a measured zero and not a guess. The value is not asserted
	// here — the fixture is not a degraded one — only that it is now a reading.
	if !h.registry.Gauges.Measured(observability.GaugeIncidentDegraded) {
		t.Error("the degraded-incident gauge stopped being measured")
	}

	// Which means the AI-plane objective can now have an opinion at all.
	if state := h.registry.SLOs(); state.Evaluated == 0 {
		t.Fatal("the incident gauges are written and no objective was evaluated")
	}
	for _, s := range observability.Breaches(h.registry,
		observability.ReadingsFrom(h.registry)) {
		if s.Name == "ai_plane_availability" && s.Measured == "" {
			t.Error("the AI-plane objective reports no measurement at all")
		}
	}
}

func (h harness) gauge(name string) float64 {
	g := h.registry.Gauges.Snapshot()
	if v, ok := g[name]; ok {
		return v
	}
	return -1
}

// The eight objectives in slo.go are the plan's M1 gate 3, and for as long as
// `Breaches` existed it had no caller outside its own test: an objective only ever
// evaluated by a test is a description of an intention. This asserts the running
// loop evaluates them, and that a breach reaches an operator as a log line
// carrying its remedy — because `SLO` carries a `Remedy` on every entry precisely
// so that an alert is actionable, and a remedy nobody is shown is not one.
func TestTheRunningPipelineEvaluatesTheSLOsAndSaysSo(t *testing.T) {
	h := setup(t, bundle, "")
	ctx := context.Background()

	// One accepted event that reached no terminal state. The counters are set
	// directly rather than by polling, because the poll's own increments would be
	// mixed in and the objective under test is the arithmetic, not the ingest.
	//
	// The objective provoked is ingest_availability rather than the AI plane, and
	// that is deliberate: every gauge the sampler writes is now written from the
	// database, so a test cannot provoke one by setting the registry behind the
	// loop's back. The AI plane's input is covered by
	// TestTheIncidentGaugesAreMeasuredNotLeftAtUnset.
	h.registry.Counters.Inc(observability.CounterInboxAccepted)
	h.registry.Counters.Inc(observability.CounterInboxAccepted)
	h.registry.Counters.Inc(observability.CounterInboxDeadLettered)

	h.loops.SampleGauges(ctx)

	state := h.registry.SLOs()
	if state.Evaluated == 0 {
		t.Fatal("the pipeline sampled its gauges and evaluated no objectives; " +
			"Breaches still has no production caller")
	}
	var found bool
	for _, name := range state.Breached {
		if name == "ingest_availability" {
			found = true
		}
	}
	if !found {
		t.Errorf("one accepted event with no terminal state did not breach ingest_availability; "+
			"recorded %v. The objective is zero lost events, not a percentage", state.Breached)
	}

	logged := h.log.String()
	if !strings.Contains(logged, "an SLO objective is not being met") {
		t.Errorf("no SLO line reached the operator's log:\n%s", logged)
	}
	for _, want := range []string{"objective=ingest_availability", "remedy="} {
		if !strings.Contains(logged, want) {
			t.Errorf("the SLO log line has no %s; an alert with no remedy is one everybody "+
				"learns to ignore:\n%s", want, logged)
		}
	}
}

// A permanently breached objective must not become a log line on every poll for
// ever. The set is re-recorded to the registry on every evaluation — a reader sees
// the current verdict — while the log only speaks when the verdict changes.
func TestAnUnchangedBreachIsNotLoggedOnEveryPass(t *testing.T) {
	h := setup(t, bundle, "")
	ctx := context.Background()
	h.registry.Counters.Inc(observability.CounterInboxAccepted)
	h.registry.Counters.Inc(observability.CounterInboxAccepted)
	h.registry.Counters.Inc(observability.CounterInboxDeadLettered)

	h.loops.SampleGauges(ctx)
	first := strings.Count(h.log.String(), "an SLO objective is not being met")
	breached := h.registry.SLOs().Breached
	if first != len(breached) {
		t.Fatalf("the first evaluation logged %d breach lines for %d breached objectives %v",
			first, len(breached), breached)
	}
	if len(breached) == 0 {
		t.Fatal("nothing breached, so this test is asserting that a quiet site stays quiet")
	}
	for range 3 {
		h.loops.SampleGauges(ctx)
	}
	if got := strings.Count(h.log.String(), "an SLO objective is not being met"); got != first {
		t.Errorf("four identical evaluations logged %d breach lines, want %d; a gauge sampler "+
			"turning one breach into a log line per interval is how an operator learns to ignore it",
			got, first)
	}
	// And the verdict is still current on the read side, which is what makes the
	// quiet log safe.
	if state := h.registry.SLOs(); len(state.Breached) != len(breached) {
		t.Errorf("the recorded verdict went stale while the log was quiet: %v, want %v",
			state.Breached, breached)
	}
}

func (h harness) countCanonical(ctx context.Context, t *testing.T) int {
	t.Helper()
	var n int
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, h.tenant).Scan(&n); err != nil {
		t.Fatalf("count canonical events: %v", err)
	}
	return n
}
