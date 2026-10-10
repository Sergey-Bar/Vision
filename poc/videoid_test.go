package main

import (
	"strings"
	"testing"
)

// TestExtractVideoIDRejectsControlCharacters is the log-injection guard: the
// id reaches `log.Printf` at the "manual stream unavailable" call site, and
// url.Parse percent-decodes, so a URL containing %0a once put a real newline
// into the shift's log where a caller could append forged lines. An id that is
// not a YouTube id is not kept, and that is cheaper than defending every
// consumer against every character.
func TestExtractVideoIDRejectsControlCharacters(t *testing.T) {
	if got := extractVideoID("https://attacker.example/live/x%0aforged-log-line"); got != "" {
		t.Errorf("a newline-carrying id was kept: %q", got)
	}
	if got := extractVideoID("https://attacker.example/live/x%0d%0asecond"); got != "" {
		t.Errorf("a CR-carrying id was kept: %q", got)
	}
	if got := extractVideoID("https://www.youtube.com/watch?v=abc%00def%09ghi"); got != "" {
		t.Errorf("a control-character id was kept: %q", got)
	}
	// And the ordinary shapes still work, which is the other half of the guard:
	// a filter that rejects everything is not a guard, it is a blank.
	for _, want := range []struct{ in, out string }{
		{"https://www.youtube.com/watch?v=VGnFLdQW39A", "VGnFLdQW39A"},
		{"https://youtu.be/VGnFLdQW39A", "VGnFLdQW39A"},
		{"https://www.youtube.com/live/VGnFLdQW39A", "VGnFLdQW39A"},
		{"VGnFLdQW39A", "VGnFLdQW39A"},
		{"https://www.youtube.com/embed/a_-b1234567", "a_-b1234567"},
	} {
		if got := extractVideoID(want.in); got != want.out {
			t.Errorf("extractVideoID(%q) = %q, want %q", want.in, got, want.out)
		}
	}
	// The un-cleaned parse still finds the hostile id, which is the point of
	// keeping the two halves separate: the extraction is not what protects the
	// log, the allow-list is.
	if got := youtubeVideoID("https://attacker.example/live/x%0aforged"); !strings.Contains(got, "\n") {
		t.Errorf("the un-cleaned parse returned %q, want the raw newline it found", got)
	}
}
