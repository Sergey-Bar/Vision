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
	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/source"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/supervisor"
	"github.com/Sergey-Bar/ai-captain-poc/internal/ui"
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
	if spec.Live() {
		logf("source: %s", spec.LabelRedacted())
	}
	zone, err := detect.LoadZone(cfg.ZonesPath)
	if err != nil {
		return err
	}
	engine, err := detect.New(zone, detect.Config{
		ScoreThreshold: cfg.ScoreThreshold, Classes: cfg.Classes, IoU: cfg.TrackIoU,
		MaxAge: cfg.TrackMaxAge, Cooldown: cfg.Cooldown, Tolerance: cfg.LineTolerance,
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
	aic, err := ai.New(cfg.AIAddr, cfg.AIDeadline)
	if err != nil {
		return err
	}
	side, err := supervisor.StartSidecar(context.Background(), sidecarConfig(cfg), aic, logf)
	if err != nil {
		return err
	}
	defer func() { _ = side.Stop(3 * time.Second) }()

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
		CameraGUID:    cfg.CameraGUID,
		Zone:          zone,
		Provenance:    provenanceFor(cfg, spec), ClipBefore: cfg.ClipBefore, ClipAfter: cfg.ClipAfter,
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
		LastJPEG:   rec.LastFrameJPEG,
		OnFrame:    nil,
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
		runner.SetFrameHook(func(f recorder.Frame, boxes []ai.Box, jpg []byte) {
			screen.Publish(ui.Live{
				At: f.At, Frame: jpg, Width: f.Width, Height: f.Height,
				Boxes: toOverlay(boxes), Lines: toOverlayLines(zone),
				AIState: "answered", Provenance: provenanceFor(cfg, spec),
				Simulator: !cfg.IsLiveVMS(), Source: spec.Label(), Mode: cfg.Mode,
				ClockSource: cfg.ClockSource,
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
	rep, err := buildReport(st, runID, cfg, spec, end.Sub(runStart), endReason)
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

func buildReport(st *store.Store, runID string, cfg config.Config, spec source.Spec, elapsed time.Duration, endReason string) (soak.Report, error) {
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
			Heartbeats: beats, Faults: len(faults)},
		FalsePositives: soak.NewFalsePositiveRow(in.FalsePositives),
		Truth:          soak.BuildTruth(spec.Label(), cfg.XProtectBaseURL, pushTargetName(cfg), cfg.IsLiveVMS(), provenanceFor(cfg, spec)),
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
