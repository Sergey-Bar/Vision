package labelui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// fakeStore is an in-memory [Store], so the whole page can be driven without a
// database, a model, or a session — the three things a reviewer does not have and
// the three a test should not need.
type fakeStore struct {
	rows    []store.BoxObservation
	labels  []store.Label
	objects []store.LabelObject
	// putErr is returned by PutLabel, for the refusal tests.
	putErr error
}

func (f *fakeStore) BoxObservations(context.Context, string) ([]store.BoxObservation, error) {
	return f.rows, nil
}

func (f *fakeStore) PutLabel(_ context.Context, l store.Label) error {
	if f.putErr != nil {
		return f.putErr
	}
	for i := range f.labels {
		if f.labels[i].FrameSeq == l.FrameSeq && f.labels[i].BoxKey == l.BoxKey {
			f.labels[i] = l
			return nil
		}
	}
	f.labels = append(f.labels, l)
	return nil
}

func (f *fakeStore) PutLabelObject(_ context.Context, o store.LabelObject) error {
	f.objects = append(f.objects, o)
	return nil
}

func (f *fakeStore) Labels(context.Context, string) ([]store.Label, error) { return f.labels, nil }

func (f *fakeStore) LabelObjects(context.Context, string) ([]store.LabelObject, error) {
	return f.objects, nil
}

func (f *fakeStore) LabelTally(context.Context, string) (store.LabelCounts, error) {
	var c store.LabelCounts
	for _, l := range f.labels {
		c.Marked++
		switch l.Verdict {
		case store.LabelAccept:
			c.Accepted++
		case store.LabelReject:
			c.Rejected++
		}
	}
	return c, nil
}

func (f *fakeStore) ProposedBoxCount(context.Context, string) (int, error) {
	// The same filter the real store applies: MEASURED and TRACKED only. A frame
	// the pipeline never asked about is not offered to a reviewer, because asking
	// somebody to confirm a silence produces a verdict about nothing.
	n := 0
	for _, r := range f.rows {
		if r.Measured && r.TrackID >= 0 {
			n++
		}
	}
	return n, nil
}

func seededStore() *fakeStore {
	// Track IDs are REAL in this fixture, and that is load-bearing rather than
	// incidental: the store's ProposedBoxCount counts rows with `track_id >= 0`,
	// because a box the tracker never claimed is not something a reviewer is being
	// asked about. A fixture with -1 everywhere would report an UNMARKED count of
	// zero over a run with boxes in it — the same silent zero the runner's box log
	// produced before Phase 4 wired the real track IDs through.
	return &fakeStore{rows: []store.BoxObservation{
		{RunID: "r1", FrameSeq: 10, TrackID: 7, Class: "car", Score: 0.9,
			Top: 100, Left: 200, Bottom: 300, Right: 400, Source: "detector", Measured: true},
		{RunID: "r1", FrameSeq: 10, TrackID: 8, Class: "person", Score: 0.4,
			Top: 500, Left: 100, Bottom: 700, Right: 160, Source: "detector", Measured: true},
		{RunID: "r1", FrameSeq: 11, TrackID: -1, Source: "detector", Measured: false},
		// A MEASURED box the tracker never claimed: not offered to a reviewer, and
		// present so the filter is exercised rather than assumed.
		{RunID: "r1", FrameSeq: 12, TrackID: -1, Class: "bird", Score: 0.3,
			Top: 10, Left: 10, Bottom: 40, Right: 40, Source: "detector", Measured: true},
	}}
}

// TestTheRefusalsComeBeforeThePage, because a page that opens over a run with
// nothing to mark will read as a broken tool rather than as a run that recorded
// nothing.
func TestTheRefusalsComeBeforeThePage(t *testing.T) {
	ctx := context.Background()

	if err := Serve(ctx, Options{Addr: "127.0.0.1:0", Store: seededStore()}); err == nil ||
		!strings.Contains(err.Error(), "no run") {
		t.Fatalf("a session with no run produced %v", err)
	}
	if err := Serve(ctx, Options{RunID: "r1", Addr: "127.0.0.1:0"}); err == nil ||
		!strings.Contains(err.Error(), "no store") {
		t.Fatalf("a session with no store produced %v", err)
	}
	// A reviewer name is required, and the reason is on the error rather than in a
	// comment: every verdict is recorded against a name.
	err := Serve(ctx, Options{RunID: "r1", Store: seededStore(), Addr: "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "reviewer name") {
		t.Fatalf("a session with no reviewer produced %v", err)
	}
}

// TestItRefusesToServeOffLoopback is the security property, and it is structural
// rather than advisory.
//
// The page serves the footage and the reviewer's judgements about it. Neither
// belongs on an interface somebody else can reach, and a tool that binds 0.0.0.0 by
// default because that was easier is how footage of somebody's driveway ends up on
// a network somebody else is on.
func TestItRefusesToServeOffLoopback(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:8096",
		"192.168.1.10:8096",
		":8096", // no host means every interface
		"example.com:8096",
	} {
		err := checkLoopback(addr)
		if err == nil {
			t.Errorf("%q was accepted; this page serves footage and a reviewer's judgements", addr)
			continue
		}
		if !strings.Contains(err.Error(), "refusing to serve") &&
			!strings.Contains(err.Error(), "no host") &&
			!strings.Contains(err.Error(), "not an IP") {
			t.Errorf("%q was refused for an unclear reason: %v", addr, err)
		}
	}
	for _, addr := range []string{"127.0.0.1:8096", "localhost:8096", "[::1]:8096"} {
		if err := checkLoopback(addr); err != nil {
			t.Errorf("%q was refused and it is loopback: %v", addr, err)
		}
	}
}

// TestAFrameThePipelineSkippedIsShownAsSuch is the absence rule, on the page.
//
// The box log stores a row with measured=0 for every frame the pipeline never
// asked about. Without it, "the detector found nothing" and "nobody asked" are the
// same row, and a reviewer marking an unasked frame produces a verdict about
// nothing — which is exactly the confusion this store was built to prevent.
func TestAFrameThePipelineSkippedIsShownAsSuch(t *testing.T) {
	sess, err := Load(context.Background(), seededStore(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Frames) != 3 {
		t.Fatalf("frames = %d, want 3 — the two with boxes, the skipped one, and the one with "+
			"only an untracked box", len(sess.Frames))
	}
	bySeq := map[int64]Frame{}
	for _, f := range sess.Frames {
		bySeq[f.Seq] = f
	}
	if !bySeq[10].Measured {
		t.Error("the frame with boxes is not marked measured")
	}
	f11 := bySeq[11]
	if f11.Measured {
		t.Error("a frame the pipeline never asked about is marked measured")
	}
	if !f11.FrameSkip {
		t.Error("a frame the pipeline never asked about is not flagged as skipped")
	}
	// And it carries no boxes to judge, because it had none.
	if len(f11.Boxes) != 0 {
		t.Errorf("a skipped frame carries %d boxes", len(f11.Boxes))
	}
}

// TestABoxNoTrackClaimedIsNotOfferedForJudgement is the filter that the fixture
// above exercises, and it is here because getting it wrong leaves the false-
// positive rate NOT RUN forever with nothing to show for it.
//
// `store.ProposedBoxCount` counts rows with `track_id >= 0`. The runner's box log
// wrote -1 on every row for two phases, which meant the count was zero for every
// real run, which meant this tool had nothing to show a reviewer — and the page
// said "nothing to mark" rather than "this run recorded no trackable boxes".
func TestABoxNoTrackClaimedIsNotOfferedForJudgement(t *testing.T) {
	f := seededStore()
	n, err := f.ProposedBoxCount(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("proposed = %d, want 2 — the two boxes with a real track id, and not the "+
			"skipped frame's row or the untracked bird", n)
	}
	sess, err := Load(context.Background(), f, "r1")
	if err != nil {
		t.Fatal(err)
	}
	// Three frames: two with tracked boxes, one skipped, one with only an
	// untracked box.
	if len(sess.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(sess.Frames))
	}
	bySeq := map[int64]Frame{}
	for _, fr := range sess.Frames {
		bySeq[fr.Seq] = fr
	}
	// The untracked box is still SHOWN on its frame — a reviewer looking at a
	// picture should see everything the detector drew — but it is not in the
	// denominator, because there is no track for it to be a part of.
	if len(bySeq[12].Boxes) != 1 {
		t.Errorf("the untracked box is not shown; a reviewer looking at the picture should see " +
			"everything the detector drew")
	}
	if sess.Proposed != 2 {
		t.Errorf("proposed = %d; the denominator excludes the untracked box", sess.Proposed)
	}
}

// TestUnmarkedIsAlwaysVisible is the thing that keeps a session from being
// abandoned half way and reported as finished.
func TestUnmarkedIsAlwaysVisible(t *testing.T) {
	sess, err := Load(context.Background(), seededStore(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Unmarked != 2 {
		t.Fatalf("unmarked = %d, want 2", sess.Unmarked)
	}
	// Mark one and it drops by one.
	if err := Judge(context.Background(), seededStore(), store.Label{
		RunID: "r1", FrameSeq: 10, BoxKey: BoxKey(sess.Frames[0].Boxes[0]),
		Verdict: store.LabelAccept, By: "ann",
	}); err != nil {
		t.Fatal(err)
	}
	st := seededStore()
	if err := Judge(context.Background(), st, store.Label{
		RunID: "r1", FrameSeq: 10, BoxKey: "100,200,300,400",
		Verdict: store.LabelAccept, By: "ann",
	}); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(context.Background(), st, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Unmarked != 1 {
		t.Fatalf("unmarked = %d after one mark, want 1", s2.Unmarked)
	}
	if s2.Accept != 1 {
		t.Fatalf("accepted = %d, want 1", s2.Accept)
	}
}

// TestBoxKeyIsStableAcrossAReload, because marking the same box twice must be an
// UPDATE of one judgement and not two rows that disagree.
func TestBoxKeyIsStableAcrossAReload(t *testing.T) {
	o := store.BoxObservation{Top: 349, Left: 470, Bottom: 780, Right: 560}
	if got, want := BoxKey(o), "349,470,780,560"; got != want {
		t.Fatalf("BoxKey = %q, want %q", got, want)
	}
	// The page computes the key the same way, from the same truncated thousandths
	// the store writes. Two implementations of a key is two implementations of a
	// row identity.
	if !strings.Contains(pageHTML, "b.top + \",\" + b.left") {
		t.Error("the page does not build the key the same way Go does")
	}
}

// TestJudgingRefusesWhatItCannotCount is the closed vocabulary, and each refusal
// names its consequence: a false-positive rate needs a denominator of DECIDABLE
// verdicts, and every one of these widens it silently.
func TestJudgingRefusesWhatItCannotCount(t *testing.T) {
	ctx := context.Background()
	st := seededStore()
	cases := []struct {
		name string
		l    store.Label
		want string
	}{
		{"no verdict", store.Label{BoxKey: "k", By: "ann"}, "not a verdict"},
		{"maybe", store.Label{BoxKey: "k", By: "ann", Verdict: "maybe"}, "not a verdict"},
		{"no name", store.Label{BoxKey: "k", Verdict: store.LabelAccept}, "needs a name"},
		{"no box", store.Label{By: "ann", Verdict: store.LabelAccept}, "needs the box"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Judge(ctx, st, tc.l)
			if err == nil {
				t.Fatalf("a label with %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not mention %q: %v", tc.want, err)
			}
		})
	}
	if len(st.labels) != 0 {
		t.Fatalf("a refused label was written anyway: %+v", st.labels)
	}
}

// TestDrawingRefusesWhatItCannotCount is the same for the recall half, and the
// class requirement is the interesting one: an unnamed box is a rectangle, and a
// reviewer cannot tell a mistake from a marking of something nobody has a word for.
func TestDrawingRefusesWhatItCannotCount(t *testing.T) {
	ctx := context.Background()
	st := seededStore()
	if err := Draw(ctx, st, store.LabelObject{FrameSeq: 1, Class: "car", Top: 1, Left: 1, Bottom: 9, Right: 9}); err == nil ||
		!strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("an unnamed drawn object was accepted: %v", err)
	}
	if err := Draw(ctx, st, store.LabelObject{FrameSeq: 1, By: "ann", Top: 1, Left: 1, Bottom: 9, Right: 9}); err == nil ||
		!strings.Contains(err.Error(), "needs a class") {
		t.Fatalf("a classless drawn object was accepted: %v", err)
	}
	if err := Draw(ctx, st, store.LabelObject{FrameSeq: 1, By: "ann", Class: "car",
		Top: 1, Left: 1, Bottom: 9, Right: 9}); err != nil {
		t.Fatalf("a real drawn object was refused: %v", err)
	}
}

// TestThePageSaysWhatItNeedsAndRefusesWhatItCannot is the HTTP surface, exercised
// through a real handler so the routes are covered rather than assumed.
func TestThePageSaysWhatItNeedsAndRefusesWhatItCannot(t *testing.T) {
	st := seededStore()
	opt := Options{RunID: "r1", Store: st, By: "ann", Addr: "127.0.0.1:0"}
	h := routes(opt)

	// The page itself, with the session inlined so the first render needs no fetch.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"BOX MARKING", "window.__SESSION__", "/api/label"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the page is served without nosniff")
	}

	// An unknown path is a 404 and not the page, because serving the marking UI at
	// every path makes a typo look like it worked.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}

	// A verdict.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/label",
		strings.NewReader(`{"frame_seq":10,"box_key":"100,200,300,400","verdict":"accept"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/label = %d: %s", rec.Code, rec.Body.String())
	}
	if len(st.labels) != 1 || st.labels[0].Verdict != store.LabelAccept {
		t.Fatalf("the verdict was not recorded: %+v", st.labels)
	}

	// A bad verdict is a 400 with the sentence, not a 500 and not a silent write.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/label",
		strings.NewReader(`{"frame_seq":10,"box_key":"k","verdict":"perhaps"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad verdict = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not a verdict") {
		t.Errorf("the 400 does not say why: %s", rec.Body.String())
	}

	// A GET on a POST endpoint is a 405 and not a 404, because they mean different
	// things to whoever is reading the log.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/label", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/label = %d, want 405", rec.Code)
	}

	// A body that is not JSON is a 400.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/label", strings.NewReader("<html>"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a non-JSON body = %d, want 400", rec.Code)
	}
}

// TestTheStoreErrorReachesTheReviewer is the failure path. A marking session that
// silently drops a verdict is worse than one that refuses it, because the reviewer
// will move on believing they marked it.
func TestTheStoreErrorReachesTheReviewer(t *testing.T) {
	st := seededStore()
	st.putErr = errors.New("the database is locked")
	h := routes(Options{RunID: "r1", Store: st, By: "ann", Addr: "127.0.0.1:0"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/label",
		strings.NewReader(`{"frame_seq":10,"box_key":"k","verdict":"accept"}`))
	h.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Fatalf("a store failure was reported as %d; a verdict that was not saved must not look saved",
			rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "locked") {
		t.Errorf("the error does not reach the reviewer: %s", rec.Body.String())
	}
}

// TestThePageCarriesNoRemoteAnything, because a marking tool that reaches out to a
// CDN is a tool that stops working the moment the reviewer's network does — and
// that is a half-finished session nobody can resume.
func TestThePageCarriesNoRemoteAnything(t *testing.T) {
	for _, bad := range []string{"http://", "https://", "//cdn", "src=", "@import"} {
		if strings.Contains(pageHTML, bad) {
			t.Errorf("the page contains %q; it must work with no network at all", bad)
		}
	}
	// And it declares its own encoding, because a page served without one is decoded
	// by whatever the reviewer's locale says, and the box coordinates are numbers.
	if !strings.Contains(pageHTML, `<meta charset="utf-8">`) {
		t.Error("the page declares no charset")
	}
	if !strings.Contains(pageHTML, `lang="en"`) {
		t.Error("the page declares no language")
	}
}

// TestLoadingAnEmptyRunSaysSoRatherThanRenderingAnEmptyPage, because a reviewer
// who opens a page with nothing in it concludes the tool is broken.
func TestLoadingAnEmptyRunSaysSoRatherThanRenderingAnEmptyPage(t *testing.T) {
	sess, err := Load(context.Background(), &fakeStore{}, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Frames) != 0 {
		t.Fatalf("frames = %d over an empty store", len(sess.Frames))
	}
	if sess.Unmarked != 0 {
		t.Errorf("unmarked = %d over an empty store", sess.Unmarked)
	}
	if sess.Proposed != 0 {
		t.Errorf("proposed = %d over an empty store", sess.Proposed)
	}
}

// unusedFmtGuard keeps the fmt import honest across the refactors above.
var _ = fmt.Sprintf
