-- 0017_mfa_used_codes.expand.sql
--
-- A TOTP code was accepted for as long as it was mathematically valid, and
-- nothing recorded that it had been used.
--
-- RFC 6238 gives a code a 30-second step and this platform verifies one step
-- either side of now, so a code the operator read off their phone is acceptable
-- for roughly ninety seconds. That is a deliberate tolerance — an operator whose
-- device clock is thirty seconds fast otherwise fails in a way that looks like a
-- credential problem — but it also means the same six digits stay usable for
-- ninety seconds after the legitimate operator has already used them. RFC 6238
-- section 5.2 closes this: the verifier MUST NOT accept the same OTP code more
-- than once. Nothing did.
--
-- So a code that shoulder-surfed off a screen, was photographed, or was
-- intercepted in a request that was replayed could be presented a second time
-- inside the window and would be accepted again. The attacker does not need the
-- password, does not need the secret, and does not have to guess: one captured
-- code is worth one extra authentication for the length of the window.
--
-- This table is the last-used-code cache that closes it. One row per
-- (tenant, factor, step, code) that has already been consumed; the primary key is
-- what makes the check a single atomic INSERT ... ON CONFLICT DO NOTHING with the
-- rows-affected count as the verdict, rather than a read-then-write that two
-- concurrent requests carrying the same code can both win.
--
-- Why the code digest is in the key and not only the step: RFC 6238 §5.2 forbids
-- accepting the same code twice, which is narrower than "one use per step". This
-- verifier accepts both SHA-256 and SHA-1 for a single factor, because an
-- authenticator that ignores the `algorithm` parameter in the enrolment URI
-- falls back to SHA-1 — so one step yields two different six-digit codes, and
-- refusing the second would break MFA for whichever client the operator happens
-- to have. The digest is what makes the rule "the same code" and not "the same
-- step", which is the rule the RFC actually states.
--
-- It is a digest, not the code. A six-digit value is a million possibilities, so
-- a hash of one is not confidentiality either, but the table is a read of the
-- database and there is no reason for it to hold usable authentication material.
--
-- The database, not the process, holds this. A cache in memory would be emptied
-- by a restart, and a restart is something an attacker with partial access to a
-- site can arrange; more to the point, an in-memory cache on one node is not a
-- cache at all once there are two.
--
-- Step semantics — the non-obvious part:
--
--   A (step, code) pair is refused only when that exact code at that exact step
--   has already been spent. An older step is still accepted, even after a newer
--   one has been used, and so is the other algorithm's code for a step already
--   spent.
--
--   Refusing every step at or below the highest spent one would look stricter and
--   is wrong. Operators type codes from a device whose clock runs slightly behind
--   the server's, so the code in their hand is routinely from the previous step;
--   RFC 6238 asks no monotonicity of the verifier, and demanding it would refuse a
--   correct operator in order to defend against an attacker who already holds a
--   code they can present exactly once. An unspent older code is not a replay of
--   anything.
--
--   Refusing one use per step would be wrong for the same reason, and for one
--   more: this verifier accepts two hash algorithms per factor, so a single step
--   legitimately carries two different six-digit codes. "The same code" is the
--   unit RFC 6238 §5.2 names, and it is the unit this table keys on.
--
-- Row growth is bounded by one row per successful TOTP verification per factor —
-- that is, roughly one per operator login — so the table stays small without a
-- sweeper. A retention job is not added here because no such job exists for this
-- table yet, and an unbounded delete would be a decision somebody should make
-- with numbers in front of them rather than one smuggled into an expand.
--
-- Row-level security: 0008 applied tenant_isolation and FORCE ROW LEVEL
-- SECURITY to every tenant-bearing relation that existed at the time; this
-- table did not exist then and gets the same treatment below, in the same shape.
-- There is no site_isolation policy because the table carries no site_id: a used
-- code is bound to a factor, and a factor is bound to a user, not to a place.
--
-- Grants: security_core needs this table, because it is the role that verifies a
-- factor. The 0008 grant list is a list of tables that existed at 0008, so a table
-- added afterwards has to be granted here or the verifier runs as a role with no
-- write access to it and every MFA login fails.

CREATE TABLE mfa_used_codes (
    tenant_id     TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    user_id       TEXT NOT NULL,
    -- RFC 6238's T value: the 30-second counter the code was computed over, and
    -- SHA-256 of the six digits presented. Together they make "this exact code has
    -- already been spent" a database-comparable fact rather than a claim about
    -- digits someone typed.
    step_counter  BIGINT NOT NULL,
    code_digest   TEXT NOT NULL,
    used_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, credential_id, step_counter, code_digest),
    FOREIGN KEY (tenant_id, credential_id) REFERENCES mfa_credentials (tenant_id, credential_id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE
);

-- The retention sweep, when there is one, reads by tenant and age.
CREATE INDEX mfa_used_codes_tenant_used_idx ON mfa_used_codes (tenant_id, used_at);

ALTER TABLE mfa_used_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mfa_used_codes FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON mfa_used_codes
    USING (tenant_id = app_current_tenant() OR app_is_system_worker())
    WITH CHECK (tenant_id = app_current_tenant() OR app_is_system_worker());

GRANT SELECT, INSERT, DELETE ON mfa_used_codes TO security_core;
