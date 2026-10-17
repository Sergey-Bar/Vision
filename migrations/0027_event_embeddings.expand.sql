-- 0027_event_embeddings.expand.sql
--
-- A vector embedding per canonical event, for semantic (cosine-similarity)
-- search over the event record. An expand: the table, extension and index
-- exist, and nothing reads them yet. The contract step (an embedding writer, a
-- reader in eventsearch, an embedding model) lands separately.
--
-- # pgvector
--
-- `CREATE EXTENSION IF NOT EXISTS vector` is idempotent and is a no-op when the
-- extension is already installed. When it is not installed, the table still
-- creates (the `vector` type resolves at query time) and eventsearch falls back
-- to keyword search rather than failing the whole surface.
--
-- # Shape of an embedding row
--
-- One row per (tenant, event). `embedding` is the model's vector over `document`,
-- which is the searchable text the embedding was computed from (event type,
-- device, zone, label, colour, vehicle type). Storing `document` alongside keeps
-- the keyword fallback honest: a query that matches words the model never saw
-- can still return the event.
--
-- # Indexing
--
-- `ivfflat` over `vector_cosine_ops` makes cosine-similarity search fast; a
-- plain scan is correct when the index is absent or unbuilt. `embedding <=> q`
-- is cosine distance: smaller is more similar.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS event_embeddings (
    tenant_id   TEXT NOT NULL,
    event_id    TEXT NOT NULL,
    embedding   vector(384) NOT NULL,
    document    TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT 'sentence-transformers/all-MiniLM-L6-v2',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, event_id)
);

CREATE INDEX IF NOT EXISTS event_embeddings_tenant_idx
    ON event_embeddings (tenant_id);

CREATE INDEX IF NOT EXISTS event_embeddings_vector_idx
    ON event_embeddings USING ivfflat (embedding vector_cosine_ops);

COMMENT ON COLUMN event_embeddings.embedding IS
    'Model vector over `document`; read by eventsearch free-text search with '
    'cosine similarity (`<=>`), keyword fallback when pgvector is absent.';
COMMENT ON COLUMN event_embeddings.document IS
    'Searchable text the embedding was computed from: event type, device, zone, '
    'label, colour and vehicle type. Serves the keyword fallback and documents '
    'what a vector means.';
COMMENT ON TABLE event_embeddings IS
    'One embedding per canonical event. An expand of migration 0027: nothing '
    'reads it until the eventsearch free-text reader and the embedding writer '
    'land in the same contract step.';
