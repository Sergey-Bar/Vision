package device

import "context"

// Prober is the probe seam.
//
// A probe MEASURES a device. It is the only thing that may write a
// capability, and a capability that was not measured is not a capability.
//
// The interface is HERE, in the package that owns the capability vocabulary,
// rather than in the package that reaches the network. The reason is the same
// one `internal/investigate` gives for its `Narrator`: the thing that knows
// WHAT a measurement means must not be the thing that knows HOW to take it,
// or a change to the protocol silently changes the meaning of the record.
//
// # The address is part of the reference, and that is deliberate
//
// The reference carries the address the measurement is taken against, so the
// caller and the registry agree on WHICH device was measured. A probe that
// took its address from a request would be a probe any caller could point
// at any address, and the manifest it wrote would then describe a device
// that is not the one the row is about.
type Prober interface {
	// Probe measures one device and returns what it established.
	//
	// A device that does not answer produces NO manifest and an error. A
	// device that answers partially produces a manifest that says which
	// part was not read — the difference between "we did not look" and "we
	// looked and there was nothing", which is the distinction the whole
	// package exists to keep.
	Probe(ctx context.Context, ref ProbeRef) (Manifest, error)
}

// ProbeRef says which device a probe is about.
//
// The tenant and the site are the registry's business and are not read from
// the device: a device that could name its own tenant would be a device that
// could name its own site, and that is how a measurement ends up written
// into somebody else's scope.
type ProbeRef struct {
	TenantID string
	SiteID   string
	DeviceID string
	// Address is where the device is. It is a host and a port and nothing
	// else — the credential is in the keystore and is named by the registry
	// row, never here.
	Address string
}
