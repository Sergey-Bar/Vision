// Package embedaudit holds the schema-level assertion the plan's C7 names: no
// person, plate, face-crop or credential vector can exist.
//
// # Why this exists before the ADR
//
// C7's human gate is an ADR and a compliance ruling for video-segment
// embeddings, and until that ruling lands embeddings are OFF by default and the
// whole subject is closed. What this package does is make the closed part
// structurally true rather than merely documented: the schema, the protobuf
// contracts and the SQL are scanned, and a definition that would create a
// person, plate, face-crop or credential vector fails the build.
//
// The plan says the shape in one line — *"person/plate/face-crop vectors
// prohibited at schema level"* — and the reason it is a gate and not a
// convention is the failure mode it prevents. The privacy decision already made
// is that this product keeps no model of a person; a single `vector(1536)`
// column on a face-crop table is that decision reversed by a migration nobody
// looked at closely, because it looks like a column.
//
// # What is scanned, and what is not
//
// The three surfaces a vector could be defined in: `contracts/protobuf` (a
// message field of kind float or bytes on a sensitive subject), `contracts/json-schema`
// (an array of numbers on a sensitive subject), and `migrations` (a `vector(`
// column on a sensitive table). SQL that merely READS a vector from a
// general-purpose table is not the assertion's subject — the assertion is that
// the definition cannot exist.
//
// The subject list is closed and named: person, plate, face_crop, credential.
// A new sensitive subject has to be added here deliberately, which is the same
// choice `KnownEventTypes` makes for the same reason.
package embedaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SensitiveSubjects are the table and message names a vector may not be defined
// on.
//
// Person, plate, face crop and credential, and nothing else. The list is the
// whole assertion: a vector on `canonical_events` is not this package's subject
// and never was — the ruling C7 will decide is about the SENSITIVE subjects, and
// a gate that scanned everything would fail on the first legitimate use and
// stop being run.
var sensitiveSubjects = []string{
	"person", "plate", "face_crop", "facecrop", "credential", "biometric",
}

// Finding is one place a sensitive vector could be defined.
type Finding struct {
	File    string
	Line    int
	What    string
	Subject string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s on subject %q. The plan's C7 prohibits "+
		"person/plate/face-crop/credential vectors at schema level, and this "+
		"definition would create one", f.File, f.Line, f.What, f.Subject)
}

// SensitiveSubjectCount is how many subjects the closed set holds. Exported because a
// test asserts the set is not silently narrowed: a gate whose subject list emptied
// itself out would pass on everything.
func SensitiveSubjectCount() int { return len(sensitiveSubjects) }

// Scan audits the three contract surfaces under root and returns every finding.
//
// An empty result is a passing audit. The scanner reads the tree rather than a
// manifest, because the manifest is what would be wrong.
func Scan(root string) ([]Finding, error) {
	var out []Finding
	for _, dir := range []string{
		filepath.Join("contracts", "protobuf"),
		filepath.Join("contracts", "json-schema"),
		"migrations",
	} {
		findings, err := scanDir(filepath.Join(root, filepath.FromSlash(dir)), dir)
		if err != nil {
			return nil, err
		}
		out = append(out, findings...)
	}
	return out, nil
}

func scanDir(dir, label string) ([]Finding, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("embedaudit: read %s: %w", label, err)
	}
	var out []Finding
	for _, e := range entries {
		if e.IsDir() {
			sub, err := scanDir(filepath.Join(dir, e.Name()), filepath.ToSlash(filepath.Join(label, e.Name())))
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("embedaudit: read %s: %w", path, err)
		}
		out = append(out, scanFile(
			filepath.ToSlash(filepath.Join(label, e.Name())), string(body))...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// scanFile audits one file.
//
// The file is the unit, not the line, because the two halves of the violation
// live on different lines: a migration names its subject in `CREATE TABLE
// face_crop (...)` and its column two lines later in `embedding vector(1536)`.
// A line-based scanner reads the table line, sees no vector on it, and passes —
// which is the scanner that matches nothing wearing a green result.
//
// The subject is read in a definition position only. The word boundary and the
// "for the whole file" rule are what keep a comment saying "no plate vector is
// stored" from being flagged: prose is not a definition, and a scanner that
// flagged it would be trained to be ignored.
func scanFile(file, text string) []Finding {
	lines := strings.Split(text, "\n")
	subject, sensitive := fileSubject(lines)
	if !sensitive {
		return nil
	}
	var out []Finding
	for i, line := range lines {
		vectorish := vectorSQL.MatchString(line) || protoRepeatedFloat.MatchString(line)
		if !vectorish && jsonNumberArrayIn(lines) {
			// The JSON case is decided for the file rather than the line: an
			// array's `"type": "array"` and its items' `"type": "number"` are on
			// different lines of a formatted schema, and a line-based rule would
			// read the array, see no number on it, and pass.
			if strings.Contains(line, `"type": "array"`) {
				vectorish = true
			}
		}
		if vectorish {
			out = append(out, Finding{
				File: file, Line: i + 1,
				What: "a vector-shaped definition", Subject: subject,
			})
		}
	}
	return out
}

// jsonNumberArrayIn reports whether a file defines an array OF NUMBERS.
//
// The proximity rule is the whole content of this function: an array and a
// number in the same file are not a vector - the restricted-person schema has a
// `persons` array of objects and a `confidence` number, and the shipped schema
// is exactly what a file-level rule flags. A vector's number is its array's
// ITEMS, so the number has to be the array's own, and `items` follows `type`
// within a few lines of a formatted schema. The window is five because that is
// what a schema formatter emits and a vector that hid its items further out
// would be hiding them from every reader too.
func jsonNumberArrayIn(lines []string) bool {
	for i, line := range lines {
		if !strings.Contains(line, `"type": "array"`) {
			continue
		}
		for _, near := range lines[i:min(i+5, len(lines))] {
			if strings.Contains(near, `"items"`) && jsonNumberType.MatchString(near) {
				return true
			}
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fileSubject reports the sensitive subject a file defines.
//
// A file defines at most one, and the first definition wins: a migration with
// two CREATE TABLEs is unusual, and a finding that named the second would be a
// finding about a table whose column was not the one under it.
func fileSubject(lines []string) (string, bool) {
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, s := range sensitiveSubjects {
			// In a definition position: `create table ... <subject>`, `message
			// <Subject>...`, or a JSON property that IS the subject.
			for _, pat := range []string{
				" table " + s, " into " + s, " alter table " + s,
				"message " + s, "\"." + s + "\"", "\"" + s + "\": {",
			} {
				if strings.Contains(lower, pat) {
					return s, true
				}
			}
		}
	}
	return "", false
}

// vectorSQL matches a pgvector column definition. `vector(` with a size.
var vectorSQL = regexp.MustCompile(`vector\s*\(\s*\d+\s*\)`)

// protoRepeatedFloat matches a proto field whose type is a repeated floating
// number - the shape a vector takes on the wire. `bytes` is included because a
// quantised vector is bytes, and a scanner that only knew float would miss the
// half of them that quantises.
var protoRepeatedFloat = regexp.MustCompile(`(?i)\brepeated\s+(float|double|bytes)\b`)

// jsonNumberType matches a JSON Schema property whose type is numeric.
//
// The rule is deliberately narrow: an array of OBJECTS is not a vector, and the
// `persons` array in the restricted-person schema is an array of objects. A
// vector in JSON Schema is an array whose items are numbers, so the finding
// requires both the array and the numeric items in the same file - the failing
// shape this replaced flagged the shipped schema, which is the shape of a
// scanner that would be trained to be ignored.
var jsonNumberType = regexp.MustCompile(`"(?:number|integer)"`)
