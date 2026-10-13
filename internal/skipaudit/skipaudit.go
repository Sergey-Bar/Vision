// Package skipaudit turns a test run's own record of what it skipped into a
// verdict, so a green run cannot quietly mean a run that tested almost nothing.
//
// # Why this exists
//
// The database-backed suites skip rather than fail when `SP_TEST_DATABASE_URL` is
// absent. That is the right behaviour for a contributor without Postgres, and it
// is also how a verification run passes while testing a third of what it claims.
// `internal/README.md` names this as trap 2. The verification record already
// carries `database.urlSet` for exactly this reason, but a field in a JSON file at
// the end of a run is not a gate — nothing fails, and the reader has to know to
// look.
//
// The rules this package applies, in order:
//
//   - **A package whose test binary skipped is a failure.** Not a warning. The
//     whole point of the skip is that the suite's assertions did not run, and the
//     assertions are the thing the run is for.
//   - **The skip is named.** A failure that says "something skipped" sends the
//     reader to grep; a failure that says which package and which reason is
//     answerable in a minute.
//   - **An expected skip has to be declared, with a reason, and it is printed on
//     every run.** There are a handful of legitimately conditional tests — a
//     platform-specific one, a golden test that needs a corpus the developer has
//     not generated. Each is named here. A list that grows silently is a list that
//     has stopped meaning anything, and a list whose entries are printed on every
//     run is a list somebody re-reads before adding to.
//
// # What it reads
//
// `go test -json` output, one JSON object per line, streamed. It reads the stream
// rather than a summary because the summary does not say what was skipped: `ok`
// for a package whose every test skipped is `ok`.
//
// The three actions it acts on are `skip` (a test skipped), `pass` (a package's
// test binary completed) and `fail`. A package is reported as skipped when any of
// its tests skipped; a test binary that fails outright is the `test` gate's
// business, not this one's, and is left alone.
package skipaudit

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf16"
)

// Expected is one declared, acceptable skip, and the reason is mandatory.
type Expected struct {
	// Test is the test function's name, without the package prefix. It is the
	// function name rather than a package name because a skip is a property of a
	// test, and a package-level entry would excuse every future skip in it.
	Test string
	// When lists the GOOS values this declaration applies to, comma-separated, or is
	// empty for "every platform".
	//
	// It exists because a platform-gated skip is conditional in both directions.
	// `TestSymlinkEscapeIsRefused` skips on Windows and runs on Linux, so a
	// declaration with no platform would be *unused* on Linux — and the
	// unused-declaration check would fail every verification run on the platform
	// where the control actually runs. That is not a stale entry; it is a
	// declaration that is doing its job on the other machine. A gate that can only
	// be satisfied on one platform is a gate that gets disabled on the other.
	When string
	// Why must say why this test is allowed to skip. An empty reason is a
	// refusal, and a package-level skip allowance is not accepted at all.
	Why string
}

// appliesOn reports whether this declaration is in force on goos.
func (e Expected) appliesOn(goos string) bool {
	if strings.TrimSpace(e.When) == "" {
		return true
	}
	for _, g := range strings.Split(e.When, ",") {
		if strings.EqualFold(strings.TrimSpace(g), goos) {
			return true
		}
	}
	return false
}

// Event is one line of `go test -json` output, narrowed to the fields that
// matter here. The upstream type has thirty; reading three of them keeps this
// package's dependency on the format small and obvious.
type Event struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Elapsed float64
	// Output carries the skip message for `skip`-adjacent actions.
	Output string
}

// Result is the verdict over one test run.
type Result struct {
	// Skipped is every test that skipped, as "package.Test".
	Skipped []string
	// Reasons is the skip message for each, keyed the same way. Empty when the
	// test emitted none, which is legal but worth seeing.
	Reasons map[string]string
	// Expected is every declared skip that actually happened.
	Expected []string
	// ExpectedUnused is every declared skip that did not happen. A declaration
	// for a test that no longer skips is dead weight in an allow-list, and the
	// gate that has it is not the gate anybody thinks it is.
	ExpectedUnused []string
	// Packages is how many distinct test binaries reported.
	Packages int
	// NoTestFiles is how many packages reported "[no test files]" and so were never
	// tested at all. Reported rather than dropped: a package with no tests is a
	// coverage hole, and the gate that counts skips is the natural place to say
	// how many there are.
	NoTestFiles int
	// Passed is how many passed with no skip at all.
	Passed int
}

// noTestFilesMarker is the toolchain's own wording for "this package has no test
// files". Matched as a substring because the line also carries the package name
// and tab padding that vary between Go versions.
const noTestFilesMarker = "[no test files]"

// Failed reports whether the run is acceptable.
func (r Result) Failed() bool {
	return len(r.Skipped) > 0 || len(r.ExpectedUnused) > 0 || r.Packages == 0
}

// Report renders the verdict. The expected-skips list is printed on every run
// whether or not it fired, for the reason in the package comment: an exemption
// list that surfaces only when it is used is one nobody reads before widening.
func (r Result) Report(exempt map[string]Expected) string {
	var b strings.Builder
	if r.Packages == 0 {
		b.WriteString("    no test binary reported at all. This is not a pass: the stream the gate " +
			"read contained no package results, so it checked nothing.\n")
	}
	fmt.Fprintf(&b, "    %d package(s) ran tests, %d with nothing skipped, "+
		"%d package(s) have no test files at all\n",
		r.Packages, r.Passed, r.NoTestFiles)

	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "    %d skipped test(s) that were not declared. A skip is a suite whose "+
			"assertions did not run:\n", len(r.Skipped))
		for _, s := range r.Skipped {
			line := "      " + s
			if why := r.Reasons[s]; why != "" {
				line += ": " + firstLine(why)
			}
			b.WriteString(line + "\n")
		}
	}
	if len(exempt) > 0 {
		// The whole declared list, not the part of it that fired, on every run.
		// A skip allowance is a standing statement about a test — "this one does
		// not apply here" — and a standing statement nobody re-reads is a standing
		// statement that gets widened without anybody noticing.
		keys := make([]string, 0, len(exempt))
		for k := range exempt {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "    declared skips (%d), printed whether or not they fired:\n", len(keys))
		for _, k := range keys {
			scope := ""
			if when := strings.TrimSpace(exempt[k].When); when != "" {
				scope = " [" + when + "]"
			}
			b.WriteString("      " + k + scope + " -- " + exempt[k].Why + "\n")
		}
	}
	if len(r.ExpectedUnused) > 0 {
		b.WriteString("    declared skip(s) that did not happen. Delete the declaration rather than " +
			"leaving it to look like a decision that was made:\n")
		for _, s := range r.ExpectedUnused {
			b.WriteString("      " + s + "\n")
		}
	}
	return b.String()
}

// Check reads a `go test -json` stream and returns the verdict.
//
// `goos` is the host's `runtime.GOOS`. It is passed rather than read so that a
// declaration's platform scope is a parameter a test can set, which is the only
// way to test the rule in both directions on one machine.
//
// `exempt` names the skips that are allowed. An entry with no reason is refused
// here rather than honoured, which is the only way an exemption list stays
// meaningful: the cost of a bad entry is a failing gate, not a silent widening.
//
// `modulePrefix` is the module path from `go.mod`, without a trailing slash, and
// it is stripped from every package name before anything else looks at one. `go
// test -json` reports `github.com/Sergey-Bar/vision/internal/evidence`; the
// declaration list, and every human reading a failure, says
// `internal/evidence`. Leaving the prefix on would mean every declared skip
// silently fails to match its own skip, and the gate would report all four at once
// and look broken — which is how a gate loses its reader.
func Check(r io.Reader, exempt map[string]Expected, goos, modulePrefix string) (Result, error) {
	if modulePrefix != "" {
		modulePrefix = strings.TrimSuffix(modulePrefix, "/") + "/"
	}
	res := Result{Reasons: map[string]string{}}

	decoded, err := decodeStream(r)
	if err != nil {
		return res, err
	}

	fired := map[string]bool{}
	pkgs := map[string]bool{}
	// noTests holds packages whose binary reported "[no test files]".
	//
	// `go test` emits a package-level `skip` for those, and it is not a skip: it
	// is how the toolchain says a package has no tests. Counting them would put
	// four permanent, unfixable findings in the allow-list on day one, and an
	// allow-list with entries that can never be deleted is an allow-list nobody
	// reads. The signal is the output line immediately before the skip.
	noTests := map[string]bool{}
	sc := bufio.NewScanner(decoded)
	// A `-v` suite's Output events carry whole test logs, and a single one can be
	// longer than the default 64KiB line limit. The default limit turned a long
	// golden test's log line into a scanner failure, and a scanner failure reads
	// as "no results", which is the one thing this gate must never report.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	// lastPackageOutput is the most recent package-level output line, which is
	// where the toolchain puts both the "no test files" notice and a package-level
	// skip's explanation.
	lastPackageOutput := map[string]string{}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			// Not a JSON event: `go test` writes build output to stderr and a
			// warning line to stdout on some toolchains. Skipped rather than
			// failed, because failing on a toolchain's banner would make the gate
			// about the banner.
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return res, fmt.Errorf("parse go test -json line: %w", err)
		}
		if ev.Package == "" {
			continue
		}
		pkg := strings.TrimPrefix(ev.Package, modulePrefix)
		pkgs[pkg] = true

		switch ev.Action {
		case "skip":
			if ev.Test == "" {
				// A package-level skip. Two very different things arrive here and
				// they must not be confused:
				//
				//   - "[no test files]", which is how the toolchain says a package
				//     has no tests. Not a skip at all.
				//   - a TestMain that called `t.Skip`, or a build that produced no
				//     runnable binary. That is the whole binary declining to run,
				//     and it is the worst case, so it is named as the package
				//     rather than silently dropped.
				if strings.Contains(lastPackageOutput[pkg], noTestFilesMarker) {
					noTests[pkg] = true
					continue
				}
				key := pkg
				if _, seen := res.Reasons[key]; !seen {
					res.Reasons[key] = lastPackageOutput[pkg]
				}
				fired[key] = true
				continue
			}
			key := pkg + "." + ev.Test
			fired[key] = true
			if ev.Output != "" {
				res.Reasons[key] = ev.Output
			}
		case "output":
			// A skip's message arrives as an `output` event immediately before the
			// `skip` event, so it is buffered on the package and attached when the
			// skip lands. Anything else is the gate's own business.
			if ev.Test != "" {
				res.Reasons[pkg+"."+ev.Test] = ev.Output
			} else {
				lastPackageOutput[pkg] = ev.Output
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("read go test -json stream: %w", err)
	}

	// A package with no test files reported, and so never entered `pkgs`. The
	// summary says "N packages reported" and that number should not include the
	// ones that were never tested, so they are counted separately rather than
	// silently inflating the total.
	for pkg := range noTests {
		delete(pkgs, pkg)
	}
	res.Packages = len(pkgs)
	res.NoTestFiles = len(noTests)
	for key := range fired {
		if _, ok := exempt[key]; ok {
			res.Expected = append(res.Expected, key)
			continue
		}
		res.Skipped = append(res.Skipped, key)
	}
	sort.Strings(res.Skipped)
	sort.Strings(res.Expected)

	for key, ex := range exempt {
		if !ex.appliesOn(goos) {
			// A declaration for another platform. It is neither a skip to excuse
			// nor a stale entry to delete: on this machine the test runs, which is
			// the whole point of the control.
			continue
		}
		if !fired[key] {
			res.ExpectedUnused = append(res.ExpectedUnused, key)
		}
	}
	sort.Strings(res.ExpectedUnused)

	for key, ex := range exempt {
		if strings.TrimSpace(ex.Why) == "" {
			return res, fmt.Errorf(
				"the skip allow-list excuses %s without saying why, which is not an excuse", key)
		}
	}

	res.Passed = res.Packages - countPackages(res.Skipped)
	return res, nil
}

// decodeStream normalises the byte order of the stream.
//
// Not a portability nicety: this gate's only producer is `scripts/verify.ps1`,
// and Windows PowerShell 5.1's `>` redirection writes **UTF-16LE with a BOM**.
// Without this the gate reads a stream of `{\0"\0T\0` fragments, finds no JSON,
// reports "no test binary reported at all", and fails every verification run on
// the developer's machine while passing on a shell that writes UTF-8. A gate that
// only works on one shell is a gate that gets switched off on the other.
//
// UTF-8 with or without a BOM, and UTF-16LE or BE with a BOM, are all accepted.
// Anything else is passed through untouched, because guessing at an unknown
// encoding would turn a malformed stream into a stream that looks fine.
func decodeStream(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(4)
	if err != nil && len(head) == 0 {
		// An empty stream is an empty stream. The caller reports it.
		return br, nil
	}

	switch {
	case len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE:
		return bytes.NewReader(decodeUTF16(br, binary.LittleEndian)), nil
	case len(head) >= 2 && head[0] == 0xFE && head[1] == 0xFF:
		return bytes.NewReader(decodeUTF16(br, binary.BigEndian)), nil
	case len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF:
		if _, err := br.Discard(3); err != nil {
			return nil, fmt.Errorf("discard the UTF-8 BOM: %w", err)
		}
	}
	return br, nil
}

// decodeUTF16 reads the rest of br as UTF-16 in the given order.
//
// The standard library's decoder, which pairs surrogates and replaces an unpaired
// one with U+FFFD. A replacement rather than an error is the right behaviour for
// a stream whose last line may be truncated by a console: refusing the whole run
// over one bad unit would make this gate a source of false failures.
func decodeUTF16(br *bufio.Reader, order binary.ByteOrder) []byte {
	raw, err := io.ReadAll(br)
	if err != nil {
		raw = nil
	}
	// Two bytes per unit, and an odd trailing byte is not a unit.
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, order.Uint16(raw[i:i+2]))
	}
	var out bytes.Buffer
	for _, r := range utf16.Decode(units) {
		out.WriteRune(r)
	}
	return out.Bytes()
}

// countPackages is how many distinct packages appear in a list of "package.Test"
// keys. Used only for the summary line, so an over-count after a package-level
// skip is not worth a second parse.
func countPackages(keys []string) int {
	seen := map[string]bool{}
	for _, k := range keys {
		if i := strings.LastIndex(k, "."); i > 0 {
			seen[k[:i]] = true
		} else {
			seen[k] = true
		}
	}
	return len(seen)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
