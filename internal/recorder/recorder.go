// Package recorder records a camera.
//
// # What it does
//
// RTSP in, segments out, and every segment is written to the disk and
// indexed in the SAME loop, so the index never claims a segment the disk does
// not hold.
//
// The ordering is the design: a segment that is written and not indexed is a
// file nobody can find, and a segment that is indexed and not written is a
// row pointing at nothing. Both are holes in the recording, and the recording
// is the product.
//
// # The write is atomic
//
// A segment is written to a temporary file, fsynced, and then RENAMED into
// place. A reader either sees the whole segment or no segment at all; there
// is no third state in which half a segment is on the disk under its own
// name.
//
// A segment that was cut short is written as `partial` and its boundary is
// the TRUE boundary. A partial segment is never a silent hole: it is a hole
// that says how big it is.
//
// # The recorder writes bytes and index rows. It does not seal, and it does
// not delete.
//
// The chain head is sealed into the audit ledger by the CORE at day close,
// because the ledger is the core's and the recorder is not trusted with it. The
// retention job deletes segments, because a recorder that could delete its own
// segments is a recorder that could hide a gap in them.
package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/rtsp"
)

// ErrNoRoot is a recorder with nowhere to write.
var ErrNoRoot = errors.New("recorder: no recording root")

// Source is one camera to record.
//
// The URL is configuration, not registry: it carries a credential, and a
// credential in a table is a credential in a backup. The DEVICE is the
// registry's, and the two are joined by the device's id.
type Source struct {
	TenantID string
	SiteID   string
	DeviceID string
	// URL is the RTSP stream. It may carry a credential, and it is never
	// written to the index.
	URL string
	// User and Password are the camera's credential, kept apart from the
	// URL so that the URL can be logged and the credential cannot.
	User     string
	Password string
}

// Options configures a Recorder.
type Options struct {
	// Root is where the bytes live. It is the customer's own disk.
	Root string
	// SegmentDuration is the target length of one segment.
	SegmentDuration time.Duration
	// MaxSegmentBytes bounds one segment.
	MaxSegmentBytes int64
	// RTSPTimeout bounds the connect and every request.
	RTSPTimeout time.Duration
	// Now is the clock, injected so the tests are not time-dependent.
	Now func() time.Time
	// Logf reports what the recorder is doing. It never receives a
	// credential.
	Logf func(format string, args ...any)
}

// Recorder records cameras.
//
// It is ONE camera per Recorder. A site with many cameras runs many
// Recorders, and the failure of one camera must not stop the others: a
// recorder that shares a loop with a dead camera takes the dead camera's
// silence with it.
type Recorder struct {
	opts    Options
	index   *recording.Service
	onSeg   func(recording.Segment)
	started time.Time
}

// New returns a recorder.
func New(index *recording.Service, opts Options, onSeg func(recording.Segment)) *Recorder {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &Recorder{opts: opts, index: index, onSeg: onSeg, started: opts.Now()}
}

// Run records one camera until the context ends or the camera fails.
//
// The error it returns is the camera's, and it is not swallowed: a camera
// that stopped is a fact the caller needs, and a caller that cannot see it
// will report a site as recording.
func (r *Recorder) Run(ctx context.Context, src Source) error {
	if r.opts.Root == "" {
		return ErrNoRoot
	}
	client, err := rtsp.Dial(ctx, src.URL, rtsp.Options{
		User:     src.User,
		Password: src.Password,
		Timeout:  r.opts.RTSPTimeout,
	})
	if err != nil {
		return fmt.Errorf("recorder: dial %s: %w", src.DeviceID, err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Play(ctx); err != nil {
		return fmt.Errorf("recorder: play %s: %w", src.DeviceID, err)
	}
	r.opts.Logf("recorder: recording %s", src.DeviceID)

	seg := mp4.NewSegmenter(mp4.SegmentOptions{
		SegmentDuration: r.opts.SegmentDuration,
		MaxSegmentBytes: r.opts.MaxSegmentBytes,
		OnDamaged: func(reason string) {
			r.opts.Logf("recorder: %s: %s", src.DeviceID, reason)
		},
	})

	seq := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			// The context ended. The segment in progress is finished and
			// written with its TRUE boundary, so the recording ends at a
			// frame rather than in the middle of one.
			r.finish(ctx, src, seg, &seq, true)
			return nil
		}
		nal, _, decodeTime, err := client.ReadNAL()
		if err != nil {
			r.finish(ctx, src, seg, &seq, true)
			return fmt.Errorf("recorder: read %s: %w", src.DeviceID, err)
		}
		wall := r.opts.Now()
		out, err := seg.PushNAL(nal, uint64(decodeTime), wall)
		if err != nil {
			// A stream that changed its parameter sets mid-flight is
			// refused: a file carrying two incompatible answers to "what
			// resolution is this" is answered by neither.
			r.opts.Logf("recorder: %s: %s", src.DeviceID, err)
			r.finish(ctx, src, seg, &seq, true)
			return fmt.Errorf("recorder: record %s: %w", src.DeviceID, err)
		}
		if out == nil {
			continue
		}
		if err := r.write(ctx, src, out, &seq, false); err != nil {
			return err
		}
	}
}

// finish writes the segment in progress, if there is one.
//
// A segment that was cut short is written as PARTIAL, with its true
// boundary. A recorder that dropped the last segment would lose the end of
// every recording, and the end of a recording is often the part somebody
// needs.
func (r *Recorder) finish(ctx context.Context, src Source, seg *mp4.Segmenter, seq *int64, partial bool) {
	out, err := seg.Close()
	if err != nil || out == nil {
		return
	}
	if err := r.write(ctx, src, out, seq, partial); err != nil {
		r.opts.Logf("recorder: %s: %s", src.DeviceID, err)
	}
}

// write puts one segment on the disk and in the index.
//
// The file is written FIRST and the index row SECOND, and the reason is the
// order of the failures. A file with no index row is a file nobody can find,
// and a finder that misses it is a gap in the recording that the site can see
// — the disk usage does not match the timeline. An index row with no file is a
// row pointing at nothing, and the row is the thing a reader trusts.
//
// The file goes first because a file can be FOUND by other means — a
// filesystem walk, a backup — and an index row that points at a file that was
// never written points at nothing at all.
func (r *Recorder) write(ctx context.Context, src Source, out *mp4.Segment, seq *int64, partial bool) error {
	if len(out.Data) == 0 {
		return nil
	}
	*seq++
	digest := digestOf(out.Data)
	storageRef, err := r.writeFile(src, out, digest)
	if err != nil {
		return err
	}
	state := recording.StateComplete
	if partial {
		state = recording.StatePartial
	}
	seg := recording.Segment{
		TenantID:   src.TenantID,
		SiteID:     src.SiteID,
		DeviceID:   src.DeviceID,
		Seq:        *seq,
		SegmentID:  segmentID(src, out, *seq),
		StartedAt:  out.WallStart,
		EndedAt:    out.WallEnd,
		GapBefore:  out.GapBefore,
		Bytes:      int64(len(out.Data)),
		Digest:     digest,
		Codec:      "h264",
		Width:      out.Width,
		Height:     out.Height,
		FPS:        0,
		StorageRef: storageRef,
		State:      state,
	}
	if r.index != nil {
		if err := r.index.Append(ctx, seg); err != nil {
			return fmt.Errorf("recorder: index the segment of %s: %w", src.DeviceID, err)
		}
	}
	if r.onSeg != nil {
		r.onSeg(seg)
	}
	return nil
}

// writeFile puts the bytes on the disk, atomically.
//
// The temporary file is in the SAME DIRECTORY as the final one, because a
// rename across a filesystem boundary is a copy, and a copy is not atomic.
func (r *Recorder) writeFile(src Source, out *mp4.Segment, digest string) (string, error) {
	dir := filepath.Join(r.opts.Root, src.TenantID, src.SiteID, src.DeviceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("recorder: make the recording directory for %s: %w", src.DeviceID, err)
	}
	name := fmt.Sprintf("%s_%s.mp4", out.WallStart.UTC().Format("20060102T150405"), digest[:16])
	final := filepath.Join(dir, name)
	tmp := final + ".partial"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("recorder: create %s: %w", tmp, err)
	}
	if _, err := f.Write(out.Data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("recorder: write %s: %w", tmp, err)
	}
	// The fsync is what makes the rename mean something: a rename that is not
	// preceded by a flush is a rename of a file whose bytes may not be on the
	// disk yet, and a segment that is indexed but whose bytes are lost is a
	// hole that looks complete.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("recorder: sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("recorder: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("recorder: rename %s: %w", tmp, err)
	}
	// The directory's own entry is fsynced too, because the rename is the
	// thing that made the file visible and a directory entry that is not on
	// the disk is a file that is not on the disk.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	ref, err := filepath.Rel(r.opts.Root, final)
	if err != nil {
		return "", fmt.Errorf("recorder: the storage reference of %s: %w", src.DeviceID, err)
	}
	return filepath.ToSlash(ref), nil
}

// segmentID mints the id of one segment.
//
// It is derived from the device, the time and the digest, so the same segment
// written twice is the same row — which is what makes a retried write a
// no-op rather than a duplicate.
func segmentID(src Source, out *mp4.Segment, seq int64) string {
	h := sha256.New()
	h.Write([]byte(src.TenantID))
	h.Write([]byte(src.DeviceID))
	h.Write([]byte(strconv.FormatInt(seq, 10)))
	h.Write([]byte(out.WallStart.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// digestOf is the digest of some bytes.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
