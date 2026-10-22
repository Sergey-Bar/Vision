// Package recording is the index over what the recorder wrote.
//
// # What this is, and what it is not
//
// The BYTES live on the filesystem, in the customer's own disk. This package
// is the map over them: one row per segment, and the chain that binds one
// segment to the next.
//
// The chain is the part that matters. A segment on its own is a file; a
// segment whose digest is chained to its predecessor's is EVIDENCE, and the
// chain head is sealed into the audit ledger at day close so that a
// modification months later is detectable.
//
// # The chain
//
//	chain[n] = SHA256(chain[n-1] ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬â€œ segment_digest ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬â€œ started_at)
//
// The digest is the segment's own bytes. The time is the segment's own start.
// The previous hash is the previous segment's. Nothing else goes in, so a
// segment that is moved, edited, dropped or inserted changes the chain from
// that point on ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â which is the property the whole thing exists for.
//
// The chain is per device per DAY. Per day because the head is sealed at day
// close, and a ledger row per segment at 8 640 segments a day per device is a
// self-inflicted denial of service on the ledger.
package recording

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// The states a segment can be in.
const (
	// StateComplete is a segment that was cut where it was meant to be cut.
	StateComplete = "complete"
	// StatePartial is a segment that was cut short, and whose boundary is the
	// TRUE boundary.
	//
	// A partial segment is never a silent hole: it is a hole that says how
	// big it is. A segment that was quietly dropped and one that was cut at
	// the camera's last byte look the same on disk unless one of them says
	// so.
	StatePartial = "partial"
)

// ErrNoSuchChain is a device with no chain for a day.
//
// It is a refusal and not an empty chain, because "this device recorded
// nothing on that day" and "we do not know about that device" are different
// facts and only one of them is a gap in the recording.
var ErrNoSuchChain = errors.New("recording: no chain for that device and day")

// Segment is one row of the index.
type Segment struct {
	TenantID  string
	SiteID    string
	DeviceID  string
	Seq       int64
	SegmentID string

	StartedAt time.Time
	EndedAt   time.Time
	// GapBefore is the measured silence before this segment.
	GapBefore time.Duration

	Bytes  int64
	Digest string
	Codec  string
	Width  int
	Height int
	FPS    float64
	// StorageRef is where the bytes are, relative to the recording root.
	// Never absolute and never a URL.
	StorageRef string
	State      string
	ChainPrev  string
	ChainHash  string
}

// Chain is the state of one device's chain for one day.
type Chain struct {
	TenantID      string
	SiteID        string
	DeviceID      string
	Day           time.Time
	ChainHead     string
	Segments      int
	LastStartedAt time.Time
	LastDigest    string
	SealedAt      time.Time
}

// Gap is a measured hole in a recording.
//
// The gap is the time between the previous segment's last frame and this
// segment's first. On a healthy camera it is about one frame interval, because
// the keyframe that ends one segment is the first frame of the next. A gap of
// four minutes is a camera that was not being watched, and it is published
// rather than hidden, because a recording that cannot say it had a hole in it
// is a recording that lies by omission.
type Gap struct {
	DeviceID  string
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
}

// ChainVerification is the result of recomputing a chain from the rows.
type ChainVerification struct {
	DeviceID string
	Day      time.Time
	// Expected is the head the rows produce.
	Expected string
	// Recorded is the head the chain row carries.
	Recorded string
	// Segments is how many segments the day holds.
	Segments int
	// OK is whether the two agree.
	OK bool
	// BrokenAt is the sequence of the first segment whose recomputed hash
	// differs from the recorded one. Zero means the day is sound.
	BrokenAt int64
}

// Service reads and writes the recording index.
//
// The Root is where the segments' BYTES live ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â the customer's own disk. It
// is not the evidence root and it is not the database: the index is the map
// over them, and the bytes are somewhere else.
//
// It is empty when the install does not record, and every read of the bytes
// and every sweep then refuses with the reason. An install with no recording
// root is a supported configuration, and it says so rather than answering
// with an empty list that reads as "nothing was recorded".
type Service struct {
	Pool *dbx.Pool
	// Root is the recording root: the directory the segments live under.
	Root string
	// verifyDigestOnRead re-hashes a segment's bytes against the row's
	// digest before serving them.
	verifyDigestOnRead bool
	// logf reports what the retention sweep did.
	logf func(format string, args ...any)
}

// NewService returns the index service.
//
// The digest check is ON, and it is the same default the evidence store uses.
func NewService(pool *dbx.Pool) *Service {
	return &Service{Pool: pool, verifyDigestOnRead: defaultVerifyDigestOnRead}
}

// Append writes a segment and advances the chain.
//
// Both happen in ONE transaction, and that is the whole point: a segment row
// whose chain hash was never written is a segment nobody can prove, and a chain
// hash whose segment was never written is a chain that skips a frame. Either
// alone is a hole in the recording, and the recording is the product.
func (s *Service) Append(ctx context.Context, seg Segment) error {
	if s == nil || s.Pool == nil {
		return errors.New("recording: the index has no pool behind it")
	}
	if seg.TenantID == "" || seg.SiteID == "" || seg.DeviceID == "" {
		return errors.New("recording: a segment needs a tenant, a site and a device")
	}
	if seg.Digest == "" {
		return errors.New("recording: a segment with no digest cannot be chained")
	}
	if seg.State == "" {
		seg.State = StateComplete
	}
	if seg.Seq <= 0 {
		return errors.New("recording: a segment needs a sequence number")
	}

	day := seg.StartedAt.UTC().Truncate(24 * time.Hour)

	// The timestamps are brought DOWN to the precision the storage keeps
	// BEFORE they are written, and that is the fix for a proof that failed
	// at random.
	//
	// Postgres stores a TIMESTAMPTZ in microseconds and it ROUNDS: a
	// `12:00:00.123456789` comes back as `12:00:00.123457`. A chain
	// computed over the writer's nanosecond value would be over
	// `12:00:00.123456`, so the link written on the way in and the link
	// recomputed on the way out would differ ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â for HALF of all timestamps,
	// and for the rest only when the clock's last digits happened to land
	// differently.
	//
	// So the value is truncated here, once, and the SAME value is stored
	// and hashed. Postgres then has nothing to round, and the two sides
	// cannot disagree.
	seg.StartedAt = chainTime(seg.StartedAt)
	seg.EndedAt = chainTime(seg.EndedAt)

	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: seg.TenantID, ActorID: "recorder"}, func(tx pgx.Tx) error {
		// The chain's current head, the DEVICE-WIDE sequence the next segment
		// takes, and the digest and start the chain resumes from.
		var (
			head        string
			segments    int
			lastSeq     int64
			lastDigest  string
			lastStarted time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(chain_head, ''), segments, last_seq,
			       COALESCE(last_digest, ''), COALESCE(last_started_at, 'epoch'::timestamptz)
			  FROM recording_chains
			 WHERE tenant_id = $1 AND device_id = $2 AND day = $3
			 FOR UPDATE`,
			seg.TenantID, seg.DeviceID, day).Scan(&head, &segments, &lastSeq, &lastDigest, &lastStarted)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("recording: read the chain of %s for %s: %w", seg.DeviceID, day.Format("2006-01-02"), err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// The first segment of the day. It may not be the first segment
			// of the DEVICE, so the chain continues from whatever the
			// previous day left ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â and the previous day's head is what makes
			// the day a continuation rather than a new world.
			head, lastDigest, lastStarted, lastSeq = s.previousDay(ctx, tx, seg, day)
		}

		// The sequence is the DEVICE's, and it never restarts.
		//
		// The chain is per day so that it can be SEALED per day, and the
		// segment's uniqueness is per device across every day: a sequence
		// that restarted at midnight would collide with the segment that
		// took the same number yesterday, and the collision is a unique
		// violation the writer cannot tell from a genuine duplicate.
		seq := lastSeq + 1
		// The DAY's count is a different number: it is how many segments
		// this day's chain covers, and it is what the sealed row says.
		dayCount := segments + 1
		hash := ChainHash(head, seg.Digest, seg.StartedAt)

		// The KEY is claimed first, in the same transaction as the segment.
		//
		// The segment table is partitioned by the segment's start, and
		// Postgres requires a partitioned table's unique index to include the
		// partition key ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â so the segment's id and the device's sequence are
		// unique only WITHIN a month here, and the global uniqueness lives
		// in its own small table.
		//
		// Claiming it first means a duplicate is a unique violation the
		// writer treats as a no-op, rather than a second row that quietly
		// forks the chain. The same arrangement `inbox_event_keys` uses.
		if _, err := tx.Exec(ctx, `
			INSERT INTO recording_segment_keys
			    (tenant_id, segment_id, device_id, seq, started_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, segment_id) DO NOTHING`,
			seg.TenantID, seg.SegmentID, seg.DeviceID, seq, seg.StartedAt); err != nil {
			return fmt.Errorf("recording: claim the key of %s: %w", seg.SegmentID, err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO recording_segments
			    (tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
			     gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
			     state, chain_prev, chain_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (tenant_id, started_at, segment_id) DO NOTHING`,
			seg.TenantID, seg.SiteID, seg.DeviceID, seq, seg.SegmentID,
			seg.StartedAt, seg.EndedAt, seg.GapBefore.Milliseconds(),
			seg.Bytes, seg.Digest, seg.Codec, seg.Width, seg.Height, seg.FPS,
			seg.StorageRef, seg.State, head, hash); err != nil {
			return fmt.Errorf("recording: write the segment %s: %w", seg.SegmentID, err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO recording_chains
			    (tenant_id, site_id, device_id, day, chain_head, segments, last_seq,
			     last_started_at, last_digest)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, device_id, day) DO UPDATE
			   SET chain_head = EXCLUDED.chain_head,
			       segments = EXCLUDED.segments,
			       last_seq = EXCLUDED.last_seq,
			       last_started_at = EXCLUDED.last_started_at,
			       last_digest = EXCLUDED.last_digest`,
			seg.TenantID, seg.SiteID, seg.DeviceID, day, hash, dayCount, seq,
			seg.StartedAt, seg.Digest); err != nil {
			return fmt.Errorf("recording: advance the chain of %s: %w", seg.DeviceID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// previousDay finds the chain the day before, so a day is a CONTINUATION of
// the device's history rather than a new world.
//
// A chain that started fresh each day would be a chain that could be
// REPLACED: an attacker who removed a whole day would leave a chain that
// still verifies from the next morning. The chain is per day so that it can
// be SEALED per day, and it CROSSES the day boundary so that it is one chain.
//
// It returns the previous day's head, its last digest, its last start and the
// DEVICE-WIDE sequence its last segment took ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â because the sequence is the
// device's and not the day's.
func (s *Service) previousDay(ctx context.Context, tx pgx.Tx, seg Segment, day time.Time) (string, string, time.Time, int64) {
	var (
		head    string
		digest  string
		last    time.Time
		lastSeq int64
	)
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(chain_head, ''), COALESCE(last_digest, ''),
		       COALESCE(last_started_at, 'epoch'::timestamptz), last_seq
		  FROM recording_chains
		 WHERE tenant_id = $1 AND device_id = $2 AND day < $3
		 ORDER BY day DESC
		 LIMIT 1`,
		seg.TenantID, seg.DeviceID, day).Scan(&head, &digest, &last, &lastSeq)
	if err != nil {
		// No previous day, or it could not be read. A chain that starts
		// here is a chain with an unknown beginning, and the segment that
		// starts it says so through its chain_prev being empty.
		return "", "", time.Time{}, 0
	}
	return head, digest, last, lastSeq
}

// ChainHash computes one link of the chain.
//
// The three inputs are the whole rule: the previous hash, the segment's own
// digest, and the segment's own start. The start is in there so that two
// segments with the same bytes at different times are different links, and the
// digest is in there so that the same bytes at the same time cannot be
// swapped for different bytes.
//
// # The timestamp is truncated, and that is the whole reason it verifies
//
// Postgres stores a TIMESTAMPTZ in MICROSECONDS. A hash over a nanosecond
// timestamp is a hash over a value the round trip does not preserve: the
// chain computed on the way in would use `12:00:00.123456789` and the chain
// recomputed from the stored row would use `12:00:00.123456`, and the two
// would differ ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â so a recording that was never touched would report itself
// as broken.
//
// It is not a cosmetic truncation, and the SHAPE of it matters. Postgres
// ROUNDS a nanosecond value to the nearest microsecond, so
// `12:00:00.123456789` comes back as `12:00:00.123457` ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â and a chain
// computed over the TRUNCATED `12:00:00.123456` is a different link. That
// is a proof that fails for HALF of all timestamps and for the rest only
// when the clock's last digits happened to land differently: a proof that
// fails at random is worse than no proof at all, because it looks like
// tampering and it is really a rounding rule.
//
// So the segment's timestamps are brought DOWN to the storage's precision
// BEFORE they are written, and the same value is stored and hashed. The
// truncation lives in `chainTime`, and every side of the proof goes through
// it.
func ChainHash(prev, digest string, startedAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte(digest))
	h.Write([]byte(chainTime(startedAt).Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))
}

// chainTime is the timestamp the chain is over.
//
// Microseconds, UTC, and nothing else. UTC because the chain is per UTC day
// and a link computed from a local rendering of the same instant would be a
// different link.
//
// It TRUNCATES and does not round, and the reason is the writer: the
// segment's timestamps are truncated to this precision before they are
// stored, so Postgres has nothing left to round. A rounding rule on this
// side would disagree with the storage's own rounding rule about half the
// time.
func chainTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

// Previous returns the segment that ended at or before the given instant.
//
// It is the PRE-ROLL: the seconds before a window are in the segment before
// the window's first one, and that segment may have ended a long time before
// the window opened. Asking for "a segment near this instant" would find the
// wrong one, so this asks the exact question.
//
// It returns nil when there is no such segment, which is a fact about the
// recording ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â the window starts at the beginning of what was recorded ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â and
// not a failure.
func (s *Service) Previous(ctx context.Context, tenant, site, device string, before time.Time) (*Segment, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("recording: the index has no pool behind it")
	}
	var seg Segment
	err := s.Pool.WithSites(tenant, "recording", []string{site}).QueryRow(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2 AND device_id = $3
		   AND ended_at <= $4
		 ORDER BY ended_at DESC
		 LIMIT 1`,
		tenant, site, device, before).Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
		&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
		&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("recording: find the segment before %s: %w", before.Format(time.RFC3339), err)
	}
	return &seg, nil
}

// Overlapping returns the segments that OVERLAP a window.
//
// It is not the same as `Timeline`, and the difference matters to a clip.
//
// `Timeline` answers "which segments BEGAN in this window", and it is the
// right question for a listing and for the gap computation: two segments that
// started in the window are two consecutive segments.
//
// `Overlapping` answers "which segments are partly in this window", and it is
// the right question for a CLIP: a segment that started at 13:59:55 and ran
// until 14:00:05 was partly recorded in a window that opens at 14:00, and a
// clip that leaves it out starts in the middle of a frame.
func (s *Service) Overlapping(ctx context.Context, tenant, site, device string, from, to time.Time) ([]Segment, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("recording: the index has no pool behind it")
	}
	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2 AND device_id = $3
		   AND started_at < $5 AND ended_at > $4
		 ORDER BY started_at`,
		tenant, site, device, from, to)
	if err != nil {
		return nil, fmt.Errorf("recording: read the overlapping segments of %s: %w", device, err)
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
			&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
			&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash); err != nil {
			return nil, fmt.Errorf("recording: scan a segment: %w", err)
		}
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recording: iterate the overlapping segments of %s: %w", device, err)
	}
	return out, nil
}

// Continuity is how CONTINUOUS the site's recording is.
//
// It is the answer to the question a VMS exists to answer, measured rather
// than inferred: how quiet is each camera, right now.
type Continuity struct {
	// Cameras is how many cameras have recorded anything.
	Cameras int
	// Segments is how many segments they recorded in the window.
	Segments int
	// WorstGapSeconds is the largest gap between two consecutive segments,
	// across every camera.
	//
	// The WORST one and not the average, because the question an operator
	// has is "is anything wrong", and one camera that has been dark since
	// Tuesday is a thing that is wrong even when every other camera is
	// perfect.
	//
	// It is NEGATIVE when the camera has recorded nothing at all, and that
	// is the honest reading: a camera with no segments has been quiet for
	// ever, which is longer than any gap.
	WorstGapSeconds float64
	// PerCamera is each camera's own gap, for a page that wants to name
	// WHICH camera is quiet.
	PerCamera map[string]float64
}

// Continuity reads how continuous the site's recording is.
//
// The window is the last hour, and it is a WINDOW rather than the whole
// history because the question is about now: a camera that was dark a week
// ago and has been recording since is not a camera that is dark.
//
// A camera with no segments in the window is reported with a gap of
// NEGATIVE infinity ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â it has been quiet for longer than the window ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â and the
// caller leaves the gauge unwritten rather than reporting a number it cannot
// defend.
func (s *Service) Continuity(ctx context.Context, tenant, site string) (Continuity, error) {
	var out Continuity
	if s == nil || s.Pool == nil {
		return out, errors.New("recording: the index has no pool behind it")
	}
	to := time.Now().UTC()
	from := to.Add(-time.Hour)

	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT device_id, started_at, ended_at
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2
		   AND started_at >= $3 AND started_at < $4
		 ORDER BY device_id, started_at`,
		tenant, site, from, to)
	if err != nil {
		return out, fmt.Errorf("recording: read the continuity of %s: %w", site, err)
	}
	defer rows.Close()

	out.PerCamera = map[string]float64{}
	lastEnd := map[string]time.Time{}
	seen := map[string]bool{}
	for rows.Next() {
		var (
			device  string
			started time.Time
			ended   time.Time
		)
		if err := rows.Scan(&device, &started, &ended); err != nil {
			return out, fmt.Errorf("recording: scan a continuity row: %w", err)
		}
		seen[device] = true
		out.Segments++
		if prev, ok := lastEnd[device]; ok {
			gap := started.Sub(prev).Seconds()
			if gap > out.WorstGapSeconds {
				out.WorstGapSeconds = gap
			}
			out.PerCamera[device] = gap
		}
		lastEnd[device] = ended
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("recording: iterate the continuity of %s: %w", site, err)
	}

	out.Cameras = len(seen)
	// A camera that has recorded something but whose last segment ENDED a
	// while ago is quiet NOW, and that is the gap the operator cares about.
	for device, ended := range lastEnd {
		gap := to.Sub(ended).Seconds()
		if gap > out.WorstGapSeconds {
			out.WorstGapSeconds = gap
		}
		out.PerCamera[device] = gap
	}
	return out, nil
}

// List returns the segments in a window, for a site or for one device.
//
// The device is optional and an empty device means the WHOLE SITE, which is
// the question an operator asks when they open a VMS: "what did we record?".
// The rows come back newest-first, because a list of a day's recording that
// starts at the beginning of the day is a list nobody scrolls to the end of.
func (s *Service) List(ctx context.Context, tenant, site, device string, from, to time.Time, limit int) ([]Segment, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("recording: the index has no pool behind it")
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2
		   AND started_at >= $3 AND started_at < $4
		   AND ($5 = '' OR device_id = $5)
		 ORDER BY started_at DESC
		 LIMIT $6`,
		tenant, site, from, to, device, limit)
	if err != nil {
		return nil, fmt.Errorf("recording: list the segments of %s: %w", site, err)
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
			&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
			&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash); err != nil {
			return nil, fmt.Errorf("recording: scan a segment: %w", err)
		}
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recording: iterate the segments of %s: %w", site, err)
	}
	return out, nil
}

// Timeline returns the segments in a window, in order.
func (s *Service) Timeline(ctx context.Context, tenant, site, device string, from, to time.Time) ([]Segment, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("recording: the index has no pool behind it")
	}
	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2 AND device_id = $3
		   AND started_at >= $4 AND started_at < $5
		 ORDER BY started_at`,
		tenant, site, device, from, to)
	if err != nil {
		return nil, fmt.Errorf("recording: read the timeline of %s: %w", device, err)
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
			&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
			&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash); err != nil {
			return nil, fmt.Errorf("recording: scan a segment: %w", err)
		}
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recording: iterate the timeline of %s: %w", device, err)
	}
	return out, nil
}

// Gaps returns the measured holes in a window.
//
// A gap is only a gap when it is LARGER than a frame. The segment boundary
// itself is about one frame wide, because the keyframe that ends one segment
// is the first frame of the next, and calling that a gap would report a
// camera with a hundred gaps a second recording without a hole in it.
func (s *Service) Gaps(ctx context.Context, tenant, site, device string, from, to time.Time, frame time.Duration) ([]Gap, error) {
	segments, err := s.Timeline(ctx, tenant, site, device, from, to)
	if err != nil {
		return nil, err
	}
	var out []Gap
	for i := 1; i < len(segments); i++ {
		gap := segments[i].StartedAt.Sub(segments[i-1].EndedAt)
		if gap <= frame {
			continue
		}
		out = append(out, Gap{
			DeviceID:  device,
			StartedAt: segments[i-1].EndedAt,
			EndedAt:   segments[i].StartedAt,
			Duration:  gap,
		})
	}
	return out, nil
}

// VerifyChain recomputes a device's chain for a day and compares it with the
// head the chain row carries.
//
// This is the proof, and it is a RECOMPUTATION rather than a read: a reader
// that trusted the stored head would report a broken chain as sound.
//
// # The recomputation starts from the PREVIOUS day's head
//
// The chain is per day so that it can be SEALED per day, and it CROSSES the
// day boundary so that it is one chain. A verifier that started from an
// empty head would recompute a DIFFERENT chain from the one the writer
// built ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â the writer's first link of the day hangs off yesterday's head.
//
// So the verifier does what the writer does: it reads the head the device
// carried into the day and starts from there. A verifier that started from
// nothing would report every device with a history as broken, which is a
// proof that cries wolf.
func (s *Service) VerifyChain(ctx context.Context, tenant, site, device string, day time.Time) (ChainVerification, error) {
	if s == nil || s.Pool == nil {
		return ChainVerification{}, errors.New("recording: the index has no pool behind it")
	}
	day = day.UTC().Truncate(24 * time.Hour)
	from := day
	to := day.Add(24 * time.Hour)

	var recorded string
	err := s.Pool.WithSites(tenant, "recording", []string{site}).QueryRow(ctx, `
		SELECT COALESCE(chain_head, '')
		  FROM recording_chains
		 WHERE tenant_id = $1 AND device_id = $2 AND day = $3`,
		tenant, device, day).Scan(&recorded)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChainVerification{}, ErrNoSuchChain
		}
		return ChainVerification{}, fmt.Errorf("recording: read the chain of %s: %w", device, err)
	}

	segments, err := s.Timeline(ctx, tenant, site, device, from, to)
	if err != nil {
		return ChainVerification{}, err
	}
	v := ChainVerification{DeviceID: device, Day: day, Recorded: recorded, Segments: len(segments)}
	head := s.headBefore(ctx, tenant, site, device, day)
	for _, seg := range segments {
		head = ChainHash(head, seg.Digest, seg.StartedAt)
		if seg.ChainHash != "" && seg.ChainHash != head {
			v.BrokenAt = seg.Seq
			v.Expected = head
			v.OK = false
			fmt.Fprintf(os.Stderr, "DEBUG VerifyChain FAIL: device=%s seq=%d seg.StartedAt=%v chainTime=%v digest=%s chainHash=%s computed=%s head=%s\n",
				seg.DeviceID, seg.Seq, seg.StartedAt, chainTime(seg.StartedAt), seg.Digest, seg.ChainHash, head, head)
			break
		}
	}
	v.Expected = head
	v.OK = head == recorded
	return v, nil
}

// headBefore reads the head the device's chain carried INTO the given day.
//
// It is the reader's half of the same rule the writer follows, and an empty
// head means the device had no chain before this day ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â which is a chain with
// an unknown beginning and not a broken one.
func (s *Service) headBefore(ctx context.Context, tenant, site, device string, day time.Time) string {
	row := s.Pool.WithSites(tenant, "recording", []string{site}).QueryRow(ctx, `
		SELECT COALESCE(chain_head, '')
		  FROM recording_chains
		 WHERE tenant_id = $1 AND device_id = $2 AND day < $3
		 ORDER BY day DESC
		 LIMIT 1`,
		tenant, device, day)
	defer row.Close()
	var head string
	if err := row.Scan(&head); err != nil {
		return ""
	}
	return head
}
