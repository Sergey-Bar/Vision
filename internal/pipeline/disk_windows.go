//go:build windows

package pipeline

import (
	"errors"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// Windows has no `Statfs`, so this reads the same number through the same kernel.
//
// `syscall` does not export a disk-space function on Windows, and the plan cuts new
// dependencies, so this goes through the loader the standard library already provides
// rather than adding `golang.org/x/sys` for one call. That is a judgement about the
// dependency list, and it is worth being honest about what it costs: the proc is
// resolved once at init and every argument below is a pointer into Go memory passed
// to unmanaged code, so the unsafe blocks below are real and are the reason the
// alternative deserves reconsidering if a second Windows-specific syscall is ever
// needed.
//
// Two things make it safe for this call specifically:
//
//   - `GetDiskFreeSpaceExW` writes four out-parameters and returns a BOOL. The
//     `runtime.KeepAlive` on the buffers is what keeps them from being collected
//     while unmanaged code holds a pointer to them; without it this is a use-after-free
//     waiting for a GC.
//   - The path is converted with `UTF16PtrFromString`, which allocates its own
//     NUL-terminated copy, so the string it reads cannot be one the collector moves.
//
// A drive letter is required by the API, so a UNC path or a volume mount point is
// resolved to its first path element. That is the same limitation `wmic` and most
// Windows tooling has, and it is stated here rather than discovered on a site.
var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procDiskFreeExe = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// diskFreePercent is the Windows half of the evidence-volume reading.
//
// The API's `freeBytesAvailableToCaller` is the counterpart of Unix's `Bavail`: it
// excludes the space reserved for the system and for other users, which is what a
// service can actually write to.
func diskFreePercent(path string) (float64, error) {
	root, err := windowsVolumeRoot(path)
	if err != nil {
		return 0, err
	}
	rootPtr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}

	var freeToCaller, total, totalFree uint64
	//nolint:gosec // G103: the audit this asks for is the three lines below.
	// GetDiskFreeSpaceExW takes five pointers and writes through four of them, and
	// `syscall` on Windows exports no wrapper for it. Every pointer is either a
	// pointer into Go memory that `KeepAlive` holds for the duration of the call, or
	// the process's own address, and nothing is derived from caller-supplied bytes.
	// The alternative is `golang.org/x/sys/windows`, which is a dependency added for
	// one function; this is the trade, made once and written down here rather than
	// left as four unexplained casts.
	ok, _, callErr := procDiskFreeExe.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	// The four KeepAlives are the point of the block above and must not be removed:
	// without them the compiler may collect `freeToCaller` while unmanaged code holds
	// a pointer to it.
	runtime.KeepAlive(rootPtr)
	runtime.KeepAlive(&freeToCaller)
	runtime.KeepAlive(&total)
	runtime.KeepAlive(&totalFree)

	if ok == 0 {
		// `callErr` comes back as a `syscall.Errno` and `Errno(0)` on a proc that
		// merely did not signal success, so the zero check is `errors.Is` on the
		// sentinel rather than `!=` on the interface -- comparing a wrapped Errno with
		// `!=` is false for the same value the moment anything wraps it.
		if !errors.Is(callErr, syscall.Errno(0)) {
			return 0, callErr
		}
		return 0, errors.New("GetDiskFreeSpaceExW failed on " + root)
	}
	if total == 0 {
		// Same reason as the Unix half, and the same consequence: a zero total would
		// be a NaN gauge, and NaN compares false against every threshold, so the
		// objective would read green on a volume that is not mounted.
		return 0, errors.New("GetDiskFreeSpaceExW reported zero total bytes for " + root)
	}
	return 100 * float64(freeToCaller) / float64(total), nil
}

// windowsVolumeRoot returns the volume root for a path, as `X:\`.
//
// `filepath.VolumeName` gives the drive or UNC share without a trailing separator,
// and the API rejects `C:` -- it wants `C:\` -- so the separator is what has to be
// added back. A UNC share is taken verbatim because `\\server\share` is already the
// root the API wants.
func windowsVolumeRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		return "", errors.New("no volume in " + abs + "; GetDiskFreeSpaceExW needs a drive or a UNC share")
	}
	if len(vol) > 2 && vol[:2] == `\\` {
		// UNC: `\\server\share` is already the root.
		return vol, nil
	}
	return vol + `\`, nil
}
