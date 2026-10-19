package emulator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/ortdetect"
)

// Detector measures a frame.
//
// It is an interface and not the concrete ORT detector so that the emulator can
// be exercised without a GPU, a model and a runtime: the shape of the wire is
// the thing worth testing, and a test that needs 2.4 GB of CUDA libraries to
// check a JSON envelope is a test nobody runs.
//
// The in-process ONNX detector satisfies it as it stands.
type Detector interface {
	// Detect measures one frame, or one crop of it.
	Detect(seq uint64, rgb []byte, frameW, frameH int, crop ortdetect.Rect) ortdetect.Answer
	// Identity is what a measurement made through this detector must name.
	Identity() ortdetect.Identity
}

// ClassCounter is a detector that knows how many classes its model declares.
//
// It is separate from [Detector] because it is optional: a model whose output
// does not name its classes — an open-vocabulary export — has no count, and the
// control surface says so rather than reporting zero.
type ClassCounter interface {
	// ClassCount is the count and whether it was declared.
	ClassCount() (int, bool)
}

// Options configures an [Engine].
type Options struct {
	// Listen is host:port for the vendor surface. Port 0 asks the OS for one.
	Listen string
	// Token is the bearer credential every request must carry. Empty means the
	// surface refuses everything, which is the safe default for a thing that
	// listens on a socket.
	Token string
	// Now is the engine's clock.
	Now func() time.Time
	// Sources are the cameras.
	Sources []Source
	// Detector is the model. Nil means the emulator plays video and publishes
	// nothing but the camera inventory, and says so on its own surface: a VMS
	// with no analytics engine configured is a real configuration, and it is
	// not the same thing as a VMS that found nothing.
	Detector Detector
	// Vendor is the provenance string published with every analytics event. It
	// names THIS engine, so the platform's record says "our detector asserted
	// this" rather than borrowing the vendor's authority for a fact the vendor
	// never asserted.
	Vendor string
	// FFmpeg is the ffmpeg binary. Empty takes it from PATH.
	FFmpeg string
	// Width and Height are the detection size. Zero takes the defaults.
	Width  int
	Height int
	// ClipDir is where cut clips and snapshots are written. Empty takes a
	// temporary directory.
	ClipDir string
	// Loop says whether a source that reaches its end starts again.
	Loop bool
	// Logger receives the engine's own lines. Nil takes the default logger.
	Logger *slog.Logger
}

// Engine is a running VMS.
//
// It owns the cameras, the frames, the detector and the vendor's HTTP surface.
type Engine struct {
	opts   Options
	log    *slog.Logger
	clip   string
	server *server

	mu       sync.RWMutex
	cameras  map[string]*camera
	events   []event
	lastID   int
	frameSeq uint64
}

// camera is one virtual camera's live state.
type camera struct {
	source Source
	// width and height are the detection size every frame of this camera is
	// scaled to.
	width  int
	height int

	mu         sync.RWMutex
	detections []Detection
	lastFrame  []byte
	frameAt    time.Time
	frameIndex int
	frameCount int
	measured   int
	latencyMS  float64
	fps        float64
	refusal    string
	paused     bool
	startedAt  time.Time
}

// New builds an engine.
//
// It does not start anything: [Engine.Run] does. Construction validates what
// can be validated without a frame, so a misconfiguration is a refusal at
// startup rather than a camera that plays nothing and says nothing.
func New(opts Options) (*Engine, error) {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Width <= 0 {
		opts.Width = DefaultWidth
	}
	if opts.Height <= 0 {
		opts.Height = DefaultHeight
	}
	if opts.Vendor == "" {
		opts.Vendor = "vision-detector"
	}
	clipDir := opts.ClipDir
	if clipDir == "" {
		dir, err := os.MkdirTemp("", "vms-emulator-clips-")
		if err != nil {
			return nil, fmt.Errorf("emulator: make a clip directory: %w", err)
		}
		clipDir = dir
	}
	if err := os.MkdirAll(clipDir, 0o750); err != nil {
		return nil, fmt.Errorf("emulator: make the clip directory %s: %w", clipDir, err)
	}
	e := &Engine{
		opts:    opts,
		log:     opts.Logger,
		clip:    clipDir,
		cameras: map[string]*camera{},
	}
	for _, src := range opts.Sources {
		if src.ID == "" {
			return nil, fmt.Errorf("emulator: a source with no id cannot be addressed")
		}
		if src.FPS <= 0 {
			src.FPS = DefaultFPS
		}
		if src.Kind == "" {
			src.Kind = KindFile
		}
		if _, exists := e.cameras[src.ID]; exists {
			return nil, fmt.Errorf("emulator: two cameras are called %q, and a camera the console cannot "+
				"tell apart is a camera whose events cannot be attributed", src.ID)
		}
		e.cameras[src.ID] = &camera{
			source:    src,
			width:     opts.Width,
			height:    opts.Height,
			startedAt: opts.Now(),
		}
	}
	// Every camera announces itself, the way a device announces its inventory.
	//
	// It is a real event and not a decoration: the platform learns which cameras
	// exist from the device rather than from a configuration it was told about,
	// and the console's camera list is then a fact about the VMS.
	e.announceCameras()
	return e, nil
}

// announceCameras publishes one camera-online event per camera.
func (e *Engine) announceCameras() {
	now := e.opts.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.cameras))
	for id := range e.cameras {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		e.lastID++
		e.events = append(e.events, event{
			ID:       fmt.Sprintf("emu-camera-online-%s", sanitiseName(id)),
			Type:     "CameraOnline",
			DeviceID: id,
			At:       now,
			Vendor:   e.opts.Vendor,
			RuleName: "device-inventory",
		})
	}
}

// Run starts the vendor surface and plays every camera until the context ends.
//
// It returns when the context is cancelled. The surface stops first, so a
// client that is mid-poll gets a connection error rather than a hang.
func (e *Engine) Run(ctx context.Context) error {
	srv, err := newServer(e)
	if err != nil {
		return err
	}
	e.server = srv
	if err := srv.start(ctx); err != nil {
		return err
	}
	e.log.Info("vms emulator listening", "url", srv.URL(), "cameras", len(e.cameras), "vendor", e.opts.Vendor)

	var wg sync.WaitGroup
	for _, cam := range e.cameras {
		wg.Add(1)
		go func(c *camera) {
			defer wg.Done()
			e.play(ctx, c)
		}(cam)
	}
	<-ctx.Done()
	srv.close()
	wg.Wait()
	return nil
}

// URL is the address of the vendor surface.
func (e *Engine) URL() string {
	if e.server == nil {
		return ""
	}
	return e.server.URL()
}

// play is one camera's loop: read a frame, measure it, publish what was found.
func (e *Engine) play(ctx context.Context, cam *camera) {
	if err := ffmpegAvailable(e.opts.FFmpeg); err != nil {
		e.log.Error("camera cannot play", "camera", cam.source.ID, "error", err.Error())
		e.setRefusal(cam, err.Error())
		return
	}
	r, err := newReader(ctx, e.opts.FFmpeg, cam.source, e.opts.Width, e.opts.Height, e.opts.Now)
	if err != nil {
		e.log.Error("camera cannot play", "camera", cam.source.ID, "error", err.Error())
		e.setRefusal(cam, err.Error())
		return
	}
	defer r.close()

	interval := time.Duration(float64(time.Second) / cam.source.FPS)
	next := time.Now()
	seq := 0
	for {
		if ctx.Err() != nil {
			return
		}
		frame, err := r.next()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if cam.source.Loop || e.opts.Loop {
				if err := r.restart(ctx); err != nil {
					e.log.Error("camera cannot restart", "camera", cam.source.ID, "error", err.Error())
					e.setRefusal(cam, err.Error())
					return
				}
				continue
			}
			e.log.Info("camera reached the end of its source", "camera", cam.source.ID)
			e.setRefusal(cam, "the source reached its end and is not looping")
			return
		}
		seq++
		e.measure(ctx, cam, frame, seq)

		// The pacing is the camera's, not the reader's: ffmpeg is asked for
		// frames as fast as it can produce them and the camera decides when to
		// look at one, so the detection rate is a choice and the latency is a
		// measurement.
		next = next.Add(interval)
		if wait := time.Until(next); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		} else {
			next = time.Now()
		}
	}
}

// measure runs the detector over one frame and publishes what it found.
func (e *Engine) measure(ctx context.Context, cam *camera, frame Frame, seq int) {
	cam.mu.Lock()
	cam.frameCount++
	cam.mu.Unlock()

	if e.opts.Detector == nil {
		e.storeFrame(cam, frame, nil)
		return
	}
	ans := e.opts.Detector.Detect(uint64(seq), frame.Pix, frame.Width, frame.Height, ortdetect.Rect{})
	cam.mu.Lock()
	cam.measured++
	cam.latencyMS = ans.LatencyMS
	if ans.Refusal != nil {
		cam.refusal = ans.Refusal.What
	}
	cam.mu.Unlock()
	if ans.Refusal != nil {
		e.storeFrame(cam, frame, nil)
		return
	}

	dets := make([]Detection, 0, len(ans.Boxes))
	for _, b := range ans.Boxes {
		dets = append(dets, Detection{
			Class:      b.Class,
			Confidence: b.Score,
			X1:         b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2,
		})
	}
	refined := associate(dets)
	e.storeFrame(cam, frame, refined)
	e.publish(cam, refined)
}

// storeFrame keeps the latest frame and its boxes for the live view.
//
// The frame is kept as the emulator's own pixels and the console draws the
// boxes on top of them, which is the only way a box can be checked against the
// picture it claims to describe.
func (e *Engine) storeFrame(cam *camera, frame Frame, dets []Detection) {
	cam.mu.Lock()
	defer cam.mu.Unlock()
	cam.detections = dets
	cam.lastFrame = frame.Pix
	cam.frameAt = frame.At
	cam.frameIndex = frame.Index
}

// setRefusal records why a camera is not producing.
func (e *Engine) setRefusal(cam *camera, why string) {
	cam.mu.Lock()
	defer cam.mu.Unlock()
	cam.refusal = why
}

// publish turns one frame's detections into the vendor's events.
//
// One event per detected object, plus one event per frame carrying the person
// count. The count is a property of the frame, so it is its own event; two
// people in a frame are two object events and a third event that says how many
// people were there.
func (e *Engine) publish(cam *camera, dets []Detection) {
	now := e.opts.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, d := range dets {
		e.lastID++
		e.events = append(e.events, event{
			ID:         fmt.Sprintf("emu-%s-%06d", cam.source.ID, e.lastID),
			Type:       d.Class,
			DeviceID:   cam.source.ID,
			At:         now,
			ClassName:  d.Class,
			ClassID:    classIDFor(d.Class, 0),
			Confidence: d.Confidence,
			Vendor:     e.opts.Vendor,
			RuleName:   "object-detected",
		})
	}
	people := countPeople(dets)
	e.lastID++
	e.events = append(e.events, event{
		ID:        fmt.Sprintf("emu-%s-%06d", cam.source.ID, e.lastID),
		Type:      classPersonCount,
		DeviceID:  cam.source.ID,
		At:        now,
		ClassName: classPersonCount,
		ClassID:   classIDFor(classPersonCount, people),
		Vendor:    e.opts.Vendor,
		RuleName:  "frame-analytics",
	})
	// The feed is a rolling window. A VMS holds a retention period; this one
	// holds a bounded number of events, and the oldest fall off, because an
	// unbounded feed in a long demo is a memory leak with a clock on it.
	const maxEvents = 20000
	if len(e.events) > maxEvents {
		e.events = append([]event(nil), e.events[len(e.events)-maxEvents:]...)
	}
}

// eventsSince returns the events after a moment.
func (e *Engine) eventsSince(since time.Time) []event {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]event, 0, len(e.events))
	for _, ev := range e.events {
		if ev.At.After(since) {
			out = append(out, ev)
		}
	}
	return out
}

// camera reads one camera's configuration.
//
// It returns the camera itself rather than a copy, because the media surface
// needs the source's own path and the camera's own clock, and a copy of those
// would be a copy that could drift.
func (e *Engine) camera(id string) (*camera, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cam, ok := e.cameras[id]
	return cam, ok
}

// cameraState is the control surface's view of one camera.
type cameraState struct {
	ID          string
	Name        string
	Kind        Kind
	Location    string
	FPS         float64
	FrameIndex  int
	FrameCount  int
	Measured    int
	LatencyMS   float64
	Refusal     string
	Paused      bool
	Detections  []Detection
	LastFrameAt time.Time
}

// state reads one camera's live state.
func (e *Engine) state(id string) (cameraState, bool) {
	e.mu.RLock()
	cam, ok := e.cameras[id]
	e.mu.RUnlock()
	if !ok {
		return cameraState{}, false
	}
	cam.mu.RLock()
	defer cam.mu.RUnlock()
	return cameraState{
		ID:          cam.source.ID,
		Name:        cam.source.Name,
		Kind:        cam.source.Kind,
		Location:    cam.source.Location,
		FPS:         cam.source.FPS,
		FrameIndex:  cam.frameIndex,
		FrameCount:  cam.frameCount,
		Measured:    cam.measured,
		LatencyMS:   cam.latencyMS,
		Refusal:     cam.refusal,
		Paused:      cam.paused,
		Detections:  cam.detections,
		LastFrameAt: cam.frameAt,
	}, true
}

// frame reads one camera's latest pixels.
func (e *Engine) frame(id string) ([]byte, int, int, bool) {
	e.mu.RLock()
	cam, ok := e.cameras[id]
	e.mu.RUnlock()
	if !ok {
		return nil, 0, 0, false
	}
	cam.mu.RLock()
	defer cam.mu.RUnlock()
	return cam.lastFrame, cam.width, cam.height, true
}

// clipPath is where a clip for a camera and a moment is written.
//
// The name carries the camera and the nanosecond of the moment, so two clips
// of the same camera at the same moment are the same file and two clips at
// different moments never collide.
func (e *Engine) clipPath(cameraID string, at time.Time) string {
	name := fmt.Sprintf("%s-%d.mp4", sanitiseName(cameraID), at.UTC().UnixNano())
	return filepath.Join(e.clip, name)
}

// sanitiseName makes a camera id safe to put in a file name.
//
// A camera id comes from configuration, and configuration is not trusted
// input in the sense that matters here: it ends up in a path.
func sanitiseName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
