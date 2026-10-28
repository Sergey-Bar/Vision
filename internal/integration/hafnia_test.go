package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/hafnia"
	"github.com/Sergey-Bar/vision/internal/integration"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// testTransport rewrites the pinned hafnia host to the test server.
type testTransport struct {
	base string
	do   func(*http.Request) (*http.Response, error)
}

func (t *testTransport) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.base, "http://")
	return t.do(req)
}

func hafniaConfig(t *testing.T, handler http.HandlerFunc) *hafnia.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := hafnia.Config{
		Region:              hafnia.RegionEU,
		APIKey:              "test-key",
		PromptTemplate:      "PROMPT VERBATIM",
		PromptVersion:       "v1",
		OutputSchemaVersion: 1,
		APIModelName:        "hafnia-8b",
		Timeout:             2 * time.Second,
		Transport:           &testTransport{base: srv.URL, do: srv.Client().Do},
	}
	c, err := hafnia.New(cfg)
	if err != nil {
		t.Fatalf("hafnia.New: %v", err)
	}
	return c
}

func validHafniaAnswer() string {
	return `{"model":"hafnia-8b","choices":[{"message":{"content":"{\"summary\":\"a person crossed\",\"verdict\":\"ALERT CONFIRMED\",\"confidence\":0.9}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`
}

func TestAskReturnsStructuredResultOnSuccess(t *testing.T) {
	c := hafniaConfig(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			return
		}
		_, _ = w.Write([]byte(validHafniaAnswer()))
	})

	evidence := []airuntime.Evidence{{
		ID:          "e1",
		SHA256:      "abc",
		ContentType: "video/mp4",
		Data:        []byte("clip"),
	}}

	res, err := integration.Ask(context.Background(), c, "tenant-1", "site-1", evidence)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if res.Degraded {
		t.Fatalf("expected non-degraded result, got degraded: %s", res.DegradedDetail)
	}
	if res.Structured == nil {
		t.Fatal("expected structured output")
	}
	if got, want := res.Structured["summary"], "a person crossed"; got != want {
		t.Errorf("summary = %v, want %v", got, want)
	}
	if got := res.ModelIdentity; got != "hafnia-8b" {
		t.Errorf("ModelIdentity = %q, want %q", got, "hafnia-8b")
	}
	if got := res.Provider; got != "" {
		// Hafnia client doesn't set Provider; that's fine.
		_ = got
	}
}

func TestAskDegradedOnMalformedAnswer(t *testing.T) {
	c := hafniaConfig(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":`)) // malformed
	})

	evidence := []airuntime.Evidence{{
		ID:          "e1",
		SHA256:      "abc",
		ContentType: "video/mp4",
		Data:        []byte("clip"),
	}}

	res, err := integration.Ask(context.Background(), c, "tenant-1", "site-1", evidence)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !res.Degraded {
		t.Fatal("expected degraded result for malformed answer")
	}
	if res.Structured != nil {
		t.Errorf("degraded result must not carry structured output (no fabricated negative), got %v", res.Structured)
	}
	if !strings.Contains(res.DegradedDetail, "reason=answer_malformed") {
		t.Errorf("detail %q should carry reason=answer_malformed", res.DegradedDetail)
	}
	if !observations.Degradations[res.DegradedReason] {
		t.Errorf("degradation %q outside closed vocabulary", res.DegradedReason)
	}
}

func TestAskDegradedOnQuotaExhausted(t *testing.T) {
	var calls int
	cfg := hafnia.Config{
		Region:              hafnia.RegionEU,
		APIKey:              "test-key",
		PromptTemplate:      "PROMPT VERBATIM",
		PromptVersion:       "v1",
		OutputSchemaVersion: 1,
		APIModelName:        "hafnia-8b",
		DailyQueryCap:       1,
	}
	// Use a real httptest server but count calls on the inference route.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets" && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			return
		}
		if r.URL.Path == "/chat/completions" {
			calls++
			_, _ = w.Write([]byte(validHafniaAnswer()))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cfg.Transport = &testTransport{base: srv.URL, do: srv.Client().Do}
	cfg.Transport = &testTransport{base: srv.URL, do: srv.Client().Do}
	c, err := hafnia.New(cfg)
	if err != nil {
		t.Fatalf("hafnia.New: %v", err)
	}

	evidence := []airuntime.Evidence{{
		ID:          "e1",
		SHA256:      "abc",
		ContentType: "video/mp4",
		Data:        []byte("clip"),
	}}

	// First call succeeds.
	res1, err := integration.Ask(context.Background(), c, "tenant-1", "site-1", evidence)
	if err != nil {
		t.Fatalf("Ask #1: %v", err)
	}
	if res1.Degraded {
		t.Fatalf("first call should not be degraded: %s", res1.DegradedDetail)
	}

	// Second call exhausts the cap.
	res2, err := integration.Ask(context.Background(), c, "tenant-1", "site-1", evidence)
	if err != nil {
		t.Fatalf("Ask #2: %v", err)
	}
	if !res2.Degraded {
		t.Fatal("second call should be degraded (cap reached)")
	}
	if !strings.Contains(res2.DegradedDetail, "reason=cap_reached") {
		t.Errorf("detail %q should carry reason=cap_reached", res2.DegradedDetail)
	}
	if res2.Structured != nil {
		t.Errorf("degraded result must not carry structured output, got %v", res2.Structured)
	}
}
