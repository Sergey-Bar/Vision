package vlm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The provider's paths, as verified against the published API
// (hafnia.readme.io/docs/vlm-api, retrieved 2026-09-30).
//
// They carry the `/api/v1` prefix. A client that omits it is not "tolerated" by
// a gateway, it gets a 404 whose body does not say which half of the path was
// wrong, and the operator spends an hour on a typo.
const (
	PathAssets      = "/api/v1/assets"
	PathCompletions = "/api/v1/chat/completions"
)

// MaxUploadBytes is the largest clip this client will send.
//
// It matches the provider's stated cap. It is a *transmit* limit and not a
// substitute for the capture-side cap: a 4K clip that breaches it should have
// been downscaled before it was stored, and refusing it here is the last line.
const MaxUploadBytes = 100 << 20

// MaxResponseBytes bounds a response body, so a provider that streams forever
// cannot exhaust the process.
const MaxResponseBytes = 8 << 20

// MaxClipSeconds is the length the provider recommends for a clip.
//
// It is checked against the clip's own duration where the caller knows it, and
// it is advisory rather than a refusal: a slightly long clip is answered, and a
// clipped one is not. Truncating a clip on the way out to fit a provider's
// recommendation would mean the evidence and the answer are about different
// seconds.
const MaxClipSeconds = 30

// Asset is an uploaded clip.
type Asset struct {
	ID         string
	MediaType  string
	SizeBytes  int
	UploadedAt time.Time
}

// ErrNoAsset is returned when a call needs an asset and none is given.
var ErrNoAsset = errors.New("vlm: no asset for this request")

// ErrMalformedAnswer is returned when the provider's response cannot be read as
// the shape it claims to be.
//
// "Parse, never guess. A malformed answer is cannot_tell, not a partial parse." A
// partial parse is the worst outcome: the caller gets a summary and no verdict,
// stores it, and the incident carries a badge driven by a field that happened to
// survive.
var ErrMalformedAnswer = errors.New("vlm: the provider's answer is not the shape it claims to be")

// ErrRegionUnavailable is returned when the pinned region cannot be reached.
//
// Deliberately not ErrCrossRegion and deliberately not a failover: the answer is
// a degradation at this site, and the footage stays here.
var ErrRegionUnavailable = errors.New("vlm: the pinned region could not be reached")

// ErrAssetNotFound is returned when an asset id is unknown to the provider.
//
// On a delete it means the work is already done, which is success for that
// purpose. On a completion it means the asset was swept or expired, and the
// caller's only correct response is to upload again.
var ErrAssetNotFound = errors.New("vlm: the asset is not on the provider")

// ErrRedirectRefused is returned when the provider answers with a redirect.
//
// Its own error type because "the provider tried to send us somewhere else" is
// the single most security-relevant thing that can happen to an upload, and it
// deserves a name an operator can be paged about.
var ErrRedirectRefused = errRedirect{}

type errRedirect struct{}

func (errRedirect) Error() string {
	return "vlm: refusing a redirect; the pinned host is the only destination"
}

// UploadAsset puts a clip on the provider and returns its id.
//
// The clip is the first thing that leaves the site, so the caller has already
// checked the lawful basis and the consent; the client does the last of the size
// checks and refuses the rest, because a client that can be talked into a larger
// upload is a client that will be.
func (c *Client) UploadAsset(ctx context.Context, mediaType string, data []byte) (Asset, error) {
	if len(data) == 0 {
		return Asset{}, errors.New("vlm: refusing to upload an empty clip")
	}
	if len(data) > MaxUploadBytes {
		return Asset{}, fmt.Errorf("vlm: the clip is %d bytes and the provider's cap is %d; the clip must be shortened "+
			"or downscaled before it is sent, not truncated on the way out", len(data), MaxUploadBytes)
	}
	if mediaType == "" {
		mediaType = "video/mp4"
	}
	if mediaType != "video/mp4" {
		return Asset{}, fmt.Errorf("vlm: refusing to upload %s; the provider's asset endpoint accepts mp4 only", mediaType)
	}
	body, contentType, err := encodeUpload(mediaType, data)
	if err != nil {
		return Asset{}, err
	}
	resp, err := c.call(ctx, http.MethodPost, PathAssets, contentType, body)
	if err != nil {
		return Asset{}, err
	}
	defer drain(resp)

	var wire struct {
		ID string `json:"id"`
	}
	if err := decodeStrict(resp, &wire); err != nil {
		return Asset{}, err
	}
	if strings.TrimSpace(wire.ID) == "" {
		return Asset{}, fmt.Errorf("%w: the upload succeeded and returned no asset id, so the clip cannot be deleted",
			ErrMalformedAnswer)
	}
	asset := Asset{ID: wire.ID, MediaType: mediaType, SizeBytes: len(data), UploadedAt: c.now()}
	c.assets.record(asset.ID)
	return asset, nil
}

// DeleteAsset removes an asset.
//
// A 404 is success for this method's purpose: the asset is not on the provider,
// which is the state the caller asked for. Anything else is an error, because
// "the delete call returned 200" and "the footage is gone" are different claims.
func (c *Client) DeleteAsset(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("vlm: no asset id to delete")
	}
	resp, err := c.call(ctx, http.MethodDelete, PathAssets+"/"+id, "", nil)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) {
			c.assets.release(id)
			return nil
		}
		return err
	}
	drain(resp)
	c.assets.release(id)
	return nil
}

// Ask runs the prompt against an uploaded asset, reserving one call from the
// site's budget first.
func (c *Client) Ask(ctx context.Context, assetID string, prompt Prompt) (Observation, error) {
	if err := c.quota.take(); err != nil {
		return Observation{}, err
	}
	return c.ask(ctx, assetID, prompt)
}

// SweepAssets deletes every asset this client uploaded and has not been told to
// delete, and returns what it deleted and what it could not.
//
// It exists for the case the `defer` cannot cover: a process that was killed
// between the upload and the delete. Without it, footage that left the site
// stays on a third party's disk and nobody is looking for it, because the run
// that uploaded it is gone.
func (c *Client) SweepAssets(ctx context.Context) (deleted []string, failed []string) {
	for _, id := range c.assets.ids() {
		if err := c.DeleteAsset(ctx, id); err != nil {
			failed = append(failed, id)
			continue
		}
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	sort.Strings(failed)
	return deleted, failed
}

// call performs one request against the pinned host.
//
// The host check is here rather than at each call site so that no endpoint can
// be added later without passing through the allow-list.
func (c *Client) call(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	c.calls.bump()
	host := c.host
	endpoint := "https://" + host + path
	if err := c.requireRegion(host); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrRegionUnavailable, method, path, err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("vlm: build the request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// The documented scheme is `ApiKey <key>`. `Bearer <key>` is the scheme the
	// root platform's client sends, and it is a 401 on every call: a silent
	// wrongness, because the endpoint exists and answers.
	req.Header.Set("Authorization", "ApiKey "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// A refused redirect arrives here as a transport error wrapping the
		// refusal, not as a 3xx: the client never returned the response. Naming it
		// correctly is the difference between "the provider tried to move the
		// call" and "the region was unreachable", and only one of those is
		// somebody else's load balancer.
		if errors.Is(err, ErrRedirectRefused) {
			return nil, fmt.Errorf("%w: %s %s: nothing was sent to it", ErrRedirectRefused, method, path)
		}
		return nil, fmt.Errorf("%w: %s %s: %w", ErrRegionUnavailable, method, path, err)
	}
	if err := c.classify(resp); err != nil {
		drain(resp)
		return nil, err
	}
	return resp, nil
}

// classify maps a status code to a named error.
//
// Each of these is a *different* operational fact, and collapsing them into
// "request failed" is how a provider's rate limit turns into a support ticket
// about connectivity.
func (c *Client) classify(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("vlm: the provider rejected the credentials (%d); check the site's API key. The documented scheme is 'Authorization: ApiKey <key>'",
			resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: the provider is rate limiting this site", ErrRegionUnavailable)
	case resp.StatusCode == http.StatusNotFound:
		return ErrAssetNotFound
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// Named as a redirect even though the HTTP client already refuses to
		// follow one. The refusal covers something between here and the provider;
		// this is the provider itself doing it, and an operator reading "the
		// provider answered 302" learns nothing.
		return fmt.Errorf("%w: the provider answered %d pointing at %q; nothing was sent to it",
			ErrRedirectRefused, resp.StatusCode, resp.Header.Get("Location"))
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: the provider answered %d", ErrRegionUnavailable, resp.StatusCode)
	default:
		return fmt.Errorf("vlm: the provider answered %d", resp.StatusCode)
	}
}

// completionRequest is the exact request body.
//
// A typed content array, because that is the documented shape: a flat string
// with an asset id somewhere in it is structurally wrong, and it fails in the
// way that is hardest to see — a 400 whose body does not name the field.
type completionRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat responseFmt   `json:"response_format"`
	MaxTokens      int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string      `json:"role"`
	Content []contentPt `json:"content"`
}

type contentPt struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	AssetID string `json:"asset_id,omitempty"`
}

type responseFmt struct {
	Type string `json:"type"`
}

func (c *Client) encodeCompletion(prompt Prompt, assetID string) ([]byte, error) {
	if strings.TrimSpace(prompt.Text) == "" {
		return nil, ErrNoPrompt
	}
	if strings.TrimSpace(c.cfg.APIModelName) == "" {
		return nil, errors.New("vlm: no API model name; the request would name no model")
	}
	return json.Marshal(completionRequest{
		Model: c.cfg.APIModelName,
		Messages: []chatMessage{{
			Role: "user",
			Content: []contentPt{
				{Type: "text", Text: prompt.Text},
				{Type: "asset_id", AssetID: assetID},
			},
		}},
		Temperature:    c.cfg.Temperature,
		ResponseFormat: responseFmt{Type: "json_object"},
		MaxTokens:      256,
	})
}

// answerResponse is the provider's wire shape.
type answerResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// decodeStrict decodes a response body into a value, refusing anything that is
// not exactly the shape the value describes.
//
// The body is bounded before it is read, so a provider that streams forever
// cannot exhaust the process, and the decode error is wrapped rather than
// replaced.
func decodeStrict(resp *http.Response, into any) error {
	dec := json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes))
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedAnswer, err)
	}
	return nil
}

func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
	_ = resp.Body.Close()
}

// assetLedger is the set of asset ids this client uploaded and has not been told
// to delete.
type assetLedger struct {
	mu     sync.Mutex
	held   map[string]time.Time
	reason map[string]string
}

func newAssetLedger() *assetLedger {
	return &assetLedger{held: map[string]time.Time{}, reason: map[string]string{}}
}

func (l *assetLedger) record(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held[id] = time.Now().UTC()
}

func (l *assetLedger) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, id)
}

func (l *assetLedger) ids() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.held))
	for id := range l.held {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// multipart builds a multipart/form-data body.
//
// The framing is a file part and an optional name, and nothing else: every extra
// field is a field the provider has to tolerate and this client has to get right.
func encodeUpload(mediaType string, data []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	w := newMultipart(&buf)
	if err := w.file("file", "clip.mp4", mediaType, data); err != nil {
		return nil, "", err
	}
	if err := w.field("name", "aicaptain-alarm-triage"); err != nil {
		return nil, "", err
	}
	if err := w.close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.contentType(), nil
}
