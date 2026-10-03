// Command watch is the POC's service: preflight, then supervise, then the soak
// lifecycle. It is the only long-running process in the module.
//
// Nothing here decides whether a run passed. The store records what happened,
// the soak package scores it, and the report prints it. This command's job is to
// keep the recorder, the sidecar and the clock honest and to stop when a
// supervised condition says something is wrong.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/config"
	"github.com/Sergey-Bar/ai-captain-poc/internal/detect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/source"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/supervisor"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ui"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

func main() {
	// --help and --config are handled here and then REMOVED from the argument
	// list, because the configuration loader parses the same slice and would
	// otherwise read `--config` as a flag that needs a value. That bug made
	// `watch --config` fail every time, and the secret check that was supposed
	// to catch a leak passed vacuously because the command died before printing
	// anything.
	//
	// The filter runs to completion BEFORE anything is dispatched. Returning
	// from inside the loop left os.Args unrewritten, so the loader saw the very
	// flag that had just been handled.
	rest := make([]string, 0, len(os.Args)-1)
	var showHelp, showConfig bool
	for _, a := range os.Args[1:] {
		switch a {
		case "-h", "--help", "-help":
			showHelp = true
		case "--config", "-config":
			showConfig = true
		default:
			rest = append(rest, a)
		}
	}
	os.Args = append(os.Args[:1], rest...)

	switch {
	case showHelp:
		printUsage()
		return
	case showConfig:
		printConfig()
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "watch: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`watch — the active-monitoring POC's service

  preflight, then supervise, then the soak lifecycle. Nothing here decides whether
  a run passed: the store records what happened, the soak package scores it, and
  the report prints it.

  Every setting comes from an SP_POC_* variable or from a flag with the same
  meaning, and a flag wins. Every one of them is required; a missing value fails
  by name with the consequence of not having it.

`)
	config.WriteUsage(os.Stdout)
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("    watch --data-root C:\\poc-data --source-kind loop --source-arg testdata\\loop.mp4")
	fmt.Println("    watch --help-config   (there is none; every value is listed above)")
	fmt.Println()
	fmt.Println("  There is no --data-root default. A measurement that defaulted its storage")
	fmt.Println("  location would write to somewhere nobody chose.")
}

func printConfig() {
	cfg, err := config.LoadEnvAndFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch --config: %v\n", err)
		os.Exit(1)
	}
	// The secrets are blanked by the CONFIG PACKAGE, from the same list that
	// `Secrets()` returns and that the tests check.
	//
	// It used to be a hand-written switch over two names. When the live camera's
	// password arrived as the third, `--config` would have printed it in full
	// while every test that checked for a secret still passed, because the test
	// was looking for the other two. A secret enumerated in one place and blanked
	// in another is a secret that will be printed.
	secrets := cfg.SecretFields()
	shown := cfg.Redacted()

	// HTML escaping is turned OFF. A config dump is read by a person, and
	// "<redacted: present>" arriving escaped makes the output look broken.
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shown); err != nil {
		fmt.Fprintf(os.Stderr, "watch --config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("Every SP_POC_* variable, with the values the process will use. The secrets are")
	fmt.Println("shown as", config.Redacted, "— not because they were empty, but because this dump is")
	fmt.Println("the kind of thing that ends up in a screenshot.")
	fmt.Println()
	fmt.Println("The variables that are secrets, and were blanked above: " + strings.Join(secrets, ", "))
}

func logf(format string, args ...any) {
	fmt.Printf("%s  %s\n", time.Now().UTC().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func run() error {
	cfg, err := config.LoadEnvAndFlags()
	if err != nil {
		// The configuration error names every missing variable and what breaks
		// without it. Printing it and exiting is the whole response: a run that
		// starts with a guessed value is a run that reports numbers nobody chose.
		return err
	}

	spec, err := source.Parse(cfg.SourceKind, cfg.SourceArg)
	if err != nil {
		return err
	}
	// The camera's credentials live on the spec and nowhere else: they are not
	// in the URL, they are not in the run row, and they are not in any log line.
	spec.User = cfg.SourceUser
	spec.Password = cfg.SourcePassword
	spec.Relay = cfg.SourceRelay
	if spec.Live() {
		// `Label()` already begins with "source: ", so the prefix is not added
		// here. It was, and the log read `source: source: live rtsp camera`.
		logf("%s", spec.LabelRedacted())
	}
	zone, err := detect.LoadZone(cfg.ZonesPath)
	if err != nil {
		return err
	}
	engine, err := detect.New(zone, detect.Config{
		ScoreThreshold: cfg.ScoreThreshold, Classes: cfg.Classes, IoU: cfg.TrackIoU,
		MaxAge: cfg.TrackMaxAge, Cooldown: cfg.Cooldown, Tolerance: cfg.LineTolerance,
		// The ONE place the "may this class cross" question is answered. The engine
		// used to rebuild the answer from `Classes`, and its rebuild did not know that
		// a family entry approves classes outside the taxonomy — so with
		// `SP_POC_CLASSES=other` it refused `motion`, the novelty engine's own class,
		// and every candidate was dropped as `class not on the approved list`. The
		// engine's own map is still built and still used when there is no predicate,
		// so the package behaves exactly as before for every other caller.
		Approve: cfg.IsApproved,
		// The approved/rejected split's four numbers. They arrived with the split
		// and the family taxonomy, they are REQUIRED by the configuration with no
		// defaults, and they were being validated and then thrown away: the engine
		// was built with GhostScore 0, MinArea 0, TrackMinFrames 0 and
		// MaxBoxesDrawn 0, which means every rejected box above zero was drawn as
		// a ghost and no track was ever too young to cross. A configuration value
		// that is read, validated and not used is worse than one that is absent:
		// the report says the operator set it and the run did not obey it.
		GhostScore: cfg.GhostMinScore, MinArea: cfg.MinBoxArea,
		TrackMinFrames: cfg.TrackMinFrames, MaxBoxesDrawn: cfg.MaxBoxesDrawn,
	})
	if err != nil {
		return err
	}

	// The store first: a run that cannot record anything must not start.
	st, err := store.Open(cfg.DataRoot)
	if err != nil {
		return err
	}
	defer st.Close()

	ev, err := evidence.Open(filepath.Join(cfg.DataRoot, "evidence"))
	if err != nil {
		return err
	}

	runID, err := newRunID(cfg)
	if err != nil {
		return err
	}
	runStart := time.Now().UTC()
	if err := st.StartRun(context.Background(), store.Run{
		ID: runID, Label: cfg.Label, Mode: cfg.Mode,
		SourceKind: cfg.SourceKind, SourceArg: spec.Arg,
		PushBaseURL: cfg.XProtectBaseURL, PushIsVMS: cfg.IsLiveVMS(),
		ClockSource: cfg.ClockSource, StartedAt: runStart,
		TargetSeconds: cfg.SoakTarget.Seconds(),
		Note:          "started by cmd/watch",
	}); err != nil {
		return err
	}
	logf("run %s recorded: %s", runID, spec.Label())

	// The recorder: one ffmpeg, two outputs, one clock.
	rolling := filepath.Join(cfg.DataRoot, "rolling")
	rec, err := recorder.New(recorder.Options{
		FFmpeg:         "ffmpeg",
		FFmpegArgs:     spec.InputArgs(),
		RollingDir:     rolling,
		SegmentSeconds: cfg.SegmentSeconds,
		FPS:            cfg.AIFPS,
		Width:          cfg.AIInputWidth,
		Backpressure:   cfg.AIBackpressure,
		QueueDepth:     64,
		WatchInterval:  100 * time.Millisecond,
		Logf:           logf,
	})
	if err != nil {
		return err
	}

	// The sidecar, started and proven ready before the recording begins.
	//
	// W1-4: it is started only in `sidecar` trigger mode. It used to be started
	// unconditionally, which meant every shipped run — the one that measures a
	// region with pure Go and asks a hosted model once per event — required a
	// Python interpreter, numpy, onnxruntime and an ONNX file it never loaded,
	// and `watch` returned an error before the recorder started if any of them
	// was missing. A hard dependency on a component the run does not use is a
	// dependency an operator debugs for an hour before discovering the answer is
	// "you don't need this".
	usesSidecar := soak.UsesSidecar(cfg.TriggerMode)
	var aic *ai.Client
	var side *supervisor.Sidecar
	if usesSidecar {
		aic, err = ai.New(cfg.AIAddr, cfg.AIDeadline)
		if err != nil {
			return err
		}
		side, err = supervisor.StartSidecar(context.Background(), sidecarConfig(cfg), aic, logf)
		if err != nil {
			return err
		}
		defer func() { _ = side.Stop(3 * time.Second) }()
		logf("trigger mode: sidecar — the per-frame ONNX classifier is in the trigger path")
	}

	// The mode is announced ONCE, from the value the run was actually configured
	// with, and the sentence describes THAT mode.
	//
	// The novelty line used to live in the `else` of `if usesSidecar`, which made
	// it the announcement for every mode that was not sidecar — including
	// `detector`, the shipped default. So every detector run logged
	//
	//	trigger mode: novelty — the local novelty engine measures regions...
	//	trigger mode: detector — models\yolov4n.onnx, sha256 ..., input 416x416
	//
	// two lines apart, the first of which is false. A reader who takes the first
	// line has been told this run has no ONNX and no per-frame model while the
	// second line names both. The cost argument this module is built on — one
	// inference per EVENT against one per FRAME — is decided by which of those two
	// sentences a reader believes, and the run was printing both.
	//
	// This is the same shape of defect as the demo's own trigger banner, which had
	// no `detector` case at all: a closed set of three modes and a hand-written
	// branch per mode, where the mode that became the default is the one nobody
	// added a branch for. It is announced from `cfg.TriggerMode` here, so a fourth
	// mode cannot produce a sentence borrowed from a third.
	switch cfg.TriggerMode {
	case soak.TriggerDetector:
		// Named by the detector block below, once the identity is known. Saying
		// "detector" twice would be noise, and the identity line is the better one.
	case soak.TriggerSidecar:
		// Named by the sidecar block above.
	default:
		logf("trigger mode: %s — the local novelty engine measures regions and the model verifies each event once",
			cfg.TriggerMode)
	}

	// The in-process detector. Opened here, once, and its IDENTITY printed before
	// the recorder starts — so a run that is about to be un-attributable says so
	// while there is still nobody watching.
	//
	// It is opened before `rec.Start` deliberately. A model that cannot be loaded is
	// a configuration error, and the cheapest moment to discover it is before ffmpeg
	// is running and a camera is being recorded. Discovering it at frame 3000 means
	// a run that recorded three minutes of somebody's footage and then found it had
	// no detector.
	var detector *soak.ORTDetector
	var detIdentity soak.DetIdentity
	if soak.UsesDetector(cfg.TriggerMode) {
		d, id, err := buildDetector(cfg, logf)
		if err != nil {
			return err
		}
		detector = d
		detIdentity = id
		defer func() { _ = d.Session().Close() }()
		logf("trigger mode: detector — %s, sha256 %s, licence %s, input %dx%d, %d threads",
			id.ModelPath, shortSHA(id.SHA256), licenceOrUnrecorded(id.Licence), id.InputSize, id.InputSize, id.Threads)
		if id.Licence == "" {
			// Not fatal, and said out loud. An unrecorded licence does not stop a run,
			// but a demo that redistributes its own output inherits whatever the
			// weights' licence says, and nobody can find that out later.
			logf("the model's licence is NOT RECORDED. A run whose output is shown to somebody else " +
				"depends on a term nobody has written down; record it with `pocctl fetch-runtime`")
		}
	}

	// The verifier. One hosted model, one region, no failover.
	observer, err := buildObserver(cfg, logf)
	if err != nil {
		return err
	}
	if observer == nil {
		logf("no verifier is configured (SP_POC_VLM_PROVIDER=%q): every event will be an UNVERIFIED proposal, "+
			"and the report will say so", cfg.VLMProvider)
	} else {
		logf("verifier: %s in region %s", soak.NameOf(observer), cfg.VLMRegion)
	}

	// The trigger. Pure Go, licence-free, and it owns the region.
	trigger, err := buildTrigger(cfg, st, ev)
	if err != nil {
		return err
	}

	if err := rec.Start(context.Background()); err != nil {
		return err
	}

	// The push target.
	var push *xprotect.Client
	if cfg.XProtectBaseURL != "" {
		push, err = xprotect.New(xprotect.Config{
			BaseURL: cfg.XProtectBaseURL, Timeout: cfg.PushTimeout,
			Credentials: xprotect.Credentials{
				ClientID: cfg.XProtectClientID, ClientSecret: cfg.XProtectClientSecret,
				Username: cfg.XProtectUsername, Password: cfg.XProtectPassword,
			},
			Logf: logf,
		})
		if err != nil {
			return err
		}
		if err := push.Authenticate(context.Background()); err != nil {
			// An auth failure is recorded and the run continues without a push
			// target, so the report shows the pipeline's own health next to an
			// honest "no push happened".
			_ = st.RecordFault(context.Background(), runID, store.Fault{
				At: time.Now().UTC(), Reason: "push_target_unavailable", Detail: err.Error(),
			})
			logf("push target not usable: %v", err)
			push = nil
		}
	}

	runner, err := soak.NewRunner(soak.RunnerOptions{
		RunID: runID, DataRoot: cfg.DataRoot, Spec: spec, Store: st, Evidence: ev,
		Client: aic, XProtect: push, Engine: engine, Recorder: rec,
		TriggerMode: cfg.TriggerMode, Trigger: trigger, Observer: observer,
		Detector: detector, DetIdentity: detIdentity, BoxLogFrames: cfg.BoxLogFrames,
		AIFPS: cfg.AIFPS,
		DetDecode: soak.DetDecode{
			NMSClass: cfg.DetNMSClass, NMSGlobal: cfg.DetNMSGlobal,
			EmitScore: cfg.DetScore, MaxBoxes: cfg.DetMaxBoxes, MinBoxArea: cfg.MinBoxArea,
		},
		BookmarkDevicePath: cfg.DevicePath,
		// The clipper and the VMS identifiers are wired HERE, from the
		// configuration, and not left to a caller to remember.
		//
		// Both were once unwired, and both failed SILENTLY in the worst possible
		// way: a zero-valued Clipper has an empty RollingDir, so every clip was
		// refused with "cannot read the rolling directory :" — an empty path and
		// no name in the message — and with no type guid every push was refused
		// as a 400. The pipeline looked healthy, the detector found real objects,
		// and AC3 and AC4 came out NOT RUN for a reason that had nothing to do
		// with the material.
		Clipper:       recorder.Clipper{FFmpeg: "ffmpeg", RollingDir: rolling, SegmentSeconds: cfg.SegmentSeconds, Index: rec.Index},
		EventTypeGUID: cfg.EventTypeGUID,
		// The blindness map and its resolver, together. `EventTypeGUIDs` is carried
		// so `watch --config` can print what is configured, and the resolver is the
		// only thing the runner calls: a caller that reached for the map directly
		// would skip the refusal that keeps an absence from arriving as a crossing.
		EventTypeGUIDs:   cfg.EventTypeGUIDs,
		EventTypeGUIDFor: cfg.EventTypeGUIDFor,
		CameraGUID:       cfg.CameraGUID,
		Zone:             zone,
		Provenance:       provenanceFor(cfg, spec), ClipBefore: cfg.ClipBefore, ClipAfter: cfg.ClipAfter,
		Target: cfg.SoakTarget, Heartbeat: cfg.HeartbeatInterval, Gap: cfg.GapThreshold,
		PushMax: cfg.PushMaxAttempts, MaxDisk: cfg.MaxDisk,
		DeclaredRSS: cfg.RSSLimit, DeclaredDB: cfg.DBLimit, DeclaredCPU: cfg.CPULimit,
		ChildPIDs:        func() []int { return childPIDs(rec, side) },
		DeclaredAIBudget: cfg.AIBudget,
		PushBaseURL:      cfg.XProtectBaseURL, PushName: pushTargetName(cfg), PushIsVMS: cfg.IsLiveVMS(),
		ClockSource: cfg.ClockSource, Logf: logf,
		// The rehearsal rows live in THIS run's own store, because `soak start` writes
		// them there. Reading them from a scratch directory is what left AC7 as
		// NOT RUN in every run before it was fixed.
		Rehearsals: loadRehearsals(st, runID),
		// W2-4 / W2-5: the picture, the sequence it belongs to, and the picture OF
		// A NAMED FRAME. The screen needs the newest; the snapshot path needs the
		// one the decision was made on, and those are different questions.
		LastJPEG:     rec.LastFrameJPEG,
		LastJPEGSeq:  rec.LastFrameJPEGSeq,
		FrameJPEGFor: rec.FrameJPEGFor,
		OnFrame:      nil,
	})
	if err != nil {
		return err
	}

	// The screen, if one was asked for.
	var screen *ui.Server
	if cfg.UIAddr != "" {
		screen, err = ui.New(st, runID, cfg.UIAddr)
		if err != nil {
			return err
		}
		runner.SetFrameHook(func(f recorder.Frame, boxes []ai.Box, jpg []byte, tel ai.Telemetry, aiState string) {
			// W3-6: the run's own record of itself, read ONCE per frame so the truth
			// bar and the picture cannot show two different moments of the same run.
			proofLedger, proofSeal := proofFor(st, context.Background(), runID, verifierLabelFromEnv(cfg))
			// aiState is whatever `ai.Interpret` returned, from the runner, for
			// THIS frame. It used to be the literal "answered", which is not one
			// of the four words in CONTRACT.md §2.2 and which was printed for
			// frames that were never sent — a screen that says `answered` over a
			// silent sidecar is the single most reassuring lie this module could
			// tell.
			screen.Publish(ui.Live{
				At: f.At, Frame: jpg, Width: f.Width, Height: f.Height,
				Boxes: toOverlay(boxes), Lines: toOverlayLines(zone),
				AIState: aiState, AIStateKnown: aiState != "",
				Telemetry: tel, Families: familyTally(engine),
				LiveTracks: len(engine.LiveTracks()), Crossings: crossingsThisRun(st, runID),
				AIP99:      p99Millis(runner),
				Provenance: provenanceFor(cfg, spec), MaxBoxesDrawn: cfg.MaxBoxesDrawn,
				Simulator: !cfg.IsLiveVMS(), Source: spec.Label(), Mode: cfg.Mode,
				ClockSource: cfg.ClockSource,
				// W2-8: the verdict line, read from the store rather than kept in
				// the runner, so the picture can only say what the proof chain can
				// read back. Mapped field by field, because `ui.Live` holds
				// strings and a string cannot drag a package dependency across a
				// boundary into a drawing routine.
				Verdict:        verdictFor(runner, context.Background()).Verdict,
				VerdictKnown:   verdictFor(runner, context.Background()).Known,
				VerdictSummary: verdictFor(runner, context.Background()).Summary,
				PushResult:     verdictFor(runner, context.Background()).PushResult,
				Ledger:         proofLedger,
				Seal:           proofSeal,
				Model:          verifierLabelFromEnv(cfg),
				Counters: fmt.Sprintf("det %d  ·  pushed %d  ·  vlm %s",
					crossingsThisRun(st, runID), pushesThisRun(st, runID),
					verifierLabel(observer)),
			})
		})
		uiCtx, cancelUI := context.WithCancel(context.Background())
		defer cancelUI()
		go func() {
			if err := screen.Serve(uiCtx); err != nil {
				logf("the screen stopped: %v", err)
			}
		}()
		logf("screen at http://%s (read-only, loopback only)", cfg.UIAddr)
	}

	// The supervisor: the three conditions that must become recorded faults.
	sup, err := supervisor.New(supervisor.Options{
		Pipe: rec, Sidecar: side, Backpressure: cfg.AIBackpressure,
		OnFault: soak.Sink{Store: st, RunID: runID, Logf: logf},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		logf("interrupt received: stopping cleanly. A clean stop is not a fault.")
		cancel()
	}()

	// The disk budget: a run that fills its data root stops with a named reason
	// rather than deleting the evidence it exists to keep.
	//
	// For a LIVE source the rolling recording is also a RING bounded by age, and
	// that is not an optimisation: a camera recorded at full rate until a disk
	// fills is 40 GB a day at 4 Mbit/s, kept in order to use the six seconds
	// around a crossing. The ring makes the recording a constant few tens of MB
	// for the whole run, and the ceiling drops to being the backstop.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		d := recorder.Disk{Root: cfg.DataRoot, RollingDir: rolling, Ceiling: cfg.MaxDisk}
		if spec.Live() {
			d.Keep = ringWindow(cfg)
			d.SegmentSeconds = cfg.SegmentSeconds
			logf("live source: the rolling recording is a %s ring rather than a file that grows until the disk fills. The ceiling stays as the backstop.", d.Keep)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				res, err := d.Sweep()
				if err != nil {
					var budget *recorder.ErrDiskBudget
					if errors.As(err, &budget) {
						_ = st.RecordFault(context.Background(), runID, store.Fault{
							At: time.Now().UTC(), Reason: store.FaultDiskBudget, Detail: err.Error(),
						})
						logf("FAULT %s: %v", store.FaultDiskBudget, err)
						cancel()
						return
					}
					logf("disk sweep: %v", err)
					continue
				}
				if len(res.Deleted) > 0 {
					logf("disk sweep removed %d rolling segment(s), freed %s, %s in use against a ceiling of %s",
						len(res.Deleted), soak.Bytes(res.BytesFreed), soak.Bytes(res.InUse), soak.Bytes(res.Ceiling))
				}
			}
		}
	}()

	logf("starting the pipeline; target %s, heartbeats every %s", cfg.SoakTarget, cfg.HeartbeatInterval)

	// The fault rehearsal runs INSIDE the run, against the run's own push target,
	// and writes to the run's own store.
	//
	// It used to be a separate command run before the clock started, which meant
	// AC7 was `NOT RUN` in every run: the evidence about surviving failure lived
	// in a different place from the measurement it was supposed to justify. And
	// it only arms anything when the target is a SIMULATOR — a real VMS has no
	// control surface to inject a fault into, and inventing failures on somebody's
	// production system is not a rehearsal.
	if push != nil {
		(&soak.Rehearser{
			BaseURL: cfg.XProtectBaseURL, Store: st, RunID: runID, Logf: logf,
			NewClient: func() (*xprotect.Client, error) {
				return xprotect.New(xprotect.Config{
					BaseURL: cfg.XProtectBaseURL, Timeout: cfg.PushTimeout,
					Credentials: xprotect.Credentials{
						ClientID: cfg.XProtectClientID, ClientSecret: cfg.XProtectClientSecret,
						Username: cfg.XProtectUsername, Password: cfg.XProtectPassword,
					},
				})
			},
		}).StartRehearsal(ctx, 12*time.Second, nil)
		logf("the fault rehearsal will run 12s in, against this run's own push target, and its results will be scored by AC7")
	}

	runErr := make(chan error, 2)
	go func() { runErr <- runner.Run(ctx) }()
	go func() { runErr <- sup.Run(ctx) }()

	endReason := "target reached or the run was stopped"
	var firstErr error
	// Exactly two results, one per goroutine. Whichever arrives FIRST ends the
	// run and cancels the context, and the second is read once more so its
	// goroutine is not left blocked on an unread channel.
	//
	// This is a bug the module's own tests could not have found and a real run
	// found in one pass: waiting for both without cancelling is how a run
	// reaches its target, logs it, and then sits there forever with no report.
	first := <-runErr
	if first != nil {
		firstErr = first
		endReason = first.Error()
	}
	cancel()
	<-runErr
	runner.StopPump()
	if err := rec.Stop(5 * time.Second); err != nil {
		logf("the recorder did not stop cleanly: %v", err)
	}
	cancel()

	// Close the run, write the report, and print the truth line one last time.
	end := time.Now().UTC()
	if err := st.EndRun(context.Background(), runID, end, endReason); err != nil {
		logf("could not close the run: %v", err)
	}
	// AC6's first two clauses are read OFF THE RUNNER here. They were never read
	// anywhere, which meant `0 unparsed, 0 healthy-from-failed` was printed over a
	// run in which every frame had been refused — the criterion that exists to
	// catch an invented answer was structurally incapable of failing on one.
	unparsedAI, healthyFromFailed := runner.AICounters()
	aiCounters := [2]int{unparsedAI, healthyFromFailed}
	// The detector's arithmetic is persisted BEFORE the report is built, because
	// the report reads it back out of the store rather than off the runner. That
	// indirection is the point: `pocctl report` has no runner to read it from, so
	// a tally kept in memory only would explain a zero on one path and not the
	// other.
	if err := st.SaveDetectorTally(context.Background(), runID, runner.DetectorTally()); err != nil {
		logf("could not record the detector's tally, so this report cannot say WHY the run produced the number of detections it did: %v", err)
	}
	rep, err := buildReport(st, runID, cfg, spec, end.Sub(runStart), endReason, aiCounters)
	if err != nil {
		logf("could not build the report: %v", err)
	} else {
		md := rep.Markdown()
		path := filepath.Join(cfg.DataRoot, "report.md")
		if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
			logf("could not write %s: %v", path, err)
		}
		if js, err := rep.JSON(); err == nil {
			_ = os.WriteFile(filepath.Join(cfg.DataRoot, "report.json"), js, 0o644)
		}
		fmt.Println()
		fmt.Println(md)
		logf("report written to %s and report.json", path)
	}
	if firstErr != nil {
		return firstErr
	}
	return nil
}

// ringWindow is how much rolling video a live source keeps.
//
// It is the pre-detection window plus the clip window plus one full segment, and
// never less than two segments: a ring shorter than a segment deletes the file
// ffmpeg is writing into, and the recording then stops with no error anywhere.
func ringWindow(cfg config.Config) time.Duration {
	one := time.Duration(cfg.SegmentSeconds) * time.Second
	w := cfg.ClipBefore + cfg.ClipAfter + 15*time.Second
	if w < 2*one {
		w = 2 * one
	}
	return w
}

// buildObserver builds the verifier, or returns nil when the run declares none.
//
// A nil observer is a legitimate configuration and it is NOT a degraded one: the
// novelty trigger measures a region and proposes it, and an operator who wants an
// unverified proposal has exactly that. What must never happen is the reverse — a
// run that says it verifies and cannot — and that is a configuration refusal in
// internal/config rather than a nil here.
func buildObserver(cfg config.Config, logf func(string, ...any)) (soak.Observer, error) {
	if strings.EqualFold(strings.TrimSpace(cfg.VLMProvider), "none") || cfg.VLMProvider == "" {
		return nil, nil
	}
	region, err := vlm.ParseRegion(cfg.VLMRegion)
	if err != nil {
		return nil, err
	}
	c, err := vlm.New(vlm.Config{
		Region: region, APIKey: cfg.VLMAPIKey, APIModelName: cfg.VLMModel,
		DailyQueryCap: cfg.VLMDailyCap, Logf: logf,
	})
	if err != nil {
		return nil, err
	}
	host, _ := region.Host()
	logf("footage is pinned to %s for this run. There is no failover: if %s is unreachable the run degrades "+
		"and the footage stays on site.", host, host)
	return vlmObserver{c: c}, nil
}

// vlmObserver adapts *vlm.Client to soak.Observer.
//
// It is a two-method adapter rather than making Client implement the interface
// directly, so internal/vlm never learns what a pipeline is. The dependency runs
// one way: the pipeline may use a client, and a client does not use a pipeline.
type vlmObserver struct{ c *vlm.Client }

func (o vlmObserver) Observe(ctx context.Context, detectionID, class string, clip []byte, mediaType string) vlm.Observation {
	return o.c.Observe(ctx, detectionID, class, clip, mediaType)
}

func (o vlmObserver) PromptIDFor(class string) (string, string, error) {
	p, err := o.c.Prompts().Get(class)
	if err != nil {
		return "", "", err
	}
	return p.ID, p.Version, nil
}

// buildTrigger builds the local novelty engine for this run's trigger mode.
//
// Two engines are possible and the mode picks one:
//
//   - `alarm` is TEMPORAL. It proposes a measured region per frame and the line
//     crossing machine turns a track of them into an event.
//   - `camera_integrity` is REFERENCE. It pins a view and fires when the view has
//     STAYED different for a sustained period, which is the only shape a moved or
//     covered camera has.
//
// Both are built here because the choice belongs to the operator's declared mode
// and not to a default inside the runner: a detector that silently became a
// different detector would change what the report is about without changing the
// report's text.
func buildTrigger(cfg config.Config, st *store.Store, ev *evidence.Store) (soak.Trigger, error) {
	// `detector` mode has no novelty trigger at all. It builds nil here and the
	// detector is built separately, because the two answer different questions: the
	// novelty trigger asks "did the whole view change" and the detector asks "what
	// is in this frame". Wiring a detector through the trigger's candidate type
	// would discard every class it found — see soak.Detector for that arithmetic.
	if soak.UsesSidecar(cfg.TriggerMode) || soak.UsesDetector(cfg.TriggerMode) {
		return nil, nil
	}
	base := novelty.Config{
		Threshold: cfg.NoveltyThreshold, Release: cfg.NoveltyRelease,
		Sustain: cfg.NoveltySustain, GridWidth: cfg.NoveltyGrid,
		Cooldown: cfg.Cooldown,
	}
	// The trigger is the operator's declared mode and not a value read from the
	// environment behind the configuration's back. It was `os.Getenv` until this
	// commit: a variable that changes what the run measures, was in no schema,
	// was printed by no `watch --config`, and was covered by no test.
	if cfg.CameraIntegrity {
		nc := base
		nc.Mode = novelty.ModeReference
		nc.Class = "camera_integrity"
		nc.PromptID = "camera_integrity"
		nc.Sustain = cfg.NoveltySustain
		eng, err := novelty.New(nc)
		if err != nil {
			return nil, err
		}
		return soak.NewNoveltyTrigger(eng, st, ev, cfg.DataRoot), nil
	}
	nc := base
	nc.Mode = novelty.ModeTemporal
	nc.Class = "motion"
	nc.PromptID = "alarm"
	nc.Sustain = 1
	eng, err := novelty.New(nc)
	if err != nil {
		return nil, err
	}
	return soak.NewNoveltyTrigger(eng, st, ev, cfg.DataRoot), nil
}

// buildDetector opens the in-process ONNX detector and reports what it is.
//
// The identity comes back alongside the detector because the run must be able to
// PRINT it before it starts, and a detector whose weights cannot be named is a
// detector whose every measurement is unattributable. Three things happen here that
// are worth naming:
//
//   - the session's DECLARED input is cross-checked against SP_POC_DET_INPUT, and a
//     mismatch is refused rather than adjusted. The input size is a property of the
//     export, not a preference, and letterboxing into the wrong square produces
//     boxes rather than an error;
//   - the model's SHA-256 is computed from the bytes on disk and carried into every
//     decision the run writes, because that is RC-18 and it is unreachable
//     otherwise;
//   - the number of class names the model itself declares is read and reported,
//     because falling back to COCO-80 is a fact about the answer and not a detail.
//
// Every failure here is a REFUSAL with a name. A detector that opened and reported
// nothing would be indistinguishable from an empty road, which is the defect the
// previous build shipped.
func buildDetector(cfg config.Config, logf func(string, ...any)) (*soak.ORTDetector, soak.DetIdentity, error) {
	sess, err := ortdetect.Open(ortdetect.SessionConfig{
		LibraryPath: cfg.ORTLib,
		ModelPath:   cfg.DetModel,
		Threads:     cfg.ORTThreads,
		// The provider is declared, never discovered. ORT falls back to the CPU when
		// the CUDA provider cannot be used and logs it rather than erroring, so a
		// session opened without saying which device was wanted is a session whose
		// numbers carry the wrong label.
		ExecutionProvider: cfg.ORTEP,
		CUDADeviceID:      cfg.ORTCUDADevice,
	})
	if err != nil {
		// The no-cgo refusal arrives here, and it names the toolchain to install.
		// It is wrapped rather than replaced so the operator sees the reason and
		// not "the detector failed to start".
		return nil, soak.DetIdentity{}, fmt.Errorf(
			"SP_POC_TRIGGER_MODE=detector but the detector could not be opened: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = sess.Close()
		}
	}()

	// The declared input is checked BEFORE the detector is built, so the failure
	// names the model and the number rather than a later refusal about a box.
	if err := sess.CheckInputSize(cfg.DetInput); err != nil {
		return nil, soak.DetIdentity{}, fmt.Errorf(
			"SP_POC_TRIGGER_MODE=detector: %w", err)
	}

	in := sess.Input()
	d, err := ortdetect.NewDetector(ortdetect.Config{
		Session: sess,
		Decode: ortdetect.DecodeConfig{
			EmitScore: cfg.DetScore, MaxBoxes: cfg.DetMaxBoxes,
			NMSClass: cfg.DetNMSClass, NMSGlobal: cfg.DetNMSGlobal,
			MinBoxArea: cfg.MinBoxArea,
		},
		LatencyWindow: 0,
		InputSize:     cfg.DetInput,
	})
	if err != nil {
		return nil, soak.DetIdentity{}, fmt.Errorf(
			"SP_POC_TRIGGER_MODE=detector: %w", err)
	}

	// The model's own declared shapes, printed before a single frame is decoded.
	// "Why is the picture empty" is answered by the tensor shape, and it cannot be
	// answered later unless the shape was recorded.
	for _, o := range sess.Outputs() {
		logf("model output %q declares %v", o.Name, o.Dims)
	}
	logf("model input %q declares %s %dx%d", in.Name, in.Layout, in.W, in.H)

	id := sess.Identity()
	// The licence is read from the provenance record rather than invented. An empty
	// one is reported as unrecorded rather than defaulted, and a run whose output
	// somebody else will see depends on a term that is written down or is not.
	identity := soak.DetIdentity{
		ModelPath: id.ModelPath, SHA256: id.SHA256, Licence: id.Licence,
		InputSize: cfg.DetInput, Threads: cfg.ORTThreads, RuntimeVersion: id.RuntimeVersion,
	}

	adapter, err := soak.NewORTDetector(d)
	if err != nil {
		return nil, soak.DetIdentity{}, err
	}
	ok = true
	return adapter, identity, nil
}

// shortSHA abbreviates a hash for a log line while leaving it identifiable.
//
// Twelve hex characters is enough to recognise a model by eye in a run log and too
// few to be mistaken for the full digest a report has to carry. The full hash is on
// every decision row; this is only what a human reads while a run is in progress.
func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// licenceOrUnrecorded says so in words rather than printing an empty string.
//
// A log line reading `licence ` tells nobody anything, and a reader who does not
// notice the gap will assume there was no gap.
func licenceOrUnrecorded(l string) string {
	if strings.TrimSpace(l) == "" {
		return "NOT RECORDED"
	}
	return l
}

// verifierLabel is what the picture prints for the verifier, so a screenshot says
// whether a model looked at this and which one. `none` is printed as `none` and
// not as an empty cell: a picture that cannot say whether anything verified it is
// the picture from which the fabricated negative is cropped.
func verifierLabel(o soak.Observer) string {
	if o == nil {
		return "none"
	}
	return soak.NameOf(o)
}

// verifierLabelFromEnv is the same answer, read from the configuration the run was
// started with rather than from a handle that only exists while it is running.
//
// The report is regenerated long after the run ended, and a truth line that can
// only be produced inside the run is a truth line that goes stale. It is also the
// answer the REPORT should give: "who was configured to verify this" is a property
// of the configuration, and a report that says "somebody" because a handle had
// been dropped is worse than useless.
func verifierLabelFromEnv(cfg config.Config) string {
	if strings.EqualFold(strings.TrimSpace(cfg.VLMProvider), "none") || cfg.VLMProvider == "" {
		return "none"
	}
	if cfg.VLMModel != "" {
		return cfg.VLMProvider + " (" + cfg.VLMModel + ")"
	}
	return cfg.VLMProvider
}

// verdictFor reads the store for the verdict the picture will show. It is a
// function so the four fields in the Publish call are read from ONE snapshot: four
// reads of a table another goroutine is writing can show four different answers on
// one frame, which is worse than showing a slightly old one.
func verdictFor(r *soak.Runner, ctx context.Context) soak.VerdictLine {
	return r.Verdict(ctx)
}

// proofFor reads the run's own record of itself for the screen's truth bar. W3-6.
//
// It is read once per frame and published with the frame, so the bar and the
// picture can never show two different moments of the same run.
func proofFor(st *store.Store, ctx context.Context, runID, model string) (ledger, seal string) {
	t := soak.BuildTruth("", "", "", false, "").WithProof(st, ctx, runID, model)
	return t.Ledger, t.Seal
}

// pushesThisRun counts the detections whose event reached the VMS, which is a
// different number from the detections and is the one an operator checks against
// Smart Client.
func pushesThisRun(st *store.Store, runID string) int {
	entries, err := st.Stats(context.Background(), runID)
	if err != nil {
		return 0
	}
	_ = entries
	return entries.WithResult
}

func sidecarConfig(cfg config.Config) supervisor.SidecarConfig {
	script := cfg.SidecarScript
	model := cfg.AIModel
	python, args := pythonCommand()
	return supervisor.SidecarConfig{
		Python: python, PythonArgs: args, Script: script, Model: model,
		Addr: cfg.AIAddr, ReadyDeadline: 90 * time.Second,
	}
}

// pythonCommand picks the interpreter, and the order matters. On Windows the
// launcher is `py -3`; on Unix it is `python3`; `python` is last because on a
// Windows box with the Store alias installed, `python` resolves to a stub that
// prints an error and exits 9009, and a run that starts a stub and calls it a
// model problem sends the operator looking in entirely the wrong place.
//
// The lookup is exec.LookPath, NOT a stat of the current directory: `py.exe`
// lives in the system directory, not beside the binary, and a stat of "py.exe"
// only succeeds by accident.
func pythonCommand() (string, []string) {
	if p := os.Getenv("SP_POC_PYTHON"); p != "" {
		return p, splitArgs(os.Getenv("SP_POC_PYTHON_ARGS"))
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"py", []string{"-3"}},
		{"python3", nil},
		{"python", nil},
	} {
		if _, err := exec.LookPath(c.name); err == nil {
			return c.name, c.args
		}
	}
	return "python3", nil
}

func splitArgs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Fields(s)
}

func newRunID(cfg config.Config) (string, error) {
	if v := os.Getenv("SP_POC_RUN_ID"); v != "" {
		return v, nil
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%d", cfg.Label, cfg.Mode, cfg.SourceKind, cfg.SourceArg, time.Now().UnixNano())
	return "run-" + hex.EncodeToString(h.Sum(nil))[:16], nil
}

// provenanceFor decides what the report prints beside every number.
//
// For a LIVE source the default is `real`, because a camera delivering real
// frames is real footage whatever the operator meant by it. The previous default
// was `synthetic` for everything, which for a connected camera would have put
// `provenance: synthetic` beside real numbers — a false label on a real project,
// and the single most damaging thing this report could do.
//
// `SP_POC_PROVENANCE` still overrides, because an operator streaming a recording
// over RTSP genuinely is presenting synthetic material and must be able to say
// so.
func provenanceFor(cfg config.Config, spec source.Spec) string {
	if v := os.Getenv("SP_POC_PROVENANCE"); v != "" {
		if v != store.ProvenanceReal && v != store.ProvenanceSynthetic {
			return "INVALID: " + v
		}
		return v
	}
	if cfg.Mode == config.ModeDemoReplay {
		// A demo plays labelled recorded material, and that material's
		// provenance is whatever its labels.json says. The default is synthetic
		// because that is what the committed test material is.
		return store.ProvenanceSynthetic
	}
	if spec.Live() {
		return store.ProvenanceReal
	}
	return store.ProvenanceSynthetic
}

func pushTargetName(cfg config.Config) string {
	if cfg.IsLiveVMS() {
		return "XProtect"
	}
	return xpsim.Name
}

// loadRehearsals reads the fault-rehearsal rows out of the run's own store.
//
// It does NOT filter by runID, and that is deliberate. A rehearsal is evidence
// about a BUILD — that this code survives a 500, a reset connection, a timeout —
// not about one run, and it is recorded under its own `rehearsal-<timestamp>`
// run row so the two are distinguishable. Filtering by the measured run's id
// therefore found nothing, and AC7 was `NOT RUN` in every run that had actually
// been rehearsed. The gate must read the evidence, not insist the evidence
// carries a particular label.
// loadDetectorTally reads the run's detector arithmetic back out of the store.
//
// The live report could read it off the runner in memory. This reads it from the
// STORE, deliberately, because `pocctl report` has no runner to read it from and
// a report that can explain a zero on one path and not the other is worse than
// one that never could.
//
// A run with no stored tally comes back as `Seen: false`, which the report
// prints as `NOT RUN` with the reason. It must not come back as zeros: zeros
// read as a measurement of an empty road.
func loadDetectorTally(st *store.Store, runID string) soak.DetectorTally {
	raw, found, err := st.DetectorTally(context.Background(), runID)
	if err != nil || !found {
		return soak.DetectorTally{}
	}
	var t soak.DetectorTally
	if err := json.Unmarshal(raw, &t); err != nil {
		return soak.DetectorTally{}
	}
	return t
}

func loadRehearsals(st *store.Store, _ string) []soak.Rehearsal {
	faults, err := st.FaultsByReasonPrefix(context.Background(), "rehearsal:")
	if err != nil {
		return nil
	}
	out := make([]soak.Rehearsal, 0, len(faults))
	for _, f := range faults {
		out = append(out, soak.Rehearsal{
			Fault:    strings.TrimPrefix(f.Reason, "rehearsal:"),
			Expected: f.Expected, Observed: f.Observed, At: f.At, Detail: f.Detail,
		})
	}
	return out
}

func buildReport(st *store.Store, runID string, cfg config.Config, spec source.Spec, elapsed time.Duration, endReason string, aiCounters [2]int) (soak.Report, error) {
	ctx := context.Background()
	run, err := st.RunByID(ctx, runID)
	if err != nil {
		return soak.Report{}, err
	}
	counts, _ := st.CountByStatus(ctx, runID)
	faults, _ := st.Faults(ctx, runID)
	recs, _ := st.Recent(ctx, runID, 200)
	beats, _ := st.HeartbeatCount(ctx, runID)
	gaps, _ := st.HeartbeatGaps(ctx, runID, cfg.GapThreshold)
	stalled, _ := st.Stalled(ctx, runID, 5*time.Minute)
	peaks, _ := st.SamplePeaks(ctx, runID)
	push, _ := st.Stats(ctx, runID)
	rehearsals := loadRehearsals(st, runID)
	detectorTally := loadDetectorTally(st, runID)

	evidenceStats := store.EvidenceStats{}
	if ev, err := evidence.Open(filepath.Join(cfg.DataRoot, "evidence")); err == nil {
		evidenceStats, _ = st.VerifyStats(ctx, runID, func(e store.Evidence) (string, error) {
			sum, _, err := ev.Get(e.SHA256)
			return sum, err
		})
	}

	in := soak.Inputs{
		Elapsed: elapsed, Target: cfg.SoakTarget, RunEnded: true,
		HeartbeatCount: beats, ResourceSamples: peaks.Samples,
		TotalDetections: 0, EvidenceStored: evidenceStats.Stored,
		EvidenceVerified: evidenceStats.Verified, EvidenceMismatched: evidenceStats.Mismatched,
		PushAttempts: push.Total, PushesWithResult: push.WithResult,
		UnattributedPushes: push.Unattributed, DeadLettered: push.DeadLetter,
		PeakRSSBytes: peaks.RSSBytes, PeakDBBytes: peaks.DB, PeakDiskBytes: peaks.DiskBytes,
		DeclaredRSS: cfg.RSSLimit, DeclaredDB: cfg.DBLimit, DeclaredMaxDisk: cfg.MaxDisk,
		DeclaredCPU: cfg.CPULimit, CPUMeasured: peaks.CPUAnyMeasured, AIP99: time.Duration(peaks.AIP99MS) * time.Millisecond,
		AIP99Samples: peaks.Samples, DeclaredAIBudget: cfg.AIBudget,
		UnparsedAIResponses: aiCounters[0], HealthyFromFailed: aiCounters[1],
		RehearsedFaults: rehearsals,
		Provenance:      provenanceFor(cfg, spec), PushTargetIsSimulator: !cfg.IsLiveVMS(),
		SourceKind: cfg.SourceKind, SourceLabel: spec.LabelRedacted(),
	}
	for _, g := range gaps {
		in.HeartbeatGaps = append(in.HeartbeatGaps, soak.GapMeasurement{From: g.From, To: g.To, Seconds: g.Seconds})
	}
	for _, s := range stalled {
		in.StalledDetections = append(in.StalledDetections, soak.StalledDetection{ID: s.ID, Status: s.Status, Age: time.Since(s.UpdatedAt)})
	}
	for _, v := range counts {
		in.TotalDetections += v
	}

	criteria := soak.Score(in)
	summary := soak.Overall(in, criteria)
	faultRows := make([]soak.FaultRow, 0, len(faults))
	for _, f := range faults {
		v := "recorded"
		if f.Expected != "" {
			if f.Observed == f.Expected {
				v = "PASS"
			} else {
				v = "FAIL"
			}
		}
		faultRows = append(faultRows, soak.FaultRow{At: f.At, Reason: f.Reason, Detail: f.Detail,
			Expected: f.Expected, Observed: f.Observed, Verdict: v})
	}
	rep := soak.Report{
		GeneratedAt: time.Now().UTC(),
		Run: soak.RunInfo{ID: runID, Label: cfg.Label, Mode: cfg.Mode, StartedAt: run.StartedAt,
			EndedAt: &time.Time{}, TargetSecs: cfg.SoakTarget.Seconds(), ElapsedSecs: elapsed.Seconds(),
			ClockSource: cfg.ClockSource, Database: st.Path()},
		Verdict: string(summary.Verdict), Provisional: summary.Provisional,
		Criteria: criteria, Summary: summary, Faults: faultRows, Rehearsals: rehearsals,
		Peaks: soak.Peaks{RSSBytes: peaks.RSSBytes, DeclaredRSS: cfg.RSSLimit, DBBytes: peaks.DB,
			DeclaredDB: cfg.DBLimit, DiskBytes: peaks.DiskBytes, DeclaredDisk: cfg.MaxDisk,
			CPUPercent: peaks.CPUPercent, DeclaredCPU: cfg.CPULimit, CPUMeasured: peaks.CPUAnyMeasured, AIP99MS: peaks.AIP99MS,
			DeclaredAIMs: float64(cfg.AIBudget.Milliseconds()), Samples: peaks.Samples},
		Counters: soak.Counters{Detections: in.TotalDetections, ByStatus: counts,
			Evidence: evidenceStats.Stored, PushAttempts: push.Total, DeadLettered: push.DeadLetter,
			Heartbeats: beats, Faults: len(faults), Detector: detectorTally},
		FalsePositives: soak.NewFalsePositiveRow(in.FalsePositives),
		Truth:          soak.BuildTruth(spec.Label(), cfg.XProtectBaseURL, pushTargetName(cfg), cfg.IsLiveVMS(), provenanceFor(cfg, spec)).WithProof(st, ctx, runID, verifierLabelFromEnv(cfg)),
		NotProven:      soak.NotProven(in, summary),
		Warnings:       warnings(endReason, in, cfg),
	}
	end := time.Now().UTC()
	rep.Run.EndedAt = &end
	_ = recs
	return rep, nil
}

func warnings(endReason string, in soak.Inputs, cfg config.Config) []string {
	var out []string
	if endReason != "target reached or the run was stopped" {
		out = append(out, "The run ended early: "+endReason)
	}
	if cfg.IsLiveVMS() {
		out = append(out, "The push target was declared live. This build has never been connected to a real VMS; treat any claim that an event reached Smart Client as unproven until someone checks a real server.")
	}
	if len(in.RehearsedFaults) == 0 {
		out = append(out, "No fault was rehearsed on this run, so AC7 is NOT RUN and nothing here is evidence of behaviour under failure.")
	}
	return out
}

// childPIDs is the set AC5's CPU clause is about: ffmpeg and the sidecar. It is
// read on every sample because the set changes when a child restarts, and a
// stale PID is a process that is no longer ours.
func childPIDs(rec *recorder.Recorder, side *supervisor.Sidecar) []int {
	var pids []int
	if rec != nil {
		if p := rec.ProcessID(); p > 0 {
			pids = append(pids, p)
		}
	}
	if side != nil {
		if p := side.ProcessID(); p > 0 {
			pids = append(pids, p)
		}
	}
	return pids
}

func toOverlay(boxes []ai.Box) []ui.OverlayBox {
	out := make([]ui.OverlayBox, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, ui.OverlayBox{Class: b.Class, Score: b.Score, X1: b.X1, Y1: b.Y1, X2: b.X2, Y2: b.Y2})
	}
	return out
}

func toOverlayLines(z detect.Zone) []ui.OverlayLine {
	out := make([]ui.OverlayLine, 0, len(z.Lines))
	for _, l := range z.Lines {
		out = append(out, ui.OverlayLine{Name: l.Name, A: l.A, B: l.B})
	}
	return out
}

// familyTally is the run's approved boxes by family, as the screen reads them.
//
// It is a function and not a field because the tally is the ENGINE's and the
// screen is a reader: a counter the screen kept would be a second measurement of
// the same thing, and the two would disagree the moment one of them was wrong.
func familyTally(e *detect.Engine) map[string]int {
	out := map[string]int{}
	if e == nil {
		return out
	}
	for _, row := range e.Tally().Rows() {
		out[string(row.Family)] = row.Approved
	}
	return out
}

// crossingsThisRun is read from the store rather than kept in a counter, for the
// reason every number on this screen is a number from a row: a counter in the
// publisher process would be lost on a restart and would not agree with the
// detections table beside it.
func crossingsThisRun(st *store.Store, runID string) int {
	n, err := st.Count(context.Background(), runID)
	if err != nil {
		return 0
	}
	return n
}

// p99Millis is the AI round-trip reservoir's p99. Zero means no round trip has
// been measured, and the strip prints NOT RUN for it rather than `0 ms`, which
// would read as a model that answered instantly.
func p99Millis(r *soak.Runner) int64 {
	if r == nil {
		return 0
	}
	_, _, p99, _, _ := r.Latencies().Percentiles()
	return p99.Milliseconds()
}
