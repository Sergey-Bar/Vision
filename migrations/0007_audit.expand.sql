-- 0007_audit.expand.sql
--
-- The audit ledger: append-only, hash-chained, and separate from operational
-- logs. Logs are diagnostic; this is authoritative.
--
-- The chain is computed by the database, not by the caller. If the application
-- supplied prev_hash and hash, a bug in one writer would break the chain for
-- every row it touched, and the chain would only mean "whatever the last writer
-- believed". Making the database the sole source of both values means the chain
-- is a property of the table, not of the code that wrote it.

CREATE TABLE audit_ledger (
    tenant_id          TEXT NOT NULL,
    sequence           BIGINT NOT NULL,
    ts                 TIMESTAMPTZ NOT NULL,
    actor              TEXT NOT NULL,
    actor_type         TEXT NOT NULL CHECK (actor_type IN
                         ('operator', 'admin', 'service', 'adapter', 'ai_runtime', 'system')),
    action             TEXT NOT NULL,
    target_type        TEXT NOT NULL DEFAULT '',
    target_id          TEXT NOT NULL DEFAULT '',
    -- State the actor saw, and state they produced. Together they make a
    -- transition legible without re-reading the log.
    prev_state_hash    TEXT,
    result_state_hash  TEXT,
    policy_version     TEXT NOT NULL DEFAULT '',
    reason             TEXT NOT NULL DEFAULT '',
    evidence_refs      TEXT NOT NULL DEFAULT '[]'::jsonb,
    -- Per-request correlation. Never a secret, never a credential.
    request_id         TEXT NOT NULL DEFAULT '',
    session_id         TEXT NOT NULL DEFAULT '',
    site_scope         TEXT NOT NULL DEFAULT '[]'::jsonb,
    prev_hash          TEXT NOT NULL,
    hash               TEXT NOT NULL,
    PRIMARY KEY (tenant_id, sequence)
);

CREATE INDEX audit_ledger_ts_idx ON audit_ledger (tenant_id, ts);
CREATE INDEX audit_ledger_actor_idx ON audit_ledger (tenant_id, actor, ts);
CREATE INDEX audit_ledger_action_idx ON audit_ledger (tenant_id, action, ts);
CREATE INDEX audit_ledger_target_idx ON audit_ledger (tenant_id, target_type, target_id, ts);

-- Genesis prev_hash: the well-known zero hash.
CREATE OR REPLACE FUNCTION audit_genesis_hash() RETURNS TEXT
    LANGUAGE sql IMMUTABLE AS $$
    SELECT repeat('0', 64);
$$;

-- Canonical, length-prefixed serialization.
--
-- Length prefixing is not decoration. A naive '|'-joined encoding is ambiguous:
-- actor "a|b" and actor "a" with action "b" produce the same bytes, and two
-- legitimate rows could then be made to collide into one chain. With a length
-- in front of every field there is exactly one byte string per row, so any edit
-- to any field changes the digest.
CREATE OR REPLACE FUNCTION audit_canonical(
    p_tenant        TEXT,
    p_sequence      BIGINT,
    p_ts            TIMESTAMPTZ,
    p_actor         TEXT,
    p_actor_type    TEXT,
    p_action        TEXT,
    p_target_type   TEXT,
    p_target_id     TEXT,
    p_prev_state    TEXT,
    p_result_state  TEXT,
    p_policy_version TEXT,
    p_reason        TEXT,
    p_evidence_refs TEXT,
    p_request_id    TEXT,
    p_session_id    TEXT,
    p_site_scope    TEXT,
    p_prev_hash     TEXT
) RETURNS TEXT
    LANGUAGE sql IMMUTABLE AS $$
    SELECT concat_ws('',
        length(p_tenant), ':', p_tenant,
        length(p_sequence::text), ':', p_sequence::text,
        length(to_char(p_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')), ':',
            to_char(p_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        length(p_actor), ':', p_actor,
        length(p_actor_type), ':', p_actor_type,
        length(p_action), ':', p_action,
        length(coalesce(p_target_type, '')), ':', coalesce(p_target_type, ''),
        length(coalesce(p_target_id, '')), ':', coalesce(p_target_id, ''),
        length(coalesce(p_prev_state, '')), ':', coalesce(p_prev_state, ''),
        length(coalesce(p_result_state, '')), ':', coalesce(p_result_state, ''),
        length(coalesce(p_policy_version, '')), ':', coalesce(p_policy_version, ''),
        length(coalesce(p_reason, '')), ':', coalesce(p_reason, ''),
        length(coalesce(p_evidence_refs, '')), ':', coalesce(p_evidence_refs, ''),
        length(coalesce(p_request_id, '')), ':', coalesce(p_request_id, ''),
        length(coalesce(p_session_id, '')), ':', coalesce(p_session_id, ''),
        length(coalesce(p_site_scope, '')), ':', coalesce(p_site_scope, ''),
        length(p_prev_hash), ':', p_prev_hash
    );
$$;

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

    -- The chain head is the last row of this tenant's ledger, serialized with a
    -- row lock. Concurrent writers for different tenants never contend; two
    -- writers for the same tenant queue here rather than both reading the same
    -- head.
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id, 0));

    SELECT hash INTO v_prev
      FROM audit_ledger
     WHERE tenant_id = NEW.tenant_id
       AND sequence = NEW.sequence - 1
     FOR UPDATE;

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

CREATE TRIGGER audit_ledger_stamp_before_insert
    BEFORE INSERT ON audit_ledger
    FOR EACH ROW EXECUTE FUNCTION audit_ledger_stamp();

-- Append-only, without exception. A row that has been written is a historical
-- fact; a correction is a new compensating row, not an edit. This trigger is
-- what makes "editing any row must fail" true at the storage layer rather than
-- only in a test.
CREATE TRIGGER audit_ledger_append_only
    BEFORE UPDATE OR DELETE ON audit_ledger
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- The next sequence for a tenant. Kept in the ledger's PK space so there is one
-- authority for ordering, and so a duplicate application-assigned sequence
-- fails the primary key instead of forking the chain.
CREATE OR REPLACE FUNCTION audit_next_sequence(p_tenant TEXT) RETURNS BIGINT
    LANGUAGE sql AS $$
    SELECT coalesce(max(sequence), 0) + 1 FROM audit_ledger WHERE tenant_id = p_tenant;
$$;

-- Full-chain verification. Reports the first row whose stored hash does not
-- match a recomputation, or whose prev_hash does not match its predecessor.
-- A checkpoint seals the digest of this function's result; any edit anywhere in
-- the sealed range makes verification fail and the seal no longer matches.
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
BEGIN
    v_prev := audit_genesis_hash();
    v_first := p_from;
    FOR r IN
        SELECT * FROM audit_ledger
         WHERE tenant_id = p_tenant AND sequence >= p_from
         ORDER BY sequence
    LOOP
        v_count := v_count + 1;
        v_last := r.sequence;

        IF r.prev_hash <> v_prev THEN
            RETURN QUERY SELECT false, v_first, v_last, r.sequence, v_count;
            RETURN;
        END IF;

        v_expect := encode(digest(convert_to(audit_canonical(
            r.tenant_id, r.sequence, r.ts, r.actor, r.actor_type, r.action,
            r.target_type, r.target_id, r.prev_state_hash, r.result_state_hash,
            r.policy_version, r.reason, r.evidence_refs::text,
            r.request_id, r.session_id, r.site_scope::text, r.prev_hash
        ), 'UTF8'), 'sha256'), 'hex');

        IF r.hash <> v_expect THEN
            RETURN QUERY SELECT false, v_first, v_last, r.sequence, v_count;
            RETURN;
        END IF;

        v_prev := r.hash;
    END LOOP;

    RETURN QUERY SELECT true, v_first, coalesce(v_last, p_from), NULL::BIGINT, v_count;
END;
$$;

-- Sealed checkpoints. M1 sinks a signed offline file; production points the
-- same interface at a WORM-capable target. The seal records the digest of the
-- verified chain, so a later edit inside the sealed range is detectable without
-- keeping every row forever.
CREATE TABLE audit_checkpoints (
    checkpoint_id    TEXT NOT NULL,
    tenant_id        TEXT NOT NULL,
    sealed_at        TIMESTAMPTZ NOT NULL,
    from_sequence    BIGINT NOT NULL,
    to_sequence      BIGINT NOT NULL,
    entry_count      BIGINT NOT NULL,
    chain_digest     TEXT NOT NULL,
    algorithm        TEXT NOT NULL DEFAULT 'sha256',
    signature        TEXT NOT NULL,
    public_key       TEXT NOT NULL,
    key_id           TEXT NOT NULL,
    sink_ref         TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, checkpoint_id)
);

CREATE INDEX audit_checkpoints_range_idx ON audit_checkpoints (tenant_id, to_sequence);
CREATE INDEX audit_checkpoints_sealed_idx ON audit_checkpoints (tenant_id, sealed_at DESC);
