// Package xprotect is the Milestone MIP VMS REST client.
//
// It knows the wire format and nothing else. It does not know what a detection
// is, it does not touch the database, and it does not know that a simulator
// exists: the simulator is a server on loopback that happens to answer, and the
// only thing the client needs to be able to say about it is "who are you",
// which every push target must answer.
//
// The paths and body fields here are checked against the specification
// downloaded into contracts/xprotect/, by a test in this package. A path that
// drifts from the specification fails that test.
package xprotect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/shared/aistate"
)

// The documented paths. They are constants, not strings assembled at call sites,
// so a grep for a path finds exactly one place and the contract test has
// something to compare against the specification file.
const (
	PathToken      = "/API/IDP/connect/token"
	PathEvents     = "/api/rest/v1/events"
	PathEventsBulk = "/api/rest/v1/events/bulk"
	PathBookmarks  = "/api/rest/v1/bookmarks"
	PathEventTypes = "/api/rest/v1/eventTypes"
	APIRestPrefix  = "/api/rest/v1"
)

// The documented status codes.
const (
	StatusAccepted    = 202 // POST /events
	StatusMultiStatus = 207 // POST /events/bulk
	StatusOKBookmark  = 200 // POST /bookmarks
)

// Credentials. The secret is read from the environment and never written to a
// file, a log line, a database row or an outbox payload.
type Credentials struct {
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
}

// Config is the client configuration.
type Config struct {
	BaseURL     string
	Timeout     time.Duration
	Credentials Credentials
	// Logf receives operational lines. It must never receive a token or a
	// secret; the package asserts that by construction, passing only the fields
	// it lists below.
	Logf func(format string, args ...any)
}

// Client talks to the VMS.
type Client struct {
	cfg  Config
	http *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	// grant records which grant type actually worked, so the report can print
	// it instead of claiming both were used.
	grant string
}

// New validates the configuration. Plain http to a non-loopback host is refused:
// the token and the bearer-authenticated push must not cross a network in clear
// text.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("xprotect: no base URL: there is no push target, so no event is ever attempted and AC4 is NOT RUN")
	}
	if !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return nil, fmt.Errorf("xprotect: base URL %q is not http or https", cfg.BaseURL)
	}
	if strings.HasPrefix(cfg.BaseURL, "http://") && !isLoopbackHost(cfg.BaseURL) {
		return nil, fmt.Errorf("xprotect: base URL %q is plain http to a non-loopback host; the OAuth token and the bearer-authenticated push would cross the network in clear text — use https", cfg.BaseURL)
	}
	if cfg.Timeout <= 0 {
		return nil, errors.New("xprotect: the push timeout must be positive; a hung provider with no deadline is a silent hang, not a timeout")
	}
	if cfg.Credentials.ClientID == "" {
		return nil, errors.New("xprotect: no client id: there is no OAuth client, so the token call cannot be made and auth_failed is terminal for the whole run")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: cfg.Timeout,
			// A redirect would forward the Authorization header to wherever the
			// server points. Not following them keeps the token on the host the
			// operator named.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func isLoopbackHost(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Grant records which grant type the token call actually used.
func (c *Client) Grant() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.grant
}

// TokenExpiresAt is when the current token is considered expired. The report
// prints it, because a token whose lifetime was guessed is a fact the operator
// needs.
func (c *Client) TokenExpiresAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expiresAt
}

// ErrAuth is returned when the token call itself failed. It is terminal for the
// push path, and the caller must record it rather than retry forever.
type ErrAuth struct {
	Status   int
	Detail   string
	Grant    string
	Response string
}

func (e *ErrAuth) Error() string {
	return fmt.Sprintf("auth_failed: the IDP token call with grant_type=%s returned %d: %s; the secret is not shown, and without a token nothing can be pushed, so this is terminal for the push path until the credentials are fixed", e.Grant, e.Status, e.Detail)
}

// Authenticate gets a token. client_credentials is preferred; password is the
// fallback, and which one worked is recorded.
func (c *Client) Authenticate(ctx context.Context) error {
	_, err := c.accessToken(ctx, true)
	return err
}

// token returns a valid token, refreshing on demand and on a 401.
func (c *Client) accessToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	if !force && c.token != "" && time.Now().Before(c.expiresAt) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	attempts := []struct {
		grant string
		form  url.Values
	}{
		{"client_credentials", url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {c.cfg.Credentials.ClientID},
			"client_secret": {c.cfg.Credentials.ClientSecret},
		}},
		{"password", url.Values{
			"grant_type": {"password"},
			"client_id":  {c.cfg.Credentials.ClientID},
			"username":   {c.cfg.Credentials.Username},
			"password":   {c.cfg.Credentials.Password},
		}},
	}
	var last error
	for _, a := range attempts {
		tok, exp, err := c.callToken(ctx, a.grant, a.form)
		if err == nil {
			c.mu.Lock()
			c.token, c.expiresAt, c.grant = tok, exp, a.grant
			c.mu.Unlock()
			c.cfg.Logf("token obtained with grant_type=%s, expires at %s", a.grant, exp.UTC().Format(time.RFC3339))
			return tok, nil
		}
		last = err
		// Only a credentials problem is worth trying the other grant for. A
		// network failure or a 5xx will fail again, and the error the caller
		// gets must say "retry", not "your secret is wrong" — a provider that is
		// down is not a credential problem and must not be recorded as one.
		if _, isAuth := err.(*ErrAuth); !isAuth {
			return "", err
		}
	}
	return "", last
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	// The quickstart shows only access_token. expires_in is preferred when it is
	// there and the JWT's exp is the fallback; see tokenExpiry.
	ExpiresIn *int64 `json:"expires_in"`
}

// callToken performs the token request.
//
// A 400 or 401 is a credentials problem: *ErrAuth, and the caller may try the
// other grant. Anything else — a 5xx, a reset connection, a timeout — is a
// transport problem: *RetryableError, and the caller must not be told the secret
// is wrong when the server is simply down. Getting that distinction backwards
// would send an operator to rotate a credential that is fine.
func (c *Client) callToken(ctx context.Context, grant string, form url.Values) (string, time.Time, error) {
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathToken
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, &RetryableError{Status: 0, Detail: "the token request could not be built: " + err.Error()}
	}
	// The documented content type. A JSON body here is a 400 from a real VMS.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", time.Time{}, &RetryableError{Status: 0, Detail: "the token call did not complete: " + scrub(err.Error(), c)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return "", time.Time{}, &ErrAuth{Status: resp.StatusCode, Detail: firstLine(string(body)), Grant: grant, Response: string(body)}
	default:
		return "", time.Time{}, &RetryableError{Status: resp.StatusCode, Detail: "the token call answered " + firstLine(string(body))}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, &RetryableError{Status: resp.StatusCode, Detail: "the token response is not usable JSON, so whether a token was issued is unknown"}
	}
	if tr.AccessToken == "" {
		return "", time.Time{}, &RetryableError{Status: resp.StatusCode, Detail: "the token response has no access_token"}
	}
	return tr.AccessToken, tokenExpiry(tr, c.cfg.Timeout), nil
}

// tokenExpiry prefers expires_in and falls back to the exp claim in the JWT.
//
// The fallback reads the claim and does not verify the signature. That is
// deliberate and it is safe here: the token arrived over TLS from the host the
// operator named, so a forged claim is not the threat. It is written down
// because "we parsed a JWT" reads like "we validated a JWT" and it is not.
func tokenExpiry(tr tokenResponse, fallback time.Duration) time.Time {
	if tr.ExpiresIn != nil && *tr.ExpiresIn > 0 {
		return time.Now().Add(time.Duration(*tr.ExpiresIn) * time.Second)
	}
	if exp, ok := jwtExp(tr.AccessToken); ok {
		return exp
	}
	// Nothing declared. Assume the request timeout, refresh often, and say so:
	// an assumed lifetime that is silently treated as declared is how a run
	// spends an hour sending 401s.
	return time.Now().Add(fallback)
}

// jwtExp reads the exp claim. It splits on dots, base64url-decodes the payload,
// and does not check the signature. See tokenExpiry.
func jwtExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}

// Outcome is what a push attempt produced. The states are the ones the
// outbox stores, so a caller cannot invent a fourth.
type Outcome struct {
	State    string // "push_sent", "push_refused", or "dead" for the caller's use
	RemoteID string
	Status   int
	Detail   string
	// Body is the raw response, kept for the report's fault table.
	Body string
}

// RefusedError is a definite "no" from the provider. It is terminal for that
// detection, not "waiting".
type RefusedError struct {
	Status int
	Detail string
	Body   string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("push_refused: the VMS answered %d: %s; this is final for this detection, not a pending state", e.Status, e.Detail)
}

// RetryableError is a failure worth another attempt: 5xx, a timeout, a reset
// connection, or a body that did not arrive whole.
type RetryableError struct {
	Status int
	Detail string
	Body   string
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("push failed in a way that may succeed later: %s", e.Detail)
}

// Box is the bounding box in the AnalyticsData payload.
type Box struct {
	Top    int `json:"top"`
	Left   int `json:"left"`
	Bottom int `json:"bottom"`
	Right  int `json:"right"`
}

// Object is analyticData.object.
type Object struct {
	Type        string  `json:"type"`
	Value       string  `json:"value"`
	Confidence  float64 `json:"confidence"`
	BoundingBox Box     `json:"boundingBox"`
}

// Rule is analyticData.rule.
type Rule struct {
	Name string `json:"name"`
}

// Vendor is analyticData.vendor.
type Vendor struct {
	Name string `json:"name"`
}

// EvidenceRef is the POC's own evidence pointer, carried inside the event so the
// event is enough to investigate on its own. A bookmark points at a VMS
// recording, and this build has no VMS recording, so without this the pushed
// event would reference nothing.
//
// The inner fields of `data` are this repository's OWN extension. Milestone's
// downloaded events specification declares `data` as "AnalyticsData (object or
// null)" and the request sample is `"data": { }` — the inner fields are never
// enumerated, so a real VMS may drop this object silently. That is why
// [Provenance.TagSuffix] exists and why every rendered line names the carrier it
// used: a demo that prints provenance the VMS discarded is exactly the failure
// class this repository exists to prevent, and the only defence is to say which
// field the reader is looking at.
type EvidenceRef struct {
	StorageRef string `json:"storageRef"`
	SHA256     string `json:"sha256"`
	TimeBegin  string `json:"timeBegin"`
	TimeEnd    string `json:"timeEnd"`
	Note       string `json:"note"`
}

// Provenance is CONTRACT.md R8's attribution bundle: everything a reader needs in
// order to CHECK a sentence this module pushed, rather than take it on trust.
//
// Six fields and no more, and none of them is a weight hash. R8 forbids one: a
// hosted model has no weights to hash, so inventing one would be the failure the
// rule exists to prevent. `ModelIdentity` is what the API SAID it used and is
// rendered with the word "provider claim" beside it every time, because a reader who
// does not know that will read it as something this module established.
type Provenance struct {
	// AIState is the ask's state, from the aistate vocabulary. It is REQUIRED on
	// every pushed event, because "the model was asked and said this" and "nobody
	// could reach the model" are different facts about the same event and an event
	// that does not say which one it is is unreadable.
	AIState string
	// Verdict is what the answer was, and is empty when there was no answer. It is
	// never defaulted: a verdict on an unasked window is the fabricated negative.
	Verdict         string
	Summary         string
	ModelIdentity   string
	ContractVersion string
	Temperature     float64
	OutputHash      string
	// AbsenceCount is how many proposals in the window share this event's
	// blindness. Zero on an ordinary detection event.
	//
	// It lives here rather than on EventInput because it is part of what a reader
	// has to be told about the sentence: "this camera was not assessed" and "this
	// camera was not assessed 500 times in an hour" are different alarms, and the
	// second is the one that tells an operator whether to look now or in the
	// morning.
	AbsenceCount int
}

// Absent reports whether the ask produced no answer.
//
// It is the branch that decides what an event must carry. A blindness event has no
// model identity, no contract version and no output hash — and that absence is the
// fact, so requiring them there would force the writer to invent all three.
func (p Provenance) Absent() bool { return aistate.IsBlindness(p.AIState) }

// Complete reports whether an ANSWERED ask carries the three things R8 requires.
func (p Provenance) Complete() bool {
	return p.ModelIdentity != "" && p.ContractVersion != "" && p.OutputHash != ""
}

// Validate is the refusal pair, kept together because they are the same sentence
// with two different endings: an event must say what state its ask was in, and an
// event that was answered must say who answered it.
//
// Two conditions and not one, because they are two defects and folding them makes
// one unreachable: an event with no state at all never reaches the attribution
// checks, and a single combined refusal cannot say which of the two things is
// missing. Each has its own red check.
func (p Provenance) Validate() error {
	if strings.TrimSpace(p.AIState) == "" {
		return errors.New("the event names no AI state, so a reader cannot tell whether the model was asked " +
			"and answered or whether we could not reach it. The state is one of " +
			strings.Join(aistate.All, ", ") + " and it is required on every pushed event")
	}
	if !aistate.Valid(p.AIState) {
		return fmt.Errorf("the event names the AI state %q, which is not one of %s. An unknown state is a "+
			"protocol error, not a new answer", p.AIState, strings.Join(aistate.All, ", "))
	}
	if p.Absent() {
		// No model identity here is CORRECT: nobody was asked, so there is nothing to
		// attribute. A verdict would not be: it is the fabricated negative.
		if strings.TrimSpace(p.Verdict) != "" {
			return fmt.Errorf("the event names the AI state %q and also carries the verdict %q, but an unasked "+
				"or unanswered window has no verdict. A verdict here is a negative answer we did not receive", p.AIState, p.Verdict)
		}
		return nil
	}
	var missing []string
	if strings.TrimSpace(p.ModelIdentity) == "" {
		missing = append(missing, "model_identity")
	}
	if strings.TrimSpace(p.ContractVersion) == "" {
		missing = append(missing, "contract_version")
	}
	if strings.TrimSpace(p.OutputHash) == "" {
		missing = append(missing, "output_hash")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the event carries the AI state %q and an attributed sentence, but no %s. R8 requires "+
			"every rendered claim to name the model that produced it, the contract version it was produced under and "+
			"the hash of the validated output; an event with the sentence and none of those is a caption this "+
			"repository cannot check", p.AIState, strings.Join(missing, ", "))
	}
	return nil
}

// Note renders the bundle as the one line that rides in
// [EvidenceRef.Note], with the model's own sentence first and VERBATIM.
//
// Verbatim is the whole point: R8 forbids a paraphrase, because a paraphrase of a
// model's sentence is a sentence this repository invented. It is first because the
// reader in Smart Client reads the beginning of the field, and putting the metadata
// ahead of the sentence would put our bookkeeping where the finding should be.
//
// There is no `model_hash` here and there must never be one. `model=` is labelled
// `provider claim` on every render because that is what it is.
func (p Provenance) Note() string {
	head := strings.TrimSpace(p.Summary)
	if head == "" {
		head = "(no sentence was produced for this event)"
	}
	var b strings.Builder
	b.WriteString(sanitizeNote(head))
	b.WriteString(" | ai-captain: aistate=")
	b.WriteString(p.AIState)
	if p.Verdict != "" {
		b.WriteString(" verdict=")
		b.WriteString(p.Verdict)
	}
	if p.Absent() {
		// Said as a positive fact rather than left as three blank fields: a reader
		// who sees no model identity must be told WHY, and the why is the product.
		b.WriteString(" model=NOT ASKED contract=n/a temperature=n/a output=n/a")
	} else {
		fmt.Fprintf(&b, " model=%s (provider claim) contract=%s temperature=%.4f output=%s",
			p.ModelIdentity, p.ContractVersion, p.Temperature, p.OutputHash)
	}
	if n := p.AbsenceCount; n > 0 {
		fmt.Fprintf(&b, " window_count=%d", n)
	}
	b.WriteString(" carrier=evidence.note+tag")
	return b.String()
}

// sanitizeNote keeps the model's sentence on one line.
//
// A newline inside a note renders in Smart Client as a layout this module cannot
// control, and the line after it looks like a separate field the VMS understood. It
// replaces the control characters with spaces rather than refusing: the sentence is
// the model's and refusing to push it because of a byte would turn a rendering
// problem into a lost event, and a lost event is worse.
func sanitizeNote(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// TagSuffix is the machine-readable half of the provenance, for the one field
// Milestone's specification actually documents as filterable: *"Custom tag
// assignable to the event (for filtering and sorting purposes)."*
//
// It exists because `data.evidence` is an undeclared extension. If a real VMS drops
// the evidence object — and the specification gives no reason to think it will not —
// the tag is what survives, so the absence state and the output hash have to be in
// it as well. Carrying the same two facts in both places is not duplication for its
// own sake: it is the difference between a claim that degrades and a claim that
// vanishes.
//
// It is appended to the caller's idempotency tag rather than replacing it, and that
// keeps idempotency intact: the suffix is derived from the observation, and a
// detection whose answer has not changed produces the same suffix and therefore the
// same tag, so the read-back still collapses a retry onto the original event.
func (p Provenance) TagSuffix() string {
	var b strings.Builder
	b.WriteString("|state=")
	b.WriteString(tagToken(p.AIState))
	if p.Verdict != "" {
		b.WriteString("|v=")
		b.WriteString(tagToken(p.Verdict))
	}
	if p.OutputHash != "" {
		b.WriteString("|out=")
		b.WriteString(shortHash(p.OutputHash))
	}
	if p.AbsenceCount > 0 {
		b.WriteString("|n=")
		b.WriteString(itoa(p.AbsenceCount))
	}
	return b.String()
}

// shortHash is the first 16 hex characters, which is what the tag can carry without
// becoming a sentence. It is a PREFIX and the note carries the whole hash, so a
// reader who needs the full value has it in the field that is meant to be read.
func shortHash(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) <= 16 {
		return tagToken(h)
	}
	return tagToken(h[:16])
}

// tagToken keeps a value usable inside a tag.
//
// It is deliberately NOT the same function as `soak.sanitizeTag`, which builds the
// tag's PREFIX and has different requirements. This one has to survive being handed
// a provider's model identity, which may contain spaces, dots, slashes and colons,
// and the tag is also the query in `FindByTag`'s URL: an unescaped space would make
// the read-back 400 and the push would look like it worked while nothing could find
// it again. So anything outside [a-z0-9_-] becomes a dash and runs of dashes
// collapse.
func tagToken(s string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped too
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func itoa(n int) string { return strconv.Itoa(n) }

// AnalyticsData is the payload of an analytics event.
type AnalyticsData struct {
	Object   Object      `json:"object"`
	Rule     Rule        `json:"rule"`
	Vendor   Vendor      `json:"vendor"`
	Evidence EvidenceRef `json:"evidence"`
}

// EventRequest is the body of POST /events. The required fields are Type,
// Source and Datatype; Tag is what the read-back matches on, and Data is the
// AnalyticsData payload.
type EventRequest struct {
	Type     string        `json:"type"`
	Source   string        `json:"source"`
	Time     string        `json:"time"`
	Datatype string        `json:"datatype"`
	Tag      string        `json:"tag"`
	Data     AnalyticsData `json:"data"`
}

// BookmarkRequest is the body of POST /bookmarks. Header, DevicePath and
// TimeTriggered are the required fields. The body carries no video: a bookmark
// is a pointer, not a copy.
type BookmarkRequest struct {
	Header        string     `json:"header"`
	DevicePath    DevicePath `json:"devicePath"`
	TimeTriggered string     `json:"timeTriggered"`
	TimeBegin     string     `json:"timeBegin,omitempty"`
	TimeEnd       string     `json:"timeEnd,omitempty"`
	Description   string     `json:"description,omitempty"`
	Reference     string     `json:"reference,omitempty"`
}

// DevicePath is the device a bookmark refers to.
type DevicePath struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// EventInput is everything needed to build an event, in the POC's own terms. The
// client does the wire translation so the caller never assembles a Milestone
// body by hand.
type EventInput struct {
	EventTypeGUID string
	SourceGUID    string
	At            time.Time
	Tag           string
	Class         string
	Value         string
	Confidence    float64
	Box           Box
	RuleName      string
	Evidence      EvidenceRef
	// Provenance is R8's bundle and it is REQUIRED. An event with a sentence and no
	// attribution is a caption nobody can check, and the Smart Client shows it to
	// somebody who has no way of knowing that.
	//
	// `Evidence.Note` is NOT where it is carried: the note is built FROM the
	// provenance, so a caller cannot fill in one and forget the other.
	Provenance Provenance
}

// Empty reports whether the box names no region at all.
//
// It exists for RC-6. A box of all zeros is not "a small object" and it is not "a
// conservative default": it is a missing measurement printed as a real one, and
// on a pushed XProtect event it is the difference between a VMS drawing the
// object on the operator's timeline and drawing nothing at all.
func (b Box) Empty() bool {
	return b.Top == 0 && b.Left == 0 && b.Bottom == 0 && b.Right == 0
}

// NonEmpty reports whether the box names a real region. A full-frame box counts.
func (b Box) NonEmpty() bool {
	return b.Right > b.Left && b.Bottom > b.Top
}

// BuildEvent turns a detection into the documented body. The source is
// "cameras/<guid>" because that is the shape the events API expects for a
// camera-originated event.
func BuildEvent(in EventInput) (EventRequest, error) {
	if in.EventTypeGUID == "" {
		return EventRequest{}, errors.New("xprotect: the event has no type guid, which the contract marks required, so the push would be a 400")
	}
	if in.SourceGUID == "" {
		return EventRequest{}, errors.New("xprotect: the event has no source camera guid, so it would name nothing and an Alarm Definition could never match it")
	}
	if in.Tag == "" {
		return EventRequest{}, errors.New("xprotect: the event has no tag; the tag is derived from the detection's content and is the only field the read-back can match on")
	}
	// REDCHECK RC-22: every pushed event names the state of its ask.
	//
	// It is a separate refusal from the attribution half below, and it comes FIRST on
	// purpose. An event with no state at all never reaches the attribution checks, so
	// folding the two into one condition would make whichever half came second
	// unreachable, and its red check would pass vacuously. Two defects, two controls.
	if err := in.Provenance.Validate(); err != nil {
		return EventRequest{}, fmt.Errorf("xprotect: refusing to push an event with no attribution: %w", err)
	}
	// REDCHECK RC-6: an event with an all-zero box is refused rather than sent.
	//
	// Every pushed event used to carry top/left/bottom/right all zero while the
	// row in the database beside it held real numbers. That is the worst thing a
	// demo can show, because the bounding box is what makes a VMS draw the object
	// on the operator's timeline — and it fails while the database says otherwise.
	//
	// The check below is the ONLY one that catches the zero box. The inversion
	// check after it deliberately does not: a box that is 0,0,0,0 is inverted by
	// every ordinary definition (`right < left` is false for equal values), so
	// folding the two together would make this line unreachable and the red check
	// for it would pass vacuously. Two refusals, two defects, two controls.
	if in.Box.Empty() {
		return EventRequest{}, fmt.Errorf("xprotect: refusing to push an event whose bounding box is all zero (%+v); "+
			"the region is measured locally and a zero region is a missing measurement, not a conservative default", in.Box)
	}
	if in.Box.Right < in.Box.Left || in.Box.Bottom < in.Box.Top {
		return EventRequest{}, fmt.Errorf("xprotect: refusing to push an event whose bounding box is inverted (%+v); "+
			"bottom/right must be at or above top/left", in.Box)
	}
	// The note is BUILT here rather than accepted from the caller, so a caller cannot
	// push a sentence with no provenance beside it by filling in one and leaving the
	// other. The note the caller supplied, if any, is dropped — which is the point:
	// there is exactly one function in this module that renders a model claim.
	ref := in.Evidence
	ref.Note = in.Provenance.Note()
	// The tag carries the same two facts in machine form. Milestone's specification
	// documents `tag` as assignable "for filtering and sorting purposes" and never
	// enumerates the inner fields of `data`, so `data` is the carrier that may
	// survive and `tag` is the one that certainly does. Both, deliberately.
	tag := in.Tag + in.Provenance.TagSuffix()
	return EventRequest{
		Type:     in.EventTypeGUID,
		Source:   "cameras/" + in.SourceGUID,
		Time:     in.At.UTC().Format("2006-01-02T15:04:05.000Z"),
		Datatype: "analytics",
		Tag:      tag,
		Data: AnalyticsData{
			Object:   Object{Type: in.Class, Value: in.Value, Confidence: in.Confidence, BoundingBox: in.Box},
			Rule:     Rule{Name: in.RuleName},
			Vendor:   Vendor{Name: "aicaptain"},
			Evidence: ref,
		},
	}, nil
}

// TriggerEvent posts one analytics event and reads it back. The read-back is
// part of the operation, not a separate convenience: the Events API has no
// idempotency key, so the only way to know a 202 was not a swallowed duplicate
// is to look for the tag afterwards.
func (c *Client) TriggerEvent(ctx context.Context, in EventInput) (Outcome, error) {
	body, err := BuildEvent(in)
	if err != nil {
		return Outcome{}, &RefusedError{Status: 0, Detail: err.Error()}
	}
	// The read-back matches on the tag that was ACTUALLY pushed, which is
	// `body.Tag` and not `in.Tag`: [BuildEvent] appends the provenance suffix, and a
	// read-back against the unsuffixed tag searches for an event that was never
	// sent. That failure is silent in the worst way — the push is real, the event is
	// in the VMS, and the caller concludes it was not.
	//
	// The two can only disagree because the tag is assembled in two places, which is
	// why they are read off the SAME built body rather than recomputed.
	tag := body.Tag
	// Read back first. A duplicate from a previous attempt that returned 202 and
	// then lost its response must not become a second event.
	if id, err := c.FindByTag(ctx, tag); err != nil {
		return Outcome{}, err
	} else if id != "" {
		return Outcome{State: "push_sent", RemoteID: id, Status: StatusAccepted,
			Detail: "an event with this tag already exists, so it was not pushed again"}, nil
	}

	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return Outcome{}, err
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathEvents + "?triggerRules=true"
	status, respBody, err := c.do(ctx, http.MethodPost, endpoint, tok, payload, "application/json")
	if err != nil {
		return Outcome{}, err
	}
	if status == StatusAccepted {
		id, _ := idFromResponse(respBody)
		// The read-back is what turns "the server said 202" into "the event is
		// there". Without it, push_sent would be a claim about a status code.
		found, rberr := c.FindByTag(ctx, tag)
		if rberr != nil {
			return Outcome{State: "unknown", Status: status, Body: respBody, Detail: "the push returned 202 but the read-back failed, so the event's existence is not established"}, rberr
		}
		if found == "" {
			return Outcome{State: "unknown", Status: status, Body: respBody,
				Detail: "the push returned 202 but a read-back of the tag found nothing, so the event is not treated as sent; the outbox row keeps it for a human"}, nil
		}
		if id == "" {
			id = found
		}
		return Outcome{State: "push_sent", RemoteID: id, Status: status, Body: respBody,
			Detail: "202 and the read-back found the event"}, nil
	}
	switch {
	case status == 403 || status == 400 || status == 409:
		return Outcome{State: "push_refused", Status: status, Body: respBody}, &RefusedError{Status: status, Detail: firstLine(respBody), Body: respBody}
	default:
		return Outcome{State: "retry", Status: status, Body: respBody}, &RetryableError{Status: status, Detail: firstLine(respBody), Body: respBody}
	}
}

// PushPayload is an already-built event body, together with the tag the
// read-back matches on. It exists so an outbox row can carry the exact bytes
// that were meant to be sent: reconstructing an EventInput from a stored payload
// would be a lossy round trip through a Go struct, and the field that quietly
// did not survive it would be the one the report claims was pushed.
type PushPayload struct {
	Body []byte
	Tag  string
}

// SendPayload posts a pre-built event body and reads it back. It is the same
// operation as TriggerEvent with the body supplied rather than assembled.
func (c *Client) SendPayload(ctx context.Context, p PushPayload) (Outcome, error) {
	if len(p.Body) == 0 {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: an empty payload cannot be pushed"}
	}
	if p.Tag == "" {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: a payload with no tag has nothing for the read-back to match on"}
	}
	if id, err := c.FindByTag(ctx, p.Tag); err != nil {
		return Outcome{}, err
	} else if id != "" {
		return Outcome{State: "push_sent", RemoteID: id, Status: StatusAccepted,
			Detail: "an event with this tag already exists, so it was not pushed again"}, nil
	}
	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return Outcome{}, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathEvents + "?triggerRules=true"
	status, respBody, err := c.do(ctx, http.MethodPost, endpoint, tok, p.Body, "application/json")
	if err != nil {
		return Outcome{State: classifyPushStatus(status), Status: status, Body: respBody}, err
	}
	if status == StatusAccepted {
		id, _ := idFromResponse(respBody)
		found, rberr := c.FindByTag(ctx, p.Tag)
		if rberr != nil {
			return Outcome{State: "unknown", Status: status, Body: respBody,
				Detail: "the push returned 202 but the read-back failed, so the event's existence is not established"}, rberr
		}
		if found == "" {
			return Outcome{State: "unknown", Status: status, Body: respBody,
				Detail: "the push returned 202 but a read-back of the tag found nothing, so the event is not treated as sent; the outbox row keeps it for a human"}, nil
		}
		if id == "" {
			id = found
		}
		return Outcome{State: "push_sent", RemoteID: id, Status: status, Body: respBody, Detail: "202 and the read-back found the event"}, nil
	}
	switch {
	case status == 403 || status == 400 || status == 409:
		return Outcome{State: "push_refused", Status: status, Body: respBody}, &RefusedError{Status: status, Detail: firstLine(respBody), Body: respBody}
	default:
		return Outcome{State: "retry", Status: status, Body: respBody}, &RetryableError{Status: status, Detail: firstLine(respBody), Body: respBody}
	}
}

func classifyPushStatus(status int) string {
	switch status {
	case 403, 400, 409:
		return "push_refused"
	case 401:
		return "auth_failed"
	}
	return "retry"
}

// getInto performs a READ-ONLY GET and decodes the response.
//
// It is separate from `do` because `do` requires 200/201/202, and a probe has to be
// able to see a 401, a 404 and a 500 as ANSWERS rather than as errors it must sort
// out afterwards: the whole point is to find out what the server did.
//
// The decode is LENIENT, unlike `do`'s. Everywhere else a response is being
// validated into a value this client will act on; here it is being read to see what
// shape the server actually used, and a server that adds a field has not produced a
// broken response — it has produced a different shape than the document claims, which
// is the thing a probe exists to discover.
func (c *Client) getInto(ctx context.Context, path string, query map[string]string, out any) (int, error) {
	if strings.TrimSpace(c.token) == "" {
		return 0, errors.New("xprotect: no access token; a read-only probe still authenticates, because whether the credentials work is one of the four questions")
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + path
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		endpoint += "?" + q.Encode()
	}
	// `attempt` rather than `do`: `attempt` returns the status and the body whatever
	// the server said, and a probe needs the 401 and the 404 back as answers. The
	// classification below is the probe's own, because `do`'s is about a PUSH —
	// whether to retry a detection — and a probe has no detection to retry.
	status, body, err := c.attempt(ctx, http.MethodGet, endpoint, c.token, nil, "")
	if err != nil {
		return status, err
	}
	if status < 200 || status >= 300 {
		return status, &RefusedError{Status: status, Detail: "the route answered " + firstLine(body), Body: body}
	}
	if out == nil {
		return status, nil
	}
	if derr := json.Unmarshal([]byte(body), out); derr != nil {
		return status, fmt.Errorf("xprotect: GET %s returned %d with a body this build cannot read: %w", path, status, derr)
	}
	return status, nil
}

// FindByTag reads an event back by its tag. This is the idempotency mechanism:
// the API has no idempotency key, so "have I already pushed this?" is answered
// by asking the server.
func (c *Client) FindByTag(ctx context.Context, tag string) (string, error) {
	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("tag", "equals:'"+tag+"'")
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathEvents + "?" + q.Encode()
	status, body, err := c.do(ctx, http.MethodGet, endpoint, tok, nil, "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", &RetryableError{Status: status, Detail: "the tag read-back answered " + firstLine(body), Body: body}
	}
	var out struct {
		Data []struct {
			ID  string `json:"id"`
			Tag string `json:"tag"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "", &RetryableError{Status: status, Detail: "the tag read-back is not usable JSON, so whether the event exists is unknown", Body: body}
	}
	for _, e := range out.Data {
		if e.Tag == tag {
			return e.ID, nil
		}
	}
	return "", nil
}

// BookmarkInput is what a bookmark needs, in the POC's terms.
type BookmarkInput struct {
	Header        string
	DevicePath    string
	TimeTriggered time.Time
	TimeBegin     time.Time
	TimeEnd       time.Time
	Description   string
	Reference     string
}

// BuildBookmark turns a bookmark into the documented body.
//
// It is a separate function from [Client.PlaceBookmark] rather than being folded
// into it, because the outbox has to carry the exact bytes that were meant to be
// sent and a body assembled at the send site is a body nobody reviewed. The
// refusals are the same ones the send path makes, so a bookmark that would be
// refused is refused at build time — while the incident is still being recorded
// and the failure can be attributed to it.
//
// A bookmark carries no video. It is a pointer, and in a real VMS it points at the
// VMS's own recording of that interval, which is exactly its value.
func BuildBookmark(in BookmarkInput) (BookmarkRequest, error) {
	if in.Header == "" {
		return BookmarkRequest{}, errors.New("xprotect: the bookmark has no header, which the contract marks required")
	}
	if in.DevicePath == "" {
		return BookmarkRequest{}, errors.New("xprotect: the bookmark has no device path, which the contract marks required; " +
			"a bookmark with no device points at nothing, and an operator who follows it finds nothing")
	}
	if in.TimeTriggered.IsZero() {
		return BookmarkRequest{}, errors.New("xprotect: the bookmark has no timeTriggered; a bookmark without a time does not point at anything")
	}
	if in.Reference == "" {
		return BookmarkRequest{}, errors.New("xprotect: the bookmark has no reference; the bookmarks API has no tag, so the " +
			"reference is the only field the read-back can match on and a bookmark without one can be placed twice")
	}
	body := BookmarkRequest{
		Header:        in.Header,
		DevicePath:    DevicePath{Type: "local", ID: in.DevicePath},
		TimeTriggered: in.TimeTriggered.UTC().Format(time.RFC3339Nano),
		Description:   in.Description,
		Reference:     in.Reference,
	}
	if !in.TimeBegin.IsZero() {
		body.TimeBegin = in.TimeBegin.UTC().Format(time.RFC3339Nano)
	}
	if !in.TimeEnd.IsZero() {
		body.TimeEnd = in.TimeEnd.UTC().Format(time.RFC3339Nano)
	}
	return body, nil
}

// PlaceBookmark posts a bookmark.
//
// The bookmark is a pointer, not a copy: the body carries no video. In a real
// VMS it points at the VMS's own recording of that interval, which is exactly
// its value — the operator watches the video there. This build has no VMS and no
// recording, so the pointer is empty by construction, and that is stated in the
// request's description so the pushed record does not read as if it were
// usable on its own. The event's evidence ref is the usable pointer.
func (c *Client) PlaceBookmark(ctx context.Context, in BookmarkInput) (Outcome, error) {
	if in.Header == "" {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: the bookmark has no header, which the contract marks required"}
	}
	if in.DevicePath == "" {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: the bookmark has no device path, which the contract marks required"}
	}
	if in.TimeTriggered.IsZero() {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: the bookmark has no timeTriggered; a bookmark without a time does not point at anything"}
	}
	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return Outcome{}, err
	}
	body := BookmarkRequest{
		Header:        in.Header,
		DevicePath:    DevicePath{Type: "local", ID: in.DevicePath},
		TimeTriggered: in.TimeTriggered.UTC().Format(time.RFC3339Nano),
		Description:   in.Description,
		Reference:     in.Reference,
	}
	if !in.TimeBegin.IsZero() {
		body.TimeBegin = in.TimeBegin.UTC().Format(time.RFC3339Nano)
	}
	if !in.TimeEnd.IsZero() {
		body.TimeEnd = in.TimeEnd.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathBookmarks
	status, respBody, err := c.do(ctx, http.MethodPost, endpoint, tok, payload, "application/json")
	if err != nil {
		return Outcome{}, err
	}
	if status == StatusOKBookmark {
		id, _ := idFromResponse(respBody)
		return Outcome{State: "push_sent", RemoteID: id, Status: status, Body: respBody, Detail: "200"}, nil
	}
	if status == 403 || status == 400 {
		return Outcome{State: "push_refused", Status: status, Body: respBody}, &RefusedError{Status: status, Detail: firstLine(respBody), Body: respBody}
	}
	return Outcome{State: "retry", Status: status, Body: respBody}, &RetryableError{Status: status, Detail: firstLine(respBody), Body: respBody}
}

// SendBookmarkPayload posts a pre-built bookmark body and reads it back.
//
// It is the same operation as [Client.PlaceBookmark] with the body supplied
// rather than assembled, and it exists for the same reason
// [Client.SendPayload] does: an outbox row has to carry the exact bytes that were
// meant to be sent, because a body reconstructed from a Go struct is a body whose
// fields nobody reviewed.
//
// The read-back is not optional. A bookmark answers 200 whether or not it created
// anything useful, so "we placed a bookmark" is a claim about a status code unless
// something goes and looks.
func (c *Client) SendBookmarkPayload(ctx context.Context, p PushPayload) (Outcome, error) {
	if len(p.Body) == 0 {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: an empty bookmark body cannot be pushed"}
	}
	if p.Tag == "" {
		return Outcome{}, &RefusedError{Status: 0, Detail: "xprotect: a bookmark with no reference has nothing for the read-back to match on"}
	}
	// Read back first. A duplicate from a previous attempt that returned 200 and
	// then lost its response must not become a second bookmark.
	if id, err := c.FindBookmarkByRef(ctx, p.Tag); err != nil {
		return Outcome{}, err
	} else if id != "" {
		return Outcome{State: "push_sent", RemoteID: id, Status: StatusOKBookmark,
			Detail: "a bookmark with this reference already exists, so it was not placed again"}, nil
	}
	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return Outcome{}, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathBookmarks
	status, respBody, err := c.do(ctx, http.MethodPost, endpoint, tok, p.Body, "application/json")
	if err != nil {
		return Outcome{State: classifyPushStatus(status), Status: status, Body: respBody}, err
	}
	if status == StatusOKBookmark {
		id, _ := idFromResponse(respBody)
		found, rberr := c.FindBookmarkByRef(ctx, p.Tag)
		if rberr != nil {
			return Outcome{State: "unknown", Status: status, Body: respBody,
				Detail: "the bookmark returned 200 but the read-back failed, so the bookmark's existence is not established"}, rberr
		}
		if found == "" {
			return Outcome{State: "unknown", Status: status, Body: respBody,
				Detail: "the bookmark returned 200 but a read-back of the reference found nothing, so it is not treated as " +
					"placed; the outbox row keeps it for a human"}, nil
		}
		if id == "" {
			id = found
		}
		return Outcome{State: "push_sent", RemoteID: id, Status: status, Body: respBody,
			Detail: "200 and the read-back found the bookmark"}, nil
	}
	switch {
	case status == 403 || status == 400:
		return Outcome{State: "push_refused", Status: status, Body: respBody}, &RefusedError{Status: status, Detail: firstLine(respBody), Body: respBody}
	default:
		return Outcome{State: "retry", Status: status, Body: respBody}, &RetryableError{Status: status, Detail: firstLine(respBody), Body: respBody}
	}
}

// FindBookmarkByRef reads a bookmark back by its reference field.
//
// The bookmarks API has no idempotency key and no tag, so the reference IS the
// key — which is why BookmarkInput.Reference carries the event's tag rather than a
// human label. A bookmark whose reference is prose cannot be found again, and a
// push that cannot be found again is a push that can happen twice.
func (c *Client) FindBookmarkByRef(ctx context.Context, ref string) (string, error) {
	tok, err := c.accessToken(ctx, false)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("reference", "equals:'"+ref+"'")
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + PathBookmarks + "?" + q.Encode()
	status, body, err := c.do(ctx, http.MethodGet, endpoint, tok, nil, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		// The bookmarks API answers 404 for an empty result set on some versions
		// and 200 with an empty array on others. Both mean "not there", and
		// treating the 404 as an error would make every first push look like a
		// failure on one version and not the other.
		return "", nil
	}
	if status != http.StatusOK {
		return "", &RetryableError{Status: status, Detail: "the bookmark read-back answered " + firstLine(body), Body: body}
	}
	var out struct {
		Data []struct {
			ID        string `json:"id"`
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "", &RetryableError{Status: status, Detail: "the bookmark read-back is not usable JSON, so whether the " +
			"bookmark exists is unknown", Body: body}
	}
	for _, b := range out.Data {
		if b.Reference == ref {
			return b.ID, nil
		}
	}
	return "", nil
}

// do performs one request, refreshing the token once on a 401 and retrying.
func (c *Client) do(ctx context.Context, method, endpoint, token string, payload []byte, ctype string) (int, string, error) {
	status, body, err := c.attempt(ctx, method, endpoint, token, payload, ctype)
	if err != nil {
		return status, body, err
	}
	if status != http.StatusUnauthorized {
		return status, body, nil
	}
	// One refresh, one retry. Two 401s in a row is an auth problem, not a stale
	// token, and retrying forever would turn a bad secret into a hang.
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
	fresh, aerr := c.accessToken(ctx, true)
	if aerr != nil {
		return status, body, aerr
	}
	status, body, err = c.attempt(ctx, method, endpoint, fresh, payload, ctype)
	if err != nil {
		return status, body, err
	}
	if status == http.StatusUnauthorized {
		// The refreshed token was refused too. The credential is the problem, and
		// recording this as "retry" would send an operator to look at the network
		// when the answer is in the secret.
		return status, body, &ErrAuth{Status: status, Detail: "the VMS refused the request with 401 both before and after the token was refreshed", Grant: c.Grant()}
	}
	return status, body, nil
}

func (c *Client) attempt(ctx context.Context, method, endpoint, token string, payload []byte, ctype string) (int, string, error) {
	var reader io.Reader
	if payload != nil {
		reader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, "", &RetryableError{Status: 0, Detail: "the request could not be built: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", &RetryableError{Status: 0, Detail: scrub(err.Error(), c), Body: ""}
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if rerr != nil {
		return resp.StatusCode, "", &RetryableError{Status: resp.StatusCode,
			Detail: "the response body did not arrive whole, so whether the request was applied is unknown"}
	}
	return resp.StatusCode, string(body), nil
}

func idFromResponse(body string) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "", err
	}
	return out.Data.ID, nil
}

// scrub removes the secret from an error string before it can reach a log. A
// Go http error does not normally contain the Authorization header, but a
// redirect or a proxy error can echo the request line, and the secret must never
// reach a log line under any circumstances.
func scrub(s string, c *Client) string {
	for _, secret := range []string{c.cfg.Credentials.ClientSecret, c.cfg.Credentials.Password, c.token} {
		if secret != "" && strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
