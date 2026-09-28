-- 0004_cases.expand.sql
--
-- Incidents, their supporting events and evidence, and the deterministic
-- verification result that explains why an incident exists.

CREATE TABLE incidents (
    incident_id          TEXT NOT NULL,
    tenant_id            TEXT NOT NULL,
    primary_site_id      TEXT NOT NULL,
    related_site_ids     TEXT NOT NULL DEFAULT '[]'::jsonb,
    rule_id              TEXT NOT NULL,
    rule_bundle_version  TEXT NOT NULL,
    bundle_content_hash  TEXT NOT NULL,
    policy_version       TEXT NOT NULL,
    state                TEXT NOT NULL DEFAULT 'open'
                         CHECK (state IN ('open', 'in_review', 'dispositioned', 'closed')),
    severity             TEXT NOT NULL DEFAULT 'medium'
                         CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    title                TEXT NOT NULL DEFAULT '',
    first_event_at       TIMESTAMPTZ NOT NULL,
    last_event_at        TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at            TIMESTAMPTZ,
    -- True when any contributing signal was degraded. A pilot must be able to
    -- measure how often the AI path was actually live, not assume it was.
    degraded             BOOLEAN NOT NULL DEFAULT FALSE,
    degraded_reasons     TEXT NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (tenant_id, incident_id),
    FOREIGN KEY (tenant_id, primary_site_id) REFERENCES sites (tenant_id, site_id) ON DELETE RESTRICT
);

CREATE INDEX incidents_feed_idx ON incidents (tenant_id, primary_site_id, state, created_at DESC);
CREATE INDEX incidents_time_idx ON incidents (tenant_id, first_event_at);
CREATE INDEX incidents_degraded_idx ON incidents (tenant_id, primary_site_id, created_at DESC)
    WHERE degraded;
CREATE INDEX incidents_rule_idx ON incidents (tenant_id, rule_id, rule_bundle_version);

CREATE TABLE incident_events (
    tenant_id    TEXT NOT NULL,
    incident_id  TEXT NOT NULL,
    event_id     TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT 'supporting'
                 CHECK (role IN ('trigger', 'supporting', 'contradicting')),
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, incident_id, event_id, role)
);

CREATE INDEX incident_events_event_idx ON incident_events (tenant_id, event_id);

CREATE TABLE incident_evidence (
    tenant_id    TEXT NOT NULL,
    incident_id  TEXT NOT NULL,
    evidence_id  TEXT NOT NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, incident_id, evidence_id)
);

CREATE INDEX incident_evidence_evidence_idx ON incident_evidence (tenant_id, evidence_id);

-- One row per deterministic evaluation. A months-old replay reads this row and
-- the stored observation; it never re-runs the model to explain a past outcome.
CREATE TABLE verification_results (
    verification_id           TEXT NOT NULL,
    tenant_id                 TEXT NOT NULL,
    incident_id               TEXT NOT NULL,
    event_id                  TEXT,
    rule_id                   TEXT NOT NULL,
    rule_bundle_version       TEXT NOT NULL,
    bundle_content_hash       TEXT NOT NULL,
    policy_version            TEXT NOT NULL,
    state                     TEXT NOT NULL CHECK (state IN
                               ('corroborated', 'unverified', 'contradicted', 'duplicate', 'suppressed')),
    min_required_tier         TEXT NOT NULL DEFAULT 'B' CHECK (min_required_tier IN ('A', 'B', 'C')),
    supporting_event_ids      TEXT NOT NULL DEFAULT '[]'::jsonb,
    supporting_observation_ids TEXT NOT NULL DEFAULT '[]'::jsonb,
    contradicting_event_ids   TEXT NOT NULL DEFAULT '[]'::jsonb,
    -- Devices excluded from time-windowed corroboration because of clock drift.
    -- Recorded here rather than applied silently.
    drift_exclusions          JSONB NOT NULL DEFAULT '[]'::jsonb,
    reason_code               TEXT NOT NULL DEFAULT '',
    explanation               TEXT NOT NULL DEFAULT '',
    ai_degradation            TEXT NOT NULL DEFAULT 'none' CHECK (ai_degradation IN
                               ('none', 'model_unavailable', 'no_runtime', 'timeout', 'error', 'absent')),
    deterministic             BOOLEAN NOT NULL DEFAULT TRUE,
    evaluated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, verification_id)
);

CREATE INDEX verification_incident_idx ON verification_results (tenant_id, incident_id, evaluated_at);
CREATE INDEX verification_state_idx ON verification_results (tenant_id, state, evaluated_at);
CREATE INDEX verification_event_idx ON verification_results (tenant_id, event_id) WHERE event_id IS NOT NULL;
