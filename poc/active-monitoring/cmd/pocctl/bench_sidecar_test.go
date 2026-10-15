package main

// Tests for the sidecar timing pass: the loopback p99 `pocctl preflight`'s AI
// row multiplies, measured over the same still set.
//
// Two controls, both the shape the calibration record exists for:
//
//   - the warm-up is DISCARDED. The first HTTP round trip pays for the worker's
//     own lazy state and the connection setup; a calibration that kept it
//     would refuse a sidecar that demonstrably answers. This is the discipline
//     the in-process pass already applies, and the sidecar pass inherits it by
//     construction rather than by hope.
//   - a REFUSAL is a wall. The client reports its refusals inside the Result
//     WITH a latency, so a loop that only collected latencies would measure the
//     cost of failing and call it the cost of answering.
//
// The double is a real HTTP server speaking the real wire format, because the
// latencies the pass collects are the client's own round trips; a stub that
// returned a Result would test the loop rather than the clock.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
)

// sidecarFake answers /healthz and /infer like the real worker. `first` is how
// long the FIRST inference takes and `rest` is every one after it â€” which is
// what makes the warm-up observable. When `degraded` is set, every inference
// answers 500, which the client turns into a degraded Result WITH a latency.
func sidecarFake(t *testing.T, model string, input int, providers []string, first, rest time.Duration, degraded bool) (*ai.Client, string) {
	t.Helper()
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ready": true, "model": model,
				"input_width": input, "input_height": input,
				"providers": providers,
			})
		case "/infer":
			seen++
			d := rest
			if seen == 1 {
				d = first
			}
			time.Sleep(d)
			if degraded {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("the model was not asked for"))
				return
			}
			// The wire format the real worker sends, fields and all: the
			// client decodes strictly, so a double that drifted would be
			// testing a protocol nobody runs. The seq is echoed from the
			// request, which is the correlation the client requires.
			seq, _ := strconv.Atoi(r.Header.Get("X-Sequence"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"seq":      seq,
				"boxes":    []any{},
				"width":    64,
				"height":   64,
				"model":    model,
				"infer_ms": 1,
				"telemetry": map[string]any{
					"layout": "nchw", "decoder_family": "fused", "coord_space": "pixel",
					"has_objectness": false, "fields": 84, "classes": 80,
					"candidates": 1, "pre_nms": 1, "post_nms_class": 0, "post_nms": 0,
					"dropped_degenerate": 0, "dropped_tiny": 0,
					"nms_class_iou": 0.45, "nms_global_iou": 0.75, "median_box_area": 0,
					"median_object_px": 0, "identical_geometry": 0, "scales": 1, "note": "",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := ai.New(srv.URL, sidecarBenchDeadline)
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	return client, srv.URL
}

// TestTheSidecarTimingPassDiscardsItsWarmUp is the red-check's subject.
//
// The sidecar's first round trip is slower than every one after it â€” the worker
// pays its lazy state once â€” and a calibration that kept it in the window would
// write a p99 that refuses a sidecar that demonstrably answers. The double
// takes 120ms on the first inference and 1ms on every one after: a window that
// includes the warm-up has a p99 of ~120ms, and one that discards it has ~1ms.
func TestTheSidecarTimingPassDiscardsItsWarmUp(t *testing.T) {
	client, _ := sidecarFake(t, "yolov4n.onnx", 416, []string{"CPUExecutionProvider"},
		120*time.Millisecond, time.Millisecond, false)
	rgb := make([]byte, 64*64*3)
	p50, p99, n, err := sidecarTimings(client, rgb, 64, 64, 5)
	if err != nil {
		t.Fatalf("the timing pass failed over a sidecar that answers: %v", err)
	}
	if n != 5 {
		t.Fatalf("the record reports %d frames over a 5-inference window, want 5; a warm-up that was "+
			"measured would make it 6", n)
	}
	if p99 > 20*time.Millisecond {
		t.Fatalf("p99 = %s, which is the warm-up's cost and not the steady state's: the first inference "+
			"(%s) was NOT discarded", p99, 120*time.Millisecond)
	}
	if p99 < p50 {
		t.Fatalf("p99 %s is below p50 %s over a window of identical round trips; the percentiles are "+
			"misordered, which is a benchmark reporting confidence about nothing", p99, p50)
	}
}

// TestTheSidecarTimingPassRefusesToMeasureRefusals is the other control.
//
// Every failed round trip comes back with a latency, and a pass that collected
// latencies without reading states would measure the cost of failing and call
// it the cost of answering â€” the row it feeds would then clear against the
// arithmetic of a sidecar that answered nothing at all.
func TestTheSidecarTimingPassRefusesToMeasureRefusals(t *testing.T) {
	client, _ := sidecarFake(t, "yolov4n.onnx", 416, []string{"CPUExecutionProvider"},
		time.Millisecond, time.Millisecond, true)
	rgb := make([]byte, 64*64*3)
	_, _, _, err := sidecarTimings(client, rgb, 64, 64, 3)
	if err == nil {
		t.Fatal("a window of failed round trips produced a calibration")
	}
	for _, want := range []string{"REFUSED", "the cost of failing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestTheSidecarCalibrationNamesItsPathAndItsSidecar: the record the sidecar
// pass writes carries the trigger, the address and the providers â€” the fields
// `Applies` compares â€” because a loopback p99 without its sidecar is a number
// about no machine in particular.
func TestTheSidecarCalibrationNamesItsPathAndItsSidecar(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH on this machine; the still set's decode is the one part of this " +
			"pass that shells out, and the timing discipline it guards is covered above without it")
	}
	root, err := pocModuleRoot()
	if err != nil {
		t.Skipf("the module root could not be found, so the still set could not be located: %v", err)
	}
	client, url := sidecarFake(t, "yolov4n.onnx", 416, []string{"CPUExecutionProvider"},
		time.Millisecond, time.Millisecond, false)
	cal, line, err := sidecarCalibration(client,
		stillImage{Path: filepath.Join(root, "testdata", "still", "injection-hostile.png"), Name: "injection-hostile.png"},
		3, "models/yolov4n.onnx", url, "CPUExecutionProvider", 416, "abc123")
	if err != nil {
		t.Skipf("the still set could not be decoded on this machine: %v", err)
	}
	if cal.Trigger != "sidecar" {
		t.Errorf("trigger = %q, want sidecar: the record must say which path measured it", cal.Trigger)
	}
	if cal.Address != url {
		t.Errorf("address = %q, want the sidecar that was measured (%s)", cal.Address, url)
	}
	if cal.Library != "CPUExecutionProvider" || cal.Input != 416 || cal.Threads != 0 {
		t.Errorf("the record's build fields are %+v", cal)
	}
	if cal.Model != "models/yolov4n.onnx" || cal.ModelSHA256 != "abc123" {
		t.Errorf("the record does not name the model it was measured through: %+v", cal)
	}
	if cal.Frames != 3 {
		t.Errorf("frames = %d, want 3", cal.Frames)
	}
	if !strings.Contains(line, "discarded as the warm-up") {
		t.Errorf("the bench line does not say the warm-up was discarded: %q", line)
	}
}
