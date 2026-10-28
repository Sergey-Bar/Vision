package recorder_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// TestTheChainVerifiesRepeatedly runs the chain verification 10 times to catch
// the intermittent failure caused by Postgres rounding TIMESTAMPTZ to
// microseconds while chainTime truncates.
func TestTheChainVerifiesRepeatedly(t *testing.T) {
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, pool.Pool)
	index := recording.NewService(pool)
	ctx := context.Background()

	now := time.Now().UTC().Add(-36 * time.Hour).Truncate(24 * time.Hour).Add(time.Hour)

	for i := 0; i < 20; i++ {
		segID := "seg-" + string(rune('a'+i)) + "-" + string(rune('0'+i%10))
		start := now.Add(time.Duration(i) * time.Second)
		writeSegment(t, index, tenant, site, "cam-1", int64(i+1), segID, start)
	}

	for i := 0; i < 20; i++ {
		start := now.Add(time.Duration(i) * time.Second)
		day := start.UTC().Truncate(24 * time.Hour)
		v, err := index.VerifyChain(ctx, tenant, site, "cam-1", day)
		if err != nil {
			t.Fatalf("verify chain day %d: %v", i, err)
		}
		if !v.OK {
			t.Fatalf("chain does not verify for day %d: head=%s expected=%s brokenAt=%d",
				i, v.Recorded, v.Expected, v.BrokenAt)
		}
	}
}

// indexPool opens a migrated database pool for the chain-repeat test.
//
// It skips when there is no test database, the same way every other
// database-backed test in this package does.
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

// writeSegment appends one synthetic segment to the index.
func writeSegment(t *testing.T, index *recording.Service, tenant, site, deviceID string, seq int64, segID string, start time.Time) {
	t.Helper()
	seg := recording.Segment{
		TenantID:   tenant,
		SiteID:     site,
		DeviceID:   deviceID,
		Seq:        seq,
		SegmentID:  segID,
		StartedAt:  start,
		EndedAt:    start.Add(time.Second),
		Bytes:      1024,
		Digest:     segID + strings.Repeat("0", 64-len(segID)),
		Codec:      "h264",
		StorageRef: segID + ".mp4",
		State:      recording.StateComplete,
	}
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append segment %s: %v", segID, err)
	}
}
