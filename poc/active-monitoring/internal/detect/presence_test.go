package detect

// Presence mode: a zone with no lines, and the source-aware track minimum.
//
// The tests here are the ones the crop pass and the zero-configuration claim both rest
// on. "Everything that passes" is only true if an object seen twice in a magnified crop
// becomes an event, and only honest if an object seen twice in a downscaled whole frame
// does not.

import (
	"strings"
	"testing"
	"time"
)

// presenceConfig is the minimum configuration presence mode runs on: a class list, an
// association threshold, and the two minima.
func presenceConfig() Config {
	c := testConfig()
	c.TrackMinFrames = 3
	c.CutMinFrames = 2
	return c
}

func presenceEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := New(Zone{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// feedCut is one frame in which the given boxes were found by looking at magnified
// crops. The engine cannot tell them apart from whole-frame boxes on its own, it is
// told through `Answer.Cut`, which is exactly the channel the runner uses.
func feedCut(e *Engine, at time.Time, boxes []Box, cut ...Box) []Crossing {
	c, _, _ := e.FeedAnswered(at, Answer{Boxes: boxes, AIState: "asked_answered", Cut: cut})
	return c
}

func feedWhole(e *Engine, at time.Time, boxes ...Box) []Crossing {
	c, _, _ := e.FeedAnswered(at, Answer{Boxes: boxes, AIState: "asked_answered"})
	return c
}

// TestPresenceWithNoZoneProducesOneEventPerTrack is the headline control: no line is
// drawn, nothing is configured, and a tracked object is reported once.
//
// The specific thing being protected is the word ONCE. A presence event that repeats
// per frame is the same failure as a crossing event that repeats per frame, which this
// package has a whole latch for, and a bird on screen for four seconds at two samples
// a second would otherwise be eight events.
func TestPresenceWithNoZoneProducesOneEventPerTrack(t *testing.T) {
	e := presenceEngine(t, presenceConfig())
	at := time.Now().UTC()

	b := carAt(0.5, 0.5)
	var total []Crossing
	// Five sightings: three are enough for the bar, and the remaining two must not
	// produce anything.
	for i := 0; i < 5; i++ {
		total = append(total, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)...)
	}
	if len(total) != 1 {
		t.Fatalf("five sightings of one object produced %d events, want exactly 1. A presence event "+
			"that repeats per frame turns one bird into a hundred events and the event rate in the "+
			"report becomes a function of the frame rate", len(total))
	}
	got := total[0]
	if got.Line != "" || got.Direction != "" {
		t.Fatalf("the event carries line %q direction %q. There is no line and there is no direction, "+
			"and inventing either would put a name on an event that never crossed anything",
			got.Line, got.Direction)
	}
	if got.Class != "car" {
		t.Fatalf("class = %q, want the DETECTED class car. The whole claim of this mode is that the "+
			"class comes from the detector and not from a per-camera list", got.Class)
	}
	if got.TrackID == 0 {
		t.Fatal("the event carries no track id, so it cannot be tied back to the track that produced it")
	}
	if got.Frames < 3 {
		t.Fatalf("frames = %d, want at least the minimum of 3", got.Frames)
	}
	if got.Score <= 0 {
		t.Fatalf("score = %v. A pushed event with confidence 0 tells an operator the detector had no "+
			"opinion, which is not what happened", got.Score)
	}
	if !got.Box.NonEmpty() {
		t.Fatalf("box = %+v, want a measured extent the operator can see", got.Box)
	}
}

// TestTheLatchIsTheOnlyThingHoldingOneTrackToOneEvent is the red-check on the latch.
//
// It exists because the test above does NOT protect the latch. That test has a
// ten-second cooldown and five sightings two hundred milliseconds apart, so removing the
// latch still yields one event: the cooldown suppresses the repeats. A test that passes
// for the wrong reason is worse than no test, because it appears in a report as
// evidence that the latch is watched.
//
// So the cooldown is set to ZERO here. Nothing but the latch can suppress anything: one
// track, six sightings, one event.
func TestTheLatchIsTheOnlyThingHoldingOneTrackToOneEvent(t *testing.T) {
	cfg := presenceConfig()
	cfg.Cooldown = 0
	e := presenceEngine(t, cfg)
	at := time.Now().UTC()
	b := carAt(0.5, 0.5)

	var got []Crossing
	for i := 0; i < 6; i++ {
		got = append(got, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)...)
	}
	if len(got) != 1 {
		t.Fatalf("with the cooldown off, six sightings of ONE track produced %d events, want 1. The "+
			"per-track latch is the only mechanism standing between presence mode and an event per "+
			"frame per object, and a test that lets the cooldown stand in for it proves nothing "+
			"about it", len(got))
	}
	// And a SECOND track is a second event. It needs its own three sightings, because a
	// new track is not an object yet, and the same anti-jitter rule applies to it.
	// Feeding it once would be testing nothing.
	other := carAt(0.2, 0.2)
	for i := 0; i < 3; i++ {
		got = append(got, feedWhole(e, at.Add(time.Duration(3+i)*200*time.Millisecond), other)...)
	}
	if len(got) != 2 {
		t.Fatalf("a second track produced %d cumulative events, want 2. One track is one event; two "+
			"tracks are two, and the latch is what tells them apart", len(got))
	}
	if got[1].TrackID == got[0].TrackID {
		t.Fatalf("two boxes at different places share track id %d, so the latch cannot be telling "+
			"tracks apart at all", got[0].TrackID)
	}
}

// TestATrackOverTrackMinFramesEmitsExactlyOneEvent is the same claim stated about the
// boundary, because a latch that fires one frame late is a latch that is not a latch.
func TestATrackOverTrackMinFramesEmitsExactlyOneEvent(t *testing.T) {
	e := presenceEngine(t, presenceConfig())
	at := time.Now().UTC()
	b := carAt(0.4, 0.4)

	// Exactly TrackMinFrames sightings. The event must come on the last of them, not one
	// later and not one earlier.
	var total []Crossing
	for i := 0; i < 3; i++ {
		total = append(total, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)...)
	}
	if len(total) != 1 {
		t.Fatalf("three sightings against a minimum of 3 produced %d events, want 1", len(total))
	}
	// A different class is a different track and its own event, after the class cooldown.
	// The latch is per TRACK, not per class.
	bird := Box{Class: "bird", Score: 0.8, X1: 0.3, Y1: 0.3, X2: 0.5, Y2: 0.5}
	var second []Crossing
	for i := 0; i < 3; i++ {
		second = append(second, feedWhole(e, at.Add(time.Duration(3+i)*200*time.Millisecond), bird)...)
	}
	if len(second) != 1 {
		t.Fatalf("a different class produced %d events, want 1: the latch is per TRACK, and the class "+
			"cooldown is a separate mechanism with a separate window", len(second))
	}
}

// TestASmallObjectInACropIsFoundWhereTheWholeFramePassMissesIt is the crop pass's
// headline case, and it is the test that protects the source-aware minimum.
//
// The two objects below are seen the SAME NUMBER OF TIMES, at the SAME class, in the
// SAME engine. The only difference is which pass found them. The magnified one is
// reported at two sightings; the downscaled one is not reported at three. That
// difference IS the crop pass. Without it there is no reason to run a second inference,
// and the smallest object the system exists to report is erased by the rule written to
// reject compression artefacts.
func TestASmallObjectInACropIsFoundWhereTheWholeFramePassMissesIt(t *testing.T) {
	at := time.Now().UTC()

	// The whole-frame object: three sightings against a minimum of 3.
	whole := presenceEngine(t, presenceConfig())
	var wholeEvents []Crossing
	wb := carAt(0.3, 0.3)
	for i := 0; i < 3; i++ {
		wholeEvents = append(wholeEvents, feedWhole(whole, at.Add(time.Duration(i)*200*time.Millisecond), wb)...)
	}

	// The magnified object: the same class and score, found twice, in a crop.
	cut := presenceEngine(t, presenceConfig())
	bird := Box{Class: "bird", Score: 0.8, X1: 0.55, Y1: 0.55, X2: 0.6, Y2: 0.6}
	var cutEvents []Crossing
	for i := 0; i < 2; i++ {
		cutEvents = append(cutEvents, feedCut(cut, at.Add(time.Duration(i)*200*time.Millisecond),
			[]Box{bird}, bird)...)
	}

	if len(cutEvents) != 1 {
		t.Fatalf("a bird found in a magnified crop on TWO sightings produced %d events, want 1. A bird "+
			"at 30 m is on screen for about a second and a half, which at the detection rate is two or "+
			"three samples, so a crop-sourced track held to the whole-frame bar of 3 is erased by the "+
			"anti-jitter rule and the headline case quietly stops working", len(cutEvents))
	}
	if !cutEvents[0].Cut {
		t.Fatal("the event does not say the box came from a magnified crop. That is a stored column the " +
			"replay reads, so a hardcoded answer here is a false record on the one field a reader would " +
			"use to tell a whole-frame detection from a crop-sourced one")
	}
	// The control: the SAME bird, the SAME two sightings, NOT in a crop, must produce
	// nothing. Without this the test would pass on any engine that simply lowered its
	// global minimum, which is the change that re-admits the single-frame noise the rule
	// exists to stop.
	uncut := presenceEngine(t, presenceConfig())
	var uncutEvents []Crossing
	for i := 0; i < 2; i++ {
		uncutEvents = append(uncutEvents, feedWhole(uncut, at.Add(time.Duration(i)*200*time.Millisecond), bird)...)
	}
	if len(uncutEvents) != 0 {
		t.Fatalf("the same object on the same two sightings produced %d events when it was NOT in a "+
			"crop. The minimum has to be source-aware and not merely lower: a lower global minimum "+
			"re-admits exactly the single-frame shadow and artefact noise the rule was added to stop, "+
			"and that would be visible in the false-positive rate", len(uncutEvents))
	}
	if len(wholeEvents) != 1 {
		t.Fatalf("the whole-frame control produced %d events at three sightings, want 1. If this is "+
			"zero the whole-frame minimum has been broken by the change and the test above proves "+
			"nothing", len(wholeEvents))
	}
}

// TestACropSourcedMinimumAboveTheWholeFrameOneIsRefused: a "lower bar" that is higher is
// not a lower bar.
//
// A configuration that accepted one would report a source-aware minimum while applying
// a single stricter bar to both, and the crop pass's cost would buy nothing.
func TestACropSourcedMinimumAboveTheWholeFrameOneIsRefused(t *testing.T) {
	cfg := presenceConfig()
	cfg.CutMinFrames = cfg.TrackMinFrames + 1
	_, err := New(Zone{}, cfg)
	if err == nil || !strings.Contains(err.Error(), "no lower bar at all") {
		t.Fatalf("a crop minimum above the whole-frame minimum was accepted: %v", err)
	}
}

// TestTheZeroConfigGetsNoMinimumAtAll: a caller that means "no minimum" must get none
// from the crop path either.
//
// The failure this protects against is specific: defaulting the crop bar to 1 while the
// whole-frame bar is 0 makes the BETTER measurement STRICTER, and a caller that set
// neither is suddenly refused by its own configuration.
//
// The expected count is ONE and not three, and that is the second claim here: with the
// minimum off, the track is allowed on its very first sighting, emits, and the presence
// latch suppresses the rest. One track is one event whether the bar is one or nine. The
// latch does not need a minimum to work.
func TestTheZeroConfigGetsNoMinimumAtAll(t *testing.T) {
	e, err := New(Zone{}, Config{ScoreThreshold: 0.45, Classes: []string{"car"}, IoU: 0.3, MaxAge: time.Second})
	if err != nil {
		t.Fatalf("a Config with no minima was refused: %v", err)
	}
	at := time.Now().UTC()
	b := carAt(0.5, 0.5)
	var got []Crossing
	for i := 0; i < 3; i++ {
		got = append(got, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)...)
	}
	if len(got) != 1 {
		t.Fatalf("with no minimum configured, three sightings of one track produced %d events, want 1. "+
			"The presence latch is per TRACK and does not depend on the track-minimum check, so a run "+
			"with no minimum emits one event per object rather than one per frame", len(got))
	}
	if got[0].Frames != 1 {
		t.Fatalf("the event was emitted on sighting %d, want the first: with no minimum the track is "+
			"allowed immediately, and an event that waited would mean something was still gating it",
			got[0].Frames)
	}
}

// TestTheCooldownSuppressesASecondObjectOfTheSameClass: presence reuses the per-class
// cooldown rather than inventing a second suppression mechanism.
//
// Two birds, the first inside the window and the second after it. Without the cooldown a
// birdfly produces one event per bird, which is the false-positive rate this whole
// project is measured on.
func TestTheCooldownSuppressesASecondObjectOfTheSameClass(t *testing.T) {
	cfg := presenceConfig()
	cfg.Cooldown = 10 * time.Second
	e := presenceEngine(t, cfg)
	at := time.Now().UTC()
	bird := Box{Class: "bird", Score: 0.8, X1: 0.1, Y1: 0.1, X2: 0.2, Y2: 0.2}

	var first []Crossing
	for i := 0; i < 3; i++ {
		first = append(first, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), bird)...)
	}
	if len(first) != 1 {
		t.Fatalf("the first bird produced %d events, want 1", len(first))
	}
	// A second bird at a DIFFERENT place inside the window. A new track, same class, so
	// the cooldown is the only thing that can suppress it.
	other := Box{Class: "bird", Score: 0.8, X1: 0.7, Y1: 0.7, X2: 0.8, Y2: 0.8}
	var second []Crossing
	for i := 0; i < 3; i++ {
		second = append(second, feedWhole(e, at.Add(time.Duration(10+i)*200*time.Millisecond), other)...)
	}
	if len(second) != 0 {
		t.Fatalf("a second bird inside the cooldown produced %d events, want 0. One birdfly is not %d "+
			"security events, and the false-positive rate is the only number that matters at scale",
			len(second), len(second))
	}
	// And after the window it is reported, which is the difference between a cooldown and
	// a permanent suppression.
	late := Box{Class: "bird", Score: 0.8, X1: 0.4, Y1: 0.4, X2: 0.5, Y2: 0.5}
	var third []Crossing
	for i := 0; i < 3; i++ {
		third = append(third, feedWhole(e, at.Add(time.Duration(70+i)*200*time.Millisecond), late)...)
	}
	if len(third) != 1 {
		t.Fatalf("a bird after the cooldown produced %d events, want 1: a cooldown is a window, not a "+
			"permanent suppression", len(third))
	}
}

// TestASuppressedEngineEmitsNothingAndSaysHowMuchItWithheld is the tracker half of
// the view-change rule.
//
// A camera moved is not "nothing happened": the detector has to emit nothing AND the run
// has to be able to say it emitted nothing ON PURPOSE. A gate with no counter is
// silence, and silence in a security pipeline reads as "nothing happened", which is the
// one sentence it must never be allowed to say.
func TestASuppressedEngineEmitsNothingAndSaysHowMuchItWithheld(t *testing.T) {
	e := presenceEngine(t, presenceConfig())
	at := time.Now().UTC()
	b := carAt(0.5, 0.5)

	// One object, tracked and old enough, then gated.
	for i := 0; i < 3; i++ {
		feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)
	}
	e.SetSuppressed(true)
	if !e.Suppressed() {
		t.Fatal("SetSuppressed(true) did not take")
	}
	var got []Crossing
	for i := 3; i < 12; i++ {
		got = append(got, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), b)...)
	}
	if len(got) != 0 {
		t.Fatalf("a suppressed engine emitted %d event(s). A camera that just moved makes every box in "+
			"the new view fall inside some old track's neighbourhood, so the flood comes from exactly "+
			"here", len(got))
	}
	// A track that ALREADY emitted has nothing withheld, and is correctly not counted.
	// The counter is of withheld EVENTS, not of suppressed frames, and this is the
	// assertion that says which: an engine that counted frames would report a number
	// that rises while a scene sits quietly with one latched track in it.
	if n := e.SuppressedEvents(); n != 0 {
		t.Fatalf("the engine counted %d withheld event(s) from a track that had already emitted. There "+
			"was nothing to withhold", n)
	}
	// A track that would have emitted, and did not, IS counted.
	//
	// It is a DIFFERENT CLASS from the latched one on purpose. The class cooldown is
	// still armed from the first object, so a same-class track would be suppressed by
	// the cooldown rather than by the gate, and this test would then be asserting that
	// the gate works while measuring the cooldown.
	bird := Box{Class: "bird", Score: 0.8, X1: 0.2, Y1: 0.2, X2: 0.35, Y2: 0.35}
	var withheld []Crossing
	for i := 0; i < 4; i++ {
		withheld = append(withheld, feedWhole(e, at.Add(time.Duration(20+i)*200*time.Millisecond), bird)...)
	}
	if len(withheld) != 0 {
		t.Fatalf("a second track emitted %d event(s) while suppressed", len(withheld))
	}
	if e.SuppressedEvents() == 0 {
		t.Fatal("the engine withheld nothing it counted. The absence has to be reportable or it is " +
			"indistinguishable from a quiet scene")
	}
	// And it emits AGAIN once cleared, because the object really was there. Latching it
	// while suppressed would lose a real detection permanently.
	e.SetSuppressed(false)
	var after []Crossing
	for i := 24; i < 27; i++ {
		after = append(after, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), bird)...)
	}
	if len(after) != 1 {
		t.Fatalf("after the view settled the engine emitted %d event(s), want 1. The track was latched "+
			"while suppressed, or the latch fired before the gate", len(after))
	}
}

// TestInvalidateAllDropsEveryTrackAndReturnsTheCount is the tracker's half.
//
// After a camera moves, every track describes a position the camera is no longer looking
// at. The association threshold would happily match the new view's boxes to the old
// view's tracks, because a wall at x=0.1 is still at x=0.1 in the new view's noise and IoU
// does not know the camera moved.
func TestInvalidateAllDropsEveryTrackAndReturnsTheCount(t *testing.T) {
	cfg := presenceConfig()
	cfg.Cooldown = 0
	e := presenceEngine(t, cfg)
	at := time.Now().UTC()
	// Three distinct objects, each aged past the minimum. The events they emitted are
	// collected so this test also proves the setup worked: an invalidation test that
	// passes because nothing was ever tracked proves nothing.
	var emitted []Crossing
	// Three objects, all aged past the minimum, all seen on the SAME short run of
	// frames.
	//
	// The frames are 200 ms apart and there is NO GAP before the invalidation. The first
	// red-check of this control let a 19-second gap sit between the last sighting and
	// the re-sighting, and the tracks aged out on their own, so the mutation "keep the
	// map" passed a test that had already removed the tracks by other means. A control
	// that cannot survive the clock is not watching the line it names.
	three := []Box{carAt(0.2, 0.5), carAt(0.45, 0.5), carAt(0.7, 0.5)}
	for f := 0; f < 4; f++ {
		emitted = append(emitted, feedWhole(e, at.Add(time.Duration(f)*200*time.Millisecond), three...)...)
	}
	if len(emitted) != 3 {
		t.Fatalf("the setup emitted %d event(s), want 3. The invalidation assertions below are about "+
			"tracks that existed", len(emitted))
	}
	live := e.LiveTrackCount()
	if live != 3 {
		t.Fatalf("the engine holds %d live track(s) before the invalidation, want 3. The count asserted "+
			"below is read from the engine and compared against this", live)
	}
	if n := e.InvalidateAll(); n != live {
		t.Fatalf("InvalidateAll returned %d and the engine held %d. \"A view change happened\" does not "+
			"tell an operator what their event stream just did; the dropped count does", n, live)
	}
	if e.LiveTrackCount() != 0 {
		t.Fatalf("%d track(s) survived the invalidation", e.LiveTrackCount())
	}
	if e.InvalidatedTracks() != 3 {
		t.Fatalf("the run total says %d tracks were dropped, want 3", e.InvalidatedTracks())
	}
	// And the old tracks cannot be matched: an object at the SAME place starts a fresh
	// track and needs the full minimum again. This is the assertion that fails if the
	// invalidation left the map alone and merely stopped the counting.
	var got []Crossing
	for i := 4; i < 6; i++ {
		got = append(got, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), three[0])...)
	}
	if len(got) != 0 {
		t.Fatalf("an object at a pre-move position emitted %d event(s) on its second sighting. The old "+
			"track survived the invalidation", len(got))
	}
	got = feedWhole(e, at.Add(6*200*time.Millisecond), three[0])
	if len(got) != 1 {
		t.Fatalf("after the minimum the same object emitted %d event(s), want 1: the invalidation starts "+
			"it a fresh track", len(got))
	}
}

// TestPresenceAndCrossingDoNotBothRun: the mode is chosen by the zone and only one
// machine is active.
//
// A zone WITH lines must still produce crossings and not presence events, because a run
// that silently started reporting every tracked object would multiply the event rate by
// however many objects happened to be on screen and nothing in the output would say the
// mode had changed.
func TestPresenceAndCrossingDoNotBothRun(t *testing.T) {
	at := time.Now().UTC()
	z := testZone(t)
	e, err := New(z, presenceConfig())
	if err != nil {
		t.Fatal(err)
	}
	var crossings []Crossing
	for i := 0; i < 8; i++ {
		frac := float64(i) / 7
		cy := 0.1 + frac*0.8
		crossings = append(crossings, feedWhole(e, at.Add(time.Duration(i)*200*time.Millisecond), carAt(0.5, cy))...)
	}
	if len(crossings) == 0 {
		t.Fatal("a zone with a line produced no crossing at all: the presence branch must not have " +
			"replaced the crossing machine, it must be a different machine for a different zone")
	}
	for _, c := range crossings {
		if c.Line == "" {
			t.Fatalf("a zone WITH lines emitted a presence event with no line name: %+v", c)
		}
	}
}
