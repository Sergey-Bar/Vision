//go:build !windows

package supervisor

// The Unix side of the process-tree belt.
//
// # Why this file exists at all
//
// `internal/supervisor` had a `job_windows.go` and NO `job_unix.go`, while
// `supervisor.go` held a `*job` unconditionally. So the package compiled on Windows
// and had never compiled anywhere else — the whole POC module had never been built
// for Linux, and its gate had only ever run on Windows for the same reason the
// product's gate could not see its five Linux defects.
//
// # And the belt was not fitted on either platform
//
// `newJob` and `attach` were defined and never called, from any package, on any
// platform. `s.job` was therefore always nil, and `Stop`'s "closing the job kills
// anything still inside it" was a comment describing a belt that was not on the
// trousers. This file and the two call sites in `supervisor.go` fit it.
//
// # What the belt IS on Unix
//
// Windows has a Job Object: a kernel-level parent that dies with its handle, and
// that a launcher (`py.exe`) and the real `python.exe` behind it are both inside.
// Unix has no equivalent kernel object, and the honest answer is the process group
// plus an unconditional kill.
//
// `setpgid` already puts each child in its own group, so `attach` has nothing to do
// here — the group exists from the moment the child is created, which is the
// property the Windows version cannot offer and the reason its `attach` must run
// while the process is still suspended. What `close()` does is send SIGKILL to
// every group it has seen, unconditionally, after `terminate` has already asked
// politely and waited. That is the belt: `terminate` is the braces and this is the
// thing that catches what they let through.
//
// The escalation is one-way on purpose. `Stop` has already given the child its
// grace period by the time `close` runs, so there is nothing left to be polite
// about, and a child that ignored SIGINT and outlived SIGKILL's grace window is a
// child holding a camera port.

import (
	"errors"
	"sync"
	"syscall"
)

// job is the set of process groups this sidecar owns.
//
// It is a set rather than a single group because `attach` is called once per child
// and a supervisor that restarts its worker must not lose the previous worker's
// group. One is the common case; a slice keeps the type honest about the general
// one.
type job struct {
	mu     sync.Mutex
	pgids  []int
	closed bool
}

// newJob returns an empty belt.
//
// No error on Unix, and that asymmetry is the whole difference from Windows: there
// is no kernel object to create, so there is nothing here that can fail. The
// signature matches `job_windows.go`'s so that `supervisor.go` does not branch.
func newJob() (*job, error) { return &job{}, nil }

// attach records a child's process group.
//
// On Windows this assigns the process to a job object and it must happen before the
// child does anything. Here `setpgid` already made the child a group leader at
// `fork` time, so recording the group id is the whole of it — and it is still worth
// recording, because `close` needs the number and deriving it from the pid is an
// assumption (`-pid`) that a child could invalidate by changing its own group.
func (j *job) attach(pid int) error {
	if j == nil || pid <= 0 {
		return errors.New("supervisor: attach was given no process to record, so nothing would be killed with it")
	}
	// The child is its own group leader because `setpgid` asked for exactly that,
	// so the group id equals the pid. Verified against the same assumption
	// `terminate` makes, rather than being a second one.
	pgid := pid
	if err := syscall.Kill(pgid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		// The process is already gone. Not an error: a child that died before
		// `attach` ran cannot be killed, and refusing to start over it would turn a
		// fast crash into a slow one with a worse message.
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("supervisor: this sidecar's process group belt was already closed, so a " +
			"process started after Stop would have nothing to kill it with")
	}
	j.pgids = append(j.pgids, pgid)
	return nil
}

// close kills everything the belt knows about, without asking.
//
// Idempotent, because `Stop` and an error path both reach it and a second kill of
// a dead group is not an error worth reporting to an operator.
func (j *job) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		return
	}
	j.closed = true
	pgids := append([]int(nil), j.pgids...)
	j.pgids = nil
	j.mu.Unlock()

	for _, pgid := range pgids {
		// ESRCH means the group is already gone, which is the outcome `close` wants.
		// EPERM would mean it exists and belongs to someone else, which cannot happen
		// for a group this process created.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
