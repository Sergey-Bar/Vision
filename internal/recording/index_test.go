package recording_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The index, over a real database.
//
// The chain is the PROOF, and the proof rests on two things the reader has
// to check: that the chain verifies from the rows, and that the SEQUENCE the
// chain is over is the device's and not the day's.

// indexPool opens the package's own database, migrated.
func indexPool(t *testing.T) *dbx.Pool {
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

// segment is one segment to write.
func segment(tenant, site string, seq int64, id string, started time.Time) recording.Segment {
	return recording.Segment{
		TenantID:   tenant,
		SiteID:     site,
		DeviceID:   "cam-1",
		Seq:        seq,
		SegmentID:  id,
		StartedAt:  started,
		EndedAt:    started.Add(10 * time.Second),
		Bytes:      1024,
		Digest:     "aa" + repeatHex("0", 62),
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		StorageRef: "a/b/c.mp4",
		State:      recording.StateComplete,
	}
}

// repeatHex repeats a character.
func repeatHex(c string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, c[0])
	}
	return string(out)
}

func TestTheSequenceIsTheDevicesAndNotTheDays(t *testing.T) {
	// The chain is per DAY so that it can be SEALED per day, and the
	// segment's uniqueness is per DEVICE across every day.
	//
	// A sequence that restarted at midnight would collide with the segment
	// that took the same number yesterday, and the collision is a unique
	// violation the writer cannot tell from a genuine duplicate — so the
	// second day's first segment would be REFUSED and the recording would
	// stop at midnight.
	//
	// This is the defect the recorder found in the index: the sequence came
	// from the day's segment COUNT, which is the day's count and not the
	// device's.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)
	ctx := context.Background()

	yesterday := time.Now().UTC().Add(-36 * time.Hour).Truncate(24 * time.Hour).Add(time.Hour)
	today := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)

	// The first segment of YESTERDAY.
	if err := index.Append(ctx, segment(tenant, site, 1, "s-yesterday-1", yesterday)); err != nil {
		t.Fatalf("append yesterday's segment: %v", err)
	}
	// The first segment of TODAY. It is the SECOND segment of the device,
	// and its sequence must say so.
	if err := index.Append(ctx, segment(tenant, site, 2, "s-today-1", today)); err != nil {
		t.Fatalf("append today's segment: %v", err)
	}

	segments, err := index.Timeline(ctx, tenant, site, "cam-1",
		today.Add(-48*time.Hour), today.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("read the timeline: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("the timeline holds %d segment(s), want 2", len(segments))
	}
	byID := map[string]int64{}
	for _, s := range segments {
		byID[s.SegmentID] = s.Seq
	}
	if byID["s-yesterday-1"] != 1 {
		t.Errorf("yesterday's segment took sequence %d, want 1", byID["s-yesterday-1"])
	}
	if byID["s-today-1"] != 2 {
		t.Errorf("today's segment took sequence %d, want 2. The sequence is the DEVICE's and "+
			"it does not restart at midnight: a sequence that restarts collides with the "+
			"segment that took the same number yesterday", byID["s-today-1"])
	}
}

func TestTheChainCrossesTheDayBoundary(t *testing.T) {
	// The chain is SEALED per day and it CROSSES the boundary, so removing a
	// whole day leaves a chain that does not verify.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)
	ctx := context.Background()

	yesterday := time.Now().UTC().Add(-36 * time.Hour).Truncate(24 * time.Hour).Add(time.Hour)
	today := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)

	if err := index.Append(ctx, segment(tenant, site, 1, "s-yesterday-1", yesterday)); err != nil {
		t.Fatalf("append yesterday's segment: %v", err)
	}
	if err := index.Append(ctx, segment(tenant, site, 2, "s-today-1", today)); err != nil {
		t.Fatalf("append today's segment: %v", err)
	}

	// Today's chain verifies.
	v, err := index.VerifyChain(ctx, tenant, site, "cam-1", today.Truncate(24*time.Hour))
	if err != nil {
		t.Fatalf("verify today's chain: %v", err)
	}
	if !v.OK {
		t.Errorf("today's chain does not verify: recorded %q, expected %q",
			v.Recorded, v.Expected)
	}
}
