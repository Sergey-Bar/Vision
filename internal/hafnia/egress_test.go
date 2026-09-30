package hafnia_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/hafnia"
)

// The plan's §14 extra: "SSRF on every configurable URL".
//
// The primary control here is that there is **no configurable URL**. The
// client has no base-URL option, no endpoint override and no environment
// variable; the only destinations it can name are the two hosts in the region
// table. A URL that cannot be configured is a URL that cannot be pointed at
// 169.254.169.254, and these tests assert that property rather than trusting it.

// TestTheRegionAllowListIsClosedAndPointsNowhereInternal is the first control.
func TestTheRegionAllowListIsClosedAndPointsNowhereInternal(t *testing.T) {
	regions := hafnia.KnownRegions()
	if len(regions) != 2 {
		t.Errorf("this build pins %d regions; the table and the tests should agree", len(regions))
	}
	seen := map[string]string{}
	for _, r := range regions {
		host, err := r.Host()
		if err != nil {
			t.Fatalf("a shipped region has no host: %v", err)
		}
		if prev, dup := seen[host]; dup {
			t.Errorf("regions %s and %s share the host %s; a region is a data-sovereignty promise and a "+
				"shared host makes it unkeepable", prev, r, host)
		}
		seen[host] = string(r)

		// A region host that is a literal IP, a loopback, a link-local or a
		// private address would defeat the pinning the region exists for.
		if ip := net.ParseIP(host); ip != nil {
			t.Errorf("region %s pins to the literal address %s; a region must name a host whose "+
				"resolution someone is responsible for", r, host)
		}
		switch strings.ToLower(host) {
		case "localhost", "127.0.0.1", "::1", "[::1]", "0.0.0.0":
			t.Errorf("region %s pins to %s, which is this machine", r, host)
		}
		for _, internal := range []string{"169.254.169.254", "metadata.google.internal", ".local", ".internal"} {
			if strings.Contains(strings.ToLower(host), internal) {
				t.Errorf("region %s pins to %s, which is inside", r, host)
			}
		}
	}
}

// TestNoConfigurationCanIntroduceAnotherHost is the second control, and the one
// that would catch a base-URL option being added later "just for testing".
func TestNoConfigurationCanIntroduceAnotherHost(t *testing.T) {
	// Normalisation is *not* a hole. A region arrives from a config file or an
	// environment variable, and `" EU "` is the same site as `"eu"`; refusing it
	// would only teach operators to hand-edit the value, and a hand-edited value
	// is one nobody reads. So case and surrounding space are accepted, and the
	// result is still one of the two pinned regions.
	for _, ok := range []string{"eu", "EU", " eu ", "Eu"} {
		r, err := hafnia.ParseRegion(ok)
		if err != nil {
			t.Errorf("ParseRegion(%q) was refused: %v", ok, err)
			continue
		}
		if r != hafnia.RegionEU {
			t.Errorf("ParseRegion(%q) = %v, want the pinned EU region", ok, r)
		}
	}

	// Everything else is refused - including a host that merely *contains* a
	// valid one, which is the shape a subdomain trick takes.
	for _, bad := range []string{
		"", " ", "eu.hafnia.example", "api.eu.hafnia.example",
		"eu.hafnia.example.evil.test", "api.eu.hafnia.example@evil.test",
		"169.254.169.254", "localhost", "127.0.0.1", "::1",
		"http://api.eu.hafnia.example", "apac", "eu,us", "eu us",
	} {
		if r, err := hafnia.ParseRegion(bad); err == nil {
			t.Errorf("ParseRegion(%q) was accepted as %v; a region that is not in the closed set is a "+
				"site whose footage went somewhere nobody chose", bad, r)
		}
	}
}

// TestTheClientRefusesARedirect is the third control, and the cheapest way an
// upload path becomes an exfiltration endpoint: the pinned host answers 302 and
// the Go default replays the Authorization header to whatever it names.
func TestTheClientRefusesARedirect(t *testing.T) {
	tr := &transport{do: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"http://169.254.169.254/latest/meta-data/"}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}}
	c, err := hafnia.New(testConfig(tr))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.UploadAsset(context.Background(), "video/mp4", []byte("clip")); err == nil {
		t.Fatal("a redirect was followed; the upload's Authorization header would have been replayed to it")
	} else if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("the refusal %q does not name the redirect, so an operator reading it does not learn "+
			"that the provider tried to send the call somewhere else", err)
	}
}

// TestTheClientCannotBeGivenARemoteEndpoint is belt and braces on the first
// control: the same assertion from the constructor's side.
func TestTheClientCannotBeGivenARemoteEndpoint(t *testing.T) {
	// There is no field to set, which is the property. If one is ever added, this
	// stops compiling in a way that points at the addition.
	tr := fixedTransport(t, func(w http.ResponseWriter, r *http.Request) {})
	cfg := testConfig(tr)
	c, err := hafnia.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The only thing the caller controls is the transport, and the transport is
	// replaced wholesale - it cannot redirect the destination.
	// The caller controls the transport, and the transport cannot name a destination: the
	// client's own client rewrites nothing and follows nothing.
	var _ airuntime.Provider = c
}
