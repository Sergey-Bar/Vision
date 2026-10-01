package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEveryResponseCarriesTheSecurityHeaders is W2-9.
//
// The screen is loopback-only and read-only, and it was carrying NO browser
// policy at all. That matters for a demo specifically: the earlier claim was "we
// show the verdict", carried by a row of HTML that a crop tool removes, and the
// cheapest fix for a sceptic is a bookmarklet that hides the row. A verdict that
// is drawn in the pixels cannot be hidden by a page that has no script.
//
// So the policy says there is no script, and it says it in a header rather than in
// a comment: a future edit that adds JavaScript has to edit this line, and the
// edit is visible in a diff.
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	st, runID := uiStore(t)
	s := newUI(t, st, runID)
	h := s.Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	for _, path := range []string{"/", "/board", "/api/summary", "/frame.jpg", "/live.mjpeg", "/no-such-route"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			csp := resp.Header.Get("Content-Security-Policy")
			if csp == "" {
				t.Fatalf("%s answered with no Content-Security-Policy", path)
			}
			for _, want := range []string{
				"default-src 'none'",
				"script-src 'none'",
				"img-src 'self' data:",
				"frame-ancestors 'none'",
				"base-uri 'none'",
				"form-action 'none'",
			} {
				if !strings.Contains(csp, want) {
					t.Errorf("%s: the policy does not contain %q. Policy: %s", path, want, csp)
				}
			}
			if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("%s: Referrer-Policy is %q; the screen's URLs carry a run id and a detection id", path, got)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("%s: X-Content-Type-Options is %q", path, got)
			}
		})
	}
}

// TestThePolicyIsNotSoStrictTheScreenBreaks: a policy that makes the screen blank
// is not a policy, it is an outage. This fetches the page and reads the picture
// element out of it, because a CSP that blocks `img-src` fails as a picture that
// does not appear and nobody calls it a security win.
func TestThePolicyIsNotSoStrictTheScreenBreaks(t *testing.T) {
	st, runID := uiStore(t)
	s := newUI(t, st, runID)
	// A frame has to be published, or the stage correctly says "no frame has been
	// read yet" and there is no <img> to check. A policy test that passes because
	// the page is empty is a policy test that proves nothing.
	s.Publish(Live{At: time.Now().UTC(), Frame: tinyJPEG(t, 64, 48), Width: 64, Height: 48,
		AIState: "not_asked", AIStateKnown: true})
	srv := httptest.NewServer(s.Handler())
	h := s.Handler()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := get2(t, h, "/")

	if !strings.Contains(body, "<img") {
		t.Fatal("the live page has no <img>; the picture is drawn server-side and served as a JPEG")
	}
	if !strings.Contains(body, "/frame.jpg") && !strings.Contains(body, "/live.mjpeg") {
		t.Fatal("the live page names no picture source; the operator screen would show an empty page that looks fine")
	}
	// No script anywhere. If this assertion ever fails, the architecture changed and
	// ContentSecurityPolicy has to change with it — deliberately, in a diff.
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Fatal("the live page contains a <script> element; this module draws server-side and must stay that way")
	}
	// The JSON endpoint still answers, because `connect-src 'self'` is the only
	// thing the live page fetches.
	jr, err := http.Get(srv.URL + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer jr.Body.Close()
	var probe map[string]any
	if err := json.NewDecoder(jr.Body).Decode(&probe); err != nil {
		t.Fatalf("/api/summary is not JSON: %v", err)
	}
}

// TestTheVerdictReachesThePictureAndThePage is W2-8's other half: the verdict is
// carried on `ui.Live`, drawn into the overlay, and printed on the page, so a
// screenshot of either carries it.
func TestTheVerdictReachesThePictureAndThePage(t *testing.T) {
	st, runID := uiStore(t)
	s := newUI(t, st, runID)
	s.Publish(Live{
		At: time.Now().UTC(), Frame: tinyJPEG(t, 64, 48), Width: 64, Height: 48,
		AIState: "asked_answered", AIStateKnown: true,
		VerdictKnown: true, Verdict: "confirmed", VerdictSummary: "a vehicle crossed the line",
		PushResult: "push_sent", Counters: "det 4  ·  pushed 4  ·  vlm hafnia",
	})
	srv := httptest.NewServer(s.Handler())
	h := s.Handler()
	defer srv.Close()

	// The page.
	body := get2(t, h, "/")
	if !strings.Contains(body, "confirmed") {
		t.Fatal("the page does not carry the verdict; the claim that the screen shows it is not true")
	}

	// The pixels. This is the one that cannot be cropped, because there is no
	// script and no DOM layer between the operator and the image.
	img, err := http.Get(srv.URL + "/frame.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer img.Body.Close()
	if img.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("/frame.jpg answered %q", img.Header.Get("Content-Type"))
	}
	jpeg, err := io.ReadAll(img.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(jpeg) == 0 {
		t.Fatal("/frame.jpg is empty")
	}
	if string(jpeg[:2]) != "\xff\xd8" {
		t.Fatal("/frame.jpg is not a JPEG")
	}
}

// TestAnUnansweredRunSaysSoRatherThanLookingQuiet: a screen with an empty verdict
// cell next to a live picture reads as a negative answer, which is the one
// impression this module must never give by omission.
func TestAnUnansweredRunSaysSoRatherThanLookingQuiet(t *testing.T) {
	st, runID := uiStore(t)
	s := newUI(t, st, runID)
	s.Publish(Live{At: time.Now().UTC(), Frame: tinyJPEG(t, 64, 48), Width: 64, Height: 48,
		AIState: "not_asked", AIStateKnown: true})
	srv := httptest.NewServer(s.Handler())
	h := s.Handler()
	defer srv.Close()
	body := strings.ToLower(get2(t, h, "/"))
	if strings.Contains(body, ">confirmed<") || strings.Contains(body, ">false_positive<") {
		t.Fatalf("an unanswered run shows a verdict: %s", body)
	}
	if !strings.Contains(body, "not asked") && !strings.Contains(body, "no verdict") {
		t.Fatalf("an unanswered run says nothing about the verdict at all: %s", body)
	}
}
