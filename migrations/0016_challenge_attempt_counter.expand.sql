-- 0016_challenge_attempt_counter.expand.sql
--
-- The MFA step had no brute-force control at all.
--
-- PasswordStep counts failures and locks the account: it has registerFailure and
-- ShouldLock, and MaxFailedAttempts is honoured. CompleteChallenge — the step
-- that consumes a correct password and owes a second factor — had neither. The
-- challenge row is the only thing standing between an intercepted password and a
-- session, it lives for two minutes, and a TOTP code has six digits inside a
-- plus-or-minus thirty-second window. A caller who held a challenge id could
-- simply keep guessing, unbounded, until the code landed.
--
-- The counter lives on the sessions row because that is where a challenge is
-- stored: 0008 put challenges in the sessions table under a "chal_" id so one
-- sweep prunes them. Adding the column there keeps the challenge a single row
-- and keeps this an expand — a new column with a default, which every binary
-- that predates it ignores and every binary that follows it reads.
--
-- The column is only meaningful for rows whose session_id begins "chal_"; for a
-- real session it stays at its default and nothing reads it.

ALTER TABLE sessions
    ADD COLUMN mfa_attempts INTEGER NOT NULL DEFAULT 0;

-- The counter is read on every MFA verify and written on every rejection, and
-- the read is always scoped to one tenant's unconsumed challenges.
--
-- The predicate is a prefix match, not an equality, because the column the read
-- filters on is tenant_id and the thing that distinguishes a challenge from a
-- session is the session_id prefix. Postgres will not use a partial index whose
-- predicate it cannot prove, so `starts_with(session_id, 'chal_')` is written
-- out: the LIKE with a literal prefix has exactly that meaning, and the rewrite
-- is one the planner performs for this pattern.
CREATE INDEX sessions_challenge_idx ON sessions (tenant_id, mfa_attempts)
    WHERE starts_with(session_id, 'chal_');
