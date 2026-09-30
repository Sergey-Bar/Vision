//go:build windows

package soak

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetProcessMemoryInfo lives in PSAPI, not in kernel32. Looking for it in the
// wrong DLL is not a compile error and not a returned error: a LazyProc panics
// when the symbol is missing, and a panic in a goroutine takes the whole process
// down. The first version of this reader looked in kernel32, and a real run died
// one heartbeat in with a Go panic that no amount of log filtering would have
// mistaken for a clean stop.
//
// So: the right DLL, and Find() rather than Call(), with the absence reported
// rather than panicked on.
var (
	modPSAPI                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modPSAPI.NewProc("GetProcessMemoryInfo")
)

// processMemoryCountersSize is generously larger than
// sizeof(PROCESS_MEMORY_COUNTERS) so a 32-bit build is not truncated.
const processMemoryCountersSize = 128

// workingSetOffset is the byte offset of WorkingSetSize inside
// PROCESS_MEMORY_COUNTERS on 64-bit Windows: cb at 0, PageFaultCount at 4,
// PeakWorkingSetSize at 8, WorkingSetSize at 16. On 32-bit the SIZE_T fields
// are four bytes, so it shifts to 12.
//
// The buffer is read BY OFFSET rather than through a Go struct on purpose. A Go
// struct's field offsets are chosen by the compiler and only happen to match the
// C layout; a syscall that depends on that coincidence breaks on the next Go
// release. Fixed offsets from a byte buffer cannot.
var workingSetOffset = func() uint32 {
	if bits.UintSize == 64 {
		return 16
	}
	return 12
}()

// windowsRSS reads this process's working set from the kernel.
func windowsRSS() (int64, error) {
	if err := procGetProcessMemoryInfo.Find(); err != nil {
		return 0, errors.New("soak: GetProcessMemoryInfo is not available in psapi.dll on this machine, so the RSS clause of AC5 is NOT RUN rather than passed: " + err.Error())
	}
	var buf [processMemoryCountersSize]byte
	binary.LittleEndian.PutUint32(buf[0:], processMemoryCountersSize)
	r, _, err := procGetProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if r == 0 {
		return 0, errors.New("soak: GetProcessMemoryInfo failed, so the RSS clause of AC5 is NOT RUN rather than passed: " + err.Error())
	}
	return int64(binary.LittleEndian.Uint64(buf[workingSetOffset:])), nil
}
