package pipeline

// A loop that panics is RESTARTED, and a loop that is not running is VISIBLE.
//
// # Why the restart
//
// A panic in a loop used to kill the process rather than one stage of the
// pipeline, and the reason it was recovered at all was that a silently-stopped
// ingest loop is an outage that looks like a healthy core: the process keeps
// answering /v1/health, the backlog grows, and nothing says so.
//
// Recovering the panic and letting the goroutine die was the same outage with
// a log line attached. So the loop is restarted, after a pause, and the pause
// grows with the number of consecutive panics.
//
// # Why the pause grows
//
// A loop that panics on every pass would otherwise spin, and a spinning loop
// is a hot loop that burns the CPU the product needs to do the work it is
// failing at.
//
// # Why the state is visible
//
// Because a restarted loop is not a recovered loop. A loop that panics on every
// pass restarts forever, and the only way an operator learns that is for the
// health endpoint to name it.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestALoopThatPanicsIsRestarted is the restart.
//
// The loop panics on its first pass and returns on the second. It has to run
// twice, because a loop that panics and dies is an outage that looks like a
// healthy core.
func TestALoopThatPanicsIsRestarted(t *testing.T) {
	l := New(Deps{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var passes atomic.Int32
	restarted := make(chan struct{}, 4)
	l.spawn(ctx, "panicky", func(ctx context.Context) {
		if passes.Add(1) == 1 {
			panic("the first pass fails")
		}
		select {
		case restarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
	})

	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop was not restarted. A loop that panics and dies is an outage that " +
			"looks like a healthy core")
	}

	state := l.Snapshot()
	if len(state) != 1 {
		t.Fatalf("the snapshot holds %d loops, want 1", len(state))
	}
	if state[0].Name != "panicky" {
		t.Errorf("the loop is named %q, want panicky", state[0].Name)
	}
	if state[0].Panics != 1 {
		t.Errorf("the loop recorded %d panics, want 1", state[0].Panics)
	}
	if !state[0].Running {
		t.Error("the loop is not running after a restart")
	}
}

// TestALoopThatPanicsIsVisible is the visibility.
//
// A loop that panics on every pass restarts forever. The state has to show
// that, because "the loop is running" and "the loop keeps failing" are
// different answers and an operator needs the second one.
func TestALoopThatPanicsIsVisible(t *testing.T) {
	l := New(Deps{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var passes atomic.Int32
	go l.spawn(ctx, "always-panicky", func(ctx context.Context) {
		passes.Add(1)
		panic("every pass fails")
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := l.Snapshot()
		if len(state) == 1 && state[0].Panics >= 2 {
			if state[0].Running {
				t.Errorf("the loop is reported as running after %d panics. A restarted loop "+
					"is not a recovered loop", state[0].Panics)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the loop never recorded more than one panic, so it was not restarted")
}

// TestAHealthyLoopIsRunning is the other half.
//
// The liveness report is worth nothing if it says "stopped" about a loop that
// is working.
func TestAHealthyLoopIsRunning(t *testing.T) {
	l := New(Deps{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan struct{})
	l.spawn(ctx, "healthy", func(ctx context.Context) {
		l.Beat("healthy")
		<-stop
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := l.Snapshot()
		if len(state) == 1 && state[0].Running && state[0].Panics == 0 {
			close(stop)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	t.Fatal("a healthy loop is not reported as running")
}
