package novelty

import (
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"os"
	"sync"
	"time"
)

// Config is one trigger's measurement settings.
//
// Every field has a default, and the defaults are the measured ones rather than
// round numbers: a threshold tuned on footage the team controls is not a
// threshold that transfers, and the plan's own rule is that the threshold is the
// number a judge will challenge, so it has to be stateable and it has to be
// stored with the decision.
type Config struct {
	// Mode is which comparison this trigger makes.
	Mode Mode
	// Class is the detector's own name for what it found. It is the model's
	// class that is authoritative about the INCIDENT; this is what the arithmetic
	// calls it.
	Class string
	// PromptID is the incident class asked about. It is the only thing that
	// distinguishes one incident class from another.
	PromptID string

	// Threshold is the changed fraction at or above which a cell contributes.
	// It is applied to the normalised residual, not to raw luma, so it is
	// independent of exposure.
	Threshold float64
	// NoiseK is how many robust deviations count as "changed".
	NoiseK float64
	// FloorLuma is the absolute noise floor, in luma units, below which no cell
	// counts however clean the two frames look. Without it two identical frames
	// with one cell of rounding between them have a scale of zero and any
	// residual is infinitely significant.
	FloorLuma float64

	// GridWidth is the width of the luma grid the mask is computed on.
	GridWidth int
	// MinCells is the smallest component that counts as a region. Below it, the
	// changed cells are compression speckle.
	MinCells int

	// Sustain is how many consecutive frames the score must be at or above the
	// threshold before a reference trigger fires. One frame is a car; a hundred
	// is a moved camera.
	Sustain int
	// Release is the score below which the sustain streak resets. It is a
	// hysteresis band: firing on one frame and resetting on the next is a
	// detector that stutters on a scene with noise in it.
	Release float64
	// Cooldown is how long after an event this trigger stays quiet. It is the
	// same machine detect.Engine uses for a line crossing, and for the same
	// reason: a camera that has been knocked asks the same question every frame
	// until somebody walks over and fixes it.
	Cooldown time.Duration

	// MaxCandidates is how many proposed regions one frame may contribute. It
	// bounds the work per frame and the number of boxes on an operator's screen.
	MaxCandidates int
}

// Defaults are the measured values. They are named so a config that says nothing
// says something defensible rather than zero.
func Defaults() Config {
	return Config{
		Mode:          ModeTemporal,
		Threshold:     0.35,
		NoiseK:        6,
		FloorLuma:     10.0,
		GridWidth:     64,
		MinCells:      6,
		Sustain:       90,
		Release:       0.20,
		Cooldown:      10 * time.Minute,
		MaxCandidates: 8,
	}
}

// Validate refuses a configuration that would produce a number nobody can
// interpret.
func (c Config) Validate() error {
	if !ValidMode(c.Mode) {
		return fmt.Errorf("novelty: mode %q is not one of temporal, reference; a trigger with no stated reference is a word and not a measurement", c.Mode)
	}
	if c.Threshold <= 0 || c.Threshold > 1 {
		return fmt.Errorf("novelty: the threshold is %g; it is a changed FRACTION and must be in (0,1]", c.Threshold)
	}
	if c.NoiseK <= 0 {
		return fmt.Errorf("novelty: the noise multiplier is %g; a score with no scale is a raw difference", c.NoiseK)
	}
	if c.GridWidth < 4 {
		return fmt.Errorf("novelty: a grid width of %d is too small to locate anything", c.GridWidth)
	}
	if c.MinCells < 1 {
		return fmt.Errorf("novelty: the minimum component size is %d cells; every change would be a region", c.MinCells)
	}
	if c.Release > c.Threshold {
		return fmt.Errorf("novelty: the release level is %g and the threshold is %g; a release above the trigger is an upside-down hysteresis band and the trigger can never reset",
			c.Release, c.Threshold)
	}
	if c.MaxCandidates < 1 {
		return fmt.Errorf("novelty: at most %d candidates may be contributed per frame", c.MaxCandidates)
	}
	return nil
}

// Engine is one trigger's measurement state: the previous frame, and for a
// reference trigger the pinned view.
//
// It is safe for concurrent use, because the pipeline calls it from the frame
// loop and the screen reads its state to burn the score into the picture.
type Engine struct {
	cfg Config

	mu       sync.Mutex
	prev     *grid
	ref      *grid
	refMeta  referenceMeta
	streak   int
	lastFire time.Time
	last     Score
	lastFind Finding
	fires    int
}

// referenceMeta is what is known about the pinned view.
type referenceMeta struct {
	// SHA256 is the hash of the stored frame the reference was built from. It is
	// what makes the reference attributable: a reviewer can be handed the exact
	// JPEG the measurement was made against.
	SHA256 string
	// PinnedAt is when it was pinned.
	PinnedAt time.Time
	// Source is where the pinned view came from: `first_frame` or `restored`.
	Source string
}

// New builds an engine.
//
// It refuses a configuration that would produce a score nobody could interpret,
// and it does that here rather than at the first frame: a misconfiguration
// discovered at second ninety is a run that has already produced ninety seconds
// of numbers.
func New(cfg Config) (*Engine, error) {
	d := Defaults()
	if cfg.Mode == "" {
		cfg.Mode = d.Mode
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = d.Threshold
	}
	if cfg.NoiseK == 0 {
		cfg.NoiseK = d.NoiseK
	}
	if cfg.FloorLuma == 0 {
		cfg.FloorLuma = d.FloorLuma
	}
	if cfg.GridWidth == 0 {
		cfg.GridWidth = d.GridWidth
	}
	if cfg.MinCells == 0 {
		cfg.MinCells = d.MinCells
	}
	if cfg.MaxCandidates == 0 {
		cfg.MaxCandidates = d.MaxCandidates
	}
	if cfg.Cooldown == 0 {
		cfg.Cooldown = d.Cooldown
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Mode == ModeReference && cfg.Sustain <= 0 {
		cfg.Sustain = 1
	}
	return &Engine{cfg: cfg}, nil
}

// Config returns the settings, so a report can print the numbers a decision was
// made with.
func (e *Engine) Config() Config { return e.cfg }

// Mode is which comparison this engine makes.
func (e *Engine) Mode() Mode { return e.cfg.Mode }

// Class is the detector's own name for what it found.
func (e *Engine) Class() string { return e.cfg.Class }

// PromptID is the incident class asked about.
func (e *Engine) PromptID() string { return e.cfg.PromptID }

// Last is the most recent score, for the screen.
func (e *Engine) Last() Score {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last
}

// LastFinding is the most recent trigger event, for the screen.
func (e *Engine) LastFinding() Finding {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastFind
}

// Fires is how many events this engine has raised.
func (e *Engine) Fires() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fires
}

// Pinned reports whether a reference view is pinned.
func (e *Engine) Pinned() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ref != nil
}

// Reference describes the pinned view, for the report.
func (e *Engine) Reference() (sha string, at time.Time, source string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.refMeta.SHA256, e.refMeta.PinnedAt, e.refMeta.Source
}

// Pin sets the reference view from a frame.
//
// It is the caller's job to STORE the frame first and then pin it from the same
// bytes, because a reference nobody can be shown is a reference that cannot be
// checked. The sha is the stored frame's content hash, and it is recorded.
func (e *Engine) Pin(rgb []byte, w, h int, sha string, at time.Time) error {
	if e.cfg.Mode != ModeReference {
		return errors.New("novelty: a temporal trigger has no reference view to pin; its reference IS the previous frame")
	}
	g, err := e.plane(rgb, w, h)
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ref = &g
	e.refMeta = referenceMeta{SHA256: sha, PinnedAt: at, Source: "first_frame"}
	return nil
}

// Restore pins a reference view read back from disk, which is how a run after
// the first one measures against the same view.
func (e *Engine) Restore(path string, at time.Time) error {
	if e.cfg.Mode != ModeReference {
		return errors.New("novelty: a temporal trigger has no reference view to restore")
	}
	rgb, w, h, err := decodeJPEG(path)
	if err != nil {
		return err
	}
	g, err := e.plane(rgb, w, h)
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ref = &g
	e.refMeta = referenceMeta{SHA256: fileSHA(path), PinnedAt: at, Source: "restored"}
	return nil
}

// plane downsamples to this engine's grid, with the height derived from the
// frame's aspect ratio so a cell stays roughly square. A non-square cell would
// make a region the wrong shape before anything is even measured.
func (e *Engine) plane(rgb []byte, w, h int) (grid, error) {
	gw := e.cfg.GridWidth
	gh := gw * h / max(1, w)
	if gh < 4 {
		gh = 4
	}
	return lumaPlane(rgb, w, h, gw, gh)
}

// Candidates measures this frame against the previous one and returns proposed
// regions. Temporal triggers only.
//
// A reference trigger returns nothing here and uses Watch instead: the whole
// frame changed, there is no region, and returning a region would be inventing
// one.
func (e *Engine) Candidates(rgb []byte, w, h int) ([]Candidate, Score) {
	if e.cfg.Mode != ModeTemporal {
		return nil, Score{Mode: e.cfg.Mode, Reason: ReasonNoPrevious}
	}
	cur, err := e.plane(rgb, w, h)
	if err != nil {
		return nil, Score{Mode: e.cfg.Mode, Reason: ReasonCorrupt}
	}
	prev := e.advance(&cur)
	if prev == nil {
		e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonFirstFrame}, Finding{})
		return nil, Score{Mode: e.cfg.Mode, Reason: ReasonFirstFrame}
	}
	if prev.W != cur.W || prev.H != cur.H {
		// A frame whose dimensions changed makes the previous frame
		// incomparable. Dropping the old one is the honest handling; rescaling
		// would be a measurement of a picture nobody has.
		e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonCorrupt}, Finding{})
		return nil, Score{Mode: e.cfg.Mode, Reason: ReasonCorrupt}
	}
	sc, m := e.measure(*prev, cur)
	if sc.Cells == 0 {
		e.publish(sc, Finding{})
		return nil, sc
	}
	var out []Candidate
	for _, c := range components(m, e.cfg.MinCells) {
		if len(out) >= e.cfg.MaxCandidates {
			break
		}
		out = append(out, Candidate{Box: c.box, Score: sc.Value, Cells: c.cells, Reason: ReasonChanged})
	}
	if len(out) == 0 {
		sc.Reason = ReasonNoComponent
	}
	e.publish(sc, Finding{})
	return out, sc
}

// Watch measures this frame against the pinned reference and reports whether the
// view has STAYED different.
//
// Sustained is the whole of the `camera_integrity` class. One frame that differs
// from the reference is a car, a shadow or a compression artefact; a camera that
// has been moved differs from every frame it will ever see again.
func (e *Engine) Watch(at time.Time, rgb []byte, w, h int) (Finding, bool) {
	cur, err := e.plane(rgb, w, h)
	if err != nil {
		e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonCorrupt}, Finding{})
		return Finding{}, false
	}
	prev := e.advance(&cur)
	e.mu.Lock()
	ref := e.ref
	e.mu.Unlock()

	var base *grid
	switch {
	case e.cfg.Mode == ModeReference:
		if ref == nil {
			e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonUnpinned}, Finding{})
			return Finding{}, false
		}
		base = ref
	default:
		if prev == nil {
			e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonFirstFrame}, Finding{})
			return Finding{}, false
		}
		base = prev
	}
	if base.W != cur.W || base.H != cur.H {
		e.publish(Score{Mode: e.cfg.Mode, Reason: ReasonCorrupt}, Finding{})
		return Finding{}, false
	}

	sc, _ := e.measure(*base, cur)
	now := at
	if now.IsZero() {
		now = time.Now()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if sc.Value < e.cfg.Release {
		e.streak = 0
		sc.Reason = ReasonBelowRelease
		e.last = sc
		e.lastFind = Finding{}
		return Finding{}, false
	}
	if sc.Value < e.cfg.Threshold {
		e.streak = 0
		sc.Reason = ReasonBelowThreshold
		e.last = sc
		e.lastFind = Finding{}
		return Finding{}, false
	}
	e.streak++
	sc.Reason = ReasonNotSustained
	if e.streak < e.cfg.Sustain {
		e.last = sc
		e.lastFind = Finding{}
		return Finding{}, false
	}
	if !e.lastFire.IsZero() && now.Sub(e.lastFire) < e.cfg.Cooldown {
		sc.Reason = ReasonCooldown
		e.last = sc
		e.lastFind = Finding{}
		return Finding{}, false
	}
	e.lastFire = now
	e.fires++
	sc.Reason = ReasonChanged
	f := Finding{
		Box:    WholeFrame(),
		Score:  sc.Value,
		Streak: e.streak,
		// A global change has no region, so the box is the whole frame and it
		// says so. The distinction between "measured and it happens to cover
		// everything" and "covers everything because everything changed" is
		// reported, not inferred.
		RegionSource: RegionWholeFrame,
		Reason:       ReasonChanged,
	}
	e.last = sc
	e.lastFind = f
	return f, true
}

// advance stores this frame as the previous one and returns what was there.
//
// One function because the read and the write must not be separated: reading
// `prev`, then storing, then reading `prev` again is how a temporal trigger ends
// up comparing a frame with itself and reporting zero forever.
func (e *Engine) advance(cur *grid) *grid {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.prev
	e.prev = cur
	return prev
}

// measure is the shared arithmetic for both modes: diff, remove the illumination
// offset, threshold, and report the fraction of the frame that changed.
func (e *Engine) measure(base, cur grid) (Score, mask) {
	d := diff(cur, base)
	residual, illum, spread := robust(d, e.cfg.NoiseK, e.cfg.FloorLuma)
	m, n := scoreMask(residual, cur.W, cur.H, e.cfg.Threshold)
	sc := Score{
		Mode:         e.cfg.Mode,
		Cells:        n,
		Illumination: illum,
		Spread:       spread,
		Reason:       ReasonChanged,
	}
	sc.Value = float64(n) / float64(len(residual))
	if n == 0 {
		sc.Reason = ReasonBelowThreshold
		if spread <= lumaEpsilon && math.Abs(illum) > lumaEpsilon {
			// Every cell moved by the same amount: a lighting change, and the
			// most common way a novelty detector cries wolf on a real camera.
			sc.Reason = ReasonIlluminationOnly
		}
	}
	return sc, m
}

func (e *Engine) publish(sc Score, f Finding) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.last = sc
	if f.Box.NonEmpty() {
		e.lastFind = f
	}
}

// decodeJPEG reads a stored frame back into RGB for pinning.
func decodeJPEG(path string) ([]byte, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("novelty: cannot read the reference view at %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("novelty: the reference view at %s is not a readable JPEG: %w", path, err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("novelty: the reference view at %s has no pixels", path)
	}
	out := make([]byte, w*h*3)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, a := img.At(x, y).RGBA()
			_ = a
			out[i] = byte(r >> 8)
			out[i+1] = byte(g >> 8)
			out[i+2] = byte(bb >> 8)
			i += 3
		}
	}
	return out, w, h, nil
}

// ExportReference writes the pinned view out as a JPEG, so a reference is
// something a person can be shown.
//
// It is the write half of Pin: the caller pins first, then exports, and the
// exported bytes are what the evidence store keeps. The two being different
// pictures would be exactly the defect W2-5 exists to stop.
func (e *Engine) ExportReference(path string) error {
	e.mu.Lock()
	ref := e.ref
	e.mu.Unlock()
	if ref == nil {
		return errors.New("novelty: there is no pinned reference view to export")
	}
	return writeLumaJPEG(ref, path)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
