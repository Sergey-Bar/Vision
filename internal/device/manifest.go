// Package device holds the device registry and the measured capability
// manifest that goes with it.
//
// # The one rule this package exists to enforce
//
// **A capability without a probe result is not a capability.**
//
// A camera names the profiles it supports. A camera is routinely wrong about
// that: it advertises a Profile M it does not implement, or an analytics
// engine that returns nothing, or a PTZ it cannot do in the dark. A product
// that believes a device's own claims publishes a capability report that is
// fiction, and the first customer who finds out is the customer who trusted
// it.
//
// So the registry records what a PROBE established, per device, with the
// probe's name and the time next to it, and every consumer of a capability
// reads the `verified` entries only. A device nobody has probed has an empty
// capability set, and an empty capability set is an honest answer rather than
// a missing one.
//
// This is the same discipline `internal/milestone` applies to a vendor's
// routes, and it is copied rather than invented: the flag there is a field
// rather than a comment, so it cannot rot, and a human has to take it off.
//
// # The states
//
// The vocabulary is closed and it is four words, and the published JSON Schema
// carries the same four:
//
//	unverified   claimed by the device, never checked
//	verified     a probe established it. The only state that counts
//	contradicted a probe established the device does NOT have it
//	unsupported  a probe established the device does not have it and does
//	             not claim it
//
// `contradicted` and `unsupported` are separate on purpose. The first is a
// finding — the device lied, or the profile exists but the implementation
// does not — and the second is an absence. A report that merges them loses
// the only information an operator can act on.
//
// # What this package does not do
//
// It does not talk to cameras. `internal/onvif` does that, and it produces a
// `Manifest`; this package holds one, validates it, and reads it back.
//
// It does not hold a secret. A device's credential is a REFERENCE into the
// keystore, and the reference is a pointer rather than the secret, because a
// camera's default password is the single most valuable secret a physical
// security product holds.
package device

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// State is the state of one measured capability.
type State string

// The four states. The set is closed: a fifth word is a vocabulary change, and
// a vocabulary change is a contract change.
const (
	// StateUnverified is what the device claimed and nobody checked.
	StateUnverified State = "unverified"
	// StateVerified is what a probe established. The only state that counts.
	StateVerified State = "verified"
	// StateContradicted is what a probe found when the device said otherwise.
	StateContradicted State = "contradicted"
	// StateUnsupported is what a probe found when the device did not claim it.
	StateUnsupported State = "unsupported"
)

// States is the closed vocabulary, in the order the published schema lists it.
var States = []State{
	StateUnverified,
	StateVerified,
	StateContradicted,
	StateUnsupported,
}

// knownState reports whether a state is one of the four.
func knownState(s State) bool {
	for _, candidate := range States {
		if candidate == s {
			return true
		}
	}
	return false
}

// Capability is one capability of one device, and the probe that decided it.
type Capability struct {
	// Name is the capability's name in `namespace.verb` form:
	// `onvif.profile_m`, `rtsp.tcp_interleaved`, `ptz.absolute`.
	Name string
	// State is what the probe decided.
	State State
	// Probe is what established the state. A verified capability with no
	// probe behind it is a claim, and `Validate` refuses it.
	Probe string
	// At is when the probe observed it.
	At time.Time
	// Detail is the probe's own words: the token, the status, the reason.
	Detail string
}

// Manifest is what a probe established about one device.
//
// It is read whole or not at all. A consumer needs the whole set to decide
// what to enable, and half a manifest is not a smaller manifest — it is a
// wrong one.
type Manifest struct {
	TenantID string
	SiteID   string
	DeviceID string
	// ProbeID names the probe run that produced this manifest. It is what a
	// reader checks a capability against, and it is what makes the manifest
	// falsifiable rather than merely present.
	ProbeID string
	// ProbedAt is when the probe ran. A manifest with no probe time is a
	// claim.
	ProbedAt time.Time
	// Make, Model and Firmware are the device's own identity, as the probe
	// read it. They are never a capability claim: a model name is not a
	// measurement.
	Make     string
	Model    string
	Firmware string
	// ONVIFDeviceID is the identifier the device reports for itself. It is not
	// a tenant-scoped key and it is not unique across sites.
	ONVIFDeviceID string
	// Capabilities is the measured set.
	Capabilities []Capability
}

// The refusals. They are named so that a caller can say which rule it broke,
// and so that a test can assert on the rule rather than on a message.
var (
	// ErrNoTenant is a manifest with no tenant.
	ErrNoTenant = errors.New("device: the manifest has no tenant")
	// ErrNoSite is a manifest with no site.
	ErrNoSite = errors.New("device: the manifest has no site")
	// ErrNoDevice is a manifest with no device.
	ErrNoDevice = errors.New("device: the manifest has no device")
	// ErrNoProbeID is a manifest with no probe behind it.
	ErrNoProbeID = errors.New("device: the manifest names no probe")
	// ErrNoProbeTime is a manifest that was never probed.
	ErrNoProbeTime = errors.New("device: the manifest has no probe time")
	// ErrCapabilityWithoutProbe is a verified capability with nothing behind
	// it. This is the refusal the package exists for.
	ErrCapabilityWithoutProbe = errors.New("device: a verified capability has no probe behind it")
	// ErrCapabilityWithoutName is a capability with no name.
	ErrCapabilityWithoutName = errors.New("device: a capability has no name")
	// ErrUnknownState is a state outside the closed vocabulary.
	ErrUnknownState = errors.New("device: a capability carries a state outside the closed vocabulary")
)

// Validate checks the manifest against the rules that make it falsifiable.
//
// The order is deliberate: the identity of the manifest is checked before its
// contents, so a manifest with no device says so rather than complaining
// about a capability three messages later.
func (m Manifest) Validate() error {
	if m.TenantID == "" {
		return ErrNoTenant
	}
	if m.SiteID == "" {
		return ErrNoSite
	}
	if m.DeviceID == "" {
		return ErrNoDevice
	}
	if m.ProbeID == "" {
		return ErrNoProbeID
	}
	if m.ProbedAt.IsZero() {
		return ErrNoProbeTime
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if c.Name == "" {
			return ErrCapabilityWithoutName
		}
		if !knownState(c.State) {
			return fmt.Errorf("%w: %q on %s", ErrUnknownState, c.State, c.Name)
		}
		if c.State == StateVerified && c.Probe == "" {
			return fmt.Errorf("%w: %s", ErrCapabilityWithoutProbe, c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("device: the manifest names %s twice", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

// NewManifest validates a manifest and returns it.
//
// It returns the manifest rather than a pointer so that a caller cannot hold a
// manifest that was never checked: the value that comes out of here has been
// through `Validate`.
func NewManifest(m Manifest) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Claimed returns the names of the capabilities that were VERIFIED, sorted.
//
// Sorted because a caller that renders them must not render them in map
// order, and two readers of the same manifest must see the same list.
func (m Manifest) Claimed() []string {
	var out []string
	for _, c := range m.Capabilities {
		if c.State == StateVerified {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Supports reports whether a capability was verified.
//
// This is the question the product actually asks. A capability in any other
// state is not a capability, and asking about it returns false rather than
// the state, because a caller that gets a state has to know what to do with
// it, and the answer is always the same: not this one.
func (m Manifest) Supports(name string) bool {
	for _, c := range m.Capabilities {
		if c.Name == name {
			return c.State == StateVerified
		}
	}
	return false
}

// StateOf returns the state of one capability, and whether it was present at
// all.
//
// The second value matters: a capability that is absent and a capability that
// is present-and-unverified are different facts, and a report that merges them
// cannot say whether a device was asked.
func (m Manifest) StateOf(name string) (State, bool) {
	for _, c := range m.Capabilities {
		if c.Name == name {
			return c.State, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The wire shape
// ---------------------------------------------------------------------------

// The published shape is `contracts/json-schema/capability-manifest.schema.json`.
// The names below are the schema's names, not Go's, and they are written out
// rather than derived: a JSON tag that drifts from the schema is a manifest
// that validates against nothing.

// wireManifest is the schema's shape.
type wireManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	TenantID      string           `json:"tenantId"`
	SiteID        string           `json:"siteId"`
	DeviceID      string           `json:"deviceId"`
	ProbeID       string           `json:"probeId"`
	ProbedAt      string           `json:"probedAt"`
	Make          string           `json:"make,omitempty"`
	Model         string           `json:"model,omitempty"`
	Firmware      string           `json:"firmware,omitempty"`
	ONVIFDeviceID string           `json:"onvifDeviceId,omitempty"`
	Capabilities  []wireCapability `json:"capabilities"`
}

// wireCapability is the schema's capability shape.
type wireCapability struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Probe      string `json:"probe,omitempty"`
	ObservedAt string `json:"observedAt,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// SchemaVersion is the version of the published manifest shape this package
// writes.
const SchemaVersion = 1

// MarshalJSON writes the manifest in the published shape.
func (m Manifest) MarshalJSON() ([]byte, error) {
	w := wireManifest{
		SchemaVersion: SchemaVersion,
		TenantID:      m.TenantID,
		SiteID:        m.SiteID,
		DeviceID:      m.DeviceID,
		ProbeID:       m.ProbeID,
		ProbedAt:      m.ProbedAt.UTC().Format(time.RFC3339),
		Make:          m.Make,
		Model:         m.Model,
		Firmware:      m.Firmware,
		ONVIFDeviceID: m.ONVIFDeviceID,
		Capabilities:  make([]wireCapability, 0, len(m.Capabilities)),
	}
	for _, c := range m.Capabilities {
		item := wireCapability{
			Name:   c.Name,
			State:  string(c.State),
			Probe:  c.Probe,
			Detail: c.Detail,
		}
		if !c.At.IsZero() {
			item.ObservedAt = c.At.UTC().Format(time.RFC3339)
		}
		w.Capabilities = append(w.Capabilities, item)
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads a manifest in the published shape.
//
// The result is NOT validated. A caller that reads a manifest from a database
// or from a wire wants to see what is there, and the validation lives in
// `Validate` so that a reader can choose to run it. `ReadManifest` runs it
// for you.
func (m *Manifest) UnmarshalJSON(raw []byte) error {
	var w wireManifest
	if err := json.Unmarshal(raw, &w); err != nil {
		return fmt.Errorf("device: read the manifest: %w", err)
	}
	m.TenantID = w.TenantID
	m.SiteID = w.SiteID
	m.DeviceID = w.DeviceID
	m.ProbeID = w.ProbeID
	m.Make = w.Make
	m.Model = w.Model
	m.Firmware = w.Firmware
	m.ONVIFDeviceID = w.ONVIFDeviceID
	m.ProbedAt, _ = time.Parse(time.RFC3339, w.ProbedAt)
	m.Capabilities = nil
	for _, c := range w.Capabilities {
		capability := Capability{
			Name:   c.Name,
			State:  State(c.State),
			Probe:  c.Probe,
			Detail: c.Detail,
		}
		if c.ObservedAt != "" {
			capability.At, _ = time.Parse(time.RFC3339, c.ObservedAt)
		}
		m.Capabilities = append(m.Capabilities, capability)
	}
	return nil
}
