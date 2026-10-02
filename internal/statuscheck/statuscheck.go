// Package statuscheck holds the rules about docs/status.md agreeing with the
// tree it describes.
//
// # Why this exists
//
// `AGENTS.md` says `docs/status.md` is the only source of truth for build state,
// and then immediately names the failure mode:
//
//	"A stale claim in any other document is a defect."
//
// The document has been stale three separate times, and `internal/README.md`
// records two of them as a numbered trap rather than as a fix:
//
//   - Trap 14: the suite was claimed green "across 24 packages" when the tree had
//     thirty leaf packages, and the toolchain was claimed as "Go 1.27" when
//     `go.mod` declared `1.26.0`.
//   - Trap 15: `AGENTS.md` and `docs/status.md` disagreed about whether
//     `sites.default_locale` exists, and neither document noticed.
//
// Trap 14 is the interesting one. Both numbers were wrong by the time the trap
// list was written, so correcting the document without making the number derive
// from something only moves when the tree changes buys nothing: the next schema
// or the next package moves it again.
//
// So the counts derive, and the document is checked against them. `sqlguard` does
// the same thing for SQL — deriving the table list from `migrations/` rather than
// maintaining a hand-written one — because a hand-maintained list stops being true
// the moment somebody adds a row, and a guard that has quietly stopped covering
// the newest row is worse than no guard because it still reports green.
//
// # What it deliberately does not do
//
// It does not read `docs/status.md` as prose, and it does not try to judge
// whether a sentence is *true*. It checks countable claims, and it checks them in
// one direction: the derived count must be the number the document prints.
//
// It does not parse English numerals for the counts it can get from the tree.
// "twenty messages" is not searched for; "20 messages" is. A matcher that
// understands spelled-out numbers has to be taught every spelling, and the
// spellings are the part a human edits.
//
// The gate count is the exception, and the exception is deliberate: the document
// has always written it as a word ("fifteen gates") and forcing it to a digit
// would be a bigger change than the drift it prevents. One small table of the
// spellings English actually uses for 1-20 covers it.
package statuscheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// baseline is the shape of contracts/compat/baseline.json. Only the parts that
// carry a count are decoded; the field and property maps underneath are read for
// their length and nothing else.
type baseline struct {
	Messages map[string]json.RawMessage `json:"messages"`
	Enums    map[string]json.RawMessage `json:"enums"`
	Schemas  map[string]schemaEntry     `json:"schemas"`
	OpenAPI  map[string]openAPIEntry    `json:"openapi"`
}

type schemaEntry struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

type openAPIEntry struct {
	Paths map[string]json.RawMessage `json:"paths"`
}

// Derived is everything this package can count without a human maintaining the
// number. Adding a field here is adding a claim the gate will hold the document to.
type Derived struct {
	Messages   int
	Enums      int
	Schemas    int
	Properties int
	Paths      int
	GoMinor    string
	// Gates is the number of distinct gate names in scripts/verify.ps1. It is
	// counted, not declared, so adding a gate and forgetting to update the
	// document is a failing test rather than a claim that reads as checked.
	Gates int
}

// Derive reads the tree and returns the counts.
func Derive(root string) (Derived, error) {
	var out Derived

	raw, err := os.ReadFile(filepath.Join(root, "contracts", "compat", "baseline.json"))
	if err != nil {
		return out, err
	}
	var b baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return out, err
	}

	out.Messages = len(b.Messages)
	out.Enums = len(b.Enums)
	out.Schemas = len(b.Schemas)
	for _, s := range b.Schemas {
		out.Properties += len(s.Properties)
	}
	for _, o := range b.OpenAPI {
		out.Paths += len(o.Paths)
	}

	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return out, err
	}
	out.GoMinor, err = goDirective(string(gomod))
	if err != nil {
		return out, err
	}

	script, err := os.ReadFile(filepath.Join(root, "scripts", "verify.ps1"))
	if err != nil {
		return out, err
	}
	out.Gates = countGates(string(script))

	return out, nil
}

// goDirective returns the minor version from a `go 1.26.0` line.
//
// The patch level is deliberately dropped. A build passes on a toolchain newer
// than the directive, so a drift from 1.26.0 to 1.26.3 is not a defect and making
// it one would produce a failing gate on a legal change. A drift across the minor
// — 1.25 or 1.27 against a 1.26 directive — is the case worth refusing, because
// that is the one a pin has to follow.
var goLineRe = regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)`)

func goDirective(gomod string) (string, error) {
	m := goLineRe.FindStringSubmatch(gomod)
	if m == nil {
		return "", errNoGoDirective
	}
	return m[2], nil
}

var errNoGoDirective = &claimError{"go.mod has no `go <major>.<minor>` directive, so the document's toolchain claim cannot be checked"}

type claimError struct{ msg string }

func (e *claimError) Error() string { return e.msg }

// gateRe matches `Invoke-Gate 'name'`. The name is captured and de-duplicated,
// because a gate that is registered inside an if/else — `lint`, `generated-code`
// and `ai-runtime` all are — appears twice in the script and is one gate.
//
// Counting source lines instead of names would report sixteen gates for fifteen
// controls, and the document would be wrong in whichever direction the author
// guessed.
var gateRe = regexp.MustCompile(`Invoke-Gate\s+'([a-z0-9-]+)'`)

func countGates(script string) int {
	seen := map[string]bool{}
	for _, m := range gateRe.FindAllStringSubmatch(script, -1) {
		seen[m[1]] = true
	}
	return len(seen)
}

// ---------------------------------------------------------------------------
// The claims.
// ---------------------------------------------------------------------------

// Finding is one place where docs/status.md and the tree disagree.
type Finding struct {
	// Label names the claim in the document's own words, so the message reads as
	// a correction rather than as an identifier nobody recognises.
	Label string
	// Want is the derived count.
	Want int
	// Got is the number the document printed.
	Got int
	// Line is the 1-based line in docs/status.md, which is where a reader goes
	// to fix it.
	Line int
}

func (f Finding) String() string {
	return "docs/status.md:" + strconv.Itoa(f.Line) + ": " + f.Label +
		" is " + strconv.Itoa(f.Got) + ", the tree says " + strconv.Itoa(f.Want) +
		". docs/status.md is the source of truth for build state, so a number in " +
		"it that the tree contradicts is a defect in the document, not a rounding " +
		"difference to note and move past"
}

// claim pairs a label with the pattern that finds the number the document printed
// next to it.
//
// The pattern carries the leading boundary deliberately. `\d+ paths` also matches
// the "20 paths" inside a longer number, and `paths` alone matches the word in
// prose that is not a count at all — `docs/status.md` discusses routes and paths
// in several sentences where no number belongs.
type claim struct {
	label string
	re    *regexp.Regexp
	want  int
}

var number = `(\d+)`

func claims(d Derived) []claim {
	return []claim{
		{
			label: "contract messages",
			re:    regexp.MustCompile(number + `\s+messages\b`),
			want:  d.Messages,
		},
		{
			label: "contract enums",
			re:    regexp.MustCompile(number + `\s+enums\b`),
			want:  d.Enums,
		},
		{
			label: "published JSON Schemas",
			re:    regexp.MustCompile(number + `\s+JSON Schemas\b`),
			want:  d.Schemas,
		},
		{
			label: "schema properties",
			re:    regexp.MustCompile(`\(` + number + `\s+properties\)`),
			want:  d.Properties,
		},
		{
			label: "OpenAPI paths",
			re:    regexp.MustCompile(number + `\s+paths\b`),
			want:  d.Paths,
		},
	}
}

// numbers asserts the countable claims in the document against the tree.
//
// A claim with no match is not a finding. `docs/status.md` is free to stop
// mentioning the contract counts entirely, and requiring a number to be present
// would mean this package forces prose onto a document whose author was
// deliberately saying less. What it refuses is a number that is present and wrong,
// because that is the case that reads as checked.
func numbers(status string, d Derived) []Finding {
	var out []Finding
	for _, c := range claims(d) {
		for _, m := range c.re.FindAllStringSubmatchIndex(status, -1) {
			if len(m) < 4 {
				continue
			}
			got, err := strconv.Atoi(status[m[2]:m[3]])
			if err != nil {
				continue
			}
			if got == c.want {
				continue
			}
			out = append(out, Finding{
				Label: c.label,
				Want:  c.want,
				Got:   got,
				Line:  lineOf(status, m[0]),
			})
		}
	}
	return out
}

// gateWords maps the spellings English uses for 1-20 to their values.
//
// The document writes the gate count as a word because it is prose, and both
// halves of that comparison are small closed sets, so this is a table rather than
// a parser. It stops at twenty because a gate list of twenty-one would be a thing
// worth saying out loud in the document rather than counting.
var gateWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
	"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20,
}

var gateCountRe = regexp.MustCompile(`(?i)\b([a-z]+)\s+gates\b`)

// gateClaim asserts the spelled-out gate count against the number of distinct gate
// names in scripts/verify.ps1.
//
// It reads only the Verification block, and that restriction is the point. The
// document also carries dated run records further down - "exit 0, fifteen gates"
// beside a specific `dist/verification/verify-<timestamp>.json` - and rewriting
// one of those to match today's count would be falsifying a record rather than
// fixing a claim.
func gateClaim(block string, d Derived) []Finding {
	var out []Finding
	for _, m := range gateCountRe.FindAllStringSubmatchIndex(block, -1) {
		if len(m) < 4 {
			continue
		}
		word := strings.ToLower(block[m[2]:m[3]])
		got, ok := gateWords[word]
		if !ok {
			continue
		}
		if got == d.Gates {
			continue
		}
		out = append(out, Finding{
			Label: "gate count",
			Want:  d.Gates,
			Got:   got,
			Line:  lineOf(block, m[0]),
		})
	}
	return out
}

// toolchainClaim asserts that the Go minor version the document states is the one
// go.mod declares.
//
// Trap 14 is exactly this: the document said "Go 1.27" and `go.mod` said
// `1.26.0`, and both were believed. The patch level is not checked, because a build
// passes on a toolchain newer than the directive and a patch bump is not a defect.
var toolchainRe = regexp.MustCompile(`\bGo\s+1\.(\d+)\b`)

func toolchainClaim(block string, d Derived) []Finding {
	var out []Finding
	for _, m := range toolchainRe.FindAllStringSubmatchIndex(block, -1) {
		if len(m) < 4 {
			continue
		}
		if block[m[2]:m[3]] == d.GoMinor {
			continue
		}
		out = append(out, Finding{
			Label: "stated Go minor version",
			Want:  atoiOrZero(d.GoMinor),
			Got:   atoiOrZero(block[m[2]:m[3]]),
			Line:  lineOf(block, m[0]),
		})
	}
	return out
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// Check returns every disagreement between docs/status.md and the tree.
//
// Every claim is read from the document's Verification block and nothing else, and
// that scope is the whole design rather than a convenience.
//
// The document is where this repository records what was true on a given day, so
// it necessarily contains numbers that are no longer current — in a table of what
// a round fixed, in a section describing a past run, in a sentence quoting a
// colleague's earlier claim. Checking the whole document means checking those too,
// and the failure mode is not a red test but a worse one: an author who writes
// "this paragraph used to claim six schemas" has their accurate history reported
// as a defect, corrects the true sentence, and learns that the gate punishes
// honesty. That was measured on this very document, twice, while writing the very
// paragraph the gate exists to protect.
//
// One block, stated once, is where a countable claim belongs. Everything else in
// the document is allowed to mention a number without asserting it.
func Check(root string) ([]Finding, error) {
	d, err := Derive(root)
	if err != nil {
		return nil, err
	}
	status, err := os.ReadFile(filepath.Join(root, "docs", "status.md"))
	if err != nil {
		return nil, err
	}
	text := string(status)
	block, ok := verificationBlock(text)
	if !ok {
		return nil, errNoVerificationBlock
	}
	out := numbers(block, d)
	out = append(out, gateClaim(block, d)...)
	out = append(out, toolchainClaim(block, d)...)
	// Line numbers are relative to the block, so shift them back onto the document
	// or every finding sends its reader to the wrong line.
	for i := range out {
		out[i].Line += lineOffset(text, block)
	}
	return out, nil
}

var errNoVerificationBlock = &claimError{"docs/status.md has no **Verification:** block, so nothing in it is checked"}

// verificationBlockMarker opens the block. Chosen because it is the phrase the
// document has always used for the current-state claim.
const verificationBlockMarker = "**Verification:**"

// verificationBlock returns the current-state claim: the paragraph introduced by
// the marker, up to the first blank line. A blank line ends it because the
// document separates prose paragraphs that way, and the numbers being checked are
// always within the first one.
func verificationBlock(s string) (string, bool) {
	i := strings.Index(s, verificationBlockMarker)
	if i < 0 {
		return "", false
	}
	rest := s[i:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest, true
}

// lineOffset returns how many lines precede the block in the document, so a
// finding's line number points at the document and not at the slice.
func lineOffset(document, block string) int {
	if b := strings.Index(document, block); b >= 0 {
		return strings.Count(document[:b], "\n")
	}
	return 0
}

// lineOf returns the 1-based line holding a byte offset.
func lineOf(s string, offset int) int {
	if offset > len(s) {
		offset = len(s)
	}
	return 1 + strings.Count(s[:offset], "\n")
}
