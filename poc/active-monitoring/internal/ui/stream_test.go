package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTheLiveViewIsAStreamNotARefresh is the no-JavaScript promise, tested.
//
// A browser renders a `multipart/x-mixed-replace` response inside a plain <img>
// and updates it as parts arrive, so the live view needs no script at all. If
// the endpoint stopped being a stream the page would silently become a picture
// that has to be reloaded by hand, and nothing in the markup would say so — which
// is why this is a test and not a comment.
func TestTheLiveViewIsAStreamNotARefresh(t *testing.T) {
	s, id := uiStore(t)
	u := newUI(t, s, id)
	u.Publish(Live{At: time.Now(), Seq: 7, Width: 4, Height: 3, AIState: "no_objects",
		Provenance: "synthetic", Source: "recorded loop (no live camera)", Mode: "demo-replay",
		Frame: tinyJPEG(t, 4, 3)})

	srv := httptest.NewServer(u.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/live.mjpeg", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the stream endpoint did not open: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/x-mixed-replace") {
		t.Fatalf("the live view served %q, which is not a stream; the page would need a reload by hand", ct)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if n == 0 {
		t.Fatal("the stream opened and delivered nothing")
	}
	if part := string(buf[:n]); !strings.Contains(part, "--pocframe") {
		t.Fatalf("the first stream bytes are not a multipart part: %q", part)
	}

	// And the page itself must carry no script, on either view.
	for _, path := range []string{"/", "/board"} {
		_, body := get(t, u.Handler(), path)
		if strings.Contains(strings.ToLower(body), "<script") {
			t.Fatalf("%s contains a script; the live view is a stream precisely so it does not need one", path)
		}
	}
	if !strings.Contains(get2(t, u.Handler(), "/"), "/live.mjpeg") {
		t.Fatal("the live page does not embed the stream")
	}
}

func get2(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	_, body := get(t, h, path)
	return body
}
