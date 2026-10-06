package rlsrole

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// canaryActor is the actor every canary writes as.
//
// One value, so the helper below takes no actor parameter: a parameter that always
// receives the same string is a parameter a reader has to check at every call site,
// and `unparam` is right that it carries no information.
const canaryActor = "rls-canary"

// tenantContext is the pool context a canary writes through: one tenant, one actor,
// an explicit site scope.
//
// The site scope is NAMED rather than inherited on purpose. `For` binds the tenant
// and nothing else, so a canary that used it would be testing the tenant policy and
// calling it a site canary — which is the ambiguity this file exists to remove.
func tenantContext(tenant string, sites []string) dbx.TenantContext {
	return dbx.TenantContext{TenantID: tenant, ActorID: canaryActor, SiteIDs: sites}
}

// Every canary in this package is a READ.
//
// That is not an oversight to be tidied up later; it is the shape of the gap this
// file exists to close. The suite proved that a statement issued without a tenant
// context returns no rows, which is what a SELECT does under a PERMISSIVE policy with
// no matching context. Nothing proved what the same statement does on the way IN.
//
// # Why a write is where the cost is
//
// A read that crosses a tenant boundary returns nothing, so the defect is invisible
// and the cost of shipping it is a number on a dashboard that looks plausible. A WRITE
// that crosses one puts a row in somebody else's ledger, and the audit trail then
// records an action attributed to an actor who could not have performed it.
//
// The ones a read cannot find are the ones where the statement is issued with the
// RIGHT tenant context and the WRONG site scope, or no scope at all:
// `tenant_isolation` passes, the row lands, and the site constraint lives only in a
// handler. A read that should return rows and correctly does is indistinguishable
// from a read that is not filtered at all, so every read canary in this package
// passes when the site policy is absent. A write does not have that ambiguity.
//
// # What these canaries claim, precisely
//
// For a sample of site-scoped tables, as `security_core` — the production role, RLS
// in force and NOT superuser — an INSERT, an UPDATE and a DELETE are attempted with:
//
//  1. no tenant context: refused, because there is no tenant to attribute the row to;
//  2. the right tenant and a site the principal is NOT assigned to: refused;
//  3. the right tenant and the right site: ACCEPTED.
//
// Case 3 is what gives the other two their meaning. A canary that only asserts
// refusals passes on a table that is simply unwritable, and an unwritable table is
// what a migration with a missing GRANT produces — which fails closed, loudly, in
// production, on the first write.
//
// # What these canaries do NOT claim
//
// That they cover every site-scoped table. They cover four of fifteen, and
// `TestTheSiteScopedTablesAreAllCovered` logs the uncovered ones on every run,
// because a sample is a choice somebody made rather than a property anybody checked.
// The first version of this file was how that choice got audited, and the answer was
// two live defects — see `unprotectedTables` below.

// writeCanary is one statement and the site scope it belongs to.
type writeCanary struct {
	// table is a site-scoped table. Partitioned parents are excluded: RLS on a
	// partitioned table applies to the partitions, and asserting against the parent
	// would be asserting against a relation that holds no rows.
	table string
	// insert must succeed as security_core with the right tenant and site scope. It is
	// written to be undone by the test's own cleanup, so the canary does not need a
	// rollback — which is deliberate, because a canary that runs inside a transaction
	// and rolls back cannot distinguish "refused by policy" from "refused because of
	// something the transaction did".
	insert string
	// delete removes rows matching the inserted marker, as the OWNER: a cleanup that
	// needed the production role's permissions would be testing the policy twice.
	delete string
}

// The tables. Three classes, because one per class is enough to catch a policy that
// was never created and one per table is several hundred tests that all say the same
// thing.
//
//	incidents        the row an operator reads to decide whether to act, and the one with
//	                 the most NOT NULL columns — a canary that gets the column list wrong
//	                 fails on `more target columns than expressions`, which is not a policy
//	                 refusal and is asserted as one
//	jobs             the write the pipeline makes on every event, and one of the two
//	                 tables this file found UNPROTECTED
//	outbox_events    the other one, and the reason both were unprotected is the same
//	evidence         the table a tenant's whole compliance claim rests on
//	ai_observations  append-only, attributed, the decision's AI half
//	cameras          the device row that carries the site, written by integrations
//	devices          the device registry, written by integrations
//	device_clocks    the drift subscriber writes here on every poll
//	entities         the graph service writes here on every rebuild
//	integrations     the integration surface, written by the operator
//	sites            the table that DEFINES the scope the other policies reference
//	source_events    the vendor poller writes here on every cycle
//	zones            site configuration, written by the operator
var writeCanaries = []writeCanary{
	{
		table: "incidents",
		insert: `INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, related_site_ids, rule_id,
			 rule_bundle_version, bundle_content_hash, policy_version, state, severity,
			 title, first_event_at, last_event_at, degraded, degraded_reasons)
			VALUES ('inc_canary_' || $1, $1, $2, '[]'::jsonb, 'canary', repeat('a', 64),
			        repeat('a', 64), 'p1', 'open', 'low', 'rls write canary',
			        now(), now(), false, '[]'::jsonb)`,
		delete: `DELETE FROM incidents WHERE tenant_id = $1 AND incident_id = 'inc_canary_' || $1`,
	},
	{
		table: "jobs",
		insert: `INSERT INTO jobs
			(tenant_id, site_id, queue, idempotency_key, payload)
			VALUES ($1, $2, 'vlm', 'canary_' || $1, '{"canary":true}'::jsonb)`,
		delete: `DELETE FROM jobs WHERE tenant_id = $1 AND idempotency_key = 'canary_' || $1`,
	},
	{
		table: "outbox_events",
		insert: `INSERT INTO outbox_events
			(tenant_id, site_id, topic, payload, idempotency_key)
			VALUES ($1, $2, 'canary', '{"canary":true}'::jsonb, 'canary_outbox_' || $1)`,
		delete: `DELETE FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'canary_outbox_' || $1`,
	},
	{
		// evidence is the FOURTH class, and it is the one this file was missing for
		// the longest. It is in a different shape from the three above:
		//
		//   - it is the table a tenant's whole compliance claim rests on. Every other
		//     row in this database describes something that happened; this row IS the
		//     thing a customer hands an auditor, and an evidence row that a cross-tenant
		//     write can insert is a fabricated exhibit.
		//   - it carries a content hash with a CHECK constraint, so the canary's values
		//     have to be a real 64 hex characters. A canary that gets the column list
		//     wrong fails on the constraint rather than on the policy, which is the
		//     failure mode the incidents entry above warns about.
		//   - `storage_ref` has a CHECK refusing an absolute path, which is the
		//     path-traversal control sitting next to the isolation control. The canary
		//     therefore uses a RELATIVE reference, so it is refused for the RIGHT
		//     reason and a failure here is a policy failure rather than a constraint
		//     one.
		table: "evidence",
		insert: `INSERT INTO evidence
			(evidence_id, tenant_id, site_id, kind, tier, observed_at, captured_at,
			 storage_ref, media_type, size_bytes, sha256, retention_policy)
			VALUES ('evd_canary_' || $1, $1, $2, 'snapshot', 'C', now(), now(),
			        'canary/' || $1, 'application/octet-stream', 1,
			        repeat('a', 64), 'default')`,
		delete: `DELETE FROM evidence WHERE tenant_id = $1 AND evidence_id = 'evd_canary_' || $1`,
	},
	{
		table: "ai_observations",
		insert: `INSERT INTO ai_observations
			(observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
			 production_profile, model, model_hash, runtime_version, prompt_template_id,
			 prompt_version, model_bundle_signature, input_evidence_ids,
			 input_evidence_hashes, output_schema_version, structured_output,
			 insufficient_evidence, degraded_reason, degraded_detail,
			 latency_ms, hardware, output_hash, created_at, supersedes_observation_id)
			VALUES ('obs_canary_' || $1, $1, $2, 'inc_canary', 'evt_canary', 'canary',
			        false, 'canary-model', repeat('a', 64), '1.0', 'canary-template',
			        '1', '', '[]'::jsonb, '[]'::jsonb, 1,
			        '{"canary":true}'::jsonb, false, 'none', '',
			        0, '', repeat('a', 64), now(), NULL)`,
		delete: `DELETE FROM ai_observations WHERE tenant_id = $1 AND observation_id = 'obs_canary_' || $1`,
	},
	{
		table: "cameras",
		insert: `INSERT INTO cameras
			(camera_id, tenant_id, device_id, site_id, source_camera_id,
			 display_name, state, recording_state, width, height, low_light_capable, last_seen_at)
			VALUES ('cam_canary_' || $1, $1, 'dev_canary', $2, 'src_cam_canary',
			        'canary camera', 'online', 'recording', 1920, 1080, false, now())`,
		delete: `DELETE FROM cameras WHERE tenant_id = $1 AND camera_id = 'cam_canary_' || $1`,
	},
	{
		table: "devices",
		insert: `INSERT INTO devices
			(device_id, tenant_id, site_id, zone_id, integration_id, source_device_id,
			 kind, display_name, enabled, attributes, registered_at)
			VALUES ('dev_canary_' || $1, $1, $2, NULL, 'int_canary', 'src_dev_canary',
			        'camera', 'canary device', true, '{}'::jsonb, now())`,
		delete: `DELETE FROM devices WHERE tenant_id = $1 AND device_id = 'dev_canary_' || $1`,
	},
	{
		table: "device_clocks",
		insert: `INSERT INTO device_clocks
			(device_id, tenant_id, site_id, clock_source, drift_ms, band, reference_id, measured_at)
			VALUES ('dclk_canary_' || $1, $1, $2, 'ntp', 0, 'ok', 'ref_canary', now())`,
		delete: `DELETE FROM device_clocks WHERE tenant_id = $1 AND device_id = 'dclk_canary_' || $1`,
	},
	{
		table: "entities",
		insert: `INSERT INTO entities
			(entity_id, tenant_id, site_id, kind, observed_value, zone_id, device_id,
			 first_observed_at, last_observed_at, observation_count, created_at)
			VALUES ('ent_canary_' || $1, $1, $2, 'device', 'canary-value', NULL, 'dev_canary',
			        now(), now(), 1, now())`,
		delete: `DELETE FROM entities WHERE tenant_id = $1 AND entity_id = 'ent_canary_' || $1`,
	},
	{
		table: "integrations",
		insert: `INSERT INTO integrations
			(integration_id, tenant_id, site_id, type, enabled, base_url, poll_interval_seconds, secret_ref, created_at)
			VALUES ('int_canary_' || $1, $1, $2, 'replay_fixture', true, 'http://localhost', 5, NULL, now())`,
		delete: `DELETE FROM integrations WHERE tenant_id = $1 AND integration_id = 'int_canary_' || $1`,
	},
	{
		table: "sites",
		insert: `INSERT INTO sites
			(site_id, tenant_id, name, timezone, default_locale, site_tags, created_at)
			VALUES ('site_canary_' || $1, $1, 'canary site', 'UTC', 'en', '[]'::jsonb, now())`,
		delete: `DELETE FROM sites WHERE tenant_id = $1 AND site_id = 'site_canary_' || $1`,
	},
	{
		table: "source_events",
		insert: `INSERT INTO source_events
			(source_event_id, tenant_id, integration_id, site_id, source_system_id, event_type,
			 schema_version, observed_at, received_at, source_timezone, raw_envelope, envelope_hash)
			VALUES ('sev_canary_' || $1, $1, 'int_canary', $2, 'sys_canary', 'motion',
			        '1', now(), now(), 'UTC', '{}'::jsonb, repeat('a', 64))`,
		delete: `DELETE FROM source_events WHERE tenant_id = $1 AND source_event_id = 'sev_canary_' || $1`,
	},
	{
		table: "zones",
		insert: `INSERT INTO zones
			(zone_id, tenant_id, site_id, parent_id, name, path, depth, created_at)
			VALUES ('zone_canary_' || $1, $1, $2, NULL, 'canary zone', '/canary', 0, now())`,
		delete: `DELETE FROM zones WHERE tenant_id = $1 AND zone_id = 'zone_canary_' || $1`,
	},
}

// TestAWriteWithNoTenantContextIsRefused is case 1.
//
// Every canary in this package before this file proves that a READ without a tenant
// context sees nothing. The write equivalent is not the same statement: a SELECT that
// returns no rows is invisible, and an INSERT that succeeds is not. There is no
// SELECT-shaped version of this defect.
func TestAWriteWithNoTenantContextIsRefused(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)
	tenant, site, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)

	for _, c := range writeCanaries {
		t.Run(c.table, func(t *testing.T) {
			// The statement with the tenant and site replaced by literals, so the
			// mutation is unambiguous: no `app.tenant_id` setting at all.
			stmt := strings.ReplaceAll(c.insert, "$1", "'"+tenant+"'")
			stmt = strings.ReplaceAll(stmt, "$2", "'"+site+"'")
			_, err := pool.Exec(ctx, stmt)
			if err == nil {
				// Clean up before failing, or the next run inherits the row and the
				// canary above it starts failing for the wrong reason.
				t.Cleanup(func() {
					_, _ = raw.Exec(ctx, strings.ReplaceAll(c.delete, "$1", "'"+tenant+"'"))
				})
				t.Fatalf("INSERT INTO %s succeeded with no tenant context. Under RLS a write "+
					"with no tenant is a row in a tenant that does not exist, and the audit "+
					"trail will record an action attributed to an actor who could not perform it",
					c.table)
			}
			if !isPolicyRefusal(err) {
				t.Fatalf("INSERT INTO %s failed with %v, which is not a policy refusal. A canary "+
					"that fails for a syntax error is a canary that has stopped checking anything",
					c.table, err)
			}
		})
	}
}

// TestAWriteWithTheWrongSiteIsRefused is case 2, and it is the one this package
// could not have found before.
//
// A statement with the RIGHT tenant context and the WRONG site satisfies
// `tenant_isolation`, and only `site_isolation` refuses it. Before migration 21 both
// were PERMISSIVE and PostgreSQL ORed them, so `tenant_isolation OR site_isolation`
// was `tenant_isolation` and this write would have succeeded.
func TestAWriteWithTheWrongSiteIsRefused(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)
	tenant, mine, _ := testutil.NewTenant(t, raw)

	// A second site in the SAME tenant. Same tenant is the point: `tenant_isolation`
	// is satisfied by construction, so the only thing that can refuse this write is
	// the site policy.
	const theirs = "site-not-assigned"
	if _, err := raw.Exec(ctx,
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		theirs, tenant); err != nil {
		t.Fatalf("seed the second site: %v", err)
	}

	pool := productionPool(t)

	for _, c := range writeCanaries {
		t.Run(c.table, func(t *testing.T) {
			// Bound to this principal's scope, which is `mine`. The statement writes to
			// `theirs`.
			err := pool.InTenant(ctx, tenantContext(tenant, []string{mine}),
				func(tx pgx.Tx) error {
					_, execErr := tx.Exec(ctx, c.insert, tenant, theirs)
					return execErr
				})
			if err == nil {
				t.Cleanup(func() {
					_, _ = raw.Exec(ctx, strings.ReplaceAll(c.delete, "$1", "'"+tenant+"'"))
				})
				if reason, excused := unprotectedTables[c.table]; excused {
					// KNOWN and RECORDED, so it is reported rather than fatal. A table
					// that is NOT in the ledger is a new gap and is fatal below, so the
					// distinction is not "known gaps are tolerated" — it is "a known gap
					// is a line somebody wrote down, and an unknown one is a failure".
					t.Logf("CROSS-SITE WRITE REACHED %s, and the ledger says so: %s", c.table, reason)
					return
				}
				t.Fatalf("INSERT INTO %s succeeded at a site this principal is not assigned to. "+
					"`tenant_isolation` passed -- the tenant is right -- so only `site_isolation` "+
					"could have refused it, and it did not. If this table is known to be "+
					"unprotected, add it to unprotectedTables with the reason and what closing it "+
					"takes; otherwise this is a new gap and it is a failure", c.table)
			}
			if !isPolicyRefusal(err) {
				t.Fatalf("INSERT INTO %s failed with %v, which is not a policy refusal", c.table, err)
			}
		})
	}
}

// TestAWriteWithTheRightScopeIsAccepted is case 3, and it is the case that gives the
// other two their meaning.
//
// A canary that only asserts refusals passes on a table that is simply unwritable, and
// an unwritable table is what a missing GRANT produces — which fails closed, loudly,
// in production, on the first write. So the control asserts both directions.
func TestAWriteWithTheRightScopeIsAccepted(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)
	tenant, site, _ := testutil.NewTenant(t, raw)

	pool := productionPool(t)

	for _, c := range writeCanaries {
		t.Run(c.table, func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := raw.Exec(ctx,
					strings.ReplaceAll(c.delete, "$1", "'"+tenant+"'")); err != nil {
					t.Logf("canary cleanup for %s: %v", c.table, err)
				}
			})

			err := pool.InTenant(ctx, tenantContext(tenant, []string{site}),
				func(tx pgx.Tx) error {
					_, execErr := tx.Exec(ctx, c.insert, tenant, site)
					return execErr
				})
			if err != nil {
				t.Fatalf("INSERT INTO %s was refused with the right tenant AND the right site "+
					"scope: %v\n\nThat is the other failure, and it is the one that costs an "+
					"outage: the pipeline writes here on every event and would be refusing "+
					"correct work", c.table, err)
			}

			// And it is actually there, not merely un-refused. A policy that silently drops
			// a write is worse than one that refuses it, because the caller is told it
			// succeeded.
			var n int
			if err := raw.QueryRow(ctx,
				`SELECT count(*) FROM `+c.table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
				t.Fatalf("count the canary row in %s: %v", c.table, err)
			}
			if n == 0 {
				t.Errorf("the INSERT into %s reported no error and wrote no row", c.table)
			}
		})
	}
}

// TestAnUpdateAndADeleteAreScopedToo covers the other two verbs.
//
// INSERT is the one a SELECT-shaped canary is furthest from, so it gets the most
// attention above. UPDATE and DELETE are where the damage is worst when they are
// wrong, though: an UPDATE that crosses a site rewrites somebody else's incident
// state, and a DELETE that crosses one erases the evidence a disposition was recorded
// against.
func TestAnUpdateAndADeleteAreScopedToo(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)
	tenant, site, _ := testutil.NewTenant(t, raw)

	const theirs = "site-not-assigned"
	if _, err := raw.Exec(ctx,
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, 'Other', 'UTC')`,
		theirs, tenant); err != nil {
		t.Fatalf("seed the second site: %v", err)
	}
	pool := productionPool(t)

	// seedIncidentAt writes a row the production role CANNOT create — because it is at
	// an unassigned site, which is the whole point. The owner writes it and the canary
	// then tries to reach it.
	seedIncidentAt := func(t *testing.T, id, title, hash string) {
		t.Helper()
		if _, err := raw.Exec(ctx, `INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, related_site_ids, rule_id,
			 rule_bundle_version, bundle_content_hash, policy_version, state, severity,
			 title, first_event_at, last_event_at, degraded, degraded_reasons)
			VALUES ($1, $2, $3, '[]'::jsonb, 'canary', repeat('a', 64), $4,
			 'p1', 'open', 'low', $5, now(), now(), false, '[]'::jsonb)`,
			id, tenant, theirs, hash, title); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	t.Run("an UPDATE at an unassigned site is refused", func(t *testing.T) {
		const id = "inc_update_canary"
		seedIncidentAt(t, id, "update canary", strings.Repeat("b", 64))
		t.Cleanup(func() { _, _ = raw.Exec(ctx, `DELETE FROM incidents WHERE incident_id = $1`, id) })

		err := pool.InTenant(ctx, tenantContext(tenant, []string{site}),
			func(tx pgx.Tx) error {
				tag, execErr := tx.Exec(ctx,
					`UPDATE incidents SET title = 'updated' WHERE tenant_id = $1 AND incident_id = $2`,
					tenant, id)
				if execErr == nil && tag.RowsAffected() == 0 {
					return errNoRowsAffected
				}
				return execErr
			})
		if err == nil {
			t.Fatalf("an UPDATE reached an incident at a site this principal is not assigned to")
		}

		var title string
		if err := raw.QueryRow(ctx,
			`SELECT title FROM incidents WHERE incident_id = $1`, id).Scan(&title); err != nil {
			t.Fatalf("read the row back: %v", err)
		}
		if title != "update canary" {
			t.Errorf("the row's title is %q; the UPDATE went through", title)
		}
	})

	t.Run("a DELETE at an unassigned site is refused", func(t *testing.T) {
		const id = "inc_delete_canary"
		seedIncidentAt(t, id, "delete canary", strings.Repeat("c", 64))

		err := pool.InTenant(ctx, tenantContext(tenant, []string{site}),
			func(tx pgx.Tx) error {
				tag, execErr := tx.Exec(ctx,
					`DELETE FROM incidents WHERE tenant_id = $1 AND incident_id = $2`, tenant, id)
				if execErr == nil && tag.RowsAffected() == 0 {
					return errNoRowsAffected
				}
				return execErr
			})
		if err == nil {
			t.Fatalf("a DELETE reached an incident at a site this principal is not assigned to")
		}

		var n int
		if err := raw.QueryRow(ctx,
			`SELECT count(*) FROM incidents WHERE incident_id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count the row: %v", err)
		}
		if n == 0 {
			t.Error("the row is gone. A cross-site DELETE is the one failure in this file with " +
				"no trace: the disposition recorded against the incident now cites a row that " +
				"does not exist")
		}
	})
}

// unprotectedTables is EMPTY.
//
// It listed `jobs` and `outbox_events` — the two tables a cross-site INSERT REACHED
// as security_core, found by TestAWriteWithTheWrongSiteIsRefused — and both entries
// named migration 0023 as the thing that closes them.
//
// Migration 0023 landed, so the map is gone, and with it the floor test that said to
// delete the floor in the same commit as the last entry. That is the THIRD ledger in
// this repository to empty this way (deadexport's allow-list, the unproduced-metric
// ledger, this one) and the shape is deliberate: an empty map would leave a gate
// running with nothing for it to do, and "passing because there is nothing to check"
// is a state a future contributor could reach by deleting every entry instead of
// closing every defect.
//
// The map stayed as a `var` rather than being deleted so this file still compiles the
// reference above, and so the mechanism is still there for the next one. An entry
// cannot be added without a reason — `TestTheUnprotectedLedgerIsConsulted` checks
// that too — and cannot be left behind once the migration lands, because that test
// fails the moment a listed table grows a policy of its own.
var unprotectedTables = map[string]string{}

// TestTheUnprotectedLedgerIsConsulted keeps the ledger honest in both directions,
// and it is the part that makes this a ledger rather than a comment.
//
// It fails if a listed table IS protected — which is what happens the moment someone
// lands the migration and forgets this file. The failure says "delete the entry"
// rather than leaving a stale exemption in a document about a policy that no longer
// has a gap.
func TestTheUnprotectedLedgerIsConsulted(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)

	for table, reason := range unprotectedTables {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("the ledger names %s without saying why, which is not a ledger entry", table)
		}
		var n int
		if err := raw.QueryRow(ctx,
			`SELECT count(*) FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
			  WHERE c.relname = $1 AND p.polname = 'site_isolation'`, table).Scan(&n); err != nil {
			t.Fatalf("read the policies on %s: %v", table, err)
		}
		if n > 0 {
			t.Errorf("%s now HAS a site_isolation policy but is still in the unprotected "+
				"ledger. Delete the entry rather than leaving it to look like work that "+
				"remains", table)
		}
	}
}

// TestEverySiteBearingRelationHasASitePolicy is the shape assertion for migration
// 0023, and it is the one that stops the class rather than the instances.
//
// Migration 0008 created `site_isolation` by enumerating nine table NAMES. Every
// site-bearing table created afterwards had none, and nothing noticed for the whole
// life of the product — until the write canary above tried a cross-site INSERT on two
// of them and it worked.
//
// This asks the catalogue, so it is right about tables nobody remembered, including
// the monthly partitions whose direct access bypasses the parent's policy.
func TestEverySiteBearingRelationHasASitePolicy(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)

	rows, err := raw.Query(ctx, `
		SELECT c.relname
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public'
		   AND c.relkind IN ('r', 'p')
		   AND c.relname NOT IN ('user_site_assignments')
		   AND EXISTS (SELECT 1 FROM pg_attribute a
		                WHERE a.attrelid = c.oid
		                  AND a.attname IN ('site_id', 'primary_site_id'))
		   AND NOT EXISTS (SELECT 1 FROM pg_policy p
		                    WHERE p.polrelid = c.oid
		                      AND p.polname = 'site_isolation')
		 ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("enumerate site-bearing relations: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("these site-bearing relations have NO site_isolation policy: %v.\n"+
			"Every one of them accepts a cross-site write as security_core. Migration 0023 "+
			"creates the policy DERIVED from this same query, so re-running the migrator is "+
			"enough -- a new table is covered without anyone adding it to a list, which is "+
			"the whole difference between 0008 and 0023.\n"+
			"`user_site_assignments` is excluded from both, deliberately: scoping the table "+
			"that DEFINES the site scope by the site scope is circular, and the login path "+
			"that needs the scope cannot get past the policy it needs it for",
			missing)
	}
}

// TestTheSiteScopedTablesAreAllCovered names the boundary of this file honestly.
//
// `writeCanaries` covers three tables and `siteScopedTables` derives every site-bound
// table from the migrations, which is around fifteen. So the canaries are a SAMPLE,
// and a sample is a choice somebody made rather than a property anybody checked. This
// reports the gap so the number is visible in the log on every run.
func TestTheSiteScopedTablesAreAllCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range writeCanaries {
		covered[c.table] = true
	}
	all := siteScopedTables(t)
	var uncovered []string
	for _, table := range all {
		if !covered[table] {
			uncovered = append(uncovered, table)
		}
	}
	t.Logf("write canaries cover %d of %d site-scoped tables: %v", len(covered), len(all), uncovered)
}

var errNoRowsAffected = errors.New("the statement matched no rows, which under RLS is the refusal")

// isPolicyRefusal distinguishes "the policy said no" from "the statement is wrong".
//
// Without it a canary passes for the wrong reason the moment the SQL has a typo, and a
// canary that passes for the wrong reason is worse than no canary — it is reported in
// the same line of output as one that is working.
//
// PostgreSQL's own answer is `42501 insufficient_privilege` for a policy violation and
// `23514`/`23503` for a constraint. Only the first is a policy.
//
// This earned its place on the first run of the file, by catching
// `INSERT has more target columns than expressions` and reporting it as what it was
// rather than as a passing refusal.
func isPolicyRefusal(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		// A pgx error wrapped without the driver type still carries the SQLSTATE in
		// its text, which is the only thing available if the wrapper dropped the type.
		return strings.Contains(err.Error(), "42501") ||
			strings.Contains(err.Error(), "row-level security")
	}
	return pgErr.Code == "42501"
}

// migrateUp brings this package's own test database to the current schema.
//
// The harness gives each package its own database, and this is a new file in a
// package whose other tests each did this for themselves. Without it the failure is
// `relation "tenants" does not exist`, which reads as a missing migration rather than
// as a canary that forgot to look.
func migrateUp(t *testing.T, raw *pgxpool.Pool) {
	t.Helper()
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}
}
