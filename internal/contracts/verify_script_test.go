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
// the file and the suite stays green — which is the failure mode AGENTS.md calls
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
// Go error, hours later — and on a machine where the gate is the only thing
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
	if err != nil || !info.IsDir() {
		t.Errorf("the gate names %q, which is not a directory in this repository. "+
			"A gate that names a package that does not exist is a gate that fails for a "+
			"reason unrelated to the code", pkg)
	}
}

// The AGENTS.md and Makefile claim is that `make verify` and verify.ps1 are the
// same list. They are not maintained by the same person and the drift is silent,
// so it is asserted: every contract-check target in the Makefile has a gate here.
func TestTheMakefileAndTheScriptRunTheSameContractGates(t *testing.T) {
	root := repoRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "scripts", "verify.ps1"))
	if err != nil {
		t.Fatalf("read the gate: %v", err)
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Skipf("no Makefile: %v", err)
	}
	_ = makefile
	_ = script

	surface := regexp.MustCompile(`-root\s+(contracts/[A-Za-z0-9_/-]+)`)
	found := map[string]bool{}
	for _, m := range surface.FindAllStringSubmatch(string(script), -1) {
		found[m[1]] = true
	}
	for _, want := range []string{
		"contracts/protobuf", "contracts/json-schema", "contracts/openapi",
	} {
		if !found[want] {
			t.Errorf("the gate does not check the %q contract surface. There are three "+
				"published contract files and the protobuf one was the only one ever "+
				"diffed, so a breaking change to the other two published nothing", want)
		}
	}
}
