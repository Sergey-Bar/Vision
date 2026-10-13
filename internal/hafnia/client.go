package hafnia

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
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/observations"
)

// The provider's four endpoints, and nothing else. Each is one method, each
// returns a typed error or a typed result, and none of them guesses.

// Asset is an uploaded clip.
type Asset struct {
	ID         string
	MediaType  string
	SizeBytes  int
	UploadedAt time.Time
}

// ErrNoAsset is returned when a call needs an asset and none is given.
var ErrNoAsset = errors.New("hafnia: no asset for this request")

// ErrMalformedAnswer is returned when the provider's response cannot be read as
// the shape it claims to be.
//
// The plan's P3.6 is "parse, never guess. A malformed answer is a degraded
// observation, not a partial parse." A partial parse is the worst outcome: the
// caller gets a `summary` and no `verdict`, stores it, and the incident has a
// badge driven by a field that happened to survive.
var ErrMalformedAnswer = errors.New("hafnia: the provider's answer is not the shape it claims to be")

// ErrRegionUnavailable is returned when the pinned region cannot be reached.
//
// Deliberately not ErrCrossRegion and deliberately not a failover: the answer is
// a degradation at this site, and the footage stays here.
var ErrRegionUnavailable = errors.New("hafnia: the pinned region could not be reached")

// UploadAsset puts a clip on the provider and returns its id.
//
// The clip is the first thing that leaves the site, so this method is where the
// lawful basis, the consent and the size cap have to be checked by the caller -
// the client does the last of those and refuses the rest, because a client that
// can be talked into a larger upload is a client that will be.
func (c *Client) UploadAsset(ctx context.Context, mediaType string, data []byte) (Asset, error) {
	if len(data) == 0 {
		return Asset{}, errors.New("hafnia: refusing to upload an empty clip")
	}
	if len(data) > MaxUploadBytes {
		return Asset{}, fmt.Errorf("hafnia: the clip is %d bytes and the provider's cap is %d; the clip must be "+
			"shortened or downscaled before it is sent, not truncated on the way out", len(data), MaxUploadBytes)
	}
	body, contentType, err := c.encodeUpload(mediaType, data)
	if err != nil {
		return Asset{}, err
	}
	resp, err := c.call(ctx, http.MethodPost, "/assets", contentType, body)
	if err != nil {
		return Asset{}, err
	}
	defer drain(resp)

	var wire struct {
		ID        string `json:"id"`
		MediaType string `json:"mediaType"`
		SizeBytes int64  `json:"sizeBytes"`
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
		return errors.New("hafnia: no asset id to delete")
	}
	resp, err := c.call(ctx, http.MethodDelete, "/assets/"+id, "", nil)
	if err != nil {
		return err
	}
	drain(resp)
	c.assets.release(id)
	return nil
}

// Answer is one inference against one asset.
type Answer struct {
	// Summary, Verdict and the rest are the provider's structured output, after
	// validation against the profile schema. They are only meaningful when
	// Structured is true.
	Structured map[string]any
	// ModelIdentity is what the provider said it used.
	ModelIdentity string
	// PromptTokens and CompletionTokens are the provider's own accounting, kept
	// as it reported them rather than recomputed: a bill that disagrees with the
	// meter is a dispute, and recomputing invites one.
	PromptTokens     int
	CompletionTokens int
	// LatencyMS is measured here, not taken from the provider.
	LatencyMS int64
}

// answerRequest is the exact request body. It is a struct rather than a map so
// that a field cannot be added by accident: a request that quietly grows a field
// is a request whose wire shape nobody has reviewed.
type answerRequest struct {
	AssetID      string  `json:"assetId"`
	Prompt       string  `json:"prompt"`
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	OutputFormat string  `json:"outputFormat"`
}

// Ask runs the prompt against an uploaded asset, reserving one call from the
// site's budget first.
func (c *Client) Ask(ctx context.Context, assetID string) (Answer, error) {
	if err := c.quota.take(); err != nil {
		return Answer{}, err
	}
	return c.ask(ctx, assetID)
}

// ask is the billable call with the reservation already made.
//
// It is separate from [Client.Ask] so that [Client.Do] can reserve *before* the
// upload. That ordering is the whole point: reserving after the upload would
// mean a site that has reached its daily cap still ships footage to a third
// party on every event, for a call that is then refused. The plan's cost
// argument and its privacy argument both require the budget to be consulted
// before anything leaves the site.
func (c *Client) ask(ctx context.Context, assetID string) (Answer, error) {
	if strings.TrimSpace(assetID) == "" {
		return Answer{}, ErrNoAsset
	}
	started := c.now()
	body, err := json.Marshal(answerRequest{
		AssetID:      assetID,
		Prompt:       c.cfg.PromptTemplate,
		Model:        c.cfg.APIModelName,
		Temperature:  c.cfg.Temperature,
		OutputFormat: "json",
	})
	if err != nil {
		return Answer{}, fmt.Errorf("hafnia: encode the request: %w", err)
	}
	resp, err := c.call(ctx, http.MethodPost, "/chat/completions", "application/json", body)
	if err != nil {
		return Answer{}, err
	}
	defer drain(resp)

	ans, err := c.parseAnswer(resp)
	if err != nil {
		return Answer{}, err
	}
	ans.LatencyMS = c.now().Sub(started).Milliseconds()
	if ans.LatencyMS < 0 {
		ans.LatencyMS = 0
	}
	return ans, nil
}

// answerResponse is the provider's wire shape.
type answerResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			// Content is the model's answer. It is a JSON document, and it is
			// parsed rather than pattern-matched: a substring search for a verdict
			// is a rule that changes behaviour when a prompt is rephrased.
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// parseAnswer reads the provider's answer strictly.
//
// Strict means: exactly one choice, non-empty content, and content that is a
// JSON object. A response with two choices is ambiguous and is refused, because
// picking the first is a guess whose failure mode is invisible.
func (c *Client) parseAnswer(resp *http.Response) (Answer, error) {
	var wire answerResponse
	if err := decodeStrict(resp, &wire); err != nil {
		return Answer{}, err
	}
	if len(wire.Choices) != 1 {
		return Answer{}, fmt.Errorf("%w: %d choices; exactly one is required and picking one would be a guess",
			ErrMalformedAnswer, len(wire.Choices))
	}
	content := strings.TrimSpace(wire.Choices[0].Message.Content)
	if content == "" {
		return Answer{}, fmt.Errorf("%w: the choice carried no content", ErrMalformedAnswer)
	}
	var structured map[string]any
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&structured); err != nil {
		return Answer{}, fmt.Errorf("%w: the content is not a JSON object: %w", ErrMalformedAnswer, err)
	}
	if structured == nil {
		return Answer{}, fmt.Errorf("%w: the content decoded to null", ErrMalformedAnswer)
	}
	return Answer{
		Structured:       structured,
		ModelIdentity:    wire.Model,
		PromptTokens:     wire.Usage.PromptTokens,
		CompletionTokens: wire.Usage.CompletionTokens,
	}, nil
}

// call performs one request against the pinned host.
//
// The host check is here rather than at each call site so that no endpoint can
// be added later without passing through the allow-list.
func (c *Client) call(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	if err := c.requireRegion(ctx); err != nil {
		return nil, err
	}
	endpoint := "https://" + c.host + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("hafnia: build the request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	// The API key must not be sent to a different host even if a future edit
	// loosened the URL construction, so it is set here and nowhere else.
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrRegionUnavailable, method, path, err)
	}
	if err := c.classify(resp); err != nil {
		drain(resp)
		return nil, err
	}
	return resp, nil
}

// requireRegion refuses a call whose destination is not the pinned one.
//
// It is cheap and it is the last check before bytes leave, and it exists because
// the alternative - trusting every call site to build the URL correctly - is a
// guarantee held in prose rather than in code.
func (c *Client) requireRegion(ctx context.Context) error {
	if c.host == "" {
		return fmt.Errorf("%w: no pinned host", ErrCrossRegion)
	}
	if _, err := c.region.Host(); err != nil {
		return err
	}
	return ctx.Err()
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
		return fmt.Errorf("hafnia: the provider rejected the credentials (%d); check the site's API key",
			resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: the provider is rate limiting this site", ErrRegionUnavailable)
	case resp.StatusCode == http.StatusNotFound:
		return ErrAssetNotFound
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// Named as a redirect even though the HTTP client is already refusing to
		// follow one. The refusal exists for the case where something between
		// here and the provider answers instead; this is the case where the
		// provider itself does, and an operator reading "the provider answered
		// 302" learns nothing while "the provider tried to redirect us" tells
		// them to look at the vendor's load balancer.
		return fmt.Errorf("%w: the provider answered %d pointing at %q; nothing was sent to it",
			ErrRedirectRefused, resp.StatusCode, resp.Header.Get("Location"))
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: the provider answered %d", ErrRegionUnavailable, resp.StatusCode)
	default:
		return fmt.Errorf("hafnia: the provider answered %d", resp.StatusCode)
	}
}

// ErrAssetNotFound is returned when an asset id is unknown to the provider.
//
// On a delete it means the work is already done, which is success for that
// purpose. On an ask it means the asset was swept or expired, and the caller's
// only correct response is to upload again.
var ErrAssetNotFound = errors.New("hafnia: the asset is not on the provider")

func isGone(err error) bool { return is(err, ErrAssetNotFound) }

// decodeStrict decodes a response body into a value, refusing anything that is
// not exactly the shape the value describes.
//
// The body is bounded before it is read, so a provider that streams forever
// cannot exhaust the process, and the decode error is wrapped rather than
// replaced - a caller that wants to know the provider was unreadable and a
// caller that wants the underlying JSON error both get what they asked for.
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

// MaxUploadBytes is the largest clip this client will send.
//
// It matches the provider's stated cap. The plan is explicit that this is a
// *transmit* limit and not a substitute for the capture-side cap: a 4K clip that
// breaches it should have been downscaled before it was stored, and refusing it
// here is the last line rather than the plan.
const MaxUploadBytes = 100 << 20

// MaxResponseBytes bounds a response body, so a provider that streams forever
// cannot exhaust the process.
const MaxResponseBytes = 8 << 20

// encodeUpload builds the multipart body for an asset.
//
// The framing is a file part and a content-type, and nothing else: every extra
// field is a field the provider has to tolerate and this client has to get right.
func (c *Client) encodeUpload(mediaType string, data []byte) ([]byte, string, error) {
	if mediaType == "" {
		mediaType = "video/mp4"
	}
	var buf bytes.Buffer
	w := newMultipart(&buf)
	if err := w.file("file", "clip.mp4", mediaType, data); err != nil {
		return nil, "", err
	}
	if err := w.field("model", c.cfg.APIModelName); err != nil {
		return nil, "", err
	}
	if err := w.close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.contentType(), nil
}

var _ airuntime.Provider = (*Client)(nil)

// ReasonCode is the machine-readable cause of a degradation.
//
// It exists because the Degradation vocabulary is a **closed set pinned by a
// schema CHECK constraint**, and the plan's two most important failure modes -
// the site reached its cap, and the pinned region was refused - are not in it.
// Adding them is a contract and migration change, which is a human-reviewed
// change, and it is not one this package makes on its own.
//
// So the category is shared and the cause is not. Every degraded result carries
// a `reason=<code>` field, in a fixed key=value shape, so a consumer parses a
// field rather than matching prose - and a test asserts that each transport
// failure produces a *different* code, which is the assertion that will go red
// the day someone decides to extend the vocabulary.
type ReasonCode string

// The reason codes this client can produce.
const (
	ReasonNone              ReasonCode = "none"
	ReasonQuotaExhausted    ReasonCode = "cap_reached"
	ReasonRegionRefused     ReasonCode = "region_refused"
	ReasonRegionUnavailable ReasonCode = "region_unavailable"
	ReasonAnswerMalformed   ReasonCode = "answer_malformed"
	ReasonAssetMissing      ReasonCode = "asset_missing"
	ReasonUploadRefused     ReasonCode = "upload_refused"
	ReasonTimeout           ReasonCode = "timeout"
	ReasonRedirectRefused   ReasonCode = "redirect_refused"
	ReasonError             ReasonCode = "error"
)

// ReasonFor maps a transport error to its code.
func ReasonFor(err error) ReasonCode {
	switch {
	case err == nil:
		return ReasonNone
	case is(err, ErrQuotaExhausted):
		return ReasonQuotaExhausted
	case is(err, ErrCrossRegion):
		return ReasonRegionRefused
	case is(err, ErrRegionUnavailable):
		return ReasonRegionUnavailable
	case is(err, ErrMalformedAnswer):
		return ReasonAnswerMalformed
	case is(err, ErrAssetNotFound):
		return ReasonAssetMissing
	case is(err, ErrRedirectRefused):
		return ReasonRedirectRefused
	default:
		return ReasonError
	}
}

// degradationFor maps a transport error to the closed Degradation vocabulary.
//
// Every "we did not ask" failure shares one category, because the vocabulary has
// one for that and no more. What tells them apart is the reason code in the
// detail, which is why the detail is built here rather than left to each call
// site: two call sites formatting the same failure differently is how a
// "cap_reached" becomes a "cap reached" and stops being matchable.
func degradationFor(err error) observations.Degradation {
	switch {
	case err == nil:
		return observations.DegradationNone
	case is(err, ErrMalformedAnswer):
		// We asked, and the answer could not be read. That is an error, not an
		// absence: the distinction matters because an error is a page for someone
		// and an absence is a line on a status board.
		return observations.DegradationError
	case isTimeout(err):
		return observations.DegradationTimeout
	default:
		// Every refusal and every unavailability means the model was not asked.
		// The reason code carries which.
		return observations.DegradationModelUnavailable
	}
}

// detailFor renders the machine-readable detail for a failure.
//
// Fixed key=value shape on purpose. The repository's own rule is that nothing
// matches on prose, and a sentence built with fmt.Sprintf is prose.
func detailFor(err error, extra map[string]string) string {
	if err == nil {
		return ""
	}
	parts := []string{"reason=" + string(ReasonFor(err))}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+extra[k])
	}
	return strings.Join(parts, " ")
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
