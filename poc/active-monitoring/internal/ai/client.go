// Package ai is the Go side of the sidecar. It sends raw RGB frames to a Python
// process over loopback HTTP and parses the JSON that comes back.
//
// The package has one job beyond moving bytes: it never lets an absence become
// an answer. A frame that was never sent is not_asked. A frame that was sent
// and came back malformed, late, or from a failing read is degraded. A frame
// whose response parsed but cannot be interpreted is cannot_tell. Only a
// response the model actually produced, with an empty array, is no_objects.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is the AI absence vocabulary from CONTRACT.md §2.2. These four strings
// are the only ones that may appear in detections.ai_state.
const (
	StateAnswered   = "asked_answered"
	StateNoObjects  = "no_objects"
	StateDegraded   = "degraded"
	StateNotAsked   = "not_asked"
	StateCannotTell = "cannot_tell"
)

// AbsenceOf reports whether a state means "we do not know whether anything was
// there". It is the predicate AC6 uses: a healthy answer derived from a failed
// read is the thing AC6 forbids.
func AbsenceOf(state string) bool {
	switch state {
	case StateDegraded, StateNotAsked, StateCannotTell:
		return true
	}
	return false
}

// Box is one detection, in normalised coordinates so the model input size
// cannot change what a box means.
type Box struct {
	Class string  `json:"cls"`
	Score float64 `json:"score"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
}

// Result is one sidecar answer.
type Result struct {
	State  string
	Boxes  []Box
	Reason string
	// Latency is the wall clock of the round trip, which is what the p50/p95/p99
	// budget in AC6 is computed from.
	Latency time.Duration
	// Seq is the frame sequence number, echoed for correlation. A mismatch
	// between the sent and received seq is a protocol error, not a detection.
	Seq uint64
}

// NoObjects is a real answer: the model ran, parsed, and found nothing. It is
// the only result with an empty box list that is not a failure.
func (r Result) NoObjects() bool { return r.State == StateNoObjects }

// Response is the sidecar's wire format. It is declared strictly: every field
// is required, and a missing one is a protocol error rather than a default.
type Response struct {
	Seq     uint64  `json:"seq"`
	Boxes   []Box   `json:"boxes"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Model   string  `json:"model"`
	InferMS float64 `json:"infer_ms"`
}

// Client talks to the sidecar.
type Client struct {
	BaseURL  string
	HTTP     *http.Client
	Deadline time.Duration
	Logf     func(string, ...any)
}

// New builds a client. The deadline is mandatory: a sidecar that never answers
// must produce a `degraded` result on a timer, not a worker that hangs forever.
func New(baseURL string, deadline time.Duration) (*Client, error) {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("ai: sidecar address %q is not an http or https URL; the sidecar is a loopback service and there is no third form", baseURL)
	}
	if deadline <= 0 {
		return nil, fmt.Errorf("ai: the deadline must be positive; without one a slow answer is indistinguishable from no answer, which breaks R4")
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		// The HTTP timeout is the deadline plus a margin, so the request
		// transport itself cannot outlive the decision the caller makes about
		// it.
		HTTP:     &http.Client{Timeout: deadline + 2*time.Second},
		Deadline: deadline,
		Logf:     func(string, ...any) {},
	}, nil
}

// Probe checks that the sidecar is up and that its model is loaded. Preflight
// calls this before the run, because a model that fails to load is a crash in
// minute one.
type ProbeResult struct {
	Ready     bool
	Model     string
	InputSize [2]int
	Providers []string
	Detail    string
}

func (c *Client) Probe(ctx context.Context) (ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ai: cannot build the health request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ProbeResult{Detail: err.Error()}, fmt.Errorf("ai: the sidecar at %s is not answering: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return ProbeResult{Detail: string(body)}, fmt.Errorf("ai: the sidecar answered %s on /healthz: %s", resp.Status, firstLine(string(body)))
	}
	var out struct {
		Ready     bool     `json:"ready"`
		Model     string   `json:"model"`
		InputW    int      `json:"input_width"`
		InputH    int      `json:"input_height"`
		Providers []string `json:"providers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ProbeResult{Detail: string(body)}, fmt.Errorf("ai: the sidecar's health response is not usable JSON, so we do not know whether the model is loaded: %w", err)
	}
	if !out.Ready {
		return ProbeResult{Model: out.Model, Detail: "ready=false"}, fmt.Errorf("ai: the sidecar reports it is not ready; a run started now would degrade every frame")
	}
	return ProbeResult{Ready: true, Model: out.Model, InputSize: [2]int{out.InputW, out.InputH}, Providers: out.Providers}, nil
}

// Detect sends one frame and returns a Result whose State is one of the five
// vocabulary words. It never returns a Result with State "" and it never
// returns StateNoObjects for a failure.
func (c *Client) Detect(ctx context.Context, seq uint64, rgb []byte, w, h int) Result {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.Deadline)
	defer cancel()
	if w <= 0 || h <= 0 {
		return Result{State: StateNotAsked, Reason: fmt.Sprintf("frame %d has no dimensions (%dx%d), so there was nothing to ask about", seq, w, h)}
	}
	if want := w * h * 3; len(rgb) != want {
		return Result{State: StateNotAsked, Seq: seq, Reason: fmt.Sprintf("frame %d carries %d bytes for %dx%d RGB, want %d; the frame is corrupt and was not sent", seq, len(rgb), w, h, want)}
	}

	// The wire format is a bare octet stream: exactly the pixels, and the
	// shape in the headers. No header line inside the payload, so the sidecar
	// needs no JSON parser to receive a frame and the format cannot drift with a
	// codec version.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/infer", bytes.NewReader(rgb))
	if err != nil {
		return Result{State: StateNotAsked, Seq: seq, Reason: fmt.Sprintf("frame %d could not be sent: %v", seq, err)}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Subformat", "rgb")
	req.Header.Set("X-Frame-Width", strconv.Itoa(w))
	req.Header.Set("X-Frame-Height", strconv.Itoa(h))
	req.Header.Set("X-Sequence", strconv.FormatUint(seq, 10))

	resp, err := c.HTTP.Do(req)
	lat := time.Since(start)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// A late answer is `degraded`, never `no_objects`. Reporting it as
			// "nothing there" would be inventing a negative observation, which
			// is exactly what R4 forbids.
			return Result{State: StateDegraded, Seq: seq, Latency: lat,
				Reason: fmt.Sprintf("frame %d was not answered within %s; the answer is unknown, not empty", seq, c.Deadline)}
		}
		return Result{State: StateDegraded, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d could not be delivered to the sidecar: %v", seq, err)}
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return Result{State: StateDegraded, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d: the sidecar's answer could not be read (%v); the answer is unknown, not empty", seq, readErr)}
	}
	if resp.StatusCode != http.StatusOK {
		return Result{State: StateDegraded, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d: the sidecar answered %s: %s", seq, resp.Status, firstLine(string(raw)))}
	}

	var out Response
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return Result{State: StateCannotTell, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d: the sidecar's answer is not the documented shape (%v); it is not evidence that the frame was empty", seq, err)}
	}
	if out.Seq != seq {
		// Correlation is how a stale answer is caught. A mismatch means the
		// answer belongs to a different frame, and using it would attribute one
		// frame's boxes to another frame's instant.
		return Result{State: StateCannotTell, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d: the sidecar answered for frame %d, so the boxes belong to a different instant", seq, out.Seq)}
	}
	if out.Boxes == nil {
		return Result{State: StateCannotTell, Seq: seq, Latency: lat,
			Reason: fmt.Sprintf("frame %d: the sidecar's answer has no boxes array; absent and empty are different facts", seq)}
	}
	for i, b := range out.Boxes {
		if b.Class == "" {
			return Result{State: StateCannotTell, Seq: seq, Latency: lat,
				Reason: fmt.Sprintf("frame %d box %d has no class", seq, i)}
		}
		if math.IsNaN(b.Score) || b.Score < 0 || b.Score > 1 {
			return Result{State: StateCannotTell, Seq: seq, Latency: lat,
				Reason: fmt.Sprintf("frame %d box %d has score %v, which is not a probability", seq, i, b.Score)}
		}
		if b.X2 < b.X1 || b.Y2 < b.Y1 {
			return Result{State: StateCannotTell, Seq: seq, Latency: lat,
				Reason: fmt.Sprintf("frame %d box %d is inverted: (%v,%v)-(%v,%v)", seq, i, b.X1, b.Y1, b.X2, b.Y2)}
		}
	}
	state := StateAnswered
	if len(out.Boxes) == 0 {
		state = StateNoObjects
	}
	return Result{State: state, Boxes: out.Boxes, Seq: seq, Latency: lat, Reason: out.Model}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// Latencies keeps the round-trip samples and computes the percentiles AC6
// scores. It is bounded, because a week of samples at 5 fps is 3 million and a
// report only needs the distribution.
type Latencies struct {
	mu      sync.Mutex
	samples []time.Duration
	limit   int
}

// NewLatencies builds a bounded reservoir.
func NewLatencies(limit int) *Latencies {
	if limit <= 0 {
		limit = 100000
	}
	return &Latencies{limit: limit}
}

// Add records one sample.
func (l *Latencies) Add(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.samples) >= l.limit {
		// Ring behaviour: overwrite the oldest. A week-long run must not have
		// its early behaviour silently win the average.
		copy(l.samples, l.samples[1:])
		l.samples[len(l.samples)-1] = d
		return
	}
	l.samples = append(l.samples, d)
}

// Percentiles returns p50, p95 and p99 over the recorded samples. With no
// samples it reports ok=false, because a percentile over nothing must not render
// as 0 milliseconds.
func (l *Latencies) Percentiles() (p50, p95, p99 time.Duration, n int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n = len(l.samples)
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	s := make([]time.Duration, n)
	copy(s, l.samples)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return percentile(s, 0.50), percentile(s, 0.95), percentile(s, 0.99), n, true
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
