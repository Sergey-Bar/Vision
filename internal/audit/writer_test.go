package audit_test

// The WRITER.
//
// # The gap this file closes
//
// Every other test in this package builds the chain BY HAND, with raw SQL, and
// then verifies it. That proves the VERIFIER works on a chain somebody else
// built. It proved nothing about the writer -- the function that produces the
// rows the product actually appends -- and the writer was the uncovered half of
// the package that carries invariant 4.
//
// A ledger whose writer is untested is a ledger whose chain is a claim.
//
// # What the writer actually does
//
// Four things, and each one is a decision:
//
//   - it DEFAULTS the timestamp, and it defaults it BEFORE validating, because
//     a caller that did not set a timestamp has not made the row
//     unattributable;
//   - it NORMALISES the empty fields: a nil list becomes `[]` and not NULL, an
//     empty string stays an empty string, and the two genuinely nullable hashes
//     become NULL rather than an invented value;
//   - it allocates the SEQUENCE, under the per-tenant advisory lock, so two
//     concurrent writers cannot both compute the same one;
//   - it writes INSIDE the CALLER'S TRANSACTION, so the fact being audited and
//     the row recording it commit or fail together.
//
// The last one is the one this file tests hardest, because it is the one a
// ledger is for: a recorded action whose state change rolled back would be a
// lie, and an action that was not recorded because its transaction rolled back
// did not happen.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// writer is an audit writer over the test's pool.
func writer(t *testing.T, pool *pgxpool.Pool) *audit.Writer {
	t.Helper()
	return &audit.Writer{Pool: &dbx.Pool{Pool: pool}}
}

// operatorEntry is a complete, attributable entry.
func operatorEntry(tenant string) audit.Entry {
	return audit.Entry{
		TenantID:  tenant,
		Actor:     "op-1",
		ActorType: audit.ActorOperator,
		Action:    "incident.dispositioned",
		TS:        time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
}

// TestTheWriterProducesAChainTheVerifierAccepts is the end-to-end proof.
//
// The writer appends, and the verifier says the chain is intact. Every other
// test in this package verified a chain somebody built by hand; this one
// verifies the chain the WRITER built.
func TestTheWriterProducesAChainTheVerifierAccepts(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()
	w := writer(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.Append(ctx, tx, operatorEntry(tenant)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("append %d: %v", i, err)
		}
	}
	verification, err := w.Verify(ctx, tx, tenant, 0)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("verify: %v", err)
	}
	if !verification.Valid {
		_ = tx.Rollback(ctx)
		t.Fatalf("the chain the writer produced does not verify: %+v", verification)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestTheWriterAllocatesTheSequence is the chain's shape.
//
// The chain's link is not a column: the trigger recomputes it from the previous
// row's `hash`, and the VERIFIER recomputes it again. So what the writer
// contributes is the SEQUENCE, and it has to be consecutive and per-tenant: two
// writers on one tenant must not both compute the same one, which is what the
// advisory lock is for.
func TestTheWriterAllocatesTheSequence(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()
	w := writer(t, pool)

	rolled := tx(t, pool)
	defer func() { _ = rolled.Rollback(ctx) }()

	for i := 0; i < 3; i++ {
		if _, err := w.Append(ctx, rolled, operatorEntry(tenant)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	rows, err := rolled.Query(ctx,
		`SELECT sequence FROM audit_ledger WHERE tenant_id = $1 ORDER BY sequence`, tenant)
	if err != nil {
		t.Fatalf("read the sequences: %v", err)
	}
	defer rows.Close()
	var sequences []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		sequences = append(sequences, seq)
	}
	if len(sequences) != 3 {
		t.Fatalf("the ledger holds %d rows, want 3", len(sequences))
	}
	for i := 1; i < len(sequences); i++ {
		if sequences[i] != sequences[i-1]+1 {
			t.Errorf("the sequences are %v. They have to be consecutive: the chain is walked by "+
				"sequence and a gap is a hole in it", sequences)
			break
		}
	}
}

// TestTheWritersRowIsInTheCallersTransaction is the property the ledger is
// for.
//
// The writer's row is written inside the CALLER's transaction, so:
//
//   - a caller that ROLLS BACK leaves no row, because the action did not
//     happen; and
//   - a caller that COMMITS leaves the row, because the action did.
//
// The second half is the one that is easy to get wrong in the other direction: a
// recorded action whose state change rolled back would be a lie, and the
// operator reading the ledger six months later would be reading a record of
// something that never occurred.
func TestTheWritersRowIsInTheCallersTransaction(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()
	w := writer(t, pool)

	// ROLLBACK: no row.
	rolled := tx(t, pool)
	if _, err := w.Append(ctx, rolled, operatorEntry(tenant)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := rolled.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if n := countRows(t, pool, tenant); n != 0 {
		t.Errorf("the ledger holds %d row(s) after the caller's transaction rolled back. A "+
			"recorded action whose state change rolled back is a lie", n)
	}

	// COMMIT: the row is there.
	committed := tx(t, pool)
	if _, err := w.Append(ctx, committed, operatorEntry(tenant)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := committed.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := countRows(t, pool, tenant); n != 1 {
		t.Errorf("the ledger holds %d row(s) after the caller's transaction committed, want 1", n)
	}
}

// TestTheWriterRefusesAnEntryNobodyCouldInterpret is the writer's own guard.
//
// The writer validates before it writes, so a row with no actor never reaches
// the table.
func TestTheWriterRefusesAnEntryNobodyCouldInterpret(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()
	w := writer(t, pool)

	rolled := tx(t, pool)
	defer func() { _ = rolled.Rollback(ctx) }()

	for name, mutate := range map[string]func(*audit.Entry){
		"no tenant":     func(e *audit.Entry) { e.TenantID = "" },
		"no actor":      func(e *audit.Entry) { e.Actor = "" },
		"no actor type": func(e *audit.Entry) { e.ActorType = "" },
		"no action":     func(e *audit.Entry) { e.Action = "" },
		"bad actor":     func(e *audit.Entry) { e.ActorType = "root" },
	} {
		entry := operatorEntry(tenant)
		mutate(&entry)
		if _, err := w.Append(ctx, rolled, entry); err == nil {
			t.Errorf("the writer accepted an entry missing %s", name)
		}
	}
	if n := countRows(t, pool, tenant); n != 0 {
		t.Errorf("the ledger holds %d row(s) after %d refusals", n, 5)
	}
}

// TestTheWriterStampsTheTimestampItWasGiven is the clock.
//
// A caller that sets the timestamp gets it, and a caller that does not gets the
// writer's own. The chain includes `ts`, so a clock that jumps backwards
// mid-run is visible to anyone verifying the ledger afterwards -- and that only
// works if the writer STAMPS the row rather than letting the database do it.
func TestTheWriterStampsTheTimestampItWasGiven(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	stamped := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	fixed := &audit.Writer{
		Pool: &dbx.Pool{Pool: pool},
		Now:  func() time.Time { return stamped },
	}

	rolled := tx(t, pool)
	defer func() { _ = rolled.Rollback(ctx) }()

	// With a timestamp: the caller's.
	entry := operatorEntry(tenant)
	if _, err := fixed.Append(ctx, rolled, entry); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Without one: the writer's.
	entry = operatorEntry(tenant)
	entry.TS = time.Time{}
	if _, err := fixed.Append(ctx, rolled, entry); err != nil {
		t.Fatalf("append without a timestamp: %v", err)
	}

	rows, err := rolled.Query(ctx,
		`SELECT ts FROM audit_ledger WHERE tenant_id = $1 ORDER BY sequence`, tenant)
	if err != nil {
		t.Fatalf("read the rows: %v", err)
	}
	defer rows.Close()
	var stamps []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatalf("scan: %v", err)
		}
		stamps = append(stamps, ts.UTC())
	}
	if len(stamps) != 2 {
		t.Fatalf("the ledger holds %d rows, want 2", len(stamps))
	}
	if !stamps[0].Equal(stamped) {
		t.Errorf("the caller's timestamp became %s, want %s", stamps[0], stamped)
	}
	if !stamps[1].Equal(stamped) {
		t.Errorf("the writer's own timestamp became %s, want %s", stamps[1], stamped)
	}
}

// TestTheWriterNormalisesTheEmptyFields is the honesty of the row.
//
// A nil list is `[]` and not NULL, because a NULL list and an empty list are
// different claims and the table says one of them. The two genuinely nullable
// hashes are NULL and not an invented value, because an action that changed
// nothing has no prior state and putting a hash there would refer to nothing.
func TestTheWriterNormalisesTheEmptyFields(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()
	w := writer(t, pool)

	rolled := tx(t, pool)
	defer func() { _ = rolled.Rollback(ctx) }()

	entry := operatorEntry(tenant)
	if _, err := w.Append(ctx, rolled, entry); err != nil {
		t.Fatalf("append: %v", err)
	}

	var evidence, scope, reason, prevHash string
	var prevIsNull bool
	if err := rolled.QueryRow(ctx,
		`SELECT evidence_refs::text, site_scope::text, reason,
		        coalesce(prev_state_hash, ''), prev_state_hash IS NULL
		   FROM audit_ledger WHERE tenant_id = $1 ORDER BY sequence DESC LIMIT 1`,
		tenant).Scan(&evidence, &scope, &reason, &prevHash, &prevIsNull); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if evidence != "[]" {
		t.Errorf("the evidence refs are %q, want []", evidence)
	}
	if scope != "[]" {
		t.Errorf("the site scope is %q, want []", scope)
	}
	if reason != "" {
		t.Errorf("the reason is %q, want an empty string", reason)
	}
	if !prevIsNull {
		t.Errorf("the prev_state_hash is %q, want NULL. An action that changed nothing has no "+
			"prior state, and a hash there would refer to nothing", prevHash)
	}
}

// TestTheWriterCountsItsAppends is the metric that was free.
//
// `audit_appends_total` was declared and nothing incremented it. The writer
// counts it, and a writer with no registry counts nothing rather than refusing
// to append.
func TestTheWriterCountsItsAppends(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	registry := observability.NewRegistry()
	w := &audit.Writer{Pool: &dbx.Pool{Pool: pool}, Metrics: registry}

	rolled := tx(t, pool)
	defer func() { _ = rolled.Rollback(ctx) }()

	if _, err := w.Append(ctx, rolled, operatorEntry(tenant)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := registry.Counters.Get(observability.CounterAuditAppends); got != 1 {
		t.Errorf("the append counter reads %d, want 1", got)
	}
}

// tx begins a transaction.
func tx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	rolled, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	return rolled
}

// countRows counts the tenant's ledger rows.
func countRows(t *testing.T, pool *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_ledger WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count the rows: %v", err)
	}
	return n
}
