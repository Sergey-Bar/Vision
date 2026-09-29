// Package migrations embeds the ordered SQL that builds the Postgres schema.
//
// Embedding here rather than reading from disk at runtime is deliberate: the
// release archive ships one signed binary, and a migration set that can be
// swapped on disk is a migration set that can be tampered with. The checksum of
// every applied migration is recorded, so a later run detects any drift.
package migrations

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Phase is the expand/contract step a migration belongs to. A contract step may
// never be bundled with the expand step that makes it safe: the expand ships and
// is exercised against a live database first, the contract ships later.
type Phase string

// Migration phases. A filename without one is not a migration this runner
// applies, so an unlabelled file is refused rather than skipped.
const (
	PhaseExpand   Phase = "expand"
	PhaseBackfill Phase = "backfill"
	PhaseContract Phase = "contract"
)

// Migration is one ordered, forward-only step. Migrations are never edited once
// applied; a correction is a new, higher-numbered migration.
type Migration struct {
	Version  int
	Name     string
	Phase    Phase
	SQL      string
	Checksum string
}

// FileName returns the on-disk name, which is the ordering key's human form.
func (m Migration) FileName() string {
	return fmt.Sprintf("%04d_%s.%s.sql", m.Version, m.Name, m.Phase)
}

// IsValid reports whether the migration is safe to apply in isolation.
//
// A file that mixes phases is a rejected file, not a warning. Bundling a
// contract into its expand is the exact failure the expand/contract discipline
// exists to prevent: the expand has to be deployable and observable while the
// old column is still live.
func (m Migration) IsValid() error {
	if m.Version <= 0 {
		return fmt.Errorf("migration %q: version must be positive", m.FileName())
	}
	if m.Name == "" {
		return fmt.Errorf("migration %q: missing name", m.FileName())
	}
	switch m.Phase {
	case PhaseExpand, PhaseBackfill, PhaseContract:
	default:
		return fmt.Errorf("migration %q: unknown phase %q, want one of expand|backfill|contract", m.FileName(), m.Phase)
	}
	if strings.TrimSpace(m.SQL) == "" {
		return fmt.Errorf("migration %q: empty body", m.FileName())
	}
	if err := m.checkPhaseDiscipline(); err != nil {
		return err
	}
	return nil
}

// ContractPhaseError is returned when a migration whose phase is not `contract`
// contains DDL that belongs to a contract step.
//
// It is a distinct type rather than a bare error because the migration set
// currently contains one known violation — 0010, an applied `DROP COLUMN` in an
// `.expand.sql` — which cannot be corrected by editing the file. All() therefore
// has to tell "this file is wrong and must be fixed" apart from "this file is
// wrong, was applied to deployed databases, and is grandfathered in
// grandfatheredContractViolations below". Anything that is not a
// ContractPhaseError is a rejection, full stop.
type ContractPhaseError struct {
	Migration string
	Phase     string
	Statement string
}

func (e *ContractPhaseError) Error() string {
	return fmt.Sprintf("migration %q: %q is contract DDL in a file whose phase is %q; "+
		"contract DDL ships in its own .contract.sql migration, in a later release than "+
		"the expand that makes it safe", e.Migration, e.Statement, e.Phase)
}

// grandfatheredContractViolations names the migrations that carry contract DDL
// in a non-contract file and are already applied on deployed databases.
//
// Each entry is a defect that is being carried, not one that has been fixed, and
// the list is the place the debt is visible. Editing a migration's SQL is
// forbidden by the forward-only rule, and a site that has already run the
// version must keep agreeing with the binary that ran it, so the correction is
// a decision for a human rather than a change made here.
//
// Empty of new entries on purpose: this is a record of history, not a policy
// knob. A new violation fails IsValid and is not added here.
var grandfatheredContractViolations = map[string]string{
	"0010_drop_default_locale.expand.sql": "applied on deployed databases; correct by a human decision, not by editing the file",
}

// CarriedContractViolations reports the migrations that hold contract DDL in a
// non-contract file, and why they are being carried.
//
// Exported because a grandfather list nobody can read is a suppression with a
// table. An operator running `security-migrate preflight` sees a version it is
// about to report as clean; this is how a caller learns that the label on that
// version is not the whole truth.
func CarriedContractViolations() map[string]string {
	out := make(map[string]string, len(grandfatheredContractViolations))
	for k, v := range grandfatheredContractViolations {
		out[k] = v
	}
	return out
}

// contractStatement matches the DDL that belongs to a contract step.
//
// The patterns are deliberately anchored on the full keyword phrase and on a
// non-word boundary before it, so `dropped_at`, `truncate_date` and
// `"drop column"` inside a quoted identifier are not matches. The word after
// DROP is required, so `DROP CONSTRAINT` and `DROP INDEX` — which an expand may
// legitimately do before re-adding the object under a new name - are not
// matches either.
var contractStatements = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\bdrop\s+table\b`),
	regexp.MustCompile(`(?is)\bdrop\s+column\b`),
	regexp.MustCompile(`(?is)\btruncate\b`),
	// ALTER TABLE ... TYPE and ... RENAME, matched within a single statement.
	// The statement is bounded by `;` so a RENAME in one statement is not
	// attributed to an unrelated ALTER TABLE earlier in the file.
	regexp.MustCompile(`(?is)\balter\s+table\b[^;]*?\brename\b`),
	regexp.MustCompile(`(?is)\balter\s+table\b[^;]*?\b(?:set\s+data\s+)?type\b`),
}

// stripSQLNoise replaces the regions of a migration that a keyword search must
// not read: `--` line comments, `/* */` block comments, and single-quoted string
// literals. Dollar-quoted bodies are left alone, which is a documented false
// negative: a function body that contains the text `drop table` in a comment of
// its own is not a contract step, and a real one would name a table that a
// grep for `drop\s+table` finds with the semicolons inside its body intact.
//
// The regions are replaced by spaces rather than removed, so offsets and the
// surrounding word boundaries stay where they were.
func stripSQLNoise(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))
	runes := []rune(sql)
	for i := 0; i < len(runes); {
		switch {
		case i+1 < len(runes) && runes[i] == '-' && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				out.WriteRune(' ')
				i++
			}
		case i+1 < len(runes) && runes[i] == '/' && runes[i+1] == '*':
			for i < len(runes) {
				if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '/' {
					out.WriteString("  ")
					i += 2
					break
				}
				if runes[i] == '\n' {
					out.WriteRune('\n')
				} else {
					out.WriteRune(' ')
				}
				i++
			}
		case runes[i] == '\'':
			// A doubled quote is an escaped quote, not the end of the literal.
			out.WriteRune(' ')
			i++
			for i < len(runes) {
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						out.WriteString("  ")
						i += 2
						continue
					}
					out.WriteRune(' ')
					i++
					break
				}
				if runes[i] == '\n' {
					out.WriteRune('\n')
				} else {
					out.WriteRune(' ')
				}
				i++
			}
		case runes[i] == '"':
			// A double-quoted identifier. Its contents are a name, never a
			// keyword, so `"drop table x"` is a table called drop table x rather
			// than a DROP. Blanked for the same reason a literal is.
			out.WriteRune(' ')
			i++
			for i < len(runes) {
				if runes[i] == '"' {
					if i+1 < len(runes) && runes[i+1] == '"' {
						out.WriteString("  ")
						i += 2
						continue
					}
					out.WriteRune(' ')
					i++
					break
				}
				if runes[i] == '\n' {
					out.WriteRune('\n')
				} else {
					out.WriteRune(' ')
				}
				i++
			}
		default:
			out.WriteRune(runes[i])
			i++
		}
	}
	return out.String()
}

// checkPhaseDiscipline rejects contract DDL in a file that is not a contract
// step.
//
// The phase used to be a filename label and nothing more: IsValid checked that
// the string was one of three known words and never opened the SQL, so a
// `DROP COLUMN` in a file called `.expand.sql` validated and was applied. The
// expand/contract discipline is a property of what the file does, not of what
// it is called.
//
// Conservative by construction, and the bias is deliberate. A false positive -
// refusing a valid migration - blocks a release and teaches people to work
// around the check; a false negative - missing a real violation - is caught the
// next time somebody reads the diff, and the ledger's phase column is a lie in
// the meantime. When the two cannot both be had, this prefers the one that does
// not break a release.
//
// What this does not catch, stated plainly:
//
//   - Contract DDL inside a dollar-quoted function body, if it happens not to
//     be preceded by the keyword phrase the patterns require.
//   - `ALTER TABLE ... ALTER COLUMN ... DROP NOT NULL`, which is a constraint
//     change rather than a type or rename, and `DROP CONSTRAINT` / `DROP INDEX`,
//     which an expand may legitimately do.
//   - A contract step reached through a stored procedure or a `DO` block.
//   - Anything spelled with a Unicode homoglyph. The match is ASCII, case
//     insensitive, and does not attempt to normalise.
func (m Migration) checkPhaseDiscipline() error {
	if m.Phase == PhaseContract {
		return nil
	}
	clean := stripSQLNoise(m.SQL)
	for _, re := range contractStatements {
		if loc := re.FindStringIndex(clean); loc != nil {
			stmt := strings.TrimSpace(clean[loc[0]:loc[1]])
			stmt = strings.Join(strings.Fields(stmt), " ")
			if len(stmt) > 80 {
				stmt = stmt[:80] + "..."
			}
			return &ContractPhaseError{Migration: m.FileName(), Phase: string(m.Phase), Statement: stmt}
		}
	}
	return nil
}

// grandfathered reports whether this migration's phase violation is a known
// carried defect, and why it is being carried.
func (m Migration) grandfathered() (string, bool) {
	reason, ok := grandfatheredContractViolations[m.FileName()]
	return reason, ok
}

// All returns every embedded migration in version order, validating each one.
// Duplicate versions are an error: two migrations claiming the same slot is an
// ordering ambiguity that must never reach a live site.
func All() ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}

	seen := map[int]string{}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, phase, err := parseFileName(e.Name())
		if err != nil {
			return nil, err
		}
		body, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		m := Migration{
			Version:  version,
			Name:     name,
			Phase:    Phase(phase),
			SQL:      string(body),
			Checksum: checksum(body),
		}
		if err := m.IsValid(); err != nil {
			var contractErr *ContractPhaseError
			_, grandfathered := m.grandfathered()
			if !errors.As(err, &contractErr) || !grandfathered {
				return nil, err
			}
			// A known, already-applied violation, carried on purpose. It is
			// tolerated rather than refused because refusing it would make this
			// build unable to describe - let alone upgrade - a database that has
			// already run the file. The debt stays visible in
			// grandfatheredContractViolations and through
			// CarriedContractViolations; adding an entry there is a human
			// decision, not something a test can grant itself.
		}
		if prior, dup := seen[m.Version]; dup {
			return nil, fmt.Errorf("migrations %q and %q both claim version %d", prior, m.FileName(), m.Version)
		}
		seen[m.Version] = m.FileName()
		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func parseFileName(name string) (version int, kind, phase string, err error) {
	base := strings.TrimSuffix(name, ".sql")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return 0, "", "", fmt.Errorf("migration %q: want NNNN_name.phase.sql", name)
	}
	version, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", "", fmt.Errorf("migration %q: %q is not a version", name, parts[0])
	}
	// Split on the LAST dot, not the first. A migration name may legitimately
	// contain a dot — "0003_events.and_graph.expand.sql" — and splitting on the
	// first would silently read the phase as "and_graph", which IsValid then
	// rejects with a message that points at the phase rather than at the
	// filename.
	dot := strings.LastIndex(parts[1], ".")
	if dot <= 0 || dot == len(parts[1])-1 {
		return 0, "", "", fmt.Errorf("migration %q: want NNNN_name.phase.sql", name)
	}
	return version, parts[1][:dot], parts[1][dot+1:], nil
}
