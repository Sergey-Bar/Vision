package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
)

// Observation is the stored attribution record for one answer about one clip.
//
// It is the store's own struct rather than the client's, on purpose: the wire
// shape and the durable shape are different concerns, and a record that is
// literally the in-memory struct is a record whose layout a refactor can change
// without anyone deciding to.
type Observation struct {
	DetectionID          string
	RunID                string
	PromptID             string
	PromptVersion        string
	AIState              string
	Verdict              string
	Summary              string
	Confidence           float64
	InsufficientEvidence bool
	// ModelIdentity is what the API SAID. It is a claim by the provider and is
	// stored as one: there is no model hash beside it and there will not be one.
	ModelIdentity   string
	ContractVersion string
	Temperature     float64
	ReasonCode      string
	Detail          string
	OutputHash      string
	// InputHashes is the sorted `(id, hash)` list of the evidence the answer was
	// about. Sorted at write time so the value is stable for two runs over the
	// same clip.
	InputHashes      string
	LatencyMS        int64
	PromptTokens     int
	CompletionTokens int
	AssetID          string
	CreatedAt        time.Time
}

// InputHashesOf builds the sorted `(id, hash)` list for a set of evidence refs.
//
// Sorted because the field is a fingerprint of WHICH evidence was sent, and an
// unsorted list would hash differently for the same set — which would make two
// identical runs look like two different ones.
func InputHashesOf(refs map[string]string) string {
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+refs[k])
	}
	return strings.Join(parts, " ")
}

// PutObservation writes the attribution record.
//
// It refuses an observation whose detection does not exist, because an answer
// about nothing is an answer nobody can attach to a proof chain, and it refuses
// to store a verdict on a non-answered ask. That second refusal is the durable
// half of RC-1: the rule is enforced at parse time AND at write time, because a
// second writer that forgot the first would put a fabricated negative into the
// one table a proof chain reads.
func (s *Store) PutObservation(ctx context.Context, o Observation) error {
	if o.DetectionID == "" || o.RunID == "" {
		return errors.New("store: an observation needs a detection and a run; an unattributable answer cannot be audited")
	}
	o.AIState = strings.TrimSpace(o.AIState)
	if o.AIState == "" {
		return errors.New("store: an observation needs an ai state; a record that does not say whether the model was asked " +
			"is the fabricated negative this whole table exists to prevent")
	}
	// REDCHECK RC-1, durable half: nothing but an answered ask may carry a
	// verdict.
	if o.AIState != aistate.Asked && vlm.Verdict(o.Verdict) != vlm.VerdictNone {
		return fmt.Errorf("store: refusing to record the verdict %q against the ask state %q; a verdict without an answered "+
			"ask is a fabricated negative and this table is the one a proof chain reads", o.Verdict, o.AIState)
	}
	if !vlm.Verdict(o.Verdict).Valid() {
		return fmt.Errorf("store: %q is not in the closed verdict set", o.Verdict)
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	o.CreatedAt = o.CreatedAt.UTC()
	_, err := s.exec(ctx, `
		INSERT INTO vlm_observations (
			detection_id,run_id,prompt_id,prompt_version,aistate,verdict,summary,confidence,
			insufficient_evidence,model_identity,contract_version,temperature,reason_code,detail,
			output_hash,input_hashes,latency_ms,prompt_tokens,completion_tokens,asset_id,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(detection_id) DO UPDATE SET
			aistate=excluded.aistate, verdict=excluded.verdict, summary=excluded.summary,
			confidence=excluded.confidence, insufficient_evidence=excluded.insufficient_evidence,
			model_identity=excluded.model_identity, contract_version=excluded.contract_version,
			temperature=excluded.temperature, reason_code=excluded.reason_code, detail=excluded.detail,
			output_hash=excluded.output_hash, input_hashes=excluded.input_hashes,
			latency_ms=excluded.latency_ms, prompt_tokens=excluded.prompt_tokens,
			completion_tokens=excluded.completion_tokens, asset_id=excluded.asset_id`,
		o.DetectionID, o.RunID, o.PromptID, o.PromptVersion, o.AIState, o.Verdict, o.Summary, o.Confidence,
		boolInt(o.InsufficientEvidence), o.ModelIdentity, o.ContractVersion, o.Temperature, o.ReasonCode, o.Detail,
		o.OutputHash, o.InputHashes, o.LatencyMS, o.PromptTokens, o.CompletionTokens, o.AssetID, nowUTC(o.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: cannot record the observation for %s: %w", o.DetectionID, err)
	}
	return nil
}

// ObservationFor reads one attribution record.
func (s *Store) ObservationFor(ctx context.Context, detectionID string) (*Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT detection_id,run_id,prompt_id,prompt_version,aistate,verdict,summary,confidence,
		       insufficient_evidence,model_identity,contract_version,temperature,reason_code,detail,
		       output_hash,input_hashes,latency_ms,prompt_tokens,completion_tokens,asset_id,created_at
		FROM vlm_observations WHERE detection_id=?`, detectionID)
	o, err := scanObservation(row)
	if err != nil {
		return nil, err
	}
	return o, nil
}

// Observations lists a run's attribution records in sequence order.
func (s *Store) Observations(ctx context.Context, runID string) ([]Observation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT detection_id,run_id,prompt_id,prompt_version,aistate,verdict,summary,confidence,
		       insufficient_evidence,model_identity,contract_version,temperature,reason_code,detail,
		       output_hash,input_hashes,latency_ms,prompt_tokens,completion_tokens,asset_id,created_at
		FROM vlm_observations WHERE run_id=? ORDER BY created_at, detection_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the observations for %s: %w", runID, err)
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		o, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// ObservationCounts is the shape a report needs: how many observations carried
// each verdict, and how many carried no verdict at all.
//
// NoVerdict is reported separately from a zero in any bucket, because "we asked
// nothing" and "every answer was needs_review" are different facts about a run.
type ObservationCounts struct {
	Confirmed     int
	FalsePositive int
	NeedsReview   int
	None          int
	Asked         int
	Absent        int
	Total         int
}

// ObservationTally counts a run's verdicts.
func (s *Store) ObservationTally(ctx context.Context, runID string) (ObservationCounts, error) {
	var c ObservationCounts
	rows, err := s.db.QueryContext(ctx, `SELECT aistate, verdict FROM vlm_observations WHERE run_id=?`, runID)
	if err != nil {
		return c, fmt.Errorf("store: cannot tally the observations for %s: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var state, verdict string
		if err := rows.Scan(&state, &verdict); err != nil {
			return c, err
		}
		c.Total++
		if state == aistate.Asked {
			c.Asked++
		} else {
			c.Absent++
		}
		switch vlm.Verdict(verdict) {
		case vlm.VerdictConfirmed:
			c.Confirmed++
		case vlm.VerdictFalsePositive:
			c.FalsePositive++
		case vlm.VerdictNeedsReview:
			c.NeedsReview++
		default:
			c.None++
		}
	}
	return c, rows.Err()
}

// LatestObservation returns a run's most recent attribution record.
//
// The screen reads this rather than the runner remembering one in a field: the
// store is the thing a proof chain reads, and a screen that can show something
// the store does not hold is a screen that can lie.
func (s *Store) LatestObservation(ctx context.Context, runID string) (*Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT detection_id,run_id,prompt_id,prompt_version,aistate,verdict,summary,confidence,
		       insufficient_evidence,model_identity,contract_version,temperature,reason_code,detail,
		       output_hash,input_hashes,latency_ms,prompt_tokens,completion_tokens,asset_id,created_at
		FROM vlm_observations WHERE run_id=? ORDER BY created_at DESC, detection_id DESC LIMIT 1`, runID)
	o, err := scanObservation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}

// PushResultFor reads what the VMS said about a detection's push.
//
// A detection has both an event row and a bookmark row, and the EVENT's result is
// the one that answers "did the VMS accept this", so the kind is a parameter
// rather than an assumption. Two rows and one answer would be a lie in whichever
// direction it happened to fall.
func (s *Store) PushResultFor(ctx context.Context, detectionID string) (result, detail string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT result, detail FROM outbox WHERE detection_id=? AND kind=? ORDER BY id LIMIT 1`,
		detectionID, KindEvent).Scan(&result, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		// No event row yet. That is `push pending`, not a failure, and the
		// caller decides which word to print.
		return "", "", nil
	}
	return result, detail, err
}

func scanObservation(row interface{ Scan(...any) error }) (*Observation, error) {
	var o Observation
	var insufficient bool
	var created string
	if err := row.Scan(&o.DetectionID, &o.RunID, &o.PromptID, &o.PromptVersion, &o.AIState, &o.Verdict,
		&o.Summary, &o.Confidence, &insufficient, &o.ModelIdentity, &o.ContractVersion, &o.Temperature,
		&o.ReasonCode, &o.Detail, &o.OutputHash, &o.InputHashes, &o.LatencyMS, &o.PromptTokens,
		&o.CompletionTokens, &o.AssetID, &created); err != nil {
		return nil, fmt.Errorf("store: cannot read an observation: %w", err)
	}
	o.InsufficientEvidence = insufficient
	o.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &o, nil
}

// The wire spellings this package filters on, as literals.
//
// internal/store must not import internal/vlm: the store is below the pipeline and
// knows about rows, and a row validator that imports the package that produces the
// values is a package that cannot be read on its own.
//
// The AI STATES are not literals and never were. `internal/aistate` is the
// vocabulary package both `internal/ai` and `internal/detect` already share, and it
// imports nothing — so `pocctl search` and `pocctl brief` take the four absence
// states from there rather than declaring a fifth copy. A copy is a drift waiting to
// happen, and a drift in these words is not a compile error: it is a brief that
// reports zero confirmations and no failure.
//
// The VERDICTS and the PROMPT ID stay literals, because their owner is `internal/vlm`
// and there is no vocabulary package below it for them to live in.
// `TestTheStoredSpellingsMatchTheVerifierClosedSets` in spelling_test.go fails if any
// of them drifts from the verifier.
const (
	// verdictConfirmed, verdictNoObjects and verdictNeedsReview are the three
	// verdicts a brief or a search counts separately. `none` is a NEGATIVE answer
	// and is the only one of the four that means "the model was asked and saw
	// nothing"; `needs_review` is a refusal to decide and is never folded into
	// confirmed.
	verdictConfirmed   = "confirmed"
	verdictNoObjects   = "none"
	verdictNeedsReview = "needs_review"
	// promptAlarm is the only prompt class `pocctl search` returns. A
	// `camera_integrity` answer is a sentence about the VIEW ("moved", "covered"),
	// and matching it against an operator's search for a car would be a match on
	// the word "the". The prompt id is in `internal/vlm/prompts/manifest.json`;
	// renaming the file is a manifest edit there and a literal here, and
	// `TestTheStoredSpellingsMatchTheVerifierClosedSets` is what makes the two
	// agree.
	promptAlarm = "alarm"
)

// Decision is the stored decision input for one detection.
type Decision struct {
	DetectionID  string
	RunID        string
	TriggerMode  string
	TriggerClass string
	Score        float64
	Threshold    float64
	Release      float64
	Sustain      int
	Streak       int
	ScoreReason  string
	RegionSource string
	RegionTop    int
	RegionLeft   int
	RegionBottom int
	RegionRight  int
	FrameSeq     int64
	ReferenceSHA string
	CreatedAt    time.Time
	// Model attribution. Every field here is a POINTER, and that is the design
	// rather than an inconvenience.
	//
	// A `motion`-mode decision was made by comparing two frames and a threshold.
	// No model produced it. A `detector`-mode decision was made by one specific
	// file with one specific hash under one specific licence, and a number that
	// cannot name that file describes a build nobody can reconstruct.
	//
	// A pointer is what separates those two states. `ModelSHA256 string` with a
	// zero value records an EMPTY HASH, which is a claim — that something was
	// hashed and it hashed to nothing. A nil pointer records "no model", which is
	// the truth for a pixel diff. RC-18 is one comparison made possible by that
	// distinction: a decision in `detector` mode must have a non-nil
	// ModelSHA256, and a decision in `motion` mode must have a nil one.
	ModelIdentity *string
	ModelSHA256   *string
	ModelLicence  *string
	InputSize     *int
	NMSClassIOU   *float64
	NMSGlobalIOU  *float64
	// InferMS is this frame's own inference cost, and the three percentiles are
	// the distribution the detector had reached when this decision was made.
	// Recorded per decision rather than only in a report because the report is
	// derived and this is the record.
	InferMS         *float64
	DetLatencyP50MS *float64
	DetLatencyP95MS *float64
	DetLatencyP99MS *float64
	// TrackID is the tracker identity of the object that crossed, nil for a
	// trigger that does not track.
	TrackID *int
}

// NamesModel reports whether this decision names a model that produced it.
//
// The single predicate RC-18 is written against, and the one place the rule lives
// so the gate and the writer cannot disagree about what "named" means.
func (d Decision) NamesModel() bool {
	return d.TriggerMode == TriggerModeDetector &&
		d.ModelSHA256 != nil && *d.ModelSHA256 != "" &&
		d.ModelIdentity != nil && *d.ModelIdentity != ""
}

// TriggerModeDetector is the trigger mode whose decisions must name a model. It
// is an alias of the canonical set in `internal/config`, which owns
// SP_POC_TRIGGER_MODE; a string compared in two packages is a string one of them
// will stop updating.
const TriggerModeDetector = config.TriggerModeDetector

// Region is the measured region in 0..1000 thousandths, matching the scale
// detections.box_* and the pushed event already use.
func (d Decision) Region() (top, left, bottom, right int) {
	return d.RegionTop, d.RegionLeft, d.RegionBottom, d.RegionRight
}

// NonEmptyRegion reports whether the decision carries a real region.
//
// An all-zero region is the defect the plan calls the worst thing in a demo: the
// event would be pushed with a box that names nothing while the row beside it
// held real numbers.
func (d Decision) NonEmptyRegion() bool {
	return d.RegionRight > d.RegionLeft && d.RegionBottom > d.RegionTop
}

// PutDecision writes the decision inputs.
func (s *Store) PutDecision(ctx context.Context, d Decision) error {
	if d.DetectionID == "" || d.RunID == "" {
		return errors.New("store: a decision needs a detection and a run; a decision nobody can attribute is not a decision")
	}
	if !d.NonEmptyRegion() {
		return fmt.Errorf("store: refusing to record the all-zero region (%d,%d,%d,%d) for %s; evidence that names no "+
			"place is not a measurement", d.RegionTop, d.RegionLeft, d.RegionBottom, d.RegionRight, d.DetectionID)
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	// RC-18, on the WRITE path rather than only in a test. A decision written in
	// `detector` mode that names no model is refused here, so the defect cannot
	// reach the database at all — a reader of the table is entitled to assume
	// every row in it names the model that produced it, and a row that does not
	// is not a weaker claim, it is a false one.
	//
	// This is deliberately stricter than the test that checks it. The test proves
	// the gate goes red; this makes the gate true.
	if d.TriggerMode == TriggerModeDetector && !d.NamesModel() {
		return fmt.Errorf("store: refusing to record a %s-mode decision for %s that names no model: "+
			"a precision number that cannot name the model that produced it describes a build nobody can rebuild",
			TriggerModeDetector, d.DetectionID)
	}
	_, err := s.exec(ctx, `
		INSERT INTO decisions (
			detection_id,run_id,trigger_mode,trigger_class,score,threshold,release,sustain,streak,
			score_reason,region_source,region_top,region_left,region_bottom,region_right,frame_seq,reference_sha,created_at,
			model_identity,model_sha256,model_licence,input_size,nms_class_iou,nms_global_iou,
			infer_ms,det_latency_p50_ms,det_latency_p95_ms,det_latency_p99_ms,track_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(detection_id) DO UPDATE SET
			trigger_mode=excluded.trigger_mode, trigger_class=excluded.trigger_class,
			score=excluded.score, threshold=excluded.threshold, release=excluded.release,
			sustain=excluded.sustain, streak=excluded.streak, score_reason=excluded.score_reason,
			region_source=excluded.region_source, region_top=excluded.region_top,
			region_left=excluded.region_left, region_bottom=excluded.region_bottom,
			region_right=excluded.region_right, frame_seq=excluded.frame_seq, reference_sha=excluded.reference_sha,
			model_identity=excluded.model_identity, model_sha256=excluded.model_sha256,
			model_licence=excluded.model_licence, input_size=excluded.input_size,
			nms_class_iou=excluded.nms_class_iou, nms_global_iou=excluded.nms_global_iou,
			infer_ms=excluded.infer_ms, det_latency_p50_ms=excluded.det_latency_p50_ms,
			det_latency_p95_ms=excluded.det_latency_p95_ms, det_latency_p99_ms=excluded.det_latency_p99_ms,
			track_id=excluded.track_id`,
		d.DetectionID, d.RunID, d.TriggerMode, d.TriggerClass, d.Score, d.Threshold, d.Release, d.Sustain, d.Streak,
		d.ScoreReason, d.RegionSource, d.RegionTop, d.RegionLeft, d.RegionBottom, d.RegionRight, d.FrameSeq,
		d.ReferenceSHA, nowUTC(d.CreatedAt),
		d.ModelIdentity, d.ModelSHA256, d.ModelLicence, d.InputSize, d.NMSClassIOU, d.NMSGlobalIOU,
		d.InferMS, d.DetLatencyP50MS, d.DetLatencyP95MS, d.DetLatencyP99MS, d.TrackID)
	if err != nil {
		return fmt.Errorf("store: cannot record the decision for %s: %w", d.DetectionID, err)
	}
	return nil
}

// decisionColumns is the one column list the three decision queries share. It
// used to be written out three times, which is how a column gets added to the
// INSERT and forgotten in one of the SELECTs — and a forgotten column reads as a
// NULL rather than as a bug.
const decisionColumns = `detection_id,run_id,trigger_mode,trigger_class,score,threshold,release,sustain,streak,
		       score_reason,region_source,region_top,region_left,region_bottom,region_right,frame_seq,reference_sha,
		       model_identity,model_sha256,model_licence,input_size,nms_class_iou,nms_global_iou,
		       infer_ms,det_latency_p50_ms,det_latency_p95_ms,det_latency_p99_ms,track_id,created_at`

// DecisionFor reads one decision's inputs.
func (s *Store) DecisionFor(ctx context.Context, detectionID string) (*Decision, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions WHERE detection_id=?`, detectionID)
	d, err := scanDecision(row)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Decisions lists a run's decision inputs.
func (s *Store) Decisions(ctx context.Context, runID string) ([]Decision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions WHERE run_id=? ORDER BY created_at, detection_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the decisions for %s: %w", runID, err)
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// scanDecision reads the shared column list in the shared order.
//
// The eleven model-attribution columns are scanned into sql.Null* and then
// converted to pointers. Scanning them straight into `*float64` fields would
// work through database/sql's reflection, but it would make the difference
// between "recorded as zero" and "never recorded" invisible at the call site —
// and that difference is the entire reason they are nullable.
func scanDecision(row interface{ Scan(...any) error }) (*Decision, error) {
	var d Decision
	var created string
	var identity, sha, licence sql.NullString
	var trackID, inputSize sql.NullInt64
	var infer, p50, p95, p99, nmsClass, nmsGlobal sql.NullFloat64
	if err := row.Scan(&d.DetectionID, &d.RunID, &d.TriggerMode, &d.TriggerClass, &d.Score, &d.Threshold,
		&d.Release, &d.Sustain, &d.Streak, &d.ScoreReason, &d.RegionSource, &d.RegionTop, &d.RegionLeft,
		&d.RegionBottom, &d.RegionRight, &d.FrameSeq, &d.ReferenceSHA,
		&identity, &sha, &licence, &inputSize, &nmsClass, &nmsGlobal,
		&infer, &p50, &p95, &p99, &trackID, &created); err != nil {
		return nil, fmt.Errorf("store: cannot read a decision: %w", err)
	}
	d.ModelIdentity = strPtr(identity)
	d.ModelSHA256 = strPtr(sha)
	d.ModelLicence = strPtr(licence)
	d.InputSize = intPtr(inputSize)
	d.NMSClassIOU = floatPtr(nmsClass)
	d.NMSGlobalIOU = floatPtr(nmsGlobal)
	d.InferMS = floatPtr(infer)
	d.DetLatencyP50MS = floatPtr(p50)
	d.DetLatencyP95MS = floatPtr(p95)
	d.DetLatencyP99MS = floatPtr(p99)
	d.TrackID = intPtr(trackID)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &d, nil
}

func strPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// Reference is a pinned reference view for one camera in one run.
type Reference struct {
	RunID      string
	CameraGUID string
	StorageRef string
	SHA256     string
	PinnedAt   time.Time
	Source     string
}

// PinReference records a pinned reference view.
//
// The sha is mandatory: a pinned view nobody can be handed is not evidence, and a
// reference row without one is a promise.
func (s *Store) PinReference(ctx context.Context, r Reference) error {
	if r.RunID == "" || r.CameraGUID == "" {
		return errors.New("store: a pinned reference needs a run and a camera; a reference for nobody anchors nothing")
	}
	if r.SHA256 == "" {
		return errors.New("store: a pinned reference needs the content hash of the frame it was built from, so a reviewer " +
			"can be handed the exact picture every later measurement was made against")
	}
	if r.PinnedAt.IsZero() {
		r.PinnedAt = time.Now().UTC()
	}
	_, err := s.exec(ctx, `
		INSERT INTO reference_views (run_id,camera_guid,storage_ref,sha256,pinned_at,source)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(run_id,camera_guid) DO UPDATE SET
			storage_ref=excluded.storage_ref, sha256=excluded.sha256,
			pinned_at=excluded.pinned_at, source=excluded.source`,
		r.RunID, r.CameraGUID, r.StorageRef, r.SHA256, nowUTC(r.PinnedAt), r.Source)
	if err != nil {
		return fmt.Errorf("store: cannot record the pinned reference: %w", err)
	}
	return nil
}

// Reference reads one run's pinned reference for a camera.
func (s *Store) Reference(ctx context.Context, runID, cameraGUID string) (*Reference, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT run_id,camera_guid,storage_ref,sha256,pinned_at,source FROM reference_views WHERE run_id=? AND camera_guid=?`,
		runID, cameraGUID)
	var r Reference
	var pinned string
	if err := row.Scan(&r.RunID, &r.CameraGUID, &r.StorageRef, &r.SHA256, &pinned, &r.Source); err != nil {
		return nil, err
	}
	r.PinnedAt, _ = time.Parse(time.RFC3339Nano, pinned)
	return &r, nil
}

// LastReference finds the most recent pinned reference for a camera across runs,
// which is what makes the second run measure against the same view as the first.
func (s *Store) LastReference(ctx context.Context, cameraGUID string) (*Reference, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT run_id,camera_guid,storage_ref,sha256,pinned_at,source FROM reference_views
		WHERE camera_guid=? ORDER BY pinned_at DESC, run_id DESC LIMIT 1`, cameraGUID)
	var r Reference
	var pinned string
	if err := row.Scan(&r.RunID, &r.CameraGUID, &r.StorageRef, &r.SHA256, &pinned, &r.Source); err != nil {
		return nil, err
	}
	r.PinnedAt, _ = time.Parse(time.RFC3339Nano, pinned)
	return &r, nil
}

// HashOf is the content hash of a byte slice, used for the input-hash column and
// for anything else that needs a fingerprint without a file.
func HashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
