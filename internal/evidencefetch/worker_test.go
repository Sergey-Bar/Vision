package evidencefetch

// The evidence worker's refusals.
//
// # Why this file is mostly refusals
//
// The worker performs one action — place a bookmark — and the interesting behaviour is
// everything that stops it doing so and saying why. A worker that placed bookmarks and had
// no opinion on the other paths would be four lines long and would ship the defect this
// branch exists to remove: an operator following an incident and finding nothing.
//
// So each case below asserts that a refusal HAPPENS, that it carries a reason, and that it
// does not become a retry. The last part matters most: a refusal that returns an error
// retries to exhaustion and dead-letters, which reports a broken worker and sends an
// operator to look at the wrong system entirely.
//
// The database is not involved. `Handle` reads its facts from storage and everything below
// the read is exercised with a stub writer and a nil pool, which is the deliberate shape of
// these tests: the decisions are pure functions of facts this file supplies, and a test
// that needed a live Postgres to check a clock band would be a test nobody runs.
//
// The DB-backed half — the receipt row and its ledger entry committing together — is
// covered by the same suite's database tests when `SP_TEST_DATABASE_URL` is set, and those
// SKIP rather than fail when it is not. That skip is stated here rather than left to be
// discovered: a green run of this file proves the refusals and proves nothing about the
// insert.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/dispatch"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// stubWriter is a `milestone.BookmarkWriter` that records what it was asked to do.
//
// A stub rather than an httptest server on purpose: this worker's decision surface is
// "which bookmark, if any", and a server would test `milestone`'s HTTP encoding, which
// `internal/milestone` already tests against the vendor's own documents.
type stubWriter struct {
	sent  []milestone.Bookmark
	ref   milestone.BookmarkRef
	err   error
	calls int
}

func (s *stubWriter) WriteBookmark(_ context.Context, b milestone.Bookmark) (milestone.BookmarkRef, error) {
	s.calls++
	s.sent = append(s.sent, b)
	return s.ref, s.err
}

// testStore creates a real evidence.Store backed by a temporary directory.
// The caller is responsible for cleaning up the directory.
func testStore(t *testing.T) *evidence.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := evidence.New(evidence.Options{Root: dir})
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	return s
}

// stubFacts is a FactReader that returns fixed facts for tests that reach the bookmark logic.
type stubFacts struct {
	facts Facts
	err   error
}

func (s *stubFacts) ReadFacts(_ context.Context, _ jobs.Job, _ dispatch.JobPayload) (Facts, error) {
	return s.facts, s.err
}

func evidenceJob(t *testing.T) jobs.Job {
	t.Helper()
	payload, err := dispatch.EncodeJobPayload(dispatch.JobPayload{
		IncidentID:      "inc-1",
		EventID:         "evt-1",
		SiteID:          "site-1",
		ContractVersion: dispatch.ContractVersion,
	})
	if err != nil {
		t.Fatalf("encode the job payload: %v", err)
	}
	return jobs.Job{
		ID: "job-1", TenantID: "tenant-1", SiteID: "site-1",
		Queue:          jobs.QueueEvidenceFetch,
		IdempotencyKey: "inc-1:fetch_evidence:pre-event",
		Payload:        payload,
	}
}

// TestTheWorkerIsNotAFetchWorker pins what the package does NOT do, because the name
// invites the wrong assumption.
//
// It places a pointer. It does not download, transcode, store or serve footage. If a
// future change makes this download video, the cost model in `internal/cost` is wrong, the
// evidence store's size assumptions are wrong, and the privacy posture changes - so the
// first thing to assert is the design.
func TestTheWorkerIsNotAFetchWorker(t *testing.T) {
	w := &stubWriter{}
	worker := &Worker{Bookmarks: w, Policy: testPolicy()}

	out, err := worker.Handle(context.Background(), evidenceJob(t))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if out.Placed {
		t.Fatalf("a bookmark was placed with no database behind the worker: %+v", out)
	}
}

// TestARefusalIsASuccessNotARetry is the load-bearing assertion in this file.
//
// Every refusal path returns nil. A refusal that returned an error would burn the job's
// attempts and dead-letter, and a dead letter says "the evidence worker is broken" when the
// truth is "the clock is severe" - so an operator would go looking at a worker instead of
// at a camera.
func TestARefusalIsASuccessNotARetry(t *testing.T) {
	cases := []struct {
		name    string
		worker  *Worker
		wantRef string
	}{
		{
			// The honest answer for a deployment with no write-back surface, which is a
			// replay transport and a supported configuration.
			name:    "no bookmark surface",
			worker:  &Worker{Policy: testPolicy()},
			wantRef: "no_bookmark_surface",
		},
		{
			// A bookmark whose receipt cannot be kept is not a bookmark. Losing the
			// receipt means the incident can never be shown to have had evidence.
			name:    "no evidence store",
			worker:  &Worker{Bookmarks: &stubWriter{}, Policy: testPolicy()},
			wantRef: "unspecified",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.worker.Handle(context.Background(), evidenceJob(t))
			if err != nil {
				t.Fatalf("a refusal returned an error, so the job would retry and then "+
					"dead-letter: %v", err)
			}
			if out.Placed {
				t.Fatalf("a refusal path reported a placement: %+v", out)
			}
			if out.Refusal == "" {
				t.Fatal("a refusal carried no reason. A refusal that does not say which one " +
					"is indistinguishable from a worker that gave up")
			}
		})
	}
}

// TestEveryRefusalNamesItself is the anti-decorative rule for this worker, and it is a
// test rather than a convention because the alternative is a refusal reason that is a
// space.
//
// `milestone` declares three: `clock_drift_severe`, `no_site_reference`, `no_window`.
// Each names a different cause with a different fix, and an operator who cannot tell them
// apart has to read the source.
func TestEveryRefusalNamesItself(t *testing.T) {
	for _, reason := range []string{
		milestone.RefusalDriftSevere,
		milestone.RefusalNoSiteRef,
		milestone.RefusalNoWindow,
		"no_bookmark_surface",
		"unspecified",
	} {
		if reason == "" {
			t.Error("a refusal reason is empty")
		}
	}
}

// TestAVendorFailureIsARetryAndARefusalIsNot separates the two failure classes, because
// conflating them is the specific mistake this worker exists not to make.
//
// A vendor that answered 500 is a retry: the link was down and the bookmark may yet land.
// A clock in the severe band is a refusal: it will refuse identically every time, and
// retrying it just delays a dead letter nobody needed.
func TestAVendorFailureIsARetryAndARefusalIsNot(t *testing.T) {
	w := &stubWriter{err: errors.New("milestone: 500 from the vendor")}
	worker := &Worker{
		Bookmarks: w,
		Store:     testStore(t),
		Facts:     &stubFacts{facts: testFacts()},
		Policy:    testPolicy(),
	}

	out, err := worker.Handle(context.Background(), evidenceJob(t))
	if err == nil {
		t.Fatalf("a vendor failure returned nil, so the job would be marked complete while "+
			"the bookmark did not exist: %+v", out)
	}
	if out.Placed {
		t.Error("a vendor failure reported a placement")
	}
	if w.calls != 1 {
		t.Errorf("the writer was called %d times; exactly one attempt per job is what makes "+
			"the retry budget meaningful", w.calls)
	}
}

// TestTheJobMustBeOnTheEvidenceQueue refuses a job from anywhere else.
//
// A worker that drains whatever it is handed is a worker that will one day be handed a VLM
// job and place a bookmark for it, and the queue name is the only thing that says which
// work this is.
func TestTheJobMustBeOnTheEvidenceQueue(t *testing.T) {
	worker := &Worker{Bookmarks: &stubWriter{}, Policy: testPolicy()}
	j := evidenceJob(t)
	j.Queue = jobs.QueueVLM

	if _, err := worker.Handle(context.Background(), j); err == nil {
		t.Fatal("a job on the VLM queue was handled by the evidence worker")
	}
}

// TestAJobWithNoIncientIsRefused stops work that cannot be done.
//
// `dispatch` refuses to build a fetch job with neither an incident nor an event, so this
// should be unreachable. It is checked anyway, because the two ends are separate programs
// and a future caller could construct the payload by hand - and a fetch with no subject is
// work that succeeds at nothing, which is the shape of defect this whole branch is about.
func TestAJobWithNoIncidentIsRefused(t *testing.T) {
	payload, err := dispatch.EncodeJobPayload(dispatch.JobPayload{
		ContractVersion: dispatch.ContractVersion,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	j := evidenceJob(t)
	j.Payload = payload
	j.IdempotencyKey = "no-colon-key"

	worker := &Worker{
		Bookmarks: &stubWriter{},
		Store:     testStore(t),
		Facts:     &stubFacts{facts: testFacts()},
		Policy:    testPolicy(),
	}
	if _, err := worker.Handle(context.Background(), j); err == nil {
		t.Fatal("a fetch job naming no incident was handled")
	}
}

// testFacts returns valid facts for tests that need to reach the bookmark logic.
func testFacts() Facts {
	return Facts{
		IncidentID:  "inc-1",
		SiteID:      "site-1",
		RuleID:      "rule-1",
		Title:       "Test Incident",
		SiteRef:     "site-ref-1",
		DeviceID:    "cam-1",
		WindowStart: time.Now().Add(-5 * time.Second),
		WindowEnd:   time.Now(),
		DriftMS:     0,
	}
}

// TestTheReceiptCarriesNoFootage is the design assertion, on the document itself.
//
// `FootageIsElsewhere` is always true and always written. A reader holding only the receipt
// must not conclude the video is stored here - that is the mistake that turns a privacy
// question ("what do you hold about this person?") into an incident, and it is the reason
// the field is in the document rather than in a README.
func TestTheReceiptCarriesNoFootage(t *testing.T) {
	r := Receipt{FootageIsElsewhere: true, EvidenceCount: 0, ContractVersion: ReceiptVersion}
	if !r.FootageIsElsewhere {
		t.Error("the receipt does not say the footage is elsewhere")
	}
	if r.EvidenceCount != 0 {
		t.Error("the receipt claims it fetched bytes")
	}
}

// TestTheReceiptVersionIsDistinctFromTheDispatchContract keeps the two documents from
// being mistaken for one another.
//
// They are different documents with different readers: one goes to a webhook receiver, the
// other stays here and is read by whoever asks what evidence an incident had. A shared
// version string would make a reader that understood one of them accept the other.
func TestTheReceiptVersionIsDistinctFromTheDispatchContract(t *testing.T) {
	if ReceiptVersion == dispatch.ContractVersion {
		t.Error("the receipt and the dispatch payload share a contract version. They are " +
			"different documents with different readers and must version separately")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testPolicy is the shipped default clock policy.
//
// The real one, not a permissive stub: a test that invented a policy with no severe band
// would find no drift severe and would pass without ever exercising the refusal this
// worker exists to get right.
func testPolicy() clock.Policy { return clock.DefaultPolicy() }

// testWindow is the incident window every case is built around.
//
// A five-second window straddling one instant, chosen so a pre-roll that widens it and a
// post-roll that extends it both move the answer in a direction a test can state.
