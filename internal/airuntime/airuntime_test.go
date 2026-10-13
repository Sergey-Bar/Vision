package airuntime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// captured is what the fake runtime saw.
type captured struct {
	apiVersion any
	body       map[string]any
	raw        []byte
}

// fakeRuntime speaks the exact shapes deploy/ai-runtime emits, including its
// refusal modes.
//
// This is deliberately a hand-written server rather than a mock of the Go
// client: the wire contract lives in a Python file in another directory, and the
// only way to catch a drift between the two is to encode the Python's actual
// shapes here - including the ones that are bugs to reproduce.
//
// The handler receives the decoded body, so a handler can apply runtime.py's own
// validation to what actually arrived rather than to what the client intended.
func fakeRuntime(t *testing.T, handler func(w http.ResponseWriter, body map[string]any, raw []byte)) (*httptest.Server, *captured) {
	t.Helper()
	seen := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		seen.raw = raw
		_ = json.Unmarshal(raw, &seen.body)
		if seen.body != nil {
			seen.apiVersion = seen.body["apiVersion"]
		}
		handler(w, seen.body, raw)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func degradedBody(reason, detail string) map[string]any {
	return map[string]any{
		"apiVersion":           1,
		"observationId":        "obs-1",
		"profileId":            "restricted-person",
		"structuredOutput":     map[string]any{},
		"insufficientEvidence": true,
		"degradedReason":       reason,
		"degradedDetail":       detail,
		"model":                "",
		"modelHash":            "",
		"runtimeVersion":       "ai-runtime/1",
		"promptTemplateId":     "",
		"promptVersion":        "",
		"modelBundleSignature": "",
		"inputEvidenceHashes":  []any{},
		"outputSchemaVersion":  0,
		"latencyMs":            0,
		"hardware":             "",
		"outputHash":           strings.Repeat("c", 64),
	}
}

func newClient(t *testing.T, base string) *airuntime.Client {
	t.Helper()
	c, err := airuntime.New(base, 2*time.Second, true)
	if err != nil {
		t.Fatalf("New(%q): %v", base, err)
	}
	return c
}

// apiVersion must go on the wire as an integer. runtime.py compares it against
// the integer 1 with `!=`, so a client that sent the string "1" gets a 400 whose
// message reads like the runtime is broken.
func TestAPIVersionIsAnIntegerOnTheWire(t *testing.T) {
	srv, seen := fakeRuntime(t, func(w http.ResponseWriter, body map[string]any, _ []byte) {
		// runtime.py's own check, verbatim, applied to what actually arrived.
		if got, _ := body["apiVersion"].(float64); got != 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "unsupported apiVersion; this runtime speaks 1",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(degradedBody("no_runtime", "no model bundle"))
	})

	result, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !result.Degraded {
		t.Fatal("the degraded response was not reported as degraded")
	}
	if seen.apiVersion != float64(1) {
		t.Errorf("apiVersion went on the wire as %#v, want the number 1", seen.apiVersion)
	}
}

// The runtime refuses a string apiVersion. The client must not be able to
// produce one, and this is the assertion that it does not.
func TestAStringAPIVersionIsRefusedByTheRuntime(t *testing.T) {
	var seenBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = json.Unmarshal(raw, &seenBody)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "unsupported apiVersion '1'; this runtime speaks 1",
		})
	}))
	defer srv.Close()

	// Sent deliberately wrong, through a raw client, to prove the runtime's
	// rejection is real rather than assumed. The Go client cannot produce this.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/v1/infer",
		strings.NewReader(`{"apiVersion":"1","observationId":"o","profileId":"p"}`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a string apiVersion returned %d, want 400; the runtime's check is not "+
			"what the client assumes it is", resp.StatusCode)
	}
	if seenBody == nil {
		t.Fatal("the deliberately malformed request carried no body")
	}
	if got, _ := seenBody["apiVersion"].(string); got != "1" {
		t.Errorf("the raw request carried apiVersion %#v, want the string \"1\"",
			seenBody["apiVersion"])
	}

	// And the Go client surfaces that status as a degradation carrying the
	// runtime's own explanation, because that message is the whole diagnosis.
	_, err = newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err == nil {
		t.Fatal("a 400 was returned as a success")
	}
	if !strings.Contains(err.Error(), "unsupported apiVersion") {
		t.Errorf("the error %q does not carry the runtime's explanation", err)
	}
}

// A degraded response is a success. The runtime answered, and the answer is that
// it could not answer.
func TestDegradedResponseIsASuccessCarryingItsReason(t *testing.T) {
	srv, _ := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		_ = json.NewEncoder(w).Encode(degradedBody(
			"no_runtime",
			"a signed bundle is present but this build has no inference engine bound"))
	})

	result, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err != nil {
		t.Fatalf("a degraded response was returned as an error: %v", err)
	}
	if !result.Degraded {
		t.Fatal("Degraded is false on a degraded response")
	}
	if result.DegradedReason != observations.DegradationNoRuntime {
		t.Errorf("the reason is %q, want no_runtime", result.DegradedReason)
	}
	if !result.InsufficientEvidence {
		t.Error("insufficientEvidence is false; a consumer that ignores the degradation " +
			"would read an empty output as a negative answer")
	}
	if len(result.Structured) != 0 {
		t.Errorf("a degraded response carried structured output: %v", result.Structured)
	}
}

func TestARealResultIsCarriedThrough(t *testing.T) {
	srv, _ := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": 1, "observationId": "obs-1", "profileId": "restricted-person",
			"structuredOutput":     map[string]any{"restricted_person": true, "confidence": 0.9},
			"insufficientEvidence": false,
			"degradedReason":       "none",
			"model":                "vision-1", "modelHash": strings.Repeat("b", 64),
			"runtimeVersion": "ai-runtime/1", "promptTemplateId": "tpl-1", "promptVersion": "3",
			"modelBundleSignature": "sig", "inputEvidenceHashes": []any{strings.Repeat("e", 64)},
			"outputSchemaVersion": 1, "latencyMs": 412, "hardware": "cpu",
			"outputHash": strings.Repeat("c", 64),
		})
	})

	result, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if result.Degraded {
		t.Fatal("degradedReason=none was read as degraded")
	}
	if result.Structured["restricted_person"] != true {
		t.Errorf("structured output is %v", result.Structured)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"model", result.Model, "vision-1"},
		{"runtime version", result.RuntimeVersion, "ai-runtime/1"},
		{"prompt version", result.PromptVersion, "3"},
		{"hardware", result.Hardware, "cpu"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if result.LatencyMS != 412 {
		t.Errorf("latency is %d, want 412", result.LatencyMS)
	}
}

// Evidence is base64 and always carries the recorded hash.
//
// Open skips verification entirely when expectSHA is "", so sending an empty
// hash would quietly send bytes nobody checked to a model while the observation
// cited a hash the platform never verified.
func TestEvidenceIsMarshalledWithItsRecordedHash(t *testing.T) {
	srv, seen := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		_ = json.NewEncoder(w).Encode(degradedBody("absent", "no evidence"))
	})

	bytes := []byte("the actual camera bytes")
	const sha = "1111111111111111111111111111111111111111111111111111111111111111"

	_, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Evidence: []airuntime.Evidence{{
			ID: "ev-1", SHA256: sha, ContentType: "image/jpeg", Data: bytes,
		}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	list, _ := seen.body["evidence"].([]any)
	if len(list) != 1 {
		t.Fatalf("the request carried %d evidence item(s), want 1: %s", len(list), seen.raw)
	}
	item, _ := list[0].(map[string]any)
	if item["sha256"] != sha {
		t.Errorf("the hash went on the wire as %v, want the recorded %q", item["sha256"], sha)
	}
	encoded, _ := item["dataBase64"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("dataBase64 is not base64: %v", err)
	}
	if string(decoded) != string(bytes) {
		t.Errorf("the bytes did not survive the round trip: %q", decoded)
	}
	if item["evidenceId"] != "ev-1" {
		t.Errorf("evidenceId is %v", item["evidenceId"])
	}
	if item["contentType"] != "image/jpeg" {
		t.Errorf("contentType is %v", item["contentType"])
	}
}

// Evidence with no hash is refused client-side, because the runtime would
// compare it against the empty string, always mismatch, and report the response
// as corrupt evidence rather than saying what is actually wrong.
func TestEvidenceWithNoHashIsRefusedBeforeTheCall(t *testing.T) {
	var called bool
	srv, _ := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		called = true
		_ = json.NewEncoder(w).Encode(degradedBody("absent", "no evidence"))
	})

	_, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
		Evidence: []airuntime.Evidence{{ID: "ev-1", Data: []byte("bytes")}},
	})
	if err == nil {
		t.Fatal("evidence with no hash was accepted")
	}
	if !strings.Contains(err.Error(), "no sha256") {
		t.Errorf("the error %q does not name the missing hash", err)
	}
	if called {
		t.Error("the request was sent anyway")
	}
}

// The runtime accepts deadlineSeconds and never enforces it. The client has to,
// or a hung runtime hangs the worker holding a lease.
func TestAHungRuntimeProducesATimeoutNotAFabricatedAnswer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	start := time.Now()
	result, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hung runtime returned a result")
	}
	if elapsed > 10*time.Second {
		t.Errorf("the client waited %s; the deadline is not being enforced", elapsed)
	}
	if !result.Degraded {
		t.Error("a timeout did not produce a degraded result; a consumer would see it as a " +
			"fabricated answer rather than as a plane that did not answer")
	}
	if result.DegradedReason != observations.DegradationTimeout {
		t.Errorf("the reason is %q, want timeout; a timeout and a connection failure are "+
			"different operational findings", result.DegradedReason)
	}
	if len(result.Structured) != 0 {
		t.Errorf("a timeout carried structured output: %v", result.Structured)
	}
}

// The advertised deadline must actually reach the runtime, even though it is the
// client that enforces it.
func TestTheDeadlineIsAdvertisedToTheRuntime(t *testing.T) {
	srv, seen := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		_ = json.NewEncoder(w).Encode(degradedBody("absent", "no evidence"))
	})
	_, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	advertised, _ := seen.body["deadlineSeconds"].(float64)
	if advertised <= 0 {
		t.Errorf("deadlineSeconds went on the wire as %v", seen.body["deadlineSeconds"])
	}
}

// No configured endpoint is a supported configuration, and it must be
// distinguishable from a plane that is down.
func TestNoEndpointIsItsOwnState(t *testing.T) {
	c, err := airuntime.New("", time.Second, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if !errors.Is(err, airuntime.ErrNoEndpoint) {
		t.Fatalf("error is %v, want ErrNoEndpoint", err)
	}
}

// runtime.py has no authentication at all. A remote endpoint is refused unless
// an operator says otherwise, and the refusal is at construction so it is
// learned at startup rather than when the first incident is waiting.
func TestARemoteEndpointIsRefusedUnlessExplicitlyAllowed(t *testing.T) {
	for _, host := range []string{
		"http://10.0.0.5:8800", "http://runtime.example.com", "http://192.168.1.10:8800",
	} {
		if _, err := airuntime.New(host, time.Second, false); err == nil {
			t.Errorf("%s was accepted with a remote-host refusal switched off", host)
		} else if !strings.Contains(err.Error(), "no authentication") {
			t.Errorf("the refusal for %s does not explain why: %v", host, err)
		}
		if _, err := airuntime.New(host, time.Second, true); err != nil {
			t.Errorf("%s was refused even with the remote-host allowance set: %v", host, err)
		}
	}
}

func TestLoopbackEndpointsAreAccepted(t *testing.T) {
	for _, host := range []string{
		"http://127.0.0.1:8800", "http://localhost:8800", "http://[::1]:8800",
	} {
		if _, err := airuntime.New(host, time.Second, false); err != nil {
			t.Errorf("%s was refused: %v", host, err)
		}
	}
}

func TestANonHTTPURLIsRefused(t *testing.T) {
	if _, err := airuntime.New("ftp://127.0.0.1:8800", time.Second, false); err == nil {
		t.Error("an ftp:// endpoint was accepted")
	}
}

// The response version is checked. A runtime that answered with a different
// version is not a runtime this client understands, and guessing is worse than
// refusing.
func TestAMismatchedResponseVersionIsRefused(t *testing.T) {
	srv, _ := fakeRuntime(t, func(w http.ResponseWriter, _ map[string]any, _ []byte) {
		body := degradedBody("no_runtime", "detail")
		body["apiVersion"] = 2
		_ = json.NewEncoder(w).Encode(body)
	})
	_, err := newClient(t, srv.URL).Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err == nil {
		t.Fatal("a response with apiVersion 2 was accepted")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("the error %q does not name the version mismatch", err)
	}
}

func TestAnUnreachableEndpointIsADegradation(t *testing.T) {
	// Port 1 on loopback: nothing listens there.
	c, err := airuntime.New("http://127.0.0.1:1", time.Second, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := c.Do(context.Background(), airuntime.Request{
		ObservationID: "obs-1", ProfileID: "restricted-person",
	})
	if err == nil {
		t.Fatal("an unreachable endpoint returned a result")
	}
	if !result.Degraded {
		t.Error("an unreachable endpoint did not produce a degraded result")
	}
	if result.DegradedReason != observations.DegradationError {
		t.Errorf("the reason is %q, want error", result.DegradedReason)
	}
}
