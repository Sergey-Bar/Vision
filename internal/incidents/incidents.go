// Package incidents is the case lifecycle and the decision-replay path.
//
// The replay path is the load-bearing part. A decision made months ago must be
// reproduced from stored facts and the stored AIObservation, never by re-running
// the model. Re-running the model would answer a different question - what the
// model says now - and the answer would move with every prompt tweak, every
// model upgrade, and every inference that happened to be non-deterministic. An
// incident an operator signed off on has to still mean what it meant then.
//
// So replay reads:
//
//	verification_results  the decision that was made
//	ai_observations       what the model said, with model, prompt and hashes
//	canonical_events      the facts, as they were recorded
//	incident_events       which facts the decision relied on
//
// and nothing else. No model is invoked. There is no code path in this package
// that could.
package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/config"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// State is an incident's lifecycle state.
type State string

// Incident states. A closed incident does not reopen silently, because closing
// is what an operator signs.
const (
	StateOpen          State = "open"
	StateInReview      State = "in_review"
	StateDispositioned State = "dispositioned"
	StateClosed        State = "closed"
)

// ErrNotFound is returned for an incident the caller may not see or that does
// not exist. The two are not distinguished: telling an operator that an incident
// exists in a tenant they are not assigned to is a disclosure.
var ErrNotFound = errors.New("incidents: no such incident")

// Create is one incident to raise.
type Create struct {
	TenantID string
	SiteID   string
	// RelatedSiteIDs covers the multi-site form. M1 is single-site per install,
	// so this is usually empty, but the column exists because retrofitting it
	// later would be a migration on a table with production rows in it.
	RelatedSiteIDs []string
	Decision       rules.Decision
	// RuleBundleVersion and PolicyVersion are the versions the decision was made
	// under. An incident is not explainable without them.
	RuleBundleVersion string
	BundleContentHash string
	PolicyVersion     string
	Title             string
	// SupportingEventIDs and EvidenceIDs are what the decision cited. Written in
	// the same transaction as the incident so the incident and its justification
	// cannot disagree.
	SupportingEventIDs []string
	EvidenceIDs        []string
	// ThreatScore is the autonomy layer's score for this incident, 0..100, and
	// the chain that produced it.
	//
	// The score is what the attention queue ranks by and the chain is what
	// makes it checkable. They are written with the incident, in the incident's
	// transaction, because a score that is computed on read is the expensive
	// path on the hot path and a chain that lives somewhere else is a chain
	// that can disagree with the score it explains.
	ThreatScore       int
	ThreatReasonChain []threat.ReasonStep
}

// Service manages incidents.
type Service struct {
	Pool *dbx.Pool
	// IDGenerator produces incident ids. Injectable so golden tests are stable.
	IDGenerator func(tenant, site string, at time.Time) string
	// Audit records what happened. Optional in a unit test, never optional in a
	// running system: a state change nobody audited is a state change nobody can
	// be shown to have happened.
	Audit *audit.Writer
	// ThreatEngine is which engine's order the attention queue uses: `legacy`
	// or `calibrated`. Empty means `legacy`.
	//
	// It is a field rather than a parameter because the queue's order is the
	// INSTALL's order, and an install has one engine. A parameter would mean
	// every caller could choose, and two callers choosing differently would
	// mean two queues.
	ThreatEngine string
	// QueueHalfLife is the calibrated engine's recency decay. Zero disables
	// it, which is what the legacy engine has.
	QueueHalfLife time.Duration

	// seq disambiguates two incidents raised within one clock tick.
	//
	// It is here because the WALL CLOCK does not tick between two calls on
	// every platform: the resolution of `time.Now()` is about a millisecond on
	// Windows and about a microsecond on Linux, and an incident id derived
	// only from it repeats when two incidents of the same rule at the same
	// site are raised inside one tick. The second INSERT then failed on the
	// primary key, and the incident was LOST -- which is invariant 1, "zero
	// accepted event loss", broken by a clock's resolution.
	//
	// The counter is per process and per service, and it is only ever a
	// SUFFIX: the id stays time-ordered and site-prefixed, so an operator
	// reading a list of ids still sees the order without a query.
	seq atomic.Uint64
}

// CreateIncident raises an incident from a decision and returns its id.
func (s *Service) CreateIncident(ctx context.Context, in Create) (string, error) {
	if in.TenantID == "" {
		return "", errors.New("incidents: tenant id is required and is never read from a request body")
	}
	if in.SiteID == "" {
		return "", errors.New("incidents: site id is required; an incident is physically site-bound")
	}
	if in.Decision.RuleID == "" {
		return "", errors.New("incidents: a decision with no rule id cannot be explained and cannot be raised")
	}

	now := time.Now().UTC()
	id := s.newID(in.TenantID, in.SiteID, now)

	err := s.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: in.TenantID,
		ActorID:  "verification",
	}, func(tx pgx.Tx) error {
		related, err := json.Marshal(nonNil(in.RelatedSiteIDs))
		if err != nil {
			return fmt.Errorf("incidents: encode related sites: %w", err)
		}
		degradedReasons, err := json.Marshal([]string{string(in.Decision.DegradedReason)})
		if err != nil {
			return fmt.Errorf("incidents: encode degradation: %w", err)
		}

		// The threat score and its chain, in the incident's transaction.
		//
		// A score stored apart from the incident is a score that can disagree with
		// the incident it describes, and a chain stored apart from the score is a
		// chain that explains something else.
		chain, err := json.Marshal(in.ThreatReasonChain)
		if err != nil {
			return fmt.Errorf("incidents: encode the threat reason chain: %w", err)
		}
		if in.ThreatReasonChain == nil {
			chain = []byte("[]")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO incidents (incident_id, tenant_id, primary_site_id, related_site_ids,
			                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
			                       state, severity, title, first_event_at, last_event_at,
			                       degraded, degraded_reasons, threat_score, threat_reason_chain)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'open',$9,$10,now(),now(),$11,$12,$13,$14::jsonb)`,
			id, in.TenantID, in.SiteID, related,
			in.Decision.RuleID, in.RuleBundleVersion, in.BundleContentHash, in.PolicyVersion,
			orDefault(in.Decision.Severity, "medium"), orDefault(in.Title, in.Decision.RuleID),
			in.Decision.Degraded, degradedReasons, in.ThreatScore, chain); err != nil {
			return fmt.Errorf("incidents: insert: %w", err)
		}

		// The supporting events and the verification result are in the same
		// transaction as the incident. An incident whose justification is written
		// separately is an incident that can exist without one.
		//
		// The trigger is linked whether or not the decision lists it.
		//
		// This iterated only `in.Decision.SupportingEvents`, and read
		// `in.SupportingEventIDs` solely to decide which of them was the trigger.
		// A rule with no `corroborate` clause resolves to StateCorroborated with
		// the reason "no_corroboration_required" and an EMPTY supporting list, so
		// the incident was inserted with no `incident_events` rows at all. Replay
		// then reads `detail.Events == nil`, `signalsFrom` returns a zero-valued
		// trigger, and `reevaluate` runs the rule against `EventType: ""`. The
		// replay mismatch was structural — the incident had never recorded what it
		// was about — and not a property of the bundle.
		linked := make([]string, 0, len(in.Decision.SupportingEvents)+1)
		trigger := first(in.SupportingEventIDs)
		if trigger != "" {
			linked = append(linked, trigger)
		}
		linked = append(linked, in.Decision.SupportingEvents...)

		for _, eventID := range linked {
			role := "supporting"
			if eventID == trigger {
				role = "trigger"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
				VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
				in.TenantID, id, eventID, role); err != nil {
				return fmt.Errorf("incidents: link event: %w", err)
			}
		}
		for _, eventID := range in.Decision.ContradictingEvents {
			if _, err := tx.Exec(ctx, `
				INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
				VALUES ($1,$2,$3,'contradicting') ON CONFLICT DO NOTHING`,
				in.TenantID, id, eventID); err != nil {
				return fmt.Errorf("incidents: link contradicting event: %w", err)
			}
		}
		for _, evidenceID := range in.EvidenceIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO incident_evidence (tenant_id, incident_id, evidence_id)
				VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				in.TenantID, id, evidenceID); err != nil {
				return fmt.Errorf("incidents: link evidence: %w", err)
			}
		}

		exclusions, err := json.Marshal(exclusionRecords(in.Decision.DriftExclusions))
		if err != nil {
			return fmt.Errorf("incidents: encode drift exclusions: %w", err)
		}
		supportingEvents, err := json.Marshal(nonNil(in.Decision.SupportingEvents))
		if err != nil {
			return fmt.Errorf("incidents: encode supporting events: %w", err)
		}
		supportingObs, err := json.Marshal(nonNil(in.Decision.SupportingObservations))
		if err != nil {
			return fmt.Errorf("incidents: encode supporting observations: %w", err)
		}
		contradicting, err := json.Marshal(nonNil(in.Decision.ContradictingEvents))
		if err != nil {
			return fmt.Errorf("incidents: encode contradicting events: %w", err)
		}

		// Storage uses the lowercase form every other constrained column in this
		// schema uses, and the API reads it back uppercased to match the
		// published contract. One canonical storage form beats two.
		if _, err := tx.Exec(ctx, `
			INSERT INTO verification_results (
				verification_id, tenant_id, incident_id, rule_id, rule_bundle_version,
				bundle_content_hash, policy_version, state, min_required_tier,
				supporting_event_ids, supporting_observation_ids, contradicting_event_ids,
				drift_exclusions, reason_code, explanation, ai_degradation, deterministic)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'B',$9,$10,$11,$12,$13,$14,$15,TRUE)`,
			"vr-"+id, in.TenantID, id, in.Decision.RuleID, in.RuleBundleVersion,
			in.BundleContentHash, in.PolicyVersion, strings.ToLower(string(in.Decision.State)),
			supportingEvents, supportingObs, contradicting, exclusions,
			orDefault(in.Decision.Reason, "unspecified"),
			explain(in.Decision), string(in.Decision.DegradedReason)); err != nil {
			return fmt.Errorf("incidents: record verification: %w", err)
		}

		// The dispatches the winning rule asked for, in THIS transaction.
		//
		// This is the durability fix. `internal/pipeline` used to call `CreateIncident`,
		// let it commit, and only then open a SECOND transaction to enqueue the VLM job -
		// so an incident could commit and the work about it be lost, with nothing
		// recording that the work was supposed to happen. Invariant 1 is "anything reaching
		// inbox_events reaches a terminal state"; the same has to be true of a side effect
		// an incident promises, and the only way to get it is one transaction.
		//
		// `outbox.Enqueue` takes the caller's `pgx.Tx` precisely so this is possible. The
		// rows are written here and the relay drains them afterwards, which is what makes
		// the commit atomic without making the delivery synchronous with the ingest.
		//
		// A dispatch that cannot be built is RECORDED and the incident is still
		// raised.
		//
		// The earlier version returned the error, and the error rolled back the
		// incident with it: a rule that MATCHED -- the product had decided that
		// something happened -- produced no incident at all, because a side
		// effect of the incident could not be built. The operator's record of a
		// decision is more important than the decision's side effect, and a rule
		// asking for a model that is not there is the ordinary case, not a
		// misconfiguration: the rule asks for an observation, the observation is
		// recorded as degraded with its reason, and the incident stands.
		//
		// The gap the old code was closing is still closed: the dispatch is not
		// silently dropped. It is written to the ledger with the reason it could
		// not be built, which is the same record the operator reads.
		dispatchErr := dispatch.EnqueueOutbox(ctx, tx, dispatch.Request{
			TenantID:   in.TenantID,
			SiteID:     in.SiteID,
			IncidentID: id,
			// The trigger event, which a VLM job cannot be built without: the worker
			// looks the evidence up from the event.
			EventID:    first(in.SupportingEventIDs),
			EvidenceID: first(in.EvidenceIDs),
			RuleID:     in.Decision.RuleID,
			Title:      orDefault(in.Title, in.Decision.RuleID),
			Severity:   orDefault(in.Decision.Severity, "medium"),
			OccurredAt: in.Decision.OccurredAt,
			Dispatch:   in.Decision.Dispatch,
		})
		if dispatchErr != nil {
			if s.Audit != nil {
				if _, err := s.Audit.Append(ctx, tx, audit.Entry{
					TenantID:        in.TenantID,
					Actor:           "verification",
					ActorType:       audit.ActorSystem,
					Action:          audit.ActionIncidentDispatchUndeliverable,
					TargetType:      "incident",
					TargetID:        id,
					PolicyVersion:   in.PolicyVersion,
					Reason:          "the incident stands; a dispatch of it could not be built: " + dispatchErr.Error(),
					EvidenceRefs:    in.EvidenceIDs,
					ResultStateHash: in.BundleContentHash,
				}); err != nil {
					return err
				}
			}
		}

		if s.Audit != nil {
			if _, err := s.Audit.Append(ctx, tx, audit.Entry{
				TenantID:        in.TenantID,
				Actor:           "verification",
				ActorType:       audit.ActorSystem,
				Action:          audit.ActionIncidentCreated,
				TargetType:      "incident",
				TargetID:        id,
				PolicyVersion:   in.PolicyVersion,
				Reason:          in.Decision.Reason,
				EvidenceRefs:    in.EvidenceIDs,
				ResultStateHash: in.BundleContentHash,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// ConsumeStepUp spends a step-up proof against this incident's tenant.
//
// The proof lives in the identity package and is bound to a user, so the
// identity service spends it. This wrapper exists so the API has one call, and
// so a handler cannot accidentally skip the check by recording a disposition
// without one.
func (s *Service) ConsumeStepUp(ctx context.Context, identities StepUpConsumer, tenant, userID, proof string) error {
	if identities == nil {
		return errors.New("incidents: no step-up consumer is configured")
	}
	return identities.ConsumeStepUp(ctx, tenant, userID, proof)
}

// StepUpConsumer is the identity service's step-up check, as this package needs
// it. Declaring it here rather than importing identity keeps the dependency one
// way: incidents does not know how a factor is verified, only that one was.
type StepUpConsumer interface {
	ConsumeStepUp(ctx context.Context, tenant, userID, proof string) error
}

func (s *Service) newID(tenant, site string, at time.Time) string {
	if s.IDGenerator != nil {
		return s.IDGenerator(tenant, site, at)
	}
	// Time-ordered and prefixed, so an operator reading a list of ids sees the
	// order without a query and two incidents cannot collide.
	//
	// The COUNTER is what makes the second promise true. The wall clock does
	// not tick between every pair of calls — its resolution is about a
	// millisecond on Windows — so two incidents raised at one site inside one
	// tick used to derive the SAME id, and the second INSERT failed on the
	// primary key. The incident was then lost, which is invariant 1 broken
	// by a clock.
	return fmt.Sprintf("inc_%s_%d_%d", site, at.UnixNano(), s.seq.Add(1))
}

// Summary is a feed row.
type Summary struct {
	IncidentID string    `json:"incidentId"`
	SiteID     string    `json:"siteId"`
	State      string    `json:"state"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	RuleID     string    `json:"ruleId"`
	CreatedAt  time.Time `json:"createdAt"`
	FirstEvent time.Time `json:"firstEventAt"`
	// Degraded is the flag a pilot measures. An incident raised without the AI
	// plane is not a defect, but a pilot claiming AI reduced operator effort has
	// to be able to say how often the AI was actually there.
	Degraded        bool     `json:"degraded"`
	DegradedReasons []string `json:"degradedReasons,omitempty"`
	BundleVersion   string   `json:"ruleBundleVersion"`
	PolicyVersion   string   `json:"policyVersion"`
	// ThreatScore is the autonomy layer's score for this incident, 0..100, and
	// the chain that produced it.
	//
	// The score is what the attention queue ranks by, and the chain is what
	// makes it checkable: "why is this first" is answered by the chain, not by
	// the number.
	ThreatScore       int                 `json:"threatScore"`
	ThreatReasonChain []threat.ReasonStep `json:"threatReasonChain,omitempty"`
	// QueueScore is the score the attention queue ORDERS BY, which is not
	// always the stored score: under the calibrated engine it is the stored
	// score times the queue's recency factor.
	//
	// It is a property of the queue and not of the incident, and it is not
	// written to the incident's row for that reason. The decay is applied on
	// read because the queue's half-life is a configuration value and an
	// incident's score is a fact.
	QueueScore int `json:"queueScore"`
}

// List returns a tenant's incident feed, newest first.
func (s *Service) List(ctx context.Context, tenant, siteID string, limit int) ([]Summary, error) {
	return s.ListScoped(ctx, tenant, siteID, limit)
}

// ListAttention returns the tenant's open incidents in the order the operator
// should look at them.
//
// # The order is the product's judgement
//
// The order is the THREAT SCORE first and the recency inside it, and it is
// different from the feed's order on purpose. The feed is a record: it is
// newest first because that is what a record is for. The queue is a
// judgement: an incident that scored 80 twenty minutes ago comes before one
// that scored 10 a second ago, because the score is the autonomy layer's
// answer to "which of these needs a person" and recency is not that answer.
//
// A score of 0 is a legitimate rank: it says the autonomy layer found nothing
// anomalous, and those incidents are ordered among themselves by recency.
//
// # The two engines order it differently
//
// The LEGACY engine's order is the raw score. That is the order the product
// shipped with, and it is the product's own judgement, which is why the API
// stopped re-sorting the queue by severity: the API's sort was a second
// judgement, made in a place that had never measured anything.
//
// The CALIBRATED engine's order is the DECAYED score: the score an incident
// carries in the queue is its raw score times a recency factor, so an
// incident from this morning falls down the queue while one from a minute ago
// rises. The decay is a property of the QUEUE. The incident's stored score
// does not change, because an incident's score is a fact about the incident
// and a queue's order is a judgement.
//
// The decay has to be in the SQL rather than in the handler, because the
// query has a LIMIT: a page of the top twenty by RAW score would exclude the
// very incidents the decay promotes, and the queue would be ordered by a
// decay that had already thrown its winners away.
func (s *Service) ListAttention(ctx context.Context, tenant, siteIDs string, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var sites []string
	for _, site := range strings.Split(siteIDs, ",") {
		if site = strings.TrimSpace(site); site != "" {
			sites = append(sites, site)
		}
	}

	// The two queue scores.
	//
	// The severity is a TIE-BREAK inside the score and not a replacement
	// for it. It used to be a replacement: the API re-sorted the whole queue
	// by severity after the product had ordered it by score, so the queue an
	// operator read was a severity list with the product's judgement thrown
	// away. The severity still says something -- two incidents with the same
	// score are not equally urgent -- and now it says it where the order is
	// decided, as the second key.
	//
	// The calibrated engine's score is the DECAYED one: the score an
	// incident carries in the queue is its raw score times a recency factor,
	// so an incident from this morning falls down the queue while one from a
	// minute ago rises. The decay is a property of the QUEUE. The incident's
	// stored score does not change, because an incident's score is a fact
	// about the incident and a queue's order is a judgement.
	//
	// The decay has to be in the SQL rather than in the handler, because the
	// query has a LIMIT: a page of the top twenty by RAW score would exclude
	// the very incidents the decay promotes, and the queue would be ordered
	// by a decay that had already thrown its winners away.
	queueScore := "threat_score"
	if s.ThreatEngine == config.ThreatEngineCalibrated && s.QueueHalfLife > 0 {
		// The product of an integer and `power()` is a NUMERIC, and the
		// queue's score is an integer, so it is rounded and cast. Rounded and
		// not truncated: a score of 5.6 is six, and a queue that rounds down
		// every decayed score is a queue that quietly loses a point an
		// incident earned.
		queueScore = `(round(threat_score * power(0.5, GREATEST(0, extract(epoch FROM (now() - created_at))) / ` +
			strconv.FormatInt(int64(s.QueueHalfLife.Seconds()), 10) + `)))::int`
	}
	order := queueScore + " DESC, created_at DESC"

	rows, err := s.Pool.For(tenant, "incidents").Query(ctx, `
		SELECT incident_id, primary_site_id, state, severity, title, rule_id,
		       created_at, first_event_at, degraded, degraded_reasons,
		       rule_bundle_version, policy_version, threat_score, threat_reason_chain,
		       `+queueScore+` AS queue_score
		  FROM incidents
		 WHERE tenant_id = $1
		   AND state = 'open'
		   AND ($2::text[] IS NULL OR primary_site_id = ANY($2::text[]))
		 ORDER BY `+order+`
		 LIMIT $3`, tenant, sites, limit)
	if err != nil {
		return nil, fmt.Errorf("incidents: list the attention queue: %w", err)
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var item Summary
		var reasons []byte
		var chain []byte
		if err := rows.Scan(&item.IncidentID, &item.SiteID, &item.State, &item.Severity,
			&item.Title, &item.RuleID, &item.CreatedAt, &item.FirstEvent,
			&item.Degraded, &reasons, &item.BundleVersion, &item.PolicyVersion,
			&item.ThreatScore, &chain, &item.QueueScore); err != nil {
			return nil, fmt.Errorf("incidents: scan: %w", err)
		}
		_ = json.Unmarshal(reasons, &item.DegradedReasons)
		_ = json.Unmarshal(chain, &item.ThreatReasonChain)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListWithCursor returns a page of incidents with cursor-based pagination.
// The cursor is an opaque string encoding (created_at, incident_id) of the last
// item in the previous page. An empty cursor means the first page.
func (s *Service) ListWithCursor(ctx context.Context, tenant, siteIDs string, cursor string, limit int, state string, degraded *bool) ([]Summary, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var sites []string
	for _, s := range strings.Split(siteIDs, ",") {
		if s = strings.TrimSpace(s); s != "" {
			sites = append(sites, s)
		}
	}

	// Parse cursor if provided
	var cursorTime time.Time
	var cursorID string
	if cursor != "" {
		parts := strings.Split(cursor, "|")
		if len(parts) == 2 {
			if t, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
				cursorTime = t
				cursorID = parts[1]
			}
		}
	}

	query := `
		SELECT incident_id, primary_site_id, state, severity, title, rule_id,
		       created_at, first_event_at, degraded, degraded_reasons,
		       rule_bundle_version, policy_version
		  FROM incidents
		 WHERE tenant_id = $1
		   AND ($2::text[] IS NULL OR primary_site_id = ANY($2::text[]))`

	args := []any{tenant, sites}
	argIdx := 3

	if !cursorTime.IsZero() {
		query += fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND incident_id < $%d))", argIdx, argIdx, argIdx+1)
		args = append(args, cursorTime, cursorID)
		argIdx += 2
	}

	if state != "" {
		query += fmt.Sprintf(" AND state = $%d", argIdx)
		args = append(args, state)
		argIdx++
	}

	if degraded != nil {
		query += fmt.Sprintf(" AND degraded = $%d", argIdx)
		args = append(args, *degraded)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, incident_id DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.Pool.For(tenant, "incidents").Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("incidents: list: %w", err)
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var item Summary
		var reasons []byte
		if err := rows.Scan(&item.IncidentID, &item.SiteID, &item.State, &item.Severity,
			&item.Title, &item.RuleID, &item.CreatedAt, &item.FirstEvent,
			&item.Degraded, &reasons, &item.BundleVersion, &item.PolicyVersion); err != nil {
			return nil, "", fmt.Errorf("incidents: scan: %w", err)
		}
		_ = json.Unmarshal(reasons, &item.DegradedReasons)
		out = append(out, item)
	}

	var nextCursor string
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = fmt.Sprintf("%s|%s", last.CreatedAt.UTC().Format(time.RFC3339Nano), last.IncidentID)
	}

	return out, nextCursor, rows.Err()
}

// ListScoped returns the incident feed restricted to a set of sites.
//
// `siteIDs` is a comma-separated list, or "" for every site in the tenant. The
// restriction is in the query rather than applied to the rows afterwards,
// because a caller that asks for fifty rows and then filters them has already
// been served the incidents it was not meant to see - the count and the
// pagination were computed over data the principal could not have.
func (s *Service) ListScoped(ctx context.Context, tenant, siteIDs string, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var sites []string
	for _, s := range strings.Split(siteIDs, ",") {
		if s = strings.TrimSpace(s); s != "" {
			sites = append(sites, s)
		}
	}
	rows, err := s.Pool.For(tenant, "incidents").Query(ctx, `
		SELECT incident_id, primary_site_id, state, severity, title, rule_id,
		       created_at, first_event_at, degraded, degraded_reasons,
		       rule_bundle_version, policy_version, threat_score, threat_reason_chain
		  FROM incidents
		 WHERE tenant_id = $1
		   AND ($2::text[] IS NULL OR primary_site_id = ANY($2::text[]))
		 ORDER BY created_at DESC
		 LIMIT $3`, tenant, sites, limit)
	if err != nil {
		return nil, fmt.Errorf("incidents: list: %w", err)
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var item Summary
		var reasons []byte
		var chain []byte
		if err := rows.Scan(&item.IncidentID, &item.SiteID, &item.State, &item.Severity,
			&item.Title, &item.RuleID, &item.CreatedAt, &item.FirstEvent,
			&item.Degraded, &reasons, &item.BundleVersion, &item.PolicyVersion,
			&item.ThreatScore, &chain); err != nil {
			return nil, fmt.Errorf("incidents: scan: %w", err)
		}
		_ = json.Unmarshal(reasons, &item.DegradedReasons)
		_ = json.Unmarshal(chain, &item.ThreatReasonChain)
		out = append(out, item)
	}
	return out, rows.Err()
}

// Detail is the full incident view the console and `incident show` read.
type Detail struct {
	Summary
	Verification VerificationView  `json:"verification"`
	Events       []EventView       `json:"events"`
	Observations []ObservationView `json:"observations"`
	Evidence     []EvidenceView    `json:"evidence"`
	Dispositions []DispositionView `json:"dispositions"`
}

// VerificationView is the stored decision.
type VerificationView struct {
	VerificationID         string            `json:"verificationId"`
	State                  string            `json:"state"`
	ReasonCode             string            `json:"reasonCode"`
	Explanation            string            `json:"explanation"`
	SupportingEvents       []string          `json:"supportingEventIds"`
	SupportingObservations []string          `json:"supportingObservationIds"`
	ContradictingEvents    []string          `json:"contradictingEventIds"`
	DriftExclusions        []rules.Exclusion `json:"driftExclusions"`
	AIDegradation          string            `json:"aiDegradation"`
	EvaluatedAt            time.Time         `json:"evaluatedAt"`
}

// EventView is one stored fact, with the three timestamps kept apart.
type EventView struct {
	EventID       string    `json:"eventId"`
	SourceEventID string    `json:"sourceEventId"`
	EventType     string    `json:"eventType"`
	DeviceID      string    `json:"deviceId,omitempty"`
	ZoneID        string    `json:"zoneId,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	ReceivedAt    time.Time `json:"receivedAt"`
	NormalizedAt  time.Time `json:"normalizedAt"`
	ClockSource   string    `json:"clockSource"`
	ClockDriftMS  int32     `json:"clockDriftMs"`
	IntegrityHash string    `json:"integrityHash"`
	Payload       any       `json:"payload"`
}

// ObservationView is a stored AI observation, with its attribution.
type ObservationView struct {
	ObservationID           string    `json:"observationId"`
	ProfileID               string    `json:"profileId"`
	Model                   string    `json:"model"`
	ModelHash               string    `json:"modelHash"`
	RuntimeVersion          string    `json:"runtimeVersion"`
	PromptTemplateID        string    `json:"promptTemplateId"`
	PromptVersion           string    `json:"promptVersion"`
	BundleSignature         string    `json:"modelBundleSignature,omitempty"`
	InputEvidenceHashes     []string  `json:"inputEvidenceHashes"`
	OutputSchemaVersion     int       `json:"outputSchemaVersion"`
	StructuredOutput        any       `json:"structuredOutput"`
	InsufficientEvidence    bool      `json:"insufficientEvidence"`
	DegradedReason          string    `json:"degradedReason"`
	LatencyMS               int64     `json:"latencyMs"`
	OutputHash              string    `json:"outputHash"`
	CreatedAt               time.Time `json:"createdAt"`
	SupersedesObservationID string    `json:"supersedesObservationId,omitempty"`
	ProductionProfile       bool      `json:"productionProfile"`
}

// EvidenceView is one stored evidence object.
type EvidenceView struct {
	EvidenceID string    `json:"evidenceId"`
	Kind       string    `json:"kind"`
	Tier       string    `json:"tier"`
	StorageRef string    `json:"storageRef"`
	SHA256     string    `json:"sha256"`
	SizeBytes  int64     `json:"sizeBytes"`
	CapturedAt time.Time `json:"capturedAt"`
	StoredAt   time.Time `json:"storedAt"`
	LegalHold  bool      `json:"legalHold"`
}

// Evidence reads ONE evidence row, scoped to the sites a principal may see.
//
// It is a reader of the same table the incident's evidence list reads, so the
// detail page and the list cannot disagree about an evidence row's kind, its
// digest or its size.
//
// The site scope is in the SQL as well as in the row-level-security policy.
// The policy is the boundary; the filter is the query, and a query that
// returns a row the policy would have refused is a row the reader did not ask
// for.
func (s *Service) Evidence(ctx context.Context, tenant, evidenceID string, sites []string) (EvidenceView, error) {
	var v EvidenceView
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "console"},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT evidence_id, kind, tier, storage_ref, sha256, size_bytes,
				       captured_at, stored_at, legal_hold
				  FROM evidence
				 WHERE tenant_id = $1
				   AND site_id = ANY($2)
				   AND evidence_id = $3`,
				tenant, sites, evidenceID).Scan(
				&v.EvidenceID, &v.Kind, &v.Tier, &v.StorageRef, &v.SHA256, &v.SizeBytes,
				&v.CapturedAt, &v.StoredAt, &v.LegalHold)
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EvidenceView{}, ErrNoSuchEvidence
		}
		return EvidenceView{}, fmt.Errorf("incidents: read the evidence %s: %w", evidenceID, err)
	}
	return v, nil
}

// ErrNoSuchEvidence is an evidence row the reader does not hold.
//
// It is one error for "there is no such evidence" and "it is at a site the
// principal may not see", because the two are the same answer to a caller and
// a different one is a disclosure.
var ErrNoSuchEvidence = errors.New("incidents: no such evidence")

// DispositionView is an operator's answer. It never overwrites the observation.
type DispositionView struct {
	DispositionID        string    `json:"dispositionId"`
	ActorID              string    `json:"actorId"`
	Outcome              string    `json:"outcome"`
	Agreement            string    `json:"agreement"`
	ReasonCode           string    `json:"reasonCode"`
	Notes                string    `json:"notes"`
	RelatedObservationID string    `json:"relatedObservationId,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
}

// Get reads one incident with everything needed to explain it.
func (s *Service) Get(ctx context.Context, tenant, incidentID string) (Detail, error) {
	var d Detail
	var reasons []byte

	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "console"},
		func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `
				SELECT incident_id, primary_site_id, state, severity, title, rule_id,
				       created_at, first_event_at, degraded, degraded_reasons,
				       rule_bundle_version, policy_version
				  FROM incidents WHERE tenant_id = $1 AND incident_id = $2`,
				tenant, incidentID).Scan(
				&d.IncidentID, &d.SiteID, &d.State, &d.Severity, &d.Title, &d.RuleID,
				&d.CreatedAt, &d.FirstEvent, &d.Degraded, &reasons,
				&d.BundleVersion, &d.PolicyVersion); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrNotFound
				}
				return fmt.Errorf("incidents: get: %w", err)
			}
			_ = json.Unmarshal(reasons, &d.DegradedReasons)

			if err := scanVerification(ctx, tx, tenant, incidentID, &d.Verification); err != nil {
				return err
			}
			if err := scanEvents(ctx, tx, tenant, incidentID, &d.Events); err != nil {
				return err
			}
			if err := scanObservations(ctx, tx, tenant, incidentID, &d.Observations); err != nil {
				return err
			}
			if err := scanEvidence(ctx, tx, tenant, incidentID, &d.Evidence); err != nil {
				return err
			}
			return scanDispositions(ctx, tx, tenant, incidentID, &d.Dispositions)
		})
	return d, err
}

func scanVerification(ctx context.Context, tx pgx.Tx, tenant, incidentID string, v *VerificationView) error {
	var supporting, supportingObs, contradicting, exclusions []byte
	err := tx.QueryRow(ctx, `
		SELECT verification_id, state, reason_code, explanation, supporting_event_ids,
		       supporting_observation_ids, contradicting_event_ids, drift_exclusions,
		       ai_degradation, evaluated_at
		  FROM verification_results
		 WHERE tenant_id = $1 AND incident_id = $2
		 ORDER BY evaluated_at DESC LIMIT 1`, tenant, incidentID).Scan(
		&v.VerificationID, &v.State, &v.ReasonCode, &v.Explanation, &supporting,
		&supportingObs, &contradicting, &exclusions, &v.AIDegradation, &v.EvaluatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("incidents: verification: %w", err)
	}
	_ = json.Unmarshal(supporting, &v.SupportingEvents)
	_ = json.Unmarshal(supportingObs, &v.SupportingObservations)
	_ = json.Unmarshal(contradicting, &v.ContradictingEvents)
	_ = json.Unmarshal(exclusions, &v.DriftExclusions)
	// Stored lowercase to match the rest of the schema, published uppercase to
	// match the OpenAPI contract. The mapping lives here so it is in one place.
	v.State = strings.ToUpper(v.State)
	return nil
}

func scanEvents(ctx context.Context, tx pgx.Tx, tenant, incidentID string, out *[]EventView) error {
	rows, err := tx.Query(ctx, `
		SELECT c.event_id, c.source_event_id, c.event_type,
		       COALESCE(c.device_id, ''), COALESCE(c.zone_id, ''),
		       c.observed_at, c.received_at, c.normalized_at,
		       c.clock_source, c.clock_drift_ms, c.integrity_hash, c.payload
		  FROM incident_events ie
		  JOIN canonical_events c
		    ON c.tenant_id = ie.tenant_id AND c.event_id = ie.event_id
		 WHERE ie.tenant_id = $1 AND ie.incident_id = $2
		 ORDER BY c.normalized_at, c.event_id`, tenant, incidentID)
	if err != nil {
		return fmt.Errorf("incidents: events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e EventView
		var payload []byte
		if err := rows.Scan(&e.EventID, &e.SourceEventID, &e.EventType, &e.DeviceID, &e.ZoneID,
			&e.ObservedAt, &e.ReceivedAt, &e.NormalizedAt, &e.ClockSource, &e.ClockDriftMS,
			&e.IntegrityHash, &payload); err != nil {
			return fmt.Errorf("incidents: scan event: %w", err)
		}
		_ = json.Unmarshal(payload, &e.Payload)
		*out = append(*out, e)
	}
	return rows.Err()
}

func scanObservations(ctx context.Context, tx pgx.Tx, tenant, incidentID string, out *[]ObservationView) error {
	rows, err := tx.Query(ctx, `
		SELECT observation_id, profile_id, model, model_hash, runtime_version,
		       prompt_template_id, prompt_version, COALESCE(model_bundle_signature, ''),
		       input_evidence_hashes, output_schema_version, structured_output,
		       insufficient_evidence, degraded_reason, latency_ms, output_hash, created_at,
		       COALESCE(supersedes_observation_id, ''), production_profile
		  FROM ai_observations
		 WHERE tenant_id = $1 AND incident_id = $2
		 ORDER BY created_at, observation_id`, tenant, incidentID)
	if err != nil {
		return fmt.Errorf("incidents: observations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o ObservationView
		var hashes, output []byte
		if err := rows.Scan(&o.ObservationID, &o.ProfileID, &o.Model, &o.ModelHash, &o.RuntimeVersion,
			&o.PromptTemplateID, &o.PromptVersion, &o.BundleSignature, &hashes,
			&o.OutputSchemaVersion, &output, &o.InsufficientEvidence, &o.DegradedReason,
			&o.LatencyMS, &o.OutputHash, &o.CreatedAt, &o.SupersedesObservationID,
			&o.ProductionProfile); err != nil {
			return fmt.Errorf("incidents: scan observation: %w", err)
		}
		_ = json.Unmarshal(hashes, &o.InputEvidenceHashes)
		_ = json.Unmarshal(output, &o.StructuredOutput)
		*out = append(*out, o)
	}
	return rows.Err()
}

func scanEvidence(ctx context.Context, tx pgx.Tx, tenant, incidentID string, out *[]EvidenceView) error {
	rows, err := tx.Query(ctx, `
		SELECT e.evidence_id, e.kind, e.tier, e.storage_ref, e.sha256, e.size_bytes,
		       e.captured_at, e.stored_at, e.legal_hold
		  FROM incident_evidence ie
		  JOIN evidence e ON e.tenant_id = ie.tenant_id AND e.evidence_id = ie.evidence_id
		 WHERE ie.tenant_id = $1 AND ie.incident_id = $2
		 ORDER BY e.captured_at`, tenant, incidentID)
	if err != nil {
		return fmt.Errorf("incidents: evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e EvidenceView
		if err := rows.Scan(&e.EvidenceID, &e.Kind, &e.Tier, &e.StorageRef, &e.SHA256,
			&e.SizeBytes, &e.CapturedAt, &e.StoredAt, &e.LegalHold); err != nil {
			return fmt.Errorf("incidents: scan evidence: %w", err)
		}
		*out = append(*out, e)
	}
	return rows.Err()
}

func scanDispositions(ctx context.Context, tx pgx.Tx, tenant, incidentID string, out *[]DispositionView) error {
	rows, err := tx.Query(ctx, `
		SELECT disposition_id, actor_id, outcome, agreement, reason_code, notes,
		       COALESCE(related_observation_id, ''), created_at
		  FROM dispositions
		 WHERE tenant_id = $1 AND incident_id = $2
		 ORDER BY created_at, disposition_id`, tenant, incidentID)
	if err != nil {
		return fmt.Errorf("incidents: dispositions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DispositionView
		if err := rows.Scan(&d.DispositionID, &d.ActorID, &d.Outcome, &d.Agreement,
			&d.ReasonCode, &d.Notes, &d.RelatedObservationID, &d.CreatedAt); err != nil {
			return fmt.Errorf("incidents: scan disposition: %w", err)
		}
		*out = append(*out, d)
	}
	return rows.Err()
}

// SetState moves an incident through its lifecycle, auditing every transition.
//
// The read, the check and the write are one statement.
//
// The current state was read on a separate connection outside the transaction,
// checked against a lifecycle table that is advisory, and then written with no
// guard on the state it was read from. Two operators racing — A reads `open` and
// asks for `in_review`, B closes — had A's update land after B's and set
// `state = 'in_review'` while `COALESCE($4, closed_at)` preserved the close
// timestamp. The incident was simultaneously dispositioned-as-open and closed,
// and nothing in the ledger recorded that the two updates interleaved.
//
// `AND state = $from` is the whole fix: the update matches the state the check was
// made against, so the loser of a race updates zero rows and is told so, rather
// than silently overwriting the winner.
func (s *Service) SetState(ctx context.Context, tenant, incidentID string, to State, actor, reason string) error {
	var from State
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: actor},
		func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `
				SELECT state FROM incidents
				 WHERE tenant_id = $1 AND incident_id = $2
				 FOR UPDATE`, tenant, incidentID).Scan(&from); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("incidents: no such incident %s", incidentID)
				}
				return fmt.Errorf("incidents: read state: %w", err)
			}
			if !lifecycleAllows(from, to) {
				return fmt.Errorf("incidents: %s cannot move from %s to %s", incidentID, from, to)
			}
			closed := any(nil)
			if to == StateClosed {
				closed = time.Now().UTC()
			}
			tag, err := tx.Exec(ctx, `
				UPDATE incidents SET state = $4, closed_at = COALESCE($5, closed_at)
				 WHERE tenant_id = $1 AND incident_id = $2 AND state = $3`,
				tenant, incidentID, string(from), string(to), closed)
			if err != nil {
				return fmt.Errorf("incidents: set state: %w", err)
			}
			if tag.RowsAffected() == 0 {
				// The row lock held for the whole transaction, so this is only
				// reachable if the lifecycle changed underneath us. Either way the
				// caller is told the move did not happen.
				return fmt.Errorf("incidents: %s is no longer in %s; the transition to %s did "+
					"not happen", incidentID, from, to)
			}
			if s.Audit == nil {
				return nil
			}
			// The action is the transition, not a creation.
			//
			// It wrote `ActionIncidentCreated` for every lifecycle change, with the
			// from and to states in the state-hash fields. A reader filtering the
			// ledger on "incident created" saw N+1 creations for one incident, and
			// the rows that were not creations were indistinguishable from the one
			// that was. An append-only ledger is worth something precisely because
			// two different events are two different rows.
			_, err = s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, Actor: actor, ActorType: audit.ActorOperator,
				Action:     string(to),
				TargetType: "incident",
				TargetID:   incidentID,
				Reason:     reason,
				RequestID:  incidentID,
				SessionID:  actor,
				EvidenceRefs: []string{
					"from=" + string(from), "to=" + string(to),
				},
			})
			return err
		})
	return err
}

// lifecycleAllows enforces the transitions. Not every pair is legal: a closed
// incident is not reopened silently, because closing is what an operator signs.
func lifecycleAllows(from, to State) bool {
	switch from {
	case StateOpen:
		return to == StateInReview || to == StateDispositioned || to == StateClosed
	case StateInReview:
		return to == StateDispositioned || to == StateClosed || to == StateOpen
	case StateDispositioned:
		return to == StateClosed || to == StateInReview
	case StateClosed:
		return false
	}
	return false
}

// Disposition is an operator's answer. It never overwrites the observation: both
// are stored and both replay, so "AI said X and the operator said Y" is a fact
// rather than whichever happened to be written last.
type Disposition struct {
	TenantID   string
	IncidentID string
	Outcome    string
	ReasonCode string
	Notes      string
	// Agreement is the operator's assessment of the AI observation, which feeds
	// the evaluation harness. It is never fed back into the observation.
	Agreement  string
	ActorID    string
	ActorRoles []string
	SiteScope  []string
	SessionID  string
	// StepUpVerified records that the operator re-authenticated for a sensitive
	// disposition. Escalating or dismissing on someone else's session is a way to
	// launder a decision past a second pair of eyes.
	StepUpVerified bool
}

// DispositionOutcomes is the closed set.
var DispositionOutcomes = map[string]bool{
	"true_positive": true, "false_positive": true, "benign": true,
	"escalated": true, "acknowledged": true, "dismissed": true,
}

// stepUpRequired outcomes need a fresh factor, not just a live session.
var stepUpRequired = map[string]bool{"escalated": true, "dismissed": true}

// OutcomeNeedsStepUp reports whether an outcome requires a fresh second factor.
//
// Exported so a form can ask for the factor BEFORE the operator submits, rather than
// refusing afterwards. The rule is one map and two entries, and before this existed
// the only way to know it was to submit and read a 403 -- which is the answer an
// operator gets after they have already typed a six-digit code.
//
// The map is the same one `Record` checks, so the form and the service cannot
// disagree about which outcomes are sensitive. That matters: a form that asks for a
// factor the service does not require teaches operators to type their second factor
// on every disposition, and a form that does not ask for one the service requires
// refuses them at the worst moment.
func OutcomeNeedsStepUp(outcome string) bool { return stepUpRequired[outcome] }

// Record stores a disposition and moves the incident accordingly.
func (s *Service) Record(ctx context.Context, d Disposition) (string, error) {
	if !DispositionOutcomes[d.Outcome] {
		return "", fmt.Errorf("incidents: outcome %q is not recognised", d.Outcome)
	}
	if stepUpRequired[d.Outcome] && !d.StepUpVerified {
		return "", fmt.Errorf("incidents: %q requires a step-up MFA verification, not just a live session",
			d.Outcome)
	}
	if d.ActorID == "" {
		return "", errors.New("incidents: a disposition needs an actor")
	}

	id := fmt.Sprintf("disp_%d", time.Now().UnixNano())
	roles, _ := json.Marshal(d.ActorRoles)
	scope, _ := json.Marshal(d.SiteScope)

	// The observation the operator is responding to, if any. Read rather than
	// supplied, so a disposition cannot be pointed at an observation from another
	// incident.
	var observationID *string
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: d.TenantID, ActorID: d.ActorID},
		func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `
				SELECT observation_id FROM ai_observations
				 WHERE tenant_id = $1 AND incident_id = $2
				 ORDER BY created_at DESC LIMIT 1`,
				d.TenantID, d.IncidentID).Scan(&observationID); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("incidents: find observation: %w", err)
				}
				observationID = nil
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO dispositions (disposition_id, tenant_id, incident_id, actor_id, actor_roles,
				                          site_scope, outcome, reason_code, notes, agreement,
				                          related_observation_id, session_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				id, d.TenantID, d.IncidentID, d.ActorID, roles, scope,
				d.Outcome, d.ReasonCode, d.Notes, orDefault(d.Agreement, "unsure"),
				observationID, d.SessionID); err != nil {
				return fmt.Errorf("incidents: record disposition: %w", err)
			}

			if _, err := tx.Exec(ctx,
				`UPDATE incidents SET state = 'dispositioned' WHERE tenant_id = $1 AND incident_id = $2 AND state <> 'closed'`,
				d.TenantID, d.IncidentID); err != nil {
				return fmt.Errorf("incidents: advance state: %w", err)
			}

			if s.Audit == nil {
				return nil
			}
			_, err := s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: d.TenantID, Actor: d.ActorID, ActorType: audit.ActorOperator,
				Action: audit.ActionDispositionRecorded, TargetType: "incident", TargetID: d.IncidentID,
				Reason: d.ReasonCode, SessionID: d.SessionID,
			})
			return err
		})
	return id, err
}

func exclusionRecords(in []rules.Exclusion) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, e := range in {
		out = append(out, map[string]any{
			"deviceId": e.DeviceID, "driftMs": e.DriftMS, "reason": e.Reason,
		})
	}
	return out
}

func explain(d rules.Decision) string {
	parts := []string{fmt.Sprintf("Rule %s produced %s.", d.RuleID, d.State)}
	if len(d.SupportingEvents) > 0 {
		parts = append(parts, fmt.Sprintf("Supported by %d event(s): %v.",
			len(d.SupportingEvents), d.SupportingEvents))
	}
	if len(d.SupportingObservations) > 0 {
		parts = append(parts, fmt.Sprintf("AI observation(s) %v participated.", d.SupportingObservations))
	}
	if d.Degraded {
		parts = append(parts, fmt.Sprintf(
			"Degraded: the AI observation was %s, so the rule's declared behaviour applied and the "+
				"outcome is not fully corroborated.", d.DegradedReason))
	}
	if len(d.DriftExclusions) > 0 {
		parts = append(parts, fmt.Sprintf("%d device(s) were excluded from time-windowed "+
			"corroboration for clock drift.", len(d.DriftExclusions)))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return join(parts)
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
