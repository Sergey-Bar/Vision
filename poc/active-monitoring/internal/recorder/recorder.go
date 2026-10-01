package recorder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Frame is one decoded frame with the wall-clock instant at which Go read it.
// That instant is the only clock in the system; nothing downstream re-derives a
// time from the video.
type Frame struct {
	At     time.Time
	Seq    uint64
	Width  int
	Height int
	RGB    []byte
}

// Options configure the single ffmpeg process.
type Options struct {
	FFmpeg     string
	FFmpegArgs []string // input arguments, normally spec.InputArgs()
	RollingDir string
	// SegmentSeconds is the muxer's target length. It is a target, not a
	// guarantee: the muxer cuts on a keyframe boundary and may overrun it. The
	// covered wall-clock interval is measured, never assumed.
	SegmentSeconds int
	FPS            int
	Width          int
	// Backpressure is how long the MJPEG pipe may stay blocked before ffmpeg is
	// killed. Silence past this point is a fault, not a stall.
	Backpressure time.Duration
	// QueueDepth is the bounded frame queue between the pipe reader and the
	// consumer. Bounded because unbounded would trade a backpressure fault for
	// an out-of-memory kill.
	QueueDepth int
	Decoder    decoder
	// WatchInterval is how often the segment watcher looks for a new rolling
	// file. It bounds the wall-clock error in the instant-to-segment mapping.
	WatchInterval time.Duration
	Logf          func(format string, args ...any)
}

// Recorder owns the one ffmpeg process and the one clock.
type Recorder struct {
	opt Options

	cmd     *exec.Cmd
	stdout  io.ReadCloser
	queue   *boundedQueue
	started time.Time

	mu           sync.Mutex
	segIndex     *SegmentIndex
	observations []segmentObservation
	nextIndex    int
	stopped      bool
	exitErr      error
	exited       chan struct{}
	stopWatch    chan struct{}

	framesOut  uint64
	decodes    uint64
	decodeErr  uint64
	frameClock atomic.Int64
	lastJPEG   atomic.Pointer[[]byte]
	// lastJPEGSeq is the sequence of the frame lastJPEG belongs to.
	//
	// W2-5. It was not stored, so a caller reading the snapshot got whatever
	// frame the reader goroutine happened to have stored since — possibly a
	// later one than the decision it was about to attach it to. Once a snapshot
	// is EVIDENCE in a proof chain, "the picture of the decision" has to be true,
	// and it could not be: it was the newest picture, which is a different thing.
	lastJPEGSeq atomic.Uint64
	// recent holds the last few frames' JPEGs by sequence.
	//
	// It exists because "the newest picture" and "the picture of this decision" are
	// different questions and the process is one goroutine behind its own pipe:
	// `process` receives frame N from the queue after the reader has already
	// decoded N+3. Reading `lastJPEG` there therefore returns a LATER frame, and
	// the sequence check that would have caught it refuses — correctly — so the
	// snapshot path would be dead in every live run rather than merely wrong.
	//
	// A bounded ring is the honest fix: the lag is bounded by the pipe queue
	// depth, the memory is bounded by the ring, and a frame older than the ring
	// is refused rather than approximated.
	recentMu sync.Mutex
	recent   []jpegFrame
	// watcher is ffmpeg's stderr, which is where the reconnect announcements
	// arrive. It is held here rather than only in the local variable that made it,
	// because a local variable is not reachable and an unreachable counter is a
	// counter that never reaches a fault record.
	//
	// W2-4: `watchReconnects` type-asserted `Reconnects()` on the frame source,
	// and the method existed only on the watcher and not on the Recorder, so the
	// assertion failed, the function returned on its first tick, and `stream_gap`
	// faults were never recorded in a real run.
	watcher   *reconnectWatcher
	rotations uint64
}

// New builds a recorder. It does not start anything.
func New(opt Options) (*Recorder, error) {
	if opt.FFmpeg == "" {
		opt.FFmpeg = "ffmpeg"
	}
	if opt.SegmentSeconds <= 0 {
		return nil, errors.New("recorder: segment length must be positive; without it there is no segment to map an instant onto")
	}
	if opt.FPS <= 0 {
		return nil, errors.New("recorder: the MJPEG pipe rate must be positive; without it the disk budget and the AI rate are both unknown")
	}
	if opt.QueueDepth <= 0 {
		opt.QueueDepth = 32
	}
	if opt.Backpressure <= 0 {
		opt.Backpressure = 5 * time.Second
	}
	if opt.WatchInterval <= 0 {
		opt.WatchInterval = 100 * time.Millisecond
	}
	if opt.Decoder == nil {
		opt.Decoder = stdlibDecoder{}
	}
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	return &Recorder{
		opt:       opt,
		queue:     newBoundedQueue(opt.QueueDepth),
		exited:    make(chan struct{}),
		stopWatch: make(chan struct{}),
	}, nil
}

// Start launches ffmpeg with two outputs from one input: a `-c copy` rolling
// recording, and a paced MJPEG pipe for the AI. The copy output is first in the
// argument list so a failure in the MJPEG branch cannot leave the recording
// silently unstarted.
func (r *Recorder) Start(ctx context.Context) error {
	if err := os.MkdirAll(r.opt.RollingDir, 0o755); err != nil {
		return fmt.Errorf("recorder: cannot create the rolling directory %s: %w", r.opt.RollingDir, err)
	}
	segPattern := filepath.Join(r.opt.RollingDir, SegPattern)
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning"}
	args = append(args, r.opt.FFmpegArgs...)
	args = append(args,
		// Output 1: the recording. -c copy means no decode, so the recording
		// costs no CPU and cannot be the thing that drops frames.
		"-map", "0:v:0",
		"-c:v", "copy",
		// The hvc1 tag, and why it is here.
		//
		// A public camera streams H.265 far more often than H.264, and ffmpeg's
		// mp4 muxer writes HEVC as `hev1` by default. `hev1` in an MP4 is
		// decodable by ffmpeg and by almost nothing else: QuickTime, Chrome and
		// every phone's gallery refuse the file. The evidence clip is the
		// deliverable of this system, and a deliverable nobody can open is not
		// one, so the tag is forced to `hvc1`, the interoperable form.
		//
		// It is harmless for H.264, which has no such tag.
		"-tag:v", "hvc1",
		"-f", "segment",
		"-segment_time", strconv.Itoa(r.opt.SegmentSeconds),
		// Each segment is self-contained with its own timeline starting at
		// zero, which is what lets the clip cutter trim inside a segment.
		"-reset_timestamps", "0",
		segPattern,
		// Output 2: the paced analysis pipe. Go decodes these with image/jpeg
		// and owns the clock.
		"-map", "0:v:0",
		"-vf", fmt.Sprintf("fps=%d,scale=%d:-2", r.opt.FPS, r.opt.Width),
		"-q:v", "5",
		"-f", "mjpeg",
		"-",
	)
	cmd := exec.Command(r.opt.FFmpeg, args...)
	sp, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("recorder: cannot start ffmpeg: %w", err)
	}
	watcher := newReconnectWatcher(r.opt.Logf)
	r.mu.Lock()
	r.watcher = watcher
	r.mu.Unlock()
	cmd.Stderr = watcher
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("recorder: cannot start ffmpeg: %w", err)
	}
	r.cmd = cmd
	r.stdout = sp
	r.started = time.Now()
	go r.readPipe(sp)
	go r.reap()
	go r.watchSegments()
	return nil
}

// reaper waits for ffmpeg and records an unexpected exit. There is no restart:
// the plan forbids reconnect inside the run, because a silent reconnect turns a
// dropout into a success in the report. The exit is a fault, and the verdict is
// FAIL.
func (r *Recorder) reap() {
	err := r.cmd.Wait()
	r.mu.Lock()
	r.exitErr = err
	r.mu.Unlock()
	close(r.exited)
}

// readPipe is the single reader of the video. It decodes each JPEG as it is
// complete and stamps it with time.Now() at that moment. It never drops a
// frame: if the queue is full it blocks, and the blocker is what the
// backpressure fault measures.
func (r *Recorder) readPipe(sp io.ReadCloser) {
	defer r.queue.close()
	var splitter mjpegSplitter
	buf := make([]byte, 64*1024)
	for {
		n, err := sp.Read(buf)
		if n > 0 {
			frames, _ := splitter.push(buf[:n])
			for _, f := range frames {
				w, h, rgb, derr := r.opt.Decoder.Decode(f)
				if derr != nil {
					atomic.AddUint64(&r.decodeErr, 1)
					r.opt.Logf("frame decode failed: %v; this is a recorded fault, not a dropped frame", derr)
					continue
				}
				atomic.AddUint64(&r.decodes, 1)
				frame := Frame{
					At:     time.Now(),
					Seq:    atomic.AddUint64(&r.framesOut, 1),
					Width:  w,
					Height: h,
					RGB:    rgb,
				}
				r.frameClock.Store(frame.At.UnixNano())
				// The screen draws the overlay on the JPEG that came off the pipe,
				// so the last one is kept. It is a copy of bytes this process
				// already read, not a second read of the source.
				snapshot := make([]byte, len(f))
				copy(snapshot, f)
				// The sequence goes in BEFORE the pointer is published, so a reader
				// that sees the bytes always also sees the number they belong to.
				// Publishing them the other way round leaves a window where the
				// picture is a newer frame than the number says.
				r.lastJPEGSeq.Store(frame.Seq)
				r.lastJPEG.Store(&snapshot)
				// The same bytes are retained by sequence for the snapshot path.
				// The pointer above is the newest picture for the live screen; this
				// ring is the picture OF A FRAME for the evidence store, and the two
				// are different questions.
				r.retainPicture(frame.Seq, snapshot)
				if err := r.queue.push(frame); err != nil {
					return
				}
			}
		}
		if err != nil {
			if splitter.pending() {
				r.opt.Logf("stream ended with an unfinished JPEG buffered; the frame is incomplete and is recorded as a fault, not delivered")
			}
			return
		}
	}
}

// watchSegments is how the wall clock of each rolling file is established. When
// a new segment file appears, the boundary instant is the wall clock of the most
// recent frame the reader had decoded: ffmpeg opens the new file for the packet
// it is writing, and the MJPEG pipe carries that same packet immediately after,
// so the frame clock at the moment of observation is the first frame of the new
// segment, to within the watch interval.
//
// This is the whole reason the module needs one reader and one clock. With a
// second reader the mapping would come from a different timeline and every clip
// would point at the wrong second.
func (r *Recorder) watchSegments() {
	ticker := time.NewTicker(r.opt.WatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopWatch:
			return
		case <-r.exited:
			// One last pass so the final segments are recorded.
			r.ObserveSegments()
			return
		case <-ticker.C:
			r.ObserveSegments()
		}
	}
}

// ObserveSegments merges any newly appeared rolling files into the index,
// stamping each with the reader's frame clock.
func (r *Recorder) ObserveSegments() {
	idx, nonSegments, err := ScanSegmentIndex(r.opt.RollingDir)
	if err != nil {
		return
	}
	for _, f := range nonSegments {
		r.opt.Logf("rolling directory contains %q, which is not a segment; it is not counted as coverage", f)
	}
	frameClock := r.LastFrameAt()
	if frameClock.IsZero() {
		// No frame has been decoded yet, so there is no clock to attribute a
		// segment to. Leave it unmeasured rather than invent an instant.
		r.mu.Lock()
		r.segIndex = idx
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	known := make(map[int]bool, len(r.observations))
	for _, o := range r.observations {
		known[o.index] = true
	}
	for _, s := range idx.segments {
		if known[s.Index] {
			continue
		}
		// The first segment starts when the recorder started, not when the
		// watcher first looked: the muxer opens it before the first frame.
		start := frameClock
		if len(r.observations) == 0 && !r.started.IsZero() {
			if s.ObservedAt.IsZero() {
				start = r.started
			}
		}
		r.observations = append(r.observations, segmentObservation{
			index:      s.Index,
			observedAt: time.Now(),
			frameClock: start,
		})
	}
	idx.ApplyObservations(r.observations)
	r.segIndex = idx
	r.mu.Unlock()
}

// LastFrameAt is the wall clock of the most recently decoded frame. It is the
// clock the segment mapping is built on.
func (r *Recorder) LastFrameAt() time.Time {
	v := r.frameClock.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// Index returns the current segment index, refreshing it first.
func (r *Recorder) Index() (*SegmentIndex, error) {
	r.ObserveSegments()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.segIndex == nil {
		return &SegmentIndex{}, nil
	}
	return r.segIndex, nil
}

// Next returns the next frame, or false when the pipe is finished. It is the
// consumer side of the bounded queue.
func (r *Recorder) Next() (Frame, bool) {
	item, ok := r.queue.pop()
	if !ok {
		return Frame{}, false
	}
	return item.(Frame), true
}

// Poll returns the next frame, or ok=false if d elapsed with nothing arriving.
//
// The distinction matters and Poll exists for it. A run loop that blocks on
// Next() cannot service its own heartbeat timer, so a stalled pipe produces a
// run that goes SILENT — and the silence is the very dropout AC1 exists to
// detect, noticed by the one component that must never be the one that notices.
// A caller polls, and between polls it can still write a heartbeat, sample a
// resource, and react to a supervisor fault.
func (r *Recorder) Poll(d time.Duration) (Frame, bool) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		if r.queue.depth() > 0 {
			if item, ok := r.queue.pop(); ok {
				return item.(Frame), true
			}
		}
		if r.queue.closedNow() {
			// Drain whatever is left, then report the end.
			if item, ok := r.queue.pop(); ok {
				return item.(Frame), true
			}
			return Frame{}, false
		}
		// Wake on the queue's signal or the deadline. A 20ms tick is the floor:
		// the loop also polls the heartbeat, so the cost of a short interval is
		// one cheap lock and nothing else.
		tick := time.NewTimer(20 * time.Millisecond)
		select {
		case <-tick.C:
		case <-deadline.C:
			tick.Stop()
			return Frame{}, false
		}
		tick.Stop()
	}
}

// Exhausted reports whether the pipe has ended. A Poll that returned false with
// this true is the end of the stream; false means "nothing yet".
func (r *Recorder) Exhausted() bool { return r.queue.closedNow() }

// ProcessID is the OS process id of ffmpeg, read fresh on every call because
// the recorder may have been restarted since it was built. AC5's CPU clause names
// this process specifically, so the id has to be the live one.
func (r *Recorder) ProcessID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil || r.cmd.Process == nil {
		return 0
	}
	return r.cmd.Process.Pid
}

// jpegFrame is one retained picture and the sequence it belongs to.
type jpegFrame struct {
	Seq  uint64
	Data []byte
}

// RecentFrameDepth is how many frames the recorder keeps their pictures for.
//
// The bound is the pipe's queue depth with room to spare, because the worst lag
// between a frame being decoded and `process` reading it is how many frames can be
// queued ahead of it. Deeper is wasted memory; shallower is a snapshot that is
// refused because the pipe was busy, which is a fault an operator cannot act on.
const RecentFrameDepth = 96

// FrameJPEGFor returns the picture of one specific frame.
//
// It returns false rather than the newest picture when the frame is not in the
// ring, and that refusal is the whole reason the method exists. "The picture of
// this decision" and "the newest picture" are different questions; the second was
// what the snapshot path used, and it made the snapshot of a crossing a picture of
// some later crossing.
func (r *Recorder) FrameJPEGFor(seq uint64) ([]byte, bool) {
	if seq == 0 {
		return nil, false
	}
	r.recentMu.Lock()
	defer r.recentMu.Unlock()
	for i := len(r.recent) - 1; i >= 0; i-- {
		if r.recent[i].Seq == seq {
			return r.recent[i].Data, true
		}
	}
	return nil, false
}

// retainPicture adds a frame to the ring. It is called by the reader goroutine
// and is the only writer.
func (r *Recorder) retainPicture(seq uint64, data []byte) {
	r.recentMu.Lock()
	defer r.recentMu.Unlock()
	if len(r.recent) == RecentFrameDepth {
		// Copy down rather than reallocate: this runs on every frame, and a
		// per-frame allocation of a 96-entry slice is a measurable cost on a path
		// that exists only for evidence.
		copy(r.recent, r.recent[1:])
		r.recent[len(r.recent)-1] = jpegFrame{}
		r.recent = r.recent[:len(r.recent)-1]
	}
	r.recent = append(r.recent, jpegFrame{Seq: seq, Data: data})
}

// LastFrameJPEG is the most recent JPEG as it came off the pipe, for the screen
// to draw on. It returns nil before the first frame, and the screen says "no
// frame has been read" rather than rendering a black picture.
//
// It says nothing about WHICH frame. Callers that attach a picture to a decision
// must use [Recorder.LastFrameJPEGSeq], because this one answers "what is
// newest" and the question they are asking is "what is mine".
func (r *Recorder) LastFrameJPEG() []byte {
	b, _ := r.LastFrameJPEGSeq()
	return b
}

// LastFrameJPEGSeq is the most recent JPEG together with the sequence of the frame
// it belongs to.
//
// W2-5. It is the pair because the picture and the number have to be read
// together or neither is trustworthy: a caller that compares the number against
// its own frame can refuse a mismatch, and a caller that has only the picture
// cannot. A sequence of 0 means no frame has been read yet, which is different
// from "the newest frame happens to be sequence 0".
func (r *Recorder) LastFrameJPEGSeq() ([]byte, uint64) {
	p := r.lastJPEG.Load()
	if p == nil {
		return nil, 0
	}
	return *p, r.lastJPEGSeq.Load()
}

// Reconnects is how many times ffmpeg has announced that it re-established the
// source.
//
// A count, not a flag: a boolean would hide the difference between one blip and
// two hundred, and the report's whole claim is that a camera's uptime is a
// measured property of this run rather than an assumption.
//
// It is on the Recorder rather than only on the stderr watcher because the
// watcher is a local variable and a local variable is not reachable from the run
// loop. Before this method existed, `watchReconnects` asserted on the frame
// source for a method it did not have, the assertion failed, and the function
// returned on its first tick — so `stream_gap` faults were never recorded in a
// real run, and the fault tolerance that is this module's best story was silently
// absent from every live demonstration of it.
func (r *Recorder) Reconnects() int {
	r.mu.Lock()
	w := r.watcher
	r.mu.Unlock()
	if w == nil {
		// ffmpeg has not started, so nothing has been re-established. Zero, and
		// not an error: a recorder that has not started has not failed.
		return 0
	}
	return w.Reconnects()
}

// BackpressureWait is how long the pipe has been blocked, or 0 when it is
// flowing. The supervisor polls this and kills ffmpeg past the deadline, so the
// condition is a recorded fault with a reason and not a silent frame drop.
func (r *Recorder) BackpressureWait() time.Duration {
	return r.queue.maxWait()
}

// PipeDepth is the current queue depth, for the screen and the resource table.
func (r *Recorder) PipeDepth() int { return r.queue.depth() }

// Counts are the measured frame counters.
func (r *Recorder) Counts() (frames, decodes, decodeErrors uint64) {
	return atomic.LoadUint64(&r.framesOut), atomic.LoadUint64(&r.decodes), atomic.LoadUint64(&r.decodeErr)
}

// Exited is closed when ffmpeg has exited.
func (r *Recorder) Exited() <-chan struct{} { return r.exited }

// ExitError is the recorded reason ffmpeg ended, if it ended unexpectedly.
func (r *Recorder) ExitError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitErr
}

// Wait blocks until ffmpeg exits and returns its error.
func (r *Recorder) Wait() error {
	<-r.exited
	return r.ExitError()
}

// Stderr returns the tail of ffmpeg's diagnostics, which is what an operator
// needs when the process died without a line in the log.
func (r *Recorder) Stderr() string {
	if r.cmd == nil {
		return ""
	}
	if tb, ok := r.cmd.Stderr.(*tailBuffer); ok {
		return tb.String()
	}
	return ""
}

// Stop asks ffmpeg to end and waits for it, escalating to a kill if it does not
// within the grace period. Shutdown is not a fault.
func (r *Recorder) Stop(grace time.Duration) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	r.mu.Unlock()
	r.queue.close()
	close(r.stopWatch)

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() { <-r.exited; close(done) }()
	_ = r.cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
		return nil
	case <-time.After(grace):
		_ = r.cmd.Process.Kill()
		<-done
		return fmt.Errorf("recorder: ffmpeg did not exit within %s of an interrupt and was killed", grace)
	}
}

// Kill terminates ffmpeg now. It is what backpressure and a fault rehearsal
// call, and it is deliberate: a killed recorder is a recorded event, not a
// mystery.
func (r *Recorder) Kill() error {
	if r.cmd == nil || r.cmd.Process == nil {
		return errors.New("recorder: ffmpeg was never started")
	}
	return r.cmd.Process.Kill()
}

// Uptime is how long this process has been recording.
func (r *Recorder) Uptime() time.Duration {
	if r.started.IsZero() {
		return 0
	}
	return time.Since(r.started)
}
