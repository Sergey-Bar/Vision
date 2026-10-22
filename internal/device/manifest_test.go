package device_test

// The capability manifest's rules, and the one of them that matters.
//
// # Why these tests are about ONE rule
//
// The manifest exists so that a product can say "this camera does analytics"
// and mean that a probe established it. The single way that promise breaks is
// a capability that says "verified" with nothing behind it — a string in a
// column, set by a code path that trusted a device's own advertisement.
//
// So the refusal is the control, and the rest of the file is the shape around
// it: the closed vocabulary, the round trip through the published schema, and
// the difference between "we did not look" and "we looked and there was
// nothing".

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Sergey-Bar/vision/internal/contracts/schemasync"
	"github.com/Sergey-Bar/vision/internal/device"
)

func probeTime() time.Time {
	return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
}

// aManifest is a valid manifest with one verified capability, used as the base
// the tests vary.
func aManifest() device.Manifest {
	return device.Manifest{
		TenantID: "tenant-a",
		SiteID:   "site-a",
		DeviceID: "device-a",
		ProbeID:  "probe-1",
		ProbedAt: probeTime(),
		Capabilities: []device.Capability{
			{
				Name:   "onvif.profile_s",
				State:  device.StateVerified,
				Probe:  "probe-1",
				At:     probeTime(),
				Detail: "profile \"main\" carries a video encoder configuration",
			},
		},
	}
}

// TestAVerifiedCapabilityWithNoProbeIsRefused is the control.
//
// A capability whose state is "verified" and which names no probe is a claim
// wearing a measurement's clothes. The manifest must refuse it, and it must
// refuse it by name so that a caller can say which rule it broke.
func TestAVerifiedCapabilityWithNoProbeIsRefused(t *testing.T) {
	m := aManifest()
	m.Capabilities[0].Probe = ""

	err := m.Validate()
	if !errors.Is(err, device.ErrCapabilityWithoutProbe) {
		t.Fatalf("a verified capability with no probe was accepted (error was %v). "+
			"The manifest is the only thing that separates a measured capability "+
			"from a device's own advertisement, and this is the case that "+
			"separates them.", err)
	}
}

// TestTheManifestRefusesAManifestWithNoProbeBehindIt is the same rule at the
// manifest's own level.
//
// A manifest with no probe id has nothing to check its capabilities against,
// so every capability in it is a claim.
func TestTheManifestRefusesAManifestWithNoProbeBehindIt(t *testing.T) {
	m := aManifest()
	m.ProbeID = ""

	if err := m.Validate(); !errors.Is(err, device.ErrNoProbeID) {
		t.Fatalf("a manifest with no probe id was accepted (error was %v). A manifest "+
			"that names no probe is a set of claims with a timestamp on them.", err)
	}
}

// TestTheClaimedSetIsOnlyTheVerifiedCapabilities is the other half.
//
// A capability the device CLAIMED is not a capability, and a product that
// offers a feature because a camera advertised it is the failure this whole
// package exists to prevent.
func TestTheClaimedSetIsOnlyTheVerifiedCapabilities(t *testing.T) {
	m := aManifest()
	m.Capabilities = append(m.Capabilities,
		device.Capability{
			Name:   "onvif.profile_m",
			State:  device.StateUnverified,
			Probe:  "probe-1",
			At:     probeTime(),
			Detail: "the device says so",
		},
		device.Capability{
			Name:   "onvif.analytics",
			State:  device.StateContradicted,
			Probe:  "probe-1",
			At:     probeTime(),
			Detail: "the device names the analytics service and does not implement it",
		},
		device.Capability{
			Name:   "onvif.events",
			State:  device.StateUnsupported,
			Probe:  "probe-1",
			At:     probeTime(),
			Detail: "no events service",
		},
	)

	claimed := m.Claimed()
	if len(claimed) != 1 || claimed[0] != "onvif.profile_s" {
		t.Fatalf("the claimed set is %v; it should hold only the verified capability. "+
			"A capability the device claimed, or one a probe contradicted, is not a "+
			"capability the product may offer.", claimed)
	}
	if m.Supports("onvif.profile_m") {
		t.Error("the manifest claims a capability the device only asserted")
	}
	if !m.Supports("onvif.profile_s") {
		t.Error("the manifest does not claim the capability a probe established")
	}
}

// TestAManifestRefusesAStateOutsideTheClosedVocabulary keeps the vocabulary
// closed.
//
// A fifth state is a vocabulary change, and a vocabulary change is a contract
// change. The gate that stops it is here.
func TestAManifestRefusesAStateOutsideTheClosedVocabulary(t *testing.T) {
	m := aManifest()
	m.Capabilities[0].State = "probably"

	if err := m.Validate(); !errors.Is(err, device.ErrUnknownState) {
		t.Fatalf("a capability in a state outside the closed vocabulary was accepted "+
			"(error was %v). The vocabulary is four words and a fifth one is a contract "+
			"change that nobody made.", err)
	}
}

// TestAManifestRefusesTheSameCapabilityTwice keeps the manifest a SET.
//
// Two entries for one capability mean two answers to "does this device do
// this?", and a reader that takes the first one is reading whichever one the
// order happened to produce.
func TestAManifestRefusesTheSameCapabilityTwice(t *testing.T) {
	m := aManifest()
	m.Capabilities = append(m.Capabilities, m.Capabilities[0])

	if err := m.Validate(); err == nil {
		t.Fatal("a manifest naming the same capability twice was accepted")
	}
}

// TestTheManifestRoundTripsThroughThePublishedShape keeps the Go type and the
// published schema the same shape.
//
// The schema is the contract; the Go type is how the product holds one. If
// they drift, a manifest written by the product validates against nothing and
// a client reading the contract sees fields the product does not have.
func TestTheManifestRoundTripsThroughThePublishedShape(t *testing.T) {
	m := aManifest()
	m.Make = "Acme"
	m.Model = "Cam 9000"
	m.Firmware = "1.4.2"
	m.ONVIFDeviceID = "SN-0001"

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	var back device.Manifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	if back.TenantID != m.TenantID || back.SiteID != m.SiteID || back.DeviceID != m.DeviceID {
		t.Fatalf("the round trip lost the manifest's identity: %+v", back)
	}
	if back.ProbeID != m.ProbeID || !back.ProbedAt.Equal(m.ProbedAt) {
		t.Fatalf("the round trip lost the probe: %+v", back)
	}
	if back.Make != m.Make || back.Model != m.Model || back.Firmware != m.Firmware {
		t.Fatalf("the round trip lost the device's identity: %+v", back)
	}
	if len(back.Capabilities) != 1 || back.Capabilities[0].State != device.StateVerified {
		t.Fatalf("the round trip lost the capabilities: %+v", back.Capabilities)
	}
	if back.Capabilities[0].Probe != m.Capabilities[0].Probe {
		t.Fatalf("the round trip lost the probe behind the capability: %+v", back.Capabilities[0])
	}
}

// TestThePublishedShapeAcceptsAManifest validates a manifest against the
// published JSON Schema.
//
// The schema is the wire contract and the Go type is what the product holds,
// and the two only agree if something checks. This is that something.
func TestThePublishedShapeAcceptsAManifest(t *testing.T) {
	schemaBytes, err := schemasync.Bytes("capability-manifest.schema.json")
	if err != nil {
		t.Fatalf("read the published capability manifest schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		t.Fatalf("parse the published capability manifest schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("capability-manifest.schema.json", doc); err != nil {
		t.Fatalf("compile the published capability manifest schema: %v", err)
	}
	schema, err := compiler.Compile("capability-manifest.schema.json")
	if err != nil {
		t.Fatalf("compile the published capability manifest schema: %v", err)
	}

	m := aManifest()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	if err := schema.Validate(decoded); err != nil {
		t.Fatalf("the published schema refused a manifest the product writes: %v", err)
	}
}

// TestThePublishedShapeRefusesAManifestWithNoProbeID keeps the schema and the
// Go rule in step.
//
// The Go type refuses a manifest with no probe id, and the schema has to
// refuse it too — otherwise a manifest written by another implementation
// would pass the contract and fail the product.
func TestThePublishedShapeRefusesAManifestWithNoProbeID(t *testing.T) {
	schemaBytes, err := schemasync.Bytes("capability-manifest.schema.json")
	if err != nil {
		t.Fatalf("read the published capability manifest schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		t.Fatalf("parse the published capability manifest schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("capability-manifest.schema.json", doc); err != nil {
		t.Fatalf("compile the published capability manifest schema: %v", err)
	}
	schema, err := compiler.Compile("capability-manifest.schema.json")
	if err != nil {
		t.Fatalf("compile the published capability manifest schema: %v", err)
	}

	raw := []byte(`{"schemaVersion":1,"tenantId":"t","siteId":"s","deviceId":"d",` +
		`"probedAt":"2026-10-21T12:00:00Z","capabilities":[]}`)
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	if err := schema.Validate(decoded); err == nil {
		t.Fatal("the published schema accepted a manifest with no probe id, while the " +
			"product refuses one. A schema that is looser than the code is a contract " +
			"that permits a thing the product will not hold.")
	}
}
