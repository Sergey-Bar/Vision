-- 0020_drop_evidence_export_from_the_role_seed.expand.sql
--
-- The seeded role table claimed a capability no code enforces.
--
-- 0008 seeded tenant_admin and security_manager with `"evidence.*"`, which
-- includes `evidence.export`. `evidence.export` is not a route, not a check, and
-- not an audit action: a tree-wide search for it finds the constant in
-- internal/identity, the two role rows that grant it, and nothing that reads it.
-- The capability grants nothing at all — which is worse than not having it,
-- because the role table is the document an auditor reads to answer "who can
-- take this site's evidence out of the building", and it answers with a
-- capability that does not exist and does not gate.
--
-- The capability is removed rather than given a route. Inventing an export route
-- to justify a constant somebody declared is the wrong direction: the cost would
-- be a new route, a new audit action, an export format, and a new way for
-- evidence bytes to leave a site, all to keep a name that was never asked for.
-- `evidence.fetch` already gates the one route that releases evidence bytes, and
-- `evidence.read` gates the metadata. Those are the two things the product
-- enforces, and this migration makes the seed say so.
--
-- This is an UPDATE of seed data and nothing else. No column is dropped, no
-- table is dropped, no type is changed, so it is an expand and not a contract
-- step — which matters, because this milestone cannot ship a contract step at
-- all: `TestM1ContainsNoContractStep` in migrations/migrations_test.go refuses
-- any file whose phase is `contract`, and internal/release refuses to build an
-- archive carrying one.
--
-- The guard is on the row's *current* value, not on its role_id. These are the
-- five roles 0008 inserted and nothing else, and a site whose operator has since
-- edited a role's capabilities is carrying a decision somebody made on purpose.
-- Replacing that with the shipped default because the role_id happened to match
-- would undo it silently, on a table that is the authority for authorization.
-- A site that never edits a role gets exactly the value 0008 intended; a site
-- that does keeps whatever it decided, which is the whole point of the table
-- being data rather than code.
--
-- The comparison is on the text, not on jsonb. `roles.capabilities` is declared
-- `TEXT` with a `'[]'::jsonb` default — the type is the column's and the default
-- is a literal cast, and the difference is not academic: writing `'...'::jsonb`
-- here makes Postgres compare a jsonb against a text column and raise
-- `operator does not exist: text = jsonb` on every site, on the migration
-- rather than on the mistake. 0008's own seed is unadorned string literals for
-- the same reason, and so is this.
--
-- The same reasoning as 0010 applies here and is why 0008 is not edited: a site
-- that already ran version 8 keeps a ledger that matches the SQL that ran.

UPDATE roles
   SET capabilities = '["incident.*","evidence.read","evidence.fetch","rules.*","audit.read","identity.*","site.configure"]'
 WHERE role_id = 'tenant_admin'
   AND capabilities = '["incident.*","evidence.*","rules.*","audit.read","identity.*","site.configure"]';

UPDATE roles
   SET capabilities = '["incident.*","evidence.read","evidence.fetch","rules.read","audit.read","disposition.*"]'
 WHERE role_id = 'security_manager'
   AND capabilities = '["incident.*","evidence.*","rules.read","audit.read","disposition.*"]';
