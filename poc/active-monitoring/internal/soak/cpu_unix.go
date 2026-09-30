//go:build !windows

package soak

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processCPUTime returns the CPU seconds a process has consumed, counting both
// user and system time.
//
// AC5's CPU clause is about ffmpeg and the sidecar, not about this process, so
// it is measured per child. A recorded 0 is not a small number: it is a ceiling
// that passes on any load at all.
func processCPUTime(pid int) (float64, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("soak: cannot read CPU time for pid %d: %w", pid, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, fmt.Errorf("soak: /proc/%d/stat produced no data", pid)
	}
	line := sc.Text()
	// The second field is the command name in parentheses and may contain
	// spaces, so the fields after it are found from the LAST ')'.
	close := strings.LastIndex(line, ")")
	if close < 0 {
		return 0, fmt.Errorf("soak: /proc/%d/stat is not in the expected shape", pid)
	}
	fields := strings.Fields(line[close+1:])
	// After the ')' the fields are state(0) ppid(1) ... utime is index 11 and
	// stime index 12.
	const clockTicks = 100 // Linux USER_HZ
	if len(fields) < 13 {
		return 0, fmt.Errorf("soak: /proc/%d/stat has %d fields after the name, want at least 13", pid, len(fields))
	}
	utime, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseFloat(fields[12], 64)
	if err != nil {
		return 0, err
	}
	return (utime + stime) / clockTicks, nil
}
