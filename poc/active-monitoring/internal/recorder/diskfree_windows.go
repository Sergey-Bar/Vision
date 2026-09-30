//go:build windows

package recorder

import "golang.org/x/sys/windows"

// getDiskFree reports the free space of the volume holding path. Preflight's
// 1.5x headroom rule is refused by name when this cannot be read, because "we
// could not measure the disk" must not read as "there is room".
func getDiskFree(path string, free *uint64) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var available, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &totalFree); err != nil {
		return err
	}
	*free = available
	return nil
}
