package recorder_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/recorder"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/rtsp"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The recorder, against a real camera.
//
// The camera is the repository's own RTSP server replaying the committed
// H.264 fixture — a real server, a real session, a real elementary stream —
// and the recording lands on a real disk and in a real database.
//
// A recorder tested against a stub of itself proves that the stub works.

const fixture = "../../testdata/poc/testsrc.h264"

// startCamera serves the fixture over RTSP and returns its URL.
func startCamera(t *testing.T) string {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	nals, err := rtsp.ReadNALs(f)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	srv, err := rtsp.StartServer(context.Background(), rtsp.ServerOptions{
		Listen: "127.0.0.1:0",
		NALs:   nals,
		FPS:    25,
		Loop:   true,
	})
	if err != nil {
		t.Fatalf("start the camera: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	url, err := srv.URL()
	if err != nil {
		t.Fatalf("the camera's URL: %v", err)
	}
	return url
}

// testPool opens the package's own database, migrated.
// testPool opens the package's own database, migrated.
//
// The migration is here rather than left to the caller because the recorder
// writes to tables this migration created, and a test that runs against an
// unmigrated database fails with "relation does not exist" — which reads as
// a product defect and is not one.
func testPool(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply the DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestTheRecorderWritesSegmentsAndIndexesThem(t *testing.T) {
	pool := testPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	root := t.TempDir()
	url := startCamera(t)

	var seen []recording.Segment
	rec := recorder.New(index, recorder.Options{
		Root:            root,
		SegmentDuration: time.Second,
		Now:             func() time.Time { return time.Now().UTC() },
		Logf:            func(string, ...any) {},
	}, func(s recording.Segment) { seen = append(seen, s) })

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	// The camera loops the fixture for ever, so the recording is stopped by
	// the context rather than by the stream ending.
	_ = rec.Run(ctx, recorder.Source{
		TenantID: tenant,
		SiteID:   site,
		DeviceID: "cam-1",
		URL:      url,
	})

	if len(seen) == 0 {
		t.Fatalf("the recorder wrote no segment. The camera serves the fixture in a loop, so " +
			"twelve seconds at a second a segment is about a dozen segments")
	}
	for i, seg := range seen {
		if seg.Bytes == 0 {
			t.Errorf("segment %d has no bytes", i)
		}
		if seg.Digest == "" {
			t.Errorf("segment %d has no digest, so it cannot be chained", i)
		}
		if seg.StorageRef == "" {
			t.Errorf("segment %d has no storage reference", i)
		}
		// The bytes are on the disk, under the reference the index carries.
		full := filepath.Join(root, filepath.FromSlash(seg.StorageRef))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("segment %d is indexed at %s and the file is not there: %v", i, seg.StorageRef, err)
		}
	}
}

func TestTheRecordedChainVerifies(t *testing.T) {
	pool := testPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	root := t.TempDir()
	url := startCamera(t)

	rec := recorder.New(index, recorder.Options{
		Root:            root,
		SegmentDuration: time.Second,
		Now:             func() time.Time { return time.Now().UTC() },
		Logf:            func(string, ...any) {},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = rec.Run(ctx, recorder.Source{
		TenantID: tenant, SiteID: site, DeviceID: "cam-1", URL: url,
	})

	// The reads use a FRESH context: the recording's own context has expired
	// by now, and a read that inherits it measures the recorder's deadline
	// rather than the recording.
	segments, err := index.Timeline(context.Background(), tenant, site, "cam-1",
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("read the timeline: %v", err)
	}
	if len(segments) == 0 {
		t.Fatal("the timeline is empty")
	}
	day := segments[0].StartedAt.UTC().Truncate(24 * time.Hour)
	v, err := index.VerifyChain(context.Background(), tenant, site, "cam-1", day)
	if err != nil {
		t.Fatalf("verify the chain: %v", err)
	}
	if !v.OK {
		t.Errorf("the chain does not verify: the head is %v and the rows produce %v, and the "+
			"first broken segment is %d", v.Recorded, v.Expected, v.BrokenAt)
	}
}

func TestASegmentIsWrittenAtomically(t *testing.T) {
	// The write is a temporary file, an fsync, and a RENAME. A reader either
	// sees the whole segment or no segment at all, and there is no third
	// state in which half a segment is on the disk under its own name.
	//
	// The observable half of that is the temporary file: a segment written
	// in place would leave one of these behind every time it was cut short,
	// and a recorder that leaves half-segments around is a recorder whose
	// disk fills with the pieces of segments nobody can play.
	pool := testPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	root := t.TempDir()
	url := startCamera(t)

	rec := recorder.New(index, recorder.Options{
		Root:            root,
		SegmentDuration: time.Second,
		Now:             func() time.Time { return time.Now().UTC() },
		Logf:            func(string, ...any) {},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = rec.Run(ctx, recorder.Source{
		TenantID: tenant, SiteID: site, DeviceID: "cam-1", URL: url,
	})

	// Nothing half-written is left behind.
	var leftovers int
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".partial") {
			leftovers++
		}
		return nil
	}); err != nil {
		t.Fatalf("walk the recording root: %v", err)
	}
	if leftovers != 0 {
		t.Errorf("%d temporary file(s) are left in the recording root. A segment written in "+
			"place rather than renamed into place leaves one behind every time it is cut short",
			leftovers)
	}

	// And the segments that ARE there are whole: each one is a file whose
	// size is the size the index says.
	segments, err := index.Timeline(context.Background(), tenant, site, "cam-1",
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("read the timeline: %v", err)
	}
	if len(segments) == 0 {
		t.Fatal("the timeline is empty")
	}
	for _, seg := range segments {
		full := filepath.Join(root, filepath.FromSlash(seg.StorageRef))
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("the segment is indexed at %s and the file is not there: %v", seg.StorageRef, err)
			continue
		}
		if info.Size() != seg.Bytes {
			t.Errorf("the segment at %s is %d bytes on the disk and %d in the index",
				seg.StorageRef, info.Size(), seg.Bytes)
		}
	}
}

func TestTheChainBreaksWhenASegmentIsModified(t *testing.T) {
	// The chain is the PROOF, and a proof that cannot be broken is not a
	// proof. This is the test that says the chain has teeth: one byte changed
	// in one segment, and the chain from that point on no longer verifies.
	pool := testPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	segments := []recording.Segment{
		{TenantID: tenant, SiteID: site, DeviceID: "cam-1", Seq: 1, SegmentID: "s1",
			StartedAt: time.Now().UTC().Truncate(time.Second), EndedAt: time.Now().UTC(),
			Bytes: 10, Digest: "aa" + repeat("0", 62), StorageRef: "a/b", State: recording.StateComplete},
		{TenantID: tenant, SiteID: site, DeviceID: "cam-1", Seq: 2, SegmentID: "s2",
			StartedAt: time.Now().UTC().Truncate(time.Second).Add(time.Second),
			EndedAt:   time.Now().UTC().Add(time.Second),
			Bytes:     10, Digest: "bb" + repeat("0", 62), StorageRef: "c/d", State: recording.StateComplete},
	}
	for _, seg := range segments {
		if err := index.Append(ctx(), seg); err != nil {
			t.Fatalf("append the segment: %v", err)
		}
	}
	day := segments[0].StartedAt.UTC().Truncate(24 * time.Hour)
	v, err := index.VerifyChain(ctx(), tenant, site, "cam-1", day)
	if err != nil {
		t.Fatalf("verify the chain: %v", err)
	}
	if !v.OK {
		t.Fatalf("a chain of two untouched segments does not verify: %v", v)
	}

	// Now change one byte of the first segment's digest, the way an edit
	// would.
	if _, err := testutil.Pool(t).Exec(ctx(),
		`UPDATE recording_segments SET digest = $1 WHERE tenant_id = $2 AND segment_id = $3`,
		"cc"+repeat("0", 62), tenant, "s1"); err != nil {
		t.Fatalf("modify the segment: %v", err)
	}
	v2, err := index.VerifyChain(ctx(), tenant, site, "cam-1", day)
	if err != nil {
		t.Fatalf("verify the chain again: %v", err)
	}
	if v2.OK {
		t.Fatal("the chain verifies after a segment was modified. A chain that cannot be broken " +
			"is not a proof")
	}
	if v2.BrokenAt == 0 {
		t.Errorf("the chain does not say WHERE it broke: %+v", v2)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}

func ctx() context.Context { return context.Background() }
