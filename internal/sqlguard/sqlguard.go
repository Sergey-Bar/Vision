// Package sqlguard holds the static rules about the SQL this repository writes.
//
// # Why this exists
//
// Three adversarial reviews were run against this codebase in one day. Between
// them they found, independently:
//
//   - `outbox.deliver` marking a message delivered with no lease-owner guard, so
//     a slow relay overwrote a dead letter another relay had written.
//   - `outbox.markFailed` doing the same on the failure path, flipping a
//     delivered row back to `failed` for a retry that would run for ever.
//   - `inbox.Fail` and `inbox.Complete` matching on the key alone, so a worker
//     whose lease had lapsed could write a terminal state onto a row another
//     worker was actively processing.
//   - `incidents.SetState` reading the state on a separate connection and
//     writing with no guard on what it read, so two operators could interleave
//     into an incident that was both open and closed.
//
// All five are the same shape: **a mutation whose WHERE clause did not constrain
// the thing that grants permission to make it.** Three separate agents found
// them by reading carefully; this package finds them by looking.
//
// A test suite cannot catch these. Each of them needs a race, a lease expiry, or
// a second worker to manifest, and a suite that arranged all of those would be
// testing a scenario rather than a rule. A rule, though, is exactly the kind of
// thing a machine is better at: any UPDATE against a table that has a
// `lease_owner` column constrains `lease_owner`, and any UPDATE or DELETE
// against a tenant-scoped table constrains `tenant_id`. Both are derivable from
// the migrations, so they stay true as the schema changes instead of rotting into
// a hand-maintained list.
//
// # What it deliberately does not do
//
// It does not execute SQL, parse SQL properly, or understand transactions. It
// reads Go string literals and asks narrow questions about them. A rule that
// needs a real parser is a rule that will produce false positives, and a gate
// that produces false positives is a gate people learn to ignore ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â which is the
// condition this repository keeps finding in other checks.
package sqlguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TableFacts is what the migrations say about one table.
type TableFacts struct {
	// Name is the table as declared.
	Name string
	// TenantScoped is true when the table has a `tenant_id` column, which is
	// invariant 5's first half: a tenant id is NOT NULL everywhere.
	TenantScoped bool
	// Leased is true when the table has a `lease_owner` column, which is the
	// product's statement that a claimed row may only be written by the worker
	// that claimed it.
	Leased bool
	// AppendOnly is true when the table carries a mutation-forbidding trigger.
	// Application code has no business issuing an UPDATE or DELETE against one.
	AppendOnly bool
}

// Finding is one statement that breaks a rule.
type Finding struct {
	// File and Line locate it. Line is 1-based and points at the Go line the SQL
	// literal starts on, which is where a reader goes to fix it.
	File string
	Line int
	// Table is the table the statement touches.
	Table string
	// Verb is UPDATE, DELETE, or INSERT.
	Verb string
	// Rule names what was broken, for the message.
	Rule string
	// Excerpt is a trimmed slice of the statement, so the finding identifies
	// itself without the reader opening the file.
	Excerpt string
}

func (f Finding) String() string {
	return f.File + ":" + strconv.Itoa(f.Line) + ": " + f.Rule + " (" + f.Verb +
		" " + f.Table + "): " + f.Excerpt
}

// ---------------------------------------------------------------------------
// The rules.
// ---------------------------------------------------------------------------

// Mutation patterns, matched against the start of a SQL literal.
//
// They are deliberately loose about whitespace and case and deliberately strict
// about nothing else: a rule that can be satisfied by a plausible-looking
// string is worse than no rule, because it teaches its readers that passing the
// gate means something.
var (
	updateRe   = regexp.MustCompile(`(?is)\bupdate\s+([a-z_][a-z0-9_]*)`)
	deleteRe   = regexp.MustCompile(`(?is)\bdelete\s+from\s+([a-z_][a-z0-9_]*)`)
	insertRe   = regexp.MustCompile(`(?is)\binsert\s+into\s+([a-z_][a-z0-9_]*)`)
	truncateRe = regexp.MustCompile(`(?is)\btruncate\s+(?:table\s+)?([a-z_][a-z0-9_]*)`)
)

// The exemption marker.
//
// A rule that cannot be excepted is a rule somebody turns off. Every legitimate
// exception to the lease rule has the same shape Ã¢â‚¬â€ a statement that is
// *supposed* to act on a row nobody holds a lease on Ã¢â‚¬â€ so the exception is a
// named, greppable marker in the SQL comment, and the guard refuses an exemption
// that does not say why.
//
// It is a marker rather than a filename or a package list because the exception
// belongs to the statement. A list of files goes stale the moment a function
// moves, and a stale exception is an exemption for a statement nobody has read.
var exemptRe = regexp.MustCompile("(?i)sqlguard:exempt\\s+(\\S+)\\s*[-\u2014:]\\s*([^\\n]+)")

// exemptions reads the exemptions declared inside one SQL literal.
//
// Per literal, not per file. A file-scoped marker would let an exemption
// written for the claim silently cover a second, unrelated statement in the
// same file, which is the shape of a rule that gets quietly widened.
func exemptions(literal string) map[string]string {
	out := map[string]string{}
	for _, m := range exemptRe.FindAllStringSubmatch(literal, -1) {
		if len(m) < 3 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
	}
	return out
}

// Check runs every rule over the SQL literals in one file.
func Check(path, source string, tables map[string]TableFacts) []Finding {
	var out []Finding
	for _, lit := range literals(source) {
		exempt := exemptions(lit.text)
		for _, stmt := range statementsFrom(lit) {
			if reason, ok := exempt[stmt.ruleKey()]; ok {
				// Recorded, not forgotten: an excused finding still says so, so a
				// reader sees the exception exists and can judge it rather than
				// rediscover it.
				out = append(out, Finding{
					File: path, Line: stmt.line, Table: stmt.table, Verb: stmt.verb,
					Rule:    "exempt: " + reason,
					Excerpt: stmt.excerpt,
				})
				continue
			}
			out = append(out, checkStatement(path, stmt, tables)...)
		}
	}
	return out
}

type statement struct {
	line    int
	verb    string
	table   string
	text    string
	excerpt string
}

// ruleKey names the rule a statement is most likely to break, which is what an
// exemption names. One key per statement keeps an exemption narrow: it cannot
// silence the tenant rule and the lease rule at once, because it has to say
// which one it is excusing.
func (s statement) ruleKey() string {
	switch s.verb {
	case "UPDATE":
		return "lease"
	case "DELETE":
		return "tenant"
	default:
		return "tenant"
	}
}

func checkStatement(path string, s statement, tables map[string]TableFacts) []Finding {
	facts, known := tables[s.table]
	if !known {
		// A table this scan does not know about is a table added by a migration
		// that predates the guard's table list, or a name that came from a
		// variable rather than a literal. Either way, saying nothing is correct:
		// the rule is about tables it has facts for.
		return nil
	}

	var out []Finding
	excerpt := s.excerpt
	switch s.verb {
	case "UPDATE", "DELETE":
		// Rule 1: invariant 5. A mutation against a tenant-scoped row that does
		// not name the tenant is a mutation whose blast radius is the table.
		//
		// This is the check that would have caught `outbox.deliver` and
		// `markFailed`, which both wrote with `WHERE outbox_id = $1` and no tenant
		// predicate at all.
		if facts.TenantScoped && !mentions(statementWhere(s.text), "tenant_id") {
			out = append(out, Finding{
				File: path, Line: s.line, Table: s.table, Verb: s.verb, Excerpt: excerpt,
				Rule: "a mutation of a tenant-scoped table must constrain tenant_id. " +
					"Without it the statement's blast radius is the table rather than one " +
					"tenant's rows, and nothing in Go can undo it",
			})
		}
		// Rule 2: only the lease holder may write a leased row.
		//
		// A row that carries a lease is a row a worker is holding. An UPDATE that
		// ignores the owner lets a worker whose lease lapsed ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â and which has no
		// way of knowing it lapsed, because nothing tells it ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â write a terminal
		// state over the work the current holder is doing.
		if s.verb == "UPDATE" && facts.Leased && !mentions(statementWhere(s.text), "lease_owner") {
			out = append(out, Finding{
				File: path, Line: s.line, Table: s.table, Verb: s.verb, Excerpt: excerpt,
				Rule: "an UPDATE of a leased table must constrain lease_owner. Only the " +
					"worker that claimed a row may write it; without the constraint a " +
					"worker whose lease has expired can overwrite the work of the one " +
					"holding it now",
			})
		}
	}

	// Rule 3: an append-only table is written by INSERT and nothing else.
	//
	// The trigger enforces this in the database, which is the control. This is
	// here so a statement that tries it fails the gate with an explanation
	// rather than at run time with a PostgreSQL error.
	if facts.AppendOnly && s.verb != "INSERT" {
		out = append(out, Finding{
			File: path, Line: s.line, Table: s.table, Verb: s.verb, Excerpt: excerpt,
			Rule: "an append-only table is written by INSERT and by nothing else. " +
				"The trigger will refuse this, so find the writer that belongs here " +
				"instead",
		})
	}
	return out
}

// statementWhere returns the WHERE clause of a statement, or the whole statement
// when there is none ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â because a statement with no WHERE has no tenant
// predicate in it, which is the finding.
var whereRe = regexp.MustCompile(`(?i)\bwhere\b`)

func statementWhere(sql string) string {
	flat := strings.ToLower(normaliseWhitespace(sql))
	// The word, not a spaced token. A statement whose text begins with the WHERE
	// Ã¢â‚¬â€ because the verb and the table name were already consumed Ã¢â‚¬â€ has no
	// leading space, and requiring one silently classified every such statement
	// as having no predicate at all.
	loc := whereRe.FindStringIndex(flat)
	if loc == nil {
		return ""
	}
	rest := flat[loc[1]:]
	// Stop at the first clause that is not part of the predicate. Being generous
	// here only weakens the check, never breaks it.
	for _, terminator := range []string{" order by ", " group by ", " limit ", " returning ", " for update"} {
		if j := strings.Index(rest, terminator); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
}

// normaliseWhitespace collapses every run of whitespace to a single space,
// including the two-character sequences a raw Go string contains when SQL is
// written with `\n` and `\t` in it.
//
// A statement in this repository is written across several lines with tab
// indentation, and a matcher that requires a literal space before `where` misses
// every one of them Ã¢â‚¬â€ which is the same bug as a rule that never fires.
func normaliseWhitespace(s string) string {
	s = strings.ReplaceAll(s, `\n`, " ")
	s = strings.ReplaceAll(s, `\t`, " ")
	return strings.Join(strings.Fields(s), " ")
}

// mentions reports whether a clause references an identifier, matching a whole
// word so `tenant_id` is not satisfied by `sub_tenant_id`.
func mentions(clause, identifier string) bool {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(identifier) + `\b`)
	return re.MatchString(clause)
}

// ---------------------------------------------------------------------------
// Reading SQL out of Go source.
// ---------------------------------------------------------------------------

// statements finds the SQL literals in a Go file and, for each UPDATE, DELETE,
// INSERT and TRUNCATE, the span of the statement within the literal.
// statementsFrom reads the statements in one literal.
func statementsFrom(lit literal) []statement {
	var out []statement
	{
		lower := strings.ToLower(lit.text)
		for _, m := range []struct {
			re   *regexp.Regexp
			verb string
		}{
			{updateRe, "UPDATE"},
			{deleteRe, "DELETE"},
			{insertRe, "INSERT"},
			{truncateRe, "TRUNCATE"},
		} {
			for _, hit := range m.re.FindAllStringSubmatchIndex(lower, -1) {
				if len(hit) < 4 {
					continue
				}
				table := lit.text[hit[2]:hit[3]]
				// A table's name is a bare identifier; if the token has anything
				// else in it the match is a word that happened to be followed by
				// `update`, and acting on it would be a guess.
				if !bareIdentifier.MatchString(table) {
					continue
				}
				out = append(out, statement{
					line:    lit.line,
					verb:    m.verb,
					table:   strings.ToLower(table),
					text:    lit.text[hit[1]:],
					excerpt: excerpt(lit.text[hit[1]:]),
				})
			}
		}
	}
	return out
}

var bareIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 110 {
		return s[:110] + "ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬Ãƒâ€šÃ‚Â¦"
	}
	return s
}

type literal struct {
	text string
	// line is the 1-based line in the Go file on which the literal starts.
	line int
}

// literals finds backtick raw strings and interpreted strings in a Go file.
//
// Backtick literals are where the SQL lives in this repository, and they are
// read whole, which is what lets a rule look at a statement's WHERE clause.
// Interpreted strings are skipped: a Go string built by concatenation cannot be
// read statically, and pretending otherwise would produce a rule that passes
// vacuously.
func literals(source string) []literal {
	var out []literal
	line := 1
	for i := 0; i < len(source); i++ {
		c := source[i]
		if c == '\n' {
			line++
			continue
		}
		if c != '`' {
			continue
		}
		start := i
		startLine := line
		j := i + 1
		for j < len(source) && source[j] != '`' {
			if source[j] == '\n' {
				line++
			}
			j++
		}
		if j >= len(source) {
			break // unterminated: nothing more to read
		}
		out = append(out, literal{text: source[start+1 : j], line: startLine})
		i = j
	}
	return out
}

// ---------------------------------------------------------------------------
// The table list.
// ---------------------------------------------------------------------------

// TablesFromMigrations derives the facts from the migration files themselves.
//
// A hand-maintained list of tenant-scoped tables is a list that stops being
// true the moment somebody adds a table, and a guard that has quietly stopped
// covering the newest table is worse than no guard because it still reports
// green. Deriving it means the guard is a function of the schema, so adding a
// table to a migration adds it to the guard with no second edit.
func TablesFromMigrations(dir string) (map[string]TableFacts, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// The append-only tables are named by their triggers rather than by a list,
	// so a new append-only table is covered by writing the trigger.
	appendOnly := appendOnlyTables(dir, entries)

	out := map[string]TableFacts{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, block := range createTableBlocks(string(body)) {
			name := block.name
			out[name] = TableFacts{
				Name:         name,
				TenantScoped: hasColumn(block.body, "tenant_id"),
				Leased:       hasColumn(block.body, "lease_owner"),
				AppendOnly:   appendOnly[name],
			}
		}
	}
	return out, nil
}

type tableBlock struct {
	name string
	body string
}

// createTableBlocks splits the migration text into `CREATE TABLE ... (...)`
// bodies. It is a splitter and not a parser: it looks for the opening paren,
// counts to its match, and takes the name from between the keyword and it.
func createTableBlocks(sqlText string) []tableBlock {
	var out []tableBlock
	lower := strings.ToLower(sqlText)
	for i := 0; ; {
		idx := strings.Index(lower[i:], "create table")
		if idx < 0 {
			return out
		}
		i += idx + len("create table")
		rest := sqlText[i:]

		// A partition declaration has no column list:
		//   CREATE TABLE x PARTITION OF y FOR VALUES FROM (...)
		// so the first `(` belongs to the FOR VALUES clause, not to a body, and
		// taking the text before it as a name produces the word "from" as a
		// table. Partitions inherit the facts of the table they partition, and
		// nothing in this repository writes one directly.
		if upto := strings.Index(rest, ";"); upto >= 0 {
			head := strings.ToUpper(rest[:upto])
			if at := strings.Index(head, " PARTITION OF "); at >= 0 {
				// Truncate to just the name, and only skip if what remains has no
				// column list. A statement that opens a paren before the marker is
				// not a partition declaration.
				head = head[:at]
				if !strings.Contains(head, "(") {
					i += upto
					continue
				}
			}
		}

		open := strings.IndexAny(rest, "(;")
		if open < 0 {
			return out
		}
		if rest[open] == ';' {
			i += open
			continue
		}
		// `CREATE TABLE [IF NOT EXISTS] name` ÃƒÂ¢Ã¢â€šÂ¬Ã¢â‚¬Â the name is the last word before
		// the body opens.
		name := lastIdentifier(rest[:open])
		// Walk to the paren that closes the body, counting nesting so a
		// `CHECK (x IN (1,2))` does not end the table early.
		depth, end := 0, -1
		for k := open; k < len(rest); k++ {
			switch rest[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = k
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return out
		}
		out = append(out, tableBlock{name: name, body: rest[open:end]})
		// Resume just past the keyword rather than past the block.
		//
		// Advancing by the block means one spurious match â€” the words "create
		// table" inside a comment, or inside a PL/pgSQL body â€” consumes everything
		// up to the next closing paren and silently drops the real tables in
		// between. Resuming immediately costs a re-scan of a few kilobytes and
		// cannot lose a table, and names are deduplicated by the map.
		i += len("create table")
	}
}

func lastIdentifier(s string) string {
	s = strings.ReplaceAll(s, "IF NOT EXISTS", " ")
	s = strings.ReplaceAll(s, "IF EXISTS", " ")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[len(fields)-1], `";`)
}

func hasColumn(body, column string) bool {
	// A column declaration starts a line and is followed by a type, so requiring
	// the name at the start of a trimmed line avoids matching a reference to the
	// column inside a constraint.
	re := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(column) + `\s+[a-z]`)
	return re.MatchString(body)
}

var createTriggerRe = regexp.MustCompile(`(?is)create\s+trigger\s+(\w+)`)

// appendOnlyTables finds the tables a trigger forbids UPDATE or DELETE on.
//
// The trigger's name is not evidence; its body is. A trigger that raises on
// UPDATE or DELETE is a mutation guard by whatever it is called, and matching on
// the definition rather than the name means a renamed trigger is still covered.
func appendOnlyTables(dir string, entries []os.DirEntry) map[string]bool {
	out := map[string]bool{}
	// Both verbs, not either.
	//
	// `rule_bundles_content_immutable` is `BEFORE UPDATE ON rule_bundles ...
	// WHEN (OLD.content IS DISTINCT FROM NEW.content)`: it refuses a change to
	// the bundle's bytes and allows every other update, which is a correct and
	// deliberate design Ã¢â‚¬â€ a bundle's content is fixed once stored and its
	// `active` flag is not. Reading that trigger as "append-only" made the guard
	// report four findings against code doing the right thing, and a guard that
	// cries wolf is a guard that gets switched off.
	reject := regexp.MustCompile(`(?is)\bupdate\s+or\s+delete\b`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		text := string(body)
		lower := strings.ToLower(text)
		for _, m := range createTriggerRe.FindAllStringSubmatchIndex(text, -1) {
			rest := lower[m[0]:]
			// The shape is CREATE TRIGGER <name> BEFORE <events> ON <table>, so
			// the event list is *before* the ON clause. Reading the text after
			// `on` finds the table and never the events, which is how an
			// append-only table stops being recognised as one Ã¢â‚¬â€ a guard that
			// quietly covers less while reporting green.
			on := strings.Index(rest, " on ")
			if on < 0 {
				continue
			}
			if !reject.MatchString(rest[:on]) {
				continue
			}
			fields := strings.Fields(rest[on+len(" on "):])
			if len(fields) == 0 {
				continue
			}
			out[strings.ToLower(strings.Trim(fields[0], `";`))] = true
		}
	}
	return out
}
