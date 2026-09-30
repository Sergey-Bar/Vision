package soak

import (
	"runtime"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
)

// TestRSSIsMeasuredNotGuessedOnThisPlatform: AC5's memory clause compares a
// measured peak against a declared ceiling. A reader that returns 0 would make
// the clause pass on a process of any size, and a reader that HANGS would stop
// the heartbeat, which is the run's only evidence that it was alive.
//
// The hang was found by a real run, not by a test: the first version of this
// reader blocked the heartbeat loop, and the run produced one heartbeat and
// then silence.
func TestRSSIsMeasuredNotGuessedOnThisPlatform(t *testing.T) {
	if !HasRSSReader() {
		t.Skipf("this platform (%s) has no resident-set reader in this build, so the RSS clause of AC5 is NOT RUN rather than passed. That is the correct answer on a platform with no reader, and it has to be visible rather than a zero.", runtime.GOOS)
	}
	done := make(chan int64, 1)
	go func() {
		n, err := currentRSS()
		if err != nil {
			t.Errorf("the resident-set reader failed on a platform that claims to have one: %v", err)
			done <- 0
			return
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n <= 0 {
			t.Fatalf("the resident-set reader returned %d; a zero here would make AC5's memory clause pass on a process of any size", n)
		}
		// A running Go process is never under a megabyte resident, and a reader
		// reporting kilobytes is reading the wrong field.
		if n < 1<<20 {
			t.Fatalf("the resident-set reader returned %d bytes (%s), which is not a resident set for a running Go process", n, Bytes(n))
		}
		t.Logf("resident set measured at %s", Bytes(n))
	case <-time.After(5 * time.Second):
		t.Fatal("the resident-set reader did not return within 5s; a reader that blocks takes the heartbeat with it, and a run with no heartbeats cannot be shown to have been alive")
	}
}

// TestTheHeartbeatDoesNotPanicWithoutAStore: the heartbeat writes to the store,
// and a runner built without one must report rather than take the run down.
func TestTheHeartbeatDoesNotPanicWithoutAStore(t *testing.T) {
	r := &Runner{
		opt: RunnerOptions{RunID: "r1", Recorder: &silentPipe{}, Store: nil},
		lat: ai.NewLatencies(16),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A nil context is a programming error, and the heartbeat must return
		// an error for it rather than panicking or hanging.
		_ = r.heartbeat(nil) //nolint:staticcheck // deliberately nil, to prove the guard
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat hung with no store")
	}
}

// TestTheLoggerNeverCallsItself: a first version of logf ended up calling
// logf, and a real run died of a stack overflow one heartbeat in. The symptom
// was a process that started, logged four lines, and then produced no report —
// which reads exactly like a hung run and cost an hour to find.
//
// A runner with no logger must be silent, and a runner with one must reach it.
func TestTheLoggerNeverCallsItself(t *testing.T) {
	var lines []string
	r := &Runner{opt: RunnerOptions{Logf: func(f string, a ...any) { lines = append(lines, f) }}, log: nil}
	r.log = r.opt.Logf
	r.logf("with a logger: %d", 1)
	if len(lines) != 1 {
		t.Fatalf("a runner with a logger produced %d lines, want 1", len(lines))
	}
	silent := &Runner{}
	// A runner with no logger returns immediately. If this recursed, the test
	// would die of a stack overflow rather than fail, which is exactly what
	// happened in the run.
	done := make(chan struct{})
	go func() {
		defer close(done)
		silent.logf("no logger: %d", 1)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logf with no logger did not return: it is calling itself")
	}
}

type silentPipe struct{}

func (s *silentPipe) Poll(time.Duration) (recorder.Frame, bool) { return recorder.Frame{}, false }
func (s *silentPipe) Exhausted() bool                           { return true }
func (s *silentPipe) Next() (recorder.Frame, bool)              { return recorder.Frame{}, false }
func (s *silentPipe) Counts() (uint64, uint64, uint64)          { return 0, 0, 0 }
func (s *silentPipe) Index() (*recorder.SegmentIndex, error)    { return &recorder.SegmentIndex{}, nil }
func (s *silentPipe) PipeDepth() int                            { return 0 }
func (s *silentPipe) LastFrameJPEG() []byte                     { return nil }
