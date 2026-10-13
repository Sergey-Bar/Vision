package evidencefetch

// The plan's C1 DoD, and the one item the M2 pass left open: a kill -9 at the
// fetch stage.
//
// # What is being proved
//
// Invariant 1 is "zero accepted event loss... kill -9 at any stage: no loss, no
// duplicate side effect". C1 added a durable stage this product did not have
// before: bytes taken from the vendor's archive, written through the evidence
// store, and only afterwards recorded as an `evidence` row and an
// `evidence.fetched` ledger entry. A process killed in the middle of that write
// has to leave:
//
//   - no visible clip. The store publishes by rename, so a file that was never
//     renamed has no ref, and nothing can address it or mistake it for evidence.
//   - no `evidence` row. Object-then-row means the row was never reached.
//   - no `evidence.fetched` ledger entry. It commits with the row or not at all,
//     because an action this product performed and cannot account for is the
//     failure the ledger exists to prevent.
//   - a job that is still claimable. The lease lapses and another worker takes
//     the work back, which is what makes a crash recoverable rather than fatal.
//
// And then the half of invariant 1 that is easy to leave out: when the job is
// retried, it writes ONE row and ONE ledger entry. A kill -9 that survived but
// doubled its evidence would be a quieter failure than one that lost it.
//
// # Why it is a real child process
//
// Every interesting way this fails fails because a `defer` did not run. The
// staging file is removed by a deferred closure, the transaction is rolled back
// by a deferred call, and a fault-injection hook inside this process would run
// both of them on the way out — so the drill would pass against a fetch that
// leaks on every restart, which is the actual bug. So the child is a separate
// process and the kill is TerminateProcess / SIGKILL: no deferred functions, no
// flushing, no unwinding. The same shape, for the same reason, as
// `internal/evidence`'s own kill test.
//
// # Why the kill lands on the clip and not on the receipt
//
// The worker stores a receipt as well as the clip, and the receipt's write goes
// through a staging file too. A parent that killed on "a staging file exists"
// would kill during the receipt's write, satisfy every assertion below, and
// prove nothing about the fetch.
//
// The first version of this drill marked the fetch from inside `FetchClip` and
// killed on the first staging file after that marker. It was falsified by the
// row-before-bytes mutation of `fetch.go` — a kill that should have left a row
// pointing at bytes that were never written — and the mutation passed, because
// in that code path the receipt's staging file is the first one after the
// marker. So the marker now comes from the clip's own reader, on its first
// byte: a signal that is inseparable from the write it describes. The residue
// is then asserted to exist, which is how the drill tells "the child really was
// mid-write" from "the child finished and we killed a process that had nothing
// to do".

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The child's contract with the parent: one switch and the rows the parent
// seeded. Nothing here is derived from configuration, because a child that
// guessed its own incident would be testing a different incident than the one
// the assertions are about.
const (
	killHelperEnv = "EVIDENCEFETCH_KILL_HELPER"

	killRootEnv     = "EVIDENCEFETCH_KILL_ROOT"
	killMarkersEnv  = "EVIDENCEFETCH_KILL_MARKERS"
	killTenantEnv   = "EVIDENCEFETCH_KILL_TENANT"
	killSiteEnv     = "EVIDENCEFETCH_KILL_SITE"
	killIncidentEnv = "EVIDENCEFETCH_KILL_INCIDENT"
	killJobEnv      = "EVIDENCEFETCH_KILL_JOB"
)

// killClipBody is the bytes the child streams and the parent retries. The same
// body both times, because the store is content-addressed: the retry has to land
// at the ref the killed write was heading for, or an assertion about there being
// one row is measuring a different write.
//
// Eight megabytes, and that is not a round number chosen for looks. The child
// hands it over in 4 KiB pieces with a pause between them, so the write is
// still running half a minute after it starts — far longer than the parent needs
// to notice it and kill it, and long enough that a machine thirty times slower
// cannot finish the write before the kill lands.
var killClipBody = bytes.Repeat([]byte("V"), 8<<20)

// killChildLease is the lease the child takes its job under.
//
// Short, and deliberately so: the drill's last assertion is that the job is
// claimable again, and a lease the child holds for a minute would make the
// parent wait a minute to ask. Three seconds is far longer than the child needs
// to reach the fetch and far shorter than a reader will notice.
const killChildLease = 3 * time.Second

// TestKilledMidFetchLeavesNoClipNoRowAndAReclaimableJob is the drill.
func TestKilledMidFetchLeavesNoClipNoRowAndAReclaimableJob(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	ctx := context.Background()
	// The child gets its own deadline, so a child that hangs waiting for a vendor
	// that never answers is killed rather than hanging the run - and the go test
	// default timeout is not the thing that stops it, because `go test` would kill
	// the parent with it.
	runCtx, stopChild := context.WithTimeout(ctx, 2*time.Minute)
	defer stopChild()
	tenant, site, incident := seedFetchIncident(t)
	raw := testutil.Pool(t)
	root := t.TempDir()
	markerDir := t.TempDir()

	// The job row, enqueued the way `dispatch` enqueues one: a lease-bearing row
	// on the evidence_fetch queue, naming its site because `evidence.site_id` is
	// NOT NULL and the receipt this job writes carries it.
	payload, err := dispatch.EncodeJobPayload(dispatch.JobPayload{
		ContractVersion: dispatch.ContractVersion,
		IncidentID:      incident,
	})
	if err != nil {
		t.Fatalf("encode the job payload: %v", err)
	}
	pool := openFetchPool(t)
	tx, err := raw.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	created, jobID, err := jobs.New(pool).Enqueue(ctx, tx, jobs.Enqueue{
		TenantID:       tenant,
		SiteID:         site,
		Queue:          jobs.QueueEvidenceFetch,
		IdempotencyKey: incident + ":evidence.fetch",
		Payload:        payload,
		MaxAttempts:    5,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("enqueue the fetch job: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fetch job: %v", err)
	}
	if !created {
		t.Fatalf("the job with key %q already exists, so this run would assert about "+
			"another run's job", incident+":evidence.fetch")
	}

	// Re-executing this test binary as the child. The path is os.Args[0], which
	// is the binary the test runner already started — not anything this test reads
	// from configuration, an environment variable, or a file. The context is the
	// one above: the linter's noctx rule is right that a subprocess a test starts
	// and never bounds is a subprocess that outlives the run that asked for it,
	// and gosec's G204 is overruled on purpose — the argument is this process's own
	// binary, which is the one thing here that cannot be attacker-chosen.
	//nolint:gosec // the argument is the running test binary, not input
	cmd := exec.CommandContext(runCtx, os.Args[0], "-test.run=TestKilledMidFetch_Helper")
	cmd.Env = append(os.Environ(),
		killHelperEnv+"=1",
		killRootEnv+"="+root,
		killMarkersEnv+"="+markerDir,
		killTenantEnv+"="+tenant,
		killSiteEnv+"="+site,
		killIncidentEnv+"="+incident,
		killJobEnv+"="+jobID,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the child: %v", err)
	}
	// A leaked child holding a staging file open would make the next run's
	// residue assertions meaningless, so it is killed however this test leaves.
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	// 1. Wait until the clip's bytes are demonstrably arriving, then until the
	//    clip's staging file exists. The marker is written by the clip's own
	//    reader, so it cannot be satisfied by any other write in the path, and the
	//    two together are the proof the child is mid-write on the right file.
	marker := filepath.Join(markerDir, "fetching")
	if !waitForFile(marker, 60*time.Second, cmd) {
		t.Fatalf("the child never began reading the clip, so the kill would have "+
			"proved nothing about it. The child said:\n%s", out.String())
	}
	if !waitForStagingFile(root, 30*time.Second, cmd) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the child began reading the clip but never opened a staging "+
			"file, so no write was in progress to kill. The child said:\n%s", out.String())
	}

	// 2. Kill it hard, mid-write.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()

	// 3. No visible clip. The ref is the content hash, and a file that was never
	//    renamed into place has no ref — nothing can address it, and nothing can
	//    serve it either.
	sum := sha256.Sum256(killClipBody)
	sha := hex.EncodeToString(sum[:])
	ref := evidence.RefFor(tenant, sha)
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("reopen the store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref))); err == nil {
		t.Error("a partial clip is visible at its final ref")
	}
	if ok, err := store.Exists(ref); err == nil && ok {
		t.Error("a partial clip was readable through the store")
	}
	if _, _, err := store.Open(ref, sha); err == nil {
		t.Error("a partial clip opened through the store, so a killed write is not " +
			"merely invisible — it is servable")
	}

	// 4. The residue is on disk, which is the other half of the problem and the
	//    proof this drill exercised something. A kill that leaves no staging file
	//    is a kill of a process that was not writing.
	if residue := stagingFilesIn(root); len(residue) == 0 {
		t.Error("the killed child left no residue, so this test is not exercising " +
			"anything: the write was not in progress when the kill landed")
	}

	// 5. No clip row, no fetched ledger entry. The receipt, by contrast, is
	//    expected to be there: it commits before the fetch starts, and a drill
	//    whose receipt is missing would be one whose child died earlier than the
	//    stage under test.
	assertCount(t, raw,
		`SELECT count(*) FROM evidence WHERE tenant_id = $1 AND evidence_id = $2`,
		"the clip row", tenant, "evd-clip-"+incident, 0)
	assertCount(t, raw,
		`SELECT count(*) FROM evidence WHERE tenant_id = $1 AND evidence_id = $2`,
		"the receipt row", tenant, "evd-bookmark-"+incident, 1)
	assertCount(t, raw,
		`SELECT count(*) FROM audit_ledger WHERE tenant_id = $1 AND action = $2`,
		"the "+actionFetched+" ledger entry", tenant, actionFetched, 0)
	assertCount(t, raw,
		`SELECT count(*) FROM audit_ledger WHERE tenant_id = $1 AND action = $2`,
		"the "+ActionPlaced+" ledger entry", tenant, ActionPlaced, 1)

	// 6. The job is leased by a dead worker — not done, not dead-lettered — and
	//    the claim predicate takes it back once the lease expires. A job whose
	//    worker died is the case the claim's `status = 'leased' AND
	//    lease_expires_at < now()` clause exists for.
	var status string
	var attempts int
	if err := raw.QueryRow(ctx,
		`SELECT status, attempt_count FROM jobs WHERE job_id = $1`, jobID).
		Scan(&status, &attempts); err != nil {
		t.Fatalf("read the job: %v", err)
	}
	if status != string(jobs.StatusLeased) {
		t.Errorf("the job is %q after its worker was killed; a killed worker leaves "+
			"its job leased until the lease lapses, and anything else means the "+
			"terminal write ran without a live process", status)
	}
	if attempts != 1 {
		t.Errorf("the job's attempt count is %d, want 1: the claim advanced it once "+
			"and the killed handler must not count as a failure", attempts)
	}
	if !waitFor(30*time.Second, func() bool {
		var claimable int
		if err := raw.QueryRow(ctx, `
			SELECT count(*) FROM jobs
			 WHERE job_id = $1 AND run_after <= now()
			   AND (status IN ('pending','failed')
			     OR (status = 'leased' AND lease_expires_at IS NOT NULL
			         AND lease_expires_at < now()))`, jobID).Scan(&claimable); err != nil {
			t.Fatalf("ask whether the job is claimable: %v", err)
		}
		return claimable == 1
	}) {
		t.Fatalf("the job never became claimable again, so a worker killed mid-fetch " +
			"holds its evidence work until a human intervenes")
	}

	// 7. And the retry: the same job, run again to completion, writes exactly one
	//    row and one ledger entry, and the bytes are then readable at the ref the
	//    killed write was heading for. No loss and no duplicate side effect.
	q := &jobs.Worker{
		Enqueuer:      *jobs.New(pool),
		Name:          jobs.QueueEvidenceFetch,
		LeaseDuration: time.Minute,
	}
	claimed, err := q.Claim(ctx, tenant, "parent-retry", 5)
	if err != nil {
		t.Fatalf("re-claim the job: %v", err)
	}
	var retryOf jobs.Job
	for _, j := range claimed {
		if j.ID == jobID {
			retryOf = j
		}
	}
	if retryOf.ID == "" {
		t.Fatalf("the re-claim returned %d job(s) and none was ours; the drill "+
			"cannot assert about a retry it did not perform", len(claimed))
	}
	if retryOf.AttemptCount != 2 {
		t.Errorf("the re-claimed job's attempt count is %d, want 2", retryOf.AttemptCount)
	}

	retry := fetchWorkerOver(t, &wideProbeMedia{body: killClipBody}, root)
	outc, err := retry.Handle(ctx, retryOf)
	if err != nil {
		t.Fatalf("the retry failed, so the work a kill -9 interrupted is work the "+
			"retry could not finish: %v", err)
	}
	if outc.Fetched == "" {
		t.Fatalf("the retry took no bytes; the kill -9 must not turn a fetch into a gap")
	}
	if err := q.Complete(ctx, tenant, retryOf.ID, "parent-retry"); err != nil {
		t.Fatalf("complete the retried job: %v", err)
	}

	assertCount(t, raw,
		`SELECT count(*) FROM evidence WHERE tenant_id = $1 AND evidence_id = $2`,
		"the clip row after the retry", tenant, "evd-clip-"+incident, 1)
	assertCount(t, raw,
		`SELECT count(*) FROM audit_ledger WHERE tenant_id = $1 AND action = $2`,
		"the "+actionFetched+" ledger entry after the retry", tenant, actionFetched, 1)
	if body, _, err := store.Open(ref, sha); err != nil {
		t.Errorf("the retried write is not readable at its ref: %v", err)
	} else {
		_ = body.Close()
	}
}

// TestKilledMidFetch_Helper is the child process. It is a test so it reuses the
// test binary, and it is gated on an environment variable so an ordinary run
// does nothing.
//
// It does exactly what the production loop does: claim the job under a lease,
// run the worker over it, complete it. The parent kills it during the fetch, so
// a zero exit code means the kill did not land — which the parent's assertions
// then report as the failure it is.
//
// The body is a separate function for one reason, and it is the linter's:
// `os.Exit` in a function that holds deferred cleanups skips them, and the child's
// cleanups close a database pool. So the body returns its exit code and the exit
// happens here, in the one function with no `defer` of its own.
func TestKilledMidFetch_Helper(t *testing.T) {
	if os.Getenv(killHelperEnv) != "1" {
		t.Skip("helper process")
	}
	if code := runKillHelper(t); code != 0 {
		os.Exit(code)
	}
}

func runKillHelper(t *testing.T) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	root := os.Getenv(killRootEnv)
	tenant := os.Getenv(killTenantEnv)
	site := os.Getenv(killSiteEnv)
	incident := os.Getenv(killIncidentEnv)
	jobID := os.Getenv(killJobEnv)
	for name, value := range map[string]string{
		killRootEnv: root, killTenantEnv: tenant, killSiteEnv: site,
		killIncidentEnv: incident, killJobEnv: jobID,
	} {
		if value == "" {
			fmt.Fprintf(os.Stderr, "child: %s is not set\n", name)
			return 2
		}
	}

	pool := openFetchPool(t)
	media := &killMedia{markerDir: os.Getenv(killMarkersEnv)}
	w := fetchWorkerOver(t, media, root)

	q := &jobs.Worker{
		Enqueuer:      *jobs.New(pool),
		Name:          jobs.QueueEvidenceFetch,
		LeaseDuration: killChildLease,
		Handler: func(ctx context.Context, j jobs.Job) error {
			_, err := w.Handle(ctx, j)
			return err
		},
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "child: "+format+"\n", args...)
		},
	}
	handled, err := q.Run(ctx, tenant, "kill-9-child", 1)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child: run:", err)
		return 3
	}
	if handled != 1 {
		// The kill did not land and the work failed. Say why, in the job's own
		// words, because a child that exits quietly leaves the parent reporting a
		// fetch that never happened with no reason attached to it.
		var lastErr string
		_ = pool.QueryRow(ctx,
			`SELECT COALESCE(last_error, '') FROM jobs WHERE job_id = $1`, jobID).Scan(&lastErr)
		fmt.Fprintf(os.Stderr, "child: the job failed: %s\n", lastErr)
		return 4
	}
	return 0
}

// killMedia is the vendor's recording surface as the child sees it: a probe that
// answers, and a clip that arrives one small piece at a time so the parent can
// kill the write while it is happening.
type killMedia struct {
	markerDir string
}

func (m *killMedia) ProbeRange(_ context.Context, _ string, from, to time.Time) (milestone.MediaRange, error) {
	// Widened from the ask rather than fixed: the fetch's own pre/post rolls and
	// the drift correction move the window, and a fixture interval narrower than
	// the ask would make the probe refuse a window the vendor really holds.
	return milestone.MediaRange{
		Source: "kill-fixture",
		Available: []milestone.Interval{
			{From: from.Add(-time.Hour), To: to.Add(time.Hour)},
		},
	}, nil
}

func (m *killMedia) FetchClip(_ context.Context, _ string, from, to time.Time) (milestone.MediaClip, error) {
	// The parent's signal is NOT written here. It is written by the clip's own
	// reader, on its first byte, because the kill condition is "the clip's
	// staging file exists AND the clip's reader has started" — and that is the
	// only combination that can only be the clip's write.
	//
	// A marker written in FetchClip is satisfied by the receipt's staging file
	// in any code path that stores the receipt first, which is how this drill
	// was found to be auditing the receipt rather than the bytes: the
	// row-before-bytes mutation of `fetch.go` passed it, because the kill
	// landed on the receipt's write. The marker is where the bytes are, or it
	// is not evidence.
	return milestone.MediaClip{
		Kind:        milestone.MediaClipKind,
		ContentType: "video/mp4",
		Size:        int64(len(killClipBody)),
		Reader: io.NopCloser(io.LimitReader(
			&slowFetchSource{r: bytes.NewReader(killClipBody), markers: m.markerDir},
			int64(len(killClipBody)))),
		Range: milestone.Interval{From: from, To: to},
	}, nil
}

func (m *killMedia) FetchSnapshot(_ context.Context, _ string, _ time.Time) (milestone.MediaClip, error) {
	return milestone.MediaClip{Kind: milestone.MediaSnapshotKind}, nil
}

// wideProbeMedia is the same surface, fast, for the retry: the same bytes, so
// the retry lands at the content-addressed ref the killed write was heading for.
type wideProbeMedia struct{ body []byte }

func (m *wideProbeMedia) ProbeRange(_ context.Context, _ string, from, to time.Time) (milestone.MediaRange, error) {
	return milestone.MediaRange{
		Source: "kill-fixture",
		Available: []milestone.Interval{
			{From: from.Add(-time.Hour), To: to.Add(time.Hour)},
		},
	}, nil
}

func (m *wideProbeMedia) FetchClip(_ context.Context, _ string, from, to time.Time) (milestone.MediaClip, error) {
	return milestone.MediaClip{
		Kind:        milestone.MediaClipKind,
		ContentType: "video/mp4",
		Size:        int64(len(m.body)),
		Reader:      io.NopCloser(bytes.NewReader(m.body)),
		Range:       milestone.Interval{From: from, To: to},
	}, nil
}

func (m *wideProbeMedia) FetchSnapshot(_ context.Context, _ string, _ time.Time) (milestone.MediaClip, error) {
	return milestone.MediaClip{Kind: milestone.MediaSnapshotKind}, nil
}

// slowFetchSource reads in small pieces with a pause, so the parent can kill the
// process at a point where the bytes are genuinely still arriving. The pause is
// what makes this a mid-write kill rather than a race.
//
// It writes `markers/fetching` on its first byte, which is the parent's signal
// that the clip's write is under way. Written here rather than by the fixture
// that produced the reader, because only here is the signal attached to the
// bytes the drill is about.
type slowFetchSource struct {
	r       io.Reader
	markers string
}

func (s *slowFetchSource) Read(p []byte) (int, error) {
	if s.markers != "" {
		_ = os.WriteFile(filepath.Join(s.markers, "fetching"), []byte("1"), 0o600)
		s.markers = ""
	}
	if len(p) > 4096 {
		p = p[:4096]
	}
	n, err := s.r.Read(p)
	time.Sleep(25 * time.Millisecond)
	return n, err
}

// waitForFile reports whether path appeared within the deadline, giving up early
// if the child dies first — a child that has exited is never going to write one.
func waitForFile(path string, within time.Duration, cmd *exec.Cmd) bool {
	return waitFor(within, func() bool {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		return cmd.ProcessState != nil
	})
}

// waitForStagingFile reports whether the store root held a staging file within
// the deadline, giving up early if the child dies first.
func waitForStagingFile(root string, within time.Duration, cmd *exec.Cmd) bool {
	return waitFor(within, func() bool {
		if len(stagingFilesIn(root)) > 0 {
			return true
		}
		return cmd.ProcessState != nil
	})
}

// stagingFilesIn lists the store's staging files, which is how the parent tells
// "the child really was mid-write" from "the child finished".
func stagingFilesIn(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && bytes.HasPrefix([]byte(e.Name()), []byte(".incoming-")) {
			out = append(out, e.Name())
		}
	}
	return out
}

// waitFor polls a condition until it holds or the deadline passes. A condition
// that returns true early — "the child is gone" — ends the wait, because the
// thing being waited for is never coming.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// assertCount is a scalar count assertion with a name, because "0 != 1" without
// saying which of the four counts is wrong sends the reader to the source.
func assertCount(t *testing.T, pool *pgxpool.Pool, sql, what, tenant string, arg any, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), sql, tenant, arg).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", what, err)
	}
	if got != want {
		t.Errorf("%s count is %d, want %d", what, got, want)
	}
}
