package recorder_test

import (
	"context"
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
	tenant, site, _ := testutil.NewTenant(t, pool.Pool())
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
