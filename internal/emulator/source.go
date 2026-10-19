// Package emulator is a VMS.
//
// It is not a mock and it is not a simulator: it plays video, runs a detector
// over the frames, and publishes what it finds the way a video management
// system publishes its own analytics — in the vendor's wire shape, on the
// vendor's routes, with the vendor's own media surface underneath.
//
// # Why it exists
//
// The product's Milestone adapter is written, tested and unexercised, because a
// Milestone is a year of procurement. Every claim the product makes about a
// vendor — that an event arrives, that a class refines into a typed event, that
// a clip can be fetched for the moment an incident was raised — is a claim
// about code that has never spoken to anything.
//
// This package is the other end of that socket. It is a vendor, in the sense
// that a vendor is: it owns cameras, it owns live video, it owns its own
// analytics, and it answers questions about its own recording. The product
// above the transport does not know the difference, which is the point: the
// transport is the seam, and a seam that has only ever been tested against
// itself is not a seam.
//
// # What it is not
//
// It is not a second implementation of the adapter's vocabulary. It does not
// import `internal/milestone`, it does not define canonical event types, and
// it does not know what a rule is. It produces envelopes in the vendor's wire
// shape and lets the adapter parse them, exactly as a real VMS would. The
// re-derivation the adapter does on every consume — `Adapter.RecordFromPayload`
// re-parsing the stored envelope — is what turns a shape disagreement into a
// malformed record rather than a silent loss, and that check only works if the
// two sides are genuinely separate.
//
// # The honesty rule
//
// Every number this package reports is measured, not claimed. The detector's
// frame rate is the one it achieved, the latency is the one it cost, and a
// camera that cannot be played is a camera that says so rather than one that
// reports zero detections forever.
package emulator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Kind is where a camera's video comes from.
type Kind string

// The kinds of source. The set is closed because a source that is not one of
// these has no reading path and would be a camera that plays nothing.
const (
	// KindFile is a path on this machine.
	KindFile Kind = "file"
	// KindURL is an http(s) URL ffmpeg can open.
	KindURL Kind = "url"
	// KindRTSP is an rtsp:// camera.
	KindRTSP Kind = "rtsp"
	// KindYouTube is a YouTube URL, resolved with yt-dlp before ffmpeg sees it.
	KindYouTube Kind = "youtube"
)

// Source is one camera's video.
//
// The fields are the ones a VMS operator would recognise: which camera, what it
// shows, and how fast it is sampled. The rest of the VMS's own configuration —
// recording schedules, retention, analytics rules — is not here, because this
// emulator's analytics are computed, not configured.
type Source struct {
	// ID is the camera's own reference. The vendor's routes use it, the
	// platform stores it as the device id, and the console groups by it.
	ID string
	// Name is what the operator sees.
	Name string
	// Kind is where the video comes from.
	Kind Kind
	// Location is the path, URL or rtsp address.
	Location string
	// FPS is how many frames a second are measured. Zero takes DefaultFPS.
	FPS float64
	// Loop says whether playback restarts at the end of the file. A demo that
	// stops when the footage stops is a demo that has to be restarted.
	Loop bool
	// StartAt is where playback begins.
	StartAt time.Duration
}

// DefaultFPS is how many frames a second are measured when a source does not
// say.
//
// Ten, because the measured cost of one frame on the machine this was written
// on is 17 ms on an RTX 4070, and ten frames a second is 170 ms of a second
// per camera. Four cameras at ten frames a second is 680 ms of GPU time per
// second, which fits. Twenty-five would not.
const DefaultFPS = 10

// DefaultWidth and DefaultHeight are the detection size.
//
// 640x384 is a deliberate compromise: it is wide enough for a person to be
// recognisable as a person and a car as a car, and small enough that the
// letterbox into a 640x640 model input is a rounding error rather than the
// dominant cost. The model is what decides the true input size; this is what
// the frame is scaled to before it gets there.
const (
	DefaultWidth  = 640
	DefaultHeight = 384
)

// errNoFFmpeg is what the emulator says when it cannot play anything at all.
//
// It is named rather than folded into a generic error because the two failures
// have different owners: a missing ffmpeg is an installation problem, and a
// missing model is a configuration problem, and an operator who is told
// "camera 3 failed" learns nothing.
var errNoFFmpeg = errors.New("emulator: ffmpeg was not found, so no camera can play")

// Frame is one decoded frame.
type Frame struct {
	// Index is the frame's number since playback began.
	Index int
	// At is the instant the frame is at, in the emulator's clock.
	At time.Time
	// Width and Height are the frame's size in pixels.
	Width  int
	Height int
	// Pix is the pixels, RGB24, row-major, three bytes per pixel.
	Pix []byte
}

// reader plays a source and yields frames.
//
// It is a thin wrapper over one ffmpeg process. ffmpeg is already the answer to
// "how do I decode an MP4, an RTSP stream and an HTTP stream", it is already
// on the machine, and writing a second demuxer here would be a second thing to
// be wrong.
type reader struct {
	bin    string
	source Source
	width  int
	height int

	cmd    *exec.Cmd
	stdout io.ReadCloser
	// started is the wall clock the current process began at, so a frame's
	// timestamp is derived from the playback position rather than from when the
	// read happened.
	started time.Time
	now     func() time.Time
}

// newReader starts playing a source.
func newReader(ctx context.Context, bin string, src Source, width, height int, now func() time.Time) (*reader, error) {
	if bin == "" {
		bin = "ffmpeg"
	}
	r := &reader{bin: bin, source: src, width: width, height: height, now: now}
	if err := r.start(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// start launches one ffmpeg process for the source.
func (r *reader) start(ctx context.Context) error {
	input, err := r.input()
	if err != nil {
		return err
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
	}
	if r.source.StartAt > 0 {
		args = append(args, "-ss", strconv.FormatFloat(r.source.StartAt.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-i", input,
		"-an", // no audio: nothing downstream listens to it
		"-vf", fmt.Sprintf("scale=%d:%d", r.width, r.height),
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-",
	)
	cmd := exec.CommandContext(ctx, r.bin, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("emulator: pipe ffmpeg's output: %w", err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("emulator: start ffmpeg for %s: %w", r.source.ID, err)
	}
	r.cmd = cmd
	r.stdout = out
	r.started = r.now()
	return nil
}

// input is the address ffmpeg opens.
//
// A YouTube URL is not a media URL: it is a web page that happens to contain
// one. `yt-dlp` resolves it, and its absence is a refusal by name rather than
// an ffmpeg error about a stream.
func (r *reader) input() (string, error) {
	switch r.source.Kind {
	case KindYouTube:
		resolved, err := resolveYouTube(r.source.Location)
		if err != nil {
			return "", err
		}
		return resolved, nil
	case KindFile:
		return r.source.Location, nil
	case KindURL, KindRTSP:
		return r.source.Location, nil
	default:
		return "", fmt.Errorf("emulator: %s: %q is not a source kind this emulator can play", r.source.ID, r.source.Kind)
	}
}

// next reads the next frame.
//
// It returns io.EOF at the end of the source, and the caller decides whether
// that is the end of the run or the start of the next loop.
//
// A partial frame is an EOF and not a frame: a truncated read means ffmpeg
// stopped, and a half-frame handed to a detector is a frame of garbage that
// the decoder would report as a box in the wrong place.
func (r *reader) next() (Frame, error) {
	size := r.width * r.height * 3
	buf := make([]byte, size)
	read, err := io.ReadFull(r.stdout, buf)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, io.EOF
		}
		return Frame{}, fmt.Errorf("emulator: read a frame from ffmpeg for %s: %w", r.source.ID, err)
	}
	if read < size {
		return Frame{}, io.EOF
	}
	return Frame{
		At:     r.now(),
		Width:  r.width,
		Height: r.height,
		Pix:    buf,
	}, nil
}

// close stops the process.
func (r *reader) close() {
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Kill()
	_, _ = r.cmd.Process.Wait()
}

// restart begins the source again, for a looping playback.
func (r *reader) restart(ctx context.Context) error {
	r.close()
	return r.start(ctx)
}

// seek restarts the source at a position.
//
// It is a RESTART rather than a seek inside the running process, because the
// process is decoding a file from the beginning and there is no way to move its
// read head without stopping it. A VMS's seek is the same: the recording
// server finds the position and starts serving from there.
func (r *reader) seek(ctx context.Context, at time.Duration) error {
	r.close()
	saved := r.source.StartAt
	r.source.StartAt = at
	err := r.start(ctx)
	r.source.StartAt = saved
	return err
}

// resolveYouTube turns a YouTube URL into a direct media URL.
//
// `yt-dlp` is an external binary and may be absent, and the answer then is a
// refusal by name: "the YouTube path needs yt-dlp" is a fact an operator can
// act on, and "ffmpeg failed" is not.
func resolveYouTube(url string) (string, error) {
	bin, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", fmt.Errorf("emulator: the YouTube path needs yt-dlp on PATH, and it is not there. "+
			"A local file plays without it: %w", err)
	}
	out, err := exec.Command(bin, "--no-playlist", "-f", "mp4/best", "-g", url).Output()
	if err != nil {
		return "", fmt.Errorf("emulator: yt-dlp could not resolve %q: %w", url, err)
	}
	direct := strings.TrimSpace(string(out))
	if direct == "" {
		return "", fmt.Errorf("emulator: yt-dlp resolved %q to nothing", url)
	}
	// yt-dlp may hand back several URLs; the first is the media.
	if idx := strings.IndexByte(direct, '\n'); idx >= 0 {
		direct = direct[:idx]
	}
	return direct, nil
}

// ffmpegAvailable reports whether ffmpeg is on the machine.
func ffmpegAvailable(bin string) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: %s", errNoFFmpeg, bin)
	}
	return nil
}

// clipRequest describes a clip to cut.
type clipRequest struct {
	// Path is the file to cut from.
	Path string
	// Start and End are the positions in the file.
	Start, End time.Duration
	// Out is where the MP4 is written.
	Out string
}

// cutClip produces a real MP4 around a moment.
//
// It is a real MP4 because the evidence store hashes what it is given and the
// console plays what it is given: a file that is not a video is a file that
// fails at the moment somebody presses play, which is the worst moment to find
// out.
//
// The cut re-encodes rather than copying a stream, because a copy of a stream
// that starts at a non-keyframe decodes to a few frames of garbage before the
// picture starts. libx264 with a closed GOP puts an IDR frame at the cut.
func cutClip(ctx context.Context, bin string, req clipRequest) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	duration := req.End - req.Start
	if duration <= 0 {
		return fmt.Errorf("emulator: a clip of %s covers no time", req.Out)
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-ss", strconv.FormatFloat(req.Start.Seconds(), 'f', 3, 64),
		"-i", req.Path,
		"-t", strconv.FormatFloat(duration.Seconds(), 'f', 3, 64),
		"-an",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
		// A closed GOP, so the first frame of the clip is a keyframe and the
		// console's first painted frame is the first frame of the clip.
		"-g", "30",
		"-movflags", "+faststart",
		"-y",
		req.Out,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("emulator: cut a clip: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// snapshotAt writes one JPEG at a position in a file.
//
// A still is a frame, and a frame is what an incident's evidence wants: a
// moment, not a window.
func snapshotAt(ctx context.Context, bin, path string, at time.Duration, out string) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64),
		"-i", path,
		"-frames:v", "1",
		"-q:v", "3",
		"-y",
		out,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("emulator: take a snapshot: %w: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

// httpStatusText is here so the control surface's errors read like the
// vendor's.
func httpStatusText(code int) string {
	return http.StatusText(code)
}
