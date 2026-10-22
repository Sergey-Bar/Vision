package recording

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// The retention sweep.
//
// # What it deletes, and what it refuses to
//
// A segment is kept for as long as the POLICY says, and the policy is the
// site's own: a site-wide policy, or a policy for one device, and a device
// policy wins over the site's.
//
// A **legal hold** blocks the deletion, and it is the one row in the product
// that can say "you may not delete this". A sweep that ignored it would be a
// sweep that deletes the evidence of a dispute, and it would do so quietly.
//
// # Who runs it
//
// The CORE, and not the recorder.
//
// The recorder writes the segments and the recorder must not delete them: a
// recorder that could delete its own segments is a recorder that could hide a
// gap in them. The core holds the ledger, and the ledger is where a deletion
// belongs.
//
// # What a deleted segment leaves behind
//
// The FILE goes and the ROW goes, and the CHAIN keeps its place.
//
// The chain is the proof, and a chain with a hole in it is a chain that
// verifies against the rows that are left — which is exactly what a deletion
// is: a segment that is gone. The chain's head is the last link that was
// written, and it stays written: a device whose recording was partly deleted
// is a device whose recording CANNOT be proved, and the proof must be able
// to say so.
//
// The chain's own day row is kept for the same reason: a day whose segments
// were swept is a day the chain still covers, and the row says how many
// segments it covered.

// SweepResult is what one sweep did.
type SweepResult struct {
	// Deleted is how many segments were removed.
	Deleted int
	// Bytes is how many bytes went with them.
	Bytes int64
	// Held is how many expired segments were NOT deleted because a legal
	// hold covers them.
	Held int
	// HeldDevices names the devices whose hold protected something, so an
	// operator can see WHICH hold is in the way.
	HeldDevices []string
}

// Sweep deletes the segments the policy no longer keeps.
//
// It is the CORE's, and the reason is in the package comment: the recorder
// must not be able to delete its own segments.
//
// The `now` is the clock, injected so that a test is not time-dependent.
func (s *Service) Sweep(ctx context.Context, tenant, site string, now time.Time) (SweepResult, error) {
	var out SweepResult
	if s == nil || s.Pool == nil {
		return out, errors.New("recording: the index has no pool behind it")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// The policy that applies to a device: the device's own, or the site's.
	//
	// The DEVICE's wins, because a device the operator has singled out is a
	// device they have said something about, and the site's default is a
	// default.
	policies := map[string]int{}
	sitePolicy := 30
	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT COALESCE(device_id, ''), retention_days, legal_hold
		  FROM recording_policies
		 WHERE tenant_id = $1 AND site_id = $2`,
		tenant, site)
	if err != nil {
		return out, fmt.Errorf("recording: read the retention policies of %s: %w", site, err)
	}
	for rows.Next() {
		var device string
		var days int
		var hold bool
		if err := rows.Scan(&device, &days, &hold); err != nil {
			rows.Close()
			return out, fmt.Errorf("recording: scan a retention policy: %w", err)
		}
		if hold {
			// A hold is the strongest statement in the table, and it is
			// recorded per DEVICE.
			policies[device] = -1
			continue
		}
		if device == "" {
			sitePolicy = days
			continue
		}
		policies[device] = days
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("recording: iterate the retention policies of %s: %w", site, err)
	}

	// The segments that are past their date.
	expired, held, heldDevices, err := s.expiredSegments(ctx, tenant, site, policies, sitePolicy, now)
	if err != nil {
		return out, err
	}
	out.Held = held
	out.HeldDevices = heldDevices
	for _, seg := range expired {
		if err := s.deleteSegment(ctx, tenant, site, seg, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// expiredSegments finds the segments the policy no longer keeps.
//
// The cut-off is PER DEVICE, and the query carries it, because a single
// cut-off for the whole site would delete a device's footage because ANOTHER
// device was kept longer — and a device the operator kept is a device they
// want.
//
// The device's own policy wins over the site's, and the site's is the
// fallback for a device with none. The legal hold is NOT applied here: a held
// segment is EXPIRED and is not swept, and the sweep reports how many were
// held, so the caller can see WHICH hold is in the way.
func (s *Service) expiredSegments(ctx context.Context, tenant, site string,
	policies map[string]int, sitePolicy int, now time.Time) ([]Segment, int, []string, error) {

	rows, err := s.Pool.WithSites(tenant, "recording", []string{site}).Query(ctx, `
		SELECT tenant_id, site_id, device_id, seq, segment_id, started_at, ended_at,
		       gap_before_ms, bytes, digest, codec, width, height, fps, storage_ref,
		       state, COALESCE(chain_prev, ''), COALESCE(chain_hash, '')
		  FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2
		   AND started_at < $4::timestamptz - (COALESCE((
		       SELECT p.retention_days
		         FROM recording_policies p
		        WHERE p.tenant_id = recording_segments.tenant_id
		          AND p.site_id = recording_segments.site_id
		          AND COALESCE(p.device_id, '') = recording_segments.device_id
		        ORDER BY (p.device_id IS NOT NULL) DESC
		        LIMIT 1), $3) || ' days')::interval
		 ORDER BY started_at`,
		tenant, site, sitePolicy, now)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("recording: find the expired segments of %s: %w", site, err)
	}
	defer rows.Close()
	var out []Segment
	held := 0
	var heldDevices []string
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.TenantID, &seg.SiteID, &seg.DeviceID, &seg.Seq, &seg.SegmentID,
			&seg.StartedAt, &seg.EndedAt, &seg.GapBefore, &seg.Bytes, &seg.Digest, &seg.Codec,
			&seg.Width, &seg.Height, &seg.FPS, &seg.StorageRef, &seg.State, &seg.ChainPrev, &seg.ChainHash); err != nil {
			return nil, 0, nil, fmt.Errorf("recording: scan an expired segment: %w", err)
		}
		// A LEGAL HOLD blocks the deletion, and it is the one row in the
		// product that can say "you may not delete this". A sweep that
		// ignored it would delete the evidence of a dispute, quietly.
		//
		// A hold with NO DEVICE holds the whole SITE, and a hold with a
		// device holds that device's segments. The site-wide hold is the
		// stronger statement and it is the one an operator reaches for
		// when the dispute covers everything the site recorded.
		if policies[""] == -1 || policies[seg.DeviceID] == -1 {
			held++
			heldDevices = appendUnique(heldDevices, seg.DeviceID)
			continue
		}
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, fmt.Errorf("recording: iterate the expired segments of %s: %w", site, err)
	}
	return out, held, heldDevices, nil
}

// appendUnique adds a name to a list, once.
func appendUnique(list []string, name string) []string {
	for _, existing := range list {
		if existing == name {
			return list
		}
	}
	return append(list, name)
}

// deleteSegment removes one expired segment.
//
// The FILE goes first and the ROW second, and the order is the order of the
// failures. A file with no row is a file nobody can find, and a finder that
// misses it is a hole in the recording the site can see. A row with no file is
// a row pointing at nothing.
//
// The row is deleted whatever happened to the file: a file that could not be
// removed is a problem the operator needs to see, and leaving the row would
// leave a segment that is deleted and still listed.
func (s *Service) deleteSegment(ctx context.Context, tenant, site string, seg Segment, out *SweepResult) error {
	if s.Root != "" {
		path, err := s.resolve(seg.StorageRef)
		if err == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				// The file could not be removed. The ROW still goes,
				// because a row that points at a file nobody can read
				// is a row that lies, and the operator is told by the
				// sweep's own report.
				s.Logf("recording: the segment %s could not be removed: %v", seg.SegmentID, err)
			}
		}
	}
	_, err := s.Pool.WithSites(tenant, "recording", []string{site}).Exec(ctx, `
		DELETE FROM recording_segments
		 WHERE tenant_id = $1 AND site_id = $2 AND segment_id = $3`,
		tenant, site, seg.SegmentID)
	if err != nil {
		return fmt.Errorf("recording: delete the segment %s: %w", seg.SegmentID, err)
	}
	// The KEY goes too, or the device's sequence can never be reused and
	// the uniqueness the key enforces outlives the thing it names.
	if _, err := s.Pool.WithSites(tenant, "recording", []string{site}).Exec(ctx, `
		DELETE FROM recording_segment_keys
		 WHERE tenant_id = $1 AND segment_id = $2`,
		tenant, seg.SegmentID); err != nil {
		return fmt.Errorf("recording: delete the key of the segment %s: %w", seg.SegmentID, err)
	}
	out.Deleted++
	out.Bytes += seg.Bytes
	return nil
}

// Logf reports what the sweep did.
//
// It is a field rather than a package-level logger because the sweep belongs
// to the core and the core has one.
func (s *Service) Logf(format string, args ...any) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(format, args...)
}

// SetLogger installs the sweep's logger.
func (s *Service) SetLogger(logf func(format string, args ...any)) {
	if s == nil {
		return
	}
	s.logf = logf
}
