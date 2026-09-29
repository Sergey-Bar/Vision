-- 0018_audit_subrange_verification.expand.sql
--
-- `audit_verify_chain(tenant, p_from)` reported every sub-range as broken.
--
-- It seeded the running previous-hash with the genesis hash regardless of
-- p_from, so the first row it examined — sequence = p_from — was compared
-- against a genesis it does not have: that row's prev_hash is the hash of
-- sequence p_from - 1. Every sub-range verification therefore returned
-- `valid = false, broken_at = p_from`, and reported the break at the first row
-- rather than at the real one.
--
-- It passed the tests because the tests only ever verified from 1.
--
-- Why that mattered: `audit_checkpoints` stores from_sequence and
-- to_sequence for every sealed range, so this is the function that verifies a
-- seal. Invariant 4 is "the audit ledger is append-only and hash-chained;
-- editing any row must fail checkpoint verification", and a verifier that
-- fails closed on an untouched ledger cannot distinguish tampering from a bug
-- in the verifier. An operator told "your ledger is broken" on an intact one
-- has learned to ignore the output, which is the same as having no verifier.
--
-- The fix seeds the running hash from the row immediately before p_from, and
-- refuses outright when that row is missing: a gap means there is no way to
-- establish what the first examined row should have linked to, and reporting
-- "valid" for a range that starts after a hole would be the more dangerous
-- answer. A refusal is a refusal; it is not reported as a broken chain, because
-- the operator's response to each is different.

CREATE OR REPLACE FUNCTION audit_verify_chain(p_tenant TEXT, p_from BIGINT DEFAULT 1)
    RETURNS TABLE (valid BOOLEAN, from_sequence BIGINT, to_sequence BIGINT, broken_at BIGINT, checked BIGINT)
    LANGUAGE plpgsql STABLE AS $$
DECLARE
    r RECORD;
    v_prev TEXT;
    v_expect TEXT;
    v_count BIGINT := 0;
    v_first BIGINT;
    v_last BIGINT;
    v_link TEXT;
BEGIN
    v_first := p_from;

    -- The hash the first examined row must link to.
    --
    -- At p_from = 1 that is the genesis hash. Above 1 it is the stored hash of
    -- sequence p_from - 1, read from the ledger rather than assumed: if that row
    -- has been removed, or was never there, the chain from p_from cannot be
    -- established and saying so is the only correct answer.
    IF p_from > 1 THEN
        SELECT hash INTO v_link FROM audit_ledger
         WHERE tenant_id = p_tenant AND sequence = p_from - 1;
        IF v_link IS NULL THEN
            RAISE EXCEPTION
                'audit: cannot verify from sequence % for tenant %: the row at % is missing, '
                'so the chain from there cannot be linked to anything',
                p_from, p_tenant, p_from - 1
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
        v_prev := v_link;
    ELSE
        v_prev := audit_genesis_hash();
    END IF;

    FOR r IN
        SELECT * FROM audit_ledger
         WHERE tenant_id = p_tenant AND sequence >= p_from
         ORDER BY sequence
    LOOP
        v_count := v_count + 1;
        v_last := r.sequence;

        -- The link is checked before the row's own hash, because a break in the
        -- link explains the hash mismatch too, and reporting the row after the
        -- break is what the reader needs in order to start looking.
        IF r.prev_hash IS DISTINCT FROM v_prev THEN
            RETURN QUERY SELECT false, v_first, v_last, r.sequence, v_count;
            RETURN;
        END IF;

        v_expect := encode(digest(convert_to(audit_canonical(
            r.tenant_id, r.sequence, r.ts, r.actor, r.actor_type, r.action,
            r.target_type, r.target_id, r.prev_state_hash, r.result_state_hash,
            r.policy_version, r.reason, r.evidence_refs::text,
            r.request_id, r.session_id, r.site_scope::text, r.prev_hash
        ), 'UTF8'), 'sha256'), 'hex');

        IF r.hash IS DISTINCT FROM v_expect THEN
            RETURN QUERY SELECT false, v_first, v_last, r.sequence, v_count;
            RETURN;
        END IF;

        v_prev := r.hash;
    END LOOP;

    RETURN QUERY SELECT true, v_first, coalesce(v_last, p_from), NULL::BIGINT, v_count;
END;
$$;

-- A retry bound with no upper limit.
--
-- `max_attempts` is the only thing standing between a job that keeps failing and
-- a job that is retried for ever, so a row that can ask for a bound of two
-- billion attempts is a row that never reaches a terminal state. The backoff in
-- Worker.Fail is now capped before its integer cast, so the arithmetic cannot
-- raise any more — but the bound itself is still a number a caller chose, and a
-- caller who chooses 100 has asked for something no operator wants. The ceiling
-- is generous enough that no realistic policy reaches it and tight enough that
-- "forever" is not expressible.
ALTER TABLE jobs
    ADD CONSTRAINT jobs_max_attempts_ceiling CHECK (max_attempts <= 1000);
