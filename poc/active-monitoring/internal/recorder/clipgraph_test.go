package recorder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The clip filter graph, tested as a GRAPH and then against a real ffmpeg.
//
// Two bugs lived here at once, and both produced the same symptom — a bare
// `exit status 0xffffffea` with NO stderr at all on ffmpeg 8.1.1, so the run
// recorded an empty reason and the report said only that clips were refused:
//
//   - `[0:v]` was named TWICE in the single-segment graph, once for the trim and
//     once for a passthrough. A filtergraph may consume each input pad once.
//   - the multi-segment graph fed `concat` the RAW pads, so it concatenated the
//     segments' own timelines rather than the window — a clip of the wrong
//     seconds, which is worse than no clip.
//
// The graph tests are instant and name the rule. The real-cut tests are the ones
// that would have caught it before a five-minute run did.

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// buildClipGraph mirrors what Clipper.Cut assembles, so a test can fail on the
// GRAPH without an ffmpeg run and the failure names the rule.
func buildClipGraph(inputs int, trims [][2]float64) string {
	var filters, trimmed []string
	for n := 0; n < inputs; n++ {
		s, e := 0.0, 0.0
		if n < len(trims) {
			s, e = trims[n][0], trims[n][1]
		}
		label := sprintf("[p%d]", n)
		if n == inputs-1 {
			label = "[out]"
		}
		filters = append(filters, sprintf("[%d:v]trim=start=%.3f:end=%.3f,setpts=PTS-STARTPTS%s", n, s, e, label))
		trimmed = append(trimmed, sprintf("[p%d]", n))
	}
	g := strings.Join(filters, ";")
	if len(trimmed) > 1 {
		return g + ";" + strings.Join(trimmed, "") + sprintf("concat=n=%d:v=1:a=0[out]", len(trimmed))
	}
	return g
}

// TestEachInputPadIsNamedOnce: a filtergraph may consume each input pad exactly
// once. The single-segment graph named `[0:v]` twice and ffmpeg rejected it
// silently.
func TestEachInputPadIsNamedOnce(t *testing.T) {
	for _, inputs := range []int{1, 2, 3} {
		trims := make([][2]float64, inputs)
		for i := range trims {
			trims[i] = [2]float64{float64(i), float64(i) + 1}
		}
		graph := buildClipGraph(inputs, trims)
		for n := 0; n < inputs; n++ {
			pad := sprintf("[%d:v]", n)
			if got := strings.Count(graph, pad); got != 1 {
				t.Errorf("with %d input(s) the pad %s is named %d times in %q; a filtergraph may consume each pad once", inputs, pad, got, graph)
			}
		}
		if !strings.Contains(graph, "[out]") {
			t.Errorf("with %d input(s) the graph has no [out] label: %q", inputs, graph)
		}
	}
}

// TestConcatIsFedTheTrimmedOutputs: `concat` must take the trims' OUTPUTS. Fed
// the raw pads it would join the segments' own timelines, not the window.
func TestConcatIsFedTheTrimmedOutputs(t *testing.T) {
	graph := buildClipGraph(2, [][2]float64{{0, 1}, {1, 2}})
	concatAt := strings.Index(graph, "concat=")
	if concatAt < 0 {
		t.Fatalf("two inputs produced no concat: %q", graph)
	}
	head := graph[:concatAt]
	if i := strings.LastIndex(head, ";"); i >= 0 {
		head = head[i+1:]
	}
	if !strings.HasPrefix(head, "[p0][p1]") {
		t.Errorf("concat is fed %q, want [p0][p1] — the trims' OUTPUTS: %q", head, graph)
	}
	if strings.Contains(head, ":v]") {
		t.Errorf("concat is fed a raw input pad %q, which would concatenate the segments' own timelines rather than the window: %q", head, graph)
	}
}

// TestConcatIsNeverEmittedForASingleInput: ffmpeg's concat filter requires
// n >= 2, so `concat=n=1` fails with EINVAL and no message.
func TestConcatIsNeverEmittedForASingleInput(t *testing.T) {
	graph := buildClipGraph(1, [][2]float64{{0, 1}})
	if strings.Contains(graph, "concat") {
		t.Fatalf("a single input produced a concat filter: %q", graph)
	}
}

// cutFixture builds real 10-second segments, applies the recorder's wall-clock
// measurements, and returns a clipper over them.
//
// The SECOND segment is the point: a clip may only be cut from a segment ffmpeg
// has finished writing, so a one-segment fixture is a fixture in which every
// cut is correctly refused.
func cutFixture(t *testing.T, seconds int) (Clipper, time.Time) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2)")
	}
	work := t.TempDir()
	clip := filepath.Join(work, "src.mp4")
	if o, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=320x180:rate=10", seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not render the fixture: %v: %s", err, o)
	}
	rolling := filepath.Join(work, "rolling")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	// The recorder's real segment files: a COUNTER in the name, REAL VIDEO
	// inside, and the wall clock measured from the frames rather than read out of
	// the filename.
	//
	// The video matters. A one-byte placeholder named like a segment makes ffmpeg
	// die with an access violation (0xC0000005), which looks nothing like a bad
	// argument and says nothing at all about the graph under test — which is
	// exactly what the first version of this fixture did.
	const segSeconds = 10
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2)")
	}
	n := seconds / segSeconds
	start := time.Now().Add(-time.Duration(seconds+10) * time.Second)
	var observations []segmentObservation
	for i := 0; i < n; i++ {
		out := filepath.Join(rolling, SegmentFileName(i))
		if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
			"-ss", fmt.Sprint(i*segSeconds), "-t", fmt.Sprint(segSeconds), "-i", clip,
			"-c", "copy", out).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg could not cut segment %d: %v: %s", i, err, o)
		}
		observations = append(observations, obs(i, start.Add(time.Duration(i*segSeconds)*time.Second)))
	}
	idx, _, err := ScanSegmentIndex(rolling)
	if err != nil {
		t.Fatal(err)
	}
	idx.ApplyObservations(observations)
	if idx.Complete() < 1 {
		t.Fatalf("the fixture has %d closed segments; a cut needs at least one", idx.Complete())
	}
	return Clipper{FFmpeg: "ffmpeg", RollingDir: rolling,
		Index: func() (*SegmentIndex, error) { return idx, nil }}, start
}

func probeSeconds(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe failed on %s: %v", path, err)
	}
	var d float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d); err != nil {
		t.Fatalf("ffprobe returned %q for %s, which is not a duration", strings.TrimSpace(string(out)), path)
	}
	return d
}

// TestAClipInsideOneClosedSegmentIsCutForReal is the end-to-end proof, against a
// real ffmpeg and real segments, and it checks the clip's LENGTH as well as its
// existence: a clip of the right size showing the wrong window is worse than no
// clip.
func TestAClipInsideOneClosedSegmentIsCutForReal(t *testing.T) {
	c, start := cutFixture(t, 30)
	outPath := filepath.Join(t.TempDir(), "clip.mp4")
	at := start.Add(2 * time.Second) // 2..6s, entirely inside the first 10s segment
	res, err := c.Cut(context.Background(), ClipRequest{From: at, To: at.Add(4 * time.Second), Out: outPath})
	if err != nil {
		t.Fatalf("a clip inside one CLOSED segment was refused: %v", err)
	}
	if res.Bytes <= 0 {
		t.Fatal("the clip is empty")
	}
	if len(res.Inputs) != 1 {
		t.Fatalf("the clip used %d inputs, want 1", len(res.Inputs))
	}
	if !res.Reencoded {
		t.Fatal("the clip was not re-encoded, so it may not begin on the window's first frame")
	}
	if d := probeSeconds(t, outPath); d < 3.0 || d > 5.0 {
		t.Fatalf("the clip is %.2fs long for a 4s window; the trim or the re-encode is wrong", d)
	}
}

// TestAClipSpanningTwoSegmentsIsCutForReal is the multi-segment path.
func TestAClipSpanningTwoSegmentsIsCutForReal(t *testing.T) {
	c, start := cutFixture(t, 30)
	outPath := filepath.Join(t.TempDir(), "clip.mp4")
	at := start.Add(8 * time.Second) // 8..12s, crossing the 10s boundary
	res, err := c.Cut(context.Background(), ClipRequest{From: at, To: at.Add(4 * time.Second), Out: outPath})
	if err != nil {
		t.Fatalf("a clip spanning two segments was refused: %v", err)
	}
	if len(res.Inputs) != 2 {
		t.Fatalf("the clip used %d inputs, want 2: the window 8..12s crosses the 10s boundary", len(res.Inputs))
	}
	if d := probeSeconds(t, outPath); d < 3.0 || d > 5.0 {
		t.Fatalf("the clip spanning two segments is %.2fs long for a 4s window; concat is not fed the trimmed outputs", d)
	}
}

// TestTheNewestSegmentIsRefusedForAClip is why cutFixture needs two segments,
// written down so nobody simplifies it back to one and wonders why every cut
// fails.
func TestTheNewestSegmentIsRefusedForAClip(t *testing.T) {
	c, start := cutFixture(t, 20) // exactly two 10s segments
	_, err := c.Cut(context.Background(), ClipRequest{
		From: start.Add(15 * time.Second), To: start.Add(18 * time.Second),
		Out: filepath.Join(t.TempDir(), "c.mp4"),
	})
	if err == nil {
		t.Fatal("a clip was cut from the segment ffmpeg is still writing; the file would be truncated at its tail")
	}
	if !strings.Contains(err.Error(), "still writing") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}
