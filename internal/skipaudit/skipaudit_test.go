package skipaudit_test

// The gate's own tests.
//
// The rule this package enforces is "a skipped test is a failed run", so the
// tests have to be able to see a skip without being one themselves. A test that
// calls `t.Skip` to set up its fixture would be a test that can never prove the
// gate works, so the fixtures here are written to a temp directory and fed in as
// strings.

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/skipaudit"
)

// stream builds a `go test -json` stream. The shape is what `go test` actually
// emits: run/output/skip per test, then a package-level pass or fail.
func stream(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

const (
	okTest   = `{"Action":"run","Package":"p","Test":"TestPassing"}`
	okOutput = `{"Action":"output","Package":"p","Test":"TestPassing","Output":""}`
	okEnd    = `{"Action":"pass","Package":"p","Test":"TestPassing","Elapsed":0.01}`
	pkgEnd   = `{"Action":"pass","Package":"p","Elapsed":0.02}`

	skipRun    = `{"Action":"run","Package":"p","Test":"TestSkipped"}`
	skipOutput = `{"Action":"output","Package":"p","Test":"TestSkipped","Output":"    database_test.go:12: SP_TEST_DATABASE_URL is not set; skipping database-backed test\n"}`
	skipEnd    = `{"Action":"skip","Package":"p","Test":"TestSkipped","Elapsed":0}`
)

func TestACleanRunPasses(t *testing.T) {
	res, err := skipaudit.Check(strings.NewReader(stream(okTest, okOutput, okEnd, pkgEnd)), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Failed() {
		t.Errorf("a run where nothing skipped was reported as failed:\n%s", res.Report(nil))
	}
	if res.Packages != 1 || res.Passed != 1 {
		t.Errorf("1 package passed, got Packages=%d Passed=%d", res.Packages, res.Passed)
	}
}

// TestASkippedDatabaseSuiteFails is the red-check for the gate, in the direction
// that matters: the fixture is exactly what an unset SP_TEST_DATABASE_URL
// produces, and it must be a failure rather than a pass.
func TestASkippedDatabaseSuiteFails(t *testing.T) {
	in := stream(okTest, okOutput, okEnd, skipRun, skipOutput, skipEnd, pkgEnd)
	res, err := skipaudit.Check(strings.NewReader(in), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Failed() {
		t.Fatal("a skipped database-backed test was reported as a pass. This is the whole defect " +
			"the gate exists for, and a gate that lets it through is a comment")
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "p.TestSkipped" {
		t.Errorf("expected the skip to be named, got %v", res.Skipped)
	}
	// The reason is attached, because a failure that says only "something skipped"
	// sends the reader to grep.
	if why := res.Reasons["p.TestSkipped"]; !strings.Contains(why, "SP_TEST_DATABASE_URL") {
		t.Errorf("the skip's reason was not carried through: %q", why)
	}
	report := res.Report(nil)
	if !strings.Contains(report, "SP_TEST_DATABASE_URL") {
		t.Errorf("the report does not name the reason:\n%s", report)
	}
}

// TestAPackageThatDeclinedToRunFails: `go test` emits a package-level skip with no
// Test field when the whole binary declines, and a gate that requires a Test name
// silently drops exactly the case where nothing ran at all.
func TestAPackageThatDeclinedToRunFails(t *testing.T) {
	in := stream(
		`{"Action":"output","Package":"p","Output":"    a_test.go:5: no database; skipping\n"}`,
		`{"Action":"skip","Package":"p","Elapsed":0}`,
	)
	res, err := skipaudit.Check(strings.NewReader(in), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Failed() {
		t.Fatal("a test binary that declined to run was reported as a pass")
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "p" {
		t.Errorf("expected the package itself to be named, got %v", res.Skipped)
	}
}

func TestADeclaredSkipIsAllowedAndPrinted(t *testing.T) {
	exempt := map[string]skipaudit.Expected{
		"p.TestSkipped": {Test: "p.TestSkipped", Why: "only runs on a platform with a camera SDK"},
	}
	in := stream(okTest, okOutput, okEnd, skipRun, skipOutput, skipEnd, pkgEnd)
	res, err := skipaudit.Check(strings.NewReader(in), exempt, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Failed() {
		t.Errorf("a declared skip failed the gate:\n%s", res.Report(exempt))
	}
	report := res.Report(exempt)
	if !strings.Contains(report, "only runs on a platform with a camera SDK") {
		t.Errorf("the declared skip and its reason are not printed on the run that used it:\n%s", report)
	}
}

func TestADeclaredSkipIsAlsoPrintedOnACleanRun(t *testing.T) {
	exempt := map[string]skipaudit.Expected{
		"p.TestSkipped": {Test: "p.TestSkipped", Why: "only runs on a platform with a camera SDK"},
	}
	res, err := skipaudit.Check(strings.NewReader(stream(okTest, okOutput, okEnd, pkgEnd)), exempt, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(res.Report(exempt), "only runs on a platform with a camera SDK") {
		t.Error("the allow-list is not printed on a run that did not use it. An exemption list that " +
			"surfaces only when it fires is one nobody reads before widening it")
	}
}

// TestADeclarationThatDidNotFireFails: an allow-list entry for a test that no
// longer skips is an exemption for the next test somebody names after it, and the
// gate that has it is not the gate anybody thinks it is.
func TestADeclarationThatDidNotFireFails(t *testing.T) {
	exempt := map[string]skipaudit.Expected{
		"p.TestGone": {Test: "p.TestGone", Why: "declared for a test that was deleted"},
	}
	res, err := skipaudit.Check(strings.NewReader(stream(okTest, okOutput, okEnd, pkgEnd)), exempt, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Failed() {
		t.Error("a declaration for a test that no longer skips was accepted silently")
	}
	if len(res.ExpectedUnused) != 1 || res.ExpectedUnused[0] != "p.TestGone" {
		t.Errorf("the unused declaration was not named: %v", res.ExpectedUnused)
	}
}

func TestADeclarationWithoutAReasonIsRefused(t *testing.T) {
	exempt := map[string]skipaudit.Expected{"p.TestSkipped": {Test: "p.TestSkipped"}}
	_, err := skipaudit.Check(strings.NewReader(stream(skipRun, skipOutput, skipEnd)), exempt, "linux", "")
	if err == nil || !strings.Contains(err.Error(), "without saying why") {
		t.Errorf("a blank reason was not refused: %v", err)
	}
}

// TestAnEmptyStreamFails: the gate reading nothing must not report a pass, because
// "no skips" and "no tests" are the same observation to a naive count.
func TestAnEmptyStreamFails(t *testing.T) {
	res, err := skipaudit.Check(strings.NewReader(""), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Failed() {
		t.Error("an empty stream was reported as a pass")
	}
	if !strings.Contains(res.Report(nil), "no test binary reported") {
		t.Errorf("the report does not say the gate covered nothing:\n%s", res.Report(nil))
	}
}

// TestANonJSONLineIsIgnored: `go test` writes build diagnostics to stderr and some
// toolchains write a banner to stdout, and a gate that fails on a banner is a gate
// about the banner.
func TestANonJSONLineIsIgnored(t *testing.T) {
	in := "# warning: some build output\n" + stream(okTest, okOutput, okEnd, pkgEnd)
	res, err := skipaudit.Check(strings.NewReader(in), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Failed() {
		t.Errorf("a banner line failed the gate:\n%s", res.Report(nil))
	}
}

// TestALongOutputLineIsNotAScannerFailure: a `-v` suite's Output event carries the
// whole test log, and bufio.Scanner's 64KiB default turns a long golden test into
// a scanner error — which reads as "no results", the one thing this gate must never
// report. It was found by a golden test's log, not by reasoning.
func TestALongOutputLineIsNotAScannerFailure(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	in := stream(
		`{"Action":"output","Package":"p","Test":"TestVerbose","Output":`+quote(long)+`}`,
		okRun, okOutput, okEnd, pkgEnd,
	)
	res, err := skipaudit.Check(strings.NewReader(in), nil, "linux", "")
	if err != nil {
		t.Fatalf("a 200KiB output line was a scanner failure: %v", err)
	}
	if res.Packages != 1 {
		t.Errorf("the long line lost the rest of the stream: Packages=%d", res.Packages)
	}
	if res.Failed() {
		t.Errorf("a clean run with one long log line was reported as failed:\n%s", res.Report(nil))
	}
}

// TestAPackageWithNoTestFilesIsNotASkip: `go test` emits a package-level `skip`
// for a package with no test files. Counting those would put four permanent
// unfixable entries in the allow-list on day one, and an allow-list with entries
// that can never be deleted is one nobody reads.
//
// The distinction matters in the other direction too: a package-level skip that
// is *not* the "no test files" notice means the whole binary declined to run, and
// that is the worst case this gate exists for. `TestAPackageThatDeclinedToRunFails`
// is the positive case.
func TestAPackageWithNoTestFilesIsNotASkip(t *testing.T) {
	in := stream(
		`{"Action":"output","Package":"empty","Output":"?   \tempty\t[no test files]\n"}`,
		`{"Action":"skip","Package":"empty","Elapsed":0}`,
		okTest, okOutput, okEnd, pkgEnd,
	)
	res, err := skipaudit.Check(strings.NewReader(in), nil, "linux", "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Failed() {
		t.Errorf("a package with no test files was reported as a skip:\n%s", res.Report(nil))
	}
	if res.NoTestFiles != 1 {
		t.Errorf("the package was not counted as untested: NoTestFiles=%d", res.NoTestFiles)
	}
	if res.Packages != 1 {
		t.Errorf("a package with no test files was counted as one that ran tests: Packages=%d", res.Packages)
	}
}

const okRun = `{"Action":"run","Package":"p","Test":"TestPassing"}`

func TestADeclarationForAnotherPlatformIsNotStale(t *testing.T) {
	// This is the rule that makes the gate usable on both platforms at once.
	// `TestSymlinkEscapeIsRefused` skips on Windows and runs on Linux. A
	// declaration with no platform scope would therefore be *unused* on Linux, and
	// the unused-declaration check would fail every verification run on the
	// machine where the control actually runs — which is how a cross-platform gate
	// gets disabled.
	exempt := map[string]skipaudit.Expected{
		"p.TestSkipped": {
			Test: "p.TestSkipped", When: "windows",
			Why: "creating a symlink on Windows needs a privilege this test does not assume",
		},
	}
	clean := stream(okTest, okOutput, okEnd, pkgEnd)

	// On Linux the test runs, so the declaration is neither needed nor stale.
	res, err := skipaudit.Check(strings.NewReader(clean), exempt, "linux", "")
	if err != nil {
		t.Fatalf("check on linux: %v", err)
	}
	if len(res.ExpectedUnused) != 0 {
		t.Errorf("a linux run reported a windows declaration as unused: %v", res.ExpectedUnused)
	}

	// On Windows the test skips, and the declaration is then required to fire.
	res, err = skipaudit.Check(strings.NewReader(clean), exempt, "windows", "")
	if err != nil {
		t.Fatalf("check on windows: %v", err)
	}
	if len(res.ExpectedUnused) != 1 {
		t.Errorf("on windows the declaration should be required and unmet: %v", res.ExpectedUnused)
	}

	// And when the Windows skip does happen, it is excused rather than reported.
	skipping := stream(okTest, okOutput, okEnd, skipRun, skipOutput, skipEnd, pkgEnd)
	res, err = skipaudit.Check(strings.NewReader(skipping), exempt, "windows", "")
	if err != nil {
		t.Fatalf("check on windows with a skip: %v", err)
	}
	if res.Failed() {
		t.Errorf("a windows-gated declared skip failed the gate:\n%s", res.Report(exempt))
	}
}

// TestTheModulePrefixIsStripped: `go test -json` reports the full import path and
// every human says the short one. Without stripping it, each declared skip fails
// to match its own skip and the gate reports every entry as both undeclared and
// unused at once, which reads as a broken gate rather than as a spelling mismatch.
func TestTheModulePrefixIsStripped(t *testing.T) {
	exempt := map[string]skipaudit.Expected{
		"internal/evidence.TestSkipped": {
			Test: "internal/evidence.TestSkipped",
			Why:  "the database is not configured on this machine",
		},
	}
	in := stream(
		`{"Action":"run","Package":"github.com/Sergey-Bar/Ai-Captain/internal/evidence","Test":"TestSkipped"}`,
		`{"Action":"output","Package":"github.com/Sergey-Bar/Ai-Captain/internal/evidence",`+
			`"Test":"TestSkipped","Output":"    no database; skipping\n"}`,
		`{"Action":"skip","Package":"github.com/Sergey-Bar/Ai-Captain/internal/evidence","Test":"TestSkipped","Elapsed":0}`,
		`{"Action":"pass","Package":"github.com/Sergey-Bar/Ai-Captain/internal/evidence","Elapsed":0.1}`,
	)
	res, err := skipaudit.Check(strings.NewReader(in), exempt, "linux", "github.com/Sergey-Bar/Ai-Captain")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Failed() {
		t.Errorf("a declared skip did not match its own stream entry:\n%s", res.Report(exempt))
	}
	if res.Packages != 1 {
		t.Errorf("the module prefix was counted as a separate package: Packages=%d", res.Packages)
	}
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
