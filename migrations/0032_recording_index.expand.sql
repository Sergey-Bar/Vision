-- 0032_recording_index.expand.sql
--
-- The recording index: one row per segment, the chain that binds them, and the
-- policy that says how long they are kept.
--
-- # What this migration is for
--
-- The AI VMS plan's media plane. The product now owns the camera path, and
-- owning it means answering three questions a VMS that only reads a vendor's
-- recording cannot answer:
--
--   - what did we record, and when;
--   - is the recording CONTINUOUS, or is there a hole in it;
--   - can the recording be PROVED, months later, to be the bytes we wrote.
--
-- The bytes live on the filesystem, in the customer's own disk. This table is
-- the index over them, and it is deliberately NOT the evidence: the bytes are
-- content-addressed and the chain is the proof, and the index is the map.
--
-- # `recording_segments` is partitioned, and why
--
-- One row per ten seconds per device. A hundred devices is 864 000 rows a
-- day, and the rows are written and never updated, so the index is exactly the
-- shape that bloats a table and then makes every retention sweep expensive.
--
-- Monthly partitions, not daily ones. The plan said "partitioned by day", and a
-- partition per day of TEN-SECOND SEGMENTS would be 8 640 partitions a month
-- per device — a partition per 25 rows, which is not partitioning, it is
-- a catalogue. Monthly is the same shape the events tables already use, and
-- the retention sweep drops a whole month at a time.
--
-- The partition NAME is the month the partition STARTS in, and that is the
-- convention `migrate.EnsurePartitions` relies on when it derives the next
-- boundary from the names on disk.
--
-- # The chain is per device per day
--
-- `chain[n] = SHA256(chain[n-1] ‖ segment_digest ‖ started_at)`.
--
-- Per DAY and not per segment: the chain head is SEALED into the audit ledger
-- at day close, and a ledger row per segment at 8 640 rows a day per device is
-- a self-inflicted denial of service on the ledger. One row per device-day is
-- a number a site can hold.
--
-- # The DEFAULT partition
--
-- Every partitioned table here has one, and the reason is the same: a segment
-- that arrives for a month nobody created a partition for must not fail the
-- write. It lands in the DEFAULT partition, and the partition sweeper notices
-- it and creates the real one. A write that fails is a hole in the recording,
-- and a hole in the recording is the one failure this product must not have.

CREATE TABLE recording_segments (
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    -- The sequence is per device and monotonic. It is what the chain is over,
    -- and it is what makes a segment identifiable without its bytes.
    seq          BIGINT NOT NULL,
    -- The segment's own identity: a UUID, minted once, so two segments can
    -- never collide on an id even if two devices claim the same sequence.
    segment_id   TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ NOT NULL,
    -- The wall-clock gap between the previous segment and this one. It is
    -- MEASURED, not derived, because a camera that was quiet for four minutes
    -- and one running at 25 frames a second differ only in this number, and
    -- the recording-continuity metric is computed from it.
    gap_before_ms INTEGER NOT NULL DEFAULT 0,
    bytes        BIGINT NOT NULL CHECK (bytes >= 0),
    -- The digest of the segment's BYTES. It is what the chain is over, and it
    -- is what makes the segment provable.
    digest       TEXT NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    codec        TEXT NOT NULL DEFAULT 'h264',
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    fps          DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- Where the bytes are, relative to the recording root. Never absolute, and
    -- never a URL: a path with a host in it is a path that can leave the
    -- machine.
    storage_ref  TEXT NOT NULL,
    -- The state of the segment. `partial` means the segment was cut short and
    -- the boundary is the TRUE boundary -- a partial segment is never a
    -- silent hole.
    state        TEXT NOT NULL DEFAULT 'complete'
                 CHECK (state IN ('complete', 'partial')),
    -- The chain this segment belongs to, and the chain hash it produced.
    chain_prev   TEXT,
    chain_hash   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The partition key is in the primary key because Postgres requires it:
    -- a unique index on a partitioned table must include the partition
    -- column, or the constraint would only be enforced WITHIN a month.
    PRIMARY KEY (tenant_id, started_at, segment_id)
) PARTITION BY RANGE (started_at);

-- The uniqueness that has to be global lives in its own table.
--
-- A segment's id is unique across the whole install, and a device's sequence
-- is unique per device — and both of those are cross-month facts, which a
-- partitioned table's unique index cannot carry. So the key lives here, in a
-- small non-partitioned table, and the writer inserts the key FIRST in the
-- same transaction as the segment. A duplicate is a unique violation the
-- writer treats as a no-op, which is exactly-once effect with the guarantee
-- in a table that can hold it.
--
-- This is the same arrangement `inbox_event_keys` and
-- `canonical_event_keys` use, for the same reason.
CREATE TABLE recording_segment_keys (
    tenant_id   TEXT NOT NULL,
    segment_id  TEXT NOT NULL,
    device_id   TEXT NOT NULL,
    seq         BIGINT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, segment_id),
    UNIQUE (tenant_id, device_id, seq)
);

CREATE INDEX recording_segments_device_started_idx
    ON recording_segments (tenant_id, site_id, device_id, started_at DESC);
CREATE INDEX recording_segments_site_started_idx
    ON recording_segments (tenant_id, site_id, started_at DESC);

-- The DEFAULT partition, so a write for an un-created month lands rather than
-- failing. See the note at the head of this migration.
CREATE TABLE recording_segments_default PARTITION OF recording_segments DEFAULT;

-- The chain: one row per device per day, holding the head of that day's chain.
CREATE TABLE recording_chains (
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    day          DATE NOT NULL,
    -- The head of the chain at the last segment of the day.
    chain_head   TEXT,
    -- The number of segments the chain covers.
    segments     INTEGER NOT NULL DEFAULT 0,
    -- The DEVICE-WIDE sequence the last segment took.
    --
    -- It is not the same as `segments`, and the difference matters. `segments`
    -- counts the segments in THIS DAY, because the chain is sealed per day.
    -- The sequence is per DEVICE and never restarts, because the segment's
    -- uniqueness is per device across every day: a segment whose sequence
    -- restarted at midnight would collide with the segment that took the same
    -- number yesterday, and the collision is a unique violation the writer
    -- cannot distinguish from a genuine duplicate.
    --
    -- It is a BIGINT and not an INTEGER because a device at ten seconds a
    -- segment takes a million sequences in four months.
    last_seq     BIGINT NOT NULL DEFAULT 0,
    -- The last segment's started_at, which is where the chain resumes.
    last_started_at TIMESTAMPTZ,
    -- The last byte of the last segment, which is the chain's input for the
    -- next one.
    last_digest  TEXT,
    -- The instant the chain was sealed into the audit ledger. A sealed chain
    -- is one the ledger knows about, which is what makes it provable.
    sealed_at    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, device_id, day)
);

-- The policy: how long a segment is kept.
--
-- A policy per site and per device, and a NULL device means the SITE's
-- policy. A policy per SEGMENT would be a row per segment, and a policy is
-- a thing a person sets.
--
-- # The key, and why it is a surrogate
--
-- The natural key is (tenant, site, device) and the device is NULLABLE --
-- a site-level policy names no device -- and a NULL cannot be part of a
-- primary key: Postgres makes every column of a primary key NOT NULL, so
-- `PRIMARY KEY (tenant_id, site_id, device_id)` would have made the
-- site-level policy unrepresentable while the column said NULL.
--
-- So the key is a surrogate, and the natural key is a UNIQUE INDEX over
-- `COALESCE(device_id, '')`. The COALESCE is what makes "no device" a
-- value rather than an absence: Postgres treats NULLs as distinct in a
-- unique index, so two site-level policies for one site would both be
-- accepted without it.
CREATE TABLE recording_policies (
    tenant_id      TEXT NOT NULL,
    site_id        TEXT NOT NULL,
    -- NULL means the site's own policy, which every device inherits.
    device_id      TEXT,
    policy_id      UUID NOT NULL DEFAULT gen_random_uuid(),
    continuous     BOOLEAN NOT NULL DEFAULT TRUE,
    pre_roll_s     INTEGER NOT NULL DEFAULT 15,
    post_roll_s    INTEGER NOT NULL DEFAULT 15,
    segment_s      INTEGER NOT NULL DEFAULT 10,
    retention_days INTEGER NOT NULL DEFAULT 30,
    -- A legal hold blocks deletion. It is the one row in the product that can
    -- say "you may not delete this", and it says it here.
    legal_hold     BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (tenant_id, policy_id)
);

CREATE UNIQUE INDEX recording_policies_scope_idx
    ON recording_policies (tenant_id, site_id, COALESCE(device_id, ''));

-- The monthly partitions.
--
-- The DEFAULT partition above is why ingest does not fail on the boundary date, and it
-- is also why the failure is INVISIBLE when it arrives: every row lands in one
-- unpartitioned relation, nothing prunes, index growth is unbounded, and every
-- timeline query becomes a sequential scan. So the real partitions exist, from
-- January 2026 through December 2030 -- the same horizon the events tables
-- carry, and the horizon the partition-horizon control asks for.
--
-- The NAME is the month the partition STARTS in, and that is the convention
-- migrate.EnsurePartitions relies on when it derives the next boundary from the
-- names on disk.
CREATE TABLE recording_segments_2026_01 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-01-01T00:00:00Z') TO ('2026-02-01T00:00:00Z');
CREATE TABLE recording_segments_2026_02 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-02-01T00:00:00Z') TO ('2026-03-01T00:00:00Z');
CREATE TABLE recording_segments_2026_03 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-03-01T00:00:00Z') TO ('2026-04-01T00:00:00Z');
CREATE TABLE recording_segments_2026_04 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-04-01T00:00:00Z') TO ('2026-05-01T00:00:00Z');
CREATE TABLE recording_segments_2026_05 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-05-01T00:00:00Z') TO ('2026-06-01T00:00:00Z');
CREATE TABLE recording_segments_2026_06 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-06-01T00:00:00Z') TO ('2026-07-01T00:00:00Z');
CREATE TABLE recording_segments_2026_07 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-07-01T00:00:00Z') TO ('2026-08-01T00:00:00Z');
CREATE TABLE recording_segments_2026_08 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-08-01T00:00:00Z') TO ('2026-09-01T00:00:00Z');
CREATE TABLE recording_segments_2026_09 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-09-01T00:00:00Z') TO ('2026-10-01T00:00:00Z');
CREATE TABLE recording_segments_2026_10 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-10-01T00:00:00Z') TO ('2026-11-01T00:00:00Z');
CREATE TABLE recording_segments_2026_11 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-11-01T00:00:00Z') TO ('2026-12-01T00:00:00Z');
CREATE TABLE recording_segments_2026_12 PARTITION OF recording_segments
    FOR VALUES FROM ('2026-12-01T00:00:00Z') TO ('2027-01-01T00:00:00Z');
CREATE TABLE recording_segments_2027_01 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-01-01T00:00:00Z') TO ('2027-02-01T00:00:00Z');
CREATE TABLE recording_segments_2027_02 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-02-01T00:00:00Z') TO ('2027-03-01T00:00:00Z');
CREATE TABLE recording_segments_2027_03 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-03-01T00:00:00Z') TO ('2027-04-01T00:00:00Z');
CREATE TABLE recording_segments_2027_04 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-04-01T00:00:00Z') TO ('2027-05-01T00:00:00Z');
CREATE TABLE recording_segments_2027_05 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-05-01T00:00:00Z') TO ('2027-06-01T00:00:00Z');
CREATE TABLE recording_segments_2027_06 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-06-01T00:00:00Z') TO ('2027-07-01T00:00:00Z');
CREATE TABLE recording_segments_2027_07 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-07-01T00:00:00Z') TO ('2027-08-01T00:00:00Z');
CREATE TABLE recording_segments_2027_08 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-08-01T00:00:00Z') TO ('2027-09-01T00:00:00Z');
CREATE TABLE recording_segments_2027_09 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-09-01T00:00:00Z') TO ('2027-10-01T00:00:00Z');
CREATE TABLE recording_segments_2027_10 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-10-01T00:00:00Z') TO ('2027-11-01T00:00:00Z');
CREATE TABLE recording_segments_2027_11 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-11-01T00:00:00Z') TO ('2027-12-01T00:00:00Z');
CREATE TABLE recording_segments_2027_12 PARTITION OF recording_segments
    FOR VALUES FROM ('2027-12-01T00:00:00Z') TO ('2028-01-01T00:00:00Z');
CREATE TABLE recording_segments_2028_01 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-01-01T00:00:00Z') TO ('2028-02-01T00:00:00Z');
CREATE TABLE recording_segments_2028_02 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-02-01T00:00:00Z') TO ('2028-03-01T00:00:00Z');
CREATE TABLE recording_segments_2028_03 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-03-01T00:00:00Z') TO ('2028-04-01T00:00:00Z');
CREATE TABLE recording_segments_2028_04 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-04-01T00:00:00Z') TO ('2028-05-01T00:00:00Z');
CREATE TABLE recording_segments_2028_05 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-05-01T00:00:00Z') TO ('2028-06-01T00:00:00Z');
CREATE TABLE recording_segments_2028_06 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-06-01T00:00:00Z') TO ('2028-07-01T00:00:00Z');
CREATE TABLE recording_segments_2028_07 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-07-01T00:00:00Z') TO ('2028-08-01T00:00:00Z');
CREATE TABLE recording_segments_2028_08 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-08-01T00:00:00Z') TO ('2028-09-01T00:00:00Z');
CREATE TABLE recording_segments_2028_09 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-09-01T00:00:00Z') TO ('2028-10-01T00:00:00Z');
CREATE TABLE recording_segments_2028_10 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-10-01T00:00:00Z') TO ('2028-11-01T00:00:00Z');
CREATE TABLE recording_segments_2028_11 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-11-01T00:00:00Z') TO ('2028-12-01T00:00:00Z');
CREATE TABLE recording_segments_2028_12 PARTITION OF recording_segments
    FOR VALUES FROM ('2028-12-01T00:00:00Z') TO ('2029-01-01T00:00:00Z');
CREATE TABLE recording_segments_2029_01 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-01-01T00:00:00Z') TO ('2029-02-01T00:00:00Z');
CREATE TABLE recording_segments_2029_02 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-02-01T00:00:00Z') TO ('2029-03-01T00:00:00Z');
CREATE TABLE recording_segments_2029_03 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-03-01T00:00:00Z') TO ('2029-04-01T00:00:00Z');
CREATE TABLE recording_segments_2029_04 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-04-01T00:00:00Z') TO ('2029-05-01T00:00:00Z');
CREATE TABLE recording_segments_2029_05 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-05-01T00:00:00Z') TO ('2029-06-01T00:00:00Z');
CREATE TABLE recording_segments_2029_06 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-06-01T00:00:00Z') TO ('2029-07-01T00:00:00Z');
CREATE TABLE recording_segments_2029_07 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-07-01T00:00:00Z') TO ('2029-08-01T00:00:00Z');
CREATE TABLE recording_segments_2029_08 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-08-01T00:00:00Z') TO ('2029-09-01T00:00:00Z');
CREATE TABLE recording_segments_2029_09 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-09-01T00:00:00Z') TO ('2029-10-01T00:00:00Z');
CREATE TABLE recording_segments_2029_10 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-10-01T00:00:00Z') TO ('2029-11-01T00:00:00Z');
CREATE TABLE recording_segments_2029_11 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-11-01T00:00:00Z') TO ('2029-12-01T00:00:00Z');
CREATE TABLE recording_segments_2029_12 PARTITION OF recording_segments
    FOR VALUES FROM ('2029-12-01T00:00:00Z') TO ('2030-01-01T00:00:00Z');
CREATE TABLE recording_segments_2030_01 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-01-01T00:00:00Z') TO ('2030-02-01T00:00:00Z');
CREATE TABLE recording_segments_2030_02 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-02-01T00:00:00Z') TO ('2030-03-01T00:00:00Z');
CREATE TABLE recording_segments_2030_03 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-03-01T00:00:00Z') TO ('2030-04-01T00:00:00Z');
CREATE TABLE recording_segments_2030_04 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-04-01T00:00:00Z') TO ('2030-05-01T00:00:00Z');
CREATE TABLE recording_segments_2030_05 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-05-01T00:00:00Z') TO ('2030-06-01T00:00:00Z');
CREATE TABLE recording_segments_2030_06 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-06-01T00:00:00Z') TO ('2030-07-01T00:00:00Z');
CREATE TABLE recording_segments_2030_07 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-07-01T00:00:00Z') TO ('2030-08-01T00:00:00Z');
CREATE TABLE recording_segments_2030_08 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-08-01T00:00:00Z') TO ('2030-09-01T00:00:00Z');
CREATE TABLE recording_segments_2030_09 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-09-01T00:00:00Z') TO ('2030-10-01T00:00:00Z');
CREATE TABLE recording_segments_2030_10 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-10-01T00:00:00Z') TO ('2030-11-01T00:00:00Z');
CREATE TABLE recording_segments_2030_11 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-11-01T00:00:00Z') TO ('2030-12-01T00:00:00Z');
CREATE TABLE recording_segments_2030_12 PARTITION OF recording_segments
    FOR VALUES FROM ('2030-12-01T00:00:00Z') TO ('2031-01-01T00:00:00Z');

-- ---------------------------------------------------------------------------
-- The policies, the force, and the grants
-- ---------------------------------------------------------------------------
--
-- A table born after migration 0008's and 0023's catalogue loops gets nothing
-- from either loop's next run: both loops ask the catalogue about tables as it
-- was. So the policies, the force, AND the grants all belong here, together.

ALTER TABLE recording_segments ENABLE ROW LEVEL SECURITY;
-- FORCE, not just ENABLE: the table owner would otherwise bypass its own
-- policies, and an owner connection is exactly the connection a maintenance
-- script tends to use.
ALTER TABLE recording_segments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON recording_segments
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON recording_segments
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

ALTER TABLE recording_chains ENABLE ROW LEVEL SECURITY;
ALTER TABLE recording_chains FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON recording_chains
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON recording_chains
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

ALTER TABLE recording_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE recording_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON recording_policies
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON recording_policies
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

-- The PARTITIONS get their own policies, and the reason is the same one
-- migration 0015 gives for the events tables: a policy on the parent is not
-- inherited by a partition.
--
-- A partition is a table in its own right. The parent's policies apply when a
-- query goes THROUGH the parent, and a query that names a partition -- which
-- a retention sweep does, and which an operator's own SQL does -- is governed
-- by the PARTITION's policies. A partition with no policy is a relation
-- anyone may read and anyone may write, and a recording index with such a
-- partition is an index whose contents can be replaced.
--
-- The loop covers every partition of `recording_segments`, including the
-- DEFAULT one, because the DEFAULT partition is where a segment for an
-- un-created month lands -- and that is exactly the segment nobody is
-- watching.
DO $$
DECLARE
    t RECORD;
BEGIN
    FOR t IN
        SELECT c.relname
          FROM pg_class c
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND c.relkind = 'r'
           AND c.relname ~ '^recording_segments_(20[0-9]{2}_[0-9]{2}|default)$'
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t.relname);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t.relname);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_current_tenant() OR app_is_system_worker()) '
            'WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker())',
            t.relname);
        EXECUTE format(
            'CREATE POLICY site_isolation ON %I USING (app_site_visible(site_id) OR app_is_system_worker()) '
            'WITH CHECK (app_site_visible(site_id) OR app_is_system_worker())',
            t.relname);
    END LOOP;
END;
$$;

-- The recorder WRITES the segments and the chain, and READS the policies. It
-- does not DELETE them: deletion is the retention job's, and the retention job
-- runs in the core, which holds the ledger. A recorder that could delete its
-- own segments is a recorder that can hide a gap.
GRANT SELECT, INSERT, UPDATE ON recording_segments TO security_core;
GRANT SELECT, INSERT, UPDATE ON recording_chains TO security_core;
GRANT SELECT, INSERT, UPDATE, DELETE ON recording_policies TO security_core;
GRANT SELECT, INSERT ON recording_segment_keys TO security_core;

COMMENT ON TABLE recording_segments IS
    'One row per recorded segment. The bytes live on the filesystem, in the customer''s own '
    'disk; this is the index over them. The digest is what the chain is over, and the chain is '
    'what makes the recording provable months later. Created by migration 0032 for the AI VMS '
    'media plane.';

COMMENT ON COLUMN recording_segments.gap_before_ms IS
    'The MEASURED gap between the previous segment and this one. A camera that was quiet for '
    'four minutes and one running at 25 frames a second differ only in this number, and the '
    'recording-continuity metric is computed from it. A gap is a fact about the camera, and a '
    'recording that cannot say it had one is a recording that lies by omission.';

COMMENT ON COLUMN recording_segments.state IS
    '`complete` or `partial`. A partial segment was cut short and the boundary is the TRUE '
    'boundary. A partial segment is never a silent hole: it is a hole that says how big it '
    'is.';

COMMENT ON TABLE recording_chains IS
    'One row per device per day, holding the head of that day''s chain. The head is SEALED into '
    'the audit ledger at day close -- one ledger row per device-day, because a ledger row per '
    'segment at 8 640 segments a day per device is a self-inflicted denial of service on the '
    'ledger.';

COMMENT ON TABLE recording_policies IS
    'How long a segment is kept, per site and per device. A NULL device is the site''s own '
    'policy, which every device inherits. `legal_hold` blocks deletion, and it is the one row '
    'in the product that can say "you may not delete this".';
