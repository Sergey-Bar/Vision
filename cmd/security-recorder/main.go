// Command security-recorder records the site.
//
// # Why it is a SEPARATE binary
//
// The recorder opens sockets to cameras and writes to a disk. The core does
// neither. Keeping them apart is the difference between a camera that stops
// answering taking the console down with it and a camera that stops
// answering being a hole in the recording that the console can SHOW.
//
// The two have separate failure domains, separate credentials, and separate
// lifecycles. A recorder that is restarted on a schedule does not interrupt
// an operator's session; a core that is restarted does not interrupt a
// recording.
//
// # What it does
//
// For each camera in the camera list:
//
//  1. Dial the RTSP stream.
//  2. Cut it into segments on its keyframes, with no hole between them.
//  3. Write each segment to the disk ATOMICALLY — temporary file, fsync,
//     rename — so a reader sees either the whole segment or none of it.
//  4. Index it, and advance the chain that makes it provable.
//
// The file goes to the disk BEFORE the index row, because a file with no
// index row is a file nobody can find and a finder that misses it is a hole
// in the recording the site can SEE, while an index row with no file is a row
// pointing at nothing.
//
// # What it does NOT do
//
// It does not seal the chain into the audit ledger, and it does not delete
// segments. Both are the CORE's: the ledger is the core's, and a recorder
// that could delete its own segments is a recorder that could hide a gap in
// them.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/Sergey-Bar/vision/internal/config"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/device"
	"github.com/Sergey-Bar/vision/internal/recorder"
	"github.com/Sergey-Bar/vision/internal/recording"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet, so this goes to stderr directly. A
		// refusal about configuration is the one thing that must be readable
		// even when the logging setup is the thing that failed.
		fmt.Fprintf(os.Stderr, "security-recorder: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadRecorder()
	if err != nil {
		return err
	}

	log, err := newLogger(cfg.LogLevel)
	if err != nil {
		return err
	}
	log.Info("starting security-recorder", flatten(cfg.Redacted())...)

	pool, err := dbx.Open(context.Background(), cfg.Database, config.DatabasePassword())
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer pool.Close()

	sources, err := recorder.LoadSources(cfg.Sources)
	if err != nil {
		// An EMPTY camera list is a configuration fault and not a parse
		// error, and the two need different things from the person who
		// sees them: this one needs a camera in the list, and the other
		// needs the file fixed.
		if errors.Is(err, recorder.ErrNoSources) {
			return fmt.Errorf("%w: %w", errIsNotConfigured, err)
		}
		return err
	}
	log.Info("the camera list is loaded", "cameras", sources.Len())

	index := recording.NewService(pool)

	// The signal is trapped BEFORE anything long-running starts, so a signal
	// that arrives during construction is not lost.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The PLAN: the camera list joined with the device registry.
	//
	// The registry is the recorder's INPUT, and the join is what stops the
	// recorder recording a camera the site's own inventory does not name. It
	// is also what stops it recording a camera nobody has MEASURED, because
	// a guess about a camera's capabilities is how a product ends up
	// promising H.265 it cannot deliver.
	plan, err := recorder.PlanFor(ctx, &device.Service{Pool: pool}, sources.All(), cfg.TenantID, cfg.Sites())
	if err != nil {
		return err
	}
	// The plan is LOGGED, and both halves of it. A recorder that starts and
	// records nothing says nothing, and the operator sees a site with no
	// footage and no explanation.
	log.Info("the recording plan", "plan", plan.Describe())
	for _, skipped := range plan.Skipped {
		log.Warn("a camera will not be recorded",
			"device_id", skipped.DeviceID, "reason", skipped.Reason)
	}
	if len(plan.Sources) == 0 {
		return fmt.Errorf("%w: no camera in the list is one the product is responsible for, and "+
			"nothing would be recorded. The plan above says which cameras were skipped and why",
			errNothingToRecord)
	}

	// One recorder per camera, each in its own goroutine.
	//
	// A camera that dies takes down its OWN goroutine and not the others: a
	// recorder that shares a loop with a dead camera takes the dead camera's
	// silence with it, and the site ends up with one hole where it had one
	// camera.
	var wg sync.WaitGroup
	for _, src := range plan.Sources {
		wg.Add(1)
		go func(src recorder.Source) {
			defer wg.Done()
			recordCamera(ctx, index, cfg, src, log)
		}(src)
	}

	<-ctx.Done()
	log.Info("shutting down: finishing the segments in progress")
	wg.Wait()
	log.Info("shutdown complete")
	return nil
}

// recordCamera records one camera, for as long as the context lasts.
//
// The retry is the loop: a camera that fails is re-dialled after a delay,
// and the failure is REPORTED every time. A recorder that logged one failure
// and stopped would report a site as recording while the camera is dark.
func recordCamera(ctx context.Context, index *recording.Service, cfg config.Recorder,
	src recorder.Source, log *slog.Logger) {

	log = log.With(
		"tenant_id", src.TenantID,
		"site_id", src.SiteID,
		"device_id", src.DeviceID)

	for {
		if ctx.Err() != nil {
			return
		}
		err := recordOnce(ctx, index, cfg, src, log)
		if ctx.Err() != nil {
			return
		}
		switch {
		case err == nil:
			// The camera's stream ENDED, which is a fact and not a
			// failure. A camera that stops sending is either finished or
			// broken, and the distinction matters to whoever has to fix it.
			log.Info("the camera's stream ended")
		case errors.Is(err, recorder.ErrNoRoot):
			// A PERMANENT fault, and the retry loop must not spin on it.
			// The recorder has nowhere to write, and re-dialling the
			// camera will not produce a root. It is a configuration fault
			// and it is said so as one.
			log.Error("the recorder has nowhere to write; stopping this camera",
				"error", err.Error())
			return
		default:
			log.Warn("the camera stopped recording", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.RetryDelay):
		}
	}
}

// recordOnce records one camera until the stream ends or the context ends.
func recordOnce(ctx context.Context, index *recording.Service, cfg config.Recorder,
	src recorder.Source, log *slog.Logger) error {

	rec := recorder.New(index, recorder.Options{
		Root:            cfg.Root,
		SegmentDuration: cfg.SegmentDuration,
		RTSPTimeout:     cfg.RTSPTimeout,
		Now:             func() time.Time { return time.Now().UTC() },
		Logf: func(format string, args ...any) {
			log.Warn(fmt.Sprintf(format, args...))
		},
	}, func(seg recording.Segment) {
		log.Info("segment recorded",
			"seq", seg.Seq,
			"bytes", seg.Bytes,
			"started_at", seg.StartedAt.Format(time.RFC3339Nano),
			"gap_before_ms", seg.GapBefore.Milliseconds(),
			"state", seg.State)
	})
	return rec.Run(ctx, src)
}

// newLogger is the recorder's logger.
//
// Text to stderr. Stderr is where systemd collects it, and a console log
// mixed into an operator's terminal output on an SSH session is a stream
// nobody reads.
func newLogger(level string) (*slog.Logger, error) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("SP_LOG_LEVEL must be one of debug, info, warn, error, got %q", level)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})), nil
}

// flatten turns the startup map into alternating key/value arguments for slog,
// in sorted order so two runs produce comparable log lines.
func flatten(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		out = append(out, k, m[k])
	}
	return out
}

// errNothingToRecord is a plan that would record nothing.
//
// It is a REFUSAL and not a warning: a recorder that starts, reports that it
// is running, and records nothing is a site that believes it is being
// recorded and is not.
var errNothingToRecord = errors.New("security-recorder: nothing would be recorded")

// errIsNotConfigured marks a refusal that needs a CONFIGURATION change rather
// than a log.
//
// It exists so the exit path can tell "the operator has not configured this
// yet" from "the recorder failed", because the two need different things
// from the person who sees them: the first needs a variable, the second needs
// a log.
var errIsNotConfigured = errors.New("security-recorder: the install is not configured")
