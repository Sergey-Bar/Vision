package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// Migration 0015 exists because 0002 and 0003 both ended their monthly
// partitions at 2027-01-01 with no DEFAULT. These two tests are the control.
//
// The first asserts the specific boundary that was about to kill intake: a row
// dated inside 2027 must be accepted by a named partition. The second asserts
// the property that stops it recurring: a row dated years out must be accepted
// at all. Before 0015 both statements failed with "no partition of relation
// found for row", which on the product's own write path is a refused ingest.
func TestInboxAndCanonicalAcceptRowsBeyondTheOldPartitionBoundary(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, pool)

	for _, tc := range []struct {
		name       string
		receivedAt string
	}{
		{"inside the newly declared months", "2027-06-01T12:00:00Z"},
		{"far past any declared month", "2031-01-01T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertInboxRowAccepted(t, pool, tenant, site, tc.receivedAt)
			assertCanonicalRowAccepted(t, pool, tenant, site, tc.receivedAt)
		})
	}
}

// Every partition carries the tenant and site policies. PostgreSQL applies a
// partition's own policies and not the parent's, so a partition created without
// them is an unfiltered relation - the kind of hole that only shows up after an
// incident. This asserts the isolation is present on the 2027 and DEFAULT
// partitions, not just on the 2026 ones 0008 happened to see.
func TestNewPartitionsCarryTenantAndSitePolicies(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, pool)

	partitions := []string{
		"inbox_events_2027_06", "inbox_events_default",
		"canonical_events_2027_06", "canonical_events_default",
		"recording_segments_2027_06", "recording_segments_default",
	}
	for _, part := range partitions {
		t.Run(part, func(t *testing.T) {
			var forced bool
			if err := pool.QueryRow(ctx,
				`SELECT relrowsecurity AND relforcerowsecurity
				   FROM pg_class WHERE oid = $1::regclass`, part).Scan(&forced); err != nil {
				t.Fatalf("%s: %v", part, err)
			}
			if !forced {
				t.Errorf("%s does not have row-level security forced; a partition without "+
					"FORCE is bypassed by its owner and is readable by anyone with a grant", part)
			}

			var policies []string
			rows, err := pool.Query(ctx,
				`SELECT polname FROM pg_policy WHERE polrelid = $1::regclass ORDER BY polname`, part)
			if err != nil {
				t.Fatalf("%s policies: %v", part, err)
			}
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					t.Fatalf("scan: %v", err)
				}
				policies = append(policies, n)
			}
			_ = rows.Err()
			rows.Close()
			for _, want := range []string{"site_isolation", "tenant_isolation"} {
				if !contains(policies, want) {
					t.Errorf("%s has policies %v, missing %s", part, policies, want)
				}
			}
		})
	}

	// Behaviour, not just catalogue: a second tenant's row is invisible to the
	// first under the product role.
	otherTenant, _, _ := testutil.NewTenant(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO inbox_events (tenant_id, integration_id, source_event_id, site_id,
		                          payload, payload_hash, received_at)
		VALUES ($1, 'i', $2, $3, '{}'::jsonb, repeat('a', 64), '2027-06-01T00:00:00Z')`,
		otherTenant, "src-"+otherTenant, site); err != nil {
		t.Fatalf("insert second tenant's row: %v", err)
	}

	var visible int
	testutil.AsRole(t, pool, "security_core", tenant, []string{site}, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM inbox_events WHERE tenant_id = $1`, otherTenant).Scan(&visible); err != nil {
			t.Fatalf("count as security_core: %v", err)
		}
	})
	if visible != 0 {
		t.Errorf("a second tenant's inbox rows are visible under security_core (%d rows); "+
			"the new partition is not isolated", visible)
	}
}

func assertInboxRowAccepted(t *testing.T, pool *pgxpool.Pool, tenant, site, receivedAt string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO inbox_events (tenant_id, integration_id, source_event_id, site_id,
		                          payload, payload_hash, received_at)
		VALUES ($1, 'i', $2, $3, '{}'::jsonb, repeat('b', 64), $4::timestamptz)`,
		tenant, "src-"+receivedAt, site, receivedAt); err != nil {
		t.Fatalf("inbox insert at %s: %v", receivedAt, err)
	}
}

func assertCanonicalRowAccepted(t *testing.T, pool *pgxpool.Pool, tenant, site, at string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at,
		                              received_at, normalized_at, integrity_hash)
		VALUES ($1, $2, $3, 'sys', $2, 'i', 'motion', '1', $4::timestamptz, $4::timestamptz,
		        $4::timestamptz, repeat('c', 64))`,
		tenant, "e-"+at, site, at); err != nil {
		t.Fatalf("canonical insert at %s: %v", at, err)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

var _ = pgx.Tx(nil)
