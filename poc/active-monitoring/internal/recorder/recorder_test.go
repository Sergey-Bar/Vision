package recorder

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// requireFFmpeg is deliberate. D2 makes ffmpeg a hard runtime dependency of this
// module, so a machine without it cannot run the pipeline and cannot run its
// tests. Skipping here would produce a green run that measured nothing, which
// is the exact failure mode this project treats as a defect.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is not on PATH: D2 makes it a hard runtime dependency of this module, so this test is not skipped, it fails. Install ffmpeg and run the POC's scripts/verify.ps1")
	}
	return path
}

// makeClip renders a short synthetic clip with ffmpeg. The material is
// synthetic by construction, which is what provenance: synthetic means in
// labels.json.
func makeClip(t *testing.T, path string, seconds int) {
	t.Helper()
	ff := requireFFmpeg(t)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=640x360:rate=25", seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "25", path,
	}
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not render the test clip: %v: %s", err, out)
	}
}

// --- MJPEG splitter ---------------------------------------------------------

func jpegBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSplitterReassemblesAFrameSplitAcrossChunks(t *testing.T) {
	frame := jpegBytes(t, color.RGBA{R: 200, G: 10, B: 10, A: 255})
	var sp mjpegSplitter
	var got [][]byte
	// One byte at a time is the worst case a pipe can produce, and it is the
	// case that loses a frame if the splitter is not streaming.
	for i := 0; i < len(frame); i++ {
		frames, _ := sp.push(frame[i : i+1])
		got = append(got, frames...)
	}
	if len(got) != 1 {
		t.Fatalf("byte-at-a-time delivery produced %d frames, want 1", len(got))
	}
	if !bytes.Equal(got[0], frame) {
		t.Fatalf("reassembled %d bytes, want %d identical bytes", len(got[0]), len(frame))
	}
}

func TestSplitterEmitsSeveralFramesFromOneChunk(t *testing.T) {
	a := jpegBytes(t, color.RGBA{R: 255, A: 255})
	b := jpegBytes(t, color.RGBA{B: 255, A: 255})
	var sp mjpegSplitter
	frames, _ := sp.push(append(append([]byte{}, a...), b...))
	if len(frames) != 2 {
		t.Fatalf("got %d frames from a two-frame chunk, want 2", len(frames))
	}
}

func TestSplitterLeavesAPartialFramePendingRatherThanEmittingIt(t *testing.T) {
	frame := jpegBytes(t, color.RGBA{G: 255, A: 255})
	var sp mjpegSplitter
	frames, _ := sp.push(frame[:len(frame)-3])
	if len(frames) != 0 {
		t.Fatalf("a truncated frame was emitted as a frame: %d emitted", len(frames))
	}
	if !sp.pending() {
		t.Fatal("the partial frame was not reported as pending; the caller needs to know a frame is incomplete rather than assume the pipe is idle")
	}
}

func TestSplitterRecoversAfterGarbage(t *testing.T) {
	frame := jpegBytes(t, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var sp mjpegSplitter
	frames, _ := sp.push([]byte{0x00, 0x01, 0x02, 0x03})
	if len(frames) != 0 {
		t.Fatalf("garbage produced %d frames", len(frames))
	}
	frames, _ = sp.push(frame)
	if len(frames) != 1 {
		t.Fatalf("the splitter did not resynchronise after garbage: %d frames", len(frames))
	}
}

func TestDecoderProducesPlainRGB(t *testing.T) {
	src := jpegBytes(t, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	w, h, rgb, err := stdlibDecoder{}.Decode(src)
	if err != nil {
		t.Fatal(err)
	}
	if w != 8 || h != 8 {
		t.Fatalf("dimensions = %dx%d", w, h)
	}
	if len(rgb) != w*h*3 {
		t.Fatalf("rgb length = %d, want %d", len(rgb), w*h*3)
	}
	// The sidecar gets raw RGB, so the red channel must actually be red. JPEG
	// is lossy, so this is a closeness check and not an equality check.
	near := func(got, want uint8) bool {
		d := int(got) - int(want)
		return d < 12 && d > -12
	}
	if !near(rgb[0], 200) || !near(rgb[1], 100) || !near(rgb[2], 50) {
		t.Fatalf("colour round-trip failed: got rgb[%d,%d,%d] for a solid (200,100,50) source", rgb[0], rgb[1], rgb[2])
	}
}

func TestDecoderRejectsGarbage(t *testing.T) {
	if _, _, _, err := (stdlibDecoder{}).Decode([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("garbage was decoded as an image")
	}
}

// --- backpressure -----------------------------------------------------------

// TestBoundedQueueBlocksAndTimesTheBlock is the R6 mechanism: the queue is
// bounded, the producer blocks, and the block is measured. A test that only
// checked "it does not grow forever" would pass with silent frame dropping,
// which is the thing that must not happen.
func TestBoundedQueueBlocksAndTimesTheBlock(t *testing.T) {
	q := newBoundedQueue(2)
	for i := 0; i < 2; i++ {
		if err := q.push(i); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = q.push(99)
		done <- time.Since(start)
	}()
	select {
	case d := <-done:
		t.Fatalf("the producer was not blocked on a full queue; it returned after %s", d)
	case <-time.After(150 * time.Millisecond):
	}
	if _, ok := q.pop(); !ok {
		t.Fatal("pop failed")
	}
	select {
	case d := <-done:
		if d < 100*time.Millisecond {
			t.Fatalf("the block lasted %s, which is shorter than the time it was actually blocked", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the producer never woke after a pop")
	}
	waited := q.maxWait()
	if waited < 100*time.Millisecond {
		t.Fatalf("recorded max wait = %s, want at least the 150ms the producer was blocked", waited)
	}
	// The counter resets, so one fault reports one number and not a running
	// total.
	if again := q.maxWait(); again != 0 {
		t.Fatalf("maxWait did not reset: %s", again)
	}
}

func TestBoundedQueueUnblocksEveryoneOnClose(t *testing.T) {
	q := newBoundedQueue(1)
	_ = q.push(1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = q.push(2) }()
	go func() { defer wg.Done(); _, _ = q.pop() }()
	time.Sleep(50 * time.Millisecond)
	q.close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not unblock a waiting producer")
	}
}

// writeSegment creates a rolling file the way ffmpeg's segment muxer does. The
// start argument is the measured wall clock of the segment's first frame, which
// the tests supply because the reader is what measures it in a real run.
func writeSegment(t *testing.T, dir string, n int, start time.Time, size int) string {
	t.Helper()
	path := filepath.Join(dir, SegmentFileName(n))
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- segment index ----------------------------------------------------------

// obs builds a measurement for segment n, as the reader would record it.
func obs(n int, start time.Time) segmentObservation {
	return segmentObservation{index: n, observedAt: start.Add(50 * time.Millisecond), frameClock: start}
}

func buildIndex(t *testing.T, dir string, starts ...time.Time) *SegmentIndex {
	t.Helper()
	for n, at := range starts {
		writeSegment(t, dir, n, at, 1000)
	}
	idx, _, err := ScanSegmentIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	observations := make([]segmentObservation, 0, len(starts))
	for n, at := range starts {
		observations = append(observations, obs(n, at))
	}
	idx.ApplyObservations(observations)
	return idx
}

func TestParseSegmentNameRoundTrips(t *testing.T) {
	if n, err := ParseSegmentName(SegmentFileName(42)); err != nil || n != 42 {
		t.Fatalf("ParseSegmentName(%s) = %d, %v", SegmentFileName(42), n, err)
	}
}

func TestParseSegmentNameRejectsAFileThatIsNotASegment(t *testing.T) {
	// A zero value with no error would let a stray file silently become a hole
	// or a phantom of coverage.
	for _, name := range []string{"notes.txt", "seg-20060102T150405.mp4", "seg-.mp4", "seg-abc.mp4"} {
		if _, err := ParseSegmentName(name); err == nil {
			t.Fatalf("%q was accepted as a segment", name)
		}
	}
}

func TestFindMapsAnInstantOntoItsSegment(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	idx := buildIndex(t, dir, base, base.Add(60*time.Second), base.Add(120*time.Second))

	seg, err := idx.Find(base.Add(30 * time.Second))
	if err != nil {
		t.Fatalf("an instant 30s into the first segment was not found: %v", err)
	}
	if seg.Index != 0 {
		t.Fatalf("mapped to segment %d, want 0", seg.Index)
	}
	// Half-open intervals: the boundary instant belongs to the later segment,
	// and exactly one segment ever claims a frame.
	seg, err = idx.Find(base.Add(60 * time.Second))
	if err != nil {
		t.Fatalf("the boundary instant was not found: %v", err)
	}
	if seg.Index != 1 {
		t.Fatalf("the boundary instant was claimed by segment %d, want the later one (1)", seg.Index)
	}
}

func TestTheNewestSegmentHasNoCoverageUntilItsSuccessorIsSeen(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	idx := buildIndex(t, dir, base, base.Add(60*time.Second))
	if got := idx.Complete(); got != 1 {
		t.Fatalf("complete segments = %d, want 1: the newest segment has no known end", got)
	}
	// A clip that reaches into the segment ffmpeg is still writing would be
	// truncated, so it is refused by name.
	_, err := idx.Find(base.Add(90 * time.Second))
	if err == nil || !strings.Contains(err.Error(), "still writing") {
		t.Fatalf("want a still-writing refusal, got %v", err)
	}
}

func TestFindInASweptHoleSaysTheRecordingIsGone(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	// Counter 0 then counter 4: segments 1..3 were removed by the disk budget.
	writeSegment(t, dir, 0, base, 1000)
	writeSegment(t, dir, 4, base.Add(240*time.Second), 1000)
	idx, _, _ := ScanSegmentIndex(dir)
	idx.ApplyObservations([]segmentObservation{obs(0, base), obs(4, base.Add(240*time.Second))})
	_, err := idx.Find(base.Add(120 * time.Second))
	if err == nil {
		t.Fatal("an instant inside a swept hole was mapped to a segment")
	}
	if !strings.Contains(err.Error(), "swept") {
		t.Fatalf("the error does not say the recording is gone: %v", err)
	}
}

func TestFindBeforeTheFirstSegmentIsNotCalledAGap(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	idx := buildIndex(t, dir, base, base.Add(60*time.Second))
	_, err := idx.Find(base.Add(-10 * time.Second))
	if err == nil || !strings.Contains(err.Error(), "before the first recorded frame") {
		t.Fatalf("want a 'before the first recorded frame' reason, got %v", err)
	}
}

// TestGapsIgnoreConsecutiveSegments: coverage is a partition built from
// measured frame clocks, so a long segment is a long segment, not a dropout.
// Reporting it as a gap would make AC1 a function of the segment length.
func TestGapsIgnoreConsecutiveSegments(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	idx := buildIndex(t, dir, base, base.Add(300*time.Second), base.Add(360*time.Second))
	if gaps := idx.Gaps(); len(gaps) != 0 {
		t.Fatalf("consecutive segments were reported as gaps: %+v", gaps)
	}
	if idx.LongestGap() != 0 {
		t.Fatalf("longest gap = %s, want 0", idx.LongestGap())
	}
	if !idx.Segments()[0].Complete || idx.Segments()[0].End != idx.Segments()[1].Start {
		t.Fatalf("segment 0 does not end where segment 1 begins: %+v", idx.Segments()[0])
	}
}

func TestGapsAreReportedWithSpanAndReason(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	writeSegment(t, dir, 0, base, 1000)
	writeSegment(t, dir, 3, base.Add(300*time.Second), 1000)
	idx, _, _ := ScanSegmentIndex(dir)
	idx.ApplyObservations([]segmentObservation{obs(0, base), obs(3, base.Add(300*time.Second))})
	gaps := idx.Gaps()
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want 1", gaps)
	}
	if gaps[0].SpanSec != 300 {
		t.Fatalf("gap span = %.1fs, want 300s", gaps[0].SpanSec)
	}
	if gaps[0].Reason == "" {
		t.Fatal("a gap with no reason")
	}
	if idx.LongestGap() != 300*time.Second {
		t.Fatalf("longest gap = %s, want 5m", idx.LongestGap())
	}
}

func TestZeroLengthSegmentIsNotCoverage(t *testing.T) {
	// ffmpeg creates the file before it writes a byte. Treating it as coverage
	// would close a gap that is still open.
	dir := t.TempDir()
	writeSegment(t, dir, 0, time.Now(), 0)
	idx, _, _ := ScanSegmentIndex(dir)
	if idx.Len() != 0 {
		t.Fatalf("a zero-length segment was counted: %d", idx.Len())
	}
}

func TestNonSegmentFilesAreReportedNotSkippedSilently(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	_, nonSegments, err := ScanSegmentIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonSegments) != 1 || nonSegments[0] != "notes.txt" {
		t.Fatalf("nonSegments = %v", nonSegments)
	}
}

func TestUnmeasuredSegmentIsGivenNoCoverage(t *testing.T) {
	// A file the reader never saw has no wall clock, and crediting it with an
	// interval would be inventing coverage.
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	writeSegment(t, dir, 0, base, 1000)
	writeSegment(t, dir, 1, base.Add(60*time.Second), 1000)
	idx, _, _ := ScanSegmentIndex(dir)
	idx.ApplyObservations([]segmentObservation{obs(0, base)})
	if idx.Segments()[1].Complete {
		t.Fatal("a segment with no measured start was marked complete")
	}
	if !idx.Segments()[1].End.IsZero() {
		t.Fatal("a segment with no measured start was given an end time")
	}
}

// --- disk budget ------------------------------------------------------------

// TestSweepDeletesTheOldestSegmentFirst.
func TestSweepDeletesTheOldestSegmentFirst(t *testing.T) {
	root := t.TempDir()
	rolling := filepath.Join(root, "rolling")
	os.MkdirAll(rolling, 0o755)
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		writeSegment(t, rolling, i, base.Add(time.Duration(i)*60*time.Second), 1000)
	}
	d := Disk{Root: root, RollingDir: rolling, Ceiling: 2500}
	res, err := d.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 3 {
		t.Fatalf("deleted %d segments, want 3 to get 5x1000 under 2500: %v", len(res.Deleted), res.Deleted)
	}
	// The lowest counter went first, which is the oldest in time.
	if !strings.HasSuffix(res.Deleted[0], SegmentFileName(0)) {
		t.Fatalf("first deleted was %s, want the oldest (%s)", res.Deleted[0], SegmentFileName(0))
	}
	if res.InUse > d.Ceiling {
		t.Fatalf("still over the ceiling: %d > %d", res.InUse, d.Ceiling)
	}
}

// TestSweepStopsWithADiskBudgetFaultRatherThanDeletingEvidence: the ceiling is
// measured on the whole data root, and evidence is never deleted to meet it. So
// a run that filled up while keeping its evidence is told to stop, and the
// report can say the space went to evidence.
func TestSweepStopsWithADiskBudgetFaultRatherThanDeletingEvidence(t *testing.T) {
	root := t.TempDir()
	rolling := filepath.Join(root, "rolling")
	ev := filepath.Join(root, "evidence", "ab")
	os.MkdirAll(rolling, 0o755)
	os.MkdirAll(ev, 0o755)
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	writeSegment(t, rolling, 0, base, 4000)
	// The evidence is the pressure. Deleting it is the one move the plan
	// forbids, so the sweep must stop with a named fault instead.
	clip := filepath.Join(ev, "c0ffee.mp4")
	os.WriteFile(clip, make([]byte, 4000), 0o644)

	d := Disk{Root: root, RollingDir: rolling, Ceiling: 1000}
	_, err := d.Sweep()
	if err == nil {
		t.Fatal("a run that cannot fit while keeping its evidence was allowed to continue")
	}
	var de *ErrDiskBudget
	if !asDiskBudget(err, &de) {
		t.Fatalf("want *ErrDiskBudget, got %T: %v", err, err)
	}
	if !strings.Contains(de.Error(), "stored evidence is never deleted") {
		t.Fatalf("the refusal does not say why it stopped: %s", de.Error())
	}
	if de.EvidenceUse != 4000 {
		t.Fatalf("evidence use = %d, want 4000 so the report can say where the space went", de.EvidenceUse)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Fatal("the stored clip was deleted to satisfy the disk budget")
	}
	if de.Deleted != 1 {
		t.Fatalf("deleted %d rolling segments, want the 1 it was allowed to delete", de.Deleted)
	}
}

func TestSweepRefusesToDeleteAnythingUnderTheEvidenceRoot(t *testing.T) {
	// Pointing the rolling directory at a path that lives under the evidence
	// root is the unreachable case today. The guard exists so that a future
	// change that mixes the roots fails loudly instead of deleting an incident.
	root := t.TempDir()
	ev := filepath.Join(root, "evidence", "ab")
	os.MkdirAll(ev, 0o755)
	rolling := filepath.Join(ev, "rolling")
	os.MkdirAll(rolling, 0o755)
	writeSegment(t, rolling, 0, time.Now(), 4000)
	d := Disk{Root: root, RollingDir: rolling, Ceiling: 1000}
	_, err := d.Sweep()
	if err == nil || !strings.Contains(err.Error(), "under the evidence root") {
		t.Fatalf("want a refusal to sweep evidence, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(rolling, SegmentFileName(0))); statErr != nil {
		t.Fatal("the evidence file was deleted")
	}
}

func asDiskBudget(err error, dst **ErrDiskBudget) bool {
	e, ok := err.(*ErrDiskBudget)
	if ok {
		*dst = e
	}
	return ok
}

func TestCheckRefusesInsufficientHeadroomByName(t *testing.T) {
	// 1 Mbit/s for 7 days is about 7.5 GB; 1.5x headroom wants ~11.3 GB.
	budget := int64(1_000_000 / 8 * 86400 * 7)
	if err := Check(budget*2, budget, 1.5, 7); err != nil {
		t.Fatalf("2x headroom was refused: %v", err)
	}
	err := Check(budget, budget, 1.5, 7)
	if err == nil {
		t.Fatal("a run that would fill the disk was allowed to start")
	}
	if !strings.Contains(err.Error(), "disk_budget_exceeded") {
		t.Fatalf("the refusal does not name the consequence: %v", err)
	}
}

func TestCheckReadsRealFreeSpaceOnThisMachine(t *testing.T) {
	dir := t.TempDir()
	free, err := Free(dir)
	if err != nil {
		t.Fatalf("free space is not readable on this filesystem: %v", err)
	}
	if free <= 0 {
		t.Fatalf("free space on the temp filesystem = %d; preflight would then refuse every run for a reason that is not true", free)
	}
}

// --- integration: the real ffmpeg ------------------------------------------

func startRecorder(t *testing.T, clip string, seconds int) (*Recorder, string) {
	t.Helper()
	ff := requireFFmpeg(t)
	rolling := filepath.Join(t.TempDir(), "rolling")
	rec, err := New(Options{
		FFmpeg:         ff,
		FFmpegArgs:     []string{"-re", "-stream_loop", "-1", "-i", clip},
		RollingDir:     rolling,
		SegmentSeconds: seconds,
		FPS:            5,
		Width:          320,
		Backpressure:   2 * time.Second,
		QueueDepth:     8,
		WatchInterval:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Start(context.Background()); err != nil {
		t.Fatalf("the single ffmpeg did not start: %v", err)
	}
	t.Cleanup(func() { _ = rec.Stop(3 * time.Second) })
	return rec, rolling
}

// TestSegmentWallClockComesFromTheFrameReader is the anti-drift test. Each
// segment's coverage must be the wall clock of its first frame, so a clip cut
// for an instant lands inside the segment that actually contains those frames.
func TestSegmentWallClockComesFromTheFrameReader(t *testing.T) {
	clip := filepath.Join(t.TempDir(), "src.mp4")
	makeClip(t, clip, 4)
	rec, _ := startRecorder(t, clip, 2)

	limit := time.Now().Add(20 * time.Second)
	var idx *SegmentIndex
	for time.Now().Before(limit) {
		rec.Next()
		i, err := rec.Index()
		if err == nil && i.Complete() >= 2 {
			idx = i
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if idx == nil {
		t.Fatal("two complete segments never appeared")
	}
	segs := idx.Segments()
	for _, s := range segs {
		if s.Complete && (s.Start.IsZero() || s.End.IsZero()) {
			t.Fatalf("a complete segment has no measured coverage: %+v", s)
		}
	}
	// Coverage must be a monotonic partition of the timeline, derived from the
	// reader's clock and not from an assumed segment length.
	for n := 1; n < idx.Complete(); n++ {
		prev, cur := segs[n-1], segs[n]
		if !cur.Start.After(prev.Start) {
			t.Fatalf("segment %d starts at or before its predecessor: %s then %s", cur.Index, prev.Start, cur.Start)
		}
		if !cur.Start.Equal(prev.End) {
			t.Fatalf("segment %d starts at %s but segment %d ends at %s; coverage must be a partition of the timeline", cur.Index, cur.Start, prev.Index, prev.End)
		}
	}
	// The boundaries must be close to the requested segment length, which is
	// what proves the wall clock came from the real rotation and not from
	// arithmetic on an assumed constant.
	span := segs[idx.Complete()-1].End.Sub(segs[0].Start)
	if span < 2*time.Second || span > 12*time.Second {
		t.Fatalf("two 2-second segments cover %s of measured wall clock, which is not a real rotation", span)
	}
}

func probeDuration(t *testing.T, path string) float64 {
	t.Helper()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe is required to verify a cut clip")
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe failed on %s: %v", path, err)
	}
	var d float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d)
	return d
}

func TestClipOutsideTheRecordingIsRefusedWithAReason(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	writeSegment(t, dir, 0, base, 1000)
	writeSegment(t, dir, 1, base.Add(60*time.Second), 1000)
	c := Clipper{FFmpeg: requireFFmpeg(t), RollingDir: dir}
	_, err := c.Cut(context.Background(), ClipRequest{From: base.Add(-time.Hour), To: base.Add(-59 * time.Minute), Out: filepath.Join(t.TempDir(), "c.mp4")})
	if err == nil {
		t.Fatal("a clip before the recording was produced")
	}
	if !strings.Contains(err.Error(), "before the first recorded frame") {
		t.Fatalf("reason does not say the event is outside the recording: %v", err)
	}
}

func TestEmptyWindowIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeSegment(t, dir, 0, time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local), 1000)
	c := Clipper{FFmpeg: requireFFmpeg(t), RollingDir: dir}
	at := time.Date(2026, 9, 30, 10, 0, 10, 0, time.Local)
	_, err := c.Cut(context.Background(), ClipRequest{From: at, To: at, Out: filepath.Join(t.TempDir(), "c.mp4")})
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("an empty window was accepted: %v", err)
	}
}

func TestNoCompleteSegmentIsRefusedNotSilentlyEmpty(t *testing.T) {
	dir := t.TempDir()
	c := Clipper{FFmpeg: requireFFmpeg(t), RollingDir: dir}
	_, err := c.Cut(context.Background(), ClipRequest{
		From: time.Now().Add(-time.Second), To: time.Now(), Out: filepath.Join(t.TempDir(), "c.mp4"),
	})
	if err == nil || !strings.Contains(err.Error(), "no complete segment") {
		t.Fatalf("want a no-segment refusal, got %v", err)
	}
}

func TestOptionsRefuseUnusableValues(t *testing.T) {
	if _, err := New(Options{SegmentSeconds: 0, FPS: 5}); err == nil {
		t.Fatal("a zero segment length was accepted")
	}
	if _, err := New(Options{SegmentSeconds: 60, FPS: 0}); err == nil {
		t.Fatal("a zero pipe rate was accepted")
	}
}
