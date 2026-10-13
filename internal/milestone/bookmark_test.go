package milestone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/milestone"
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
		IncidentID: "inc_abc123",
		// A device reference in the vendor's own REST form, which is what this API
		// requires: `bookmarks-rest-v1.openapi.yaml:224-235` wants
		// `devicePath{type,id}`, and `events-rest-v1.reference.md:291` shows the id
		// in a source as `cameras/<uuid>`. It used to be the display string
		// "Perimeter North [CAM-014]", which the documented body has no place for.
		SiteRef:     "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
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
			_, _ = w.Write([]byte(`{"array":[{"reference":"bm-99"}]}`))
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
		t.Error("the placed bookmark carried no idempotency key. The documented body has no field " +
			"for one, so the header is the only place it can travel")
	}
	if sent.SiteRef != aRequest().SiteRef {
		t.Errorf("the device reached the vendor as %q, want the request's own %q",
			sent.SiteRef, aRequest().SiteRef)
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
//
// It decodes the documented `newBookmarkBody`, which has no idempotency field, so
// the key is read from the header and that is reported as the header value. The
// previous version asserted the header and the body carried the same key, which
// was a coherent check against a body shape that does not exist.
func readBookmark(t *testing.T, r *http.Request) milestone.Bookmark {
	t.Helper()
	var body struct {
		Header     string `json:"header"`
		DevicePath struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"devicePath"`
		TimeTriggered string `json:"timeTriggered"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("the write-back was not the expected JSON: %v", err)
	}
	if body.Header == "" || body.DevicePath.ID == "" || body.TimeTriggered == "" {
		t.Errorf("the write-back omitted a required field: %+v", body)
	}
	return milestone.Bookmark{
		// The header is the only place this key can travel: neither published
		// document declares it, so there is nowhere in the body to put it. See
		// docs/vendor/milestone/route-audit.md §2.5.
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		SiteRef:        body.DevicePath.Type + "/" + body.DevicePath.ID,
		Label:          body.Header,
		Time:           mustParseRFC3339(t, body.TimeTriggered),
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
