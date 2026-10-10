package main

import (
	"testing"
	"time"
)

func TestMapCOCOClass(t *testing.T) {
	cases := []struct {
		in, cat, sub string
	}{
		{"person", "biological", "human"},
		{"car", "vehicle", "car"},
		{"truck", "vehicle", "truck"},
		{"bus", "vehicle", "bus"},
		{"motorcycle", "vehicle", "motorcycle"},
		{"dog", "biological", "dog"},
		{"bird", "biological", "bird"},
		{"cat", "biological", "cat"},
		{"backpack", "other", "backpack"},
		{"traffic light", "other", "traffic light"},
		{"bench", "other", "bench"},
	}
	for _, c := range cases {
		cat, sub := mapCOCOClass(c.in)
		if cat != c.cat || sub != c.sub {
			t.Errorf("mapCOCOClass(%q) = (%q,%q), want (%q,%q)", c.in, cat, sub, c.cat, c.sub)
		}
	}
	if cat, sub := mapCOCOClass("something-new"); cat != "other" || sub != "something-new" {
		t.Errorf("unknown class mapped to (%q,%q), want it visible in other", cat, sub)
	}
}

func TestDetectionLabel(t *testing.T) {
	if got := detectionLabel("vehicle", "car", "red"); got != "red car" {
		t.Errorf("vehicle label = %q, want %q", got, "red car")
	}
	if got := detectionLabel("vehicle", "truck", ""); got != "truck" {
		t.Errorf("colourless vehicle label = %q, want %q", got, "truck")
	}
	if got := detectionLabel("biological", "human", ""); got != "person" {
		t.Errorf("human label = %q, want %q", got, "person")
	}
	if got := detectionLabel("biological", "dog", ""); got != "dog" {
		t.Errorf("dog label = %q", got)
	}
	if got := detectionLabel("other", "backpack", ""); got != "backpack" {
		t.Errorf("other label = %q", got)
	}
}

func TestNameColor(t *testing.T) {
	cases := []struct {
		r, g, b float64
		want    string
	}{
		{0.85, 0.10, 0.10, "red"},
		{0.10, 0.20, 0.85, "blue"},
		{0.10, 0.75, 0.15, "green"},
		{0.95, 0.90, 0.10, "yellow"},
		{0.95, 0.95, 0.95, "white"},
		{0.05, 0.05, 0.05, "black"},
		{0.55, 0.55, 0.55, "silver"},
		{0.30, 0.30, 0.32, "gray"},
		{0.35, 0.18, 0.07, "brown"},
		{0.90, 0.55, 0.15, "orange"},
	}
	for _, c := range cases {
		if got := nameColor(c.r, c.g, c.b); got != c.want {
			t.Errorf("nameColor(%.2f,%.2f,%.2f) = %q, want %q", c.r, c.g, c.b, got, c.want)
		}
	}
}

func TestEstimateColorSamplesInsideBox(t *testing.T) {
	const w, h = 1280, 720
	px := make([]byte, w*h*3)
	// Background white everywhere.
	for i := range px {
		px[i] = 245
	}
	// A solid blue vehicle region.
	box := wireBox{X: 400, Y: 300, Width: 200, Height: 100}
	for y := int(box.Y); y < int(box.Y+box.Height); y++ {
		for x := int(box.X); x < int(box.X+box.Width); x++ {
			i := (y*w + x) * 3
			px[i], px[i+1], px[i+2] = 20, 40, 220
		}
	}
	if got := estimateColor(px, w, h, box); got != "blue" {
		t.Errorf("estimateColor = %q, want blue", got)
	}
	// A degenerate box must return "" rather than guess.
	if got := estimateColor(px, w, h, wireBox{X: 10, Y: 10, Width: 0, Height: 0}); got != "" {
		t.Errorf("degenerate box colour = %q, want empty", got)
	}
}

func TestTrackerKeepsIdentityAcrossFrames(t *testing.T) {
	tr := &tracker{}
	now := time.Now()

	frame1 := []frameObject{
		{Category: "vehicle", Sub: "car", BBox: wireBox{X: 100, Y: 500, Width: 200, Height: 100}},
		{Category: "biological", Sub: "human", BBox: wireBox{X: 600, Y: 300, Width: 60, Height: 160}},
	}
	frame1 = tr.update(frame1, now)
	idCar1, idPerson1 := frame1[0].ID, frame1[1].ID
	if idCar1 == "" || idPerson1 == "" || idCar1 == idPerson1 {
		t.Fatalf("ids not assigned distinctly: %q %q", idCar1, idPerson1)
	}

	// Same scene, slightly moved: ids must hold.
	frame2 := []frameObject{
		{Category: "vehicle", Sub: "car", BBox: wireBox{X: 130, Y: 500, Width: 200, Height: 100}},
		{Category: "biological", Sub: "human", BBox: wireBox{X: 605, Y: 305, Width: 60, Height: 160}},
	}
	frame2 = tr.update(frame2, now.Add(time.Second))
	if frame2[0].ID != idCar1 || frame2[1].ID != idPerson1 {
		t.Errorf("identity not stable: car %q->%q person %q->%q",
			idCar1, frame2[0].ID, idPerson1, frame2[1].ID)
	}

	// A different class at the same spot is a different track.
	frame3 := []frameObject{
		{Category: "vehicle", Sub: "truck", BBox: wireBox{X: 130, Y: 500, Width: 200, Height: 100}},
	}
	frame3 = tr.update(frame3, now.Add(2*time.Second))
	if frame3[0].ID == idCar1 {
		t.Errorf("a truck reused the car's id %q", idCar1)
	}

	// After the TTL the track is gone: a return gets a fresh id, so a
	// re-entry alerts again instead of being invisible.
	frame4 := []frameObject{
		{Category: "vehicle", Sub: "car", BBox: wireBox{X: 100, Y: 500, Width: 200, Height: 100}},
	}
	frame4 = tr.update(frame4, now.Add(trackTTL+2*time.Second))
	if frame4[0].ID == idCar1 {
		t.Errorf("expired track %q was reused", idCar1)
	}
}
