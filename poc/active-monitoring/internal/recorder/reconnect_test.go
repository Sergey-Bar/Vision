package recorder

import (
	"strings"
	"testing"
	"time"
)

// realffmpegReconnect is what ffmpeg 8 actually prints when a live source drops
// and comes back. The watcher reads exactly this and nothing else.
const realffmpegReconnect = `[rtsp @ 000001a2b3c4d5e6] method: TEARDOWN
[rtsp @ 000001a2b3c4d5e6] Reconnecting to rtsp://10.0.0.9/stream
[rtsp @ 000001a2b3c4d5e6] Reconnect index 1 requested for stream 0
`

// newTestWatcher builds a watcher with a clock the test drives, so "a second
// dropout five minutes later" and "the next line of the same blip" are
// distinguishable without sleeping.
func newTestWatcher(step time.Duration) (*reconnectWatcher, *int) {
	tick := 0
	w := &reconnectWatcher{n: 8}
	w.clock = func() time.Time {
		tick++
		return time.Unix(1_700_000_000, 0).Add(time.Duration(tick) * step)
	}
	count := 0
	w.onReconn = func(_ time.Time, _ string, n int) { count = n }
	return w, &count
}

// TestAReconnectIsCounted is the point of the whole arrangement.
//
// A public camera reboots. If the run dies, it measures nothing. If the run
// ignores it, it measures a camera with better uptime than any real camera has.
// So the reconnect is counted: this is the number AC1 fails on.
func TestAReconnectIsCounted(t *testing.T) {
	w, seen := newTestWatcher(time.Hour)
	_, _ = w.Write([]byte(realffmpegReconnect))
	if w.Reconnects() != 1 {
		t.Fatalf("one camera drop was counted as %d reconnects", w.Reconnects())
	}
	if *seen != 1 {
		t.Fatalf("the callback fired %d times, want 1: the fault row is written per reconnect", *seen)
	}
}

// TestOneBlipIsNotThreeDropouts: ffmpeg prints three lines for one dropout. A
// count that is a function of ffmpeg's log wording is not a measurement of the
// camera.
func TestOneBlipIsNotThreeDropouts(t *testing.T) {
	w, _ := newTestWatcher(time.Hour)
	// All three lines, written together as ffmpeg writes them: one write.
	_, _ = w.Write([]byte(realffmpegReconnect))
	if w.Reconnects() != 1 {
		t.Fatalf("three log lines for one dropout were counted as %d", w.Reconnects())
	}
	// And a second, later blip is a second dropout.
	_, _ = w.Write([]byte(realffmpegReconnect))
	if w.Reconnects() != 2 {
		t.Fatalf("two separate blips were counted as %d", w.Reconnects())
	}
}

// TestABurstOfLinesInsideTwoSecondsIsOneDropout: the same protection, when the
// lines arrive in separate writes.
func TestABurstOfLinesInsideTwoSecondsIsOneDropout(t *testing.T) {
	w, _ := newTestWatcher(100 * time.Millisecond)
	for _, line := range []string{
		"[rtsp @ 1] method: TEARDOWN",
		"[rtsp @ 1] Reconnecting to rtsp://cam/stream",
		"[rtsp @ 1] Reconnect index 1 requested",
	} {
		_, _ = w.Write([]byte(line + "\n"))
	}
	if w.Reconnects() != 1 {
		t.Fatalf("a burst 100ms apart was counted as %d dropouts", w.Reconnects())
	}
}

// TestTheProgressLineIsNotAReconnect: ffmpeg's repeating live-update line must
// not be counted, or a healthy run reports thousands of dropouts.
func TestTheProgressLineIsNotAReconnect(t *testing.T) {
	w, _ := newTestWatcher(time.Hour)
	_, _ = w.Write([]byte(
		"frame=  120 fps= 30 q=28.0 size=     256KiB time=00:00:04.00 bitrate= 524.3kbits/s speed=1.00x\n" +
			"frame=  240 fps= 30 q=28.0 size=     512KiB time=00:00:08.00 bitrate= 524.3kbits/s speed=1.00x\n"))
	if w.Reconnects() != 0 {
		t.Fatalf("ffmpeg's progress line was counted as %d reconnects", w.Reconnects())
	}
}

// TestAnOrdinaryWarningIsNotAReconnect: the matcher must be narrow, or any
// warning that mentions a word becomes a dropout.
func TestAnOrdinaryWarningIsNotAReconnect(t *testing.T) {
	w, _ := newTestWatcher(time.Hour)
	_, _ = w.Write([]byte(
		"[h264 @ 1] SPS NALU: Nal unit size differs from frame\n" +
			"[tcp @ 1] Read an error requesting 19 bytes\n"))
	if w.Reconnects() != 0 {
		t.Fatalf("an ordinary warning was counted as a reconnect: %q", w.String())
	}
}

// TestA401IsAReconnectClass: a camera that answers 401 has not re-established
// anything, and the reader needs to know it was an authentication failure rather
// than a blip.
func TestAnAuthFailureIsReportedAsAReconnect(t *testing.T) {
	w, _ := newTestWatcher(time.Hour)
	_, _ = w.Write([]byte("[rtsp @ 1] Server returned 401 (Unauthorized)\n"))
	if w.Reconnects() != 1 {
		t.Fatalf("a 401 from the camera was not noticed; the operator would see a silent stall instead of an auth failure")
	}
}

// TestTheTailIsStillAvailable: Stderr() is what an operator reads when the
// recorder died, so the watcher must not replace it with a bare counter.
func TestTheTailIsStillAvailable(t *testing.T) {
	w, _ := newTestWatcher(time.Hour)
	_, _ = w.Write([]byte("first line\n" +
		"[rtsp @ 1] Reconnecting to rtsp://cam/stream\n" +
		"last line\n"))
	got := w.String()
	for _, want := range []string{"first line", "Reconnecting", "last line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the diagnostics tail is missing %q: %q", want, got)
		}
	}
}
