package fixture_test

// This file holds the end-of-stream tests. `fixture_test.go` and
// `helpers_test.go` are shared with the rest of the package's suite, so these
// land in a file of their own.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/fixture"
)

// diesMidBody is a reader that hands over a prefix and then reports that the
// stream ended before the body did. This is what a capture harness sees when the
// connection drops between two packets of a response, and it is the shape a
// truncated capture arrives in.
type diesMidBody struct {
	prefix   string
	afterHit error
	hit      bool
}

func (r *diesMidBody) Read(p []byte) (int, error) {
	if r.hit {
		return 0, r.afterHit
	}
	r.hit = true
	n := copy(p, r.prefix)
	if n < len(r.prefix) {
		// Put the tail back for the next call, so a short buffer does not
		// silently become the end of the body.
		r.prefix = r.prefix[n:]
		r.hit = false
		return n, nil
	}
	return n, r.afterHit
}

// `io.EOF` and `io.ErrUnexpectedEOF` are different facts and only one of them is
// a complete body. A capture recorded from a reader that returned the second was
// stored as a valid fixture, and the manifest hash matched the truncated bytes -
// which defeats the whole point of a manifest, which is to notice a fixture set
// that was changed after anybody read it.
func TestATruncatedCaptureIsNotRecordedAsAValidFixture(t *testing.T) {
	const body = `{"events":[{"id":"evt-1"}]}`

	t.Run("a clean end of stream is recorded", func(t *testing.T) {
		// A clean io.EOF at the boundary is a complete body. Refusing this one
		// would break every capture, and a check that refuses the normal case
		// gets switched off.
		raw, err := fixture.ReadLimited(strings.NewReader(body))
		if err != nil {
			t.Fatalf("a reader that reached the boundary was refused: %v", err)
		}
		if string(raw) != body {
			t.Errorf("read %q, want %q", raw, body)
		}
	})

	// The first two are the same defect reached two ways: a bare
	// `io.ErrUnexpectedEOF` and one our own code wrapped with context. The third
	// is what the old substring check accepted - an error that merely mentions
	// EOF and is neither sentinel. It is not a truncation, so it is not reported
	// as one, but it is a failed capture and it is not stored.
	for _, tc := range []struct {
		name           string
		reader         io.Reader
		wantIncomplete bool
	}{
		{"io.ErrUnexpectedEOF", &diesMidBody{prefix: body, afterHit: io.ErrUnexpectedEOF}, true},
		{"a wrapper naming io.ErrUnexpectedEOF", &diesMidBody{
			prefix: body, afterHit: fmt.Errorf("read body: %w", io.ErrUnexpectedEOF)}, true},
		{"an error merely containing the text EOF", &diesMidBody{
			prefix: body, afterHit: errContainsEOF{}}, false},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			raw, err := fixture.ReadLimited(tc.reader)
			if err == nil {
				t.Fatalf("a capture that failed mid-stream was recorded: %d byte(s) came "+
					"back as a complete body. The bytes are %q, and a manifest would hash "+
					"these and agree with them", len(raw), raw)
			}
			if raw != nil {
				t.Errorf("the incomplete read was handed back as usable bytes: %q", raw)
			}
			if tc.wantIncomplete && !strings.Contains(err.Error(), "incomplete") {
				t.Errorf("the refusal should say the capture is incomplete, got %q", err)
			}
		})
	}
}

// errContainsEOF has "EOF" somewhere in its message and is not either sentinel.
// The old check was `strings.Contains(err.Error(), "EOF")`, so it accepted this.
type errContainsEOF struct{}

func (errContainsEOF) Error() string { return "transport gave up: EOF while idle" }

// The same distinction at the choke point a capture actually goes through. A
// response whose body dies mid-stream must be refused by SanitizeResponse, and
// the reason it has to be refused here rather than left to `json.Valid` is that
// json.Valid is a shape check: a truncation that happens to land on a boundary
// parses.
func TestSanitizeRefusesAResponseWhoseBodyDiedMidStream(t *testing.T) {
	const body = `{"id":"evt-1","events":[]}`
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(&diesMidBody{prefix: body, afterHit: io.ErrUnexpectedEOF}),
	}
	resp.Header.Set("Content-Type", "application/json")

	if _, _, err := fixture.SanitizeResponse(resp); err == nil {
		t.Fatal("a response whose body died mid-stream was stored as a fixture")
	} else if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("the refusal should say the capture is incomplete, got %q", err)
	}
}

// A clean end of stream through the same choke point is still a fixture, so the
// refusal above is about truncation and not about being strict.
func TestSanitizeStillRecordsACompleteResponse(t *testing.T) {
	const body = `{"id":"evt-1","events":[]}`
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	resp.Header.Set("Content-Type", "application/json")

	clean, headers, err := fixture.SanitizeResponse(resp)
	if err != nil {
		t.Fatalf("a complete response was refused: %v", err)
	}
	if len(clean) == 0 || len(headers) == 0 {
		t.Errorf("a complete response recorded nothing: %q %v", clean, headers)
	}
}

// The two sentinels are the whole fix, and a test that only went through the
// reader above would still pass if isEOF were handed a non-sentinel error that
// wraps io.EOF. A wrapper around a clean EOF is still clean: our own context
// should not turn a good read into a refused one.
func TestACleanEOFWrappedInContextIsStillClean(t *testing.T) {
	raw, err := fixture.ReadLimited(&diesMidBody{
		prefix:   `{"a":1}`,
		afterHit: fmt.Errorf("reading the capture body: %w", io.EOF),
	})
	if err != nil {
		t.Fatalf("a wrapped clean EOF was refused: %v", err)
	}
	if string(raw) != `{"a":1}` {
		t.Errorf("read %q, want the whole body", raw)
	}
	// And the sentinel test is a real assertion, not a tautology: the same
	// wrapper around the other sentinel is not clean.
	if errors.Is(io.ErrUnexpectedEOF, io.EOF) {
		t.Error("this test's premise is wrong: io.ErrUnexpectedEOF satisfies " +
			"errors.Is(err, io.EOF), so no implementation could tell them apart")
	}
}
