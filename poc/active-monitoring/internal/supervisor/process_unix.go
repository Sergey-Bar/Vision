//go:build !windows

package supervisor

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// The supervisor's process control is platform-specific in a way that matters.
//
// On Unix a child is put in its own process group and the group is signalled,
// so a shell wrapper or a launcher dies with the thing it launched. On Windows
// the same intent is served by a Job Object, because `py.exe` is a launcher that
// exits and leaves `python.exe` holding the port.
const termGrace = 3 * time.Second

// setpgid puts the child in a new process group so the group can be signalled
// as a unit.
func setpgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminate ends the child and everything in its process group.
func terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	pgid := -pid
	_ = syscall.Kill(pgid, syscall.SIGINT)
	deadline := time.Now().Add(termGrace)
	for time.Now().Before(deadline) {
		if !groupAlive(pgid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pgid, syscall.SIGKILL)
	for i := 0; i < 20; i++ {
		if !groupAlive(pgid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errStillRunning
}

func groupAlive(pgid int) bool {
	// Signal 0 tests for existence without delivering anything.
	err := syscall.Kill(pgid, 0)
	return err == nil || err == syscall.EPERM
}

var errStillRunning = errors.New("supervisor: the child is still running after its process group was asked to die")

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
