package main

// Ingest: the committed fixtures, replayed through the real adapter and the real
// pipeline.
//
// # Nothing here is a second path
//
// The adapter is `milestone.New(transport, …)` over `milestone.ReplayTransport`,
// which is the same adapter the product polls a live VMS with and the same
// transport `securityctl rules simulate` replays fixtures through. The loops are
// `pipeline.Loops` built with the same `Deps` fields `cmd/security-core` fills in.
// `PollOnce` and `ConsumeOnce` are the production entry points, exported
// precisely so a caller can drive a pass without a ticker.
//
// So the incidents this step raises were raised by the product. The seed's only
// contribution is the fixed clock, which is what makes the run repeatable.
//
// # Drain, do not wait
//
// The consume loop is a lease-and-claim over `ORDER BY received_at`, so draining
// means claiming and processing until a claim comes back empty. Waiting for a
// background loop instead would make the seed's duration a function of a
// tick interval, and the acceptance report that quotes it would then be quoting
// the interval rather than the work.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sergey-Bar/vision/internal/bundles"
	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/rules"
)

// drainBounded is how many consume passes the seed will make before it stops.
//
// Bounded rather than unbounded because "until nothing is left" and "until the
// clock runs out" are the same loop when one stage keeps re-queuing work. The
// bound is generous next to the fixture set — a dozen scenarios and a few dozen
// events — so reaching it means something is wrong and the seed says so rather
// than reporting a partial site as a complete one.
const drainBounded = 64

// activateBundle stores the bundle and activates it through the activation gate.
func (s *seeder) activateBundle(ctx context.Context) error {
	content, err := readBundle(s.opts.bundlePath)
	if err != nil {
		return err
	}
	// The site's own compiler: the one that knows this site's zones, so "this
	// rule refers to a zone that does not exist" is a finding here rather than a
	// bundle that loads and then never matches.
	compiler := rules.NewCompiler()
	svc := &bundles.Service{Pool: s.pool, Compiler: compiler, Audit: s.auditWriter(), Metrics: newRegistry()}

	meta, problems, err := svc.Store(ctx, s.opts.tenant, content, "seed", "security-seed", "")
	if err != nil {
		return fmt.Errorf("store the rule bundle: %w", err)
	}
	// A WARNING is not a failure and the difference is load-bearing: the compiler
	// warns for things a rule author has DECIDED — a `proceed_without` fallback,
	// an `ifAIAvailable` clause with nothing to apply it to — and the shipped
	// bundle contains both on purpose. `internal/pipeline` treats any problem as
	// fatal here and was wrong to; this one reads severity.
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			return fmt.Errorf("the rule bundle has an error-level problem and was not "+
				"stored: %s: %s", p.Code, p.Message)
		}
		s.say("bundle %s stored; %s %s: %s", meta.Version, p.Severity, p.Code, p.Message)
	}
	s.report.BundleVersion = meta.Version
	s.report.BundleHash = meta.ContentHash

	if err := svc.Activate(ctx, s.opts.tenant, meta.Version, simulateReportFor(meta.Version), "security-seed"); err != nil {
		return fmt.Errorf("activate the rule bundle: %w", err)
	}
	s.say("bundle %s activated through the simulate-digest gate, not by writing "+
		"active=true behind the service", meta.Version)
	return nil
}

// readBundle accepts a file or a directory, because `securityctl rules lint`
// accepts both and a seed that accepted only one would be a trap.
func readBundle(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read the rule bundle at %s: %w", path, err)
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read the rule bundle at %s: %w", path, err)
		}
		return b, nil
	}
	entries, err := filepath.Glob(filepath.Join(path, "*.yaml"))
	if err != nil || len(entries) == 0 {
		return nil, errf("security-seed: %s is a directory with no .yaml bundle in it", path)
	}
	if len(entries) > 1 {
		return nil, errf("security-seed: %s holds %d bundles; name the one to activate with --bundle",
			path, len(entries))
	}
	b, err := os.ReadFile(entries[0])
	if err != nil {
		return nil, fmt.Errorf("read the rule bundle at %s: %w", entries[0], err)
	}
	return b, nil
}

// drive replays the fixtures and drains the pipeline.
func (s *seeder) drive(ctx context.Context) error {
	scenarios, err := loadScenarios(s.opts.fixturesDir)
	if err != nil {
		return err
	}
	total := 0
	for _, sc := range scenarios {
		total += len(sc.Events)
	}
	s.say("%d scenario(s), %d envelope(s), rebased onto %s", len(scenarios), total,
		s.epoch.Format(time.RFC3339))

	transport := milestone.NewReplayTransport(scenarios).
		WithEpoch(s.epoch).
		// The site id is the SEEDED site's, not the fixture's. `ReplayTransport`
		// exists so one captured scenario replays against a site a test set up
		// rather than the site it was captured on, and a seed that left the
		// fixture's id in place would write events scoped to a site that does not
		// exist — which the RLS policies refuse, for the right reason.
		WithSiteID(s.opts.site)
	adapter := milestone.New(transport, s.opts.site, s.opts.integration).
		// The adapter's clock is injected rather than taken from the host, because
		// `received_at` is what the inbox claims rows `ORDER BY received_at` and a
		// wall clock would reorder the fixtures between two runs of the same seed.
		WithClock(func() time.Time { return s.epoch })
	defer func() { _ = transport.Close() }()

	loops := s.serviceGraph(adapter, &bundles.Service{
		Pool: s.pool, Compiler: rules.NewCompiler(), Audit: s.auditWriter(),
	})

	// One poll, then drain. The poll writes every fixture envelope to the inbox;
	// the consume passes take them in `received_at` order and run the real
	// normalize -> graph -> correlate -> evaluate -> incident path over each.
	loops.PollOnce(ctx)

	accepted := 0
	for pass := 1; pass <= drainBounded; pass++ {
		before, err := s.pendingInbox(ctx)
		if err != nil {
			return err
		}
		if before == 0 {
			s.say("inbox drained after %d consume pass(es)", pass-1)
			break
		}
		loops.ConsumeOnce(ctx)
		after, err := s.pendingInbox(ctx)
		if err != nil {
			return err
		}
		accepted += before - after
		if pass == drainBounded {
			return errf("security-seed: %d accepted event(s) are still in the inbox after %d "+
				"consume passes. The seed will not report a partial site as a complete one; "+
				"the fixture set is a dozen scenarios, so this is a defect rather than a load",
				after, drainBounded)
		}
	}

	// Every accepted event must have reached a TERMINAL state. This is invariant
	// 1 and the seed asserts it rather than reporting it, because a seed that
	// leaves a backlog behind produces a console whose absence panel has something
	// to say and no explanation for why.
	var received, processed, deadLettered int
	if err := s.scoped(ctx).QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'received'),
		       count(*) FILTER (WHERE status = 'processed'),
		       count(*) FILTER (WHERE status = 'dead_letter')
		  FROM inbox_events WHERE tenant_id = $1`, s.opts.tenant).
		Scan(&received, &processed, &deadLettered); err != nil {
		return fmt.Errorf("read the inbox partition: %w", err)
	}
	if received != 0 {
		return errf("security-seed: %d event(s) are still in 'received' after the drain. An "+
			"event that reaches inbox_events must reach a terminal state unless it is "+
			"explicitly dead-lettered, and the seed is not going to print a site that "+
			"violates that", received)
	}
	s.say("inbox: %d processed, %d dead-lettered, 0 left in 'received'", processed, deadLettered)
	s.report.Inbox = InboxCounts{Processed: processed, DeadLettered: deadLettered}
	_ = accepted
	return nil
}

// pendingInbox counts the rows a claim would take.
func (s *seeder) pendingInbox(ctx context.Context) (int, error) {
	var n int
	err := s.scoped(ctx).QueryRow(ctx, `
		SELECT count(*) FROM inbox_events
		 WHERE tenant_id = $1
		   AND (status = 'received'
		     OR (status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()))`,
		s.opts.tenant).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count the pending inbox rows: %w", err)
	}
	return n, nil
}

// loadScenarios reads a fixture set and verifies its manifest.
//
// The manifest verification is not ceremony. A fixture's identity is its content
// hash, and a seed that replayed a fixture set whose envelopes had been edited
// would be replaying a scenario whose recorded hash no longer describes it —
// which is the defect `fixture.Manifest` exists to catch.
func loadScenarios(dir string) ([]fixture.Scenario, error) {
	store, err := fixture.NewStore(dir)
	if err != nil {
		return nil, errf("security-seed: read the fixture set at %s: %w", dir, err)
	}
	if _, err := store.VerifyManifest(); err != nil {
		return nil, errf("security-seed: the fixture set at %s does not match its own "+
			"manifest: %w", dir, err)
	}
	scenarios, err := store.All()
	if err != nil {
		return nil, errf("security-seed: read the fixture set at %s: %w", dir, err)
	}
	if len(scenarios) == 0 {
		return nil, errf("security-seed: the fixture set at %s is empty; there is nothing to "+
			"replay and a site with no events demonstrates nothing", dir)
	}
	return scenarios, nil
}
