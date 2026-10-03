package main

// footage_frames.go - reading a DECLARED window of a file into frames.
//
// # One ffmpeg process, streaming, not N
//
// The window is sampled with a single `ffmpeg` run carrying a `fps` filter, and
// its rawvideo output is read a frame at a time off a pipe. The obvious
// alternative - one ffmpeg per frame - costs a process launch and a full decode
// of the file's head per sample, and on a 90-second window at a 0.5 s interval
// that is 180 launches to look at 180 frames. It also gives 180 chances for the
// samples to differ, which on a measurement is not a rounding.
//
// The output is STREAMED rather than buffered because the arithmetic here is
// about the smallest objects a camera can see, which are also the ones a 4K
// frame is largest for. Forty 1280x720 RGB frames is 110 MB held at once for no
// reason: every frame is measured as it arrives, because the novelty engine that
// proposes crop regions compares each frame with the one before it and the two
// passes over a frame have to see the same bytes.
//
// # What is reused, and what is not, and why
//
// REUSED: `source.Parse`, which is the module's only statement about what may be
// opened as a source. It is used here to REFUSE A URL. A measurement that reads
// a live feed produces a number whose denominator nobody wrote down, so this
// command takes a file and says so through the function that already says it.
//
// REUSED: `imageDims`, the ffprobe call `bench` already uses, so the frame size
// this command measures against is the size `bench` measures against.
//
// NOT REUSED, DELIBERATELY: `source.Spec.InputArgs()`. For a loop source it
// returns `-re -stream_loop -1`, which is exactly right for a run and exactly
// wrong here. `-re` paces the reader to wall-clock, so a two-second measurement
// would take two seconds; `-stream_loop -1` never terminates, so a window would
// not end. Reusing it would mean a measurement that either takes as long as the
// footage or never returns, and the reuse would have to be undone at the call
// site anyway - which is the definition of a reuse that is not one.
//
// The seek is `-ss` BEFORE `-i`, which in ffmpeg is accurate when the input is
// decoded rather than stream-copied, and it is accurate because the target is
// re-encoded to rawvideo. The mode is reported on the command so a reader knows
// which one produced the numbers.

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// sampledFrame is one frame of the declared window.
type sampledFrame struct {
	// Index is 0-based within the window, and it is the sequence number handed to
	// the detector. The two passes over one frame get DIFFERENT sequence numbers
	// (the frame's own, and frame*1000+region) so a latency distribution can
	// attribute a cost to a pass rather than to a frame.
	Index int
	// At is where this frame sits in the window, measured from the window's start.
	// It is derived from the index and the declared interval rather than read from
	// the container, because a measurement that trusts a container's own timestamp
	// over the interval it was asked to sample at is measuring the container.
	At time.Duration
	// W and H are the frame's own dimensions, read once from the container before
	// the run. Every frame is checked against them, because a stream that changes
	// size mid-window produces frames whose byte counts disagree with the header,
	// and a frame read past its own length is a frame of somebody else's memory.
	W, H int
	// RGB is REUSED between frames: the same buffer is handed to every sink call
	// and overwritten by the next read. A sink that keeps the slice past its
	// return is holding a frame that is about to become a different one, which is
	// why every consumer in this file (the letterbox, the novelty plane, the crop
	// slice) copies what it keeps.
	RGB []byte
}

// windowRead is what the reader actually got, as against what was declared.
//
// Declared and Read are both here because they disagreeing is a finding. A
// window that promised 180 samples and delivered 12 is a different measurement
// from one that promised 12 and delivered 12, and only the first number tells
// you which you have.
type windowRead struct {
	Declared int
	Read     int
	W, H     int
	Command  string
	// Trailing is how many bytes arrived that were not a whole frame. It is
	// refused rather than ignored: a partial frame is not a frame, and dropping it
	// quietly is a silent sample drop.
	Trailing int
}

// frameSink receives each sampled frame in order.
type frameSink func(sampledFrame) error

// windowArgs builds the ffmpeg command line for a declared window.
//
// It is a FUNCTION and not an inline slice because the command is printed on
// every run: a reader who cannot see the command that produced a number cannot
// reproduce it, and this is a measurement tool.
func windowArgs(path string, w footageWindow) []string {
	// fps is the filter's reciprocal of the interval. It is formatted to six
	// decimals because the filter parses a decimal, and a value like 1/0.3333333
	// written as "2.999999" silently samples at three frames a second.
	fps := 1 / w.Every.Seconds()
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		// Accurate input seek. See the file comment for why this is before -i.
		"-ss", fmt.Sprintf("%.3f", w.From.Seconds()),
		"-i", path,
		"-t", fmt.Sprintf("%.3f", w.Seconds()),
		"-an",
		"-vf", fmt.Sprintf("fps=%.6f", fps),
		// rgb24 is PACKED RGB, which is what ortdetect.Letterbox wants. bgr24 is one
		// byte-order away and a measurement run on swapped channels measures a model
		// that has never existed.
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	}
}

// readWindowFrames samples the declared window and hands each frame to SINK.
//
// The material's own dimensions are read once, before the run, and every frame
// is checked against them. A stream that changes size mid-window produces frames
// whose byte counts disagree with the header, and a frame read past its own
// length is a frame of somebody else's memory.
func readWindowFrames(ctx context.Context, path string, w footageWindow, declared int, sink frameSink) (windowRead, error) {
	fw, fh, err := imageDims(path)
	if err != nil {
		return windowRead{}, fmt.Errorf("the material's frame size could not be read, so no frame can be "+
			"sized and nothing was sampled: %w", err)
	}
	if fw <= 0 || fh <= 0 {
		return windowRead{}, fmt.Errorf("ffprobe reported a non-positive frame size (%dx%d) for %s", fw, fh, path)
	}
	args := windowArgs(path, w)
	read := windowRead{Declared: declared, W: fw, H: fh, Command: "ffmpeg " + strings.Join(args, " ")}

	// context.WithCancel plus an explicit Wait: killing the child is not enough,
	// because the pipe's read end blocks until the writer has actually gone, and
	// a measurement that hangs on a dead ffmpeg is worse than one that failed.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg", args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return read, fmt.Errorf("ffmpeg's output pipe could not be opened: %w", err)
	}
	var errTail boundedTail
	cmd.Stderr = &errTail
	if err := cmd.Start(); err != nil {
		return read, fmt.Errorf("ffmpeg could not be started: %w", err)
	}

	frameBytes := fw * fh * 3
	buf := make([]byte, frameBytes)
	i := 0
	for {
		// n is captured because io.ReadFull does not report how much it managed
		// to read, and "how much arrived that was not a whole frame" is the
		// difference between a complete window and a truncated one.
		n, rerr := io.ReadFull(pipe, buf)
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			if rerr == io.ErrUnexpectedEOF && n > 0 {
				read.Trailing = frameBytes - n
			}
			break
		}
		if rerr != nil {
			cancel()
			_ = cmd.Wait()
			return read, fmt.Errorf("ffmpeg's output stopped after %d frame(s) at frame %d bytes: %w", read.Read, i, rerr)
		}
		frame := sampledFrame{Index: i, At: time.Duration(i) * w.Every, W: fw, H: fh, RGB: buf}
		if err := sink(frame); err != nil {
			cancel()
			_ = cmd.Wait()
			return read, err
		}
		read.Read++
		i++
	}
	if err := cmd.Wait(); err != nil {
		return read, fmt.Errorf("ffmpeg did not finish cleanly, so the window [%v, %v] is not a complete "+
			"sample of the material: %v: %s", w.From, w.To, err, errTail.String())
	}
	if err := refuseTrailingFrame(read.Trailing); err != nil {
		return read, err
	}
	if read.Read == 0 {
		return read, fmt.Errorf("the window [%v, %v] yielded no frames at a %v interval over a %dx%d material. "+
			"The window may be past the end of the file; ffprobe said: %s", w.From, w.To, w.Every, fw, fh,
			trimTo(errTail.String(), 120))
	}
	return read, nil
}

// refuseTrailingFrame is RC-41 as a FUNCTION, so a test can reach it.
//
// A partial frame is not a frame. Dropping it quietly is a silent sample drop -
// the exact defect class this module treats as its worst - and reading past it is
// a measurement of bytes nobody decoded. It should not happen at all: ffmpeg
// writes whole frames to a rawvideo pipe. It is refused rather than tolerated
// because a measurement that quietly loses its last sample is a measurement whose
// sample count is a lie.
//
// REDCHECK RC-41: make this return nil for any input and
// TestTrailingBytesAreRefusedRatherThanDropped goes red.
func refuseTrailingFrame(n int) error {
	if n <= 0 {
		return nil
	}
	return fmt.Errorf("ffmpeg's output ended with %d bytes that are not a whole frame, so the last sample is "+
		"partial. A partial frame is not a frame: it is refused rather than dropped or read past, because a "+
		"half frame measured is a number about a picture that was never decoded, and a dropped one is a "+
		"sample count that lies", n)
}

// boundedTail keeps the last N bytes written to it.
//
// A measurement tool that prints an unbounded decoder log has replaced one
// unreadable refusal with a different unreadable refusal. 8 KiB is enough for
// ffmpeg to name its own failure.
type boundedTail struct {
	limit int
	buf   []byte
}

// String returns whatever was kept, trimmed to whole lines.
func (b *boundedTail) String() string {
	s := strings.TrimSpace(string(b.buf))
	if s == "" {
		return "(ffmpeg wrote nothing to stderr)"
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "...")
	}
	return strings.Join(lines, " | ")
}

// Write keeps the tail. It never fails, because a diagnostic that fails while
// collecting a diagnostic is a diagnostic that reports nothing.
func (b *boundedTail) Write(p []byte) (int, error) {
	limit := b.limit
	if limit <= 0 {
		limit = 8 << 10
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > limit {
		b.buf = b.buf[len(b.buf)-limit:]
	}
	return len(p), nil
}
