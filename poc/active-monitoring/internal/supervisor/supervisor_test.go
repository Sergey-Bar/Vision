package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePipe struct {
	wait   time.Duration
	depth  int
	killN  int
	exited chan struct{}
	err    error
	mu     sync.Mutex
}

func newFakePipe() *fakePipe { return &fakePipe{exited: make(chan struct{})} }

func (f *fakePipe) BackpressureWait() time.Duration { return f.wait }
func (f *fakePipe) PipeDepth() int                  { return f.depth }
func (f *fakePipe) Exited() <-chan struct{}         { return f.exited }
func (f *fakePipe) ExitError() error                { return f.err }
func (f *fakePipe) Kill() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killN++
	select {
	case <-f.exited:
	default:
		close(f.exited)
	}
	return nil
}
func (f *fakePipe) kills() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killN
}

type recorder struct {
	mu     sync.Mutex
	faults []Fault
}

func (r *recorder) Record(_ context.Context, f Fault) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faults = append(r.faults, f)
}

func (r *recorder) all() []Fault {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Fault, len(r.faults))
	copy(out, r.faults)
	return out
}

func TestNewRefusesAnUnwatchableConfiguration(t *testing.T) {
	if _, err := New(Options{Backpressure: time.Second}); err == nil {
		t.Fatal("a supervisor with no pipe was built")
	}
	if _, err := New(Options{Pipe: newFakePipe()}); err == nil {
		t.Fatal("a supervisor with no backpressure deadline was built: a blocked pipe would then be a silent stall")
	}
}

// TestBackpressureIsAFaultAndKillsTheRecorder is R6. The recorder is killed and
// the condition is recorded with its reason. Nothing is dropped quietly and
// nothing is retried.
func TestBackpressureIsAFaultAndKillsTheRecorder(t *testing.T) {
	pipe := newFakePipe()
	rec := &recorder{}
	s, err := New(Options{Pipe: pipe, Backpressure: 50 * time.Millisecond, Tick: 5 * time.Millisecond, OnFault: rec})
	if err != nil {
		t.Fatal(err)
	}
	pipe.wait = 200 * time.Millisecond
	err = s.Run(context.Background())
	if err == nil {
		t.Fatal("the supervisor did not stop on backpressure")
	}
	if !strings.Contains(err.Error(), string(ReasonAIPipeBackpressure)) {
		t.Fatalf("the error does not name the reason: %v", err)
	}
	if pipe.kills() != 1 {
		t.Fatalf("the recorder was killed %d times, want 1", pipe.kills())
	}
	faults := rec.all()
	if len(faults) != 1 {
		t.Fatalf("faults = %d, want exactly 1: a condition recorded twice is two incidents", len(faults))
	}
	if faults[0].Reason != ReasonAIPipeBackpressure {
		t.Fatalf("reason = %s", faults[0].Reason)
	}
	if !strings.Contains(faults[0].Detail, "blocked for") {
		t.Fatalf("the detail does not say how long it was blocked: %s", faults[0].Detail)
	}
}

// TestAnUnexpectedRecorderExitIsAStreamGapAndIsNotRestarted. A reconnect inside
// the run would turn the dropout into a success in the report, which is the one
// thing the plan forbids.
func TestAnUnexpectedRecorderExitIsAStreamGap(t *testing.T) {
	pipe := newFakePipe()
	rec := &recorder{}
	s, err := New(Options{Pipe: pipe, Backpressure: time.Second, Tick: 5 * time.Millisecond, OnFault: rec})
	if err != nil {
		t.Fatal(err)
	}
	pipe.err = errString("ffmpeg: conversion failed")
	close(pipe.exited)

	runErr := s.Run(context.Background())
	if runErr == nil {
		t.Fatal("the supervisor did not stop when the recorder exited")
	}
	faults := rec.all()
	if len(faults) != 1 || faults[0].Reason != ReasonStreamGap {
		t.Fatalf("faults = %+v, want one stream_gap", faults)
	}
	if !strings.Contains(faults[0].Detail, "not restarted") {
		t.Fatalf("the detail does not say the recorder was not restarted: %s", faults[0].Detail)
	}
	if pipe.kills() != 0 {
		t.Fatalf("the recorder was killed %d times after it had already exited", pipe.kills())
	}
}

func TestACleanEndOfInputIsStillADropoutForAStreakRun(t *testing.T) {
	// ffmpeg exiting zero is not an error, but for a run that expected to
	// continue it is still the end of the stream, and the report must say so.
	pipe := newFakePipe()
	close(pipe.exited)
	s, err := New(Options{Pipe: pipe, Backpressure: time.Second, Tick: 5 * time.Millisecond, OnFault: &recorder{}})
	if err != nil {
		t.Fatal(err)
	}
	runErr := s.Run(context.Background())
	if runErr == nil {
		t.Fatal("a recorder that ended on its own did not end the watch")
	}
	if !strings.Contains(runErr.Error(), "source ran out") {
		t.Fatalf("the reason is not the end of the source: %v", runErr)
	}
}

func TestAQuietPipeKeepsTheSupervisorRunning(t *testing.T) {
	pipe := newFakePipe()
	s, err := New(Options{Pipe: pipe, Backpressure: time.Second, Tick: 5 * time.Millisecond, OnFault: &recorder{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	if pipe.kills() != 0 {
		t.Fatal("a healthy pipe caused a kill")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a healthy run ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the supervisor did not stop on cancellation")
	}
}

func TestASidecarExitIsItsOwnFault(t *testing.T) {
	pipe := newFakePipe()
	side := &Sidecar{exited: make(chan struct{})}
	rec := &recorder{}
	s, err := New(Options{Pipe: pipe, Sidecar: side, Backpressure: time.Second, Tick: 5 * time.Millisecond, OnFault: rec})
	if err != nil {
		t.Fatal(err)
	}
	close(side.exited)
	runErr := s.Run(context.Background())
	if runErr == nil {
		t.Fatal("the supervisor did not stop when the sidecar exited")
	}
	faults := rec.all()
	if len(faults) != 1 || faults[0].Reason != ReasonSidecarExit {
		t.Fatalf("faults = %+v, want one sidecar_exit", faults)
	}
	if !strings.Contains(faults[0].Detail, "not_asked") {
		t.Fatalf("the detail does not say what a missing sidecar means for the answers: %s", faults[0].Detail)
	}
}

func TestStopIsNotAFault(t *testing.T) {
	rec := &recorder{}
	pipe := newFakePipe()
	s, err := New(Options{Pipe: pipe, Backpressure: time.Second, Tick: 5 * time.Millisecond, OnFault: rec})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a deliberate stop returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not end the watch")
	}
	if len(rec.all()) != 0 {
		t.Fatalf("a deliberate stop recorded %d faults", len(rec.all()))
	}
}

// --- sidecar configuration --------------------------------------------------

func TestSidecarProbeRefusesAMissingModel(t *testing.T) {
	// This is preflight's most valuable check: a run that starts without a
	// model dies in minute one and the report calls it a dropout.
	cfg := SidecarConfig{Python: pythonOnPath(t), Script: scriptOnPath(t), Model: "no-such-model.onnx"}
	err := cfg.Probe()
	if err == nil || !strings.Contains(err.Error(), "fetch-model") {
		t.Fatalf("a missing model was accepted: %v", err)
	}
}

func TestSidecarProbeRefusesAnEmptyModel(t *testing.T) {
	dir := t.TempDir()
	empty := dir + "/empty.onnx"
	writeFile(t, empty, nil)
	cfg := SidecarConfig{Python: pythonOnPath(t), Script: scriptOnPath(t), Model: empty}
	err := cfg.Probe()
	if err == nil || !strings.Contains(err.Error(), "zero bytes") {
		t.Fatalf("a zero-byte model was accepted: %v", err)
	}
}

func TestSidecarProbeRefusesAMissingScript(t *testing.T) {
	dir := t.TempDir()
	model := dir + "/m.onnx"
	writeFile(t, model, []byte("x"))
	cfg := SidecarConfig{Python: pythonOnPath(t), Script: "no-such-script.py", Model: model}
	if err := cfg.Probe(); err == nil {
		t.Fatal("a missing script was accepted")
	}
}

func TestSidecarArgsCarryHostPortAndModel(t *testing.T) {
	cfg := SidecarConfig{Python: "py", PythonArgs: []string{"-3"}, Script: "pyworker/server.py", Model: "m.onnx", Addr: "127.0.0.1:8099"}
	got := strings.Join(cfg.Args(), " ")
	for _, want := range []string{"-3", "pyworker/server.py", "--host 127.0.0.1", "--port 8099", "--model m.onnx"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the sidecar command %q is missing %q", got, want)
		}
	}
}

func TestSidecarArgsHandleAnIPv6Address(t *testing.T) {
	cfg := SidecarConfig{Python: "py", Script: "s.py", Model: "m.onnx", Addr: "[::1]:9000"}
	got := strings.Join(cfg.Args(), " ")
	if !strings.Contains(got, "--host ::1") || !strings.Contains(got, "--port 9000") {
		t.Fatalf("an IPv6 address was mangled: %q", got)
	}
}

// TestSidecarArgsTolerateASchemeOnTheAddress: the configuration variable is a
// URL because it is used as a URL everywhere else, and the sidecar's flag wants
// a bare host. Handing the sidecar "http://127.0.0.1" makes it refuse, and the
// refusal points at the sidecar rather than at the caller that built the
// argument.
func TestSidecarArgsTolerateASchemeOnTheAddress(t *testing.T) {
	for _, addr := range []string{"http://127.0.0.1:8099", "https://127.0.0.1:8099", "127.0.0.1:8099"} {
		cfg := SidecarConfig{Python: "py", Script: "s.py", Model: "m.onnx", Addr: addr}
		got := strings.Join(cfg.Args(), " ")
		if !strings.Contains(got, "--host 127.0.0.1") || !strings.Contains(got, "--port 8099") {
			t.Fatalf("%q produced %q; the scheme must not reach the sidecar's --host", addr, got)
		}
	}
	// An address with no port is refused by name rather than guessed at.
	cfg := SidecarConfig{Python: "py", Script: "s.py", Model: "m.onnx", Addr: "127.0.0.1"}
	host, port, err := splitHostPort(cfg.Addr)
	if err == nil {
		t.Fatalf("a portless address was accepted: %q %q", host, port)
	}
}

// pythonOnPath returns an interpreter that exists, so a test about the MODEL is
// not a test about the interpreter.
func pythonOnPath(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"py", "python", "python3"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	t.Skip("no Python interpreter on PATH on this machine")
	return ""
}

func scriptOnPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.py")
	writeFile(t, p, []byte("# stub\n"))
	return p
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
