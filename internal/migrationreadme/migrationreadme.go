// Package migrationreadme keeps `migrations/README.md` in step with the directory
// it describes.
//
// # Why this exists
//
// The README was written when the directory held fourteen files and stopped
// being updated when the directory passed twenty. It still said "Fourteen files,
// `0001` through `0014`", its table stopped at 0014, and its copy of
// `compat.json` said `maxSchemaVersion: 12` while the real manifest said 28.
//
// A README that describes a directory which no longer exists is not a small
// defect in a security product. It is the document a new engineer reads before
// writing a migration, and it told them the schema history ended at 0014 -- so
// the migrations from 0015 to 0028 had no entry, no stated reason and no
// reader, which is how a `contract` step or a missing GRANT gets written twice.
//
// The check is the same shape as `internal/statuscheck`'s: the numbers in the
// document are derived from the files that define them, so the only way to go
// green is to make the document match. There is no second copy of the truth.
//
// # What is checked, and what is not
//
// Three things, all mechanical:
//
//   - the table's rows and the directory's files are the same set, in both
//     directions. A file the table does not name is a finding; a row the
//     directory does not have is a finding;
//   - the version range the README states ("`0001` through `0028`") is the
//     directory's lowest and highest;
//   - the `compat.json` snippet the README quotes is the manifest on disk.
//
// The prose around them is not checked, and cannot be: whether a migration's
// one-line description is ACCURATE is a question a human answers by reading the
// migration, and a machine that cannot tell an accurate description from a
// plausible one would be a machine that reports every description as fine.
//
// # What this gate does NOT claim
//
// That the README is complete, current, or correct in its reasoning. It claims
// one thing: the list of files in the README is the list of files in the
// directory. A migration that exists and is undescribed is the failure this
// catches, and it catches it in the same commit the migration lands in, rather
// than a year later when somebody goes looking for what a table is.
package migrationreadme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Finding is one place where migrations/README.md and the directory disagree.
type Finding struct {
	// Label names the claim in the document's own words, so the message reads as
	// a correction rather than as an identifier nobody recognises.
	Label string
	// Line is the 1-based line in the README, which is where a reader goes to
	// fix it.
	Line int
	// Detail says what the tree says, which is the half of the sentence the
	// document does not have.
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("migrations/README.md:%d: %s. %s", f.Line, f.Label, f.Detail)
}

// readmePath is the document, relative to the repository root.
const readmePath = "migrations/README.md"

// migrationsDir is the directory it describes.
const migrationsDir = "migrations"

// compatPath is the manifest the README quotes.
const compatPath = "migrations/compat.json"

// tableRowRe finds one row of the file table.
//
// The row is a markdown table row whose second cell is a backticked file name
// ending in `.sql`. Matching the file name rather than the version number is
// deliberate: the version is derived FROM the file name, so a row that carried
// a version the file does not have would be a second source of truth, which is
// the thing this package exists to remove.
var tableRowRe = regexp.MustCompile("^\\|\\s*([0-9]{4})\\s*\\|\\s*`([^`]+\\.sql)`\\s*\\|")

// rangeRe finds the stated version range: "`0001` through `0028`".
var rangeRe = regexp.MustCompile("`([0-9]{4})`\\s*through\\s*`([0-9]{4})`")

// compatSnippetRe finds the `maxSchemaVersion` inside the README's quoted copy
// of the manifest.
var compatSnippetRe = regexp.MustCompile(`"maxSchemaVersion"\s*:\s*([0-9]+)`)

// compatibility is the part of the manifest this package reads.
type compatibility struct {
	MinSchemaVersion int `json:"minSchemaVersion"`
	MaxSchemaVersion int `json:"maxSchemaVersion"`
}

// Check returns every disagreement between migrations/README.md and the
// directory it describes.
func Check(root string) ([]Finding, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(readmePath)))
	if err != nil {
		return nil, fmt.Errorf("migrationreadme: read %s: %w", readmePath, err)
	}
	readme := string(raw)

	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(migrationsDir)))
	if err != nil {
		return nil, fmt.Errorf("migrationreadme: read %s: %w", migrationsDir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("migrationreadme: %s holds no .sql files, so there is nothing to check "+
			"the document against", migrationsDir)
	}

	var out []Finding
	out = append(out, checkText(readme, files)...)
	out = append(out, compatFindings(root, readme)...)
	return out, nil
}

// checkText is the part of the check that reads only the document and the
// directory's file names.
//
// It is separate from Check so that the red-check can feed it a document with
// a row removed and watch it refuse, without a second copy of the repository
// on disk.
func checkText(readme string, files []string) []Finding {
	var out []Finding
	out = append(out, tableFindings(readme, files)...)
	out = append(out, rangeFindings(readme, files)...)
	return out
}

// tableFindings compares the file table with the directory.
//
// Both directions, because they fail differently. A file the table does not
// name is a migration nobody described. A row the directory does not have is a
// migration that was renamed, deleted, or never existed -- and a reader
// following the document would look for a file that is not there.
func tableFindings(readme string, files []string) []Finding {
	inTable := map[string]int{}
	lines := strings.Split(readme, "\n")
	for i, line := range lines {
		m := tableRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		inTable[m[2]] = i + 1
	}

	var out []Finding
	for _, name := range files {
		if _, ok := inTable[name]; !ok {
			out = append(out, Finding{
				Label:  "the file table does not name " + name,
				Line:   1,
				Detail: "The directory holds it and the table does not, so the document describes a directory that no longer exists. A migration nobody described is a migration nobody reads before writing the next one.",
			})
		}
	}
	// The rows the directory does not have, in the order the document lists
	// them, so the findings read in document order.
	seen := map[string]bool{}
	for _, name := range sortedKeys(inTable) {
		if seen[name] {
			continue
		}
		seen[name] = true
		if !contains(files, name) {
			out = append(out, Finding{
				Label:  "the file table names " + name + ", which is not in the directory",
				Line:   inTable[name],
				Detail: "A row with no file behind it sends a reader looking for a migration that does not exist.",
			})
		}
	}
	return out
}

// rangeFindings checks the stated version range against the directory's own
// lowest and highest.
func rangeFindings(readme string, files []string) []Finding {
	lowest, highest := files[0], files[len(files)-1]
	lowVersion := versionOf(lowest)
	highVersion := versionOf(highest)

	var out []Finding
	m := rangeRe.FindStringSubmatchIndex(readme)
	if m == nil {
		return append(out, Finding{
			Label:  "the document does not state the version range",
			Line:   1,
			Detail: "It used to say \"`0001` through `0014`\". A reader needs to know how far the schema history goes.",
		})
	}
	gotLow := readme[m[2]:m[3]]
	gotHigh := readme[m[4]:m[5]]
	line := strings.Count(readme[:m[0]], "\n") + 1
	if gotLow != lowVersion {
		out = append(out, Finding{
			Label:  "the stated range starts at " + gotLow,
			Line:   line,
			Detail: "The lowest migration in the directory is " + lowVersion + ".",
		})
	}
	if gotHigh != highVersion {
		out = append(out, Finding{
			Label:  "the stated range ends at " + gotHigh,
			Line:   line,
			Detail: "The highest migration in the directory is " + highVersion + ".",
		})
	}
	return out
}

// compatFindings checks the manifest the README quotes against the manifest on
// disk.
//
// The README tells a reader to raise `maxSchemaVersion` in `compat.json`, and
// it quotes the file so they can see the shape. A quoted file that has drifted
// is worse than no quote: a reader copies the stale number back into the file
// they were told to edit.
func compatFindings(root, readme string) []Finding {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(compatPath)))
	if err != nil {
		return []Finding{{
			Label:  "the compatibility manifest could not be read",
			Line:   1,
			Detail: err.Error(),
		}}
	}
	var c compatibility
	if err := json.Unmarshal(raw, &c); err != nil {
		return []Finding{{
			Label:  "the compatibility manifest is not valid JSON",
			Line:   1,
			Detail: err.Error(),
		}}
	}

	m := compatSnippetRe.FindStringSubmatchIndex(readme)
	if m == nil {
		return []Finding{{
			Label:  "the document does not quote the compatibility manifest",
			Line:   1,
			Detail: "It used to, and the quote is where a reader looks for the number to raise.",
		}}
	}
	got, err := strconv.Atoi(readme[m[2]:m[3]])
	if err != nil {
		return nil
	}
	if got == c.MaxSchemaVersion {
		return nil
	}
	return []Finding{{
		Label:  "the quoted maxSchemaVersion is " + strconv.Itoa(got),
		Line:   strings.Count(readme[:m[0]], "\n") + 1,
		Detail: "The manifest says " + strconv.Itoa(c.MaxSchemaVersion) + ". A quoted manifest that has drifted sends a reader back to the number they were told to raise.",
	}}
}

// versionOf is the four-digit version a file name starts with.
func versionOf(name string) string {
	if len(name) < 4 {
		return name
	}
	return name[:4]
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
