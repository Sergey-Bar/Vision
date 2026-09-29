-- 0014_edges_active_view_access.expand.sql
--
-- Give the product's own role access to the graph's only traversal view, and
-- make that view obey row-level security.
--
-- Two defects, both invisible to the suite, both total in production.
--
-- First: 0003 created edges_active as a view, and 0008 granted privileges on the
-- tables underneath it but not on the view. A view is a distinct object with its
-- own ACL, so security_core held SELECT on edges and edge_revocations and no
-- privilege at all on edges_active. Every graph read the product performs --
-- Snapshot, Walk, CheckRebuild -- goes through the view, so under the role the
-- product runs as every one of them failed with "permission denied for view
-- edges_active". The graph is what the console renders and what an incident
-- cites; this is a total loss of the feature, not a degradation.
--
-- The suite did not see it because it connects as a superuser, which bypasses
-- both ACLs and RLS. The same reason it did not see the second defect.
--
-- Second: a view is security-definer by default in Postgres. The query runs
-- with the privileges of the view's owner, and — more to the point — RLS on the
-- underlying tables is evaluated for the owner, not the caller. The owner is the
-- migration role, and only security_migrate has BYPASSRLS. So even once the
-- grant exists, a snapshot for tenant A would return tenant B's edges: the query
-- reads every row and the isolation happens afterwards. `security_invoker = true`
-- makes the view run as the querying role, which is the only setting under which
-- the tenant policies on `edges` apply to a traversal.
--
-- The fix is the option, not a rewrite of the view. Postgres 15 and later accept
-- security_invoker, and this schema targets Postgres 16.
--
-- This is an expand: it grants a privilege the running code expects and changes
-- an access mode. It does not remove anything, and a binary that predates it
-- fails the same way it failed before.

GRANT SELECT ON edges_active TO security_core;

ALTER VIEW edges_active SET (security_invoker = true);
