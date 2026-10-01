package novelty

import (
	"testing"
	"time"
)

// TestAnUnpinnedReferenceTriggerNeverSaysUnchanged is RC-11. A reference-mode
// engine with nothing pinned has compared every frame against NOTHING, and a
// status page that renders that as `unchanged` is making a fabricated claim about a
// physical object: that the camera has not been moved, covered or knocked.
func TestAnUnpinnedReferenceTriggerNeverSaysUnchanged(t *testing.T) {
	e := newReference(t, nil)
	if got := e.IntegrityState(); got != IntegrityUnpinned {
		t.Fatalf("state = %q before anything was pinned; it must be unpinned", got)
	}
	// And a quiet frame does not change that. This is the branch the guard exists
	// for: the engine has been fed the same frame a hundred times and has no
	// baseline, so the score is unpinned rather than below-threshold.
	quiet := scene(320, 240, 0, 0, 0)
	for i := 0; i < 50; i++ {
		e.Watch(time.Now(), quiet, 320, 240)
	}
	if e.Last().Reason != ReasonUnpinned {
		t.Fatalf("reason = %q; an unpinned engine must keep saying unpinned however quiet the frames are", e.Last().Reason)
	}
	if got := e.IntegrityState(); got != IntegrityUnpinned {
		t.Fatalf("state = %q after fifty quiet frames with no reference; it must still be unpinned", got)
	}
	if e.Last().Unchanged() {
		t.Fatal("a score whose reason is unpinned reports itself unchanged")
	}
}

// TestAPinnedReferenceTriggerCanSayUnchangedAndChanged: the refusal above must not
// make the word unreachable. A pinned reference compared against quiet frames IS
// evidence of integrity, and refusing to say it would make the tool useless.
func TestAPinnedReferenceTriggerCanSayUnchangedAndChanged(t *testing.T) {
	ref := scene(320, 240, 0, 0, 0)
	moved := scene(320, 240, 36, 30, 0)

	e := newReference(t, nil)
	if err := e.Pin(ref, 320, 240, "sha-ref", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Identical frames against a pinned reference: a real comparison, and it found
	// nothing.
	for i := 0; i < 5; i++ {
		e.Watch(time.Now(), ref, 320, 240)
	}
	if got := e.IntegrityState(); got != IntegrityUnchanged {
		t.Fatalf("state = %q; a pinned reference compared against the same frame IS unchanged", got)
	}
	if !e.Last().Unchanged() {
		t.Fatalf("reason = %q; a below-threshold score against a pinned reference is a real negative", e.Last().Reason)
	}

	// And a sustained change flips it.
	for i := 0; i < 10; i++ {
		e.Watch(time.Now(), moved, 320, 240)
	}
	if got := e.IntegrityState(); got != IntegrityChanged {
		t.Fatalf("state = %q; a sustained difference from the pinned view is a change", got)
	}
}

// TestATemporalEngineHasNoIntegrityClaim: the temporal detector has no reference,
// so it must not be rendered as one with an opinion about a camera's integrity.
func TestATemporalEngineHasNoIntegrityClaim(t *testing.T) {
	e, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if got := e.IntegrityState(); got != IntegrityUnpinned {
		t.Fatalf("state = %q for a temporal engine; it has no reference and no integrity claim", got)
	}
}

// TestTheFirstFrameAndACorruptFrameAreNotNegatives pins the other two reasons the
// same rule covers, so a new reason added later has an obvious place to be added.
func TestTheFirstFrameAndACorruptFrameAreNotNegatives(t *testing.T) {
	for _, r := range []Reason{ReasonUnpinned, ReasonFirstFrame, ReasonNoPrevious, ReasonCorrupt} {
		if (Score{Reason: r}).Unchanged() {
			t.Fatalf("reason %q reports itself unchanged; it means the measurement never happened", r)
		}
	}
	for _, r := range []Reason{ReasonBelowThreshold, ReasonBelowRelease, ReasonNotSustained, ReasonIlluminationOnly, ReasonNoComponent, ReasonCooldown} {
		if !(Score{Reason: r}).Unchanged() {
			t.Fatalf("reason %q does not report itself unchanged; it IS a comparison that found nothing", r)
		}
	}
}
