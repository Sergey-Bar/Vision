package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/camerahealth"
	"github.com/Sergey-Bar/ai-captain-poc/detectclient"
)

// The camera fleet.
//
// One resolved stream, one ffmpeg pipe and one detection loop per camera,
// over ONE shared sidecar client. Each camera degrades by name: a camera
// whose URL will not resolve, whose capture dies or whose sidecar call fails
// is marked with the reason and the rest of the fleet keeps running — the
// source_reason pattern the single-stream path always had, now per camera
// instead of per process.
//
// What is deliberately NOT shared: the per-camera identity tracker. Two
// cameras' frames arrive in different pixel spaces, so their boxes are not
// comparable and a shared tracker would fuse two strangers standing in
// different car parks. What IS shared is the correlation tracker the
// autonomy pass feeds: it is the one place the fleet's cameras meet, and it
// is serialized by the autonomy pass's own lock.

// Camera kinds. A live camera has a stream URL; the sim pseudo-camera is the
// demo source wearing a camera's clothes so the wall has one tile per
// source rather than a special case in the dashboard.
const (
	camKindLive = "live"
	camKindSim  = "sim"

	// simCameraID is the pseudo-camera the demo simulator publishes as.
	simCameraID = "sim"
)

// cameraRuntime is one camera in the fleet. Everything the status snapshot
// reads is atomic, so a snapshot never locks a running capture loop.
type cameraRuntime struct {
	id     string
	name   string
	kind   string
	url    string
	zoneID []string

	videoID string // YouTube id, for the player tile

	cancel context.CancelFunc
	done   chan struct{}

	// trk gives this camera's detections a stable identity across frames.
	// The sidecar answers per frame with no memory; without this, every
	// frame would read as a fresh entry and the alert feed would flood.
	trk *tracker

	// occupancy is this camera's grid-occupancy cube: how many frames each
	// cell held a detection in. It is the configurator's learned-zone input
	// and costs one map increment per occupied cell per frame.
	occupancy *occupancyCube

	frames     atomic.Int64
	lastFrame  atomic.Int64 // unix nanos of the last frame that arrived
	firstFrame atomic.Int64 // unix nanos of the first frame this loop produced
	lastBlind  atomic.Bool
	stopped    atomic.Bool // the capture loop has exited; no more frames are coming
	started    atomic.Bool // a capture loop actually began for this camera

	reason atomic.Value // string: the named degradation reason, if any
	health atomic.Value // string: last observed health state

	// sim reports whether this is the demo pseudo-camera, which is started
	// and stopped by the demo-source path rather than by a capture loop.
	sim bool
}

func newCameraRuntime(id, name, kind, url string, zoneIDs []string) *cameraRuntime {
	c := &cameraRuntime{
		id:        id,
		name:      name,
		kind:      kind,
		url:       url,
		trk:       &tracker{},
		sim:       kind == camKindSim,
		occupancy: &occupancyCube{cells: map[cellKey]int{}},
	}
	if len(zoneIDs) > 0 {
		c.zoneID = append([]string(nil), zoneIDs...)
	}
	c.videoID = extractVideoID(url)
	return c
}

// fail records the named reason this camera is not producing frames, and
// marks its loop stopped so the health read is silent immediately rather
// than after the silent window notices on its own — a camera whose capture
// died one second ago is not "fresh" just because its last frame is recent.
// It is the whole degradation contract: the reason is what the dashboard
// shows, and nothing about the rest of the fleet changes.
func (c *cameraRuntime) fail(reason string) {
	c.reason.Store(reason)
	c.stopped.Store(true)
}

func (c *cameraRuntime) failReason() string {
	if v := c.reason.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (c *cameraRuntime) lastFrameTime() time.Time {
	if ns := c.lastFrame.Load(); ns > 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// healthFor computes this camera's health state at now from its counters and
// its last frame's blind verdict. Computing it on read rather than on a
// ticker is what makes a dead camera go silent on its own: there is no
// goroutine that has to notice. A camera whose loop has exited is silent at
// once, whatever its last frame's age says.
func (c *cameraRuntime) healthFor(now time.Time, w healthWindows) string {
	if c.stopped.Load() {
		return camSilent
	}
	return cameraHealth(c.lastFrameTime(), c.lastBlind.Load(), now, w)
}

// isLive reports whether this camera is producing usable frames right now.
// The predicate is camerahealth's — there is one definition of "watching" and
// this is a reader of it, not a second spelling.
func (c *cameraRuntime) isLive(now time.Time, w healthWindows) bool {
	return camerahealth.Live(c.healthFor(now, w))
}

// cameraFleet is the POC's camera registry: one runtime per camera, keyed by
// camera id. The site model declares which cameras exist; the config says
// which of them have a stream attached.
type cameraFleet struct {
	mu   sync.Mutex
	cams map[string]*cameraRuntime
}

func newCameraFleet() *cameraFleet {
	return &cameraFleet{cams: make(map[string]*cameraRuntime)}
}

// upsert registers a camera or updates an existing one in place. An update
// replaces the stream and the name but never the zone declaration with an
// empty one: the declaration comes from the site model, and erasing it with
// the config's silence would turn a declared camera into an undeclared one
// without anybody saying so.
func (f *cameraFleet) upsert(id, name, kind, url string, zoneIDs []string) *cameraRuntime {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cams[id]
	if !ok {
		c = newCameraRuntime(id, name, kind, url, zoneIDs)
		f.cams[id] = c
		return c
	}
	c.name = name
	c.kind = kind
	c.url = url
	if len(zoneIDs) > 0 {
		c.zoneID = append([]string(nil), zoneIDs...)
	}
	c.videoID = extractVideoID(url)
	return c
}

func (f *cameraFleet) get(id string) *cameraRuntime {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cams[id]
}

// all returns every camera in a stable order, so the dashboard and the JSON
// API cannot disagree about which tile is which between two snapshots. A nil
// fleet has no cameras, which is what an ingest built without one reports.
func (f *cameraFleet) all() []*cameraRuntime {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*cameraRuntime, 0, len(f.cams))
	for _, c := range f.cams {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// liveCameras returns the fleet's real (non-sim) cameras that have a stream
// URL, in a stable order.
func (f *cameraFleet) liveCameras() []*cameraRuntime {
	var out []*cameraRuntime
	for _, c := range f.all() {
		if !c.sim && c.url != "" {
			out = append(out, c)
		}
	}
	return out
}

// anyLive reports whether any real camera is producing usable frames.
func (f *cameraFleet) anyLive(now time.Time, w healthWindows) bool {
	for _, c := range f.all() {
		if c.sim {
			continue
		}
		if c.isLive(now, w) {
			return true
		}
	}
	return false
}

// cameraDegraded reports whether a camera is stale, silent or blind — the
// three states that mean "not watching". A camera the fleet does not know is
// degraded too: a camera declared in the site model that has no runtime is
// not watching either, and the coverage maths must fail in the direction
// that says so rather than the direction that reads as all-clear.
//
// The set is camerahealth's, not a second opinion: the brief, the coverage
// rule, telemetry and this function all answer the same question the same way,
// so a shift cannot render "every camera reporting" beside "under-observed".
func (s *StreamIngest) cameraDegraded(cameraID string) bool {
	c := s.fleet.get(cameraID)
	if c == nil {
		return true
	}
	return camerahealth.Degraded(c.healthFor(time.Now(), healthWindowsFor(s.config.Live.CaptureFPS)))
}

// buildFleet registers the fleet the POC runs with: every camera the site
// model declares, the ones the config attaches a stream to, and the sim
// pseudo-camera. A site-model camera the config says nothing about never
// starts and says so by name ("no stream configured") — the coverage panel
// reports it as uncovered rather than the fleet inventing a URL for it.
func (s *StreamIngest) buildFleet() {
	s.fleet = newCameraFleet()
	s.fleet.upsert(simCameraID, "Demo simulator", camKindSim, "", nil)

	// The camera->zone declaration map is built here, while nothing else is
	// reading it, because the autonomy pass consults it once per incident per
	// frame and a lazy init inside that path would need the very lock the pass
	// already holds.
	s.zoneCameras = map[string][]string{}
	if s.siteModel != nil {
		for _, cam := range s.siteModel.Cameras {
			c := s.fleet.upsert(cam.ID, cam.ID, camKindLive, "", cam.ZoneIDs)
			c.fail("not started: no stream configured")
		}
		for _, z := range s.siteModel.Zones {
			s.zoneCameras[z.ID] = s.siteModel.CamerasForZone(z.ID)
		}
	}
	for _, cc := range s.config.Cameras {
		if cc.ID == "" {
			log.Printf("camera config entry without an id is ignored")
			continue
		}
		name := cc.Name
		if name == "" {
			name = cc.ID
		}
		// The camera→zone declaration comes from the site model only; the
		// config's job here is the stream and its name.
		c := s.fleet.upsert(cc.ID, name, camKindLive, cc.YouTubeURL, nil)
		// A configured camera starts unless its URL is empty, in which case
		// it is the site model's camera the config declined to attach.
		if cc.YouTubeURL == "" {
			c.fail("not started: no stream configured")
		} else {
			c.reason.Store("")
			c.stopped.Store(false)
		}
	}
}

// startCameras starts one ingest loop per configured camera against a single
// shared sidecar client. The sidecar is one process serving N loops, so the
// Python side's concurrency is an unverified assumption — until it is
// measured, the inference call itself is serialized in the POC (see
// detectMu), which costs nothing at capture_fps 1 and removes the question.
func (s *StreamIngest) startCameras(ctx context.Context) error {
	cams := s.fleet.liveCameras()
	if len(cams) == 0 {
		return fmt.Errorf("no cameras are configured (add a cameras: list to the config)")
	}
	client, err := s.ensureSidecar(ctx)
	if err != nil {
		// The sidecar is shared, so this failure is shared too — but it is
		// recorded per camera, so every tile names the reason instead of
		// the dashboard going quiet with one global error.
		for _, c := range cams {
			c.fail(fmt.Sprintf("sidecar: %v", err))
		}
		s.StartDemoSource(ctx)
		return nil
	}
	for _, c := range cams {
		s.startCamera(ctx, c, client)
	}
	return nil
}

// startCamera runs one camera's ingest under its own cancellable context.
func (s *StreamIngest) startCamera(ctx context.Context, c *cameraRuntime, client *detectclient.Client) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	c.started.Store(true)
	go func() {
		defer close(c.done)
		s.cameraLoop(ctx, c, client)
		s.cameraStopped(c)
	}()
}

// stopCamera cancels one camera's loop and waits for it to return.
func (s *StreamIngest) stopCamera(c *cameraRuntime) {
	if c == nil {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
		c.done = nil
		c.cancel = nil
	}
}

// cameraLoop resolves and captures one camera's stream, then runs the
// shared frame loop. Every step is allowed to fail by name.
func (s *StreamIngest) cameraLoop(ctx context.Context, c *cameraRuntime, client *detectclient.Client) {
	hlsURL, err := s.resolveHLSURL(ctx, c.url)
	if err != nil {
		c.fail(fmt.Sprintf("stream resolve: %v", err))
		return
	}
	capture, err := startFrameCapture(ctx, hlsURL, s.config.Live.CaptureFPS)
	if err != nil {
		c.fail(fmt.Sprintf("frame capture: %v", err))
		return
	}
	s.cameraFrameLoop(ctx, c, capture, client)
}

// cameraFrameLoop is the per-frame body every camera shares: capture, sidecar
// round trip, local identity, health digest, publish. The autonomy pass runs
// inside publishFrame, so every camera's frames take the same path through
// the shared correlation tracker.
func (s *StreamIngest) cameraFrameLoop(ctx context.Context, c *cameraRuntime, capture *frameCapture, client *detectclient.Client) {
	minScore := s.config.Live.MinScore
	// The index interval is a positive count or the default. It is
	// converted once, here, where the guard is — rather than inside the
	// loop, where an int to uint64 conversion deserves that comment.
	indexEvery := uint64(5)
	if n := s.config.Live.IndexEveryFrames; n > 0 {
		indexEvery = uint64(n)
	}

	var seq uint64
	var prev frameDigest
	hasPrev := false
	firstFrame := true

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-capture.frames:
			if !ok {
				c.fail(captureEndedReason(capture))
				return
			}
			seq++
			now := time.Now().UTC()

			// One sidecar, N cameras: the Go client is safe to call
			// concurrently (immutable config plus an *http.Client) but the
			// Python server's concurrency has never been measured, so the
			// round trip is serialized here. At capture_fps 1 this costs
			// nothing; the day it is measured, this lock is the line that
			// comes out.
			s.detectMu.Lock()
			res := client.Detect(ctx, seq, frame, frameW, frameH)
			s.detectMu.Unlock()
			s.inferLat.Add(res.Latency)
			s.recordAIState(res)

			var dets []frameObject
			if !detectclient.AbsenceOf(res.State) {
				dets = s.mapDetections(res, frame, minScore)
			}
			dets = c.trk.update(dets, now)

			// Health from the frame bytes this loop already holds: a
			// per-frame digest is nearly free and says whether the camera
			// can see at all.
			cur := digestFrame(frame, frameW, frameH)
			blind, why := blindReason(prev, cur, hasPrev)
			prev, hasPrev = cur, true
			if blind {
				log.Printf("camera %s: %s", c.id, why)
			}

			c.frames.Store(int64(seq))
			if c.firstFrame.Load() == 0 {
				c.firstFrame.Store(now.UnixNano())
			}
			c.lastFrame.Store(now.UnixNano())
			c.lastBlind.Store(blind)
			c.stopped.Store(false)
			c.reason.Store("")
			s.captureCount.Store(capture.read.Load())

			// The occupancy cube is the configurator's learned-zone input,
			// accumulated from the boxes this loop already has.
			if !c.sim && len(dets) > 0 {
				c.noteOccupancy(occupancyCellsFor(dets, learnConfig()))
			}

			if firstFrame {
				firstFrame = false
				s.onCameraFirstFrame(c)
			}
			s.publishFrame(c.id, "onnx", now, int64(seq), dets, seq%indexEvery == 0 && len(dets) > 0)
		}
	}
}

// onCameraFirstFrame applies the courtesy in the other direction: a real
// camera producing pixels takes the dashboard back from the demo source,
// exactly as the single-stream path always did.
func (s *StreamIngest) onCameraFirstFrame(c *cameraRuntime) {
	if !s.demoRunning.Load() {
		return
	}
	log.Printf("camera %s is producing frames; the demo source stands down", c.id)
	s.stopDemoSource()
}

// cameraStopped applies the fleet-level courtesy: when the last live camera
// dies, the demo source takes the dashboard back rather than leaving it
// empty. One dead camera among several changes nothing.
//
// It is also where a stopped camera's zones are recorded as going dark: the
// coverage line in the duty log is what makes "nobody was watching zone X" a
// hand-over fact rather than an inference from an empty alert feed.
func (s *StreamIngest) cameraStopped(c *cameraRuntime) {
	if s.demoRunning.Load() || s.fleet.anyLive(time.Now(), healthWindowsFor(s.config.Live.CaptureFPS)) {
		return
	}
	reason := c.failReason()
	if reason == "" {
		reason = "stopped"
	}
	log.Printf("camera %s stopped (%s); no camera is producing frames — the demo source takes over", c.id, reason)
	s.StartDemoSource(s.rootCtx)
}

// captureEndedReason names why a capture ended, or says only that it did.
func captureEndedReason(capture *frameCapture) string {
	if v := capture.err.Load(); v != nil {
		return "capture ended: " + v.(string)
	}
	return "capture ended"
}

// startManualCamera attaches a manually loaded stream as its own camera. It
// is how the dashboard's "Load Stream" row works in a fleet: the stream
// becomes a tile, not a takeover of the whole process. Resolution and
// capture failures are reported to the caller AND recorded on the camera, so
// the tile names its own reason instead of the dashboard going quiet.
func (s *StreamIngest) startManualCamera(parent context.Context, watchURL, name string) error {
	const manualID = "manual"
	s.stopCamera(s.fleet.get(manualID))
	s.stopDemoSource()

	c := s.fleet.upsert(manualID, name, camKindLive, watchURL, nil)
	c.reason.Store("")
	c.stopped.Store(false)
	c.lastBlind.Store(false)
	c.lastFrame.Store(0)
	c.frames.Store(0)

	client, err := s.ensureSidecar(parent)
	if err != nil {
		c.fail(fmt.Sprintf("sidecar: %v", err))
		return err
	}
	hlsURL, err := s.resolveHLSURL(parent, watchURL)
	if err != nil {
		c.fail(fmt.Sprintf("stream resolve: %v", err))
		return err
	}
	capture, err := startFrameCapture(parent, hlsURL, s.config.Live.CaptureFPS)
	if err != nil {
		c.fail(fmt.Sprintf("frame capture: %v", err))
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.done = make(chan struct{})
	c.started.Store(true)
	go func() {
		defer close(c.done)
		s.cameraFrameLoop(ctx, c, capture, client)
		s.cameraStopped(c)
	}()
	return nil
}

// cameraStatus is one camera's row in the status snapshot: what the fleet
// knows, named, with no guess about anything the bytes do not say.
type cameraStatus struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	VideoID string   `json:"video_id,omitempty"`
	URL     string   `json:"url,omitempty"`
	ZoneIDs []string `json:"zone_ids,omitempty"`
	State   string   `json:"state"`
	Frames  int64    `json:"frames"`
	// FpsAvg is the rate this camera ACTUALLY achieved, frames since its first
	// one. It is the observable for the shared sidecar's serialized round
	// trip: the plan's capture ceiling is one detection per sidecar latency,
	// and until that is measured the achieved rate is the only honest way to
	// see whether a camera's configured fps is what the fleet is delivering.
	FpsAvg    float64 `json:"fps_avg,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	LastFrame string  `json:"last_frame,omitempty"`
	AgeSec    float64 `json:"age_seconds,omitempty"`
}

// fleetStatus is every camera's current row, in the fleet's stable order.
func (s *StreamIngest) fleetStatus(now time.Time) []cameraStatus {
	w := healthWindowsFor(s.config.Live.CaptureFPS)
	out := make([]cameraStatus, 0, 4)
	for _, c := range s.fleet.all() {
		st := cameraStatus{
			ID:      c.id,
			Name:    c.name,
			Kind:    c.kind,
			VideoID: c.videoID,
			URL:     c.url,
			ZoneIDs: c.zoneID,
			State:   c.healthFor(now, w),
			Frames:  c.frames.Load(),
			Reason:  c.failReason(),
		}
		if first := c.firstFrame.Load(); first > 0 && c.frames.Load() > 1 {
			elapsed := now.Sub(time.Unix(0, first)).Seconds()
			if elapsed > 0 {
				st.FpsAvg = float64(c.frames.Load()) / elapsed
			}
		}
		if !c.lastFrameTime().IsZero() {
			st.LastFrame = c.lastFrameTime().UTC().Format(time.RFC3339)
			st.AgeSec = now.Sub(c.lastFrameTime()).Seconds()
		}
		out = append(out, st)
	}
	return out
}

// takeCameraHealthSamples records degraded-state transitions into telemetry.
// It runs on the metrics loop's clock, so a camera that goes silent is
// counted once when it crosses, not once per frame.
//
// Two cameras are excluded. The sim pseudo-camera, because its health is the
// demo source's and it correctly goes quiet the moment a real camera takes
// the dashboard over — counting that as a blind camera would be counting the
// system working. And a camera that was never started, because a camera that
// was never supposed to produce frames has not "gone" silent; it is
// unconfigured, which the coverage panel reports as a gap rather than as a
// failure.
func (s *StreamIngest) takeCameraHealthSamples(now time.Time) {
	if s.telemetry == nil || s.fleet == nil {
		return
	}
	w := healthWindowsFor(s.config.Live.CaptureFPS)
	for _, c := range s.fleet.all() {
		// A camera that never started has not "gone" degraded; it was never
		// running, which the coverage panel reports as a gap rather than this
		// counter counting as a failure.
		if c.sim || !c.started.Load() {
			continue
		}
		state := c.healthFor(now, w)
		prev := ""
		if v := c.health.Load(); v != nil {
			prev = v.(string)
		}
		if state != prev {
			c.health.Store(state)
			// The degraded set is camerahealth's, not a second opinion spelled
			// here: a stale camera is degraded for the brief, for coverage and
			// for the capture plan, so it is degraded for the counter too.
			if camerahealth.Degraded(state) {
				s.telemetry.RecordCameraDegraded(state)
			}
		}
	}
}
