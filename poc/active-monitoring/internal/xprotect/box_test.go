package xprotect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAnAllZeroBoxIsRefused is RC-6, at the place where the box becomes a wire
// body.
//
// Every pushed event used to carry top/left/bottom/right all zero while the row in
// the database beside it held real numbers. The bounding box is what makes a VMS
// draw the object on the operator's timeline, so that is the demo's worst possible
// failure and it happened while the database disagreed with the wire in the
// operator's favour.
//
// The test lives HERE rather than in the pipeline because the pipeline never
// produces a zero box: `regionThousandths` clamps an empty measurement to the whole
// frame. The refusal is the second line of defence, and a second line of defence
// that is only exercised by a caller that cannot exist is not a defence.
func TestAnAllZeroBoxIsRefused(t *testing.T) {
	base := func(box Box) EventInput {
		return EventInput{
			EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "aicap-alarm-abc",
			Class: "motion", Value: "motion", Confidence: 0.5, RuleName: "alarm-l", Box: box,
		}
	}
	if _, err := BuildEvent(base(Box{})); err == nil {
		t.Fatal("an event with an all-zero bounding box was accepted; the VMS would draw nothing on the operator timeline")
	}
	_, err := BuildEvent(base(Box{}))
	if !strings.Contains(err.Error(), "all zero") {
		t.Fatalf("the refusal does not say the box is all zero: %v", err)
	}
	if !strings.Contains(err.Error(), "measured") {
		t.Fatalf("the refusal does not say the region is measured locally: %v", err)
	}

	// A partial zero is not a zero box: a box that touches the top edge legitimately
	// has top=0, and refusing that would make the top strip of every frame
	// unpushable.
	for _, ok := range []Box{
		{Top: 0, Left: 0, Bottom: 1000, Right: 1000}, // the whole frame
		{Top: 0, Left: 100, Bottom: 400, Right: 500}, // touches the top edge
		{Top: 200, Left: 0, Bottom: 600, Right: 300}, // touches the left edge
		{Top: 200, Left: 100, Bottom: 1000, Right: 500},
		{Top: 200, Left: 100, Bottom: 600, Right: 1000},
		{Top: 1, Left: 1, Bottom: 2, Right: 2}, // tiny but real
	} {
		if _, err := BuildEvent(base(ok)); err != nil {
			t.Fatalf("a real box %+v was refused: %v", ok, err)
		}
	}

	// An inverted box is a DIFFERENT defect and gets a different sentence, so the
	// red check on the zero box cannot pass vacuously through the inverted check.
	if _, err := BuildEvent(base(Box{Top: 600, Left: 500, Bottom: 200, Right: 100})); err == nil {
		t.Fatal("an inverted bounding box was accepted")
	} else if strings.Contains(err.Error(), "all zero") {
		t.Fatalf("the inverted-box refusal quotes the wrong defect: %v", err)
	}
}

// TestTheBoxSurvivesTheBodyUnchanged: the box that goes on the wire is the box that
// was measured, in the same 0..1000 thousandths the detection row stores, and there
// is no scaling step between them to drift.
func TestTheBoxSurvivesTheBodyUnchanged(t *testing.T) {
	in := EventInput{
		EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "aicap-alarm-abc",
		Class: "motion", Value: "motion", Confidence: 0.5, RuleName: "alarm-l",
		Box: Box{Top: 120, Left: 200, Bottom: 460, Right: 520},
	}
	body, err := BuildEvent(in)
	if err != nil {
		t.Fatal(err)
	}
	if body.Data.Object.BoundingBox != in.Box {
		t.Fatalf("the body's box is %+v and the input's is %+v; there is a scaling step between the measurement and the wire",
			body.Data.Object.BoundingBox, in.Box)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"top":120`, `"left":200`, `"bottom":460`, `"right":520`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the serialised body does not carry %s: %s", want, raw)
		}
	}
}
