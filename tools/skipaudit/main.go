// Command skipaudit reads a `go test -json` stream and fails when the run skipped
// a test it was supposed to run.
//
// It is a separate binary rather than a test in `internal/skipaudit` because the
// question it answers is about *other* packages. A test can only see its own
// binary's events, and the whole defect is that a package's skips are invisible
// from inside it: `ok` for a package whose every test skipped is `ok`.
//
// Usage:
//
//	go test -json ./... > run.json
//	go run ./tools/skipaudit -input run.json
//
// `-allow-file` points at a JSON array of declared skips, so the exemptions are
// data rather than a second hard-coded list. The default is
// `internal/skipaudit/declared-skips.json`, and a missing file is a refusal rather
// than an empty allow-list: a gate whose exemptions are supplied by a file that
// silently does not exist is a gate with an unbounded allow-list and no gate.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Sergey-Bar/vision/internal/skipaudit"
)

func main() {
	os.Exit(run())
}

func run() int {
	var input, allowFile, moduleFile string
	flag.StringVar(&input, "input", "", "Path to a `go test -json` stream. Required.")
	flag.StringVar(&allowFile, "allow-file", "internal/skipaudit/declared-skips.json",
		"Path to the declared-skip allow-list.")
	flag.StringVar(&moduleFile, "go-mod", "go.mod",
		"Path to go.mod, whose module line is stripped from the stream's package names.")
	flag.Parse()

	if input == "" {
		fmt.Fprintln(os.Stderr, "skipaudit: -input is required")
		return 2
	}

	exempt, err := loadAllowList(allowFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipaudit: %v\n", err)
		return 1
	}

	module, err := readModulePath(moduleFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipaudit: %v\n", err)
		return 1
	}

	f, err := os.Open(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipaudit: open %s: %v\n", input, err)
		return 1
	}
	defer func() { _ = f.Close() }()

	res, err := skipaudit.Check(f, exempt, runtime.GOOS, module)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipaudit: %v\n", err)
		return 1
	}

	fmt.Print(res.Report(exempt))
	if res.Failed() {
		fmt.Fprintln(os.Stderr,
			"skipaudit: FAILED. A skipped test is a suite whose assertions did not run, and a run "+
				"that skipped one is not a run that tested the product.")
		return 1
	}
	fmt.Println("skipaudit: ok. Nothing undeclared was skipped, and every declared skip that applies " +
		"to this platform fired.")
	return 0
}

// loadAllowList reads the declared skips. The file's shape is
// `{"note": [...], "declared": [{"test": "package.Test", "why": "..."}]}`.
//
// A missing file is an error, not an empty list. That is the whole reason the
// exemptions are data: an absent file must not read as "nothing is excused",
// because a contributor on a fresh checkout would get a gate that fails on the
// one legitimate platform skip and would then be tempted to delete the gate.
func loadAllowList(path string) (map[string]skipaudit.Expected, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the skip allow-list %s: %w. It is not optional: a missing "+
			"allow-list is an unbounded one, and this gate exists to stop a green run from "+
			"meaning nothing", path, err)
	}
	var file struct {
		Note     []string `json:"note"`
		Declared []struct {
			Test string `json:"test"`
			When string `json:"when"`
			Why  string `json:"why"`
		} `json:"declared"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entries := file.Declared
	out := make(map[string]skipaudit.Expected, len(entries))
	for _, e := range entries {
		if e.Test == "" {
			return nil, fmt.Errorf("%s: an entry names no test", path)
		}
		// A package-level entry is refused, not honoured.
		//
		// The file says "one test per entry, named `package.Test`, never a
		// package", and the gate says the same. Neither enforced it, so one line —
		// `{"test": "internal/evidence", "why": "..."}` — would excuse *every* skip
		// in that package, including the twenty database-backed ones that exist for
		// a test suite to skip when it cannot reach Postgres. A rule that is written
		// down in two places and checked in neither is the condition this repository
		// keeps finding, and this is the gate that finds it.
		if !strings.Contains(e.Test, ".") {
			return nil, fmt.Errorf("%s: %q names a package, not a test. A package-level entry excuses "+
				"every skip in it, including the database-backed suites that skip when no database is "+
				"configured, which is the whole class this gate exists to catch. Name it "+
				"`package.Test`", path, e.Test)
		}
		if _, dup := out[e.Test]; dup {
			return nil, fmt.Errorf("%s: %s is declared twice", path, e.Test)
		}
		out[e.Test] = skipaudit.Expected{Test: e.Test, When: e.When, Why: e.Why}
	}
	return out, nil
}

// readModulePath reads the module line out of go.mod.
//
// It is the only thing in this tool that has to parse a file by hand, and it is
// five lines because `go.mod`'s grammar is not worth a dependency: a `module`
// directive, optionally quoted, at the start of a line outside any block.
func readModulePath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || rest == line {
			continue
		}
		rest = strings.TrimSpace(rest)
		rest = strings.Trim(rest, `"`)
		if rest != "" {
			return rest, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return "", fmt.Errorf("%s has no module directive, so package names cannot be shortened. "+
		"This is not a pass: without it every declared skip fails to match its own stream entry "+
		"and the gate reports the whole list at once", path)
}
