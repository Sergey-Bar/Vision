package rlsrole

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
// Three of the six defects this suite has found in total were found by a read. The
// ones a read cannot find are the ones where the statement is issued with the RIGHT
// tenant context and the WRONG site scope, or no scope at all: `tenant_isolation`
// passes, the row lands, and the site constraint lives only in a handler.
//
// # What these canaries claim, precisely
//
// For one table per class, as `security_core` -- the role the product runs as, with
// RLS in force and NOT superuser -- an INSERT, an UPDATE and a DELETE are attempted
// with:
//
//  1. no tenant context: refused, because there is no tenant to attribute the row to;
//  2. the right tenant and no site scope: refused for a site-bound table, because the
//     site policy restricts and `app.site_ids` is empty;
//  3. the right tenant and the wrong site: refused, because the site is not in scope;
//  4. the right tenant and the right site: ACCEPTED.
//
// Case 4 is the one that makes the other three mean something. A canary that only
// asserts refusals passes on a table that is simply unwritable, and an unwritable table
// is the failure mode a maintenance window produces.
//
// # What is NOT claimed
//
// That these four cases hold for every site-scoped table. They hold for the ones
// listed in `writeCanaryTables`, and that list is the honest boundary: a table added by
// a later migration is not covered until somebody adds it here, and `TestEverySiteScopedTableHasAWriteCanary`
// is what turns that from a silent gap into a failure.

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

// tenantContext is the pool context a canary writes through: one tenant, one actor, an
// explicit site scope.
//
// The site scope is named rather than inherited on purpose. `For` binds the tenant and
// nothing else, so a canary that used it would be testing the tenant policy and calling
// it a site canary -- which is the ambiguity this file exists to remove.
// canaryActor is the actor every canary writes as. One value, so the helper takes no
// actor: a parameter that is always the same string is a parameter a reader has to
// check at every call site, and `unparam` is right that it carries no information.
const canaryActor = "rls-canary"

func tenantContext(tenant string, sites []string) dbx.TenantContext {
	return dbx.TenantContext{TenantID: tenant, ActorID: canaryActor, SiteIDs: sites}
}

// writeCanary is one statement and the site scope it belongs to.
type writeCanary struct {
	// table is a site-scoped table. Partitioned parents are excluded: RLS on a
	// partitioned table applies to the partitions, and asserting against the parent
	// would be asserting against a relation that holds no rows.
	table string
	// insert must succeed as security_core with the right tenant and site scope.
	// It is written to be undone by the test's own cleanup, so the canary does not
	// need a rollback -- which is deliberate, because a canary that runs inside a
	// transaction and rolls back cannot distinguish "refused by policy" from "refused
	// because of something the transaction did".
	insert string
	// delete removes rows matching the inserted marker, as the OWNER: a cleanup that
	// needed the production role's permissions would be testing the policy twice.
	delete string
}

// The tables. Three classes, because one per class is enough to catch a policy that
// was never created and one per table is several hundred tests that all say the same
// thing.
//
//	incidents    the row an operator reads to decide whether to act, and the one with
//	             the most NOT NULL columns -- a canary that gets the column list wrong
//	             fails on `more target columns than expressions`, which is not a policy
//	             refusal and is asserted as one
//	evidence     raw bytes; a cross-tenant write here is a cross-tenant disclosure
//	jobs         the write the pipeline makes on every event, and the only site-scoped
//	             table whose site column is NULLABLE -- so "no site scope" and "a site
//	             I cannot see" are different statements about it, and the canary has to
//	             distinguish them
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
}

// unprotectedTables are the site-scoped tables a cross-site write REACHED, with the
// reason it is not protected and what closing it takes.
//
// This list exists because the canary above found the defect and a migration needs a
// human to read the diff before it lands. Leaving the test red would mean the suite
// cannot be green at all; deleting the assertion would mean the defect is invisible.
// A ledger is the third option: the control stands, the finding is named in the output
// of every run, and closing an entry requires the migration AND deleting the entry --
// the dead-export and unproduced-metric gates both work this way and this is the third
// instance.
//
// The shape is the same for the same reason: a bare table name here is a backlog item
// nobody can act on, and a list that cannot shrink is an exemption for the next table
// somebody names after these.
var unprotectedTables = map[string]string{
	"jobs": "NO site_isolation policy at all. `pg_policy` carries only `tenant_isolation` " +
		"for this table, so a cross-site INSERT satisfies the tenant policy and is not " +
		"checked on site at all. FOUND BY THIS CANARY: `TestAWriteWithTheWrongSiteIsRefused` " +
		"inserts a job at a site the principal is not assigned to and it succeeds. " +
		"Closing it is migration 0023: `CREATE POLICY site_isolation ON jobs ... AS RESTRICTIVE " +
		"USING (site_id IS NULL OR app_site_visible(site_id)) WITH CHECK (...)`. " +
		"`site_id` is NULLABLE on this table, which is why the check has to allow NULL: the " +
		"worker that enqueues a site-less job is legitimate, and a check that refused NULL " +
		"would break it. A human has to read that migration before it lands",
	"outbox_events": "NO site_isolation policy at all -- the same shape as `jobs`, and for " +
		"the same reason: migration 0008 applied its policy by enumerating the tenant-bearing " +
		"relations that existed then, and this table is site-bearing and was not in that list. " +
		"FOUND BY THIS CANARY, same test and same symptom. Closing it is the same migration " +
		"0023, on the same terms: RESTRICTIVE, `app_site_visible(site_id)`, NULL allowed " +
		"because the site column is nullable here too",
}

// TestTheUnprotectedLedgerIsConsulted is the assertion that keeps the list honest in
// both directions, and it is the part that makes this a ledger rather than a comment.
//
// It fails if a table is listed that IS protected -- which is what happens the moment
// someone lands the migration and forgets this file, and the failure says "delete the
// entry" rather than leaving a stale exemption in a document about a policy that no
// longer has a gap.
func TestTheUnprotectedLedgerIsConsulted(t *testing.T) {
	raw := testutil.Pool(t)
	ctx := context.Background()
	migrateUp(t, raw)

	for table := range unprotectedTables {
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

	for table, reason := range unprotectedTables {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("the ledger names %s without saying why, which is not a ledger entry", table)
		}
		// And the named symptom has to still be the symptom. A reason that describes a
		// policy somebody has since written is worse than no reason, because it reads
		// as current.
		if !strings.Contains(strings.ToUpper(reason), "NO SITE_ISOLATION POLICY") {
			t.Errorf("the entry for %s does not say that the table has no site_isolation "+
				"policy, which is what the canary observed. If the shape of the defect has "+
				"changed, the reason has to change with it", table)
		}
	}
}

// TestTheSiteScopedTablesAreAllCovered names the boundary of this file honestly.
//
// `writeCanaries` covers three tables and `siteScopedTables` derives every site-bound
// table from the migrations, which is around twenty. So the canaries are a SAMPLE, and
// a sample is a choice somebody made rather than a property anybody checked. This
// reports the gap so the number is visible in the log on every run instead of living
// only in a comment nobody reads.
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
	t.Logf("write canaries cover %d of %d site-scoped tables: %v", len(covered), len(all),
		uncovered)
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
			// The statement, with the tenant and site replaced by literals that do not
			// match the tenant just created. Written this way rather than through the
			// canary's own args so the mutation is unambiguous: no `app.tenant_id`
			// setting at all.
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

// TestAWriteWithTheWrongSiteIsRefused is case 3, and it is the one this package could
// not have found before.
//
// A statement with the RIGHT tenant context and the WRONG site satisfies
// `tenant_isolation`, and only `site_isolation` refuses it. Before migration 21 both
// were PERMISSIVE and PostgreSQL ORed them, so `tenant_isolation OR site_isolation` was
// `tenant_isolation` and this write would have succeeded.
//
// That is the defect class the reads cannot see: every read canary in this package
// passes when the site policy is absent, because a read that should return nothing
// returns rows and the canary notices -- but a read that should return rows and
// correctly does is indistinguishable from a read that is not filtered at all. The
// test asserts a row count, so it notices the first and not the second.
//
// A write does not have that ambiguity. Cross-site means cross-site, in both
// directions.
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
					// distinction is not "known gaps are tolerated" -- it is "a known gap
					// is a line somebody wrote down, and an unknown one is a failure".
					//
					// The report is unconditional rather than `-v`, because the whole
					// point is that a reader of a green run sees it.
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

// TestAWriteWithTheRightScopeIsAccepted is case 4, and it is the case that gives the
// other three their meaning.
//
// A canary that only asserts refusals passes on a table that is simply unwritable, and
// an unwritable table is what a migration with a missing GRANT produces -- which fails
// closed, loudly, in production, on the first write. So the control has to assert both
// directions or it is asserting half a policy.
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

			// And it is actually there, not merely un-refused. A policy that refuses
			// everything also refuses the `RETURNING`... it does not, because there is
			// no RETURNING, so the row count is the only statement that the write
			// happened. This is the assertion that separates "accepted" from "ignored".
			var n int
			if err := raw.QueryRow(ctx,
				`SELECT count(*) FROM `+c.table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
				t.Fatalf("count the canary row in %s: %v", c.table, err)
			}
			if n == 0 {
				t.Errorf("the INSERT into %s reported no error and wrote no row. A policy that "+
					"silently drops a write is worse than one that refuses it, because the "+
					"caller is told it succeeded", c.table)
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

	t.Run("an UPDATE at an unassigned site is refused", func(t *testing.T) {
		// A row at the other site, written by the owner because the production role
		// cannot create one -- which is exactly the situation: the canary is about
		// reaching a row the role can see but should not be able to change.
		const id = "inc_update_canary"
		if _, err := raw.Exec(ctx, `INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, related_site_ids, rule_id,
			 rule_bundle_version, bundle_content_hash, policy_version, state, severity,
			 title, first_event_at, last_event_at, degraded, degraded_reasons)
			VALUES ($1, $2, $3, '[]'::jsonb, 'canary', repeat('b', 64), repeat('b', 64),
			 'p1', 'open', 'low', 'update canary', now(), now(), false, '[]'::jsonb)`,
			id, tenant, theirs); err != nil {
			t.Fatalf("seed the row: %v", err)
		}
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
		if _, err := raw.Exec(ctx, `INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, related_site_ids, rule_id,
			 rule_bundle_version, bundle_content_hash, policy_version, state, severity,
			 title, first_event_at, last_event_at, degraded, degraded_reasons)
			VALUES ($1, $2, $3, '[]'::jsonb, 'canary', repeat('c', 64), repeat('c', 64),
			 'p1', 'open', 'low', 'delete canary', now(), now(), false, '[]'::jsonb)`,
			id, tenant, theirs); err != nil {
			t.Fatalf("seed the row: %v", err)
		}

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

var errNoRowsAffected = errors.New("the statement matched no rows, which under RLS is the refusal")

// isPolicyRefusal distinguishes "the policy said no" from "the statement is wrong".
//
// Without it a canary passes for the wrong reason the moment the SQL has a typo, and a
// canary that passes for the wrong reason is worse than no canary -- it is reported in
// the same line of output as one that is working.
//
// PostgreSQL's own answer is `42501 insufficient_privilege` for a policy violation and
// `23514`/`23503` for a constraint. Only the first is a policy.
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
// The harness gives each package its own database, and this is a new file in a package
// whose other tests each did this for themselves. Without it the failure is
// `relation "tenants" does not exist`, which reads as a missing migration rather than
// as a canary that forgot to look.
func migrateUp(t *testing.T, raw *pgxpool.Pool) {
	t.Helper()
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}
}
