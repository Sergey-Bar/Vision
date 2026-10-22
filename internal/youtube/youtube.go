// Package youtube contains small YouTube URL helpers for the live POC.
package youtube

import (
	"errors"
	"regexp"
)

// videoIDPatterns are the shapes a YouTube id arrives in.
//
// The BOUNDARY after the eleven characters is not decoration. Without it, a
// fourteen-character id matches its first eleven and the helper returns a
// DIFFERENT video -- and the caller then requests a video the operator did not
// paste. A YouTube id is exactly eleven characters, so anything longer is not
// an id and the honest answer is to refuse it.
//
// The boundary is "a character that is not part of an id, or the end of the
// string", so `watch?v=ID&t=30` and `youtu.be/ID` both work.
var videoIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/embed/|youtube\.com/live/)([A-Za-z0-9_-]{11})(?:[^\w-]|$)`),
	regexp.MustCompile(`^([A-Za-z0-9_-]{11})$`),
}

// ExtractVideoID accepts the common YouTube URL shapes plus a raw 11-byte id.
func ExtractVideoID(raw string) (string, error) {
	for _, pattern := range videoIDPatterns {
		match := pattern.FindStringSubmatch(raw)
		if len(match) == 2 {
			return match[1], nil
		}
	}
	return "", errors.New("invalid YouTube video id or URL")
}
