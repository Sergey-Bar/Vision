package main

// Tests for the pinned model fetch — plan B3.
//
// The pin is two constants and one behaviour, and each has its own failure mode:
//
//   - a URL that is not https, or a digest that is not 64 hex characters, is a
//     pin that would silently never match anything;
//   - a Go constant and a JSON record of the same fact drift the moment one of
//     them is edited alone, which is why the test reads the record;
//   - the WARNING-not-wall behaviour is the plan's own words and the thing most
//     likely to be "simplified" into a refusal later, so it is asserted against
//     a server rather than described.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheModelPinIsAnHTTPSURLAndA64CharacterDigest(t *testing.T) {
	if !strings.HasPrefix(defaultModelURL, "https://") {
		t.Errorf("defaultModelURL = %q. A pinned default that is not https is a default whose bytes "+
			"cross the network unauthenticated to a name that could be intercepted", defaultModelURL)
	}
	if len(defaultModelSHA256) != 64 {
		t.Fatalf("defaultModelSHA256 is %d characters, want 64: %q", len(defaultModelSHA256), defaultModelSHA256)
	}
	for _, c := range defaultModelSHA256 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("defaultModelSHA256 holds %q, which is not lowercase hex: %s. hashFile produces lowercase, "+
				"and a pin in any other case compares equal to nothing", string(c), defaultModelSHA256)
		}
	}
}

// TestTheModelPinAgreesWithTheLicenceRecord is the drift check.
//
// `pyworker/fixtures/yolov4n.licence.json` is the committed provenance record and
// `defaultModelURL`/`defaultModelSHA256` are the pin the fetch checks against.
// Two records of one fact in two files is exactly the arrangement that drifts,
// and the failure it produces is a specific one: a judge fetches the pinned URL,
// the bytes match the pin, and the record beside them names a different origin —
// or worse, a record that says NOT RECORDED while the code claims provenance.
//
// The ANCHORS row is the same check for the second pinned digest: the model's
// `[anchors]` section is the file a clean clone cannot decode without, and its
// pin (`defaultAnchorsSHA256`), the bytes the binary embeds and the licence
// record's `anchors.sha256` are three records of one fact.
func TestTheModelPinAgreesWithTheLicenceRecord(t *testing.T) {
	path := filepath.Join("..", "..", "pyworker", "fixtures", "yolov4n.licence.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s is not reachable from here (%v). The unit checks above still hold; the cross-file "+
			"agreement could not be measured on this layout", path, err)
	}
	var rec struct {
		SHA256 string `json:"sha256"`
		Origin struct {
			URL string `json:"url"`
		} `json:"origin"`
		Anchors struct {
			SHA256        string `json:"sha256"`
			CommittedCopy string `json:"committed_copy"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("%s is not readable as the licence record: %v", path, err)
	}
	if got := strings.ToLower(strings.TrimSpace(rec.SHA256)); got != defaultModelSHA256 {
		t.Errorf("the licence record's sha256 is %q and the fetch pin is %q. They describe the same bytes; "+
			"a judge trusting the record beside a fetch that checked the pin would be trusting one of two "+
			"disagreeing numbers", got, defaultModelSHA256)
	}
	if got := strings.TrimSpace(rec.Origin.URL); got != defaultModelURL {
		t.Errorf("the licence record's origin.url is %q and the fetch pin is %q. The origin the code will "+
			"fetch from and the origin the record names are the same claim", got, defaultModelURL)
	}
	if got := strings.ToLower(strings.TrimSpace(rec.Anchors.SHA256)); got != defaultAnchorsSHA256 {
		t.Errorf("the licence record's anchors.sha256 is %q and the fetch's anchors pin is %q. They "+
			"describe the same file, and `fetch-model` writes the pinned one: a judge whose clone "+
			"receives one and whose record names the other is told a provenance that did not arrive",
			got, defaultAnchorsSHA256)
	}
	if got := strings.TrimSpace(rec.Anchors.CommittedCopy); got == "" {
		t.Error("the licence record's anchors row names no committed copy, so a clean clone has nowhere " +
			"to be told the anchors come from")
	}
}

// serveModel stands up a one-file HTTP server so the fetch's two digest paths are
// exercised against real bytes without the network.
func serveModel(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAMatchingDigestIsConfirmed is the success path, and it asserts the file is
// written with the bytes that were served rather than merely that no error
// came back: a fetch that accepted the right NAME and the wrong content would
// pass an error-only test.
func TestAMatchingDigestIsConfirmed(t *testing.T) {
	const body = "not really 257 MB of weights, but the same bytes at both ends"
	srv := serveModel(t, body)
	out := filepath.Join(t.TempDir(), "models", "yolov4n.onnx")
	if err := fetchModelTo(srv.URL+"/yolov4.onnx", out, sumOf(t, body), true, false); err != nil {
		t.Fatalf("a download matching the expected digest was refused: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if string(got) != body {
		t.Fatalf("wrote %q, want the bytes that were served", got)
	}
}

// TestAnOperatorPinnedMismatchIsARefusalAndRemovesTheFile is the wall half.
//
// Somebody passed --sha256, which is a decision, and the decision is honoured:
// the bytes do not reach `models/`, because a run that loaded a library beside
// the one it measured against is a crash inside the detector a week later with
// an error naming neither file.
func TestAnOperatorPinnedMismatchIsARefusalAndRemovesTheFile(t *testing.T) {
	srv := serveModel(t, "the bytes that arrived")
	out := filepath.Join(t.TempDir(), "models", "yolov4n.onnx")
	err := fetchModelTo(srv.URL+"/yolov4.onnx", out, strings.Repeat("0", 64), true, false)
	if err == nil {
		t.Fatal("a download that did not match an operator-pinned digest was accepted")
	}
	if !strings.Contains(err.Error(), "--sha256") {
		t.Errorf("the refusal does not name the flag that created the wall: %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("the mismatched file was left on disk at the path a run loads; a wall that keeps the " +
			"payload is a warning that shouts")
	}
}

// TestADefaultPinMismatchIsAPrintedWarningAndNotAWall is the plan's own sentence
// for B3, asserted as behaviour rather than described in a comment.
//
// Nobody pinned a value, so the default's digest is the check, and the file is
// KEPT with the mismatch printed in full. The reasoning is in the fetch's own
// doc comment: a default that hard-refused on drift would turn a label problem
// into a broken demo, and the judge whose download arrived different needs to be
// told what arrived and how to record it.
func TestADefaultPinMismatchIsAPrintedWarningAndNotAWall(t *testing.T) {
	srv := serveModel(t, "the bytes that arrived")
	out := filepath.Join(t.TempDir(), "models", "yolov4n.onnx")
	out2 := captureStdout(t, func() {
		err := fetchModelTo(srv.URL+"/yolov4.onnx", out, strings.Repeat("0", 64), false, true)
		if err != nil {
			t.Fatalf("a default-pin mismatch refused instead of warning: %v", err)
		}
	})
	for _, want := range []string{"WARNING", "does NOT match", out} {
		if !strings.Contains(out2, want) {
			t.Errorf("the warning does not say %q:\n%s", want, out2)
		}
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the file was not kept, which makes the warning unusable — the operator cannot record "+
			"the digest of bytes that were deleted: %v", err)
	}
	if string(got) != "the bytes that arrived" {
		t.Fatalf("wrote %q, want the bytes that were served", got)
	}
}

// TestTheDefaultPathSaysWhereThePinCameFrom covers the first-run print.
//
// A judge who clones and runs `fetch-model` with no flags is trusting a URL
// compiled into the binary. The print names the URL, says it was measured, and
// points at the record — because "the default is plausible" is not provenance and
// "the default was checked against the very bytes this repository measured" is.
func TestTheDefaultPathSaysWhereThePinCameFrom(t *testing.T) {
	const body = "not really 257 MB of weights, but the same bytes at both ends"
	srv := serveModel(t, body)
	out := filepath.Join(t.TempDir(), "models", "yolov4n.onnx")
	got := captureStdout(t, func() {
		// fromDefault with a matching digest is the clean-clone path.
		if err := fetchModelTo(srv.URL+"/yolov4.onnx", out, sumOf(t, body), false, true); err != nil {
			t.Fatalf("the default path refused a matching download: %v", err)
		}
	})
	for _, want := range []string{"pinned default", "MEASURED", "yolov4n.licence.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("the default path does not say %q:\n%s", want, got)
		}
	}
}

func sumOf(t *testing.T, body string) string {
	t.Helper()
	sum, _, err := hashFile(writeTemp(t, "model.onnx", body))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return sum
}
