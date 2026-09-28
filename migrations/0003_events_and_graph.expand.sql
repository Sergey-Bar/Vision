-- 0003_events_and_graph.expand.sql
--
-- Canonical events and the M1 Security Graph.
--
-- M1 graph scope is topology and provenance only. Person identity resolution,
-- cross-camera identity, re-identification, behavioral graphs and entity
-- aliasing are explicitly out, and there is no entity_aliases table here. An
-- entity is only ever a concrete thing a device reported: a plate, a badge, a
-- door.

CREATE TABLE canonical_events (
    tenant_id           TEXT NOT NULL,
    event_id            TEXT NOT NULL,
    site_id             TEXT NOT NULL,
    source_system_id    TEXT NOT NULL,
    source_event_id     TEXT NOT NULL,
    integration_id      TEXT NOT NULL,
    event_type          TEXT NOT NULL,
    schema_version      TEXT NOT NULL,
    -- What the source claimed.
    observed_at         TIMESTAMPTZ NOT NULL,
    -- Our ingest.
    received_at         TIMESTAMPTZ NOT NULL,
    -- Drift-corrected. The only field a time-window predicate may read.
    normalized_at       TIMESTAMPTZ NOT NULL,
    source_timezone     TEXT,
    clock_source        TEXT NOT NULL DEFAULT 'unknown'
                        CHECK (clock_source IN ('ntp', 'vms', 'host', 'unknown')),
    clock_drift_ms      INTEGER NOT NULL DEFAULT 0,
    device_id           TEXT,
    zone_id             TEXT,
    correlation_id      TEXT,
    causation_id        TEXT,
    payload             JSONB NOT NULL DEFAULT '{}'::jsonb,
    vendor_payload_ref  TEXT,
    integrity_hash      TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
) PARTITION BY RANGE (observed_at);

CREATE TABLE canonical_events_2026_01 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-01-01T00:00:00Z') TO ('2026-02-01T00:00:00Z');
CREATE TABLE canonical_events_2026_02 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-02-01T00:00:00Z') TO ('2026-03-01T00:00:00Z');
CREATE TABLE canonical_events_2026_03 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-03-01T00:00:00Z') TO ('2026-04-01T00:00:00Z');
CREATE TABLE canonical_events_2026_04 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-04-01T00:00:00Z') TO ('2026-05-01T00:00:00Z');
CREATE TABLE canonical_events_2026_05 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-05-01T00:00:00Z') TO ('2026-06-01T00:00:00Z');
CREATE TABLE canonical_events_2026_06 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-06-01T00:00:00Z') TO ('2026-07-01T00:00:00Z');
CREATE TABLE canonical_events_2026_07 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-07-01T00:00:00Z') TO ('2026-08-01T00:00:00Z');
CREATE TABLE canonical_events_2026_08 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-08-01T00:00:00Z') TO ('2026-09-01T00:00:00Z');
CREATE TABLE canonical_events_2026_09 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-09-01T00:00:00Z') TO ('2026-10-01T00:00:00Z');
CREATE TABLE canonical_events_2026_10 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-10-01T00:00:00Z') TO ('2026-11-01T00:00:00Z');
CREATE TABLE canonical_events_2026_11 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-11-01T00:00:00Z') TO ('2026-12-01T00:00:00Z');
CREATE TABLE canonical_events_2026_12 PARTITION OF canonical_events
    FOR VALUES FROM ('2026-12-01T00:00:00Z') TO ('2027-01-01T00:00:00Z');

-- Facts are never rewritten. A new interpretation is a new row, which is what
-- lets a months-old decision replay from the same stored fact it was made from.
--
-- Uniqueness is scoped globally, so — as with the inbox — the key lives in its
-- own non-partitioned table: a partitioned table's unique index must include
-- the partition key, which would scope uniqueness to a month.
CREATE TABLE canonical_event_keys (
    tenant_id        TEXT NOT NULL,
    integration_id   TEXT NOT NULL,
    source_event_id  TEXT NOT NULL,
    event_id         TEXT NOT NULL,
    PRIMARY KEY (tenant_id, integration_id, source_event_id)
);

CREATE INDEX canonical_events_site_time_idx ON canonical_events (tenant_id, site_id, normalized_at);
CREATE INDEX canonical_events_device_time_idx ON canonical_events (tenant_id, site_id, device_id, normalized_at);
CREATE INDEX canonical_events_tenant_time_idx ON canonical_events (tenant_id, normalized_at);
CREATE INDEX canonical_events_correlation_idx ON canonical_events (tenant_id, correlation_id)
    WHERE correlation_id IS NOT NULL;
CREATE INDEX canonical_events_type_time_idx ON canonical_events (tenant_id, event_type, normalized_at);
CREATE INDEX canonical_events_payload_gin_idx ON canonical_events USING GIN (payload jsonb_path_ops);

-- Per-device clock state. A device past the severe band is excluded from
-- time-windowed corroboration, and the exclusion is recorded on the
-- verification result rather than applied silently.
CREATE TABLE device_clocks (
    tenant_id    TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    clock_source TEXT NOT NULL DEFAULT 'unknown'
                 CHECK (clock_source IN ('ntp', 'vms', 'host', 'unknown')),
    drift_ms     INTEGER NOT NULL DEFAULT 0,
    band         TEXT NOT NULL DEFAULT 'ok' CHECK (band IN ('ok', 'warn', 'severe')),
    reference_id TEXT,
    measured_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, device_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE CASCADE
);

CREATE INDEX device_clocks_severe_idx ON device_clocks (tenant_id, site_id) WHERE band = 'severe';

CREATE TABLE entities (
    tenant_id          TEXT NOT NULL,
    entity_id          TEXT NOT NULL,
    site_id            TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN
                         ('license_plate', 'badge', 'door', 'gate', 'vehicle', 'device', 'other')),
    observed_value     TEXT NOT NULL,
    zone_id            TEXT,
    device_id          TEXT,
    first_observed_at  TIMESTAMPTZ NOT NULL,
    last_observed_at   TIMESTAMPTZ NOT NULL,
    observation_count  BIGINT NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, entity_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX entities_identity_idx
    ON entities (tenant_id, site_id, kind, observed_value);
CREATE INDEX entities_time_idx ON entities (tenant_id, site_id, last_observed_at);
CREATE INDEX entities_device_idx ON entities (tenant_id, device_id) WHERE device_id IS NOT NULL;

-- Every edge carries provenance. An edge with no asserted_by is not allowed in.
CREATE TABLE edges (
    edge_id          TEXT NOT NULL,
    tenant_id        TEXT NOT NULL,
    from_type        TEXT NOT NULL,
    from_id          TEXT NOT NULL,
    relation         TEXT NOT NULL,
    to_type          TEXT NOT NULL,
    to_id            TEXT NOT NULL,
    asserted_by      TEXT NOT NULL,
    source_event_id  TEXT,
    observed_at      TIMESTAMPTZ,
    confidence       DOUBLE PRECISION NOT NULL DEFAULT 1.0
                     CHECK (confidence >= 0.0 AND confidence <= 1.0),
    method           TEXT NOT NULL CHECK (method IN ('observed', 'asserted', 'inferred')),
    rule_or_model_id TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, edge_id)
);

CREATE UNIQUE INDEX edges_unique_idx
    ON edges (tenant_id, from_type, from_id, relation, to_type, to_id);
CREATE INDEX edges_from_idx ON edges (tenant_id, from_type, from_id);
CREATE INDEX edges_to_idx ON edges (tenant_id, to_type, to_id);
CREATE INDEX edges_source_event_idx ON edges (tenant_id, source_event_id) WHERE source_event_id IS NOT NULL;
CREATE INDEX edges_asserted_by_idx ON edges (tenant_id, asserted_by);

-- Edges are never updated in place. A superseded claim is recorded as a
-- revocation and a new edge is written, so the graph stays a projection that can
-- be rebuilt deterministically. edges_active is the only view the application
-- traverses; a revoked edge is invisible to it, not deleted.
CREATE TABLE edge_revocations (
    tenant_id            TEXT NOT NULL,
    edge_id              TEXT NOT NULL,
    revoked_at           TIMESTAMPTZ NOT NULL,
    revoked_by           TEXT NOT NULL,
    reason               TEXT NOT NULL,
    replacement_edge_id  TEXT,
    PRIMARY KEY (tenant_id, edge_id)
);

CREATE VIEW edges_active AS
    SELECT e.*
      FROM edges e
      LEFT JOIN edge_revocations r
             ON r.tenant_id = e.tenant_id AND r.edge_id = e.edge_id
     WHERE r.edge_id IS NULL;

-- Evidence supporting an edge lives in 0006, alongside the evidence table it
-- references. It is one table with one purpose — justifying an edge — and the
-- only reason it is not here is that a foreign key cannot point forward.
