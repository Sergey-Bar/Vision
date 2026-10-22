package operatormetrics_test

// M1 gate 5's ratio.
//
// "The 14-day run records events ingested → canonical events → incidents
// raised, per camera, and the ratio is published in the release notes."
//
// The test asserts the three things a release note depends on: the counts are
// per camera, an incident is attributed to the camera whose event triggered it,
// and a camera with no events has NO rate rather than a rate of zero.

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// canonicalFor writes one canonical event for a named device.
func canonicalFor(t *testing.T, h harness, eventID, deviceID string, at time.Time) {
	t.Helper()
	if _, err := h.pool.WithSites(h.tenant, "test", []string{h.site}).Exec(context.Background(),
		`INSERT INTO canonical_events
			(tenant_id, event_id, site_id, source_system_id, source_event_id, integration_id,
			 event_type, schema_version, observed_at, received_at, normalized_at, device_id,
			 integrity_hash)
		 VALUES ($1, $2, $3, 'sys-canary', $2, 'int-canary', 'motion', '1', $4, $4, $4, $5,
		         repeat('a', 64))`,
		h.tenant, eventID, h.site, at, deviceID); err != nil {
		t.Fatalf("insert the canonical event %s: %v", eventID, err)
	}
}

// TestTheIncidentRateIsPerCamera is the ratio.
//
// Two cameras. The counts are per camera, an incident is attributed to the
// camera whose event triggered it, and the total is the sum of the rows.
func TestTheIncidentRateIsPerCamera(t *testing.T) {
	h := setup(t)
	now := h.now
	// The window ends an hour AFTER the incident, because a half-open window
	// whose end is the incident's own instant excludes it.
	win := operatormetrics.Window{From: now.Add(-14 * 24 * time.Hour), To: now.Add(time.Hour)}

	// Two events on one camera, one on another.
	canonicalFor(t, h, "evt-rate-1", "cam-a", now.Add(-time.Hour))
	canonicalFor(t, h, "evt-rate-2", "cam-a", now.Add(-2*time.Hour))
	canonicalFor(t, h, "evt-rate-3", "cam-b", now.Add(-3*time.Hour))

	// One incident, raised by camera A's first event.
	id := h.raise(t, 2*time.Second)
	if _, err := h.pool.For(h.tenant, "incidents").Exec(ctx(),
		`INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
		 VALUES ($1, $2, $3, 'trigger')`, h.tenant, id, "evt-rate-1"); err != nil {
		t.Fatalf("attribute the incident to a camera: %v", err)
	}

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	report, err := reader.IncidentRate(context.Background(), h.tenant, win)
	if err != nil {
		t.Fatalf("IncidentRate: %v", err)
	}

	if report.Source != "postgres" {
		t.Errorf("the source is %q, want postgres", report.Source)
	}
	if report.Total.CanonicalEvents != 3 {
		t.Errorf("the total counts %d canonical events, want 3", report.Total.CanonicalEvents)
	}
	if report.Total.IncidentsRaised != 1 {
		t.Errorf("the total counts %d incidents, want 1", report.Total.IncidentsRaised)
	}
	if report.Total.Rate == nil {
		t.Fatal("the total has no rate")
	}
	if report.Total.CI == nil {
		t.Error("the total has no interval. A rate without an interval is a number with an " +
			"undisclosed precision")
	}

	// The rows are per camera, and the counts add up to the total.
	byDevice := map[string]operatormetrics.IncidentRateRow{}
	var events int64
	for _, row := range report.Rows {
		byDevice[row.DeviceID] = row
		events += row.CanonicalEvents
	}
	if events != report.Total.CanonicalEvents {
		t.Errorf("the rows count %d canonical events and the total counts %d. The total is the "+
			"sum of the rows", events, report.Total.CanonicalEvents)
	}
	camA, ok := byDevice["cam-a"]
	if !ok {
		t.Fatalf("there is no row for camera A: %v", report.Rows)
	}
	if camA.CanonicalEvents != 2 {
		t.Errorf("camera A counts %d canonical events, want 2", camA.CanonicalEvents)
	}
	if camA.IncidentsRaised != 1 {
		t.Errorf("camera A counts %d incidents, want 1. An incident belongs to the camera whose "+
			"event triggered it", camA.IncidentsRaised)
	}
	camB, ok := byDevice["cam-b"]
	if !ok {
		t.Fatalf("there is no row for camera B: %v", report.Rows)
	}
	if camB.IncidentsRaised != 0 {
		t.Errorf("camera B counts %d incidents, want 0", camB.IncidentsRaised)
	}
}

// TestACameraWithNoEventsHasNoRate is the honest absence.
//
// A camera that produced no canonical events has no rate. A rate of zero would
// read as "this camera never raised an incident", which is a different claim.
func TestACameraWithNoEventsHasNoRate(t *testing.T) {
	h := setup(t)
	now := h.now
	win := operatormetrics.Window{From: now.Add(-time.Hour), To: now.Add(time.Hour)}

	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	report, err := reader.IncidentRate(context.Background(), h.tenant, win)
	if err != nil {
		t.Fatalf("IncidentRate: %v", err)
	}
	if report.Total.Rate != nil {
		t.Errorf("the total rate is %v over a window with no events, want nil. A rate of zero out "+
			"of zero events is not a rate", *report.Total.Rate)
	}
	if report.Total.CI != nil {
		t.Errorf("the total has an interval over a window with no events: %v", report.Total.CI)
	}
}

// TestTheIncidentRateRefusesWhatItCannotAnswer is the refusals.
func TestTheIncidentRateRefusesWhatItCannotAnswer(t *testing.T) {
	h := setup(t)
	reader := &operatormetrics.Reader{Pool: h.pool, Sites: []string{h.site}}
	now := time.Now().UTC()
	if _, err := reader.IncidentRate(context.Background(), "", operatormetrics.Window{
		From: now.Add(-time.Hour), To: now}); err == nil {
		t.Error("the ratio accepted a window with no tenant")
	}
	if _, err := reader.IncidentRate(context.Background(), h.tenant, operatormetrics.Window{
		From: now, To: now}); err == nil {
		t.Error("the ratio accepted an empty window")
	}
}
