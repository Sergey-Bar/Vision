-- 0006_evidence.expand.sql
--
-- Evidence metadata. The bytes live in a local filesystem evidence directory
-- behind the EvidenceStore interface; Postgres holds addressing, hash and
-- retention law.
--
-- storage_ref is a storage-relative path and nothing else. No row in this table
-- may hold an absolute path, a drive letter, a `..` segment or a backslash.
-- The constraint below is the database's half of that law; the filesystem store
-- enforces the other half on read, and a test asserts both.
--
-- A NUL byte is not listed because Postgres cannot store one: text columns
-- reject it at the type level, so the database half of that rule already holds.
-- The null-byte negative test still exists, in the evidence store, because the
-- bytes arriving from a source system do not pass through this column on their
-- way to the filesystem.

CREATE TABLE evidence (
    evidence_id        TEXT NOT NULL,
    tenant_id          TEXT NOT NULL,
    site_id            TEXT NOT NULL,
    incident_id        TEXT,
    camera_id          TEXT,
    device_id          TEXT,
    kind               TEXT NOT NULL CHECK (kind IN ('snapshot', 'clip', 'bookmark', 'export')),
    tier               TEXT NOT NULL DEFAULT 'A' CHECK (tier IN ('A', 'B', 'C')),
    observed_at        TIMESTAMPTZ NOT NULL,
    captured_at        TIMESTAMPTZ NOT NULL,
    stored_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    storage_ref        TEXT NOT NULL,
    media_type         TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes         BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256             TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    retention_policy   TEXT NOT NULL DEFAULT 'default',
    legal_hold         BOOLEAN NOT NULL DEFAULT FALSE,
    retain_until       TIMESTAMPTZ,
    fetched_by_actor   TEXT,
    PRIMARY KEY (tenant_id, evidence_id),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT,
    CONSTRAINT evidence_storage_ref_relative CHECK (
        storage_ref <> ''
        AND storage_ref NOT LIKE '/%'
        AND storage_ref !~ '(^|/)\.\.(/|$)'
        AND storage_ref !~ '^[A-Za-z]:'
        AND storage_ref !~ '\\'
    )
);

CREATE INDEX evidence_incident_idx ON evidence (tenant_id, incident_id, stored_at) WHERE incident_id IS NOT NULL;
CREATE INDEX evidence_camera_idx ON evidence (tenant_id, camera_id, captured_at) WHERE camera_id IS NOT NULL;
CREATE INDEX evidence_site_time_idx ON evidence (tenant_id, site_id, stored_at);
-- Retention sweep. legal_hold rows are never swept; the predicate says so
-- explicitly rather than leaving it to the sweeper's discretion.
CREATE INDEX evidence_retention_idx ON evidence (retain_until)
    WHERE legal_hold = FALSE AND retain_until IS NOT NULL;
-- Content-addressed lookup: the same clip fetched twice is the same bytes.
CREATE INDEX evidence_hash_idx ON evidence (tenant_id, sha256);

-- Evidence supporting a graph edge, kept separate so one edge can be justified
-- by several independent artifacts.
CREATE TABLE edge_evidence (
    tenant_id      TEXT NOT NULL,
    edge_id        TEXT NOT NULL,
    evidence_id    TEXT NOT NULL,
    -- '' rather than NULL: an expression in a primary key is not allowed, and
    -- the alternative — a surrogate key plus a second unique index — buys
    -- nothing here.
    observation_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, edge_id, evidence_id, observation_id),
    FOREIGN KEY (tenant_id, evidence_id) REFERENCES evidence (tenant_id, evidence_id) ON DELETE CASCADE
);

CREATE INDEX edge_evidence_tenant_idx ON edge_evidence (tenant_id, evidence_id);
CREATE INDEX edge_evidence_observation_idx ON edge_evidence (tenant_id, observation_id)
    WHERE observation_id <> '';
