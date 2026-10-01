package vlm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// probeTransport answers with a fixed status and body, so a probe can be tested
// without a provider and without a clip.
type probeTransport struct {
	status int
	body   string
	hits   int
	// location is the Location header on a redirect response.
	location string
}

func (p *probeTransport) Do(*http.Request) (*http.Response, error) {
	p.hits++
	h := http.Header{}
	if p.location != "" {
		h.Set("Location", p.location)
	}
	return &http.Response{
		StatusCode: p.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(p.body)),
	}, nil
}

func probeClient(t *testing.T, tr Doer) *Client {
	t.Helper()
	c, err := New(Config{
		Region: RegionEU, APIKey: "probe-key-not-a-secret", APIModelName: "cosmos-3-nano-16b",
		Timeout: 2 * time.Second, Transport: tr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProbeSendsNoFootageAndUsesTheTypedContentArray(t *testing.T) {
	tr := &probeTransport{status: 200, body: `{"model":"cosmos-3-nano-16b","choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`}
	c := probeClient(t, tr)
	rep, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v (body %s)", err, rep.Body)
	}
	if rep.Host != "api.eu.hafnia.milestonesys.com" {
		t.Fatalf("host = %q; the probe must report the PINNED host so the contested EU name is settled by the wire", rep.Host)
	}
	if rep.Status != 200 || rep.ModelIdentity != "cosmos-3-nano-16b" {
		t.Fatalf("status=%d model=%q", rep.Status, rep.ModelIdentity)
	}
	// No asset was uploaded and no clip left the site: `hits` counts one request
	// and the probe made no upload.
	if tr.hits != 1 {
		t.Fatalf("the probe made %d requests; it is a probe, not a pipeline, and must be exactly one call", tr.hits)
	}
	if rep.Body != tr.body {
		t.Fatalf("the raw body was altered:\n got %q\nwant %q", rep.Body, tr.body)
	}
}

// TestProbeRefusesToFollowARedirect: a probe is run by hand against a host an
// operator typed, so a 302 to somewhere else is the open-redirect case and must be
// refused by name rather than followed.
func TestProbeRefusesToFollowARedirect(t *testing.T) {
	tr := &probeTransport{status: http.StatusFound, body: `{"error":"moved"}`, location: "https://elsewhere.example.com/api/v1/chat/completions"}
	c := probeClient(t, tr)
	rep, err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("a redirect was accepted; a probe must not follow one")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("the refusal does not name the hazard: %v", err)
	}
	if rep.Status != http.StatusFound {
		t.Fatalf("status = %d; the report must still record what answered", rep.Status)
	}
}

// TestProbeRefusesWithoutAnAPIKey: `New` already refuses one, and that refusal is
// the point. A probe that echoed the configuration back would look like a probe.
func TestProbeRefusesWithoutAnAPIKey(t *testing.T) {
	if _, err := New(Config{Region: RegionEU, APIModelName: "m"}); err == nil {
		t.Fatal("a client with no API key was built; the call would upload footage unauthenticated")
	}
}

// TestProbeNamesTheErrorRatherThanClaimingAnAnswer: a 401 is a credentials
// problem and must not be recorded as anything else.
func TestProbeNamesTheErrorRatherThanClaimingAnAnswer(t *testing.T) {
	tr := &probeTransport{status: http.StatusUnauthorized, body: `{"detail":"bad key"}`}
	c := probeClient(t, tr)
	rep, err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("a 401 was accepted")
	}
	if !strings.Contains(err.Error(), "ApiKey") {
		t.Fatalf("the refusal does not name the documented auth scheme: %v", err)
	}
	if rep.Err == nil {
		t.Fatal("the report carries no error; a caller writing the raw body beside the reason would have nothing to write")
	}
}

// TestTheProbeSpendsNoQuota: a probe must not be able to consume a site's daily
// inference budget.
func TestTheProbeSpendsNoQuota(t *testing.T) {
	tr := &probeTransport{status: 200, body: `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`}
	c, err := New(Config{Region: RegionEU, APIKey: "k", APIModelName: "m", DailyQueryCap: 1, Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Probe(context.Background()); err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
	}
	if q := c.Quota(); q.Used != 0 {
		t.Fatalf("used = %d; a probe must not spend a site's inference budget, and %d of it was spent", q.Used, q.Used)
	}
	if q := c.Quota(); q.Limit != 1 || q.Remaining != 1 {
		t.Fatalf("quota = %+v; the cap itself must be untouched by a probe", q)
	}
}
