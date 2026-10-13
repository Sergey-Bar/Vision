package main

// Small helpers shared by the subcommands, and the boundary they sit on.
//
// # Why these read the environment at all
//
// `internal/config` is the only thing in the module that reads the environment,
// and nothing else is allowed to. These helpers are the exception, and they are the
// exception because a DIAGNOSTIC tool has to be able to reach a model whose
// variables are not yet in the schema — which is the whole situation
// `fetch-runtime` and `fetch-groundtruth` exist for. Routing them through
// `config.Load` would mean the tool that diagnoses a misconfiguration cannot
// itself be configured for the thing it is diagnosing.
//
// The list is closed and every entry names a `SP_POC_*` variable the schema also
// knows about, so a reader can see these are the same variables and not a second
// vocabulary. Nothing outside `cmd/` calls them.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// binName is a built binary's file name on THIS platform.
//
// Windows carries the .exe suffix and the other two platforms do not, and the
// demo used to spell `pocctl.exe` as a literal — which meant the headline
// command of the module built `bin/pocctl.exe` on Linux, into a directory whose
// binary nobody could then find, and reported a missing file. One function, so
// the spelling exists once.
func binName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// findModuleRoot walks up from the working directory to the directory holding
// go.mod.
//
// Every documented invocation runs from the module root, and `repoRoot()`
// checks that and WARNS when it is not true — a warning is the right answer for
// a command that builds binaries. But a command that READS a provenance record
// must not refuse because it was launched from a subdirectory or, as tests do,
// from inside its own package directory, and the answer is to find the root
// rather than to be handed it.
//
// It returns "" when there is no go.mod above here, and every caller has a
// fallback for that: a refusal naming what it could not read, never a zero.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// sameFile reports whether two paths name the same file.
//
// It compares ABSOLUTE paths rather than strings, because the interesting case is
// `testdata/groundtruth.json` named as `./testdata/groundtruth.json`, and a string
// comparison would say those are different files when they are the same one. That
// is precisely the mistake that would let a command overwrite a file it was
// supposed to be checking against.
func sameFile(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// envOr reads a fallback from the environment, treating an empty value as unset.
//
// Empty-as-unset is the module's rule, and it is why a variable set to nothing has
// said nothing. It is applied here for the same reason it is applied in `config`.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, flagValue string, fallback int) int {
	v := envOr(name, "")
	if v == "" {
		v = strings.TrimSpace(flagValue)
	}
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(name, flagValue string, fallback float64) float64 {
	v := envOr(name, "")
	if v == "" {
		v = strings.TrimSpace(flagValue)
	}
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// licenceOrUnrecorded says so in words rather than printing an empty string.
//
// A log line reading `licence ` tells nobody anything, and a reader who does not
// notice the gap will assume there was no gap.
func licenceOrUnrecorded(l string) string {
	if strings.TrimSpace(l) == "" {
		return "NOT RECORDED"
	}
	return l
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// readImageRGB decodes an image file to packed RGB.
//
// ffmpeg rather than image/png, and the reason is that the still set is JPEG and
// the image package's JPEG decoder is fine but PNG is not what is here — so this
// goes through the one decoder that handles both, and it is the same decoder the
// recorder already uses, which means the pixels the benchmark measures are the
// pixels the pipeline would have seen.
//
// The channel order matters: `-pix_fmt rgb24` gives PACKED RGB, which is what the
// detector's letterbox wants. `rgb24` and `bgr24` are one byte-order apart and a
// benchmark run on swapped channels measures a model that has never existed.
func readImageRGB(path string) ([]byte, int, int, error) {
	w, h, err := imageDims(path)
	if err != nil {
		return nil, 0, 0, err
	}
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("ffmpeg could not decode it: %w", err)
	}
	want := w * h * 3
	if len(out) < want {
		return nil, 0, 0, fmt.Errorf("ffmpeg returned %d bytes for a %dx%d image, which needs %d. "+
			"A short buffer is refused rather than read past: a decoder handed zeros produces a "+
			"clean picture of an empty road", len(out), w, h, want)
	}
	return out[:want], w, h, nil
}

func imageDims(path string) (int, int, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", path).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe could not read the image: %w", err)
	}
	// The FIRST line only: ffprobe emits one `WIDTHxHEIGHT` per stream it reports.
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	parts := strings.Split(strings.TrimSpace(line), "x")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("ffprobe said %q, which is not WIDTHxHEIGHT", line)
	}
	var w, h int
	if _, err := fmt.Sscan(parts[0], &w); err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscan(parts[1], &h); err != nil {
		return 0, 0, err
	}
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("ffprobe reported a non-positive frame size")
	}
	return w, h, nil
}
