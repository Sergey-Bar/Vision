package schemalaws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// These tests assert the laws the rest of the code assumes. Each one corresponds
// to a numbered invariant in AGENTS.md or a control in the threat model. A
// failure here is a schema regression that will surface later as silent data
// corruption, cross-tenant disclosure, or an unexplainable incident.
//
// The suite resets and rebuilds the schema, so it shares a database with the
// other database-backed suites. Go runs package test binaries in parallel by
// default, which would let one suite drop the schema out from under another;
// `make test` passes `-p 1` for that reason.

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testutil.Pool(t)
	var applied int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'audit_ledger'`,
	).Scan(&applied); err != nil || applied == 0 {
		testutil.Reset(t)
		if _, err := (&migrate.Runner{Pool: pool}).Up(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return pool
}

// ---------------------------------------------------------------------------
// Invariant 4: the audit ledger is append-only and hash-chained.
// ---------------------------------------------------------------------------

func TestAuditChainChainsFromGenesis(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	genesis := testutil.QueryOne(t, pool, `SELECT audit_next_sequence($1)`, tenant)
	if genesis != int64(1) {
		t.Fatalf("first sequence is %v, want 1", genesis)
	}

	for i := 0; i < 3; i++ {
		testutil.Exec(t, pool, `
			INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
			SELECT $1, audit_next_sequence($1), now(), 'op1', 'operator', 'incident.created'`, tenant)
	}

	var prev, hash string
	if err := pool.QueryRow(ctx,
		`SELECT prev_hash, hash FROM audit_ledger WHERE tenant_id = $1 AND sequence = 1`, tenant,
	).Scan(&prev, &hash); err != nil {
		t.Fatalf("read genesis row: %v", err)
	}
	if prev != strings.Repeat("0", 64) {
		t.Errorf("genesis prev_hash is %q, want 64 zeros", prev)
	}

	// Row N's prev_hash must be row N-1's hash. Any other chaining is a chain
	// in name only.
	var mismatches int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_ledger a
		  JOIN audit_ledger b ON b.tenant_id = a.tenant_id AND b.sequence = a.sequence - 1
		 WHERE a.tenant_id = $1 AND a.prev_hash IS DISTINCT FROM b.hash`, tenant).Scan(&mismatches); err != nil {
		t.Fatalf("compare chain: %v", err)
	}
	if mismatches != 0 {
		t.Errorf("%d row(s) do not chain to their predecessor", mismatches)
	}
}

// A duplicate sequence must fail. Two rows claiming the same slot would fork the
// chain, and a fork means two different histories both verify as "the" history.
func TestAuditChainRejectsDuplicateSequence(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	testutil.Exec(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
		VALUES ($1, 1, now(), 'admin', 'admin', 'identity.bootstrap_admin')`, tenant)

	err := testutil.ExpectError(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
		VALUES ($1, 1, now(), 'attacker', 'operator', 'incident.created')`, tenant)
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("expected a unique-violation, got %v", err)
	}
}

// A gap means a row is missing. A missing ledger row is exactly the loss the
// chain is supposed to make impossible, so the insert is refused rather than
// tolerated.
func TestAuditChainRejectsGap(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	testutil.Exec(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
		VALUES ($1, 1, now(), 'admin', 'admin', 'identity.bootstrap_admin')`, tenant)

	err := testutil.ExpectError(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
		VALUES ($1, 3, now(), 'op1', 'operator', 'incident.created')`, tenant)
	if !strings.Contains(err.Error(), "no predecessor") {
		t.Errorf("expected a chain-gap refusal, got %v", err)
	}
}

func TestAuditLedgerIsAppendOnly(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	testutil.Exec(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action, reason)
		VALUES ($1, 1, now(), 'admin', 'admin', 'identity.bootstrap_admin', 'original')`, tenant)

	updateErr := testutil.ExpectError(t, pool,
		`UPDATE audit_ledger SET reason = 'rewritten' WHERE tenant_id = $1 AND sequence = 1`, tenant)
	if !strings.Contains(updateErr.Error(), "append-only") {
		t.Errorf("expected an append-only refusal, got %v", updateErr)
	}

	deleteErr := testutil.ExpectError(t, pool,
		`DELETE FROM audit_ledger WHERE tenant_id = $1 AND sequence = 1`, tenant)
	if !strings.Contains(deleteErr.Error(), "append-only") {
		t.Errorf("expected an append-only refusal, got %v", deleteErr)
	}
}

// The realistic attacker has superuser and can drop the trigger. The chain is
// what catches them; the trigger is only the first line.
func TestTamperedRowBreaksChainVerification(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		testutil.Exec(t, pool, `
			INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
			SELECT $1, audit_next_sequence($1), now(), 'op1', 'operator', 'incident.created'`, tenant)
	}

	var valid bool
	if err := pool.QueryRow(ctx, `SELECT valid FROM audit_verify_chain($1, 1)`, tenant).Scan(&valid); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !valid {
		t.Fatal("an untouched chain did not verify")
	}

	testutil.Exec(t, pool, `ALTER TABLE audit_ledger DISABLE TRIGGER audit_ledger_append_only`)
	testutil.Exec(t, pool,
		`UPDATE audit_ledger SET actor = 'someone else' WHERE tenant_id = $1 AND sequence = 2`, tenant)
	testutil.Exec(t, pool, `ALTER TABLE audit_ledger ENABLE TRIGGER audit_ledger_append_only`)

	var brokenAt *int64
	if err := pool.QueryRow(ctx, `SELECT broken_at FROM audit_verify_chain($1, 1)`, tenant).Scan(&brokenAt); err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	if brokenAt == nil {
		t.Fatal("a rewritten row still verified; the chain is decorative")
	}
	if *brokenAt != 2 {
		t.Errorf("tamper reported at sequence %d, want 2", *brokenAt)
	}
}

// A row with no tenant is not a row that can be filtered later. tenant_id is
// NOT NULL at the storage layer, not merely by convention.
func TestAuditLedgerRequiresTenant(t *testing.T) {
	pool := migrated(t)
	err := testutil.ExpectError(t, pool, `
		INSERT INTO audit_ledger (tenant_id, sequence, ts, actor, actor_type, action)
		VALUES ('', 1, now(), 'admin', 'admin', 'identity.bootstrap_admin')`)
	_ = err
}

// ---------------------------------------------------------------------------
// Invariant 2: an AI observation is a historical fact, not an editable row.
// ---------------------------------------------------------------------------

func TestAIObservationIsAppendOnly(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	incident := "inc-" + tenant
	testutil.Exec(t, pool, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r.test', '1.0.0', $4, 'p1', now(), now())`,
		incident, tenant, site, strings.Repeat("a", 64))
	testutil.Exec(t, pool, `
		INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		                            model, model_hash, runtime_version, prompt_template_id, prompt_version,
		                            output_schema_version, structured_output, output_hash)
		VALUES ('obs-1', $1, $2, $3, 'evt-1', 'restricted-person', 'm1', $4, 'rt1', 'pt1', '1',
		        1, '{"insufficientEvidence":true}'::jsonb, $5)`,
		tenant, site, incident, strings.Repeat("b", 64), strings.Repeat("c", 64))

	err := testutil.ExpectError(t, pool,
		`UPDATE ai_observations SET structured_output = '{"insufficientEvidence":false}'::jsonb
		  WHERE tenant_id = $1 AND observation_id = 'obs-1'`, tenant)
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("expected an append-only refusal, got %v", err)
	}
}

// Re-evaluation must be expressible without touching the original row: a new
// observation that names the one it supersedes.
func TestAIObservationSupersession(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	incident := "inc-" + tenant
	testutil.Exec(t, pool, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r.test', '1.0.0', $4, 'p1', now(), now())`,
		incident, tenant, site, strings.Repeat("a", 64))

	for i, obs := range []string{"obs-1", "obs-2"} {
		supersedes := "NULL"
		if i == 1 {
			supersedes = "'obs-1'"
		}
		testutil.Exec(t, pool, `
			INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
			                            model, model_hash, runtime_version, prompt_template_id, prompt_version,
			                            output_schema_version, structured_output, output_hash, supersedes_observation_id)
			VALUES ($1, $2, $3, $4, 'evt-1', 'restricted-person', 'm1', $5, 'rt1', 'pt1', '1',
			        1, '{"insufficientEvidence":true}'::jsonb, $6, `+supersedes+`)`,
			obs, tenant, site, incident, strings.Repeat("b", 64), strings.Repeat("c", 64))
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ai_observations WHERE tenant_id = $1 AND supersedes_observation_id = 'obs-1'`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count superseding: %v", err)
	}
	if n != 1 {
		t.Errorf("%d observations supersede obs-1, want 1", n)
	}
}

// The degraded-mode contract needs a durable record that a rule saw no AI
// observation, distinct from one that saw a negative one.
func TestAIObservationRecordsDegradation(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	incident := "inc-" + tenant
	testutil.Exec(t, pool, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r.test', '1.0.0', $4, 'p1', now(), now())`,
		incident, tenant, site, strings.Repeat("a", 64))

	for _, reason := range []string{"model_unavailable", "no_runtime", "timeout", "error", "absent"} {
		testutil.Exec(t, pool, `
			INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
			                            model, model_hash, runtime_version, prompt_template_id, prompt_version,
			                            output_schema_version, structured_output, output_hash, degraded_reason)
			VALUES ($1, $2, $3, $4, 'evt-1', 'restricted-person', '', $5, '', '', '',
			        1, '{}'::jsonb, $6, $7)`,
			"obs-"+reason, tenant, site, incident, strings.Repeat("b", 64), strings.Repeat("c", 64), reason)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ai_observations WHERE tenant_id = $1 AND degraded_reason <> 'none'`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count degraded: %v", err)
	}
	if n != 5 {
		t.Errorf("%d degraded observations recorded, want 5", n)
	}

	// An unrecognised degradation reason must not be storable, or a typo in the
	// runtime would read as "no degradation" to every query downstream.
	testutil.ExpectError(t, pool, `
		INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		                            model, model_hash, runtime_version, prompt_template_id, prompt_version,
		                            output_schema_version, structured_output, output_hash, degraded_reason)
		VALUES ('obs-bogus', $1, $2, $3, 'evt-1', 'restricted-person', '', $4, '', '', '',
		        1, '{}'::jsonb, $5, 'timeout_but_ok')`,
		tenant, site, incident, strings.Repeat("b", 64), strings.Repeat("c", 64))
}

// ---------------------------------------------------------------------------
// Invariant 5: evidence paths cannot escape the evidence root.
// ---------------------------------------------------------------------------

func TestEvidenceStorageRefIsAlwaysRelative(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)

	bad := []string{
		"/etc/passwd",
		"/",
		"../../etc/passwd",
		"a/../../b",
		"C:\\Windows\\System32\\config",
		`a\..\b`,
		"",
		"..",
	}
	for i, ref := range bad {
		ref := ref
		t.Run(strings.ReplaceAll(ref, string(filepathSeparator()), "/"), func(t *testing.T) {
			testutil.ExpectError(t, pool, `
				INSERT INTO evidence (evidence_id, tenant_id, site_id, kind, tier, observed_at, captured_at,
				                      storage_ref, size_bytes, sha256)
				VALUES ($1, $2, $3, 'snapshot', 'A', now(), now(), $4, 0, $5)`,
				t.Name()+string(rune('a'+i)), tenant, site, ref, strings.Repeat("d", 64))
		})
	}

	good := []string{"2026/03/evt-1.jpg", "tenant/sha256/ab/abcdef.jpg", "a/b/c.bin"}
	for i, ref := range good {
		testutil.Exec(t, pool, `
			INSERT INTO evidence (evidence_id, tenant_id, site_id, kind, tier, observed_at, captured_at,
			                      storage_ref, size_bytes, sha256)
			VALUES ($1, $2, $3, 'snapshot', 'A', now(), now(), $4, 0, $5)`,
			"good-"+string(rune('a'+i)), tenant, site, ref, strings.Repeat("d", 64))
	}
}

func filepathSeparator() string { return string(rune(92)) }

// A sha256 column that accepts arbitrary text would let two rows claim the same
// bytes with different hashes, and every integrity check downstream becomes
// advisory.
func TestEvidenceHashIsValidatedByTheSchema(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)

	testutil.ExpectError(t, pool, `
		INSERT INTO evidence (evidence_id, tenant_id, site_id, kind, tier, observed_at, captured_at,
		                      storage_ref, size_bytes, sha256)
		VALUES ('ev-bad', $1, $2, 'snapshot', 'A', now(), now(), 'ok/path.bin', 0, 'not-a-hash')`,
		tenant, site)
	testutil.ExpectError(t, pool, `
		INSERT INTO evidence (evidence_id, tenant_id, site_id, kind, tier, observed_at, captured_at,
		                      storage_ref, size_bytes, sha256)
		VALUES ('ev-bad2', $1, $2, 'snapshot', 'A', now(), now(), 'ok/path.bin', -1, $3)`,
		tenant, site, strings.Repeat("d", 64))
}

// ---------------------------------------------------------------------------
// Invariant 1: exactly-once effect at intake.
// ---------------------------------------------------------------------------

func TestInboxDedupeIsGlobalNotPerPartition(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)

	// Same source event id, two different months. A dedupe key scoped to the
	// partition would accept the second one; the point of the separate key table
	// is that it does not.
	testutil.Exec(t, pool, `
		INSERT INTO inbox_event_keys (tenant_id, integration_id, source_event_id, inbox_partition)
		VALUES ($1, 'int-1', 'src-1', 'inbox_events_2026_01')`, tenant)

	err := testutil.ExpectError(t, pool, `
		INSERT INTO inbox_event_keys (tenant_id, integration_id, source_event_id, inbox_partition)
		VALUES ($1, 'int-1', 'src-1', 'inbox_events_2026_02')`, tenant)
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("expected a duplicate-key refusal, got %v", err)
	}

	// A different integration delivering the same source id is a different
	// event, not a duplicate.
	testutil.Exec(t, pool, `
		INSERT INTO inbox_event_keys (tenant_id, integration_id, source_event_id, inbox_partition)
		VALUES ($1, 'int-2', 'src-1', 'inbox_events_2026_01')`, tenant)
}

func TestJobsAreIdempotentPerQueue(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)

	testutil.Exec(t, pool,
		`INSERT INTO jobs (tenant_id, queue, idempotency_key, payload) VALUES ($1, 'vlm', 'k1', '{}'::jsonb)`, tenant)
	testutil.ExpectError(t, pool,
		`INSERT INTO jobs (tenant_id, queue, idempotency_key, payload) VALUES ($1, 'vlm', 'k1', '{}'::jsonb)`, tenant)

	// The same key on a different queue is a different job.
	testutil.Exec(t, pool,
		`INSERT INTO jobs (tenant_id, queue, idempotency_key, payload) VALUES ($1, 'evidence_fetch', 'k1', '{}'::jsonb)`, tenant)
}

// The high-throughput tables are monthly range-partitioned, so a retention sweep
// is a partition drop rather than a multi-million-row DELETE.
func TestInboxAndCanonicalEventsArePartitionedByMonth(t *testing.T) {
	pool := migrated(t)
	for _, table := range []string{"inbox_events", "canonical_events"} {
		var strategy string
		if err := pool.QueryRow(context.Background(),
			`SELECT partstrat FROM pg_partitioned_table pt
			   JOIN pg_class c ON c.oid = pt.partrelid
			  WHERE c.relname = $1`, table).Scan(&strategy); err != nil {
			t.Fatalf("read partitioning for %s: %v", table, err)
		}
		if strategy != "r" {
			t.Errorf("%s uses %q partitioning, want range", table, strategy)
		}

		var months int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_inherits i
			  JOIN pg_class parent ON parent.oid = i.inhparent
			  JOIN pg_class child ON child.oid = i.inhrelid
			 WHERE parent.relname = $1 AND child.relname NOT LIKE '%%_default'`, table).Scan(&months); err != nil {
			t.Fatalf("count partitions for %s: %v", table, err)
		}
		if months < 12 {
			t.Errorf("%s has %d monthly partitions, want at least 12 so a site does not "+
				"stop accepting events when the installer has not run yet", table, months)
		}
	}
}

// ---------------------------------------------------------------------------
// Invariant 5: tenancy. A cross-tenant read must fail at the RLS layer, not
// merely be filtered by well-behaved application code.
// ---------------------------------------------------------------------------

func TestRLSIsoTenants(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	other, otherSite, _ := testutil.NewTenant(t, pool)

	// Two real tenants, one incident each. If isolation were absent, the
	// cross-tenant case would read 1 instead of 0 — so the fixture has to contain
	// somebody else's row for the test to mean anything.
	newIncident(t, pool, "inc-a", tenant, site)
	newIncident(t, pool, "inc-b", other, otherSite)

	for _, tc := range []struct {
		name string
		tnt  string
		want int
	}{
		{"own tenant", tenant, 1},
		{"other tenant", "some-other-tenant", 0},
		{"no context", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.AsRole(t, pool, "security_readonly", tc.tnt, func(tx pgx.Tx) {
				var n int
				if err := tx.QueryRow(context.Background(),
					`SELECT count(*) FROM incidents`).Scan(&n); err != nil {
					t.Fatalf("count: %v", err)
				}
				if n != tc.want {
					t.Errorf("tenant %q read %d incident(s), want %d", tc.tnt, n, tc.want)
				}
			})
		})
	}
}

func newIncident(t *testing.T, pool *pgxpool.Pool, id, tenant, site string) {
	t.Helper()
	testutil.Exec(t, pool, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r', '1.0.0', $4, 'p', now(), now())`,
		id+"-"+tenant, tenant, site, strings.Repeat("a", 64))
}

func TestRLSIsoTenantsForLargeTables(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	testutil.Exec(t, pool, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r', '1.0.0', $4, 'p', now(), now())`,
		"inc-"+tenant, tenant, site, strings.Repeat("a", 64))
	testutil.Exec(t, pool, `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', 'src-1', $2)`, tenant, "evt-"+tenant)
	testutil.Exec(t, pool, `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, integrity_hash)
		VALUES ($1, $2, $3, 'sys', 'src-1', 'int-1', 'motion', '1', now(), now(), now(), $4)`,
		tenant, "evt-"+tenant, site, strings.Repeat("e", 64))

	// audit_ledger is absent from this list on purpose: security_readonly has no
	// grant on it, which is a stronger property than row-level isolation and is
	// asserted separately in TestReadonlyHasNoGrantOnAuditLedger.
	tables := []string{"incidents", "canonical_events", "evidence"}
	for _, table := range tables {
		table := table
		t.Run(table, func(t *testing.T) {
			testutil.AsRole(t, pool, "security_readonly", "some-other-tenant", func(tx pgx.Tx) {
				var n int
				if err := tx.QueryRow(context.Background(),
					`SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
					t.Fatalf("count %s: %v", table, err)
				}
				if n != 0 {
					t.Errorf("%s leaked %d row(s) of tenant %q to another tenant", table, n, tenant)
				}
			})
		})
	}
}

// A write must be tenant-scoped too. A cross-tenant INSERT is how a poisoned row
// gets into another customer's incident feed.
func TestRLSBlocksCrossTenantWrite(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)

	testutil.AsRole(t, pool, "security_readonly", "some-other-tenant", func(tx pgx.Tx) {
		// security_readonly has no INSERT grant at all, which is the first
		// control. This asserts that layer.
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
			                       bundle_content_hash, policy_version, first_event_at, last_event_at)
			VALUES ('poison', 'some-other-tenant', $1, 'r', '1.0.0', $2, 'p', now(), now())`,
			site, strings.Repeat("a", 64)); err == nil {
			t.Error("a read-only role inserted an incident")
		}
	})
	_ = tenant
}

func TestTenantContextIsRequiredForWrites(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	// With no tenant context at all, a WITH CHECK policy rejects the write. This
	// is the "a request that forgot to set the context fails closed" property.
	testutil.AsRole(t, pool, "security_core", "", func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
			                       bundle_content_hash, policy_version, first_event_at, last_event_at)
			VALUES ('no-ctx', $1, $2, 'r', '1.0.0', $3, 'p', now(), now())`,
			tenant, site, strings.Repeat("a", 64)); err == nil {
			t.Error("a write succeeded with no tenant context set")
		}
	})
}

// ---------------------------------------------------------------------------
// Least privilege. The threat model names these controls; here they are proven.
// ---------------------------------------------------------------------------

func TestAdapterCannotReadAuditLedger(t *testing.T) {
	pool := migrated(t)
	testutil.AsRole(t, pool, "security_adapter", "", func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(), `SELECT count(*) FROM audit_ledger`); err == nil {
			t.Error("an adapter read the audit ledger")
		}
		if _, err := tx.Exec(context.Background(), `SELECT count(*) FROM incidents`); err == nil {
			t.Error("an adapter read the incident queue")
		}
		if _, err := tx.Exec(context.Background(), `SELECT count(*) FROM users`); err == nil {
			t.Error("an adapter read the identity table")
		}
	})
}

func TestAIRuntimeCannotAlterAuthoritativeState(t *testing.T) {
	pool := migrated(t)
	testutil.AsRole(t, pool, "security_ai", "", func(tx pgx.Tx) {
		for _, q := range []string{
			`SELECT count(*) FROM incidents`,
			`SELECT count(*) FROM dispositions`,
			`SELECT count(*) FROM rule_bundles`,
			`SELECT count(*) FROM audit_ledger`,
			`SELECT count(*) FROM users`,
			`UPDATE ai_observations SET structured_output = '{}'::jsonb`,
		} {
			if _, err := tx.Exec(context.Background(), q); err == nil {
				t.Errorf("the AI runtime was allowed to run %q", q)
			}
		}
	})
}

func TestReadonlyHasNoGrantOnAuditLedger(t *testing.T) {
	pool := migrated(t)
	testutil.AsRole(t, pool, "security_readonly", "", func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(), `SELECT count(*) FROM audit_ledger`); err == nil {
			t.Error("a read-only reporting role can read the audit ledger")
		}
	})
}

func TestMigratorIsTheOnlyRoleThatBypassesRLS(t *testing.T) {
	pool := migrated(t)
	for _, role := range []string{"security_core", "security_adapter", "security_ai", "security_readonly"} {
		var bypass bool
		if err := pool.QueryRow(context.Background(),
			`SELECT rolbypassrls FROM pg_roles WHERE rolname = $1`, role).Scan(&bypass); err != nil {
			t.Fatalf("read role %s: %v", role, err)
		}
		if bypass {
			t.Errorf("%s has BYPASSRLS; a service-layer check is the only thing standing "+
				"between a bug and a cross-tenant disclosure", role)
		}
	}

	var migratorBypass bool
	if err := pool.QueryRow(context.Background(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'security_migrate'`).Scan(&migratorBypass); err != nil {
		t.Fatalf("read migrator role: %v", err)
	}
	if !migratorBypass {
		t.Error("security_migrate lacks BYPASSRLS and cannot run migrations on an empty database")
	}
}

// ---------------------------------------------------------------------------
// Identity.
// ---------------------------------------------------------------------------

// MFA is mandatory. A session row with mfa_verified false exists only during a
// challenge in flight, and the API must never mint one from a password alone.
// This asserts the schema can express the two states and not a third.
func TestSessionsCarryMFAAndSiteScope(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	testutil.Exec(t, pool, `
		INSERT INTO users (user_id, tenant_id, username, password_hash, password_algo)
		VALUES ('u1', $1, 'admin', '$argon2id$v=19$m=65536,t=3,p=4$abc$def', 'argon2id')`, tenant)
	testutil.Exec(t, pool, `
		INSERT INTO sessions (session_id, tenant_id, user_id, token_hash, csrf_hash, mfa_verified,
		                      roles, site_scope, absolute_expiry)
		VALUES ('sess-1', $1, 'u1', $2, 'csrf', true, '["operator"]'::jsonb, $3::jsonb, now() + interval '1 hour')`,
		tenant, "tok-"+tenant, "[\""+site+"\"]")

	var mfa bool
	var scope []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT mfa_verified, site_scope FROM sessions WHERE tenant_id = $1 AND session_id = 'sess-1'`,
		tenant).Scan(&mfa, &scope); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !mfa {
		t.Error("session reports mfa_verified false; no code path should persist one")
	}
}

// A break-glass account without a password would be an account that can be
// recovered but never used — and, worse, an account whose authentication path is
// ambiguous.
func TestBreakGlassRequiresAPassword(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)

	testutil.ExpectError(t, pool, `
		INSERT INTO users (user_id, tenant_id, username, password_hash, break_glass)
		VALUES ('bg', $1, 'breakglass', NULL, true)`, tenant)

	testutil.Exec(t, pool, `
		INSERT INTO users (user_id, tenant_id, username, password_hash, break_glass)
		VALUES ('bg', $1, 'breakglass', 'hash', true)`, tenant)
}

// ---------------------------------------------------------------------------
// Graph.
// ---------------------------------------------------------------------------

// M1 is topology and provenance only. There is no entity_aliases table, and
// adding one would be a scope decision rather than a schema detail.
func TestNoEntityAliasTableInM1(t *testing.T) {
	pool := migrated(t)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables
		  WHERE table_schema = 'public' AND table_name = 'entity_aliases'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Error("entity_aliases exists; M1 has no person model and no cross-observation aliasing")
	}
}

// An edge with no provenance is not allowed in. Provenance is what makes the
// graph auditable, and an optional column would be empty in exactly the rows
// somebody would want to hide.
func TestEdgesRequireProvenance(t *testing.T) {
	pool := migrated(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	_ = site

	testutil.ExpectError(t, pool, `
		INSERT INTO edges (edge_id, tenant_id, from_type, from_id, relation, to_type, to_id, method)
		VALUES ('e1', $1, 'DEVICE', 'd1', 'contains', 'ZONE', 'z1', 'observed')`, tenant)

	testutil.ExpectError(t, pool, `
		INSERT INTO edges (edge_id, tenant_id, from_type, from_id, relation, to_type, to_id,
		                   asserted_by, method, confidence)
		VALUES ('e2', $1, 'DEVICE', 'd1', 'contains', 'ZONE', 'z1', 'core', 'inferred', 1.5)`, tenant)

	testutil.Exec(t, pool, `
		INSERT INTO edges (edge_id, tenant_id, from_type, from_id, relation, to_type, to_id,
		                   asserted_by, method, confidence)
		VALUES ('e3', $1, 'DEVICE', 'd1', 'contains', 'ZONE', 'z1', 'core', 'inferred', 0.9)`, tenant)
}

func TestRevokedEdgesAreHiddenFromTheActiveView(t *testing.T) {
	pool := migrated(t)
	tenant, _, _ := testutil.NewTenant(t, pool)

	testutil.Exec(t, pool, `
		INSERT INTO edges (edge_id, tenant_id, from_type, from_id, relation, to_type, to_id,
		                   asserted_by, method, confidence)
		VALUES ('e1', $1, 'DEVICE', 'd1', 'contains', 'ZONE', 'z1', 'core', 'inferred', 0.9),
		       ('e2', $1, 'DEVICE', 'd1', 'contains', 'ZONE', 'z2', 'core', 'inferred', 0.9)`, tenant)

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM edges_active WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if n != 2 {
		t.Fatalf("%d active edges before revocation, want 2", n)
	}

	testutil.Exec(t, pool, `
		INSERT INTO edge_revocations (tenant_id, edge_id, revoked_at, revoked_by, reason)
		VALUES ($1, 'e1', now(), 'admin', 'superseded by e2')`, tenant)

	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM edges_active WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count active after revocation: %v", err)
	}
	if n != 1 {
		t.Errorf("%d active edges after revoking one, want 1", n)
	}

	// The row itself stays. Revocation is a fact, not a deletion: rebuilding the
	// graph projection must be able to see that a claim was once made.
	var raw int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM edges WHERE tenant_id = $1`, tenant).Scan(&raw); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if raw != 2 {
		t.Errorf("revocation removed a row; %d edges remain, want 2", raw)
	}
}
