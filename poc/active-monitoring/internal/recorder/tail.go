package recorder

import (
	"strings"
	"sync"
)

// tailBuffer keeps the last n lines of a child process's diagnostics. ffmpeg
// writes warnings to stderr continuously; keeping all of it would be an
// unbounded log, and keeping none of it would make a silent failure
// undebuggable.
type tailBuffer struct {
	mu    sync.Mutex
	lines []string
	n     int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			t.lines = append(t.lines, l)
		}
	}
	if len(t.lines) > t.n {
		t.lines = t.lines[len(t.lines)-t.n:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
