// Package observations persists and validates AI observations.
//
// An observation is a historical fact: "this model, this prompt, said this, about
// evidence with these hashes, at this time". It is append-only, it is
// attributable, and it is never overwritten — a re-evaluation is a new row that
// names the one it supersedes, so the delta between them is measurable and a
// decision that cited the earlier one still replays.
//
// The three absences are kept apart throughout, because the whole product
// invariant rests on the distinction:
//
//	no observation        the AI plane was never asked
//	degraded observation  it was asked and could not answer
//	insufficient evidence the model answered "I cannot tell"
//
// None of them is a negative answer, and none of them may satisfy a rule that
// required an observation.
package observations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// Degradation names why an observation is not evidence.
type Degradation string

// Degradation reasons. The same closed set the schema's CHECK allows: an
// unrecognised value would be stored as a string nothing reads, and interpreted
// downstream as no degradation at all.
const (
	DegradationNone             Degradation = "none"
	DegradationModelUnavailable Degradation = "model_unavailable"
	DegradationNoRuntime        Degradation = "no_runtime"
	DegradationTimeout          Degradation = "timeout"
	DegradationError            Degradation = "error"
	DegradationAbsent           Degradation = "absent"
)

// Degradations is the closed set, matching the schema's CHECK so an unknown value
// cannot be stored by a caller that skipped this package.
var Degradations = map[Degradation]bool{
	DegradationNone: true, DegradationModelUnavailable: true, DegradationNoRuntime: true,
	DegradationTimeout: true, DegradationError: true, DegradationAbsent: true,
}

// Profile is a set of observation shapes.
//
// M1 has one production profile and two shadow profiles. A shadow profile
// produces an observation and creates no incident and suppresses nothing, which
// is what makes a failing profile safe to try against live traffic.
type Profile struct {
	ID      string
	Shadow  bool
	Version int
	// Fields is the top-level field set of the validated output. A rule may name
	// a rule's aiObservation.field only if it is here, checked at bundle compile
	// time.
	Fields map[string]bool
}

// M1Profiles is the shipped profile set.
var M1Profiles = map[string]Profile{
	"restricted-person": {
		ID: "restricted-person", Version: 1,
		Fields: map[string]bool{
			"insufficientEvidence": true, "persons": true, "accessState": true,
			"restrictedZoneBreach": true, "afterHours": true, "summary": true,
			"confidence": true,
		},
	},
	"door-forced": {
		ID: "door-forced", Shadow: true, Version: 1,
		Fields: map[string]bool{
			"insufficientEvidence": true, "doorState": true, "personPresent": true,
			"obstruction": true, "openDurationSeconds": true, "summary": true,
			"confidence": true,
		},
	},
	"vehicle-at-gate": {
		ID: "vehicle-at-gate", Shadow: true, Version: 1,
		Fields: map[string]bool{
			"insufficientEvidence": true, "vehicleState": true, "vehicleCount": true,
			"plateLegible": true, "plateText": true, "gateState": true,
			"summary": true, "confidence": true,
		},
	},
}

// IsProductionProfile reports whether a profile may affect authoritative state.
func IsProductionProfile(id string) bool {
	p, ok := M1Profiles[id]
	return ok && !p.Shadow
}

// Observation is one record to persist.
type Observation struct {
	ObservationID string
	TenantID      string
	SiteID        string
	IncidentID    string
	EventID       string
	ProfileID     string

	// Attribution. Every field is mandatory, and the schema enforces it. An
	// observation without a model and a prompt version is not explainable, and an
	// unexplainable observation is one an operator cannot act on.
	Model                string
	ModelHash            string
	RuntimeVersion       string
	PromptTemplateID     string
	PromptVersion        string
	ModelBundleSignature string

	InputEvidenceIDs    []string
	InputEvidenceHashes []string

	OutputSchemaVersion int
	// StructuredOutput is the validated output. It is never model prose: a rule
	// that could match wording would change behaviour when the prompt is
	// rephrased, and an incident that appears or disappears because a prompt was
	// edited is not an incident anyone can defend.
	StructuredOutput     map[string]any
	InsufficientEvidence bool
	Degraded             Degradation
	DegradedDetail       string

	LatencyMS int64
	Hardware  string
	// Supersedes names the observation this one replaces. Nil for a first
	// evaluation, and the historical row is retained either way.
	SupersedesObservationID string
	CreatedAt               time.Time
}

// ErrInvalid is returned for an observation that must not be stored.
var ErrInvalid = errors.New("observations: invalid observation")

// Validate enforces the minimum an auditor needs in order to read the row six
// months later with no other table to consult.
func (o *Observation) Validate() error {
	// An empty degradation is the healthy case, and requiring every caller to
	// spell it out is how a real observation ends up carrying "because" instead.
	// An unrecognised non-empty value is still refused, because it would read as
	// no degradation to everything downstream.
	if o.Degraded == "" {
		o.Degraded = DegradationNone
	}
	switch {
	case o.TenantID == "":
		return fmt.Errorf("%w: tenant id is required and is never read from a request body", ErrInvalid)
	case o.SiteID == "":
		return fmt.Errorf("%w: site id is required", ErrInvalid)
	case o.IncidentID == "":
		return fmt.Errorf("%w: incident id is required", ErrInvalid)
	case o.EventID == "":
		return fmt.Errorf("%w: event id is required", ErrInvalid)
	case o.ProfileID == "":
		return fmt.Errorf("%w: profile id is required", ErrInvalid)
	case o.Model == "":
		return fmt.Errorf("%w: model is required; an observation without one cannot be attributed", ErrInvalid)
	case o.ModelHash == "":
		return fmt.Errorf("%w: model hash is required; a model name is not an identity", ErrInvalid)
	case o.RuntimeVersion == "":
		return fmt.Errorf("%w: runtime version is required", ErrInvalid)
	case o.PromptTemplateID == "":
		return fmt.Errorf("%w: prompt template id is required", ErrInvalid)
	case o.PromptVersion == "":
		return fmt.Errorf("%w: prompt version is required; the same model with a different "+
			"prompt is a different thing", ErrInvalid)
	case o.OutputSchemaVersion <= 0:
		return fmt.Errorf("%w: output schema version is required", ErrInvalid)
	case !Degradations[o.Degraded]:
		return fmt.Errorf("%w: unknown degradation %q; a value this package does not "+
			"recognise would read as no degradation downstream", ErrInvalid, o.Degraded)
	}
	if o.Degraded == DegradationNone && o.StructuredOutput == nil {
		return fmt.Errorf("%w: a non-degraded observation must carry its output", ErrInvalid)
	}
	return nil
}

// OutputHash is the SHA-256 over the canonical serialization of the output.
//
// It is what makes "the model said this" checkable: a months later somebody can
// recompute the hash from the stored output and compare it to the stored hash,
// and a mismatch means the output was edited after the fact.
func (o *Observation) OutputHash() (string, error) {
	body, err := json.Marshal(o.StructuredOutput)
	if err != nil {
		return "", fmt.Errorf("observations: encode output: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// InputHash is the SHA-256 over the input evidence hashes, which is what ties an
// observation to the exact bytes it was shown.
func (o *Observation) InputHash() (string, error) {
	h := sha256.New()
	for _, id := range o.InputEvidenceIDs {
		_, _ = fmt.Fprintf(h, "id:%d:%s", len(id), id)
	}
	for _, hash := range o.InputEvidenceHashes {
		_, _ = fmt.Fprintf(h, "hash:%d:%s", len(hash), hash)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Writer persists observations.
type Writer struct {
	Pool *dbx.Pool
	// IDGenerator produces observation ids. Injectable so a golden test is stable.
	IDGenerator func(tenant, profile, event string) string
	// Now is injectable so a golden test is stable.
	Now func() time.Time
	// Profiles is the profile set this build enforces. Defaults to M1Profiles.
	Profiles map[string]Profile
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Writer) profiles() map[string]Profile {
	if w.Profiles != nil {
		return w.Profiles
	}
	return M1Profiles
}

const insertSQL = `
INSERT INTO ai_observations (
	observation_id, tenant_id, site_id, incident_id, event_id, profile_id, production_profile,
	model, model_hash, runtime_version, prompt_template_id, prompt_version, model_bundle_signature,
	input_evidence_ids, input_evidence_hashes, output_schema_version, structured_output,
	insufficient_evidence, degraded_reason, degraded_detail, latency_ms, hardware,
	output_hash, created_at, supersedes_observation_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
RETURNING observation_id`

// Record persists one observation.
//
// It validates before it writes, and it writes inside the caller's transaction so
// the observation and whatever state change referenced it commit together.
func (w *Writer) Record(ctx context.Context, o Observation) (Observation, error) {
	if err := o.Validate(); err != nil {
		return o, err
	}
	profile, known := w.profiles()[o.ProfileID]
	if !known {
		return o, fmt.Errorf("%w: profile %q is not in this build's profile set; a rule "+
			"could name a field nobody emits", ErrInvalid, o.ProfileID)
	}
	if err := validateAgainstProfile(o.StructuredOutput, profile); err != nil {
		return o, err
	}

	if o.ObservationID == "" {
		o.ObservationID = w.newID(o)
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = w.now()
	}

	ids, _ := json.Marshal(orEmptyList(o.InputEvidenceIDs))
	hashes, _ := json.Marshal(orEmptyList(o.InputEvidenceHashes))
	output, err := json.Marshal(orEmptyMap(o.StructuredOutput))
	if err != nil {
		return o, fmt.Errorf("observations: encode output: %w", err)
	}
	hash, err := o.OutputHash()
	if err != nil {
		return o, err
	}

	err = w.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: o.TenantID,
		// The AI plane is a distinct actor on the ledger, because "the model said
		// this" and "the system decided this" are different facts and a reader
		// must be able to tell them apart.
		ActorID: "ai_runtime",
	}, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, insertSQL,
			o.ObservationID, o.TenantID, o.SiteID, o.IncidentID, o.EventID, o.ProfileID,
			!profile.Shadow,
			o.Model, o.ModelHash, o.RuntimeVersion, o.PromptTemplateID, o.PromptVersion,
			o.ModelBundleSignature, ids, hashes,
			o.OutputSchemaVersion, output,
			o.InsufficientEvidence, string(o.Degraded), o.DegradedDetail,
			o.LatencyMS, o.Hardware, hash, o.CreatedAt, nullString(o.SupersedesObservationID),
		).Scan(&id); err != nil {
			return fmt.Errorf("observations: record: %w", err)
		}
		o.ObservationID = id
		return nil
	})
	return o, err
}

// RecordDegraded persists an observation recording that the AI plane could not
// answer.
//
// It is a real row, not a log line. "The model was down at 03:14" is a fact a
// pilot needs six weeks later when somebody asks why the incident count rose,
// and a log line is not where anybody looks for that.
func (w *Writer) RecordDegraded(ctx context.Context, o Observation, reason Degradation, detail string) (Observation, error) {
	o.Degraded = reason
	o.DegradedDetail = detail
	o.StructuredOutput = map[string]any{}
	// A degraded observation is not a "no". The insufficient-evidence flag is
	// left true so that any consumer which forgets to check the degradation still
	// fails closed rather than treating an empty output as a negative answer.
	o.InsufficientEvidence = true
	return w.Record(ctx, o)
}

func (w *Writer) newID(o Observation) string {
	if w.IDGenerator != nil {
		return w.IDGenerator(o.TenantID, o.ProfileID, o.EventID)
	}
	// A random id, deliberately, and the opposite of how canonical events get
	// theirs.
	//
	// A canonical event is a fact being recorded once, so deriving its id from
	// its scoped key is what makes a crash retry land on the same row. An
	// observation is a *run* of a model over evidence. Two runs of the same model
	// on the same event are two observations, and the whole point of supersession
	// is that both are kept. A derived id would collide them, or force the clock
	// to be part of the identity — which is worse, because a golden test then
	// cannot pin it.
	//
	// The content hash is what identifies an observation's meaning; the id
	// identifies the run.
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A failure to read randomness is a failure to be able to distinguish two
		// runs, and a fallback that is not unique would silently lose one.
		sum := sha256.Sum256([]byte(o.TenantID + "\x00" + o.EventID +
			"\x00" + o.CreatedAt.Format(time.RFC3339Nano) + "\x00" + o.PromptVersion))
		return "obs_" + hex.EncodeToString(sum[:12])
	}
	return "obs_" + hex.EncodeToString(buf[:])
}

// validateAgainstProfile refuses a field the profile does not declare.
//
// The published JSON Schema for a profile is the contract, and this is the
// enforcement of the half of it the engine can check without pulling in a full
// validator: every top-level key in the output must be one the profile declares.
func validateAgainstProfile(output map[string]any, profile Profile) error {
	if output == nil {
		return nil
	}
	unknown := make([]string, 0)
	for key := range output {
		if !profile.Fields[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// Sorted so a diagnostic is stable and diffable.
	sortStrings(unknown)
	return fmt.Errorf("%w: profile %s emitted field(s) %s that its schema does not declare; "+
		"an undeclared field is one nothing validates",
		ErrInvalid, profile.ID, strings.Join(unknown, ", "))
}

// Get reads one observation.
func (w *Writer) Get(ctx context.Context, tenant, observationID string) (Observation, error) {
	var o Observation
	var ids, hashes, output []byte
	var degraded string
	var supersedes *string
	err := w.Pool.QueryRow(ctx, `
		SELECT observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		       model, model_hash, runtime_version, prompt_template_id, prompt_version,
		       COALESCE(model_bundle_signature, ''), input_evidence_ids, input_evidence_hashes,
		       output_schema_version, structured_output, insufficient_evidence, degraded_reason,
		       degraded_detail, latency_ms, hardware, created_at,
		       COALESCE(supersedes_observation_id, '')
		  FROM ai_observations WHERE tenant_id = $1 AND observation_id = $2`,
		tenant, observationID).Scan(
		&o.ObservationID, &o.TenantID, &o.SiteID, &o.IncidentID, &o.EventID, &o.ProfileID,
		&o.Model, &o.ModelHash, &o.RuntimeVersion, &o.PromptTemplateID, &o.PromptVersion,
		&o.ModelBundleSignature, &ids, &hashes,
		&o.OutputSchemaVersion, &output, &o.InsufficientEvidence, &degraded,
		&o.DegradedDetail, &o.LatencyMS, &o.Hardware, &o.CreatedAt, &supersedes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return o, fmt.Errorf("observations: no observation %q", observationID)
		}
		return o, fmt.Errorf("observations: get: %w", err)
	}
	_ = json.Unmarshal(ids, &o.InputEvidenceIDs)
	_ = json.Unmarshal(hashes, &o.InputEvidenceHashes)
	_ = json.Unmarshal(output, &o.StructuredOutput)
	o.Degraded = Degradation(degraded)
	if supersedes != nil {
		o.SupersedesObservationID = *supersedes
	}
	return o, nil
}

// LatestForIncident reads the most recent observation for an incident, which is
// the one a decision cited when the verification did not name one.
func (w *Writer) LatestForIncident(ctx context.Context, tenant, incidentID string) (*Observation, error) {
	var id string
	err := w.Pool.QueryRow(ctx, `
		SELECT observation_id FROM ai_observations
		 WHERE tenant_id = $1 AND incident_id = $2
		 ORDER BY created_at DESC LIMIT 1`, tenant, incidentID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("observations: latest: %w", err)
	}
	o, err := w.Get(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// SupersededBy returns the observations that replaced this one.
//
// A re-evaluation is a new row that names the one it supersedes, so the chain is
// walkable and the delta between an observation and its replacement is a
// query rather than a reconstruction.
func (w *Writer) SupersededBy(ctx context.Context, tenant, observationID string) ([]Observation, error) {
	rows, err := w.Pool.Query(ctx, `
		SELECT observation_id FROM ai_observations
		 WHERE tenant_id = $1 AND supersedes_observation_id = $2
		 ORDER BY created_at`, tenant, observationID)
	if err != nil {
		return nil, fmt.Errorf("observations: superseded: %w", err)
	}
	defer rows.Close()

	var out []Observation
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("observations: scan superseded: %w", err)
		}
		o, err := w.Get(ctx, tenant, id)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Stats is the AI-plane health summary a pilot reads.
//
// DegradedCount is the number that matters. A pilot claiming the AI plane reduced
// operator effort has to be able to say how often it was actually available, and
// this is where that number comes from.
type Stats struct {
	Total            int64
	Insufficient     int64
	Degraded         int64
	DegradedByReason map[string]int64
	ByProfile        map[string]int64
}

// Availability is the fraction of observations that were real inferences, and it
// is the number a pilot is asked for.
func (s Stats) Availability() float64 {
	if s.Total == 0 {
		return 0
	}
	usable := s.Total - s.Degraded
	return float64(usable) / float64(s.Total)
}

// Stats reads the observation health summary for a window.
func (w *Writer) Stats(ctx context.Context, tenant string, since time.Time) (Stats, error) {
	var s Stats
	s.DegradedByReason = map[string]int64{}
	s.ByProfile = map[string]int64{}

	rows, err := w.Pool.Query(ctx, `
		SELECT profile_id, degraded_reason, insufficient_evidence, count(*)
		  FROM ai_observations
		 WHERE tenant_id = $1 AND created_at >= $2
		 GROUP BY profile_id, degraded_reason, insufficient_evidence`,
		tenant, since)
	if err != nil {
		return s, fmt.Errorf("observations: stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var profile, reason string
		var insufficient bool
		var n int64
		if err := rows.Scan(&profile, &reason, &insufficient, &n); err != nil {
			return s, fmt.Errorf("observations: scan stats: %w", err)
		}
		s.Total += n
		s.ByProfile[profile] += n
		if Degradation(reason) != DegradationNone {
			s.Degraded += n
			s.DegradedByReason[reason] += n
		}
		if insufficient && Degradation(reason) == DegradationNone {
			// The model answered "I cannot tell". That is not a negative answer
			// and it is not an availability failure either, so it is counted
			// separately rather than folded into either.
			s.Insufficient += n
		}
	}
	return s, rows.Err()
}

func orEmptyList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
