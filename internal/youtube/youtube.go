// Package youtube contains small YouTube URL helpers for the live POC.
package youtube

import (
	"errors"
	"regexp"
)

var videoIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/embed/|youtube\.com/live/)([A-Za-z0-9_-]{11})`),
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
