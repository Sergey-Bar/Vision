-- 0025_search_plans.expand.sql
--
-- Every question an operator asked and every query it compiled to, written BEFORE
-- the query runs.
--
-- WHY THIS TABLE IS NOT AUDIT_LOG
--
-- The audit ledger records that a principal did a thing. This records WHAT THAT
-- THING WAS, in enough detail to run it again: the question as typed, the plan as
-- approved, the statement, and the bounds. The difference matters for the claim
-- this product makes about itself — that a decision is derived rather than stored.
-- A search that is replayed has to have somewhere to replay FROM, and if that
-- somewhere is a log written after the fact, a reviewer is trusting a row somebody
-- could have edited between the run and the audit.
--
-- WHY IT IS WRITTEN BEFORE, NOT AFTER
--
-- Because "after" cannot answer the question the table exists for. A plan written
-- after execution records what ran, which is a fact about the past and proves
-- nothing about what was approved. A plan written before execution IS the artefact
-- an operator approved, and the fact that the row's `planned_at` precedes the rows it
-- produced is checkable.
--
-- A plan that was REFUSED is stored too, with `refused_reason` and the statement it
-- WOULD have run. A gateway that only records what it allowed has invisible
-- refusals, and a search refused sixty times in a day is a finding about the
-- PRINCIPAL'S SCOPE that nobody would see.
--
-- This is an expand and not a contract step: it creates one table, adds no column to
-- an existing one, and creates two policies on the new table alone. A 24-era binary
-- reads this database with one more table it does not name, and the two new
-- policies constrain nothing it already did. No existing row shape changes, which
-- is the direction the compat window exists to permit.

CREATE TABLE search_plans (
    plan_id            TEXT        NOT NULL,
    tenant_id          TEXT        NOT NULL,
    site_id            TEXT        NOT NULL,
    actor_id           TEXT        NOT NULL,

    -- The question as the operator typed it AND as a model proposed it. Both are
    -- stored because they can differ, and the difference is the whole point of the
    -- approval step: the words a person wrote and the words a model turned into
    -- structure are not the same artefact.
    question_text      TEXT        NOT NULL,
    question_structured JSONB      NOT NULL,

    -- What was approved, and what would run. `plan_sql` is the STATEMENT with its
    -- placeholders; `plan_args` is the bound array. Neither is executable alone,
    -- which is deliberate: a row somebody could paste into a psql prompt is a row
    -- that has escaped the scope check the plan exists to enforce.
    plan               JSONB      NOT NULL,
    plan_sql           TEXT       NOT NULL,
    plan_args          JSONB      NOT NULL,

    -- The gate's own words, so a plan can be tied to the code that wrote it.
    prompt_sha256      TEXT       NOT NULL,
    host               TEXT       NOT NULL,

    -- NULL `refused_reason` means the plan was approved. A non-NULL one means the
    -- gateway DECLINED and no query ran. The CHECK keeps those two states from being
    -- confused, which is the confusion that would make a refused search look like a
    -- search that found nothing.
    refused_reason     TEXT,
    refusal_kind       TEXT        NOT NULL DEFAULT 'none'
                        CHECK (refusal_kind IN ('none', 'site_out_of_scope', 'no_sites',
                                                'no_tenant', 'unknown_source_kind',
                                                'inverted_window', 'bad_limit',
                                                'unpinned_host', 'prompt_digest_mismatch',
                                                'table_not_allowed',
                                                'tenant_predicate_dropped',
                                                'site_scope_widened', 'unparseable_question')),

    planned_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set when the plan is run. NULL on a refused plan.
    executed_at        TIMESTAMPTZ,
    row_count          INTEGER     CHECK (row_count IS NULL OR row_count >= 0),
    -- The sha256 of `plan_sql` and `plan_args` as stored, so a later reader can
    -- tell whether the row in front of them is the row that ran.
    plan_sha256        TEXT        NOT NULL,

    PRIMARY KEY (tenant_id, plan_id),
    CONSTRAINT search_plans_tenant_fk  FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id),
    -- COMPOSITE, because `sites`' primary key is (tenant_id, site_id) and not
    -- site_id alone. A single-column reference to a composite key fails with 42830
    -- "no unique constraint matching given keys", which names the constraint and not
    -- the mistake.
    CONSTRAINT search_plans_site_fk    FOREIGN KEY (tenant_id, site_id) REFERENCES sites (tenant_id, site_id)
);

-- The listing is "this site's plans, newest first", which is the shape
-- `securityctl search --plan` reads and the shape an audit reads.
CREATE INDEX search_plans_site_planned_at_idx
    ON search_plans (site_id, planned_at DESC);

-- The refusals, which are the interesting half and which nobody would find by
-- listing approved plans.
CREATE INDEX search_plans_refused_at_idx
    ON search_plans (tenant_id, refusal_kind, planned_at DESC)
    WHERE refused_reason IS NOT NULL;

COMMENT ON TABLE search_plans IS
    'Every question an operator asked and every query it compiled to, stored BEFORE '
    'the query ran. A refused plan is stored too, with its reason and the statement it '
    'would have run: a gateway that only records what it allowed has invisible '
    'refusals, and a search refused sixty times in a day is a finding about the '
    'principal''s scope.';
COMMENT ON COLUMN search_plans.plan_sha256 IS
    'The sha256 of plan_sql and plan_args as stored. Verified BEFORE a replay hands '
    'back the statement: a row edited after it was written replays as itself, and a '
    'plan that replays as itself is not evidence of anything.';
COMMENT ON COLUMN search_plans.plan_sql IS
    'The STATEMENT with its $n placeholders, not an executable statement.';

-- Tenant isolation, and the site policy.
--
-- Hand-written rather than through 0023's shared template because a table added
-- after 0023 is exactly the case where hand-writing is honest: the alternative is
-- regenerating an APPLIED migration, which may not be edited.
CREATE POLICY search_plans_tenant_isolation ON search_plans
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE POLICY search_plans_site_isolation ON search_plans
    USING (app_site_visible(site_id))
    WITH CHECK (app_site_visible(site_id));
