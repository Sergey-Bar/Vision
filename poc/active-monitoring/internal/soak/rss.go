package soak

import (
	"runtime"
	"sync"
)

// currentRSS is the resident set size of this process, read from the OS.
//
// The AC5 ceiling applies to this process as sampled, and the sampling interval
// is declared in the report rather than assumed. Each platform has its own
// reader because the number is not portable and a wrong one would make AC5 pass
// on a process several times its real size.
var currentRSS = func() (int64, error) { return 0, errNoRSSReader }

var errNoRSSReader = errStr("soak: this platform has no resident-set reader in this build, so the RSS clause of AC5 is NOT RUN rather than passed")

type errStr string

func (e errStr) Error() string { return string(e) }

var rssInit sync.Once

func init() {
	rssInit.Do(func() {
		switch runtime.GOOS {
		case "windows":
			currentRSS = windowsRSS
		}
	})
}

// HasRSSReader reports whether this build can measure its own resident set. The
// report uses it to say NOT RUN rather than 0 for the RSS clause on a platform
// with no reader.
func HasRSSReader() bool { return runtime.GOOS == "windows" || runtime.GOOS == "linux" }
