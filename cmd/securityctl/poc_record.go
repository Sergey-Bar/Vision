package main

import (
	"fmt"
	"os"
)

// clipRecorder writes the session's NAL units to a .h264 file as they arrive.
//
// It is here rather than on either source because recording is a property of
// the run, not of the transport: a capture taken from a camera and a capture
// taken from a recording are the same artefact, and having one code path for it
// is what makes `--record` mean the same thing in both cases. Wiring it into
// only the live source produced an empty file for a replay, which is the worst
// kind of wrong - it looks like a flag that works.
type clipRecorder struct {
	f       *os.File
	limit   int64
	written int64
	// stopped carries the reason recording ended, or empty while it runs. A
	// recording that stopped is reported as a named degradation, because a
	// truncated fixture replayed later produces a truncated clip and the
	// truncation is invisible in the artefact.
	stopped string
}

func openClipRecorder(path string, limitMB int64) (*clipRecorder, error) {
	if path == "" {
		return &clipRecorder{}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("poc: create --record: %w", err)
	}
	return &clipRecorder{f: f, limit: limitMB << 20}, nil
}

// write appends one NAL with a four-byte start code.
//
// The framing is written by this code rather than assumed from the input: an
// RTSP session delivers NAL units with no framing at all, and a file that
// carries whatever start codes the source happened to use is not an elementary
// stream anybody else can read.
func (r *clipRecorder) write(nal []byte) {
	if r.f == nil || r.stopped != "" {
		return
	}
	if r.limit > 0 && r.written+int64(len(nal))+4 > r.limit {
		r.halt(fmt.Sprintf("recording stopped at the --record-max-mb cap of %d MiB; the capture itself continued",
			r.limit>>20))
		return
	}
	sc := [4]byte{0, 0, 0, 1}
	if _, err := r.f.Write(sc[:]); err != nil {
		r.halt("recording stopped: " + err.Error())
		return
	}
	n, err := r.f.Write(nal)
	if err != nil {
		r.halt("recording stopped: " + err.Error())
		return
	}
	r.written += int64(n) + 4
}

func (r *clipRecorder) halt(reason string) {
	if r.stopped == "" {
		r.stopped = reason
	}
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
}

// Close finishes the file and reports anything that stopped it early.
func (r *clipRecorder) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	if err != nil {
		return fmt.Errorf("poc: close the recording: %w", err)
	}
	return nil
}

// Bytes is how much was written, for the report.
func (r *clipRecorder) Bytes() int64 { return r.written }
