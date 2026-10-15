package cams

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed table is itself under test. A table nobody loads in CI is a
// table that drifts into a URL typo, and the demo discovers it on stage.
func TestTheCommittedTableLoads(t *testing.T) {
	root := findRoot(t)
	cams, err := LoadFromRoot(root)
	if err != nil {
		t.Fatalf("the committed cameras.json did not load: %v", err)
	}
	// The plan's Definition of Done names two real cameras on the screen. The
	// table must be able to supply at least two, or the DoD is unreachable
	// from this checkout.
	if len(cams) < 2 {
		t.Fatalf("the table holds %d camera(s); the demo shows at least two real tiles, so a table smaller than that cannot evidence it", len(cams))
	}
	for _, c := range cams {
		if !c.HasLicence() {
			t.Errorf("camera %q has no usable licence basis; the loader enforces it, so reaching here means the loader and the table disagree", c.ID)
		}
		if c.ClearedForExternalUse {
			t.Errorf("camera %q is marked cleared for external use. Every camera in this table belongs to somebody else and no written permission to republish has been obtained; a screenshot of the demo is a screenshot of the camera", c.ID)
		}
	}
}

// findRoot is the module root, found from the test's own directory. The table
// is at the root and the test is two directories below it.
func findRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := os.Stat(filepath.Join(wd, TableFile)); err == nil {
			return wd
		}
		wd = filepath.Dir(wd)
	}
	t.Fatalf("no %s found walking up from the test's directory", TableFile)
	return ""
}

func writeTable(t *testing.T, cams []Camera) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), TableFile)
	raw, err := json.Marshal(map[string]any{"cameras": cams})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func goodCamera() Camera {
	return Camera{
		ID: "test-cam", Name: "A public test camera", Place: "Nowhere",
		Operator: "Somebody", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa",
		Licence:  "published publicly by the operator for viewing; no written permission to store",
		Verified: "2026-10-14", ContainsPeople: "yes", ClearedForExternalUse: false,
	}
}

func TestARowWithNoRecordedLicenceIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, licence string }{
		{"empty", ""},
		{"the marker", "NOT RECORDED, and it is not recoverable from this file"},
		{"the marker lower case", "not recorded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := goodCamera()
			c.Licence = tc.licence
			if _, err := Load(writeTable(t, []Camera{c})); err == nil {
				t.Fatalf("a camera row whose licence is %q was accepted. A public feed being PUBLIC is not the same as being free to record, and the table is where that gets said", tc.licence)
			} else if !strings.Contains(err.Error(), "licence") {
				t.Fatalf("the refusal does not name the licence field: %v", err)
			}
		})
	}
}

func TestARowThatIsNotAYoutubePageIsRefused(t *testing.T) {
	for _, u := range []string{
		"https://example.com/live.m3u8",
		"http://www.youtube.com/watch?v=aaaaaaaaaaa",
		"rtsp://camera.example/stream",
	} {
		c := goodCamera()
		c.URL = u
		if _, err := Load(writeTable(t, []Camera{c})); err == nil {
			t.Errorf("camera URL %q was accepted; this table is the YouTube relay path and the resolver has been run against nothing else", u)
		}
	}
}

func TestARowWithNoVerifiedDateIsRefused(t *testing.T) {
	for _, v := range []string{"", "last Tuesday", "2026-13-01"} {
		c := goodCamera()
		c.Verified = v
		if _, err := Load(writeTable(t, []Camera{c})); err == nil {
			t.Errorf("verified date %q was accepted; a row nobody has resolved live is a row describing a stream that may not exist", v)
		}
	}
}

func TestDuplicateIDsAreRefused(t *testing.T) {
	a, b := goodCamera(), goodCamera()
	b.URL = "https://www.youtube.com/watch?v=bbbbbbbbbbb"
	if _, err := Load(writeTable(t, []Camera{a, b})); err == nil {
		t.Fatal("two rows share an id and the table accepted them; one of them would be resolved as the other")
	}
}

func TestAnEmptyTableIsRefused(t *testing.T) {
	if _, err := Load(writeTable(t, nil)); err == nil {
		t.Fatal("an empty camera table was accepted; it resolves nothing while looking like a configuration")
	}
}

// --- the resolver -----------------------------------------------------------

func fakeResolver(out string, err error) Resolver {
	return Resolver{
		LookPath: func(string) (string, error) { return "/fake/yt-dlp", nil },
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(out), err
		},
	}
}

func TestAResolveReturnsTheStreamURL(t *testing.T) {
	r := fakeResolver("https://manifest.googlevideo.com/api/manifest/hls_playlist/x\n", nil)
	stream, err := r.Resolve(context.Background(), goodCamera())
	if err != nil {
		t.Fatalf("a clean resolve failed: %v", err)
	}
	if !strings.HasPrefix(stream, "https://manifest.googlevideo.com/") {
		t.Fatalf("stream %q is not the URL the resolver printed", stream)
	}
}

func TestAResolvePassesTheFormatAndRuntime(t *testing.T) {
	var got []string
	r := Resolver{
		LookPath:  func(string) (string, error) { return "/fake/yt-dlp", nil },
		JSRuntime: "node",
		Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			got = args
			return []byte("https://h/x.m3u8\n"), nil
		},
	}
	if _, err := r.Resolve(context.Background(), goodCamera()); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"--js-runtimes node", "-g", "-f bestvideo", goodCamera().URL} {
		if !strings.Contains(joined, want) {
			t.Errorf("the resolver did not pass %q; it passed %q. `-f best` asks for a pre-merged file, which a segmented live stream does not have, so the selection is part of the contract", want, joined)
		}
	}
}

func TestAResolveRefusesWhatIsNotOneHTTPSURL(t *testing.T) {
	cam := goodCamera()
	for _, tc := range []struct{ name, out string }{
		{"two lines", "https://h/v.m3u8\nhttps://h/a.m3u8\n"},
		{"nothing", "   \n\n"},
		{"plaintext", "http://h/x.m3u8\n"},
		{"credentials inline", "https://user:pass@h/x.m3u8\n"},
		{"not a URL", "not a url at all\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fakeResolver(tc.out, nil)
			if _, err := r.Resolve(context.Background(), cam); err == nil {
				t.Fatalf("resolver output %q was accepted", tc.out)
			}
		})
	}
}

func TestAResolveCarriesTheToolsOwnWordsWhenItFails(t *testing.T) {
	r := fakeResolver("ERROR: Video unavailable", errors.New("exit status 1"))
	_, err := r.Resolve(context.Background(), goodCamera())
	if err == nil {
		t.Fatal("a failing yt-dlp was reported as a successful resolve")
	}
	if !strings.Contains(err.Error(), "Video unavailable") {
		t.Fatalf("the error does not carry yt-dlp's own words: %v", err)
	}
}

func TestAResolveRefusesByInstallLineWhenTheToolIsAbsent(t *testing.T) {
	r := Resolver{LookPath: func(string) (string, error) { return "", errors.New("not found") }}
	_, err := r.Resolve(context.Background(), goodCamera())
	if err == nil {
		t.Fatal("a machine with no resolver produced a stream")
	}
	if !errors.Is(err, errUnresolvedTool) {
		t.Fatalf("the refusal is not the missing-tool sentinel: %v", err)
	}
	for _, want := range []string{"winget install", "SP_POC_YTDLP"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the operator has nothing to do: %v", want, err)
		}
	}
}

func TestTheJSRuntimeIsAutoDetectedAndDisableable(t *testing.T) {
	seen := map[string]int{}
	r := Resolver{
		LookPath: func(name string) (string, error) {
			seen[name]++
			if name == "node" || name == "yt-dlp" {
				return "/fake/" + name, nil
			}
			return "", errors.New("absent")
		},
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("https://h/x.m3u8\n"), nil
		},
	}
	if _, err := r.Resolve(context.Background(), goodCamera()); err != nil {
		t.Fatal(err)
	}
	if seen["node"] != 1 {
		t.Errorf("the resolver looked for node %d times; YouTube extraction needs a JS runtime and yt-dlp only warns about the ones it has", seen["node"])
	}

	seen = map[string]int{}
	r.JSRuntime = "none"
	if _, err := r.Resolve(context.Background(), goodCamera()); err != nil {
		t.Fatal(err)
	}
	if seen["node"] != 0 || seen["deno"] != 0 {
		t.Errorf("JSRuntime=none still probed for a runtime: %v", seen)
	}
}

func TestTheEnvVarNamesTheResolver(t *testing.T) {
	t.Setenv(EnvYtDlp, "/fake/yt-dlp")
	var used string
	r := Resolver{
		LookPath: func(name string) (string, error) {
			if name == "/fake/yt-dlp" {
				return name, nil
			}
			return "", errors.New("absent")
		},
		Run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			used = name
			return []byte("https://h/x.m3u8\n"), nil
		},
	}
	if _, err := r.Resolve(context.Background(), goodCamera()); err != nil {
		t.Fatal(err)
	}
	if used != "/fake/yt-dlp" {
		t.Fatalf("SP_POC_YTDLP was not honoured; the resolver used %q", used)
	}
}

// --- the spec ---------------------------------------------------------------

func TestTheSpecIsAPublicRelayAndGoesThroughParse(t *testing.T) {
	stream, err := Spec(goodCamera(), "https://manifest.googlevideo.com/x.m3u8")
	if err != nil {
		t.Fatalf("Spec refused a resolved stream: %v", err)
	}
	if stream.Kind != "url" {
		t.Errorf("kind = %q, want url; the camera is read over http(s) like any other live source", stream.Kind)
	}
	if !stream.PublicRelay {
		t.Error("PublicRelay is false. A YouTube live cam is a real camera relayed by somebody else, and the source sentence that derives from this flag is the only true one available")
	}
	if !stream.Live() {
		t.Error("the spec is not live; a resolved live stream must report real-time provenance")
	}
	// The same validation Parse applies to every other source applies here:
	// a URL with credentials is refused, so the row cannot smuggle one in
	// through the resolver.
	if _, err := Spec(goodCamera(), "https://user:pass@h/x.m3u8"); err == nil {
		t.Error("Spec accepted a stream URL with credentials inline; Parse refuses that for every other kind and this path must not be the exception")
	}
}

func TestRedactedKeepsTheHostAndDropsTheQuery(t *testing.T) {
	got := Redacted("https://manifest.googlevideo.com/api/manifest/hls_playlist/expire/1/ei/x/id/JQ_jwk/ip/1.2.3.4")
	if strings.Contains(got, "1.2.3.4") || strings.Contains(got, "expire") {
		t.Fatalf("Redacted kept the signed path, which carries the resolver's address: %q", got)
	}
	if !strings.HasPrefix(got, "https://manifest.googlevideo.com/") {
		t.Fatalf("Redacted lost the host, which is the part an operator recognises: %q", got)
	}
}
