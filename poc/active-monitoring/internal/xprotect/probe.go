package xprotect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// RouteState is what one documented route did when a probe asked it.
//
// It is a closed set of four because those four are the only outcomes an operator
// can act on, and each has a different next step:
//
//   - reachable — the route answered with the shape the specification declares
//   - contradicted — the route answered, but not with that shape
//   - unauthenticated — the route exists and refused the credentials
//   - absent — the route did not answer at all
//
// "contradicted" and "unauthenticated" are separate because they are the two ways a
// published API most often differs from the implementation, and they have opposite
// diagnoses: the first means the DOCUMENT is wrong about this server, the second
// means the CREDENTIALS are.
type RouteState string

const (
	RouteReachable       RouteState = "reachable"
	RouteContradicted    RouteState = "contradicted"
	RouteUnauthenticated RouteState = "unauthenticated"
	RouteAbsent          RouteState = "absent"
	// RouteUnknown is what a probe reports when it could not reach the host at all,
	// so every route is unknown for the same reason. It exists so a table of
	// fourteen `absent` rows is not printed when the answer is one sentence: the
	// server did not answer.
	RouteUnknown RouteState = "unknown"
)

// RouteProbe is one row of the probe's answer.
type RouteProbe struct {
	Op     string
	Method string
	Path   string
	State  RouteState
	Status int
	// Detail is a sentence a person can act on. It is never a raw body: a body is
	// evidence and belongs in a file the probe names, not in a column of a table
	// somebody will screenshot.
	Detail string
}

// ProbeInput is what the probe needs to ask the questions it can ask.
//
// It carries no tag and no detection: a probe is read-only by construction, and the
// only write in this package's surface is `PushEvents`. There is deliberately no way
// to reach it from here.
type ProbeInput struct {
	CameraGUID    string
	EventTypeGUID string
	// Tag is a content-derived tag the run WOULD have pushed. It is never pushed; it
	// is used to ask whether the tag query returns an empty list rather than an
	// error, which is the only way to learn whether the filter syntax is the one this
	// client writes.
	Tag string
}

// Probe asks every documented route and reports what each one did.
//
// It is deliberately READ-ONLY. The single most useful thing a probe can tell an
// operator is "your credentials work and your event type exists", and the fastest way
// to lose their trust is a probe that posts a real event into a live VMS at 09:00 on
// the day of a demo. There is no flag to override this, and there is no handle in
// ProbeInput that reaches `PushEvents`.
//
// It never promotes anything. A route that answered with the documented shape is
// `reachable`, not `verified`: verification is a human flipping a row in
// contracts/xprotect/contract.json after reading the evidence, and a probe that could
// do that would be a probe that could mark its own homework.
func (c *Client) Probe(ctx context.Context, in ProbeInput) ([]RouteProbe, error) {
	if err := c.Authenticate(ctx); err != nil {
		// One refusal for the whole table. Every route is unknown for the same
		// reason, and printing fourteen `absent` rows would be fourteen claims.
		return []RouteProbe{{
			Op: "connect", Method: http.MethodPost, Path: PathToken, State: RouteUnknown,
			Status: statusOf(err), Detail: err.Error(),
		}}, nil
	}
	var out []RouteProbe
	out = append(out, c.probeToken(ctx))
	out = append(out, c.probeEventTypes(ctx))
	out = append(out, c.probeTagQuery(ctx, in.Tag))
	out = append(out, c.probeBookmarkReadBack(ctx))
	return out, nil
}

func (c *Client) probeToken(ctx context.Context) RouteProbe {
	p := RouteProbe{Op: "authenticate", Method: http.MethodPost, Path: PathToken}
	if c.token == "" {
		p.State = RouteUnauthenticated
		p.Detail = "the token endpoint answered but no access token was issued"
		return p
	}
	p.State = RouteReachable
	p.Status = http.StatusOK
	p.Detail = "an access token was issued for these credentials"
	return p
}

// probeEventTypes lists the event types the server offers, which is the only way to
// answer "is there an Analytics Event Type we may push to, and what is its GUID".
func (c *Client) probeEventTypes(ctx context.Context) RouteProbe {
	p := RouteProbe{Op: "list event types", Method: http.MethodGet, Path: PathEventTypes}
	var body struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	status, err := c.getInto(ctx, PathEventTypes, nil, &body)
	p.Status = status
	switch {
	case err != nil:
		p.State, p.Detail = stateFrom(err, status)
	case len(body.Data) == 0:
		p.State = RouteContradicted
		p.Detail = "the route answered with no event types; a Milestone instance always publishes at least one, so the response shape is not the documented one"
	default:
		p.State = RouteReachable
		p.Detail = fmt.Sprintf("%d event type(s); pick the Analytics one and put its GUID in SP_POC_EVENT_TYPE_GUID", len(body.Data))
	}
	return p
}

// probeTagQuery asks whether the tag filter this client WRITES is the one the server
// accepts. It queries a tag that does not exist, so a correct answer is an empty
// list; a syntax error would come back as a 4xx and is the whole question.
func (c *Client) probeTagQuery(ctx context.Context, tag string) RouteProbe {
	p := RouteProbe{Op: "search events by tag", Method: http.MethodGet, Path: PathEvents}
	if strings.TrimSpace(tag) == "" {
		p.State = RouteUnknown
		p.Detail = "no tag was supplied, so the tag query was not attempted. The read-back in 1.1 supplies one."
		return p
	}
	q := map[string]string{"tag": "equals:'" + tag + "'"}
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	status, err := c.getInto(ctx, PathEvents, q, &body)
	p.Status = status
	switch {
	case err != nil:
		p.State, p.Detail = stateFrom(err, status)
	case body.Data == nil:
		p.State = RouteContradicted
		p.Detail = "the route answered with no `data` array; an empty result must still be an array, and its absence is the wrong shape"
	default:
		p.State = RouteReachable
		p.Detail = fmt.Sprintf("the tag filter was accepted and returned %d row(s) for a tag this run never pushed", len(body.Data))
	}
	return p
}

// probeBookmarkReadBack lists bookmarks with a filter that matches nothing. It is
// the read-back half of AC4 without the write: a server that answers the query can
// be asked "is my event there" later, and one that does not cannot.
func (c *Client) probeBookmarkReadBack(ctx context.Context) RouteProbe {
	p := RouteProbe{Op: "read back a bookmark", Method: http.MethodGet, Path: PathBookmarks}
	q := map[string]string{"reference": "equals:'aicap-probe-never-created'"}
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	status, err := c.getInto(ctx, PathBookmarks, q, &body)
	p.Status = status
	switch {
	case err != nil:
		p.State, p.Detail = stateFrom(err, status)
	case body.Data == nil:
		p.State = RouteContradicted
		p.Detail = "the route answered with no `data` array; the documented response is an array even when nothing matches"
	default:
		p.State = RouteReachable
		p.Detail = fmt.Sprintf("the bookmark query was accepted and returned %d row(s) for a bookmark that does not exist", len(body.Data))
	}
	return p
}

// stateFrom maps a classified error to the four states.
//
// A 5xx is `contradicted` and not `absent`: something answered, and what it said
// does not match the specification. A transport failure is `unknown`, because the
// host could not be reached and saying the route is absent would be a claim about
// the ROUTE when the answer was about the network.
func stateFrom(err error, status int) (RouteState, string) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return RouteUnauthenticated, fmt.Sprintf("status %d: the route exists and refused these credentials", status)
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return RouteAbsent, fmt.Sprintf("status %d: the route is not published at this path", status)
	case status >= 500:
		return RouteContradicted, fmt.Sprintf("status %d: the route answered with a server error rather than the documented shape", status)
	case status == 0:
		return RouteUnknown, err.Error()
	default:
		return RouteContradicted, fmt.Sprintf("status %d: %s", status, err)
	}
}

func statusOf(err error) int {
	switch e := err.(type) {
	case *RefusedError:
		return e.Status
	case *RetryableError:
		return e.Status
	default:
		return 0
	}
}
