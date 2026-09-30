package recorder

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"sync"
	"time"
)

// ErrTruncatedFrame is returned when the stream ended inside a JPEG. It is an
// error and not an empty frame: a partial frame is not a frame, and dropping
// it quietly is the silent-frame-drop the architecture forbids.
var ErrTruncatedFrame = errors.New("recorder: stream ended inside a JPEG frame")

const (
	soi = 0xD8
	eoi = 0xD9
)

// mjpegSplitter reassembles JPEGs from a byte stream. It scans for SOI/EOI
// rather than trusting a container: ffmpeg's `-f mjpeg` output over a pipe is a
// bare concatenation of JPEGs, and 0xFFD9 cannot appear inside entropy-coded
// data because ffmpeg's muxer byte-stuffs 0xFF as 0xFF00.
type mjpegSplitter struct {
	buf []byte
}

func (s *mjpegSplitter) push(chunk []byte) (frames [][]byte, truncated bool) {
	s.buf = append(s.buf, chunk...)
	for {
		start := bytes.Index(s.buf, []byte{0xFF, 0xD8})
		if start < 0 {
			// Keep the tail in case SOI straddles the chunk boundary, but do not
			// let an un-parseable stream grow without bound.
			if len(s.buf) > 1 {
				s.buf = s.buf[len(s.buf)-1:]
			}
			return frames, false
		}
		s.buf = s.buf[start:]
		end := findEOI(s.buf)
		if end < 0 {
			return frames, false
		}
		frames = append(frames, s.buf[:end+2])
		s.buf = s.buf[end+2:]
	}
}

// pending reports whether a partial frame is buffered.
func (s *mjpegSplitter) pending() bool { return len(s.buf) > 2 }

func (s *mjpegSplitter) reset() { s.buf = nil }

func findEOI(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == 0xFF && b[i+1] == eoi {
			return i
		}
	}
	return -1
}

// decoder turns JPEG bytes into raw RGB. It is injected so tests can count
// decodes and so a decode failure is observable rather than a dropped frame.
type decoder interface {
	Decode(jpegBytes []byte) (w, h int, rgb []byte, err error)
}

// stdlibDecoder uses image/jpeg from the standard library. No cgo, no external
// codec, and it is the same decoder the test suite uses, so the test path and
// the run path cannot diverge.
type stdlibDecoder struct{}

func (stdlibDecoder) Decode(b []byte) (int, int, []byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("jpeg decode: %w", err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if yc, ok := img.(*image.YCbCr); ok {
		if yc.SubsampleRatio != image.YCbCrSubsampleRatio420 {
			// The loop below assumes one chroma sample per 2x2 luma block. Any
			// other ratio falls through to the generic path rather than
			// producing wrong colours.
			return w, h, toRGBGeneric(img), nil
		}
	}
	return w, h, toRGB(img), nil
}

func toRGB(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*3)

	// Fast path: jpeg.Decode returns *image.YCbCr for 4:2:0, and this avoids a
	// per-pixel interface dispatch in the hot loop.
	if yc, ok := img.(*image.YCbCr); ok {
		cw, ch := 2, 2
		_ = yc.YOffset(1, 1)
		for y := 0; y < h; y++ {
			row := yc.Y[y*yc.YStride:]
			crow := yc.Cb[(y/ch)*yc.CStride:]
			rrow := yc.Cr[(y/ch)*yc.CStride:]
			for x := 0; x < w; x++ {
				o := (y*w + x) * 3
				out[o], out[o+1], out[o+2] = ycbcrToRGB(row[x], crow[x/cw], rrow[x/cw])
			}
		}
		return out
	}
	return toRGBGeneric(img)
}

func toRGBGeneric(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := (y*w + x) * 3
			out[o] = uint8(r >> 8)
			out[o+1] = uint8(g >> 8)
			out[o+2] = uint8(bl >> 8)
		}
	}
	return out
}

func ycbcrToRGB(y, cb, cr uint8) (uint8, uint8, uint8) {
	yy := float64(y)
	cbf := float64(cb) - 128
	crf := float64(cr) - 128
	r := yy + 1.402*crf
	g := yy - 0.344136*cbf - 0.714136*crf
	b := yy + 1.772*cbf
	return clamp8(r), clamp8(g), clamp8(b)
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// boundedQueue is the backpressure boundary. The reader goroutine pushes into
// it; if the consumer stops reading, the push blocks, the block is timed, and
// the supervisor kills ffmpeg with a recorded fault. Frames are never dropped
// to make room.
type boundedQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []any
	closed bool
	limit  int
	// waited is the longest a producer has been blocked, which is the number the
	// backpressure fault reports.
	waited   int64
	waitFrom time.Time
}

func newBoundedQueue(limit int) *boundedQueue {
	q := &boundedQueue{limit: limit}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *boundedQueue) push(item any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return io.ErrClosedPipe
	}
	if q.waitFrom.IsZero() && len(q.items) >= q.limit {
		// The block clock starts when the queue is full and the producer is
		// about to wait, not when it starts pushing. Starting it earlier would
		// reset on every successful push and the recorded wait would be zero.
		q.waitFrom = time.Now()
	}
	for len(q.items) >= q.limit {
		if q.closed {
			return io.ErrClosedPipe
		}
		q.cond.Wait()
	}
	q.noteWaitLocked()
	q.items = append(q.items, item)
	q.cond.Signal()
	return nil
}

func (q *boundedQueue) pop() (any, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	q.cond.Signal()
	return it, true
}

func (q *boundedQueue) noteWaitLocked() {
	if q.waitFrom.IsZero() {
		return
	}
	d := time.Since(q.waitFrom)
	if d > time.Duration(q.waited) {
		q.waited = int64(d)
	}
	q.waitFrom = time.Time{}
}

// maxWait returns the longest time a producer was blocked, including a block
// that is still in progress right now, and resets the completed-block counter
// so the caller reports one number per fault and not a running total. The
// in-progress clock keeps running until the block ends.
func (q *boundedQueue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *boundedQueue) maxWait() time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	best := time.Duration(q.waited)
	if !q.waitFrom.IsZero() {
		if live := time.Since(q.waitFrom); live > best {
			best = live
		}
	}
	q.waited = 0
	return best
}

// popWait blocks until an item is available or the queue is closed. It returns
// the queue's own condition channels so a caller can also wake on a timeout.
func (q *boundedQueue) popWait() (any, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return nil, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	q.cond.Signal()
	return it, true
}

// closedNow reports whether the queue has been closed, which is how a caller
// tells "nothing has arrived yet" from "nothing will ever arrive again".
func (q *boundedQueue) closedNow() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

func (q *boundedQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}
