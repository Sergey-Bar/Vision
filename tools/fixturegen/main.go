// Command fixturegen authors the synthetic fixture set.
//
// The plan requires a fixture set covering motion, analytics, alarm
// created/updated/acknowledged, camera offline/online, bookmark, recording
// state, metadata, malformed, duplicate, delayed, out-of-order, missing camera
// and reconnect/replay, and it requires CI to run with no Milestone server.
//
// These fixtures are authored, not captured. No Milestone installation is
// reachable from this codebase, so a "captured" fixture would be fiction with a
// capture date on it. Every scenario says Source: synthetic, and a test asserts
// that a scenario claiming to be a capture names a real one. When a real capture
// harness runs, real scenarios land beside these and the manifest distinguishes
// them.
//
// The envelopes are shaped like what the Milestone API is assumed to return, and
// every route they claim to come from is still marked Unverified in
// internal/milestone. That flag is the honest boundary of this file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/fixture"
)

// epoch is the instant every scenario's offset zero refers to. It is a constant
// so regenerating the set produces identical content hashes: a fixture set whose
// hashes move on every run cannot be reviewed in a diff.
var epoch = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

const siteID = "site-fixture"
const integrationID = "int-milestone-fixture"

const (
	eventsRoute         = "/api/v1/Events"
	alarmsRoute         = "/api/v1/Alarms"
	bookmarksRoute      = "/api/v1/Bookmarks"
	servletsRoute       = "/api/v1/Servlets"
	resourceGroupsRoute = "/api/v1/ResourceGroups"
)

// spec pairs a scenario with the behaviours it exists to prove. A scenario whose
// purpose is unstated is a scenario nobody notices has stopped testing anything.
type spec struct {
	scenario fixture.Scenario
	covers   []string
}

func main() {
	out := flag.String("out", filepath.Join("testdata", "fixtures"), "directory to write the fixture set into")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "fixturegen: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	store, err := fixture.NewStore(out)
	if err != nil {
		return err
	}

	manifest := fixture.Manifest{
		FormatVersion: fixture.FormatVersion,
		GeneratedAt:   epoch,
		Source:        "synthetic",
		Notes: "Authored from the assumed Milestone API surface. No live Milestone " +
			"server has been reachable, so these are shape fixtures, not captures. " +
			"Replace or supplement them with real captures before claiming " +
			"integration-mode parity.",
	}

	for _, s := range specs() {
		entry, err := store.WriteScenario(s.scenario)
		if err != nil {
			return fmt.Errorf("%s: %w", s.scenario.Name, err)
		}
		entry.Covers = s.covers
		manifest.Scenarios = append(manifest.Scenarios, entry)
	}

	return store.WriteManifest(manifest)
}

func base(name, description string) fixture.Scenario {
	return fixture.Scenario{
		FormatVersion: fixture.FormatVersion,
		Name:          name,
		Description:   description,
		Source:        "synthetic",
		CapturedAt:    epoch,
		Epoch:         epoch,
		SiteID:        siteID,
	}
}

// env builds one event envelope. Marshalling a map[string]any cannot fail, so
// this does not return an error; a fixture that cannot be built is a build-time
// bug, not a runtime condition to propagate through twelve call sites.
func env(kind string, offsetMS int64, body map[string]any) fixture.Event {
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("fixturegen: marshal envelope: %v", err))
	}
	return fixture.Event{
		OffsetMS: offsetMS,
		Kind:     kind,
		Method:   "GET",
		Envelope: raw,
		Headers:  map[string]string{"content-type": "application/json"},
	}
}

func ts(offsetMS int64) string {
	return epoch.Add(time.Duration(offsetMS) * time.Millisecond).Format(time.RFC3339Nano)
}

func specs() []spec {
	return []spec{
		{scenario: motionInRestrictedZone(), covers: []string{"motion", "analytics", "corroboration", "after-hours"}},
		{scenario: analyticsObjectCrossed(), covers: []string{"analytics"}},
		{scenario: alarmLifecycle(), covers: []string{"alarm.created", "alarm.acknowledged", "alarm.closed"}},
		{scenario: cameraOfflineOnline(), covers: []string{"device.camera_offline", "device.camera_online"}},
		{scenario: bookmarkAndMetadata(), covers: []string{"bookmark", "metadata"}},
		{scenario: recordingState(), covers: []string{"recording.started", "recording.stopped"}},
		{scenario: malformedEnvelopes(), covers: []string{"malformed", "unreadable-id", "unreadable-type", "unreadable-time", "unknown-type"}},
		{scenario: duplicateDelivery(), covers: []string{"duplicate"}},
		{scenario: delayedArrival(), covers: []string{"delayed", "ingest-lag"}},
		{scenario: outOfOrderArrival(), covers: []string{"out-of-order"}},
		{scenario: missingCamera(), covers: []string{"missing-camera", "malformed"}},
		{scenario: reconnectReplay(), covers: []string{"reconnect", "replay", "duplicate"}},
	}
}

// motionInRestrictedZone is the one production incident type in M1: a person in
// a restricted zone after hours, with a second independent signal inside the
// time window so corroboration has something to corroborate.
func motionInRestrictedZone() fixture.Scenario {
	sc := base("motion-restricted-zone",
		"Motion in a restricted zone after hours, corroborated by a door sensor.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "mp-000001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]", "Priority": "Normal",
			"Message": "Motion detected in zone Loading Bay",
		}),
		env(eventsRoute, 1500, map[string]any{
			"Id": "mp-000002", "Type": "Analytics", "Timestamp": ts(1500),
			"Source": "Entrance Camera 01 [CAM-001]", "Priority": "Normal",
			"Message":   "Object detected: person",
			"Analytics": map[string]any{"ObjectType": "Person", "Confidence": 0.91},
		}),
		env(servletsRoute, 2100, map[string]any{
			"Id": "sv-0003", "Type": "Servlets.State", "Timestamp": ts(2100),
			"Source": "Loading Bay Door [DOR-004]", "Message": "Door opened",
			"State": "Open",
		}),
	}
	return sc
}

func analyticsObjectCrossed() fixture.Scenario {
	sc := base("analytics-object-crossed",
		"Analytics line-crossing and loitering on an unrestricted camera.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "mp-000010", "Type": "Analytics", "Timestamp": ts(0),
			"Source": "Corridor Camera 07 [CAM-007]", "Priority": "Normal",
			"Message":   "Line crossed",
			"Analytics": map[string]any{"ObjectType": "Person", "AnalyticsType": "LineCrossing"},
		}),
		env(eventsRoute, 40000, map[string]any{
			"Id": "mp-000011", "Type": "Analytics", "Timestamp": ts(40000),
			"Source": "Corridor Camera 07 [CAM-007]", "Priority": "Normal",
			"Message":   "Loitering detected",
			"Analytics": map[string]any{"ObjectType": "Person", "AnalyticsType": "Loitering"},
		}),
	}
	return sc
}

// alarmLifecycle is one scenario for three states, because the behaviour that
// matters is the transition, not the individual states.
func alarmLifecycle() fixture.Scenario {
	sc := base("alarm-lifecycle",
		"An alarm created, then acknowledged, then closed, as three feed entries.")
	sc.Events = []fixture.Event{
		env(alarmsRoute, 0, map[string]any{
			"Id": "al-0001", "Type": "Alarm", "Timestamp": ts(0),
			"Source": "Perimeter North [CAM-014]", "Priority": "High",
			"Message": "Perimeter intrusion alarm", "AlarmState": "Created",
			"Acknowledged": false,
		}),
		env(alarmsRoute, 30000, map[string]any{
			"Id": "al-0001", "Type": "Alarm", "Timestamp": ts(30000),
			"Source": "Perimeter North [CAM-014]", "Priority": "High",
			"Message": "Perimeter intrusion alarm", "AlarmState": "Acknowledged",
			"Acknowledged": true, "AcknowledgedBy": "operator@example.invalid",
		}),
		env(alarmsRoute, 120000, map[string]any{
			"Id": "al-0001", "Type": "Alarm", "Timestamp": ts(120000),
			"Source": "Perimeter North [CAM-014]", "Priority": "High",
			"Message": "Perimeter intrusion alarm", "AlarmState": "Closed",
			"Acknowledged": true,
		}),
	}
	return sc
}

func cameraOfflineOnline() fixture.Scenario {
	sc := base("camera-offline-online",
		"A camera going offline and returning, which is both a device-health signal "+
			"the graph records and a clock reference the drift subsystem re-measures.")
	sc.Events = []fixture.Event{
		env(servletsRoute, 0, map[string]any{
			"Id": "sv-0001", "Type": "Servlets.State", "Timestamp": ts(0),
			"Source": "Dock Camera 22 [CAM-022]", "State": "Offline",
		}),
		env(servletsRoute, 90000, map[string]any{
			"Id": "sv-0002", "Type": "Servlets.State", "Timestamp": ts(90000),
			"Source": "Dock Camera 22 [CAM-022]", "State": "Online",
		}),
	}
	return sc
}

func bookmarkAndMetadata() fixture.Scenario {
	sc := base("bookmark-and-metadata",
		"A bookmark and a metadata event, which are the two evidence-fetch triggers.")
	sc.Events = []fixture.Event{
		env(bookmarksRoute, 0, map[string]any{
			"Id": "bm-0001", "Type": "Bookmark", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]", "Message": "Contractor arrival",
		}),
		env(eventsRoute, 500, map[string]any{
			"Id": "md-0001", "Type": "Metadata", "Timestamp": ts(500),
			"Source": "Gate 1 [GAT-001]", "Message": "Vehicle plate read",
			"Metadata": map[string]any{"Plate": "XXXXX123", "Direction": "Inbound"},
		}),
	}
	return sc
}

func recordingState() fixture.Scenario {
	sc := base("recording-state",
		"Recording starting and stopping, which evidence retention depends on.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "rc-0001", "Type": "RecordingStarted", "Timestamp": ts(0),
			"Source": "Yard Camera 31 [CAM-031]",
		}),
		env(eventsRoute, 60000, map[string]any{
			"Id": "rc-0002", "Type": "RecordingStopped", "Timestamp": ts(60000),
			"Source": "Yard Camera 31 [CAM-031]",
		}),
	}
	return sc
}

// malformedEnvelopes covers four ways a source can be unreadable. Each is still
// valid JSON with a correct envelope: a file that will not parse at all is
// indistinguishable from a typo, and a scenario that silently stops testing is
// worse than one that fails loudly.
func malformedEnvelopes() fixture.Scenario {
	sc := base("malformed-envelopes",
		"Envelopes that are readable JSON but break the assumed field contract: no "+
			"id, no type, an unparseable timestamp, and a type nobody has seen.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Type": "Motion", "Timestamp": ts(0), "Source": "No Id Camera",
		}),
		env(eventsRoute, 100, map[string]any{
			"Id": "ml-0002", "Timestamp": ts(100), "Source": "No Type Camera",
		}),
		env(eventsRoute, 200, map[string]any{
			"Id": "ml-0003", "Type": "Motion", "Timestamp": "not-a-timestamp",
			"Source": "Bad Clock Camera",
		}),
		env(eventsRoute, 300, map[string]any{
			"Id": "ml-0004", "Type": "QuantumFlux", "Timestamp": ts(300),
			"Source": "Future Camera",
		}),
	}
	return sc
}

// duplicateDelivery is the same source id twice, as a reconnect produces.
func duplicateDelivery() fixture.Scenario {
	sc := base("duplicate-delivery",
		"One source event delivered twice, which the inbox must turn into a single "+
			"canonical event and no second side effect.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "dup-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
		env(eventsRoute, 2000, map[string]any{
			"Id": "dup-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
	}
	return sc
}

// delayedArrival is an event the platform sees 45 seconds after the source says
// it happened. Ingest lag is a metric, and past the site bound correlation must
// not silently join two events an operator would call unrelated.
func delayedArrival() fixture.Scenario {
	sc := base("delayed-arrival",
		"An event whose observed time precedes its arrival by 45 seconds.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 45000, map[string]any{
			"Id": "dl-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
	}
	return sc
}

// outOfOrderArrival delivers the later event first. Correlation is driven by
// normalized_at and never by arrival order, and this is the fixture that proves
// it rather than asserting it.
func outOfOrderArrival() fixture.Scenario {
	sc := base("out-of-order-arrival",
		"Two events delivered newest-first, so arrival order contradicts observation order.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "oo-0002", "Type": "Motion", "Timestamp": ts(20000),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
		env(eventsRoute, 100, map[string]any{
			"Id": "oo-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
	}
	return sc
}

// missingCamera names a device the platform never registered. It must become a
// malformed record with a reason, not a canonical event pointing at a device that
// does not exist.
func missingCamera() fixture.Scenario {
	sc := base("missing-camera",
		"An event from a camera id that was never registered on this platform.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "mc-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Ghost Camera 99 [CAM-999]",
		}),
	}
	return sc
}

// reconnectReplay re-serves the previous window, as happens when a reconnect
// loses the cursor. Every id repeats and none may be re-ingested.
func reconnectReplay() fixture.Scenario {
	sc := base("reconnect-replay",
		"A reconnect that re-serves the previous window, duplicating every id.")
	sc.Events = []fixture.Event{
		env(eventsRoute, 0, map[string]any{
			"Id": "rr-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
		env(eventsRoute, 800, map[string]any{
			"Id": "rr-0002", "Type": "Analytics", "Timestamp": ts(800),
			"Source":    "Entrance Camera 01 [CAM-001]",
			"Analytics": map[string]any{"ObjectType": "Person"},
		}),
		env(eventsRoute, 20000, map[string]any{
			"Id": "rr-0001", "Type": "Motion", "Timestamp": ts(0),
			"Source": "Entrance Camera 01 [CAM-001]",
		}),
		env(eventsRoute, 20800, map[string]any{
			"Id": "rr-0002", "Type": "Analytics", "Timestamp": ts(800),
			"Source":    "Entrance Camera 01 [CAM-001]",
			"Analytics": map[string]any{"ObjectType": "Person"},
		}),
	}
	return sc
}
