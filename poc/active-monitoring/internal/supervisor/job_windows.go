//go:build windows

package supervisor

import (
	"errors"
	"syscall"
	"unsafe"
)

// The Windows kernel entry points are reached directly rather than through a
// package wrapper, because the wrappers for exactly these three are not in
// x/sys and the alternative is a build dependency for three declarations.

var (
	modkernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess              = modkernel32.NewProc("OpenProcess")
	procWaitForSingleObject      = modkernel32.NewProc("WaitForSingleObject")
	procTerminateProcess         = modkernel32.NewProc("TerminateProcess")
	procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
	procCreateJobObject          = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modkernel32.NewProc("SetInformationJobObject")
)

const (
	// synchronize is PROCESS_SYNCHRONIZE: enough to wait on a handle.
	synchronize = 0x00100000
	// waitTimeout is WAIT_TIMEOUT: the object is still signalled.
	waitTimeout = 0x00000102
	// jobObjectExtendedLimitInformation is the information class that carries
	// KILL_ON_JOB_CLOSE.
	jobObjectExtendedLimitInformation = 9
	terminateExitCode                 = 1
)

// job kills a whole tree on Windows. It exists because `taskkill /T` is a
// subprocess that can be slow, can be missing, and prints to the console; a Job
// Object is a kernel-level parent that dies with the handle.
type job struct {
	handle syscall.Handle
}

// newJob creates a job object whose members are killed when the handle closes.
// A child assigned to it inherits that fate, and so does anything the child
// starts, which is exactly the py.exe -> python.exe case.
func newJob() (*job, error) {
	h, _, err := procCreateJobObject.Call(0, 0)
	if h == 0 {
		return nil, errors.New("supervisor: CreateJobObject failed, so a child could not be given a process tree to die with: " + err.Error())
	}
	j := &job{handle: syscall.Handle(h)}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
	// The struct is 144 bytes on 64-bit; only the first two fields are read.
	var info [144]byte
	putUint32(info[0:], 144)         // BasicLimitInformation.LimitFlags offset is at 44
	putUint32(info[44:], 0x00002000) // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if r, _, err := procSetInformationJobObject.Call(uintptr(j.handle), jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info[0])), 144); r == 0 {
		_ = closeHandle(j.handle)
		return nil, errors.New("supervisor: SetInformationJobObject failed, so a child could not be given kill-on-close: " + err.Error())
	}
	return j, nil
}

// attach assigns a process to the job. It must happen while the process is
// still suspended, or between CreateProcess and the child doing anything, which
// is why the supervisor starts children and attaches them immediately.
func (j *job) attach(pid int) error {
	h, err := openProcess(pid)
	if err != nil {
		return err
	}
	defer closeHandle(h)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(j.handle), uintptr(h)); r == 0 {
		return errors.New("supervisor: AssignProcessToJobObject failed for pid " + itoa(pid) + ": " + err.Error())
	}
	return nil
}

// close releases the job, which kills anything still in it.
func (j *job) close() {
	if j == nil || j.handle == 0 {
		return
	}
	_ = closeHandle(j.handle)
	j.handle = 0
}

func openProcess(pid int) (syscall.Handle, error) {
	h, _, err := procOpenProcess.Call(synchronize|terminateProcessAccess, 0, uintptr(pid))
	if h == 0 {
		return 0, errors.New("supervisor: OpenProcess(" + itoa(pid) + ") failed: " + err.Error())
	}
	return syscall.Handle(h), nil
}

const terminateProcessAccess = 0x0001

func openProcessSynchronize(pid int) (syscall.Handle, error) {
	h, _, err := procOpenProcess.Call(synchronize, 0, uintptr(pid))
	if h == 0 {
		return 0, errors.New("supervisor: OpenProcess(synchronize, " + itoa(pid) + ") failed: " + err.Error())
	}
	return syscall.Handle(h), nil
}

func closeHandle(h syscall.Handle) error {
	if h == 0 {
		return nil
	}
	return syscall.CloseHandle(h)
}

func waitForSingleObject(h syscall.Handle, ms uint32) (uint32, error) {
	r, _, err := procWaitForSingleObject.Call(uintptr(h), uintptr(ms))
	return uint32(r), err
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

var errStillRunning = errors.New("supervisor: the child is still running after its process tree was asked to die")
