//go:build windows

package soak

import (
	"errors"
	"strconv"
	"unsafe"
)

// processCPUTime returns the CPU seconds a process has consumed, counting both
// user and kernel time.
//
// AC5's CPU clause is about ffmpeg and the sidecar, not about this process, so
// it is measured per child. The previous version recorded 0, which is not a
// small number: it is a ceiling that passes on any load at all, and a seven-day
// run scored against a zero would be a run whose CPU clause was never checked.
func processCPUTime(pid int) (float64, error) {
	// PROCESS_QUERY_LIMITED_INFORMATION is enough for GetProcessTimes and is
	// available to an unelevated process on a modern Windows.
	const queryLimitedInformation = 0x1000

	// Both procedures are resolved up front, so a missing one is reported before
	// any handle is opened and the clause is NOT RUN with a reason.
	if err := findProcedureErr(kernel32OpenProcess); err != nil {
		return 0, errors.New("soak: OpenProcess is not available, so the CPU clause of AC5 is NOT RUN rather than passed: " + err.Error())
	}
	if err := findProcedureErr(kernel32GetProcessTimes); err != nil {
		return 0, errors.New("soak: GetProcessTimes is not available, so the CPU clause of AC5 is NOT RUN rather than passed: " + err.Error())
	}

	h, _, callErr := kernel32OpenProcess.Call(queryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return 0, errors.New("soak: cannot open pid " + strconv.Itoa(pid) + " for CPU accounting: " + callErr.Error())
	}
	defer closeProcHandle(h)

	// FILETIME is two 32-bit halves: low word, then high word.
	type filetime struct{ low, high uint32 }
	ftToUint := func(f filetime) uint64 { return uint64(f.high)<<32 | uint64(f.low) }

	var creation, exit, kernel, user filetime
	r, _, callErr := kernel32GetProcessTimes.Call(
		h,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0, errors.New("soak: GetProcessTimes failed for pid " + strconv.Itoa(pid) + ": " + callErr.Error())
	}
	// A FILETIME counts 100-nanosecond intervals.
	const tickToSeconds = 1e-7
	secs := float64(ftToUint(kernel)+ftToUint(user)) * tickToSeconds
	return secs, nil
}
