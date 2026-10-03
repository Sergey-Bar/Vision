package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSidecar is a stand-in for the Python worker. It is used here to test the
// Go side's behaviour under the failures the real worker can produce; the real
// worker has its own tests in pyworker/test_server.py.
type fakeSidecar struct {
	handler func(w http.ResponseWriter, r *http.Request)
	calls   atomic.Int64
	seq     atomic.Uint64
}

func (f *fakeSidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"model":"fake","input_width":640,"input_height":384,"providers":["CPUExecutionProvider"]}`))
		return
	}
	if f.handler != nil {
		f.handler(w, r)
		return
	}
	seq := f.seq.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Response{Seq: seq, Boxes: []Box{}, Width: 640, Height: 384, Model: "fake", Telemetry: healthyTelemetry()})
}

func newClient(t *testing.T, h func(http.ResponseWriter, *http.Request)) (*Client, *httptest.Server, *fakeSidecar) {
	t.Helper()
	f := &fakeSidecar{handler: h}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv, f
}

func rgb(w, h int) []byte { return make([]byte, w*h*3) }

func TestDetectEmptyAnswerIsNoObjects(t *testing.T) {
	c, _, f := newClient(t, nil)
	// The default handler echoes an incrementing counter; seed it so the
	// answer's seq matches the request's.
	f.seq.Store(6)
	r := c.Detect(context.Background(), 7, rgb(2, 2), 2, 2)
	if r.State != StateNoObjects {
		t.Fatalf("state = %q, want %q", r.State, StateNoObjects)
	}
	if len(r.Boxes) != 0 {
		t.Fatalf("a no_objects result carries %d boxes", len(r.Boxes))
	}
}

func TestDetectWithBoxesIsAnswered(t *testing.T) {
	c, _, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"seq":1,"boxes":[{"cls":"bird","score":0.87,"x1":0.1,"y1":0.2,"x2":0.3,"y2":0.4}],"width":640,"height":384,"model":"yolov4n","infer_ms":12.5,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
	})
	r := c.Detect(context.Background(), 1, rgb(2, 2), 2, 2)
	if r.State != StateAnswered {
		t.Fatalf("state = %q, want %q (%s)", r.State, StateAnswered, r.Reason)
	}
	if len(r.Boxes) != 1 || r.Boxes[0].Class != "bird" {
		t.Fatalf("boxes = %+v", r.Boxes)
	}
}

// TestMalformedAnswerIsNeverNoObjects is the AC6 test. A body that is not the
// documented shape, a non-200, a truncated JSON body and an unexpected field
// must all be absences, and none of them may read as "nothing was there".
func TestMalformedAnswerIsNeverNoObjects(t *testing.T) {
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		want    string
	}{
		{"not json", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("this is not json"))
		}, StateCannotTell},
		{"truncated json", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":[{"cls":"bird",`))
		}, StateCannotTell},
		{"null boxes is not an empty list", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":null,"width":640,"height":384,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
		}, StateCannotTell},
		{"unknown field", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":[],"width":640,"height":384,"model":"x","infer_ms":1,"surprise":42}`))
		}, StateCannotTell},
		{"score out of range", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":[{"cls":"bird","score":1.4,"x1":0,"y1":0,"x2":1,"y2":1}],"width":640,"height":384,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
		}, StateCannotTell},
		{"inverted box", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":[{"cls":"bird","score":0.5,"x1":0.9,"y1":0.9,"x2":0.1,"y2":0.1}],"width":640,"height":384,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
		}, StateCannotTell},
		{"no class", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":1,"boxes":[{"cls":"","score":0.5,"x1":0,"y1":0,"x2":1,"y2":1}],"width":640,"height":384,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
		}, StateCannotTell},
		{"answer for another frame", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"seq":99,"boxes":[],"width":640,"height":384,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
		}, StateCannotTell},
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("model exploded"))
		}, StateDegraded},
		{"refused", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("loading model"))
		}, StateDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newClient(t, tc.handler)
			r := c.Detect(context.Background(), 1, rgb(2, 2), 2, 2)
			if r.State == StateNoObjects {
				t.Fatalf("a broken answer was reported as no_objects: %s", r.Reason)
			}
			if r.State != tc.want {
				t.Fatalf("state = %q, want %q (%s)", r.State, tc.want, r.Reason)
			}
			if AbsenceOf(r.State) != true {
				t.Fatalf("state %q is not classified as an absence", r.State)
			}
			if r.Reason == "" {
				t.Fatal("an absence with no reason: the report has to print why")
			}
		})
	}
}

func TestTimeoutIsDegradedNotEmpty(t *testing.T) {
	c, _, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	})
	r := c.Detect(context.Background(), 1, rgb(2, 2), 2, 2)
	if r.State != StateDegraded {
		t.Fatalf("a slow answer produced %q, want %q", r.State, StateDegraded)
	}
	if !strings.Contains(r.Reason, "unknown, not empty") {
		t.Fatalf("the reason does not distinguish unknown from empty: %s", r.Reason)
	}
}

func TestSidecarGoneIsDegraded(t *testing.T) {
	c, srv, _ := newClient(t, nil)
	srv.Close()
	r := c.Detect(context.Background(), 1, rgb(2, 2), 2, 2)
	if r.State != StateDegraded {
		t.Fatalf("a dead sidecar produced %q", r.State)
	}
}

func TestCorruptFrameIsNotAskedAndNeverSent(t *testing.T) {
	c, _, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the sidecar was called for a frame that was never valid")
	})
	r := c.Detect(context.Background(), 1, []byte{1, 2, 3}, 2, 2)
	if r.State != StateNotAsked {
		t.Fatalf("state = %q, want %q", r.State, StateNotAsked)
	}
	if !strings.Contains(r.Reason, "corrupt") {
		t.Fatalf("reason = %s", r.Reason)
	}
}

func TestZeroDimensionFrameIsNotAsked(t *testing.T) {
	c, _, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the sidecar was called for a frame with no dimensions")
	})
	r := c.Detect(context.Background(), 1, nil, 0, 0)
	if r.State != StateNotAsked {
		t.Fatalf("state = %q", r.State)
	}
}

func TestAbsenceOfCoversExactlyTheThreeAbsences(t *testing.T) {
	for _, s := range []string{StateDegraded, StateNotAsked, StateCannotTell} {
		if !AbsenceOf(s) {
			t.Fatalf("%q must be an absence", s)
		}
	}
	for _, s := range []string{StateAnswered, StateNoObjects} {
		if AbsenceOf(s) {
			t.Fatalf("%q must not be an absence", s)
		}
	}
}

func TestNewRefusesAnUnusableAddressOrDeadline(t *testing.T) {
	if _, err := New("127.0.0.1:8099", time.Second); err == nil {
		t.Fatal("a bare host:port was accepted as a sidecar address")
	}
	if _, err := New("http://127.0.0.1:8099", 0); err == nil {
		t.Fatal("a zero deadline was accepted")
	}
}

func TestProbeReportsNotReadyAsAnError(t *testing.T) {
	f := &fakeSidecar{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ready":false,"model":"","input_width":0,"input_height":0,"providers":[]}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, time.Second)
	if _, err := c.Probe(context.Background()); err == nil {
		t.Fatal("a not-ready sidecar passed the probe; preflight must refuse the run")
	} else if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("error = %v", err)
	}
	_ = f
}

func TestProbeReportsAnUnreadableHealthBodyAsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not the sidecar</html>"))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, time.Second)
	_, err := c.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not usable JSON") {
		t.Fatalf("an HTML health response passed the probe: %v", err)
	}
}

func TestPercentilesRefuseToInventAZero(t *testing.T) {
	l := NewLatencies(1000)
	if _, _, _, n, ok := l.Percentiles(); ok || n != 0 {
		t.Fatalf("an empty reservoir reported ok=%v n=%d", ok, n)
	}
	for i := 1; i <= 100; i++ {
		l.Add(time.Duration(i) * time.Millisecond)
	}
	p50, p95, p99, n, ok := l.Percentiles()
	if !ok || n != 100 {
		t.Fatalf("n = %d ok = %v", n, ok)
	}
	if p50 != 50*time.Millisecond {
		t.Fatalf("p50 = %s, want 50ms", p50)
	}
	if p95 < 90*time.Millisecond || p95 > 100*time.Millisecond {
		t.Fatalf("p95 = %s", p95)
	}
	// Nearest rank: p99 of 100 samples 1..100ms is the 99th, which is 99ms.
	// Reporting 100ms here would mean the percentile is quietly an "at most".
	if p99 != 99*time.Millisecond {
		t.Fatalf("p99 = %s, want 99ms", p99)
	}
}

func TestReservoirIsBoundedAndKeepsRecentSamples(t *testing.T) {
	l := NewLatencies(4)
	for i := 1; i <= 10; i++ {
		l.Add(time.Duration(i) * time.Millisecond)
	}
	_, _, _, n, _ := l.Percentiles()
	if n != 4 {
		t.Fatalf("n = %d, want 4", n)
	}
	// The ring keeps the newest, so the recent behaviour is what gets reported.
	if _, _, p99, _, _ := l.Percentiles(); p99 != 10*time.Millisecond {
		t.Fatalf("p99 = %s, want 10ms: the reservoir kept the oldest samples", p99)
	}
}

// TestTheWireFormatIsABareOctetStream pins the sidecar's contract: the body is
// exactly the pixels and the shape is in the headers. A header line inside the
// payload would be one more thing for the sidecar to parse, on the side that
// has the least reason to parse anything, and one more thing that can drift.
func TestTheWireFormatIsABareOctetStream(t *testing.T) {
	var gotLen int
	var gotCT, gotW, gotH, gotSeq, gotSub string
	var firstBytes []byte
	c, _, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotW = r.Header.Get("X-Frame-Width")
		gotH = r.Header.Get("X-Frame-Height")
		gotSeq = r.Header.Get("X-Sequence")
		gotSub = r.Header.Get("X-Subformat")
		buf, _ := io.ReadAll(r.Body)
		gotLen = len(buf)
		if len(buf) > 3 {
			firstBytes = buf[:3]
		}
		_, _ = w.Write([]byte(`{"seq":3,"boxes":[],"width":4,"height":2,"model":"x","infer_ms":1,"telemetry":{"layout":"nchw","decoder_family":"fused","coord_space":"pixel","has_objectness":false,"fields":84,"classes":80,"candidates":1200,"pre_nms":9,"post_nms_class":4,"post_nms":4,"dropped_degenerate":0,"dropped_tiny":0,"nms_class_iou":0.45,"nms_global_iou":0.75,"median_box_area":0.004,"median_object_px":24.5,"identical_geometry":0,"scales":1,"note":""}}`))
	})
	pix := make([]byte, 4*2*3)
	for i := range pix {
		pix[i] = byte(i)
	}
	r := c.Detect(context.Background(), 3, pix, 4, 2)
	if r.State != StateNoObjects {
		t.Fatalf("state = %q", r.State)
	}
	if gotCT != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", gotCT)
	}
	if gotW != "4" || gotH != "2" || gotSeq != "3" || gotSub != "rgb" {
		t.Fatalf("headers: width=%q height=%q seq=%q subformat=%q", gotW, gotH, gotSeq, gotSub)
	}
	if gotLen != 24 {
		t.Fatalf("the body is %d bytes, want exactly 24: a prefix would mean the sidecar has to parse before it can look at a frame", gotLen)
	}
	if len(firstBytes) == 3 && (firstBytes[0] != 0 || firstBytes[1] != 1 || firstBytes[2] != 2) {
		t.Fatalf("the body's first bytes are %v, want the first three pixels verbatim", firstBytes)
	}
}

func TestEveryAbsenceCarriesAReason(t *testing.T) {
	c, srv, _ := newClient(t, nil)
	srv.Close()
	r := c.Detect(context.Background(), 1, rgb(2, 2), 2, 2)
	if r.Reason == "" {
		t.Fatal("a dead sidecar produced no reason")
	}
	if r.State != StateDegraded {
		t.Fatalf("a dead sidecar produced %q, want %q", r.State, StateDegraded)
	}
	// A refused connection can complete in less than the clock's resolution, so
	// the sample is legitimately zero. This test used to require a positive
	// latency and it failed roughly one run in four for that reason. Zero is a
	// measurement: it is what a connection refused immediately actually cost.
	// Treating it as "not measured" would put a floor under a number the plan
	// says must be a measurement.
	lat := NewLatencies(4)
	lat.Add(r.Latency)
	p50, _, _, n, ok := lat.Percentiles()
	if !ok || n != 1 {
		t.Fatalf("a zero-latency sample was not recorded: n=%d ok=%v", n, ok)
	}
	if p50 != r.Latency {
		t.Fatalf("the reservoir reported %s for a sample of %s", p50, r.Latency)
	}
}
