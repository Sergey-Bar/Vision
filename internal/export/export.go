// Package export turns a recorded window into a clip.
//
// # What it is
//
// An operator watching a clip wants the seconds BEFORE the moment as well as
// the moment. The moment is in a segment; the seconds before it are in the
// segment before THAT one.
//
// So a clip is a LIST of segments: the ones covering the window, plus the
// PRE-ROLL — the segment immediately before the window, which is where the
// lead-in lives.
//
// # Why it is a list and not a file
//
// Each segment is an independently playable fragmented MP4, and the
// product's own recorder wrote them that way on purpose: a segment that
// cannot be decoded on its own is not evidence.
//
// Concatenating them into one file means RE-MUXING, and a re-mux is a second
// implementation of a muxer that already exists. The list is honest, it plays,
// and it is what the recording index already describes.
//
// A caller that wants ONE file walks the list and concatenates the BYTES: the
// segments are contiguous by construction — the keyframe that ends one is the
// first frame of the next — so a byte concatenation is a playable clip on
// any player that accepts a fragmented MP4 with several movie fragments.
package export

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision/internal/recording"
)

// ErrNoWindow is a window with no device in it.
var ErrNoWindow = errors.New("export: the window names no device")

// Clip is a window of the recording.
type Clip struct {
	// DeviceID is the camera the window is about.
	DeviceID string
	// From and To are the window, as asked for.
	From time.Time
	To   time.Time
	// Segments are the segments covering the window, in order.
	Segments []recording.Segment
	// PreRoll is the segment immediately BEFORE the window, when there is
	// one.
	//
	// It is the lead-in: the seconds before the moment the operator asked
	// about. It is nil when the window starts at the beginning of what was
	// recorded, and that is a fact about the recording rather than a gap in
	// the clip.
	PreRoll *recording.Segment
	// Gaps are the holes inside the window.
	Gaps []recording.Gap
	// CoveredMs is how much of the window the recording actually covers.
	CoveredMs int64
	// WindowMs is how long the window is.
	WindowMs int64
}

// Service builds clips out of the recording index.
type Service struct {
	Index *recording.Service
	// Frame is the frame interval, used to tell a segment boundary from a
	// hole.
	//
	// It defaults to a hundred milliseconds — a camera at ten frames a
	// second — because the boundary between two segments is about one
	// frame wide and a gap is not.
	Frame time.Duration
}

// NewService returns the export service.
func NewService(index *recording.Service) *Service {
	return &Service{Index: index, Frame: 100 * time.Millisecond}
}

// Clip builds the clip for a window.
//
// The window is half-open: it covers [from, to). The pre-roll is the
// segment that ENDS at or after `from` and starts before it — the one the
// moment sits in the middle of.
func (s *Service) Clip(ctx context.Context, tenant, site, deviceID string, from, to time.Time) (*Clip, error) {
	if s == nil || s.Index == nil {
		return nil, errors.New("export: the export service has no index behind it")
	}
	if deviceID == "" {
		return nil, ErrNoWindow
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: the window ends before it starts", ErrNoWindow)
	}

	// The segments that cover the window.
	//
	// The OVERLAPPING ones and not the ones that began in it: a segment that
	// started before the window opened and ran into it was partly recorded
	// in the window, and a clip that leaves it out starts in the middle of a
	// frame.
	segments, err := s.Index.Overlapping(ctx, tenant, site, deviceID, from, to)
	if err != nil {
		return nil, err
	}

	clip := &Clip{
		DeviceID: deviceID,
		From:     from,
		To:       to,
		WindowMs: to.Sub(from).Milliseconds(),
	}
	for i := range segments {
		seg := segments[i]
		clip.Segments = append(clip.Segments, seg)
		covered := seg.EndedAt.Sub(seg.StartedAt)
		if seg.StartedAt.Before(from) {
			covered = seg.EndedAt.Sub(from)
		}
		if seg.EndedAt.After(to) {
			covered = to.Sub(seg.StartedAt)
		}
		if covered > 0 {
			clip.CoveredMs += covered.Milliseconds()
		}
	}

	// The PRE-ROLL: the segment that ended at or before the window opened.
	//
	// It is asked for EXACTLY rather than looked for nearby, because the
	// segment before the window's first one may have ended a long time
	// before the window opened — and the lead-in is in that segment
	// whatever the gap between them was.
	preRoll, err := s.Index.Previous(ctx, tenant, site, deviceID, from)
	if err != nil {
		return nil, err
	}
	clip.PreRoll = preRoll

	gaps, err := s.Index.Gaps(ctx, tenant, site, deviceID, from, to, s.Frame)
	if err != nil {
		return nil, err
	}
	clip.Gaps = gaps
	return clip, nil
}
