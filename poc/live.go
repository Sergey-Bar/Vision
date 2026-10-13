package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/detectclient"
)

// The real path: resolved stream → ffmpeg frames → Python sidecar (the
// repo's own anchored-YOLOv4 decoder, which the in-process Go decoder refuses
// by design) → category/colour mapping → track ids → the shared fan-out.
//
// Every step is allowed to fail by name, and a failure degrades the camera
// that owns it: the tile says why it is dark and the rest of the fleet keeps
// watching. There is exactly one sidecar process shared by every camera (see
// ensureSidecar), and exactly one demo pseudo-camera that stands in when no
// real camera is producing frames.

// StartDemoSource runs the simulated source as the fleet's pseudo-camera
// ("sim"), under its own cancellable context so any camera that starts
// producing real frames can stand it down. It is the dashboard-alive
// courtesy: with nothing real to show, the demo runs rather than an empty
// page; with something real to show, it gets out of the way.
func (s *StreamIngest) StartDemoSource(parent context.Context) {
	if !s.demoRunning.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.demoCancel = cancel
	s.mu.Unlock()

	// The source line is not set here: statusSnapshot derives it from the
	// fleet's own counters (fleetSourceLine), which is why a status row can
	// never claim a source the fleet is not running. The demo source's
	// presence is one of the facts that derivation reads.

	go func() {
		s.StartDemoPipeline(ctx)
		cancel()
		s.demoRunning.Store(false)
	}()
}

// stopDemoSource stops the demo pseudo-camera, if it is running.
func (s *StreamIngest) stopDemoSource() {
	if !s.demoRunning.CompareAndSwap(true, false) {
		return
	}
	s.mu.Lock()
	cancel := s.demoCancel
	s.demoCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// mapDetections turns sidecar boxes (frame fractions) into categorised frame
// objects, naming vehicle colours from the captured pixels themselves.
func (s *StreamIngest) mapDetections(res detectclient.Result, frame []byte, minScore float64) []frameObject {
	out := make([]frameObject, 0, len(res.Boxes))
	for _, b := range res.Boxes {
		if b.Score < minScore {
			continue
		}
		cat, sub := mapCOCOClass(b.Class)
		box := wireBox{
			X:      b.X1 * frameW,
			Y:      b.Y1 * frameH,
			Width:  (b.X2 - b.X1) * frameW,
			Height: (b.Y2 - b.Y1) * frameH,
		}
		attrs := map[string]any{
			"coco_class": b.Class,
			"score":      b.Score,
			"model":      res.Reason, // the sidecar returns the model name here
		}
		if vehicleClasses[b.Class] {
			if color := estimateColor(frame, frameW, frameH, box); color != "" {
				attrs["color"] = color
			}
		}
		color, _ := attrs["color"].(string)
		out = append(out, frameObject{
			Category: cat,
			Sub:      sub,
			Label:    detectionLabel(cat, sub, color),
			Conf:     b.Score,
			BBox:     box,
			Attrs:    attrs,
		})
	}
	return out
}

// ensureSidecar reuses a healthy sidecar if one is already listening (the dev
// loop runs one by hand), otherwise starts pyworker/server.py and waits for
// /healthz — refusing by name when the interpreter, script or model is absent.
//
// It is the ONE sidecar for the whole fleet: every camera's Detect call goes
// to the same process, which is why the client is shared and the round trip
// serialized (see cameraFrameLoop).
func (s *StreamIngest) ensureSidecar(ctx context.Context) (*detectclient.Client, error) {
	addr := s.config.Live.SidecarAddr
	if addr == "" {
		addr = "127.0.0.1:8099"
	}
	base := "http://" + addr
	deadline := s.config.Live.Deadline
	if deadline <= 0 {
		deadline = 5 * time.Second
	}

	if client, err := detectclient.New(base, deadline); err == nil {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, perr := client.Probe(pctx)
		cancel()
		if perr == nil {
			log.Printf("sidecar: reusing the one already listening on %s", addr)
			return client, nil
		}
	}

	cfg := s.config.Live
	if cfg.Model == "" {
		return nil, fmt.Errorf("live.model is not configured (point it at an ONNX model)")
	}
	if _, err := os.Stat(cfg.Model); err != nil {
		return nil, fmt.Errorf("model file: %w", err)
	}
	script := cfg.SidecarScript
	if script == "" {
		return nil, fmt.Errorf("live.sidecar_script is not configured")
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("sidecar script: %w", err)
	}
	python, pyArgs, err := pythonCommand(cfg.Python)
	if err != nil {
		return nil, err
	}

	host, port := "127.0.0.1", "8099"
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		if h, p, ok := splitHostPort(u.Host); ok {
			host, port = h, p
		}
	}
	args := append([]string{}, pyArgs...)
	args = append(args, script, "--host", host, "--port", port, "--model", cfg.Model)
	if cfg.Anchors != "" {
		args = append(args, "--anchors", cfg.Anchors)
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Stderr = &lineLogger{prefix: "sidecar: "}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start sidecar (%s): %w", python, err)
	}
	log.Printf("sidecar: started pid=%d: %s %s", cmd.Process.Pid, python, strings.Join(args, " "))

	client, err := detectclient.New(base, deadline)
	if err != nil {
		return nil, err
	}
	ready := time.Now().Add(60 * time.Second)
	for time.Now().Before(ready) {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sidecar did not become ready: %w", ctx.Err())
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, perr := client.Probe(pctx)
		cancel()
		if perr == nil {
			return client, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("sidecar did not become ready: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("sidecar was not ready within 60s (model load on CPU can be slow)")
}

// pythonCommand picks the interpreter, preferring an explicit config value,
// then the py launcher, then python3/python — the same order the active-
// monitoring supervisor uses, for the same reason: a Windows Store stub named
// `python` must never be mistaken for the interpreter that has onnxruntime.
func pythonCommand(configured string) (string, []string, error) {
	if configured != "" {
		if _, err := exec.LookPath(configured); err == nil {
			return configured, nil, nil
		}
		return "", nil, fmt.Errorf("configured python %q is not on PATH", configured)
	}
	for _, c := range []struct {
		name string
		args []string
	}{{"py", []string{"-3"}}, {"python3", nil}, {"python", nil}} {
		if _, err := exec.LookPath(c.name); err == nil {
			return c.name, c.args, nil
		}
	}
	return "", nil, fmt.Errorf("no Python interpreter on PATH (tried py -3, python3, python)")
}

func splitHostPort(hostport string) (string, string, bool) {
	i := strings.LastIndex(hostport, ":")
	if i < 0 {
		return "", "", false
	}
	return hostport[:i], hostport[i+1:], true
}
