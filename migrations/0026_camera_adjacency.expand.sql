-- 0026_camera_adjacency.expand.sql
--
-- Camera adjacency, persisted. The plan's C3 item — "Camera adjacency — schema,
-- graph relation, backfill" — pulled forward from M6 on the recommendation
-- `ai-and-data.md` already records: *"Neighbouring cameras — Does not exist ...
-- Recommendation: pull adjacency forward. M2 cannot deliver its exit gate without
-- it."*
--
-- # Why not a graph edge
--
-- `edges` is tenant-scoped and has no `site_id` (`0003_events_and_graph.expand.sql`
-- :129-146). Adjacency is a fact about a site's geometry — which camera sees
-- which other camera's ground — and a fact about one site's geometry written
-- into a tenant-scoped relation is a cross-site disclosure waiting for a query
-- that forgets to filter. Invariant 5 draws the line exactly here: `site_id` is
-- NOT NULL on physically site-bound records, and a camera's neighbours are as
-- site-bound as the camera.
--
-- It is also not a *claim* about an event, which is what `edges` holds: nothing
-- observed adjacency, and the provenance that matters is the site model it was
-- derived from, not the event that asserted it.
--
-- # What the relation means
--
-- Two relations, and one row per unordered pair. The pair is canonicalised by
-- `camera_id < neighbour_id` so a mirror row cannot exist: adjacency is
-- symmetric, and a symmetric fact stored twice is two rows to revoke and a
-- question about which one is authoritative.
--
--   shares_zone             both cameras are configured on the same zone
--   overlaps_field_of_view  their fields of view overlap on the ground
--
-- The second is empty until a site declares its cameras' bearings and extents.
-- That is the honest state of it: an unmeasured field of view is not a zero
-- field of view, and a defaulted bearing would draw a false corridor between
-- two cameras that have never seen each other's ground.
--
-- # What recomputes it
--
-- `sitemodel.Service.ReplaceFromModel` derives both relations from the loaded
-- site model and rewrites the rows for a site. The derivation is pure over the
-- model, so the table is a projection of the YAML an operator already owns, and
-- `securityctl` can re-run it without a vendor call. `camera_id <> neighbour_id`
-- is the one thing the derivation cannot get wrong, stated here rather than
-- trusted there.

CREATE TABLE camera_adjacency (
    tenant_id    TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    camera_id    TEXT NOT NULL,
    neighbour_id TEXT NOT NULL,
    -- The closed relation set. A relation outside it is a relation the code
    -- cannot walk.
    relation     TEXT NOT NULL
                 CHECK (relation IN ('shares_zone', 'overlaps_field_of_view')),
    -- Which site model produced this row. Adjacency is recomputed when the
    -- model is reloaded, and a row whose model_version is not the loaded one
    -- is a stale projection rather than a fact.
    source       TEXT NOT NULL
                 CHECK (source IN ('site_model', 'operator')),
    model_version TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The unordered pair, so the relation is stored once.
    PRIMARY KEY (tenant_id, site_id, camera_id, neighbour_id, relation),
    CONSTRAINT camera_adjacency_no_self CHECK (camera_id <> neighbour_id),
    -- Canonical ordering makes the PK the uniqueness guarantee: without it the
    -- same pair could arrive as (A,B) and (B,A), and "neighbours of A" would
    -- answer about a pair that was never recorded.
    CONSTRAINT camera_adjacency_canonical_pair CHECK (camera_id < neighbour_id)
);

-- One index per direction the product asks by: whose neighbours a camera has,
-- and which pair a reconciliation compares against.
CREATE INDEX camera_adjacency_camera_idx ON camera_adjacency (tenant_id, site_id, camera_id);
CREATE INDEX camera_adjacency_neighbour_idx ON camera_adjacency (tenant_id, site_id, neighbour_id);

-- Site isolation, written the way migration 0023's loop writes it for every
-- site-bearing relation, and written here rather than inherited because 0023
-- has already run: its catalogue loop covers the tables that existed when it
-- ran, and a table created afterwards is covered by the next re-run of 0023's
-- body — not retroactively. `internal/rlsrole`'s canary asks the catalogue which
-- site-bearing relations LACK the policy, so a table created without one is a
-- failing test rather than a silent gap.
--
-- # The four things a table created after the catalogue loops needs
--
-- A table born after 0008 and 0023 gets nothing from either loop's next run:
-- both loops ask the catalogue about tables as it was. This migration wrote the
-- site policy first and the write canary then reported `camera_adjacency`
-- succeeding under RLS with no tenant context — because a CREATE POLICY without
-- `ENABLE`/`FORCE ROW LEVEL SECURITY` is a policy on a table with row security
-- off, and `TestEverySiteBearingRelationHasASitePolicy` passed the whole time
-- because it asks about the policy, not about whether it is in force. So the
-- policy, the force, AND the tenant policy all belong here, together:
ALTER TABLE camera_adjacency ENABLE ROW LEVEL SECURITY;
-- FORCE, not just ENABLE: the table owner would otherwise bypass its own
-- policies, and an owner connection is exactly the connection a maintenance
-- script tends to use.
ALTER TABLE camera_adjacency FORCE ROW LEVEL SECURITY;
-- Tenant first, site second: the two compose, and the site policy is the
-- RESTRICTIVE one — a permissive site policy ORed with the tenant one and a
-- cross-tenant write with a borrowed site id would land.
CREATE POLICY tenant_isolation ON camera_adjacency
    AS PERMISSIVE FOR ALL TO PUBLIC
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());
CREATE POLICY site_isolation ON camera_adjacency
    AS RESTRICTIVE FOR ALL TO PUBLIC
    USING (app_site_visible(site_id) OR app_is_system_worker())
    WITH CHECK (app_site_visible(site_id) OR app_is_system_worker());

-- The role's privileges are the OTHER half a table created after 0008 lacks, and
-- the canary's third case found it: with the policy present and no grant, a
-- correct-scope write from `security_core` is refused with `permission denied` —
-- which reads as a policy failure and is a missing GRANT, and it would cost an
-- outage rather than a red test. 0008 enumerated its tables by name; this one
-- was created after, so it is granted here, with the same privileges the
-- site-scoped device tables above hold.
GRANT SELECT, INSERT, UPDATE, DELETE ON camera_adjacency TO security_core;

COMMENT ON TABLE camera_adjacency IS
    'Which cameras see which other cameras'' ground, derived from the site model. '
    'Site-scoped rather than a graph edge because adjacency is a fact about one '
    'site''s geometry. Plan C3, pulled forward from M6 on ai-and-data.md''s '
    'recommendation; recomputed by sitemodel.Service.ReplaceFromModel.';
