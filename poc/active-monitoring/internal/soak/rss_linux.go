//go:build linux

package soak

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// linuxRSS reads this process's resident set size from /proc/self/statm, which
// reports it in pages. The page size is read rather than assumed, because
// assuming 4096 on a machine with 16384-byte pages understates the number by
// four times and AC5 would then pass on a process four times its size.
func linuxRSS() (int64, error) {
	pageSize := int64(os.Getpagesize())
	f, err := os.Open("/proc/self/statm")
	if err != nil {
		return 0, fmt.Errorf("soak: cannot read /proc/self/statm: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, fmt.Errorf("soak: /proc/self/statm produced no data")
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 2 {
		return 0, fmt.Errorf("soak: /proc/self/statm has %d fields, want at least 2", len(fields))
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("soak: /proc/self/statm resident field %q is not a number: %w", fields[1], err)
	}
	return pages * pageSize, nil
}
