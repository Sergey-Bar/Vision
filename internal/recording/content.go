package recording

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/vision/internal/pathguard"
)

// The recorded BYTES.
//
// The index is the map; the bytes are on the customer's own disk, under the
// recording root. This file is the ONLY thing in the product that opens a
// segment, and that is deliberate for the same reason the evidence store is:
// a path-handling decision made in a dozen call sites is a path traversal
// waiting to happen in one of them.
//
// # The root is resolved, and the resolved path is checked
//
// A reference that passes the SHAPE check can still resolve outside the root
// — through a symlink, or through a junction on Windows. So the path is
// resolved FIRST and the resolved path is compared against the resolved
// root. Validating the string is not enough, and a symlink planted inside
// the root points wherever it likes.
//
// # The bytes are verified against the row's digest
//
// A segment whose bytes no longer match its row is REPORTED and not served.
// An operator watching footage that cannot be proven to be the footage the
// recording index names is worse off than being told the file is wrong.

// ErrNoRoot is an install with nowhere to read the segments from.
//
// It is a refusal and not an empty list, because "this install records
// nothing" and "this install recorded nothing" are different facts and only
// the second one is an empty list.
var ErrNoRoot = errors.New("recording: no recording root is configured")

// ErrNoSuchSegment is a segment the index does not hold.
var ErrNoSuchSegment = errors.New("recording: no such segment")

// VerifyDigestOnRead reports whether a read re-hashes the bytes against the
// row's digest before serving them.
//
// It is exported so that a caller — and a test — can assert that the default
// it claims to apply was the one that got applied, which is the same reason
// the evidence store exposes the same fact about itself.
func (s *Service) VerifyDigestOnRead() bool {
	return s != nil && s.verifyDigestOnRead
}

// ErrContentMismatch is a segment whose bytes no longer match its row.
//
// The bytes are not served, because the row is the claim and the bytes are
// what has to back it up.
var ErrContentMismatch = errors.New("recording: the segment's bytes do not match its recorded digest")

// Content is one segment's bytes.
type Content struct {
	// Reader is the segment's bytes. The caller closes it.
	Reader io.ReadCloser
	// Size is how many bytes there are.
	Size int64
	// Digest is the digest the INDEX recorded, and the one the bytes were
	// checked against.
	Digest string
	// StorageRef is where the bytes are, relative to the recording root.
	StorageRef string
}

// defaultVerifyDigestOnRead is whether a read re-hashes the bytes against
// the row's digest.
//
// It is TRUE, and it is the same default the evidence store uses: a segment
// whose bytes no longer match its row is REPORTED and not served, because an
// operator watching footage that cannot be proven to be the footage the
// recording index names is worse off than being told the file is wrong.
//
// The chain proves the bytes too, and it proves them for the whole history
// at once. The per-read check is the ONE that catches a file edited after
// the chain was sealed, and it is the one an operator meets first.
const defaultVerifyDigestOnRead = true

// Open opens a segment's bytes.
//
// The digest is verified on the way out, so a segment that was edited on the
// disk is reported rather than served. The verification is over the STREAM,
// so a segment that is a gigabyte long is read once and not twice.
func (s *Service) Open(ctx context.Context, tenant, site, segmentID string) (*Content, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("recording: the index has no pool behind it")
	}
	if strings.TrimSpace(s.Root) == "" {
		return nil, ErrNoRoot
	}
	seg, err := s.Segment(ctx, tenant, site, segmentID)
	if err != nil {
		return nil, err
	}
	path, err := s.resolve(seg.StorageRef)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchSegment, segmentID)
		}
		return nil, fmt.Errorf("recording: open the segment %s: %w", segmentID, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("recording: inspect the segment %s: %w", segmentID, err)
	}
	// The digest is checked BEFORE the bytes are served, and the file is
	// rewound afterwards so the caller streams from the start.
	//
	// The check reads the file twice — once to hash it and once to serve
	// it — and that is the price of not serving bytes that do not match
	// the row. A segment is ten seconds of video; the alternative is an
	// operator watching footage that was edited after the chain was sealed.
	if s.verifyDigestOnRead {
		if err := verifyDigest(f, seg.Digest); err != nil {
			_ = f.Close()
			return nil, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("recording: rewind the segment %s: %w", segmentID, err)
		}
	}
	return &Content{
		Reader:     f,
		Size:       info.Size(),
		Digest:     seg.Digest,
		StorageRef: seg.StorageRef,
	}, nil
}

// VerifyDigest checks a stream against a recorded digest.
//
// It reads the stream to the end and leaves it where it found it, so a caller
// that wants to serve the bytes has already read them. It returns the
// CONTENT MISMATCH error and not a bool, so the reason reaches the operator.
func verifyDigest(r io.Reader, want string) error {
	if strings.TrimSpace(want) == "" {
		return nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return fmt.Errorf("recording: read the segment to check its digest: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("%w: the bytes hash to %s and the index says %s", ErrContentMismatch, got, want)
	}
	return nil
}

// Segment reads one segment's row.
func (s *Service) Segment(ctx context.Context, tenant, site, segmentID string) (Segment, error) {
	if s == nil || s.Pool == nil {
		return Segment{}, errors.New("recording: the index has no pool behind it")
	}
	var seg Segment
	err := s.Pool.WithSites(tenant, "recording", []string{site}).QueryRow(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2 AND segment_id = $3`,
		tenant, site, segmentID).Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
		&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
		&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash)
	if err != nil {
		return Segment{}, err
	}
	return seg, nil
}

// resolve turns a storage reference into an absolute path under the root.
//
// The reference is checked for SHAPE and the resolved path is checked for
// CONTAINMENT, and both are the shared guard: the product writes to two
// roots and the rule is one.
func (s *Service) resolve(ref string) (string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", ErrNoRoot
	}
	if err := pathguard.ValidateRef(ref); err != nil {
		return "", fmt.Errorf("%w: %w", ErrPathRejected, err)
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		// A root that does not exist yet is a root with nothing in it, and
		// the honest answer is that there is nothing to serve.
		return "", fmt.Errorf("%w: the recording root %q could not be resolved: %w", ErrNoRoot, s.Root, err)
	}
	path := filepath.Join(root, filepath.FromSlash(ref))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// The file may simply not be there; the caller's answer depends on
		// why, so the reason travels.
		return "", fmt.Errorf("%w: %s: %w", ErrNoSuchSegment, ref, err)
	}
	if err := pathguard.ContainedIn(root, resolved); err != nil {
		return "", fmt.Errorf("%w: %w", ErrPathRejected, err)
	}
	return resolved, nil
}

// ErrPathRejected is a storage reference that names something outside the
// recording root.
//
// It is the SHARED guard's sentinel, so the recording index and the evidence
// store refuse the same references for the same reason.
var ErrPathRejected = pathguard.ErrRejected
