package onvif

import (
	"context"
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision/internal/device"
)

// The capability names the probe establishes.
//
// They are namespaced, because a capability name that could belong to two
// subsystems is a name two subsystems will eventually both claim. The profile
// names follow the ONVIF profile letters, which is what an operator reading
// a camera's datasheet will recognise.
const (
	// CapProfileS is a profile with a video encoder configuration.
	CapProfileS = "onvif.profile_s"
	// CapProfileT is a profile with an audio encoder configuration.
	CapProfileT = "onvif.profile_t"
	// CapProfileG is a profile with an imaging configuration.
	CapProfileG = "onvif.profile_g"
	// CapProfileM is a profile with a real video-analytics configuration.
	// This is the one that matters: a token in a list is not a profile.
	CapProfileM = "onvif.profile_m"
	// CapProfileA is a profile with an analytics-event configuration.
	CapProfileA = "onvif.profile_a"
	// CapProfileD is a profile with a device configuration.
	CapProfileD = "onvif.profile_d"
	// CapProfilePTZ is a profile with a pan, tilt and zoom configuration. It
	// is a PROFILE capability and not the PTZ service: the service is what
	// the device names, and this is what a profile carries.
	CapProfilePTZ = "onvif.profile_ptz"
)

// Reference says which device a probe is about.
//
// The tenant and the site are the registry's business and are not read from
// the device: a device that could name its own tenant would be a device that
// could name its own site, and that is how a probe ends up writing a
// measurement into somebody else's scope.
type Reference struct {
	TenantID string
	SiteID   string
	DeviceID string
	// ProbeID names this probe run. It is what a reader checks the
	// capabilities against.
	ProbeID string
}

// Probe measures one device and returns what it established.
//
// The three reads are the whole of it, and the order matters: the device
// service first, because without it there is no identity; then the
// capabilities document, because it says what the device claims; then the
// media profiles, because they say what the device actually implements.
//
// A device that fails the first read produces no manifest at all. A device
// that fails the second produces a manifest with no service capabilities. A
// device that fails the third produces a manifest whose profile capabilities
// are absent — and that is the honest reading, because a device that lists
// a profile and then refuses to describe it is a finding, not an absence.
func Probe(ctx context.Context, ref Reference, c *Client) (device.Manifest, error) {
	if ref.TenantID == "" || ref.SiteID == "" || ref.DeviceID == "" {
		return device.Manifest{}, fmt.Errorf("onvif: a probe needs a tenant, a site and a device")
	}
	if c == nil {
		return device.Manifest{}, fmt.Errorf("onvif: a probe needs a client")
	}
	probeID := ref.ProbeID
	if probeID == "" {
		probeID = fmt.Sprintf("onvif:%s:%d", ref.DeviceID, time.Now().UTC().UnixNano())
	}
	observedAt := time.Now().UTC()

	info, err := c.GetDeviceInformation(ctx)
	if err != nil {
		return device.Manifest{}, fmt.Errorf("onvif: read the device information of %s: %w", ref.DeviceID, err)
	}

	manifest := device.Manifest{
		TenantID:      ref.TenantID,
		SiteID:        ref.SiteID,
		DeviceID:      ref.DeviceID,
		ProbeID:       probeID,
		ProbedAt:      observedAt,
		Make:          info.Manufacturer,
		Model:         info.Model,
		Firmware:      info.FirmwareVersion,
		ONVIFDeviceID: info.SerialNumber,
	}

	// The device service answered, so that capability is established. The
	// others are not, and the difference is the whole point of the package.
	manifest.Capabilities = append(manifest.Capabilities, device.Capability{
		Name:   CapDevice,
		State:  device.StateVerified,
		Probe:  probeID,
		At:     observedAt,
		Detail: "the device service answered GetDeviceInformation",
	})

	caps, capsErr := c.GetCapabilities(ctx)
	if capsErr != nil {
		// A device that will not describe its capabilities has not been
		// contradicted about them; it has not been asked. The manifest says
		// so rather than guessing.
		manifest.Capabilities = append(manifest.Capabilities, device.Capability{
			Name:   CapMedia,
			State:  device.StateUnverified,
			Probe:  probeID,
			At:     observedAt,
			Detail: "the device did not return its capabilities: " + capsErr.Error(),
		})
		return device.NewManifest(manifest)
	}

	for _, name := range []string{CapAnalytics, CapEvents, CapImaging} {
		if _, named := caps[name]; !named {
			continue
		}
		// The device's capabilities document NAMES this service. Nothing has
		// called it yet, so it is not a capability. It is a claim, recorded as
		// a claim.
		manifest.Capabilities = append(manifest.Capabilities, device.Capability{
			Name:   name,
			State:  device.StateUnverified,
			Probe:  probeID,
			At:     observedAt,
			Detail: "the device's capabilities document names this service; nothing has called it",
		})
	}

	profiles, err := c.GetProfiles(ctx)
	if err != nil {
		manifest.Capabilities = append(manifest.Capabilities, device.Capability{
			Name:   CapMedia,
			State:  device.StateUnverified,
			Probe:  probeID,
			At:     observedAt,
			Detail: "the media service did not return its profiles: " + err.Error(),
		})
		return device.NewManifest(manifest)
	}

	manifest.Capabilities = append(manifest.Capabilities, device.Capability{
		Name:   CapMedia,
		State:  device.StateVerified,
		Probe:  probeID,
		At:     observedAt,
		Detail: fmt.Sprintf("the media service answered with %d profile(s)", len(profiles)),
	})
	manifest.Capabilities = append(manifest.Capabilities, profileCapabilities(profiles, probeID, observedAt)...)

	// The finding the package exists for. A device whose capabilities document
	// names the analytics service, and whose media profiles carry no video
	// analytics configuration, has been contradicted about Profile M — and a
	// product that believes the advertisement would offer an analytics
	// feature the camera cannot do.
	if _, named := caps[CapAnalytics]; named && !hasCapability(manifest, CapProfileM, device.StateVerified) {
		manifest.Capabilities = append(manifest.Capabilities, device.Capability{
			Name:   CapProfileM,
			State:  device.StateContradicted,
			Probe:  probeID,
			At:     observedAt,
			Detail: "the device names the analytics service, and none of its media profiles carries a video analytics configuration",
		})
	}

	return device.NewManifest(manifest)
}

// profileCapabilities turns the media profiles into capabilities.
//
// A profile capability is established when a profile actually carries the
// configuration, and that is the measurement: the profile list is what the
// device advertises, and the configuration inside a profile is what it
// implements.
func profileCapabilities(profiles []MediaProfile, probeID string, at time.Time) []device.Capability {
	var out []device.Capability
	// A profile capability is added only when the profile CARRIES the
	// configuration, so it is VERIFIED by construction: there is no branch
	// here for "not established", because a profile that does not carry the
	// configuration produces no capability at all.
	add := func(name string, detail string) {
		out = append(out, device.Capability{
			Name:   name,
			State:  device.StateVerified,
			Probe:  probeID,
			At:     at,
			Detail: detail,
		})
	}
	for _, p := range profiles {
		if p.HasVideo {
			add(CapProfileS, describeProfile(p, "a video encoder configuration"))
		}
		if p.HasAudio {
			add(CapProfileT, describeProfile(p, "an audio encoder configuration"))
		}
		if p.HasImaging {
			add(CapProfileG, describeProfile(p, "an imaging configuration"))
		}
		if p.HasAnalytics {
			add(CapProfileM, describeProfile(p, "a video analytics configuration"))
		}
		if p.HasAnalyticsEvents {
			add(CapProfileA, describeProfile(p, "an analytics event configuration"))
		}
		if p.HasDevice {
			add(CapProfileD, describeProfile(p, "a device configuration"))
		}
		if p.HasPTZ {
			add(CapProfilePTZ, describeProfile(p, "a pan, tilt and zoom configuration"))
		}
	}
	return out
}

// describeProfile describes one profile's contribution in a sentence.
func describeProfile(p MediaProfile, what string) string {
	name := p.Name
	if name == "" {
		name = p.Token
	}
	return fmt.Sprintf("profile %q carries %s", name, what)
}

// hasCapability reports whether a manifest already carries a capability in a
// state.
func hasCapability(m device.Manifest, name string, state device.State) bool {
	found, ok := m.StateOf(name)
	return ok && found == state
}
