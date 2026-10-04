package soak

import "errors"

// Resident-set measurement, and the two platforms that have one.
//
// The AC5 ceiling applies to this process as sampled, and the sampling interval is
// declared in the report rather than assumed. Each platform has its own reader
// because the number is not portable and a wrong one would make AC5 pass on a
// process several times its real size.
//
// # Why the selection is not a switch
//
// It WAS one, in this file:
//
//	switch runtime.GOOS {
//	case "windows":
//		currentRSS = windowsRSS
//	case "linux":
//		currentRSS = linuxRSS
//	}
//
// which cannot compile on either platform, because a `switch` on `runtime.GOOS` is
// still a compiled reference: on Windows `linuxRSS` is behind `//go:build linux` and
// does not exist, and on Linux `windowsRSS` does not exist. `runtime.GOOS` decides
// which file you are in; it does not make a reference to the other platform's
// symbol legal.
//
// So each platform's file installs its own reader from its own `init`, and a
// platform with neither file leaves `currentRSS` nil. There is no list anywhere to
// fall out of date with the files on disk.
//
// `HasRSSReader` used to be `runtime.GOOS == "windows" || runtime.GOOS == "linux"`
// sitting beside an `init` that wired only the Windows one. So on Linux it answered
// "yes" while every read returned the not-implemented sentinel: a report that
// claimed the RSS clause was measured, with nothing behind the number. It is now
// derived from whether a reader was installed.

// currentRSS is the resident set size of this process, read from the OS.
//
// Nil on a platform with no reader, which is how "this build cannot measure it"
// says so rather than reporting zero.
var currentRSS func() (int64, error)

// errNoRSSReader is what a sample records when there is no reader, so the report
// names the reason rather than printing an empty error.
var errNoRSSReader = errors.New("soak: this platform has no resident-set reader in this build, so the RSS clause of AC5 is NOT RUN rather than passed")

// HasRSSReader reports whether this build can measure its own resident set. The
// report uses it to say NOT RUN rather than 0 for the RSS clause.
func HasRSSReader() bool { return currentRSS != nil }
