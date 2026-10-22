-- 0029_metric_rollups.expand.sql
--
-- The durable metric rollup: one row per (metric, bucket, site, scope).
--
-- # What this table is for
--
-- `internal/observability` is the LIVE half of the metric surface: a registry of
-- counters and gauges, a set of objectives, and an exposition format. It answers
-- "what is happening right now" and it is explicitly not the source of truth for a
-- number anybody signs. The registry is a process's memory; it resets on restart,
-- and a report built on it would restart too.
--
-- This table is the other half. Every number in it is a Postgres computation over
-- the tables the product already writes, which is the rule
-- `docs/roadmap/engineering/metrics-and-analytics.md` states outright: Postgres is
-- the source of truth and the counter is for live SLO evaluation only. A gate
-- artefact, a release note and a customer report all read these rows.
--
-- # The metric vocabulary is CLOSED, and the CHECK is the copy
--
-- `metric` is constrained to the vocabulary in `internal/operatormetrics`, which is
-- the same closed set the JSON Schema's enum carries and the same set the report
-- renders. Three copies of a vocabulary is two too many, so the list below is
-- checked against the Go set by `internal/operatormetrics`'s own test, and that test
-- fails the build if a name is added to one and not the others.
--
-- The alternative -- a free-text metric column -- is what the live registry is, and
-- the live registry's problem is the reason this table exists.
--
-- # `site_id` is NOT NULL, and that is the load-bearing decision here
--
-- `app_site_visible(x)` returns TRUE when `x` IS NULL. A site-scoped principal can
-- therefore see a row whose site is NULL, and a tenant-wide row -- "the whole
-- tenant's attention minutes", rolled up with `site_id` left NULL -- would be
-- visible to a principal who is assigned to ONE site. The principal would read
-- another site's aggregate attention minutes inside its own scope.
--
-- So the tenant-wide view is not a row. It is the SUM of the site rows the
-- principal may see, computed by the reader. `site_id` is NOT NULL, and a
-- tenant-wide aggregate is something the report computes rather than something the
-- table stores.
--
-- # `scope_key`: uniqueness across NULLs
--
-- Postgres forbids NULL in a unique key, and `rule_id` and `device_id` are both
-- nullable here -- NULL means "every class" and "every device". Two rows for the
-- same metric, bucket and site, one for one rule and one for all rules, are
-- DIFFERENT rows and must both exist.
--
-- The generated column carries the uniqueness instead:
--
--     scope_key = coalesce(rule_id, '') || ':' || coalesce(device_id, '')
--
-- The empty string is the marker for "not scoped", and the colon separator is what
-- stops `rule_id = 'a:b'` with no device from colliding with `rule_id = 'a'` and
-- `device_id = 'b'`.
--
-- # The bucketing
--
-- A bucket is a half-open interval `[bucket_start, bucket_end)`. The writer closes
-- a bucket when it rolls, and the boundaries are derived from the window rather
-- than from the time the writer happened to run -- so a bucket is the same bucket
-- whether it was written on time, late, or rebuilt from the raw tables.
--
-- That property is the whole point: the rollup is a DERIVATION, not a recording of
-- a moment. Any bucket can be re-derived from the raw tables, and running the
-- derivation twice produces the same row.
--
-- # Expand, not contract
--
-- A new table, a new index, two new policies and a grant. No column is added to an
-- existing table, no data is moved, and nothing that reads the product's existing
-- tables changes behaviour. The contract step -- if one is ever needed -- is a
-- separate file, and `TestM1ContainsNoContractStep` refuses a contract file at all.

CREATE TABLE IF NOT EXISTS metric_rollups (
    tenant_id   TEXT NOT NULL,
    -- The metric's wire name. The vocabulary is closed and the list is in
    -- `internal/operatormetrics`.
    metric      TEXT NOT NULL
                CHECK (metric IN (
                    'canonical_events',
                    'attention_minutes_capped',
                    'attention_minutes_uncapped',
                    'attention_incidents',
                    'incidents_raised',
                    'verification_corroborated',
                    'verification_unverified',
                    'verification_contradicted',
                    'verification_duplicate',
                    'verification_suppressed',
                    'dispositions_agreed',
                    'dispositions_disagreed',
                    'dispositions_unsure',
                    'dispositions_no_ai',
                    'ai_observations',
                    'ai_observations_degraded',
                    'ai_observations_insufficient',
                    'evidence_items_stored',
                    'evidence_fetch_failed',
                    'north_star_attention_minutes_per_1000_events',
                    'operator_override_rate',
                    'ai_false_escalation_rate',
                    'evidence_completeness',
                    'missed_incident_rate'
                )),
    -- The bucket is half-open: [bucket_start, bucket_end).
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_end   TIMESTAMPTZ NOT NULL,
    -- NOT NULL, and the reason is in the section above: a NULL site is a
    -- tenant-wide row, and `app_site_visible(NULL)` is TRUE, so a NULL site
    -- would put one site's aggregate in another site's scope.
    site_id      TEXT NOT NULL,
    -- NULL means "every class". Only two metrics are recorded per device
    -- (`canonical_events` and `incidents_raised`), which is the per-camera ratio
    -- M1 gate 5 asks for.
    rule_id      TEXT,
    device_id    TEXT,
    -- The measured value. A DOUBLE, because a rollup of a duration or a ratio is
    -- not an integer, and a column that can only hold whole numbers would make
    -- the first ratio the product publishes impossible to store.
    value        DOUBLE PRECISION NOT NULL,
    -- The N behind the value: how many things were counted. A rollup of a
    -- duration has one incident behind it and a rollup of a count has as many as
    -- the count.
    n            BIGINT NOT NULL CHECK (n >= 0),
    -- The two halves of a rate, when the metric IS a rate. NULL for a count,
    -- because there is nothing to divide.
    numerator    DOUBLE PRECISION,
    denominator  DOUBLE PRECISION,
    -- When this row was computed. It is NOT the bucket: the bucket says which
    -- period the number covers and this says when the number was derived, and a
    -- reader checking freshness wants the second.
    computed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The scope in one string, so the primary key can be a key.
    --
    -- GENERATED ALWAYS AS ... STORED, so it cannot drift from the columns it is
    -- built from: a scope key a caller could write by hand is a scope key that
    -- disagrees with the row.
    scope_key    TEXT GENERATED ALWAYS AS (coalesce(rule_id, '') || ':' || coalesce(device_id, '')) STORED,
    -- A bucket is a non-empty interval.
    CONSTRAINT metric_rollups_bucket_is_ordered CHECK (bucket_end > bucket_start),
    -- The uniqueness, with the NULL-bearing scope carried by `scope_key`.
    PRIMARY KEY (tenant_id, metric, bucket_start, site_id, scope_key)
);

-- One index per direction the product asks by: every metric for a tenant over
-- time, and one site's metrics over time.
--
-- The DESC on `bucket_start` is the read order: a dashboard asks for the most
-- recent buckets first, and a b-tree that has to be reversed for every page is a
-- b-tree that is read the wrong way round.
CREATE INDEX IF NOT EXISTS metric_rollups_tenant_metric_idx
    ON metric_rollups (tenant_id, metric, bucket_start DESC);
CREATE INDEX IF NOT EXISTS metric_rollups_tenant_site_metric_idx
    ON metric_rollups (tenant_id, site_id, metric, bucket_start DESC);

-- ---------------------------------------------------------------------------
-- The policies, the force, and the grant
-- ---------------------------------------------------------------------------
--
-- A table born after migration 0008's and 0023's catalogue loops gets nothing
-- from either loop's next run: both loops ask the catalogue about tables as it
-- was. So the policies, the force, AND the grant all belong here, together.
ALTER TABLE metric_rollups ENABLE ROW LEVEL SECURITY;
-- FORCE, not just ENABLE: the table owner would otherwise bypass its own
-- policies, and an owner connection is exactly the connection a maintenance
-- script tends to use.
ALTER TABLE metric_rollups FORCE ROW LEVEL SECURITY;
-- Tenant first, site second: the two compose, and the site policy is the
-- RESTRICTIVE one. A permissive site policy ORed with the tenant one and a
-- cross-tenant write with a borrowed site id would land.
CREATE POLICY tenant_isolation ON metric_rollups
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON metric_rollups
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

-- The role's privileges are the OTHER half a table created after 0008 lacks.
-- Without the GRANT a correct-scope write from `security_core` is refused with
-- `permission denied`, which reads as a policy failure and is a missing GRANT,
-- and it would cost an outage rather than a red test.
GRANT SELECT, INSERT, UPDATE, DELETE ON metric_rollups TO security_core;

COMMENT ON TABLE metric_rollups IS
    'The durable metric rollup: one row per (metric, bucket, site, scope). Every value '
    'is a Postgres computation over the tables the product already writes, so a report '
    'signed by somebody survives a restart. The metric vocabulary is closed and the CHECK '
    'constraint carries the list; `site_id` is NOT NULL because `app_site_visible(NULL)` '
    'is TRUE and a tenant-wide row would put one site''s aggregate in another site''s '
    'scope; the tenant-wide view is the SUM of the site rows a principal may see, not a '
    'row. Created by migration 0029; written by `internal/operatormetrics`''s rollup '
    'writer and read by the report.';

COMMENT ON COLUMN metric_rollups.scope_key IS
    'coalesce(rule_id, '''') || '':'' || coalesce(device_id, ''''), generated so it cannot '
    'drift from the columns it is built from. It exists because Postgres forbids NULL in '
    'a unique key and both rule_id and device_id are nullable: without it, a row for one '
    'rule and a row for every rule would collide, or would not be unique at all.';

COMMENT ON COLUMN metric_rollups.n IS
    'How many things the value counts. A rollup of a duration has one incident behind it '
    'and a rollup of a count has as many as the count. It is the N every rate needs and '
    'the number a reader needs to know whether an average is an average.';
