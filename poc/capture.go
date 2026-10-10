package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync/atomic"
)

// frameCapture reads decoded RGB frames straight out of ffmpeg for one
// resolved stream URL.
//
// The player and this capture watch the SAME stream: the browser plays it
// through the YouTube IFrame, and the pipeline samples its pixels here. One
// fixed geometry (frameW×frameH) for both, because the zone polygons and the
// overlay are drawn in that space.
type frameCapture struct {
	cmd    *exec.Cmd
	frames chan []byte
	read   atomic.Int64
	err    atomic.Value // string; set when ffmpeg exited unexpectedly
}

// startFrameCapture spawns ffmpeg on the resolved URL and yields fixed-size
// rgb24 frames at fps. It returns an error naming the missing binary rather
// than a channel that never produces.
func startFrameCapture(ctx context.Context, hlsURL string, fps float64) (*frameCapture, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg is not on PATH (needed to decode the live stream): %w", err)
	}
	if fps <= 0 {
		fps = 1
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", hlsURL,
		"-an", "-sn",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=fast_bilinear,fps=%g", frameW, frameH, fps),
		"-pix_fmt", "rgb24",
		"-f", "rawvideo",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdout: %w", err)
	}
	cmd.Stderr = &lineLogger{prefix: "ffmpeg: "}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg could not start: %w", err)
	}

	fc := &frameCapture{cmd: cmd, frames: make(chan []byte, 1)}
	go fc.readLoop(ctx, stdout)
	return fc, nil
}

func (fc *frameCapture) readLoop(ctx context.Context, r io.Reader) {
	defer close(fc.frames)
	frameSize := frameW * frameH * 3
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		buf := make([]byte, frameSize)
		if _, err := io.ReadFull(r, buf); err != nil {
			if ctx.Err() == nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				fc.err.Store(fmt.Sprintf("frame read ended: %v", err))
				log.Printf("capture: %v", err)
			}
			return
		}
		fc.read.Add(1)
		select {
		case fc.frames <- buf:
		case <-ctx.Done():
			return
		}
	}
}

// lineLogger funnels a child process's stderr into the log without letting it
// block on a full pipe.
type lineLogger struct {
	prefix string
	buf    []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := indexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		log.Printf("%s%s", l.prefix, string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 4096 {
		log.Printf("%s%s", l.prefix, string(l.buf))
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
