//go:build !windows

package pipeline

import "syscall"

// diskFreePercent is the percentage of the filesystem holding path that is free to
// use by an unprivileged process.
//
// `Bavail` rather than `Bfree`, and the difference is the whole question: `Bfree`
// counts the blocks reserved for root, so on a filesystem mounted with a reserved
// percentage -- ext4's default is 5% -- `Bfree` reports space the service cannot
// write to. A gauge that counted it would report a volume as having room right up to
// the point where every write fails with ENOSPC, which is precisely when an operator
// most needs the number to be true.
//
// The divisor is `Blocks`, the filesystem's total, not `Bsize`-scaled: blocks and
// free blocks are already in the same unit, and scaling by the block size on both
// sides would cancel out.
//
// No error type is invented. A filesystem that cannot be stat'd returns the syscall's
// own error, so the caller logs whatever the kernel said rather than a paraphrase of
// it.
func diskFreePercent(path string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := float64(stat.Blocks)
	if total <= 0 {
		// A filesystem reporting zero total blocks is not mounted, or is a
		// pseudo-filesystem with no capacity. Returning an error keeps the caller
		// from dividing by zero, which would put a NaN into a gauge that an SLO
		// compares against a threshold -- and NaN compares false against everything,
		// so the objective would be permanently green on a volume that is not there.
		return 0, syscall.ENODEV
	}
	return 100 * float64(stat.Bavail) / total, nil
}
