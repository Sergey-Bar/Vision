// Package migrations embeds the ordered SQL that builds the Postgres schema.
//
// Embedding here rather than reading from disk at runtime is deliberate: the
// release archive ships one signed binary, and a migration set that can be
// swapped on disk is a migration set that can be tampered with. The checksum of
// every applied migration is recorded, so a later run detects any drift.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
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
	return nil
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
			return nil, err
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
