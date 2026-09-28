-- 0012_canonical_events_primary_key.expand.sql
--
-- Give canonical_events a primary key.
--
-- It had none. Postgres requires the partition key in a partitioned table's
-- primary key, and the table was created without either, so nothing enforced
-- uniqueness on the event table itself. Dedupe was still correct — it lives in
-- canonical_event_keys — but "a canonical event row exists at most once per
-- (tenant, event id)" was a convention rather than a constraint, and a bug that
-- wrote the same event twice would have been caught by nothing.
--
-- observed_at is in the key because observed_at is the partition key and
-- Postgres will not accept a primary key that omits it. That is a slight
-- widening: the same event id at two different observed times would both fit.
-- In practice an event id implies one observed time, and the scoped key table
-- remains the authority on that.

ALTER TABLE canonical_events
    ADD CONSTRAINT canonical_events_pkey
    PRIMARY KEY (tenant_id, event_id, observed_at);
