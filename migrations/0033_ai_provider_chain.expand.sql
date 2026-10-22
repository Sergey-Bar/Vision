-- 0033_ai_provider_chain.expand.sql
--
-- The provider chain: which model answers, in what order, and what left the
-- site when it did.
--
-- # What this migration is for
--
-- The AI VMS plan's second AI family. The product's brain is a CHAIN, not a
-- selection: a site names the providers it may use and the order they are
-- tried in, and the chain walks them until one answers.
--
-- A chain rather than a setting, for three reasons:
--
--   - A site that names ONE provider has a single point of failure, and the
--     failure is visible only as "no observations today".
--   - The local-first rule wants a LOCAL model first and a hosted one as the
--     fallback, and that is an ORDER, not a choice.
--   - The order is a POLICY. It says what may leave the site, and it is the
--     thing an auditor asks about.
--
-- # `ai_policies.providers` is JSON, and why
--
-- The chain is an ORDERED LIST of adapters, and each adapter carries its own
-- settings. A row-per-adapter table would need an ordinal column, and an
-- ordinal column is a number somebody renumbers by hand. The list is short,
-- it is read whole every time, and it is written whole every time, so a
-- JSONB array is the right shape for it.
--
-- # The egress log, and what it does NOT carry
--
-- `ai_egress_log` records what LEFT the site: which provider, which
-- observation, how many pieces of evidence and how many bytes -- and the
-- DIGESTS of that evidence.
--
-- It never carries the content. That is the whole point of the table: a log
-- of what left the site is a log a customer can read and an auditor can
-- check, and a log that carried the content would be a SECOND COPY of the
-- evidence somewhere nobody chose to put it.
--
-- The digests are enough to answer the question the log exists for: "was
-- this exact piece of evidence what was sent?" -- without keeping the thing
-- that was sent.

CREATE TABLE ai_policies (
    tenant_id   TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    -- The ordered chain. Each entry names an adapter KIND and its settings.
    --
    -- The kinds are the closed set the Go side implements: `openai_compatible`,
    -- `anthropic` and `local_bundle`. A kind the build does not know is a
    -- chain that cannot run, and it is refused at startup rather than skipped
    -- at call time.
    providers   JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The system prompt every provider in the chain is given. It is the site's
    -- own text and it is configuration, not a secret.
    system_prompt TEXT NOT NULL DEFAULT '',
    -- Whether this site may send anything off the machine at all.
    --
    -- It is FALSE by default and the refusal is enforced in Go rather than
    -- only here, because a row that says "no egress" and a code path that
    -- ignores it is a promise with no enforcement behind it.
    egress_allowed BOOLEAN NOT NULL DEFAULT FALSE,
    -- The deadline one provider call gets, in seconds.
    timeout_seconds INTEGER NOT NULL DEFAULT 30
                   CHECK (timeout_seconds BETWEEN 1 AND 600),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, site_id)
);

COMMENT ON TABLE ai_policies IS
    'The per-site provider CHAIN: which model answers, in what order, and whether the site '
    'may send anything off the machine. The order is the policy -- it says what may leave the '
    'site -- and it is the thing an auditor asks about. Created by migration 0033 for the AI '
    'VMS provider chain.';

COMMENT ON COLUMN ai_policies.egress_allowed IS
    'Whether this site may send evidence to a provider that is not on this machine. FALSE by '
    'default, and the refusal is enforced in Go as well as here: a row that says "no egress" '
    'and a code path that ignores it is a promise with no enforcement behind it.';

CREATE TABLE ai_egress_log (
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    -- The row's own id, so a log line can be named without naming what it
    -- carried.
    egress_id    TEXT NOT NULL,
    -- The provider that answered, and the observation the answer was for.
    provider     TEXT NOT NULL,
    observation_id TEXT NOT NULL,
    profile_id   TEXT NOT NULL,
    -- How much left: how many pieces of evidence, and how many bytes.
    evidence_count INTEGER NOT NULL CHECK (evidence_count >= 0),
    evidence_bytes BIGINT NOT NULL CHECK (evidence_bytes >= 0),
    -- The digests of what left -- and NEVER the content.
    --
    -- The digests are enough to answer the question the log exists for: "was
    -- this exact piece of evidence what was sent?" -- without keeping the
    -- thing that was sent. A log that carried the content would be a second
    -- copy of the evidence somewhere nobody chose to put it.
    evidence_hashes TEXT[] NOT NULL DEFAULT '{}',
    -- When it left.
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, egress_id)
);

CREATE INDEX ai_egress_log_site_requested_idx
    ON ai_egress_log (tenant_id, site_id, requested_at DESC);

COMMENT ON TABLE ai_egress_log IS
    'What LEFT the site: which provider, which observation, how much, and the digests of the '
    'evidence that went. It never carries the content -- a log of what left that kept what left '
    'would be a second copy of the evidence in a place nobody chose to put it.';

-- ---------------------------------------------------------------------------
-- The policies, the force, and the grants
-- ---------------------------------------------------------------------------

ALTER TABLE ai_policies ENABLE ROW LEVEL SECURITY;
-- FORCE, not just ENABLE: the table owner would otherwise bypass its own
-- policies, and an owner connection is exactly the connection a maintenance
-- script tends to use.
ALTER TABLE ai_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_policies
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON ai_policies
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

ALTER TABLE ai_egress_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_egress_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_egress_log
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON ai_egress_log
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

-- The core reads the policy and WRITES the egress log. It does not delete
-- either: the log is the record of what left the site, and a record that the
-- product can delete is not a record.
GRANT SELECT, INSERT, UPDATE ON ai_policies TO security_core;
GRANT SELECT, INSERT ON ai_egress_log TO security_core;
