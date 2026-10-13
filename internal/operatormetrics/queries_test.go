package operatormetrics_test

// The durable metric computations.
//
// These are the tests for the four things ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¾Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â¦ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â¦ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â§6.2 of the plan said were missing:
// attention_minutes had no producer, MTTI had no computation, two verification
// states had no number anywhere, and evidence_fetch_failed_total had no durable
// source. Each test below asserts the part that makes the number honest rather
// than merely present:
//
//   - attention reports BOTH the capped and the uncapped figure, and an incident
//     nobody opened is not counted as a zero;
//   - MTTI is three numbers, not one, because a mean of detection latency and
//     investigation latency has the remedy of neither;
//   - the two silent states are in the distribution;
//   - the evidence proxy is labelled a proxy, so a fetch that eventually
//     succeeded is not counted as a failure that never happened.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

func ctx() context.Context { return context.Background() }

// TestRule is the one rule class these tests raise under. Kept a constant rather
// than a parameter, because a helper that takes a value every caller passes the
// same value of is a parameter that exists to be varied by nobody.
const TestRule = "core.camera-offline"

// testSession is the one session these tests open incidents from. A constant for
// the same reason as TestRule: a helper parameter nobody varies is a parameter
// nobody will read.
const testSession = "session-a"

// ---------------------------------------------------------------------------
// the harness: one database for the package, one tenant per test
// ---------------------------------------------------------------------------

// harness is one test's view: a fresh tenant over the package's shared pool, and
// the two services and the audit writer these queries read the rows of.
type harness struct {
	pool     *dbx.Pool
	tenant   string
	site     string
	queries  *operatormetrics.Queries
	incident *incidents.Service
	audit    *audit.Writer
	now      time.Time
}

// sharedPool builds the package's test database ONCE.
//
// `testutil.Pool(t)` is not a getter: it drops the previous run's package database
// and recreates it from scratch, which is the right behaviour when one test owns a
// package and the wrong one when ten tests share a binary. Called per test it is
// ten schema migrations and ten drops, which is most of what this package's run
// spends â€” and, under `go test -p 8` with every other database-backed package doing
// the same, is most of what the GATE spends.
//
// So the pool is built once and cached, and every test gets a fresh TENANT instead.
// A tenant is four INSERTs and arrives with its own site, its own device and its own
// RLS scope, so nothing one test writes is visible to the next â€” which is the
// isolation the dropping was buying, at a four-hundredth of the price.
var (
	sharedPoolOnce sync.Once
	sharedPool     *dbx.Pool
	sharedPoolErr  error
	sharedRaw      *pgxpool.Pool
	sharedRawErr   error
)

func packagePool(t *testing.T) (*dbx.Pool, *pgxpool.Pool) {
	t.Helper()
	// The skip lives OUTSIDE the Once, and that is the whole fix. `testutil.Pool`
	// skips when the harness is unconfigured, but a skip inside a sync.Once skips
	// the test that happened to be first and leaves the cached fields nil - so
	// every test after it took the nil pool and panicked inside the migrator,
	// which reads as a broken package rather than as an absent environment. The
	// repository's rule is that a database-backed suite SKIPS: a green run that
	// tested nothing is bad, and a red run for a reason nobody saw is worse.
	if os.Getenv(testutil.DatabaseURLEnv) == "" {
		t.Skip("no SP_TEST_DATABASE_URL: the database-backed tests skip rather than fail")
	}
	sharedPoolOnce.Do(func() {
		raw := testutil.Pool(t) // skips or fails fatally when the harness is wrong
		sharedRaw = raw
		cfg := dbx.DefaultConfig()
		if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
			sharedPoolErr = fmt.Errorf("apply DSN: %w", err)
			return
		}
		pool, err := dbx.Open(ctx(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
		if err != nil {
			sharedPoolErr = fmt.Errorf("open: %w", err)
			return
		}
		sharedPool = pool
	})
	if sharedRawErr != nil {
		t.Fatalf("pool: %v", sharedRawErr)
	}
	if sharedPoolErr != nil {
		t.Fatalf("pool: %v", sharedPoolErr)
	}
	return sharedPool, sharedRaw
}

// setup returns a fresh tenant over the package's shared pool.
func setup(t *testing.T) harness {
	t.Helper()
	pool, raw := packagePool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)
	writer := &audit.Writer{Pool: pool, Now: func() time.Time { return time.Now().UTC() }}
	return harness{
		pool:     pool,
		tenant:   tenant,
		site:     site,
		queries:  &operatormetrics.Queries{Pool: pool},
		incident: &incidents.Service{Pool: pool},
		audit:    writer,
		now:      time.Now().UTC(),
	}
}

// open writes the incident.opened row the console's read path writes, at a
// caller-chosen instant.
//
// The timestamp is supplied to the entry rather than taken by the writer, because
// attention is a subtraction and an interval measured from a clock the test did
// not choose cannot be asserted. `audit.Writer.Now` is not enough: it is stamped
// per entry, and the ledger's own design lets a caller state the instant.
func (h harness) open(t *testing.T, incidentID string, at time.Time) {
	t.Helper()
	if err := h.pool.InSystemTenant(ctx(), func(tx pgx.Tx) error {
		_, err := h.audit.Append(ctx(), tx, audit.Entry{
			TenantID: h.tenant, Actor: "operator-a", ActorType: audit.ActorOperator,
			Action: audit.ActionIncidentOpened, TargetType: "incident", TargetID: incidentID,
			SessionID: testSession, TS: at,
		})
		return err
	}); err != nil {
		t.Fatalf("append incident.opened: %v", err)
	}
}

// raise is one corroborated incident whose first_event_at is `detectionAgo`
// before its created_at, so detection latency is a number the test chose rather
// than one the clock chose.
func (h harness) raise(t *testing.T, detectionAgo time.Duration) string {
	t.Helper()
	id, err := h.incident.CreateIncident(ctx(), incidents.Create{
		TenantID:           h.tenant,
		SiteID:             h.site,
		Decision:           rules.Decision{State: rules.StateCorroborated, RuleID: TestRule, Severity: "low", Reason: "corroborated", DegradedReason: rules.DegradedNone, Action: rules.Action{CreateIncident: true, Severity: "low"}},
		RuleBundleVersion:  "0.1.0",
		BundleContentHash:  "hash",
		PolicyVersion:      "p1",
		Title:              TestRule,
		SupportingEventIDs: []string{},
	})
	if err != nil {
		t.Fatalf("raise %s: %v", TestRule, err)
	}
	// first_event_at is what detection latency is measured from, and the create
	// path stamps created_at itself. Both are set directly rather than through a
	// path that would replay an event: this test is about the arithmetic, and a
	// seeded timestamp is the only way to make the expected value exact.
	if _, err := h.pool.For(h.tenant, "incidents").Exec(ctx(),
		`UPDATE incidents SET first_event_at = $1, created_at = $2 WHERE incident_id = $3`,
		h.now.Add(-detectionAgo), h.now, id); err != nil {
		t.Fatalf("stamp the incident: %v", err)
	}
	return id
}

// disposition records an operator verdict at a caller-chosen instant.
//
// Written directly rather than through `incidents.Service.Record`, because
// created_at defaults to now() and an interval measured from a timestamp the test
// did not choose is an interval the test cannot assert.
func (h harness) disposition(t *testing.T, incidentID string, at time.Time) {
	t.Helper()
	if _, err := h.pool.For(h.tenant, "dispositions").Exec(ctx(), `
		INSERT INTO dispositions (disposition_id, tenant_id, incident_id, actor_id,
		                          outcome, agreement, session_id, created_at)
		VALUES ($1, $2, $3, 'operator-a', 'true_positive', 'agreed', 'session-a', $4)`,
		"disp-"+incidentID, h.tenant, incidentID, at); err != nil {
		t.Fatalf("disposition: %v", err)
	}
}

// ---------------------------------------------------------------------------
// attention_minutes
// ---------------------------------------------------------------------------

// Both halves, always. The uncapped number is an over-count by construction ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¾Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â¦ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â¦ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â it
// includes the operator who opened an incident and went to lunch ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¾Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â¦ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â‚¬Å¾Ã‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â¦ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â and reporting
// only one half is how a cap starts to flatter the product.
func TestAttentionReportsTheCappedAndTheUncappedNumber(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)
	// Five hours of attention: past the declared four-hour cap.
	h.disposition(t, id, h.now.Add(5*time.Hour))

	report, err := h.queries.Attention(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(6*time.Hour), operatormetrics.DefaultAttentionCap)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if report.Samples != 1 {
		t.Fatalf("%d measured incident(s), want 1", report.Samples)
	}
	if got := report.UncappedSeconds; got < 5*3600-1 || got > 5*3600+1 {
		t.Errorf("uncapped is %.0fs, want about 18000 (five hours)", got)
	}
	if got := report.CappedSeconds; got < 4*3600-1 || got > 4*3600+1 {
		t.Errorf("capped is %.0fs, want about 14400 (the declared cap)", got)
	}
	if report.CappedCount != 1 {
		t.Errorf("%d incident(s) hit the cap, want 1: the cap that bit has to be visible", report.CappedCount)
	}
	for _, item := range report.Incidents {
		if !item.Capped {
			t.Error("the incident is not marked capped, so a reader cannot tell which half is the approximation")
		}
	}
}

// An incident nobody opened is NOT zero minutes.
//
// This build predates the producer, or the session is older than the ledger, and
// either way the honest answer is "not measured". A zero would say "nobody looked
// at this incident", which is a different fact and a wrong one.
func TestAnIncidentNobodyOpenedIsNotCountedAsZero(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.disposition(t, id, h.now.Add(3*time.Minute))

	report, err := h.queries.Attention(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(time.Hour), operatormetrics.DefaultAttentionCap)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if report.Samples != 0 {
		t.Errorf("%d measured incident(s), want 0: an incident with no incident.opened row "+
			"was never measured, and measuring it as zero minutes would assert nobody looked", report.Samples)
	}
	if report.UncappedSeconds != 0 || report.CappedSeconds != 0 {
		t.Errorf("totals are %.0f/%.0f, want 0/0 over no measured incident", report.UncappedSeconds, report.CappedSeconds)
	}
}

// An operator is still looking: counted as open, in no total. A window's
// attention minutes do not include an incident still accruing them.
func TestAnOpenIncidentIsReportedAndExcludedFromEveryTotal(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 2*time.Second)
	h.open(t, id, h.now)

	report, err := h.queries.Attention(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(time.Hour), operatormetrics.DefaultAttentionCap)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if report.OpenCount != 1 {
		t.Errorf("%d open incident(s), want 1", report.OpenCount)
	}
	if report.Samples != 0 || report.CappedSeconds != 0 {
		t.Errorf("an open incident contributed to a total: %d samples, %.0f capped seconds", report.Samples, report.CappedSeconds)
	}
}

// ---------------------------------------------------------------------------
// mt_ti_seconds
// ---------------------------------------------------------------------------

// Three numbers, never one. The mean of detection latency and investigation
// latency is a number with the remedy of neither, and a reader who wanted either
// would act on the wrong thing.
func TestMTTIReportsThreeNumbersRatherThanOne(t *testing.T) {
	h := setup(t)
	id := h.raise(t, 30*time.Second)                // detection = 30s
	h.disposition(t, id, h.now.Add(90*time.Second)) // investigation = 90s

	report, err := h.queries.MTTI(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("mtti: %v", err)
	}
	if len(report.Classes) != 1 {
		t.Fatalf("%d class(es), want 1", len(report.Classes))
	}
	c := report.Classes[0]
	if c.RuleID != "core.camera-offline" {
		t.Errorf("class is %q, want core.camera-offline", c.RuleID)
	}
	if c.MedianDetection < 29 || c.MedianDetection > 31 {
		t.Errorf("median detection is %.1fs, want about 30", c.MedianDetection)
	}
	if c.MedianInvestigation < 89 || c.MedianInvestigation > 91 {
		t.Errorf("median investigation is %.1fs, want about 90", c.MedianInvestigation)
	}
	if c.MedianMTTI < 119 || c.MedianMTTI > 121 {
		t.Errorf("median MTTI is %.1fs, want about 120 (the sum of the two parts)", c.MedianMTTI)
	}
	// With one sample the mean and the median agree, which is the check that the
	// mean is reported at all rather than quietly dropped.
	if c.MeanMTTI < 119 || c.MeanMTTI > 121 {
		t.Errorf("mean MTTI is %.1fs, want about 120", c.MeanMTTI)
	}
}

// ---------------------------------------------------------------------------
// the two silent verification states
// ---------------------------------------------------------------------------

// duplicate and suppressed are the two states with no number anywhere, and they
// are the two an operator asks about when they suspect the rules are eating
// incidents.
func TestVerificationStatesIncludeTheTwoSilentOnes(t *testing.T) {
	h := setup(t)

	for _, state := range []rules.VerificationState{
		rules.StateDuplicate, rules.StateSuppressed, rules.StateUnverified,
	} {
		// Each decision writes one verification_results row, which is the row
		// the distribution is computed over.
		if _, err := h.incident.CreateIncident(ctx(), incidents.Create{
			TenantID: h.tenant, SiteID: h.site,
			Decision: rules.Decision{
				State: state, RuleID: "core.camera-offline", Severity: "low",
				Reason: string(state), DegradedReason: rules.DegradedNone, Action: rules.Action{CreateIncident: true, Severity: "low"},
			},
			RuleBundleVersion: "0.1.0", BundleContentHash: "hash", PolicyVersion: "p1",
			Title: string(state), SupportingEventIDs: []string{},
		}); err != nil {
			// A decision that does not raise an incident writes no row; the two
			// states under test do, and a UNVERIFIED one does too.
			if state != rules.StateDuplicate {
				t.Fatalf("raise %s: %v", state, err)
			}
		}
	}

	counts, err := h.queries.VerificationStates(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("verification states: %v", err)
	}
	if counts.ByState["duplicate"] == 0 {
		t.Errorf("duplicate is %d: the state that means ingest working correctly had no "+
			"number at all, which is the gap this query closes", counts.ByState["duplicate"])
	}
	if counts.ByState["suppressed"] == 0 {
		t.Errorf("suppressed is %d: a rule suppressing on purpose had no number, so "+
			"nothing could show an operator the two silent states exist", counts.ByState["suppressed"])
	}
}

// ---------------------------------------------------------------------------
// the evidence proxy
// ---------------------------------------------------------------------------

// The durable proxy counts dead-lettered jobs and nothing else. A job that failed
// twice and then succeeded is not a failure that reached an operator, and
// counting it would turn a floor on evidence trouble into a measurement of it.
func TestEvidenceFetchFailuresCountsDeadLetteredJobsOnly(t *testing.T) {
	h := setup(t)
	dead := func(reason string) {
		t.Helper()
		if _, err := h.pool.For(h.tenant, "jobs").Exec(ctx(), `
			INSERT INTO jobs (tenant_id, queue, status, idempotency_key, dead_letter_reason, completed_at, created_at)
			VALUES ($1, 'evidence_fetch', 'dead_letter', $2, $3, now(), now())`,
			h.tenant, "key-"+reason, reason); err != nil {
			t.Fatalf("insert dead-lettered job: %v", err)
		}
	}
	dead("hash mismatch")

	// A delivered job with the same queue is not a failure, and neither is one
	// that is still retrying: status is what the query reads.
	if _, err := h.pool.For(h.tenant, "jobs").Exec(ctx(), `
		INSERT INTO jobs (tenant_id, queue, status, idempotency_key, completed_at, created_at)
		VALUES ($1, 'evidence_fetch', 'done', 'key-done', now(), now())`, h.tenant); err != nil {
		t.Fatalf("insert done job: %v", err)
	}
	if _, err := h.pool.For(h.tenant, "jobs").Exec(ctx(), `
		INSERT INTO jobs (tenant_id, queue, status, idempotency_key, completed_at, created_at)
		VALUES ($1, 'evidence_fetch', 'failed', 'key-retry', now(), now())`, h.tenant); err != nil {
		t.Fatalf("insert retrying job: %v", err)
	}

	n, err := h.queries.EvidenceFetchFailures(ctx(), h.tenant, h.now.Add(-time.Hour), h.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("evidence failures: %v", err)
	}
	if n != 1 {
		t.Errorf("%d dead-lettered evidence-fetch job(s) counted, want 1: a done job and a "+
			"still-retrying one are both being counted as failures that did not happen", n)
	}
}

// One tenant's report does not include another's.
//
// This is the test for a bug that was found while writing the others: the first
// draft of these queries carried the tenant only in `Pool.For(...)`, which sets a
// session variable that ROW-LEVEL SECURITY reads ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â and the test suite connects as
// a superuser, which PostgreSQL exempts from every policy. Two tenants in one
// package run each saw both tenants' incidents, and every number below would have
// been the sum over the whole installation.
//
// The predicate is now in the SQL. This test is what keeps it there: a reader of
// the query cannot tell a predicate from a comment, and the next person to "clean
// up" a `WHERE tenant_id = $1` that looks redundant with the session setting will
// meet this failing.
func TestOneTenantsReportDoesNotIncludeAnothers(t *testing.T) {
	mine := setup(t)
	theirs := setup(t)

	// Same rule class, different tenants: if the second tenant's incident leaked
	// into the first's report the class would show two samples where it raised one.
	myID := mine.raise(t, 2*time.Second)
	mine.open(t, myID, mine.now)
	mine.disposition(t, myID, mine.now.Add(10*time.Minute))

	theirID := theirs.raise(t, 500*time.Second)
	theirs.open(t, theirID, theirs.now)
	theirs.disposition(t, theirID, theirs.now.Add(700*time.Second))

	report, err := mine.queries.MTTI(ctx(), mine.tenant, mine.now.Add(-time.Hour), mine.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("mtti: %v", err)
	}
	if len(report.Classes) != 1 {
		t.Fatalf("%d class(es) in my tenant's report, want 1", len(report.Classes))
	}
	if got := report.Classes[0].Samples; got != 1 {
		t.Errorf("my tenant's report covers %d incident(s), want 1: another tenant's incident "+
			"reached it, which is a disclosure and a wrong number at once", got)
	}
	if report.Classes[0].MedianDetection > 10 {
		t.Errorf("my tenant's detection latency is %.0fs, want about 2: it is reading the other "+
			"tenant's 500s", report.Classes[0].MedianDetection)
	}

	// And the same over attention, where the incident ids collide across tenants
	// and a tenant-less join would pair my incident with their disposition.
	mine2, err := mine.queries.Attention(ctx(), mine.tenant, mine.now.Add(-time.Hour), mine.now.Add(time.Hour), operatormetrics.DefaultAttentionCap)
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if mine2.Samples != 1 {
		t.Errorf("%d measured incident(s) in my tenant's attention, want 1", mine2.Samples)
	}
	if mine2.UncappedSeconds > 620 || mine2.UncappedSeconds < 580 {
		t.Errorf("my attention is %.0fs, want about 600: the disposition read from another "+
			"tenant's incident set the interval", mine2.UncappedSeconds)
	}
}

// The aggregate answers "how often does this system stay quiet". The slice
// answers the only follow-up that matters: WHERE.
//
// A rule suppressing 60% of its decisions at one site and 2% at another is a
// configuration question about that site, and the aggregate hides it completely â€”
// the two average into a plausible total that describes neither. This test builds
// both and asserts the aggregate cannot see the difference while the slice can.
func TestTheSilentShareIsReportedWhereItHappensNotOnlyInTotal(t *testing.T) {
	mine := setup(t)
	theirs := setup(t)

	// Two sites, same rule class, opposite behaviour.
	//  - "site-loud": three decided, one suppressed.
	//  - "site-quiet": one decided, three suppressed.
	//
	// The sites are created rather than named, because incidents.primary_site_id
	// is a foreign key and a decision at a site that does not exist writes
	// nothing at all â€” the same "silently absent" a skipped test would be.
	newSite := func(h harness, id string) {
		t.Helper()
		if _, err := testutil.Pool(t).Exec(ctx(),
			`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, $3, 'UTC')`,
			id, h.tenant, "Site "+id); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	newSite(mine, "site-loud")
	newSite(mine, "site-quiet")
	newSite(theirs, "site-quiet")

	seed := func(h harness, site string, decided, silent int) {
		t.Helper()
		for i := 0; i < decided+silent; i++ {
			state := rules.StateCorroborated
			if i >= decided {
				state = rules.StateSuppressed
			}
			if _, err := h.incident.CreateIncident(ctx(), incidents.Create{
				TenantID: h.tenant, SiteID: site,
				Decision: rules.Decision{
					State: state, RuleID: TestRule, Severity: "low",
					Reason: string(state), DegradedReason: rules.DegradedNone,
					Action: rules.Action{CreateIncident: true, Severity: "low"},
				},
				RuleBundleVersion: "0.1.0", BundleContentHash: "hash", PolicyVersion: "p1",
				Title: string(state), SupportingEventIDs: []string{},
			}); err != nil {
				t.Fatalf("seed %s: %v", state, err)
			}
		}
	}
	seed(mine, "site-loud", 3, 1)
	seed(mine, "site-quiet", 1, 3)

	slice, err := mine.queries.VerificationStatesBySiteAndClass(ctx(), mine.tenant,
		mine.now.Add(-time.Hour), mine.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if len(slice.Cells) != 2 {
		t.Fatalf("%d cells, want 2 (one per site)", len(slice.Cells))
	}
	if slice.TotalSilent != 4 || slice.TotalDecided != 4 {
		t.Errorf("window totals are %d silent / %d decided, want 4/4", slice.TotalSilent, slice.TotalDecided)
	}

	bySite := map[string]operatormetrics.StateSlice{}
	for _, c := range slice.Cells {
		bySite[c.SiteID] = c
		if c.Silent+c.ByState["corroborated"] != c.Total {
			t.Errorf("cell %s: %d silent + %d corroborated != %d total", c.SiteID, c.Silent, c.ByState["corroborated"], c.Total)
		}
	}
	loud := bySite["site-loud"]
	quiet := bySite["site-quiet"]
	if loud.Silent != 1 || quiet.Silent != 3 {
		t.Errorf("silent counts are loud=%d quiet=%d, want 1 and 3: the aggregate's 50%% is "+
			"exactly the average of two very different behaviours, and it describes neither site",
			loud.Silent, quiet.Silent)
	}

	// And the other tenant's rows do not appear in mine â€” the tenant predicate on
	// BOTH sides of the join, not just the driving table.
	seed(theirs, "site-quiet", 3, 3)
	again, err := mine.queries.VerificationStatesBySiteAndClass(ctx(), mine.tenant,
		mine.now.Add(-time.Hour), mine.now.Add(time.Hour))
	if err != nil {
		t.Fatalf("slice again: %v", err)
	}
	if len(again.Cells) != 2 || again.TotalSilent != 4 {
		t.Errorf("after a second tenant suppressed 6 more, my window shows %d cells and %d "+
			"silent, want 2 and 4: another tenant's suppressions reached my report",
			len(again.Cells), again.TotalSilent)
	}
}

// ---------------------------------------------------------------------------
// the helpers
// ---------------------------------------------------------------------------

// The median is the headline, and a single sample must not be a zero.
func TestMedianAndMean(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []float64
		want float64
	}{
		{"one sample", []float64{7}, 7},
		{"two samples", []float64{3, 9}, 6},
		{"odd count", []float64{5, 1, 9}, 5},
		{"even count", []float64{2, 8, 4, 6}, 5},
	} {
		if got := operatormetrics.Median(tc.in); got != tc.want {
			t.Errorf("%s: median is %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := operatormetrics.Mean([]float64{2, 4, 6}); got != 4 {
		t.Errorf("mean is %v, want 4", got)
	}
	// An empty population is not a median, and the documented answer is 0 with
	// every caller checking its own sample count first.
	if got := operatormetrics.Median(nil); got != 0 {
		t.Errorf("median of nothing is %v, want 0", got)
	}
}

// A pool that was never wired is a configuration error, reported rather than
// panicking: the report runs from a CLI and an operator's first run of a
// half-configured install should say what is wrong.
//
// The sentinel is what makes the answer distinguishable from a database failure,
// and it is asserted with errors.Is because that is the only way a caller can tell
// "you forgot to wire this" from "the query failed" ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã¢â‚¬Â¦Ãƒâ€šÃ‚Â¡ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™Ãƒâ€ Ã¢â‚¬â„¢ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã†â€™Ãƒâ€šÃ‚Â¢ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€¦Ã‚Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â¬ÃƒÆ’Ã†â€™ÃƒÂ¢Ã¢â€šÂ¬Ã…Â¡ÃƒÆ’Ã¢â‚¬Å¡Ãƒâ€šÃ‚Â a distinction the command's
// own caller needs, and one this test enforces by reading the symbol from outside
// the package.
func TestQueriesWithoutAPoolRefuseRatherThanPanic(t *testing.T) {
	q := &operatormetrics.Queries{}
	check := func(name string, _ error, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s with no pool succeeded", name)
		}
		if !errors.Is(err, operatormetrics.ErrNoPool) {
			t.Errorf("%s failed with %v; want ErrNoPool, so a caller can tell an "+
				"unwired report from a failed query", name, err)
		}
	}
	_, err := q.Attention(ctx(), "t", time.Now(), time.Now(), 0)
	check("attention", err, err)
	_, err = q.MTTI(ctx(), "t", time.Now(), time.Now())
	check("mtti", err, err)
	_, err = q.MTTV(ctx(), "t", time.Now(), time.Now())
	check("mttv", err, err)
	_, err = q.VerificationStates(ctx(), "t", time.Now(), time.Now())
	check("verification states", err, err)
	_, err = q.EvidenceFetchFailures(ctx(), "t", time.Now(), time.Now())
	check("evidence failures", err, err)
}
