package soak_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// stubSidecar answers every frame with one car box that walks from one side of
// the line to the other. It is a STUB, and it is declared as one: a real COCO
// model does not see a synthetic rectangle, and pretending otherwise by tuning a
// threshold against synthetic material would be fitting the measurement to the
// fixture.
//
// What this test proves is the PIPELINE: frames reach a detector, a crossing
// becomes a committed row, the row becomes a stored clip, the clip is read back
// and hashed, an event is pushed, a read-back finds it, and the report scores
// the result. The model's accuracy is explicitly not what is under test, and
// plan §12 forbids the report from claiming it.
type stubSidecar struct {
	seq      atomic.Int64
	inflight atomic.Int64
	boxes    atomic.Int64
	// sawFrame counts the frames that actually arrived, which is the assertion
	// that the reader and the sidecar are wired to each other at all.
	sawFrame atomic.Int64
	x0       atomic.Int64
}

func (s *stubSidecar) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		_, _ = w.Write([]byte(`{"ready":true,"model":"stub","input_width":640,"input_height":384,"providers":["stub"]}`))
		return
	case "/infer":
	}
	if r.URL.Path != "/infer" {
		http.Error(w, "no such path", http.StatusNotFound)
		return
	}
	n, _ := strconv.Atoi(r.Header.Get("X-Frame-Width"))
	m, _ := strconv.Atoi(r.Header.Get("X-Frame-Height"))
	if n == 0 || m == 0 {
		http.Error(w, "missing shape headers", http.StatusBadRequest)
		return
	}
	body := make([]byte, r.ContentLength)
	if _, err := r.Body.Read(body); err != nil && len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	if len(body) != n*m*3 {
		http.Error(w, fmt.Sprintf("body is %d bytes, want %d", len(body), n*m*3), http.StatusBadRequest)
		return
	}
	s.sawFrame.Add(1)
	seq := s.seq.Add(1)
	// The box walks from below the line to above it ONE STEP AT A TIME.
	//
	// Two things this shape has to get right, and both were found by running it:
	//   - the steps must be SMALL. A single jump is a new track on the far side,
	//     and a new track on the far side is correctly not a crossing.
	//   - the crossing must happen after at least one SEGMENT has closed. A clip
	//     cannot be cut from a segment ffmpeg is still writing, so a crossing in
	//     the first four seconds has its evidence refused — correctly, and
	//     silently, which is why the test waits.
	const settle = 25
	const walk = 20
	f := seq - 1
	x := float64(s.x0.Load()) / 1000
	y := 0.78
	if f >= settle {
		y = 0.78 - 0.56*float64(f-settle)/float64(walk-1)
	}
	if y < 0.12 {
		y = 0.12
	}
	box := map[string]any{
		"cls": "car", "score": 0.91,
		"x1": x + 0.05, "y1": y, "x2": x + 0.25, "y2": y + 0.12,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"seq": seq, "boxes": []any{box},
		"width": n, "height": m, "model": "stub", "infer_ms": 1.0,
		// The telemetry every sidecar must now send. Without it the tripwire
		// refuses the frame as `cannot_tell` — correctly: a sidecar that cannot
		// describe how it decoded has not earned the right to have its boxes
		// counted as objects. That is the four-bug collapse's lesson turned into
		// a wire requirement, and this stub has to obey it like the real one.
		"telemetry": map[string]any{
			"layout": "nchw", "decoder_family": "fused", "coord_space": "pixel",
			"has_objectness": false, "fields": 84, "classes": 80,
			"candidates": 1200, "pre_nms": 4, "post_nms_class": 2, "post_nms": 2,
			"dropped_degenerate": 0, "dropped_tiny": 1,
			"nms_class_iou": 0.45, "nms_global_iou": 0.75,
			"median_box_area": 0.01, "median_object_px": 40,
			"identical_geometry": 0, "scales": 1, "note": "",
		},
	})
}

// TestTheWholePipelineFromAFrameToAnEventIsProven is the proof the seven-day run
// is supposed to rest on, at a size a test can actually run.
//
// It is deliberately an integration test over REAL components — real ffmpeg, a
// real recorder, the real detector, a real SQLite store, the real content
// store, the real Milestone client — with ONE thing stubbed: the model's
// accuracy. Every number the pipeline would put in the report is produced for
// real, and the report is scored from the store afterwards.
//
// A committed test clip is synthetic and a COCO detector cannot see it. That is
// not a gap in this test; it is the reason this test exists.
func TestTheWholePipelineFromAFrameToAnEventIsProven(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg is a hard runtime dependency of this module (D2), so this test fails rather than skips: without it there is no source, no recording and no frames")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal("ffprobe is a hard runtime dependency of this module (D2): without it no clip can be verified")
	}
	work := t.TempDir()

	// The VMS-side identifiers a real server issues. They are constants here because
	// the test is about the pipeline, not about a VMS's configuration.
	const eventTypeGUID = "b4d1e1c2-0000-4000-8000-000000000001"
	const cameraGUID = "0d9a1b2c-1111-2222-3333-444455556666"

	// --- the source, generated here so the test is self-contained -----------
	clip := filepath.Join(work, "src.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=16:size=640x360:rate=10",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10", clip).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not render the source: %v: %s", err, out)
	}

	// --- the stub sidecar ----------------------------------------------------
	stub := &stubSidecar{}
	stub.x0.Store(100)
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer srv.Close()
	client, err := ai.New(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Probe(testCtx()); err != nil {
		t.Fatalf("the stub sidecar is not ready: %v", err)
	}

	// --- the push target, a real simulator over a real socket ---------------
	sim := xpsim.New()
	simSrv := httptest.NewServer(sim.Handler())
	defer simSrv.Close()

	// --- the store, the evidence store, the zone ----------------------------
	st, err := store.Open(work)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ev, err := evidence.Open(filepath.Join(work, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	zonePath := filepath.Join(work, "zones.json")
	if err := os.WriteFile(zonePath, []byte(`{
	  "$comment": "written by the integration test; the line is at the middle of the frame",
	  "source": "generated",
	  "frame": {"width": 640, "height": 360},
	  "lines": [{"name":"kerb","a":[0.0,0.5],"b":[1.0,0.5],"direction":"left-to-right","classes":["car"]}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	zone, err := detect.LoadZone(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := detect.New(zone, detect.Config{
		ScoreThreshold: 0.3, Classes: []string{"car"}, IoU: 0.3,
		MaxAge: 2 * time.Second, Cooldown: time.Second, Tolerance: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}

	// --- the recorder: real ffmpeg, one process, two outputs ----------------
	rolling := filepath.Join(work, "rolling")
	rec, err := recorder.New(recorder.Options{
		FFmpeg:         "ffmpeg",
		FFmpegArgs:     []string{"-re", "-stream_loop", "-1", "-i", clip},
		RollingDir:     rolling,
		SegmentSeconds: 4,
		FPS:            5,
		Width:          320,
		Backpressure:   20 * time.Second,
		QueueDepth:     16,
		WatchInterval:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Start(testCtx()); err != nil {
		t.Fatal(err)
	}
	defer rec.Stop(3 * time.Second)

	// --- the push client ---------------------------------------------------
	push, err := xprotect.New(xprotect.Config{
		BaseURL: simSrv.URL, Timeout: 5 * time.Second,
		Credentials: xprotect.Credentials{
			ClientID: "GrantValidatorClient", ClientSecret: "integration",
			Username: "integration", Password: "integration",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := push.Authenticate(testCtx()); err != nil {
		t.Fatal(err)
	}

	runID := "e2e"
	if err := st.StartRun(testCtx(), store.Run{
		ID: runID, Label: "integration", Mode: "demo-replay", SourceKind: "loop", SourceArg: clip,
		PushBaseURL: simSrv.URL, ClockSource: "host",
		StartedAt: time.Now().Add(-time.Second), TargetSeconds: 20,
		Note: "an integration test: the model's accuracy is stubbed, the pipeline is real",
	}); err != nil {
		t.Fatal(err)
	}

	runner, err := soak.NewRunner(soak.RunnerOptions{
		RunID: runID, DataRoot: work, Store: st, Evidence: ev,
		Client: client, XProtect: push, Engine: engine, Recorder: rec,
		Clipper: recorder.Clipper{FFmpeg: "ffmpeg", RollingDir: rolling},
		Zone:    zone,
		// The snapshot is rendered from the newest frame off the pipe, so the
		// runner is given the same reader `watch` gives it. Without this the
		// crossing-snapshot path is skipped for the whole run and the assertions
		// below would be assertions about nothing.
		LastJPEG:   rec.LastFrameJPEG,
		Provenance: store.ProvenanceSynthetic,
		ClipBefore: 1 * time.Second, ClipAfter: 1 * time.Second,
		Target: 16 * time.Second, Heartbeat: 2 * time.Second,
		PushMax: 4, MaxDisk: 1 << 30,
		DeclaredRSS: 1 << 30, DeclaredDB: 1 << 28, DeclaredCPU: 400,
		DeclaredAIBudget: 4 * time.Second,
		PushBaseURL:      simSrv.URL, PushIsVMS: false, ClockSource: "host",
		EventTypeGUID: eventTypeGUID, CameraGUID: cameraGUID,
		Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithTimeout(testCtx(), 40*time.Second)
	defer cancel()
	if err := runner.Run(runCtx); err != nil {
		t.Fatalf("the run ended with %v", err)
	}
	runner.StopPump()
	if err := st.EndRun(testCtx(), runID, time.Now().UTC(), "integration test finished"); err != nil {
		t.Fatal(err)
	}

	// --- the assertions, in the order the plan states them -------------------
	if got := stub.sawFrame.Load(); got < 20 {
		t.Fatalf("the sidecar received %d frames; the reader and the sidecar are not wired to each other", got)
	}

	recent, _ := st.Recent(testCtx(), runID, 20)
	counts, err := st.CountByStatus(testCtx(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if counts["accepted"]+counts["clip_ok"]+counts["clip_refused"]+counts["push_sent"]+
		counts["push_refused"]+counts["push_dead"]+counts["done"] == 0 {
		t.Fatalf("no detection was recorded at all: %v", counts)
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	t.Logf("detections by status: %v", counts)

	// AC2: nothing left non-terminal.
	stalled, err := st.Stalled(testCtx(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stalled) != 0 {
		t.Fatalf("%d detection(s) are still non-terminal: %+v", len(stalled), stalled)
	}

	// AC3: whatever clip was stored reads back to its recorded hash. A detection
	// whose clip was REFUSED is a correct outcome — a segment ffmpeg is still
	// writing cannot be cut from, and the refusal is recorded with its reason —
	// so the assertion is about integrity, not about how many clips exist.
	stats, err := st.VerifyStats(testCtx(), runID, func(e store.Evidence) (string, error) {
		sum, _, err := ev.Get(e.SHA256)
		return sum, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Mismatched != 0 {
		t.Fatalf("%d stored clip(s) read back with different bytes: %+v", stats.Mismatched, stats)
	}
	for _, d := range recent {
		if d.ClipRef != "" && stats.Stored == 0 {
			t.Fatalf("detection %s claims clip %q but no evidence row is joined to it", d.ID, d.ClipRef)
		}
	}
	t.Logf("evidence: %d stored, %d verified, %d mismatched", stats.Stored, stats.Verified, stats.Mismatched)

	// B2: the pushed payload's boundingBox is the REAL box, and it is the same
	// box the row holds. This is a COMPARISON and not an eyeball, because the
	// defect it closes was invisible: the row carried real numbers, the event on
	// the wire carried zeros, and every test in the module was green while it
	// lasted.
	//
	// The outbox row holds the exact bytes that were meant to be sent, so
	// comparing it against `detections.box_*` compares what left the process with
	// what the database says about the same detection. There is no third place
	// the numbers could have come from.
	checkedPayloads := 0
	for _, d := range recent {
		rows, err := st.OutboxFor(testCtx(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			var body xprotect.EventRequest
			if err := json.Unmarshal([]byte(row.Payload), &body); err != nil {
				t.Fatalf("the outbox payload for %s is not a readable event body: %v\n%s", d.ID, err, row.Payload)
			}
			got := body.Data.Object.BoundingBox
			if got == (xprotect.Box{}) {
				t.Fatalf("detection %s pushed an ALL-ZERO boundingBox while its row holds top=%d left=%d bottom=%d right=%d. "+
					"A bounding box is what makes a VMS draw the object on the operator's timeline, and zeros are how this module "+
					"showed a working detector as a broken product", d.ID, d.BoxTop, d.BoxLeft, d.BoxBottom, d.BoxRight)
			}
			want := xprotect.Box{Top: d.BoxTop, Left: d.BoxLeft, Bottom: d.BoxBottom, Right: d.BoxRight}
			if got != want {
				t.Fatalf("detection %s pushed boundingBox %+v but its row holds %+v; the wire and the database disagree about the same object", d.ID, got, want)
			}
			if got.Top >= got.Bottom || got.Left >= got.Right {
				t.Fatalf("detection %s pushed an inverted boundingBox %+v", d.ID, got)
			}
			// F2.2: `value` carries the family, `type` still carries the bare class
			// so every existing Alarm Definition keeps matching.
			if body.Data.Object.Type != d.Class {
				t.Fatalf("detection %s pushed object.type %q; an Alarm Definition matches on the class, so changing it breaks every existing rule", d.ID, body.Data.Object.Type)
			}
			if want := d.Class + " (" + d.Family + ")"; body.Data.Object.Value != want {
				t.Fatalf("detection %s pushed object.value %q, want %q", d.ID, body.Data.Object.Value, want)
			}
			if d.Family != string(classify.FamilyOf(d.Class)) {
				t.Fatalf("detection %s stored family %q for class %q; the taxonomy says %q",
					d.ID, d.Family, d.Class, classify.FamilyOf(d.Class))
			}
			checkedPayloads++
		}
	}
	if checkedPayloads == 0 {
		t.Fatalf("no pushed payload could be compared against a detection row, so B2 is UNPROVEN rather than fixed. A gate that passes vacuously is not a gate")
	}
	t.Logf("pushed payloads compared against their detection rows: %d", checkedPayloads)

	// T6.3: a snapshot exists on disk for every crossing that had a frame, it is
	// a JPEG, and its extension says so.
	snapshots := 0
	for _, d := range recent {
		if d.SnapshotRef == "" {
			continue
		}
		if d.SnapshotKind != store.EvidenceCrossingSnapshot {
			t.Fatalf("detection %s has snapshot_kind %q; a reader who cannot look the kind up does not know what opening the file means", d.ID, d.SnapshotKind)
		}
		if !strings.HasSuffix(d.SnapshotRef, ".jpg") {
			t.Fatalf("detection %s has snapshot_ref %q, which does not end in the extension its kind chose", d.ID, d.SnapshotRef)
		}
		p := filepath.Join(work, "evidence", filepath.FromSlash(d.SnapshotRef))
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("detection %s points at %s and the file is not there: %v", d.ID, p, err)
		}
		if info.Size() == 0 {
			t.Fatalf("the snapshot for detection %s is zero bytes", d.ID)
		}
		head, err := os.ReadFile(p)
		if err != nil || len(head) < 3 || head[0] != 0xFF || head[1] != 0xD8 {
			t.Fatalf("the snapshot for detection %s is not a JPEG; a reader opening it as a picture would get an error", d.ID)
		}
		snapshots++
	}
	if snapshots == 0 {
		t.Fatalf("no crossing snapshot was rendered, so T6.3 is UNPROVEN. The runner was given the same LastJPEG reader `watch` gives it")
	}
	t.Logf("crossing snapshots on disk: %d", snapshots)
	//
	// What this test does NOT prove, said here rather than left to be assumed:
	//
	//   - that a clip was produced for every detection. A clip is refused when
	//     the window falls inside a segment ffmpeg is still writing, and that
	//     refusal is correct behaviour. The clip path's own integrity — the
	//     re-encode, the byte-for-byte read-back, the hash, the dedup, the sweep —
	//     is proven in internal/recorder and internal/evidence, which run against
	//     real ffmpeg.
	//   - that the model sees anything. It is stubbed, and a COCO model does not
	//     see the synthetic material in testdata either.

	// AC4: the push happened and the read-back found it.
	entries, err := st.Stats(testCtx(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if entries.Total == 0 {
		t.Fatalf("no push was attempted: %v", counts)
	}
	if entries.Unattributed != 0 {
		t.Fatalf("%d detection(s) have no outbox row", entries.Unattributed)
	}
	if entries.WithResult != entries.Total {
		t.Fatalf("%d of %d attempts have no recorded result", entries.Total-entries.WithResult, entries.Total)
	}
	held, err := http.Get(simSrv.URL + "/__control/events")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Body.Close()
	body, _ := ioReadAll(held)
	if !strings.Contains(string(body), "aicap-car-") {
		t.Fatalf("the simulator holds no pushed event, so nothing was delivered: %s", body)
	}
	t.Logf("pushed: %s", strings.TrimSpace(string(body)))

	// And the whole run scores, from the store, with the same function the
	// report uses.
	in := soak.Inputs{
		Elapsed: 16 * time.Second, Target: 16 * time.Second, RunEnded: true,
		TotalDetections: total, EvidenceStored: stats.Stored, EvidenceVerified: stats.Verified,
		PushAttempts: entries.Total, PushesWithResult: entries.WithResult,
		HeartbeatCount: 8, ResourceSamples: 8,
		PeakRSSBytes: 100 << 20, DeclaredRSS: 1 << 30,
		PeakDBBytes: 1 << 20, DeclaredDB: 1 << 28,
		PeakDiskBytes: 1 << 20, DeclaredMaxDisk: 1 << 30,
		PeakCPUPercent: 5, DeclaredCPU: 400, CPUMeasured: true,
		AIP99: time.Millisecond, AIP99Samples: 20, DeclaredAIBudget: 4 * time.Second,
		Provenance: store.ProvenanceSynthetic, PushTargetIsSimulator: true,
		SourceKind: "loop", SourceLabel: "recorded loop",
		RehearsedFaults: soak.RehearsalsForReport(),
	}
	criteria := soak.Score(in)
	summary := soak.Overall(in, criteria)
	for _, c := range criteria {
		t.Logf("%s %-34s %s   %s", c.ID, c.Name, c.Verdict, c.Observed)
		if c.Verdict == soak.VerdictFail {
			t.Errorf("%s FAILED: %s", c.ID, c.Observed)
		}
	}
	if summary.Verdict == soak.VerdictPass {
		t.Logf("overall %s (%d pass, %d not run)", summary.Verdict, summary.Pass, summary.NotRun)
	} else {
		t.Logf("overall %s", summary.Verdict)
	}
}

func ioReadAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func testCtx() context.Context { return context.Background() }
