//go:build windows

package soak

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// The two kernel entry points the CPU sampler needs, resolved through x/sys.
//
// `Find` is used rather than `Call` because `LazyProc.Call` PANICS when a
// procedure is missing, and a panic in the sampler would take the whole run down
// at the first sample. A missing procedure here means the CPU clause of AC5
// becomes NOT RUN with a reason, which is the right answer; a panic is not.
//
// The lesson came from the RSS reader, which looked for GetProcessMemoryInfo in
// kernel32 instead of psapi and killed a real run one heartbeat in.
var (
	kernel32OpenProcess     = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenProcess")
	kernel32GetProcessTimes = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessTimes")
)

// findProcedureErr resolves a LazyProc, reporting absence instead of panicking.
func findProcedureErr(p *windows.LazyProc) error { return p.Find() }

// closeProcHandle releases a handle opened for accounting.
func closeProcHandle(h uintptr) { _ = syscall.CloseHandle(syscall.Handle(h)) }
