// Package incidents is the case lifecycle and the decision-replay path.
//
// The replay path is the load-bearing part. A decision made months ago must be
// reproduced from stored facts and the stored AIObservation, never by re-running
// the model. Re-running the model would answer a different question — what the
// model says now — and the answer would move with every prompt tweak, every
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
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// State is an incident's lifecycle state.
type State string

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

		if _, err := tx.Exec(ctx, `
			INSERT INTO incidents (incident_id, tenant_id, primary_site_id, related_site_ids,
			                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
			                       state, severity, title, first_event_at, last_event_at,
			                       degraded, degraded_reasons)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'open',$9,$10,now(),now(),$11,$12)`,
			id, in.TenantID, in.SiteID, related,
			in.Decision.RuleID, in.RuleBundleVersion, in.BundleContentHash, in.PolicyVersion,
			orDefault(in.Decision.Severity, "medium"), orDefault(in.Title, in.Decision.RuleID),
			in.Decision.Degraded, degradedReasons); err != nil {
			return fmt.Errorf("incidents: insert: %w", err)
		}

		// The supporting events and the verification result are in the same
		// transaction as the incident. An incident whose justification is written
		// separately is an incident that can exist without one.
		for _, eventID := range in.Decision.SupportingEvents {
			role := "supporting"
			if eventID == first(in.SupportingEventIDs) {
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

func (s *Service) newID(tenant, site string, at time.Time) string {
	if s.IDGenerator != nil {
		return s.IDGenerator(tenant, site, at)
	}
	// Time-ordered and prefixed, so an operator reading a list of ids sees the
	// order without a query and two incidents cannot collide.
	return fmt.Sprintf("inc_%s_%d", site, at.UnixNano())
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
}

// List returns a tenant's incident feed, newest first.
func (s *Service) List(ctx context.Context, tenant, siteID string, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT incident_id, primary_site_id, state, severity, title, rule_id,
		       created_at, first_event_at, degraded, degraded_reasons,
		       rule_bundle_version, policy_version
		  FROM incidents
		 WHERE tenant_id = $1 AND ($2 = '' OR primary_site_id = $2)
		 ORDER BY created_at DESC
		 LIMIT $3`, tenant, siteID, limit)
	if err != nil {
		return nil, fmt.Errorf("incidents: list: %w", err)
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var item Summary
		var reasons []byte
		if err := rows.Scan(&item.IncidentID, &item.SiteID, &item.State, &item.Severity,
			&item.Title, &item.RuleID, &item.CreatedAt, &item.FirstEvent,
			&item.Degraded, &reasons, &item.BundleVersion, &item.PolicyVersion); err != nil {
			return nil, fmt.Errorf("incidents: scan: %w", err)
		}
		_ = json.Unmarshal(reasons, &item.DegradedReasons)
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
		 ORDER BY created_at`, tenant, incidentID)
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
		 ORDER BY created_at`, tenant, incidentID)
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
func (s *Service) SetState(ctx context.Context, tenant, incidentID string, to State, actor, reason string) error {
	from, err := s.currentState(ctx, tenant, incidentID)
	if err != nil {
		return err
	}
	if !lifecycleAllows(from, to) {
		return fmt.Errorf("incidents: %s cannot move from %s to %s", incidentID, from, to)
	}

	return s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: actor},
		func(tx pgx.Tx) error {
			closed := any(nil)
			if to == StateClosed {
				closed = time.Now().UTC()
			}
			if _, err := tx.Exec(ctx, `
				UPDATE incidents SET state = $3, closed_at = COALESCE($4, closed_at)
				 WHERE tenant_id = $1 AND incident_id = $2`,
				tenant, incidentID, string(to), closed); err != nil {
				return fmt.Errorf("incidents: set state: %w", err)
			}
			if s.Audit == nil {
				return nil
			}
			_, err := s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, Actor: actor, ActorType: audit.ActorOperator,
				Action: audit.ActionIncidentCreated, TargetType: "incident", TargetID: incidentID,
				PrevStateHash: string(from), ResultStateHash: string(to), Reason: reason,
			})
			return err
		})
}

func (s *Service) currentState(ctx context.Context, tenant, incidentID string) (State, error) {
	var st State
	err := s.Pool.QueryRow(ctx,
		`SELECT state FROM incidents WHERE tenant_id = $1 AND incident_id = $2`,
		tenant, incidentID).Scan(&st)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("incidents: current state: %w", err)
	}
	return st, nil
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

var _ = clock.BandOK
