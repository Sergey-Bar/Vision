-- 0022: honest attribution for a hosted model.
--
-- Expands `ai_observations` so that a provider whose weights are not ours and
-- not addressable can be attributed without a fabricated hash.
--
-- **Expand only.** No contract step is bundled here. `AGENTS.md` forbids a
-- contract step shipping with the expand step that makes it safe, because until
-- the contract is applied a reader sees a NOT NULL drop the code has not been
-- taught to accept.
--
-- The Go layer is already verified against this shape
-- (`internal/observations.Validate`, red-checked by
-- `TestTheAttributionNamesTheModelWithoutInventingAHash`), so this migration
-- makes the database agree with code that already exists rather than teaching
-- the code to tolerate a lie.

ALTER TABLE ai_observations
    ADD COLUMN model_identity         TEXT NOT NULL DEFAULT '',
    ADD COLUMN model_contract_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN temperature            DOUBLE PRECISION;

COMMENT ON COLUMN ai_observations.model_identity IS
    'What the provider reported it used. A CLAIM, not a verification: the weights '
    'are not ours, are not addressable, and change on the provider''s schedule. '
    'Empty for a local runtime, which records a content signature instead.';

COMMENT ON COLUMN ai_observations.model_contract_version IS
    'This build''s content hash over the prompt template, the prompt version, the '
    'output schema version and the API model name. Reproducible from the repository '
    'and therefore auditable in a way model_identity is not.';

COMMENT ON COLUMN ai_observations.temperature IS
    'The sampling temperature requested. An answer at 0.9 is not the answer at 0.01, '
    'and a replay that does not record it cannot reproduce which one it is comparing '
    'against. NULL when the provider does not expose it.';

-- `model_hash` stops being unconditionally required.
--
-- The two ways to keep it required are both worse than leaving it empty:
-- leaving it empty and calling the column required teaches every reader of the
-- schema to ignore it, and writing a synthetic 64-character string defeats the
-- one thing the column exists for, which is proving which weights answered.
ALTER TABLE ai_observations
    ALTER COLUMN model_hash DROP NOT NULL;

-- The replacement is not "no attribution". It is attribution by a different and
-- weaker claim, and the difference has to be visible in the row rather than in a
-- reader's head.
--
-- `model_hash <> ''` covers the local runtime, including a row whose hash is NULL
-- only if the Go layer let it be -- which it does not, so this is the local case
-- and the hash-present case together. The second disjunct is the hosted case.
ALTER TABLE ai_observations
    ADD CONSTRAINT ai_observations_attribution_ck
    CHECK (
        COALESCE(model_hash, '') <> ''
        OR (COALESCE(model_identity, '') <> ''
            AND COALESCE(model_contract_version, '') <> '')
    );

COMMENT ON CONSTRAINT ai_observations_attribution_ck ON ai_observations IS
    'An observation must be attributable, and there are two honest ways to be. '
    'Either it names the weights that answered by hash -- the local runtime, whose '
    'bundle we sign ourselves -- or it names the provider that answered and the '
    'contract version this build pinned, which is a claim about a model rather than '
    'a proof of one. What it may not do is have neither: an observation with no '
    'attribution cannot be replayed or audited, which is the property the column '
    'was NOT NULL for in the first place.';

-- `model_hash = ''` was previously possible only by way of a synthetic hash; with
-- NOT NULL dropped, an empty string and NULL both mean "this provider has no hash
-- to give", and the constraint above distinguishes that from "no attribution at
-- all" without treating the two spellings as different.
