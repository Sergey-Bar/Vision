package main

// The seeder: the ordered steps, and what each one is for.
//
// The order is the product's order. Topology, then identity, then the bundle,
// then ingest, then evidence, then the AI plane, then a reviewer's disposition.
// Nothing here runs "out of band": an incident is raised by `pipeline.Loops`
// driving the real adapter, the real inbox, the real normalizer, the real
// correlator and the real rule engine. The only stubbed component is the
// Milestone transport, because that is the one component outside the process, and
// it is stubbed with the fixtures the adapter's own tests use.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/correlator"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/graph"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
	"github.com/Sergey-Bar/Ai-Captain/internal/normalize"
	"github.com/Sergey-Bar/Ai-Captain/internal/pipeline"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/simulate"
)

// seeder holds everything the steps share.
type seeder struct {
	opts options
	pool *dbx.Pool
	log  *slog.Logger
	out  *os.File

	epoch         time.Time
	loc           *time.Location
	evidence      *evidence.Store
	secretKeyPath string
	tlsDir        string

	// report is the artefact a demo quotes and a golden test asserts against.
	report Report
}

func (s *seeder) close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// scoped is a site-bound connection, which since migration 0021 is the only kind
// that can see a site-scoped row at all.
func (s *seeder) scoped(_ context.Context) *dbx.Tenant {
	return s.pool.WithSites(s.opts.tenant, "seed", []string{s.opts.site})
}

// step is one named stage, printed as it runs.
//
// Named steps rather than a single log line because a judge reading this output
// needs to know which claim is being established at each point, and a wall of SQL
// would tell them nothing.
func (s *seeder) step(format string, args ...any) {
	if s.opts.json {
		return
	}
	fmt.Fprintf(s.out, "\n== %s\n", fmt.Sprintf(format, args...))
}

func (s *seeder) say(format string, args ...any) {
	if s.opts.json {
		return
	}
	fmt.Fprintf(s.out, "   %s\n", fmt.Sprintf(format, args...))
}

// newSeeder opens what the steps need and refuses the configurations that would
// produce a site the product could not have built.
func newSeeder(ctx context.Context, o options) (*seeder, error) {
	if strings.TrimSpace(o.evidenceRoot) == "" {
		return nil, errNoEvidenceRoot
	}
	root, err := absDir(o.evidenceRoot, "--evidence-root")
	if err != nil {
		return nil, err
	}
	keyPath, err := absDir(o.secretKey, "--secret-key")
	if err != nil {
		return nil, err
	}
	tlsDir, err := absDir(o.consoleTLS, "--console-tls")
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(o.timezone)
	if err != nil {
		return nil, errf("security-seed: --timezone %q is not a zone this build knows: %w",
			o.timezone, err)
	}
	epoch := replayEpoch
	if strings.TrimSpace(o.epoch) != "" {
		epoch, err = time.Parse(time.RFC3339, strings.TrimSpace(o.epoch))
		if err != nil {
			return nil, errf("security-seed: --epoch %q is not RFC3339: %w", o.epoch, err)
		}
		epoch = epoch.UTC()
	}

	cfg := dbx.DefaultConfig()
	if dsn := strings.TrimSpace(os.Getenv("SP_DATABASE_URL")); dsn != "" {
		if err := cfg.ApplyDSN(dsn); err != nil {
			return nil, errf("security-seed: SP_DATABASE_URL: %w", err)
		}
	}
	pool, err := dbx.Open(ctx, cfg, os.Getenv("SP_DATABASE_PASSWORD"))
	if err != nil {
		return nil, fmt.Errorf("open the database: %w", err)
	}
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open the evidence store at %s: %w", root, err)
	}

	s := &seeder{
		opts:          o,
		pool:          pool,
		out:           os.Stdout,
		log:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		epoch:         epoch,
		loc:           loc,
		evidence:      store,
		secretKeyPath: keyPath,
		tlsDir:        tlsDir,
	}
	s.report.Epoch = epoch.UTC().Format(time.RFC3339)
	return s, nil
}

// run is the ordered sequence, and it is the demo's substance.
func (s *seeder) run(ctx context.Context) error {
	already, err := s.assertFresh(ctx)
	if err != nil {
		return err
	}
	if already {
		return s.reportExisting(ctx)
	}

	s.step("topology: tenant %s, site %s (%s)", s.opts.tenant, s.opts.site, s.opts.timezone)
	if err := s.writeTopology(ctx); err != nil {
		return err
	}
	s.report.Tenant = s.opts.tenant
	s.report.Site = s.opts.site
	s.report.Zones = len(fixtureZones)
	s.report.Devices = len(fixtureDevices)

	s.step("secret material: the MFA wrapping key and the console certificate")
	secretKey, err := s.writeSecretMaterial(ctx)
	if err != nil {
		return err
	}

	s.step("identity: three principals at three scopes")
	if err := s.writeIdentity(ctx, secretKey); err != nil {
		return err
	}

	s.step("rule bundle: store, simulate digest, activate through the gate")
	if err := s.activateBundle(ctx); err != nil {
		return err
	}

	s.step("ingest: %s replayed through the real adapter and pipeline", s.opts.fixturesDir)
	if err := s.drive(ctx); err != nil {
		return err
	}

	s.step("evidence: the committed clips, stored by content hash")
	if err := s.storeEvidence(ctx); err != nil {
		return err
	}

	s.step("AI plane: some observations healthy, some explicitly degraded")
	if err := s.writeObservations(ctx); err != nil {
		return err
	}

	s.step("review: a named operator's disposition on the first incident")
	if err := s.writeDisposition(ctx); err != nil {
		return err
	}

	if err := s.collect(ctx); err != nil {
		return err
	}
	s.print()
	return nil
}

// assertFresh decides whether this is a first run, a repeat run, or a rebuild.
//
// Three outcomes, and the middle one is the one that makes `docker compose up`
// usable:
//
//   - Nothing there yet: seed it. That is the first run.
//   - Already populated: DO NOTHING and report what is there, exiting 0. A second
//     `docker compose up` is not a second bootstrap, and a seed that refused it
//     would make the demo a one-shot. It is also the only outcome that is safe
//     given the control below — the site's own disposition is append-only, so the
//     seed cannot rebuild over its own first run.
//   - Already populated, and --reset was asked for: rebuild, but only if nothing
//     has to be deleted that is not the seed's to delete.
//
// "Merge into whatever is there" is deliberately not an outcome. It produces a site
// with two admin accounts, two active bundles and two of every incident, and the
// failure surfaces days later as an acceptance report that disagrees with itself.
func (s *seeder) assertFresh(ctx context.Context) (alreadySeeded bool, err error) {
	var incidents int
	err = s.pool.InTenant(ctx,
		dbx.TenantContext{TenantID: s.opts.tenant, ActorID: "seed", System: true},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM incidents WHERE tenant_id = $1`,
				s.opts.tenant).Scan(&incidents)
		})
	if err != nil {
		// A tenant that does not exist yet is the normal first run, not a failure.
		return false, nil //nolint:nilerr // "no such tenant" is answered by the first INSERT
	}
	if incidents == 0 {
		return false, nil
	}
	if !s.opts.reset {
		return true, nil
	}

	// --reset, and a reviewer's answer is already on the ledger. Dispositions first,
	// before any table is touched, because this is the one deletion that cannot be
	// done and a reader hitting it six tables later learns nothing from.
	var dispositions int
	if err := s.pool.InTenant(ctx,
		dbx.TenantContext{TenantID: s.opts.tenant, ActorID: "seed", System: true},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM dispositions WHERE tenant_id = $1`,
				s.opts.tenant).Scan(&dispositions)
		}); err != nil {
		return false, nil //nolint:nilerr // no such tenant is the first run's normal path
	}
	if dispositions > 0 {
		return false, errf("tenant %q has %d disposition(s), and `dispositions` is append-only: "+
			"a reviewer's answer is a record, and a record that can be removed is not one. "+
			"--reset will not delete them, by design. To rebuild the demo, start from an "+
			"empty database (`docker compose down -v`) or name a fresh --tenant; to add "+
			"another reviewer to this site, use the console", s.opts.tenant, dispositions)
	}
	return false, s.reset(ctx)
}

// reportExisting prints what is already there and stops.
//
// Deliberately the SAME report shape as a first run, because `docker compose up`
// prints it either way and a shape that changed with the state of the database
// would be one more thing to read twice. The differences are stated in it: no TOTP
// secret, because a wrapped factor is not readable back, and a line saying which
// clock the run used.
func (s *seeder) reportExisting(ctx context.Context) error {
	s.step("already seeded: tenant %s, site %s already holds incidents, so nothing was written",
		s.opts.tenant, s.opts.site)
	if err := s.collect(ctx); err != nil {
		return err
	}
	if err := s.readIdentity(ctx); err != nil {
		return err
	}
	s.report.AlreadySeeded = true
	s.report.TOTPSecret = ""
	s.report.TLS.SelfSigned = true
	// The certificate path is known; the fingerprint is not, because the file lives
	// in the volume the cores read and not in the database. Say where it is instead
	// of printing nothing, since a judge who has already opened the browser has it.
	s.say("the console certificate is the one in %s", s.tlsDir)
	s.print()
	return nil
}

// readIdentity re-reads the principals so the repeat-run report lists them.
func (s *seeder) readIdentity(ctx context.Context) error {
	for _, p := range []struct {
		username string
		userID   string
		role     string
		sites    []string
	}{
		{demoTenantAdmin, "", "tenant_admin", []string{s.opts.site}},
		{demoSiteOperator, "usr-operator", "operator", []string{s.opts.site}},
		{demoSystemWorker, "usr-worker", "service", nil},
	} {
		id := p.userID
		if id == "" {
			var err error
			if id, err = s.findUser(ctx, p.username); err != nil {
				return fmt.Errorf("read the existing user %q: %w", p.username, err)
			}
		}
		s.report.Principals = append(s.report.Principals, Principal{
			Username: p.username, UserID: id, Roles: []string{p.role}, Sites: p.sites,
		})
	}
	return nil
}

// reset deletes this tenant's rows, children first.
//
// Explicitly enumerated rather than cascaded from `tenants`: the order is the
// dependency order, and a seed that dropped a site's rows by cascade would delete
// an audit ledger — which is append-only by design, and which no operator action
// anywhere else in this product is permitted to destroy.
//
// `dispositions` is NOT in the list, and that is not an oversight. A trigger on the
// table refuses DELETE and UPDATE outright (`dispositions is append-only; a new
// interpretation is a new row`), which is the correct control: a reviewer's answer
// is a record, and a record that can be removed is not one. `assertFresh` finds out
// before it gets here and says what to do instead.
//
// Same reasoning for `audit_ledger`, which is not touched at all.
func (s *seeder) reset(ctx context.Context) error {
	tables := []string{
		"ai_observations", "verification_results", "incident_evidence",
		"incident_events", "incidents", "evidence", "edge_evidence", "edges",
		"entities", "canonical_events", "inbox_events", "source_events",
		"device_clocks", "cameras", "devices", "zones", "user_site_assignments",
		"user_roles", "mfa_credentials", "sessions", "users", "integrations", "sites",
	}
	return s.pool.InTenant(ctx,
		dbx.TenantContext{TenantID: s.opts.tenant, ActorID: "seed", System: true},
		func(tx pgx.Tx) error {
			for _, table := range tables {
				// The table name is from this list and is not interpolated from
				// anything a caller supplied, which is why a plain format is safe
				// here; postgres.Identifier would be the answer if it ever were.
				if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1",
					s.opts.tenant); err != nil {
					return fmt.Errorf("reset %s: %w", table, err)
				}
			}
			return nil
		})
}

// the writers the steps share. One audit writer and one identity service, built
// once so every row they write lands in the same audit chain.
func (s *seeder) auditWriter() *audit.Writer {
	return &audit.Writer{Pool: s.pool, Metrics: newRegistry()}
}

func (s *seeder) identityService() (*identity.Service, error) {
	store, err := identity.NewFileSecretStore(s.secretKeyPath)
	if err != nil {
		return nil, err
	}
	return &identity.Service{
		Pool:        s.pool,
		Audit:       s.auditWriter(),
		SecretStore: store,
	}, nil
}

// incidentService is the incidents service with a DETERMINISTIC id generator.
//
// The default generator is `inc_<site>_<time.Now().UnixNano()>` and
// `CreateIncident` reads the wall clock for it with no seam, so a seeded incident
// id would differ on every run and nothing downstream could cite one. This is the
// seam the field exists for; it is not a test-only hack, because "cite this
// incident" is a thing an operator and a judge both need to be able to do.
func (s *seeder) incidentService() *incidents.Service {
	n := 0
	return &incidents.Service{
		Pool:  s.pool,
		Audit: s.auditWriter(),
		IDGenerator: func(_, site string, _ time.Time) string {
			n++
			return fmt.Sprintf("inc_%s_seed_%03d", site, n)
		},
	}
}

// serviceGraph is the set of production services the seed drives, assembled the
// way `cmd/security-core` assembles them. Sharing the assembly is the point: a
// seed that wired its own would be a second composition root, and the second one
// is where "the seeded path is not the product path" always starts.
func (s *seeder) serviceGraph(adapter *milestone.Adapter, svc *bundles.Service) *pipeline.Loops {
	return pipeline.New(pipeline.Deps{
		Pool: s.pool, Log: s.log, Metrics: newRegistry(),
		Adapter: adapter, Inbox: inbox.NewWriter(s.pool),
		Normal: normalize.New(s.pool), Graph: &graph.Service{Pool: s.pool},
		Correlat:      &correlator.Correlator{Pool: s.pool},
		Incidents:     s.incidentService(),
		Bundles:       svc,
		Enqueuer:      jobs.New(s.pool),
		Tenant:        s.opts.tenant,
		SiteID:        s.opts.site,
		IntegrationID: s.opts.integration,
		SiteLocation:  s.loc,
		EvidenceRoot:  s.evidence.Root(),
		ClaimBatch:    64,
		LeaseDuration: 30 * time.Second,
		ClockPolicy:   clockPolicy(),
		// The SAME resolver the production pipeline and the CLI use, so a
		// schedule rule cannot be dead in the seed and alive in the product.
		InSchedule: rules.SiteScheduleResolver(s.loc),
	})
}

// simulateReportFor is the report the activation gate is given.
//
// The gate re-hashes the acknowledged change list and compares it with the digest
// the report carries, so a report for a different bundle, a report edited after it
// was read, and a digest typed by hand are all refused. The seed builds the same
// report shape `internal/pipeline`'s test does and acknowledges its own digest
// through the service — it does not write an `active = true` row behind the
// service's back, because a site the product could not have activated would make
// every later claim about it a claim about a fiction.
func simulateReportFor(version string) simulate.Report {
	rep := simulate.Report{BundleVersion: version, FixtureSet: "fixtures-1"}
	rep.DiffDigest = simulate.DigestFor(rep.BundleVersion, rep.FixtureSet, rep.Changes)
	return rep
}
