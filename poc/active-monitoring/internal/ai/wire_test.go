package ai

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The wire is pinned from BOTH sides, and this is the Go side of the pin.
//
// The bug this exists to prevent made the whole POC silently useless: the sidecar
// began sending `input_w` and `input_h`, `Detect` decodes strictly, and so every
// single reply was refused. 169 of 169 frames came back `cannot_tell` — a
// DESIGNED state, so nothing was red. The tests were green. The report said
// `cannot_tell`, which reads as the tripwire working.
//
// A strict decoder is correct and stays. What changes is that the two sides are
// pinned against each other, so a field added on one without the other is a test
// failure instead of a whole quiet run.
//
// The Python side of the same pin is pyworker/test_wire.py, which asserts the
// live sidecar's key set equals the same fixture file.

// recordedTelemetryKeys is the fixture the sidecar is pinned against.
func recordedTelemetryKeys(t *testing.T) []string {
	t.Helper()
	// The fixture lives next to the sidecar, which is above this package. The path
	// is walked up out of `internal/ai` rather than hardcoded, because a
	// hardcoded path is a test that fails for the wrong reason on another
	// machine and looks like a missing fixture.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "pyworker", "fixtures", "telemetry_keys.json"))
	if err != nil {
		t.Fatalf("the telemetry key fixture cannot be read, so the wire is unpinned: %v", err)
	}
	var f struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("the telemetry key fixture is not readable: %v", err)
	}
	if len(f.Keys) < 15 {
		t.Fatalf("the telemetry key fixture has %d keys; a fixture that lost its contents would make this test compare [] to [] and pass", len(f.Keys))
	}
	return f.Keys
}

// TestTheSidecarTelemetryShapeIsAccepted: every key the sidecar records must be
// one the Go struct can hold.
//
// It is asserted by DECODING, with the same strict decoder production uses, so a
// field the Go side is missing fails here with the same message the run would
// have given 169 times.
func TestTheSidecarTelemetryShapeIsAccepted(t *testing.T) {
	// Every key present, with a value of the right JSON kind. A number key needs
	// a number, a bool key a bool, and a string key a string, or the decode would
	// fail for a reason that has nothing to do with the key set.
	payload := map[string]any{
		"seq":      1,
		"boxes":    []Box{},
		"width":    640,
		"height":   360,
		"model":    "yolov4n.onnx",
		"infer_ms": 12.5,
	}
	telemetry := map[string]any{}
	for _, k := range recordedTelemetryKeys(t) {
		switch k {
		case "has_objectness":
			telemetry[k] = true
		case "coord_space", "layout", "decoder_family":
			telemetry[k] = "pixel"
		default:
			telemetry[k] = 1.0
		}
	}
	payload["telemetry"] = telemetry

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("the sidecar's recorded telemetry shape does not decode on the Go side: %v.\n"+
			"  A key the sidecar sends and this build does not have makes EVERY reply "+
			"unreadable, and the run reports `cannot_tell` on every frame with nothing red. "+
			"  Add the field to internal/ai.Telemetry and re-record "+
			"pyworker/fixtures/telemetry_keys.json in the SAME commit.", err)
	}
	if out.Telemetry.InputW != 1 {
		t.Fatalf("input_w decoded as %d; the key is present but not carried", out.Telemetry.InputW)
	}
}

// TestTheTripwireRefusesNothingForAMeaningfulTelemetry: the same payload must
// also INTERPRET.
//
// It is the other half and it is the half that actually matters at runtime. A
// sidecar that sends a well-formed answer full of numbers must not be refused by
// the tripwire, or the POC reports `cannot_tell` forever against a model that is
// working perfectly — which is the state the run above was in, for a different
// reason and with the same visible symptom.
func TestTheTripwireRefusesNothingForAMeaningfulTelemetry(t *testing.T) {
	tel := Telemetry{
		Layout: "nhwc", DecoderFamily: "anchored", CoordSpace: "pixel",
		Fields: 85, Classes: 0, InputW: 416, InputH: 416,
		Candidates: 10647, PreNms: 63, PostNmsClass: 22, PostNms: 22,
		MedianBoxArea: 0.001379, MedianObjectPx: 24.29, IdenticalGeometry: 0,
	}
	yes := true
	tel.HasObjectness = &yes
	state, reason := Interpret(1, tel, []Box{{Class: "car", Score: 0.84}})
	if state != StateAnswered {
		t.Fatalf("a complete, self-consistent telemetry block was refused: state=%s reason=%s.\n"+
			"  The measured model produces exactly this telemetry, so a refusal here "+
			"means the POC reports cannot_tell on every frame of every run and detects "+
			"nothing at all", state, reason)
	}
}
