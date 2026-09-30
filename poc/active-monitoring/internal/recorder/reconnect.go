package recorder

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// reconnectWatcher watches ffmpeg's own diagnostics for the lines it prints when
// it re-establishes a broken live source.
//
// # WHY THIS EXISTS
//
// The plan forbids an in-run reconnect, because a reconnect that is not counted
// turns a dropout into a success in the report. That reasoning is right, and it
// also means a seven-day run cannot survive a camera that reboots, loses WiFi or
// takes a new DHCP lease — which a PUBLIC camera does, routinely, and which the
// run would end on the first time.
//
// So the reconnect stays, and it stops being silent. ffmpeg announces every
// reconnection on its diagnostics; each announcement becomes a recorded
// `stream_gap` fault with the moment it happened, and the run continues.
//
// That is the whole trade, stated plainly: a public camera cannot be measured by
// a process that dies when the camera blinks, and it cannot be measured by a
// process that pretends the camera did not blink. So it is measured by a process
// that records every blink and reports the count, and AC1 fails on it — zero
// tolerance, as the criterion says — which is the honest outcome: the camera's
// uptime becomes a measured property of this POC rather than an assumption.
var (
	// ffmpeg prints "Reconnecting to rtsp://..." and, for a stream that dropped
	// mid-flight, "Reconnect index N requested" and a live connection being torn
	// down. All three mean the same thing to a reader: the source was not
	// continuous.
	reconnectRe = regexp.MustCompile(`(?i)\b(reconnect(?:ing|ed)?|reconnect_request|Connection .* lost|Server returned .*(401|403|404|503))\b`)
	// ffmpeg prints a repeating live-update line; it is noise and it would drown
	// the reconnects.
	liveUpdateRe = regexp.MustCompile(`(?i)frame=\s*\d+.*time=`)
)

// reconnectWatcher is an io.Writer that scans ffmpeg's diagnostics and calls back
// once per reconnect. It keeps the last lines as well, so `Stderr` still works.
type reconnectWatcher struct {
	mu      sync.Mutex
	lines   []string
	n       int
	reconns int
	// lastCounted is when a reconnect was last COUNTED, not when a line was last
	// seen. The difference is the whole burst rule.
	lastCounted time.Time
	onReconn    func(at time.Time, line string, count int)
	clock       func() time.Time
}

const reconnectTailLines = 64

func newReconnectWatcher(logf func(string, ...any)) *reconnectWatcher {
	w := &reconnectWatcher{n: reconnectTailLines, clock: time.Now}
	w.onReconn = func(at time.Time, line string, count int) {
		if logf != nil {
			logf("the source was reconnected (%d so far): %s", count, line)
		}
	}
	return w
}

// Write implements io.Writer.
// Write implements io.Writer.
//
// The callback is invoked AFTER the lock is released, never inside it. The
// first version called it while holding the lock and never unlocked: the
// process's stderr copier calls this on every line ffmpeg writes, and the soak
// loop calls Reconnects() on a two-second ticker, so whichever arrived first
// blocked the other forever and the run hung with no error and no report. A
// counter that can deadlock is worse than no counter at all.
func (w *reconnectWatcher) Write(p []byte) (int, error) {
	type event struct {
		at    time.Time
		line  string
		count int
	}
	var events []event

	now := w.clock()
	w.mu.Lock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w.lines = append(w.lines, line)
		if len(w.lines) > w.n {
			w.lines = w.lines[len(w.lines)-w.n:]
		}
		if liveUpdateRe.MatchString(line) || !reconnectRe.MatchString(line) {
			continue
		}
		// ffmpeg prints three lines for one dropout, often in a single write and
		// sometimes spread over several. Counting each line would report one
		// camera blip as four dropouts, and the count would be a function of
		// ffmpeg's log wording rather than of the camera.
		//
		// The comparison is against the last COUNTED event, not the last write,
		// so the three lines of one blip collapse whether they arrive together
		// or separately.
		if !w.lastCounted.IsZero() && now.Sub(w.lastCounted) < 2*time.Second {
			continue
		}
		w.reconns++
		w.lastCounted = now
		events = append(events, event{at: now, line: line, count: w.reconns})
	}
	cb := w.onReconn
	w.mu.Unlock()

	for _, e := range events {
		if cb != nil {
			cb(e.at, e.line, e.count)
		}
	}
	return len(p), nil
}

// String returns the tail of ffmpeg's diagnostics.
func (w *reconnectWatcher) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

// Reconnects is how many times the source was re-established. It is a COUNT and
// not a flag, because a single number of reconnects is the measurement AC1 needs
// and a boolean would hide the difference between one blip and two hundred.
func (w *reconnectWatcher) Reconnects() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reconns
}

var _ = fmt.Sprintf
