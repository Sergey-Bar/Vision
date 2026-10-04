-- 0023_site_isolation_for_every_site_bearing_table.expand.sql
--
-- Give EVERY site-bearing relation a `site_isolation` policy, derived rather than
-- enumerated. The single deliberate exclusion is `user_site_assignments`, and the
-- reason -- that scoping it is circular -- is in its own section below.
--
-- # What was wrong
--
-- Migration 0008 created `site_isolation` by looping over a hand-written list of
-- nine tables:
--
--     'incidents', 'devices', 'cameras', 'evidence', 'canonical_events',
--     'inbox_events', 'source_events', 'ai_observations', 'verification_results'
--
-- and migration 0021 made each of those RESTRICTIVE. Both worked on the tables they
-- named. The list is the defect: any site-bearing table created afterwards got no
-- policy at all, and nothing noticed, because nothing asked.
--
-- `internal/rlsrole/write_canary_test.go` found the first two by attempting a
-- cross-site INSERT as `security_core` and having it succeed:
--
--     INSERT INTO jobs succeeded at a site this principal is not assigned to
--     INSERT INTO outbox_events succeeded at a site this principal is not assigned to
--
-- Both are writes the pipeline makes on every event. Once that canary existed, the
-- catalogue said the number is eight, not two:
--
--   device_clocks   entities   integrations   jobs   outbox_events
--   sites   user_site_assignments   zones
--
-- plus twenty-four monthly partitions (`canonical_events_2026_01` through
-- `_2026_12` and the same for `inbox_events`) that have no policy of their own. The
-- 2027 partitions DO have one, which is how a reader can date the drift: the
-- partition-creation migration added policies for the partitions it made and nobody
-- backfilled the ones that already existed.
--
-- # Why derived, and not two more names
--
-- A list is the defect. Adding `jobs` and `outbox_events` to another list would fix
-- those two and leave the six, and the next table after them. This loop asks the
-- catalogue which relations are site-bearing and which lack the policy, so a table
-- added tomorrow is covered the next time this runs -- and, more to the point, is
-- COVERED BY THIS MIGRATION'S OWN RE-RUN with no edit at all.
--
-- The column is chosen the way migration 0008 chose it: `site_id` when the relation
-- has one, `primary_site_id` otherwise.
--
-- # Partitions
--
-- A policy defined on a partitioned table applies to queries that go through the
-- parent. Accessing a partition DIRECTLY does not inherit it, so a query naming
-- `inbox_events_2026_03` has had no site policy for its whole life. Covering the
-- partitions costs nothing and closes the path; the parent's policy is untouched.
--
-- # The NULL site
--
-- `site_id` is NULLABLE on `jobs` and `outbox_events`, and `app_site_visible(x)`
-- returns TRUE when `x` IS NULL. So a site-less row stays visible to every principal
-- in the tenant.
--
-- That is the existing helper's semantics and this migration does not change it,
-- because a check that refused NULL would break the worker that enqueues a
-- site-less job, and that failure appears under load rather than at migration time.
-- The consequence is stated rather than hidden: **a site-less row on these tables is
-- tenant-scoped, not site-scoped.** Whether that is right is a decision about what a
-- site-less job MEANS, and it is recorded as D-11 in `docs/decisions.md` rather than
-- decided here.
--
-- The system worker is unaffected: `app_is_system_worker()` is ORed in exactly as
-- migration 0008 wrote it, so a migration, a backup or an operator running a
-- deliberate cross-site query still can.
--
-- # Expand, not contract
--
-- No column, no data, no dropped object. It adds predicates, and the refusal
-- direction is fail-closed: a statement that reaches the pool without a site scope
-- now sees nothing on these tables rather than everything.
--
-- That is the reason the Go side has to be right first, and it is -- `WithSites`
-- threads `app.site_ids` through the ingest, consume, correlator, jobs and outbox
-- paths, and `config.validate` refuses to start without `SP_SITE_ID`.

-- # The one table deliberately EXCLUDED: `user_site_assignments`
--
-- It is site-bearing, and it is still excluded, because scoping it by site scope is
-- circular:
--
--     reading a principal's assignments  ->  needs `app.site_ids`
--     `app.site_ids`                    ->  comes from reading the assignments
--
-- `site_isolation` on that table answers "can this principal see this assignment?",
-- which is true for every row a login would ever ask about and is therefore
-- useless as a control -- while making the login itself unresolvable, because the
-- scope it needs is behind the policy it needs it for.
--
-- `TestRolesAndScopeAreReadableUnderRLS` is what caught this, and it caught it the
-- only way it could: the assignment read came back empty and the test said "the site
-- assignment was not read" rather than "a policy refused".
--
-- The table is still tenant-scoped, which is the isolation that matters for it: a
-- principal cannot see another TENANT's assignments, which is what the
-- `tenant_isolation` policy already does.
--
-- # `sites` IS scoped, and bootstrap needs the worker flag because of it
--
-- `sites` has a row that IS the site, so `app_site_visible(site_id)` is a coherent
-- question. Adding the policy means a tenant-scoped, site-unscoped read of `sites`
-- returns nothing -- which broke `identity.BootstrapAdmin`'s existence check, since a
-- bootstrap has no principal and therefore no scope yet.
--
-- That is the "the Go side has to be right first" risk this migration's own header
-- names, and it was not right: `BootstrapAdmin` bound a tenant context and expected
-- to read the site. `dbx.TenantContext` now carries an explicit `System` flag for a
-- transaction that needs a named tenant AND the worker exemption, and bootstrap
-- sets it. Setting the GUC by hand would also have worked; it is a field because a
-- caller reaching past the type to set it is a caller that does not know it needed to.

DO $$
DECLARE
    r RECORD;
    made INT := 0;
BEGIN
    FOR r IN
        SELECT c.relname AS table_name
          FROM pg_class c
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND c.relkind IN ('r', 'p')
           AND c.relname <> 'user_site_assignments'
           AND EXISTS (SELECT 1 FROM pg_attribute a
                        WHERE a.attrelid = c.oid
                          AND a.attname IN ('site_id', 'primary_site_id'))
           AND NOT EXISTS (SELECT 1 FROM pg_policy p
                            WHERE p.polrelid = c.oid
                              AND p.polname = 'site_isolation')
         ORDER BY c.relname
    LOOP
        EXECUTE format(
            'CREATE POLICY site_isolation ON %I AS RESTRICTIVE FOR ALL TO PUBLIC '
            'USING (%s OR app_is_system_worker()) '
            'WITH CHECK (%s OR app_is_system_worker())',
            r.table_name,
            CASE WHEN EXISTS (SELECT 1 FROM pg_attribute a
                               WHERE a.attrelid = format('%I.%I', 'public', r.table_name)::regclass
                                 AND a.attname = 'site_id')
                 THEN 'app_site_visible(site_id)'
                 ELSE 'app_site_visible(primary_site_id)' END,
            CASE WHEN EXISTS (SELECT 1 FROM pg_attribute a
                               WHERE a.attrelid = format('%I.%I', 'public', r.table_name)::regclass
                                 AND a.attname = 'site_id')
                 THEN 'app_site_visible(site_id)'
                 ELSE 'app_site_visible(primary_site_id)' END);
        made := made + 1;
    END LOOP;

    IF made = 0 THEN
        RAISE NOTICE
            'site_isolation: every site-bearing relation already has one; this migration has already been applied';
    ELSE
        RAISE NOTICE 'site_isolation: created on % site-bearing relation(s)', made;
    END IF;
END;
$$;

-- Say what happened, in a form a later query can check. `pg_policy` already records
-- it and that is the check; this is so the catalogue and the ledger agree about when
-- the change landed.
COMMENT ON POLICY site_isolation ON jobs IS
    'RESTRICTIVE as of schema 23. This table had NO site policy until then: migration 0008 '
    'enumerated nine tables by name and this was not among them, so a cross-site INSERT '
    'succeeded as security_core. Found by internal/rlsrole/write_canary_test.go.';
