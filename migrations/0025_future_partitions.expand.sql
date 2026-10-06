-- 0025_future_partitions.expand.sql
--
-- Extends the partition horizon from 2028-01 to 2031-01 (three more years).
-- 0015 added 2027 partitions and DEFAULT partitions for both inbox_events and
-- canonical_events. The DEFAULT partitions mean ingest never fails, but explicit
-- monthly partitions are what the retention sweeper and partition-level statements
-- read, and they are what the query planner prunes. Without them, every INSERT
-- and SELECT hits the DEFAULT partition, which defeats the whole point of
-- partitioning.
--
-- This migration adds 2028, 2029, 2030 monthly partitions for both tables,
-- plus RLS policies on each new partition (same shape as 0008/0015).

-- inbox_events: 2028 monthly partitions.
CREATE TABLE inbox_events_2028_01 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-01-01T00:00:00Z') TO ('2028-02-01T00:00:00Z');
CREATE TABLE inbox_events_2028_02 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-02-01T00:00:00Z') TO ('2028-03-01T00:00:00Z');
CREATE TABLE inbox_events_2028_03 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-03-01T00:00:00Z') TO ('2028-04-01T00:00:00Z');
CREATE TABLE inbox_events_2028_04 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-04-01T00:00:00Z') TO ('2028-05-01T00:00:00Z');
CREATE TABLE inbox_events_2028_05 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-05-01T00:00:00Z') TO ('2028-06-01T00:00:00Z');
CREATE TABLE inbox_events_2028_06 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-06-01T00:00:00Z') TO ('2028-07-01T00:00:00Z');
CREATE TABLE inbox_events_2028_07 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-07-01T00:00:00Z') TO ('2028-08-01T00:00:00Z');
CREATE TABLE inbox_events_2028_08 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-08-01T00:00:00Z') TO ('2028-09-01T00:00:00Z');
CREATE TABLE inbox_events_2028_09 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-09-01T00:00:00Z') TO ('2028-10-01T00:00:00Z');
CREATE TABLE inbox_events_2028_10 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-10-01T00:00:00Z') TO ('2028-11-01T00:00:00Z');
CREATE TABLE inbox_events_2028_11 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-11-01T00:00:00Z') TO ('2028-12-01T00:00:00Z');
CREATE TABLE inbox_events_2028_12 PARTITION OF inbox_events
    FOR VALUES FROM ('2028-12-01T00:00:00Z') TO ('2029-01-01T00:00:00Z');

-- inbox_events: 2029 monthly partitions.
CREATE TABLE inbox_events_2029_01 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-01-01T00:00:00Z') TO ('2029-02-01T00:00:00Z');
CREATE TABLE inbox_events_2029_02 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-02-01T00:00:00Z') TO ('2029-03-01T00:00:00Z');
CREATE TABLE inbox_events_2029_03 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-03-01T00:00:00Z') TO ('2029-04-01T00:00:00Z');
CREATE TABLE inbox_events_2029_04 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-04-01T00:00:00Z') TO ('2029-05-01T00:00:00Z');
CREATE TABLE inbox_events_2029_05 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-05-01T00:00:00Z') TO ('2029-06-01T00:00:00Z');
CREATE TABLE inbox_events_2029_06 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-06-01T00:00:00Z') TO ('2029-07-01T00:00:00Z');
CREATE TABLE inbox_events_2029_07 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-07-01T00:00:00Z') TO ('2029-08-01T00:00:00Z');
CREATE TABLE inbox_events_2029_08 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-08-01T00:00:00Z') TO ('2029-09-01T00:00:00Z');
CREATE TABLE inbox_events_2029_09 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-09-01T00:00:00Z') TO ('2029-10-01T00:00:00Z');
CREATE TABLE inbox_events_2029_10 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-10-01T00:00:00Z') TO ('2029-11-01T00:00:00Z');
CREATE TABLE inbox_events_2029_11 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-11-01T00:00:00Z') TO ('2029-12-01T00:00:00Z');
CREATE TABLE inbox_events_2029_12 PARTITION OF inbox_events
    FOR VALUES FROM ('2029-12-01T00:00:00Z') TO ('2030-01-01T00:00:00Z');

-- inbox_events: 2030 monthly partitions.
CREATE TABLE inbox_events_2030_01 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-01-01T00:00:00Z') TO ('2030-02-01T00:00:00Z');
CREATE TABLE inbox_events_2030_02 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-02-01T00:00:00Z') TO ('2030-03-01T00:00:00Z');
CREATE TABLE inbox_events_2030_03 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-03-01T00:00:00Z') TO ('2030-04-01T00:00:00Z');
CREATE TABLE inbox_events_2030_04 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-04-01T00:00:00Z') TO ('2030-05-01T00:00:00Z');
CREATE TABLE inbox_events_2030_05 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-05-01T00:00:00Z') TO ('2030-06-01T00:00:00Z');
CREATE TABLE inbox_events_2030_06 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-06-01T00:00:00Z') TO ('2030-07-01T00:00:00Z');
CREATE TABLE inbox_events_2030_07 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-07-01T00:00:00Z') TO ('2030-08-01T00:00:00Z');
CREATE TABLE inbox_events_2030_08 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-08-01T00:00:00Z') TO ('2030-09-01T00:00:00Z');
CREATE TABLE inbox_events_2030_09 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-09-01T00:00:00Z') TO ('2030-10-01T00:00:00Z');
CREATE TABLE inbox_events_2030_10 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-10-01T00:00:00Z') TO ('2030-11-01T00:00:00Z');
CREATE TABLE inbox_events_2030_11 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-11-01T00:00:00Z') TO ('2030-12-01T00:00:00Z');
CREATE TABLE inbox_events_2030_12 PARTITION OF inbox_events
    FOR VALUES FROM ('2030-12-01T00:00:00Z') TO ('2031-01-01T00:00:00Z');

-- canonical_events: the same twelve months for 2028, on the same boundaries.
CREATE TABLE canonical_events_2028_01 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-01-01T00:00:00Z') TO ('2028-02-01T00:00:00Z');
CREATE TABLE canonical_events_2028_02 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-02-01T00:00:00Z') TO ('2028-03-01T00:00:00Z');
CREATE TABLE canonical_events_2028_03 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-03-01T00:00:00Z') TO ('2028-04-01T00:00:00Z');
CREATE TABLE canonical_events_2028_04 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-04-01T00:00:00Z') TO ('2028-05-01T00:00:00Z');
CREATE TABLE canonical_events_2028_05 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-05-01T00:00:00Z') TO ('2028-06-01T00:00:00Z');
CREATE TABLE canonical_events_2028_06 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-06-01T00:00:00Z') TO ('2028-07-01T00:00:00Z');
CREATE TABLE canonical_events_2028_07 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-07-01T00:00:00Z') TO ('2028-08-01T00:00:00Z');
CREATE TABLE canonical_events_2028_08 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-08-01T00:00:00Z') TO ('2028-09-01T00:00:00Z');
CREATE TABLE canonical_events_2028_09 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-09-01T00:00:00Z') TO ('2028-10-01T00:00:00Z');
CREATE TABLE canonical_events_2028_10 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-10-01T00:00:00Z') TO ('2028-11-01T00:00:00Z');
CREATE TABLE canonical_events_2028_11 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-11-01T00:00:00Z') TO ('2028-12-01T00:00:00Z');
CREATE TABLE canonical_events_2028_12 PARTITION OF canonical_events
    FOR VALUES FROM ('2028-12-01T00:00:00Z') TO ('2029-01-01T00:00:00Z');

-- canonical_events: 2029 monthly partitions.
CREATE TABLE canonical_events_2029_01 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-01-01T00:00:00Z') TO ('2029-02-01T00:00:00Z');
CREATE TABLE canonical_events_2029_02 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-02-01T00:00:00Z') TO ('2029-03-01T00:00:00Z');
CREATE TABLE canonical_events_2029_03 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-03-01T00:00:00Z') TO ('2029-04-01T00:00:00Z');
CREATE TABLE canonical_events_2029_04 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-04-01T00:00:00Z') TO ('2029-05-01T00:00:00Z');
CREATE TABLE canonical_events_2029_05 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-05-01T00:00:00Z') TO ('2029-06-01T00:00:00Z');
CREATE TABLE canonical_events_2029_06 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-06-01T00:00:00Z') TO ('2029-07-01T00:00:00Z');
CREATE TABLE canonical_events_2029_07 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-07-01T00:00:00Z') TO ('2029-08-01T00:00:00Z');
CREATE TABLE canonical_events_2029_08 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-08-01T00:00:00Z') TO ('2029-09-01T00:00:00Z');
CREATE TABLE canonical_events_2029_09 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-09-01T00:00:00Z') TO ('2029-10-01T00:00:00Z');
CREATE TABLE canonical_events_2029_10 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-10-01T00:00:00Z') TO ('2029-11-01T00:00:00Z');
CREATE TABLE canonical_events_2029_11 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-11-01T00:00:00Z') TO ('2029-12-01T00:00:00Z');
CREATE TABLE canonical_events_2029_12 PARTITION OF canonical_events
    FOR VALUES FROM ('2029-12-01T00:00:00Z') TO ('2030-01-01T00:00:00Z');

-- canonical_events: 2030 monthly partitions.
CREATE TABLE canonical_events_2030_01 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-01-01T00:00:00Z') TO ('2030-02-01T00:00:00Z');
CREATE TABLE canonical_events_2030_02 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-02-01T00:00:00Z') TO ('2030-03-01T00:00:00Z');
CREATE TABLE canonical_events_2030_03 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-03-01T00:00:00Z') TO ('2030-04-01T00:00:00Z');
CREATE TABLE canonical_events_2030_04 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-04-01T00:00:00Z') TO ('2030-05-01T00:00:00Z');
CREATE TABLE canonical_events_2030_05 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-05-01T00:00:00Z') TO ('2030-06-01T00:00:00Z');
CREATE TABLE canonical_events_2030_06 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-06-01T00:00:00Z') TO ('2030-07-01T00:00:00Z');
CREATE TABLE canonical_events_2030_07 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-07-01T00:00:00Z') TO ('2030-08-01T00:00:00Z');
CREATE TABLE canonical_events_2030_08 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-08-01T00:00:00Z') TO ('2030-09-01T00:00:00Z');
CREATE TABLE canonical_events_2030_09 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-09-01T00:00:00Z') TO ('2030-10-01T00:00:00Z');
CREATE TABLE canonical_events_2030_10 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-10-01T00:00:00Z') TO ('2030-11-01T00:00:00Z');
CREATE TABLE canonical_events_2030_11 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-11-01T00:00:00Z') TO ('2030-12-01T00:00:00Z');
CREATE TABLE canonical_events_2030_12 PARTITION OF canonical_events
    FOR VALUES FROM ('2030-12-01T00:00:00Z') TO ('2031-01-01T00:00:00Z');

-- Row-level security for the partitions created above. Same shape as 0015,
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
           -- Only the partitions this migration creates.
           AND c.relname ~ '^(inbox_events|canonical_events)_(202[8-9]_[0-9]{2}|2030_[0-9]{2})$'
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