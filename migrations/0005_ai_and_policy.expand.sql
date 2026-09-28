-- 0005_ai_and_policy.expand.sql
--
-- The AI plane's landing zone and the rule/policy store.
--
-- AI is never on the availability and boot critical path. Nothing in this
-- migration is required for the core to boot, to migrate, or to serve an
-- incident — that is why ai_observations has no dependency the core must
-- resolve at start-up.

CREATE TABLE ai_observations (
    observation_id              TEXT NOT NULL,
    tenant_id                   TEXT NOT NULL,
    site_id                     TEXT NOT NULL,
    incident_id                 TEXT NOT NULL,
    event_id                    TEXT NOT NULL,
    profile_id                  TEXT NOT NULL,
    production_profile          BOOLEAN NOT NULL DEFAULT FALSE,

    -- Attribution is mandatory. An observation without it is not attributable,
    -- and an unattributable observation cannot be replayed or audited.
    model                       TEXT NOT NULL,
    model_hash                  TEXT NOT NULL,
    runtime_version             TEXT NOT NULL,
    prompt_template_id          TEXT NOT NULL,
    prompt_version              TEXT NOT NULL,
    model_bundle_signature      TEXT NOT NULL DEFAULT '',

    input_evidence_ids          TEXT NOT NULL DEFAULT '[]'::jsonb,
    input_evidence_hashes       TEXT NOT NULL DEFAULT '[]'::jsonb,

    output_schema_version       INTEGER NOT NULL,
    -- Validated against the profile's versioned JSON schema before persistence.
    -- Rules read these fields; they never read model prose.
    structured_output           JSONB NOT NULL,
    insufficient_evidence       BOOLEAN NOT NULL DEFAULT FALSE,
    degraded_reason             TEXT NOT NULL DEFAULT 'none' CHECK (degraded_reason IN
                                  ('none', 'model_unavailable', 'no_runtime', 'timeout', 'error', 'absent')),
    degraded_detail             TEXT NOT NULL DEFAULT '',

    latency_ms                  INTEGER NOT NULL DEFAULT 0,
    hardware                    TEXT NOT NULL DEFAULT '',
    output_hash                 TEXT NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Re-evaluation is an explicitly new observation. The historical row is
    -- retained forever and the delta between the two is measurable.
    supersedes_observation_id  TEXT,
    PRIMARY KEY (tenant_id, observation_id),
    FOREIGN KEY (tenant_id, incident_id) REFERENCES incidents (tenant_id, incident_id) ON DELETE CASCADE
);

CREATE INDEX ai_observations_incident_idx ON ai_observations (tenant_id, incident_id, created_at);
CREATE INDEX ai_observations_profile_idx ON ai_observations (tenant_id, profile_id, created_at);
CREATE INDEX ai_observations_degraded_idx ON ai_observations (tenant_id, degraded_reason, created_at)
    WHERE degraded_reason <> 'none';
CREATE INDEX ai_observations_supersedes_idx ON ai_observations (tenant_id, supersedes_observation_id)
    WHERE supersedes_observation_id IS NOT NULL;
CREATE INDEX ai_observations_production_idx ON ai_observations (tenant_id, created_at)
    WHERE production_profile;

-- Append-only. An AI observation is a historical fact about what the model said
-- about evidence with a given hash, at a given time. Rewriting it would make
-- every decision that cited it a lie.
CREATE OR REPLACE FUNCTION forbid_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; a new interpretation is a new row', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ai_observations_append_only
    BEFORE UPDATE OR DELETE ON ai_observations
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- The operator's answer. It never overwrites the AI observation: "AI said X" and
-- "operator said Y" are both stored and both replay.
CREATE TABLE dispositions (
    disposition_id         TEXT NOT NULL,
    tenant_id              TEXT NOT NULL,
    incident_id            TEXT NOT NULL,
    actor_id               TEXT NOT NULL,
    actor_roles            TEXT NOT NULL DEFAULT '[]'::jsonb,
    site_scope             TEXT NOT NULL DEFAULT '[]'::jsonb,
    outcome                TEXT NOT NULL CHECK (outcome IN
                            ('true_positive', 'false_positive', 'benign', 'escalated', 'acknowledged', 'dismissed')),
    reason_code            TEXT NOT NULL DEFAULT '',
    notes                  TEXT NOT NULL DEFAULT '',
    agreement              TEXT NOT NULL DEFAULT 'unsure' CHECK (agreement IN
                            ('no_ai_observation', 'agreed', 'disagreed', 'unsure')),
    related_observation_id TEXT,
    session_id             TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, disposition_id),
    FOREIGN KEY (tenant_id, incident_id) REFERENCES incidents (tenant_id, incident_id) ON DELETE CASCADE
);

CREATE INDEX dispositions_incident_idx ON dispositions (tenant_id, incident_id, created_at);
CREATE INDEX dispositions_actor_idx ON dispositions (tenant_id, actor_id, created_at);

CREATE TRIGGER dispositions_append_only
    BEFORE UPDATE OR DELETE ON dispositions
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE policies (
    policy_id      TEXT NOT NULL,
    tenant_id      TEXT NOT NULL,
    version        TEXT NOT NULL,
    content_hash   TEXT NOT NULL,
    content        JSONB NOT NULL,
    active         BOOLEAN NOT NULL DEFAULT FALSE,
    activated_at   TIMESTAMPTZ,
    activated_by   TEXT,
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, policy_id, version)
);

CREATE UNIQUE INDEX policies_single_active_idx ON policies (tenant_id, policy_id) WHERE active;

-- Immutable, versioned rule bundles. Git is the development source of truth only;
-- never an operational requirement. A bundle is stored immutably and activated
-- by pointer, so rollback restores a previous version verbatim.
CREATE TABLE rule_bundles (
    bundle_id         TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    version           TEXT NOT NULL,
    content_hash      TEXT NOT NULL,
    content           BYTEA NOT NULL,
    source_reference  TEXT NOT NULL,
    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    active            BOOLEAN NOT NULL DEFAULT FALSE,
    supersedes_version TEXT,
    min_core_version  TEXT NOT NULL DEFAULT '',
    max_core_version  TEXT NOT NULL DEFAULT '',
    lint_status       TEXT NOT NULL DEFAULT 'not_run' CHECK (lint_status IN ('not_run', 'passed', 'failed')),
    -- The activate endpoint refuses a bundle whose simulate diff was not
    -- acknowledged by this exact digest.
    activate_diff_digest TEXT,
    PRIMARY KEY (tenant_id, bundle_id)
);

CREATE UNIQUE INDEX rule_bundles_version_idx ON rule_bundles (tenant_id, version);
CREATE UNIQUE INDEX rule_bundles_content_idx ON rule_bundles (tenant_id, content_hash);
CREATE UNIQUE INDEX rule_bundles_single_active_idx ON rule_bundles (tenant_id) WHERE active;

CREATE TRIGGER rule_bundles_content_immutable
    BEFORE UPDATE ON rule_bundles
    FOR EACH ROW
    WHEN (OLD.content IS DISTINCT FROM NEW.content
       OR OLD.content_hash IS DISTINCT FROM NEW.content_hash
       OR OLD.version IS DISTINCT FROM NEW.version)
    EXECUTE FUNCTION forbid_mutation();
