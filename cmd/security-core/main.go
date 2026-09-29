// Command security-core is the runnable core service.
//
// It is the first binary in this repository that runs a loop. Everything else is
// a library with a test and a caller in this file, which is the point: the
// pipeline's order and its lifecycle used to be documented and unenforced, and a
// document does not stop a shutdown from leaving the listener open.
//
// # Startup, and what it refuses
//
// Configuration is read once and validated. A refusal exits non-zero with the
// offending variable named and the consequence explained, because a unit that
// restarts in a loop printing "invalid configuration" is an outage nobody can
// diagnose at three in the morning.
//
// # Shutdown, and its order
//
// The order is the whole design and it is enforced here rather than hoped for:
//
//  1. Stop accepting connections. Nothing new arrives.
//  2. Stop the loops. In-flight work finishes against a pool that is still open,
//     so a leased inbox row is either completed or left to expire - never
//     half-written against a closing pool.
//  3. Drain the outbox, so the side effects a decision produced are delivered
//     before the process goes away. The decisions themselves are already durable;
//     the outbox is the delivery half of that.
//  4. Close the pool.
//
// Doing 4 before 2 would fail in-flight normalization with a closed pool and
// turn a clean stop into a retry storm. Doing 1 after 2 would leave a window
// where requests are accepted by a process whose loops have already stopped.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/api"
	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/bundles"
	"github.com/Sergey-Bar/Ai-Captain/internal/checkpoint"
	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/clocksub"
	"github.com/Sergey-Bar/Ai-Captain/internal/config"
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
	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/pipeline"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet, so this goes to stderr directly. A
		// refusal about configuration is the one thing that must be readable even
		// when the logging setup is the thing that failed.
		fmt.Fprintf(os.Stderr, "security-core: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	start := time.Now()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log, err := newLogger(cfg.LogLevel)
	if err != nil {
		return err
	}
	log.Info("starting security-core", flatten(cfg.Redacted())...)

	pool, err := dbx.Open(context.Background(), cfg.Database, os.Getenv("SP_DATABASE_PASSWORD"))
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer func() {
		// The last close, after everything else has stopped. See the package
		// comment: closing this early turns a clean stop into a retry storm.
		pool.Close()
		log.Info("the database pool is closed")
	}()

	// Signals are trapped before anything long-running starts, so a signal that
	// arrives during construction is not lost and the process does not die with a
	// half-built graph.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := readiness(ctx, pool, log); err != nil {
		return err
	}

	registry := observability.NewRegistry()
	writer := &audit.Writer{Pool: pool}

	store, err := evidence.New(evidence.Options{Root: cfg.EvidenceRoot})
	if err != nil {
		return fmt.Errorf("open the evidence store: %w", err)
	}

	transport, err := milestone.NewHTTPTransport(
		milestoneBaseURL(), milestoneToken(), 15*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = transport.Close() }()

	adapter := milestone.New(transport, cfg.SiteID, cfg.IntegrationID)

	secretStore, err := identity.NewFileSecretStore(secretKeyPath())
	if err != nil {
		return err
	}
	ident := &identity.Service{Pool: pool, Audit: writer, SecretStore: secretStore}

	correlatorSvc := &correlator.Correlator{Pool: pool}
	incidentsSvc := &incidents.Service{Pool: pool, Audit: writer}
	bundlesSvc := &bundles.Service{Pool: pool, Compiler: rules.NewCompiler(), Audit: writer}
	inboxSvc := inbox.NewWriter(pool)
	jobWorker := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, Name: jobs.QueueVLM}

	aiClient, err := airuntime.New(cfg.AIEndpoint, cfg.AIDeadline, aiAllowsRemote())
	if err != nil {
		return err
	}
	vlm := airuntime.NewVLMWorker(jobWorker, aiClient,
		&observations.Writer{Pool: pool}, store, registry, log)

	relay := &outbox.Relay{
		Pool: pool,
		Handler: func(_ context.Context, d outbox.Delivery) error {
			// M1 dispatches no side effects. Acknowledging and logging is the honest
			// behaviour; a topic with no consumer that silently disappears is a
			// dead-letter backlog nobody is watching.
			log.Info("outbox message acknowledged",
				"tenant_id", d.TenantID, "topic", d.Topic, "outbox_id", d.OutboxID)
			return nil
		},
		MaxAttempts: 5,
	}

	server := &api.Server{
		Identity: ident, Audit: writer, Log: log,
		Tenant: cfg.Tenant,
		// Secure is configuration, not a constant. It was hardcoded true, which
		// marked the cookie Secure over a plaintext listener instead of letting
		// config refuse the pairing. config refuses Secure=false on anything but a
		// loopback listener, so this cannot silently hand a session to the network.
		Secure:         cfg.SessionCookieSecure,
		Pool:           pool,
		HealthCheck:    readinessChecks(aiClient, store),
		BehindProxy:    cfg.BehindProxy,
		TrustedProxies: cfg.TrustedProxyCIDRs,
	}
	server.SetStartTime(start)

	deps := api.Deps{
		Identity:    ident,
		Incidents:   incidentsSvc,
		Bundles:     bundlesSvc,
		Evidence:    store,
		DeadLetters: api.DeadLetters{Inbox: inboxSvc, Jobs: jobWorker},
		ClockDrift: api.ClockDrift{
			Service: &clocksub.Service{
				Pool:      pool,
				Reference: clockPolicy().Reference,
				Now:       func() time.Time { return time.Now().UTC() },
			},
			Policy: clockPolicy(),
		},
		Metrics: registry,
		InboxCounts: func() (api.Backlog, error) {
			counts, err := api.BacklogReader{
				Inbox:  inboxSvc,
				Jobs:   jobWorker,
				Outbox: relay.Pending,
			}.Count(context.Background(), cfg.Tenant)
			return api.Backlog{
				InboxPending:    counts.InboxPending,
				InboxDeadLetter: counts.InboxDeadLetter,
				JobPending:      counts.JobPending,
				JobDeadLetter:   counts.JobDeadLetter,
				OutboxPending:   counts.OutboxPending,
			}, err
		},
	}
	server.Routes = func(mux *http.ServeMux) { server.RegisterRoutes(deps)(mux) }

	// Checkpoint sealing starts only when a directory and a signing key are both
	// configured, and its absence is stated rather than being a silent no-op.
	if cfg.CheckpointDir != "" {
		scheduler, err := newSealer(cfg, pool, registry, log)
		if err != nil {
			log.Warn("the audit ledger will not be sealed", "error", err.Error())
		} else {
			go func() {
				if err := scheduler.Run(ctx); err != nil && ctx.Err() == nil {
					log.Error("the checkpoint sealer stopped", "error", err.Error())
				}
			}()
		}
	} else {
		log.Warn("no checkpoint directory is configured; the audit ledger is not being sealed")
	}

	loops := pipeline.Start(ctx, pipeline.Deps{
		Pool: pool, Log: log, Metrics: registry,
		Adapter: adapter, Inbox: inboxSvc, Normal: normalize.New(pool),
		Graph: &graph.Service{Pool: pool}, Correlat: correlatorSvc,
		Incidents: incidentsSvc, Bundles: bundlesSvc, Enqueuer: jobs.New(pool),
		Tenant: cfg.Tenant, SiteID: cfg.SiteID, IntegrationID: cfg.IntegrationID,
		PollInterval:  cfg.PollInterval,
		ClaimBatch:    cfg.ClaimBatchSize,
		LeaseDuration: cfg.LeaseDuration,
		Jobs:          jobWorker,
		AIEndpoint:    cfg.AIEndpoint,
		// The relay runs in the pipeline, not here, so the whole process has one
		// place that decides what runs and in what order at shutdown.
		Relay:       relay,
		ClockPolicy: clockPolicy(),
	})

	go runVLMWorker(ctx, jobWorker, vlm.Handle, cfg.Tenant, log)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe(ctx, cfg.Listen, cfg.TLSCertFile, cfg.TLSKeyFile) }()

	select {
	case err := <-serveErr:
		stop()
		loops.Stop()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	// 1. Stop accepting.
	log.Info("shutting down: stopping the listener",
		"grace", cfg.ShutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		// A failed drain is not a reason to skip the rest of the order. Closing the
		// pool while the loops still hold leases is worse than an in-flight request
		// that did not finish.
		log.Error("the listener did not drain cleanly", "error", err.Error())
	}

	// 2. Stop the loops, with the pool still open.
	log.Info("shutting down: stopping the pipeline")
	loops.Stop()

	// 3. Drain the outbox once more. Whatever the loops enqueued in their last
	// pass is delivered while there is still a pool to deliver it with.
	if n, err := relay.DrainOnce(shutdownCtx, cfg.Tenant, "shutdown", 200); err != nil {
		log.Warn("the final outbox drain did not complete", "error", err.Error())
	} else if n > 0 {
		log.Info("final outbox drain delivered", "messages", n)
	}

	// 4. The pool closes in the deferred call above.
	log.Info("shutdown complete")
	return nil
}

// readiness refuses to start against a database that is not there, or is not the
// schema this binary understands.
//
// A core that starts against an older schema and then fails every request is
// worse than one that does not start: systemd will show the service as running
// while it is refusing work.
func readiness(ctx context.Context, pool *dbx.Pool, log *slog.Logger) error {
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("the database is not reachable: %w", err)
	}
	var applied int
	if err := pool.QueryRow(pingCtx,
		`SELECT count(*) FROM schema_migrations WHERE checksum IS NOT NULL`).Scan(&applied); err != nil {
		return fmt.Errorf("read the applied migrations: %w", err)
	}
	if applied == 0 {
		return errors.New("the database has no migrations applied; run security-migrate first. " +
			"A core that starts against an empty schema answers /v1/health and refuses every " +
			"request, which to a monitor is indistinguishable from a healthy service")
	}
	log.Info("the schema is present", "migrations", applied)
	return nil
}

// readinessChecks is the health set.
//
// A check named "aiRuntime" is informational and never makes the core unready,
// because a site with no model is a supported configuration and taking the
// console down with it would make the console unavailable exactly when an
// operator needs to read about why.
func readinessChecks(ai *airuntime.Client, store *evidence.Store) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"evidenceRoot": func(_ context.Context) error {
			if store == nil || store.Root() == "" {
				return errors.New("no evidence root is configured")
			}
			return nil
		},
		"aiRuntime": func(_ context.Context) error {
			if ai == nil || ai.BaseURL == "" {
				return errors.New("no AI runtime is configured; every observation will be " +
					"recorded as degraded with reason no_runtime, which is the correct " +
					"behaviour for a site running without a model")
			}
			return nil
		},
	}
}

// newSealer builds the checkpoint scheduler from configuration.
func newSealer(cfg config.Config, pool *dbx.Pool, registry *observability.Registry,
	log *slog.Logger) (*checkpoint.Scheduler, error) {
	keyID := strings.TrimSpace(os.Getenv("SP_CHECKPOINT_KEY_ID"))
	if keyID == "" {
		keyID = "default"
	}
	signer, err := checkpoint.LoadSignerKey(cfg.CheckpointKeyPath, keyID)
	if err != nil {
		return nil, err
	}
	sink, err := checkpoint.NewFileSink(cfg.CheckpointDir)
	if err != nil {
		return nil, err
	}
	return &checkpoint.Scheduler{
		Pool:   pool,
		Sealer: &checkpoint.Sealer{Sink: sink, Signer: signer, Trusted: signer.PublicKey()},
		Tenant: cfg.Tenant,
		Logf: func(format string, args ...any) {
			log.Info("checkpoint: "+fmt.Sprintf(format, args...),
				"tenant_id", cfg.Tenant, "site_id", cfg.SiteID)
		},
		OnSealed: func(seal checkpoint.Seal) {
			registry.Counters.Inc(observability.CounterCheckpointsSealed)
			log.Info("sealed the audit ledger",
				"tenant_id", seal.TenantID,
				"from_sequence", seal.FromSequence,
				"to_sequence", seal.ToSequence,
				"entries", seal.EntryCount)
		},
	}, nil
}

func runVLMWorker(ctx context.Context, w *jobs.Worker,
	handler func(context.Context, jobs.Job) error, tenant string, log *slog.Logger) {
	w.Handler = handler
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		if _, err := w.Run(ctx, tenant, "vlm", 4); err != nil && ctx.Err() == nil {
			log.Error("the vlm worker could not run", "error", err.Error())
		}
	}
}

func newLogger(level string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("SP_LOG_LEVEL must be one of debug, info, warn, error, got %q", level)
	}
	// Text to stderr. Stderr is where systemd collects it, and a console log mixed
	// into an operator's terminal output on an SSH session is a stream nobody reads.
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})), nil
}

// flatten turns the startup map into alternating key/value arguments for slog, in
// sorted order so two runs produce comparable log lines.
func flatten(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		out = append(out, k, m[k])
	}
	return out
}

// milestoneBaseURL is the adapter's endpoint. It has no field on Config because
// M1 supports one vendor and one integration, and inventing a setting for it now
// is a setting somebody has to document and migrate later.
func milestoneBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("SP_MILESTONE_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8000/api"
}

// milestoneToken is the adapter's bearer credential.
func milestoneToken() string { return strings.TrimSpace(os.Getenv("SP_MILESTONE_TOKEN")) }

// secretKeyPath is where the MFA wrapping key lives.
//
// A fixed path rather than a configuration field: it is created by the installer
// at a known location with known permissions, and a setting that lets an operator
// point it somewhere else is a setting that lets them point it somewhere the core
// cannot read.
func secretKeyPath() string {
	if v := strings.TrimSpace(os.Getenv("SP_SECRET_KEY")); v != "" {
		return v
	}
	return "/etc/security-platform/secret.key"
}

// clockPolicy is the site's clock policy.
//
// The defaults the clock package declares, applied here explicitly so the value
// the correlator and the console agree on is visible in one place rather than
// being two independent default constructions that can drift.
func clockPolicy() clock.Policy { return clock.DefaultPolicy() }

// aiAllowsRemote reports whether the operator opted into sending evidence to a
// non-loopback AI runtime.
//
// The default is no, because runtime.py has no authentication at all: anything
// that can reach the socket can read the site's evidence and dictate what the
// model says about it.
func aiAllowsRemote() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SP_AI_ALLOW_REMOTE")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
