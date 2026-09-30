package airuntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// VLMWorker consumes jobs.QueueVLM.
//
// It exists as a worker rather than a direct call so the AI plane is reached at
// the queue's pace: a site that cannot keep up with the plane retries, dead
// letters, and shows an operator a backlog, rather than blocking the consume loop
// on an inference.

// VLMJob is the payload the consume loop enqueues.
type VLMJob struct {
	IncidentID string `json:"incidentId"`
	EventID    string `json:"eventId"`
}

// VLMWorker serves the VLM queue.
type VLMWorker struct {
	*jobs.Worker
	Client *Client
	// Observations persists results. Every path writes a row, including the
	// failures: "the model was unavailable at 03:14" is a fact a pilot needs six
	// weeks later, and a log line is not where anybody looks for it.
	Observations *observations.Writer
	// Evidence reads the bytes an observation cites. Nil means no evidence can
	// be fetched, and the worker degrades rather than calling the runtime with
	// nothing.
	Evidence *evidence.Store
	// Metrics is best-effort and never on the availability path.
	Metrics *observability.Registry
	Log     *slog.Logger
	// DefaultProfile is the profile used when the job names none.
	DefaultProfile string
}

// NewVLMWorker builds the worker and its queue binding.
//
// The lease is jobs.VLMLeaseDuration and is not overridden: an inference that
// takes minutes is exactly the case the longer lease exists for, and shortening
// it would have a slow runtime's job redelivered while the first attempt is
// still running.
func NewVLMWorker(pool *jobs.Worker, client *Client, obs *observations.Writer,
	store *evidence.Store, metrics *observability.Registry, log *slog.Logger,
) *VLMWorker {
	w := &VLMWorker{
		Client: client, Observations: obs, Evidence: store,
		Metrics: metrics, Log: log,
		DefaultProfile: "restricted-person",
		Worker: &jobs.Worker{
			Enqueuer: jobs.Enqueuer{Pool: pool.Pool},
			Name:     jobs.QueueVLM,
			// jobs.Worker.lease() already returns VLMLeaseDuration for this
			// queue; setting LeaseDuration here would only duplicate the rule in
			// a second place that could drift.
			Handler: nil,
		},
	}
	w.Handler = w.Handle
	return w
}

// vlmPayload is read from the job.
type vlmPayload struct {
	IncidentID string `json:"incidentId"`
	EventID    string `json:"eventId"`
	// EvidenceID names one stored object to show the model. A job with none
	// degrades: the runtime answers `absent` for an empty evidence list, and
	// asking it to would spend a round trip to be told nothing.
	EvidenceID  string `json:"evidenceId,omitempty"`
	EvidenceSHA string `json:"evidenceSha256,omitempty"`
	ProfileID   string `json:"profileId,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// Handle runs one VLM job.
//
// It is idempotent on the incident: a redelivery after a crash writes a *second*
// observation, which is the correct shape - two runs of a model are two
// observations and supersession is how the later one wins. What it must not do
// is write a fabricated answer on the retry, which is why every non-success path
// here ends in RecordDegraded.
func (w *VLMWorker) Handle(ctx context.Context, j jobs.Job) error {
	if w.Metrics != nil {
		w.Metrics.Counters.Inc(observability.CounterVLMInvocations)
	}
	fail := func(err error) error {
		if w.Metrics != nil {
			w.Metrics.Counters.Inc(observability.CounterVLMErrors)
		}
		return err
	}

	var payload VLMJob
	enqueued := vlmPayload{}
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &enqueued); err != nil {
			return fail(fmt.Errorf("airuntime: the job payload is not JSON: %w", err))
		}
	}
	payload = VLMJob{IncidentID: enqueued.IncidentID, EventID: enqueued.EventID}
	if payload.IncidentID == "" || payload.EventID == "" {
		return fail(errors.New("airuntime: the job names no incident or no event; an " +
			"observation cannot exist without both, because ai_observations requires them"))
	}

	profileID := enqueued.ProfileID
	if profileID == "" {
		profileID = w.DefaultProfile
	}

	base := observations.Observation{
		TenantID:   j.TenantID,
		SiteID:     j.SiteID,
		IncidentID: payload.IncidentID,
		EventID:    payload.EventID,
		ProfileID:  profileID,
	}

	// No runtime configured. This is a supported configuration - a site with no
	// model is a real deployment - and the honest record is a degraded
	// observation saying so, not a connection error and not a fabricated answer.
	if w.Client == nil {
		return w.record(ctx, base, nil, nil,
			observations.DegradationNoRuntime,
			"no AI endpoint is configured on this core")
	}

	evidenceList, degraded, err := w.gatherEvidence(ctx, enqueued)
	if err != nil {
		return fail(err)
	}
	if degraded != nil {
		return w.record(ctx, base, evidenceList, nil, *degraded, degradedDetail(*degraded))
	}

	// The observation id is generated here, before the call, so the same id is
	// used in the request and in the stored row. The runtime echoes it back, and
	// a mismatch would mean the observation describes an inference that did not
	// happen.
	obsID := "obs_pending_" + j.ID

	result, callErr := w.Client.Do(ctx, Request{
		ObservationID: obsID,
		ProfileID:     profileID,
		Evidence:      evidenceList,
	})

	applied := applyResult(base, result)

	if callErr != nil {
		// The call failed. A degraded observation is still written, because "the
		// plane was unreachable at 03:14" is the fact a pilot is measuring. The
		// error is returned so the job retries - a retry that also fails writes
		// another observation, which is honest: they were separate attempts.
		if rerr := w.record(ctx, applied, evidenceList, &result,
			result.DegradedReason, callErr.Error()); rerr != nil {
			return fail(rerr)
		}
		return callErr
	}

	if result.Degraded {
		// The runtime answered, and the answer was that it could not answer.
		// That is a success: the job is done, the row is written, and nothing
		// retries.
		return w.record(ctx, applied, evidenceList, &result, result.DegradedReason, result.DegradedDetail)
	}

	_, err = w.recordResult(ctx, applied, evidenceList, result)
	return err
}

// gatherEvidence reads the bytes the observation will cite.
//
// It returns a degradation rather than an error when the evidence cannot be
// obtained, because "we could not fetch the evidence" is a recorded degradation
// and not a job failure worth retrying: the fetch will not succeed on a retry
// either if the object is gone.
//
// The context is accepted for symmetry with the rest of the worker's methods and
// is not yet plumbed into the fetch: evidence.Open is a local read, and the
// lease that bounds this work is the job's, not a deadline the store honours.
// The parameter is named _ rather than dropped so a context-aware Open is a
// one-line change here rather than a signature change at every call site.
func (w *VLMWorker) gatherEvidence(_ context.Context, p vlmPayload) ([]Evidence, *observations.Degradation, error) {
	if p.EvidenceID == "" {
		reason := observations.DegradationAbsent
		return nil, &reason, nil
	}
	if w.Evidence == nil {
		reason := observations.DegradationError
		return nil, &reason, errors.New("airuntime: an evidence store is required to fetch " +
			"cited evidence and none is configured")
	}

	rc, _, err := w.Evidence.Open(p.EvidenceID, p.EvidenceSHA)
	if err != nil {
		reason := observations.DegradationError
		return nil, &reason, fmt.Errorf("airuntime: open evidence %q: %w", p.EvidenceID, err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(io.LimitReader(rc, evidence.DefaultMaxBytes))
	if err != nil {
		reason := observations.DegradationError
		return nil, &reason, fmt.Errorf("airuntime: read evidence %q: %w", p.EvidenceID, err)
	}
	return []Evidence{{
		ID: p.EvidenceID,
		// Always the recorded hash, never one computed here. Open skips
		// verification entirely when expectSHA is empty, so passing "" would
		// quietly send bytes nobody checked to a model, and the observation would
		// cite a hash the platform never verified.
		SHA256:      p.EvidenceSHA,
		ContentType: p.ContentType,
		Data:        data,
	}}, nil, nil
}

func degradedDetail(reason observations.Degradation) string {
	switch reason {
	case observations.DegradationAbsent:
		return "the job cited no evidence, and an inference over nothing is not an observation"
	default:
		return "the evidence the job cited could not be read"
	}
}

// applyResult copies the runtime's attribution onto the base observation.
func applyResult(base observations.Observation, r Result) observations.Observation {
	if r.Model != "" {
		base.Model = r.Model
	}
	if r.ModelHash != "" {
		base.ModelHash = r.ModelHash
	}
	if r.RuntimeVersion != "" {
		base.RuntimeVersion = r.RuntimeVersion
	}
	if r.PromptTemplateID != "" {
		base.PromptTemplateID = r.PromptTemplateID
	}
	if r.PromptVersion != "" {
		base.PromptVersion = r.PromptVersion
	}
	base.ModelBundleSignature = r.ModelBundleSignature
	return base
}

// record persists a degraded observation. Every failure path in this worker ends
// here, and it always calls RecordDegraded rather than Record - RecordDegraded
// forces StructuredOutput to {} and InsufficientEvidence to true, so a consumer
// that forgets to check the degradation fails closed rather than reading an
// empty output as a negative answer.
//
// It returns only the error. Every caller discards the stored observation: the
// id it is given is derived from the job, so the caller could reconstruct it and
// a returned copy would only invite a caller to believe the row exists without
// having checked the error.
func (w *VLMWorker) record(ctx context.Context, base observations.Observation,
	evidenceList []Evidence, result *Result, reason observations.Degradation, detail string,
) error {
	if w.Observations == nil {
		return errors.New("airuntime: no observation writer is " +
			"configured; an inference that produced no record is not an inference")
	}
	// A degraded response from the runtime carries no attribution, because there
	// was no model to attribute. Validate requires the attribution columns, so a
	// documented placeholder is supplied and marked, rather than the row being
	// refused and the degradation being lost.
	applyDegradedPlaceholders(&base)
	base.InputEvidenceIDs = evidenceIDs(evidenceList)
	base.InputEvidenceHashes = evidenceHashes(evidenceList, result)
	if result != nil && result.RuntimeVersion != "" {
		base.RuntimeVersion = result.RuntimeVersion
	}

	recorded, err := w.Observations.RecordDegraded(ctx, base, reason, detail)
	if err != nil {
		if w.Log != nil {
			w.Log.Error("a degraded observation could not be recorded",
				"tenant_id", base.TenantID, "site_id", base.SiteID,
				"incident_id", base.IncidentID, "reason", string(reason),
				"error", err.Error())
		}
		return err
	}
	if w.Log != nil {
		w.Log.Info("recorded a degraded observation",
			"tenant_id", base.TenantID, "site_id", base.SiteID,
			"incident_id", base.IncidentID,
			"observation_id", recorded.ObservationID,
			"reason", string(reason))
	}
	return nil
}

// recordResult persists a real inference.
func (w *VLMWorker) recordResult(ctx context.Context, base observations.Observation,
	evidenceList []Evidence, r Result) (observations.Observation, error) {
	if w.Observations == nil {
		return observations.Observation{}, errors.New("airuntime: no observation writer is " +
			"configured; an inference that produced no record is not an inference")
	}
	base.StructuredOutput = r.Structured
	base.InsufficientEvidence = r.InsufficientEvidence
	base.Degraded = observations.DegradationNone
	base.LatencyMS = r.LatencyMS
	base.Hardware = r.Hardware
	if r.OutputSchemaVersion > 0 {
		base.OutputSchemaVersion = r.OutputSchemaVersion
	}
	base.InputEvidenceIDs = evidenceIDs(evidenceList)
	base.InputEvidenceHashes = evidenceHashes(evidenceList, &r)

	recorded, err := w.Observations.Record(ctx, base)
	if err != nil {
		return observations.Observation{}, err
	}
	if w.Log != nil {
		w.Log.Info("recorded an observation",
			"tenant_id", base.TenantID, "site_id", base.SiteID,
			"incident_id", base.IncidentID,
			"observation_id", recorded.ObservationID)
	}
	return recorded, nil
}

// applyDegradedPlaceholders fills the attribution columns a degraded observation
// still has to carry. The vocabulary lives with the schema it satisfies, in the
// observations package, because a second copy of it would drift into a second
// set of values for the same fact.
func applyDegradedPlaceholders(base *observations.Observation) {
	observations.ApplyDegradedPlaceholders(base)
}

func evidenceIDs(list []Evidence) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

func evidenceHashes(list []Evidence, result *Result) []string {
	// The runtime echoes the hashes it actually verified, which is the stronger
	// statement: it is the model's view of what it was shown, not ours.
	if result != nil && len(result.InputEvidenceHashes) > 0 {
		return result.InputEvidenceHashes
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.SHA256)
	}
	return out
}
