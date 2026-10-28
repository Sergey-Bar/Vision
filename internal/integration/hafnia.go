// Package integration adapts the Hafnia VLM client into the shared integration
// flow: build an airuntime.Request, call client.Do, return the Result.
package integration

import (
	"context"
	"fmt"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/hafnia"
)

// GeneralMonitoringProfile is the profile used when no per-scenario profile is
// configured. Its only structured claims are "something departed from normal"
// and "a hazard is visible", with the concept carried by the operator's
// question in the prompt.
const GeneralMonitoringProfile = "general-monitoring"

// GeneralMonitoringSchema is the output schema the general-monitoring profile
// answers in. It mirrors the Fields declared in
// observations.M1Profiles["general-monitoring"].
var GeneralMonitoringSchema = map[string]any{
	"properties": map[string]any{
		"insufficientEvidence": map[string]any{"type": "boolean"},
		"summary":              map[string]any{"type": "string"},
		"sceneDescription":     map[string]any{"type": "string"},
		"confidence":           map[string]any{"type": "number"},
		"abnormalEvent":        map[string]any{"type": "boolean"},
		"hazardVisible":        map[string]any{"type": "boolean"},
		"entities":             map[string]any{"type": "array"},
		"recommendedAction":    map[string]any{"type": "string"},
	},
	"required": []any{"summary"},
}

// Ask builds an airuntime.Request for the given event and evidence, calls the
// Hafnia client directly (hafnia is not a registered aichain kind), and returns
// the Result. Observation ids are random (hafnia.NewObservationID), so retries
// produce different ids — the bridge must not assume derived/idempotent ids.
//
// tenantID and siteID are explicit parameters, never read from a request body
// (invariant 5).
func Ask(ctx context.Context, client *hafnia.Client, tenantID, siteID string, evidence []airuntime.Evidence) (airuntime.Result, error) {
	obsID, err := hafnia.NewObservationID()
	if err != nil {
		return airuntime.Result{}, fmt.Errorf("integration: mint observation id: %w", err)
	}
	req := airuntime.Request{
		ObservationID: obsID,
		ProfileID:     GeneralMonitoringProfile,
		TenantID:      tenantID,
		SiteID:        siteID,
		Evidence:      evidence,
		ProfileSchema: GeneralMonitoringSchema,
		Temperature:   hafnia.DefaultTemperature,
	}
	return client.Do(ctx, req)
}
