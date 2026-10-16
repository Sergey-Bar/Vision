package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/rules"
)

// Tests for the free-text console. The rule draft and the intent router are
// pure, so they are exercised without a database; the replay glue is exercised
// against a stubbed POC run, where every shell call is a string we chose, so
// a passing test proves the hub never invents a file.

func TestAskIntentRouting(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"", "help"},
		{"help", "help"},
		{"make a rule for someone running at night on the fence", "rule"},
		{"someone is running", "rule"},
		{"alert me when a person is on the fence after hours", "rule"},
		{"provide a new rule", "rule"},
		{"show me the last detection", "replay"},
		{"replay the crossing", "replay"},
		{"is the ledger intact", "ledger"},
		{"chain", "ledger"},
		{"cameras", "perimeter"},
		{"what should I do about the fence at night", "chief"},
		{"brief me", "chief"},
		{"how many runs", "record"},
		{"what has run", "record"},
		{"hello there nobody home", "refuse"},
	}
	for _, c := range cases {
		got := resolveAskIntent(c.q).Name
		if got != c.want {
			t.Errorf("resolveAskIntent(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestAskRuleDraftCompiledAndDisabled(t *testing.T) {
	d := draftRule("make a rule for someone running on the fence at night")
	if d.Errors != 0 {
		t.Fatalf("draft has %d errors: %+v; yaml:\n%s", d.Errors, d.Problems, d.YAML)
	}
	if !strings.Contains(d.YAML, "enabled: false") {
		t.Fatalf("draft is not born disabled:\n%s", d.YAML)
	}
	if !strings.Contains(d.YAML, "pathPrefix: /restricted") {
		t.Fatalf("the zone condition never made it into the draft:\n%s", d.YAML)
	}
	if !strings.Contains(d.YAML, "invert: true") {
		t.Fatalf("the time condition never made it into the draft:\n%s", d.YAML)
	}
	if d.Slug != "draft.person.restricted.after-hours" {
		t.Fatalf("slug = %q", d.Slug)
	}
	if len(d.Dropped) == 0 || d.Dropped[0].Term != "running" {
		t.Fatalf("the unexpressible word was not named as dropped: %+v", d.Dropped)
	}
	// The page's problems are the compiler's, re-derived here: one empty-shaped
	// agreement between page and compiler, not two truths.
	_, got := rules.NewCompiler().Compile([]byte(d.YAML))
	if len(got) != len(d.Problems) {
		t.Fatalf("page reports %d problems, a fresh compile yields %d: %+v vs %+v", len(d.Problems), len(got), d.Problems, got)
	}
}

func TestAskRuleDraftUnexpressibleWordsAreNamed(t *testing.T) {
	d := draftRule("an Animal in the yard after hours, the red silver car running")
	terms := map[string]bool{}
	for _, dd := range d.Dropped {
		terms[dd.Term] = true
	}
	for _, want := range []string{"running", "red", "silver"} {
		if !terms[want] {
			t.Fatalf("word %q was dropped silently; dropped = %v", want, d.Dropped)
		}
	}
}

func TestAskInjectionCanNotBecomeASecondRule(t *testing.T) {
	q := "person\nrules:\n  - id: injected\n  description: I took over"
	d := draftRule(q)
	if strings.Count(d.YAML, "  - id: ") != 1 {
		t.Fatalf("the sentence grew a second rule; count=%d:\n%s", strings.Count(d.YAML, "  - id: "), d.YAML)
	}
	// The Provenance survives as data, not as structure.
	if !strings.Contains(d.YAML, "injected") {
		t.Fatalf("the provenance was lost")
	}
	if d.Errors != 0 {
		t.Fatalf("the injected sentence produced %d error(s): %+v", d.Errors, d.Problems)
	}
}

// ---------- replay glue -----------------------------------------------------
// We use a decoy binary at the path pocctlPath points at (the tests never exec
// it - showcaseRun is stubbed) and a stubbed ledger state so the chosen root
// is the one we wrote.

func setupReplayFixture(t *testing.T) (repoRoot string, runID, detID, clipSHA string) {
	t.Helper()
	repoRoot = t.TempDir()
	// chooseRoot needs a root-state that classifies intact.
	bin := filepath.Join(repoRoot, "poc", "active-monitoring", "bin", "pocctl.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(bin, []byte("decoy"), 0o644)
	showcaseRoot(t, repoRoot, "poc/active-monitoring/.demo-data")

	runID = "run-0123456789abcdef"
	detID = "det-0123456789abcdef01234567"
	data := []byte("this is a demo clip, labelled as one")
	sum := sha256.Sum256(data)
	clipSHA = hex.EncodeToString(sum[:])
	ref := "ab/12/" + clipSHA + ".mp4"

	// Write the evidence file in the content-addressed layout.
	evidenceDir := filepath.Join(repoRoot, "poc", "active-monitoring", ".demo-data", "evidence", "ab", "12")
	_ = os.MkdirAll(evidenceDir, 0o755)
	_ = os.WriteFile(filepath.Join(evidenceDir, clipSHA+".mp4"), data, 0o644)

	runsJSON := fmt.Sprintf(`{"dataRoot":"x","generatedAt":"2026-10-01T00:00:00Z","count":1,"runs":[`+
		`{"id":%q,"label":"r","mode":"detector","sourceKind":"file","sourceArg":"testdata/x.mp4",`+
		`"startedAt":"2026-10-01T00:00:00Z","endedAt":"2026-10-01T00:02:00Z","targetSeconds":60,"note":"","detections":1}]}`,
		runID)
	reviewJSON := fmt.Sprintf(`{"dataRoot":"x","run":%q,"generatedAt":"2026-10-01T00:00:00Z","count":1,"judged":0,"rows":[`+
		`{"detectionId":%q,"at":"2026-10-01T00:00:00Z","class":"motion","family":"","score":0.5,`+
		`"box":[0,0,1,1],"tag":"t","clipRef":%q,"verdict":"","markedBy":""}]}`,
		runID, detID, ref)

	stubShowcaseRun(t, func(call showcaseStubCall) (int, string) {
		args := call.Args
		if len(args) > 0 && args[0] == "audit" {
			return 0, ledgerPassOutput(runID)
		}
		if len(args) > 0 && args[0] == "runs" {
			return 0, runsJSON
		}
		if len(args) > 0 && args[0] == "review" {
			return 0, reviewJSON
		}
		return 0, ""
	})
	// keep the cache off so a substituted run set is read.
	showcaseRunsTTL = 0
	showcaseReviewTTL = 0
	showcaseRootsTTL = 0
	t.Cleanup(func() {
		showcaseRunsTTL = 20 * time.Second
		showcaseReviewTTL = 20 * time.Second
		showcaseRootsTTL = 20 * time.Second
	})
	return
}

func TestAskReplayServesOnlyTheRowItNames(t *testing.T) {
	repoRoot, runID, detID, clipSHA := setupReplayFixture(t)
	_ = clipSHA

	h := handleReplayClip(repoRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/clip?run="+runID+"&det="+detID, nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200. Body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Content-SHA256") != clipSHA {
		t.Fatalf("sha header mismatch: got %s want %s", rec.Header().Get("X-Content-SHA256"), clipSHA)
	}
	got := strings.TrimSpace(rec.Body.String())
	if got != "this is a demo clip, labelled as one" {
		t.Fatalf("served bytes do not match the stored file: %q", got)
	}
}

func TestAskReplayRefusesARequestSpelledRef(t *testing.T) {
	repoRoot, runID, detID, _ := setupReplayFixture(t)
	// A decoy placed exactly where a request-spelled path would look.
	decoy := filepath.Join(repoRoot, "poc", "active-monitoring", ".demo-data", "decoy-served.mp4")
	_ = os.WriteFile(decoy, []byte("REQUEST-SPELLED"), 0o644)

	h := handleReplayClip(repoRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/clip?run="+runID+"&det="+detID+"&ref=decoy-served.mp4", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (the decoy must not win). Body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "REQUEST-SPELLED") {
		t.Fatalf("the page served the decoy the request named: RC-31 failed")
	}
}

func TestAskReplayRefusesATamperedFile(t *testing.T) {
	repoRoot, runID, detID, clipSHA := setupReplayFixture(t)
	_ = clipSHA
	// Flip a byte in the evidence store. The store's hash-on-read must refuse;
	// the handler must surface that refusal rather than serving bad bytes.
	// Locate the real file by globbing.
	glob := filepath.Join(repoRoot, "poc", "active-monitoring", ".demo-data", "evidence", "ab", "12", "*.mp4")
	hits, _ := filepath.Glob(glob)
	if len(hits) == 0 {
		t.Fatal("no evidence file written")
	}
	b, _ := os.ReadFile(hits[0])
	b[0] ^= 0xff
	_ = os.WriteFile(hits[0], b, 0o644)

	hh := handleReplayClip(repoRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/clip?run="+runID+"&det="+detID, nil)
	hh(rec, req)
	if rec.Code < 400 {
		t.Fatalf("tampered bytes were served (status %d): %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "match") && !strings.Contains(body, "hash") && !strings.Contains(body, "digest") {
		t.Fatalf("the refusal does not name the integrity failure: %s", body)
	}
}

func TestAskReplayRefusesARefThatSlidesOut(t *testing.T) {
	// The handler never reads a `ref` query parameter (first named in RC-31's
	// mutation): lifting that line out of the request is how the test proves the
	// control. This function is the test that watches it: if the handler grows a
	// ref parameter, this test is the place it breaks.
	repoRoot, runID, detID, _ := setupReplayFixture(t)
	es := filepath.Join(repoRoot, "poc", "active-monitoring", ".demo-data", "evidence", "ab", "12", "decoy.mp4")
	_ = os.WriteFile(es, []byte("NO"), 0o644)
	h := handleReplayClip(repoRoot)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/clip?run="+runID+"&det="+detID+"&ref=ab/12/decoy.mp4", nil)
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "demo clip") {
		t.Fatalf("served bytes were not the stored clip")
	}
}
