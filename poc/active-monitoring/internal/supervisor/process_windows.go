//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// The supervisor's process control is platform-specific in a way that matters.
//
// On Windows, killing a process does not kill what it started. `ffmpeg` is a
// single process, but the Python sidecar on Windows is `py.exe`, which is a
// LAUNCHER that starts a real `python.exe` and then usually exits. Killing the
// launcher leaves the worker running: it keeps the port, keeps the model
// loaded, and the next preflight finds a sidecar nobody is supervising.
//
// So on Windows the supervisor kills the whole tree, and on Unix it kills the
// process group. Both are the same intent: when this supervisor says the child
// is gone, the child IS gone, and nothing of it is left holding a port.
const (
	// termGrace is how long a child is asked to end before it is killed.
	termGrace = 3 * time.Second
	// jobTimeout is how long Windows waits for the job to drain.
	jobTimeout = 5 * time.Second
)

// terminate ends a child and everything it started.
func terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Signal(os.Interrupt)
	deadline := time.Now().Add(termGrace)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// taskkill /T walks the tree, which is the only thing on Windows that
	// actually reaches the python behind py.exe.
	_ = exec.Command("taskkill", "/F", "/T", "/PID", itoa(pid)).Run()
	waited := time.Now().Add(jobTimeout)
	for time.Now().Before(waited) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errStillRunning
}

func processAlive(pid int) bool {
	// OpenProcess with SYNCHRONIZE fails when the process is gone, which is the
	// cheapest reliable liveness check on Windows without allocating a handle
	// we have to remember to close.
	h, err := openProcessSynchronize(pid)
	if err != nil {
		return false
	}
	defer closeHandle(h)
	_, err = waitForSingleObject(h, 0)
	return err == nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var _ = syscall.SIGINT
