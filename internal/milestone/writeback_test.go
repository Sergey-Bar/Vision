package milestone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// P1.1: a bookmark into the vendor's own recording, a clock reading, and a
// capability report - all on *optional* interfaces, so a fixture transport keeps
// working without growing a fake implementation of a system it stands in for.

func newLiveTransport(t *testing.T, h http.HandlerFunc) milestone.Transport {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tr, err := milestone.NewHTTPTransport(srv.URL, "test-token", 2*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	return tr
}

// mustWriter and mustProbe are how a real caller reaches the optional surface:
// the transport is the base interface and the additions are found by assertion.
// A test that reached them directly would be testing a different calling
// convention than the one every consumer uses.
func mustWriter(t *testing.T, tr milestone.Transport) milestone.BookmarkWriter {
	t.Helper()
	w, ok := tr.(milestone.BookmarkWriter)
	if !ok {
		t.Fatal("the live transport does not implement BookmarkWriter")
	}
	return w
}

func mustProbe(t *testing.T, tr milestone.Transport) milestone.ClockProbe {
	t.Helper()
	p, ok := tr.(milestone.ClockProbe)
	if !ok {
		t.Fatal("the live transport does not implement ClockProbe")
	}
	return p
}

// A device reference in the vendor's own REST form.
//
// `bookmarks-rest-v1.openapi.yaml:224-235` requires `devicePath{type,id}` and
// `events-rest-v1.reference.md:291` shows a source as `cameras/<uuid>`. The value
// used to be the display string "Perimeter North [CAM-014]", which the documented
// body has no field for.
const testDeviceRef = "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c"

func validBookmark() milestone.Bookmark {
	return milestone.Bookmark{
		IdempotencyKey: "inc_abc123",
		SiteRef:        testDeviceRef,
		Time:           time.Date(2026, 7, 1, 12, 0, 30, 0, time.UTC),
		Label:          "Unverified change",
		Description:    "The always-on detector reported a novelty score above the threshold.",
	}
}

// placedBookmarkAnswer is the vendor's documented answer to a task-less bookmark
// POST: a `bookmarkArrayBody` whose `reference` is the bookmark id
// (`bookmarks-rest-v1.openapi.yaml:380-386`, `:408-410`).
const placedBookmarkAnswer = `{"array":[{"header":"Unverified change","reference":"bm-1"}]}`

// TestTheLiveTransportImplementsTheOptionalSurface is the assertion that the
// seam is real: the three additions are discovered by type assertion, so a
// transport that does not have one is a replay and a transport that does is
// discoverable.
func TestTheLiveTransportImplementsTheOptionalSurface(t *testing.T) {
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {})

	if _, ok := tr.(milestone.BookmarkWriter); !ok {
		t.Error("the live transport does not implement BookmarkWriter")
	}
	if _, ok := tr.(milestone.ClockProbe); !ok {
		t.Error("the live transport does not implement ClockProbe")
	}
	caps := milestone.ProbeCapabilities(tr)
	if !caps.Has[milestone.CapabilityBookmarks] {
		t.Error("the probe does not report bookmarks for a transport that can write them")
	}
	if !caps.Has[milestone.CapabilityClockProbe] {
		t.Error("the probe does not report a clock probe for a transport that can sample one")
	}
}

// TestAReplayTransportIsNotBroken is the plan's reason for making these
// optional, asserted rather than assumed.
func TestAReplayTransportIsNotBroken(t *testing.T) {
	tr := milestone.NewReplayTransport(nil)
	var boxed milestone.Transport = tr
	if _, ok := boxed.(milestone.BookmarkWriter); ok {
		t.Error("a replay transport claims to place bookmarks; it has no vendor to place them in")
	}
	caps := milestone.ProbeCapabilities(boxed)
	if caps.Has[milestone.CapabilityBookmarks] {
		t.Error("the probe reports bookmarks for a transport that cannot place one")
	}
	// And a nil transport is answered, not dereferenced.
	if caps := milestone.ProbeCapabilities(nil); len(caps.Has) != 0 {
		t.Errorf("a nil transport probed as %+v", caps)
	}
}

// TestABookmarkIsAPointerNotACopyOfTheFootage: the whole of the "we do not
// duplicate an archive" decision is that what crosses this seam is a time and a
// label. If a byte of video crossed here, the statement would be false.
func TestABookmarkIsAPointerNotACopyOfTheFootage(t *testing.T) {
	var body map[string]any
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(placedBookmarkAnswer))
	})
	ref, err := mustWriter(t, tr).WriteBookmark(context.Background(), validBookmark())
	if err != nil {
		t.Fatalf("WriteBookmark: %v", err)
	}
	if ref.ID != "bm-1" {
		t.Errorf("bookmark id %q", ref.ID)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(strings.ToLower(string(raw)), "clip") ||
		strings.Contains(strings.ToLower(string(raw)), "data") {
		t.Errorf("the bookmark body carries something that looks like footage: %s", raw)
	}
	if !strings.Contains(string(raw), "2026-07-01T12:00:30Z") {
		t.Errorf("the bookmark time is not the second the operator should open at: %s", raw)
	}
}

// TestTheBookmarkBodyIsTheDocumentedOne. The body this adapter used to send
// carried `bookmarks`, `idempotencyKey`, `deviceRef`, `time` and `label` — five
// field names that appear nowhere in the published schema — and omitted `header`
// and `devicePath`, which the vendor requires.
//
// It is worth a test that names every one of them, because "the request shape
// looks about right" is not a thing anybody can check by eye, and because the
// previous body's problem was invisible precisely: it was a struct with json
// tags, and it compiled.
func TestTheBookmarkBodyIsTheDocumentedOne(t *testing.T) {
	var body map[string]any
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(placedBookmarkAnswer))
	})
	if _, err := mustWriter(t, tr).WriteBookmark(context.Background(), validBookmark()); err != nil {
		t.Fatalf("WriteBookmark: %v", err)
	}

	for _, required := range []string{"header", "devicePath", "timeTriggered"} {
		if _, ok := body[required]; !ok {
			t.Errorf("the bookmark body omits %q, which the vendor requires "+
				"(bookmarks-rest-v1.openapi.yaml:424-457). Body: %v", required, body)
		}
	}
	// Every field the documented schema declares, and nothing it does not.
	declared := map[string]bool{"header": true, "description": true, "timeBegin": true,
		"timeEnd": true, "timeTriggered": true, "reference": true, "devicePath": true}
	for field := range body {
		if !declared[field] {
			t.Errorf("the bookmark body sends %q, which the published schema does not declare", field)
		}
	}
	// The device is a {type,id} pair, not a string and not a display name.
	device, ok := body["devicePath"].(map[string]any)
	if !ok {
		t.Fatalf("devicePath is %T, not an object: %v", body["devicePath"], body["devicePath"])
	}
	if device["type"] != "cameras" || device["id"] != "e028f597-c64b-4f27-bb8d-08f5cd768e1c" {
		t.Errorf("devicePath is %v; this API names a device as {type,id}", device)
	}
	// And the wrapper array is gone: the documented body is one bookmark.
	if _, present := body["bookmarks"]; present {
		t.Error(`the body still wraps the bookmark in "bookmarks"; the documented body is a single bookmark`)
	}
}

// TestTheWriteSendsNoTaskParameter is the decision, asserted.
//
// `bookmarks-rest-v1.openapi.yaml:142-161` gives `newReference` a meaning that
// sounds like the product's ("create a new reference and trigger rules") and then
// says the thing that decides it: "The newReference task does not create the
// bookmark in the database." A task-less POST creates the bookmark and does not
// trigger the vendor's rules, which is what "a pointer the operator can follow"
// means — and triggering a customer's rules is a capability activation, which
// AGENTS.md reserves to a human.
func TestTheWriteSendsNoTaskParameter(t *testing.T) {
	var got string
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("task")
		_, _ = w.Write([]byte(placedBookmarkAnswer))
	})
	if _, err := mustWriter(t, tr).WriteBookmark(context.Background(), validBookmark()); err != nil {
		t.Fatalf("WriteBookmark: %v", err)
	}
	if got != "" {
		t.Errorf("the write sent task=%q. No task creates the bookmark without triggering the "+
			"vendor's rules; `newReference` triggers rules and creates no bookmark", got)
	}
}

// TestADisplayNameIsNotADeviceReference. `devicePath` is required, and a
// required field carrying a value the vendor cannot interpret is a 400 the
// caller has to reverse-engineer. Refusing here names the cause before any bytes
// leave.
func TestADisplayNameIsNotADeviceReference(t *testing.T) {
	var reached atomic.Bool
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		_, _ = w.Write([]byte(placedBookmarkAnswer))
	})
	for _, ref := range []string{
		"Perimeter North [CAM-014]",
		"cameras/",
		"microphones/014",
		"cameras/not-a-uuid-but-a-path",
		"detectors/cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
	} {
		b := validBookmark()
		b.SiteRef = ref
		if _, err := mustWriter(t, tr).WriteBookmark(context.Background(), b); err == nil {
			t.Errorf("the device reference %q was accepted", ref)
		}
	}
	if reached.Load() {
		t.Error("an unresolvable device reference reached the vendor anyway")
	}
}

// TestA200WithNoIdIsNotASuccess. "The call returned 200" and "a bookmark exists"
// are different claims, and a bookmark that silently did not land is an operator
// who follows the incident and finds nothing.
func TestA200WithNoIdIsNotASuccess(t *testing.T) {
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	_, err := mustWriter(t, tr).WriteBookmark(context.Background(), validBookmark())
	if err == nil {
		t.Fatal("a 200 with no bookmark id was reported as a placed bookmark")
	}
	if !strings.Contains(err.Error(), "will find nothing") {
		t.Errorf("the refusal %q does not say what the operator will experience", err)
	}
}

// TestTheIdempotencyKeyTravelsInAHeaderAsWellAsTheBody: a vendor that honours
// A vendor that honours an idempotency header and ignores everything else is
// exactly the case where a retry creates a second bookmark, and a duplicate
// bookmark is an operator-visible defect.
//
// It is now the ONLY place the key can travel: the documented body
// (`bookmarks-rest-v1.openapi.yaml:424-457`) declares no idempotency field, so the
// header is not "as well as" anything. See docs/vendor/milestone/route-audit.md
// §2.5 for what that leaves unmitigated.
func TestTheIdempotencyKeyTravelsInAHeader(t *testing.T) {
	var gotKey, gotPath string
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(placedBookmarkAnswer))
	})
	if _, err := mustWriter(t, tr).WriteBookmark(context.Background(), validBookmark()); err != nil {
		t.Fatalf("WriteBookmark: %v", err)
	}
	if gotKey != "inc_abc123" {
		t.Errorf("the Idempotency-Key header was %q", gotKey)
	}
	if gotPath != "/api/rest/v1/bookmarks" {
		t.Errorf("the write went to %q; the documented path is /api/rest/v1/bookmarks", gotPath)
	}
}

// TestTwoConcurrentWritesDoNotShareAKey: the key travels as a parameter, so two
// incidents written back at once cannot cross their keys. A field on the
// transport would make this a data race that only shows up under load.
func TestTwoConcurrentWritesDoNotShareAKey(t *testing.T) {
	var first, second atomic.Value
	var n atomic.Int32
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			first.Store(r.Header.Get("Idempotency-Key"))
		case 2:
			second.Store(r.Header.Get("Idempotency-Key"))
		}
		_, _ = w.Write([]byte(`{"array":[{"reference":"bm"}]}`))
	})
	a := validBookmark()
	b := validBookmark()
	b.IdempotencyKey = "inc_zzz999"
	go func() { _, _ = mustWriter(t, tr).WriteBookmark(context.Background(), a) }()
	go func() { _, _ = mustWriter(t, tr).WriteBookmark(context.Background(), b) }()
	for i := 0; i < 100 && (first.Load() == nil || second.Load() == nil); i++ {
		time.Sleep(time.Millisecond)
	}
	got := map[string]bool{
		first.Load().(string):  true,
		second.Load().(string): true,
	}
	if !got["inc_abc123"] || !got["inc_zzz999"] {
		t.Errorf("the two writes carried %v; each must carry its own key", got)
	}
}

// TestAWriteIsRefusedBeforeBytesLeave when there is no token, so the recorded
// cause is "not configured" rather than the vendor's 401 arriving later.
func TestAWriteIsRefusedBeforeBytesLeave(t *testing.T) {
	var reached atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
	}))
	defer srv.Close()
	tr, err := milestone.NewHTTPTransport(srv.URL, "  ", 2*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPTransport: %v", err)
	}
	_, err = tr.(milestone.BookmarkWriter).WriteBookmark(context.Background(), validBookmark())
	if err == nil {
		t.Fatal("a write with no token succeeded")
	}
	if !strings.Contains(err.Error(), "no Milestone token") {
		t.Errorf("the refusal %q does not name the configuration problem", err)
	}
	if reached.Load() {
		t.Error("the request was sent anyway")
	}
}

// TestABookmarkWithoutItsEssentialsIsRefused: each of the three is a different
// operator-visible defect and each is caught at the call site rather than by the
// vendor.
func TestABookmarkWithoutItsEssentialsIsRefused(t *testing.T) {
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an invalid bookmark reached the vendor")
	})
	for name, mutate := range map[string]func(*milestone.Bookmark){
		"no key":    func(b *milestone.Bookmark) { b.IdempotencyKey = "" },
		"no device": func(b *milestone.Bookmark) { b.SiteRef = "" },
		"no time":   func(b *milestone.Bookmark) { b.Time = time.Time{} },
	} {
		b := validBookmark()
		mutate(&b)
		if _, err := mustWriter(t, tr).WriteBookmark(context.Background(), b); err == nil {
			t.Errorf("a bookmark with %s was accepted", name)
		}
	}
}

// TestTheClockIsSampledWithItsUncertainty: a reading with no round trip attached
// cannot be compared with a drift threshold, and comparing it anyway biases the
// answer in the direction of the link being up.
func TestTheClockIsSampledWithItsUncertainty(t *testing.T) {
	// The handler takes a measurable amount of time on purpose. Asserting that a
	// loopback round trip is non-zero is a flaky assertion, because the platform
	// clock's resolution is coarse enough on Windows that a sub-tick call reads
	// back as exactly zero - and a test that fails once in fifty runs teaches
	// people to re-run rather than to read. The arithmetic the round trip feeds
	// is pinned deterministically in TestSamplingDriftDiscountsHalfTheRoundTrip
	// with a fake probe; this test only has to prove the field is wired.
	const handlerDelay = 5 * time.Millisecond
	tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(handlerDelay)
		_, _ = w.Write([]byte(`{"dateTime":"2026-07-01T12:00:00.000Z"}`))
	})
	probe := mustProbe(t, tr)
	reading, err := probe.ServerTime(context.Background())
	if err != nil {
		t.Fatalf("ServerTime: %v", err)
	}
	want := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if !reading.At.Equal(want) {
		t.Errorf("the clock read %v, want %v", reading.At, want)
	}
	if reading.RoundTrip < handlerDelay {
		t.Errorf("the reading reports a %v round trip across a handler that slept %v; the uncertainty "+
			"is not being measured, and a drift computed from it is a guess", reading.RoundTrip, handlerDelay)
	}
}

// TestAnUnreadableClockShapeIsNamedRatherThanZero: a shape this build does not
// know must be a probe failure, not a zero time. A zero clock is worse than no
// clock, because it looks like a measurement of 1970.
func TestAnUnreadableClockShapeIsNamedRatherThanZero(t *testing.T) {
	for _, body := range []string{`{}`, `{"dateTime":""}`, `{"dateTime":"not a time"}`, `not json`} {
		tr := newLiveTransport(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		})
		reading, err := mustProbe(t, tr).ServerTime(context.Background())
		if err == nil {
			t.Errorf("the body %q produced a reading of %v", body, reading.At)
			continue
		}
		if !strings.Contains(err.Error(), "clock probe") {
			t.Errorf("the body %q produced %q, which does not name the clock probe", body, err)
		}
	}
}

// TestTheClockRouteStaysUnverified: the plan's P0.5 is the probe that promotes
// these routes, and until one runs against a live server they are a hypothesis.
// A passing unit test is not the probe — and neither is a published document,
// which is what refuted these paths without being able to clear them.
func TestTheClockRouteStaysUnverified(t *testing.T) {
	for _, r := range milestone.Routes {
		if r.Path == "/api/rest/v1/System/Time" && !r.Unverified {
			t.Error("the clock route was promoted to verified by a unit test; only a live probe can do that")
		}
		if r.Path == "/api/rest/v1/bookmarks" && !r.Unverified {
			t.Error("the bookmark route was promoted to verified by a unit test")
		}
	}
	if !milestone.UnverifiedRoute(milestone.APIPrefix + "/System/Time") {
		t.Error("the clock route is no longer in the unverified set, and no probe has run")
	}
}

// TestDriftIsBandedRatherThanABareNumber: the plan says a camera in the severe
// band is refused with a named reason, and a band with no names is not a band.
func TestDriftIsBandedRatherThanABareNumber(t *testing.T) {
	for offset, want := range map[time.Duration]string{
		0:               "ok",
		time.Second:     "ok",
		3 * time.Second: "warn",
		time.Minute:     "severe",
	} {
		got := milestone.Drift{Offset: offset}.Band()
		if got != want {
			t.Errorf("a drift of %v is %q, want %q", offset, got, want)
		}
	}
}

// TestSamplingDriftDiscountsHalfTheRoundTrip pins the *sign* of the correction,
// which is the part that is easy to get backwards.
//
// The shape of a probe: we send at S, the vendor samples at S+δ, and we receive
// at S+rtt. `ours()` is called after the call, so it reads S+rtt. The vendor's
// answer is its clock at S+δ. Drift is the vendor's clock at S minus ours at S,
// which is (S+δ) - (S+rtt-rtt) once the sampling delay is estimated as half the
// round trip - so the correction *adds* rtt/2 to the reading and subtracts our
// later instant. Getting the sign wrong reports a healthy link as drifting by
// half its own latency, and pushes a good site into the warn band.
func TestSamplingDriftDiscountsHalfTheRoundTrip(t *testing.T) {
	const rtt = 400 * time.Millisecond
	send := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	receive := send.Add(rtt)
	halfRTT := rtt / 2

	cases := []struct {
		name       string
		vendorLead time.Duration // how far the vendor's clock runs ahead of ours
		want       time.Duration
	}{
		{"in step", 0, 0},
		{"five seconds fast", 5 * time.Second, 5 * time.Second},
		{"one second slow", -time.Second, -time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The vendor sampled halfway through the round trip and its clock
			// reads that instant plus its own lead.
			probe := fakeProbe{at: send.Add(halfRTT).Add(tc.vendorLead), rtt: rtt}
			drift, err := milestone.SampleDrift(context.Background(), probe, func() time.Time { return receive })
			if err != nil {
				t.Fatalf("SampleDrift: %v", err)
			}
			if d := drift.Offset - tc.want; d > time.Millisecond || d < -time.Millisecond {
				t.Errorf("drift %v, want %v (off by %v)", drift.Offset, tc.want, d)
			}
		})
	}

	if _, err := milestone.SampleDrift(context.Background(), nil, nil); err == nil {
		t.Error("a nil clock probe was accepted; a window selected without it is a guess")
	}
}

type fakeProbe struct {
	at  time.Time
	rtt time.Duration
	err error
}

func (f fakeProbe) ServerTime(context.Context) (milestone.ServerTime, error) {
	if f.err != nil {
		return milestone.ServerTime{}, f.err
	}
	return milestone.ServerTime{At: f.at, RoundTrip: f.rtt}, nil
}
