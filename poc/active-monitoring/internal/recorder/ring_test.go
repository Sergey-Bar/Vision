package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// modOf reads a segment's modification time, which is what the ring ages on.
func modOf(t *testing.T, dir string, n int) time.Time {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, SegmentFileName(n)))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// ageSegment writes one rolling file and stamps its mtime. The package's own
// writeSegment leaves the mtime alone, because the segment-index tests do not
// care about it; the ring cares about nothing else.
func ageSegment(t *testing.T, dir string, n int, start time.Time, size int) {
	t.Helper()
	p := writeSegment(t, dir, n, start, size)
	if err := os.Chtimes(p, start, start); err != nil {
		t.Fatal(err)
	}
}

// TestTheRecordingIsBoundedByAgeAndNotByAFillingDisk is the answer to "how much
// do you download from a live camera, and could this be endless".
//
// The rolling recording exists for ONE reason: when a crossing is detected the
// pixels around it are already gone, so the seconds BEFORE it have to have been
// kept. That is six seconds out of eighty-six thousand.
//
// The first version bounded the recording only by a disk ceiling, so a live
// camera was recorded at full rate until the disk filled and the sweep raced the
// fill. At the plan's own 4 Mbit/s that is 40 GB a day and 282 GB a week, kept
// in order to use six seconds of it.
//
// With an age bound the recording is a RING: bounded by design, constant for the
// length of the run, and the ceiling becomes a backstop rather than the only
// thing holding the line.
func TestTheRecordingIsBoundedByAgeAndNotByAFillingDisk(t *testing.T) {
	dir := t.TempDir()
	rolling := filepath.Join(dir, "rolling")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)

	// Two hours of one-minute segments, as a camera would have produced them.
	const segments = 120
	const eachSize = 1_500_000
	for i := 0; i < segments; i++ {
		ageSegment(t, rolling, i, base.Add(time.Duration(i)*time.Minute), eachSize)
	}
	idx, _, err := ScanSegmentIndex(rolling)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Len() != segments {
		t.Fatalf("the fixture has %d segments, want %d", idx.Len(), segments)
	}

	// now is one minute after the newest segment's own start, so the newest file
	// is inside the ring and the 118 before it are not.
	now := base.Add(segments * time.Minute)
	keep := 90 * time.Second
	cutoff := now.Add(-keep)
	t.Logf("newest mtime %s, cutoff %s, oldest %s",
		modOf(t, rolling, segments-1).Format(time.RFC3339), cutoff.Format(time.RFC3339),
		modOf(t, rolling, 0).Format(time.RFC3339))

	d := Disk{Root: dir, RollingDir: rolling, Ceiling: 1 << 60, Keep: keep, SegmentSeconds: 60,
		now: func() time.Time { return now }}

	res, err := d.AgeOut(now)
	if err != nil {
		t.Fatal(err)
	}
	// Every segment older than the cutoff goes. Exactly ONE survives: the newest, whose
	// mtime is half a minute before the cutoff's own clock and therefore inside
	// the ring. The file ffmpeg has open is that one.
	wantAged := segments - 1
	if res.AgedOut != wantAged {
		t.Fatalf("the ring aged out %d of %d segments, want %d", res.AgedOut, segments, wantAged)
	}
	if want := wantAged * eachSize; res.BytesFreed < int64(want)*95/100 {
		t.Fatalf("ageing freed %d bytes, which is not the ~%d MB the ring exists to discard", res.BytesFreed, want/1_000_000)
	}
	after, _, _ := ScanSegmentIndex(rolling)
	if after.Len() != 1 {
		t.Fatalf("%d segments survived the ring, want 1 (the newest, which ffmpeg has open)", after.Len())
	}

	// The arithmetic, written down, so the answer is a number and not a promise.
	const bitsPerSecond int64 = 4_000_000
	ringBytes := bitsPerSecond / 8 * int64(keep/time.Second)
	mb := func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }
	if ringBytes < 10_000_000 || ringBytes > 60_000_000 {
		t.Fatalf("a %s ring at 4 Mbit/s is %d bytes, which is not in the range the design assumes", keep, ringBytes)
	}
	t.Logf("a %s ring at 4 Mbit/s is %s and stays there; the same camera with only a disk ceiling is %s a day",
		keep, mb(ringBytes), mb(bitsPerSecond/8*86400))
}

// TestTheRingNeverDeletesTheSegmentBeingWritten: ffmpeg is writing into the
// newest segment right now, and deleting a file it has open is how a recorder
// stops recording without saying why.
func TestTheRingNeverDeletesTheSegmentBeingWritten(t *testing.T) {
	dir := t.TempDir()
	rolling := filepath.Join(dir, "rolling")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		ageSegment(t, rolling, i, base.Add(time.Duration(i)*time.Minute), 1000)
	}
	// "now" sits INSIDE the newest segment's own interval.
	now := base.Add(4*time.Minute + 5*time.Second)
	d := Disk{Root: dir, RollingDir: rolling, Ceiling: 1 << 60, Keep: 90 * time.Second, SegmentSeconds: 60,
		now: func() time.Time { return now }}
	if _, err := d.AgeOut(now); err != nil {
		t.Fatal(err)
	}
	idx, _, _ := ScanSegmentIndex(rolling)
	segs := idx.Segments()
	if len(segs) == 0 {
		t.Fatal("the ring deleted every segment, including the one ffmpeg has open")
	}
	if segs[len(segs)-1].Index != 4 {
		t.Fatalf("the newest segment is gone; the ring deleted the file ffmpeg is writing into")
	}
}

// TestARingShorterThanASegmentIsRefused: a 30 second ring over 60 second
// segments would delete the file being written, and ffmpeg would then stop
// recording with no error anywhere. This was found by the test above failing
// with exactly those numbers, which is the reason the rule is written down.
func TestARingShorterThanASegmentIsRefused(t *testing.T) {
	dir := t.TempDir()
	rolling := filepath.Join(dir, "rolling")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	ageSegment(t, rolling, 0, time.Now().Add(-time.Hour), 1000)
	d := Disk{Root: dir, RollingDir: rolling, Ceiling: 1 << 60, Keep: 30 * time.Second, SegmentSeconds: 60}
	_, err := d.AgeOut(time.Now())
	if err == nil {
		t.Fatal("a 30s ring over 60s segments was accepted; it would delete the file ffmpeg is writing into")
	}
	if !strings.Contains(err.Error(), "writing into") {
		t.Fatalf("the refusal does not say what breaks: %v", err)
	}
}

// TestTheRingStillNeverTouchesEvidence: the ring is about the ROLLING recording.
// Stored evidence is swept by the evidence store, against a retention period the
// operator declared — and a clip is the one thing the run exists to keep.
func TestTheRingStillNeverTouchesEvidence(t *testing.T) {
	dir := t.TempDir()
	rolling := filepath.Join(dir, "rolling")
	ev := filepath.Join(dir, "evidence", "ab")
	for _, d := range []string{rolling, ev} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	ageSegment(t, rolling, 0, base, 1000)

	clip := filepath.Join(ev, "c0ffee.mp4")
	if err := os.WriteFile(clip, []byte("an incident"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := base.Add(time.Hour)
	d := Disk{Root: dir, RollingDir: rolling, Ceiling: 1 << 60, Keep: 90 * time.Second, SegmentSeconds: 60,
		now: func() time.Time { return now }}
	if _, err := d.AgeOut(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Fatalf("the ring deleted a stored clip (%v). The ring is about the rolling recording; a clip is the one thing the run exists to keep", err)
	}
}

// TestNoAgeRuleMeansTheLoopBehaviourIsUnchanged: a loop's rolling recording is
// the working set and a ceiling is the right bound for it. The ring is a LIVE
// camera's rule, not everyone's.
func TestNoAgeRuleMeansTheLoopBehaviourIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	rolling := filepath.Join(dir, "rolling")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	for i := 0; i < 20; i++ {
		ageSegment(t, rolling, i, base.Add(time.Duration(i)*time.Minute), 1000)
	}
	d := Disk{Root: dir, RollingDir: rolling, Ceiling: 5000}
	res, err := d.AgeOut(base.Add(10 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgedOut != 0 {
		t.Fatalf("with no age rule, %d segments were aged out; the loop's bound is the ceiling, as it always was", res.AgedOut)
	}
	idx, _, _ := ScanSegmentIndex(rolling)
	if idx.Len() != 20 {
		t.Fatalf("%d segments survived; an age rule appeared where none was configured", idx.Len())
	}
}
