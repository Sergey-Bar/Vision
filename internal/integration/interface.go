// Package integration bridges Milestone XProtect and Hafnia VLM for one shared
// test scenario: XProtect motion/analytics event → clip evidence → Hafnia VLM
// answer → Milestone bookmark on the camera timeline.
//
// The shared interface is a Go interface plus the doc at SHARED_INTERFACE_EN.md.
// It is deliberately not a protobuf or wire change: the two adapters already
// exist (internal/milestone, internal/hafnia) and this package is the seam
// between them.
//
// Tenant and site ids are explicit parameters — never read from a request body
// (invariant 5). milestone.Adapter.tenant() returns "", so they come from the
// caller/session.
package integration

import "time"

// Event is the minimal shared shape both adapters understand.
type Event interface {
	// EventID is the source's own id for the event.
	EventID() string
	// Source is the source system that produced it.
	Source() string
	// LogicalCamera is the camera the event is about.
	LogicalCamera() string
	// OccurredAt is the UTC instant the source claimed.
	OccurredAt() time.Time
	// Description is a human-readable description of what happened.
	Description() string
	// EventType is one of the closed event-type vocabulary below.
	EventType() string
	// Simulated is true when the input is synthetic/manual test data.
	Simulated() bool
}

// Closed event-type vocabulary, mirroring milestone.NormalizedEventTypes()
// (normalized names, not vendor spellings).
const (
	EventTypeMotion            = "motion"
	EventTypeAnalytics         = "analytics"
	EventTypeRestrictedPerson  = "analytics.restricted_person"
	EventTypePersonWithBag     = "analytics.person_with_bag"
	EventTypeVehicle           = "analytics.vehicle"
	EventTypeAnimal            = "analytics.animal"
	EventTypeAlarmCreated      = "alarm.created"
	EventTypeAlarmAcknowledged = "alarm.acknowledged"
	EventTypeAlarmClosed       = "alarm.closed"
	EventTypeCameraOffline     = "device.camera_offline"
	EventTypeCameraOnline      = "device.camera_online"
	EventTypeRecordingStarted  = "recording.started"
	EventTypeRecordingStopped  = "recording.stopped"
	EventTypeBookmark          = "bookmark"
	EventTypeMetadata          = "metadata"
	EventTypeAccessGranted     = "access.granted"
	EventTypeAccessDenied      = "access.denied"
	EventTypeGate              = "gate"
)
