package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/hafnia"
	"github.com/Sergey-Bar/vision/internal/integration"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/observations"
)

func TestBridgeStructuredResultAndBookmark(t *testing.T) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := integration.SimulatedRawRecord(
		"evt-bridge-1", "xprotect-demo",
		"cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		occurred, integration.EventTypeMotion, "Motion detected",
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/assets" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
		case r.URL.Path == "/chat/completions" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(validHafniaAnswer()))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/assets/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	doer := &testTransport{base: srv.URL, do: srv.Client().Do}
	cfg := hafnia.Config{
		Region:              hafnia.RegionEU,
		APIKey:              "k",
		PromptTemplate:      "p",
		PromptVersion:       "v1",
		OutputSchemaVersion: 1,
		APIModelName:        "hafnia-8b",
		Transport:           doer,
	}
	hafniaClient, err := hafnia.New(cfg)
	if err != nil {
		t.Fatalf("hafnia.New: %v", err)
	}

	envelope := eventEnvelope(rec)
	scenario := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "test-scenario",
		Source:        "synthetic",
		SiteID:        "site-demo",
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
	adapter := milestone.New(replay, "site-demo", "integration-hafnia")

	writer := &stubBookmarkWriter{}
	bridge := &integration.Bridge{
		Adapter:        adapter,
		Hafnia:         hafniaClient,
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		ProfileID:      integration.GeneralMonitoringProfile,
		EvidenceSource: &testPatternSource{},
		BookmarkWriter: writer,
	}

	results, err := bridge.Run(context.Background())
	if err != nil {
		t.Fatalf("bridge.Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	r := results[0]
	if r.Event == nil {
		t.Fatal("no event in result")
	}
	if r.Event.EventID() != "evt-bridge-1" {
		t.Errorf("event id = %q", r.Event.EventID())
	}
	if r.Event.Simulated() {
		t.Error("expected non-simulated event from replay")
	}
	if r.Err != nil {
		t.Fatalf("processing error: %v", r.Err)
	}
	res := r.Result
	if res.Degraded {
		t.Fatalf("expected structured result, got degraded: %s", res.DegradedDetail)
	}
	if res.Structured == nil {
		t.Fatal("expected structured output")
	}
	if r.BookmarkRefusal != "" {
		t.Fatalf("bookmark refused: %s", r.BookmarkRefusal)
	}
	if r.BookmarkRef == nil {
		t.Fatal("expected bookmark to be placed")
	}
	if r.BookmarkRef.ID == "" {
		t.Error("bookmark ref has no id")
	}
	if len(writer.placed) != 1 {
		t.Fatalf("expected 1 bookmark placed, got %d", len(writer.placed))
	}
	if writer.placed[0].SiteRef != "e028f597-c64b-4f27-bb8d-08f5cd768e1c" {
		t.Errorf("bookmark SiteRef = %q", writer.placed[0].SiteRef)
	}
	if writer.placed[0].IdempotencyKey == "" {
		t.Error("bookmark has no idempotency key")
	}
}

func TestBridgeDegradedWhenHafniaMalformed(t *testing.T) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := integration.SimulatedRawRecord(
		"evt-bridge-2", "xprotect-demo",
		"cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		occurred, integration.EventTypeMotion, "Motion detected",
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":`)) // malformed
	}))
	defer srv.Close()

	doer := &testTransport{base: srv.URL, do: srv.Client().Do}
	cfg := hafnia.Config{
		Region: hafnia.RegionEU, APIKey: "k", PromptTemplate: "p",
		PromptVersion: "v1", OutputSchemaVersion: 1, APIModelName: "hafnia-8b",
		Transport: doer,
	}
	hafniaClient, _ := hafnia.New(cfg)

	envelope := eventEnvelope(rec)
	scenario := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "test-scenario",
		Source:        "synthetic",
		SiteID:        "site-demo",
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
	adapter := milestone.New(replay, "site-demo", "integration-hafnia")

	bridge := &integration.Bridge{
		Adapter:        adapter,
		Hafnia:         hafniaClient,
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		ProfileID:      integration.GeneralMonitoringProfile,
		EvidenceSource: &testPatternSource{},
		BookmarkWriter: nil,
	}

	results, err := bridge.Run(context.Background())
	if err != nil {
		t.Fatalf("bridge.Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("processing error: %v", r.Err)
	}
	if !r.Result.Degraded {
		t.Fatal("expected degraded result for malformed answer")
	}
	if r.Result.Structured != nil {
		t.Errorf("degraded result carried structured output %v; that is a fabricated answer", r.Result.Structured)
	}
	if !strings.Contains(r.Result.DegradedDetail, "reason=") {
		t.Errorf("degraded detail %q lacks reason=", r.Result.DegradedDetail)
	}
	if r.BookmarkRef != nil {
		t.Error("expected no bookmark when degraded and no writer")
	}
}

func TestBridgeNoEvidenceIsDegradedAbsent(t *testing.T) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := integration.SimulatedRawRecord(
		"evt-bridge-3", "xprotect-demo",
		"cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		occurred, integration.EventTypeMotion, "Motion detected",
	)

	evSource := &nilEvidenceSource{}

	envelope := eventEnvelope(rec)
	scenario := fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          "test-scenario",
		Source:        "synthetic",
		SiteID:        "site-demo",
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
	adapter := milestone.New(replay, "site-demo", "integration-hafnia")

	// Hafnia is not called when there is no evidence, but the bridge
	// requires a non-nil client at setup. Use a dummy one.
	doer := &testTransport{base: "http://127.0.0.1:1", do: func(*http.Request) (*http.Response, error) { return nil, nil }}
	hafniaClient, _ := hafnia.New(hafnia.Config{
		Region: hafnia.RegionEU, APIKey: "k", PromptTemplate: "p",
		PromptVersion: "v1", OutputSchemaVersion: 1, APIModelName: "hafnia-8b",
		Transport: doer,
	})

	bridge := &integration.Bridge{
		Adapter:        adapter,
		Hafnia:         hafniaClient,
		TenantID:       "tenant-1",
		SiteID:         "site-1",
		ProfileID:      integration.GeneralMonitoringProfile,
		EvidenceSource: evSource,
		BookmarkWriter: nil,
	}

	results, err := bridge.Run(context.Background())
	if err != nil {
		t.Fatalf("bridge.Run: %v", err)
	}
	r := results[0]
	if !r.Result.Degraded {
		t.Fatal("expected degraded result when no evidence")
	}
	if r.Result.DegradedReason != observations.DegradationAbsent {
		t.Errorf("degraded reason = %v, want %v", r.Result.DegradedReason, observations.DegradationAbsent)
	}
	if r.Result.Structured != nil {
		t.Errorf("absent result must not carry structured output, got %v", r.Result.Structured)
	}
}

func eventEnvelope(rec milestone.RawRecord) []byte {
	b, err := json.Marshal(map[string]any{
		"Id":        rec.SourceEventID,
		"Type":      rec.EventType,
		"Source":    rec.SourceDeviceRef,
		"Timestamp": rec.ObservedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		panic(err)
	}
	return b
}

type nilEvidenceSource struct{}

func (s *nilEvidenceSource) EvidenceFor(_ context.Context, _ integration.Event) ([]byte, string, error) {
	return nil, "", nil
}

// stubBookmarkWriter records bookmark placements without reaching a vendor.
type stubBookmarkWriter struct {
	placed []milestone.Bookmark
}

func (s *stubBookmarkWriter) WriteBookmark(_ context.Context, b milestone.Bookmark) (milestone.BookmarkRef, error) {
	s.placed = append(s.placed, b)
	return milestone.BookmarkRef{ID: "bmk-stub-" + b.IdempotencyKey}, nil
}

// testPatternSource generates synthetic test-pattern clip bytes for an event.
type testPatternSource struct{}

func (s *testPatternSource) EvidenceFor(_ context.Context, event integration.Event) ([]byte, string, error) {
	return []byte("SYNTHETIC-TEST-PATTERN-CLIP-" + event.EventID()), "video/mp4", nil
}
