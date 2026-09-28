-- 0008_identity_and_rls.expand.sql
--
-- Identity, sessions, and tenant isolation.
--
-- Defense in depth: the service layer enforces authorization, and RLS enforces
-- it again underneath. A bug in the service layer must not become a
-- cross-tenant read, and a bug in the service layer that forgets to set the
-- tenant context must fail closed rather than read everything.

CREATE TABLE roles (
    role_id      TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    -- Capability list, not a code path. Every privileged operation names a
    -- capability; adding one is a schema change, which is the point.
    capabilities TEXT NOT NULL DEFAULT '[]'::jsonb,
    description  TEXT NOT NULL DEFAULT ''
);

INSERT INTO roles (role_id, name, capabilities, description) VALUES
    ('tenant_admin',    'Tenant Admin',    '["incident.*","evidence.*","rules.*","audit.read","identity.*","site.configure"]', 'Manages a tenant: rules, identity, site configuration.'),
    ('security_manager','Security Manager','["incident.*","evidence.*","rules.read","audit.read","disposition.*"]', 'Runs the security desk. Dispositions and escalations.'),
    ('operator',        'Operator',        '["incident.read","incident.disposition","evidence.read"]', 'Works the incident queue.'),
    ('viewer',          'Viewer',          '["incident.read","evidence.read"]', 'Read-only observer.'),
    ('service',         'Service',         '["service.*"]', 'Non-human principal for adapters and the AI runtime.');

CREATE TABLE users (
    user_id            TEXT NOT NULL,
    tenant_id          TEXT NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    username           TEXT NOT NULL,
    display_name       TEXT NOT NULL DEFAULT '',
    email              TEXT,
    -- Argon2id. The hash and its parameters live here; the verifier is a
    -- product-managed key file, never a config file and never this table.
    password_hash      TEXT,
    password_algo      TEXT,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    -- Audited recovery material. Set only by `securityctl bootstrap-admin` or an
    -- audited recovery; never self-service.
    recovery_required  BOOLEAN NOT NULL DEFAULT FALSE,
    failed_attempts    INTEGER NOT NULL DEFAULT 0,
    locked_until       TIMESTAMPTZ,
    break_glass        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at      TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, user_id),
    UNIQUE (tenant_id, username),
    -- A break-glass account still needs a password; an account with no
    -- password is an account that can only be recovered, not used.
    CONSTRAINT users_break_glass_has_password CHECK (NOT break_glass OR password_hash IS NOT NULL)
);

CREATE INDEX users_tenant_idx ON users (tenant_id, enabled);

-- MFA is mandatory. There is no code path that authenticates a session without
-- a verified factor, which is why this table has no "optional" flag.
CREATE TABLE mfa_credentials (
    credential_id  TEXT NOT NULL,
    tenant_id      TEXT NOT NULL,
    user_id        TEXT NOT NULL,
    method         TEXT NOT NULL CHECK (method IN ('webauthn', 'totp')),
    label          TEXT NOT NULL DEFAULT '',
    -- WebAuthn: the public key and counter. TOTP: an Argon2id-wrapped shared
    -- secret. Both are non-exportable in practice and neither is a password.
    public_key     BYTEA,
    wrapped_secret BYTEA,
    sign_count     BIGINT NOT NULL DEFAULT 0,
    verified_at    TIMESTAMPTZ,
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, credential_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT mfa_credentials_method_payload CHECK (
        (method = 'webauthn' AND public_key IS NOT NULL AND wrapped_secret IS NULL)
     OR (method = 'totp'   AND wrapped_secret IS NOT NULL AND public_key IS NULL)
    )
);

CREATE INDEX mfa_credentials_user_idx ON mfa_credentials (tenant_id, user_id) WHERE revoked_at IS NULL;

CREATE TABLE user_roles (
    tenant_id  TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    role_id    TEXT NOT NULL REFERENCES roles (role_id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, user_id, role_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE
);

-- Site scoping. A principal with no row here has no site access at all, which
-- is the safe default: a misconfigured assignment denies rather than opens.
CREATE TABLE user_site_assignments (
    tenant_id   TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, user_id, site_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id) ON DELETE CASCADE
);

CREATE INDEX user_site_assignments_site_idx ON user_site_assignments (tenant_id, site_id);

CREATE TABLE sessions (
    session_id      TEXT NOT NULL,
    tenant_id       TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    -- Hash of the opaque session token, never the token.
    token_hash      TEXT NOT NULL UNIQUE,
    csrf_hash       TEXT NOT NULL,
    roles           TEXT NOT NULL DEFAULT '[]'::jsonb,
    site_scope      TEXT NOT NULL DEFAULT '[]'::jsonb,
    mfa_verified    BOOLEAN NOT NULL,
    break_glass     BOOLEAN NOT NULL DEFAULT FALSE,
    ip              TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    absolute_expiry TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    PRIMARY KEY (tenant_id, session_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE
);

CREATE INDEX sessions_token_idx ON sessions (token_hash);
CREATE INDEX sessions_user_idx ON sessions (tenant_id, user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expiry_idx ON sessions (absolute_expiry) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Row-level security
-- ---------------------------------------------------------------------------

-- The application sets these per transaction, from the authenticated session.
-- They are read with missing_ok so that a request that forgot to set them sees
-- an empty tenant and therefore no rows, rather than a full table.
CREATE OR REPLACE FUNCTION app_current_tenant() RETURNS TEXT
    LANGUAGE sql STABLE AS $$
    SELECT nullif(current_setting('app.tenant_id', true), '');
$$;

CREATE OR REPLACE FUNCTION app_current_site_ids() RETURNS TEXT[]
    LANGUAGE sql STABLE AS $$
    SELECT coalesce(
        nullif(current_setting('app.site_ids', true), '')::text[],
        ARRAY[]::text[]
    );
$$;

-- Background workers — the inbox normalizer, the job runner, the checkpoint
-- sealer — legitimately operate on rows across tenants, taking the tenant from
-- the row they are already processing. They say so explicitly, and only they do:
-- `app.system_worker` is set by dbx.InSystemTenant and is not reachable from a
-- console request, whose tenant context comes from the session.
--
-- The blast radius of this predicate is bounded by grants, not by this function:
-- the workers' service accounts hold no grants on identity or the rule store, so
-- "sees all tenants" never becomes "can change any tenant".
CREATE OR REPLACE FUNCTION app_is_system_worker() RETURNS BOOLEAN
    LANGUAGE sql STABLE AS $$
    SELECT coalesce(current_setting('app.system_worker', true), '') = 'true';
$$;

-- site_id may legitimately be NULL on a record that is not physically bound to
-- a place. Such a record is tenant-scoped only; a site-scoped principal still
-- sees it, because the site assignment is a restriction on sites, not a filter
-- that would hide tenant-wide configuration from an operator.
CREATE OR REPLACE FUNCTION app_site_visible(p_site_id TEXT) RETURNS BOOLEAN
    LANGUAGE sql STABLE AS $$
    SELECT p_site_id IS NULL OR p_site_id = ANY (app_current_site_ids());
$$;

DO $$
DECLARE
    t RECORD;
BEGIN
    -- Every table with a tenant_id gets a policy, in one place, so a new table
    -- cannot be added without isolation by review rather than by memory.
    FOR t IN
        SELECT c.relname
          FROM pg_class c
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND c.relkind IN ('r', 'p')
           AND c.relname <> 'schema_migrations'
           AND EXISTS (
                SELECT 1 FROM pg_attribute a
                 WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND a.attnum > 0
           )
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t.relname);
        -- FORCE, not just ENABLE: the table owner would otherwise bypass its own
        -- policies, and an owner connection is exactly the connection a
        -- maintenance script tends to use.
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t.relname);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = app_current_tenant() OR app_is_system_worker()) '
            'WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker())',
            t.relname);
    END LOOP;
END;
$$;

-- Site-scoped narrowing for the tables an operator reads through a site feed.
-- Kept as additional policies rather than a rewrite of the tenant policy, so
-- the two concerns compose: tenant first, then site.
DO $$
DECLARE
    t RECORD;
BEGIN
    FOR t IN
        SELECT c.relname
          FROM pg_class c
          JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public'
           AND c.relkind IN ('r', 'p')
           AND c.relname IN (
                 'incidents', 'devices', 'cameras', 'evidence', 'canonical_events',
                 'inbox_events', 'source_events', 'ai_observations', 'verification_results'
               )
    LOOP
        IF EXISTS (
            SELECT 1 FROM pg_attribute a
             WHERE a.attrelid = format('%I.%I', 'public', t.relname)::regclass
               AND a.attname IN ('site_id', 'primary_site_id')
        ) THEN
            EXECUTE format(
                'CREATE POLICY site_isolation ON %I USING (%s OR app_is_system_worker()) '
                'WITH CHECK (%s OR app_is_system_worker())',
                t.relname,
                CASE WHEN EXISTS (SELECT 1 FROM pg_attribute a
                                   WHERE a.attrelid = format('%I.%I', 'public', t.relname)::regclass
                                     AND a.attname = 'site_id') THEN 'app_site_visible(site_id)' ELSE 'app_site_visible(primary_site_id)' END,
                CASE WHEN EXISTS (SELECT 1 FROM pg_attribute a
                                   WHERE a.attrelid = format('%I.%I', 'public', t.relname)::regclass
                                     AND a.attname = 'site_id') THEN 'app_site_visible(site_id)' ELSE 'app_site_visible(primary_site_id)' END);
        END IF;
    END LOOP;
END;
$$;

-- ---------------------------------------------------------------------------
-- Least-privilege service accounts
--
-- Created here without passwords; the installer binds credentials out of the
-- encrypted secret store at install time. A distinct account per adapter and
-- per AI runtime scope, each with minimum grants. An adapter cannot read the
-- audit ledger or another tenant; the AI runtime cannot write to anything that
-- is authoritative state.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    r TEXT;
BEGIN
    FOREACH r IN ARRAY ARRAY[
        'security_migrate', 'security_core', 'security_adapter', 'security_ai', 'security_readonly'
    ] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
            EXECUTE format('CREATE ROLE %I NOLOGIN', r);
        END IF;
    END LOOP;

    -- Only the migrator bypasses RLS. security_core in particular does not: the
    -- whole point of the RLS layer is that it constrains the platform's own
    -- connections, including the ones serving a console request. A core that
    -- could set an arbitrary tenant context and read another tenant would make
    -- every service-layer authorization check decorative.
    EXECUTE 'ALTER ROLE security_migrate BYPASSRLS';

    EXECUTE 'GRANT security_migrate TO CURRENT_USER';
END;
$$;

-- security_core: the platform itself. Full read/write on platform state, read
-- on identity, and append-only on the audit ledger via a dedicated function.
GRANT USAGE ON SCHEMA public TO security_core;
GRANT SELECT, INSERT, UPDATE, DELETE
    ON tenants, sites, zones, devices, cameras, integrations, source_events,
       canonical_event_keys, device_clocks, entities, edges, edge_revocations,
       edge_evidence, incidents, incident_events, incident_evidence,
       verification_results, policies, rule_bundles, evidence, inbox_event_keys,
       users, user_roles, user_site_assignments, sessions, mfa_credentials, roles
    TO security_core;
GRANT SELECT, INSERT, UPDATE
    ON inbox_events, outbox_events, jobs, canonical_events, dispositions
    TO security_core;
GRANT SELECT, INSERT ON ai_observations TO security_core;
GRANT SELECT, INSERT ON audit_ledger TO security_core;
GRANT SELECT ON audit_checkpoints TO security_core;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO security_core;

-- An adapter writes to the inbox and reads only its own integration. It cannot
-- read the audit ledger, canonical events, incidents, identity, or any other
-- tenant. Least privilege is the control; the RLS policies underneath are the
-- backstop.
GRANT USAGE ON SCHEMA public TO security_adapter;
GRANT SELECT ON integrations, devices, cameras TO security_adapter;
GRANT SELECT, INSERT ON inbox_event_keys, source_events TO security_adapter;
GRANT SELECT, INSERT ON inbox_events TO security_adapter;

-- The AI runtime persists observations and nothing else. It has no grant on
-- incidents' state, dispositions, the rule store, identity, or the audit
-- ledger, so a compromised model runtime cannot alter authoritative state.
GRANT USAGE ON SCHEMA public TO security_ai;
GRANT SELECT, INSERT ON ai_observations TO security_ai;
GRANT SELECT ON evidence TO security_ai;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO security_ai;

GRANT USAGE ON SCHEMA public TO security_readonly;
GRANT SELECT ON incidents, incident_events, incident_evidence, verification_results,
              evidence, devices, cameras, zones, sites, canonical_events, ai_observations
    TO security_readonly;
