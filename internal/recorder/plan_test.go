package recorder_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/device"
	"github.com/Sergey-Bar/vision/internal/recorder"
)

// The recorder's PLAN.
//
// The claim the plan makes is that the recorder records the INTERSECTION of
// the camera list and the device registry, and that it records nothing it has
// not MEASURED.

// aRegistry is a registry with a fixed set of devices.
type aRegistry struct {
	devices []device.Device
	err     error
}

// List returns the devices.
func (r aRegistry) List(_ context.Context, _ string, _ []string) ([]device.Device, error) {
	return r.devices, r.err
}

// aSource is one camera in the list.
func aSource(id string) recorder.Source {
	return recorder.Source{TenantID: "acme", SiteID: "site-1", DeviceID: id, URL: "rtsp://" + id}
}

// aDevice is one registered device.
func aDevice(id string, enabled bool, probed bool) device.Device {
	d := device.Device{DeviceID: id, SiteID: "site-1", Kind: "camera", Enabled: enabled}
	if probed {
		d.Manifest = &device.Manifest{DeviceID: id}
	}
	return d
}

func TestThePlanRecordsWhatTheRegistryKnowsAndHasMeasured(t *testing.T) {
	registry := aRegistry{devices: []device.Device{
		aDevice("cam-1", true, true),
		aDevice("cam-2", true, false), // never probed
		aDevice("cam-3", false, true), // disabled
	}}
	sources := []recorder.Source{aSource("cam-1"), aSource("cam-2"), aSource("cam-3")}

	plan, err := recorder.PlanFor(context.Background(), registry, sources, "acme", []string{"site-1"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Sources) != 1 {
		t.Fatalf("the plan records %d camera(s), want 1: %v", len(plan.Sources), plan.Describe())
	}
	if plan.Sources[0].DeviceID != "cam-1" {
		t.Errorf("the plan records %q, want cam-1", plan.Sources[0].DeviceID)
	}
	// And the skipped cameras are NAMED, with their reasons. A camera that
	// is quietly not recorded is a hole in the recording that looks exactly
	// like a camera with nothing happening.
	if len(plan.Skipped) != 2 {
		t.Fatalf("the plan skips %d camera(s), want 2: %v", len(plan.Skipped), plan.Describe())
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.DeviceID] = s.Reason
	}
	if !strings.Contains(reasons["cam-2"], "PROBED") {
		t.Errorf("the unprobed camera's reason does not say it was never probed: %q", reasons["cam-2"])
	}
	if !strings.Contains(reasons["cam-3"], "DISABLED") {
		t.Errorf("the disabled camera's reason does not say it is disabled: %q", reasons["cam-3"])
	}
}

func TestThePlanSkipsACameraTheRegistryDoesNotKnow(t *testing.T) {
	// A camera in the list and not in the registry is a camera nobody is
	// responsible for, and recording it would put a camera on the site's
	// disk that the site's own inventory does not name.
	registry := aRegistry{devices: []device.Device{aDevice("cam-1", true, true)}}
	plan, err := recorder.PlanFor(context.Background(), registry,
		[]recorder.Source{aSource("cam-1"), aSource("cam-ghost")}, "acme", []string{"site-1"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Sources) != 1 {
		t.Errorf("the plan records %d camera(s), want 1", len(plan.Sources))
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].DeviceID != "cam-ghost" {
		t.Fatalf("the plan skips %v, want cam-ghost", plan.Skipped)
	}
	if !strings.Contains(plan.Skipped[0].Reason, "registry") {
		t.Errorf("the reason does not mention the registry: %q", plan.Skipped[0].Reason)
	}
}

func TestThePlanRecordsTheCamerasInAStableOrder(t *testing.T) {
	// A recorder that recorded the cameras in a different order each time
	// would be a recorder whose behaviour could not be compared between
	// runs.
	registry := aRegistry{devices: []device.Device{
		aDevice("cam-c", true, true),
		aDevice("cam-a", true, true),
		aDevice("cam-b", true, true),
	}}
	plan, err := recorder.PlanFor(context.Background(), registry,
		[]recorder.Source{aSource("cam-c"), aSource("cam-a"), aSource("cam-b")},
		"acme", []string{"site-1"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := []string{"cam-a", "cam-b", "cam-c"}
	for i, src := range plan.Sources {
		if src.DeviceID != want[i] {
			t.Errorf("the plan records %q at position %d, want %q", src.DeviceID, i, want[i])
		}
	}
}

func TestThePlanRefusesWithoutARegistry(t *testing.T) {
	// A recorder with no registry cannot tell which cameras the product is
	// responsible for, and it must not guess.
	_, err := recorder.PlanFor(context.Background(), nil, []recorder.Source{aSource("cam-1")},
		"acme", []string{"site-1"})
	if err == nil {
		t.Fatal("the plan was built with no registry")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestThePlanDescribesBothHalves(t *testing.T) {
	// The description is what an operator reads in the log, and a plan that
	// only says what it WILL do hides the cameras it will not.
	registry := aRegistry{devices: []device.Device{aDevice("cam-1", true, true)}}
	plan, err := recorder.PlanFor(context.Background(), registry,
		[]recorder.Source{aSource("cam-1"), aSource("cam-ghost")}, "acme", []string{"site-1"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	text := plan.Describe()
	if !strings.Contains(text, "recording cam-1") {
		t.Errorf("the description does not name what is recorded: %s", text)
	}
	if !strings.Contains(text, "skipping cam-ghost") {
		t.Errorf("the description does not name what is skipped: %s", text)
	}
}
