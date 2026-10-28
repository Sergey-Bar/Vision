package main

// The demo surface.
//
// /api/demo-hafnia runs the Milestone -> evidence -> Hafnia VLM answer ->
// Milestone bookmark bridge entirely in-process against synthetic fixtures and
// a local httptest Hafnia server. No securityctl, no live Milestone, no
// network egress. Every byte it touches is synthetic/demo data and the
// response carries `simulated: true`; it is never presented as automatic
// detection.
//
// Tenant and site ids are explicit constants here (invariant 5): they are
// never read from a request body.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/hafnia"
	"github.com/Sergey-Bar/vision/internal/integration"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

const (
	demoTenantID = "tenant-demo"
	demoSiteID   = "site-demo"
)

// demoHafniaRequest is the whole request: one action, no parameters.
//
// Decoding with DisallowUnknownFields keeps a misspelled field from being
// silently ignored.
type demoHafniaRequest struct{}

type demoHafniaResponse struct {
	Event     demoEvent    `json:"event"`
	Hafnia    demoHafnia   `json:"hafnia"`
	Bookmark  demoBookmark `json:"bookmark"`
	Simulated bool         `json:"simulated"`
}

type demoEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Camera     string `json:"camera"`
	OccurredAt string `json:"occurredAt"`
}

type demoHafnia struct {
	Degraded   bool           `json:"degraded"`
	Reason     string         `json:"reason,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Structured map[string]any `json:"structured,omitempty"`
	Model      string         `json:"model,omitempty"`
}

type demoBookmark struct {
	Placed  bool   `json:"placed"`
	Ref     string `json:"ref,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// hafniaDoer rewrites the pinned hafnia host to the local test server, so the
// client's own allow-list and redirect policy are exercised rather than
// bypassed.
type hafniaDoer struct {
	base string
	do   func(*http.Request) (*http.Response, error)
}

func (d *hafniaDoer) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(d.base, "http://")
	return d.do(req)
}

// stubDemoBookmarkWriter records bookmark placements without reaching a vendor.
type stubDemoBookmarkWriter struct {
	placed []milestone.Bookmark
}

func (s *stubDemoBookmarkWriter) WriteBookmark(_ context.Context, b milestone.Bookmark) (milestone.BookmarkRef, error) {
	s.placed = append(s.placed, b)
	return milestone.BookmarkRef{ID: "bmk-demo-" + b.IdempotencyKey}, nil
}

// testPatternSource generates synthetic test-pattern clip bytes for an event.
type testPatternSource struct{}

func (s *testPatternSource) EvidenceFor(_ context.Context, event integration.Event) ([]byte, string, error) {
	data := []byte("SYNTHETIC-TEST-PATTERN-CLIP-" + event.EventID())
	return data, "video/mp4", nil
}

// validDemoAnswer is a well-formed Hafnia structured answer the demo server
// returns, so the happy path renders structured output rather than a degraded
// one.
const validDemoAnswer = `{"model":"hafnia-8b","choices":[{"message":{"content":"{\"summary\":\"a person crossed the restricted zone\",\"sceneDescription\":\"A person is crossing the restricted zone at the entrance camera.\",\"confidence\":0.9,\"abnormalEvent\":true,\"hazardVisible\":false,\"entities\":[\"person\"],\"recommendedAction\":\"Review the restricted-zone footage.\"}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`

func handleDemoHafnia() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleDemoHafnia: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if !methodIs(w, r) {
			return
		}
		var req demoHafniaRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			return
		}

		ctx := r.Context()

		// Hafnia pinned to a local test server (allow-list safe).
		hafniaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch {
			case req.URL.Path == "/assets" && req.Method == http.MethodPost:
				body, _ := io.ReadAll(req.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"asset-%s","mediaType":"video/mp4","sizeBytes":%d}`, demoHash(req.URL.String()), len(body))
			case req.URL.Path == "/chat/completions" && req.Method == http.MethodPost:
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, validDemoAnswer)
			case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/assets/"):
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer hafniaServer.Close()

		doer := &hafniaDoer{base: hafniaServer.URL, do: hafniaServer.Client().Do}
		hafniaClient, err := hafnia.New(hafnia.Config{
			Region:              hafnia.RegionEU,
			APIKey:              "demo-key",
			PromptTemplate:      "PROMPT VERBATIM FROM THE PROVIDER'S DOCUMENTATION",
			PromptVersion:       "v1",
			OutputSchemaVersion: 1,
			APIModelName:        "hafnia-8b",
			Timeout:             30 * time.Second,
			Transport:           doer,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("hafnia client: %v", err), http.StatusInternalServerError)
			return
		}

		// Synthetic Milestone scenario with a UUID-shaped camera ref so the
		// bookmark write-back's parseDevicePath accepts it.
		occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		rec := integration.SimulatedRawRecord(
			"evt-motion-001", "xprotect-demo", "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
			occurred, integration.EventTypeMotion, "Motion detected at entrance",
		)
		envelope, err := json.Marshal(map[string]any{
			"Id":        rec.SourceEventID,
			"Type":      rec.EventType,
			"Source":    rec.SourceDeviceRef,
			"Timestamp": rec.ObservedAt.UTC().Format(time.RFC3339),
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("encode event: %v", err), http.StatusInternalServerError)
			return
		}
		scenario := fixture.Scenario{
			FormatVersion: fixture.FormatVersion,
			Name:          "synthetic-motion",
			Source:        "synthetic",
			SiteID:        demoSiteID,
			Epoch:         occurred,
			Events: []fixture.Event{
				{
					Kind:     milestone.APIPrefix + "/events",
					Method:   "GET",
					OffsetMS: 0,
					Envelope: envelope,
				},
			},
		}
		replay := milestone.NewReplayTransport([]fixture.Scenario{scenario})
		adapter := milestone.New(replay, demoSiteID, "integration-hafnia")

		writer := &stubDemoBookmarkWriter{}
		bridge := &integration.Bridge{
			Adapter:        adapter,
			Hafnia:         hafniaClient,
			TenantID:       demoTenantID,
			SiteID:         demoSiteID,
			ProfileID:      integration.GeneralMonitoringProfile,
			EvidenceSource: &testPatternSource{},
			BookmarkWriter: writer,
		}

		results, err := bridge.Run(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf("bridge: %v", err), http.StatusInternalServerError)
			return
		}

		var r0 integration.BridgeResult
		if len(results) > 0 {
			r0 = results[0]
		}

		resp := demoHafniaResponse{
			Event: demoEvent{
				ID:         eventID(r0.Event),
				Type:       eventType(r0.Event),
				Camera:     camera(r0.Event),
				OccurredAt: occurredAt(r0.Event),
			},
			Hafnia: demoHafnia{
				Degraded:   r0.Result.Degraded,
				Reason:     string(r0.Result.DegradedReason),
				Detail:     r0.Result.DegradedDetail,
				Structured: r0.Result.Structured,
				Model:      r0.Result.Model,
			},
			Simulated: r0.Simulated,
		}
		if r0.BookmarkRef != nil {
			resp.Bookmark.Placed = true
			resp.Bookmark.Ref = r0.BookmarkRef.ID
		} else {
			resp.Bookmark.Refusal = r0.BookmarkRefusal
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func eventID(e integration.Event) string {
	if e == nil {
		return ""
	}
	return e.EventID()
}

func eventType(e integration.Event) string {
	if e == nil {
		return ""
	}
	return e.EventType()
}

func camera(e integration.Event) string {
	if e == nil {
		return ""
	}
	return e.LogicalCamera()
}

func occurredAt(e integration.Event) string {
	if e == nil {
		return ""
	}
	return e.OccurredAt().UTC().Format(time.RFC3339)
}

func demoHash(s string) string {
	sum := 0
	for _, c := range s {
		sum = (sum*31 + int(c)) & 0xffffffff
	}
	return fmt.Sprintf("%08x", sum)
}
