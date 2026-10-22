package airuntime

import (
	"bytes"
	"context"
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

// The provider adapters.
//
// Three plain-HTTP adapters, and they are three because they are three
// different wire shapes — not because three is a good number.
//
//   - `openai_compatible` speaks the OpenAI chat-completions API, which is
//     what every OpenAI-compatible endpoint speaks: OpenCode's and Kilo
//     Code's own providers, a self-hosted vLLM, an Ollama, a corporate
//     gateway.
//   - `anthropic` speaks the Anthropic messages API, which is a different
//     header, a different body and a different answer shape.
//   - `local_bundle` speaks the FIRST shape again, against a bundle served on
//     this machine. It is a separate adapter and not a flag on the first one
//     because the three differ in the ways that matter: a local bundle has
//     no credential to leak, it may not be remote, and its answer carries a
//     bundle signature that the other two cannot produce.
//
// The rule that matters for all three: **an adapter never fabricates a
// negative.** A provider that could not answer returns a Result with
// Degraded set and a reason, or an error. Returning an empty Structured with
// Degraded false would be a fabricated "there is nothing here", which is the
// single worst thing this product can do.

// AdapterOptions is the configuration every adapter shares.
type AdapterOptions struct {
	// BaseURL is where the provider lives. It carries no credential: a
	// credential in a URL is a credential in every log line the URL reaches.
	BaseURL string
	// Model is the model the provider is asked for.
	Model string
	// APIKey is the provider's credential. It is never rendered.
	APIKey string
	// System is the system prompt. It is the site's own text and it is
	// configuration, not a secret.
	System string
	// Timeout bounds one call. It is a floor on the caller's deadline: a
	// provider that hangs must not hold a lease.
	Timeout time.Duration
	// Temperature is the sampling temperature the provider is asked for.
	//
	// It is part of the ATTRIBUTION, not a knob: an answer drawn at 0.9 and
	// one drawn at 0.01 are different sentences, and a record that cannot
	// say which was drawn is a record that cannot be reproduced.
	Temperature float64
	// HTTP is the client. Nil takes a default with the timeout applied.
	HTTP *http.Client
}

// ErrNoBaseURL is an adapter with nowhere to send anything.
var ErrNoBaseURL = errors.New("airuntime: the provider has no base URL")

// ErrRemoteBundle is a local bundle reached from off the machine.
//
// A local bundle is a model ON THIS SITE. Reaching it from the network is
// not a configuration mistake that can be retried; it is a bundle exposed to
// whoever can reach the port, and it is refused by name.
var ErrRemoteBundle = errors.New("airuntime: a local model bundle may only be reached on this machine")

// OpenAICompatible speaks the OpenAI chat-completions API.
//
// It is the shape most providers speak, and the shape a self-hosted runtime
// speaks too, which is why the local bundle adapter reuses its wire format
// rather than inventing a second one.
type OpenAICompatible struct{ AdapterOptions }

// Name identifies the provider on a reading.
func (a OpenAICompatible) Name() string { return "openai_compatible" }

// Do performs one inference.
//
// It returns a Result even when the provider has nothing to say, because
// "I could not answer" is a Result and not an absence.
func (a OpenAICompatible) Do(ctx context.Context, req Request) (Result, error) {
	body := map[string]any{
		"model":    a.Model,
		"messages": openAIMessages(a.System, req),
	}
	// The answer is JSON, and the schema says which fields it may name. A
	// model that answers in prose cannot be validated against a schema, and
	// an answer that cannot be validated is not an observation.
	if len(req.ProfileSchema) > 0 {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	// The temperature is always sent, and always recorded. An answer whose
	// temperature is not published is an answer that cannot be reproduced.
	//
	// The REQUEST's temperature wins when it names one, because the two
	// uses of a chain want different things.
	temp := a.Temperature
	if req.Temperature > 0 {
		temp = req.Temperature
	}
	body["temperature"] = temp

	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("airuntime: encode the request: %w", err)
	}

	raw, err := post(ctx, a.AdapterOptions, "/chat/completions", payload, map[string]string{
		"Authorization": "Bearer " + a.APIKey,
		"Content-Type":  "application/json",
	})
	if err != nil {
		return Result{}, err
	}

	var answer struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Result{}, fmt.Errorf("airuntime: the provider's answer is not JSON: %w", err)
	}
	if len(answer.Choices) == 0 {
		return degraded(req, observations.DegradationModelUnavailable,
			"the provider answered with no choice at all"), nil
	}

	structured, reason, detail := parseStructured(answer.Choices[0].Message.Content, req.ProfileSchema)
	res := Result{
		ObservationID: req.ObservationID,
		ProfileID:     req.ProfileID,
		Structured:    structured,
		Model:         a.Model,
		// The provider SAID which model it used. It is a claim, and it is
		// stored as a claim: a hosted model's weights are not ours and
		// change on the provider's schedule.
		ModelIdentity: answer.Model,
		Provider:      a.Name(),
		Temperature:   temp,
		Degraded:      reason != "",
	}
	if reason != "" {
		res.DegradedReason = reason
		res.DegradedDetail = detail
	}
	return res, nil
}

// post sends one request and returns the body.
//
// It is a package-level function rather than a method so that every adapter
// shares one HTTP implementation: two adapters with two HTTP clients is two
// places where a timeout is forgotten.
func post(ctx context.Context, opts AdapterOptions, path string, payload []byte, headers map[string]string) ([]byte, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, ErrNoBaseURL
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("airuntime: the provider's base URL does not parse: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + path

	ctx, cancel := withTimeout(ctx, opts.Timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("airuntime: build the request: %w", err)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	client := opts.HTTP
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("airuntime: the provider did not answer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("airuntime: read the provider's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("airuntime: the provider answered %d: %s",
			resp.StatusCode, describeHTTPFailure(resp.StatusCode, raw))
	}
	return raw, nil
}

// withTimeout bounds the call.
//
// The caller's deadline is a FLOOR: the adapter never waits longer than the
// caller allowed, and it never waits longer than its own timeout either.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// Anthropic speaks the Anthropic messages API.
//
// It is a different header, a different body and a different answer shape,
// and it is a separate adapter rather than a flag because those differences
// are exactly the kind that a "one adapter with options" turns into a
// request body with three branches in it.
type Anthropic struct{ AdapterOptions }

// Name identifies the provider on a reading.
func (a Anthropic) Name() string { return "anthropic" }

// Do performs one inference.
func (a Anthropic) Do(ctx context.Context, req Request) (Result, error) {
	temp := a.Temperature
	if req.Temperature > 0 {
		temp = req.Temperature
	}
	body := map[string]any{
		"model":       a.Model,
		"max_tokens":  2048,
		"temperature": temp,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": renderEvidence(req)},
			},
		}},
	}
	if a.System != "" {
		body["system"] = a.System
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("airuntime: encode the request: %w", err)
	}

	raw, err := post(ctx, a.AdapterOptions, "/v1/messages", payload, map[string]string{
		"x-api-key":         a.APIKey,
		"anthropic-version": "2023-06-01",
		"Content-Type":      "application/json",
	})
	if err != nil {
		return Result{}, err
	}

	var answer struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Result{}, fmt.Errorf("airuntime: the provider's answer is not JSON: %w", err)
	}
	text := ""
	for _, block := range answer.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if strings.TrimSpace(text) == "" {
		return degraded(req, observations.DegradationModelUnavailable,
			"the provider answered with no text"), nil
	}

	structured, reason, detail := parseStructured(text, req.ProfileSchema)
	res := Result{
		ObservationID: req.ObservationID,
		ProfileID:     req.ProfileID,
		Structured:    structured,
		Model:         a.Model,
		ModelIdentity: answer.Model,
		Provider:      a.Name(),
		Temperature:   temp,
		Degraded:      reason != "",
	}
	if reason != "" {
		res.DegradedReason = reason
		res.DegradedDetail = detail
	}
	return res, nil
}

// LocalBundle is a model bundle served ON THIS MACHINE.
//
// It speaks the same wire shape as the OpenAI-compatible API, because that
// is the shape a local runtime normally serves, and because inventing a
// second wire format for the same product is how two implementations of one
// thing drift.
//
// It is a separate adapter for the three reasons that matter: it has no
// credential, it may not be reached from the network, and its answer carries
// a bundle signature that a hosted provider cannot produce.
type LocalBundle struct{ AdapterOptions }

// Name identifies the provider on a reading.
func (a LocalBundle) Name() string { return "local_bundle" }

// Do performs one inference.
func (a LocalBundle) Do(ctx context.Context, req Request) (Result, error) {
	if err := a.assertLocal(); err != nil {
		return Result{}, err
	}
	// The two types have the same shape, so the wire logic is shared rather
	// than copied. A copy would be a second implementation of the same
	// request format, and the two would drift.
	inner := OpenAICompatible(a)
	res, err := inner.Do(ctx, req)
	if err != nil {
		return Result{}, err
	}
	// The bundle's identity is the LOCAL one: a signature over the files on
	// disk, not a name a remote service reported.
	res.ModelIdentity = ""
	// And the adapter that answered is the BUNDLE, not the wire shape it
	// borrows. A chain that walked a local bundle and a hosted provider
	// would otherwise attribute both to the same adapter.
	res.Provider = a.Name()
	return res, nil
}

// assertLocal refuses a bundle reached from off the machine.
//
// The check is on the URL's HOST and not on the operator's intention: a
// bundle on `0.0.0.0` is a bundle on every interface, and a name that
// resolves elsewhere is a name that will.
func (a LocalBundle) assertLocal() error {
	if strings.TrimSpace(a.BaseURL) == "" {
		return ErrNoBaseURL
	}
	u, err := url.Parse(a.BaseURL)
	if err != nil {
		return fmt.Errorf("airuntime: the bundle's base URL does not parse: %w", err)
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRemoteBundle, host)
}

// openAIMessages renders the request as the messages an OpenAI-compatible
// endpoint expects.
func openAIMessages(system string, req Request) []map[string]any {
	messages := make([]map[string]any, 0, len(req.Evidence)+1)
	if system != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": system,
		})
	}
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": renderEvidence(req),
	})
	return messages
}

// renderEvidence renders the evidence as the text the model reads.
//
// The evidence's BYTES are the input, and they are rendered rather than
// summarised: a provider that received a summary of the evidence would be a
// provider answering about something other than what the product holds.
func renderEvidence(req Request) string {
	var b strings.Builder
	b.WriteString("Observation ")
	b.WriteString(req.ObservationID)
	b.WriteString(" (profile ")
	b.WriteString(req.ProfileID)
	b.WriteString(").\n\n")
	for _, ev := range req.Evidence {
		b.WriteString("- ")
		b.WriteString(ev.ID)
		b.WriteString(" (")
		b.WriteString(ev.ContentType)
		b.WriteString(", sha256 ")
		b.WriteString(ev.SHA256)
		b.WriteString(")\n")
		b.Write(ev.Data)
		b.WriteString("\n")
	}
	if len(req.ProfileSchema) > 0 {
		b.WriteString("\nAnswer in JSON with exactly these fields: ")
		b.WriteString(schemaFieldList(req.ProfileSchema))
		b.WriteString(".")
	}
	return b.String()
}

// schemaFieldList renders a schema's declared field names.
func schemaFieldList(schema map[string]any) string {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return "answer, citations, confidence"
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sortStrings(names)
	return strings.Join(names, ", ")
}

// parseStructured parses an answer and validates it against the schema.
//
// It returns a reason and a detail when the answer does not hold, and the
// reason is what the observation records. An answer that names a field the
// schema does not declare, or that omits one it requires, is DEGRADED rather
// than stored: an observation the product cannot vouch for is worse than an
// observation it knows is missing.
func parseStructured(text string, schema map[string]any) (map[string]any, observations.Degradation, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, observations.DegradationModelUnavailable, "the provider answered with nothing"
	}
	// A model that wrapped its JSON in a fence still answered in JSON, and
	// a provider that answers correctly behind a fence has not failed.
	if fenced, ok := unwrapFence(trimmed); ok {
		trimmed = fenced
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(trimmed), &structured); err != nil {
		return nil, observations.DegradationModelUnavailable,
			"the provider's answer is not JSON: " + err.Error()
	}
	if len(schema) == 0 {
		return structured, "", ""
	}
	if problem := validateAgainstSchema(structured, schema); problem != "" {
		return nil, observations.DegradationError, problem
	}
	return structured, "", ""
}

// unwrapFence removes a markdown code fence from around a JSON answer.
func unwrapFence(text string) (string, bool) {
	if !strings.HasPrefix(text, "```") {
		return text, false
	}
	rest := text[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), true
}

// sortStrings sorts a slice of strings in place.
//
// It is here rather than importing `sort` for one call, because the file is
// about three adapters and the one place it needs an order is a field list
// that a human reads.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// degraded builds a Result that says the provider could not answer.
//
// It is a helper rather than a literal at each call site, because the shape
// of "could not answer" is the same everywhere: Degraded set, a reason, and
// a detail that says what happened.
func degraded(req Request, reason observations.Degradation, detail string) Result {
	return Result{
		ObservationID:  req.ObservationID,
		ProfileID:      req.ProfileID,
		Degraded:       true,
		DegradedReason: reason,
		DegradedDetail: detail,
	}
}
