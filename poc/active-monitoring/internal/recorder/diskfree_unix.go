//go:build !windows

package recorder

import "golang.org/x/sys/unix"

// getDiskFree reports the free space of the filesystem holding path.
func getDiskFree(path string, free *uint64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	*free = uint64(st.Bavail) * uint64(st.Bsize)
	return nil
}
