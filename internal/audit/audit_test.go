package audit_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/checkpoint"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testutil.Pool(t)
	var applied int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='audit_ledger'`,
	).Scan(&applied); err != nil || applied == 0 {
		testutil.Reset(t)
		if _, err := (&migrate.Runner{Pool: pool}).Up(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return pool
}

// The writer must refuse an entry that an auditor could not later interpret. A
// ledger row with no actor is a row nobody can attribute, and attribution is the
// entire point.
func TestValidateRejectsIncompleteEntries(t *testing.T) {
	base := audit.Entry{
		TenantID:  "t1",
		Actor:     "op1",
		ActorType: audit.ActorOperator,
		Action:    audit.ActionLoginSuccess,
		TS:        time.Now(),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("a complete entry was rejected: %v", err)
	}

	cases := map[string]func(*audit.Entry){
		"no tenant":     func(e *audit.Entry) { e.TenantID = "" },
		"no actor":      func(e *audit.Entry) { e.Actor = "" },
		"no actor type": func(e *audit.Entry) { e.ActorType = "" },
		"no action":     func(e *audit.Entry) { e.Action = "" },
		"no timestamp":  func(e *audit.Entry) { e.TS = time.Time{} },
		"bad actor":     func(e *audit.Entry) { e.ActorType = "root" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := base
			mutate(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("an entry missing %s was accepted", name)
			}
		})
	}
}

func appendOne(ctx context.Context, pool *pgxpool.Pool, e audit.Entry) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// The same per-tenant advisory lock the trigger takes, taken here so that two
	// concurrent writers cannot both compute the same next sequence and race for
	// the primary key. This mirrors internal/audit.Writer.Append.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, e.TenantID); err != nil {
		return err
	}
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT audit_next_sequence($1)`, e.TenantID).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action, reason)
		 VALUES ($1, $2, now(), $3, $4, $5, $6)`,
		e.TenantID, seq, e.Actor, string(e.ActorType), e.Action, e.Reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Two writers appending concurrently must produce one chain, not two. Both the
// sequence allocation and the trigger's advisory lock are per tenant, which is
// the only reason this holds.
func TestConcurrentAppendsProduceOneChain(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	const writers = 8
	const perWriter = 10

	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func() {
			for i := 0; i < perWriter; i++ {
				if err := appendOne(ctx, pool, audit.Entry{
					TenantID:  tenant,
					Actor:     "op",
					ActorType: audit.ActorOperator,
					Action:    audit.ActionIncidentCreated,
					Reason:    "concurrent append",
				}); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	for w := 0; w < writers; w++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_ledger WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != writers*perWriter {
		t.Errorf("%d rows for %d appends; a lost row is a lost audit fact", n, writers*perWriter)
	}

	var maxSeq int64
	var distinct int64
	if err := pool.QueryRow(ctx,
		`SELECT max(sequence), count(DISTINCT sequence) FROM audit_ledger WHERE tenant_id = $1`,
		tenant).Scan(&maxSeq, &distinct); err != nil {
		t.Fatalf("sequence stats: %v", err)
	}
	if distinct != maxSeq {
		t.Errorf("sequences are not dense: %d distinct up to %d", distinct, maxSeq)
	}

	var valid bool
	if err := pool.QueryRow(ctx, `SELECT valid FROM audit_verify_chain($1, 1)`, tenant).Scan(&valid); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !valid {
		t.Error("concurrent appends produced a broken chain")
	}
}

// Two tenants writing at once must not serialise against each other, and
// interleaving must not cross-link their chains.
func TestChainsAreIsolatedPerTenant(t *testing.T) {
	pool := migrated(t)
	tenantA, _, _ := testutil.NewTenant(t, pool)
	tenantB, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	errs := make(chan error, 2)
	for _, tenant := range []string{tenantA, tenantB} {
		go func(id string) {
			for i := 0; i < 20; i++ {
				if err := appendOne(ctx, pool, audit.Entry{
					TenantID: id, Actor: "op", ActorType: audit.ActorOperator,
					Action: audit.ActionIncidentCreated,
				}); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}(tenant)
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	for _, tenant := range []string{tenantA, tenantB} {
		var valid bool
		if err := pool.QueryRow(ctx, `SELECT valid FROM audit_verify_chain($1, 1)`, tenant).Scan(&valid); err != nil {
			t.Fatalf("verify %s: %v", tenant, err)
		}
		if !valid {
			t.Errorf("tenant %s chain is broken by the other tenant's writes", tenant)
		}
	}
}

// The sealer verifies before it seals. A seal over a chain that does not hold
// would be a claim rather than evidence, which is worse than no seal.
func TestSealerRefusesToSealABrokenChain(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if err := appendOne(ctx, pool, audit.Entry{
			TenantID: tenant, Actor: "op", ActorType: audit.ActorOperator,
			Action: audit.ActionIncidentCreated,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	signer, _ := checkpoint.NewSigner("test-key")
	dir := t.TempDir()
	sink, err := checkpoint.NewFileSink(dir)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	sealer := &checkpoint.Sealer{Sink: sink, Signer: signer, Trusted: signer.PublicKey()}

	testutil.Exec(t, pool, `ALTER TABLE audit_ledger DISABLE TRIGGER audit_ledger_append_only`)
	testutil.Exec(t, pool,
		`UPDATE audit_ledger SET action = 'something.else' WHERE tenant_id = $1 AND sequence = 2`, tenant)
	testutil.Exec(t, pool, `ALTER TABLE audit_ledger ENABLE TRIGGER audit_ledger_append_only`)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err := sealer.SealRange(ctx, tx, tenant, 1, 0, time.Now()); err == nil {
		t.Fatal("a seal was produced over a chain that does not verify")
	} else if !strings.Contains(err.Error(), "refusing to seal") {
		t.Errorf("unexpected error: %v", err)
	}

	if files := listFiles(t, dir); len(files) != 0 {
		t.Errorf("a refused seal still wrote %d file(s)", len(files))
	}
}

func TestSealerProducesAVerifiableSeal(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		if err := appendOne(ctx, pool, audit.Entry{
			TenantID: tenant, Actor: "op", ActorType: audit.ActorOperator,
			Action: audit.ActionDispositionRecorded,
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	signer, _ := checkpoint.NewSigner("test-key")
	dir := t.TempDir()
	sink, err := checkpoint.NewFileSink(dir)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	sealer := &checkpoint.Sealer{Sink: sink, Signer: signer, Trusted: signer.PublicKey()}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	seal, err := sealer.SealRange(ctx, tx, tenant, 1, 0, time.Now())
	if err != nil {
		t.Fatalf("SealRange: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if seal.EntryCount != 6 {
		t.Errorf("sealed %d entries, want 6", seal.EntryCount)
	}
	if err := checkpoint.VerifySeal(seal, signer.PublicKey()); err != nil {
		t.Fatalf("the seal this package produced does not verify: %v", err)
	}

	var recorded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_checkpoints WHERE tenant_id = $1 AND checkpoint_id = $2`,
		tenant, seal.CheckpointID).Scan(&recorded); err != nil {
		t.Fatalf("count checkpoints: %v", err)
	}
	if recorded != 1 {
		t.Errorf("%d checkpoint rows recorded, want 1", recorded)
	}

	loaded, err := sink.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("%d seal files, want 1", len(loaded))
	}
	if err := checkpoint.VerifySeal(loaded[0], signer.PublicKey()); err != nil {
		t.Fatalf("the reloaded seal does not verify: %v", err)
	}
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
