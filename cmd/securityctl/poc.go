package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/detector"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/mp4"
	"github.com/Sergey-Bar/Ai-Captain/internal/rtsp"
)

// pocBuildVersion is printed in the banner and stored on the incident.
//
// It is a literal rather than a linker-injected value so that `go run` and an
// installed binary report the same thing. A banner reading "(devel)" tells an
// operator nothing about which capture path they are looking at.
const pocBuildVersion = "poc/0.1.0"

// pocCommand is the Stage 1 proof: a live camera to a stored, hashed clip and a
// raised incident, on one machine, with no key and no model.
func pocCommand() *command.Command {
	return &command.Command{
		Name:    "poc",
		Summary: "Run the capture path end to end: stream, detect, clip, store, raise",
		Usage: `poc — the whole capture path, in one command.

Reads a camera over RTSP, or replays a recorded fixture, watches it with a
local always-on detector, assembles a bounded clip with lead-in when the
detector fires, stores the clip by content hash, and raises an incident for it.

There is no AI plane in this build. Every incident it raises is therefore
decided WITHOUT an observation, and it says so on the banner, in the output
and in the artefact it writes. That is not a placeholder to be filled in later:
it is the "missing is not negative" rule exercised on every single run, and the
three absences stay distinguishable in the output.

  --camera    live RTSP camera
  --stream    replay a recorded .h264 elementary stream
  --record    write the session's NAL units to a .h264 file as they arrive
  --out       evidence root, and the directory the artefacts are written to
  --json      emit the machine-readable report only

Examples:
  securityctl poc --stream testdata/poc/testsrc.h264
  securityctl poc --camera rtsp://CAMERA/stream --record capture.h264 //nolint:gosec // usage text, not a credential
  securityctl poc --camera $CAM --json > run.json`,
		Run: runPoc,
	}
}

type pocOptions struct {
	camera      string
	stream      string
	record      string
	out         string
	tenant      string
	site        string
	threshold   float64
	preRoll     time.Duration
	postRoll    time.Duration
	maxTime     time.Duration
	maxBytes    int64
	seconds     time.Duration
	pictures    int
	timeout     time.Duration
	fps         float64
	recordMaxMB int64
	asJSON      bool
	quiet       bool
}

// registerPocFlags binds the options. They are all flags and never positional
// arguments: a positional path is a path whose meaning cannot be read back out
// of a process listing, and this command takes a camera URL, which is worse.
func registerPocFlags(o *pocOptions, fs *flag.FlagSet) {
	fs.StringVar(&o.camera, "camera", "", "Live RTSP camera URL. Credentials in the URL are used and never printed.")
	fs.StringVar(&o.stream, "stream", "", "Replay a recorded .h264 elementary stream instead of opening a camera.")
	fs.StringVar(&o.record, "record", "", "Write the session's NAL units to this .h264 file as they arrive.")
	fs.StringVar(&o.out, "out", "", "Evidence root, and directory the artefacts are written to. Defaults to a temp dir.")
	fs.StringVar(&o.tenant, "tenant", "poc", "Tenant id. Recorded on the incident and the evidence layout.")
	fs.StringVar(&o.site, "site", "poc-camera", "Site id. Recorded on the incident.")
	fs.Float64Var(&o.threshold, "threshold", 0.5, "Detector score above which a picture is worth a query.")
	fs.DurationVar(&o.preRoll, "pre-roll", 5*time.Second, "Lead-in kept before the trigger.")
	fs.DurationVar(&o.postRoll, "post-roll", 5*time.Second, "How long to keep recording after the trigger.")
	fs.DurationVar(&o.maxTime, "max-time", 30*time.Second, "Hard cap on clip duration.")
	fs.Int64Var(&o.maxBytes, "max-bytes", mp4.DefaultMaxBytes, "Hard cap on clip bytes.")
	fs.DurationVar(&o.seconds, "seconds", 0, "Stop a live run after this much wall time. A --stream replay reads at file speed and usually finishes first; use --pictures to bound it.")
	fs.IntVar(&o.pictures, "pictures", 0, "Stop after this many pictures. Zero reads to the end. This is the bound that works for a replay.")
	fs.DurationVar(&o.timeout, "timeout", rtsp.DefaultTimeout, "RTSP request and read timeout.")
	fs.Float64Var(&o.fps, "fps", 25, "Nominal frame rate for --stream, which carries no timing of its own.")
	fs.Int64Var(&o.recordMaxMB, "record-max-mb", 512, "Stop writing --record past this many MiB, and say so.")
	fs.BoolVar(&o.asJSON, "json", false, "Emit the machine-readable report only, with no banner or live line.")
	fs.BoolVar(&o.quiet, "quiet", false, "Suppress the live status line but keep the banner and the summary.")
}

func runPoc(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("poc", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o pocOptions
	registerPocFlags(&o, fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &command.UsageError{Err: errors.New("pass the source with --camera or --stream, not as a positional argument")}
	}
	switch {
	case o.camera == "" && o.stream == "":
		return &command.UsageError{Err: errors.New("one of --camera or --stream is required")}
	case o.camera != "" && o.stream != "":
		return &command.UsageError{Err: errors.New("--camera and --stream are mutually exclusive")}
	case o.tenant == "":
		return &command.UsageError{Err: errors.New("--tenant is required; a run that silently picks a tenant is a run whose evidence lands in the wrong place")}
	}
	if o.preRoll < 0 || o.postRoll < 0 || o.seconds < 0 || o.pictures < 0 {
		return &command.UsageError{Err: errors.New("--pre-roll, --post-roll, --seconds and --pictures cannot be negative")}
	}
	if o.threshold <= 0 || o.threshold > 1 {
		return &command.UsageError{Err: errors.New("--threshold is a normalised score and must be in (0,1]")}
	}

	work := o.out
	if work == "" {
		dir, err := os.MkdirTemp("", "securityctl-poc-")
		if err != nil {
			return fmt.Errorf("poc: create a working directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		work = dir
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		return fmt.Errorf("poc: create %s: %w", work, err)
	}

	recorder, err := openClipRecorder(o.record, o.recordMaxMB)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := recorder.Close(); cerr != nil {
			fmt.Fprintln(env.Stderr, cerr)
		}
	}()

	src, err := openSource(ctx, o)
	if err != nil {
		return err
	}
	defer func() { _ = src.close() }()

	store, err := evidence.New(evidence.Options{Root: filepath.Join(work, "evidence")})
	if err != nil {
		return fmt.Errorf("poc: open the evidence store: %w", err)
	}

	rep := &pocReport{
		ReportVersion: reportVersion,
		Build:         pocBuildVersion,
		TenantID:      o.tenant,
		SiteID:        o.site,
		Source:        src.source(),
		Detector:      src.detectorName(),
		StartedAt:     time.Now().UTC(),
		Limitations: []string{
			"no AI plane is configured in this build, so the observation is degraded and the incident is decided without one",
			"the detector is the decoder-free coded-size reference: it measures change, not what changed",
			"the clip is muxed by this build; verify it with an independent decoder before trusting it",
		},
	}

	onReading := func(r detector.Reading) {
		if o.asJSON || o.quiet {
			return
		}
		fmt.Fprintf(env.Stdout, "\r%-6s score %.2f/%.2f  %-70.70s",
			r.Detector, r.Score, r.Threshold, r.Reason)
	}
	onReady := func() {
		if !o.asJSON {
			printBanner(env, rep, o)
		}
	}
	run, runErr := runCapture(ctx, o, recorder, src, store, rep, onReading, onReady)
	if recorder.stopped != "" {
		rep.Degradations = append(rep.Degradations, "recording: "+recorder.stopped)
	}
	// A source that produced no picture at all still gets a banner, because a run
	// that says nothing is not a run that can be judged.
	if !o.asJSON && rep.Pictures == 0 {
		printBanner(env, rep, o)
	}
	return reportPoc(env, o, rep, run, runErr)
}
