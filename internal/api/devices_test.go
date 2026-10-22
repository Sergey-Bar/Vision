package api_test

// The device registry, at the edge.
//
// The discipline the whole device package exists for: a camera NAMES the
// profiles it supports and is routinely wrong about it, so a capability that
// was not MEASURED is not a capability. The tests here are about that rule
// surviving the boundary — a registration cannot set a capability, a probe
// records one, and a device that was never probed says so by name.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/device"
)

// aProber answers with a fixed manifest, or with an error.
//
// It stamps the tenant, the site and the device onto the manifest it returns,
// because that is what a real prober does: the measurement is about the
// device the caller named, and a manifest that named a different one would
// be recorded against the wrong row.
type aProber struct {
	manifest device.Manifest
	err      error
	calls    int
	ref      device.ProbeRef
}

func (p *aProber) Probe(ctx context.Context, ref device.ProbeRef) (device.Manifest, error) {
	p.calls++
	p.ref = ref
	if p.err != nil {
		return device.Manifest{}, p.err
	}
	m := p.manifest
	m.TenantID = ref.TenantID
	m.SiteID = ref.SiteID
	m.DeviceID = ref.DeviceID
	return m, nil
}

func deviceServer(t *testing.T, prober device.Prober) (*api.Server, string, string) {
	t.Helper()
	s, tenant, site, _ := serverWithRegistry(t, nil, func(d *api.Deps) {
		d.Devices = &device.Service{Pool: adminPool(t)}
		d.DeviceProber = prober
	})
	return s, tenant, site
}

func registerDevice(t *testing.T, s *api.Server) {
	t.Helper()
	body := map[string]any{"deviceId": "cam-1", "address": "192.0.2.10", "kind": "camera"}
	rec := call(t, s, http.MethodPost, "/v1/devices", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register cam-1: got %d: %s", rec.Code, rec.Body)
	}
}

func TestTheDeviceListPublishesTheClosedVocabularies(t *testing.T) {
	s, _, _ := deviceServer(t, nil)

	rec := call(t, s, http.MethodGet, "/v1/devices", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the devices: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	// The vocabularies are published so a reader knows the whole set rather
	// than only the members this install has met. A report that only names
	// the capabilities it has seen is a report about the past.
	vocab, _ := body["vocabulary"].(map[string]any)
	if vocab == nil {
		t.Fatalf("the device list publishes no vocabulary: %s", rec.Body)
	}
	for _, key := range []string{"credentialPurposes", "capabilityStates", "onvifCapabilities", "onvifProfiles"} {
		if _, ok := vocab[key]; !ok {
			t.Errorf("the vocabulary does not publish %q: %s", key, rec.Body)
		}
	}
}

func TestARegisteredDeviceThatWasNeverProbedSaysSo(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	registerDevice(t, s)

	rec := call(t, s, http.MethodGet, "/v1/devices", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the devices: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	var first map[string]any
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["deviceId"] == "cam-1" {
			first = row
			break
		}
	}
	if first == nil {
		t.Fatalf("the registry does not hold cam-1: %s", rec.Body)
	}
	if first["probed"] != false {
		t.Errorf("a device that was never probed reports probed:%v. An empty capability list reads as 'we looked and it can do nothing', which is a different fact", first["probed"])
	}
	caps, _ := first["capabilities"].([]any)
	if len(caps) != 0 {
		t.Errorf("a device that was never probed carries %d capability/capabilities: %v", len(caps), caps)
	}
}

func TestARegistrationCannotSetACapability(t *testing.T) {
	// The registration body has no capability field, and the Go decoder
	// refuses unknown fields, so a caller that tries to assert a capability
	// is refused rather than ignored.
	s, _, _ := deviceServer(t, nil)
	body := map[string]any{
		"deviceId":     "cam-1",
		"address":      "192.0.2.10",
		"capabilities": []string{"onvif.profile_m"},
	}
	rec := call(t, s, http.MethodPost, "/v1/devices", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a registration that tried to set a capability was accepted: got %d: %s", rec.Code, rec.Body)
	}
}

func TestAMalformedAddressIsRefusedAtRegistration(t *testing.T) {
	// A typo caught when the operator typed it is a typo they can fix. A
	// typo caught three steps later, by a probe against a host that does not
	// exist, is a typo the operator has to guess at.
	s, _, _ := deviceServer(t, nil)
	rec := call(t, s, http.MethodPost, "/v1/devices",
		map[string]any{"deviceId": "cam-1", "address": "://not a host"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed address was accepted: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["code"] != "bad_address" {
		t.Errorf("the refusal does not name the address: %v", body["code"])
	}
}

func TestTheProbeRecordsWhatItMeasured(t *testing.T) {
	prober := &aProber{manifest: device.Manifest{
		TenantID: "t", SiteID: "s", DeviceID: "cam-1",
		ProbeID: "probe-1", ProbedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC),
		Make: "Acme", Model: "Bullet-1",
		Capabilities: []device.Capability{
			{Name: "onvif.profile_s", State: device.StateVerified, Probe: "probe-1", At: time.Now()},
			{Name: "onvif.profile_m", State: device.StateContradicted, Probe: "probe-1", At: time.Now()},
		},
	}}
	s, _, _ := deviceServer(t, prober)
	registerDevice(t, s)

	rec := call(t, s, http.MethodPost, "/v1/devices/cam-1/probe", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe the device: got %d: %s", rec.Code, rec.Body)
	}
	if prober.calls != 1 {
		t.Fatalf("the prober was called %d time(s), want 1", prober.calls)
	}
	// The probe is taken against the REGISTERED address, so the row and the
	// measurement are about the same device.
	if prober.ref.DeviceID != "cam-1" {
		t.Errorf("the probe was about %q, want cam-1", prober.ref.DeviceID)
	}

	// And now the device reports its capabilities.
	rec = call(t, s, http.MethodGet, "/v1/devices", nil)
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	var row map[string]any
	for _, it := range items {
		r, _ := it.(map[string]any)
		if r["deviceId"] == "cam-1" {
			row = r
			break
		}
	}
	if row == nil {
		t.Fatalf("the registry does not hold cam-1: %s", rec.Body)
	}
	if row["probed"] != true {
		t.Errorf("a device that was probed reports probed:%v", row["probed"])
	}
	caps, _ := row["capabilities"].([]any)
	if len(caps) != 1 {
		t.Fatalf("the device carries %d capability/capabilities, want 1: %v", len(caps), caps)
	}
	// Only the VERIFIED one counts. A contradicted capability is a finding
	// about the device, not a capability.
	if caps[0] != "onvif.profile_s" {
		t.Errorf("the device's capability is %v, want onvif.profile_s. Only a VERIFIED capability is a capability", caps[0])
	}
}

func TestACapabilityThatWasContradictedIsNotSupported(t *testing.T) {
	prober := &aProber{manifest: device.Manifest{
		TenantID: "t", SiteID: "s", DeviceID: "cam-1",
		ProbeID: "probe-1", ProbedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC),
		Capabilities: []device.Capability{
			{Name: "onvif.profile_s", State: device.StateVerified, Probe: "probe-1", At: time.Now()},
			{Name: "onvif.profile_m", State: device.StateContradicted, Probe: "probe-1", At: time.Now()},
			{Name: "onvif.ptz", State: device.StateUnverified, Probe: "probe-1", At: time.Now()},
		},
	}}
	s, _, _ := deviceServer(t, prober)
	registerDevice(t, s)
	call(t, s, http.MethodPost, "/v1/devices/cam-1/probe", nil)

	// The VERIFIED one is supported.
	rec := call(t, s, http.MethodGet, "/v1/devices/cam-1/capabilities?capability=onvif.profile_s", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the capabilities: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["supported"] != true {
		t.Errorf("a verified capability is not supported: %v", body["supported"])
	}

	// The CONTRADICTED one is not, and the state says why.
	rec = call(t, s, http.MethodGet, "/v1/devices/cam-1/capabilities?capability=onvif.profile_m", nil)
	body = decodeBody(t, rec)
	if body["supported"] != false {
		t.Errorf("a contradicted capability is supported: %v", body["supported"])
	}
	if body["state"] != string(device.StateContradicted) {
		t.Errorf("the state is %v, want %q", body["state"], device.StateContradicted)
	}
	if body["present"] != true {
		t.Errorf("a contradicted capability is absent: %v", body["present"])
	}

	// The UNVERIFIED one was never measured, so it is not a capability and
	// it was not contradicted either.
	rec = call(t, s, http.MethodGet, "/v1/devices/cam-1/capabilities?capability=onvif.ptz", nil)
	body = decodeBody(t, rec)
	if body["supported"] != false {
		t.Errorf("an unverified capability is supported: %v", body["supported"])
	}
	if body["state"] != string(device.StateUnverified) {
		t.Errorf("the state is %v, want %q", body["state"], device.StateUnverified)
	}
}

func TestAProbeThatFailsWritesNoManifest(t *testing.T) {
	prober := &aProber{err: context.DeadlineExceeded}
	s, _, _ := deviceServer(t, prober)
	registerDevice(t, s)

	rec := call(t, s, http.MethodPost, "/v1/devices/cam-1/probe", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("a failed probe was accepted: got %d: %s", rec.Code, rec.Body)
	}
	// The device is still unprobed. A probe that failed must not leave a
	// half-written manifest behind.
	rec = call(t, s, http.MethodGet, "/v1/devices", nil)
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	var first map[string]any
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["deviceId"] == "cam-1" {
			first = row
			break
		}
	}
	if first == nil {
		t.Fatalf("the registry does not hold cam-1: %s", rec.Body)
	}
	if first["probed"] != false {
		t.Errorf("a device whose probe failed reports probed:%v", first["probed"])
	}
}

func TestADeviceWithNoAddressCannotBeProbed(t *testing.T) {
	prober := &aProber{}
	s, _, _ := deviceServer(t, prober)
	// Registered with no address at all.
	rec := call(t, s, http.MethodPost, "/v1/devices", map[string]any{"deviceId": "cam-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register the device: got %d: %s", rec.Code, rec.Body)
	}

	rec = call(t, s, http.MethodPost, "/v1/devices/cam-1/probe", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a device with no address was probed: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["code"] != "no_address" {
		t.Errorf("the refusal does not name the address: %v", body["code"])
	}
	if prober.calls != 0 {
		t.Errorf("the prober was called %d time(s) for a device with no address", prober.calls)
	}
}

func TestTheCredentialInventoryHoldsReferencesAndNeverSecrets(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	registerDevice(t, s)

	rec := call(t, s, http.MethodPost, "/v1/devices/cam-1/credentials",
		map[string]any{"purpose": "onvif", "secretRef": "keystore://camera/1/onvif"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("enrol the credential: got %d: %s", rec.Code, rec.Body)
	}

	rec = call(t, s, http.MethodGet, "/v1/devices/cam-1/credentials", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the credentials: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the inventory holds %d credential(s), want 1: %s", len(items), rec.Body)
	}
	first, _ := items[0].(map[string]any)
	if first["purpose"] != "onvif" {
		t.Errorf("the purpose is %v, want onvif", first["purpose"])
	}
	if first["secretRef"] != "keystore://camera/1/onvif" {
		t.Errorf("the reference is %v", first["secretRef"])
	}
	// A credential that has never been checked against the device is not a
	// working credential, and the inventory says so.
	if first["verified"] != false {
		t.Errorf("a credential that was never verified reports verified:%v", first["verified"])
	}
}

func TestACredentialWithAPurposeOutsideTheVocabularyIsRefused(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	registerDevice(t, s)

	rec := call(t, s, http.MethodPost, "/v1/devices/cam-1/credentials",
		map[string]any{"purpose": "the camera login", "secretRef": "keystore://camera/1/x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a free-text purpose was accepted: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["code"] != "unknown_purpose" {
		t.Errorf("the refusal does not name the purpose: %v", body["code"])
	}
}

func TestACredentialWithNoReferenceIsRefused(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	registerDevice(t, s)

	rec := call(t, s, http.MethodPost, "/v1/devices/cam-1/credentials",
		map[string]any{"purpose": "onvif"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a credential with no reference was accepted: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["code"] != "no_secret_ref" {
		t.Errorf("the refusal does not name the missing reference: %v", body["code"])
	}
}

func TestADeviceOutsideThePrincipalsScopeIsNotFound(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	registerDevice(t, s)

	// A device that was never registered, at a site the principal may not
	// see. The answer is a 404, because "no such device" and "not yours" are
	// the same answer and a different one is a disclosure.
	rec := call(t, s, http.MethodGet, "/v1/devices/cam-nobody/credentials", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a device outside the scope was answered with %d: %s", rec.Code, rec.Body)
	}
}

func TestTheDeviceRoutesDoNotTakeATenantFromTheRequest(t *testing.T) {
	s, _, _ := deviceServer(t, nil)
	rec := call(t, s, http.MethodGet, "/v1/devices?tenantId=someone-else&siteId=someone-else", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read the devices: got %d: %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["tenantId"] == "someone-else" {
		t.Errorf("the route took the tenant from the request: %v", body["tenantId"])
	}
}
