package vlm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProbeReport is what one live call actually returned.
//
// It exists because the whole of this package was written against a published
// DOCUMENT and no wire, and the eight rows in contracts/hafnia/README.md marked
// ASSERTED are claims about prose. This is the one type in the package whose
// subject is the claim rather than the call: it carries the raw body so a person
// can read what the provider said, next to what this build assumed it would say.
type ProbeReport struct {
	// Host is the pinned host this call went to, which is the answer to the
	// contested assumption 6. It is resolved from the region map and not from a
	// flag, so a probe cannot be pointed at a host the run would never use.
	Host string
	// Region is the pinned region, for the same reason.
	Region Region
	// Method and Path are what was actually requested. They are reported rather
	// than assumed, because a wrong prefix is the failure this package has twice
	// shipped.
	Method string
	Path   string
	// Status is the HTTP status code, or 0 when no response arrived at all.
	Status int
	// Body is the raw response body, bounded by MaxResponseBytes. It is returned
	// UNCHANGED: pretty-printing it, truncating it or scrubbing it would make the
	// record a description of the response rather than the response, and the point
	// is to settle an argument with the wire.
	Body string
	// ModelIdentity is what the API CLAIMED it used. Empty when the provider
	// reported none. It is a claim by the provider and is labelled as one wherever
	// it is printed.
	ModelIdentity string
	// LatencyMS is measured here, not taken from the provider.
	LatencyMS int64
	// Err is the classified failure, or nil. It is returned IN the report rather
	// than only as the return value so a caller writing the raw body beside the
	// reason has both in one place.
	Err error
	// At is when the call was made.
	At time.Time
}

// Probe makes ONE call against the pinned host and reports what came back.
//
// It is deliberately not [Client.Observe] and it sends no footage. The open
// question is "does this host, this path and this auth scheme answer", and
// answering it with a clip would ship a site's video to settle a question about
// spelling. So the probe asks the completions endpoint with a text-only message
// and no asset: the cheapest thing that exercises the path, the auth header and
// the response shape, and uploads nothing.
//
// It never follows a redirect, which is a property of the shared HTTP client
// (see newHTTPClient) and is restated here in the report so a reader of a probe
// result does not have to know it: a probe is a thing an operator runs by hand
// against a host they typed, and an open redirect on a probe is an open redirect.
//
// It reserves nothing from the daily quota and it records no observation. A probe
// is not a run and must not be able to spend a site's inference budget.
func (c *Client) Probe(ctx context.Context) (ProbeReport, error) {
	started := c.now()
	rep := ProbeReport{Host: c.host, Region: c.region, Method: http.MethodPost, Path: PathCompletions, At: started.UTC()}

	// The region check is the same one every other call makes, reached through the
	// same function. A probe that could be pointed somewhere else would be the
	// one code path in this package with no allow-list on it.
	if err := c.requireRegion(c.host); err != nil {
		rep.Err = err
		return rep, err
	}

	body, err := c.encodeProbe()
	if err != nil {
		rep.Err = err
		return rep, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.host+PathCompletions, strings.NewReader(string(body)))
	if err != nil {
		rep.Err = fmt.Errorf("vlm: the probe request could not be built: %w", err)
		return rep, rep.Err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "ApiKey "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		rep.LatencyMS = elapsedMS(started, c.now())
		if strings.Contains(err.Error(), errRedirect{}.Error()) {
			rep.Err = fmt.Errorf("%w: nothing was sent to the redirect target", ErrRedirectRefused)
			return rep, rep.Err
		}
		rep.Err = fmt.Errorf("%w: %s %s: %w", ErrRegionUnavailable, rep.Method, rep.Path, err)
		return rep, rep.Err
	}
	defer drain(resp)

	rep.Status = resp.StatusCode
	rep.Body = readBounded(resp)
	rep.LatencyMS = elapsedMS(started, c.now())

	// The model name is read even on a failure status. A 400 whose body names the
	// model it could not find is exactly the evidence that settles assumption 6,
	// and a probe that dropped it on the error path would throw away the answer it
	// was run to get.
	//
	// It is decoded LENIENTLY here, unlike every other decode in this package.
	// Everywhere else a body is being validated into an answer; here it is being
	// read for what it says, and a provider that adds a field has not produced a
	// malformed answer — it has produced a different shape than the documentation
	// claims, which is the thing a probe exists to discover.
	var wire answerResponse
	if err := json.Unmarshal([]byte(rep.Body), &wire); err == nil {
		rep.ModelIdentity = wire.Model
	}
	if err := c.classify(resp); err != nil {
		rep.Err = err
		return rep, err
	}
	if rep.ModelIdentity == "" {
		rep.Err = fmt.Errorf("%w: the response carried no model name, so assumption 7 — that the API documents no model identity — "+
			"is contradicted rather than confirmed, and the raw body beside this line is the evidence", ErrMalformedAnswer)
		return rep, rep.Err
	}
	return rep, nil
}

// encodeProbe builds the probe's request body: the same typed content array, with
// a text part and NO asset part.
//
// The typed array is deliberate even though there is no asset. The point of the
// probe is to find out whether the provider accepts the content shape this client
// sends, and sending a flat string to test that would test the wrong shape.
func (c *Client) encodeProbe() ([]byte, error) {
	if strings.TrimSpace(c.cfg.APIModelName) == "" {
		return nil, errors.New("vlm: no API model name; a probe with no model would not settle whether the host serves the model this run asks for")
	}
	return json.Marshal(completionRequest{
		Model: c.cfg.APIModelName,
		Messages: []chatMessage{{
			Role: "user",
			Content: []contentPt{
				{Type: "text", Text: probeText},
			},
		}},
		Temperature:    c.cfg.Temperature,
		ResponseFormat: responseFmt{Type: "json_object"},
		MaxTokens:      ProbeMaxTokens,
	})
}

// probeText is what the probe asks. It is the required output shape and nothing
// else, so a 200 means the host, the path, the auth scheme and the JSON mode all
// work, and the answer is irrelevant.
//
// It is the `alarm` prompt's SHAPE without its content: a probe has no clip, so
// there is no region and no incident, and a prompt about a region the probe did
// not send would be asking about footage that does not exist.
const probeText = "This is a connectivity probe from an operator tool. There is no video. " +
	"Answer with a single JSON object and nothing else — no prose before it, no code fence, no " +
	"explanation after it. Exactly these keys, no others:\n" +
	`  {"verdict": string, "summary": string, "confidence": number, "insufficient_evidence": boolean}` + "\n" +
	"  - verdict is one of: confirmed, false_positive, needs_review, none.\n" +
	"  - summary is one or two sentences saying that this is a connectivity probe.\n" +
	"  - confidence is a number in [0,1].\n" +
	"  - insufficient_evidence is true when the request does not show enough to decide.\n" +
	"  Never guess, and never return an empty object.\n"

// ProbeMaxTokens is the probe's answer ceiling.
//
// It is [ask]'s ceiling and not a separate number: the probe's answer is a fixed
// short sentence like every other answer, and a probe that asked for more tokens
// would be asking the provider for more than the pipeline ever asks for, which
// makes the probe a worse rehearsal of the real path rather than a better one.
const ProbeMaxTokens = 256

// readBounded reads a response body under the same ceiling every other read uses.
func readBounded(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < MaxResponseBytes {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}
