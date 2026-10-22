package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/recording"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The recording routes.
//
// The product's claim about a recording is that it can PROVE it, and the
// routes are where that claim is either kept or broken. The tests below are
// about the three things an operator is actually asking:
//
//   - what did this camera record?
//   - is the recording continuous?
//   - can the recording be proved?
//
// And the one thing the product must never do: report a camera as recording
// when it recorded nothing.

// indexPool opens the package's own database for the recording index.
//
// It is the same database the API server reads, because `testutil.Pool` points
// `SP_TEST_DATABASE_URL` at the package's database and every reader of it
// lands in the same place. A second database would be a test that passed
// against rows nobody else can see.
func indexPool(t *testing.T) *dbx.Pool {
	t.Helper()
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedSegment puts one segment in the index.
func seedSegment(t *testing.T, index *recording.Service, tenant, site, device string, seq int64,
	started time.Time) {

	t.Helper()
	seg := recording.Segment{
		TenantID:   tenant,
		SiteID:     site,
		DeviceID:   device,
		Seq:        seq,
		SegmentID:  device + "-" + strconv.FormatInt(seq, 10),
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
	if err := index.Append(context.Background(), seg); err != nil {
		t.Fatalf("append the segment: %v", err)
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

func TestTheRecordingListNamesTheSiteAndTheWindow(t *testing.T) {
	s, tenant, site := server(t)
	seedSegment(t, recording.NewService(indexPool(t)), tenant, site, "cam-1", 1, time.Now().UTC().Add(-time.Minute))
	seedSegment(t, recording.NewService(indexPool(t)), tenant, site, "cam-2", 1, time.Now().UTC().Add(-time.Minute))

	rec := call(t, s, http.MethodGet, "/v1/recordings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the recording list answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Available bool   `json:"available"`
		SiteID    string `json:"siteId"`
		Items     []struct {
			SegmentID string `json:"segmentId"`
			Digest    string `json:"digest"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Available {
		t.Error("the recording list says the index is not configured, but it is")
	}
	if body.SiteID != site {
		t.Errorf("the list is for site %q, want %q", body.SiteID, site)
	}
	if len(body.Items) != 2 {
		t.Fatalf("the list holds %d segment(s), want 2", len(body.Items))
	}
	if body.Items[0].Digest == "" {
		t.Error("the segment carries no digest, so it cannot be proved")
	}
}

func TestTheTimelineOfACameraThatRecordedNothingSaysSo(t *testing.T) {
	// The claim this protects: a camera that has never recorded is NOT a
	// camera whose recording is healthy. The two are different facts, and
	// only one of them is true.
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/recordings/timeline?deviceId=cam-nothing", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the timeline answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Verdict struct {
			State string `json:"state"`
		} `json:"verdict"`
		Continuity struct {
			SegmentCount int `json:"segmentCount"`
		} `json:"continuity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict.State != "no_recording" {
		t.Errorf("the verdict is %q, want no_recording. A camera that recorded nothing is not "+
			"a camera whose recording is healthy", body.Verdict.State)
	}
	if body.Continuity.SegmentCount != 0 {
		t.Errorf("the continuity counts %d segment(s) for a camera that recorded nothing",
			body.Continuity.SegmentCount)
	}
}

func TestTheTimelineReportsAHoleInTheRecording(t *testing.T) {
	// Two segments with a hole between them. The hole is the fact the
	// product exists to publish, and it is published with its SIZE.
	s, tenant, site := server(t)
	index := recording.NewService(indexPool(t))
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	seedSegment(t, index, tenant, site, "cam-1", 1, start)
	// The next segment starts a minute later: a camera that was quiet for
	// fifty seconds.
	seedSegment(t, index, tenant, site, "cam-1", 2, start.Add(70*time.Second))

	rec := call(t, s, http.MethodGet, "/v1/recordings/timeline?deviceId=cam-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the timeline answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Verdict struct {
			State string `json:"state"`
		} `json:"verdict"`
		Gaps []struct {
			DurationMs int64 `json:"durationMs"`
		} `json:"gaps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict.State != "broken" {
		t.Errorf("the verdict is %q, want broken: there is a minute of silence between the "+
			"two segments", body.Verdict.State)
	}
	if len(body.Gaps) != 1 {
		t.Fatalf("the timeline publishes %d gap(s), want 1", len(body.Gaps))
	}
	if body.Gaps[0].DurationMs < 50000 {
		t.Errorf("the hole is %dms, want about 60000ms", body.Gaps[0].DurationMs)
	}
}

func TestTheTimelineOfAContinuousRecordingSaysContinuous(t *testing.T) {
	s, tenant, site := server(t)
	index := recording.NewService(indexPool(t))
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	// Two segments that touch: the second starts where the first ends.
	seedSegment(t, index, tenant, site, "cam-1", 1, start)
	seedSegment(t, index, tenant, site, "cam-1", 2, start.Add(10*time.Second))

	rec := call(t, s, http.MethodGet, "/v1/recordings/timeline?deviceId=cam-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the timeline answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Verdict struct {
			State string `json:"state"`
		} `json:"verdict"`
		Gaps []any `json:"gaps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verdict.State != "continuous" {
		t.Errorf("the verdict is %q, want continuous", body.Verdict.State)
	}
	if len(body.Gaps) != 0 {
		t.Errorf("the timeline publishes %d gap(s) for a continuous recording", len(body.Gaps))
	}
}

func TestTheChainRouteVerifiesARecordingThatIsSound(t *testing.T) {
	s, tenant, site := server(t)
	index := recording.NewService(indexPool(t))
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	seedSegment(t, index, tenant, site, "cam-1", 1, start)
	seedSegment(t, index, tenant, site, "cam-1", 2, start.Add(10*time.Second))

	rec := call(t, s, http.MethodGet, "/v1/recordings/chain?deviceId=cam-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the chain answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Verified     bool   `json:"verified"`
		RecordedHead string `json:"recordedHead"`
		ExpectedHead string `json:"expectedHead"`
		SegmentCount int    `json:"segmentCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Verified {
		t.Errorf("the chain of an untouched recording does not verify: recorded %q, expected %q",
			body.RecordedHead, body.ExpectedHead)
	}
	if body.RecordedHead != body.ExpectedHead {
		t.Errorf("the recorded head is %q and the recomputed head is %q", body.RecordedHead, body.ExpectedHead)
	}
}

func TestTheChainRouteSaysWhenACameraRecordedNothingThatDay(t *testing.T) {
	// A device with no chain is a FACT about the device, not an error, so
	// the route answers 200 with a reason rather than a 404 that reads as
	// "the route is broken".
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/recordings/chain?deviceId=cam-nothing", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the chain answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Verified bool   `json:"verified"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Verified {
		t.Error("a camera with no chain reports a verified chain")
	}
	if body.Reason == "" {
		t.Error("the answer says no chain is verified and does not say why")
	}
}

func TestTheRecordingRoutesRefuseASiteThePrincipalIsNotAssignedTo(t *testing.T) {
	// The scope is the PRINCIPAL's, and a `siteId` in the query is CHECKED
	// against it rather than believed. A value a caller sends is an
	// instruction and not an authorisation.
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/recordings?siteId=somebody-elses-site", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the recording list answered %d for a site outside the scope: %s",
			rec.Code, rec.Body.String())
	}
}

func TestTheRecordingRoutesRefuseAMalformedWindow(t *testing.T) {
	// A window that cannot be parsed is a REFUSAL with the reason, not a
	// window of everything. The answer to "what changed since yesterday"
	// must never be "everything" when the question was "what changed since
	// yesterday".
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/recordings?from=yesterday", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the recording list answered %d for a malformed window: %s",
			rec.Code, rec.Body.String())
	}
}

func TestTheRecordingRoutesRefuseAWindowWiderThanTheCeiling(t *testing.T) {
	// A year of one camera is 315 360 rows. The route refuses the window
	// with the reason rather than silently clamping it, because a clamped
	// window answers a different question from the one that was asked.
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet,
		"/v1/recordings?from=2000-01-01T00:00:00Z&to=2030-01-01T00:00:00Z", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the recording list answered %d for a thirty-year window: %s",
			rec.Code, rec.Body.String())
	}
}

func TestTheTimelineRefusesACameraItWasNotAskedAbout(t *testing.T) {
	// A timeline is a CAMERA's timeline. Asking for the timeline of
	// nothing is a request that named nothing.
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/v1/recordings/timeline", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the timeline answered %d with no camera named: %s", rec.Code, rec.Body.String())
	}
}
