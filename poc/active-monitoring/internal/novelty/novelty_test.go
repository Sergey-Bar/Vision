package novelty

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// obj is a rectangle painted in FRAME space, which is what a thing moving
// through the scene is.
type obj struct{ x, y, w, h, v int }

// scene renders a synthetic frame with three INDEPENDENT degrees of freedom,
// because the three ways this detector goes wrong need three different fixtures:
//
//   - ox, oy displace the SCENE, which is what a camera that has been moved or
//     knocked does. A displaced scene changes most of the frame and no part of it
//     uniformly.
//   - lift raises or lowers every pixel's luma by the same amount, which is what
//     a change of exposure does. It must not fire anything.
//   - objs are painted in frame space, which is what a vehicle crossing the view
//     is. It changes a small part of the frame.
func scene(w, h, ox, oy, lift int, objs ...obj) []byte {
	out := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u, v := x-ox, y-oy
			// A structured background at the scale the mask works at: blocks of
			// tens of pixels, so a 64-wide luma grid still sees them. The
			// structure is what makes a displaced view measurable at all — a flat
			// wall moved one pixel is a flat wall moved one pixel, and so is a
			// wall of per-pixel noise once it has been box-averaged away.
			// The levels stay inside the 8-bit range at BOTH ends of the lift
			// range this test uses. A fixture that saturates is a fixture where a
			// "uniform brightness change" is not uniform, and it would be testing
			// the clamp rather than the illumination removal.
			l := 40 + 55*float64((u/37+v/29)%3)
			if u > 40 && u < 84 && v > 50 && v < 92 {
				l = 165
			}
			if u > 150 && u < 206 && v > 120 && v < 172 {
				l = 60
			}
			if u > 260 && u < 300 && v > 300 && v < 330 {
				l = 120
			}
			out[(y*w+x)*3] = clamp8(int(l) + lift)
			out[(y*w+x)*3+1] = clamp8(int(l) + lift)
			out[(y*w+x)*3+2] = clamp8(int(l) + lift)
		}
	}
	for _, o := range objs {
		for y := o.y; y < o.y+o.h && y < h; y++ {
			for x := o.x; x < o.x+o.w && x < w; x++ {
				if x < 0 || y < 0 {
					continue
				}
				i := (y*w + x) * 3
				out[i] = clamp8(o.v)
				out[i+1] = clamp8(o.v)
				out[i+2] = clamp8(o.v)
			}
		}
	}
	return out
}

func clamp8(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func newTemporal(t *testing.T, mutate func(Config) Config) *Engine {
	t.Helper()
	cfg := Defaults()
	cfg.Mode = ModeTemporal
	cfg.Class = "motion"
	cfg.PromptID = "alarm"
	if mutate != nil {
		cfg = mutate(cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func newReference(t *testing.T, mutate func(Config) Config) *Engine {
	t.Helper()
	cfg := Defaults()
	cfg.Mode = ModeReference
	cfg.Class = "camera_integrity"
	cfg.PromptID = "camera_integrity"
	cfg.Sustain = 3
	if mutate != nil {
		cfg = mutate(cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

// TestTheEngineIsAScoreAndAReasonAndNeverAVerdict is the shape assertion for the
// whole package. A novelty engine that could say "this is an incident" would be a
// second opinion, and nobody could trace where it came from.
func TestTheEngineIsAScoreAndAReasonAndNeverAVerdict(t *testing.T) {
	e := newTemporal(t, nil)
	a := scene(320, 240, 0, 0, 0)
	b := scene(320, 240, 0, 0, 0, obj{100, 90, 40, 60, 225})
	e.Candidates(a, 320, 240)
	cands, sc := e.Candidates(b, 320, 240)
	if len(cands) == 0 {
		t.Fatalf("a bright rectangle moving into frame produced no candidate: %+v", sc)
	}
	if sc.Value <= 0 {
		t.Fatalf("the score is %g; a change that produced a candidate must have a score above zero", sc.Value)
	}
	if sc.Reason == "" {
		t.Fatal("the score carries no reason")
	}
	if sc.Value > 1 {
		t.Fatalf("the score is %g; it is a FRACTION of the frame", sc.Value)
	}
	for _, c := range cands {
		if c.Reason == "" {
			t.Fatal("a candidate carries no reason")
		}
		if !c.Box.NonEmpty() {
			t.Fatalf("a candidate carries an empty box: %+v", c.Box)
		}
		if c.Cells <= 0 {
			t.Fatalf("a candidate claims %d cells; a region with no cells is not a measurement", c.Cells)
		}
	}
}

// TestTheMeasuredBoxIsMeasured pins the property the plan is most specific
// about: the region on a pushed event is arithmetic on pixels, not something the
// model was asked for. A rectangle pasted at a known place must produce a box
// over that place and not over the whole frame.
func TestTheMeasuredBoxIsMeasured(t *testing.T) {
	e := newTemporal(t, nil)
	a := scene(320, 240, 0, 0, 0)
	// An object occupying the left third, vertically centred.
	b := scene(320, 240, 0, 0, 0, obj{20, 100, 80, 40, 230})
	e.Candidates(a, 320, 240)
	cands, _ := e.Candidates(b, 320, 240)
	if len(cands) == 0 {
		t.Fatal("a rectangle pasted over the left third produced no candidate")
	}
	box := cands[0].Box
	if box.X1 > 0.20 || box.X2 < 0.20 {
		t.Fatalf("the measured box %+v does not cover the left edge where the object was pasted (x1=%.2f x2=%.2f)",
			box, box.X1, box.X2)
	}
	if box.X2 > 0.55 {
		t.Fatalf("the measured box %+v runs to x=%.2f; the object ends near 0.31, so the box is not measuring the object",
			box, box.X2)
	}
	if box.Y1 > 0.55 || box.Y2 < 0.45 {
		t.Fatalf("the measured box %+v is not over the object vertically", box)
	}
}

// TestAReferenceScoreDoesNotRiseOnAMovingObject is the first of the two silent
// failures. A car crossing an intact view is a LARGE temporal change and a SMALL
// global one, and a camera_integrity trigger that fires on it is a false alarm on
// every vehicle that passes.
func TestAReferenceScoreDoesNotRiseOnAMovingObject(t *testing.T) {
	ref := scene(640, 480, 0, 0, 0)
	// The same scene with a car-sized rectangle crossing the bottom.
	car := scene(640, 480, 0, 0, 0, obj{300, 380, 150, 90, 235})
	// The same scene with the camera physically displaced: the whole view shifts.
	knocked := scene(640, 480, 44, 37, 0)

	// Sustain=1 here so the assertion is about the SCORE, not about the streak.
	// The streak is TestSustainedIsWhatFiresAndOneFrameIsNot's whole subject, and
	// a score test that also depends on a frame count fails for two reasons at
	// once.
	engRef := newReference(t, func(c Config) Config { c.Threshold = 0.25; c.Sustain = 1; return c })
	if err := engRef.Pin(ref, 640, 480, "sha", time.Now()); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	var carScore, tamperScore Score
	for i := 0; i < 4; i++ {
		engRef.Watch(time.Now(), car, 640, 480)
		carScore = engRef.Last()
	}
	// The first knocked frame fires; the rest are held by the cooldown, which is
	// the point of the cooldown and the reason the assertion is on the first.
	f, fired := engRef.Watch(time.Now(), knocked, 640, 480)
	tamperScore = engRef.Last()
	if !fired {
		t.Fatalf("a camera displaced by 44x37 pixels did not fire the reference trigger: %+v / %+v", f, tamperScore)
	}
	if !engRef.LastFinding().Box.NonEmpty() {
		t.Fatal("the reference trigger reported no region")
	}
	if engRef.LastFinding().RegionSource != RegionWholeFrame {
		t.Fatalf("the region source is %q; a global change must say whole_frame rather than imply a measurement",
			engRef.LastFinding().RegionSource)
	}
	for i := 0; i < 3; i++ {
		if _, again := engRef.Watch(time.Now(), knocked, 640, 480); again {
			t.Fatalf("the reference trigger fired again on consecutive frame %d; the cooldown did not hold", i+2)
		}
	}
	if carScore.Value >= tamperScore.Value {
		t.Fatalf("a moving object (%g) scored at or above a displaced camera (%g); the reference score is measuring the wrong thing",
			carScore.Value, tamperScore.Value)
	}
	if carScore.Value >= engRef.Config().Threshold {
		t.Fatalf("a moving object scored %g, at or above the %g threshold: every vehicle that passes would raise a tamper event",
			carScore.Value, engRef.Config().Threshold)
	}
}

// TestATemporalScoreDoesNotFireOnALightingChange is the second of the two silent
// failures. Every cell moves when the exposure moves, and nothing happened.
func TestATemporalScoreDoesNotFireOnALightingChange(t *testing.T) {
	for _, lift := range []int{12, 30, -14} {
		e := newTemporal(t, func(c Config) Config { c.Threshold = 0.20; return c })
		dark := scene(320, 240, 0, 0, 0)
		bright := scene(320, 240, 0, 0, lift)
		cands, _ := e.Candidates(dark, 320, 240)
		cands2, sc2 := e.Candidates(bright, 320, 240)
		if len(cands) != 0 || len(cands2) != 0 {
			t.Fatalf("a uniform brightness change of %d produced %d and then %d candidates (%+v, illumination=%g spread=%g); it produced nothing",
				lift, len(cands), len(cands2), sc2, sc2.Illumination, sc2.Spread)
		}
		if sc2.Value >= e.Config().Threshold {
			t.Fatalf("a uniform brightness change of %d scored %g, at or above the %g threshold",
				lift, sc2.Value, e.Config().Threshold)
		}
		if sc2.Spread > lumaEpsilon {
			t.Fatalf("a uniform brightness change of %d left a residual spread of %g; the whole-frame offset was not removed",
				lift, sc2.Spread)
		}
		if sc2.Illumination == 0 {
			t.Fatalf("a brightness change of %d reported no illumination offset at all, so the removal is not being measured", lift)
		}
		if sc2.Reason != ReasonIlluminationOnly {
			t.Fatalf("a brightness change of %d reported reason %q rather than illumination_only", lift, sc2.Reason)
		}
	}
}

// TestSustainedIsWhatFiresAndOneFrameIsNot is the camera_integrity rule: one
// frame is a car, a hundred is a moved camera.
func TestSustainedIsWhatFiresAndOneFrameIsNot(t *testing.T) {
	ref := scene(320, 240, 0, 0, 0)
	moved := scene(320, 240, 36, 30, 0)
	e := newReference(t, func(c Config) Config { c.Sustain = 5; c.Threshold = 0.20; c.Release = 0.10; return c })
	if err := e.Pin(ref, 320, 240, "sha", time.Now()); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	for i := 1; i < 5; i++ {
		if _, fired := e.Watch(time.Now(), moved, 320, 240); fired {
			t.Fatalf("the trigger fired on frame %d of 5; it requires 5 sustained frames", i)
		}
		if got := e.Last().Reason; got != ReasonNotSustained {
			t.Fatalf("on frame %d the reason was %q rather than not_sustained", i, got)
		}
	}
	f, fired := e.Watch(time.Now(), moved, 320, 240)
	if !fired {
		t.Fatalf("the trigger did not fire on the fifth sustained frame: %+v", e.Last())
	}
	if f.Streak != 5 {
		t.Fatalf("the streak reported is %d rather than 5", f.Streak)
	}
}

// TestTheHysteresisBandResetsAndTheCooldownHolds is the same machine the line
// crossing uses, for the same reason: a camera that has been knocked asks the
// same question every frame until somebody walks over.
func TestTheHysteresisBandResetsAndTheCooldownHolds(t *testing.T) {
	ref := scene(320, 240, 0, 0, 0)
	moved := scene(320, 240, 36, 30, 0)
	back := scene(320, 240, 0, 0, 0)
	e := newReference(t, func(c Config) Config {
		c.Sustain = 2
		c.Threshold = 0.20
		c.Release = 0.10
		c.Cooldown = time.Minute
		return c
	})
	if err := e.Pin(ref, 320, 240, "sha", time.Now()); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	// Above threshold, below sustain.
	e.Watch(time.Now(), moved, 320, 240)
	// Below release: the streak resets.
	_, fired := e.Watch(time.Now(), back, 320, 240)
	if fired {
		t.Fatal("a frame back at the reference level fired the trigger")
	}
	if e.Last().Reason != ReasonBelowRelease {
		t.Fatalf("a frame back at the reference level reported %q rather than below_release", e.Last().Reason)
	}
	// Two more sustained frames reach sustain, and then the cooldown holds.
	e.Watch(time.Now(), moved, 320, 240)
	e.Watch(time.Now(), moved, 320, 240)
	e.Watch(time.Now(), moved, 320, 240)
	e.Watch(time.Now(), moved, 320, 240)
	if _, fired := e.Watch(time.Now(), moved, 320, 240); fired {
		t.Fatal("the cooldown did not hold after the first event")
	}
	if e.Last().Reason != ReasonCooldown {
		t.Fatalf("the cooldown held but the reason was %q rather than cooldown", e.Last().Reason)
	}
	// Past the cooldown it fires again.
	_, fired = e.Watch(time.Now().Add(2*time.Minute), moved, 320, 240)
	if !fired {
		t.Fatal("past the cooldown the trigger did not fire again")
	}
}

// TestACorruptFrameIsNotAZero is the property that stops a short buffer from
// becoming "nothing changed", which is a silence the operator reads as a camera
// that is fine.
func TestACorruptFrameIsNotAZero(t *testing.T) {
	e := newTemporal(t, nil)
	if _, sc := e.Candidates([]byte{1, 2, 3}, 320, 240); sc.Reason != ReasonCorrupt {
		t.Fatalf("a 3-byte frame claiming to be 320x240 scored as %q rather than corrupt_frame", sc.Reason)
	}
	if _, sc := e.Candidates(nil, 0, 0); sc.Reason != ReasonCorrupt {
		t.Fatalf("a frame with no dimensions scored as %q rather than corrupt_frame", sc.Reason)
	}
	if _, err := lumaPlane(make([]byte, 10), 320, 240, 64, 48); err == nil {
		t.Fatal("a short buffer was accepted")
	}
}

// TestAPinnedReferenceIsAttributable is why the reference is exportable and
// restorable: a measurement against a reference nobody can be shown is not
// evidence.
func TestAPinnedReferenceIsAttributable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ref.jpg")
	rgb := scene(320, 240, 0, 0, 0)
	if err := WriteRGBJPEG(rgb, 320, 240, src); err != nil {
		t.Fatalf("WriteRGBJPEG: %v", err)
	}
	a := newReference(t, func(c Config) Config { c.Sustain = 1; return c })
	if err := a.Restore(src, time.Now()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !a.Pinned() {
		t.Fatal("a restored reference did not mark the engine pinned")
	}
	sha, _, source := a.Reference()
	if sha == "" {
		t.Fatal("a restored reference carries no content hash, so it cannot be attributed")
	}
	if source != "restored" {
		t.Fatalf("the reference says it came from %q rather than restored", source)
	}
	out := filepath.Join(dir, "out.jpg")
	if err := a.ExportReference(out); err != nil {
		t.Fatalf("ExportReference: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("the exported reference does not exist: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("the exported reference is zero bytes")
	}
	// A restored reference must measure the SAME scene as no change at all.
	if _, fired := a.Watch(time.Now(), rgb, 320, 240); fired || a.Last().Value >= a.Config().Release {
		t.Fatalf("a frame identical to the pinned reference scored %g; the reference is not the reference", a.Last().Value)
	}
}

// TestATemporalTriggerHasNoReference pins the closed set of modes from the other
// side: pinning a temporal trigger is refused rather than silently ignored,
// because a measurement against a view nobody chose is not a measurement.
func TestATemporalTriggerHasNoReference(t *testing.T) {
	e := newTemporal(t, nil)
	if err := e.Pin(scene(32, 32, 0, 0, 0), 32, 32, "sha", time.Now()); err == nil {
		t.Fatal("a temporal trigger accepted a pinned reference")
	}
	if cands, sc := e.Candidates(scene(32, 32, 0, 0, 0), 32, 32); cands != nil || sc.Reason != ReasonFirstFrame {
		t.Fatalf("the first frame of a temporal trigger returned %v / %q; there is nothing to compare it against",
			cands, sc.Reason)
	}
	ref := newReference(t, nil)
	if cands, _ := ref.Candidates(scene(32, 32, 0, 0, 0), 32, 32); cands != nil {
		t.Fatal("a reference trigger returned proposed regions; a global change has no region and inventing one is worse")
	}
	if _, fired := ref.Watch(time.Now(), scene(32, 32, 0, 0, 0), 32, 32); fired {
		t.Fatal("a reference trigger with no pinned view fired")
	}
	if ref.Last().Reason != ReasonUnpinned {
		t.Fatalf("an unpinned reference trigger reported %q rather than unpinned", ref.Last().Reason)
	}
}

// TestConfigRefusesNumbersNobodyCouldInterpret.
func TestConfigRefusesNumbersNobodyCouldInterpret(t *testing.T) {
	// Validate is tested directly rather than through New, because New applies
	// the measured defaults first and a caller who says nothing gets a
	// defensible number. The refusals are about a caller who says something
	// impossible.
	cases := map[string]func(Config) Config{
		"an unknown mode":         func(c Config) Config { c.Mode = "psychic"; return c },
		"a threshold above one":   func(c Config) Config { c.Threshold = 2; return c },
		"a zero threshold":        func(c Config) Config { c.Threshold = 0; return c },
		"a zero noise multiplier": func(c Config) Config { c.NoiseK = -1; return c },
		"a grid of two":           func(c Config) Config { c.GridWidth = 2; return c },
		"no minimum size":         func(c Config) Config { c.MinCells = 0; return c },
		"an upside-down band":     func(c Config) Config { c.Release = 0.9; return c },
		"no candidate budget":     func(c Config) Config { c.MaxCandidates = 0; return c },
	}
	for name, mutate := range cases {
		cfg := Defaults()
		cfg = mutate(cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s was accepted: %+v", name, cfg)
		}
	}
	if err := (Defaults()).Validate(); err != nil {
		t.Fatalf("the measured defaults are not valid: %v", err)
	}
}

// TestDeterminism: the same footage must give the same measurement twice, or
// the report is not reproducible and the replay in the proof chain is a fiction.
func TestDeterminism(t *testing.T) {
	rgb := func() []byte {
		r := scene(320, 240, 0, 0, 0)
		rnd := rand.New(rand.NewSource(7))
		for i := range r {
			r[i] = clamp8(int(r[i]) + rnd.Intn(9) - 4)
		}
		return r
	}
	a, b := newTemporal(t, nil), newTemporal(t, nil)
	for i := 0; i < 6; i++ {
		_, s1 := a.Candidates(rgb(), 320, 240)
		_, s2 := b.Candidates(rgb(), 320, 240)
		if s1 != s2 {
			t.Fatalf("two engines over identical footage disagree: %+v vs %+v", s1, s2)
		}
	}
}

// FuzzTheMaskSurvivesAbsurdDimensions is the fuzz target the plan asks for on
// top of the six that already exist. It asserts only that nothing panics and that
// a frame that is not the size it claims is REFUSED rather than scored.
func FuzzTheMaskSurvivesAbsurdDimensions(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4}, 3, 3, 64, 64)
	f.Add([]byte{}, 0, 0, 0, 0)
	f.Add(make([]byte, 12), 2, 2, 64, 64)
	f.Add(make([]byte, 64*48*3), 64, 48, 1, 1)
	f.Fuzz(func(t *testing.T, rgb []byte, w, h, gw, gh int) {
		g, err := lumaPlane(rgb, w, h, gw, gh)
		if err != nil {
			return
		}
		if g.W <= 0 || g.H <= 0 {
			t.Fatalf("a grid of %dx%d was produced from %d bytes", g.W, g.H, len(rgb))
		}
		if len(g.cells) != g.W*g.H {
			t.Fatalf("the grid holds %d cells and claims %dx%d", len(g.cells), g.W, g.H)
		}
		m, n := scoreMask(g.cells, g.W, g.H, 0.5)
		if n < 0 || n > len(m.cells) {
			t.Fatalf("%d of %d cells changed", n, len(m.cells))
		}
		for _, c := range components(m, 1) {
			if !c.box.NonEmpty() {
				t.Fatalf("a component of %d cells has an empty box %+v", c.cells, c.box)
			}
		}
	})
}
