package soak

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
)

// fakeRecorder is a pipe with no ffmpeg behind it, so the run LOOP can be tested
// on its own. The loop's whole job is to keep heartbeats going while frames
// arrive, and that is impossible to check against a real ffmpeg: the moment a
// real pipe stalls, the thing under test is the stall.
type fakeRecorder struct {
	mu        sync.Mutex
	frames    []time.Time // the instants to hand out
	exhausted bool
	beats     atomic.Int64
}

func (f *fakeRecorder) Poll(d time.Duration) (recorder.Frame, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.frames) > 0 {
		at := f.frames[0]
		f.frames = f.frames[1:]
		return recorder.Frame{At: at}, true
	}
	if f.exhausted {
		return recorder.Frame{}, false
	}
	// Nothing arrived within the window, and nothing ever will. A real stalled
	// pipe behaves exactly like this: Poll keeps returning "nothing yet".
	return recorder.Frame{}, false
}

func (f *fakeRecorder) Exhausted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exhausted
}

func (f *fakeRecorder) Next() (recorder.Frame, bool) { return f.Poll(time.Millisecond) }

// push hands a frame to the next Poll.
func (f *fakeRecorder) push(at time.Time) {
	f.mu.Lock()
	f.frames = append(f.frames, at)
	f.mu.Unlock()
}

func (f *fakeRecorder) Counts() (uint64, uint64, uint64) { return 0, 0, 0 }

func (f *fakeRecorder) Index() (*recorder.SegmentIndex, error) { return &recorder.SegmentIndex{}, nil }

func (f *fakeRecorder) PipeDepth() int { return 0 }

func (f *fakeRecorder) LastFrameJPEG() []byte { return nil }

// beatCounter stands in for the heartbeat write, so the loop can be tested with
// no store. It is what the assertions read.
type beatCounter struct{ n atomic.Int64 }

// newLoopRunner builds a runner whose frame source and heartbeat are both
// injected, so the LOOP is testable on its own. Both are indirected for exactly
// this reason: a real ffmpeg cannot be used to test what happens when the pipe
// stalls, because the stall is the thing under test.
func newLoopRunner(rec FrameSource, target, beatEvery time.Duration) (*Runner, *beatCounter) {
	beats := &beatCounter{}
	r := &Runner{
		opt: RunnerOptions{
			RunID: "r1", Recorder: rec, Target: target, Heartbeat: beatEvery,
			Logf: func(string, ...any) {},
		},
		lat:      ai.NewLatencies(16),
		stopPush: make(chan struct{}),
	}
	r.opt.heartbeat = func(context.Context) error { beats.n.Add(1); return nil }
	return r, beats
}

// TestTheRunLoopKeepsBeatingWhileThePipeIsQuiet is the bug this file exists
// for. A run loop that BLOCKS on the frame queue cannot service its own
// heartbeat timer, so a stalled pipe produces a run that goes silent — and
// silence is the dropout AC1 exists to detect, noticed by the one component
// that must never be the one that notices.
func TestTheRunLoopDoesNotBlockOnAQuietPipe(t *testing.T) {
	rec := &fakeRecorder{} // never answers
	r, beats := newLoopRunner(rec, 600*time.Millisecond, 50*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()

	// While the run is going, the loop must have produced beats. If the loop
	// were blocked on the pipe this counter would stay at zero and the run
	// would still be "alive" as far as the process table is concerned.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if beats.n.Load() >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := beats.n.Load(); got < 4 {
		t.Fatalf("the loop produced %d heartbeats in 400ms at a 50ms interval with a pipe that never answers. A loop blocked on the pipe cannot beat, and a run that cannot beat cannot be shown to have been alive", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the run ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the run did not end when its target was reached")
	}
}

// TestTheRunEndsWhenTheTargetIsReached: SP_POC_SOAK_TARGET is a promise made
// before the run. A run that only ever stops on a signal can never conclude,
// and a report that is always PROVISIONAL is a report about the operator's
// attention span rather than about the pipeline.
func TestTheRunEndsWhenTheTargetIsReached(t *testing.T) {
	r, _ := newLoopRunner(&fakeRecorder{}, 200*time.Millisecond, 50*time.Millisecond)
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the run ended with %v", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("the run took %s to reach a 200ms target", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the run did not end when its target was reached")
	}
}

// TestAZeroTargetDoesNotEndTheRunImmediately: a run with no declared target
// runs until it is told to stop, because ending on an undeclared number would
// be a number nobody chose.
// TestTheHeartbeatIsNotSilencedByASlowFrame is the second version of the same
// bug, and a real run found it: the heartbeat shared the frame loop, so a slow
// INFERENCE delayed it. The first call into a freshly loaded ONNX session on
// CPU took a minute, and a 71-second heartbeat gap was recorded for exactly
// that reason.
//
// The gap was real, but it was an artefact of WHERE the heartbeat ran. A
// liveness signal that a busy worker can silence is not a liveness signal.
func TestTheHeartbeatIsNotSilencedByASlowFrame(t *testing.T) {
	rec := &fakeRecorder{}
	r, beats := newLoopRunner(rec, 700*time.Millisecond, 40*time.Millisecond)

	// A "frame" that takes 300ms to deal with, which is a fast inference on a
	// slow machine and a slow one for anything bigger.
	var inFlight atomic.Int64
	r.opt.onFrame = func(recorder.Frame, []ai.Box, []byte, ai.Telemetry, string) {
		inFlight.Add(1)
		time.Sleep(300 * time.Millisecond)
	}

	// A pipe that hands out a frame every 25ms, so the frame loop is saturated.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			rec.push(time.Now())
			time.Sleep(25 * time.Millisecond)
		}
	}()
	defer close(stop)

	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	time.Sleep(350 * time.Millisecond)
	if got := beats.n.Load(); got < 4 {
		t.Fatalf("the heartbeat produced %d beats in 350ms at a 40ms interval while the frame loop was saturated. A liveness signal a busy worker can silence is not a liveness signal", got)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the run did not end when its target was reached")
	}
}

func TestAZeroTargetDoesNotEndTheRunImmediately(t *testing.T) {
	r, _ := newLoopRunner(&fakeRecorder{}, 0, 40*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := r.Run(ctx); err != nil {
		t.Fatalf("the run ended with %v", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("the run returned after %s; a target of zero must not be treated as a deadline", elapsed)
	}
}

// TestTheRunEndsWhenThePipeEnds: ffmpeg going away is the end of the stream,
// and the loop must notice without waiting for its target.
func TestTheRunEndsWhenThePipeEnds(t *testing.T) {
	rec := &fakeRecorder{exhausted: true}
	r, _ := newLoopRunner(rec, time.Hour, 50*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the run ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the run did not end when the pipe ended, on a target of an hour")
	}
}
