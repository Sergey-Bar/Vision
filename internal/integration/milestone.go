// Package integration adapts the Milestone XProtect adapter into the shared
// integration Event interface, and wraps milestone.PlaceBookmark for write-back.
package integration

import (
	"context"
	"errors"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// FromRawRecord maps a milestone RawRecord to an integration Event.
// Simulated() returns false.
func FromRawRecord(rec milestone.RawRecord) (Event, error) {
	if rec.SourceEventID == "" {
		return nil, errors.New("integration: raw record has no source event id")
	}
	return &milestoneEvent{rec: rec}, nil
}

// SimulatedRawRecord builds a milestone.RawRecord carrying synthetic/demo data.
// The caller wraps it with SimulatedEvent to mark Simulated() = true.
func SimulatedRawRecord(eventID, source, logicalCamera string, occurredAt time.Time, eventType, description string) milestone.RawRecord {
	return milestone.RawRecord{
		SourceEventID:   eventID,
		SourceSystemID:  source,
		SourceDeviceRef: logicalCamera,
		ObservedAt:      occurredAt.UTC(),
		EventType:       eventType,
		SourceTypeName:  description,
	}
}

// SimulatedEvent wraps a RawRecord as a simulated Event (Simulated() = true).
func SimulatedEvent(rec milestone.RawRecord) Event {
	return &milestoneEvent{rec: rec, simulated: true}
}

type milestoneEvent struct {
	rec       milestone.RawRecord
	simulated bool
}

func (e *milestoneEvent) EventID() string       { return e.rec.SourceEventID }
func (e *milestoneEvent) Source() string        { return e.rec.SourceSystemID }
func (e *milestoneEvent) LogicalCamera() string { return e.rec.SourceDeviceRef }
func (e *milestoneEvent) OccurredAt() time.Time { return e.rec.ObservedAt.UTC() }
func (e *milestoneEvent) Description() string   { return e.rec.SourceTypeName }
func (e *milestoneEvent) EventType() string     { return e.rec.EventType }
func (e *milestoneEvent) Simulated() bool       { return e.simulated }

// BookmarkRequest builds a milestone.BookmarkRequest from an integration Event
// and a Hafnia result, for write-back to the camera timeline.
//
// The incident id is derived from the event id (retry-safe, no clock). The
// camera reference is the event's logical camera, which must be UUID-shaped
// (cameras/<uuid> or bare uuid) — milestone.parseDevicePath refuses display
// names, so synthetic data must use UUID-shaped refs.
func BookmarkRequest(event Event, siteID string, summary string, at time.Time, preRoll, postRoll time.Duration) milestone.BookmarkRequest {
	windowStart := at.Add(-preRoll)
	windowEnd := at.Add(postRoll)
	return milestone.BookmarkRequest{
		IncidentID:  "inc_" + event.EventID(),
		SiteRef:     event.LogicalCamera(),
		SiteID:      siteID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		PreRoll:     preRoll,
		PostRoll:    postRoll,
		Summary:     summary,
	}
}

// PlaceBookmark wraps milestone.PlaceBookmark for the integration bridge.
// The bookmark is placed only when a BookmarkWriter is available (live
// transport). Without one it reports a refusal rather than silently dropping.
func PlaceBookmark(ctx context.Context, req milestone.BookmarkRequest, w milestone.BookmarkWriter) (milestone.BookmarkDecision, error) {
	if w == nil {
		return milestone.BookmarkDecision{
			Refusal: "no_bookmark_writer: write-back transport does not implement BookmarkWriter",
		}, nil
	}
	return milestone.PlaceBookmark(ctx, req, w, clock.DefaultPolicy())
}
