-- 0015_partitions_and_correlation_indexes.expand.sql
--
-- Intake stops accepting rows on 2027-01-01, and two hot paths do a sequential
-- scan per candidate row.
--
-- 1. Partitions run out.
--    0002 declared inbox_events_2026_01..12 and 0003 declared
--    canonical_events_2026_01..12. Both sets end at 2027-01-01. A range
--    partitioned table with no partition covering a row and no DEFAULT partition
--    raises "no partition of relation inbox_events found for row" on INSERT.
--    There is no Go code that creates a partition, so the failure is a calendar,
--    not a bug report: on the morning of 2027-01-01 every inbox.Write fails and
--    every canonical insert fails.
--
--    This migration adds the 2027 partitions and a DEFAULT partition for both
--    tables. The DEFAULT is the part that matters most: it means the next
--    December is not a code change. A row that lands outside the declared
--    months goes somewhere real instead of raising, so the operator gets an
--    accepted event and a visible backlog rather than a refused write. The
--    explicit 2027 partitions keep the common path on a named partition, which
--    is what the retention sweeper and the partition-level statements read.
--
-- 2. correlation scans ai_observations per candidate row.
--    The correlator joins observations on (tenant_id, event_id). The five
--    indexes from 0005 all lead with incident_id, profile_id, degraded_reason,
--    supersedes_observation_id or production_profile — none leads with event_id,
--    so each of up to 2000 candidate rows per evaluation costs a sequential
--    scan. This index is the fix.
--
-- 3. The inbox claim cannot use its index.
--    inbox_events_pending_idx leads with received_at and is partial on the three
--    claimable statuses. The claim predicate is a three-way OR including
--    "($4='' OR tenant_id=$4)", which the planner resolves as a BitmapOr over
--    two partial indexes and thereby loses the received_at ordering that
--    FOR UPDATE SKIP LOCKED ... ORDER BY received_at needs. Leading with
--    tenant_id makes the tenant arm of the OR indexable, which is what the
--    claim actually needs when a tenant is supplied.
--
-- 4. canonical_events has no index on its window predicate.
--    AGENTS.md is explicit that normalized_at is the only field a time-window
--    predicate may read, and the correlator obeys that. The table is
--    partitioned by observed_at, so Postgres cannot prune on normalized_at and
--    every evaluation touches all partitions. Repartitioning on normalized_at
--    is the correct answer and is a schema decision with a blast radius that
--    reaches the dedupe key, the replay path and the golden files; it is
--    recorded as an M2 item with a stated trigger rather than smuggled in here.
--    What ships now is a BRIN index: the table is append-only in
--    normalized_at order, so block-range summarisation is near-optimal and the
--    index is tiny. This makes the full scan cheap. It does not make it
--    pruned, and a correlation-latency SLO is added alongside it so the cost
--    is measured rather than assumed.
--
-- Row-level security: 0008 applied tenant_isolation and site_isolation to every
-- tenant-bearing relation that existed at the time, partitions included. Those
-- policies were created ON THE PARTITION, because PostgreSQL applies a
-- partitioned table's partitions' policies and not the parent's. A new
-- partition created without them is an unfiltered relation. The block at the
-- end applies the same two policies to the partitions this migration creates,
-- so no new partition is ever born wider than the one before it.

-- inbox_events: 2027 monthly partitions.
CREATE TABLE inbox_events_2027_01 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-01-01T00:00:00Z') TO ('2027-02-01T00:00:00Z');
CREATE TABLE inbox_events_2027_02 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-02-01T00:00:00Z') TO ('2027-03-01T00:00:00Z');
CREATE TABLE inbox_events_2027_03 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-03-01T00:00:00Z') TO ('2027-04-01T00:00:00Z');
CREATE TABLE inbox_events_2027_04 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-04-01T00:00:00Z') TO ('2027-05-01T00:00:00Z');
CREATE TABLE inbox_events_2027_05 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-05-01T00:00:00Z') TO ('2027-06-01T00:00:00Z');
CREATE TABLE inbox_events_2027_06 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-06-01T00:00:00Z') TO ('2027-07-01T00:00:00Z');
CREATE TABLE inbox_events_2027_07 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-07-01T00:00:00Z') TO ('2027-08-01T00:00:00Z');
CREATE TABLE inbox_events_2027_08 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-08-01T00:00:00Z') TO ('2027-09-01T00:00:00Z');
CREATE TABLE inbox_events_2027_09 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-09-01T00:00:00Z') TO ('2027-10-01T00:00:00Z');
CREATE TABLE inbox_events_2027_10 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-10-01T00:00:00Z') TO ('2027-11-01T00:00:00Z');
CREATE TABLE inbox_events_2027_11 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-11-01T00:00:00Z') TO ('2027-12-01T00:00:00Z');
CREATE TABLE inbox_events_2027_12 PARTITION OF inbox_events
    FOR VALUES FROM ('2027-12-01T00:00:00Z') TO ('2028-01-01T00:00:00Z');

-- canonical_events: the same twelve months, on the same boundaries.
CREATE TABLE canonical_events_2027_01 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-01-01T00:00:00Z') TO ('2027-02-01T00:00:00Z');
CREATE TABLE canonical_events_2027_02 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-02-01T00:00:00Z') TO ('2027-03-01T00:00:00Z');
CREATE TABLE canonical_events_2027_03 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-03-01T00:00:00Z') TO ('2027-04-01T00:00:00Z');
CREATE TABLE canonical_events_2027_04 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-04-01T00:00:00Z') TO ('2027-05-01T00:00:00Z');
CREATE TABLE canonical_events_2027_05 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-05-01T00:00:00Z') TO ('2027-06-01T00:00:00Z');
CREATE TABLE canonical_events_2027_06 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-06-01T00:00:00Z') TO ('2027-07-01T00:00:00Z');
CREATE TABLE canonical_events_2027_07 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-07-01T00:00:00Z') TO ('2027-08-01T00:00:00Z');
CREATE TABLE canonical_events_2027_08 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-08-01T00:00:00Z') TO ('2027-09-01T00:00:00Z');
CREATE TABLE canonical_events_2027_09 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-09-01T00:00:00Z') TO ('2027-10-01T00:00:00Z');
CREATE TABLE canonical_events_2027_10 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-10-01T00:00:00Z') TO ('2027-11-01T00:00:00Z');
CREATE TABLE canonical_events_2027_11 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-11-01T00:00:00Z') TO ('2027-12-01T00:00:00Z');
CREATE TABLE canonical_events_2027_12 PARTITION OF canonical_events
    FOR VALUES FROM ('2027-12-01T00:00:00Z') TO ('2028-01-01T00:00:00Z');

-- The DEFAULT partitions. Without these the 2028-01-01 boundary is exactly as
-- fatal as 2027-01-01 was; with them, an out-of-range row is accepted and
-- lands in a partition an operator can see and a sweeper can still find.
CREATE TABLE inbox_events_default PARTITION OF inbox_events DEFAULT;
CREATE TABLE canonical_events_default PARTITION OF canonical_events DEFAULT;

-- The correlation hot path's missing index.
CREATE INDEX ai_observations_event_idx ON ai_observations (tenant_id, event_id);

-- The claim path's missing index.
CREATE INDEX inbox_events_tenant_received_idx ON inbox_events (tenant_id, received_at);

-- The window predicate's index. BRIN on an append-only, time-ordered table is
-- close to free in size and prunes at page-range granularity, which is the only
-- granularity that is useful when the scan cannot be pruned at all.
CREATE INDEX canonical_events_normalized_brin ON canonical_events
    USING BRIN (normalized_at);

-- Row-level security for the partitions created above. Same shape as 0008,
-- same reason: a policy on the parent is not inherited by a partition.
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
           -- Only the partitions this migration creates. The 2026 ones already
           -- carry these policies from 0008, and re-creating a policy is an error
           -- that would abort the whole step.
           AND c.relname ~ '^(inbox_events|canonical_events)_(2027_[0-9]{2}|default)$'
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
