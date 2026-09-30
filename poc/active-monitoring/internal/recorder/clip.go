package recorder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClipRequest is a time window in wall-clock terms. The recorder maps it onto
// the segments that cover it; nothing here knows about frame indices or PTS.
type ClipRequest struct {
	From time.Time
	To   time.Time
	// Out is the file the clip is written to. It is always inside the caller's
	// own staging directory; the content store computes the final name from a
	// hash and this path is never trusted as an identity.
	Out string
}

// ClipResult says what was cut and how it was cut. `trim=reencode` is recorded
// because a re-encoded clip is a clip that starts on the frame ffmpeg could cut
// at, and the plan requires that to be visible in the report rather than
// discovered by a viewer.
type ClipResult struct {
	Path        string    `json:"path"`
	Trim        string    `json:"trim"`
	SHA256      string    `json:"sha256"`
	Bytes       int64     `json:"bytes"`
	Inputs      []string  `json:"inputs"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Reencoded   bool      `json:"reencoded"`
	DurationSec float64   `json:"duration_seconds"`
}

// Clipper cuts clips out of the rolling segments. It re-encodes rather than
// stream-copying, because a copy can only start on a keyframe and the clip
// would then miss the first second or two of the event it is meant to show.
type Clipper struct {
	FFmpeg     string
	FFprobe    string
	RollingDir string
	// Index supplies the segment index WITH its wall-clock measurements.
	//
	// It is a function and not a path on purpose. The measured coverage of a
	// segment is the wall clock of its first frame, which the recorder knows
	// because it read the frames, and that knowledge lives in memory. A fresh
	// ScanSegmentIndex finds the FILES and no times, so every segment is
	// unmeasured and every clip is refused with "before the first recorded
	// frame ((unmeasured))" — which is what the first end-to-end run on real
	// footage did, with the pipeline otherwise healthy.
	Index func() (*SegmentIndex, error)
	// SegmentSeconds is no longer needed for the mapping: coverage comes from
	// the measured wall clock of each segment's first frame.
	SegmentSeconds int
}

// Cut finds the segments covering the window, trims each to the part of the
// window it holds and concatenates them. The cut is re-encoded, so it starts on
// the exact frame rather than on the next keyframe.
func (c Clipper) Cut(ctx context.Context, req ClipRequest) (ClipResult, error) {
	if !req.To.After(req.From) {
		return ClipResult{}, fmt.Errorf("recorder: clip window [%s, %s] is empty; a clip that starts after it ends cannot show the event", req.From.Format(time.RFC3339Nano), req.To.Format(time.RFC3339Nano))
	}
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	var idx *SegmentIndex
	var err error
	if c.Index != nil {
		idx, err = c.Index()
	} else {
		idx, _, err = ScanSegmentIndex(c.RollingDir)
	}
	if err != nil {
		return ClipResult{}, err
	}
	if idx.Len() == 0 {
		return ClipResult{}, fmt.Errorf("recorder: no complete segment under %s, so there is nothing to cut a clip from; the event is recorded and its evidence is refused, not silently absent", c.RollingDir)
	}
	// Ask the index for the exact reason first, so the refusal uses the same
	// vocabulary as the rest of the module: before the recording, after it,
	// inside a gap, or inside a segment ffmpeg is still writing.
	if _, ferr := idx.Find(req.From); ferr != nil {
		return ClipResult{}, fmt.Errorf("recorder: cannot cut a clip for an event at %s: %w", req.From.Format(time.RFC3339), ferr)
	}

	// Only complete segments carry a known end, so only they are eligible. The
	// newest one is still being written and would give a truncated clip.
	var used []Segment
	for _, s := range idx.segments {
		if !s.Complete {
			break
		}
		if s.End.Before(req.From) || s.Start.After(req.To) {
			continue
		}
		used = append(used, s)
	}
	if len(used) == 0 {
		return ClipResult{}, fmt.Errorf("recorder: no segment covers [%s, %s]; the first is %s and the last is %s, so the recording does not reach this event",
			req.From.Format(time.RFC3339), req.To.Format(time.RFC3339),
			fmtTime(idx.segments[0].Start), fmtTime(idx.segments[len(idx.segments)-1].End))
	}

	// Each segment was written with -reset_timestamps 0, so it is a
	// self-contained file whose PTS starts at zero. Every segment is trimmed to
	// the part of the window it actually covers and its PTS is rebased, then the
	// pieces are concatenated. That is a re-encode: a stream copy can only
	// begin on a keyframe, so a copied clip would miss the first second of the
	// event it exists to show.
	// The filter graph.
	//
	// Two rules, and both were broken at once.
	//
	// ONE output label per filter.
	//
	// The single-segment graph ended `setpts=PTS-STARTPTS[p0][out]`, which reads as
	// giving `setpts` TWO output pads. `setpts` has one, so ffmpeg rejects the
	// graph with EINVAL — and on ffmpeg 8.1.1 on Windows it rejects it with no
	// stderr at all, so the run recorded a bare `exit status 0xffffffea` and the
	// report said only that clips were refused.
	//
	// So the LAST trim emits the final label itself, rather than emitting one and
	// having a second stage rename it.
	var filters []string
	var trimmed []string
	for n, s := range used {
		from := req.From
		if from.Before(s.Start) {
			from = s.Start
		}
		to := req.To
		if to.After(s.End) {
			to = s.End
		}
		start := from.Sub(s.Start).Seconds()
		if start < 0 {
			start = 0
		}
		end := to.Sub(s.Start).Seconds()
		if end <= start {
			continue
		}
		// The output label is decided by whether this is the LAST usable segment.
		last := n == len(used)-1
		label := fmt.Sprintf("[p%d]", n)
		if last {
			label = "[out]"
		}
		filters = append(filters, fmt.Sprintf("[%d:v]trim=start=%.3f:end=%.3f,setpts=PTS-STARTPTS%s", n, start, end, label))
		trimmed = append(trimmed, label)
	}
	if len(trimmed) == 0 {
		return ClipResult{}, fmt.Errorf("recorder: segments overlap the window [%s, %s] but none of them contains any part of it, so there is nothing to cut", req.From.Format(time.RFC3339), req.To.Format(time.RFC3339))
	}
	// concat needs n >= 2, which is the only reason the multi-input branch
	// exists. With one input the trim's own `[out]` is the output.
	if len(trimmed) > 1 {
		labels := make([]string, 0, len(trimmed)-1)
		for _, l := range trimmed {
			labels = append(labels, l)
		}
		filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[out]", strings.Join(labels, ""), len(trimmed)))
	}
	filter := strings.Join(filters, ";")

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}
	for _, s := range used {
		args = append(args, "-i", s.Path)
	}
	args = append(args, "-filter_complex", filter, "-map", "[out]", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-an", req.Out)

	cmd := exec.CommandContext(ctx, c.FFmpeg, args...)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ClipResult{}, fmt.Errorf("recorder: ffmpeg could not cut [%s, %s] from %d segment(s): %v: %s",
			req.From.Format(time.RFC3339), req.To.Format(time.RFC3339), len(used), err, stderr.String())
	}
	info, err := os.Stat(req.Out)
	if err != nil {
		return ClipResult{}, fmt.Errorf("recorder: ffmpeg reported success but %s does not exist: %w", req.Out, err)
	}
	if info.Size() == 0 {
		return ClipResult{}, fmt.Errorf("recorder: ffmpeg produced a zero-length clip for [%s, %s]; a clip that shows nothing is not evidence", req.From.Format(time.RFC3339), req.To.Format(time.RFC3339))
	}
	return ClipResult{
		Path:        req.Out,
		Trim:        "reencode",
		Bytes:       info.Size(),
		Inputs:      pathsOf(used),
		From:        req.From,
		To:          req.To,
		Reencoded:   true,
		DurationSec: req.To.Sub(req.From).Seconds(),
	}, nil
}

func pathsOf(segs []Segment) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		out = append(out, s.Path)
	}
	return out
}

func concatFilter(n int) string {
	labels := make([]string, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			labels[i] = "[first]"
		} else {
			labels[i] = fmt.Sprintf("[p%d]", i)
		}
	}
	return fmt.Sprintf("%sconcat=n=%d:v=1:a=0[cat]", join(labels, ""), n)
}

func join(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// ConcatListFor renders a concat demuxer list, used by the loop builder script
// and by tests that need to stitch segments without re-encoding.
func ConcatListFor(paths []string) string {
	var b []byte
	for _, p := range paths {
		b = append(b, "file '"...)
		b = append(b, filepath.ToSlash(p)...)
		b = append(b, "'\n"...)
	}
	return string(b)
}
