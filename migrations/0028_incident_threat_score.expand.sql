-- 0028_incident_threat_score.expand.sql
--
-- The threat score, on the incident.
--
-- # Why the score is a column and not a join
--
-- The score is the ORDER of the attention queue, and the queue is read far more
-- often than the score is computed. A score computed on read would mean
-- re-running the scorer, and the anomaly engine, over every open incident on
-- every poll of the queue -- which is the expensive path on the hot path.
--
-- The score is computed ONCE, when the incident is raised, and stored with the
-- reason chain that produced it. The chain is stored beside it because a score
-- without its chain is a number nobody can check: the operator's question is
-- "why is this first", and the answer is the chain.
--
-- # What the score is NOT
--
-- It is not a severity. Severity is what the rule's author said; the score is
-- what the autonomy layer made of it -- the zone it happened in, how long it
-- lasted, how many devices saw it. An incident can be low severity and high
-- score, and an operator needs both.
--
-- # Why it is not a degradation
--
-- A score of 0 with a chain that says why is a legitimate answer: the incident
-- happened, the autonomy layer found nothing anomalous about it, and the queue
-- ranks it by recency among the others. The chain is written either way, so a
-- score of 0 is distinguishable from a score that was never computed.
--
-- The score is NOT NULL with a default of 0, and the chain is NOT NULL with a
-- default of '[]', so a row that predates this migration reads as "computed,
-- and the answer was nothing" rather than as "not computed". An incident
-- written before the autonomy layer ran is exactly that.

ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS threat_score INTEGER NOT NULL DEFAULT 0
        -- A score outside 0..100 is a score the scorer produced wrongly, and a
        -- wrong score that sorts is worse than a wrong score that is refused.
        CHECK (threat_score >= 0 AND threat_score <= 100),
    ADD COLUMN IF NOT EXISTS threat_reason_chain JSONB NOT NULL DEFAULT '[]'::jsonb;

-- The queue's order: the score first, and the recency inside it.
--
-- A partial index, because the queue asks for OPEN incidents and the closed
-- ones are never ranked. The index carries the site because the queue is
-- site-scoped, and a queue that scans another site's rows to find its own is a
-- queue that leaks another site's count through its timing.
CREATE INDEX incidents_threat_rank_idx
    ON incidents (tenant_id, primary_site_id, threat_score DESC, created_at DESC)
    WHERE state = 'open';

COMMENT ON COLUMN incidents.threat_score IS
    'The autonomy layer''s score for this incident, 0..100, computed once when the incident was raised. The reason chain beside it says how. A score of 0 with a chain is "computed, and nothing was anomalous"; the same 0 with an empty chain on a row written before this migration is "not computed".';
COMMENT ON COLUMN incidents.threat_reason_chain IS
    'The steps the threat scorer took, in order, with the factor each contributed. It is what makes the score checkable: a number without its derivation is a number nobody can act on.';
