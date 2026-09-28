-- 0009_outbox_idempotency_scope.expand.sql
--
-- Scope the outbox idempotency key to the tenant.
--
-- The key is UNIQUE globally in 0002. That is wrong for a multi-tenant product:
-- two tenants running the same platform on one instance would collide on any
-- natural key, and the second tenant's side effect would be silently swallowed
-- as a duplicate of the first's. The key is meant to identify a side effect
-- within a tenant, not across the whole instance.

ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS outbox_events_idempotency_key_key;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_tenant_idempotency_key
    UNIQUE (tenant_id, idempotency_key);
