package airuntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// The provider adapters.
//
// The claim each test makes is the same one: an adapter turns a PROVIDER'S
// answer into a Result, and an answer the schema does not hold is DEGRADED
// rather than stored. An adapter that stored it would be storing something
// the product cannot vouch for.

// theRequest is one inference.
func theRequest() airuntime.Request {
	return airuntime.Request{
		ObservationID: "obs-1",
		ProfileID:     "general-monitoring",
		Evidence: []airuntime.Evidence{{
			ID:          "ev-1",
			SHA256:      "abc",
			ContentType: "image/jpeg",
			Data:        []byte("bytes"),
		}},
		ProfileSchema: airuntime.InvestigatorProfileSchema,
	}
}

// theAnswer is an answer the schema holds.
func theAnswer() string {
	return `{"answer":"a person at the door","citations":["ev-1"],"confidence":0.8}`
}

func TestTheOpenAICompatibleAdapterReadsTheAnswer(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("the adapter called %q, want /chat/completions", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret-key" {
			t.Errorf("the adapter sent %q, want the bearer credential", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "qwen2.5-vl",
			"choices": []any{map[string]any{
				"message": map[string]any{"content": theAnswer()},
			}},
		})
	}))
	defer srv.Close()

	a := airuntime.OpenAICompatible{AdapterOptions: airuntime.AdapterOptions{
		BaseURL: srv.URL,
		Model:   "qwen2.5-vl",
		APIKey:  "secret-key",
		Timeout: 5 * time.Second,
	}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the adapter failed: %v", err)
	}
	if res.Degraded {
		t.Fatalf("a good answer was degraded: %s", res.DegradedDetail)
	}
	if res.Structured["answer"] != "a person at the door" {
		t.Errorf("the answer is %v", res.Structured["answer"])
	}
	// The provider SAID which model it used, and that is a claim the
	// product records rather than a hash it invents.
	if res.ModelIdentity != "qwen2.5-vl" {
		t.Errorf("the model identity is %q, want the name the provider reported", res.ModelIdentity)
	}
	// And the evidence went, as BYTES rather than as a summary of them.
	messages, _ := got["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("the adapter sent no messages")
	}
	last, _ := messages[len(messages)-1].(map[string]any)
	content, _ := last["content"].(string)
	if content == "" || !contains(content, "bytes") {
		t.Errorf("the evidence did not reach the provider: %q", content)
	}
}

func TestAnAnswerThatNamesAnUndeclaredFieldIsDegraded(t *testing.T) {
	// The schema is the only thing standing between a provider's text and
	// the record, and an answer that names a field the profile has no slot
	// for is NOT that schema's answer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "m",
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": `{"answer":"x","citations":[],"confidence":0.5,"weapon":"a gun"}`,
				},
			}},
		})
	}))
	defer srv.Close()

	a := airuntime.OpenAICompatible{AdapterOptions: airuntime.AdapterOptions{BaseURL: srv.URL, Model: "m"}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the adapter failed: %v", err)
	}
	if !res.Degraded {
		t.Fatal("an answer naming an undeclared field was kept")
	}
	if res.DegradedReason != observations.DegradationError {
		t.Errorf("the degradation reason is %q, want %q", res.DegradedReason, observations.DegradationError)
	}
	if !contains(res.DegradedDetail, "weapon") {
		t.Errorf("the detail does not name the field: %q", res.DegradedDetail)
	}
}

func TestAnAnswerThatOmitsARequiredFieldIsDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "m",
			"choices": []any{map[string]any{
				"message": map[string]any{"content": `{"answer":"x"}`},
			}},
		})
	}))
	defer srv.Close()

	a := airuntime.OpenAICompatible{AdapterOptions: airuntime.AdapterOptions{BaseURL: srv.URL, Model: "m"}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the adapter failed: %v", err)
	}
	if !res.Degraded {
		t.Fatal("an answer missing a required field was kept")
	}
	if !contains(res.DegradedDetail, "citations") {
		t.Errorf("the detail does not name the missing field: %q", res.DegradedDetail)
	}
}

func TestAnAnswerThatIsNotJSONIsDegraded(t *testing.T) {
	// A model that answers in prose has NOT answered a schema, and an
	// observation the product cannot validate is worse than one it knows is
	// missing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "m",
			"choices": []any{map[string]any{
				"message": map[string]any{"content": "I think somebody walked by."},
			}},
		})
	}))
	defer srv.Close()

	a := airuntime.OpenAICompatible{AdapterOptions: airuntime.AdapterOptions{BaseURL: srv.URL, Model: "m"}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the adapter failed: %v", err)
	}
	if !res.Degraded {
		t.Fatal("an answer that is not JSON was kept")
	}
}

func TestTheAnthropicAdapterReadsItsOwnAnswer(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("the adapter called %q, want /v1/messages", r.URL.Path)
		}
		if key := r.Header.Get("x-api-key"); key != "sk-ant" {
			t.Errorf("the adapter sent %q, want the credential", key)
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("the adapter sent no anthropic-version header")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "claude",
			"content": []any{
				map[string]any{"type": "text", "text": theAnswer()},
			},
		})
	}))
	defer srv.Close()

	a := airuntime.Anthropic{AdapterOptions: airuntime.AdapterOptions{
		BaseURL: srv.URL,
		Model:   "claude",
		APIKey:  "sk-ant",
		System:  "you are a security analyst",
		Timeout: 5 * time.Second,
	}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("the adapter failed: %v", err)
	}
	if res.Degraded {
		t.Fatalf("a good answer was degraded: %s", res.DegradedDetail)
	}
	if got["system"] != "you are a security analyst" {
		t.Errorf("the system prompt did not reach the provider: %v", got["system"])
	}
}

func TestALocalBundleRefusesToBeReachedOffTheMachine(t *testing.T) {
	// A local bundle is a model ON THIS SITE. A bundle on a remote host is
	// a model somebody else is serving, and it is not a local bundle.
	a := airuntime.LocalBundle{AdapterOptions: airuntime.AdapterOptions{
		BaseURL: "http://models.example.com:8080",
		Model:   "qwen2.5-vl",
	}}
	_, err := a.Do(context.Background(), theRequest())
	if err == nil {
		t.Fatal("a remote bundle was accepted")
	}
	if !contains(err.Error(), "may only be reached on this machine") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestALocalBundleOnLoopbackIsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "qwen2.5-vl",
			"choices": []any{map[string]any{
				"message": map[string]any{"content": theAnswer()},
			}},
		})
	}))
	defer srv.Close()

	a := airuntime.LocalBundle{AdapterOptions: airuntime.AdapterOptions{
		BaseURL: srv.URL,
		Model:   "qwen2.5-vl",
	}}
	res, err := a.Do(context.Background(), theRequest())
	if err != nil {
		t.Fatalf("a loopback bundle was refused: %v", err)
	}
	if res.Degraded {
		t.Fatalf("a good answer was degraded: %s", res.DegradedDetail)
	}
	// A local bundle has no remote identity to record: its answer carries
	// the bundle's own, and a hosted model's name is a claim.
	if res.ModelIdentity != "" {
		t.Errorf("the local bundle reported a model identity %q, want none", res.ModelIdentity)
	}
}

// contains is a substring test.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

// indexOf is strings.Index, kept local so the test file has no import it
// does not need for anything else.
func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
