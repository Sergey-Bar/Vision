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
const DefaultPageSize = 200

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
			// host the Location names, so a Milestone that answers 302 — because it
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

	target := t.urlFor(route, query)

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
		return nil, fmt.Errorf("milestone: %s returned %d: %s",
			describe(route), resp.StatusCode, summarize(raw))
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

// urlFor builds the request URL.
//
// The path is joined onto the base with a single separator, and every query
// parameter is set explicitly rather than appended by concatenation, so a cursor
// containing a reserved character is encoded rather than becoming a second
// parameter.
//
// It cannot fail, so it does not return an error. It was shaped as
// (*url.URL, error) back when it composed the query by string concatenation,
// where an unencoded ampersand in a cursor was a real failure; the values are
// built through url.Values and Encode now, and a nil error on that path is a
// result every caller would have had to check to learn nothing.
func (t *httpTransport) urlFor(route Route, query Query) *url.URL {
	target := *t.base
	target.Path = strings.TrimRight(t.base.Path, "/") + "/" + strings.TrimLeft(route.Path, "/")

	q := url.Values{}
	if query.Since != nil {
		q.Set("since", query.Since.UTC().Format(time.RFC3339))
	}
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	q.Set("limit", strconv.Itoa(limit))
	if query.Cursor != "" {
		q.Set("cursor", query.Cursor)
	}
	target.RawQuery = q.Encode()
	return &target
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
