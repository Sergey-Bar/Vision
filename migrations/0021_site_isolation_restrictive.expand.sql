-- 0019_site_isolation_restrictive.expand.sql
--
-- Make `site_isolation` RESTRICTIVE, so the two policies compose as AND.
--
-- The intent is stated in migration 0008: "so the two concerns compose: tenant
-- first, then site". That is AND. What shipped is two PERMISSIVE policies, and
-- PostgreSQL ORs multiple permissive policies for the same command, so the
-- effective predicate has been
--
--     tenant_isolation OR site_isolation
--
-- which is `tenant_isolation`. There has been no site isolation at the database
-- layer at all. Every site check in this product is a handler doing it by hand,
-- and a handler that forgets one is a cross-site read with no error anywhere.
--
-- Confirmed directly, not inferred from the migration text:
--
--   SELECT polname, polpermissive FROM pg_policy WHERE polrelid = 'evidence'::regclass;
--     site_isolation      permissive = true
--     tenant_isolation    permissive = true
--
-- ALTER POLICY ... AS RESTRICTIVE intersects rather than unions, so the effective
-- predicate becomes `tenant_isolation AND site_isolation`, which is what the
-- comment always described.
--
-- This is an expand rather than a contract because it adds no column and drops
-- no data: it changes what an existing policy means, and the Go side was changed
-- first, in the same release, so that every statement reading a site-scoped
-- table binds `app.site_ids`. `config.validate` already refuses a start with no
-- `SP_SITE_ID`, and the daemon threads that site through the ingest, consume,
-- correlator and evidence paths.
--
-- The refusal direction matters and is deliberate. A statement that reaches the
-- pool without a site scope now sees NO site-scoped rows rather than all of them.
-- That is fail-closed, and it is also the reason the Go side had to be right
-- before this migration ran: a consume loop that forgot its site would ingest
-- nothing and say nothing. `internal/rlsrole/site_isolation_test.go` is the
-- check that the Go side is right; it asserts on the policy's permissiveness and
-- is what makes a regression here visible.
--
-- The system worker is unaffected: `site_isolation` already ORs in
-- `app_is_system_worker()`, and a RESTRICTIVE policy's own expression is
-- unchanged. A worker that must see every tenant's rows â€” a migration, a backup,
-- an operator running a cross-site query deliberately â€” still can.

-- PostgreSQL's `ALTER POLICY` only supports `RENAME TO`; permissiveness is fixed
-- at creation. So each policy is recreated rather than altered, carrying its
-- original USING expression and its original role list over verbatim â€” this
-- migration changes *when* the predicate is combined, never *what* it says.
--
-- A policy whose role list is anything other than PUBLIC is refused rather than
-- guessed: recreating it with the wrong roles would be a silent privilege
-- change disguised as a scope change.
DO $$
DECLARE
    r RECORD;
    n INT := 0;
BEGIN
    FOR r IN
        SELECT n.nspname AS schema,
               c.relname AS table_name,
               p.polname AS policy,
               pg_get_expr(p.polqual, p.polrelid) AS qual,
               p.polroles
          FROM pg_policy p
          JOIN pg_class c ON c.oid = p.polrelid
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND p.polname = 'site_isolation'
           AND p.polpermissive
    LOOP
        IF r.polroles IS DISTINCT FROM ARRAY[0]::oid[] THEN
            RAISE EXCEPTION
                'site_isolation on %.% applies to role oids %, not PUBLIC. This migration will '
                'not recreate it with different roles, because that would be a privilege '
                'change disguised as a scope change.',
                r.schema, r.table_name, r.polroles;
        END IF;

        EXECUTE format('DROP POLICY %I ON %I.%I', r.policy, r.schema, r.table_name);
        EXECUTE format(
            'CREATE POLICY %I ON %I.%I AS RESTRICTIVE FOR ALL TO PUBLIC USING (%s)',
            r.policy, r.schema, r.table_name, r.qual);
        n := n + 1;
    END LOOP;

    IF n = 0 THEN
        RAISE NOTICE
            'site_isolation: no permissive site policy remained; this migration has already been applied';
    END IF;
END;
$$;

-- Say what happened, in a form a later query can check. `pg_policy` already
-- records it, and that is the check; this is here so the ledger's own history
-- and the catalogue agree about when the change landed.
COMMENT ON POLICY site_isolation ON evidence IS
    'RESTRICTIVE as of schema 19: the effective predicate is tenant_isolation AND site_isolation, which is what migration 0008 always described.';
