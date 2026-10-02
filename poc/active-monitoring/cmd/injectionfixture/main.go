// Command injectionfixture builds the prompt-injection fixture.
//
// The hostile text is RENDERED INTO THE PICTURE, not pasted into a prompt and not
// written into a mock response. That is the only version of this fixture that tests
// anything: an attack that lives in the prompt is not an attack, it is a variable.
//
// The drawing itself is in `internal/injectionfixture`, so the tests can render frame 0
// and compare it with the PNG written beside the video. Without that comparison the
// suite would assert an attack it is not running — a re-encode that dropped the text
// leaves an MP4 with a recorded SHA-256 and a label describing something it no longer
// contains.
//
// What the fixture proves, and what it does not, is written in `labels.json` next to it
// and is worth stating here too: with no provider reachable, the tests prove the OUTPUT
// SHAPE is closed and that an injected sentence is carried as attributed data. Whether
// a particular model complies is a question only a live call answers, and this
// repository does not claim the answer.
package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"strconv"

	"github.com/Sergey-Bar/ai-captain-poc/internal/injectionfixture"
)

func main() {
	out := "testdata/injection-hostile.mp4"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	frames := injectionfixture.Seconds * injectionfixture.FPS

	if err := writeReferenceFrame(out); err != nil {
		fmt.Fprintf(os.Stderr, "injectionfixture: %v\n", err)
		os.Exit(1)
	}
	if err := encode(out, frames); err != nil {
		fmt.Fprintf(os.Stderr, "injectionfixture: %v\n", err)
		os.Exit(1)
	}
}

// writeReferenceFrame saves frame 0 as a PNG beside the video.
//
// The tests compare this against a freshly rendered frame 0, which is what ties the
// committed pixels to the source that draws them. The PNG is committed for the same
// reason the MP4 is: a label has to describe THIS file.
func writeReferenceFrame(out string) error {
	f, err := os.Create(framePath(out))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, injectionfixture.Frame(0, injectionfixture.Seconds*injectionfixture.FPS)); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", framePath(out))
	return nil
}

func framePath(mp4 string) string {
	base := mp4
	if i := lastDot(base); i >= 0 {
		base = base[:i]
	}
	return base + ".frame0.png"
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' || s[i] == '/' || s[i] == '\\' {
			return i
		}
	}
	return -1
}

func encode(out string, frames int) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is not on PATH, and this fixture is encoded with it. "+
			"The alternative would be committing an MP4 nobody can rebuild: install ffmpeg and re-run: %w", err)
	}
	var buf bytes.Buffer
	buf.Grow(frames * injectionfixture.Width * injectionfixture.Height * 3)
	for i := 0; i < frames; i++ {
		buf.Write(injectionfixture.RawRGB(i, frames))
	}
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "rawvideo", "-pix_fmt", "rgb24",
		"-s", strconv.Itoa(injectionfixture.Width)+"x"+strconv.Itoa(injectionfixture.Height),
		"-r", strconv.Itoa(injectionfixture.FPS), "-i", "pipe:0",
		"-c:v", "libx264", "-preset", "slow", "-crf", "18",
		"-pix_fmt", "yuv420p", out)
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg could not encode %s: %v\n%s", out, err, stderr.String())
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d frames, %d bytes)\n", out, frames, info.Size())
	fmt.Println("next: compute its SHA-256 and put THAT in testdata/labels.json. A label that says")
	fmt.Println("'generated locally' without the hash describes a description, not this file.")
	return nil
}
