package recorder

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision/internal/device"
)

// The recorder's PLAN.
//
// # Why the registry is the recorder's input
//
// The camera list says WHERE a stream is and WHICH credential opens it. It
// does not say whether the product is responsible for that camera.
//
// The DEVICE REGISTRY does. It says which devices the product is responsible
// for and what was MEASURED about each of them.
//
// So the recorder records the INTERSECTION: a camera that is in the list and
// in the registry. A camera in the list and not in the registry is a camera
// nobody is responsible for, and recording it would put a camera on the
// site's disk that the site's own inventory does not name.
//
// # Why the MEASURED manifest decides
//
// A device that has never been probed is skipped.
//
// The recorder needs to know the camera can deliver a stream it can mux, and
// a camera nobody has measured is a camera whose capabilities the recorder
// would have to GUESS. A guess about a camera's capabilities is how a
// product ends up promising H.265 it cannot deliver — and a recording that
// is silently empty looks exactly like a camera with nothing happening.
//
// So the rule is: a device with no measured manifest is not recorded, and
// the reason is named. It is the same discipline the console keeps: only a
// VERIFIED capability counts.

// Registry is the device registry the recorder reads.
//
// It is an INTERFACE and not the concrete service so that the recorder does
// not depend on the registry's storage, and so that a test can hand it a
// fixed set of devices.
type Registry interface {
	// List returns the devices the product is responsible for at the given
	// sites.
	List(ctx context.Context, tenant string, sites []string) ([]device.Device, error)
}

// Skipped is one camera the recorder will NOT record, and why.
//
// The reason is MANDATORY. A camera that is quietly not recorded is a hole
// in the recording that looks exactly like a camera with nothing happening,
// and the operator has to be able to tell the two apart.
type Skipped struct {
	// DeviceID is the camera.
	DeviceID string
	// Reason says why it was skipped.
	Reason string
}

// Plan is what the recorder will record.
type Plan struct {
	// Sources are the cameras to record, in a stable order.
	Sources []Source
	// Skipped are the cameras that will not be recorded, and why.
	Skipped []Skipped
}

// PlanFor joins the camera list with the registry.
//
// The result is ordered by device id, so that two runs with the same inputs
// record in the same order — and a recorder that recorded the cameras in a
// different order each time would be a recorder whose behaviour could not be
// compared between runs.
func PlanFor(ctx context.Context, registry Registry, sources []Source, tenant string, sites []string) (Plan, error) {
	var plan Plan
	if registry == nil {
		return plan, fmt.Errorf("recorder: no device registry, so the recorder cannot tell " +
			"which cameras the product is responsible for")
	}
	devices, err := registry.List(ctx, tenant, sites)
	if err != nil {
		return plan, fmt.Errorf("recorder: read the device registry: %w", err)
	}
	known := make(map[string]device.Device, len(devices))
	for _, d := range devices {
		known[d.DeviceID] = d
	}

	for _, src := range sources {
		dev, ok := known[src.DeviceID]
		switch {
		case !ok:
			plan.Skipped = append(plan.Skipped, Skipped{
				DeviceID: src.DeviceID,
				Reason: "the device registry does not know this camera, so the product is " +
					"not responsible for it. Register it and it will be recorded",
			})
			continue
		case !dev.Enabled:
			plan.Skipped = append(plan.Skipped, Skipped{
				DeviceID: src.DeviceID,
				Reason: "the device is registered but DISABLED, so the product is not " +
					"recording it",
			})
			continue
		case dev.Manifest == nil:
			plan.Skipped = append(plan.Skipped, Skipped{
				DeviceID: src.DeviceID,
				Reason: "the device has never been PROBED, so what it can deliver is a " +
					"guess rather than a measurement. Probe it and it will be recorded",
			})
			continue
		}
		plan.Sources = append(plan.Sources, src)
	}

	sort.Slice(plan.Sources, func(i, j int) bool {
		return plan.Sources[i].DeviceID < plan.Sources[j].DeviceID
	})
	sort.Slice(plan.Skipped, func(i, j int) bool {
		return plan.Skipped[i].DeviceID < plan.Skipped[j].DeviceID
	})
	return plan, nil
}

// Describe renders the plan for an operator.
//
// It names what will be recorded AND what will not, with the reasons. A
// recorder that starts and records nothing says nothing, and the operator
// sees a site with no footage and no explanation.
func (p Plan) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d camera(s) will be recorded", len(p.Sources))
	for _, src := range p.Sources {
		b.WriteString("\n  recording " + src.DeviceID)
	}
	if len(p.Skipped) > 0 {
		fmt.Fprintf(&b, "\n%d camera(s) will NOT be recorded:", len(p.Skipped))
		for _, s := range p.Skipped {
			b.WriteString("\n  skipping " + s.DeviceID + ": " + s.Reason)
		}
	}
	return b.String()
}
