package xprotect

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/shared/aistate"
)

// specDir is the downloaded Milestone documentation, relative to this package.
const specDir = "../../contracts/xprotect"

func readSpec(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(specDir, "bookmarks-rest-v1.openapi.yaml"))
	if err != nil {
		t.Fatalf("the downloaded specification is missing: %v; the whole point of this package is that every call is checked against it", err)
	}
	return string(body)
}

// TestEveryPathWeCallIsInTheSpecification is the T21 mechanism. Change a path in
// the code and this test fails; it does not need a VMS to fail.
//
// Each path is checked against the document that actually declares it. The
// bookmarks specification is the YAML Milestone serves; the events endpoints
// are declared in the events reference page, because Milestone's events
// OpenAPI file is not reachable (see contracts/xprotect/README.md).
func TestEveryPathWeCallIsInTheSpecification(t *testing.T) {
	bookmarks := readSpec(t)
	events := readEvents(t)

	cases := []struct {
		path    string
		doc     string
		docName string
	}{
		{PathBookmarks, bookmarks, "bookmarks OpenAPI"},
		{PathEventTypes, events, "events reference"},
		{PathEvents, events, "events reference"},
		{PathEventsBulk, events, "events reference"},
		{PathToken, events, "events reference"},
	}
	for _, tc := range cases {
		// The documents spell the REST paths relative to the server root, in
		// either case, and the events reference shows them lowercased in the
		// server URL. So the check is on the path segment, not the casing.
		if strings.Contains(tc.doc, tc.path) {
			continue
		}
		relative := strings.TrimPrefix(tc.path, APIRestPrefix)
		if strings.Contains(tc.doc, relative) {
			continue
		}
		lower := strings.ToLower(relative)
		if lower != relative && strings.Contains(tc.doc, lower) {
			continue
		}
		t.Fatalf("%s does not appear in the %s: a path that is not in the documentation is a guess", tc.path, tc.docName)
	}
}

func TestTheSpecificationSaysTheAPIRestV1Prefix(t *testing.T) {
	// The parent platform guessed /api/v1. The published servers block says
	// /api/rest/v1, in either casing, and this client uses the lower-case
	// spelling the platform's own route table uses. Getting this wrong makes
	// every call a 404, silently.
	//
	// The specification is a RECORDED VENDOR DOCUMENT and it is not edited to
	// match this client: it says what the vendor published, and the client
	// matches the vendor. What this test pins is that the two still agree
	// about the PATH, in either casing -- a vendor that changed its prefix
	// would be a fact the client has to be told about, not one the client's
	// test quietly accepts.
	spec := readSpec(t)
	if !strings.Contains(strings.ToLower(spec), strings.ToLower(APIRestPrefix)) {
		t.Fatalf("the specification no longer declares the %s prefix this client uses", APIRestPrefix)
	}
	if strings.Contains(spec, "\n  - url: https://localhost/api/v1") {
		t.Fatal("the specification now says /api/v1, and this client would be wrong")
	}
}

// TestEveryStatusCodeWeAcceptIsInTheSpecification.
func TestEveryStatusCodeWeAcceptIsInTheSpecification(t *testing.T) {
	spec := readSpec(t)
	// 200 for bookmarks and 202 for events are the two success codes the plan
	// names. They must both be present in the published document.
	if !strings.Contains(spec, "200") {
		t.Fatal("the specification no longer mentions 200, which the bookmark call accepts")
	}
	eventsSpec, err := os.ReadFile(filepath.Join(specDir, "events-rest-v1.reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventsSpec), "202") {
		t.Fatal("the events reference no longer mentions 202, which the event call accepts")
	}
	if !strings.Contains(string(eventsSpec), "207") {
		t.Fatal("the events reference no longer mentions 207, which the bulk call accepts")
	}
}

// TestEveryFieldWeSendExistsInTheSpecification is the T19/T20 field check. It
// marshals a real request and looks for each top-level field name in the
// documents.
func TestEveryFieldWeSendExistsInTheSpecification(t *testing.T) {
	spec := readSpec(t) + readEvents(t)
	bm, _ := json.Marshal(BookmarkRequest{Header: "h", DevicePath: DevicePath{Type: "local", ID: "x"},
		TimeTriggered: "2026-09-30T10:15:04Z"})
	for _, f := range fieldsOf(t, bm) {
		if f == "omitempty" {
			continue
		}
		if !strings.Contains(spec, f) {
			t.Fatalf("the bookmark body sends %q, which appears in neither downloaded document: a field the VMS does not know is a 400 or a silently ignored field", f)
		}
	}
	ev, _ := json.Marshal(BuildEventOrDie(t))
	for _, f := range fieldsOf(t, ev) {
		if f == "omitempty" {
			continue
		}
		if !strings.Contains(spec, f) {
			t.Fatalf("the event body sends %q, which appears in neither downloaded document", f)
		}
	}
}

// TestTheBookmarkBodyIsNotThePlatformsShape pins the difference from the parent
// platform's writeback.go, which posts idempotencyKey/deviceRef/label. Those
// field names do not exist in the published body.
func TestTheBookmarkBodyIsNotThePlatformsShape(t *testing.T) {
	body, _ := json.Marshal(BookmarkRequest{Header: "h", DevicePath: DevicePath{Type: "local", ID: "x"},
		TimeTriggered: "2026-09-30T10:15:04Z"})
	for _, wrong := range []string{"idempotencyKey", "deviceRef", "label", "bookmarks"} {
		if strings.Contains(string(body), wrong) {
			t.Fatalf("the bookmark body contains %q, which is the parent platform's guessed shape, not the published one", wrong)
		}
	}
	for _, right := range []string{"header", "devicePath", "timeTriggered"} {
		if !strings.Contains(string(body), right) {
			t.Fatalf("the bookmark body is missing the required field %q", right)
		}
	}
}

func TestTheBookmarkBodyCarriesNoVideo(t *testing.T) {
	// A bookmark is a pointer. If the body grew a payload field, the client
	// would be copying video to make up for a VMS recording that does not exist
	// in this build, which is both wasteful and a different claim.
	ty := reflect.TypeOf(BookmarkRequest{})
	for i := 0; i < ty.NumField(); i++ {
		switch ty.Field(i).Name {
		case "Payload", "Video", "Clip", "Data", "Bytes":
			t.Fatalf("the bookmark body grew a %s field: a bookmark points at a recording, it does not carry one", ty.Field(i).Name)
		}
	}
}

func BuildEventOrDie(t *testing.T) EventRequest {
	t.Helper()
	ev, err := BuildEvent(EventInput{
		EventTypeGUID: "b4d1e1c2-0000-4000-8000-000000000001",
		SourceGUID:    "0d9a1b2c-1111-2222-3333-444455556666",
		At:            time.Date(2026, 9, 30, 10, 15, 4, 123000000, time.UTC),
		Tag:           "aicap-bird-abc123",
		Class:         "bird", Value: "bird", Confidence: 0.87,
		Box:      Box{Top: 194, Left: 286, Bottom: 429, Right: 408},
		RuleName: "bird-line-cross",
		Evidence: EvidenceRef{StorageRef: "ab/cd/abcd.mp4", SHA256: strings.Repeat("a", 64), TimeBegin: "2026-09-30T10:14:59Z", TimeEnd: "2026-09-30T10:15:09Z"},
		Provenance: Provenance{
			AIState: aistate.Asked, Verdict: "confirmed",
			Summary:       "a bird crossed the line",
			ModelIdentity: "nvidia/cosmos-3", ContractVersion: "contract-1",
			OutputHash: strings.Repeat("b", 64),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func fieldsOf(t *testing.T, b []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func readEvents(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(specDir, "events-rest-v1.reference.md"))
	if err != nil {
		t.Fatalf("the events reference is missing: %v", err)
	}
	return string(b)
}

func TestEventBodyIsBuiltFromTheContractShape(t *testing.T) {
	ev := BuildEventOrDie(t)
	if ev.Source != "cameras/0d9a1b2c-1111-2222-3333-444455556666" {
		t.Fatalf("source = %q; the events API expects cameras/<guid>", ev.Source)
	}
	if ev.Datatype != "analytics" {
		t.Fatalf("datatype = %q; only analytics is what this POC produces", ev.Datatype)
	}
	if !strings.HasSuffix(ev.Time, "Z") {
		t.Fatalf("time = %q; the contract is ISO-8601 UTC", ev.Time)
	}
	if ev.Data.Object.BoundingBox.Top >= ev.Data.Object.BoundingBox.Bottom {
		t.Fatal("the bounding box is inverted")
	}
	if ev.Data.Vendor.Name != "aicaptain" {
		t.Fatalf("vendor = %q", ev.Data.Vendor.Name)
	}
}

func TestEventCarriesTheEvidenceRefBecauseThereIsNoVMSRecording(t *testing.T) {
	ev := BuildEventOrDie(t)
	if ev.Data.Evidence.SHA256 == "" || ev.Data.Evidence.StorageRef == "" {
		t.Fatal("the event carries no evidence ref: a bookmark points at a recording this build does not have, so the event has to be enough on its own")
	}
}

func TestBuildEventRefusesAnIncompleteDetection(t *testing.T) {
	cases := []struct {
		name string
		in   EventInput
		want string
	}{
		{"no type", EventInput{SourceGUID: "s", Tag: "t"}, "type guid"},
		{"no source", EventInput{EventTypeGUID: "t", Tag: "t"}, "source camera guid"},
		{"no tag", EventInput{EventTypeGUID: "t", SourceGUID: "s"}, "tag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildEvent(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// --- token handling ---------------------------------------------------------

func TestNewRefusesPlainHTTPToARemoteHost(t *testing.T) {
	_, err := New(Config{BaseURL: "http://vms.example.com", Timeout: time.Second,
		Credentials: Credentials{ClientID: "c"}})
	if err == nil || !strings.Contains(err.Error(), "clear text") {
		t.Fatalf("plain http to a remote host was accepted: %v", err)
	}
	if _, err := New(Config{BaseURL: "https://vms.example.com", Timeout: time.Second,
		Credentials: Credentials{ClientID: "c"}}); err != nil {
		t.Fatalf("https was refused: %v", err)
	}
}

func TestNewRefusesAMissingBaseURLClientIDOrTimeout(t *testing.T) {
	if _, err := New(Config{Timeout: time.Second, Credentials: Credentials{ClientID: "c"}}); err == nil {
		t.Fatal("a client with no base URL was built")
	}
	if _, err := New(Config{BaseURL: "https://x", Timeout: time.Second}); err == nil {
		t.Fatal("a client with no client id was built")
	}
	if _, err := New(Config{BaseURL: "https://x", Credentials: Credentials{ClientID: "c"}}); err == nil {
		t.Fatal("a client with no timeout was built")
	}
}

// TestJwtExpReadsTheClaimAndDoesNotClaimToVerify: the fallback path is used when
// the response declares no expires_in, which the quickstart's example does.
func TestJwtExpReadsTheClaim(t *testing.T) {
	// {"alg":"none","typ":"JWT"} / {"exp":1893456000} / sig
	tok := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJleHAiOjE4OTM0NTYwMDB9.sig"
	exp, ok := jwtExp(tok)
	if !ok {
		t.Fatal("the exp claim was not read")
	}
	if exp.Unix() != 1893456000 {
		t.Fatalf("exp = %d, want 1893456000", exp.Unix())
	}
	if _, ok := jwtExp("not-a-jwt"); ok {
		t.Fatal("a non-JWT was parsed as one")
	}
	if _, ok := jwtExp("a.b.c"); ok {
		t.Fatal("three arbitrary segments were parsed as a JWT")
	}
}

func TestTokenExpiryPrefersExpiresInAndFallsBack(t *testing.T) {
	// expires_in present: use it.
	got := tokenExpiry(tokenResponse{AccessToken: "x", ExpiresIn: ptr(int64(3600))}, time.Minute)
	if d := time.Until(got); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("expiry = %s, want about an hour from expires_in", d)
	}
	// No expires_in, a JWT with exp: use the claim.
	tok := "eyJhbGciOiJub25lIn0.eyJleHAiOjE4OTM0NTYwMDB9.sig"
	got = tokenExpiry(tokenResponse{AccessToken: tok}, time.Minute)
	if got.Unix() != 1893456000 {
		t.Fatalf("expiry = %s, want the claim's value", got)
	}
	// Neither: fall back to the request timeout, not to "never".
	got = tokenExpiry(tokenResponse{AccessToken: "opaque"}, 30*time.Second)
	if d := time.Until(got); d < 25*time.Second || d > 35*time.Second {
		t.Fatalf("expiry = %s, want the request timeout as the fallback", d)
	}
}

func ptr[T any](v T) *T { return &v }

func TestScrubRemovesSecretsFromErrorText(t *testing.T) {
	c := &Client{cfg: Config{Credentials: Credentials{ClientSecret: "topsecret", Password: "hunter2"}}}
	c.token = "eyJhbGciOiJub25l.payload.signature"
	in := "dial failed for client_secret=topsecret password=hunter2 token=eyJhbGciOiJub25l.payload.signature"
	got := scrub(in, c)
	for _, s := range []string{"topsecret", "hunter2", "eyJhbGciOiJub25l.payload.signature"} {
		if strings.Contains(got, s) {
			t.Fatalf("the scrubbed message still contains %q: %s", s, got)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8098", "http://localhost:1", "http://[::1]:8098"} {
		if !isLoopbackHost(u) {
			t.Fatalf("%s was not recognised as loopback", u)
		}
	}
	for _, u := range []string{"http://10.0.0.5", "https://vms.example.com", "http://127.0.0.1.evil.com"} {
		if isLoopbackHost(u) {
			t.Fatalf("%s was treated as loopback", u)
		}
	}
}

// TestTheClientDoesNotImportTheSimulator is the structural boundary the plan
// requires: internal/xprotect must not know what xpsim is. This is checked by
// reading the package's own source, because an import cycle would not compile
// but a mention would.
func TestTheClientDoesNotKnowTheSimulator(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"internal/xpsim", "xpsim"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s mentions %q: internal/xprotect must not know the simulator exists", e.Name(), forbidden)
			}
		}
	}
}

var _ = http.StatusOK
