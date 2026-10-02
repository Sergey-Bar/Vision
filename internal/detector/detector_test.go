package detector_test

import (
	"errors"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/detector"
	"github.com/Sergey-Bar/Ai-Captain/internal/mp4"
)

// The fixture is the same synthetic stream the muxer is tested against: three
// seconds of a flat grey frame, then five seconds of a moving test pattern. It
// is the corpus that makes "fires on motion, quiet on a static scene" a
// measurement rather than an assertion about a hand-made array.
const fixture = "../../testdata/poc/testsrc.h264"

const nominalFPS = 25

// picture is one fixture picture with its size, since the fixture is an
// elementary stream and nothing here decodes it.
type picture struct {
	bytes    int
	keyFrame bool
}

func loadFixture(t *testing.T) []picture {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	pr := mp4.ReadPictures(f, nominalFPS, mp4.DefaultTimescale)
	var out []picture
	for {
		p, err := pr.Next()
		if p == nil {
			break
		}
		out = append(out, picture{bytes: p.Bytes(), keyFrame: p.KeyFrame})
		_ = err
	}
	if len(out) < 100 {
		t.Fatalf("fixture yielded %d pictures", len(out))
	}
	return out
}

func TestCodedSizeStaysQuietOnAStaticSceneAndFiresOnMotion(t *testing.T) {
	pics := loadFixture(t)
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 2, Warmup: 25})

	// The fixture is static for its first three seconds.
	const staticUntil = 75
	firedInStatic, fired := 0, 0
	firstFire := -1

	for i, p := range pics {
		r, err := d.Observe(detector.Observed{CodedBytes: p.bytes, KeyFrame: p.keyFrame, Time: time.Time{}})
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if r.Triggered {
			fired++
			if firstFire < 0 {
				firstFire = i
			}
			if i < staticUntil {
				firedInStatic++
			}
		}
		if r.Score < 0 || r.Score > 1 {
			t.Fatalf("picture %d: score %v is outside [0,1]", i, r.Score)
		}
		if r.Reason == "" {
			t.Fatalf("picture %d: a reading with no reason is a reading nobody can act on", i)
		}
	}
	if firedInStatic > 0 {
		t.Errorf("the detector fired %d times on three seconds of a completely static frame", firedInStatic)
	}
	if fired == 0 {
		t.Error("the detector never fired on five seconds of continuous motion")
	}
	if firstFire >= 0 && firstFire < staticUntil {
		t.Errorf("first trigger at picture %d, inside the static segment", firstFire)
	}
}

// TestCodedSizeDoesNotLetAKeyframeRedefineNormal names a defect this detector
// had, because a test that only shows the current behaviour does not stop the
// reasoning that produced it from being repeated.
//
// A keyframe is large for a reason that has nothing to do with what is in the
// frame. Folding one into the running baseline raised the bar by the time the
// picture after it arrived, which is exactly while the motion was still there,
// so the change that started the sequence was normalised away and the trigger
// stayed quiet through the event it exists to catch. With that fixed, the
// transition picture fires; with it reintroduced, the whole stream is silent.
func TestCodedSizeDoesNotLetAKeyframeRedefineNormal(t *testing.T) {
	pics := loadFixture(t)
	const firstMotion = 75
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 2, Warmup: 25})
	firedAt := -1
	for i, p := range pics {
		r, err := d.Observe(detector.Observed{CodedBytes: p.bytes, KeyFrame: p.keyFrame})
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if r.Triggered && firedAt < 0 {
			firedAt = i
			break
		}
	}
	if firedAt < 0 {
		t.Fatal("the detector never fired; a keyframe has redefined what normal looks like")
	}
	// The change begins at the first motion picture. A trigger later than a
	// couple of pictures past it means the baseline moved with the event.
	if firedAt > firstMotion+2 {
		t.Errorf("first trigger at picture %d, but the change starts at %d", firedAt, firstMotion)
	}
}

// TestCodedSizeReasonMatchesTheScore guards the status line. A reading that
// scores 1.0 and says "below the threshold" is worse than no reason at all: an
// operator reading the console is told the two facts disagree, and learns to
// stop believing one of them.
func TestCodedSizeReasonMatchesTheScore(t *testing.T) {
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 4, Warmup: 10, Window: 3})
	for i := 0; i < 10; i++ {
		if _, err := d.Observe(detector.Observed{CodedBytes: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	// Fill the scoring window on a quiet scene, then push a sustained change and
	// stop at the first reading that is over the threshold but not yet fired.
	// That is the state the status line spends most of its time in, and it is
	// the one where an inconsistent reason shows up.
	var r detector.Reading
	found := false
	for i := 0; i < 12; i++ {
		size := 1000
		if i >= 3 {
			size = 100000
		}
		var err error
		r, err = d.Observe(detector.Observed{CodedBytes: size})
		if err != nil {
			t.Fatal(err)
		}
		if i >= 3 && r.Score >= r.Threshold && !r.Triggered {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("never reached an over-threshold, not-yet-fired reading; last was %q", r.Reason)
	}
	if !strings.Contains(r.Reason, "consecutive pictures needed") {
		t.Errorf("reason %q says neither that the threshold was passed nor why nothing fired", r.Reason)
	}
}

// TestCodedSizeDoesNotDecideOnAPartiallyFilledWindow is the red-check for the
// windowing itself.
//
// A window of one picture is not an average. Deciding on a partial window made
// the detector noisier at the start of a scored run than it would have been
// without a window at all, and the first picture after warmup on a static scene
// is the keyframe, which is large for a reason that has nothing to do with the
// scene. The detector fired on a camera pointed at a wall.
func TestCodedSizeDoesNotDecideOnAPartiallyFilledWindow(t *testing.T) {
	pics := loadFixture(t)
	const firstMotion = 75
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 2, Warmup: 25, Window: 3})
	// The fixture's keyframes land at 0, 25, 50, 75 — and 25 and 50 are
	// keyframes inside a completely static scene.
	for i := 0; i < firstMotion; i++ {
		r, err := d.Observe(detector.Observed{CodedBytes: pics[i].bytes, KeyFrame: pics[i].keyFrame})
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		if r.Triggered {
			t.Fatalf("picture %d fired on a static scene: %s", i, r.Reason)
		}
	}
}

// TestCodedSizeStopsFiringOnSustainedMotion pins the behaviour the package
// comment promises, because it is the one that decides whether a busy site gets
// a quiet trigger or a permanently firing one.
func TestCodedSizeStopsFiringOnSustainedMotion(t *testing.T) {
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 2, Warmup: 25})
	for i := 0; i < 25; i++ {
		if _, err := d.Observe(detector.Observed{CodedBytes: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	var fired int
	for i := 0; i < 400; i++ {
		r, err := d.Observe(detector.Observed{CodedBytes: 8000})
		if err != nil {
			t.Fatal(err)
		}
		if r.Triggered {
			fired++
		}
	}
	if fired == 0 {
		t.Fatal("a scene that jumps to eight times its baseline never fired")
	}
	if fired > 150 {
		t.Errorf("a permanently busy scene fired %d times in 400 pictures: the baseline never absorbed the change", fired)
	}
}

func TestCodedSizeDoesNotFireBeforeItsBaselineExists(t *testing.T) {
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Warmup: 25})
	for i := 0; i < 25; i++ {
		r, err := d.Observe(detector.Observed{CodedBytes: 50000})
		if err != nil {
			t.Fatal(err)
		}
		if r.Triggered {
			t.Fatalf("picture %d: fired before the baseline existed", i)
		}
		if !strings.Contains(r.Reason, "warming up") {
			t.Errorf("picture %d: a warm-up reading says %q, which does not say the detector is not ready", i, r.Reason)
		}
	}
}

func TestCodedSizeRefusesAnEmptyPictureRatherThanDividingByZero(t *testing.T) {
	d := detector.NewCodedSize(detector.CodedSizeOptions{Warmup: 2})
	if _, err := d.Observe(detector.Observed{CodedBytes: 0}); err == nil {
		t.Fatal("a zero-byte picture was accepted; the score would have been a division by zero")
	}
}

func TestCodedSizeResetDropsTheBaseline(t *testing.T) {
	d := detector.NewCodedSize(detector.CodedSizeOptions{Warmup: 5})
	for i := 0; i < 5; i++ {
		if _, err := d.Observe(detector.Observed{CodedBytes: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	if d.Baseline() == 0 {
		t.Fatal("no baseline was built")
	}
	d.Reset()
	if d.Baseline() != 0 {
		t.Error("Reset left the baseline in place; the next camera is measured against this one")
	}
}

// plane builds a uniformly filled luma frame.
//
// The parameter is named value rather than fill because it shadows nothing
// here and fill is the name of the rectangle helper below.
func plane(w, h int, value byte) detector.Luma {
	out := detector.Luma{Width: w, Height: h, Stride: w, Plane: make([]byte, w*h)}
	for i := range out.Plane {
		out.Plane[i] = value
	}
	return out
}

// rect paints a rectangle into a plane.
func rect(l detector.Luma, x0, y0, x1, y1 int, v byte) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			l.Plane[y*l.Stride+x] = v
		}
	}
}

func TestFrameDiffFiresOnAMovingObjectAndNotOnAStillScene(t *testing.T) {
	d := detector.NewFrameDiff(detector.FrameDiffOptions{Threshold: 0.05, Consecutive: 2, Warmup: 2})

	first := plane(160, 120, 20)
	r, err := d.Observe(detector.Observed{Luma: &first})
	if err != nil {
		t.Fatal(err)
	}
	if r.Triggered {
		t.Fatal("the first picture fired; it has nothing to compare against")
	}
	for i := 0; i < 6; i++ {
		if _, err := d.Observe(detector.Observed{Luma: &first}); err != nil {
			t.Fatal(err)
		}
	}
	// A block covering a tenth of the frame slides across it.
	fired := 0
	for step := 0; step < 6; step++ {
		cur := plane(160, 120, 20)
		rect(cur, 10+step*20, 40, 50+step*20, 80, 220)
		r, err := d.Observe(detector.Observed{Luma: &cur})
		if err != nil {
			t.Fatal(err)
		}
		if r.Triggered {
			fired++
		}
	}
	if fired == 0 {
		t.Fatal("a bright block moving across a tenth of the frame never fired")
	}
}

func TestFrameDiffIgnoresSensorNoiseBelowTheFloor(t *testing.T) {
	d := detector.NewFrameDiff(detector.FrameDiffOptions{Threshold: 0.05, NoiseFloor: 8, Consecutive: 2, Warmup: 2})
	base := plane(160, 120, 100)
	for i := 0; i < 4; i++ {
		if _, err := d.Observe(detector.Observed{Luma: &base}); err != nil {
			t.Fatal(err)
		}
	}
	//nolint:gosec // a test wants the same noise on every run, not a secure stream
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 30; i++ {
		noisy := plane(160, 120, 0)
		for j := range noisy.Plane {
			noisy.Plane[j] = byte(100 + rng.Intn(9) - 4)
		}
		r, err := d.Observe(detector.Observed{Luma: &noisy})
		if err != nil {
			t.Fatal(err)
		}
		if r.Triggered {
			t.Fatalf("picture %d: sub-floor sensor noise fired the detector (%s)", i, r.Reason)
		}
	}
}

func TestFrameDiffRestartsItsBaselineOnAResize(t *testing.T) {
	// A resolution change is a different camera as far as the detector is
	// concerned. Comparing across it scores every pixel as changed.
	d := detector.NewFrameDiff(detector.FrameDiffOptions{Threshold: 0.05, Consecutive: 2, Warmup: 2})
	small := plane(64, 48, 30)
	for i := 0; i < 4; i++ {
		if _, err := d.Observe(detector.Observed{Luma: &small}); err != nil {
			t.Fatal(err)
		}
	}
	big := plane(128, 96, 30)
	r, err := d.Observe(detector.Observed{Luma: &big})
	if err != nil {
		t.Fatal(err)
	}
	if r.Triggered || r.Score != 0 {
		t.Errorf("a resize scored %v: two different frame sizes have nothing to compare", r.Score)
	}
	if !strings.Contains(r.Reason, "frame size changed") {
		t.Errorf("reason %q does not name the resize", r.Reason)
	}
}

func TestFrameDiffSaysSoWhenItHasNoDecodedFrame(t *testing.T) {
	// The two detector families need different things from the capture path.
	// Substituting one for the other is how a stream-only deployment ends up
	// reporting frame-differencing scores it never computed.
	d := detector.NewFrameDiff(detector.FrameDiffOptions{})
	if _, err := d.Observe(detector.Observed{CodedBytes: 4000}); !errors.Is(err, detector.ErrNoLuma) {
		t.Fatalf("got %v, want ErrNoLuma", err)
	}
}

func TestFrameDiffSaysSoWhenThePlaneIsTruncated(t *testing.T) {
	d := detector.NewFrameDiff(detector.FrameDiffOptions{})
	broken := detector.Luma{Width: 64, Height: 64, Stride: 64, Plane: make([]byte, 10)}
	if _, err := d.Observe(detector.Observed{Luma: &broken}); err == nil {
		t.Fatal("a plane 10 bytes long was accepted as 64x64")
	}
}

// TestDecimateKeepsALowContrastChangeThatSubsamplingWouldDrop tests the reason
// this package averages rather than subsamples.
//
// The change is a four-pixel band across a 640-wide frame: well under one
// percent of the pixels, and a quarter of a macroblock row. Nearest-neighbour
// subsampling at a stride of eight samples rows 0, 8, 16, ... and never looks at
// rows 170 to 173, so the change is invisible to it. Block averaging folds
// those four rows into the block that contains them and the block's mean moves,
// so it is not.
//
// The noise floor is 1 rather than the default 6 because this is a digital
// stream with no analogue gain, and because a four-row band averaged into an
// eight-row block is half its amplitude by arithmetic - a floor set above half
// the amplitude would be a floor set above what the detector can see by
// construction.
func TestDecimateKeepsALowContrastChangeThatSubsamplingWouldDrop(t *testing.T) {
	const bandTop, bandRows, delta = 170, 4, 4
	full := plane(640, 360, 40)
	band := plane(640, 360, 40)
	rect(band, 0, bandTop, 640, bandTop+bandRows, 40+delta)

	// The control: a subsampler at the same stride samples rows that are not in
	// the band, so every sample it takes is unchanged.
	sampled, err := subsample(band, 8)
	if err != nil {
		t.Fatalf("subsample: %v", err)
	}
	moved := 0
	for i, v := range sampled.Plane {
		if v != full.Plane[(i/sampled.Stride)*full.Stride+i%sampled.Stride] {
			moved++
		}
	}
	if moved != 0 {
		t.Fatalf("a nearest-neighbour subsampler saw %d changed pixels; the claim this test makes is that it cannot", moved)
	}

	d := detector.NewFrameDiff(detector.FrameDiffOptions{
		Threshold: 0.05, NoiseFloor: 1, Consecutive: 1, Warmup: 1, Decimate: 8,
	})
	for i := 0; i < 3; i++ {
		if _, err := d.Observe(detector.Observed{Luma: &full}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := d.Observe(detector.Observed{Luma: &band})
	if err != nil {
		t.Fatal(err)
	}
	if r.Score <= 0 {
		t.Errorf("a %d-row band of +%d luma scored %v after block averaging; the subsampler above scored it zero",
			bandRows, delta, r.Score)
	}
}

// planeFrame is an alias so this test file's helpers read in one vocabulary.
type planeFrame = detector.Luma

// subsample is the alternative this package does not use, kept so the claim
// about it is measured rather than asserted.
func subsample(l planeFrame, factor int) (planeFrame, error) {
	w, h := l.Width/factor, l.Height/factor
	if w < 1 || h < 1 {
		return planeFrame{}, errors.New("cannot subsample by that factor")
	}
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out[y*w+x] = l.Plane[y*factor*l.Stride+x*factor]
		}
	}
	return planeFrame{Width: w, Height: h, Stride: w, Plane: out}, nil
}

// TestDecimateSaturatesABrightAverageRatherThanWrappingIt is a red-check.
//
// A block mean that exceeds 255 mid-arithmetic used to be narrowed straight to a
// byte, which wraps. A white block on a black frame then reads as a dark patch
// on a bright one, the detector sees novelty on every single frame of a static
// scene, and the false-positive rate goes to 1.0 on exactly the cameras - night,
// IR - where it is hardest to see.
func TestDecimateSaturatesABrightAverageRatherThanWrappingIt(t *testing.T) {
	// A half-and-half split: the mean is ~128 and must survive a large factor
	// intact. A factor of 16 makes the numerator 16x16 white pixels of 255,
	// which is well past what a naive byte() narrowing of the sum would keep.
	half := plane(64, 64, 0)
	rect(half, 32, 0, 64, 64, 255)

	small, err := detector.Decimate(half, 16)
	if err != nil {
		t.Fatalf("decimate: %v", err)
	}
	if small.Width != 4 || small.Height != 4 {
		t.Fatalf("a 64x64 plane decimated by 16 came out %dx%d", small.Width, small.Height)
	}
	for y := 0; y < small.Height; y++ {
		for x := 0; x < small.Width; x++ {
			// The split is at x=32, which is exactly the boundary between the
			// two 32-pixel halves, so every decimated block is wholly in one.
			want := byte(0)
			if x >= 2 {
				want = 255
			}
			if got := small.Sample(x, y); got != want {
				t.Fatalf("block (%d,%d) averaged to %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestDecimateRefusesAnImpossibleReduction(t *testing.T) {
	l := plane(8, 8, 0)
	if _, err := detector.Decimate(l, 64); err == nil {
		t.Fatal("an 8x8 plane was decimated by 64")
	}
	if _, err := detector.Decimate(l, 0); err == nil {
		t.Fatal("a decimation factor of zero was accepted")
	}
}

// TestReasonsNameAMeasurementAndNotAConclusion pins the property the package
// exists for. A reason is countable only if it is drawn from a fixed shape, and
// a reason that names a *thing* is a verdict wearing a sentence.
func TestReasonsNameAMeasurementAndNotAConclusion(t *testing.T) {
	verdicts := []string{
		"person", "intruder", "vehicle", "theft", "trespass", "weapon", "fire",
		"loiter", "abandoned", "confirmed", "threat", "suspicious", "alert",
	}
	pics := loadFixture(t)
	d := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: 0.5, Consecutive: 2, Warmup: 25})
	for i, p := range pics {
		r, err := d.Observe(detector.Observed{CodedBytes: p.bytes, KeyFrame: p.keyFrame})
		if err != nil {
			t.Fatalf("picture %d: %v", i, err)
		}
		lowered := strings.ToLower(r.Reason)
		for _, v := range verdicts {
			if strings.Contains(lowered, v) {
				t.Errorf("picture %d: the reason %q names a conclusion (%q); this detector does not decide things", i, r.Reason, v)
			}
		}
	}
}
