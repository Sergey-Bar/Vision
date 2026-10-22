package youtube_test

// The YouTube URL helper.
//
// It is a twenty-line demo helper, and it is TESTED rather than deleted
// because the alternative is worse than a test: a helper that extracts a video
// id from a URL the operator pasted is the one place a pasted string becomes a
// request to a third party, and "we deleted it" is a weaker answer than "here
// is what it accepts".

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/youtube"
)

func TestExtractVideoIDAcceptsTheCommonShapes(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=" + id,
		"https://youtu.be/" + id,
		"https://www.youtube.com/embed/" + id,
		"https://www.youtube.com/live/" + id,
		id,
	} {
		got, err := youtube.ExtractVideoID(raw)
		if err != nil {
			t.Errorf("ExtractVideoID(%q) failed: %v", raw, err)
			continue
		}
		if got != id {
			t.Errorf("ExtractVideoID(%q) = %q, want %q", raw, got, id)
		}
	}
}

func TestExtractVideoIDRefusesWhatItCannotUse(t *testing.T) {
	for _, raw := range []string{
		"",
		"not a url",
		"https://www.youtube.com/watch?v=tooshort",
		"https://www.youtube.com/watch?v=waaaaaaaaaaaaaytoolong",
	} {
		if got, err := youtube.ExtractVideoID(raw); err == nil {
			t.Errorf("ExtractVideoID(%q) = %q, and it should have refused", raw, got)
		}
	}
}
