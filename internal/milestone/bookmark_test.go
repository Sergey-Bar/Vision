package milestone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// The plan's P4.4: "The write-back bookmark lands on the incident the operator
// is looking at, with the clip pre-rolled and the time pre-rolled into the
// vendor's own recorder."
//
// The claim is only worth anything if the operator can find the moment, so the
// tests are about the *instant* the bookmark opens at, not about the call
// succeeding.

func aRequest() milestone.BookmarkRequest {
	return milestone.BookmarkRequest{
		IncidentID:  "inc_abc123",
		SiteRef:     "Perimeter North [CAM-014]",
		SiteID:      "site-hq",
		WindowStart: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 7, 1, 12, 0, 8, 0, time.UTC),
		PreRoll:     10 * time.Second,
		PostRoll:    5 * time.Second,
		Summary:     "unverified change",
	}
}

// TestTheBookmarkOpensBeforeTheWindow not at it: a detector fires *after* motion
// is visible, so a bookmark at the start of the window lands on the aftermath.
// The feature exists to stop the operator scrubbing, and scrubbing is exactly
// what an aftermath forces.
func TestTheBookmarkOpensBeforeTheWindow(t *testing.T) {
	decision, openedAt, to := milestone.DecideBookmark(aRequest(), clock.DefaultPolicy())
	if decision.Refusal != "" {
		t.Fatalf("a healthy request was refused: %s", decision.Refusal)
	}
	if !openedAt.Before(aRequest().WindowStart) {
		t.Errorf("the bookmark opens at %v, which is not before the window starts at %v",
			openedAt, aRequest().WindowStart)
	}
	if got := aRequest().WindowStart.Sub(openedAt); got != 10*time.Second {
		t.Errorf("the pre-roll is %v, want the requested 10s", got)
	}
	if !to.After(aRequest().WindowEnd) {
		t.Errorf("the post-roll did not extend past the window: %v", to)
	}
}

// TestTheDriftIsCorrected: the vendor stamped its recording on its own clock, and
// a bookmark placed at our instant opens at the wrong second. A bookmark that
// opens at the wrong second looks exactly like a system with no evidence.
func TestTheDriftIsCorrected(t *testing.T) {
	req := aRequest()
	_, withoutDrift, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())

	req.Drift = milestone.Drift{Offset: 8 * time.Second}
	_, withDrift, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())

	if withDrift.Sub(withoutDrift) != 8*time.Second {
		t.Errorf("a vendor clock 8s ahead moved the bookmark by %v; the correction is not applied",
			withDrift.Sub(withoutDrift))
	}
}

// TestASevereDriftRefusesTheBookmark: better no bookmark than a wrong one. A
// bookmark that opens thirty seconds from the event sends the operator to the
// wrong part of a recording, and they conclude the product has no evidence.
func TestASevereDriftRefusesTheBookmark(t *testing.T) {
	req := aRequest()
	req.Drift = milestone.Drift{Offset: 45 * time.Second}

	decision, _, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())
	if decision.Refusal == "" {
		t.Fatal("a bookmark was placed with a clock 45 seconds out")
	}
	if decision.Placed {
		t.Error("a refused bookmark is reported as placed")
	}
}

// TestEveryRefusalNamesItself: a bookmark silently not placed is an operator who
// follows an incident and finds nothing, and the absence of a bookmark is
// indistinguishable from the absence of evidence.
func TestEveryRefusalNamesItself(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*milestone.BookmarkRequest)
	}{
		{"no vendor camera reference", func(r *milestone.BookmarkRequest) { r.SiteRef = "" }},
		{"no window", func(r *milestone.BookmarkRequest) { r.WindowStart = time.Time{} }},
		{"inverted window", func(r *milestone.BookmarkRequest) { r.WindowEnd = r.WindowStart.Add(-time.Second) }},
		{"severe drift", func(r *milestone.BookmarkRequest) { r.Drift = milestone.Drift{Offset: time.Minute} }},
	}
	for _, tc := range cases {
		req := aRequest()
		tc.mut(&req)
		decision, _, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())
		if decision.Refusal == "" {
			t.Errorf("%s: no refusal was given", tc.name)
		}
		if decision.Placed {
			t.Errorf("%s: a refused bookmark reports itself as placed", tc.name)
		}
	}
}

// TestTheIdempotencyKeyIsDerivedAndStable is P2.8's shape applied to the write
// side: a retry after a timeout must not create a second bookmark, and a second
// bookmark at the same second is an operator-visible defect.
func TestTheIdempotencyKeyIsDerivedAndStable(t *testing.T) {
	req := aRequest()
	_, openedAt, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())
	first := milestone.BuildBookmark(req, openedAt)
	second := milestone.BuildBookmark(req, openedAt)
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Error("the same facts produced two idempotency keys; a retry would create a second bookmark")
	}
	if first.IdempotencyKey == "" {
		t.Error("no idempotency key; a retry cannot be made safe")
	}

	// A different incident must not collide.
	other := req
	other.IncidentID = "inc_def456"
	if milestone.BuildBookmark(other, openedAt).IdempotencyKey == first.IdempotencyKey {
		t.Error("two incidents produced the same idempotency key; one would silently replace the other")
	}
}

// TestTheBookmarkCarriesNoFootage: the entire cost model of this product is that
// it does not duplicate an archive. A bookmark that pointed at a stored clip
// would mean the site kept a second copy of its own video.
func TestTheBookmarkCarriesNoFootage(t *testing.T) {
	req := aRequest()
	_, openedAt, _ := milestone.DecideBookmark(req, clock.DefaultPolicy())
	b := milestone.BuildBookmark(req, openedAt)

	if b.SiteRef == "" || b.Time.IsZero() || b.Label == "" {
		t.Errorf("the bookmark is missing what makes it usable: %+v", b)
	}
	// The description says why the operator is being sent there. An operator who
	// lands on a recording with no context lands on it and leaves.
	if !strings.Contains(b.Description, "Unverified change") {
		t.Errorf("the description does not say what the operator is looking at:\n%s", b.Description)
	}
	if !strings.Contains(b.Description, "pre-rolled") {
		t.Errorf("the description does not explain why the bookmark is earlier than the window:\n%s", b.Description)
	}
	// And the label is bounded, because it is one line in somebody else's UI.
	if len(b.Label) > milestone.BookmarkLabelMax {
		t.Errorf("the label is %d characters; the vendor's bookmark list is one line", len(b.Label))
	}
}

// TestPlacingABookmarkReachesTheVendorAndReportsAFailureHonestly: when the
// placement is unconfirmed the caller has to be able to say so, because "we do
// not know whether the operator will find a bookmark" is not a sentence an
// operator can act on.
func TestPlacingABookmarkReachesTheVendorAndReportsAFailureHonestly(t *testing.T) {
	var sent milestone.Bookmark
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			sent = readBookmark(t, r)
			_, _ = w.Write([]byte(`{"id":"bm-99"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	tr, err := milestone.NewHTTPTransport(srv.URL, "t", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	w := tr.(milestone.BookmarkWriter)
	policy := clock.DefaultPolicy()

	decision, err := milestone.PlaceBookmark(context.Background(), aRequest(), w, policy)
	if err != nil {
		t.Fatalf("PlaceBookmark: %v", err)
	}
	if !decision.Placed || decision.Ref.ID != "bm-99" {
		t.Fatalf("the bookmark was not placed: %+v", decision)
	}
	if sent.IdempotencyKey == "" {
		t.Error("the placed bookmark carried no idempotency key")
	}
	if sent.Time.IsZero() {
		t.Error("the placed bookmark carried no time")
	}

	// A refusal never reaches the network at all.
	bad := aRequest()
	bad.SiteRef = ""
	decision, err = milestone.PlaceBookmark(context.Background(), bad, w, policy)
	if err == nil {
		t.Error("a refused bookmark returned no error")
	}
	if decision.Placed {
		t.Error("a refused bookmark reports itself as placed")
	}
	if decision.Refusal == "" {
		t.Error("a refusal with no reason; an operator cannot act on a silent no")
	}
}

// readBookmark pulls the interesting parts out of the body the test server
// received, so the test asserts on what crossed the wire rather than on what the
// client believed it sent.
func readBookmark(t *testing.T, r *http.Request) milestone.Bookmark {
	t.Helper()
	var body struct {
		Bookmarks []struct {
			IdempotencyKey string `json:"idempotencyKey"`
			DeviceRef      string `json:"deviceRef"`
			Time           string `json:"time"`
			Label          string `json:"label"`
		} `json:"bookmarks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("the write-back was not the expected JSON: %v", err)
	}
	if len(body.Bookmarks) != 1 {
		t.Fatalf("the write-back carried %d bookmarks, want exactly 1", len(body.Bookmarks))
	}
	b := body.Bookmarks[0]
	if got := r.Header.Get("Idempotency-Key"); got == "" || got != b.IdempotencyKey {
		t.Errorf("the Idempotency-Key header is %q and the body says %q; a vendor that reads only "+
			"one of them makes a retry unsafe", got, b.IdempotencyKey)
	}
	return milestone.Bookmark{
		IdempotencyKey: b.IdempotencyKey,
		SiteRef:        b.DeviceRef,
		Label:          b.Label,
		Time:           mustParseRFC3339(t, b.Time),
	}
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("the bookmark time %q is not RFC3339: %v", s, err)
	}
	return v
}
