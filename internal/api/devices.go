package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/device"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/onvif"
)

// The device registry.
//
// A camera is a DEVICE, and a device is three things the platform did not
// have before the AI VMS plan: what it IS, what was MEASURED about it, and
// where its credential lives.
//
// The discipline is the same one the rest of the product keeps. A camera
// NAMES the profiles it supports and is routinely wrong about it, so a
// capability that was not measured is not a capability: the manifest's
// `verified` entries are the only ones that count, and the Go side refuses
// to publish a `verified` capability with no probe behind it.
//
// # The site scope
//
// The registry is site-scoped, and the scope is the PRINCIPAL's, resolved
// from the configuration. It is never taken from a request, because a value
// a caller sends is an instruction and not an authorisation.

func (s *Server) handleDeviceList(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		// An empty list and an unconfigured registry look identical, and only
		// one of them is true. A site with no registry has no devices; a site
		// with a registry and none registered also has no devices. The
		// difference is worth saying out loud.
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no device registry configured.",
			"items":       []any{},
		})
		return
	}
	sites := p.SiteScope
	if len(sites) == 0 {
		WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	devices, err := d.Devices.List(r.Context(), p.TenantID, sites)
	if err != nil {
		// The registry's own refusals are named, and each one means a
		// different thing to the operator: a registry with no pool behind it
		// is an install that is not configured, and a read with no site scope
		// is a request that named nothing.
		switch {
		case errors.Is(err, device.ErrNoPool):
			WriteError(w, http.StatusServiceUnavailable, "not_configured",
				"This install has no device registry configured.", requestID(r))
		case errors.Is(err, device.ErrNoScope):
			WriteError(w, http.StatusForbidden, "site_not_assigned",
				"You are not assigned to that site.", requestID(r))
		default:
			WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		}
		return
	}
	items := make([]map[string]any, 0, len(devices))
	for _, dev := range devices {
		items = append(items, deviceView(dev))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"items":     items,
		// The closed vocabularies, published so a reader knows the whole set
		// rather than only the members this install has met. A console that
		// renders a capability report needs to know what a capability CAN
		// be, and a report that only names the ones it has seen is a report
		// about the past.
		"vocabulary": map[string]any{
			"credentialPurposes": device.Purposes,
			"capabilityStates":   device.States,
			"onvifCapabilities": []string{
				onvif.CapDevice, onvif.CapMedia, onvif.CapAnalytics,
				onvif.CapEvents, onvif.CapImaging, onvif.CapPTZ,
			},
			"onvifProfiles": []string{
				onvif.CapProfileS, onvif.CapProfileT, onvif.CapProfileG,
				onvif.CapProfileM, onvif.CapProfileA, onvif.CapProfileD,
				onvif.CapProfilePTZ,
			},
		},
	})
}

// handleDeviceCapabilities reads one device's MEASURED manifest.
//
// A device that has never been probed has no manifest, and the answer says
// so by name. An empty manifest would be a probe that found nothing, and the
// difference between "we did not look" and "we looked and there was nothing"
// is the difference the whole package exists to keep.
//
// The `capability` parameter answers the question the recorder will ask: is
// THIS capability one the device actually has? The answer is a boolean and
// the state, because a capability that is absent and a capability that is
// present-and-unverified are different facts.
func (s *Server) handleDeviceCapabilities(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device registry configured.", requestID(r))
		return
	}
	sites := p.SiteScope
	if len(sites) == 0 {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	manifest, err := d.Devices.Manifest(r.Context(), p.TenantID, sites, r.PathValue("id"))
	if err != nil {
		// A device that was never probed and a device that does not exist are
		// both "no manifest", and the first is a fact about the install while
		// the second is a fact about the request. The refusal names the
		// difference rather than pretending to a 404 either way.
		if errors.Is(err, device.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_probed",
				"This device has no measured manifest: it has never been probed, or it does not exist.",
				requestID(r))
			return
		}
		WriteError(w, http.StatusInternalServerError, "not_read", err.Error(), requestID(r))
		return
	}
	// The manifest is rendered in its PUBLISHED shape, and the published shape
	// is written by the package that owns it: `device.Manifest` implements
	// `json.Marshaler`, so the encoder calls it. A second rendering here would
	// be a second opinion about a wire format, and a wire format that two
	// places render is a wire format that will drift.
	//
	// The encoder is called rather than the method, and that is deliberate:
	// the method is reached through the `json.Marshaler` interface, and
	// naming it here would make the dead-export gate's method rule -- which
	// matches a method by a bare name -- believe that every `MarshalJSON` in
	// the tree has a reader.
	raw, err := json.Marshal(manifest)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "not_rendered", err.Error(), requestID(r))
		return
	}
	out := map[string]any{"manifest": json.RawMessage(raw)}
	if name := strings.TrimSpace(r.URL.Query().Get("capability")); name != "" {
		state, present := manifest.StateOf(name)
		out["capability"] = name
		out["present"] = present
		out["state"] = string(state)
		// Only a VERIFIED capability is a capability. The boolean is the
		// question the recorder asks, and the state is why.
		out["supported"] = manifest.Supports(name)
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleDeviceDiscover finds the ONVIF devices on the network.
//
// It is the FIRST step of adding a camera: the operator does not type an
// address when the camera can say where it is.
//
// The scan is a multicast probe, and on a network that does not carry
// multicast it finds nothing. That is the honest answer and the route says
// so, because an empty list and a broken scan look identical to an operator
// and only one of them is true.
func (s *Server) handleDeviceDiscover(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ Deps) {
	const scanTimeout = 3 * time.Second
	endpoints, err := onvif.Discover(r.Context(), scanTimeout)
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "scan_failed",
			"The discovery scan did not complete: "+err.Error()+
				". A network that does not carry multicast will find nothing, and a camera may also be reachable only by its address.",
			requestID(r))
		return
	}
	items := make([]map[string]any, 0, len(endpoints))
	for _, e := range endpoints {
		base, baseErr := e.BaseURL()
		item := map[string]any{
			"address": e.Address,
			"name":    e.Name,
			"types":   e.Types,
			"scopes":  e.Scopes,
		}
		if baseErr != nil {
			// The device answered and published no address it can be
			// reached at. It is still a device that answered, and the entry
			// says why the product cannot use it.
			item["usable"] = false
			item["reason"] = baseErr.Error()
		} else {
			item["usable"] = true
			item["baseUrl"] = base
			// The two service paths, so a reader can see what the device
			// will be asked about it. The client builds them, so the path
			// and the base are joined the ONE way the product joins them.
			client := &onvif.Client{BaseURL: base}
			item["deviceService"] = client.ServiceURL(onvif.DeviceServicePath)
			item["mediaService"] = client.ServiceURL(onvif.MediaServicePath)
		}
		items = append(items, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"scan": map[string]any{
			"multicastGroup": onvif.MulticastGroup,
			"multicastPort":  onvif.MulticastPort,
			"found":          len(items),
			"note":           "A device that answered but published no address is listed with usable:false and the reason.",
		},
	})
}

// handleDeviceCredentials reads one device's credential INVENTORY.
//
// The inventory is the whole of what we hold for a device: which purposes we
// have a credential for, when each was enrolled, and when each was last
// VERIFIED against the device itself. The secrets are in the keystore and
// the references are here, so a support export of this route carries no
// password.
//
// A credential that has never been verified is not a working credential, and
// the inventory says so by leaving the verification time empty.
func (s *Server) handleDeviceCredentials(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device registry configured.", requestID(r))
		return
	}
	sites := p.SiteScope
	if len(sites) == 0 {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	deviceID := r.PathValue("id")
	// The device is read through the SCOPED reader first, so the credential
	// inventory cannot be reached for a device outside the principal's scope
	// by naming its id.
	devices, err := d.Devices.List(r.Context(), p.TenantID, sites)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	found := false
	for _, dev := range devices {
		if dev.DeviceID == deviceID {
			found = true
			break
		}
	}
	if !found {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	credentials, err := d.Devices.Credentials(r.Context(), p.TenantID, deviceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "not_read", err.Error(), requestID(r))
		return
	}
	items := make([]map[string]any, 0, len(credentials))
	for _, c := range credentials {
		item := map[string]any{
			"purpose":    c.Purpose,
			"secretRef":  c.SecretRef,
			"enrolledAt": c.EnrolledAt.UTC().Format(time.RFC3339),
			// A credential that has never been checked against the device is
			// not a working credential, and the inventory says so rather than
			// implying that enrolment proved it.
			"verified": c.LastVerifiedAt != nil,
		}
		if c.LastVerifiedAt != nil {
			item["lastVerifiedAt"] = c.LastVerifiedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"deviceId": deviceID,
		"items":    items,
		// The closed vocabulary, as a set AND as what each member means. A
		// console needs the set to render a picker and the meanings to
		// render a label, and a reader that has only the set has to guess
		// what "onvif" opens.
		"purposeVocabulary": device.Purposes,
		"purposes": map[string]string{
			device.PurposeRTSP:  "the credential that opens the device's stream",
			device.PurposeONVIF: "the credential that opens the device's control service",
			device.PurposeFTP:   "the credential that opens the device's file service",
			device.PurposeOther: "anything else, named by the detail",
		},
	})
}

// handleDeviceCredentialEnrol records a credential REFERENCE for a device.
//
// What is written here is a POINTER, and the method name says "enrol" rather
// than "set password" on purpose: the keystore holds the thing the pointer
// names, and the secret itself never reaches this route, a log or the ledger.
//
// A purpose outside the closed vocabulary is refused. A free-text purpose
// becomes "the camera login" and then "the camera login (old)" and then two
// credentials for one device with nobody able to say which one is live.
func (s *Server) handleDeviceCredentialEnrol(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device registry configured.", requestID(r))
		return
	}
	sites := p.SiteScope
	if len(sites) == 0 {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	deviceID := r.PathValue("id")
	devices, err := d.Devices.List(r.Context(), p.TenantID, sites)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	found := false
	for _, dev := range devices {
		if dev.DeviceID == deviceID {
			found = true
			break
		}
	}
	if !found {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	var body struct {
		Purpose string `json:"purpose"`
		// SecretRef names the keystore entry. It is a pointer, never a
		// secret.
		SecretRef string `json:"secretRef"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if err := d.Devices.EnrolCredential(r.Context(), device.Credential{
		TenantID:  p.TenantID,
		DeviceID:  deviceID,
		Purpose:   strings.TrimSpace(body.Purpose),
		SecretRef: strings.TrimSpace(body.SecretRef),
	}); err != nil {
		switch {
		case errors.Is(err, device.ErrUnknownPurpose):
			WriteError(w, http.StatusBadRequest, "unknown_purpose", err.Error(), requestID(r))
		case errors.Is(err, device.ErrNoSecretRef):
			WriteError(w, http.StatusBadRequest, "no_secret_ref", err.Error(), requestID(r))
		default:
			WriteError(w, http.StatusInternalServerError, "not_enrolled", err.Error(), requestID(r))
		}
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"deviceId": deviceID,
		"purpose":  strings.TrimSpace(body.Purpose),
		"enrolled": true,
	})
}

// handleDeviceRegister records a device in the registry.
//
// It does NOT touch the capability manifest, and that is the point: a device
// acquires capabilities by being MEASURED, not by being registered. A
// registration that could set a capability would be a way for a caller to
// assert that a camera can do something it has never been asked about.
func (s *Server) handleDeviceRegister(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device registry configured.", requestID(r))
		return
	}
	var body struct {
		DeviceID    string `json:"deviceId"`
		SiteID      string `json:"siteId"`
		ZoneID      string `json:"zoneId"`
		Kind        string `json:"kind"`
		DisplayName string `json:"displayName"`
		// Address is where the device is. It is a host and a port and
		// nothing else: the credential is in the keystore and is named by
		// `credentialRef`, never here.
		Address string `json:"address"`
		// CredentialRef names the keystore entry. It is a pointer, never a
		// secret.
		CredentialRef    string `json:"credentialRef"`
		SubstreamProfile string `json:"substreamProfile"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), requestID(r))
		return
	}
	if strings.TrimSpace(body.DeviceID) == "" {
		WriteError(w, http.StatusBadRequest, "bad_request",
			"The device needs an id: a name the product and the operator agree on.", requestID(r))
		return
	}
	siteID := body.SiteID
	if siteID == "" {
		siteID = firstSite(p)
	}
	if !p.CanSeeSite(siteID) {
		// Refused rather than accepted-and-hidden: a registration into a
		// site the principal may not see is a row nobody can read back, and
		// "no such site" and "not yours" are the same answer.
		WriteError(w, http.StatusForbidden, "site_not_assigned",
			"You are not assigned to that site.", requestID(r))
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = "camera"
	}
	// The address is NORMALISED at registration, not at probe time.
	//
	// A malformed address is a typo, and a typo caught when the operator
	// typed it is a typo they can fix. A typo caught three steps later, by a
	// probe that failed against a host that does not exist, is a typo the
	// operator has to guess at.
	address := strings.TrimSpace(body.Address)
	if address != "" {
		base, err := onvif.ParseBaseURL(address)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_address", err.Error(), requestID(r))
			return
		}
		address = base
	}
	if err := d.Devices.Register(r.Context(), device.Device{
		TenantID:         p.TenantID,
		DeviceID:         strings.TrimSpace(body.DeviceID),
		SiteID:           siteID,
		ZoneID:           strings.TrimSpace(body.ZoneID),
		Kind:             kind,
		DisplayName:      strings.TrimSpace(body.DisplayName),
		Enabled:          true,
		ONVIFBaseURL:     address,
		CredentialRef:    strings.TrimSpace(body.CredentialRef),
		SubstreamProfile: strings.TrimSpace(body.SubstreamProfile),
	}); err != nil {
		WriteError(w, http.StatusBadRequest, "not_registered", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"deviceId":   strings.TrimSpace(body.DeviceID),
		"siteId":     siteID,
		"registered": true,
		// A device that has just been registered has NO capabilities, and
		// the answer says so. The next step is the probe.
		"probed": false,
	})
}

// handleDeviceProbe MEASURES one device and records what it established.
//
// This is the step that makes the registry honest. Everything before it is a
// claim somebody typed; everything after it is a measurement.
//
// A probe that fails does not write a manifest. A device that did not answer
// has no capabilities, and a half-answered device has a manifest that says
// which half.
func (s *Server) handleDeviceProbe(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Devices == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device registry configured.", requestID(r))
		return
	}
	if d.DeviceProber == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no device prober configured, so nothing can be measured.",
			requestID(r))
		return
	}
	sites := p.SiteScope
	if len(sites) == 0 {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	deviceID := r.PathValue("id")
	devices, err := d.Devices.List(r.Context(), p.TenantID, sites)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	var found *device.Device
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			found = &devices[i]
			break
		}
	}
	if found == nil {
		WriteError(w, http.StatusNotFound, "not_found", "No such device.", requestID(r))
		return
	}
	if found.ONVIFBaseURL == "" {
		// The product does not know where the device is. The refusal says
		// that by name, because "the probe failed" and "we do not know where
		// the device is" are different facts and only the second is fixed by
		// registering the address.
		WriteError(w, http.StatusBadRequest, "no_address",
			"This device has no address, so there is nothing to probe. Register the address first.",
			requestID(r))
		return
	}
	manifest, err := d.DeviceProber.Probe(r.Context(), device.ProbeRef{
		TenantID: p.TenantID,
		SiteID:   found.SiteID,
		DeviceID: found.DeviceID,
		Address:  found.ONVIFBaseURL,
	})
	if err != nil {
		WriteError(w, http.StatusBadGateway, "probe_failed", err.Error(), requestID(r))
		return
	}
	if err := d.Devices.SaveManifest(r.Context(), manifest); err != nil {
		// A manifest that failed validation is refused, and each refusal
		// names a DIFFERENT mistake. A manifest with no probe behind a
		// verified capability is the one that matters: it would be a claim
		// wearing a measurement's clothes.
		switch {
		case errors.Is(err, device.ErrNoTenant):
			WriteError(w, http.StatusBadRequest, "no_tenant",
				"The measurement has no tenant, so it cannot be recorded against this site.", requestID(r))
		case errors.Is(err, device.ErrNoSite):
			WriteError(w, http.StatusBadRequest, "no_site",
				"The measurement has no site, so it cannot be recorded against this site.", requestID(r))
		case errors.Is(err, device.ErrNoDevice):
			WriteError(w, http.StatusBadRequest, "no_device",
				"The measurement names no device, so it cannot be recorded.", requestID(r))
		case errors.Is(err, device.ErrNoProbeTime):
			WriteError(w, http.StatusBadRequest, "no_probe_time",
				"The measurement has no probe time, so it is a claim and not a measurement.", requestID(r))
		case errors.Is(err, device.ErrCapabilityWithoutName):
			WriteError(w, http.StatusBadRequest, "capability_without_name",
				"The measurement names a capability with no name, so it cannot be recorded.", requestID(r))
		default:
			WriteError(w, http.StatusInternalServerError, "not_saved", err.Error(), requestID(r))
		}
		return
	}
	WriteJSON(w, http.StatusOK, manifest)
}

// deviceView is the wire shape of one device.
//
// The capability set is rendered from the manifest's VERIFIED entries, and
// the manifest itself is carried whole, because a reader that needs the
// detail -- which capability was contradicted, and by which probe -- has to
// be able to get it without a second round trip.
func deviceView(dev device.Device) map[string]any {
	out := map[string]any{
		"deviceId":         dev.DeviceID,
		"siteId":           dev.SiteID,
		"zoneId":           dev.ZoneID,
		"kind":             dev.Kind,
		"displayName":      dev.DisplayName,
		"enabled":          dev.Enabled,
		"make":             dev.Make,
		"model":            dev.Model,
		"firmware":         dev.Firmware,
		"onvifDeviceId":    dev.ONVIFDeviceID,
		"rtspRef":          dev.RTSPRef,
		"substreamProfile": dev.SubstreamProfile,
		// A device that has never been probed has NO capabilities, and the
		// answer says so rather than rendering an empty list that reads as
		// "we looked and it can do nothing".
		"probed": dev.Manifest != nil,
	}
	if dev.Manifest != nil {
		out["capabilities"] = dev.Manifest.Claimed()
		out["manifest"] = dev.Manifest
		out["probedAt"] = dev.Manifest.ProbedAt.UTC().Format(time.RFC3339)
		out["probeId"] = dev.Manifest.ProbeID
	} else {
		out["capabilities"] = []string{}
	}
	return out
}
