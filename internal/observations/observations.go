// Package observations persists and validates AI observations.
//
// An observation is a historical fact: "this model, this prompt, said this, about
// evidence with these hashes, at this time". It is append-only, it is
// attributable, and it is never overwritten - a re-evaluation is a new row that
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
	"strconv"
	"strings"
	"sync/atomic"
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
	// general-monitoring is the profile the plan's "no rules to write per
	// scenario" claim rests on: one profile whose only structured claims are
	// "something departed from normal" and "a hazard is visible", with the
	// concept itself carried by the operator's question in the prompt.
	//
	// That is why its field set is so small. A profile with a field per concept
	// is a schema per concept, and a schema per concept is the rule engineering
	// this work set out to eliminate. What it does not have is per-concept
	// *certainty*: nothing here says who, what, or whether it was allowed, because
	// the model has never seen the site's access list and pretending otherwise
	// would be a fabrication with a boolean in it.
	//
	// Shadow, and deliberately: it has never been measured against a labelled set
	// of the 200 cases the plan requires, and a profile allowed to create
	// incidents before that is a profile whose false-positive rate is a guess.
	"general-monitoring": {
		ID: "general-monitoring", Shadow: true, Version: 1,
		Fields: map[string]bool{
			"insufficientEvidence": true, "summary": true, "sceneDescription": true,
			"confidence": true, "abnormalEvent": true, "hazardVisible": true,
			"entities": true, "recommendedAction": true,
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

	// Attribution. An observation without a model and a prompt version is not
	// explainable, and an unexplainable observation is one an operator cannot act
	// on.
	Model            string
	RuntimeVersion   string
	PromptTemplateID string
	PromptVersion    string
	// ModelHash is a content hash of the weights that answered.
	//
	// It is required, with exactly one exception, and the exception is not a
	// loosening: see [Observation.ModelIdentity]. A provider whose bundle this
	// product ships hashes, and its hash is the better answer. A hosted provider
	// that will not give us weights has no hash to record, and the alternatives
	// are both worse - leaving it empty and calling it required is a lie about the
	// schema, and writing a synthetic 64-character string is a lie about the
	// model. So the field is allowed to be empty when, and only when, the two
	// honest fields below are present.
	//
	// All three are WRITTEN, not merely validated. That distinction was the defect:
	// `Validate` accepted the hosted branch from the start and `insertSQL` never
	// mentioned the columns, so no row could actually take that branch without a
	// fabricated hash. See the comment on `insertSQL`.
	ModelHash            string
	ModelBundleSignature string

	// ModelIdentity is what the provider reported it used, as a claim.
	//
	// Together with ModelContractVersion it is the attribution for a provider
	// whose weights are not ours. "The API said hafnia-8b" plus "this build
	// asked model hafnia-8b with prompt version 3 against schema version 2" is a
	// complete and honest answer to "what produced this"; a fabricated hash
	// would be a complete and dishonest one.
	ModelIdentity string
	// ModelContractVersion is this build's content hash over the prompt
	// template, the prompt version, the output schema version and the API model
	// name. It is reproducible from the repository.
	ModelContractVersion string
	// Temperature is the sampling temperature that was requested. It is part of
	// attribution: an answer produced at 0.9 is not the answer produced at 0.01.
	Temperature float64

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
	case o.ModelHash == "" && (o.ModelIdentity == "" || o.ModelContractVersion == ""):
		// The one relaxation, and it is a swap rather than a loosening: a
		// provider that cannot be hashed must name itself and pin its contract
		// version instead. Filling in a hash to satisfy the first branch would
		// produce an observation that looks attributable and is not.
		return fmt.Errorf("%w: model hash is required unless the provider is named and pinned: "+
			"a model name is not an identity, and neither is a fabricated hash", ErrInvalid)
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
	case o.Degraded != DegradationNone && strings.TrimSpace(o.DegradedDetail) == "":
		// The fail-closed rule, at the last point before the row can exist.
		//
		// A degraded observation with no reason is the shape that turns into a
		// fabricated negative: the flag says something went wrong, a consumer
		// that only checks the flag records "degraded" and moves on, and a
		// consumer that branches on the reason gets the empty string and no
		// branch. Either way the reason - the only thing that says *the model was
		// never asked*, as opposed to *the model was asked and failed* - is gone
		// before anybody can see it.
		//
		// So it is refused here rather than tolerated. This is the plan's P4.3
		// red-check: with the reason removed, the row must not be writable.
		return fmt.Errorf("%w: a degraded observation must carry its reason; without it a reader "+
			"cannot tell 'the model was never asked' from 'the model answered and the answer was unusable', "+
			"which is the difference between an absence and a negative", ErrInvalid)
	}
	if o.Degraded == DegradationNone && strings.TrimSpace(o.DegradedDetail) != "" {
		// The other half, and the mirror image. A degradation detail on an
		// observation that claims to be fine is either a field that was not
		// cleared or a reason that was attached to the wrong row, and both are
		// facts about this record rather than about the run.
		return fmt.Errorf("%w: a non-degraded observation carries a degradation detail (%q); "+
			"either a field was not cleared or a reason was attached to the wrong row",
			ErrInvalid, o.DegradedDetail)
	}
	if o.Degraded == DegradationNone && o.StructuredOutput == nil {
		return fmt.Errorf("%w: a non-degraded observation must carry its output", ErrInvalid)
	}
	return nil
}

// ApplyDegradedPlaceholders fills the attribution columns a degraded observation
// still has to carry.
//
// The columns are NOT NULL and Validate requires them, because an observation
// nobody can attribute cannot be replayed or audited. A degradation genuinely
// has no model, so the values are documented placeholders rather than invented
// ones: "none" and the degradation reason say plainly that nothing ran, and any
// reader can tell the difference between that and a real model name.
//
// It lives here rather than in the caller that first needed it, because the
// schema's requirements are this package's and two copies of the placeholder
// vocabulary would drift into two different sets of values for the same fact.
func ApplyDegradedPlaceholders(o *Observation) {
	if o.Model == "" {
		o.Model = "none"
	}
	if o.ModelHash == "" {
		// 64 hex characters, the shape every other hash in the schema is, so the
		// column's own constraint accepts it and a reader's tooling does not have
		// to special-case this row.
		o.ModelHash = "0000000000000000000000000000000000000000000000000000000000000000"
	}
	if o.RuntimeVersion == "" {
		o.RuntimeVersion = "airuntime/0"
	}
	if o.PromptTemplateID == "" {
		o.PromptTemplateID = "none"
	}
	if o.PromptVersion == "" {
		o.PromptVersion = "none"
	}
	// The schema requires a positive output schema version, and a degraded run
	// produced no output to version. One is the smallest honest value: it is the
	// first shape, and it declares nothing.
	if o.OutputSchemaVersion <= 0 {
		o.OutputSchemaVersion = 1
	}
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
	// RandRead produces observation id bytes, and defaults to crypto/rand.Read.
	// It is injectable so a test can drive the non-random fallback, which is the
	// path whose uniqueness is a comment's claim and would otherwise never run.
	RandRead func(b []byte) (int, error)
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
	model, model_hash, model_identity, model_contract_version,
	runtime_version, prompt_template_id, prompt_version, model_bundle_signature,
	input_evidence_ids, input_evidence_hashes, output_schema_version, structured_output,
	insufficient_evidence, degraded_reason, degraded_detail, latency_ms, hardware,
	output_hash, created_at, supersedes_observation_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
RETURNING observation_id`

// The two columns `model_identity` and `model_contract_version` were absent from
// this statement until the seeded demo hit `ai_observations_attribution_ck`.
//
// That is the whole hosted-provider branch migration 0022 exists to open, and its
// own header says the Go layer "is already verified against this shape". It was —
// `Validate` accepts it, and the field comments describe it in detail — but the
// INSERT never mentioned the columns, so the second disjunct of the constraint was
// unsatisfiable in practice and every row had to carry a `model_hash`.
//
// Which is not a stylistic problem. It means a deployment with a hosted model, the
// one configuration the column was added for, could not record a single
// observation without writing the synthetic 64-character hash that
// `model_hash`'s own comment says would "defeat the one thing the column exists
// for, which is proving which weights answered". The alternative was to accept a
// database that would not take an honest row.
//
// The order of $10 and $11 matters only in that it has to match the statement; the
// values are passed in the same order in `Record`.

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

	// The defect this ordering encoded: newID was called first, and newID's
	// fallback hashed o.CreatedAt, which at that point was always the zero time.
	// So every degraded or failed observation for one (tenant, event, prompt
	// version) derived the same id and the second insert failed on the primary
	// key - two model outages on the same event, and the second one lost.
	// CreatedAt is a fact of the row, and the identity is derived from facts, so
	// the fact has to exist first.
	if o.CreatedAt.IsZero() {
		o.CreatedAt = w.now()
	}
	if o.ObservationID == "" {
		o.ObservationID = w.newID(o)
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
			o.Model, o.ModelHash, o.ModelIdentity, o.ModelContractVersion,
			o.RuntimeVersion, o.PromptTemplateID, o.PromptVersion,
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

// MarkDegraded shapes o as a degraded observation: the reason, the detail, an
// empty output, and insufficient_evidence left true.
//
// It is separate from the write because the shape is a fact about the run, not
// about the database, and anything else that records a degradation - a soak
// report, an incident artefact, a test - has to produce the same row. Two
// hand-built versions of this shape is how a degraded observation comes to mean
// "insufficient evidence: false" in one path and true in the other, and a flag
// that is sometimes false is a negative answer waiting to happen.
func (o *Observation) MarkDegraded(reason Degradation, detail string) {
	o.Degraded = reason
	o.DegradedDetail = detail
	o.StructuredOutput = map[string]any{}
	// A degraded observation is not a "no". The insufficient-evidence flag is
	// left true so that any consumer which forgets to check the degradation still
	// fails closed rather than treating an empty output as a negative answer.
	o.InsufficientEvidence = true
}

// RecordDegraded persists an observation recording that the AI plane could not
// answer.
//
// It is a real row, not a log line. "The model was down at 03:14" is a fact a
// pilot needs six weeks later when somebody asks why the incident count rose,
// and a log line is not where anybody looks for that.
func (w *Writer) RecordDegraded(ctx context.Context, o Observation, reason Degradation, detail string) (Observation, error) {
	o.MarkDegraded(reason, detail)
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
	// to be part of the identity - which is worse, because a golden test then
	// cannot pin it.
	//
	// The content hash is what identifies an observation's meaning; the id
	// identifies the run.
	var buf [16]byte
	if _, err := w.randRead(buf[:]); err != nil {
		// A failure to read randomness is a failure to be able to distinguish two
		// runs, and a fallback that is not unique would silently lose one.
		//
		// The sequence number is what makes it unique per call. Without it this
		// was sha256(tenant, event, CreatedAt, promptVersion), and CreatedAt - a
		// clock, in a test a frozen one - is the only thing that varied, so two
		// runs in the same clock tick derived the same id and the second insert
		// failed on the primary key. The clock still contributes; the counter is
		// what guarantees the comment above is true rather than usually true.
		seq := fallbackSeq.Add(1)
		sum := sha256.Sum256([]byte(o.TenantID + "\x00" + o.EventID + "\x00" +
			o.PromptVersion + "\x00" + o.CreatedAt.UTC().Format(time.RFC3339Nano) +
			"\x00" + strconv.FormatUint(seq, 10)))
		return "obs_" + hex.EncodeToString(sum[:12])
	}
	return "obs_" + hex.EncodeToString(buf[:])
}

// fallbackSeq counts the ids issued by the non-random path. It is a package
// counter rather than a field on Writer because a failure to read randomness is
// a process-wide condition, not a per-writer one, and two writers in one process
// must not be able to derive the same id as each other.
var fallbackSeq atomic.Uint64

// randRead is crypto/rand.Read, behind a method so a test can force the
// fallback path. The fallback is otherwise unreachable, and a code path that
// cannot be reached is a code path that is not tested.
func (w *Writer) randRead(b []byte) (int, error) {
	if w.RandRead != nil {
		return w.RandRead(b)
	}
	return rand.Read(b)
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
	err := w.Pool.For(tenant, "observations").QueryRow(ctx, `
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
	err := w.Pool.For(tenant, "observations").QueryRow(ctx, `
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
	rows, err := w.Pool.For(tenant, "observations").Query(ctx, `
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

	rows, err := w.Pool.For(tenant, "observations").Query(ctx, `
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
