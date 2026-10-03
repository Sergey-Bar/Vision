package covercheck_test

// The gate's own tests.
//
// The gate has one interesting failure mode — reporting a green run because it
// parsed nothing — and every fixture below is written so that "nothing parsed" and
// "parsed correctly" cannot both be true.

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/covercheck"
)

const mod = "github.com/Sergey-Bar/Ai-Captain"

// profile builds a `go test -coverprofile` stream. The counts are per basic
// block, which is what the toolchain emits.
func profile(lines ...string) string {
	return "mode: atomic\n" + strings.Join(lines, "\n") + "\n"
}

// blocks builds `n` blocks of `stmts` statements each, the first `covered` of
// which were executed.
//
// Written as a generator because a block's count is the number of times it *ran*,
// not a fraction: one block of a hundred statements with a count of 55 is a
// hundred covered statements. Partial coverage is expressed as some blocks run and
// some did not, and a fixture that says otherwise tests a different metric than
// the one the gate reads.
func blocks(pkg, file string, n, stmts, covered int) []string {
	out := make([]string, 0, n)
	for i := range n {
		count := 0
		if i < covered {
			count = 7
		}
		out = append(out, mod+"/"+pkg+"/"+file+":"+itoa(i*10+1)+".1,"+itoa(i*10+9)+".2 "+itoa(stmts)+" "+itoa(count))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestAPackageBelowItsFloorIsFound(t *testing.T) {
	in := profile(
		mod+"/internal/rules/engine.go:10.1,12.2 3 0",
		mod+"/internal/rules/engine.go:14.1,16.2 2 5",
		mod+"/internal/rules/compile.go:20.1,22.2 5 0",
	)
	pkgs, err := covercheck.Parse(strings.NewReader(in), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "internal/rules" {
		t.Fatalf("expected one package named internal/rules, got %+v", pkgs)
	}
	if pkgs[0].Statements != 10 || pkgs[0].Covered != 2 {
		t.Fatalf("statements=%d covered=%d, want 10 and 2", pkgs[0].Statements, pkgs[0].Covered)
	}
	if got := pkgs[0].Percent(); got != 20 {
		t.Errorf("percent = %v, want 20", got)
	}

	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{
		"internal/rules": {Percent: 80, Measured: "2026-10-02"},
	}}
	findings := covercheck.Check(pkgs, b, "race")
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %v", len(findings), findings)
	}
	if findings[0].Rule != covercheck.RuleBelowFloor {
		t.Errorf("rule is %q, want %q", findings[0].Rule, covercheck.RuleBelowFloor)
	}
}

// TestAStaleFloorIsAlsoAFinding is the half that makes this a ratchet. A gate
// that only fails downwards is a gate somebody lowers once.
func TestAStaleFloorIsAlsoAFinding(t *testing.T) {
	in := profile(mod + "/internal/rules/engine.go:1.1,2.2 10 1")
	pkgs, _ := covercheck.Parse(strings.NewReader(in), mod+"/")

	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{
		"internal/rules": {Percent: 50, Measured: "2026-08-01"},
	}}
	findings := covercheck.Check(pkgs, b, "race")
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(findings), findings)
	}
	if findings[0].Rule != covercheck.RuleStaleFloor {
		t.Fatalf("rule is %q, want %q", findings[0].Rule, covercheck.RuleStaleFloor)
	}
	// The finding must name the number to raise it to, or raising the floor is a
	// measurement somebody has to redo rather than a one-line edit.
	if !strings.Contains(findings[0].Detail, "100.0") {
		t.Errorf("the finding does not name the number to record: %s", findings[0].Detail)
	}
}

func TestARiseInsideTheMarginIsNotAFinding(t *testing.T) {
	// 20 blocks of 1 statement, 11 covered: 55%.
	in := profile(blocks("internal/rules", "engine.go", 20, 1, 11)...)
	pkgs, _ := covercheck.Parse(strings.NewReader(in), mod+"/")
	if got := pkgs[0].Percent(); got != 55 {
		t.Fatalf("fixture is wrong: measured %v%%, want 55", got)
	}

	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{
		"internal/rules": {Percent: 53, Measured: "2026-10-02"},
	}}
	if f := covercheck.Check(pkgs, b, "race"); len(f) != 0 {
		t.Errorf("a two-point rise inside the margin failed the gate: %v", f)
	}

	// And a rise outside it is a finding, so the test above is testing the margin
	// and not the absence of the rule.
	b.Floors["internal/rules"] = covercheck.Floor{Percent: 50, Measured: "2026-10-02"}
	if f := covercheck.Check(pkgs, b, "race"); len(f) != 1 {
		t.Errorf("a five-point rise did not fail the gate: %v", f)
	}
}

func TestAPackageWithNoFloorIsAFinding(t *testing.T) {
	in := profile(mod + "/internal/newthing/new.go:1.1,2.2 4 4")
	pkgs, _ := covercheck.Parse(strings.NewReader(in), mod+"/")

	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{}}
	findings := covercheck.Check(pkgs, b, "race")
	if len(findings) != 1 || findings[0].Rule != covercheck.RuleUnknown {
		t.Fatalf("a package with no recorded floor was not reported: %v", findings)
	}
}

// TestAFloorForAPackageThatNoLongerExistsIsAFinding: an entry nobody maintains.
func TestAFloorForAPackageThatNoLongerExistsIsAFinding(t *testing.T) {
	in := profile(blocks("internal/rules", "engine.go", 2, 1, 2)...)
	pkgs, _ := covercheck.Parse(strings.NewReader(in), mod+"/")

	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{
		// 100% is exactly the measured value, so the package itself produces no
		// finding and the only one left is the stale entry.
		"internal/rules": {Percent: 100, Measured: "2026-10-02"},
		"internal/gone":  {Percent: 50, Measured: "2026-10-02"},
	}}
	findings := covercheck.Check(pkgs, b, "race")
	if len(findings) != 1 || findings[0].Rule != covercheck.RuleUnlisted {
		t.Fatalf("a floor for a package that no longer exists was not reported: %v", findings)
	}
}

// TestAnExternalTestPackageIsNotCountedAsThePackagesCoverage: its statements are
// the test's, and counting them would let a package meet its floor by testing
// itself from a neighbouring directory.
// TestAGeneratedFileIsNotMeasured: `securityv1` is entirely protoc output, and a
// floor for it is either zero — which teaches a reader to ignore the file — or a
// number that has to be raised every time a field is added to a proto.
func TestAGeneratedFileIsNotMeasured(t *testing.T) {
	in := profile(mod + "/internal/contracts/securityv1/action.pb.go:58.2,61.1 40 0")
	pkgs, err := covercheck.Parse(strings.NewReader(in), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("a generated file was measured: %+v", pkgs)
	}
}

// TestThePositionSuffixIsStripped guards the above in the direction that actually
// bit: the profile's first field is `file.go:line.col,line.col`, so a check on the
// file's extension is false for every line in the file.
func TestThePositionSuffixIsStripped(t *testing.T) {
	in := profile(mod + "/internal/rules/engine.go:58.2,61.1 3 0")
	pkgs, err := covercheck.Parse(strings.NewReader(in), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("a hand-written file was not measured at all: %+v", pkgs)
	}
	if pkgs[0].Statements != 3 {
		t.Errorf("statements = %d, want 3", pkgs[0].Statements)
	}
}

func TestAnExternalTestPackageIsNotCountedAsThePackagesCoverage(t *testing.T) {
	in := profile(
		mod+"/internal/rules/engine.go:1.1,2.2 10 1",
		mod+"/internal/rules_test/helpers.go:1.1,2.2 100 100",
	)
	pkgs, err := covercheck.Parse(strings.NewReader(in), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected one package, got %d: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Statements != 10 {
		t.Errorf("statements = %d, want 10: the external test package's blocks were counted",
			pkgs[0].Statements)
	}
}

// TestAnEmptyProfileIsAnError is the one this gate most needs, because the
// alternative is a run that reports every package as meeting a floor of zero.
func TestAnEmptyProfileIsAnError(t *testing.T) {
	pkgs, err := covercheck.Parse(strings.NewReader("mode: atomic\n"), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("an empty profile produced %d packages: %+v", len(pkgs), pkgs)
	}
}

func TestAMalformedLineIsAnError(t *testing.T) {
	for _, in := range []string{
		profile("this is not a coverage line"),
		profile(mod + "/internal/rules/engine.go:1.1,2.2 notanumber 1"),
		profile(mod + "/internal/rules/engine.go:1.1,2.2 1"),
	} {
		if _, err := covercheck.Parse(strings.NewReader(in), mod+"/"); err == nil {
			t.Errorf("a malformed profile parsed cleanly: %q", in)
		}
	}
}

// TestABaselineWithNoFloorsIsRefused, because a gate that overwrites its own
// baseline with an empty map has deleted the control and reported success.
func TestABaselineWithNoFloorsIsRefused(t *testing.T) {
	if err := (covercheck.Baseline{Floors: map[string]covercheck.Floor{}}).Save(t.TempDir() + "/floors.json"); err == nil {
		t.Error("an empty baseline was written")
	}
}

func TestDeriveRecordsWhatWasMeasured(t *testing.T) {
	// 3 blocks of 1 statement with 2 covered, plus 5 blocks of 1 with 5 covered:
	// 7 of 8 = 87.5%.
	in := profile(append(
		blocks("internal/rules", "engine.go", 3, 1, 2),
		blocks("internal/rules", "compile.go", 5, 1, 5)...)...)
	in = in + "\n" + strings.Join(blocks("cmd/securityctl", "main.go", 10, 10, 0), "\n") + "\n"
	pkgs, err := covercheck.Parse(strings.NewReader(in), mod+"/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := covercheck.Derive(pkgs, mod, "atomic", covercheck.DefaultMargin, "2026-10-02", "race")

	f, ok := b.Floors["internal/rules"]
	if !ok {
		t.Fatalf("internal/rules was not recorded: %+v", b.Floors)
	}
	if f.Percent != 87.5 {
		t.Errorf("percent = %v, want 87.5", f.Percent)
	}
	if f.Measured != "2026-10-02" {
		t.Errorf("measured = %q", f.Measured)
	}
	if _, ok := b.Floors["cmd/securityctl"]; ok {
		t.Error("cmd/ was recorded. The file says it is not listed, and a floor the file denies " +
			"existing is the kind of contradiction this repository keeps finding")
	}
}

// TestTheReportShowsTheMeasuredValue, because a floor nobody can see the measured
// value of is a floor nobody can tell is close to failing.
func TestTheReportShowsTheMeasuredValue(t *testing.T) {
	in := profile(blocks("internal/rules", "engine.go", 2, 1, 1)...)
	pkgs, _ := covercheck.Parse(strings.NewReader(in), mod+"/")
	b := covercheck.Baseline{Margin: 2, Floors: map[string]covercheck.Floor{
		"internal/rules": {Percent: 90, Measured: "2026-08-01"},
	}}
	out := covercheck.Report(pkgs, b)
	for _, want := range []string{"internal/rules", "50.0", "90.0", "2026-08-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not show %q:\n%s", want, out)
		}
	}
}
