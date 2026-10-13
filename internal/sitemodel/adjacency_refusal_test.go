package sitemodel

import (
	"context"
	"testing"
)

// The service's refusals. They are the calls an operator makes by mistake, and
// each one is a sentence that names what was missing, so a run that fails says
// why rather than reporting a nil-pointer stack.
func TestTheServiceRefusesWhatItCannotUse(t *testing.T) {
	ctx := context.Background()

	var noPool Service
	if err := noPool.ReplaceFromModel(ctx, "t", "s", "v", &Site{ID: "s", Cameras: []Camera{{ID: "cam-a"}}}); err == nil {
		t.Error("a service with no pool accepted a write")
	}
	if _, err := noPool.Neighbours(ctx, "t", "s", "cam-a"); err == nil {
		t.Error("a service with no pool accepted a read")
	}
}

// A write needs a site id and a model. An empty site id writes a row no reader
// could reach, and a nil model derives nothing — it would delete the site's
// derived rows on the way to writing none, which is the one thing a projection
// must never do.
func TestAWriteNeedsASiteAndAModel(t *testing.T) {
	svc := &Service{}
	if err := svc.ReplaceFromModel(context.Background(), "t", "", "v", &Site{ID: "s"}); err == nil {
		t.Error("an empty site id was accepted")
	}
	if err := svc.ReplaceFromModel(context.Background(), "t", "s", "v", nil); err == nil {
		t.Error("a nil model was accepted; it would delete the derived rows and write none")
	}
	if err := svc.ReplaceFromModel(context.Background(), "t", "s", "v", &Site{ID: "s"}); err == nil {
		t.Error("a service with no pool was accepted")
	}
}
