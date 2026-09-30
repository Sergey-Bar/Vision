package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SegPattern is the filename shape the segment muxer is given.
//
// It is a counter, not a strftime template, and that is a measured choice. On
// ffmpeg 8.1.1 `-strftime 1` with a `%Y%m%dT%H%M%S` template produces a single
// file that is never rotated, so the "rolling" recorder silently becomes a
// single-file recorder that grows until the disk fills. The numeric template
// rotates correctly on every ffmpeg we have tested, including 8.1.1.
//
// The wall clock is therefore not read out of the filename. It is measured by
// the reader, from the frames themselves, which is better anyway: see
// Recorder.ObserveSegments.
const SegPattern = "seg-%06d.mp4"

// SegPrefix is what every rolling file starts with, so the index can tell a
// segment from a stray file.
const SegPrefix = "seg-"

var segNameRE = regexp.MustCompile(`^seg-(\d+)\.mp4$`)

// Segment is one recorded file and the wall-clock interval it covers.
//
// Start is the wall clock of the FIRST FRAME of this segment, as stamped by the
// single reader. It is not derived from the filename and not derived from an
// assumed segment length. The covered interval is [Start, nextSegment.Start),
// which is exact even when the muxer cut late on a keyframe boundary, because
// both ends come from observed frames rather than from arithmetic.
type Segment struct {
	Index      int       `json:"index"`
	Path       string    `json:"path"`
	Start      time.Time `json:"start"`
	ObservedAt time.Time `json:"observed_at"`
	End        time.Time `json:"end"`
	Size       int64     `json:"size"`
	// Complete is false for the newest segment, whose successor has not been
	// observed yet and whose end time is therefore unknown. A clip is never cut
	// from an incomplete segment: the muxer is still writing to it.
	Complete bool `json:"complete"`
}

// Contains reports whether an instant falls inside the segment's interval. The
// interval is half-open, so two adjacent segments never both claim a frame.
func (s Segment) Contains(t time.Time) bool {
	if s.End.IsZero() {
		return false
	}
	return !t.Before(s.Start) && t.Before(s.End)
}

// ParseSegmentName reads the counter out of a segment filename. A file whose
// name is not a segment is not a segment: returning zero with no error would let
// a stray file become a hole in the recording, or a phantom of coverage.
func ParseSegmentName(name string) (int, error) {
	m := segNameRE.FindStringSubmatch(name)
	if m == nil {
		return 0, fmt.Errorf("recorder: %q is not a segment filename (expected %s000000.mp4)", name, SegPrefix)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("recorder: %q has an unreadable segment counter: %w", name, err)
	}
	return n, nil
}

// SegmentFileName is the name of segment n. The recorder uses it, and the
// tests use it to build fixtures, so the two cannot drift apart.
func SegmentFileName(n int) string { return fmt.Sprintf(SegPattern, n) }

// segmentObservation is what the reader records when it notices a new segment
// file: the index, when it saw it, and the wall clock of the most recent frame
// it had decoded. That frame is the boundary, because ffmpeg writes the file
// header for a segment immediately before the first packet that belongs to it,
// and the MJPEG pipe carries the same packet right after.
type segmentObservation struct {
	index      int
	observedAt time.Time
	frameClock time.Time
}

// SegmentIndex is the set of segments on disk, ordered by counter, with their
// measured wall-clock coverage.
type SegmentIndex struct {
	segments []Segment
}

// ScanSegmentIndex reads a rolling directory and builds an index from the
// counters alone. Wall-clock coverage is not knowable from a scan; it comes
// from Recorder.ObserveSegments, which merges observations. Files that are not
// segments are reported rather than skipped silently, because a naming change is
// exactly the kind of thing that would make gap detection claim coverage that
// does not exist.
func ScanSegmentIndex(dir string) (*SegmentIndex, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("recorder: cannot read the rolling directory %s: %w", dir, err)
	}
	idx := &SegmentIndex{}
	var nonSegments []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		n, err := ParseSegmentName(name)
		if err != nil {
			nonSegments = append(nonSegments, name)
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, nil, fmt.Errorf("recorder: cannot stat segment %s: %w", name, err)
		}
		if info.Size() == 0 {
			// The muxer has created the file but not written a byte yet. It is
			// not coverage, and counting it would close an open gap.
			continue
		}
		idx.segments = append(idx.segments, Segment{
			Index: n,
			Path:  filepath.Join(dir, name),
			Size:  info.Size(),
		})
	}
	sort.Slice(idx.segments, func(i, j int) bool { return idx.segments[i].Index < idx.segments[j].Index })
	return idx, nonSegments, nil
}

// ApplyObservations merges the reader's measurements into a scanned index. The
// caller supplies observations in ascending counter order; each one supplies the
// wall clock of the first frame of that segment.
//
// A segment is given coverage only when the segment that follows it is
// consecutive in counter order. If the counter jumps, the intervening segments
// were swept from the rolling directory, and the interval is then a gap: the
// recording for those seconds is no longer on disk, which is a different fact
// from "it was never recorded" and matters differently.
func (idx *SegmentIndex) ApplyObservations(obs []segmentObservation) {
	byIndex := make(map[int]segmentObservation, len(obs))
	for _, o := range obs {
		byIndex[o.index] = o
	}
	for i := range idx.segments {
		o, ok := byIndex[idx.segments[i].Index]
		if !ok {
			continue
		}
		idx.segments[i].Start = o.frameClock
		idx.segments[i].ObservedAt = o.observedAt
	}
	for i := range idx.segments {
		cur := &idx.segments[i]
		cur.Complete = false
		cur.End = time.Time{}
		if cur.Start.IsZero() {
			continue
		}
		if i+1 >= len(idx.segments) {
			// The newest segment: ffmpeg is still writing it, so its end is
			// unknown and it can hold no clip.
			continue
		}
		next := idx.segments[i+1]
		if next.Start.IsZero() {
			continue
		}
		if next.Index != cur.Index+1 {
			continue
		}
		cur.End = next.Start
		cur.Complete = true
	}
}

// Segments returns a copy, so a caller cannot reorder the index under the
// recorder's own bookkeeping.
func (i *SegmentIndex) Segments() []Segment {
	out := make([]Segment, len(i.segments))
	copy(out, i.segments)
	return out
}

// Len is the number of files the muxer has produced.
func (i *SegmentIndex) Len() int { return len(i.segments) }

// Complete is the number of segments with a known end.
func (i *SegmentIndex) Complete() int {
	n := 0
	for _, s := range i.segments {
		if s.Complete {
			n++
		}
	}
	return n
}

// Find maps an instant onto the segment that covers it. This is the operation
// that makes a clip's contents match the moment that triggered it, and every
// refusal names which of the four things went wrong: nothing was recorded yet,
// the segments in the way were swept, or the newest segment is still open.
func (i *SegmentIndex) Find(t time.Time) (Segment, error) {
	if len(i.segments) == 0 {
		return Segment{}, fmt.Errorf("recorder: no segment has been recorded yet, so %s cannot be mapped to anything", t.Format(time.RFC3339Nano))
	}
	first := i.segments[0]
	if first.Start.IsZero() || t.Before(first.Start) {
		return Segment{}, fmt.Errorf("recorder: %s is before the first recorded frame (%s): nothing was recorded then, so a clip would show the wrong thing",
			t.Format(time.RFC3339), fmtTime(first.Start))
	}
	for n, s := range i.segments {
		if s.Start.IsZero() || t.Before(s.Start) {
			continue
		}
		if s.Complete && s.Contains(t) {
			return s, nil
		}
		// Not inside a trusted interval. Which of the two reasons applies?
		if n+1 >= len(i.segments) {
			return Segment{}, fmt.Errorf("recorder: %s is inside segment %d, which ffmpeg is still writing: a clip cut from it would be truncated, so this event's evidence is refused until the segment closes",
				t.Format(time.RFC3339), s.Index)
		}
		next := i.segments[n+1]
		if next.Index != s.Index+1 {
			missing := make([]int, 0, next.Index-s.Index-1)
			for k := s.Index + 1; k < next.Index; k++ {
				missing = append(missing, k)
			}
			return Segment{}, fmt.Errorf("recorder: %s falls in a %s hole between segment %d and segment %d (segments %v were swept from the rolling directory by the disk budget): the recording for that interval is gone",
				t.Format(time.RFC3339), time.Duration(next.Start.Sub(s.Start).Round(time.Millisecond)), s.Index, next.Index, missing)
		}
		if !next.Start.IsZero() && t.Before(next.Start) {
			return Segment{}, fmt.Errorf("recorder: %s falls in a %s gap before segment %d: the stream was not recorded there",
				t.Format(time.RFC3339), next.Start.Sub(s.Start).Round(time.Millisecond), next.Index)
		}
	}
	last := i.segments[len(i.segments)-1]
	return Segment{}, fmt.Errorf("recorder: %s is inside segment %d, which ffmpeg is still writing: a clip cut from it would be truncated, so this event's evidence is refused until the segment closes",
		t.Format(time.RFC3339), last.Index)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "(unmeasured)"
	}
	return t.Format(time.RFC3339)
}

// Gap is a hole in the recorded timeline.
type Gap struct {
	From    time.Time     `json:"from"`
	To      time.Time     `json:"to"`
	Span    time.Duration `json:"-"`
	SpanSec float64       `json:"span_seconds"`
	Reason  string        `json:"reason"`
}

// Gaps returns every interval whose recording is not on disk: the segments that
// would have covered it were removed by the disk budget, or their wall clock was
// never measured.
//
// This is deliberately not "any interval longer than segment_time". Coverage is
// a partition built from measured frame clocks, so a long segment is a long
// segment, not a dropout. The dropout signal in this module is the heartbeat
// series, which is what AC1 scores.
func (i *SegmentIndex) Gaps() []Gap {
	var out []Gap
	for n := 0; n+1 < len(i.segments); n++ {
		cur, next := i.segments[n], i.segments[n+1]
		if cur.Start.IsZero() || next.Start.IsZero() {
			continue
		}
		if next.Index == cur.Index+1 {
			continue
		}
		missing := make([]int, 0, next.Index-cur.Index-1)
		for k := cur.Index + 1; k < next.Index; k++ {
			missing = append(missing, k)
		}
		span := next.Start.Sub(cur.Start)
		out = append(out, Gap{
			From: cur.Start, To: next.Start, Span: span, SpanSec: span.Seconds(),
			Reason: fmt.Sprintf("segments %v were swept from the rolling directory, so the recording for this %s is gone", missing, span.Round(time.Millisecond)),
		})
	}
	return out
}

// LongestGap is the largest uncovered interval, which is the number the report
// prints next to the heartbeat axis.
func (i *SegmentIndex) LongestGap() time.Duration {
	var longest time.Duration
	for _, g := range i.Gaps() {
		if g.Span > longest {
			longest = g.Span
		}
	}
	return longest
}

// TotalBytes is the size of everything in the index, which is the number the
// disk budget is measured against.
func (i *SegmentIndex) TotalBytes() int64 {
	var n int64
	for _, s := range i.segments {
		n += s.Size
	}
	return n
}

// evidenceExempt is the path fragment that marks a file as stored evidence.
// The sweep never sees evidence, and there is no path by which a stored clip is
// deleted to satisfy a disk budget. "Continue and delete" is how you lose the
// only copy of the evidence.
func evidenceExempt(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(normalized, "/evidence/")
}
