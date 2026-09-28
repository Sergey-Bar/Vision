package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store reads and writes a fixture set from a directory.
type Store struct {
	Dir string
}

// NewStore opens a fixture directory, creating it if it does not exist.
func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("fixture: directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("fixture: resolve %q: %w", dir, err)
	}
	return &Store{Dir: abs}, nil
}

const manifestName = "manifest.json"

// scenarioFileName maps a scenario name to its file. The name is validated as a
// safe file-name fragment because a scenario name can arrive from a capture tool
// or a test table, and a path built from it is a path an attacker can steer.
func scenarioFileName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("fixture: scenario name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("fixture: scenario name %q is not a safe file name", name)
	}
	if strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("fixture: scenario name %q must not start with a dot", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return "", fmt.Errorf("fixture: scenario name %q contains an unsafe character %q", name, r)
		}
	}
	return name + ".json", nil
}

// WriteScenario validates a scenario, computes its content hash, and stores it.
func (s *Store) WriteScenario(sc Scenario) (ManifestEntry, error) {
	var entry ManifestEntry
	if err := sc.Validate(); err != nil {
		return entry, err
	}
	file, err := scenarioFileName(sc.Name)
	if err != nil {
		return entry, err
	}
	hash, err := sc.ContentHash()
	if err != nil {
		return entry, err
	}
	body, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return entry, fmt.Errorf("fixture: encode scenario %q: %w", sc.Name, err)
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return entry, fmt.Errorf("fixture: create %s: %w", s.Dir, err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, file), append(body, '\n'), 0o640); err != nil {
		return entry, fmt.Errorf("fixture: write scenario %q: %w", sc.Name, err)
	}
	return ManifestEntry{
		Name:   sc.Name,
		File:   file,
		Hash:   hash,
		Events: len(sc.Events),
	}, nil
}

// LoadScenario reads one scenario.
func (s *Store) LoadScenario(name string) (Scenario, error) {
	var sc Scenario
	file, err := scenarioFileName(name)
	if err != nil {
		return sc, err
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, file))
	if err != nil {
		return sc, fmt.Errorf("fixture: read scenario %q: %w", name, err)
	}
	if err := json.Unmarshal(raw, &sc); err != nil {
		return sc, fmt.Errorf("fixture: parse scenario %q: %w", name, err)
	}
	if err := sc.Validate(); err != nil {
		return sc, fmt.Errorf("fixture: scenario %q is invalid: %w", name, err)
	}
	return sc, nil
}

// List returns every scenario in the directory, name-sorted.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fixture: read %s: %w", s.Dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == manifestName {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(names)
	return names, nil
}

// All loads every scenario, validated.
func (s *Store) All() ([]Scenario, error) {
	names, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make([]Scenario, 0, len(names))
	for _, n := range names {
		sc, err := s.LoadScenario(n)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// WriteManifest stores the index.
func (s *Store) WriteManifest(m Manifest) error {
	m.FormatVersion = FormatVersion
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("fixture: encode manifest: %w", err)
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return fmt.Errorf("fixture: create %s: %w", s.Dir, err)
	}
	return os.WriteFile(filepath.Join(s.Dir, manifestName), append(body, '\n'), 0o640)
}

// LoadManifest reads the index. A missing manifest is not an error: a fixture
// directory is usable without one, and requiring it would break a test that
// generates a single scenario on the fly.
func (s *Store) LoadManifest() (Manifest, bool, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(s.Dir, manifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return m, false, nil
		}
		return m, false, fmt.Errorf("fixture: read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, false, fmt.Errorf("fixture: parse manifest: %w", err)
	}
	if m.FormatVersion != FormatVersion {
		return m, false, fmt.Errorf("fixture: manifest format version %d, this build reads %d",
			m.FormatVersion, FormatVersion)
	}
	return m, true, nil
}

// VerifyManifest recomputes every scenario hash and reports any drift.
//
// A fixture set whose manifest disagrees with its files is a fixture set that
// has been edited after review. That is the whole failure mode the manifest
// exists to catch, and it is invisible to a test that just replays the files.
func (s *Store) VerifyManifest() (Manifest, error) {
	m, found, err := s.LoadManifest()
	if err != nil {
		return m, err
	}
	if !found {
		return m, fmt.Errorf("fixture: %s has no manifest", s.Dir)
	}

	indexed := map[string]ManifestEntry{}
	for _, e := range m.Scenarios {
		indexed[e.Name] = e
	}

	names, err := s.List()
	if err != nil {
		return m, err
	}
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}

	var problems []string
	for _, n := range names {
		sc, err := s.LoadScenario(n)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		entry, ok := indexed[n]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: present on disk but absent from the manifest", n))
			continue
		}
		hash, err := sc.ContentHash()
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		if hash != entry.Hash {
			problems = append(problems, fmt.Sprintf(
				"%s: content hash %s does not match the manifest's %s; this fixture was edited after review",
				n, shortHash(hash), shortHash(entry.Hash)))
		}
		if entry.Events != len(sc.Events) {
			problems = append(problems, fmt.Sprintf("%s: manifest says %d events, the file has %d",
				n, entry.Events, len(sc.Events)))
		}
	}

	for name := range indexed {
		if !present[name] {
			problems = append(problems, fmt.Sprintf("%s: listed in the manifest but missing from disk", name))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return m, fmt.Errorf("fixture: %s does not match its manifest:\n  - %s",
			s.Dir, strings.Join(problems, "\n  - "))
	}
	return m, nil
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
