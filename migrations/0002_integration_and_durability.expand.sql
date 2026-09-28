-- 0002_integration_and_durability.expand.sql
--
-- Integration intake and the durability substrate that replaces Kafka and Redis.
--
-- A pipeline that only looks deterministic in tests will be less reliable in the
-- field, so these three tables carry the guarantees explicitly: the inbox gives
-- exactly-once effect, the outbox gives atomic side effects, the job table gives
-- retries and visibility timeouts.

CREATE TABLE integrations (
    tenant_id               TEXT NOT NULL,
    integration_id          TEXT NOT NULL,
    site_id                 TEXT NOT NULL,
    type                    TEXT NOT NULL CHECK (type IN ('xprotect_milestone', 'xprotect_mip', 'replay_fixture')),
    enabled                 BOOLEAN NOT NULL DEFAULT TRUE,
    base_url                TEXT,
    poll_interval_seconds   INTEGER NOT NULL DEFAULT 5 CHECK (poll_interval_seconds > 0),
    -- Key into the encrypted secret store. Never the secret itself.
    secret_ref              TEXT,
    last_success_at         TIMESTAMPTZ,
    reconnect_total         BIGINT NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, integration_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT
);

-- The raw, sanitized vendor envelope, kept verbatim alongside its hash. A
-- canonical event is a normalization of this, never a replacement of it.
CREATE TABLE source_events (
    tenant_id        TEXT NOT NULL,
    source_event_id  TEXT NOT NULL,
    integration_id   TEXT NOT NULL,
    site_id          TEXT NOT NULL,
    source_system_id TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    schema_version   TEXT NOT NULL,
    observed_at      TIMESTAMPTZ NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    source_timezone  TEXT,
    raw_envelope     JSONB NOT NULL,
    envelope_hash    TEXT NOT NULL,
    PRIMARY KEY (tenant_id, integration_id, source_event_id)
);

CREATE INDEX source_events_received_idx ON source_events (tenant_id, received_at);
CREATE INDEX source_events_site_idx ON source_events (tenant_id, site_id, observed_at);

-- ---------------------------------------------------------------------------
-- inbox_events
--
-- Dedupe note, and it is load-bearing: the plan specifies
-- UNIQUE (tenant_id, integration_id, source_event_id) on inbox_events, and
-- inbox_events is monthly range-partitioned. Postgres requires the partition key
-- to be part of every unique index on a partitioned table, so that constraint
-- cannot exist here — a duplicate arriving a month later would slip through it.
--
-- The uniqueness is therefore enforced one level up, on inbox_event_keys, which
-- is small, non-partitioned, and holds the key plus a pointer to the row that
-- owns it. The writer inserts the key first, in the same transaction as the
-- inbox row, so a duplicate insert is a unique violation the writer treats as a
-- no-op. Exactly-once effect is preserved; the guarantee just lives in a table
-- that can actually carry it.
-- ---------------------------------------------------------------------------

CREATE TABLE inbox_events (
    tenant_id         TEXT NOT NULL,
    integration_id    TEXT NOT NULL,
    source_event_id   TEXT NOT NULL,
    site_id           TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'received'
                      CHECK (status IN ('received', 'processing', 'processed', 'failed', 'dead_letter')),
    payload           JSONB NOT NULL,
    payload_hash      TEXT NOT NULL,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT,
    lease_owner       TEXT,
    lease_expires_at  TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    dead_letter_reason TEXT,
    canonical_event_id TEXT,
    -- Rows are never deleted inside the retention window. They are the evidence
    -- that nothing was lost.
    retention_until   TIMESTAMPTZ
) PARTITION BY RANGE (received_at);

CREATE TABLE inbox_events_2026_01 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-01-01T00:00:00Z') TO ('2026-02-01T00:00:00Z');
CREATE TABLE inbox_events_2026_02 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-02-01T00:00:00Z') TO ('2026-03-01T00:00:00Z');
CREATE TABLE inbox_events_2026_03 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-03-01T00:00:00Z') TO ('2026-04-01T00:00:00Z');
CREATE TABLE inbox_events_2026_04 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-04-01T00:00:00Z') TO ('2026-05-01T00:00:00Z');
CREATE TABLE inbox_events_2026_05 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-05-01T00:00:00Z') TO ('2026-06-01T00:00:00Z');
CREATE TABLE inbox_events_2026_06 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-06-01T00:00:00Z') TO ('2026-07-01T00:00:00Z');
CREATE TABLE inbox_events_2026_07 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-07-01T00:00:00Z') TO ('2026-08-01T00:00:00Z');
CREATE TABLE inbox_events_2026_08 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-08-01T00:00:00Z') TO ('2026-09-01T00:00:00Z');
CREATE TABLE inbox_events_2026_09 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-09-01T00:00:00Z') TO ('2026-10-01T00:00:00Z');
CREATE TABLE inbox_events_2026_10 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-10-01T00:00:00Z') TO ('2026-11-01T00:00:00Z');
CREATE TABLE inbox_events_2026_11 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-11-01T00:00:00Z') TO ('2026-12-01T00:00:00Z');
CREATE TABLE inbox_events_2026_12 PARTITION OF inbox_events
    FOR VALUES FROM ('2026-12-01T00:00:00Z') TO ('2027-01-01T00:00:00Z');

-- The dedupe key. Enforced globally, not per partition.
CREATE TABLE inbox_event_keys (
    tenant_id         TEXT NOT NULL,
    integration_id    TEXT NOT NULL,
    source_event_id   TEXT NOT NULL,
    inbox_partition   TEXT NOT NULL,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, integration_id, source_event_id)
);

CREATE INDEX inbox_events_pending_idx ON inbox_events (received_at)
    WHERE status IN ('received', 'processing', 'failed');
CREATE INDEX inbox_events_lease_idx ON inbox_events (lease_expires_at)
    WHERE status = 'processing';
CREATE INDEX inbox_events_site_idx ON inbox_events (tenant_id, site_id, received_at);
CREATE INDEX inbox_events_dead_idx ON inbox_events (tenant_id, dead_letter_reason)
    WHERE status = 'dead_letter';

-- ---------------------------------------------------------------------------
-- outbox_events
--
-- State changes and their outbound side effects commit in one transaction with
-- the outbox row. A separate relay drains the outbox and guarantees at-least-once
-- delivery; consumers dedupe on idempotency_key.
-- ---------------------------------------------------------------------------
CREATE TABLE outbox_events (
    outbox_id        BIGSERIAL PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    site_id          TEXT,
    topic            TEXT NOT NULL,
    payload          JSONB NOT NULL,
    idempotency_key  TEXT NOT NULL UNIQUE,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'delivering', 'delivered', 'failed', 'dead_letter')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    run_after        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at     TIMESTAMPTZ,
    last_error       TEXT,
    lease_owner      TEXT,
    lease_expires_at TIMESTAMPTZ
);

CREATE INDEX outbox_pending_idx ON outbox_events (run_after) WHERE status IN ('pending', 'failed');
CREATE INDEX outbox_tenant_idx ON outbox_events (tenant_id, created_at);

-- ---------------------------------------------------------------------------
-- jobs
--
-- Claimed with FOR UPDATE SKIP LOCKED, so multiple workers are safe without
-- coordination. dead_letter is a first-class state with a reason, surfaced in
-- the console and audited, never silently dropped.
-- ---------------------------------------------------------------------------
CREATE TABLE jobs (
    job_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          TEXT NOT NULL,
    site_id            TEXT,
    queue              TEXT NOT NULL CHECK (queue IN ('vlm', 'evidence_fetch', 'rule_bulk', 'checkpoint_seal')),
    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'leased', 'done', 'failed', 'dead_letter')),
    -- Unique per queue. Enqueueing the same job twice is a no-op.
    idempotency_key    TEXT NOT NULL,
    payload            JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    max_attempts       INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts >= 0),
    backoff_strategy   TEXT NOT NULL DEFAULT 'exponential',
    run_after          TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner        TEXT,
    lease_expires_at   TIMESTAMPTZ,
    last_error         TEXT,
    dead_letter_reason TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ,
    UNIQUE (tenant_id, queue, idempotency_key)
);

CREATE INDEX jobs_claim_idx ON jobs (queue, run_after, job_id) WHERE status IN ('pending', 'failed');
CREATE INDEX jobs_lease_idx ON jobs (lease_expires_at) WHERE status = 'leased';
CREATE INDEX jobs_dead_idx ON jobs (tenant_id, dead_letter_reason) WHERE status = 'dead_letter';
CREATE INDEX jobs_site_idx ON jobs (tenant_id, site_id, created_at);
