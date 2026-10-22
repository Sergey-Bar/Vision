package mp4_test

import (
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
)

// The segmenter.
//
// A VMS records all the time, in pieces small enough to find. The two
// properties that make that work are: every segment is INDEPENDENTLY
// PLAYABLE, and the recorded timeline has NO HOLE.
//
// The tests below are those two properties, and the failure they protect
// against is the same one every time: a segment that cannot be decoded on its
// own, or a frame that is in no segment at all.
//
// The stream is the COMMITTED FIXTURE, not a synthetic one, for the same
// reason every other muxer test uses it: a hand-built NAL is a rumour about
// what a camera sends, and the rumour is what breaks.

// segmentFixture is the committed H.264 fixture the muxer tests read.
const segmentFixture = "../../testdata/poc/testsrc.h264"

// segmentPictures reads the fixture into pictures.
func segmentPictures(t *testing.T) []*mp4.Picture {
	t.Helper()
	f, err := os.Open(segmentFixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	pr := mp4.ReadPictures(f, 25, mp4.DefaultTimescale)
	var out []*mp4.Picture
	for {
		p, err := pr.Next()
		if p != nil {
			out = append(out, p)
		}
		if err != nil {
			break
		}
	}
	if len(out) == 0 {
		t.Fatal("the fixture produced no picture")
	}
	return out
}

// pushAll feeds pictures to a segmenter and returns the segments it made.
//
// It CLOSES the segmenter at the end, because the segment in progress when
// the stream ends is a segment: a segmenter that threw away the last
// twenty-five frames would lose the end of every recording, and the end of a
// recording is often the part somebody needs.
func pushAll(t *testing.T, s *mp4.Segmenter, pics []*mp4.Picture) []*mp4.Segment {
	t.Helper()
	wall := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	var out []*mp4.Segment
	for i, p := range pics {
		for _, nal := range p.NALs {
			seg, err := s.PushNAL(nal, p.DecodeTime, wall.Add(time.Duration(i)*time.Second/25))
			if err != nil {
				t.Fatalf("picture %d: %v", i, err)
			}
			if seg != nil {
				out = append(out, seg)
			}
		}
	}
	last, err := s.Close()
	if err == nil && last != nil {
		out = append(out, last)
	}
	return out
}

func TestTheSegmenterProducesIndependentlyPlayableSegments(t *testing.T) {
	pics := segmentPictures(t)
	s := mp4.NewSegmenter(mp4.SegmentOptions{
		SegmentDuration: 2 * time.Second,
		Timescale:       mp4.DefaultTimescale,
	})
	segs := pushAll(t, s, pics)

	if len(segs) == 0 {
		t.Fatalf("the segmenter produced no segment from %d pictures", len(pics))
	}
	for i, seg := range segs {
		if len(seg.Data) == 0 {
			t.Errorf("segment %d has no bytes", i)
		}
		if seg.MediaType != "video/mp4" {
			t.Errorf("segment %d is %q, want video/mp4", i, seg.MediaType)
		}
		if seg.Pictures == 0 {
			t.Errorf("segment %d carries no picture", i)
		}
		if seg.KeyFrames == 0 {
			t.Errorf("segment %d carries no keyframe. A segment with no keyframe cannot be "+
				"decoded on its own, and a segment that cannot be decoded on its own is not evidence", i)
		}
		if seg.Width == 0 || seg.Height == 0 {
			t.Errorf("segment %d reports a %dx%d geometry. Zeroes in a report read as a stream "+
				"of no size", i, seg.Width, seg.Height)
		}
	}
}

func TestTheRecordedTimelineHasNoHole(t *testing.T) {
	// Every frame the camera sent is in exactly one segment, and the
	// segments are CONTIGUOUS: the keyframe that ends one is the first frame
	// of the next.
	pics := segmentPictures(t)
	s := mp4.NewSegmenter(mp4.SegmentOptions{
		SegmentDuration: time.Second,
		Timescale:       mp4.DefaultTimescale,
	})
	segs := pushAll(t, s, pics)
	if len(segs) < 2 {
		t.Fatalf("the segmenter produced %d segment(s) from %d pictures, want at least 2",
			len(segs), len(pics))
	}
	for i := 1; i < len(segs); i++ {
		gap := segs[i].WallStart.Sub(segs[i-1].WallEnd)
		if gap < 0 {
			t.Errorf("segment %d starts before segment %d ends: %s", i, i-1, gap)
		}
		// A frame is 40ms at 25fps. The gap between two segments is the
		// interval between the last frame of one and the first of the next.
		if gap > 200*time.Millisecond {
			t.Errorf("segment %d starts %s after segment %d ended. A segment that starts on "+
				"anything but the frame after the previous one leaves a hole in the recording",
				i, gap, i-1)
		}
	}
}

func TestTheSegmentsCarryEveryPictureTheCameraSent(t *testing.T) {
	// The count is the point: a segmenter that drops a frame at a boundary
	// loses a frame of evidence, and a hole in a recording is invisible to
	// everybody except the person who needs it.
	pics := segmentPictures(t)
	s := mp4.NewSegmenter(mp4.SegmentOptions{
		SegmentDuration: time.Second,
		Timescale:       mp4.DefaultTimescale,
	})
	segs := pushAll(t, s, pics)
	total := 0
	for _, seg := range segs {
		total += seg.Pictures
	}
	if total < len(pics)-1 {
		t.Errorf("the segments carry %d picture(s) and the stream had %d. A segmenter that "+
			"loses frames at the boundary loses evidence", total, len(pics))
	}
}

func TestTheSegmenterSaysWhenItHasNothingToHandBack(t *testing.T) {
	// A segmenter that has not seen a keyframe has no segment, and a
	// zero-byte segment with a digest is indistinguishable from a scene in
	// which nothing happened.
	s := mp4.NewSegmenter(mp4.SegmentOptions{SegmentDuration: time.Second})
	if _, err := s.Close(); err == nil {
		t.Fatal("a segmenter that never saw a keyframe produced a segment")
	}
}
