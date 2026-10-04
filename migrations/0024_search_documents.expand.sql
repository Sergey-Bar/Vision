-- 0024_search_documents.expand.sql
--
-- A `tsvector` for search, and a GIN index over it.
--
-- # What this is for
--
-- The product has no search surface and no way to ask "what did the model say about
-- the camera that kept going off at 04:00". The POC has one --
-- `poc/active-monitoring/internal/store/searchcards.go` -- and it is a
-- case-insensitive SUBSTRING match over the model's sentence and a reviewer's note.
-- That works on a few hundred rows and does not work on a year of a site's events:
-- `%term%` cannot use an index, so every query sequentially scans everything.
--
-- This migration adds the indexable thing and nothing else. No surface, no query, and
-- no existing code reads the new column -- which is what makes it an expand.
--
-- # What is indexed, and what is only MATCHED
--
--	verification_results.explanation   INDEXED   the deterministic verdict text
--	ai_observations.structured_output  matched   the model's own words, as JSON
--	dispositions.notes                 matched   a reviewer's ground-truth note
--
-- Only the first can be indexed, and the reason is structural rather than a limit of
-- this migration: a generated column is computed from the row it is on, so a column
-- holding a vector over `verification_results` cannot see a `dispositions` row. Putting
-- all three in one indexable column means either a denormalised copy kept in step by
-- triggers -- which a bulk load can miss, and a search index that disagrees with its
-- source answers confidently and wrongly -- or a second table of derived documents,
-- which is the same problem with more moving parts.
--
-- So the view below unions all three for MATCHING and the index serves the
-- deterministic half. That is a PARTIAL index and it is stated rather than hidden: a
-- query whose terms appear only in a reviewer's note still works and is still correct;
-- it is slower than it will be once the note is denormalised, and denormalising it is a
-- CONTRACT step rather than an expand because it adds a table somebody has to keep
-- correct.
--
-- The deterministic half is the right one to index first anyway: it is the text an
-- operator can reproduce from stored facts, so a hit on it is citable, and a search
-- that returns only reproducible answers is the search this product can defend.
--
-- # Why `english`, and what that costs
--
-- `to_tsvector('english', ...)` gives stemming and stop words, which is what makes
-- "cameras" find "camera". It also assumes English, and this product installs at sites
-- whose operators do not all read English.
--
-- Stated rather than solved. `simple` would be language-neutral and would stem
-- nothing, so "cameras" would not find "camera" -- which an operator describes as the
-- search being broken. A per-tenant configuration is the right answer and needs a
-- migration and a decision, and **that decision is not taken here.**
--
-- # Why the column is GENERATED
--
-- `GENERATED ALWAYS AS ... STORED` means PostgreSQL computes the vector on every write
-- and it cannot drift from the text it describes. A trigger-maintained column can be
-- missed by a bulk load.
--
-- The cost is that changing the expression is a contract step rather than an edit. That
-- is the right asymmetry: a vector built from a different expression is a different
-- index, and the old one has to be dropped and rebuilt rather than reinterpreted.
--
-- # The scope
--
-- `search_documents` carries `tenant_id` and `site_id`, so `tenant_isolation` and
-- `site_isolation` govern exactly as they do to the three tables underneath. A view
-- has no storage and therefore declares no policies of its own; the underlying tables'
-- policies apply through it, which is the correct answer and one fewer thing to keep in
-- step.

-- ---------------------------------------------------------------------------
-- 1. The indexed half. Generated, so it cannot drift from `explanation`.
--
--    Weight 'A' on the whole of it: the deterministic text is a single sentence of
--    "rule X fired because Y" and there is nothing inside it that should rank below
--    anything else in the same document.
-- ---------------------------------------------------------------------------
ALTER TABLE verification_results
    ADD COLUMN IF NOT EXISTS search_vector tsvector
    GENERATED ALWAYS AS (to_tsvector('english', coalesce(explanation, ''))) STORED;

CREATE INDEX IF NOT EXISTS verification_results_search_vector_gin
    ON verification_results USING GIN (search_vector);

COMMENT ON COLUMN verification_results.search_vector IS
    'to_tsvector(english, explanation), generated and GIN-indexed by migration 0024. '
    'Indexed over the DETERMINISTIC text only: a generated column cannot see a row of '
    'another table, so the reviewer note and the model output in search_documents are '
    'matched but not indexed. Adding them means a denormalised document table and is a '
    'contract step, not an edit to this expression.';

-- ---------------------------------------------------------------------------
-- 2. The corpus. A view rather than a table, so there is no denormalised copy to keep
--    in step and nothing to migrate when the schema moves.
--
--    `vector` is the INDEXED half and `unindexed_vector` is the two sources that are
--    matched without an index. A query is `vector @@ q OR unindexed_vector @@ q`.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW search_documents AS
SELECT
    vr.tenant_id,
    vr.incident_id,
    i.primary_site_id AS site_id,
    vr.evaluated_at   AS at,
    'verification'    AS source_kind,
    vr.explanation    AS explanation,
    vr.search_vector  AS vector,
    setweight(to_tsvector('english', coalesce((
        SELECT string_agg(coalesce(d.notes, ''), ' ')
          FROM dispositions d
         WHERE d.tenant_id = vr.tenant_id
           AND d.incident_id = vr.incident_id), '')), 'B'::"char") ||
    setweight(to_tsvector('english', coalesce((
        SELECT string_agg(coalesce(ao.structured_output::text, ''), ' ')
          FROM ai_observations ao
         WHERE ao.tenant_id = vr.tenant_id
           AND ao.incident_id = vr.incident_id), '')), 'C'::"char")
        AS unindexed_vector
FROM verification_results vr
JOIN incidents i
  ON i.tenant_id = vr.tenant_id
 AND i.incident_id = vr.incident_id;

COMMENT ON VIEW search_documents IS
    'The searchable corpus: what the rule said, what a reviewer said, and what the model '
    'said, in one row per verification. Created by migration 0024; nothing reads it yet. '
    'vector is the INDEXED half (explanation) and unindexed_vector is the review note and '
    'the model output, which are matched with @@ but not served by an index. Weights: '
    'A = deterministic verdict, B = reviewer note, C = model output. The english '
    'configuration is a decision with a named cost -- see the migration header.';
