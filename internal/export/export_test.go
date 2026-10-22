package export_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/export"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The clip.
//
// The claim the clip makes is the one an operator watching an incident
// depends on: the clip includes the seconds BEFORE the moment, because the
// lead-in is where the cause usually is.

// service returns the export service over the package's own database.
func service(t *testing.T) (*export.Service, *recording.Service, string, string) {
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
	tenant, site, _ := testutil.NewTenant(t, raw)
	index := recording.NewService(pool)
	return export.NewService(index), index, tenant, site
}

// writeSegment writes one segment ten seconds long.
func writeSegment(t *testing.T, index *recording.Service, tenant, site string, seq int64, id string, start time.Time) {
	t.Helper()
	seg := recording.Segment{
		TenantID:   tenant,
		SiteID:     site,
		DeviceID:   "cam-1",
		Seq:        seq,
		SegmentID:  id,
		StartedAt:  start,
		EndedAt:    start.Add(10 * time.Second),
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

// repeatHex repeats a character.
func repeatHex(c string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, c[0])
	}
	return string(out)
}

func TestTheClipCarriesThePreRollBeforeTheWindow(t *testing.T) {
	// Three segments, ten seconds each. The window covers the SECOND one,
	// and the pre-roll is the FIRST one -- the seconds before the moment.
	exporter, index, tenant, site := service(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	writeSegment(t, index, tenant, site, 1, "seg-1", base)
	writeSegment(t, index, tenant, site, 2, "seg-2", base.Add(10*time.Second))
	writeSegment(t, index, tenant, site, 3, "seg-3", base.Add(20*time.Second))

	from := base.Add(12 * time.Second)
	to := base.Add(18 * time.Second)
	clip, err := exporter.Clip(context.Background(), tenant, site, "cam-1", from, to)
	if err != nil {
		t.Fatalf("clip: %v", err)
	}
	if clip.PreRoll == nil {
		t.Fatal("the clip has no pre-roll. The seconds BEFORE the moment are where the cause " +
			"usually is, and a clip without them is a clip of the effect")
	}
	if clip.PreRoll.SegmentID != "seg-1" {
		t.Errorf("the pre-roll is %q, want seg-1", clip.PreRoll.SegmentID)
	}
	if len(clip.Segments) != 1 || clip.Segments[0].SegmentID != "seg-2" {
		t.Errorf("the clip covers %v, want just seg-2", clip.Segments)
	}
}

func TestTheClipHasNoPreRollAtTheStartOfTheRecording(t *testing.T) {
	// A window that starts at the beginning of what was recorded has no
	// pre-roll, and that is a FACT about the recording rather than a gap in
	// the clip.
	exporter, index, tenant, site := service(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	writeSegment(t, index, tenant, site, 1, "seg-1", base)

	clip, err := exporter.Clip(context.Background(), tenant, site, "cam-1",
		base, base.Add(5*time.Second))
	if err != nil {
		t.Fatalf("clip: %v", err)
	}
	if clip.PreRoll != nil {
		t.Errorf("the clip has a pre-roll (%q) at the start of the recording", clip.PreRoll.SegmentID)
	}
}

func TestTheClipReportsTheHoleInsideIt(t *testing.T) {
	// A clip that covers a window with a hole in it says so, and says how
	// big the hole is. A clip that quietly skips the hole reads as a
	// continuous recording of something that did not happen.
	exporter, index, tenant, site := service(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	writeSegment(t, index, tenant, site, 1, "seg-1", base)
	// The next segment starts a minute later: a camera that was quiet.
	writeSegment(t, index, tenant, site, 2, "seg-2", base.Add(70*time.Second))

	clip, err := exporter.Clip(context.Background(), tenant, site, "cam-1",
		base, base.Add(90*time.Second))
	if err != nil {
		t.Fatalf("clip: %v", err)
	}
	if len(clip.Gaps) != 1 {
		t.Fatalf("the clip reports %d gap(s), want 1", len(clip.Gaps))
	}
	if clip.Gaps[0].Duration < 50000 {
		t.Errorf("the hole is %dms, want about 60000ms", clip.Gaps[0].Duration.Milliseconds())
	}
}

func TestTheClipRefusesAWindowWithNoCameraInIt(t *testing.T) {
	exporter, _, tenant, site := service(t)
	_, err := exporter.Clip(context.Background(), tenant, site, "",
		time.Now().UTC().Add(-time.Hour), time.Now().UTC())
	if err == nil {
		t.Fatal("the clip was built for a window that names no camera")
	}
}
