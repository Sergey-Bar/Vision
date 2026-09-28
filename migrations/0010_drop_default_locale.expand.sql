-- 0010_drop_default_locale.expand.sql
--
-- Drop sites.default_locale.
--
-- English only. The Hebrew-first RTL operator surface was dropped by product
-- decision, and a column that can only ever hold one value is a column somebody
-- will eventually read as if it meant something.
--
-- Dropped by a new migration rather than by editing 0001, which is the whole
-- point of the forward-only rule: a site that already ran version 1 keeps a
-- ledger that matches the SQL that actually ran.

ALTER TABLE sites DROP COLUMN IF EXISTS default_locale;
