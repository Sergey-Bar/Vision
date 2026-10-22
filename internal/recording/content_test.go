package recording_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The recorded BYTES and the retention sweep.
//
// The two claims here are the ones an operator's compliance depends on:
//
//   - the bytes served are the bytes the index recorded (the digest is
//     checked, and a file that was edited is REFUSED rather than served);
//   - a segment is kept for as long as the policy says, and a LEGAL HOLD
//     blocks the deletion.

// recordingRoot returns a root with one segment's bytes in it.
func recordingRoot(t *testing.T, ref, body string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatalf("write the segment: %v", err)
	}
	return root
}

func TestTheContentReaderServesTheBytesTheIndexRecorded(t *testing.T) {
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	body := "the segment's bytes"
	root := recordingRoot(t, "site/cam/seg.mp4", body)
	index.Root = root

	seg := segment(tenant, site, 1, "seg-1", time.Now().UTC().Add(-time.Minute))
	seg.Digest = digestOf(body)
	seg.StorageRef = "site/cam/seg.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}

	content, err := index.Open(context.Background(), tenant, site, "seg-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = content.Reader.Close() }()
	got := make([]byte, len(body))
	if _, err := content.Reader.Read(got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("the bytes are %q, want %q", string(got), body)
	}
}

func TestTheContentReaderRefusesBytesThatNoLongerMatch(t *testing.T) {
	// A segment that was edited on the disk is REPORTED and not served. An
	// operator watching footage that cannot be proven to be the footage the
	// index names is worse off than being told the file is wrong.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	root := recordingRoot(t, "site/cam/seg.mp4", "the ORIGINAL bytes")
	index.Root = root

	seg := segment(tenant, site, 1, "seg-1", time.Now().UTC().Add(-time.Minute))
	seg.Digest = digestOf("the ORIGINAL bytes")
	seg.StorageRef = "site/cam/seg.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}

	// And then somebody edits the file.
	path := filepath.Join(root, filepath.FromSlash("site/cam/seg.mp4"))
	if err := os.WriteFile(path, []byte("the EDITED bytes"), 0o640); err != nil {
		t.Fatalf("edit the segment: %v", err)
	}

	_, err := index.Open(context.Background(), tenant, site, "seg-1")
	if err == nil {
		t.Fatal("the reader served bytes that do not match the index")
	}
	if !strings.Contains(err.Error(), "do not match") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestTheContentReaderRefusesAReferenceThatNamesSomethingOutsideTheRoot(t *testing.T) {
	// The reference comes out of a DATABASE, and a database is not a trusted
	// source of a path. So the resolved path is checked for CONTAINMENT, not
	// just the string for shape.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not evidence"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	index.Root = root

	for _, ref := range []string{
		"../" + filepath.Base(outside) + "/secret.txt",
		"/etc/passwd",
		"a/../../b",
	} {
		seg := segment(tenant, site, 1, "seg-"+ref, time.Now().UTC())
		seg.Digest = digestOf("x")
		seg.StorageRef = ref
		if err := index.Append(context.Background(), seg); err != nil {
			t.Fatalf("append: %v", err)
		}
		_, err := index.Open(context.Background(), tenant, site, "seg-"+ref)
		if err == nil {
			t.Errorf("the reader served the reference %q", ref)
			continue
		}
		if !strings.Contains(err.Error(), "rejected") && !strings.Contains(err.Error(), "no such segment") {
			t.Errorf("the refusal for %q does not say why: %v", ref, err)
		}
	}
}

func TestTheSweepDeletesWhatThePolicyNoLongerKeeps(t *testing.T) {
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	root := recordingRoot(t, "site/cam/old.mp4", "old bytes")
	index.Root = root

	seg := segment(tenant, site, 1, "seg-old", old)
	seg.Digest = digestOf("old bytes")
	seg.StorageRef = "site/cam/old.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}
	// The policy keeps 30 days.
	if err := writeRetentionPolicy(t, pool, tenant, site, "", 30, false); err != nil {
		t.Fatalf("write the policy: %v", err)
	}

	result, err := index.Sweep(context.Background(), tenant, site, time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Deleted != 1 {
		t.Errorf("the sweep deleted %d segment(s), want 1", result.Deleted)
	}
	// The FILE goes too.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("site/cam/old.mp4"))); !os.IsNotExist(err) {
		t.Errorf("the segment's file survived the sweep: %v", err)
	}
	// And the ROW.
	segments, err := index.Timeline(context.Background(), tenant, site, "cam-1",
		old.Add(-time.Hour), old.Add(time.Hour))
	if err != nil {
		t.Fatalf("read the timeline: %v", err)
	}
	if len(segments) != 0 {
		t.Errorf("the sweep left %d row(s) behind", len(segments))
	}
}

func TestTheSweepKeepsWhatThePolicyStillWants(t *testing.T) {
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	recent := time.Now().UTC().Add(-time.Hour)
	root := recordingRoot(t, "site/cam/recent.mp4", "recent bytes")
	index.Root = root

	seg := segment(tenant, site, 1, "seg-recent", recent)
	seg.Digest = digestOf("recent bytes")
	seg.StorageRef = "site/cam/recent.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := writeRetentionPolicy(t, pool, tenant, site, "", 30, false); err != nil {
		t.Fatalf("write the policy: %v", err)
	}

	result, err := index.Sweep(context.Background(), tenant, site, time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("the sweep deleted %d segment(s), want 0: the policy keeps them", result.Deleted)
	}
}

func TestTheSweepRefusesToDeleteWhatALegalHoldProtects(t *testing.T) {
	// A legal hold is the one row in the product that can say "you may not
	// delete this". A sweep that ignored it would delete the evidence of a
	// dispute, and it would do so quietly.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	root := recordingRoot(t, "site/cam/held.mp4", "held bytes")
	index.Root = root

	seg := segment(tenant, site, 1, "seg-held", old)
	seg.Digest = digestOf("held bytes")
	seg.StorageRef = "site/cam/held.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := writeRetentionPolicy(t, pool, tenant, site, "", 30, true); err != nil {
		t.Fatalf("write the policy: %v", err)
	}

	result, err := index.Sweep(context.Background(), tenant, site, time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("the sweep deleted %d segment(s) under a legal hold", result.Deleted)
	}
	if result.Held != 1 {
		t.Errorf("the sweep reports %d held segment(s), want 1: an operator has to be able to "+
			"see WHICH hold is in the way", result.Held)
	}
	// And the bytes are still there.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("site/cam/held.mp4"))); err != nil {
		t.Errorf("the held segment's file was removed: %v", err)
	}
}

func TestTheSweepKeepsADeviceTheOperatorKeptLonger(t *testing.T) {
	// A device's own policy wins over the site's, because a device the
	// operator has singled out is a device they have said something about.
	// A single cut-off for the whole site would delete that device's footage
	// because ANOTHER device was kept longer.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	root := recordingRoot(t, "site/cam/kept.mp4", "kept bytes")
	index.Root = root

	seg := segment(tenant, site, 1, "seg-kept", old)
	seg.DeviceID = "cam-kept"
	seg.Digest = digestOf("kept bytes")
	seg.StorageRef = "site/cam/kept.mp4"
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}
	// The SITE keeps 30 days; the DEVICE keeps 90.
	if err := writeRetentionPolicy(t, pool, tenant, site, "", 30, false); err != nil {
		t.Fatalf("write the site policy: %v", err)
	}
	if err := writeRetentionPolicy(t, pool, tenant, site, "cam-kept", 90, false); err != nil {
		t.Fatalf("write the device policy: %v", err)
	}

	result, err := index.Sweep(context.Background(), tenant, site, time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("the sweep deleted %d segment(s), want 0: the device's own policy keeps them",
			result.Deleted)
	}
}

// digestOf is the hex SHA-256 of some bytes.
func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// writeRetentionPolicy writes one retention policy.
//
// An empty device is the SITE's own policy, which every device inherits.
func writeRetentionPolicy(t *testing.T, pool *dbx.Pool, tenant, site, device string, days int, hold bool) error {
	t.Helper()
	var deviceArg any
	if device != "" {
		deviceArg = device
	}
	_, err := pool.WithSites(tenant, "recording", []string{site}).Exec(context.Background(), `
		INSERT INTO recording_policies
		    (tenant_id, site_id, device_id, continuous, pre_roll_s, post_roll_s,
		     segment_s, retention_days, legal_hold)
		VALUES ($1, $2, $3, true, 15, 15, 10, $4, $5)`,
		tenant, site, deviceArg, days, hold)
	return err
}
