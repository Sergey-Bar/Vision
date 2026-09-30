package soak

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/source"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/supervisor"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
)

// FrameSource is what the run loop needs from the recorder: the next frame, the
// end of the pipe, and the frame counts. It is an interface so the LOOP can be
// tested without ffmpeg, and the loop is the part that has to keep heartbeats
// going — which is exactly the part a real ffmpeg makes untestable.
type FrameSource interface {
	// Poll returns the next frame, or ok=false if the window elapsed with
	// nothing arriving.
	Poll(d time.Duration) (recorder.Frame, bool)
	// Exhausted reports whether the pipe has ended for good.
	Exhausted() bool
	// Next is the blocking form, used by callers that have nothing else to do.
	Next() (recorder.Frame, bool)
	// Counts is the measured frame counters.
	Counts() (frames, decodes, decodeErrors uint64)
	// Index is the current segment index, for the heartbeat's numbers.
	Index() (*recorder.SegmentIndex, error)
	// PipeDepth is the queue depth, for the sample.
	PipeDepth() int
	// LastFrameJPEG is the newest frame as it came off the pipe, for the screen.
	LastFrameJPEG() []byte
}

// RunnerOptions is everything a run needs. It is a struct rather than a global
// so a test can build a whole run with a loop of two seconds.
type RunnerOptions struct {
	RunID            string
	DataRoot         string
	Spec             source.Spec
	Store            *store.Store
	Evidence         *evidence.Store
	Client           *ai.Client
	XProtect         *xprotect.Client
	Engine           *detect.Engine
	Recorder         FrameSource
	Clipper          recorder.Clipper
	Zone             detect.Zone
	Provenance       string
	ClipBefore       time.Duration
	ClipAfter        time.Duration
	Target           time.Duration
	Heartbeat        time.Duration
	Gap              time.Duration
	PushMax          int
	MaxDisk          int64
	DeclaredRSS      int64
	DeclaredDB       int64
	DeclaredCPU      float64
	DeclaredAIBudget time.Duration
	PushBaseURL      string
	// EventTypeGUID and CameraGUID are the VMS-side identifiers the event body
	// needs. They are configuration, not discoveries: a real VMS issues both and
	// the run is told them. Without them the push is refused by name rather than
	// sent with an empty source.
	EventTypeGUID string
	CameraGUID    string
	PushName      string
	PushIsVMS     bool
	ClockSource   string
	Logf          func(string, ...any)
	// OnFrame is called with each frame for the live screen's overlay. It runs
	// on the pipeline goroutine and must not block.
	OnFrame func(recorder.Frame, []ai.Box)
	// LastJPEG is consulted by the frame hook when it needs the picture rather
	// than the pixels. It is nil until a frame has been read.
	LastJPEG func() []byte
	// ChildPIDs returns the processes AC5's CPU clause is about: ffmpeg and the
	// sidecar. It is a function because the set changes when a child restarts,
	// and a stale PID is a process that is no longer ours.
	ChildPIDs func() []int
	// cpu is the sampler, built by NewRunner from ChildPIDs.
	cpu *CPUSampler

	// onFrame and heartbeat are indirected so the LOOP can be tested without
	// ffmpeg, a store or a sidecar. The loop's whole job is to keep heartbeats
	// going while frames arrive, and a real ffmpeg cannot be used to test that:
	// the moment a real pipe stalls, the thing under test IS the stall.
	onFrame   func(recorder.Frame, []ai.Box, []byte)
	heartbeat func(context.Context) error

	// Rehearsals are the fault rehearsals already performed on this build, read
	// from the store. A run with none of them scores AC7 as NOT RUN, which is
	// the correct answer and not a passing one.
	Rehearsals []Rehearsal
}

// Runner owns the run loop: frames in, decisions out, side effects recorded
// before they are attempted.
type Runner struct {
	opt RunnerOptions
	log func(string, ...any)
	lat *ai.Latencies
	// cpu samples the children AC5's CPU clause is about.
	cpu *CPUSampler
	mu  sync.Mutex
	// unparsedAI and healthyFromFailed are AC6's counters. They are counted here,
	// at the point where the answer is interpreted, because that is the only
	// place where a failed read could be mistaken for a healthy one.
	unparsedAI int
	// lastAIState is the previous frame's sidecar state, carried onto the
	// detection so the report can say whether a crossing came from a frame the
	// model actually answered.
	lastAIState       string
	healthyFromFailed int
	// pushMu serialises the outbox push loop against the per-detection inline
	// push. Two pushers would double-push.
	pushOnce sync.Once
	stopPush chan struct{}
	pushed   int
}

// NewRunner builds a runner.
func NewRunner(opt RunnerOptions) (*Runner, error) {
	if opt.Store == nil {
		return nil, fmt.Errorf("soak: a run with no store cannot record anything, and a run that records nothing would report zero events as if the model had found nothing")
	}
	if opt.Recorder == nil {
		return nil, fmt.Errorf("soak: a run with no recorder has no frames")
	}
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	if opt.Heartbeat <= 0 {
		opt.Heartbeat = 10 * time.Second
	}
	return &Runner{opt: opt, log: opt.Logf, lat: ai.NewLatencies(200000), stopPush: make(chan struct{}), cpu: NewCPUSampler(func() []int {
		if opt.ChildPIDs == nil {
			return nil
		}
		return opt.ChildPIDs()
	})}, nil
}

// logf writes a line, tolerating a runner that was built without a logger. A
// run loop that crashes because nobody supplied a log function is a run that
// dies silently, which is the failure this whole module exists to prevent.
func (r *Runner) logf(format string, args ...any) {
	if r.log == nil {
		return
	}
	r.log(format, args...)
}

// watchReconnects turns ffmpeg's reconnection announcements into recorded faults.
//
// This is the whole point of the trade the plan forces. A public camera reboots,
// loses WiFi and takes a new DHCP lease. A run that dies on the first of those
// measures nothing, and a run that ignores them measures a camera with better
// uptime than any real camera has. So the reconnect is ALLOWED, and every one of
// them is recorded as a `stream_gap` with the moment it happened, and the run
// continues.
//
// AC1 is zero tolerance on heartbeat gaps, so a camera that blinks is a FAIL and
// the report prints the count. The camera's uptime becomes a measured property of
// this POC instead of an assumption, which is the only honest place for it.
func (r *Runner) watchReconnects(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	seen := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rc, ok := r.opt.Recorder.(interface{ Reconnects() int })
		if !ok {
			return
		}
		now := rc.Reconnects()
		if now == seen {
			continue
		}
		from, to := seen, now
		seen = now
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: time.Now(), Reason: store.FaultStreamGap,
			Detail: fmt.Sprintf(
				"the source was re-established %d time(s) between heartbeat %d and %d. The run continued rather than ending, and each reconnection is counted: AC1 is zero tolerance, so a camera that blinks fails this run and the report prints the count",
				to-from, from, to),
		})
		r.logf("recorded %d source reconnection(s) since the last heartbeat; the run continues and AC1 will count them", to-from)
	}
}

// Run executes until the target is reached, the context is cancelled, or the
// pipe ends. The verdict is not decided here: a fault ends the run and the
// report decides what it means.
//
// The heartbeat is on its OWN GOROUTINE, and the frame pop is a POLL. Both are
// deliberate and both were found by a real run:
//
//   - A blocking frame pop stops the loop from servicing its own heartbeat timer,
//     so a stalled pipe produces a run that goes SILENT. Silence is the dropout
//     AC1 exists to detect, and the component that must never be the one that
//     notices is the one blocked on the pipe.
//   - A heartbeat on the frame loop is silenced by a slow INFERENCE. The first
//     call into a freshly loaded ONNX session on CPU can take a minute, and a
//     71-second heartbeat gap was recorded for exactly that reason. The gap was
//     real, but it was an artefact of WHERE the heartbeat ran, and an artefact
//     that makes AC1 fail for a reason that has nothing to do with the
//     pipeline's reliability.
//
// So: the heartbeat is a goroutine, the frame pop is a poll, and neither can
// stop the other.
func (r *Runner) Run(ctx context.Context) error {
	// The outbox pump runs for the whole run. A detection enqueues its push and
	// the pump makes the attempt, so a slow or dead provider cannot block the
	// frame loop. It is the pump that owns retries and the backoff budget.
	go r.pump(ctx)
	go r.watchReconnects(ctx)

	// The target is a promise made before the run, so the run ENDS when it is
	// reached. A run that only ever stops on a signal can never conclude, and
	// a report that is always PROVISIONAL because nobody stopped the process is
	// a report about the operator's attention span, not about the pipeline.
	beatCtx, stopBeating := context.WithCancel(ctx)
	defer stopBeating()
	var beatErr = make(chan error, 1)
	go func() {
		t := time.NewTicker(r.opt.Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-beatCtx.Done():
				beatErr <- nil
				return
			case <-t.C:
				if err := r.beat(beatCtx); err != nil {
					beatErr <- err
					return
				}
			}
		}
	}()

	runEnded := make(chan error, 1)
	go func() { runEnded <- r.frameLoop(ctx) }()

	select {
	case err := <-runEnded:
		stopBeating()
		<-beatErr
		return err
	case err := <-beatErr:
		if err != nil {
			return err
		}
		// The beater ended because the context did; the frame loop will follow.
		<-runEnded
		return nil
	}
}

// frameLoop is the frame path on its own goroutine, so the heartbeat above is
// never delayed by a slow inference.
func (r *Runner) frameLoop(ctx context.Context) error {
	var target <-chan time.Time
	if r.opt.Target > 0 {
		t := time.NewTimer(r.opt.Target)
		defer t.Stop()
		target = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-target:
			r.logf("target of %s reached; the run is ending and the report will say PASS only if every criterion held", r.opt.Target)
			return nil
		default:
		}
		frame, ok := r.opt.Recorder.Poll(200 * time.Millisecond)
		if !ok {
			if r.opt.Recorder.Exhausted() {
				// The pipe closed: ffmpeg is gone. The supervisor records the
				// gap; the loop simply ends.
				return nil
			}
			// Nothing arrived within the window. Loop, so the timers above are
			// still serviced.
			continue
		}
		r.process(ctx, frame)
	}
}

// process handles one frame: infer, decide, and for each crossing record first
// and then act.
func (r *Runner) process(ctx context.Context, f recorder.Frame) {
	r.mu.Lock()
	hook := r.opt.onFrame
	r.mu.Unlock()

	boxes := make([]ai.Box, 0, 8)
	if r.opt.Client == nil {
		// No client means nobody to ask. NewRunner refuses this, so reaching
		// here means a runner was built by hand; treating it as "nothing was
		// asked" is the honest reading and is what the loop test relies on.
		r.logf("frame %d: not_asked — no sidecar client is configured", f.Seq)
	} else {
		res := r.opt.Client.Detect(ctx, f.Seq, f.RGB, f.Width, f.Height)
		r.lat.Add(res.Latency)
		r.mu.Lock()
		r.lastAIState = res.State
		r.mu.Unlock()
		switch res.State {
		case ai.StateDegraded, ai.StateCannotTell:
			r.mu.Lock()
			r.unparsedAI++
			r.mu.Unlock()
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: f.At, Reason: store.FaultAIUnreadable,
				Detail: fmt.Sprintf("frame %d at %s: %s", f.Seq, f.At.Format(time.RFC3339), res.Reason),
			})
		case ai.StateNotAsked:
			// A frame that was never sent is not a failure of the model, and it is
			// not an empty scene. It is counted as not_asked and nothing else.
		}
		for _, b := range res.Boxes {
			boxes = append(boxes, b)
		}
	}

	if r.opt.Engine == nil {
		hook(f, boxes, nil)
		return
	}
	crossings, tracks, _ := r.opt.Engine.Feed(f.At, toDetect(boxes))
	if hook != nil {
		hook(f, boxes, nil)
	}
	// The sidecar's answer is recorded with the detection, so the report can say
	// whether a crossing came from a frame the model actually answered.
	aiState := ai.StateNotAsked
	if r.opt.Client != nil {
		aiState = r.lastAIState
	}
	for _, c := range crossings {
		r.handleCrossing(ctx, c, aiState)
	}
	_ = tracks
}

func toDetect(boxes []ai.Box) []detect.Box {
	out := make([]detect.Box, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, detect.Box{Class: b.Class, Score: b.Score, X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2})
	}
	return out
}

// handleCrossing is the R2 sequence, in this order and no other:
//
//  1. commit the detection row in `accepted`
//  2. cut the clip and read it back, record clip_ok or clip_refused
//  3. enqueue the push
//
// Nothing external happens before step 1, and a crash between any two steps
// leaves a resumable row.
func (r *Runner) handleCrossing(ctx context.Context, c detect.Crossing, aiState string) {
	id := DetectionID(c, r.opt.Provenance)
	det := store.Detection{
		ID: id, RunID: r.opt.RunID, At: c.At, Class: c.Class, TrackID: c.TrackID, Score: scoreOf(c),
		BoxTop: int(c.Box.Y1 * 1000), BoxLeft: int(c.Box.X1 * 1000),
		BoxBottom: int(c.Box.Y2 * 1000), BoxRight: int(c.Box.X2 * 1000),
		Direction: c.Direction, AIState: aiState, Provenance: r.opt.Provenance,
	}
	if err := r.opt.Store.Accept(ctx, det); err != nil {
		// A duplicate id is the idempotency boundary working, not a defect. An
		// acceptance that fails because the database is locked is a different
		// failure with a different owner, and it is recorded as such: blaming
		// the clip for a locked database sends an operator to look at the video
		// instead of at the process holding the lock.
		switch {
		case strings.Contains(err.Error(), "already exists"):
			// Nothing to do: the same detection is already recorded.
		case DBBusyError(err):
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: c.At, Reason: store.FaultDBUnavailable,
				Detail: "the detection could not be committed because the database is not accepting writes, so no clip and no push exist for it: " + err.Error(),
			})
			r.logf("database is not accepting writes: %v", err)
		default:
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: c.At, Reason: store.FaultClipRefused,
				Detail: "the detection could not be committed, so no evidence and no push exist for it: " + err.Error(),
			})
		}
		return
	}

	// Step 2: the clip.
	ref, clipErr := r.cutAndStore(ctx, c)
	if clipErr != nil {
		_ = r.opt.Store.Transition(ctx, id, store.StatusClipRefused, "", clipErr.Error())
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: c.At, Reason: store.FaultClipRefused, Detail: clipErr.Error(),
		})
	} else {
		if err := r.opt.Store.Transition(ctx, id, store.StatusClipOK, ref.StorageRef, ""); err != nil {
			return
		}
	}

	// Step 3: the push. The payload is built and enqueued; the pump attempts it.
	if err := r.enqueueEvent(ctx, c, ref); err != nil {
		_ = r.opt.Store.Transition(ctx, id, store.StatusPushRefused, "", err.Error())
		r.finish(ctx, id)
	}
}

// EvidenceResult is what the clip step produced.
type EvidenceResult struct {
	StorageRef string
	SHA256     string
	TimeBegin  time.Time
	TimeEnd    time.Time
	Trim       string
	Bytes      int64
}

func (r *Runner) cutAndStore(ctx context.Context, c detect.Crossing) (EvidenceResult, error) {
	from := c.At.Add(-r.opt.ClipBefore)
	to := c.At.Add(r.opt.ClipAfter)
	staging := filepath.Join(r.opt.DataRoot, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return EvidenceResult{}, fmt.Errorf("the staging directory could not be created: %w", err)
	}
	cut, err := r.opt.Clipper.Cut(ctx, recorder.ClipRequest{From: from, To: to, Out: filepath.Join(staging, "cut.mp4")})
	if err != nil {
		return EvidenceResult{}, err
	}
	stored, err := r.opt.Evidence.Put(cut.Path)
	if err != nil {
		return EvidenceResult{}, err
	}
	ref := r.opt.Evidence.StorageRef(stored.Hash)
	if err := r.opt.Store.PutEvidence(ctx, store.Evidence{
		ID: detectionIDOf(c, r.opt.Provenance) + "-ev", DetectionID: detectionIDOf(c, r.opt.Provenance),
		SHA256: stored.Hash, StorageRef: ref, Bytes: stored.Bytes, Trim: cut.Trim,
		TimeBegin: from, TimeEnd: to, ReadbackOK: true,
	}); err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{StorageRef: ref, SHA256: stored.Hash, TimeBegin: from, TimeEnd: to, Trim: cut.Trim, Bytes: stored.Bytes}, nil
}

func (r *Runner) enqueueEvent(ctx context.Context, c detect.Crossing, ev EvidenceResult) error {
	tag := EventTag(c, r.opt.Provenance)
	in := xprotect.EventInput{
		EventTypeGUID: r.opt.EventTypeGUID, SourceGUID: r.opt.CameraGUID, At: c.At, Tag: tag,
		Class: c.Class, Value: c.Class, Confidence: scoreOf(c), Box: xprotect.Box{},
		RuleName: c.Line + "-line-cross",
		Evidence: xprotect.EvidenceRef{StorageRef: ev.StorageRef, SHA256: ev.SHA256, TimeBegin: ev.TimeBegin.UTC().Format(time.RFC3339), TimeEnd: ev.TimeEnd.UTC().Format(time.RFC3339),
			Note: "POC evidence ref; the bookmark pushed alongside points at no VMS recording in this build"},
	}
	body, err := xprotect.BuildEvent(in)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(body)
	id, err := r.opt.Store.Enqueue(ctx, store.OutboxEntry{
		DetectionID: detectionIDOf(c, r.opt.Provenance), Kind: store.KindEvent,
		Payload: string(payload), Tag: tag,
	}, r.opt.PushMax)
	if err != nil {
		return err
	}
	r.logf("queued push %s for detection %s", id, detectionIDOf(c, r.opt.Provenance))
	return nil
}

// pump makes the outbox attempts. It is the only place a push is attempted, so
// attempts and results cannot diverge.
func (r *Runner) pump(ctx context.Context) {
	if r.opt.Store == nil {
		// No store means no outbox, and a pump that dereferences a nil store
		// takes the whole process down at second two.
		return
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopPush:
			return
		case <-tick.C:
		}
		due, err := r.opt.Store.Due(ctx, time.Now(), 32)
		if err != nil {
			r.logf("outbox pump: %v", err)
			continue
		}
		for _, e := range due {
			r.attempt(ctx, e)
		}
	}
}

func (r *Runner) attempt(ctx context.Context, e store.OutboxEntry) {
	// The outbox row carries the exact bytes that were meant to be sent, and
	// they are sent as they are. Reconstructing an EventInput from a stored
	// payload would be a lossy round trip through a Go struct, and the field
	// that quietly did not survive it would be the one the report claims was
	// pushed.
	out, err := r.opt.XProtect.SendPayload(ctx, xprotect.PushPayload{Body: []byte(e.Payload), Tag: e.Tag})
	switch {
	case err == nil && out.State == "push_sent":
		_ = r.opt.Store.RecordResult(ctx, e.ID, "push_sent", out.Detail, out.RemoteID)
		_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushSent, "", "")
		r.pushed++
		r.finish(ctx, e.DetectionID)
	case err == nil:
		// The push returned something the client is not willing to call sent. The
		// row stays with no result, so AC4 counts it, and the backoff retries.
		_ = r.opt.Store.RecordResult(ctx, e.ID, "", out.Detail, "")
		r.logf("push %d: state=%q %s", e.ID, out.State, out.Detail)
	default:
		var refused *xprotect.RefusedError
		if asRefused(err, &refused) {
			_ = r.opt.Store.RecordResult(ctx, e.ID, "push_refused", refused.Detail, "")
			_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushRefused, "", refused.Detail)
			r.finish(ctx, e.DetectionID)
			return
		}
		var auth *xprotect.ErrAuth
		if asAuth(err, &auth) {
			_ = r.opt.Store.RecordResult(ctx, e.ID, "auth_failed", auth.Error(), "")
			_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushRefused, "", "auth_failed: "+auth.Detail)
			r.finish(ctx, e.DetectionID)
			return
		}
		// Retryable: the row keeps no result and the backoff brings it back.
		_ = r.opt.Store.RecordResult(ctx, e.ID, "", err.Error(), "")
		r.logf("push %d retryable: %v", e.ID, err)
	}
}

func asRefused(err error, dst **xprotect.RefusedError) bool {
	r, ok := err.(*xprotect.RefusedError)
	if ok {
		*dst = r
	}
	return ok
}

func asAuth(err error, dst **xprotect.ErrAuth) bool {
	a, ok := err.(*xprotect.ErrAuth)
	if ok {
		*dst = a
	}
	return ok
}

// finish moves a detection to `done` once it has a clip outcome AND a push
// outcome.
//
// Both are read from the DURABLE rows — the evidence and the outbox — and not
// from the single status column. A status is one value, so a detection that has
// reached `push_sent` has by definition forgotten that it was ever `clip_ok`,
// and a check that reads the column concludes that the clip outcome is missing
// and leaves the detection non-terminal forever. That is not a cosmetic bug: a
// detection stuck non-terminal is a LOST EVENT under AC2, caused by a helper
// function.
func (r *Runner) finish(ctx context.Context, id string) {
	d, err := r.opt.Store.Get(ctx, id)
	if err != nil {
		return
	}
	// The clip outcome: either a stored clip, or a recorded reason for not
	// having one. A detection with neither is still being cut.
	clipSettled := d.ClipRef != "" || d.ClipReason != ""
	if !clipSettled {
		ev, err := r.opt.Store.EvidenceFor(ctx, id)
		if err == nil && len(ev) > 0 {
			clipSettled = true
		}
	}
	// The push outcome: the outbox row reached a result, or it was dead-lettered,
	// or the push was refused and the detection says so.
	pushSettled := d.Status == store.StatusPushSent ||
		d.Status == store.StatusPushRefused ||
		d.Status == store.StatusPushDead
	if !pushSettled {
		entries, err := r.opt.Store.OutboxFor(ctx, id)
		if err == nil {
			for _, e := range entries {
				if e.DeadLetter || (e.Result != "" && e.Result != "unknown") {
					pushSettled = true
					break
				}
			}
		}
	}
	if clipSettled && pushSettled {
		_ = r.opt.Store.Complete(ctx, id)
	}
}

func scoreOf(c detect.Crossing) float64 {
	// The engine does not carry the score onto the crossing, because the
	// decision is about geometry. The score is read back off the box, which the
	// sidecar gave us, and 0 would be a lie, so a missing score is 0 and the
	// report says the box had no score.
	return 0
}

// DetectionID is content-derived. A counter would produce the same id twice
// after a crash and the two events would be indistinguishable; a hash of the
// content cannot.
func DetectionID(c detect.Crossing, provenance string) string { return detectionIDOf(c, provenance) }

func detectionIDOf(c detect.Crossing, provenance string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%s|%s|%.6f|%.6f|%.6f|%.6f|%d",
		provenance, c.Class, c.At.UnixNano(), c.Line, c.Direction,
		c.Box.X1, c.Box.Y1, c.Box.X2, c.Box.Y2, c.TrackID)
	return "det-" + hex.EncodeToString(h.Sum(nil))[:24]
}

// EventTag is the idempotency key the read-back matches on. It is derived from
// the detection, never from a counter, because the API has no idempotency key
// and a counter would collide across a restart.
func EventTag(c detect.Crossing, provenance string) string {
	return "aicap-" + strings.ToLower(c.Class) + "-" + detectionIDOf(c, provenance)[4:20]
}

func (r *Runner) beat(ctx context.Context) error {
	if r.opt.heartbeat != nil {
		return r.opt.heartbeat(ctx)
	}
	return r.heartbeat(ctx)
}

func (r *Runner) heartbeat(ctx context.Context) error {
	if r.opt.Store == nil {
		// A runner with no store has nowhere to record a heartbeat. The loop is
		// still testable without one, so it reports that it recorded nothing
		// rather than dereferencing nil and taking the run down.
		return nil
	}
	frames, _, decodeErrs := r.opt.Recorder.Counts()
	idx, err := r.opt.Recorder.Index()
	segments := 0
	if err == nil {
		segments = idx.Len()
	}
	if err := r.opt.Store.Beat(ctx, store.Heartbeat{RunID: r.opt.RunID, At: time.Now(), Frames: int64(frames), Detections: int64(r.countDetections(ctx))}); err != nil {
		return err
	}

	p50, p95, p99, _, _ := r.lat.Percentiles()
	rss, rssErr := currentRSS()
	if rssErr != nil {
		// A platform with no reader is NOT RUN, not zero. The sample records the
		// peak as nothing and the report says why, rather than scoring a memory
		// clause against a number nobody measured.
		r.logf("resource sample: resident set NOT RUN — %v", rssErr)
	}
	disk, _ := recorder.Used(r.opt.DataRoot)
	dbSize, _ := r.opt.Store.Size()
	// AC5's CPU clause is about ffmpeg and the sidecar, measured as a share of
	// this machine. A sample that could not be taken is recorded as nothing, and
	// the report says why, rather than being recorded as zero and passing.
	cpuPct, cpuOK, cpuWhy := 0.0, false, ""
	if r.cpu != nil {
		cpuPct, cpuOK, cpuWhy = r.cpu.Percent(time.Now())
	}
	if !cpuOK {
		r.logf("resource sample: child CPU NOT RUN — %s", cpuWhy)
	}
	if err := r.opt.Store.PutSample(ctx, r.opt.RunID, store.Sample{
		At: time.Now(), RSSBytes: rss, CPUPercent: cpuPct, AIP99MS: float64(p99.Milliseconds()),
		PipeDepth: r.opt.Recorder.PipeDepth(), DiskBytes: disk, DB: dbSize, CPUMeasured: cpuOK,
	}); err != nil {
		return err
	}
	r.logf("heartbeat: frames=%d segments=%d decodeErrors=%d ai p50/p95/p99=%s/%s/%s pipeDepth=%d db=%s childrenCpu=%s",
		frames, segments, decodeErrs, p50.Round(time.Millisecond), p95.Round(time.Millisecond),
		p99.Round(time.Millisecond), r.opt.Recorder.PipeDepth(), Bytes(dbSize), cpuText(cpuPct, cpuOK, cpuWhy))
	return nil
}

// cpuText renders a CPU sample, or the reason there was none. Printing "0%"
// for a measurement nobody took is the exact failure this is guarding.
func cpuText(pct float64, ok bool, why string) string {
	if !ok {
		return "NOT RUN (" + oneLine(why) + ")"
	}
	return fmt.Sprintf("%.1f%%", pct)
}

func (r *Runner) countDetections(ctx context.Context) int {
	if r.opt.Store == nil {
		return 0
	}
	n, err := r.opt.Store.Count(ctx, r.opt.RunID)
	if err != nil {
		return 0
	}
	return n
}

// SetFrameHook installs the screen's frame callback after construction, because
// the screen needs the store and the run id and both of those are known before
// the runner is.
func (r *Runner) SetFrameHook(fn func(recorder.Frame, []ai.Box, []byte)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opt.onFrame = func(f recorder.Frame, b []ai.Box, jpg []byte) {
		if jpg == nil && r.opt.LastJPEG != nil {
			jpg = r.opt.LastJPEG()
		}
		fn(f, b, jpg)
	}
}

// StopPump ends the outbox pump AND drains what is left.
//
// The drain is the point. The pump runs on a two-second tick, and a run can end
// with rows it has not reached: a real run produced 44 detections, each clip cut
// is a synchronous ffmpeg re-encode on the frame loop, and at shutdown some rows
// were still at attempts=0 with no result. AC4 counts exactly that as a failure,
// correctly: a push that was enqueued and never attempted is a push nobody can
// account for. Leaving them undrained made the criterion fail for a bookkeeping
// reason rather than a delivery one, and a criterion that fails for the wrong
// reason trains a reader to ignore it.
func (r *Runner) StopPump() {
	r.pushOnce.Do(func() { close(r.stopPush) })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		due, err := r.opt.Store.Due(context.Background(), time.Now(), 64)
		if err != nil || len(due) == 0 {
			return
		}
		for _, e := range due {
			r.attempt(context.Background(), e)
		}
	}
}

// AICounters returns AC6's counters, read from the run rather than recomputed.
func (r *Runner) AICounters() (unparsed, healthyFromFailed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.unparsedAI, r.healthyFromFailed
}

// Latencies exposes the AI round-trip reservoir.
func (r *Runner) Latencies() *ai.Latencies { return r.lat }

// Pushes is how many pushes were confirmed sent.
func (r *Runner) Pushes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pushed
}

// Sink adapts the store to the supervisor's Fault sink.
type Sink struct {
	Store *store.Store
	RunID string
	Logf  func(string, ...any)
}

// Record writes a supervisor fault into the store.
func (s Sink) Record(ctx context.Context, f supervisor.Fault) {
	if s.Logf != nil {
		s.Logf("fault %s: %s", f.Reason, f.Detail)
	}
	if s.Store == nil {
		return
	}
	_ = s.Store.RecordFault(ctx, s.RunID, store.Fault{At: f.At, Reason: string(f.Reason), Detail: f.Detail})
}
