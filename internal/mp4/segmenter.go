package mp4

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"
)

// The segmenter.
//
// The Recorder makes a CLIP: one bounded artefact, armed by a trigger, with
// lead-in and post-roll. That is the right shape for "keep the thirty seconds
// around this event", and the wrong shape for a VMS, which must keep
// EVERYTHING, all the time, in pieces small enough to find and cheap enough to
// keep.
//
// A segmenter is the same muxer with a different job: it never stops, and it
// hands back a finished, INDEPENDENTLY PLAYABLE segment every few seconds.
//
// # Why the segments are cut on a keyframe
//
// A segment that starts on a non-keyframe cannot be decoded on its own: the
// first picture references frames that are not in the file. So a segment is
// cut AT a keyframe, and the keyframe that ends one segment is the first
// picture of the next.
//
// That is also why there is no hole in the recorded timeline. The picture
// before the keyframe is the last picture of segment N; the keyframe is the
// first picture of segment N+1. Every frame the camera sent is in exactly one
// segment, and every segment plays from its own first frame.
//
// # Why the timeline starts at zero
//
// A demuxer measures a stream's duration from the timeline origin. A segment
// whose first sample sits at t=960ms declares a duration a second longer
// than the picture the operator is about to watch. So each segment's
// timestamps are rebased onto its own start — the same rule the Recorder
// keeps, for the same reason.

// DefaultSegmentDuration is the target length of one segment.
//
// Ten seconds is the shape the industry settled on, and the reasons are
// practical rather than traditional: a segment small enough to find in a
// timeline, large enough that the index does not become the largest thing in
// the install, and long enough that a keyframe interval is a small fraction
// of it.
const DefaultSegmentDuration = 10 * time.Second

// DefaultMaxSegmentBytes bounds one segment.
//
// It is a bound rather than a target: a camera on a busy scene produces a
// bigger segment, and the bound is what stops one device from filling the
// disk while the others are starved.
const DefaultMaxSegmentBytes = 64 << 20

// ErrNoSegment is a segmenter that has nothing to hand back.
//
// A segmenter that has not yet seen a keyframe has nothing to write, and a
// zero-byte segment with a digest is indistinguishable from a scene in which
// nothing happened.
var ErrNoSegment = errors.New("mp4: no segment was produced")

// SegmentOptions configures a Segmenter.
type SegmentOptions struct {
	// SegmentDuration is the TARGET length of one segment. The realised
	// length is the target plus up to one keyframe interval, because a
	// segment is cut at a keyframe and never in the middle of a group.
	SegmentDuration time.Duration
	// MaxSegmentBytes bounds one segment's bytes.
	MaxSegmentBytes int64
	// Timescale is the stream's own clock rate. The RTP clock (90 kHz) is
	// the usual answer.
	Timescale uint32
	// OnDamaged is called for every NAL that could not be parsed well enough
	// to find a picture boundary. A degradation printed and dropped is a
	// degradation an operator cannot see.
	OnDamaged func(string)
}

func (o SegmentOptions) defaulted() SegmentOptions {
	out := o
	if out.Timescale == 0 {
		out.Timescale = DefaultTimescale
	}
	if out.SegmentDuration <= 0 {
		out.SegmentDuration = DefaultSegmentDuration
	}
	if out.MaxSegmentBytes <= 0 {
		out.MaxSegmentBytes = DefaultMaxSegmentBytes
	}
	return out
}

// Segment is one finished segment and everything an operator needs to judge
// it.
type Segment struct {
	Data      []byte
	MediaType string

	Timescale uint32
	Duration  time.Duration
	Width     int
	Height    int
	Profile   int
	Level     int

	Pictures  int
	KeyFrames int

	// WallStart and WallEnd are the wall-clock instants the segment covers.
	// They are the wall clock and not the media timeline, and the difference
	// between the two is the drift the clock subsystem measures.
	WallStart time.Time
	WallEnd   time.Time

	// GapBefore is how long the camera was silent between the previous
	// segment's last frame and this segment's first.
	//
	// On a healthy stream it is about one frame interval, because the
	// keyframe that ends one segment is the first frame of the next. It is
	// published rather than assumed because a camera that went quiet for
	// four minutes and one that is running at 25 frames a second differ only
	// in this number, and the recording-continuity metric is computed from
	// it.
	GapBefore time.Duration

	// DamagedReason is non-empty when a NAL could not be parsed.
	DamagedReason string
}

// Size is the segment's length in bytes.
func (s *Segment) Size() int { return len(s.Data) }

// Segmenter turns a live NAL stream into a rolling sequence of segments.
//
// It is fed the same NAL units the Recorder is fed, and it never stops: a
// segmenter is a recorder that starts itself.
type Segmenter struct {
	opts    SegmentOptions
	builder PictureBuilder
	params  ParameterSets

	// sp is what params.SPS says, parsed once when the SPS was stored.
	sp   SequenceParameters
	spOK bool

	// frag is the CURRENT segment's writer. It is nil until the first
	// keyframe, because a segment cannot begin on anything else.
	frag *fragmenter

	// held is the picture waiting for its successor, so that every sample
	// can be written with the duration to the one that follows it.
	held *Picture
	// lastDelta is the duration of the last written sample, used to close
	// the final sample of a segment.
	lastDelta uint32

	segStartDecode uint64
	segStartWall   time.Time
	segEndWall     time.Time
	segPictures    int
	segKeyFrames   int

	// prevEndWall is the wall instant the previous segment ended on, and
	// the gap is measured from it.
	prevEndWall time.Time

	damaged string
	started bool
}

// NewSegmenter returns a segmenter with the given options defaulted.
func NewSegmenter(opts SegmentOptions) *Segmenter {
	o := opts.defaulted()
	s := &Segmenter{opts: o}
	s.builder.OnDamaged = func(reason string) {
		if s.damaged == "" {
			s.damaged = reason
		}
		if o.OnDamaged != nil {
			o.OnDamaged(reason)
		}
	}
	return s
}

// Geometry returns what the stream's parameter sets say about the frames.
//
// It becomes available as soon as an SPS arrives, which is typically the first
// picture of a live stream, so a status line can name a real resolution
// instead of leaving a blank for the first few seconds.
func (s *Segmenter) Geometry() (width, height, profile, level int, ok bool) {
	if !s.spOK {
		return 0, 0, 0, 0, false
	}
	return s.sp.Width, s.sp.Height, int(s.sp.Profile), int(s.sp.Level), true
}

// PushNAL adds one NAL unit, without its start code, and returns a finished
// segment when one completed.
//
// A parameter-set change mid-stream is refused rather than continued, because
// a file carrying two incompatible answers to "what resolution is this" is
// answered by neither of them.
func (s *Segmenter) PushNAL(nal []byte, decodeTime uint64, wall time.Time) (*Segment, error) {
	if typ, ok := Parse(nal); ok {
		switch typ {
		case NALPrefix:
			switch {
			case bytes.Equal(s.params.SPS, nal):
				// The same parameter set again, which is what a camera sends
				// before every keyframe. Nothing to store, and the geometry
				// already parsed from it still stands.
			case len(s.params.SPS) > 0:
				return nil, fmt.Errorf("%w: SPS changed at media time %d", ErrProfileChange, decodeTime)
			default:
				s.params.SPS = append([]byte(nil), nal...)
				if sp, err := ParseSequenceParameters(nal); err == nil {
					s.sp, s.spOK = sp, true
				}
			}
		case NALSuffix:
			switch {
			case bytes.Equal(s.params.PPS, nal):
			case len(s.params.PPS) > 0:
				return nil, fmt.Errorf("%w: PPS changed at media time %d", ErrProfileChange, decodeTime)
			default:
				s.params.PPS = append([]byte(nil), nal...)
			}
		}
	}
	p := s.builder.Push(nal, decodeTime, wall)
	if p == nil {
		return nil, nil
	}
	return s.pushPicture(p)
}

// Close finishes the segment in progress and returns it.
//
// It is what a caller does when the stream ends. A segmenter that is closed
// while it holds a segment that has never been cut is not a failure: the
// camera may simply have gone quiet, and a segmenter with nothing to say
// says so.
func (s *Segmenter) Close() (*Segment, error) {
	if !s.started {
		return nil, fmt.Errorf("%w: the segmenter never saw a keyframe", ErrNoSegment)
	}
	return s.closeSegment()
}

// pushPicture places one finished picture.
func (s *Segmenter) pushPicture(p *Picture) (*Segment, error) {
	if p.Damaged != "" && s.damaged == "" {
		s.damaged = p.Damaged
	}

	// The first segment waits for a keyframe, because a segment that starts
	// on anything else cannot be decoded on its own.
	if !s.started {
		if !p.KeyFrame {
			return nil, nil
		}
		if err := s.begin(p); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// A keyframe is where a segment may be cut. The picture BEFORE it
	// belongs to the segment that is closing, and the keyframe itself is the
	// first picture of the next one — so every frame is in exactly one
	// segment and the timeline has no hole.
	if p.KeyFrame && s.segmentLongEnough(p.DecodeTime) {
		seg, err := s.closeSegment()
		if err != nil {
			return nil, err
		}
		if err := s.begin(p); err != nil {
			return nil, err
		}
		return seg, nil
	}

	s.append(p)
	return nil, nil
}

// segmentLongEnough reports whether the segment in progress has run for its
// target length.
func (s *Segmenter) segmentLongEnough(decodeTime uint64) bool {
	if s.opts.SegmentDuration <= 0 {
		return false
	}
	if decodeTime <= s.segStartDecode {
		return false
	}
	return s.scaled(decodeTime-s.segStartDecode) >= s.opts.SegmentDuration
}

// scaled converts a media-timestamp difference into a duration.
//
// The timestamps are in the STREAM's clock, not in nanoseconds, so the divisor
// is the timescale and not a second — the same conversion `PreRoll.scaled`
// makes, and the same reason: a segment that thinks it is a second long when
// it is a frame long is a segment that is cut on every keyframe.
//
// The multiplication is split so a long difference at a high clock rate
// saturates instead of wrapping into a small number, which would be a
// segment duration of a few milliseconds.
func (s *Segmenter) scaled(d uint64) time.Duration {
	if s.opts.Timescale == 0 || d == 0 {
		return 0
	}
	whole := d / uint64(s.opts.Timescale)
	rem := d % uint64(s.opts.Timescale)
	nanos := whole*uint64(time.Second) + rem*uint64(time.Second)/uint64(s.opts.Timescale)
	if nanos > uint64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(nanos)
}

// begin starts a segment at the given picture, which must be a keyframe.
func (s *Segmenter) begin(p *Picture) error {
	if len(s.params.SPS) == 0 || len(s.params.PPS) == 0 {
		return fmt.Errorf("%w: the stream never delivered an SPS and a PPS", ErrNoParameterSets)
	}
	f, err := newFragmenter(s.opts.Timescale, &s.params)
	if err != nil {
		return err
	}
	s.frag = f
	s.started = true
	s.segStartDecode = p.DecodeTime
	s.segStartWall = p.Wall
	s.segEndWall = p.Wall
	s.segPictures = 0
	s.segKeyFrames = 0
	s.held = p
	s.lastDelta = 0
	return nil
}

// append stages one picture, holding it back until the next one arrives so
// that every sample can be written with the duration to its successor.
func (s *Segmenter) append(p *Picture) {
	if s.frag == nil {
		return
	}
	if s.held == nil {
		s.held = p
		return
	}
	d := p.DecodeTime - s.held.DecodeTime
	if d == 0 {
		d = uint64(s.lastDelta)
	}
	if d == 0 {
		d = 1
	}
	if s.write(s.held, uint32(d)) {
		s.lastDelta = uint32(d)
	}
	s.held = p
	s.segEndWall = p.Wall
}

// write emits one sample, honouring the byte bound.
//
// The byte bound checked is an upper estimate — the sample's own AVCC bytes
// plus the largest moof this writer can produce — so the emitted segment can
// never exceed the cap rather than usually not exceeding it.
func (s *Segmenter) write(p *Picture, duration uint32) bool {
	if s.frag == nil {
		return false
	}
	est := int64(AVCCLen(p.NALs)) + 256
	if int64(len(s.frag.w.b))+est > s.opts.MaxSegmentBytes {
		return false
	}
	// The segment's timeline starts at zero, not at whatever media timestamp
	// the camera happened to use.
	sample := *p
	sample.DecodeTime -= s.segStartDecode
	s.frag.appendSample(&sample, duration)
	s.segPictures++
	if p.KeyFrame {
		s.segKeyFrames++
	}
	if s.segStartWall.IsZero() {
		s.segStartWall = p.Wall
	}
	s.segEndWall = p.Wall
	return true
}

// closeSegment finishes the segment in progress and returns it.
func (s *Segmenter) closeSegment() (*Segment, error) {
	if s.frag == nil {
		return nil, fmt.Errorf("%w: no segment is in progress", ErrNoSegment)
	}
	if s.held != nil {
		d := s.lastDelta
		if d == 0 {
			d = s.opts.Timescale / 25
		}
		s.write(s.held, d)
		s.held = nil
	}
	if !s.spOK {
		return nil, errors.New("mp4: the segmenter is recording but its parameter sets were never parsed")
	}
	seg := &Segment{
		Data:          s.frag.w.b,
		MediaType:     "video/mp4",
		Timescale:     s.opts.Timescale,
		Duration:      s.scaled(uint64(s.frag.duration())),
		Width:         s.sp.Width,
		Height:        s.sp.Height,
		Profile:       int(s.sp.Profile),
		Level:         int(s.sp.Level),
		Pictures:      s.segPictures,
		KeyFrames:     s.segKeyFrames,
		DamagedReason: s.damaged,
		WallStart:     s.segStartWall,
		WallEnd:       s.segEndWall,
	}
	if !s.prevEndWall.IsZero() {
		seg.GapBefore = s.segStartWall.Sub(s.prevEndWall)
	}
	s.prevEndWall = s.segEndWall
	s.frag = nil
	s.started = false
	return seg, nil
}
