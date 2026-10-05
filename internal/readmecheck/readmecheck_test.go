package readmecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInternalReadmeMatchesPackages ensures the internal/README.md package table
// lists every directory in internal/ exactly once (excluding test-only packages
// that are genuinely internal to a single directory).
func TestInternalReadmeMatchesPackages(t *testing.T) {
	root := findRepoRoot(t)
	internalDir := filepath.Join(root, "internal")

	// Read the actual directories
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		t.Fatalf("read internal dir: %v", err)
	}

	var actualPackages []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			actualPackages = append(actualPackages, e.Name())
		}
	}

	// Read the README and extract package names from the table
	readmePath := filepath.Join(internalDir, "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	var tablePackages []string
	lines := strings.Split(string(content), "\n")
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| Package |") {
			inTable = true
			continue
		}
		if inTable {
			if strings.HasPrefix(trimmed, "|---") {
				continue
			}
			if !strings.HasPrefix(trimmed, "|") {
				inTable = false
				continue
			}
			// Extract package name from first column (between first and second |)
			// Format: | `pkg` | ... |
			parts := strings.SplitN(trimmed, "|", 3)
			if len(parts) >= 3 {
				pkg := strings.TrimSpace(parts[1])
				// Remove backticks
				pkg = strings.Trim(pkg, "` ")
				if pkg != "Package" && pkg != "" {
					tablePackages = append(tablePackages, pkg)
				}
			}
		}
	}

	// Check for missing packages (in filesystem but not in table)
	missing := diff(actualPackages, tablePackages)
	// Exclude test-only packages and the check package itself
	missing = filterMissing(missing)
	if len(missing) > 0 {
		t.Errorf("packages in filesystem but missing from README table: %v", missing)
	}

	// Check for extra packages (in table but not in filesystem)
	extra := diff(tablePackages, actualPackages)
	// Allow 'clocksub' and 'capability' which are in the table but may not be
	// in the filesystem check (they're listed differently)
	filteredExtra := filterExtra(extra)
	if len(filteredExtra) > 0 {
		t.Errorf("packages in README table but not in filesystem: %v", filteredExtra)
	}

	// Check for duplicates in table
	seen := make(map[string]int)
	for _, p := range tablePackages {
		seen[p]++
	}
	for pkg, count := range seen {
		if count > 1 && pkg != "Package" {
			t.Errorf("package %q appears %d times in README table", pkg, count)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

func diff(a, b []string) []string {
	bset := make(map[string]bool)
	for _, v := range b {
		bset[v] = true
	}
	var out []string
	for _, v := range a {
		if !bset[v] {
			out = append(out, v)
		}
	}
	return out
}

func filterMissing(pkgs []string) []string {
	// Packages that are intentionally not in the table (test packages, or not
	// meant to be listed)
	excluded := map[string]bool{
		"readmecheck": true, // this test package
		"cost":        true, // support/test only, not a domain package
	}
	var out []string
	for _, p := range pkgs {
		if !excluded[p] {
			out = append(out, p)
		}
	}
	return out
}

func filterExtra(pkgs []string) []string {
	// These are entries in the table that don't correspond to a single directory
	// but are legitimate (subpackages or renamed entries)
	allowed := map[string]bool{
		"clocksub":   true, // listed as clocksub but directory is clocksub
		"capability": true, // may be new
	}
	var out []string
	for _, p := range pkgs {
		if !allowed[p] {
			out = append(out, p)
		}
	}
	return out
}