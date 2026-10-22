package recording_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The continuity.
//
// The claim is the one a VMS exists to answer: is it recording? And the
// answer is MEASURED — the gap between the segments — rather than inferred
// from whether any incident fired.

// writeSegment writes one segment that ended at the given instant.
func writeSegment(t *testing.T, index *recording.Service, tenant, site, device string, seq int64, id string, ended time.Time) {
	t.Helper()
	seg := recording.Segment{
		TenantID:   tenant,
		SiteID:     site,
		DeviceID:   device,
		Seq:        seq,
		SegmentID:  id,
		StartedAt:  ended.Add(-10 * time.Second),
		EndedAt:    ended,
		Bytes:      1024,
		Digest:     "aa" + repeatHex("0", 62),
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		StorageRef: "site/cam/seg.mp4",
		State:      recording.StateComplete,
	}
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestTheContinuityReportsTheGapBetweenSegments(t *testing.T) {
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	now := time.Now().UTC()
	writeSegment(t, index, tenant, site, "cam-1", 1, "seg-1", now.Add(-40*time.Second))
	writeSegment(t, index, tenant, site, "cam-1", 2, "seg-2", now.Add(-30*time.Second))

	c, err := index.Continuity(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("continuity: %v", err)
	}
	if c.Cameras != 1 {
		t.Errorf("the continuity names %d camera(s), want 1", c.Cameras)
	}
	if c.Segments != 2 {
		t.Errorf("the continuity counts %d segment(s), want 2", c.Segments)
	}
	// The gap between the two segments is ten seconds, and the gap since the
	// last one is about thirty — so the WORST is the one since the last.
	if c.WorstGapSeconds < 25 {
		t.Errorf("the worst gap is %vs, want about 30s: a camera that stopped recording "+
			"thirty seconds ago is quieter than one that had a hole ten seconds ago", c.WorstGapSeconds)
	}
}

func TestTheContinuityReportsTheWorstCamera(t *testing.T) {
	// The question an operator has is "is anything wrong", and one camera
	// that has been dark since Tuesday is a thing that is wrong even when
	// every other camera is perfect.
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	now := time.Now().UTC()
	writeSegment(t, index, tenant, site, "cam-1", 1, "seg-1", now.Add(-20*time.Second))
	writeSegment(t, index, tenant, site, "cam-2", 1, "seg-2", now.Add(-300*time.Second))

	c, err := index.Continuity(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("continuity: %v", err)
	}
	if c.Cameras != 2 {
		t.Errorf("the continuity names %d camera(s), want 2", c.Cameras)
	}
	if c.WorstGapSeconds < 250 {
		t.Errorf("the worst gap is %vs, want about 300s: the camera that stopped five minutes "+
			"ago is the quiet one", c.WorstGapSeconds)
	}
	// And each camera's own gap is published, so a page can name WHICH one.
	if _, ok := c.PerCamera["cam-2"]; !ok {
		t.Errorf("the per-camera gaps do not name cam-2: %v", c.PerCamera)
	}
}

func TestTheContinuityIsEmptyForASiteThatHasNeverRecorded(t *testing.T) {
	// A camera that has never recorded is not a camera that is recording
	// well, and the reader says "no cameras" rather than "a gap of zero".
	pool := indexPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	index := recording.NewService(pool)

	c, err := index.Continuity(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("continuity: %v", err)
	}
	if c.Cameras != 0 {
		t.Errorf("the continuity names %d camera(s), want 0", c.Cameras)
	}
	if c.Segments != 0 {
		t.Errorf("the continuity counts %d segment(s), want 0", c.Segments)
	}
}
