// Package airuntime is the Go client for deploy/ai-runtime's HTTP surface.
//
// # Why this is a separate process and this is the only thing that talks to it
//
// The AI plane is reached over HTTP precisely so a hung inference cannot hang
// ingest. A library call inside the core cannot give that: a goroutine blocked in
// a native inference call holds a worker, and a worker holding a lease is a
// pipeline that stops. Over a socket, a deadline is enforceable - which is why
// this package has a client-side deadline even though the runtime accepts one
// and never applies it.
//
// # What this package must not import
//
// Nothing from the deterministic core. This is a consumer of the AI plane, not
// a participant in the decision path: an observation is a recorded second
// opinion on a decision already made, and it never rewrites it. That is
// invariant 2, and importing the rule engine or the incident store here would
// make the boundary a convention rather than a structural property.
//
// observations is imported, deliberately. It is the AI plane's own persistence
// vocabulary — the Degradation set is defined there and in the schema's CHECK
// constraint, and a second copy of that vocabulary in this package is a second
// set of answers to "what does degraded mean".
package airuntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/observations"
)

// APIVersion is the wire version this client speaks.
//
// It is an integer on the wire, not a string. runtime.py compares
// `payload.get("apiVersion") != API_VERSION` against the integer 1, so a client
// that sent "1" gets a 400 with a message about an unsupported version — which
// is exactly the kind of failure that reads as "the runtime is broken" rather
// than "the client sent the wrong thing".
const APIVersion = 1

// MaxResponseBytes bounds a response body. The runtime answers in well under a
// kilobyte; a cap is here because a peer that is not the runtime could stream
// without end, and a client that allocates until the peer stops is a way to
// consume the core.
const MaxResponseBytes = 4 << 20

// ErrNoEndpoint is returned when no runtime is configured.
//
// It is distinct from a connection failure on purpose. "The operator never
// configured an AI plane" and "the AI plane is down" are different facts, they
// produce different degradation reasons, and a pilot measuring AI availability
// cannot tell them apart if they are collapsed.
var ErrNoEndpoint = errors.New("airuntime: no endpoint is configured")

// Client calls the runtime.
type Client struct {
	// BaseURL is the runtime's root, without a trailing slash. Empty means no
	// runtime is configured and every call returns ErrNoEndpoint.
	BaseURL string
	// Timeout is the client-side deadline for one inference. The runtime accepts
	// deadlineSeconds and never enforces it - there is no socket timeout anywhere
	// in it - so this is the only bound that exists.
	Timeout time.Duration
	// HTTPClient is the transport. Nil uses a client with sensible timeouts.
	HTTPClient *http.Client
	// AllowRemoteHost permits a non-loopback endpoint.
	//
	// runtime.py has no authentication of any kind. Anything that can reach the
	// socket can make the core send evidence to it and can read the answer. On a
	// site with one network segment that is a real exposure, so a remote endpoint
	// is refused unless an operator says otherwise. This is a threat-model
	// decision, not a convenience check.
	AllowRemoteHost bool
}

// New builds a client.
//
// A non-loopback endpoint with AllowRemoteHost unset is refused here rather than
// at first call, so an operator learns at startup rather than when the first
// incident is waiting for its second opinion.
func New(baseURL string, timeout time.Duration, allowRemote bool) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return &Client{Timeout: timeout, AllowRemoteHost: allowRemote}, nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("airuntime: %q is not a URL: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("airuntime: %q must be an http:// or https:// URL", baseURL)
	}
	if !allowRemote && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("airuntime: refusing to send evidence to %s: the AI runtime has "+
			"no authentication of any kind, so any host that can reach the socket can read "+
			"the site's evidence. Bind it to loopback, or set SP_AI_ALLOW_REMOTE=true if "+
			"something in front of it terminates and authorises the connection", u.Hostname())
	}
	return &Client{
		BaseURL:         strings.TrimRight(baseURL, "/"),
		Timeout:         timeout,
		HTTPClient:      &http.Client{Timeout: timeout},
		AllowRemoteHost: allowRemote,
	}, nil
}

// isLoopbackHost reports whether a host is this machine.
//
// The names are enumerated rather than resolved: a DNS lookup here would make
// startup depend on a resolver, and a site has no DNS by design.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Evidence is one piece of input to an inference.
type Evidence struct {
	ID          string
	SHA256      string
	ContentType string
	Data        []byte
}

// InvestigatorProfileSchema declares the field set the shipped investigate engine
// emits: answer, citations, confidence. It mirrors runtime.INVESTIGATOR_SCHEMA in
// deploy/ai-runtime/runtime.py and the INVESTIGATOR_PROFILE contract.
//
// The runtime validates the engine's output against the schema the caller sends
// and degrades the response on a mismatch, so a request that sent no schema left
// validate_output with an empty property set -- and every answer, however well
// cited, degraded on "the profile schema declares no properties". That is why a
// site could run a real engine and still never record a single non-degraded
// observation. Sending this schema is what makes a grounded answer keepable; the
// two copies are the same contract by design, and the Python suite is the
// authority they are kept in step with.
var InvestigatorProfileSchema = map[string]any{
	"properties": map[string]any{
		"answer":     map[string]any{"type": "string"},
		"citations":  map[string]any{"type": "array"},
		"confidence": map[string]any{"type": "number"},
	},
	"required": []any{"answer", "citations"},
}

// ObjectDetectionProfileSchema declares the output field set for object
// detection: a list of detections, each with a label, bounding box, and
// confidence.
var ObjectDetectionProfileSchema = map[string]any{
	"properties": map[string]any{
		"detections": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label":      map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "number"},
					"bbox": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "number"},
					},
				},
				"required": []any{"label", "confidence", "bbox"},
			},
		},
	},
	"required": []any{"detections"},
}

// ObjectDetectionProfile is the profile ID used for object detection.
const ObjectDetectionProfile = "object-detection/v1"

// Detection is one object detected in a frame.
type Detection struct {
	Label      string    `json:"label"`
	Confidence float64   `json:"confidence"`
	BBox       []float64 `json:"bbox"`
}

// DetectObjects sends an image frame to the runtime for object detection.
//
// Evidence must contain exactly one item: the image bytes with a SHA-256
// hash. On a degraded response (model unavailable, no runtime, etc.), the
// returned slice is nil and the Degraded field carries the reason.
func (c *Client) DetectObjects(ctx context.Context, observationID string, evidence []Evidence) ([]Detection, *Result, error) {
	if len(evidence) != 1 {
		return nil, nil, errors.New("airuntime: DetectObjects requires exactly one evidence item")
	}
	if evidence[0].SHA256 == "" {
		return nil, nil, errors.New("airuntime: evidence item must carry a SHA-256 hash")
	}

	result, err := c.Do(ctx, Request{
		ObservationID: observationID,
		ProfileID:     ObjectDetectionProfile,
		Evidence:      evidence,
		ProfileSchema: ObjectDetectionProfileSchema,
	})
	if err != nil {
		// Do returns a degraded result on transport failure, with err set.
		if result.Degraded {
			return nil, &result, nil
		}
		return nil, &result, err
	}
	if result.Degraded {
		return nil, &result, nil
	}

	raw, ok := result.Structured["detections"]
	if !ok {
		return nil, &result, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, &result, nil
	}
	out := make([]Detection, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		d := Detection{}
		if label, ok := m["label"].(string); ok {
			d.Label = label
		}
		if conf, ok := m["confidence"].(float64); ok {
			d.Confidence = conf
		}
		if bbox, ok := m["bbox"].([]any); ok {
			for _, v := range bbox {
				if f, ok := v.(float64); ok {
					d.BBox = append(d.BBox, f)
				}
			}
		}
		out = append(out, d)
	}
	return out, &result, nil
}

// Request is one inference.
type Request struct {
	ObservationID string
	ProfileID     string
	Evidence      []Evidence
	// ProfileSchema declares the output's field set. The runtime validates the
	// answer against it and degrades the response if the answer names a field the
	// schema does not declare - so sending it is what makes an undeclared field
	// visible rather than silently stored.
	ProfileSchema map[string]any
}

// Result is what the runtime answered.
type Result struct {
	ObservationID string
	ProfileID     string
	// Structured is the validated output. Only meaningful when Degraded is false.
	Structured           map[string]any
	InsufficientEvidence bool
	// Degraded is the runtime's own statement that it could not answer. This is
	// the single most important field in the response.
	Degraded       bool
	DegradedReason observations.Degradation
	DegradedDetail string

	Model                string
	ModelHash            string
	RuntimeVersion       string
	PromptTemplateID     string
	PromptVersion        string
	ModelBundleSignature string
	InputEvidenceHashes  []string
	OutputSchemaVersion  int
	LatencyMS            int64
	Hardware             string
	OutputHash           string

	// ModelIdentity is what the provider *said* it used, for a provider that will
	// not give us weights to hash.
	//
	// It is a claim, not a verification, and it is stored as a claim. A hosted
	// VLM returns a model name and a response; its weights are not ours, are not
	// addressable, and change on the provider's schedule. So the honest record of
	// that answer names the model the API reported and leaves ModelHash empty,
	// rather than inventing a hash that would satisfy a column check and defeat
	// the one thing the column is for.
	//
	// Empty for a provider that does have a hashable bundle, where ModelHash is
	// the better answer and this stays empty.
	ModelIdentity string
	// ModelContractVersion is this build's content hash over everything it
	// controls about the call: the prompt template, the prompt version, the
	// output schema version and the API model name. It is reproducible from the
	// repository, which is what makes it worth having.
	ModelContractVersion string
	// Temperature is the sampling temperature that was requested. It is part of
	// attribution: an answer produced at 0.9 is not the answer produced at 0.01.
	Temperature float64
}

// wireRequest is the exact shape runtime.py decodes.
type wireRequest struct {
	APIVersion      int            `json:"apiVersion"`
	ObservationID   string         `json:"observationId"`
	ProfileID       string         `json:"profileId"`
	Evidence        []wireEvidence `json:"evidence"`
	ProfileSchema   map[string]any `json:"profileSchema,omitempty"`
	DeadlineSeconds float64        `json:"deadlineSeconds"`
}

type wireEvidence struct {
	EvidenceID  string `json:"evidenceId"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
	DataBase64  string `json:"dataBase64"`
}

// wireResponse is the exact shape runtime.py emits, in both the real and the
// degraded form. The two differ only in which fields carry values.
type wireResponse struct {
	APIVersion           int            `json:"apiVersion"`
	ObservationID        string         `json:"observationId"`
	ProfileID            string         `json:"profileId"`
	StructuredOutput     map[string]any `json:"structuredOutput"`
	InsufficientEvidence bool           `json:"insufficientEvidence"`
	DegradedReason       string         `json:"degradedReason"`
	DegradedDetail       string         `json:"degradedDetail"`
	Model                string         `json:"model"`
	ModelHash            string         `json:"modelHash"`
	RuntimeVersion       string         `json:"runtimeVersion"`
	PromptTemplateID     string         `json:"promptTemplateId"`
	PromptVersion        string         `json:"promptVersion"`
	ModelBundleSignature string         `json:"modelBundleSignature"`
	InputEvidenceHashes  []string       `json:"inputEvidenceHashes"`
	OutputSchemaVersion  int            `json:"outputSchemaVersion"`
	LatencyMS            int64          `json:"latencyMs"`
	Hardware             string         `json:"hardware"`
	OutputHash           string         `json:"outputHash"`
}

// Do performs one inference.
//
// A degraded response is a *success*, not an error: the runtime answered, and the
// answer is that it could not answer. Returning it as an error would make a
// caller retry a condition that will not change - and would push the caller
// towards fabricating an answer, which is the one thing this product must never
// do. Transport failures are returned as errors carrying a Degradation, so the
// caller can record a degraded observation either way.
func (c *Client) Do(ctx context.Context, req Request) (Result, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return Result{}, ErrNoEndpoint
	}
	if req.ObservationID == "" || req.ProfileID == "" {
		return Result{}, errors.New("airuntime: observationId and profileId are required")
	}

	// The deadline is applied here as well as advertised to the runtime, because
	// the runtime does not apply it. context.WithTimeout on a request whose
	// transport also has a Timeout means either one firing is enough.
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	body, err := c.marshal(req)
	if err != nil {
		return Result{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/infer", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("airuntime: build the request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		// A timeout is a degradation with its own reason, because "the runtime
		// took too long" and "the runtime is not there" are different operational
		// findings and an operator triaging them needs to know which.
		reason := observations.DegradationError
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			reason = observations.DegradationTimeout
		}
		return Result{Degraded: true, DegradedReason: reason, DegradedDetail: err.Error()},
			fmt.Errorf("airuntime: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return Result{Degraded: true, DegradedReason: observations.DegradationError,
				DegradedDetail: err.Error()},
			fmt.Errorf("airuntime: read the response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A 4xx is our fault - a malformed request - and a 5xx is theirs. Neither
		// is retryable as-is, and neither is a fabricated answer. The detail is
		// kept because runtime.py names the apiVersion mismatch explicitly and
		// that message is the whole diagnosis.
		return Result{Degraded: true, DegradedReason: observations.DegradationError,
				DegradedDetail: describeHTTPFailure(resp.StatusCode, raw)},
			fmt.Errorf("airuntime: the runtime returned %d: %s", resp.StatusCode,
				describeHTTPFailure(resp.StatusCode, raw))
	}

	var wire wireResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Result{Degraded: true, DegradedReason: observations.DegradationError,
				DegradedDetail: "the response is not JSON"},
			fmt.Errorf("airuntime: decode the response: %w", err)
	}
	if wire.APIVersion != APIVersion {
		return Result{Degraded: true, DegradedReason: observations.DegradationError,
				DegradedDetail: fmt.Sprintf("the runtime answered with apiVersion %d", wire.APIVersion)},
			fmt.Errorf("airuntime: the runtime answered with apiVersion %d, this client speaks %d",
				wire.APIVersion, APIVersion)
	}

	out := Result{
		ObservationID:        wire.ObservationID,
		ProfileID:            wire.ProfileID,
		Structured:           wire.StructuredOutput,
		InsufficientEvidence: wire.InsufficientEvidence,
		DegradedReason:       observations.Degradation(wire.DegradedReason),
		DegradedDetail:       wire.DegradedDetail,
		Model:                wire.Model,
		ModelHash:            wire.ModelHash,
		RuntimeVersion:       wire.RuntimeVersion,
		PromptTemplateID:     wire.PromptTemplateID,
		PromptVersion:        wire.PromptVersion,
		ModelBundleSignature: wire.ModelBundleSignature,
		InputEvidenceHashes:  wire.InputEvidenceHashes,
		OutputSchemaVersion:  wire.OutputSchemaVersion,
		LatencyMS:            wire.LatencyMS,
		Hardware:             wire.Hardware,
		OutputHash:           wire.OutputHash,
	}
	if out.Structured == nil {
		out.Structured = map[string]any{}
	}
	out.Degraded = out.DegradedReason != "" &&
		out.DegradedReason != observations.DegradationNone
	return out, nil
}

func (c *Client) marshal(req Request) ([]byte, error) {
	wire := wireRequest{
		APIVersion:      APIVersion,
		ObservationID:   req.ObservationID,
		ProfileID:       req.ProfileID,
		ProfileSchema:   req.ProfileSchema,
		DeadlineSeconds: c.timeout().Seconds(),
		Evidence:        make([]wireEvidence, 0, len(req.Evidence)),
	}
	for _, e := range req.Evidence {
		if e.SHA256 == "" {
			// The runtime verifies every item against its recorded hash and
			// degrades the whole response if one does not match. Sending an empty
			// hash would have it compare against the empty string and always
			// mismatch, so the failure would be reported as corrupt evidence rather
			// than as a client that forgot to supply one.
			return nil, fmt.Errorf("airuntime: evidence %q has no sha256; the runtime "+
				"verifies every item against its recorded hash, and an empty one would "+
				"degrade the response as corrupt rather than say what is actually wrong", e.ID)
		}
		contentType := e.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		wire.Evidence = append(wire.Evidence, wireEvidence{
			EvidenceID:  e.ID,
			SHA256:      e.SHA256,
			ContentType: contentType,
			DataBase64:  base64.StdEncoding.EncodeToString(e.Data),
		})
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("airuntime: encode the request: %w", err)
	}
	return body, nil
}

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: c.timeout()}
}

// describeHTTPFailure renders the runtime's error body without letting it become
// the only thing in the message. The body is the runtime's, and the runtime is a
// separate process with its own idea of what is worth saying.
func describeHTTPFailure(status int, raw []byte) string {
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &doc); err == nil && doc.Error != "" {
		return fmt.Sprintf("%d: %s", status, doc.Error)
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "…"
	}
	if trimmed == "" {
		return fmt.Sprintf("%d with no body", status)
	}
	return fmt.Sprintf("%d: %s", status, trimmed)
}

// isTimeout reports whether an error is a transport timeout.
//
// errors.Is against context.DeadlineExceeded covers the case where our own
// context fired. This covers the case where the transport's own timeout fired
// first, which is a different error value with the same operational meaning.
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
