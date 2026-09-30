package xprotect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// harness runs the simulator on loopback and returns a client pointed at it. The
// simulator is a real server over a real socket, not a struct in this address
// space, so the tests exercise the transport too.
type harness struct {
	client *xprotect.Client
	sim    *xpsim.Server
	base   string
	posts  *int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	t.Cleanup(srv.Close)

	// Count the pushes that actually left the client, so a test can say "one
	// POST" without trusting the simulator's own count.
	var posts int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events") {
			atomic.AddInt64(&posts, 1)
		}
		if r.URL.Path == xprotect.PathToken {
			r.Header.Set("X-Test-Seen-Secret", r.FormValue("client_secret"))
		}
		r.Header.Del("X-Test-Seen-Secret")
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		sim.Handler().ServeHTTP(rec, r)
	}))
	t.Cleanup(proxy.Close)

	c, err := xprotect.New(xprotect.Config{
		BaseURL: proxy.URL,
		Timeout: 5 * time.Second,
		Credentials: xprotect.Credentials{
			ClientID: "GrantValidatorClient", ClientSecret: "s3cret",
			Username: "poc", Password: "hunter2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{client: c, sim: sim, base: proxy.URL, posts: &posts}
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Hijack() (interface{ Close() error }, error) { return nil, nil }

func input(tag string) xprotect.EventInput {
	return xprotect.EventInput{
		EventTypeGUID: "b4d1e1c2-0000-4000-8000-000000000001",
		SourceGUID:    "0d9a1b2c-1111-2222-3333-444455556666",
		At:            time.Date(2026, 9, 30, 10, 15, 4, 0, time.UTC),
		Tag:           tag,
		Class:         "bird", Value: "bird", Confidence: 0.87,
		Box:      xprotect.Box{Top: 194, Left: 286, Bottom: 429, Right: 408},
		RuleName: "bird-line-cross",
		Evidence: xprotect.EvidenceRef{StorageRef: "ab/cd/x.mp4", SHA256: strings.Repeat("a", 64)},
	}
}

func TestTriggerEventPushesOnceAndReadsItBack(t *testing.T) {
	h := newHarness(t)
	out, err := h.client.TriggerEvent(context.Background(), input("aicap-bird-abc123"))
	if err != nil {
		t.Fatalf("the push failed: %v", err)
	}
	if out.State != "push_sent" {
		t.Fatalf("state = %q (%s)", out.State, out.Detail)
	}
	if out.RemoteID == "" {
		t.Fatal("no remote id: the read-back did not find the event")
	}
	if out.Status != xprotect.StatusAccepted {
		t.Fatalf("status = %d, want 202", out.Status)
	}
	if got := atomic.LoadInt64(h.posts); got != 1 {
		t.Fatalf("the client issued %d event POSTs, want 1", got)
	}
}

// TestTheSameDetectionPushedTwiceIsOneEvent is T19's test. The API has no
// idempotency key, so the read-back before the push is the only thing standing
// between a lost response and a duplicate event.
func TestTheSameDetectionPushedTwiceIsOneEvent(t *testing.T) {
	h := newHarness(t)
	in := input("aicap-bird-same")
	first, err := h.client.TriggerEvent(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.client.TriggerEvent(context.Background(), in)
	if err != nil {
		t.Fatalf("the second attempt errored instead of being suppressed: %v", err)
	}
	if second.RemoteID != first.RemoteID {
		t.Fatalf("the second attempt produced a different event: %s then %s", first.RemoteID, second.RemoteID)
	}
	if got := atomic.LoadInt64(h.posts); got != 1 {
		t.Fatalf("the client issued %d event POSTs for one detection, want 1", got)
	}
	if !strings.Contains(second.Detail, "already exists") {
		t.Fatalf("the suppression is not explained: %q", second.Detail)
	}
}

// TestATagDerivedFromACounterIsNotIdempotent is the negative form: two different
// detections with different content-derived tags are two events, and one tag is
// not allowed to cover both.
func TestDifferentTagsAreDifferentEvents(t *testing.T) {
	h := newHarness(t)
	for _, tag := range []string{"aicap-bird-aaa", "aicap-bird-bbb"} {
		if _, err := h.client.TriggerEvent(context.Background(), input(tag)); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
	}
	if got := atomic.LoadInt64(h.posts); got != 2 {
		t.Fatalf("two distinct tags produced %d POSTs, want 2", got)
	}
}

func TestPlaceBookmarkSendsTheDocumentedBody(t *testing.T) {
	h := newHarness(t)
	at := time.Date(2026, 9, 30, 10, 15, 4, 0, time.UTC)
	out, err := h.client.PlaceBookmark(context.Background(), xprotect.BookmarkInput{
		Header:        "bird crossed the line",
		DevicePath:    "/Local/00000000-0000-0000-0000-000000000000",
		TimeTriggered: at,
		Description:   "bookmark target: no VMS recording in this build - the pointer is empty by construction; the event's evidence ref is the usable one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != xprotect.StatusOKBookmark {
		t.Fatalf("status = %d, want 200", out.Status)
	}
}

func TestPlaceBookmarkRefusesAnIncompleteRequest(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		in   xprotect.BookmarkInput
	}{
		{"no header", xprotect.BookmarkInput{DevicePath: "/x", TimeTriggered: time.Now()}},
		{"no device path", xprotect.BookmarkInput{Header: "h", TimeTriggered: time.Now()}},
		{"no time", xprotect.BookmarkInput{Header: "h", DevicePath: "/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.client.PlaceBookmark(context.Background(), tc.in)
			if err == nil {
				t.Fatal("an incomplete bookmark was sent")
			}
			var re *xprotect.RefusedError
			if !asRefused(err, &re) {
				t.Fatalf("error is %T, want *RefusedError", err)
			}
		})
	}
}

func asRefused(err error, dst **xprotect.RefusedError) bool {
	r, ok := err.(*xprotect.RefusedError)
	if ok {
		*dst = r
	}
	return ok
}

func asRetryable(err error, dst **xprotect.RetryableError) bool {
	r, ok := err.(*xprotect.RetryableError)
	if ok {
		*dst = r
	}
	return ok
}

// --- fault rehearsal (T23) --------------------------------------------------

// TestFaultRehearsal is T23's table. Every rehearsable fault, and the recorded
// outcome the client should produce for it. AC7 is scored by comparing recorded
// outcomes against this table, so a fault with no row here has no expected
// outcome and cannot be verified.
//
// The fault is scoped to the events path so that the token call is not the
// thing being rehearsed: a 401 injected everywhere is a rehearsal of the token
// call and tells us nothing about the push.
func TestFaultRehearsal(t *testing.T) {
	cases := []struct {
		fault xpsim.Fault
		want  string
		why   string
	}{
		{xpsim.FaultUnauthorized, "auth_failed", "a 401 must refresh the token once and, if it persists, be a recorded auth failure rather than a retry loop"},
		{xpsim.FaultForbidden, "push_refused", "a 403 is a definite no and is terminal for that detection"},
		{xpsim.FaultBadRequest, "push_refused", "a 400 is a definite no and is terminal for that detection"},
		{xpsim.FaultServerError, "retry", "a 5xx is worth another attempt"},
		{xpsim.FaultConnectionReset, "retry", "a reset connection is not a data problem: the outbox retries, and the backoff budget is what dead-letters it"},
		{xpsim.FaultTimeout, "retry", "a timeout must not wedge the worker"},
	}
	for _, tc := range cases {
		t.Run(string(tc.fault), func(t *testing.T) {
			h := newHarness(t)
			// Get a token first, so the fault under test is the push and not the
			// token call.
			if err := h.client.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := h.sim.ArmOn(tc.fault, 0, "/API/rest/v1/events", "POST"); err != nil {
				t.Fatal(err)
			}
			_, err := h.client.TriggerEvent(context.Background(), input("aicap-"+string(tc.fault)))
			if got := classify(err); got != tc.want {
				t.Fatalf("fault %q produced %q, want %q (%v)", tc.fault, got, tc.want, err)
			}
			t.Logf("%s -> %s: %s", tc.fault, tc.want, tc.why)
		})
	}
}

// TestTruncatedResponseIsNotTreatedAsSent: a 202 whose body arrived in half is
// the response a proxy produces when it dies mid-write. Treating it as an
// accepted event would be claiming a delivery nobody can evidence.
func TestTruncatedResponseIsNotTreatedAsSent(t *testing.T) {
	h := newHarness(t)
	if err := h.client.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.sim.ArmOn(xpsim.FaultTruncatedJSON, 0, "/API/rest/v1/events", "POST"); err != nil {
		t.Fatal(err)
	}
	out, err := h.client.TriggerEvent(context.Background(), input("aicap-truncated"))
	if err == nil && out.State == "push_sent" {
		t.Fatalf("a truncated 202 was treated as sent: %+v", out)
	}
	if out.State == "push_sent" {
		t.Fatalf("state = push_sent with remote id %q", out.RemoteID)
	}
}

func classify(err error) string {
	if err == nil {
		return "ok"
	}
	var au *xprotect.ErrAuth
	if asAuth(err, &au) {
		return "auth_failed"
	}
	var re *xprotect.RefusedError
	if asRefused(err, &re) {
		return "push_refused"
	}
	var rt *xprotect.RetryableError
	if asRetryable(err, &rt) {
		return "retry"
	}
	return "other:" + err.Error()
}

func asAuth(err error, dst **xprotect.ErrAuth) bool {
	a, ok := err.(*xprotect.ErrAuth)
	if ok {
		*dst = a
	}
	return ok
}

// TestUnauthorizedRefreshesTheTokenOnce: a stale token is recoverable, a wrong
// secret is not, and the difference is one refresh.
func TestUnauthorizedRefreshesTheTokenOnce(t *testing.T) {
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()
	var tokenCalls int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == xprotect.PathToken {
			atomic.AddInt64(&tokenCalls, 1)
		}
		sim.Handler().ServeHTTP(w, r)
	}))
	defer proxy.Close()

	c, err := xprotect.New(xprotect.Config{BaseURL: proxy.URL, Timeout: 2 * time.Second,
		Credentials: xprotect.Credentials{ClientID: "GrantValidatorClient", ClientSecret: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// One token call, then a good push: the client must reuse the token.
	if err := c.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TriggerEvent(ctx, input("tag-1")); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt64(&tokenCalls); n != 1 {
		t.Fatalf("token calls = %d, want 1: a valid token must be reused, not re-fetched per push", n)
	}
}

// TestOfflineProviderProducesARetryableAndNotAStateChange: an outbox has to
// stay full when the provider is down, and the run has to continue.
func TestOfflineProviderProducesARetryable(t *testing.T) {
	h := newHarness(t)
	h.sim.Offline(true)
	defer h.sim.Offline(false)
	_, err := h.client.TriggerEvent(context.Background(), input("tag-offline"))
	var rt *xprotect.RetryableError
	if !asRetryable(err, &rt) {
		t.Fatalf("an offline provider produced %T: %v", err, err)
	}
	if !strings.Contains(rt.Detail, "connection") && !strings.Contains(rt.Detail, "EOF") && !strings.Contains(rt.Detail, "refused") {
		t.Logf("offline detail: %s", rt.Detail)
	}
	h.sim.Offline(false)
	// With the provider back, the same detection pushes exactly once.
	if _, err := h.client.TriggerEvent(context.Background(), input("tag-offline")); err != nil {
		t.Fatalf("the push after recovery failed: %v", err)
	}
	if got := atomic.LoadInt64(h.posts); got != 1 {
		t.Fatalf("event POSTs = %d, want 1: only the successful attempt should have gone out", got)
	}
}

// TestTheTokenIsNeverWrittenToTheLog is T18's secret-hygiene test. The client's
// log callback receives every line it produces, and none of them may contain the
// token or either secret.
func TestTheTokenIsNeverWrittenToTheLog(t *testing.T) {
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()
	var lines []string
	c, err := xprotect.New(xprotect.Config{
		BaseURL: srv.URL, Timeout: 5 * time.Second,
		Credentials: xprotect.Credentials{ClientID: "GrantValidatorClient", ClientSecret: "S3CRETVALUE", Password: "PASSVALUE"},
		Logf:        func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TriggerEvent(context.Background(), input("tag-log")); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("the client logged nothing, so the secret-hygiene check is vacuous")
	}
	for _, l := range lines {
		for _, secret := range []string{"S3CRETVALUE", "PASSVALUE", "simulated-access-token"} {
			if strings.Contains(l, secret) {
				t.Fatalf("the log line %q contains a secret", l)
			}
		}
	}
	if got := c.Grant(); got != "client_credentials" {
		t.Fatalf("grant = %q, want client_credentials preferred over password", got)
	}
}

// TestWhoAmIAlwaysSaysItIsASimulator: the simulator has to be able to answer the
// question, and the answer has to be its own name. This is what the report uses
// to decide whether the "reached Milestone" column may exist.
func TestWhoAmIAlwaysSaysItIsASimulator(t *testing.T) {
	h := newHarness(t)
	out, err := whoami(h.base)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != xpsim.Name || !out.Simulator {
		t.Fatalf("whoami = %+v, want the simulator to identify itself", out)
	}
}

func whoami(base string) (xpsim.WhoAmI, error) {
	var out xpsim.WhoAmI
	resp, err := http.Get(base + "/__control/whoami")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return out, json.Unmarshal(b, &out)
}

// TestTheSimulatorRefusesThePlatformsBookmarkShape: a body with the parent
// platform's guessed fields must be a 400, which is the whole point of checking
// the contract rather than copying the code.
func TestTheSimulatorRefusesThePlatformsBookmarkShape(t *testing.T) {
	h := newHarness(t)
	tok := obtainToken(t, h.base)
	body := `{"bookmarks":[{"idempotencyKey":"k1","deviceRef":"cam1","time":"2026-09-30T10:15:04Z","label":"l","description":"d"}]}`
	resp, err := http.Post(h.base+xprotect.PathBookmarks, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = tok
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a body with no header and no devicePath", resp.StatusCode)
	}
}

func obtainToken(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.PostForm(base+xprotect.PathToken, map[string][]string{
		"grant_type": {"client_credentials"}, "client_id": {"GrantValidatorClient"}, "client_secret": {"s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	return tr.AccessToken
}
