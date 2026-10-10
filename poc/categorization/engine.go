// Package categorization provides AI-based object classification for video clips.
package categorization

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
)

// Engine performs AI-based categorization of video evidence.
type Engine struct {
	client              *airuntime.Client
	profileID           string
	confidenceThreshold float64
}

// Result contains the classification results.
type Result struct {
	Objects           []DetectedObject `json:"objects"`
	MaxConfidence     float64          `json:"max_confidence"`
	Degraded          bool             `json:"degraded"`
	DegradationReason string           `json:"degradation_reason,omitempty"`
	DegradationDetail string           `json:"degradation_detail,omitempty"`
	ProcessingTimeMS  int64            `json:"processing_time_ms"`
}

// DetectedObject represents a classified object.
type DetectedObject struct {
	Category    string         `json:"category"`
	SubCategory string         `json:"sub_category,omitempty"`
	Label       string         `json:"label"`
	Confidence  float64        `json:"confidence"`
	BoundingBox Box            `json:"bounding_box,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// Box represents a bounding box.
type Box struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// NewEngine creates a new categorization engine.
func NewEngine(client *airuntime.Client, profileID string, confidenceThreshold float64) *Engine {
	return &Engine{
		client:              client,
		profileID:           profileID,
		confidenceThreshold: confidenceThreshold,
	}
}

// Classify runs AI categorization on a video clip.
func (e *Engine) Classify(ctx context.Context, evidenceID, evidenceHash string, clipData []byte) (*Result, error) {
	start := time.Now()

	if e.client == nil {
		return &Result{
			Degraded:          true,
			DegradationReason: "no_ai_runtime",
			DegradationDetail: "AI runtime client not configured",
			ProcessingTimeMS:  time.Since(start).Milliseconds(),
		}, nil
	}

	req := airuntime.Request{
		ObservationID: fmt.Sprintf("obs_%s", evidenceHash[:16]),
		ProfileID:     e.profileID,
		Evidence: []airuntime.Evidence{
			{
				ID:          evidenceID,
				SHA256:      evidenceHash,
				ContentType: "video/mp4",
				Data:        clipData,
			},
		},
		ProfileSchema: getProfileSchema(e.profileID),
	}

	result, err := e.client.Do(ctx, req)
	if err != nil {
		return &Result{
			Degraded:          true,
			DegradationReason: "inference_failed",
			DegradationDetail: err.Error(),
			ProcessingTimeMS:  time.Since(start).Milliseconds(),
		}, err
	}

	if result.Degraded {
		return &Result{
			Degraded:          true,
			DegradationReason: string(result.DegradedReason),
			DegradationDetail: result.DegradedDetail,
			ProcessingTimeMS:  time.Since(start).Milliseconds(),
		}, nil
	}

	objects := e.parseObjects(result.Structured)
	filtered := filterByConfidence(objects, e.confidenceThreshold)
	maxConf := maxConfidence(filtered)

	return &Result{
		Objects:          filtered,
		MaxConfidence:    maxConf,
		Degraded:         false,
		ProcessingTimeMS: time.Since(start).Milliseconds(),
	}, nil
}

func (e *Engine) parseObjects(output map[string]any) []DetectedObject {
	var objects []DetectedObject

	entities, ok := output["entities"].([]any)
	if !ok {
		return objects
	}

	for _, ent := range entities {
		objMap, ok := ent.(map[string]any)
		if !ok {
			continue
		}

		obj := DetectedObject{
			Category:    getString(objMap, "category"),
			SubCategory: getString(objMap, "sub_category"),
			Label:       getString(objMap, "label"),
			Confidence:  getFloat64(objMap, "confidence"),
			Attributes:  objMap,
		}

		if bbox, ok := objMap["bounding_box"].(map[string]any); ok {
			obj.BoundingBox = Box{
				X:      getFloat64(bbox, "x"),
				Y:      getFloat64(bbox, "y"),
				Width:  getFloat64(bbox, "width"),
				Height: getFloat64(bbox, "height"),
			}
		}

		if obj.Confidence >= e.confidenceThreshold {
			objects = append(objects, obj)
		}
	}

	return objects
}

func filterByConfidence(objects []DetectedObject, threshold float64) []DetectedObject {
	var filtered []DetectedObject
	for _, obj := range objects {
		if obj.Confidence >= threshold {
			filtered = append(filtered, obj)
		}
	}
	return filtered
}

func maxConfidence(objects []DetectedObject) float64 {
	maxVal := 0.0
	for _, obj := range objects {
		if obj.Confidence > maxVal {
			maxVal = obj.Confidence
		}
	}
	return maxVal
}

// ToRuleFormat converts results to rule engine format.
func (r *Result) ToRuleFormat() []map[string]any {
	var out []map[string]any
	for _, obj := range r.Objects {
		out = append(out, map[string]any{
			"category":     obj.Category,
			"sub_category": obj.SubCategory,
			"label":        obj.Label,
			"confidence":   obj.Confidence,
			"bounding_box": obj.BoundingBox,
			"attributes":   obj.Attributes,
		})
	}
	return out
}

// ToSearchFormat converts results to search index format.
func (r *Result) ToSearchFormat() []map[string]any {
	var out []map[string]any
	for _, obj := range r.Objects {
		out = append(out, map[string]any{
			"category":     obj.Category,
			"sub_category": obj.SubCategory,
			"label":        obj.Label,
			"confidence":   obj.Confidence,
		})
	}
	return out
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat64(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func getProfileSchema(profileID string) map[string]any {
	schemas := map[string]map[string]any{
		"restricted-person": {
			"type": "object",
			"properties": map[string]any{
				"insufficientEvidence": map[string]any{"type": "boolean"},
				"persons": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"category":     map[string]any{"type": "string", "enum": []string{"biological"}},
							"sub_category": map[string]any{"type": "string", "enum": []string{"human"}},
							"label":        map[string]any{"type": "string"},
							"confidence":   map[string]any{"type": "number"},
							"bounding_box": map[string]any{"type": "object"},
							"attributes":   map[string]any{"type": "object"},
						},
						"required": []string{"category", "label", "confidence"},
					},
				},
				"accessState":          map[string]any{"type": "string", "enum": []string{"authorized", "unauthorized", "unknown"}},
				"restrictedZoneBreach": map[string]any{"type": "boolean"},
				"afterHours":           map[string]any{"type": "boolean"},
				"summary":              map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "number"},
			},
			"required": []string{"insufficientEvidence", "persons", "accessState", "restrictedZoneBreach", "afterHours", "summary", "confidence"},
		},
		"vehicle-at-gate": {
			"type": "object",
			"properties": map[string]any{
				"insufficientEvidence": map[string]any{"type": "boolean"},
				"vehicleState":         map[string]any{"type": "string", "enum": []string{"approaching", "stopped", "departed", "unknown"}},
				"vehicleCount":         map[string]any{"type": "integer"},
				"plateLegible":         map[string]any{"type": "boolean"},
				"plateText":            map[string]any{"type": "string"},
				"gateState":            map[string]any{"type": "string", "enum": []string{"open", "closed", "opening", "closing", "unknown"}},
				"summary":              map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "number"},
			},
			"required": []string{"insufficientEvidence", "vehicleState", "vehicleCount", "plateLegible", "gateState", "summary", "confidence"},
		},
		"general-monitoring": {
			"type": "object",
			"properties": map[string]any{
				"insufficientEvidence": map[string]any{"type": "boolean"},
				"summary":              map[string]any{"type": "string"},
				"sceneDescription":     map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "number"},
				"abnormalEvent":        map[string]any{"type": "boolean"},
				"hazardVisible":        map[string]any{"type": "boolean"},
				"entities": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"category":     map[string]any{"type": "string", "enum": []string{"vehicle", "biological", "other"}},
							"sub_category": map[string]any{"type": "string"},
							"label":        map[string]any{"type": "string"},
							"confidence":   map[string]any{"type": "number"},
							"bounding_box": map[string]any{"type": "object"},
							"attributes":   map[string]any{"type": "object"},
						},
						"required": []string{"category", "label", "confidence"},
					},
				},
				"recommendedAction": map[string]any{"type": "string"},
			},
			"required": []string{"insufficientEvidence", "summary", "confidence", "entities"},
		},
	}

	if schema, ok := schemas[profileID]; ok {
		return schema
	}
	return schemas["general-monitoring"]
}
