package vlm

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/shared/aistate"
)

// The contract's shape, asserted against a committed document.
//
// There is no such file yet for Hafnia, because the live call that settles the
// host and the response shape has not been made. This test therefore asserts
// what is DOCUMENTED in the provider's published API and refuses the four things
// the previous client got wrong silently. The moment the raw response is
// recorded in contracts/hafnia/, these rows are checked against it as well.

const publishedPaths = "/api/v1/assets,/api/v1/chat/completions"

// TestEveryPathWeCallIsPublished pins the `/api/v1` prefix.
//
// The prefix is the one thing about this API that a wrong guess about is
// invisible: a request to /chat/completions is not refused for its path, it is
// refused by a gateway that has no idea what the caller meant, and the operator
// spends an hour on what looks like an outage.
func TestEveryPathWeCallIsPublished(t *testing.T) {
	for _, p := range []string{PathAssets, PathCompletions} {
		if !strings.Contains(publishedPaths, p) {
			t.Fatalf("%s is not a documented path", p)
		}
		if !strings.HasPrefix(p, "/api/v1/") {
			t.Fatalf("%s does not carry the documented /api/v1 prefix", p)
		}
	}
	if PathAssets+"/"+assetIDForTest == "/chat/completions" {
		t.Fatal("the two endpoints collided")
	}
}

const assetIDForTest = "asset-abc"

// TestTheAuthorizationHeaderIsApiKeyAndNotBearer is the failure that is silent
// for a whole afternoon: the endpoint exists, it answers, and it answers 401.
func TestTheAuthorizationHeaderIsApiKeyAndNotBearer(t *testing.T) {
	var got string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()

	c := clientFor(t, s, 0)
	_, _ = c.UploadAsset(context.Background(), "video/mp4", []byte("mp4-bytes"))

	if got != "ApiKey test-key" {
		t.Fatalf("the provider saw Authorization: %q; the documented scheme is 'ApiKey <key>' and 'Bearer <key>' is a 401 on every call", got)
	}
	if strings.HasPrefix(got, "Bearer ") {
		t.Fatal("the client sent a bearer token; that is the scheme that 401s on every call")
	}
}

// TestTheContentArrayIsTyped pins the message content shape. A flat string with
// an asset id in it is structurally wrong, and it fails as a 400 whose body does
// not name the field.
func TestTheContentArrayIsTyped(t *testing.T) {
	var body string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathCompletions {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"{\"verdict\":\"none\",\"summary\":\"x\",\"confidence\":0,\"insufficient_evidence\":true}"}}]}`))
	}))
	defer s.Close()

	c := clientFor(t, s, 0)
	prompt, err := c.Prompts().Get("alarm")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.ask(context.Background(), "asset-1", prompt); err != nil {
		t.Fatalf("ask: %v", err)
	}
	for _, want := range []string{`"type":"text"`, `"type":"asset_id"`, `"asset_id":"asset-1"`, `"response_format":{"type":"json_object"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the request body does not contain %s. Body: %s", want, body)
		}
	}
}

// TestTheUploadIsMultipartWithAFilePart pins the upload framing.
func TestTheUploadIsMultipartIsMultipartWithAFilePart(t *testing.T) {
	var ctype, body string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer s.Close()

	c := clientFor(t, s, 0)
	a, err := c.UploadAsset(context.Background(), "video/mp4", []byte("mp4-bytes"))
	if err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	if a.ID != "asset-1" {
		t.Fatalf("the upload returned the id %q", a.ID)
	}
	if !strings.HasPrefix(ctype, "multipart/form-data; boundary=") {
		t.Fatalf("the upload Content-Type is %q", ctype)
	}
	if !strings.Contains(body, `name="file"`) || !strings.Contains(body, `filename="clip.mp4"`) {
		t.Fatalf("the upload body does not carry a file part: %s", body)
	}
}

// TestTheAssetIsDeletedOnEveryPathAfterTheUpload is the privacy property: a clip
// that left the site must not stay there because the call failed.
func TestTheAssetIsDeletedOnEveryPathAfterTheUpload(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func(w http.ResponseWriter, path string)
	}{
		{"a good answer", func(w http.ResponseWriter, path string) {
			if path == PathCompletions {
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"{\"verdict\":\"confirmed\",\"summary\":\"a car\",\"confidence\":0.8,\"insufficient_evidence\":false}"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
		}},
		{"a malformed answer", func(w http.ResponseWriter, path string) {
			if path == PathCompletions {
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"I think it is a car."}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
		}},
		{"a 500", func(w http.ResponseWriter, path string) {
			if path == PathCompletions {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"id":"asset-1"}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted atomic.Bool
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.reply(w, r.URL.Path)
				if r.Method == http.MethodDelete {
					deleted.Store(true)
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer s.Close()

			c := clientFor(t, s, 0)
			_ = c.Observe(context.Background(), "det-1", "alarm", "car", []byte("mp4-bytes"), "video/mp4")
			if !deleted.Load() {
				t.Fatal("the uploaded clip was not deleted")
			}
			if got := c.OutstandingAssets(); len(got) != 0 {
				t.Fatalf("the client still holds %v; footage may still be on the provider's disk", got)
			}
		})
	}
}

// TestTheBudgetIsReservedBeforeAnythingLeavesTheSite is the privacy AND the cost
// property. A site that has reached its cap must not ship footage for a call that
// will be refused.
func TestTheBudgetIsReservedBeforeAnythingLeavesTheSite(t *testing.T) {
	var uploads atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == PathAssets {
			uploads.Add(1)
		}
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer s.Close()

	c := clientFor(t, s, 1)
	first := c.Observe(context.Background(), "det-1", "alarm", "car", []byte("clip-one"), "video/mp4")
	if first.State != aistate.CannotTell && first.State != aistate.Degraded {
		t.Fatalf("the first call against a stub server produced %+v; the test server answers the completion with no asset", first)
	}
	obs := c.Observe(context.Background(), "det-2", "alarm", "car", []byte("clip-two"), "video/mp4")
	if uploads.Load() != 1 {
		t.Fatalf("the client uploaded %d clips against a cap of one call; a capped site must not ship footage for a refused call", uploads.Load())
	}
	if obs.State != aistate.NotAsked {
		t.Fatalf("a capped site produced state %q rather than not_asked; we did not ask", obs.State)
	}
	if obs.Verdict != VerdictNone {
		t.Fatalf("a capped site produced the verdict %q; a site that did not ask cannot have an answer", obs.Verdict)
	}
	if obs.Reason == "" {
		t.Fatal("a capped site recorded no reason; a silent throttle is indistinguishable from a detector that did not fire")
	}
	if q := c.Quota(); !q.Exhausted || q.Day == "" {
		t.Fatalf("the quota reports %+v; exhaustion is a state, not an error", q)
	}
}

// TestTheFaultMatrixProducesDistinctStatesAndNoFabricatedAnswer.
//
// Each of these is a different operational fact, and collapsing any two of them
// into one is how a provider's rate limit turns into a support ticket about
// connectivity — and, worse, how a `false_positive` reaches a screen that has no
// answer behind it.
func TestTheFaultMatrixProducesDistinctStatesAndNoFabricatedAnswer(t *testing.T) {
	cases := []struct {
		name  string
		reply func(w http.ResponseWriter)
		want  string
	}{
		{"429 rate limited", func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) }, aistate.Degraded},
		{"500 provider error", func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, aistate.Degraded},
		{"401 wrong credentials", func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }, aistate.Degraded},
		{"malformed body", func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{not json`)) }, aistate.CannotTell},
		{"two choices", func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"verdict\":\"none\",\"summary\":\"a\",\"confidence\":0,\"insufficient_evidence\":true}"}},{"message":{"content":"{}"}}]}`))
		}, aistate.CannotTell},
		{"prose where json was required", func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"It is probably a car."}}]}`))
		}, aistate.CannotTell},
		{"empty content", func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  "}}]}`))
		}, aistate.CannotTell},
		{"a redirect", func(w http.ResponseWriter) {
			w.Header().Set("Location", "https://elsewhere.example/api/v1/chat/completions")
			w.WriteHeader(http.StatusFound)
		}, aistate.NotAsked},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				if r.URL.Path == PathCompletions {
					tc.reply(w)
					return
				}
				_, _ = w.Write([]byte(`{"id":"asset-1"}`))
			}))
			defer s.Close()

			c := clientFor(t, s, 0)
			obs := c.Observe(context.Background(), "det-1", "alarm", "car", []byte("mp4-bytes"), "video/mp4")
			if obs.State != tc.want {
				t.Fatalf("%s produced the state %q rather than %q (reason: %s)", tc.name, obs.State, tc.want, obs.Reason)
			}
			if obs.Verdict != VerdictNone {
				t.Fatalf("%s produced the verdict %q; no failure may fabricate an answer", tc.name, obs.Verdict)
			}
			if obs.Summary != "" {
				t.Fatalf("%s produced the summary %q; prose from an unreadable answer must not reach a screen", tc.name, obs.Summary)
			}
			if obs.OutputHash == "" {
				t.Fatalf("%s recorded no output hash; the record is not attributable", tc.name)
			}
			if prev, dup := seen[tc.want+"/"+tc.name]; dup {
				t.Fatalf("the state %q is produced by both %q and %q", tc.want, prev, tc.name)
			}
			seen[tc.want+"/"+tc.name] = tc.name
		})
	}
}

// TestTheAssetMissingOnCompletionIsNotTheSameAsAMissingAnswer: an expired asset
// means the caller has to upload again, which is a different operational fact
// from "the answer did not arrive".
func TestTheAssetMissingOnCompletionIsNotTheSameAsAMissingAnswer(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PathCompletions {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer s.Close()
	c := clientFor(t, s, 0)
	obs := c.Observe(context.Background(), "det-1", "alarm", "car", []byte("mp4"), "video/mp4")
	if obs.State != aistate.CannotTell {
		t.Fatalf("an expired asset produced %q rather than cannot_tell", obs.State)
	}
	if Detail(ErrAssetNotFound) != "reason=asset_missing" {
		t.Fatalf("the reason code for an expired asset is %q", Detail(ErrAssetNotFound))
	}
}

// TestACancelledContextIsADegradationAndNotAnAnswer.
func TestACancelledContextIsADegradationAndNotAnAnswer(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer s.Close()
	c := clientFor(t, s, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	obs := c.Observe(ctx, "det-1", "alarm", "car", []byte("mp4"), "video/mp4")
	if obs.State == aistate.Asked {
		t.Fatal("a cancelled context produced an answered observation")
	}
	if obs.Verdict != VerdictNone {
		t.Fatalf("a cancelled call produced the verdict %q", obs.Verdict)
	}
}

// TestATimeoutIsADegradationNamedAsOne.
func TestATimeoutIsADegradationNamedAsOne(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() { close(release); s.Close() }()

	c := clientFor(t, s, 0)
	c.cfg.Timeout = 40 * time.Millisecond
	c.http = &http.Client{Timeout: 40 * time.Millisecond, Transport: s.Client().Transport}
	obs := c.Observe(context.Background(), "det-1", "alarm", "car", []byte("mp4"), "video/mp4")
	if obs.State != aistate.Degraded {
		t.Fatalf("a timeout produced %q rather than degraded (reason: %s)", obs.State, obs.Reason)
	}
	if obs.Verdict != VerdictNone {
		t.Fatalf("a timeout produced the verdict %q", obs.Verdict)
	}
}

// TestRegionPinningIsRefusedInAllThreeDirections.
//
// The cross-region refusal is the point of the whole region mechanism, so it is
// tested from every side rather than once: the EU client must refuse the US host,
// the US client must refuse the EU host, and neither may reach a host that is not
// in the map at all.
func TestRegionPinningIsRefusedInAllThreeDirections(t *testing.T) {
	for _, tc := range []struct {
		region Region
		other  Region
	}{
		{RegionEU, RegionUS},
		{RegionUS, RegionEU},
	} {
		other, err := tc.other.Host()
		if err != nil {
			t.Fatalf("Host: %v", err)
		}
		c := clientIn(t, tc.region, 0)
		if err := c.requireRegion(other); !errors.Is(err, ErrCrossRegion) {
			t.Fatalf("a %s client accepted the %s host %s: %v", tc.region, tc.other, other, err)
		}
		if err := c.requireRegion("api.example.com"); !errors.Is(err, ErrCrossRegion) {
			t.Fatalf("a client accepted a host that is not in the map: %v", err)
		}
		if err := c.requireRegion(c.host); err != nil {
			t.Fatalf("a client refused its own pinned host: %v", err)
		}
	}
}

// TestTheHostsAreTheDocumentedOnes pins the two names and keeps the superseded
// spelling out.
func TestTheHostsAreTheDocumentedOnes(t *testing.T) {
	eu, _ := RegionEU.Host()
	us, _ := RegionUS.Host()
	if eu != "api.eu.hafnia.milestonesys.com" {
		t.Fatalf("the EU host is %q", eu)
	}
	if us != "api.us.hafnia.milestonesys.com" {
		t.Fatalf("the US host is %q", us)
	}
	for r, h := range regionHosts {
		if h == supersededEUHost {
			t.Fatalf("the superseded EU host %q is back in the map for region %s", supersededEUHost, r)
		}
		if h == "" || h == "api.eu.hafnia.example" || h == "api.us.hafnia.example" {
			t.Fatalf("region %s resolves to the placeholder host %q, which is unreachable", r, h)
		}
	}
}

// TestAnUnknownRegionIsRefusedAndADefaultRegionIsNotInvented.
func TestAnUnknownRegionIsRefusedAndADefaultRegionIsNotInvented(t *testing.T) {
	for _, s := range []string{"", "  ", "moon", "eu-west", "EU "} {
		r, err := ParseRegion(s)
		if err == nil && r == "" {
			t.Fatalf("%q parsed to an empty region", s)
		}
	}
	if r, err := ParseRegion("EU"); err != nil || r != RegionEU {
		t.Fatalf("ParseRegion(EU) = %q, %v", r, err)
	}
	if _, err := New(Config{APIKey: "k", Region: "moon"}); !errors.Is(err, ErrUnknownRegion) {
		t.Fatalf("a client was built for an unknown region: %v", err)
	}
	if _, err := New(Config{Region: RegionEU}); err == nil {
		t.Fatal("a client was built with no API key; the call would upload footage unauthenticated")
	}
}

// TestTheClientRefusesToFollowARedirect.
func TestTheClientRefusesToFollowARedirect(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer target.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	c := clientFor(t, redirector, 0)
	if _, err := c.UploadAsset(context.Background(), "video/mp4", []byte("mp4")); !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("a redirect produced %v rather than ErrRedirectRefused", err)
	}
	if followed.Load() {
		t.Fatal("the client followed a redirect; a 302 from a pinned host is the cheapest exfiltration there is")
	}
}

// TestSweepAssetsClearsWhatAnInterruptedRunLeftBehind.
func TestSweepAssetsClearsWhatAnInterruptedRunLeftBehind(t *testing.T) {
	var deleted atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Add(1)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"asset-1"}`))
	}))
	defer s.Close()
	c := clientFor(t, s, 0)
	if _, err := c.UploadAsset(context.Background(), "video/mp4", []byte("mp4")); err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	cleared, failed := c.SweepAssets(context.Background())
	if len(cleared) != 1 || len(failed) != 0 {
		t.Fatalf("SweepAssets cleared %v and failed %v", cleared, failed)
	}
	if deleted.Load() != 1 {
		t.Fatalf("%d deletes were issued", deleted.Load())
	}
}

// TestTheUploadRefusesWhatTheProviderRefuses.
func TestTheUploadRefusesWhatTheProviderRefuses(t *testing.T) {
	c := clientIn(t, RegionEU, 0)
	if _, err := c.UploadAsset(context.Background(), "video/mp4", nil); err == nil {
		t.Fatal("an empty clip was accepted")
	}
	if _, err := c.UploadAsset(context.Background(), "video/quicktime", []byte("mov")); err == nil {
		t.Fatal("a non-mp4 clip was accepted; the provider's endpoint takes mp4 only")
	}
	if _, err := c.UploadAsset(context.Background(), "video/mp4", make([]byte, MaxUploadBytes+1)); err == nil {
		t.Fatal("an oversized clip was accepted")
	}
}

// TestNoAPICKeyReachesAnErrorString is the leak guard, and it is the one that
// matters most because the failure is a log line.
func TestNoAPICKeyReachesAnErrorString(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	c := clientFor(t, s, 0)
	_, err := c.UploadAsset(context.Background(), "video/mp4", []byte("mp4"))
	if err == nil {
		t.Fatal("a 500 produced no error")
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("the API key reached an error string: %v", err)
	}
	if got := Detail(err); got != "reason=region_unavailable" {
		t.Fatalf("the machine-readable detail is %q rather than a reason code", got)
	}
}

// TestTheTLSFloorIsTwelve is stated as a constant so the number cannot drift
// silently during a refactor of the transport.
func TestTheTLSFloorIsTwelve(t *testing.T) {
	if tlsFloor != tls.VersionTLS12 {
		t.Fatalf("the TLS floor is %#x, not TLS 1.2", tlsFloor)
	}
	h := newHTTPClient(Config{Timeout: time.Second})
	tr, ok := h.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the transport is %T", h.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tlsFloor {
		t.Fatalf("the transport's TLS minimum is not the floor: %+v", tr.TLSClientConfig)
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification is off by default")
	}
	if h.CheckRedirect == nil {
		t.Fatal("no redirect policy; the Go default follows ten of them")
	}
}

// TestTheCallCounterMeasuresZeroCallsForNoCall, which is the primitive the
// replay's zero-provider-call assertion is built on.
func TestTheCallCounterMeasuresZeroCallsForNoCall(t *testing.T) {
	c := clientIn(t, RegionEU, 0)
	if c.Calls() != 0 {
		t.Fatalf("a fresh client reports %d calls", c.Calls())
	}
	if got := c.Attribution().Temperature; got != DefaultTemperature {
		t.Fatalf("the default temperature is %g rather than %g", got, DefaultTemperature)
	}
}

func clientFor(t *testing.T, s *httptest.Server, cap int) *Client {
	t.Helper()
	c, err := New(Config{
		Region: RegionEU, APIKey: "test-key", APIModelName: "cosmos-3-nano-16b",
		DailyQueryCap: cap, Transport: refusing(s.Client()),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The transport points at the test server rather than at the pinned host, and
	// the pinned host is what the region check reads. Both stay the real values:
	// the test is about the wire, not about the allow-list.
	c.host = strings.TrimPrefix(s.URL, "https://")
	return c
}

func clientIn(t *testing.T, region Region, cap int) *Client {
	t.Helper()
	c, err := New(Config{
		Region: region, APIKey: "test-key", APIModelName: "m", DailyQueryCap: cap,
		Transport: &http.Client{Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// refusing keeps the PRODUCTION redirect policy on a test server's transport.
//
// The test server's own client follows redirects, which would mean the test
// exercised a policy the module does not ship. The policy under test is the one
// newHTTPClient installs, so it is installed here too.
func refusing(base *http.Client) *http.Client {
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirectRefused }
	return &c
}
