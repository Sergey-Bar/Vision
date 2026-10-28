package integration_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/integration"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

func TestFromRawRecordMapsAllFields(t *testing.T) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := milestone.RawRecord{
		SourceEventID:   "evt-123",
		SourceSystemID:  "xprotect-hq",
		SourceDeviceRef: "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		ObservedAt:      occurred,
		EventType:       integration.EventTypeMotion,
		SourceTypeName:  "Motion detected at entrance",
	}

	event, err := integration.FromRawRecord(rec)
	if err != nil {
		t.Fatalf("FromRawRecord: %v", err)
	}

	if got := event.EventID(); got != "evt-123" {
		t.Errorf("EventID() = %q, want %q", got, "evt-123")
	}
	if got := event.Source(); got != "xprotect-hq" {
		t.Errorf("Source() = %q, want %q", got, "xprotect-hq")
	}
	if got := event.LogicalCamera(); got != "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c" {
		t.Errorf("LogicalCamera() = %q, want cameras/<uuid>", got)
	}
	if got := event.OccurredAt(); !got.Equal(occurred) {
		t.Errorf("OccurredAt() = %v, want %v", got, occurred)
	}
	if got := event.Description(); got != "Motion detected at entrance" {
		t.Errorf("Description() = %q, want %q", got, "Motion detected at entrance")
	}
	if got := event.EventType(); got != integration.EventTypeMotion {
		t.Errorf("EventType() = %q, want %q", got, integration.EventTypeMotion)
	}
	if got := event.Simulated(); got {
		t.Errorf("Simulated() = true, want false for a real record")
	}
}

func TestFromRawRecordRejectsEmptyEventID(t *testing.T) {
	_, err := integration.FromRawRecord(milestone.RawRecord{})
	if err == nil {
		t.Error("expected error for raw record with no source event id")
	}
}

func TestSimulatedEventMarksSimulated(t *testing.T) {
	rec := integration.SimulatedRawRecord(
		"evt-sim-1", "xprotect-demo", "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
		time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		integration.EventTypeMotion, "synthetic motion",
	)
	event := integration.SimulatedEvent(rec)
	if !event.Simulated() {
		t.Error("SimulatedEvent should mark Simulated() = true")
	}
	if got := event.EventID(); got != "evt-sim-1" {
		t.Errorf("EventID() = %q, want %q", got, "evt-sim-1")
	}
	if got := event.LogicalCamera(); got != "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c" {
		t.Errorf("LogicalCamera() = %q", got)
	}
}

func TestSimulatedRawRecordBuildsRawRecord(t *testing.T) {
	occurred := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := integration.SimulatedRawRecord(
		"evt-sim-2", "xprotect-demo", "cameras/11111111-1111-1111-1111-111111111111",
		occurred, integration.EventTypeRestrictedPerson, "restricted person",
	)
	if rec.SourceEventID != "evt-sim-2" {
		t.Errorf("SourceEventID = %q", rec.SourceEventID)
	}
	if rec.SourceDeviceRef != "cameras/11111111-1111-1111-1111-111111111111" {
		t.Errorf("SourceDeviceRef = %q", rec.SourceDeviceRef)
	}
	if rec.EventType != integration.EventTypeRestrictedPerson {
		t.Errorf("EventType = %q", rec.EventType)
	}
	if !rec.ObservedAt.Equal(occurred) {
		t.Errorf("ObservedAt = %v", rec.ObservedAt)
	}
}
