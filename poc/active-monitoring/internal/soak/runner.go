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

	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/detect"
	"github.com/Sergey-Bar/vision-poc/internal/evidence"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision-poc/internal/recorder"
	"github.com/Sergey-Bar/vision-poc/internal/source"
	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/supervisor"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
	"github.com/Sergey-Bar/vision-poc/internal/xprotect"
	"github.com/Sergey-Bar/vision/shared/aistate"
	"github.com/Sergey-Bar/vision/shared/classify"
	"github.com/Sergey-Bar/vision/shared/ortdetect"
	"github.com/Sergey-Bar/vision/shared/sidecar"
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
	Client           *sidecar.Client
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
	// EventTypeGUIDs resolves a blindness window's Analytics Event Type. It is a
	// VALUE and not a pointer to a Config so the runner can be built in a test
	// without one; an empty map is the real world for an account without the rights
	// to register the types, and every blindness is then refused by name rather than
	// pushed as a crossing.
	EventTypeGUIDs config.EventTypeMap
	// EventTypeGUIDFor is the resolution. It is a FUNCTION and not a method on the
	// map because the refusal has to be able to name the ordinary detection type as
	// the alternative for an answered ask, and that value lives on the same Config.
	// One resolver rather than two is what stops a caller reaching for the map
	// directly and skipping the refusal.
	EventTypeGUIDFor func(class, state string) (string, error)
	CameraGUID       string
	PushName         string
	PushIsVMS        bool
	ClockSource      string
	Logf             func(string, ...any)
	// OnFrame is called with each frame for the live screen's overlay. It runs
	// on the pipeline goroutine and must not block.
	OnFrame func(recorder.Frame, []sidecar.Box)
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
	onFrame   func(recorder.Frame, []sidecar.Box, []byte, sidecar.Telemetry, string)
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

	// Trigger is the local novelty engine. Nil in `sidecar` and `detector` modes.
	Trigger Trigger
	// Detector is the in-process ONNX detector. Nil unless TriggerMode is
	// `detector`, and nil in `detector` mode is a refusal at startup rather than a
	// run that finds nothing — see the mode validation below.
	//
	// It is a separate field from Trigger rather than a field on it, for the reason
	// the [Detector] interface documents: the novelty trigger's candidate type
	// carries no class per box, and routing real detections through it would
	// discard the one thing that makes a detection measurable.
	Detector Detector
	// Crop is the crop pass: novelty proposes a region, the detector is run on that
	// region alone at full resolution, and the result is unioned with the whole-frame
	// boxes.
	//
	// It is additive by construction and the whole-frame pass is untouched by it, which
	// is what makes it safe to ship as the default: setting SP_POC_CROP_PASS=false
	// restores the previous behaviour exactly, with no state to unwind.
	//
	// `Proposer` is nil unless the crop pass is enabled, and an enabled pass with no
	// proposer is refused at startup rather than running and finding nothing.
	Crop CropPass
	// DetIdentity is what every decision made from a detected box must name: the
	// model path, its SHA-256, its recorded licence and the input size it ran at.
	// RC-18 is this bundle reaching the database.
	DetIdentity DetIdentity
	// DetDecode is the decode configuration, recorded on every decision beside the
	// identity so a precision number can be traced to the thresholds that produced
	// it.
	DetDecode DetDecode
	// BoxLogFrames bounds the per-frame box log (SP_POC_BOX_LOG_FRAMES). At the cap
	// every crossing frame is kept and every Nth of the rest, and the run records
	// box_log_sampled with both counts.
	BoxLogFrames int
	// AIFPS is SP_POC_AI_FPS, kept here because the footer prints the DECLARED
	// detection rate beside the MEASURED decode rate, and the gap between them is the
	// signature of a detector that cannot keep up with the frames arriving.
	AIFPS int
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
	lat *sidecar.Latencies
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
	// crops is the crop pass's own arithmetic, and it is the only thing in this run that
	// can answer "why did the crop pass find nothing".
	//
	// It exists because a crop pass that stops working does NOT look broken: the
	// whole-frame pass is still finding cars, the run is still clean, and the only
	// symptom is that the smallest objects have gone missing, which is exactly the
	// symptom the whole project was built to fix and exactly the one nobody would
	// notice. Every counter here is a count of something that did NOT happen, and those
	// are the ones that would otherwise be invisible.
	cropsMu sync.Mutex
	crops   CropCounters
	// viewChanges and viewTracksDropped are the view-change arbitration's own totals.
	//
	// They are counted HERE and not read from the detector because the number a report
	// needs is not "how many times the camera moved" but "how many things the pipeline
	// threw away because it moved". A camera moved once and 14 tracks were dropped is a
	// different run from a camera moved once and nothing was dropped, and only the second
	// figure tells an operator what their event stream just did.
	viewMu            sync.Mutex
	viewChanges       int
	viewTracksDropped int
	// attribution is the RC-18 bundle for the decision currently being recorded.
	//
	// It is a field rather than a parameter because `handleIncident` reaches
	// `decisionFrom` through several layers, and threading it through each of them
	// would be a change to code that has nothing to do with model attribution. It
	// is set immediately before the call and read immediately inside it, on the one
	// frame loop goroutine, and the runner's own mutex makes that safe if a second
	// consumer is ever added.
	// start is when the run began, for the observed duration the footer prints.
	//
	// It is NOT the declared target: a rate computed against a DECLARED duration is a
	// rate against a plan.
	start       time.Time
	attribution Attribution
	// refusalCount and refusalTotal drive the refusal log's rate limit: the first
	// refusal, then every hundredth, so a misconfigured model does not write 9000
	// identical lines and bury everything else.
	refusalCount int
	refusalTotal int
	// boxLog is the bounded per-frame box log, and boxLogSampled says whether it is
	// currently discarding frames. See boxlog.go for why the bound is a sampler and
	// why crossings are exempt from it.
	boxLog         *BoxLog
	boxLogSampled  bool
	boxLogReported bool
	// absence is the open blindness windows, one per absence state. See absence.go.
	absence *absenceLedger
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

// CropTally is the run's crop-gate arithmetic, as the report needs it.
//
// It exists because `r.crops` was accumulated under a lock on every frame and read
// by nothing at all. The gate's own comment says `GatedOut` is "the counter that
// says whether the rate is too low", and a counter nobody prints is a comment about
// a measurement rather than a measurement — so the one number that says whether the
// camera is looking often enough was in memory and nowhere else for the whole life
// of this code.
//
// It is reported beside the detector's tally rather than inside it, because the two
// answer different questions: the detector's is "what did the model see", this one
// is "when did we decide not to look, and how often". Folding them together would
// let one hide the other, and the second is the one an operator has never had a
// number for.
func (r *Runner) CropTally() CropTally {
	r.cropsMu.Lock()
	defer r.cropsMu.Unlock()
	c := r.crops
	// Seen is DERIVED and not defaulted, and this is the difference between the
	// section telling the truth about a run and printing NOT RUN over one that
	// ran.
	//
	// The first version of this accessor left Seen at its zero value and the demo
	// printed "**NOT RUN.** The crop pass was never offered a frame" over a run in
	// which the crop pass had been offered eight frames and the detector had
	// refused eight crops. That is the specific defect this whole pass exists to
	// remove, reproduced by its own new code: a counter that read as measured and
	// was not. A struct field defaults to false; a claim like this one has to be
	// computed from the thing it is a claim about.
	seen := c.Proposed+c.SkippedOverlap+c.SkippedSmall+c.SkippedCap+c.GatedOut+c.Inferred+c.Refusals > 0
	// OfferedFrames is the frame count to report: the crop pass's own frame
	// count, which is the number of frames it was CALLED on. The runner calls it
	// once per frame the detector answered, so it is that and not the sum of
	// outcomes — a frame that produced nothing is still a frame it looked at.
	offeredFrames := c.Proposed + c.SkippedOverlap + c.SkippedSmall + c.SkippedCap + c.GatedOut
	if offeredFrames == 0 && c.Refusals > 0 {
		// The detector refused every crop the pass proposed, which is what a
		// degraded detector looks like from here: the pass ran, proposed, and was
		// refused. Counting it as zero frames would print NOT RUN over a run that
		// ran — see the Seen comment above for what that costs.
		offeredFrames = c.Refusals
	}
	t := CropTally{
		Seen:           seen,
		Frames:         offeredFrames,
		Proposed:       c.Proposed,
		Inferred:       c.Inferred,
		SkippedOverlap: c.SkippedOverlap,
		SkippedSmall:   c.SkippedSmall,
		SkippedCap:     c.SkippedCap,
		GatedOut:       c.GatedOut,
		ViewChanges:    c.ViewChanges,
		ViewSettling:   c.ViewSettling,
		BoxesFound:     c.BoxesFound,
		BoxesKept:      c.BoxesKept,
		Refusals:       c.Refusals,
		DedupedAway:    c.DedupedAway,
	}
	// The share is over what the GATE was offered, not over the frame count: a
	// frame refused by the detector before the gate is not a frame the gate
	// declined, and counting it would understate the share.
	if offered := c.Proposed + c.GatedOut; offered > 0 {
		t.GatedOutFrac, t.ShareMeasured = float64(c.GatedOut)/float64(offered), true
	}
	// The frames the crop pass was actually OFFERED, which is what the gated-out
	// share is over. A frame refused before the gate is not a frame the gate
	// declined, so folding the refusals into the denominator would understate it.
	if offered := c.Proposed + c.GatedOut; offered > 0 {
		t.GatedOutFrac, t.ShareMeasured = float64(c.GatedOut)/float64(offered), true
	}
	// The share is the number a reader acts on and the one a count cannot give
	// them: 40 gated-out frames means nothing on its own, and 40 out of 42 is a
	// camera that is not looking most of the time.
	if offered := c.Proposed + c.GatedOut; offered > 0 {
		t.GatedOutFrac, t.ShareMeasured = float64(c.GatedOut)/float64(offered), true
	}
	if c.Proposed > 0 {
		t.SmallShareFrac, t.SmallMeasured = float64(c.SkippedSmall)/float64(c.Proposed), true
	}
	if c.BoxesFound > 0 {
		t.KeptShareFrac, t.KeptMeasured = float64(c.BoxesKept)/float64(c.BoxesFound), true
	}
	return t
}

// CropTally is the crop gate's arithmetic over a run. See Runner.CropTally.
type CropTally struct {
	Seen bool
	// Frames is every frame the crop pass was offered, however it ended.
	Frames int
	// Proposed, SkippedOverlap, SkippedSmall and SkippedCap are the four ways a
	// proposed crop can fail to be taken, and their sum is the total offered
	// minus the ones the gate had already refused.
	Proposed       int
	Inferred       int
	SkippedOverlap int
	SkippedSmall   int
	SkippedCap     int
	// GatedOut is how many frames the novelty RATE gate did not run the pass on.
	// See CropCounters.GatedOut for why this is the counter that matters.
	GatedOut int
	// GatedOutFrac is GatedOut over (Proposed + GatedOut), the frames that were
	// actually offered. Measured is false when nothing was offered, and a
	// report must print NOT RUN for that rather than 0.
	GatedOutFrac   float64
	ShareMeasured  bool
	ViewChanges    int
	ViewSettling   int
	BoxesFound     int
	BoxesKept      int
	Refusals       int
	DedupedAway    int
	SmallShareFrac float64
	SmallMeasured  bool
	KeptShareFrac  float64
	KeptMeasured   bool
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
	// `detector` mode with no detector is REFUSED, and this is the last place it can
	// be refused cheaply. Left as it is, the frame loop falls through to
	// `measureTrigger`, which returns nothing because there is no trigger either,
	// and the run reports an empty road at full health: the exact defect this
	// project exists to end, and the one the previous build shipped.
	if UsesDetector(opt.TriggerMode) && opt.Detector == nil {
		return nil, fmt.Errorf("soak: SP_POC_TRIGGER_MODE=%s but no detector was wired into the "+
			"runner, so every frame would be decoded into nothing and the run would report a "+
			"detection rate of zero over a road with traffic on it", TriggerDetector)
	}
	// The same for the sidecar, which has always been implied rather than stated.
	if UsesSidecar(opt.TriggerMode) && opt.Client == nil {
		return nil, fmt.Errorf("soak: SP_POC_TRIGGER_MODE=%s but no sidecar client was wired into "+
			"the runner, so every frame would be answered `not_asked` and a run of it would look "+
			"like a camera fault", TriggerSidecar)
	}
	return &Runner{opt: opt, log: opt.Logf, lat: sidecar.NewLatencies(200000), stopPush: make(chan struct{}),
		boxLog: NewBoxLog(opt.BoxLogFrames), absence: newAbsenceLedger(), cpu: NewCPUSampler(func() []int {
			if opt.ChildPIDs == nil {
				return nil
			}
			return opt.ChildPIDs()
		})}, nil
}

// BoxLogStats exposes the per-frame box log's arithmetic, so the run record and the
// report can say whether this run's replay reads a COMPLETE log or a sampled one.
//
// It is exported on the runner rather than reached through a field because a
// reader that can see the counters can also see that they exist, and a reader who
// does not know the log is bounded cannot ask whether it was.
func (r *Runner) BoxLogStats() BoxLogStats { return r.boxLog.Stats() }

// BoxLogSampled reports whether the per-frame box log dropped anything.
func (r *Runner) BoxLogSampled() bool { return r.boxLog.Sampled() }

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
	// The start time, for the OBSERVED duration the footer prints. It is set here
	// rather than in NewRunner because a run that is constructed and then waits on a
	// camera is not yet running, and a duration measured from construction would
	// include the waiting.
	r.start = time.Now()

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

// Trigger modes. `detector` is the shipped path: an in-process ONNX detector at
// the frame rate. `novelty` is the whole-frame change trigger it replaced, kept
// because it is licence-free and needs no weights. `sidecar` is the per-frame
// Python classifier, kept as the calibration cross-check that earns its place.
//
// These are ALIASES of the constants in `internal/config`, which owns
// SP_POC_TRIGGER_MODE and is therefore the package that owns the vocabulary.
// They are declared here because `runner.go` is where the switch that dispatches
// on them lives, and a reader should not have to open another file to see the
// three cases of that switch.
const (
	// TriggerDetector is the in-process ONNX Runtime detector. It produces real
	// classes and real scores per frame, which is what makes a precision number
	// possible at all: the two most recent real runs approved 6,578 and 297 boxes
	// and recorded zero vehicles, zero people and zero animals, because a
	// whole-frame change-fraction has no vocabulary.
	TriggerDetector = config.TriggerModeDetector
	// TriggerNovelty is the local novelty engine: measured regions, one model
	// call per event, licence-free.
	TriggerNovelty = config.TriggerModeNovelty
	// TriggerSidecar is the per-frame Python ONNX sidecar. It is a measurement
	// tool now: useful for choosing a threshold and for the cross-decoder parity
	// test, wrong for the shipped trigger path because it costs a process and a
	// socket round trip per frame.
	TriggerSidecar = config.TriggerModeSidecar
)

// ValidTriggerMode refuses a mode this build does not operate.
func ValidTriggerMode(m string) bool { return config.ValidTriggerMode(m) }

// UsesSidecar reports whether the run's boxes come from the per-frame sidecar.
//
// Every sidecar precondition is gated on this one predicate, because three
// separate conditionals is three places where the gate was left hard-on and the
// ONNX model was required by a run that never called it.
func UsesSidecar(m string) bool { return m == TriggerSidecar }

// UsesDetector reports whether the run's boxes come from the in-process detector.
func UsesDetector(m string) bool { return m == TriggerDetector }

// DetIdentity is what a decision made from a detected box must name.
//
// It is the RC-18 bundle. A detection rate is a CLAIM about a specific file run
// with specific settings, and a claim that cannot name its weights describes a
// build nobody can reconstruct.
//
// Every field is a POINTER where it reaches the store row, because a `motion`-mode
// decision legitimately has none of them and NULL is that fact — where NOT NULL
// DEFAULT ” would be a fabricated measurement, saying a model was involved and
// produced nothing.
type DetIdentity struct {
	ModelPath string
	SHA256    string
	Licence   string
	InputSize int
	// Threads is recorded beside every latency number, because a p99 without its
	// thread count describes no build: ORT otherwise picks from the machine's core
	// count, so the same run differs between a four-core laptop and a server.
	Threads int
	// RuntimeVersion is whatever the loaded runtime reported, when it reported
	// anything. It is a claim about the LIBRARY, not about the weights.
	RuntimeVersion string
}

// DetDecode is the decode configuration a decision was made under.
//
// Recorded per decision rather than once per run, because a run's configuration is
// read at startup and a decision is written later — and a decision that cannot name
// the thresholds under which it was made is a threshold chosen by nobody.
type DetDecode struct {
	NMSClass   float64
	NMSGlobal  float64
	EmitScore  float64
	MaxBoxes   int
	MinBoxArea float64
}

// process handles one frame: measure, decide, and for each incident record first
// and then act.
func (r *Runner) process(ctx context.Context, f recorder.Frame) {
	r.mu.Lock()
	hook := r.opt.onFrame
	r.mu.Unlock()

	var boxes []sidecar.Box
	// cutBoxes is the crop pass's own contribution to this frame. It travels beside the
	// union rather than inside it because three consumers need to know which box came
	// from where: the source-aware track minimum, the stored `region_source`, and the
	// report's crop-pass block.
	var cutBoxes []sidecar.Box
	telemetry := sidecar.Telemetry{}
	// aiState is the four-word vocabulary state, and it defaults to `not_asked`
	// — the honest reading when no model is configured at all, rather than an
	// empty string that the screen would render as "fine".
	aiState := sidecar.StateNotAsked
	// detRefusal is this frame's named failure, if the detector had one. It is kept
	// apart from telemetry because a refusal is not telemetry: it is the reason the
	// answer is missing, and it has to reach the log and the screen as its own
	// thing rather than as a count.
	var detRefusal *ortdetect.Refusal
	// detLatency and detLatPercentiles are this frame's cost and the distribution
	// so far, both of which reach the decision record.
	detLatency := 0.0
	var detP50, detP95, detP99 *float64

	switch {
	case r.opt.Detector != nil:
		boxes, cutBoxes, aiState, telemetry, detRefusal, detLatency, detP50, detP95, detP99 = r.runDetector(f)
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
		// The crop pass's boxes are handed over BY VALUE and not re-derived from a
		// rounded region. Answer.Cut is a membership set over the same boxes the engine
		// is about to be given, and anything derived here would be a second answer to
		// "which box was magnified" that could disagree with the first.
		Cut: toDetect(cutBoxes),
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
		// The REAL state, from `sidecar.Interpret` — never a hardcoded `answered`.
		// The screen used to print `answered` for every frame including the ones
		// that were never sent, and `answered` is not in CONTRACT.md §2.2's
		// four-word vocabulary at all: it is a fifth word that reads as
		// reassurance and means nothing.
		hook(f, boxes, nil, telemetry, aiState)
	}
	// A detector refusal is LOGGED, once per frame, with its name. It is not folded
	// into a count because a count is unactionable: a run that says "14 degraded
	// frames" sends its reader to a profiler, and a run that logs the reason each
	// time sends them to the line that says it.
	//
	// Logged at most a few times a second rather than every frame, because a
	// misconfigured model produces the same refusal on all 9000 frames of a
	// clip and 9000 identical lines is how a real signal gets lost in a log.
	if detRefusal != nil {
		r.noteRefusal(f.Seq, detRefusal)
	}
	// The per-frame box log. It is written for EVERY measured frame, not only the
	// ones that produced a crossing, because a frame with no boxes is what tells a
	// replay "the model saw nothing" as distinct from "the pipeline never asked" —
	// and that distinction is the defect class this repository has shipped twice.
	//
	// The sampling rule is applied here rather than inside the store because it is
	// a property of the RUN (SP_POC_BOX_LOG_FRAMES) and not of the database, and
	// because it is testable without one.
	if r.boxLog != nil && f.Seq >= 0 {
		keep, because := r.boxLog.ShouldKeep(len(crossings))
		if keep {
			obs := boxObservationsFor(r.opt.RunID, int64(f.Seq), f.At, boxes, telemetry,
				tracks, SourceFor(r.opt.TriggerMode))
			r.writeBoxObservations(ctx, obs)
			if r.boxLogSampled && len(crossings) > 0 {
				r.logf("frame %d kept in the box log: %s", f.Seq, because)
			}
		} else if !r.boxLogReported {
			// The FIRST sampled-out frame says so once, because a log that goes
			// silent without saying so is a log a reader assumes is complete.
			r.logf("the per-frame box log has reached its cap of %d frames; further frames are "+
				"sampled and this run's replay reads a sampled log. Crossings are never dropped.", r.boxLog.Cap)
			r.boxLogReported = true
		}
	}
	// The measurement's state is recorded with the detection, so the report can say
	// whether a crossing came from a frame something actually measured.
	for _, c := range crossings {
		ev := toTriggerEvent(c, r.defaultPromptID(), r.triggerScore())
		r.attribution = r.nextAttribution(ev, aiState, detLatency, detP50, detP95, detP99, detRefusal)
		r.handleIncident(ctx, ev, aiState, f.Seq)
	}
	_ = tracks

	// A reference trigger's sustained event is NOT a line crossing: the whole view
	// changed, so there is no line to cross and no side to come from. It is handled
	// here, on the same spine — record, evidence, ask, push, bookmark — and the only
	// thing that differs is which arithmetic produced the trigger.
	r.handleSentinels(ctx, f)
}

// runDetector measures one frame with the in-process ONNX detector.
//
// It exists as a function rather than as three lines inline in `process` because of
// what it has to guarantee: that the CLASS the model produced survives all the way
// to the tracker. That is the whole reason the detector has its own interface —
// `measureTrigger` assigns one configured class per frame, which is right for a
// change-fraction and would discard every class a model finds.
//
// The EIGHTH thing it returns is the set of boxes the crop pass produced, kept SEPARATE
// from the union rather than merged into it. Two reasons, both about the truth of the
// record: the engine holds a source-aware track minimum that applies a lower bar to a
// magnified sighting, so it has to know which boxes were magnified; and `region_source`
// is a stored column, so a later reader has to be able to ask "was this found in a crop"
// without re-deriving it from a rounded rectangle.
//
// The whole-frame pass runs FIRST and is not conditional on anything. It finds cars and
// people, it costs one inference, and nothing about the crop pass changes it, which is
// what makes the crop pass additive and the rollback one environment variable rather
// than a code change.
func (r *Runner) runDetector(f recorder.Frame) (boxes []sidecar.Box, cuts []sidecar.Box, state string, tel sidecar.Telemetry,
	refusal *ortdetect.Refusal, latencyMS float64, p50, p95, p99 *float64) {

	ans := r.opt.Detector.Detect(f.Seq, f.RGB, f.Width, f.Height, ortdetect.Rect{})
	state = ans.State
	latencyMS = ans.LatencyMS
	refusal = ans.Refusal
	tel = ans.Telemetry.ToAITelemetry()
	r.lat.Add(time.Duration(ans.LatencyMS * float64(time.Millisecond)))

	// The percentiles come from the DETECTOR, not from here. The runner's own `lat` is a
	// running figure over every measured frame; the throughput gate needs the detector's
	// own p99 over its own bounded window, and a p99 cannot be computed from a mean at
	// all.
	p50, p95, p99 = percentilesFor(r.opt.Detector)

	out := make([]sidecar.Box, 0, len(ans.Boxes))
	for _, b := range ans.Boxes {
		if !b.Valid() {
			// The decoder already refuses an invalid box, so reaching here means
			// something between the decoder and here changed one. Dropping it is
			// right; logging it is also right, because it is a bug in this module
			// rather than in the model.
			r.logf("frame %d: the detector produced a box this build refuses to pass on: %+v", f.Seq, b)
			continue
		}
		// The class goes through UNCHANGED. Every field of the box is the model's own,
		// in the model's own coordinates, and the tracker reads them without rescaling,
		// which is why this is a copy of the struct rather than a reconstruction field
		// by field. A reconstruction is a second place for a coordinate to be rescaled.
		out = append(out, sidecar.Box(b))
	}

	// The crop pass. By construction the union is a SUPERSET of the whole-frame boxes:
	// rule 1 in crop.go skips any region a whole-frame box already covers, so the two
	// passes cannot both report the same object unless their boxes differ by more than
	// an IoU of 0.5.
	cuts, ct := r.cropPass(f, out)
	boxes, kept, deduped := unionBoxes(out, cuts)
	ct.BoxesKept = kept
	ct.DedupedAway = deduped
	r.cropsMu.Lock()
	r.crops = r.crops.Add(ct)
	r.cropsMu.Unlock()

	if len(boxes) == 0 {
		// `no_objects` and `degraded` both land here and both are correct: the STATE
		// carries the difference and the boxes are empty either way. What must not
		// happen is the boxes being empty for a third reason and the state saying
		// `asked_answered`.
		return nil, cuts, state, tel, refusal, latencyMS, p50, p95, p99
	}
	return boxes, cuts, state, tel, refusal, latencyMS, p50, p95, p99
}

// cropPass runs the crop proposals and the inferences they justify.
//
// It is a method rather than a free function so the counters land on the runner and the
// refusals go through the same rate-limited logger the whole-frame pass uses: a
// misconfigured crop is the same kind of fault as a misconfigured detector, and logging
// it by a second path would produce 9,000 lines a second at 25 fps.
func (r *Runner) cropPass(f recorder.Frame, whole []sidecar.Box) ([]sidecar.Box, CropCounters) {
	cp := r.opt.Crop
	if !cp.Enabled || cp.Proposer == nil {
		return nil, CropCounters{}
	}
	regions, ct := proposeCrops(frameSource{
		Seq: f.Seq, RGB: f.RGB, Width: f.Width, Height: f.Height, AtNS: f.At.UnixNano(),
	}, whole, cp)
	// The view-change arbitration, applied to the TRACKER.
	//
	// The crop gate already dropped its regions for a moved frame. The tracker did not,
	// and that is where the flood would come from: every box in the panned view falls
	// inside some old track's neighbourhood and the association threshold would match
	// them, so one camera move becomes fifty tracks and fifty presence events.
	//
	// So the tracks go, the count comes back, and presence is gated until the view
	// settles. It does not duplicate the camera-integrity event: the rule is that ONE
	// thing is said about the camera and nothing else is said about the scene.
	//
	// `Engine` is nil in a trigger-mode run that declared no engine, and a nil check
	// here rather than an assumption, because the arbitration is about the TRACKER and
	// there is no tracker to arbitrate.
	switch ct.ViewVerdict {
	case novelty.ViewChanged:
		dropped := 0
		if r.opt.Engine != nil {
			dropped = r.opt.Engine.InvalidateAll()
			r.opt.Engine.SetSuppressed(true)
		}
		r.viewMu.Lock()
		r.viewChanges++
		r.viewTracksDropped += dropped
		r.viewMu.Unlock()
		r.logf("frame %d: the view CHANGED. %d live track(s) invalidated, and presence events are "+
			"suppressed until the view settles. One thing is said about the camera; the silence "+
			"about the scene is an absence of findings and not a claim that nothing happened",
			f.Seq, dropped)
	case novelty.ViewSettling:
		if r.opt.Engine != nil {
			r.opt.Engine.SetSuppressed(true)
		}
	default:
		if r.opt.Engine != nil {
			r.opt.Engine.SetSuppressed(false)
		}
	}
	if len(regions) == 0 {
		return nil, ct
	}
	out := make([]sidecar.Box, 0, len(regions))
	for _, reg := range regions {
		ans := r.opt.Detector.Detect(f.Seq, f.RGB, f.Width, f.Height, reg.rect)
		r.lat.Add(time.Duration(ans.LatencyMS * float64(time.Millisecond)))
		// A refusal on ONE crop is not a refusal of the frame. The whole-frame boxes are
		// already measured and already committed to this frame's answer, and discarding
		// them because a crop failed would turn a partial success into a total one. So it
		// is counted and named, and the boxes kept.
		if ans.Refusal != nil {
			ct.Refusals++
			r.noteRefusal(f.Seq, ans.Refusal)
			continue
		}
		for _, b := range ans.Boxes {
			if !b.Valid() {
				r.logf("frame %d: the crop pass at %d,%d %dx%d produced a box this build refuses to pass on: %+v",
					f.Seq, reg.rect.X, reg.rect.Y, reg.rect.W, reg.rect.H, b)
				continue
			}
			out = append(out, sidecar.Box(b))
		}
	}
	ct.BoxesFound = len(out)
	return out, ct
}

// noteRefusal logs a detector refusal by name, no more than a few times a second.
//
// The rate limit is the point rather than a courtesy. A misconfigured model
// produces the SAME refusal on every one of a clip's 9000 frames, and 9000 identical
// lines is how one real signal gets lost in a log. The first occurrence and then
// every hundredth is enough to see a refusal that persists and to notice when it
// stops, without filling the log with it.
func (r *Runner) noteRefusal(seq uint64, refusal *ortdetect.Refusal) {
	r.mu.Lock()
	if r.refusalCount == 0 || r.refusalCount%100 == 1 {
		r.mu.Unlock()
		r.logf("frame %d: the detector refused to answer: %s. (%d frames refused so far; every 100th is logged)",
			seq, refusal.What, r.refusalTotal)
		r.mu.Lock()
	}
	r.refusalCount++
	r.refusalTotal++
	n := r.refusalTotal
	r.mu.Unlock()
	_ = n
}

// nextAttribution builds the RC-18 bundle for a decision made from a detected box.
//
// It is a method rather than a free function because it reads the run's identity,
// and returning a `store.Decision`-shaped value from here would be two types for the
// same thing. What it returns is the FIELD SET, and `decisionFrom` puts it on the
// row.
//
// Every field is a POINTER or nil. A `motion`-mode decision passes through here
// with no detector, and its attribution is entirely nil — which is the honest
// record that no model was involved, and is why those columns are nullable.
func (r *Runner) nextAttribution(ev TriggerEvent, state string, latencyMS float64,
	p50, p95, p99 *float64, refusal *ortdetect.Refusal) Attribution {

	var a Attribution
	if !UsesDetector(r.opt.TriggerMode) || r.opt.Detector == nil {
		return a
	}
	id := r.opt.DetIdentity
	// The identity is copied into POINTERS so the store row can carry NULL for the
	// fields that do not apply, and so a caller cannot mutate the run's identity
	// after a decision has been written from it.
	if id.ModelPath != "" {
		p := id.ModelPath
		a.ModelIdentity = &p
	}
	if id.SHA256 != "" {
		s := id.SHA256
		a.ModelSHA256 = &s
	}
	if id.Licence != "" {
		l := id.Licence
		a.ModelLicence = &l
	}
	if id.InputSize > 0 {
		sz := id.InputSize
		a.InputSize = &sz
	}
	if latencyMS > 0 {
		ms := latencyMS
		a.InferMS = &ms
	}
	a.DetLatencyP50MS, a.DetLatencyP95MS, a.DetLatencyP99MS = p50, p95, p99
	if ev.TrackID >= 0 {
		tid := ev.TrackID
		a.TrackID = &tid
	}
	dec := r.opt.DetDecode
	if dec.NMSClass > 0 {
		v := dec.NMSClass
		a.NMSClassIOU = &v
	}
	if dec.NMSGlobal > 0 {
		v := dec.NMSGlobal
		a.NMSGlobalIOU = &v
	}
	// The measured state goes on the row beside the identity, and a decision whose
	// frame was NOT measured says so. `asked_answered` is the only state that means
	// a model produced this box.
	a.MeasuredState = state
	if refusal != nil {
		a.Refusal = refusal.What
	}
	return a
}

// Attribution is what a decision made from a detected box carries beside its own
// arithmetic: which model, which thresholds, what it cost, and whether the frame it
// came from was actually measured.
//
// It is a separate type from `store.Decision` because it is the SAME information
// seen from the runner's side, and one type per side keeps each package from
// importing the other's structure.
type Attribution struct {
	ModelIdentity   *string
	ModelSHA256     *string
	ModelLicence    *string
	InputSize       *int
	NMSClassIOU     *float64
	NMSGlobalIOU    *float64
	InferMS         *float64
	DetLatencyP50MS *float64
	DetLatencyP95MS *float64
	DetLatencyP99MS *float64
	TrackID         *int
	// MeasuredState is the four-word state of the frame this decision came from,
	// and Refusal is the NAME of the failure if there was one. Both are on the row
	// so a reader can tell a decision made from a measured frame from one made from
	// a frame the model was asked about and could not answer.
	MeasuredState string
	Refusal       string
}

// measureTrigger runs the local novelty engine over one frame.
//
// It returns `asked_answered` when the engine produced something and
// `no_objects` when it did not, and it never returns a box it did not measure. A
// novelty engine has no opinion about whether a change is an incident, so the most
// it can honestly report is that it measured something.
func (r *Runner) measureTrigger(f recorder.Frame) ([]sidecar.Box, string) {
	if len(f.RGB) == 0 || f.Width <= 0 || f.Height <= 0 {
		r.logf("frame %d: no pixels; the trigger measured nothing and this is not a zero", f.Seq)
		return nil, sidecar.StateNotAsked
	}
	cands, _ := r.opt.Trigger.Candidates(f.RGB, f.Width, f.Height)
	out := make([]sidecar.Box, 0, len(cands))
	for _, c := range cands {
		if !c.Box.NonEmpty() {
			// An empty region is not a proposal. Passing it on would put an
			// all-zero box on a pushed event, which is the defect RC-6 exists for.
			continue
		}
		out = append(out, sidecar.Box{
			Class: r.triggerClass(), Score: c.Score,
			X1: c.Box.X1, Y1: c.Box.Y1, X2: c.Box.X2, Y2: c.Box.Y2,
		})
	}
	if len(out) == 0 {
		return nil, sidecar.StateNoObjects
	}
	return out, sidecar.StateAnswered
}

// askSidecar is the older per-frame path, kept as a measurement tool.
func (r *Runner) askSidecar(ctx context.Context, f recorder.Frame) ([]sidecar.Box, sidecar.Telemetry, string) {
	if r.opt.Client == nil {
		r.logf("frame %d: not_asked — no sidecar client is configured", f.Seq)
		return nil, sidecar.Telemetry{}, sidecar.StateNotAsked
	}
	res := r.opt.Client.Detect(ctx, f.Seq, f.RGB, f.Width, f.Height)
	r.lat.Add(res.Latency)
	r.mu.Lock()
	r.lastAIState = res.State
	r.mu.Unlock()
	switch res.State {
	case sidecar.StateDegraded, sidecar.StateCannotTell:
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
	case sidecar.StateNotAsked:
		// A frame that was never sent is not a failure of the model, and it is
		// not an empty scene. It is counted as not_asked and nothing else.
	}
	boxes := make([]sidecar.Box, 0, len(res.Boxes))
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
	r.handleIncident(ctx, ev, sidecar.StateAnswered, f.Seq)
}

func toDetect(boxes []sidecar.Box) []detect.Box {
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
	//
	// The gate is EITHER producer, not just the novelty trigger. It used to be
	// `Trigger != nil` alone, and in `detector` mode the trigger is nil — so every
	// detector incident would have been recorded as a detection with NO decision row
	// behind it. That is worse than not recording it: `detect.Detection` with no
	// `store.Decision` is an alert nobody can replay and nobody can check, and RC-4
	// and RC-20 both read that row. The detector path is exactly where replay
	// matters most, because the crossing box is Kalman-derived.
	switch {
	case r.opt.Trigger != nil:
		d := decisionFrom(ev, r.opt.RunID, r.opt.Trigger.Config(), frameSeq, r.opt.ReferenceSHA,
			r.opt.TriggerMode, r.attribution)
		d.DetectionID = id
		if err := r.opt.Store.PutDecision(ctx, d); err != nil {
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: ev.At, Reason: store.FaultDBUnavailable,
				Detail: "the decision inputs could not be recorded, so this incident cannot be replayed from facts: " + err.Error(),
			})
		}
	case r.opt.Detector != nil:
		// In detector mode there is no novelty engine and therefore no
		// novelty.Config. The thresholds on the row are the DETECTOR's, not the
		// engine's, and the class on the row is the model's own — which is the
		// whole point of this path.
		attr := r.attribution
		d := decisionFrom(ev, r.opt.RunID, novelty.Config{}, frameSeq, r.opt.ReferenceSHA,
			TriggerDetector, attr)
		d.DetectionID = id
		if err := r.opt.Store.PutDecision(ctx, d); err != nil {
			// A refusal here is almost always RC-18: a detector-mode decision that
			// named no model. That is refused ON PURPOSE by store.PutDecision, and
			// recording it as a generic database fault would bury the reason.
			r.logf("frame %d: the detector's decision for %s was NOT recorded: %v", frameSeq, id, err)
			_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
				At: ev.At, Reason: store.FaultDecisionNotReplayable,
				Detail: "a detector-mode decision naming no model was refused, so this incident " +
					"cannot be replayed from facts: " + err.Error(),
			})
		}
	}

	// Step 3: the picture of the decision. Best-effort and non-fatal — see
	// storeSnapshot.
	r.storeSnapshot(ctx, id, ev, aiState, frameSeq)

	// Step 4: the clip.
	ref, clipErr := r.cutAndStore(ctx, id, ev.At)
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

	// Step 8: the blindness, if there was one. It is a SEPARATE effect with its own
	// Analytics Event Type, and it comes last because everything above is about this
	// one detection while this is about the window.
	//
	// It runs after the detection's own event has been queued, deliberately. The
	// detection happened — a car crossed the line — and the missing thing is the
	// VERDICT. Enqueueing the blindness first would make an inability to type it look
	// like a reason not to push the event the VMS can actually draw.
	r.enqueueAbsence(ctx, ev, obs)
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
		obs := vlm.Observation{DetectionID: id, State: sidecar.StateNotAsked}
		obs.SetState(sidecar.StateNotAsked)
		obs.Finalise()
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	if clipErr != nil {
		// There is no clip to send. We did not ask, and the reason is a recorded
		// clip failure rather than a silent skip.
		obs := vlm.Observation{DetectionID: id, State: sidecar.StateNotAsked, Message: clipErr.Error()}
		obs.SetState(sidecar.StateNotAsked)
		obs.Finalise()
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	clip, err := readEvidenceClip(r.opt.Evidence, ref.SHA256)
	if err != nil {
		obs := vlm.Observation{DetectionID: id, State: sidecar.StateNotAsked, Message: err.Error()}
		obs.SetState(sidecar.StateNotAsked)
		obs.Finalise()
		_ = r.opt.Store.RecordFault(ctx, r.opt.RunID, store.Fault{
			At: ev.At, Reason: store.FaultClipRefused,
			Detail: "the stored clip could not be read back for the model, so the model was not asked: " + err.Error(),
		})
		r.recordObservation(ctx, id, ev, obs, "")
		return obs
	}
	obs := r.opt.Observer.Observe(ctx, id, ev.PromptID, ev.DetectionClass, clip, "video/mp4")
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

// cutAndStore cuts the clip around `at` and records it against `detectionID`.
//
// `detectionID` is the detection's OWN id, passed in by the caller. It used to be
// minted here — `cutDetectionID(sha, at)` — and that was wrong in a way no test
// caught: the evidence table's `detection_id` is a FOREIGN KEY, and a freshly minted
// `ev-<hash>` names a row that does not exist. Every `PutEvidence` therefore failed
// with `FOREIGN KEY constraint failed`, the clip was recorded as `clip_refused`, and
// **AC3 — evidence intact — could never pass in any run, ever.** The run carried on,
// because a refused clip is a legitimate outcome, and printed ten detections and zero
// stored evidence with no other symptom.
//
// The evidence row's `id` is derived from the detection id and the clip hash, so two
// clips for one detection get two rows and one clip for two detections cannot.
func (r *Runner) cutAndStore(ctx context.Context, detectionID string, at time.Time) (EvidenceResult, error) {
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
	if err := r.opt.Store.PutEvidence(ctx, store.Evidence{
		ID: evidenceID(detectionID, stored.Hash), DetectionID: detectionID,
		SHA256: stored.Hash, StorageRef: ref, Bytes: stored.Bytes, Trim: cut.Trim,
		TimeBegin: from, TimeEnd: to, ReadbackOK: true,
	}); err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{StorageRef: ref, SHA256: stored.Hash, TimeBegin: from, TimeEnd: to, Trim: cut.Trim, Bytes: stored.Bytes}, nil
}

// evidenceID is the evidence ROW's own primary key: the detection it belongs to plus
// the bytes it proves. The detection id is the foreign key and the detection's own
// id; the two must not be confused, and confusing them is what made AC3 unreachable.
func evidenceID(detectionID, sha string) string {
	return "ev-" + store.HashOf([]byte(detectionID + "|" + sha))[:20]
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
		// R8. The observation IS the attribution: there is no other source in this
		// run for who answered, under which contract, or with what output. A caller
		// that forgot this line would push a sentence with nothing beside it, and
		// BuildEvent now refuses rather than letting it through.
		Provenance: provenanceOf(obs),
	}
	body, err := xprotect.BuildEvent(in)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(body)
	id := ev.DetectionIDFor(r.opt.Provenance)
	// The outbox's tag is the tag that was ACTUALLY pushed, which is `body.Tag`: it
	// carries the provenance suffix that BuildEvent appends, and the push worker's
	// read-back matches on this column. Enqueuing the pre-suffix tag would make every
	// read-back search for an event that was never sent — a push that succeeds, an
	// event that exists, and a caller that concludes it did not.
	outboxID, err := r.opt.Store.Enqueue(ctx, store.OutboxEntry{
		DetectionID: id, Kind: store.KindEvent,
		Payload: string(payload), Tag: body.Tag,
	}, r.opt.PushMax)
	if err != nil {
		return err
	}
	r.appendLedger(ctx, store.LedgerPushEnqueued, id,
		"kind="+store.KindEvent, "tag="+body.Tag, "outbox="+itoa64(outboxID),
		"box="+boxString(measuredBox(ev)),
		"confidence="+fmt.Sprintf("%.6f", in.Confidence),
		"aistate="+obs.State,
		"rule="+in.RuleName)
	r.logf("queued %s push %d for detection %s", store.KindEvent, outboxID, id)
	return nil
}

// provenanceOf is the one place a VLM answer becomes a wire attribution.
//
// It is a function and not an inline literal because there are now several callers —
// the detection event and the aggregated absence event — and two constructions of
// R8's bundle would drift. The drift would be invisible: both would still compile,
// both would still pass a test that checked the fields, and one of them would stop
// carrying the summary.
func provenanceOf(obs vlm.Observation) xprotect.Provenance {
	p := xprotect.Provenance{
		AIState:         obs.State,
		Verdict:         string(obs.Verdict),
		Summary:         obs.Summary,
		ModelIdentity:   obs.ModelIdentity,
		ContractVersion: obs.ContractVersion,
		Temperature:     obs.Temperature,
		OutputHash:      obs.OutputHash,
	}
	// An unasked window has no verdict, and the observation sometimes carries a stale
	// one from a reused struct. Stripping it here rather than trusting it is what
	// keeps the fabricated negative off the wire: BuildEvent refuses a verdict on a
	// blindness state, and this is where the refusal is made reachable-by-design
	// rather than reachable-by-accident.
	if aistate.IsBlindness(obs.State) {
		p.Verdict = ""
		p.Summary = ""
		p.ModelIdentity = ""
		p.ContractVersion = ""
		p.OutputHash = ""
	}
	return p
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
	if obs.State == sidecar.StateAnswered {
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
	case obs.State != sidecar.StateAnswered:
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
	// A SWITCH and not `if bookmark else event`. With an `else`, a kind added later
	// is sent as an analytics event by default — so `absence_event` would have gone
	// out correctly by luck rather than by decision, and the next kind added would
	// go out as an analytics event by nobody's decision at all.
	switch e.Kind {
	case store.KindBookmark:
		out, err = r.opt.XProtect.SendBookmarkPayload(ctx, xprotect.PushPayload{Body: []byte(e.Payload), Tag: e.Tag})
	case store.KindEvent, store.KindAbsenceEvent:
		out, err = r.opt.XProtect.SendPayload(ctx, xprotect.PushPayload{Body: []byte(e.Payload), Tag: e.Tag})
	default:
		// Unreachable through Enqueue, which refuses an undeclared kind. Refused rather
		// than defaulted, because a row that reaches here with a payload nobody chose
		// is exactly the kind of row AC4 would count as an attempt with no result.
		_ = r.opt.Store.RecordResult(ctx, e.ID, "push_refused",
			"outbox kind "+e.Kind+" is not one this build sends; the row is kept for a human", "")
		return
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
	// Guarded, because `currentRSS` is nil on a platform that ships no reader and a
	// nil call is a panic in a goroutine that samples every few seconds. Previously
	// it was always the sentinel error function, so the guard was not needed; now
	// that nil IS the signal, the caller has to honour it.
	rss, rssErr := int64(0), error(nil)
	if HasRSSReader() {
		rss, rssErr = currentRSS()
	} else {
		rssErr = errNoRSSReader
	}
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
func (r *Runner) SetFrameHook(fn func(recorder.Frame, []sidecar.Box, []byte, sidecar.Telemetry, string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opt.onFrame = func(f recorder.Frame, b []sidecar.Box, jpg []byte, tel sidecar.Telemetry, state string) {
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
func (r *Runner) Latencies() *sidecar.Latencies { return r.lat }

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
