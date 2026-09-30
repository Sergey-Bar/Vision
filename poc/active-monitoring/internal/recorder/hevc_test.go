package recorder

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestAnH265SourceProducesInteroperableSegments is the test for a claim that was
// previously only asserted.
//
// A public camera streams H.265 far more often than H.264, and ffmpeg's mp4 muxer
// writes HEVC as `hev1` by default. `hev1` in an MP4 is decodable by ffmpeg and
// by almost nothing else: QuickTime, Chrome and every phone's gallery refuse it.
// The evidence clip is this system's deliverable, so a clip nobody can open is not
// a deliverable.
//
// The `-tag:v hvc1` was added on the strength of that reasoning and nothing else.
// Reasoning about a container tag is not evidence, so this test encodes a real
// H.265 source, runs the REAL recorder, and reads the tag back off the file the
// recorder produced.
func TestAnH265SourceProducesInteroperableSegments(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2)")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatalf("ffprobe is a hard runtime dependency of this module (D2)")
	}

	work := t.TempDir()
	src := filepath.Join(work, "hevc.mp4")
	// A genuinely HEVC file. `-c:v libx265` with an hvc1-friendly pixel format.
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x180:rate=10",
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10", src).CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg has no HEVC encoder, so the tag cannot be exercised here: %v: %s", err, out)
	}

	// What the source itself carries, before the recorder touches it.
	before := codecTag(t, ffprobe, src)
	if before != "hev1" && before != "hvc1" {
		t.Fatalf("the fixture is not H.265 (tag %q), so the test would prove nothing", before)
	}

	rolling := filepath.Join(work, "rolling")
	rec, err := New(Options{
		FFmpeg:         ffmpeg,
		FFmpegArgs:     []string{"-stream_loop", "-1", "-i", src},
		RollingDir:     rolling,
		SegmentSeconds: 1,
		FPS:            2,
		Width:          160,
		Backpressure:   30 * time.Second,
		QueueDepth:     8,
		WatchInterval:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Start(context.Background()); err != nil {
		t.Fatalf("the recorder did not start on an H.265 source: %v", err)
	}
	defer rec.Stop(3 * time.Second)

	// Wait for a segment to close, so the file has a header and a tag.
	deadline := time.Now().Add(25 * time.Second)
	var first string
	for time.Now().Before(deadline) {
		if idx, _, err := ScanSegmentIndex(rolling); err == nil {
			for _, s := range idx.Segments() {
				first = s.Path
				break
			}
		}
		if first != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if first == "" {
		t.Fatalf("the recorder wrote no segment from an H.265 source. ffmpeg said:\n%s", rec.Stderr())
	}

	got := codecTag(t, ffprobe, first)
	if got != "hvc1" {
		t.Fatalf("the segment's tag is %q, want hvc1. `hev1` is decodable by ffmpeg and by almost nothing else, and the evidence clip is this system's deliverable.\n  segment: %s", got, first)
	}
}

// TestTheHvc1TagIsHarmlessForH264: the tag exists for HEVC, and an H.264 run is
// the common case and must not regress.
func TestTheHvc1TagIsHarmlessForH264(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is a hard runtime dependency of this module (D2)")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatalf("ffprobe is a hard runtime dependency of this module (D2)")
	}
	work := t.TempDir()
	src := filepath.Join(work, "h264.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=320x180:rate=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10", src).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not render the H.264 fixture: %v: %s", err, out)
	}
	rolling := filepath.Join(work, "rolling")
	rec, err := New(Options{
		FFmpeg: ffmpeg, FFmpegArgs: []string{"-stream_loop", "-1", "-i", src},
		RollingDir: rolling, SegmentSeconds: 1, FPS: 2, Width: 160,
		Backpressure: 30 * time.Second, QueueDepth: 8, WatchInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rec.Stop(3 * time.Second)
	deadline := time.Now().Add(25 * time.Second)
	var first string
	for time.Now().Before(deadline) && first == "" {
		if idx, _, err := ScanSegmentIndex(rolling); err == nil {
			for _, s := range idx.Segments() {
				first = s.Path
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if first == "" {
		t.Fatalf("the recorder wrote no segment from an H.264 source. ffmpeg said:\n%s", rec.Stderr())
	}
	if got := codecTag(t, ffprobe, first); got != "avc1" {
		t.Fatalf("the H.264 segment's tag is %q, want avc1: the hvc1 tag must not have disturbed the common case", got)
	}
}

func codecTag(t *testing.T, ffprobe, path string) string {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_tag_string", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe failed on %s: %v", path, err)
	}
	return trimSpaces(string(out))
}

func trimSpaces(s string) string {
	out := ""
	for _, r := range s {
		if r == '\n' || r == '\r' || r == ' ' {
			continue
		}
		out += string(r)
	}
	return out
}
