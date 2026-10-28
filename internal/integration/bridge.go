// Package integration bridges Milestone XProtect and Hafnia VLM: poll →
// evidence → Hafnia ask → bookmark write-back.
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/hafnia"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// EvidenceSource provides video evidence bytes for an event. The demo supplies
// a synthetic test-pattern source; a live deployment reads from the recorder or
// evidence store.
type EvidenceSource interface {
	// EvidenceFor returns video evidence bytes and their content type for the
	// event, or (nil, "", nil) when no evidence is available.
	EvidenceFor(ctx context.Context, event Event) (data []byte, contentType string, err error)
}

// BridgeResult is the outcome of processing one source event through the
// bridge: poll → Hafnia ask → optional bookmark write-back.
type BridgeResult struct {
	// Event is the source event that was processed.
	Event Event
	// Result is Hafnia's answer (degraded when it could not answer).
	Result airuntime.Result
	// BookmarkRef is the placed bookmark's reference, or nil.
	BookmarkRef *milestone.BookmarkRef
	// BookmarkRefusal is the reason no bookmark was placed, or empty.
	BookmarkRefusal string
	// Simulated echoes the source event's Simulated flag.
	Simulated bool
	// Err is a non-fatal processing error for this event (the bridge continues).
	Err error
}

// Bridge wires Milestone poll → evidence selection → Hafnia ask → bookmark
// write-back. Tenant and site ids come from the caller/session (invariant 5).
type Bridge struct {
	// Adapter is the Milestone adapter to poll.
	Adapter *milestone.Adapter
	// Hafnia is the Hafnia VLM client.
	Hafnia *hafnia.Client
	// TenantID is the tenant this bridge runs for (never from a request body).
	TenantID string
	// SiteID is the site this bridge runs for.
	SiteID string
	// ProfileID is the Hafnia profile to ask under.
	ProfileID string
	// EvidenceSource supplies video evidence for each event.
	EvidenceSource EvidenceSource
	// BookmarkWriter is the optional write-back transport. Nil means no
	// bookmarks are placed (replay/synthetic demo).
	BookmarkWriter milestone.BookmarkWriter
	// Observations is the optional observation writer. Nil means no rows are
	// persisted.
	Observations *observations.Writer
	// Clock pins "now" for bookmark windows and observation timestamps.
	Clock func() time.Time
	// PreRoll and PostRoll widen the bookmark window around the event.
	PreRoll, PostRoll time.Duration
}

// Run executes one poll cycle: read Milestone records, ask Hafnia about each,
// and write back bookmarks. Results are returned even when individual events
// fail, so a single bad event does not lose the whole poll.
func (b *Bridge) Run(ctx context.Context) ([]BridgeResult, error) {
	if b.Adapter == nil {
		return nil, fmt.Errorf("integration: bridge has no milestone adapter")
	}
	if b.Hafnia == nil {
		return nil, fmt.Errorf("integration: bridge has no hafnia client")
	}
	if b.TenantID == "" {
		return nil, fmt.Errorf("integration: bridge requires a tenant id")
	}
	if b.SiteID == "" {
		return nil, fmt.Errorf("integration: bridge requires a site id")
	}
	if b.ProfileID == "" {
		b.ProfileID = GeneralMonitoringProfile
	}
	if b.Clock == nil {
		b.Clock = func() time.Time { return time.Now().UTC() }
	}
	if b.PreRoll == 0 {
		b.PreRoll = 10 * time.Second
	}
	if b.PostRoll == 0 {
		b.PostRoll = 5 * time.Second
	}

	recs, err := b.Adapter.Poll(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("integration: milestone poll: %w", err)
	}

	results := make([]BridgeResult, 0, len(recs))
	for _, rec := range recs {
		results = append(results, b.processEvent(ctx, rec))
	}
	return results, nil
}

// processEvent handles one source event through the bridge.
func (b *Bridge) processEvent(ctx context.Context, rec milestone.RawRecord) BridgeResult {
	event, err := FromRawRecord(rec)
	if err != nil {
		return BridgeResult{Err: fmt.Errorf("integration: convert record: %w", err)}
	}

	result := BridgeResult{
		Event:     event,
		Simulated: event.Simulated(),
	}

	// Select evidence: video bytes with SHA256. Quota is consulted by
	// hafnia.Client.Do before anything leaves the site, so the bridge does
	// not re-implement that — it only never sends content without evidence.
	var evidence []airuntime.Evidence
	if b.EvidenceSource != nil {
		data, contentType, err := b.EvidenceSource.EvidenceFor(ctx, event)
		if err != nil {
			result.Err = fmt.Errorf("integration: evidence for %q: %w", event.EventID(), err)
			return result
		}
		if data != nil {
			sum := sha256.Sum256(data)
			evidence = []airuntime.Evidence{{
				ID:          "ev_" + event.EventID(),
				SHA256:      hex.EncodeToString(sum[:]),
				ContentType: contentType,
				Data:        data,
			}}
		}
	}

	// Ask Hafnia. Degraded results are returned as results, not errors.
	if len(evidence) == 0 {
		result.Result = airuntime.Result{
			ObservationID:       "",
			ProfileID:           b.ProfileID,
			Degraded:            true,
			DegradedReason:      observations.DegradationAbsent,
			DegradedDetail:      "no evidence for this event",
			PromptTemplateID:    "none",
			PromptVersion:       "none",
			OutputSchemaVersion: 1,
		}
		return result
	}

	result.Result, err = Ask(ctx, b.Hafnia, b.TenantID, b.SiteID, evidence)
	if err != nil {
		// A hard error from the provider is exceptional; record it but continue.
		result.Err = fmt.Errorf("integration: hafnia ask for %q: %w", event.EventID(), err)
		return result
	}

	// Write back a bookmark when a writer is available.
	if b.BookmarkWriter != nil {
		var summary string
		if s, ok := result.Result.Structured["summary"]; ok {
			if str, ok := s.(string); ok {
				summary = str
			}
		}
		if summary == "" && result.Result.Degraded {
			summary = result.Result.DegradedDetail
		}
		req := BookmarkRequest(event, b.SiteID, summary, event.OccurredAt(), b.PreRoll, b.PostRoll)
		decision, err := PlaceBookmark(ctx, req, b.BookmarkWriter)
		switch {
		case err != nil:
			result.BookmarkRefusal = err.Error()
		case decision.Refusal != "":
			result.BookmarkRefusal = decision.Refusal
		default:
			result.BookmarkRef = &decision.Ref
		}
	}

	// Persist an observation when a writer is available.
	if b.Observations != nil {
		o := observationFrom(event, b.TenantID, b.SiteID, b.ProfileID, result.Result)
		if _, err := b.Observations.Record(ctx, o); err != nil {
			// Observations are optional; log via the error field is not
			// appropriate for a per-event result. Record the refusal lightly.
			if result.Err == nil {
				result.Err = fmt.Errorf("integration: record observation: %w", err)
			}
		}
	}

	return result
}

// observationFrom builds an observations.Observation from a Hafnia result.
func observationFrom(event Event, tenantID, siteID, profileID string, r airuntime.Result) observations.Observation {
	o := observations.Observation{
		TenantID:             tenantID,
		SiteID:               siteID,
		IncidentID:           "inc_" + event.EventID(),
		EventID:              event.EventID(),
		ProfileID:            profileID,
		Model:                r.Model,
		ModelHash:            r.ModelHash,
		RuntimeVersion:       r.RuntimeVersion,
		PromptTemplateID:     r.PromptTemplateID,
		PromptVersion:        r.PromptVersion,
		ModelIdentity:        r.ModelIdentity,
		ModelContractVersion: r.ModelContractVersion,
		Temperature:          r.Temperature,
		InputEvidenceHashes:  r.InputEvidenceHashes,
		StructuredOutput:     r.Structured,
		InsufficientEvidence: r.InsufficientEvidence,
		LatencyMS:            r.LatencyMS,
		OutputSchemaVersion:  r.OutputSchemaVersion,
	}
	if r.Degraded {
		o.Degraded = r.DegradedReason
		o.DegradedDetail = r.DegradedDetail
		observations.ApplyDegradedPlaceholders(&o)
	} else {
		o.Degraded = observations.DegradationNone
	}
	return o
}
