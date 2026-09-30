// Package supervisor owns the child processes and the conditions that must
// become recorded faults rather than silence.
//
// It supervises two children: the single ffmpeg that both records and feeds the
// analysis pipe, and the Python sidecar. It watches three conditions:
//
//   - backpressure on the MJPEG pipe, which kills ffmpeg and records
//     ai_pipe_backpressure rather than dropping frames quietly;
//   - ffmpeg exiting, which is recorded as a stream_gap and is never restarted,
//     because an in-run reconnect would turn a dropout into a success in the
//     report;
//   - the sidecar going away, which is recorded as its own fault and never
//     guessed at: a missing answer is not an empty scene.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
)

// FaultReason is the closed set of reasons this package produces. The store
// rejects an unnamed fault, and the report aggregates by reason, so the set is
// small and written down.
type FaultReason string

const (
	ReasonAIPipeBackpressure FaultReason = "ai_pipe_backpressure"
	ReasonStreamGap          FaultReason = "stream_gap"
	ReasonSidecarExit        FaultReason = "sidecar_exit"
	ReasonSidecarUnready     FaultReason = "sidecar_unready"
	ReasonRecorderExit       FaultReason = "process_exit"
	ReasonDiskBudget         FaultReason = "disk_budget_exceeded"
)

// Fault is what the supervisor reports. The run records it; the supervisor does
// not know what a database is.
type Fault struct {
	At     time.Time
	Reason FaultReason
	Detail string
}

// Sink receives faults. It is an interface so the supervisor has no dependency
// on the store and can be tested without one.
type Sink interface {
	Record(ctx context.Context, f Fault)
}

// SidecarConfig is how to start the Python worker.
type SidecarConfig struct {
	// Python is the interpreter. On Windows this is usually "py" with -3, on
	// Linux "python3".
	Python     string
	PythonArgs []string
	Script     string
	Model      string
	Addr       string
	// ReadyDeadline is how long to wait for /healthz. A model that fails to
	// load is a crash in minute one, so this is checked before the run starts
	// and the run is refused rather than begun.
	ReadyDeadline time.Duration
}

// Probe verifies the sidecar binary and script exist and the model file is
// there, without starting anything. Preflight calls it.
func (s SidecarConfig) Probe() error {
	if s.Python == "" {
		return errors.New("supervisor: no Python interpreter; D2 makes the sidecar a hard dependency, and without it every frame would be not_asked")
	}
	if _, err := exec.LookPath(s.Python); err != nil {
		return fmt.Errorf("supervisor: the Python interpreter %q is not on PATH: %w", s.Python, err)
	}
	if s.Script == "" {
		return errors.New("supervisor: no sidecar script; the POC has no built-in detector and the sidecar is the detector")
	}
	if _, err := os.Stat(s.Script); err != nil {
		return fmt.Errorf("supervisor: the sidecar script %s is missing: %w", s.Script, err)
	}
	if s.Model == "" {
		return errors.New("supervisor: no ONNX model; run 'pocctl fetch-model' first, because a run that starts without a model dies in minute one")
	}
	if info, err := os.Stat(s.Model); err != nil {
		return fmt.Errorf("supervisor: the model %s is missing: %w; run 'pocctl fetch-model' first, because a run that starts without a model dies in minute one and the report would call that a dropout", s.Model, err)
	} else if info.Size() == 0 {
		return fmt.Errorf("supervisor: the model %s is zero bytes; a download that produced an empty file is not a model", s.Model)
	}
	return nil
}

// Args is the command line for the sidecar.
func (s SidecarConfig) Args() []string {
	host, port, err := splitHostPort(s.Addr)
	if err != nil {
		host, port = "127.0.0.1", "8099"
	}
	args := append([]string{}, s.PythonArgs...)
	args = append(args, s.Script, "--host", host, "--port", port, "--model", s.Model)
	return args
}

// splitHostPort takes an address that may or may not carry a scheme, because
// the configuration's variable is a URL (it is used as a URL everywhere else)
// and the sidecar's flag wants a bare host.
//
// A hand-rolled LastIndex(":") got this wrong on a URL: the last colon is the
// port's, and everything before it still had "http://" on the front, so the
// sidecar was launched with `--host http://127.0.0.1` and refused it — which
// was a correct refusal of a malformed argument and a completely wrong place to
// look for the fault.
func splitHostPort(addr string) (string, string, error) {
	if u, err := url.Parse(addr); err == nil && u.Scheme != "" && u.Host != "" {
		addr = u.Host
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("supervisor: %q is not host:port, so the sidecar cannot be told where to listen: %w", addr, err)
	}
	// A bare IPv6 literal without brackets has no port, and net.SplitHostPort
	// says so; a host that is only an address gets the documented default.
	if host == "" {
		return "", "", fmt.Errorf("supervisor: %q has no host", addr)
	}
	return host, port, nil
}

// Sidecar is a running Python worker.
type Sidecar struct {
	cfg  SidecarConfig
	cmd  *exec.Cmd
	logf func(string, ...any)

	mu      sync.Mutex
	exitErr error
	exited  chan struct{}
	// job owns the process tree, so stopping the sidecar stops the python BEHIND
	// the launcher rather than leaving it holding the port.
	job *job
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// Start launches the worker and waits for it to report ready.
func StartSidecar(ctx context.Context, cfg SidecarConfig, client *ai.Client, logf func(string, ...any)) (*Sidecar, error) {
	if err := cfg.Probe(); err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cmd := exec.Command(cfg.Python, cfg.Args()...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("supervisor: cannot start the sidecar: %w", err)
	}
	s := &Sidecar{cfg: cfg, cmd: cmd, logf: logf, exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.exitErr = err
		s.mu.Unlock()
		close(s.exited)
	}()

	deadline := cfg.ReadyDeadline
	if deadline <= 0 {
		deadline = 60 * time.Second
	}
	wait := time.Now().Add(deadline)
	var lastErr error
	for time.Now().Before(wait) {
		select {
		case <-s.exited:
			// The message names what was actually checked, in the order it was
			// checked. "The model is probably the problem" sent an operator looking
			// at a 250 MB file when the real cause was a missing interpreter, and
			// that is the sort of message that costs an hour.
			return nil, fmt.Errorf("supervisor: the sidecar process exited before it was ready (%v). "+
				"Checked, in this order: the interpreter (%s), the script (%s, %d bytes), and the model (%s, %d bytes). "+
				"Read the child's own stderr above: it is the process that says which of the three it did not find",
				s.ExitError(), s.cfg.Python, s.cfg.Script, fileSize(s.cfg.Script), s.cfg.Model, fileSize(s.cfg.Model))
		default:
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		p, err := client.Probe(probeCtx)
		cancel()
		if err == nil {
			logf("sidecar ready: model=%s input=%dx%d providers=%s", p.Model, p.InputSize[0], p.InputSize[1], strings.Join(p.Providers, ","))
			return s, nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	_ = s.Stop(2 * time.Second)
	return nil, fmt.Errorf("supervisor: the sidecar did not become ready within %s: %v", deadline, lastErr)
}

// Exited is closed when the worker has exited.
func (s *Sidecar) Exited() <-chan struct{} { return s.exited }

// ProcessID is the OS process id of the worker. AC5's CPU clause names this
// process specifically, so the id has to be the live one.
func (s *Sidecar) ProcessID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// ExitError is why the worker ended.
func (s *Sidecar) ExitError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// Stop ends the worker, escalating to a kill.
func (s *Sidecar) Stop(grace time.Duration) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() { <-s.exited; close(done) }()
	// Ask the whole tree, not the one handle: a launcher that has already
	// exited has a real python behind it, and that python holds the port.
	_ = terminate(s.cmd)
	select {
	case <-done:
	case <-time.After(grace):
	}
	// Closing the job kills anything still inside it. This is the belt to
	// terminate's braces, and it is what makes "the sidecar is stopped" true.
	s.job.close()
	select {
	case <-done:
		return nil
	case <-time.After(grace):
		return fmt.Errorf("supervisor: the sidecar did not exit within %s and was killed", grace)
	}
}

// PipeWatch is the part of the recorder the supervisor needs. It is an interface
// so the supervisor is testable without ffmpeg.
type PipeWatch interface {
	// BackpressureWait is how long the MJPEG pipe has been blocked.
	BackpressureWait() time.Duration
	// PipeDepth is the current queue depth.
	PipeDepth() int
	// Kill terminates the recorder.
	Kill() error
	// Exited is closed when the recorder's process ended.
	Exited() <-chan struct{}
	// ExitError is why it ended.
	ExitError() error
}

// Options configure the supervisor.
type Options struct {
	Pipe         PipeWatch
	Sidecar      *Sidecar
	Backpressure time.Duration
	Tick         time.Duration
	OnFault      Sink
	// GraceAtEnd is how long the recorder is given to close its last segment.
	GraceAtEnd time.Duration
}

// Supervisor watches the children and turns the three dangerous conditions into
// recorded faults.
type Supervisor struct {
	opt  Options
	stop chan struct{}
	once sync.Once
	done chan struct{}
}

// New builds a supervisor.
func New(opt Options) (*Supervisor, error) {
	if opt.Pipe == nil {
		return nil, errors.New("supervisor: nothing to watch; without the recorder there is no backpressure condition and no stream to gap")
	}
	if opt.Backpressure <= 0 {
		return nil, errors.New("supervisor: the backpressure deadline must be positive; without one a blocked pipe is a silent stall, which is the failure this package exists to prevent")
	}
	if opt.Tick <= 0 {
		opt.Tick = 500 * time.Millisecond
	}
	if opt.OnFault == nil {
		opt.OnFault = discardSink{}
	}
	return &Supervisor{opt: opt, stop: make(chan struct{}), done: make(chan struct{})}, nil
}

type discardSink struct{}

func (discardSink) Record(context.Context, Fault) {}

// Run watches until something stops it or a child dies. It returns the first
// condition that ended the watch, so the caller can record the run's end reason.
func (s *Supervisor) Run(ctx context.Context) error {
	defer close(s.done)
	ticker := time.NewTicker(s.opt.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.stop:
			return nil
		case <-ticker.C:
			if err := s.tick(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Supervisor) tick(ctx context.Context) error {
	// Backpressure first: it is the condition that silently corrupts a run.
	if wait := s.opt.Pipe.BackpressureWait(); wait > s.opt.Backpressure {
		detail := fmt.Sprintf("the MJPEG pipe was blocked for %s, past the %s deadline; ffmpeg was killed rather than allowed to stall, because a stalled recorder produces a stream that looks alive and is not",
			wait.Round(time.Millisecond), s.opt.Backpressure)
		s.opt.OnFault.Record(ctx, Fault{At: time.Now(), Reason: ReasonAIPipeBackpressure, Detail: detail})
		_ = s.opt.Pipe.Kill()
		return fmt.Errorf("supervisor: %s: the MJPEG pipe was blocked for %s, past the %s deadline; ffmpeg was killed rather than allowed to stall, because a stalled recorder produces a stream that looks alive and is not",
			ReasonAIPipeBackpressure, wait.Round(time.Millisecond), s.opt.Backpressure)
	}

	// The recorder's own exit. A clean end of input is not a fault; an
	// unexpected one is a stream gap, and it is not restarted.
	select {
	case <-s.opt.Pipe.Exited():
		if err := s.opt.Pipe.ExitError(); err != nil {
			s.opt.OnFault.Record(ctx, Fault{
				At: time.Now(), Reason: ReasonStreamGap,
				Detail: fmt.Sprintf("ffmpeg exited unexpectedly (%v); it is not restarted, because a reconnect inside the run would turn this dropout into a success in the report", err),
			})
			return fmt.Errorf("supervisor: ffmpeg exited: %w", err)
		}
		return errors.New("supervisor: ffmpeg ended on its own; the source ran out, which is a dropout for a run that expected to continue")
	default:
	}

	if s.opt.Sidecar != nil {
		select {
		case <-s.opt.Sidecar.Exited():
			s.opt.OnFault.Record(ctx, Fault{
				At: time.Now(), Reason: ReasonSidecarExit,
				Detail: fmt.Sprintf("the sidecar exited (%v); every subsequent frame is not_asked, and not_asked is never reported as no_objects", s.opt.Sidecar.ExitError()),
			})
			return fmt.Errorf("supervisor: the sidecar exited: %w", s.opt.Sidecar.ExitError())
		default:
		}
	}
	return nil
}

// Stop ends the watch without recording a fault. Shutdown is not a failure.
func (s *Supervisor) Stop() {
	s.once.Do(func() { close(s.stop) })
}

// Done is closed when the watch has ended.
func (s *Supervisor) Done() <-chan struct{} { return s.done }

// Ensure the executable bit story is explicit: on Windows an .exe is found by
// name, on Unix a mode. LookPath is the platform's own answer and it is what
// preflight uses, so there is nothing to reimplement.
var _ = filepath.Join
