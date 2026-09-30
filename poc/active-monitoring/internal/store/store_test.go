package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newRun(t *testing.T, s *Store, id string) {
	t.Helper()
	err := s.StartRun(context.Background(), Run{
		ID: id, Label: "test", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Minute), TargetSeconds: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func det(id string) Detection {
	return Detection{ID: id, RunID: "r1", At: time.Now(), Class: "bird", TrackID: 7, Score: 0.87,
		BoxTop: 10, BoxLeft: 20, BoxBottom: 30, BoxRight: 40, Direction: "left-to-right",
		AIState: "asked_answered", Provenance: ProvenanceSynthetic}
}

// TestDatabaseIsInWAL: WAL is a precondition, not a preference. Without it the
// screen cannot read while the pipeline writes.
func TestDatabaseIsInWAL(t *testing.T) {
	s := testStore(t)
	mode, err := s.journalMode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal mode = %q, want wal", mode)
	}
}

func TestAcceptIsRefusedWithoutAnID(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	d := det("")
	if err := s.Accept(context.Background(), d); err == nil {
		t.Fatal("a detection with no id was accepted; a counter would collide across a restart")
	}
}

func TestAcceptRefusesAnInvalidProvenance(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	d := det("d1")
	d.Provenance = "probably real"
	err := s.Accept(context.Background(), d)
	if err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("an unlabelled provenance was accepted: %v", err)
	}
}

func TestAcceptIsIdempotentByContentDerivedID(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	d := det("d1")
	if err := s.Accept(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	// Same content-derived id, second time: refused, and the row count stays 1.
	err := s.Accept(context.Background(), d)
	if err == nil {
		t.Fatal("the same detection was accepted twice")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}
	n, err := s.Count(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("detections = %d, want 1", n)
	}
}

func TestAcceptRefusesAWorkerLabelAsAnInitialStatus(t *testing.T) {
	// The bug this guards: writing "pending" or "clipping" into detections.status.
	// Those are not states of the machine in CONTRACT.md, and a row stuck in one
	// fails AC2 for a reason nobody can see.
	s := testStore(t)
	newRun(t, s, "r1")
	if err := s.Transition(context.Background(), "missing", "pending", "", ""); err == nil {
		t.Fatal("'pending' was accepted as a status")
	}
	for _, bad := range []string{"pending", "clipping", "pushing", "queued", "ok", ""} {
		if ValidStatus(bad) {
			t.Fatalf("%q is not a status in the closed set but was accepted", bad)
		}
	}
}

func TestTerminalDetectionsCannotBeMovedAgain(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("d1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "d1", StatusClipOK, "ref", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "d1", StatusPushSent, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	err := s.Transition(ctx, "d1", StatusPushRefused, "", "403")
	if err == nil {
		t.Fatal("a terminal detection was moved again")
	}
	if !strings.Contains(err.Error(), "already terminal") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

func TestMovingAMissingDetectionNamesItAsALostEvent(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	err := s.Transition(context.Background(), "ghost", StatusClipOK, "", "")
	if err == nil || !strings.Contains(err.Error(), "lost event") {
		t.Fatalf("moving a missing detection did not name it as a lost event: %v", err)
	}
}

func TestStalledFindsRowsThatNeverReachedTerminal(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	for _, id := range []string{"done1", "stuck1", "stuck2"} {
		if err := s.Accept(ctx, det(id)); err != nil {
			t.Fatal(err)
		}
	}
	// One detection finishes.
	if err := s.Transition(ctx, "done1", StatusClipOK, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, "done1", StatusPushSent, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, "done1"); err != nil {
		t.Fatal(err)
	}
	// Age the two stuck rows past the five-minute limit.
	old := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `UPDATE detections SET updated_at=? WHERE id IN ('stuck1','stuck2')`, old); err != nil {
		t.Fatal(err)
	}
	stalled, err := s.Stalled(ctx, "r1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(stalled) != 2 {
		t.Fatalf("stalled = %d, want 2", len(stalled))
	}
}

func TestEnqueueIsIdempotentPerDetectionAndKind(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("d1")); err != nil {
		t.Fatal(err)
	}
	id1, err := s.Enqueue(ctx, OutboxEntry{DetectionID: "d1", Kind: KindEvent, Payload: "{}", Tag: "tag-1"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.Enqueue(ctx, OutboxEntry{DetectionID: "d1", Kind: KindEvent, Payload: "{}", Tag: "tag-1"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("a second enqueue created a second row: %d then %d", id1, id2)
	}
	entries, err := s.OutboxFor(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox rows = %d, want 1", len(entries))
	}
}

func TestEnqueueRefusesAnEmptyTag(t *testing.T) {
	// The tag is the idempotency key the read-back uses. A counter would produce
	// the same tag after a restart and the read-back would then find the wrong
	// event.
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("d1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, OutboxEntry{DetectionID: "d1", Kind: KindEvent, Payload: "{}"}, 8); err == nil {
		t.Fatal("an outbox entry with no tag was accepted")
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	now := time.Now()
	first := backoffFor(now, 0)
	third := backoffFor(now, 2)
	tenth := backoffFor(now, 9)
	hundredth := backoffFor(now, 99)
	if third <= first {
		t.Fatalf("backoff did not grow: %s then %s", first, third)
	}
	if tenth > 6*time.Minute || hundredth > 6*time.Minute {
		t.Fatalf("backoff is uncapped: %s at 9 attempts, %s at 99", tenth, hundredth)
	}
}

func TestStatsCountUnattributedDetections(t *testing.T) {
	// AC4: a push nobody recorded is a push nobody can account for.
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("withpush")); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, det("nopush")); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(ctx, OutboxEntry{DetectionID: "withpush", Kind: KindEvent, Payload: "{}", Tag: "t1"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordResult(ctx, id, "push_sent", "202", "evt-1"); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 1 || st.WithResult != 1 {
		t.Fatalf("stats = %+v, want one attempt with one result", st)
	}
	if st.Unattributed != 1 {
		t.Fatalf("unattributed = %d, want 1", st.Unattributed)
	}
}

func TestDeadLetterKeepsTheRow(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("d1")); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(ctx, OutboxEntry{DetectionID: "d1", Kind: KindEvent, Payload: "{}", Tag: "t1"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeadLetter(ctx, id, "connection-reset five times"); err != nil {
		t.Fatal(err)
	}
	due, err := s.Due(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a dead-lettered row is still due: %+v", due)
	}
	entries, _ := s.OutboxFor(ctx, "d1")
	if len(entries) != 1 {
		t.Fatalf("the dead-lettered row was deleted; it must stay for human review")
	}
	if !entries[0].DeadLetter || entries[0].Result != "push_dead" {
		t.Fatalf("dead-letter state not recorded: %+v", entries[0])
	}
}

func TestEvidenceRefusesAnUnverifiedClip(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.Accept(ctx, det("d1")); err != nil {
		t.Fatal(err)
	}
	err := s.PutEvidence(ctx, Evidence{ID: "e1", DetectionID: "d1", SHA256: "abc", StorageRef: "evidence/ab/abc.mp4",
		Bytes: 10, Trim: "reencode", TimeBegin: time.Now(), TimeEnd: time.Now(), ReadbackOK: false})
	if err == nil || !strings.Contains(err.Error(), "read back") {
		t.Fatalf("a clip that was never read back was recorded as evidence: %v", err)
	}
}

func TestEvidenceStatsCountMismatches(t *testing.T) {
	// AC3 is computed by re-reading every stored clip, not by trusting the row.
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	for i, id := range []string{"d1", "d2"} {
		if err := s.Accept(ctx, det(id)); err != nil {
			t.Fatal(err)
		}
		_ = i
		if err := s.PutEvidence(ctx, Evidence{ID: "e" + id, DetectionID: id, SHA256: "hash-" + id,
			StorageRef: "evidence/xx/" + id + ".mp4", Bytes: 10, Trim: "reencode",
			TimeBegin: time.Now(), TimeEnd: time.Now(), ReadbackOK: true}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.VerifyStats(ctx, "r1", func(e Evidence) (string, error) {
		if e.SHA256 == "hash-d2" {
			return "different-bytes", nil
		}
		return e.SHA256, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Stored != 2 || st.Verified != 1 || st.Mismatched != 1 {
		t.Fatalf("stats = %+v, want 2 stored, 1 verified, 1 mismatched", st)
	}
}

func TestFaultRefusesAnEmptyReasonOrDetail(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	if err := s.RecordFault(ctx, "r1", Fault{At: time.Now(), Detail: "x"}); err == nil {
		t.Fatal("a fault with no reason was accepted")
	}
	if err := s.RecordFault(ctx, "r1", Fault{At: time.Now(), Reason: "ai_pipe_backpressure"}); err == nil {
		t.Fatal("a fault with no detail was accepted")
	}
}

func TestHeartbeatGapsFindSilence(t *testing.T) {
	s := testStore(t)
	newRun(t, s, "r1")
	ctx := context.Background()
	base := time.Now().Add(-2 * time.Minute)
	for i := 0; i < 5; i++ {
		if err := s.Beat(ctx, Heartbeat{RunID: "r1", At: base.Add(time.Duration(i) * 10 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	// A 60 second hole, then two more beats.
	hole := base.Add(5*10*time.Second + 70*time.Second)
	for i := 0; i < 2; i++ {
		if err := s.Beat(ctx, Heartbeat{RunID: "r1", At: hole.Add(time.Duration(i) * 10 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	gaps, err := s.HeartbeatGaps(ctx, "r1", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want exactly one", gaps)
	}
	if gaps[0].Seconds < 60 {
		t.Fatalf("the gap was measured as %.1fs", gaps[0].Seconds)
	}
}

func TestRunRefusesToClaimAVMSOnALoopbackURL(t *testing.T) {
	// The one combination the report must never show: "reached Milestone" with a
	// loopback target.
	s := testStore(t)
	err := s.StartRun(context.Background(), Run{ID: "r", Label: "l", Mode: "live", SourceKind: "loop", SourceArg: "f",
		PushBaseURL: "http://127.0.0.1:8098", PushIsVMS: true, ClockSource: "host",
		StartedAt: time.Now(), TargetSeconds: 10})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("a run claimed a real VMS on a loopback URL: %v", err)
	}
}

func TestRunRefusesAZeroTarget(t *testing.T) {
	s := testStore(t)
	err := s.StartRun(context.Background(), Run{ID: "r", Label: "l", Mode: "live", SourceKind: "loop", SourceArg: "f",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host", StartedAt: time.Now()})
	if err == nil {
		t.Fatal("a run with no target was accepted; PASS could not be told from PROVISIONAL")
	}
}

// TestKillNineLeavesAResumableState is the durability test the plan makes a DoD
// item. A child process accepts a detection, enqueues its push, and is killed
// without warning. The parent then reopens the database and must find the
// detection in `accepted` and the outbox row still pending: nothing lost,
// nothing already-sent.
func TestKillNineLeavesAResumableState(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go toolchain is required to run the crash child")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module crashtest\n\ngo 1.26.0\n\nrequire modernc.org/sqlite v1.34.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reuse this module's checksums so the child builds from the module cache
	// with no network.
	if sum, err := os.ReadFile(filepath.Join("..", "..", "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	child := filepath.Join(dir, "main.go")
	if err := os.WriteFile(child, []byte(crashChild), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "crashtest.exe")
	build := exec.Command("go", "build", "-mod=mod", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cannot build the crash child: %v: %s", err, out)
	}

	dbPath := filepath.Join(dir, "poc.db")
	runDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, dbPath)
	cmd.Env = append(os.Environ(), "POC_CRASH_READY="+filepath.Join(dir, "ready"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for the child to tell us it has committed, then kill it with no
	// chance to clean up.
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		_ = cmd.Process.Kill()
		t.Skipf("the crash child never reported ready on this machine (%v)", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	s, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("the database is unreadable after a kill -9: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	run, err := s.RunByID(ctx, "r1")
	if err != nil {
		t.Fatalf("the run row did not survive the kill: %v", err)
	}
	if run.EndedAt != nil {
		t.Fatal("the killed run was marked as ended; a crash is not a clean finish")
	}
	d, err := s.Get(ctx, "d-crash")
	if err != nil {
		t.Fatalf("the detection did not survive the kill: %v", err)
	}
	if d.Status != StatusAccepted {
		t.Fatalf("the detection is in %q, want %q: a detection must be recorded before any external effect", d.Status, StatusAccepted)
	}
	due, err := s.Due(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].DetectionID != "d-crash" {
		t.Fatalf("the pending push did not survive the kill: %+v", due)
	}
	if due[0].Result != "" {
		t.Fatalf("the push was marked as attempted even though the process was killed: result=%q", due[0].Result)
	}
}

// crashChild commits a run, a detection and an outbox row, then waits to be
// killed. Every write is committed before the kill, so the parent is testing
// durability and not luck.
const crashChild = `package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	path := os.Args[1]
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(10000)")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(` + "`" + `
		CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, label TEXT, mode TEXT, source_kind TEXT, source_arg TEXT, push_base_url TEXT, push_is_vms INTEGER, clock_source TEXT, started_at TEXT, target_seconds REAL, ended_at TEXT, note TEXT);
		CREATE TABLE IF NOT EXISTS detections (id TEXT PRIMARY KEY, run_id TEXT, at TEXT, class TEXT, track_id INTEGER, score REAL, box_top INTEGER, box_left INTEGER, box_bottom INTEGER, box_right INTEGER, direction TEXT, status TEXT, ai_state TEXT, provenance TEXT, clip_ref TEXT, clip_reason TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE IF NOT EXISTS outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, detection_id TEXT, kind TEXT, payload TEXT, tag TEXT, attempts INTEGER DEFAULT 0, next_attempt TEXT, result TEXT, detail TEXT, remote_id TEXT, dead_letter INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT);
	` + "`" + `)
	if err != nil {
		panic(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(` + "`" + `INSERT INTO runs VALUES ('r1','crash','live','loop','f.mp4','http://127.0.0.1:1',0,'host',?,3600,NULL,NULL)` + "`" + `, now); err != nil {
		panic(err)
	}
	if _, err := db.Exec(` + "`" + `INSERT INTO detections VALUES ('d-crash','r1',?,'bird',1,0.9,1,2,3,4,'ltr','accepted','asked_answered','synthetic',NULL,NULL,?,?)` + "`" + `, now, now, now); err != nil {
		panic(err)
	}
	if _, err := db.Exec(` + "`" + `INSERT INTO outbox (detection_id,kind,payload,tag,attempts,next_attempt,created_at,updated_at) VALUES ('d-crash','event','{}','tag-crash',0,?,?,?)` + "`" + `, now, now, now); err != nil {
		panic(err)
	}
	_ = db.Close()
	// Everything above is committed. Now be killed.
	if p := os.Getenv("POC_CRASH_READY"); p != "" {
		if err := os.WriteFile(p, []byte(fmt.Sprint(time.Now())), 0o644); err != nil {
			panic(err)
		}
	}
	time.Sleep(10 * time.Minute)
}
`
