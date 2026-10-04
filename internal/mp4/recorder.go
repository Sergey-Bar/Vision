package mp4

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrNoClip is returned by Finish when the recorder was never armed, or was
// armed and never saw a keyframe to start on.
var ErrNoClip = errors.New("mp4: no clip was produced")

// gop is one complete group of pictures: an IDR and everything that depends on
// it, up to but not including the next IDR.
//
// The unit is the GOP and not the picture because that is the granularity at
// which a clip can be cut without losing its beginning. A clip that starts on a
// non-IDR picture is a clip whose first frames decode to whatever the reference
// buffer happens to hold, which is noise, and it looks fine until somebody plays
// it.
type gop struct {
	pics  []*Picture
	bytes int64
	start uint64
	end   uint64
}

// PreRoll is a per-camera ring of complete GOPs.
//
// It is per-camera state on purpose. The ring and the muxer both hold mutable
// state keyed by stream, so a process running twenty cameras holds twenty of
// each rather than one of each in total - a single shared ring is a data race
// that only shows up when two cameras' timing lines up.
type PreRoll struct {
	maxTime  time.Duration
	maxBytes int64
	ts       uint32

	gops []*gop
	cur  *gop

	// EvictedGops and EvictedBytes are the lead-in this ring could not keep.
	// They are reported, because "the clip has no lead-in" and "the clip had
	// lead-in and we dropped it" are different operational facts.
	EvictedGops  int
	EvictedBytes int64
}

// NewPreRoll returns a ring bounded by both a time window and a byte budget.
// Either may be zero for unbounded, though an unbounded ring on a 24/7 stream
// is a process that grows until something kills it.
func NewPreRoll(maxTime time.Duration, maxBytes int64, timescale uint32) *PreRoll {
	if timescale == 0 {
		timescale = DefaultTimescale
	}
	return &PreRoll{maxTime: maxTime, maxBytes: maxBytes, ts: timescale}
}

// Push adds one picture, retiring the group in progress when the picture starts
// a new one.
func (r *PreRoll) Push(p *Picture) {
	if p == nil {
		return
	}
	if r.cur == nil || p.KeyFrame {
		if r.cur != nil {
			r.retire(r.cur)
		}
		r.cur = &gop{start: p.DecodeTime}
	}
	r.cur.pics = append(r.cur.pics, p)
	r.cur.bytes += int64(p.Bytes())
	r.cur.end = p.DecodeTime
	r.evict()
}

func (r *PreRoll) retire(g *gop) {
	if len(g.pics) == 0 {
		return
	}
	r.gops = append(r.gops, g)
}

// evict drops the oldest group while the ring is over budget. The newest group
// is never dropped: a ring with nothing in it cannot answer a trigger, and the
// honest response to a single group larger than the budget is to keep it and let
// the clip's own cap report the truncation.
func (r *PreRoll) evict() {
	total := int64(0)
	var newest uint64
	for _, g := range r.gops {
		total += g.bytes
		newest = g.end
	}
	if r.cur != nil {
		total += r.cur.bytes
		newest = r.cur.end
	}
	for len(r.gops) > 1 {
		old := r.gops[0]
		overTime := r.maxTime > 0 && r.scaled(newest-old.start) > r.maxTime
		overBytes := r.maxBytes > 0 && total > r.maxBytes
		if !overTime && !overBytes {
			break
		}
		r.gops = r.gops[1:]
		total -= old.bytes
		r.EvictedGops++
		r.EvictedBytes += old.bytes
	}
}

// scaled converts a media-timestamp delta to wall time through the stream's own
// clock rate. A fixed 90 kHz conversion would be right for RTP and wrong for
// every other timescale, including a replayed fixture at 25 fps.
//
// The multiplication is done in halves rather than as one product, because a
// delta of half the uint64 range multiplied by a billion overflows silently and
// returns a *negative* duration - which then compares as "before the start" and
// makes a pre-roll ring evict everything. That is not hypothetical: at 90 kHz a
// 32-bit RTP timestamp wraps after 13 hours, and a camera left running over a
// long weekend reaches it.
func (r *PreRoll) scaled(d uint64) time.Duration {
	if r.ts == 0 || d == 0 {
		return 0
	}
	whole := d / uint64(r.ts)
	rem := d % uint64(r.ts)
	nanos := whole*uint64(time.Second) + rem*uint64(time.Second)/uint64(r.ts)
	if nanos > uint64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(nanos)
}

// Len is the number of pictures currently retained, including the group in
// progress.
func (r *PreRoll) Len() int {
	n := 0
	for _, g := range r.gops {
		n += len(g.pics)
	}
	if r.cur != nil {
		n += len(r.cur.pics)
	}
	return n
}

// Groups is the number of complete-or-in-progress groups retained, which is the
// number that says whether a clip can start on a keyframe at all.
func (r *PreRoll) Groups() int {
	if r.cur != nil {
		return len(r.gops) + 1
	}
	return len(r.gops)
}

// Deepest is the wall span currently retained: the lead-in a trigger fired now
// would get. It is what the console shows next to a camera.
func (r *PreRoll) Deepest() time.Duration {
	var first, last uint64
	if len(r.gops) > 0 {
		first, last = r.gops[0].start, r.gops[0].end
	}
	if r.cur != nil {
		if first == 0 {
			first = r.cur.start
		}
		last = r.cur.end
	}
	if last <= first {
		return 0
	}
	return r.scaled(last - first)
}

// Drain returns every retained picture oldest-first and empties the ring.
func (r *PreRoll) Drain() []*Picture {
	var out []*Picture
	for _, g := range r.gops {
		out = append(out, g.pics...)
	}
	if r.cur != nil {
		out = append(out, r.cur.pics...)
	}
	r.gops = nil
	r.cur = nil
	return out
}

// Defaults. The 30 s / 100 MB pair is the stated limit on an uploaded clip, not
// a taste decision.
const (
	DefaultPreRoll   = 5 * time.Second
	DefaultPostRoll  = 5 * time.Second
	DefaultMaxTime   = 30 * time.Second
	DefaultMaxBytes  = 100 << 20
	DefaultTimescale = 90000
)

type recorderState int

const (
	stateBuffering recorderState = iota
	stateArmed
	stateRecording
	stateExhausted
)

// Options configures a Recorder.
type Options struct {
	// PreRoll is how much lead-in the ring keeps. PreRoll and PostRoll are spent
	// out of MaxTime, so a pair larger than the cap is reduced rather than
	// refused, and the reduction is reported on the Clip.
	PreRoll  time.Duration
	PostRoll time.Duration
	MaxTime  time.Duration
	MaxBytes int64
	// Timescale is the stream's own clock rate. The RTP clock (90 kHz) is the
	// usual answer; a replayed fixture answers with its own.
	Timescale uint32
	// OnDamaged is called for every NAL that could not be parsed well enough to
	// find a picture boundary. A degradation printed and dropped is a
	// degradation an operator cannot see.
	OnDamaged func(string)

	budgetClamped bool
}

func (o Options) defaulted() Options {
	out := o
	if out.Timescale == 0 {
		out.Timescale = DefaultTimescale
	}
	if out.MaxTime <= 0 {
		out.MaxTime = DefaultMaxTime
	}
	if out.MaxBytes <= 0 {
		out.MaxBytes = DefaultMaxBytes
	}
	if out.PostRoll <= 0 {
		out.PostRoll = DefaultPostRoll
	}
	if out.PreRoll < 0 {
		out.PreRoll = 0
	}
	if out.PreRoll+out.PostRoll > out.MaxTime {
		out.PreRoll = out.MaxTime - out.PostRoll
		if out.PreRoll < 0 {
			out.PreRoll = 0
			out.PostRoll = out.MaxTime
		}
		out.budgetClamped = true
	}
	return out
}

// Recorder turns a live NAL stream into a bounded clip.
//
// The ordering rule that makes the pre-roll worth having: a trigger does not
// begin recording. It arms the recorder, and recording begins at the next IDR -
// by which point the ring holds every complete group since the last eviction,
// including the group the trigger fired inside. That is the whole difference
// between a clip that contains the cause of an event and a clip that starts
// after it, and it is why an armed-but-not-started recorder is a state and not
// a delay.
type Recorder struct {
	opts    Options
	state   recorderState
	builder PictureBuilder
	pre     *PreRoll

	// closed means "stop appending samples": a cap was reached, or the post-roll
	// expired. It is separate from state because a capped recorder is still a
	// recorder that can be finished.
	closed bool

	params ParameterSets
	// sp is what params.SPS says, parsed once when the SPS was stored. The
	// banner polls Geometry on a status line and the clip reports the same four
	// numbers at Finish, so parsing the parameter set on each of those calls is
	// work whose answer cannot change: an SPS is replaced or refused, never
	// edited. spOK is false when the bytes did not parse, and the refusal then
	// happens where it always did - when a clip is begun.
	sp   SequenceParameters
	spOK bool
	frag *fragmenter

	triggerDecode uint64
	triggerReason string
	preRollReason string

	hasStart    bool
	startDecode uint64
	held        *Picture
	lastDelta   uint32
	pictures    int
	keyframes   int
	preRollPics int
	preRollSpan time.Duration
	wallStart   time.Time
	wallEnd     time.Time

	cappedTime  bool
	cappedBytes bool
	capReason   string
	stopReason  string
	damaged     string
}

// NewRecorder returns a recorder with the given options defaulted.
func NewRecorder(opts Options) *Recorder {
	o := opts.defaulted()
	// The ring is budgeted at half the clip so that a full ring plus a full
	// clip is a bounded amount of memory rather than twice the cap.
	r := &Recorder{
		opts:  o,
		state: stateBuffering,
		pre:   NewPreRoll(o.PreRoll, o.MaxBytes/2, o.Timescale),
	}
	r.builder.OnDamaged = func(reason string) {
		if r.damaged == "" {
			r.damaged = reason
		}
		if o.OnDamaged != nil {
			o.OnDamaged(reason)
		}
	}
	return r
}

// PreRollDepth reports the lead-in a trigger would currently get, which is what
// the console shows next to a camera.
func (r *Recorder) PreRollDepth() time.Duration { return r.pre.Deepest() }

// PreRollGroups reports how many groups the ring holds, which is what says
// whether a clip could start on a keyframe at all.
func (r *Recorder) PreRollGroups() int { return r.pre.Groups() }

// Units converts a duration to this recorder's media-timestamp units. It is the
// inverse of PreRoll.scaled and is bounded the same way: the multiplication is
// split so a long duration at a high clock rate saturates instead of wrapping
// into a small number, which would be a post-roll of a few milliseconds.
func (r *Recorder) Units(d time.Duration) uint64 {
	if d <= 0 || r.opts.Timescale == 0 {
		return 0
	}
	whole := uint64(d) / uint64(time.Second)
	if whole > math.MaxUint64/uint64(r.opts.Timescale) {
		return math.MaxUint64
	}
	return whole*uint64(r.opts.Timescale) + uint64(d%time.Second)*uint64(r.opts.Timescale)/uint64(time.Second)
}

// PushNAL adds one NAL unit, without its start code.
//
// A parameter-set change mid-stream is refused rather than continued, because a
// file carrying two incompatible answers to "what resolution is this" is
// answered by neither of them.
func (r *Recorder) PushNAL(nal []byte, decodeTime uint64, wall time.Time) error {
	if typ, ok := Parse(nal); ok {
		switch typ {
		case NALPrefix:
			// Three cases, and the first is the common one: a camera re-sends the
			// same parameter set before many keyframes, and a re-send that is
			// copied again is an allocation per keyframe for no new information.
			switch {
			case bytes.Equal(r.params.SPS, nal):
				// The same parameter set again, which is what a camera sends
				// before every keyframe. Nothing to store, and the geometry
				// already parsed from it still stands.
			case len(r.params.SPS) > 0:
				return fmt.Errorf("%w: SPS changed at media time %d", ErrProfileChange, decodeTime)
			default:
				r.params.SPS = append([]byte(nil), nal...)
				if sp, err := ParseSequenceParameters(nal); err == nil {
					r.sp, r.spOK = sp, true
				}
			}
		case NALSuffix:
			switch {
			case bytes.Equal(r.params.PPS, nal):
				// A re-send, as above.
			case len(r.params.PPS) > 0:
				return fmt.Errorf("%w: PPS changed at media time %d", ErrProfileChange, decodeTime)
			default:
				r.params.PPS = append([]byte(nil), nal...)
			}
		}
	}
	if r.state == stateExhausted || r.closed {
		return nil
	}
	if done := r.builder.Push(nal, decodeTime, wall); done != nil {
		return r.pushPicture(done)
	}
	return nil
}

// Trigger arms the recorder. The reason travels onto the Clip, so the operator
// can see what caused the query and not merely that one happened.
//
// It takes a media timestamp and not a wall clock: the muxer only ever compares
// timestamps, and a wall clock here would be a second timeline that could disagree
// with the first about which picture the trigger belongs to.
func (r *Recorder) Trigger(reason string, decodeTime uint64) {
	if r.state != stateBuffering {
		return
	}
	r.state = stateArmed
	r.triggerReason = reason
	r.triggerDecode = decodeTime
}

// Triggered reports whether a trigger has been accepted, so a caller fires
// once per run rather than re-arming a recorder that is already recording.
func (r *Recorder) Triggered() bool {
	return r.state == stateArmed || r.state == stateRecording
}

// Closed reports whether the recorder has stopped accepting samples because a
// cap bound it or the post-roll window closed.
func (r *Recorder) Closed() bool { return r.closed }

// Geometry returns what the stream's parameter sets say about the frames.
//
// It becomes available as soon as an SPS arrives, which is typically the first
// picture of a live stream, so a status line can name a real resolution instead
// of leaving a blank for the first few seconds of a run.
func (r *Recorder) Geometry() (width, height, profile, level int, ok bool) {
	if !r.spOK {
		return 0, 0, 0, 0, false
	}
	return r.sp.Width, r.sp.Height, int(r.sp.Profile), int(r.sp.Level), true
}

func (r *Recorder) pushPicture(p *Picture) error {
	if p.Damaged != "" && r.damaged == "" {
		r.damaged = p.Damaged
	}
	switch r.state {
	case stateBuffering:
		r.pre.Push(p)
	case stateArmed:
		r.pre.Push(p)
		if !p.KeyFrame {
			return nil
		}
		retained := r.pre.Drain()
		if err := r.begin(retained); err != nil {
			return err
		}
		r.append(p)
	case stateRecording:
		r.append(p)
	}
	return nil
}

func (r *Recorder) begin(preRoll []*Picture) error {
	if len(preRoll) == 0 || !preRoll[0].KeyFrame {
		r.preRollReason = "the retained pre-roll does not start on a keyframe, so the lead-in would not decode"
		return fmt.Errorf("%w: %s", ErrNoClip, r.preRollReason)
	}
	if len(r.params.SPS) == 0 || len(r.params.PPS) == 0 {
		return fmt.Errorf("%w: the stream never delivered an SPS and a PPS", ErrNoParameterSets)
	}
	f, err := newFragmenter(r.opts.Timescale, &r.params)
	if err != nil {
		return err
	}
	r.frag = f
	r.state = stateRecording
	r.hasStart = true
	r.startDecode = preRoll[0].DecodeTime
	r.preRollSpan = r.pre.scaled(preRoll[len(preRoll)-1].DecodeTime - preRoll[0].DecodeTime)
	for _, p := range preRoll {
		if r.append(p) {
			r.preRollPics++
		}
	}
	return nil
}

// append stages one picture, holding it back until the next one arrives so that
// every sample can be written with the duration to its successor.
func (r *Recorder) append(p *Picture) bool {
	if r.frag == nil || r.closed {
		return false
	}
	if post := r.Units(r.opts.PostRoll); post > 0 && p.DecodeTime > r.triggerDecode &&
		p.DecodeTime-r.triggerDecode > post {
		r.closed = true
		r.noteStop("the post-roll window closed")
		return false
	}
	if r.held == nil {
		r.held = p
		return true
	}
	d := p.DecodeTime - r.held.DecodeTime
	if d == 0 {
		d = uint64(r.lastDelta)
	}
	if d == 0 {
		d = 1
	}
	if !r.write(r.held, uint32(d)) {
		r.held = nil
		return false
	}
	r.lastDelta = uint32(d)
	r.held = p
	return true
}

// write emits one sample, honouring the caps.
//
// The byte bound checked is an upper estimate - the sample's own AVCC bytes plus
// the largest moof this writer can produce - so the emitted clip can never
// exceed the cap rather than usually not exceeding it.
func (r *Recorder) write(p *Picture, duration uint32) bool {
	if r.hasStart && r.opts.MaxTime > 0 && p.DecodeTime > r.startDecode {
		if r.pre.scaled(p.DecodeTime-r.startDecode) > r.opts.MaxTime {
			r.cappedTime = true
			r.noteCap("the clip reached its time cap")
			r.closed = true
			return false
		}
	}
	est := int64(AVCCLen(p.NALs)) + 256
	if int64(len(r.frag.w.b))+est > r.opts.MaxBytes {
		r.cappedBytes = true
		r.noteCap("the clip reached its byte cap")
		r.closed = true
		return false
	}
	before := len(r.frag.w.b)
	// The clip's timeline starts at zero, not at whatever media timestamp the
	// camera happened to use. A demuxer measures a stream's duration from the
	// timeline origin, so a clip whose first sample sits at t=960ms declares a
	// duration a second longer than the picture the operator is about to watch.
	sample := *p
	sample.DecodeTime -= r.startDecode
	r.frag.appendSample(&sample, duration)
	r.pictures++
	if p.KeyFrame {
		r.keyframes++
	}
	if r.wallStart.IsZero() {
		r.wallStart = p.Wall
	}
	r.wallEnd = p.Wall
	return len(r.frag.w.b) > before
}

func (r *Recorder) noteStop(reason string) {
	if r.stopReason == "" {
		r.stopReason = reason
	}
}

func (r *Recorder) noteCap(reason string) {
	if r.capReason == "" {
		r.capReason = reason
	}
}

// Clip is the finished artefact and everything an operator needs to judge it.
type Clip struct {
	Data      []byte
	MediaType string

	Timescale uint32
	Duration  time.Duration
	Width     int
	Height    int
	Profile   int
	Level     int

	Pictures    int
	KeyFrames   int
	PreRollPics int
	PreRollSpan time.Duration

	TriggerReason string
	// PreRollReason is non-empty when the clip has no usable lead-in, and why.
	// A clip that starts at the event rather than before it is still evidence,
	// and an operator who is not told cannot tell it from a clip that has one.
	PreRollReason string
	// CapReason is non-empty when a bound stopped the clip growing.
	CapReason string
	// StopReason is non-empty when the clip ended because the post-roll window
	// closed rather than because a cap bound it. The two are kept apart because
	// "the window I asked for ended" and "the product stopped early" are
	// different things to be told.
	StopReason string
	// DamagedReason is non-empty when a NAL could not be parsed.
	DamagedReason string

	WallStart time.Time
	WallEnd   time.Time
}

// Size is the clip's length in bytes.
func (c *Clip) Size() int { return len(c.Data) }

// Finish completes the clip.
//
// An unarmed recorder, or one that never saw a keyframe, is an error rather than
// an empty file: an evidence object of zero bytes with the hash of nothing looks
// exactly like a clip of a scene where nothing happened.
func (r *Recorder) Finish() (*Clip, error) {
	if r.state != stateRecording {
		return nil, fmt.Errorf("%w: recorder was %s", ErrNoClip, stateName(r.state))
	}
	if p := r.builder.Flush(); p != nil {
		if err := r.pushPicture(p); err != nil {
			return nil, err
		}
	}
	if r.held != nil {
		d := r.lastDelta
		if d == 0 {
			d = r.opts.Timescale / 25
		}
		r.write(r.held, d)
		r.held = nil
	}
	// Recording is only reachable through begin, which parsed these bytes to
	// build the avcC, so the cached parse is the answer parseSPS would give here.
	// The guard is not a fallback: a clip that reported a geometry nobody parsed
	// would report zeroes, and zeroes in a report read as a stream of no size.
	if !r.spOK {
		return nil, errors.New("mp4: the recorder is recording but its parameter sets were never parsed")
	}
	c := &Clip{
		Data:          r.frag.w.b,
		MediaType:     "video/mp4",
		Timescale:     r.opts.Timescale,
		Duration:      r.pre.scaled(uint64(r.frag.duration())),
		Width:         r.sp.Width,
		Height:        r.sp.Height,
		Profile:       int(r.sp.Profile),
		Level:         int(r.sp.Level),
		Pictures:      r.pictures,
		KeyFrames:     r.keyframes,
		PreRollPics:   r.preRollPics,
		PreRollSpan:   r.preRollSpan,
		TriggerReason: r.triggerReason,
		PreRollReason: r.preRollReason,
		CapReason:     r.capReason,
		StopReason:    r.stopReason,
		DamagedReason: r.damaged,
		WallStart:     r.wallStart,
		WallEnd:       r.wallEnd,
	}
	if r.opts.budgetClamped && c.CapReason == "" {
		c.CapReason = "the pre-roll and post-roll asked for together exceeded the clip duration, so the pre-roll was reduced"
	}
	r.state = stateExhausted
	return c, nil
}

func stateName(s recorderState) string {
	switch s {
	case stateBuffering:
		return "never triggered"
	case stateArmed:
		return "triggered but the stream never delivered a keyframe"
	case stateRecording:
		return "recording"
	}
	return "exhausted"
}
