package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// healthyTelemetry is a decode that worked, built once so every test that means
// "nothing wrong here" says the same thing.
func healthyTelemetry() Telemetry {
	no := false
	return Telemetry{
		Layout: "nchw", DecoderFamily: "fused", CoordSpace: "pixel",
		HasObjectness: &no, Fields: 84, Classes: 80,
		Candidates: 1200, PreNms: 9, PostNmsClass: 4, PostNms: 4,
		NMSClassIoU: 0.45, NMSGlobalIoU: 0.75,
		MedianBoxArea: 0.004, MedianObjectPx: 24.5, IdenticalGeometry: 0, Scales: 1,
	}
}

func boxes() []Box {
	return []Box{{Class: "car", Score: 0.87, X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.4}}
}

// TestAHealthyDecodeIsAnswered is the control. A tripwire with no test that
// asserts the healthy case PASSES is a tripwire that might refuse everything, and
// a run that refused everything would look exactly like a run that found nothing.
func TestAHealthyDecodeIsAnswered(t *testing.T) {
	state, why := Interpret(1, healthyTelemetry(), boxes())
	if state != StateAnswered {
		t.Fatalf("a healthy decode was refused as %s: %s", state, why)
	}
	if why != "" {
		t.Errorf("a healthy frame carries a reason %q; a healthy frame's reason is nothing and printing it on every frame of a week is noise", why)
	}
}

// TestAnEmptyButHealthyAnswerIsNoObjects: the whole point of the five-word
// vocabulary. The model looked and saw nothing, which is a MEASUREMENT, and it is
// a different measurement from "the sidecar could not tell".
func TestAnEmptyButHealthyAnswerIsNoObjects(t *testing.T) {
	state, why := Interpret(1, healthyTelemetry(), nil)
	if state != StateNoObjects {
		t.Fatalf("an empty healthy answer is %s, want no_objects: %s", state, why)
	}
	// `no_objects` is a real NEGATIVE observation, so it is not an absence. The
	// three absences are degraded, not_asked and cannot_tell, and AC6 scores
	// against those: a run that called a quiet road "absent" would be reporting a
	// gap it never had.
	if AbsenceOf(state) {
		t.Error("no_objects is reported as an absence; it is a real negative observation and AC6 must not count it as one")
	}
	for _, absent := range []string{StateDegraded, StateNotAsked, StateCannotTell} {
		if !AbsenceOf(absent) {
			t.Errorf("%s is not counted as an absence", absent)
		}
	}
}

// TestTheTripwireTurnsEachOfTheFourCollapsesIntoCannotTell is T4.2, and each case
// is one of the four arithmetic bugs of §4 stated as a number the run can check.
func TestTheTripwireTurnsEachOfTheFourCollapsesIntoCannotTell(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Telemetry)
		boxes   []Box
		wantWhy string
	}{
		{
			// D1. Every box collapsed onto the frame's corner after a sigmoid
			// reached the box coordinates. Eight identical boxes is what it
			// produced, on every frame of every run.
			name:    "eight identical boxes",
			mutate:  func(tm *Telemetry) { tm.IdenticalGeometry = 8 },
			wantWhy: "collapsed",
		},
		{
			// The same, at the threshold's edge: two duplicates are a legitimate
			// answer on an ambiguous frame and must NOT trip. That is the other
			// half and it is why the limit is two and not zero.
			name:    "two identical boxes are a scene, not an arithmetic failure",
			mutate:  func(tm *Telemetry) { tm.IdenticalGeometry = 2 },
			wantWhy: "",
		},
		{
			// D2. The coordinate space was never decided on raw values, so every
			// box was in a unit nobody could name.
			name:    "unknown coordinate space",
			mutate:  func(tm *Telemetry) { tm.CoordSpace = "unknown" },
			wantWhy: "pixels or fractions",
		},
		{
			// D3. The field count matched neither 4+classes nor 5+classes, so
			// every class index would shift by one.
			name:    "undecided objectness",
			mutate:  func(tm *Telemetry) { tm.HasObjectness = nil },
			wantWhy: "shift by one",
		},
		{
			// The small end of the same class of bug: a box two pixels tall
			// crossing a line is a compression artefact.
			name:    "median object is two pixels tall",
			mutate:  func(tm *Telemetry) { tm.MedianObjectPx = 2 },
			wantWhy: "compression noise",
		},
		{
			// An old sidecar that predates the telemetry entirely.
			name:    "no telemetry at all",
			mutate:  func(tm *Telemetry) { *tm = Telemetry{} },
			wantWhy: "no telemetry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tm := healthyTelemetry()
			tc.mutate(&tm)
			bs := tc.boxes
			if bs == nil {
				bs = boxes()
			}
			state, why := Interpret(1, tm, bs)
			if tc.wantWhy == "" {
				if state != StateAnswered {
					t.Fatalf("%s was refused as %s: %s", tc.name, state, why)
				}
				return
			}
			if state != StateCannotTell {
				t.Fatalf("%s was answered as %s; it is arithmetic, not a scene", tc.name, state)
			}
			if !strings.Contains(why, tc.wantWhy) {
				t.Errorf("the refusal does not say %q: %s", tc.wantWhy, why)
			}
		})
	}
}

// TestAFrameWithNoBoxesIsNotTrippedByAnAbsenceOfGeometry: the tripwire's geometry
// checks are about the boxes that were FOUND. A frame with none has no geometry
// to be wrong about, and a decoder that reported `identical_geometry: 0` on an
// empty frame is reporting a measurement, not a fault.
func TestAFrameWithNoBoxesIsNotTrippedByAnAbsenceOfGeometry(t *testing.T) {
	tm := healthyTelemetry()
	tm.IdenticalGeometry = 0
	tm.MedianObjectPx = 0
	if state, why := Interpret(1, tm, nil); state != StateNoObjects {
		t.Fatalf("an empty healthy frame is %s, want no_objects: %s", state, why)
	}
	// And a zero median must not be read as "smaller than three pixels": zero is
	// the decoder's way of saying "there was nothing to measure", and the
	// comparison is written to notice that difference.
	tm.MedianObjectPx = 0
	tm.IdenticalGeometry = 0
	if state, _ := Interpret(1, tm, boxes()); state == StateCannotTell {
		t.Fatal("a zero median object height tripped the floor: zero means 'nothing to measure', not 'smaller than a pixel'")
	}
}

// TestTelemetryIsPartOfTheStrictWireFormat: the reply is decoded with
// DisallowUnknownFields, so the sidecar and the client cannot drift apart
// silently. A dropped field is a number the run cannot check.
func TestTelemetryIsPartOfTheStrictWireFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"seq": 1, "boxes": []Box{}, "width": 640, "height": 384, "model": "x",
			"telemetry": map[string]any{"coord_space": "pixel", "a_key_this_build_never_heard_of": 1},
		})
	}))
	defer srv.Close()
	c, err := New(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res := c.Detect(context.Background(), 1, make([]byte, 640*384*3), 640, 384)
	if res.State != StateCannotTell {
		t.Fatalf("an unrecognised telemetry key was accepted as %s; the sidecar and the client can drift apart silently", res.State)
	}
}

// TestTelemetrySurvivesTheRoundTrip: the struct is the wire format, so a field
// added here and not in JSON round-trips to nothing and the tripwire reads a zero
// as a measurement. This is the test that would catch that.
func TestTelemetrySurvivesTheRoundTrip(t *testing.T) {
	in := healthyTelemetry()
	yes := true
	in.HasObjectness = &yes
	in.CoordSpace = "fraction"
	in.DecoderFamily = "anchored"
	in.Scales = 3
	in.Note = "the sidecar said this"
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Telemetry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.CoordSpace != "fraction" || out.DecoderFamily != "anchored" || out.Scales != 3 ||
		out.Fields != in.Fields || out.IdenticalGeometry != in.IdenticalGeometry ||
		out.MedianObjectPx != in.MedianObjectPx || out.Note != in.Note {
		t.Fatalf("the telemetry did not survive its own wire format:\n in: %+v\nout: %+v", in, out)
	}
	if out.HasObjectness == nil || *out.HasObjectness != true {
		t.Error("has_objectness did not survive as true")
	}
	// And null must survive as null, because null is the state that refuses the
	// frame and a bool's zero value is `false`, which would be the opposite
	// decision.
	var nullOut Telemetry
	if err := json.Unmarshal([]byte(`{"has_objectness":null}`), &nullOut); err != nil {
		t.Fatal(err)
	}
	if nullOut.HasObjectness != nil {
		t.Error("an explicit null decoded as a decision: null means 'cannot be decided' and false means 'there is no objectness'")
	}
}

// TestSeenDistinguishesAnOldSidecarFromAQuietScene is the subtle one. A reply
// with no telemetry and a reply whose telemetry is all zeros are different
// situations and they must not produce the same answer.
func TestSeenDistinguishesAnOldSidecarFromAQuietScene(t *testing.T) {
	if (Telemetry{}).Seen() {
		t.Error("a zero telemetry block claims to have been seen")
	}
	if !(Telemetry{CoordSpace: "pixel"}).Seen() {
		t.Error("a coordinate space alone does not count as seen")
	}
	if !(Telemetry{Note: "something"}).Seen() {
		t.Error("a note alone does not count as seen")
	}
	if state, _ := Interpret(1, Telemetry{}, boxes()); state != StateCannotTell {
		t.Error("a sidecar that sent no telemetry was allowed to report objects")
	}
}
