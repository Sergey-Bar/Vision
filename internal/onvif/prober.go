package onvif

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Sergey-Bar/vision/internal/device"
)

// Prober measures devices over ONVIF.
//
// It is the implementation of `device.Prober`, and it is the ONLY place in the
// product that opens a socket to a camera to find out what a camera can do.
//
// # The credential
//
// A device that needs one is a device whose address is paired with a
// credential in the keystore. The credential is NOT an argument to `Probe`:
// an argument is a thing a caller can log, a thing a request body carries,
// and a thing an error message can quote. The credential belongs to the
// keystore, and this package asks for it through the `Credential` interface
// so that it never has to know how a secret is stored.
//
// A device that needs no credential is probed with none, and the manifest
// says which reads failed and why. That is the honest outcome: a device that
// will not describe itself has not been contradicted about itself, it has
// not been asked.
type Prober struct {
	// Credential supplies the device's secret. It may be nil, and it is
	// consulted only for a device that needs one.
	Credential Credential
	// HTTP is the transport. A nil HTTP means the default client.
	HTTP *http.Client
}

// Probe measures one device over ONVIF.
//
// The three reads are the whole of it, and the order matters: the device
// service first, because without it there is no identity; then the
// capabilities document, because it says what the device claims; then the
// media profiles, because they say what the device actually implements.
//
// A device that fails the first read produces no manifest at all.
func (p Prober) Probe(ctx context.Context, ref device.ProbeRef) (device.Manifest, error) {
	if ref.TenantID == "" || ref.SiteID == "" || ref.DeviceID == "" {
		return device.Manifest{}, fmt.Errorf("onvif: a probe needs a tenant, a site and a device")
	}
	base, err := ParseBaseURL(ref.Address)
	if err != nil {
		// The address is not known, so the device was not measured. The
		// refusal names the reason, because "the probe failed" and "we do
		// not know where the device is" are different facts and the second
		// one is fixed by registering the device.
		return device.Manifest{}, fmt.Errorf("onvif: %s has no address to probe: %w", ref.DeviceID, err)
	}
	client := &Client{
		BaseURL:    base,
		Credential: p.Credential,
	}
	if p.HTTP != nil {
		client.HTTP = p.HTTP
	}
	manifest, err := Probe(ctx, Reference{
		TenantID: ref.TenantID,
		SiteID:   ref.SiteID,
		DeviceID: ref.DeviceID,
	}, client)
	if err != nil {
		return device.Manifest{}, err
	}
	return manifest, nil
}

// Compile-time proof that the prober satisfies the seam.
var _ device.Prober = Prober{}
