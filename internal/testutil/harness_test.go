package testutil

// The harness promises two different answers to two different situations, and a
// contributor without Postgres is the case it exists for:
//
//	no database          -> a green run that SKIPS
//	the harness is wrong -> a RED run, naming the cause
//
// The second one used to HANG instead. `Pool` holds `poolMu` across its whole body
// via `defer Unlock`, and it calls `failHarness` from inside -- so with `harnessErr`
// guarded by that same non-reentrant mutex, the "the harness is broken" path blocked
// forever and the whole `go test` timeout expired with nothing printed.
//
// That is the worst of the three possible outcomes. A skip is wrong because it hides a
// broken DDL behind "no database"; a hang is worse, because it reports nothing at all
// and burns the timeout in every package that shares the binary.
//
// Found by stopping Docker Desktop mid-session, which is exactly the event the
// shared/unavailable split exists to handle.
//
// This test is IN the package rather than beside it, deliberately. Proving the fix
// needs to reach `pool` with a `*testing.T` stand-in -- `t.Fatalf` cannot be given a
// timeout, and the timeout is the whole assertion -- and exporting the hooks to let an
// external test do that would put three new exported symbols in a package whose
// `deadexport` gate exists to argue about exactly that.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// recorder is a `fatalTarget` that records and then STOPS, the way a real
// `*testing.T` does.
//
// The stopping is the subtle part and the first version got it wrong. `t.Skipf` and
// `t.Fatalf` end the goroutine by calling `runtime.Goexit`, so nothing after them in
// `pool` ever runs. A recorder that merely RECORDED and returned let `pool` carry on
// past the skip, and the message the test read was the one three lines further down --
// `test database unavailable: dbx: "" is not a postgres:// DSN` -- which is what a
// broken `ApplyDSN` says. The test failed for a reason that had nothing to do with the
// deadlock it exists to catch, and the fix looked like it was in `pool`'s ordering.
//
// So the recorder panics, and the caller recovers. The panic value carries the message
// out, so what the test reads is genuinely the last thing `pool` said.
type recorder struct {
	message string
	calls   int
}

// stop is what `Fatalf` and `Skipf` throw.
type stop struct{}

// recorder implements fatalTarget by recording and unwinding.
func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.calls++
	r.message = fmt.Sprintf(format, args...)
	panic(stop{})
}

func (r *recorder) Skipf(format string, args ...any) {
	r.calls++
	r.message = fmt.Sprintf(format, args...)
	panic(stop{})
}

// run calls f and absorbs the recorder's stop, returning the recorder.
func run(f func(r *recorder)) *recorder {
	r := &recorder{}
	func() {
		defer func() {
			if _, ok := recover().(stop); !ok {
				panic("the recorder's stop was not what unwound the stack")
			}
		}()
		f(r)
	}()
	return r
}

// withRecordedHarnessError installs a recorded harness error for the duration of one
// test and puts back whatever was there before.
//
// It exists because `failHarness` writes to a package-level `harnessErr` and never
// clears it: that is its job, since a broken harness should stay broken for the rest
// of the binary. A TEST that plants one therefore has to put it back itself, and
// forgetting to is how a package goes red for a reason that has nothing to do with the
// code -- which is exactly what `TestTheHarnessIsNotLeftBroken` caught while this was
// being written.
func withRecordedHarnessError(t *testing.T, cause string) {
	t.Helper()
	harnessMu.Lock()
	previous := harnessErr
	harnessErr = errors.New(cause)
	harnessMu.Unlock()
	t.Cleanup(func() {
		harnessMu.Lock()
		harnessErr = previous
		harnessMu.Unlock()
	})
}

// TestABrokenHarnessFailsRatherThanHanging is the regression.
//
// The poison is installed directly rather than through a database, because the defect
// is in the lock discipline and not in anything the database does. A real broken DDL
// would be a better story and a worse test: it needs Postgres, so on a machine without
// one the test that guards the "no database" path would itself skip.
func TestABrokenHarnessFailsRatherThanHanging(t *testing.T) {
	const cause = "a deliberate harness failure, planted by the test that checks this path"
	withRecordedHarnessError(t, cause)

	// In a goroutine with a deadline, because a deadlock does not fail -- it waits for
	// the test binary's own timeout, which is ten minutes by default and is reported
	// as a panic with a stack rather than as a failing test.
	holder := make(chan *recorder, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		got := run(func(rec *recorder) { pool(rec) })
		holder <- got
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("pool did not return within 20s of the harness being marked broken; it "+
			"deadlocked instead of failing. That is the defect this test exists for, and the "+
			"recorded cause was %q", cause)
	}
	got := <-holder

	if got.calls == 0 {
		t.Fatalf("pool returned without failing even though the harness was recorded as "+
			"broken (%q). A recorded harness error that does not stop the run is the skip "+
			"this was written to avoid", cause)
	}
	if !strings.Contains(got.message, "harness is broken") {
		t.Errorf("the failure does not say the harness is broken: %q", got.message)
	}
	if !strings.Contains(got.message, cause) {
		t.Errorf("the failure does not carry the recorded cause %q: %q", cause, got.message)
	}
}

// TestTheHarnessErrorIsNotGuardedByThePoolLock is the lock discipline stated as its
// own assertion, because the deadlock was exactly this and the other test only sees
// it end to end.
//
// It holds `poolMu` -- the lock `Pool` holds across its body -- and then requires
// `failHarness` to complete. With `harnessErr` guarded by `poolMu` this deadlocks,
// which is the bug; with its own `harnessMu` it returns. A test that reproduces the
// exact interleaving is worth more than one that happens to pass.
func TestTheHarnessErrorIsNotGuardedByThePoolLock(t *testing.T) {
	// The recorded error is cleared first and restored after, because `failHarness`
	// writes one and never clears it. Leaving it behind makes the NEXT test fail,
	// which is how this was found the first time -- the fix looked like it broke
	// something else.
	withRecordedHarnessError(t, "")
	harnessMu.Lock()
	harnessErr = nil
	harnessMu.Unlock()

	poolMu.Lock()
	defer poolMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		run(func(rec *recorder) { failHarness(rec, "planted while the pool lock is held") })
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("failHarness blocked while poolMu was held, so harnessErr and the pool " +
			"lifecycle still share one mutex and Pool's own call to failHarness deadlocks")
	}
}

// TestTheHarnessIsNotLeftBroken guards the poison above.
//
// A test that breaks the harness for every later test in the binary is a worse version
// of the bug it is checking: the package would go red for a reason that has nothing to
// do with the code. `pool` skips when there is no database, and the absence of a
// failure below IS the assertion.
func TestTheHarnessIsNotLeftBroken(t *testing.T) {
	pool(t)
}
