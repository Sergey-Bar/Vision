package contractcompat_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// AGENTS.md names scripts/verify.ps1 as the gate and docs/status.md says it is
// the thing to trust. Nothing asserted that it exists, that every `go run` in it
// names a real package, or that every command it invokes is registered. Delete
// the file and the suite stays green - which is the failure mode AGENTS.md calls
// out by name: a gate that runs against nothing.
//
// The same reasoning as internal/dr's runbook test, pointed at the script instead
// of the runbook: a document an operator follows must name things that exist.

func TestTheGateScriptExists(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "verify.ps1")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scripts/verify.ps1 is the gate named by AGENTS.md and docs/status.md, and "+
			"it is not there: %v. Delete it and every suite stays green", err)
	}
	if len(body) == 0 {
		t.Fatal("scripts/verify.ps1 is empty")
	}
}

// Every package the script asks `go` to run or test has to exist. A gate that
// names a package which has been renamed or deleted fails at run time, with a
// Go error, hours later - and on a machine where the gate is the only thing
// that runs, that is a red gate nobody can interpret.
func TestEveryPackageTheGateNamesExists(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "verify.ps1"))
	if err != nil {
		t.Fatalf("read the gate: %v", err)
	}

	// `go run ./tools/x` and `go test ./internal/y/...`
	pattern := regexp.MustCompile(`\./[A-Za-z0-9_./-]+`)
	seen := map[string]bool{}
	for _, match := range pattern.FindAllString(string(body), -1) {
		pkg := strings.TrimSuffix(match, ".")
		if !seen[pkg] {
			seen[pkg] = true
			assertPackageExists(t, root, pkg)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the gate names no packages, which cannot be right")
	}
}

func assertPackageExists(t *testing.T, root, pkg string) {
	t.Helper()
	// A pattern like ./internal/... is satisfied by any directory under it; the
	// Go tool expands it, so the check is that the directory exists at all.
	trimmed := strings.TrimSuffix(pkg, "/...")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(trimmed)))
	if err == nil && info.IsDir() {
		return
	}

	// A package in the POC MODULE. That module has its own go.mod, so
	// `poc/active-monitoring/internal/store` is not addressable from this module's
	// root, and the `flake` gate reaches it with `go test -C poc/active-monitoring
	// ./internal/store/`. The path in the script is therefore relative to the
	// MODULE, not to the repository.
	//
	// Without this the check reports it as a package that does not exist, which is
	// false -- and it reported it about a package that exists, which is the failure
	// direction this check exists to catch. The fallback is deliberately ONE
	// specific module rather than a search: a suffix match anywhere in the tree would
	// make the check pass for a path that is genuinely wrong, which is the whole
	// defect it was written for.
	poc := filepath.Join(root, "poc", "active-monitoring", filepath.FromSlash(trimmed))
	if info, err := os.Stat(poc); err == nil && info.IsDir() {
		return
	}

	t.Errorf("the gate names %q, which is not a directory in this repository or in "+
		"poc/active-monitoring. A gate that names a package that does not exist is a gate "+
		"that fails for a reason unrelated to the code", pkg)
}

// AGENTS.md says `make verify` and `scripts/verify.ps1` are the same list, and
// docs/status.md repeats it. They were not: the Makefile's target was a
// hand-maintained list of the checks the script runs, it drifted twice, and the
// second time it was missing `statuscheck` - the one gate that fails when a
// number in docs/status.md is stale, so the place a stale count was guaranteed to
// be caught was the place that had stopped running.
//
// The fix was to stop writing the list twice: `make verify` runs the script, and
// every other target in the Makefile is a developer's way to run one thing. This
// test is what keeps that true, because an assertion nobody can weaken is the
// only kind that survives - a comment saying "keep these in sync" is exactly what
// was there before, in `_ = makefile`.
//
// It is verified in both directions on purpose:
//
//   - `verify` must invoke the script. A target that drifts back to a list of
//     `go test` invocations is the defect this test exists for.
//   - `ci` must delegate to `verify`. `ci` was a different list for months, which
//     is how "the gate" came to mean two different sets of checks.
func TestTheMakefileRunsTheGateScriptAndNothingElse(t *testing.T) {
	root := repoRoot(t)
	makefile := readRepo(t, filepath.Join(root, "Makefile"))
	script := readRepo(t, filepath.Join(root, "scripts", "verify.ps1"))

	verify := makefileTarget(t, makefile, "verify")
	if verify == "" {
		t.Fatal("the Makefile has no `verify` target, so `make verify` - named by AGENTS.md and " +
			"the roadmap documents - is a command that does not exist")
	}
	if !strings.Contains(verify, "verify.ps1") {
		t.Errorf("`make verify` does not run scripts/verify.ps1. It is:\n%s\n\nThe gate is "+
			"that script; a second list of the same checks in the Makefile has drifted twice, "+
			"and the second time it dropped `statuscheck`", strings.TrimSpace(verify))
	}
	// A target that only prints a message about the script is a target that has
	// stopped running it.
	if !verifyRegex.MatchString(verify) {
		t.Errorf("`make verify` mentions the script without invoking it. It is:\n%s",
			strings.TrimSpace(verify))
	}

	ci := makefileTarget(t, makefile, "ci")
	if ci == "" {
		t.Fatal("the Makefile has no `ci` target")
	}
	if got := makefilePrereqs(ci); got != "verify" {
		t.Errorf("`ci` depends on %q, want `verify`. They were different lists for months, "+
			"which is how 'the gate' came to mean two different sets of checks", got)
	}

	// The script must still be the script the document trusts: it exists, and it
	// still declares gates. A gate list of zero is the one result this whole file
	// was written to notice.
	if !strings.Contains(script, "Invoke-Gate") {
		t.Fatal("scripts/verify.ps1 declares no gates, so `make verify` would run nothing")
	}
	gates := scriptGates(script)
	if len(gates) < 20 {
		t.Fatalf("found %d gates in scripts/verify.ps1, which is not the thirty-odd the "+
			"documents name. Either the script or this reader is wrong", len(gates))
	}
}

// readRepo reads a file the assertions below need, and fails the test rather than
// skipping when it is absent. A missing Makefile used to `t.Skipf`, which meant
// deleting it turned this whole check off silently — the exact quiet-pass shape
// AGENTS.md describes for a gate listed in a document but not run.
func readRepo(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(body) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return string(body)
}

// makefilePrereqs returns what a target block's first line depends on, with the
// `name:` part removed. It is how the `ci` check reads a one-line target.
func makefilePrereqs(block string) string {
	first := block
	if i := strings.IndexByte(block, '\n'); i >= 0 {
		first = block[:i]
	}
	_, after, found := strings.Cut(first, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(after)
}

// makefileTarget returns a target's whole block from the Makefile - its
// `name: prerequisites` line and the recipe lines under it - or "" when the
// target is not there at all. A target that exists but has no recipe (a
// pass-through like `ci: verify`) still has a block, and that is the difference
// this function keeps.
func makefileTarget(t *testing.T, makefile, name string) string {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	prefix := name + ":"
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var recipe []string
		for _, next := range lines[i+1:] {
			if next == "" {
				continue
			}
			if !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
				break
			}
			recipe = append(recipe, strings.TrimSpace(next))
		}
		if len(recipe) == 0 {
			return line
		}
		return line + "\n" + strings.Join(recipe, "\n")
	}
	return ""
}

// scriptGates returns the gate names the script declares, de-duplicated because
// `lint`, `generated-code` and `ai-runtime` are registered inside if/else and so
// appear twice as one gate.
func scriptGates(script string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range gateNameRe.FindAllStringSubmatch(script, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

var (
	// verifyRegex matches a command that actually runs the script. A `verify`
	// recipe that only echoes an error message would pass the "mentions
	// verify.ps1" check above while running nothing.
	verifyRegex = regexp.MustCompile(`(?m)^\s*(?:@\s*)?\$\(PWSH\)|(?m)^\s*(?:@\s*)?(?:pwsh|powershell)\b`)
	gateNameRe  = regexp.MustCompile(`Invoke-Gate\s+'([a-z0-9-]+)'`)
)
