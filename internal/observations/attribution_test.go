package observations_test

// C1's red-check: the attribution constraint on `ai_observations`.
//
// Migration 0022 dropped `model_hash NOT NULL` so that a hosted provider — whose
// weights are not ours, not addressable, and change on its own schedule — can be
// attributed without a fabricated hash. Dropping a `NOT NULL` is the kind of change
// that looks like a relaxation and is actually a substitution, and a substitution
// whose replacement goes unenforced is worse than the lie it replaced: a reader who
// sees `model_hash` nullable learns that an observation may have no attribution at
// all, and that is false.
//
// So the test below is three assertions and a removal:
//
//   - a row with no attribution at all is refused
//   - a row naming the weights by hash is accepted  (the local runtime)
//   - a row naming the provider and the pinned contract version is accepted
//
// and then `TestRedCheckRemovingTheConstraintStopsRefusingAnUnattributedRow` drops
// the constraint and asserts the refusal stops. Without that last one the three
// assertions prove the constraint exists, not that it is the reason.

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// migrated brings this package's own database to the current version, once.
//
// The per-package database starts empty — that is what makes it a package's own —
// so a test that writes rows has to create the schema first. Idempotent, and cheap
// after the first call.
var migrated sync.Once

func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testutil.Pool(t)
	migrated.Do(func() {
		if _, err := (&migrate.Runner{Pool: pool}).Up(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	})
	return pool
}

// insertObservation inserts the minimum a row needs, with the attribution the case
// is about. Every other NOT NULL column is given a placeholder because the
// assertion is about attribution and nothing else.
//
// `hash`, `identity` and `contract` are passed as strings, never as NULL: 0022
// declared the two new columns `NOT NULL DEFAULT ”` because the Go layer writes
// `""` for a provider that has nothing to report, and a NULL here would be refused
// by the NOT NULL rather than by the constraint under test — which would make every
// case below pass for the wrong reason.
func insertObservation(ctx context.Context, t *testing.T, tenant, site, id, hash, identity, contract string) error {
	t.Helper()
	pool := testutil.Pool(t)
	incidentID := "inc-" + id
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ($1, $2, $3, 'r.attr', '1.0.0', $4, 'p1', now(), now())
		ON CONFLICT DO NOTHING`,
		incidentID, tenant, site, strings.Repeat("a", 64)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO ai_observations
		     (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		      model, model_hash, model_identity, model_contract_version,
		      runtime_version, prompt_template_id, prompt_version,
		      output_schema_version, structured_output, output_hash)
		 VALUES ($1, $2, $3, $4, 'evt', 'prof',
		         'some-model', $5, $6, $7,
		         'rt', 'tpl', 'pv', 1, '{}'::jsonb, 'out')`,
		id, tenant, site, incidentID, hash, identity, contract)
	return err
}

func TestAnObservationWithNoAttributionAtAllIsRefused(t *testing.T) {
	pool := migratedPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	// Empty hash, no provider name, no contract version: nothing says which model
	// answered. This is the row the NOT NULL existed to refuse.
	err := insertObservation(ctx, t, tenant, site, "no-attribution", "", "", "")
	if err == nil {
		t.Fatal("an observation with no attribution was accepted. The column was NOT NULL for a " +
			"reason: an observation that cannot say which model answered cannot be replayed or " +
			"audited, and 0022 substituted a second honest way to be attributable rather than " +
			"removing the requirement")
	}
}

func TestALocalRuntimeObservationNamingItsWeightsIsAccepted(t *testing.T) {
	pool := migratedPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	// A 64-character content signature over a bundle we sign ourselves. This is the
	// original case and it must still work, or 0022 broke every local observation
	// rather than adding a hosted one.
	if err := insertObservation(ctx, t, tenant, site, "local-hash",
		"0000000000000000000000000000000000000000000000000000000000000000", "", ""); err != nil {
		t.Fatalf("an observation naming its weights by hash was refused: %v", err)
	}
	_ = pool
}

func TestAHostedObservationNamingItsProviderIsAccepted(t *testing.T) {
	pool := migratedPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	// The case 0022 exists for: no hash, because the provider has none to give, and
	// instead a claim plus this build's pinned contract version.
	if err := insertObservation(ctx, t, tenant, site, "hosted-claim", "", "provider-model-x", "v1:abc123"); err != nil {
		t.Fatalf("a hosted observation naming its provider was refused: %v", err)
	}
}

func TestAHalfAttributionIsStillRefused(t *testing.T) {
	pool := migratedPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	// The most likely wrong implementation: requiring *a* provider name rather than
	// both halves of the claim. A name with no pinned contract version says which
	// model the provider called itself this week, and nothing about what was asked
	// of it — so two rows with the same name and different prompts are
	// indistinguishable, which is the exact replay failure the hash prevented.
	for _, tc := range []struct{ name, identity, contract string }{
		{"name-only", "provider-model-x", ""},
		{"contract-only", "", "v1:abc123"},
	} {
		if err := insertObservation(ctx, t, tenant, site, tc.name, "", tc.identity, tc.contract); err == nil {
			t.Errorf("%s was accepted. Half an attribution is not attribution", tc.name)
		}
	}
}

// TestRedCheckRemovingTheConstraintStopsRefusingAnUnattributedRow is the red-check
// proper: it removes the control and asserts the refusals stop. If the tests above
// passed for some reason other than the constraint — a trigger, a permissions
// failure, a NOT NULL still in place — this is the one that notices.
//
// The constraint is restored in a `t.Cleanup`, because a test that leaves the
// schema weaker than it found it is a test that breaks the next one in the package.
func TestRedCheckRemovingTheConstraintStopsRefusingAnUnattributedRow(t *testing.T) {
	pool := migratedPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `ALTER TABLE ai_observations DROP CONSTRAINT ai_observations_attribution_ck`); err != nil {
		t.Fatalf("drop the constraint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			ALTER TABLE ai_observations ADD CONSTRAINT ai_observations_attribution_ck
			CHECK (
			    COALESCE(model_hash, '') <> ''
			    OR (COALESCE(model_identity, '') <> ''
			        AND COALESCE(model_contract_version, '') <> '')
			)`)
	})

	// Unique per run. The package's database persists between runs, so a stable id
	// makes the second run's insert fail on a duplicate primary key -- and a red-check
	// that only passes the first time it is run is not a red-check, it is a coincidence
	// that the harness noticed.
	err := insertObservation(ctx, t, tenant, site, "red-check-"+strconv.FormatInt(time.Now().UnixNano(), 10), "", "", "")
	if err != nil {
		// Naming the error is the point of this test. "Still refused" without the
		// reason is the report this whole gate exists to stop being able to give.
		t.Fatalf("with the constraint removed the row was still refused, so something other than "+
			"ai_observations_attribution_ck is the control and the four tests above are not evidence: %v", err)
	}
	t.Log("the control was removed and the row was accepted, which is what makes the four tests " +
		"above evidence rather than a description of the schema")
}
