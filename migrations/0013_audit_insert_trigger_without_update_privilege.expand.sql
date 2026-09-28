-- 0013_audit_insert_trigger_without_update_privilege.expand.sql
--
-- Make the audit ledger writable by the role that actually runs the product.
--
-- The insert trigger read the chain head with `SELECT ... FOR UPDATE`, and
-- Postgres requires the UPDATE privilege for a locking read. security_core is
-- granted SELECT and INSERT on audit_ledger and deliberately *not* UPDATE, so
-- every append failed with "permission denied for table audit_ledger".
--
-- The effect in production is that the audit trail was absent: no
-- authentication event, no disposition, no incident, no rule activation, and
-- the chain verifier had nothing to verify. The tests did not see it because
-- they connect as a superuser, which holds every privilege.
--
-- The row lock is removed rather than the grant added. It was redundant: the
-- trigger already takes a per-tenant advisory lock before reading the head, and
-- that lock is what serialises two concurrent appends for one tenant. Granting
-- UPDATE to the application so it can take an unnecessary lock would hand the
-- core the ability to rewrite history, which is the one thing the ledger's
-- append-only trigger and its hash chain exist to prevent.

CREATE OR REPLACE FUNCTION audit_ledger_stamp() RETURNS trigger AS $$
DECLARE
    v_prev TEXT;
    v_canon TEXT;
BEGIN
    IF NEW.tenant_id IS NULL OR btrim(NEW.tenant_id) = '' THEN
        RAISE EXCEPTION 'audit_ledger.tenant_id is NOT NULL and is never taken from a request body';
    END IF;
    IF NEW.sequence IS NULL OR NEW.sequence <= 0 THEN
        RAISE EXCEPTION 'audit_ledger.sequence must be assigned and positive';
    END IF;

    -- The chain head is the last row of this tenant's ledger. The advisory lock
    -- below serialises writers for this tenant, so no row lock is needed: two
    -- appends for one tenant cannot read the same head, and a locking read here
    -- would demand the UPDATE privilege the application must not hold.
    --
    -- The lock is taken *before* the read, not after, so the two statements
    -- cannot be reordered past each other by the planner.
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id, 0));

    SELECT hash INTO v_prev
      FROM audit_ledger
     WHERE tenant_id = NEW.tenant_id
       AND sequence = NEW.sequence - 1;

    IF NEW.sequence = 1 THEN
        v_prev := audit_genesis_hash();
    ELSIF v_prev IS NULL THEN
        RAISE EXCEPTION 'audit_ledger: sequence % for tenant % has no predecessor; a gap breaks the chain',
            NEW.sequence, NEW.tenant_id;
    END IF;

    NEW.prev_hash := v_prev;
    v_canon := audit_canonical(
        NEW.tenant_id, NEW.sequence, NEW.ts, NEW.actor, NEW.actor_type, NEW.action,
        NEW.target_type, NEW.target_id, NEW.prev_state_hash, NEW.result_state_hash,
        NEW.policy_version, NEW.reason, NEW.evidence_refs::text,
        NEW.request_id, NEW.session_id, NEW.site_scope::text, NEW.prev_hash
    );
    NEW.hash := encode(digest(convert_to(v_canon, 'UTF8'), 'sha256'), 'hex');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
