package milestone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxBodyBytes bounds a response body.
//
// Milestone's feeds return a few hundred events per page; this cap is three
// orders of magnitude above that. It exists because the adapter runs on a timer
// and a peer that streams without end would otherwise make the process allocate
// until the connection drops.
const MaxBodyBytes = 32 << 20

// DefaultPageSize is the adapter's page size.
//
// Inside the documented 1..2000 range
// (events-rest-v1.reference.md:262). A page size outside the range the vendor
// declares is a request it is entitled to refuse, so it is clamped rather than
// sent.
const DefaultPageSize = 200

// MaxPageSize is the largest page the documented API accepts.
const MaxPageSize = 2000

// httpTransport is the production Transport.
//
// It exists because the only transport the package shipped was
// ReplayTransport, which serves fixtures. That is a genuine gap rather than a
// detail: an adapter with no live transport is an adapter nothing calls, which
// is the same state the pipeline was in before this file.
type httpTransport struct {
	base   *url.URL
	token  string
	client *http.Client
}

// NewHTTPTransport builds a live transport.
//
// baseURL is the API root. token is the bearer credential; an empty token is
// accepted at construction and refused at the first call, because a site that
// has not finished setting up credentials should still start and report the
// problem in /v1/health rather than not start at all.
//
// timeout is the client-side deadline. It is the only one: the adapter polls on
// a loop and a hung Milestone would otherwise hold the ingest loop for ever,
// which is a stalled pipeline that still answers /v1/health as ready.
func NewHTTPTransport(baseURL, token string, timeout time.Duration) (Transport, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("milestone: a base URL is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("milestone: %q is not a URL: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("milestone: %q must be an http:// or https:// URL", baseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("milestone: %q has no host", baseURL)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &httpTransport{
		// The trailing slash is stripped so route.Path joins with exactly one
		// separator. Without it every URL is built by string concatenation and one
		// of the two shapes has a double slash in it forever.
		base:  u,
		token: strings.TrimSpace(token),
		client: &http.Client{
			Timeout: timeout,
			// Do not follow redirects.
			//
			// Go's default is to follow up to ten, and it replays the request
			// headers on each hop. The Authorization bearer is copied to whatever
			// host the Location names, so a Milestone that answers 302 - because it
			// is behind a load balancer that moved, or because anything on the path
			// can answer — gets the site's Milestone credential sent to a host
			// nobody configured. A 3xx is a fact about the endpoint, and this
			// adapter reports facts; it does not resolve them.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Get performs one read.
func (t *httpTransport) Get(ctx context.Context, route Route, query Query) (*Response, error) {
	if t.token == "" {
		return nil, &UnauthenticatedError{Route: route, Status: 0}
	}

	target, err := t.urlFor(route, http.MethodGet, query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("milestone: build the request for %s: %w", describe(route), err)
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/json")
	// Identifies the caller to the vendor's logs, which is the difference between
	// "an unknown client is polling us" and a ticket somebody can action.
	req.Header.Set("User-Agent", "ai-captain-security-core/1")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("milestone: %s: %w", describe(route), err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxBodyBytes))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("milestone: read the response for %s: %w", describe(route), err)
	}

	// The allow-listed headers are copied rather than the whole map: a transport
	// that forwards every header puts the vendor's cookie and request id into an
	// error message somewhere downstream.
	out := &Response{
		Status: resp.StatusCode,
		Body:   raw,
		Route:  route,
		Headers: map[string]string{
			"Content-Type":  resp.Header.Get("Content-Type"),
			"Next-Cursor":   resp.Header.Get("Next-Cursor"),
			"X-Total-Count": resp.Header.Get("X-Total-Count"),
		},
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &UnauthenticatedError{Route: route, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusNotFound:
		return nil, &NotFoundError{Route: route}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// A 3xx is refused by name rather than reported as "3xx is not 2xx". An
		// operator whose Milestone moved gets a message that says so, and the
		// Authorization header that was about to be replayed to another host is
		// not.
		return nil, fmt.Errorf("milestone: %s returned %d to %s; redirects are not followed. "+
			"Either the integration's base URL is wrong or something between here and "+
			"Milestone is redirecting. Point base_url at the address that answers",
			describe(route), resp.StatusCode, resp.Header.Get("Location"))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &StatusError{Route: route, Status: resp.StatusCode, Body: summarize(raw)}
	}

	if !json.Valid(raw) {
		return nil, &MalformedError{
			Route: route,
			Why: "the body is not JSON, so a feed that returns an HTML error page " +
				"would otherwise parse to zero events and look like a quiet site",
		}
	}
	return out, nil
}

// urlFor builds the request URL for any route, read or write.
//
// One builder, not two. `post` and `get` share it on purpose: a write-back surface
// with its own URL construction is how the base-URL trailing-slash bug gets a
// second, differently broken copy.
//
// # The query parameters are the documented ones
//
// `events-rest-v1.reference.md:219-263` is explicit about how this API takes a
// cursor, and it is not how this file used to ask:
//
//   - time: `?time=gt:'<ISO8601>'` — the filter syntax is `property=operator:value`,
//     string and DateTime values in single quotes, and for `time` only `gt` and
//     `lt` are allowed (:221-222, :311-313). There is no `since`.
//   - size: `?size=<n>`, between 1 and 2000 (:262). There is no `limit`.
//   - continuation: `?page=<n>` plus a `_links.next` value in the body
//     (:261, :377-379). There is no `cursor` parameter, and the `Next-Cursor`
//     *header* this adapter used to follow appears nowhere in either document.
//
// The old names were not merely different, they were inert: every poll asked the
// same unfiltered question and EVENTS:300 answers that by returning the oldest
// stored events first, from the start of the retention window, every time.
//
// The `Next-Cursor` header is still honoured, because a header the documents do
// not mention may still be what a given build sends, and honouring both costs one
// branch. Which one a real server uses is a probe question; see
// docs/vendor/milestone/route-audit.md §2.2.
func (t *httpTransport) urlFor(route Route, method string, query Query) (*url.URL, error) {
	target := *t.base
	target.Path = strings.TrimRight(t.base.Path, "/") + "/" + strings.TrimLeft(route.Path, "/")
	target.RawQuery = ""

	q := url.Values{}
	// The vendor selects a POST's operation with a query parameter
	// (bookmarks-rest-v1.openapi.yaml:167-171), so the task is part of the route
	// rather than something a call site supplies.
	if route.Task != "" {
		q.Set("task", route.Task)
	}

	if next, isLink := nextPageLink(query.Cursor); isLink {
		page, err := url.Parse(next)
		if err != nil {
			return nil, fmt.Errorf("milestone: %s handed back a next page this adapter cannot read: %w",
				describe(route), err)
		}
		// A next link is a URL the vendor chose. If it names another host, the
		// site's bearer credential would go there, which is the same reason
		// redirects are not followed: a 3xx is a fact about the endpoint and this
		// adapter reports facts.
		if page.Host != "" && !strings.EqualFold(page.Host, t.base.Host) {
			return nil, fmt.Errorf("milestone: %s handed back a next page on %s, which is not the "+
				"server this adapter is configured for (%s). Refusing to send the site's "+
				"credentials to a host nobody configured", describe(route), page.Host, t.base.Host)
		}
		target.Path = page.Path
		if extra, err := url.ParseQuery(page.RawQuery); err == nil {
			for name, values := range extra {
				for _, v := range values {
					q.Add(name, v)
				}
			}
		}
		target.RawQuery = q.Encode()
		return &target, nil
	}

	if query.Cursor != "" {
		q.Set("cursor", query.Cursor)
	}
	// The read parameters go on a read. A bookmark POST carries none of them, and
	// sending `size=200` to a documented body-and-task endpoint is asking the
	// vendor a question about an endpoint that has no paging.
	if method == http.MethodGet {
		if query.Since != nil {
			q.Set("time", "gt:'"+query.Since.UTC().Format(time.RFC3339Nano)+"'")
		}
		limit := query.Limit
		if limit <= 0 {
			limit = DefaultPageSize
		}
		if limit > MaxPageSize {
			limit = MaxPageSize
		}
		q.Set("size", strconv.Itoa(limit))
		// Last, so a named parameter overrides one this file derived. A caller that
		// says `size=1` or `time=...` is answering a specific question, and a
		// derived value that silently won would make the request something other
		// than what was asked for.
		//
		// Inside the GET guard, not after it, so that "a write carries no read
		// parameters" is structural rather than a consequence of no caller
		// currently being able to supply one.
		for name, value := range query.Params {
			q.Set(name, value)
		}
	}
	target.RawQuery = q.Encode()
	return &target, nil
}

// nextPageLink reports whether a cursor is a URL rather than an opaque token.
//
// The documented continuation is `_links.next`, which is a URI
// (events-rest-v1.reference.md:377-379), so a cursor that starts like one is
// followed as one. Anything else — including the synthetic cursors in the tests —
// stays an opaque parameter, which is what a server that answers with a
// `Next-Cursor` header expects.
func nextPageLink(cursor string) (string, bool) {
	if strings.HasPrefix(cursor, "/") || strings.HasPrefix(cursor, "http://") ||
		strings.HasPrefix(cursor, "https://") {
		return cursor, true
	}
	return "", false
}

// Close releases the connection pool.
//
// Closing is idempotent and safe to call on a transport whose Close was never
// reached, because the daemon defers it unconditionally.
func (t *httpTransport) Close() error {
	t.client.CloseIdleConnections()
	return nil
}

// summarize renders an error body for a message, bounded.
//
// The body belongs to the vendor. It is echoed because a vendor's error message
// is often the whole diagnosis, and bounded because an untrusted peer choosing
// the length of a string that lands in a log line is a lever.
func summarize(raw []byte) string {
	var doc struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, candidate := range []string{doc.Error, doc.Message, doc.Detail} {
			if candidate != "" {
				return clip(candidate, 200)
			}
		}
	}
	return clip(strings.TrimSpace(string(raw)), 200)
}

func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
