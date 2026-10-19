package emulator

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/ortdetect"
)

// The prefix pin lives in the ADAPTER's package, in
// `internal/milestone/emulator_pin_test.go`, and it reads both constants.
//
// It is not here because the rule this repository keeps is that a package
// outside the composition root does not import the vendor's package, and this
// package is the vendor's OTHER SIDE: an emulator that imported the adapter
// would be agreeing with it by construction, which is the agreement the probe
// exists to check.
//
// A fake detector, so the wire can be tested without a GPU, a model and 2.4 GB
// of CUDA libraries.
//
// It is a fake and not a mock: it returns real boxes in the shape the detector
// returns them in, and the test asserts on the ENVELOPE the emulator publishes,
// which is the thing the platform actually reads.
type fakeDetector struct {
	boxes []ortdetect.Box
}

func (f fakeDetector) Detect(_ uint64, _ []byte, _, _ int, _ ortdetect.Rect) ortdetect.Answer {
	return ortdetect.Answer{Seq: 1, Boxes: f.boxes, State: "asked_answered"}
}

func (f fakeDetector) Identity() ortdetect.Identity {
	return ortdetect.Identity{ModelPath: "test-model.onnx", SHA256: "deadbeef", ExecutionProvider: "cuda"}
}

// startEmulator brings one up with a fake detector and no video.
//
// No video means no camera loop, so the test publishes by hand. That is the
// honest shape for a wire test: the publisher is the unit under test and the
// video is not.
func startEmulator(t *testing.T, det Detector) *Engine {
	t.Helper()
	engine, err := New(Options{
		Listen:   "127.0.0.1:0",
		Token:    "test-token",
		Now:      func() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) },
		Sources:  []Source{{ID: "entrance", Name: "Entrance", Kind: KindFile, Location: "test.mp4", FPS: 1}},
		Detector: det,
		Vendor:   "test-detector",
	})
	if err != nil {
		t.Fatalf("build the emulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = engine.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the emulator did not stop within five seconds")
		}
	})
	waitForURL(t, engine)
	return engine
}

// waitForURL waits for the engine's URL to be readable.
func waitForURL(t *testing.T, engine *Engine) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if engine.URL() != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the emulator never reported its URL")
}

// publishOne publishes one frame's worth of detections, the way the camera loop
// would.
func publishOne(t *testing.T, engine *Engine, dets []Detection) {
	t.Helper()
	cam, ok := engine.camera("entrance")
	if !ok {
		t.Fatal("the camera was not created")
	}
	engine.publish(cam, dets)
}

// TestTheEmulatorAnswersTheVendorsRoutes is the emulator's own view of its
// own routes.
//
// The FULL list lives in the adapter's package, in
// `internal/milestone/emulator_pin_test.go`, because the route table is the
// vendor's vocabulary and this package deliberately does not import it. What is
// checked here is that the routes this emulator serves answer; what is checked
// there is that the two sides agree about which routes there are.
func TestTheEmulatorAnswersTheVendorsRoutes(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	publishOne(t, engine, []Detection{{Class: classPerson, Confidence: 0.9, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.6}})
	base := engine.URL()

	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{
		APIPrefix + "/events",
		APIPrefix + "/events/" + placeholderID,
		APIPrefix + "/Servlets",
		APIPrefix + "/System/Time",
		APIPrefix + "/cameras",
		APIPrefix + "/cameras/" + placeholderID,
		APIPrefix + "/Alarms",
		APIPrefix + "/rules",
		APIPrefix + "/recordingServers",
	} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, base+path, nil)
			if err != nil {
				t.Fatalf("build the request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("reach %s: %v", path, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("%s answered 404. The emulator serves the vendor's routes, and a route "+
					"that 404s is a contradiction the adapter's probe reports as one", path)
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s answered %s, want 200", path, resp.Status)
			}
		})
	}
}

// TestTheEmulatorRefusesAnUncredentialedRequest is the device's own access
// control.
//
// A vendor endpoint with no credential on it is a thing to be deliberate about,
// and an emulator that answers an anonymous reader is an emulator that would
// let anything on the machine read the site's camera inventory.
func TestTheEmulatorRefusesAnUncredentialedRequest(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	resp, err := http.Get(engine.URL() + "/api/rest/v1/Servlets")
	if err != nil {
		t.Fatalf("reach the emulator: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an uncredentialed request answered %s, want 401", resp.Status)
	}
}

// TestThePublishedEventCarriesTheAnalyticShape is the contract between the two
// sides.
//
// The platform reads `AnalyticData.Object.Value.ClassName`, and the adapter's
// redaction allow-list names `object.value.classname`. Spelling them differently
// here would be a disagreement the adapter resolves by degrading, and a degraded
// event is an event the operator never sees.
func TestThePublishedEventCarriesTheAnalyticShape(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	publishOne(t, engine, []Detection{{Class: classPerson, Confidence: 0.9, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.6}})
	base := engine.URL()

	req, err := http.NewRequest(http.MethodGet, base+"/api/rest/v1/events?size=5", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("reach the events route: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Array []map[string]any `json:"array"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the events: %v", err)
	}
	if len(body.Array) == 0 {
		t.Fatal("the emulator published no events")
	}
	item := body.Array[0]
	analytic, ok := item["AnalyticData"].(map[string]any)
	if !ok {
		t.Fatalf("the event carries no AnalyticData: %v", item)
	}
	object, ok := analytic["Object"].(map[string]any)
	if !ok {
		t.Fatalf("the AnalyticData carries no Object: %v", analytic)
	}
	value, ok := object["Value"].(map[string]any)
	if !ok {
		t.Fatalf("the Object carries no Value: %v", object)
	}
	if _, ok := value["ClassName"].(string); !ok {
		t.Errorf("the Value carries no ClassName, and the class is the one field that makes the "+
			"analytic shape worth having: %v", value)
	}
	if _, ok := value["DeviceObject"].(map[string]any); !ok {
		t.Errorf("the Value carries no DeviceObject, so the platform cannot tell which camera "+
			"asserted it: %v", value)
	}
	if vendor, _ := analytic["Vendor"].(string); vendor == "" {
		t.Error("the AnalyticData carries no Vendor, and the provenance distinction between the " +
			"VMS's own analytics and our model is the whole point of the field")
	}
}

// TestTheVendorFilterIsRead is the feed's cursor.
//
// The adapter asks for `time=gt:'...'` and a feed that ignores the filter
// returns the same page every poll, which is a cursor that never advances and a
// platform that re-reads its whole retention window on every tick.
func TestTheVendorFilterIsRead(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	publishOne(t, engine, []Detection{{Class: classPerson, Confidence: 0.9}})

	base := engine.URL()
	req, err := http.NewRequest(http.MethodGet, base+"/api/rest/v1/events?time=gt:'2030-01-01T00:00:00Z'", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("reach the events route: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Array []map[string]any `json:"array"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the events: %v", err)
	}
	if len(body.Array) != 0 {
		t.Errorf("a filter after 2030 returned %d events. The filter is ignored, and an ignored "+
			"filter is a cursor that never advances", len(body.Array))
	}
}

// TestABagInsideAPersonIsAPersonWithABag is the spatial association.
//
// It is the VMS's own analytics: the picture is not the platform's, so the
// platform never infers the association. It is computed here, from the two
// boxes in one frame.
func TestABagInsideAPersonIsAPersonWithABag(t *testing.T) {
	person := Detection{Class: classPerson, Confidence: 0.9, X1: 0.10, Y1: 0.10, X2: 0.50, Y2: 0.80}
	bag := Detection{Class: "backpack", Confidence: 0.8, X1: 0.20, Y1: 0.40, X2: 0.35, Y2: 0.60}

	refined := associate([]Detection{person, bag})
	found := false
	for _, d := range refined {
		if d.Class == classPersonWithBag {
			found = true
		}
	}
	if !found {
		t.Errorf("a backpack inside a person's box was not raised to %q: %+v", classPersonWithBag, refined)
	}
}

// TestABagBesideAPersonIsNotAPersonWithABag is the other half.
//
// A bag on the floor next to somebody is a bag on the floor. The association is
// containment, and a test that only checked the positive case would pass with
// an implementation that raises the class for every bag in the frame.
func TestABagBesideAPersonIsNotAPersonWithABag(t *testing.T) {
	person := Detection{Class: classPerson, Confidence: 0.9, X1: 0.10, Y1: 0.10, X2: 0.50, Y2: 0.80}
	bag := Detection{Class: "backpack", Confidence: 0.8, X1: 0.70, Y1: 0.40, X2: 0.85, Y2: 0.60}

	refined := associate([]Detection{person, bag})
	for _, d := range refined {
		if d.Class == classPersonWithBag {
			t.Errorf("a bag beside a person was raised to %q: %+v", classPersonWithBag, refined)
		}
	}
}

// TestTheCountTravelsInTheClassID is where the count goes.
//
// The adapter's redaction allow-list permits `object.value.classid` and nothing
// else in the object's value subtree, so a count in any other field is
// redacted on ingest and the platform silently loses it.
func TestTheCountTravelsInTheClassID(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	publishOne(t, engine, []Detection{
		{Class: classPerson, Confidence: 0.9},
		{Class: classPerson, Confidence: 0.8},
		{Class: classVehicle, Confidence: 0.7},
	})
	events := engine.eventsSince(time.Time{})
	var count *event
	for i := range events {
		if events[i].ClassName == classPersonCount {
			count = &events[i]
		}
	}
	if count == nil {
		t.Fatal("the frame published no person count")
	}
	if count.ClassID != "2" {
		t.Errorf("the person count travelled as %q, want \"2\"", count.ClassID)
	}
}

// TestTheControlsTakeEffectImmediately is what a control is for.
//
// A control that took effect on the next restart would be a control that does
// not work: the operator pauses a camera because something is happening in front
// of it, and the picture has to stop being REPORTED at once, not after the
// source is replayed.
func TestTheControlsTakeEffectImmediately(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	base := engine.URL()

	post := func(path string) map[string]any {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+path, nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("reach %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %s, want 200", path, resp.Status)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return body
	}

	paused := post(controlPrefix + "/cameras/entrance/pause")
	if paused["paused"] != true {
		t.Errorf("a pause answered with paused=%v, want true", paused["paused"])
	}
	playing := post(controlPrefix + "/cameras/entrance/play")
	if playing["paused"] != false {
		t.Errorf("a play answered with paused=%v, want false", playing["paused"])
	}
	rate := post(controlPrefix + "/cameras/entrance/fps?value=2")
	if rate["fps"] != 2.0 {
		t.Errorf("a frame rate of 2 answered with fps=%v", rate["fps"])
	}
	// The camera's own state agrees, because the control and the state are the
	// same camera.
	st, ok := engine.state("entrance")
	if !ok {
		t.Fatal("the camera was not created")
	}
	if st.FPS != 2 {
		t.Errorf("the camera's frame rate is %v after a control set it to 2", st.FPS)
	}
}

// TestAFrameRateOutsideTheRangeIsRefused is the control that is not a typo.
//
// A frame rate of zero measures nothing and reports a detection rate of zero
// over a scene with people in it, and a frame rate of a thousand asks the GPU
// for more than it can do. Both are refused by name.
func TestAFrameRateOutsideTheRangeIsRefused(t *testing.T) {
	engine := startEmulator(t, fakeDetector{})
	base := engine.URL()
	for _, value := range []string{"0", "600", "-1"} {
		req, err := http.NewRequest(http.MethodPost, base+controlPrefix+"/cameras/entrance/fps?value="+value, nil)
		if err != nil {
			t.Fatalf("build the request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("reach the control: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("a frame rate of %s was accepted. A rate outside the range is a typo, "+
				"and a typo that measures nothing is a camera that reports an empty road", value)
		}
	}
}

// TestTheSameFramesProduceTheSameEvents is the determinism the whole fixture
// design exists to protect, applied to the device that now publishes them.
//
// Two runs over the same frames must produce the same events, byte for byte.
// If they do not, then the demo is a performance rather than a system: the
// numbers on the screen would describe the run rather than the site, and the
// recorded session could not be replayed to check them.
//
// The clock is the emulator's, and it is FIXED here, so the only thing that
// could make the two runs differ is the publishing itself.
func TestTheSameFramesProduceTheSameEvents(t *testing.T) {
	frames := [][]Detection{
		{{Class: classPerson, Confidence: 0.91, X1: 0.10, Y1: 0.10, X2: 0.40, Y2: 0.80}},
		{
			{Class: classPerson, Confidence: 0.88, X1: 0.12, Y1: 0.12, X2: 0.42, Y2: 0.82},
			{Class: "backpack", Confidence: 0.77, X1: 0.20, Y1: 0.40, X2: 0.32, Y2: 0.58},
			{Class: classVehicle, Confidence: 0.66, X1: 0.60, Y1: 0.55, X2: 0.95, Y2: 0.75},
		},
		{{Class: classVehicle, Confidence: 0.71, X1: 0.55, Y1: 0.52, X2: 0.92, Y2: 0.74}},
	}

	publish := func() []string {
		engine := startEmulator(t, fakeDetector{})
		cam, ok := engine.camera("entrance")
		if !ok {
			t.Fatal("the camera was not created")
		}
		for _, dets := range frames {
			engine.publish(cam, dets)
		}
		var out []string
		for _, ev := range engine.eventsSince(time.Time{}) {
			raw, err := json.Marshal(ev.envelope())
			if err != nil {
				t.Fatalf("encode an event: %v", err)
			}
			out = append(out, string(raw))
		}
		return out
	}

	first := publish()
	second := publish()
	if len(first) != len(second) {
		t.Fatalf("the two runs published %d and %d events", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("event %d differs between two runs over the same frames:\n  %s\n  %s", i, first[i], second[i])
		}
	}
	if len(first) == 0 {
		t.Fatal("the runs published nothing, so the comparison proved nothing")
	}
}
