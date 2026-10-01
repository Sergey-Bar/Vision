package soak

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/source"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/supervisor"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
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
	// LastJPEGSeq is LastJPEG and the sequence of the frame it belongs to, read
	// together.
	//
	// W2-5: the snapshot path needs the PAIR, because a picture with no number
	// beside it cannot be checked against the decision it is supposed to be
	// evidence of. A nil here means no snapshot is attributed, and that is said
	// out loud as a recorded fault rather than leaving the pointer readable and
	// unchecked.
	LastJPEGSeq func() ([]byte, uint64)
	// FrameJPEGFor returns the picture of one SPECIFIC frame, by sequence.
	//
	// It is what the snapshot path uses, because the process is a goroutine or two
	// behind its own pipe and "the newest picture" is a later frame than the one
	// the decision was made on. A nil here means no snapshot is attributed, and
	// that is said out loud as a recorded fault rather than leaving the pointer
	// readable and unchecked.
	FrameJPEGFor func(seq uint64) ([]byte, bool)
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
	//
	// The hook carries the sidecar's TELEMETRY and the real AI STATE beside the
	// frame, and those two are the reason it takes five arguments. Both were
	// previously unreachable from the screen, which is how the screen came to
	// hardcode `answered`: it had no state to print, so it printed a word that
	// is not in CONTRACT.md §2.2's vocabulary. Appending them is the whole of
	// the change — the alternative was a new hook signature and a new hook type,
	// and a second way to be handed a frame is a second way to be handed an
	// incomplete one.
	onFrame   func(recorder.Frame, []ai.Box, []byte, ai.Telemetry, string)
	heartbeat func(context.Context) error

	// Rehearsals are the fault rehearsals already performed on this build, read
	// from the store. A run with none of them scores AC7 as NOT RUN, which is
	// the correct answer and not a passing one.
	Rehearsals []Rehearsal

	// TriggerMode names what produces the boxes in the trigger path.
	//
	// `novelty` is the shipped path: the local novelty engine measures a region
	// from pixels and proposes, and the VLM verifies once per event. `sidecar` is
	// the older path, where a local ONNX sidecar classified every frame at 5 fps.
	//
	// It is a field rather than a boolean because the two paths differ in cost
	// (one call per event against one per frame), in latency, and in what an
	// absent answer MEANS. A boolean `UseSidecar` is a name that has to be read
	// to know which way round it is, and that is how a demo ends up paying per
	// frame for a model it says it calls per event.
	TriggerMode string

	// Trigger is the local novelty engine. Nil in `sidecar` mode.
	Trigger Trigger
	// Observer is the VLM. Nil means nobody to ask, which is `not_asked` and not
	// a degraded answer.
	Observer Observer

	// BookmarkDevicePath is the VMS device path a bookmark points at.
	//
	// It is required to place a bookmark and was already required by
	// configuration (`SP_POC_DEVICE_PATH`) and read nowhere. A bookmark with no
	// device path names no camera, so it points at nothing, and a bookmark that
	// points at nothing is worse than no bookmark because an operator follows it.
	BookmarkDevicePath string

	// ReferenceSHA is the pinned reference's content hash for this run's camera,
	// so the decision record can name the picture every measurement was made
	// against. It is set by the runner when it pins or restores.
	ReferenceSHA string
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
	// detectors is the split's arithmetic, observed once per frame, and it is
	// the only thing in this run that can answer "why zero detections".
	detectorsMu sync.Mutex
	detectors   detect.Counters
}

// DetectorTally is the run's detector arithmetic, as the report needs it.
//
// It is a snapshot: every number is copied out under the lock, so a report
// written while frames are still arriving cannot read a half-updated tally and
// print a candidate share that disagrees with the approved count beside it.
func (r *Runner) DetectorTally() DetectorTally {
	r.detectorsMu.Lock()
	defer r.detectorsMu.Unlock()
	c := r.detectors
	t := DetectorTally{
		Seen:            c.FramesAsked > 0,
		FramesAsked:     c.FramesAsked,
		FramesAnswered:  c.FramesAnswered,
		FramesNoObjects: c.FramesNoObjects,
		FramesAbsent:    c.FramesAbsent,
		BoxCandidates:   c.BoxCandidates,
		BoxesApproved:   c.BoxesApproved,
		BoxesRejected:   c.BoxesRejected,
	}
	if share, ok := c.CandidateShare(); ok {
		t.CandidateShareFrac, t.ShareMeasured = share, true
	}
	if r.opt.Engine != nil {
		t.ApprovedByFamily, t.BoxesRejectedBy = r.opt.engineBreakdown()
	}
	return t
}

// EngineTally is a nil-safe read of the engine's own per-family tally, so
// DetectorTally does not have to carry an engine-nil check of its own. A runner
// built without an engine has no detector arithmetic to report, and saying so
// with `Seen: false` is right: nothing was offered to anything.
func (r *RunnerOptions) EngineTally() *detect.Tally {
	if r.Engine == nil {
		return nil
	}
	t := r.Engine.Tally()
	return &t
}

// engineBreakdown flattens the engine's per-family tally into the two maps the
// report prints, because a report cannot show four family rows inside a sentence
// about one number.
func (r *RunnerOptions) engineBreakdown() (byFamily, byReason map[string]int) {
	t := r.EngineTally()
	if t == nil {
		return nil, nil
	}
	byFamily, byReason = map[string]int{}, map[string]int{}
	for _, row := range t.Rows() {
		byFamily[string(row.Family)] = row.Approved
		for _, rc := range row.Reasons {
			byReason[rc.Reason] += rc.Count
		}
	}
	return byFamily, byReason
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
	if opt.TriggerMode == "" {
		// The shipped path is the novelty trigger. A runner built with neither a
		// trigger nor a sidecar client is still legal — the loop tests build one —
		// and it produces `not_asked` rather than a zero detection count.
		opt.TriggerMode = TriggerNovelty
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

// Trigger modes. The shipped path is `novelty`; `sidecar` is the older local
// classifier, kept as a measurement tool rather than in the trigger path.
const (
	// TriggerNovelty is the local novelty engine: measured regions, one model
	// call per event, licence-free.
	TriggerNovelty = "novelty"
	// TriggerSidecar is the per-frame ONNX sidecar. It is a measurement tool now:
	// useful for choosing a threshold, wrong for the trigger path because it
	// costs a call per frame and answers about frames nobody asked about.
	TriggerSidecar = "sidecar"
)

// ValidTriggerMode refuses a mode this build does not operate.
func ValidTriggerMode(m string) bool { return m == TriggerNovelty || m == TriggerSidecar }

// UsesSidecar reports whether the run's boxes come from the per-frame sidecar.
//
// Every sidecar precondition is gated on this one predicate, because three
// separate conditionals is three places where the gate was left hard-on and the
// ONNX model was required by a run that never called it.
func UsesSidecar(m string) bool { return m == TriggerSidecar }

// process handles one frame: measure, decide, and for each incident record first
// and then act.
func (r *Runner) process(ctx context.Context, f recorder.Frame) {
	r.mu.Lock()
	hook := r.opt.onFrame
	r.mu.Unlock()

	var boxes []ai.Box
	telemetry := ai.Telemetry{}
	// aiState is the four-word vocabulary state, and it defaults to `not_asked`
	// — the honest reading when no client is configured at all, rather than an
	// empty string that the screen would render as "fine".
	aiState := ai.StateNotAsked

	switch {
	case r.opt.Trigger != nil:
		boxes, aiState = r.measureTrigger(f)
	default:
		boxes, telemetry, aiState = r.askSidecar(ctx, f)
	}

	if r.opt.Engine == nil {
		if hook != nil {
			hook(f, boxes, nil, telemetry, aiState)
		}
		r.handleSentinels(ctx, f)
		return
	}
	crossings, tracks, _ := r.opt.Engine.FeedAnswered(f.At, detect.Answer{
		Boxes:      toDetect(boxes),
		AIState:    aiState,
		Candidates: telemetry.Candidates,
	})
	// The split's own arithmetic is observed here and nowhere else, because this
	// is the only place that has both the split and the frame it came from.
	//
	// It was NOT observed anywhere at all before: `detect.Counters` existed, was
	// tested, and no production code called `Observe`. So a run could report
	// `detections | 0` and there was no number anywhere in the system that could
	// say whether the model had seen nothing or the pipeline had thrown
	// everything away. Those are a fact about a road and a configuration mistake,
	// and they print identically.
	r.detectorsMu.Lock()
	r.detectors.Observe(r.opt.Engine.LastSplit())
	r.detectorsMu.Unlock()
	if hook != nil {
		// The REAL state, from `ai.Interpret` — never a hardcoded `answered`.
		// The screen used to print `answered` for every frame including the ones
		// that were never sent, and `answered` is not in CONTRACT.md §2.2's
		// four-word vocabulary at all: it is a fifth word that reads as
		// reassurance and means nothing.
		hook(f, boxes, nil, telemetry, aiState)
	}
	// The measurement's state is recorded with the detection, so the report can say
	// whether a crossing came from a frame something actually measured.
	for _, c := range crossings {
		r.handleIncident(ctx, toTriggerEvent(c, r.defaultPromptID(), r.triggerScore()), aiState, f.Seq)
	}
	_ = tracks

	// A reference trigger's sustained event is NOT a line crossing: the whole view
	// changed, so there is no line to cross and no side to come from. It is handled
	// here, on the same spine — record, evidence, ask, push, bookmark — and the only
	// thing that differs is which arithmetic produced the trigger.
	r.handleSentinels(ctx, f)
}

// measureTrigger runs the local novelty engine over one frame.
//
// It returns `asked_answered` when the engine produced something and
// `no_objects` when it did not, and it never returns a box it did not measure. A
// novelty engine has no opinion about whether a change is an incident, so the most
// it can honestly report is that it measured something.
func (r *Runner) measureTrigger(f recorder.Frame) ([]ai.Box, string) {
	if len(f.RGB) == 0 || f.Width <= 0 || f.Height <= 0 {
		r.logf("frame %d: no pixels; the trigger measured nothing and this is not a zero", f.Seq)
		return nil, ai.StateNotAsked
	}
	cands, _ := r.opt.Trigger.Candidates(f.RGB, f.Width, f.Height)
	out := make([]ai.Box, 0, len(cands))
	for _, c := range cands {
		if !c.Box.NonEmpty() {
			// An empty region is not a proposal. Passing it on would put an
			// all-zero box on a pushed event, which is the defect RC-6 exists for.
			continue
		}
		out = append(out, ai.Box{
			Class: r.triggerClass(), Score: c.Score,
			X1: c.Box.X1, Y1: c.Box.Y1, X2: c.Box.X2, Y2: c.Box.Y2,
		})
	}
	if len(out) == 0 {
		return nil, ai.StateNoObjects
	}
	return out, ai.StateAnswered
}

// askSidecar is the older per-frame path, kept as a measurement tool.
func (r *Runner) askSidecar(ctx context.Context, f recorder.Frame) ([]ai.Box, ai.Telemetry, string) {
	if r.opt.Client == nil {
		r.logf("frame %d: not_asked — no sidecar client is configured", f.Seq)
		return nil, ai.Telemetry{}, ai.StateNotAsked
	}
	res := r.opt.Client.Detect(ctx, f.Seq, f.RGB, f.Width, f.Height)
	r.lat.Add(res.Latency)
	r.mu.Lock()
	r.lastAIState = res.State
	r.mu.Unlock()
	switch res.State {
	case ai.StateDegraded, ai.StateCannotTell:
		// Only a reply that ARRIVED and could not be read counts as unparsed.
		// A `degraded` frame is a deadline, and a tripwire refusal is the
		// tripwire working; folding either into AC6's "unparsed" clause would
		// make the clause mean something other than what it says.
		if res.Unparsed {
			r.mu.Lock()
			r.unparsedAI++
			r.mu.Unlock()
		}
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: f.At, Reason: store.FaultAIUnreadable,
			Detail: fmt.Sprintf("frame %d at %s: %s", f.Seq, f.At.Format(time.RFC3339), res.Reason),
		})
	case ai.StateNotAsked:
		// A frame that was never sent is not a failure of the model, and it is
		// not an empty scene. It is counted as not_asked and nothing else.
	}
	boxes := make([]ai.Box, 0, len(res.Boxes))
	boxes = append(boxes, res.Boxes...)
	return boxes, res.Telemetry, res.State
}

// handleSentinels runs every trigger that produces a whole-view event rather than
// a proposed region.
func (r *Runner) handleSentinels(ctx context.Context, f recorder.Frame) {
	t := r.opt.Trigger
	if t == nil || len(f.RGB) == 0 {
		return
	}
	cfg := t.Config()
	if string(cfg.Mode) != string(referenceMode) {
		return
	}
	finding, fired := t.Watch(f.At, f.RGB, f.Width, f.Height)
	if !fired {
		return
	}
	ev := TriggerEvent{
		DetectionClass: cfg.Class, PromptID: cfg.PromptID,
		// No line and no direction, and both say so. A whole-frame change has
		// neither, and inventing one would put a line name on an event that never
		// crossed anything.
		Line: "reference-pinned", Direction: "none", At: f.At,
		Box:   [4]float64{0, 0, 1, 1},
		Score: finding.Score, Novelty: t.Last(), Streak: finding.Streak,
		RegionSource: finding.RegionSource,
	}
	r.handleIncident(ctx, ev, ai.StateAnswered, f.Seq)
}

func toDetect(boxes []ai.Box) []detect.Box {
	out := make([]detect.Box, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, detect.Box{Class: b.Class, Score: b.Score, X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2})
	}
	return out
}

// handleIncident is the R2 sequence, in this order and no other:
//
//  1. commit the detection row in `accepted`
//  2. record the decision inputs, so the incident can be replayed from facts
//  3. render and store the snapshot of the frame the decision was made on
//  4. cut the clip and read it back, record clip_ok or clip_refused
//  5. ask the VLM ONCE about the clip, and record the attribution
//  6. enqueue the push
//  7. enqueue the bookmark
//
// Nothing external happens before step 1, and a crash between any two steps
// leaves a resumable row.
//
// The snapshot is step 3 and not step 4 because `cutAndStore` is a synchronous
// ffmpeg re-encode that blocks the frame loop, so anything rendered after it
// would be a snapshot of a LATER frame — which is the one thing a picture of a
// decision must not be.
//
// The model call is step 5 and not per frame. A paid API belongs on the event,
// not at 5 fps: the same clip asked about twice costs twice and tells us nothing
// new, and the clip is the evidence the answer is about.
func (r *Runner) handleIncident(ctx context.Context, ev TriggerEvent, aiState string, frameSeq uint64) {
	provenance := r.opt.Provenance
	id := ev.DetectionIDFor(provenance)
	tag := ev.EventTagFor(provenance)
	top, left, bottom, right := regionThousandths(ev)

	det := store.Detection{
		ID: id, RunID: r.opt.RunID, At: ev.At, Class: ev.DetectionClass,
		Family: string(classify.FamilyOf(ev.DetectionClass)), TrackID: ev.TrackID, Score: ev.Score,
		BoxTop: top, BoxLeft: left, BoxBottom: bottom, BoxRight: right,
		Direction: ev.Direction, AIState: aiState, Provenance: provenance,
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
				At: ev.At, Reason: store.FaultDBUnavailable,
				Detail: "the detection could not be committed because the database is not accepting writes, so no clip and no push exist for it: " + err.Error(),
			})
			r.logf("database is not accepting writes: %v", err)
		default:
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: ev.At, Reason: store.FaultClipRefused,
				Detail: "the detection could not be committed, so no evidence and no push exist for it: " + err.Error(),
			})
		}
		return
	}
	r.appendLedger(ctx, store.LedgerDetectionCommitted, id,
		"class="+ev.DetectionClass, "prompt="+ev.PromptID, "line="+ev.Line,
		"direction="+ev.Direction,
		"box="+itoa64(int64(top))+","+itoa64(int64(left))+","+itoa64(int64(bottom))+","+itoa64(int64(right)),
		"score="+fmt.Sprintf("%.6f", ev.Novelty.Value),
		"threshold="+fmt.Sprintf("%.6f", r.threshold()),
		"region_source="+string(ev.RegionSource),
	)

	// Step 2: the decision inputs, stored from the same values the arithmetic
	// used. A decision whose inputs cannot be replayed is a claim.
	if r.opt.Trigger != nil {
		d := decisionFrom(ev, r.opt.RunID, r.opt.Trigger.Config(), frameSeq, r.opt.ReferenceSHA)
		d.DetectionID = id
		if err := r.opt.Store.PutDecision(ctx, d); err != nil {
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: ev.At, Reason: store.FaultDBUnavailable,
				Detail: "the decision inputs could not be recorded, so this incident cannot be replayed from facts: " + err.Error(),
			})
		}
	}

	// Step 3: the picture of the decision. Best-effort and non-fatal — see
	// storeSnapshot.
	r.storeSnapshot(ctx, id, ev, aiState, frameSeq)

	// Step 4: the clip.
	ref, clipErr := r.cutAndStore(ctx, ev.At)
	if clipErr != nil {
		_ = r.opt.Store.Transition(ctx, id, store.StatusClipRefused, "", clipErr.Error())
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultClipRefused, Detail: clipErr.Error(),
		})
	} else {
		if err := r.opt.Store.Transition(ctx, id, store.StatusClipOK, ref.StorageRef, ""); err != nil {
			return
		}
		r.appendLedger(ctx, store.LedgerClipStored, id,
			"sha256="+ref.SHA256, "ref="+ref.StorageRef,
			"trim="+ref.Trim,
			"bytes="+itoa64(ref.Bytes))
	}

	// Step 5: the model, once, on the clip that was just stored.
	obs := r.askObserver(ctx, id, ev, ref, clipErr)

	// Steps 6 and 7: the two write-backs, both queued rather than attempted, so a
	// slow VMS cannot block the frame loop.
	if err := r.enqueueEvent(ctx, ev, ref, obs, tag); err != nil {
		_ = r.opt.Store.Transition(ctx, id, store.StatusPushRefused, "", err.Error())
		r.finish(ctx, id)
		return
	}
	if err := r.enqueueBookmark(ctx, ev, ref, obs, tag); err != nil {
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultClipRefused,
			Detail: "the bookmark could not be queued, so the pushed event has no pointer to the recording: " + err.Error(),
		})
	}
}

// askObserver asks the model once about the stored clip and records the answer.
//
// There is exactly one path from a clip to an observation, and every branch of it
// records something. A caller that gets no observation cannot record that it got
// nothing, and a run that silently skips the model on some incidents reads as a
// run where the model found nothing.
func (r *Runner) askObserver(ctx context.Context, id string, ev TriggerEvent, ref EvidenceResult, clipErr error) vlm.Observation {
	if r.opt.Observer == nil {
		// Nobody to ask. `not_asked`, and it is recorded as that rather than as
		// left blank.
		obs := vlm.Observation{DetectionID: id, State: ai.StateNotAsked}
		obs.SetState(ai.StateNotAsked)
		obs.Finalise()
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	if clipErr != nil {
		// There is no clip to send. We did not ask, and the reason is a recorded
		// clip failure rather than a silent skip.
		obs := vlm.Observation{DetectionID: id, State: ai.StateNotAsked, Message: clipErr.Error()}
		obs.SetState(ai.StateNotAsked)
		obs.Finalise()
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	clip, err := readEvidenceClip(r.opt.Evidence, ref.SHA256)
	if err != nil {
		obs := vlm.Observation{DetectionID: id, State: ai.StateNotAsked, Message: err.Error()}
		obs.SetState(ai.StateNotAsked)
		obs.Finalise()
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultClipRefused,
			Detail: "the stored clip could not be read back for the model, so the model was not asked: " + err.Error(),
		})
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	obs := r.opt.Observer.Observe(ctx, id, ev.PromptID, clip, "video/mp4")
	if obs.DetectionID == "" {
		obs.DetectionID = id
	}
	r.recordObservation(ctx, id, ev, obs, ref.SHA256)
	return obs
}

// recordObservation writes the attribution row and the ledger step.
func (r *Runner) recordObservation(ctx context.Context, id string, ev TriggerEvent, obs vlm.Observation, clipSHA string) {
	input := map[string]string{}
	if clipSHA != "" {
		input["clip"] = clipSHA
	}
	if r.opt.ReferenceSHA != "" {
		input["reference"] = r.opt.ReferenceSHA
	}
	row := store.Observation{
		DetectionID: id, RunID: r.opt.RunID,
		PromptID: obs.PromptID, PromptVersion: obs.PromptVersion,
		AIState: obs.State, Verdict: string(obs.Verdict), Summary: obs.Summary,
		Confidence: obs.Confidence, InsufficientEvidence: obs.InsufficientEvidence,
		ModelIdentity: obs.ModelIdentity, ContractVersion: obs.ContractVersion,
		Temperature: obs.Temperature, ReasonCode: obs.Reason, Detail: obs.Message,
		OutputHash: obs.OutputHash, InputHashes: store.InputHashesOf(input),
		LatencyMS: obs.LatencyMS, PromptTokens: obs.PromptTokens,
		CompletionTokens: obs.CompletionTokens, AssetID: obs.AssetID,
	}
	// The prompt the row carries is the CLASS's prompt, not whatever the observer
	// last reported: the class is the fact, and an observer that returned a blank
	// prompt id has failed in a way the row must not hide.
	if row.PromptID == "" {
		row.PromptID = ev.PromptID
	}
	if err := r.opt.Store.PutObservation(ctx, row); err != nil {
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultDBUnavailable,
			Detail: "the attribution record could not be written, so this incident has no auditable answer: " + err.Error(),
		})
		return
	}
	r.appendLedger(ctx, store.LedgerVLMAsked, id,
		"aistate="+row.AIState, "verdict="+row.Verdict,
		"prompt_id="+row.PromptID, "prompt_version="+row.PromptVersion,
		"contract_version="+row.ContractVersion,
		"model_identity="+row.ModelIdentity,
		"temperature="+fmt.Sprintf("%.4f", row.Temperature),
		"latency_ms="+itoa64(row.LatencyMS),
		"input_hashes="+row.InputHashes)
	r.appendLedger(ctx, store.LedgerVLMVerdict, id,
		"output_hash="+row.OutputHash,
		"insufficient_evidence="+boolWord(row.InsufficientEvidence),
		"confidence="+fmt.Sprintf("%.6f", row.Confidence),
		"reason="+row.ReasonCode)
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// readEvidenceClip reads a stored clip back for the model.
//
// Read-back, not a path from the cut: the file on disk in staging is a temporary
// the disk budget may have swept, and an answer about a clip we no longer have is
// an answer nobody can check.
func readEvidenceClip(st *evidence.Store, sha string) ([]byte, error) {
	if st == nil || sha == "" {
		return nil, errors.New("soak: there is no stored clip to send")
	}
	rc, err := st.Open(sha)
	if err != nil {
		return nil, fmt.Errorf("soak: the stored clip %s cannot be opened: %w", sha, err)
	}
	defer func() { _ = rc.Close() }()
	return vlm.CopyTo(rc)
}

// regionThousandths is the ONE place a normalised box becomes 0..1000
// thousandths.
//
// `detections.box_*`, the pushed event and the stored decision all use it, and
// they used to be scaled by three separate expressions. Three expressions is three
// chances for one of them to drift, and a drifted box on a pushed event is a
// number on a screen that disagrees with the row beside it.
func regionThousandths(ev TriggerEvent) (top, left, bottom, right int) {
	if ev.RegionSource == novelty.RegionWholeFrame {
		return 0, 0, 1000, 1000
	}
	top = int(ev.Box[1] * 1000)
	left = int(ev.Box[0] * 1000)
	bottom = int(ev.Box[3] * 1000)
	right = int(ev.Box[2] * 1000)
	// A region that measured out as empty is clamped to the whole frame rather
	// than pushed as zeros. Zero is not a conservative box; it is a missing one,
	// and BuildEvent refuses it.
	if right <= left || bottom <= top {
		return 0, 0, 1000, 1000
	}
	return top, left, bottom, right
}

// triggerClass is the detector's own name for what the novelty engine measured.
func (r *Runner) triggerClass() string {
	if r.opt.Trigger != nil {
		return r.opt.Trigger.Config().Class
	}
	return "motion"
}

// defaultPromptID is the incident class asked about when the trigger has none.
//
// It is the `alarm` prompt because a temporal trigger's whole question is "is
// there a real incident in this clip", which is what that file says. A run with a
// trigger that declared no class is asking that question, and saying so is better
// than inventing a class.
func (r *Runner) defaultPromptID() string {
	if r.opt.Trigger != nil && r.opt.Trigger.Config().PromptID != "" {
		return r.opt.Trigger.Config().PromptID
	}
	return "alarm"
}

func (r *Runner) triggerScore() novelty.Score {
	if r.opt.Trigger != nil {
		return r.opt.Trigger.Last()
	}
	return novelty.Score{Mode: novelty.ModeTemporal, Reason: novelty.ReasonFirstFrame}
}

func (r *Runner) threshold() float64 {
	if r.opt.Trigger != nil {
		return r.opt.Trigger.Config().Threshold
	}
	return 0
}

// referenceMode is the novelty mode whose trigger is a whole-view event.
const referenceMode = novelty.ModeReference

// EvidenceResult is what the clip step produced.
type EvidenceResult struct {
	StorageRef string
	SHA256     string
	TimeBegin  time.Time
	TimeEnd    time.Time
	Trim       string
	Bytes      int64
}

func (r *Runner) cutAndStore(ctx context.Context, at time.Time) (EvidenceResult, error) {
	from := at.Add(-r.opt.ClipBefore)
	to := at.Add(r.opt.ClipAfter)
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
	id := cutDetectionID(stored.Hash, at)
	if err := r.opt.Store.PutEvidence(ctx, store.Evidence{
		ID: id + "-ev", DetectionID: id,
		SHA256: stored.Hash, StorageRef: ref, Bytes: stored.Bytes, Trim: cut.Trim,
		TimeBegin: from, TimeEnd: to, ReadbackOK: true,
	}); err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{StorageRef: ref, SHA256: stored.Hash, TimeBegin: from, TimeEnd: to, Trim: cut.Trim, Bytes: stored.Bytes}, nil
}

// cutDetectionID is the evidence row's foreign key.
//
// It is derived from the clip's CONTENT rather than from the detection, because
// the evidence table is keyed by detection and a resumed run may cut the same
// clip twice. Two cuts of the same interval produce the same id and therefore one
// row, which is what the unique index on sha256 was already enforcing one level
// up.
func cutDetectionID(sha string, at time.Time) string {
	return "ev-" + store.HashOf([]byte(sha + "|" + at.UTC().Format(time.RFC3339Nano)))[:20]
}

func (r *Runner) enqueueEvent(ctx context.Context, ev TriggerEvent, evidence EvidenceResult, obs vlm.Observation, tag string) error {
	in := xprotect.EventInput{
		EventTypeGUID: r.opt.EventTypeGUID, SourceGUID: r.opt.CameraGUID, At: ev.At, Tag: tag,
		Class: ev.DetectionClass, Value: classify.Value(ev.DetectionClass), Confidence: eventConfidence(ev, obs),
		// The box is the MEASURED box, in the same 0..1000 thousandths
		// `detections.box_*` stores, scaled by the one expression that does it.
		Box:      measuredBox(ev),
		RuleName: ruleNameFor(ev, obs),
		Evidence: xprotect.EvidenceRef{StorageRef: evidence.StorageRef, SHA256: evidence.SHA256, TimeBegin: evidence.TimeBegin.UTC().Format(time.RFC3339), TimeEnd: evidence.TimeEnd.UTC().Format(time.RFC3339),
			Note: "POC evidence ref; the bookmark pushed alongside points at no VMS recording in this build"},
	}
	body, err := xprotect.BuildEvent(in)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(body)
	id := ev.DetectionIDFor(r.opt.Provenance)
	outboxID, err := r.opt.Store.Enqueue(ctx, store.OutboxEntry{
		DetectionID: id, Kind: store.KindEvent,
		Payload: string(payload), Tag: tag,
	}, r.opt.PushMax)
	if err != nil {
		return err
	}
	r.appendLedger(ctx, store.LedgerPushEnqueued, id,
		"kind="+store.KindEvent, "tag="+tag, "outbox="+itoa64(outboxID),
		"box="+boxString(measuredBox(ev)),
		"confidence="+fmt.Sprintf("%.6f", in.Confidence),
		"rule="+in.RuleName)
	r.logf("queued %s push %d for detection %s", store.KindEvent, outboxID, id)
	return nil
}

// enqueueBookmark places the pointer that puts the operator on the recording.
//
// It is the single highest-value line in the pipeline and it is the one that was
// missing: an analytics event with a bounding box tells a VMS operator THAT
// something happened, and a bookmark tells them WHERE to look. The event carries
// no video and this build has no VMS recording to point at, so the bookmark is
// the only thing that connects the pushed record to the footage it is about.
//
// It is queued through the same outbox as the event rather than sent inline,
// because it is an external effect and every external effect in this module is
// recorded before it is attempted.
func (r *Runner) enqueueBookmark(ctx context.Context, ev TriggerEvent, evidence EvidenceResult, obs vlm.Observation, tag string) error {
	if r.opt.BookmarkDevicePath == "" {
		// Said out loud rather than skipped. An event with no bookmark is
		// weaker, not broken, and a run that quietly omits it looks identical to
		// a run that placed one.
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultProviderGone,
			Detail: "no bookmark was placed because no device path is configured (SP_POC_DEVICE_PATH); the pushed event " +
				"carries no pointer to a recording, so an operator has the alert and nowhere to go",
		})
		return nil
	}
	in := xprotect.BookmarkInput{
		Header:        bookmarkHeader(ev, obs),
		DevicePath:    r.opt.BookmarkDevicePath,
		TimeTriggered: ev.At,
		TimeBegin:     evidence.TimeBegin,
		TimeEnd:       evidence.TimeEnd,
		Description:   bookmarkDescription(ev, obs),
		// The reference is the EVENT TAG, not prose. The bookmarks API has no
		// tag field, so the reference IS the idempotency key: it is what the
		// read-back matches on, and a bookmark whose reference is a sentence
		// cannot be found again.
		Reference: tag,
	}
	body, err := xprotect.BuildBookmark(in)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(body)
	id := ev.DetectionIDFor(r.opt.Provenance)
	if _, err := r.opt.Store.Enqueue(ctx, store.OutboxEntry{
		DetectionID: id, Kind: store.KindBookmark,
		Payload: string(payload), Tag: tag,
	}, r.opt.PushMax); err != nil {
		return err
	}
	r.appendLedger(ctx, store.LedgerPushEnqueued, id,
		"kind="+store.KindBookmark, "tag="+tag,
		"device="+r.opt.BookmarkDevicePath,
		"time="+ev.At.UTC().Format(time.RFC3339))
	return nil
}

// measuredBox is the pushed event's region, from the one scaling expression.
func measuredBox(ev TriggerEvent) xprotect.Box {
	top, left, bottom, right := regionThousandths(ev)
	return xprotect.Box{Top: top, Left: left, Bottom: bottom, Right: right}
}

func boxString(b xprotect.Box) string {
	return itoa64(int64(b.Top)) + "," + itoa64(int64(b.Left)) + "," + itoa64(int64(b.Bottom)) + "," + itoa64(int64(b.Right))
}

// eventConfidence is the confidence on the pushed event.
//
// The MODEL's confidence when the model answered, and the measurement's otherwise —
// never a default. A field that is always zero tells a VMS operator the detector
// had no opinion about a detection it had just raised, and a field that is always
// the novelty score claims the model was more certain than it was. So the two are
// distinguished by whether the ask was answered, which is the same predicate
// everything else in this module uses.
func eventConfidence(ev TriggerEvent, obs vlm.Observation) float64 {
	if obs.State == ai.StateAnswered {
		return obs.Confidence
	}
	return ev.Score
}

// ruleNameFor is the rule an Alarm Definition matches on.
//
// It carries the prompt id, so the VMS-side rule that fires for a tamper event is
// a DIFFERENT rule from the one that fires for an alarm event — which is the whole
// claim: two classes, one spine, and the class is what routes them.
func ruleNameFor(ev TriggerEvent, obs vlm.Observation) string {
	name := ev.Line + "-line-cross"
	if ev.PromptID != "" {
		name = ev.PromptID + "-" + ev.Line
	}
	if obs.Verdict == vlm.VerdictNone {
		// The verdict is appended when there is one. A rule named for a verdict
		// that does not exist would be a rule that matched a fabricated negative.
		return name + "-unverified"
	}
	return name + "-" + string(obs.Verdict)
}

func bookmarkHeader(ev TriggerEvent, obs vlm.Observation) string {
	cls := ev.DetectionClass
	if cls == "" {
		cls = "incident"
	}
	return fmt.Sprintf("%s @ %s — %s", cls, ev.At.UTC().Format(time.RFC3339), verdictPhrase(obs))
}

func bookmarkDescription(ev TriggerEvent, obs vlm.Observation) string {
	parts := []string{
		"incident class: " + ev.PromptID,
		"measured region: " + string(ev.RegionSource),
		"novelty score: " + fmt.Sprintf("%.4f", ev.Novelty.Value) + " (threshold " + fmt.Sprintf("%.4f", ev.Score) + ")",
	}
	if obs.Summary != "" {
		parts = append(parts, "model: "+obs.Summary)
	}
	if obs.ModelIdentity != "" {
		parts = append(parts, "model identity (as reported by the API, not verified): "+obs.ModelIdentity)
	}
	if obs.OutputHash != "" {
		parts = append(parts, "output hash: "+obs.OutputHash)
	}
	if !obs.Absence() {
		parts = append(parts, "the bookmark points at the VMS recording of this interval; the clip this verdict is "+
			"about is the POC's own stored evidence and is referenced by the event's evidence field")
	}
	return strings.Join(parts, "; ")
}

// verdictPhrase is the operator-facing sentence for an observation.
//
// An absent answer says so in words rather than printing a word from the closed
// set. "none" next to a bookmark reads as a judgement; "the model was not asked"
// does not.
func verdictPhrase(obs vlm.Observation) string {
	switch {
	case obs.State != ai.StateAnswered:
		return "the model was not asked (" + obs.Reason + ")"
	case obs.Verdict == vlm.VerdictNone:
		return "the model returned no verdict"
	default:
		return string(obs.Verdict)
	}
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
	if r.opt.XProtect == nil {
		return
	}
	// The outbox row carries the exact bytes that were meant to be sent, and
	// they are sent as they are. Reconstructing an EventInput from a stored
	// payload would be a lossy round trip through a Go struct, and the field
	// that quietly did not survive it would be the one the report claims was
	// pushed.
	var out xprotect.Outcome
	var err error
	if e.Kind == store.KindBookmark {
		out, err = r.opt.XProtect.SendBookmarkPayload(ctx, xprotect.PushPayload{Body: []byte(e.Payload), Tag: e.Tag})
	} else {
		out, err = r.opt.XProtect.SendPayload(ctx, xprotect.PushPayload{Body: []byte(e.Payload), Tag: e.Tag})
	}
	switch {
	case err == nil && out.State == "push_sent":
		_ = r.opt.Store.RecordResult(ctx, e.ID, "push_sent", out.Detail, out.RemoteID)
		if e.Kind == store.KindEvent {
			_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushSent, "", "")
			r.pushed++
			r.finish(ctx, e.DetectionID)
		}
		r.appendLedger(ctx, store.LedgerPushResult, e.DetectionID,
			"kind="+e.Kind, "result=push_sent", "remote_id="+out.RemoteID,
			"tag="+e.Tag, "attempts="+itoa64(int64(e.Attempts+1)))
	case err == nil:
		// The push returned something the client is not willing to call sent. The
		// row stays with no result, so AC4 counts it, and the backoff retries.
		_ = r.opt.Store.RecordResult(ctx, e.ID, "", out.Detail, "")
		r.logf("push %d (%s): state=%q %s", e.ID, e.Kind, out.State, out.Detail)
	default:
		var refused *xprotect.RefusedError
		if asRefused(err, &refused) {
			_ = r.opt.Store.RecordResult(ctx, e.ID, "push_refused", refused.Detail, "")
			r.appendLedger(ctx, store.LedgerPushResult, e.DetectionID,
				"kind="+e.Kind, "result=push_refused", "detail="+oneLine(refused.Detail), "tag="+e.Tag)
			if e.Kind == store.KindEvent {
				_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushRefused, "", refused.Detail)
				r.finish(ctx, e.DetectionID)
			}
			return
		}
		var auth *xprotect.ErrAuth
		if asAuth(err, &auth) {
			_ = r.opt.Store.RecordResult(ctx, e.ID, "auth_failed", auth.Error(), "")
			r.appendLedger(ctx, store.LedgerPushResult, e.DetectionID,
				"kind="+e.Kind, "result=auth_failed", "tag="+e.Tag)
			if e.Kind == store.KindEvent {
				_ = r.opt.Store.Transition(ctx, e.DetectionID, store.StatusPushRefused, "", "auth_failed: "+auth.Detail)
				r.finish(ctx, e.DetectionID)
			}
			return
		}
		// Retryable: the row keeps no result and the backoff brings it back —
		// until the budget is spent, at which point the store dead-letters it and
		// the row says so rather than retrying until the run ends.
		_ = r.opt.Store.RecordResult(ctx, e.ID, "", err.Error(), "")
		r.logf("push %d (%s) retryable: %v", e.ID, e.Kind, err)
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
	// The score the MODEL gave, carried onto the crossing at the instant it
	// happened.
	//
	// It used to return a hardcoded 0 with a comment explaining that a missing
	// score must not be invented. The comment was right and the implementation
	// was wrong in the way that matters: `detect.Crossing` now carries the score,
	// and a hardcoded zero on every pushed event told a VMS operator that the
	// detector had no opinion about a detection it had just raised. A field that
	// is always zero is not a conservative default; it is a missing measurement
	// printed as a real one, and it appeared on the screen as `confidence: 0`.
	return c.Confidence()
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
//
// The telemetry and the AI state are arguments rather than something the
// callback has to go and ask for. A hook that could fetch them itself would be a
// hook that can be called with the wrong frame's telemetry attached to it, and a
// picture labelled with the decoder's numbers from a different moment is a lie
// that looks like data.
func (r *Runner) SetFrameHook(fn func(recorder.Frame, []ai.Box, []byte, ai.Telemetry, string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opt.onFrame = func(f recorder.Frame, b []ai.Box, jpg []byte, tel ai.Telemetry, state string) {
		if jpg == nil && r.opt.LastJPEG != nil {
			jpg = r.opt.LastJPEG()
		}
		fn(f, b, jpg, tel, state)
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
