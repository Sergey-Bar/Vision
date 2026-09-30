package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Staging residue: bytes left on disk by a process that did not survive its own
// write.
//
// The invariant this file protects is the one the plan names for P2.7: a
// half-written object is never *visible*. That half is already true and needs no
// code here - the ref is the content hash, the file is renamed into place, and a
// file that was never renamed has no ref, so nothing can address it and nothing
// can mistake it for evidence.
//
// The half that is not automatically true is the disk. A `defer` that removes the
// staging file does not run when the process is terminated, so a 100 MB clip
// killed at ninety megabytes leaves ninety megabytes on the evidence volume
// forever. On a site with twenty cameras and a supervised service that restarts,
// that is a disk-fill bug, and it is one nobody finds until the disk is full and
// the reason is a log line from a week ago.

const (
	// stagingPrefix is the name every staging file starts with. It is a
	// constant because the sweep and the writer have to agree on it exactly, and
	// two string literals in two functions is one edit away from a sweep that
	// silently matches nothing.
	stagingPrefix = ".incoming-"

	// stagingResidueAge is how old a staging file must be before the sweep will
	// remove it.
	//
	// Not zero, and that is the whole design. A staging file younger than this
	// belongs to a Put that is running *right now* in this process or another
	// one, and removing it would turn a concurrent write into an I/O error at the
	// worst possible moment. One minute is far longer than a 100 MB write takes
	// and far shorter than a restart cycle, so the residue is collected
	// promptly and never taken from under a live write.
	stagingResidueAge = time.Minute
)

// sweepStaging removes staging files older than maxAge, and reports how many it
// removed and how many bytes it reclaimed.
//
// The age bound is applied to the file's modification time, not its name, and
// the walk is bounded to the root - the same tenant confinement Sweep applies,
// and for the same reason: a walk that started at the root and removed by pattern
// would delete a tenant's object if a tenant were ever called `.incoming-x`.
func sweepStaging(root string, maxAge time.Duration) (SweepResult, error) {
	var result SweepResult
	entries, err := os.ReadDir(root)
	if err != nil {
		return result, fmt.Errorf("evidence: read the root: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		// A directory is never residue. Nothing writes one, and if something did
		// then removing a tree here would be a far worse outcome than leaving it.
		if e.IsDir() || !strings.HasPrefix(e.Name(), stagingPrefix) {
			continue
		}
		result.Scanned++
		full := filepath.Join(root, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			// Raced with another process's own cleanup. Not an error worth
			// reporting: the file is gone, which is the outcome.
			continue
		}
		if info.ModTime().After(cutoff) {
			// Too new to be residue. Almost certainly a live Put, and taking it
			// from under one would turn a concurrent write into an I/O error at
			// the worst possible moment.
			continue
		}
		if err := os.Remove(full); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		result.Removed++
		result.Freed += info.Size()
	}
	return result, nil
}

// StagingResidue reports what a sweep would remove, without removing it.
//
// It exists for a console: an operator looking at an evidence volume needs to be
// able to ask "is anything half-written on here" without a maintenance window to
// find out.
func (s *Store) StagingResidue() (SweepResult, error) {
	return sweepStaging(s.root, stagingResidueAge)
}

// LastStagingSweep is what the store collected when it opened.
//
// A sweep that is silent is a sweep nobody knows is running, and an evidence
// volume quietly eating disk is how a site finds out that something is being
// killed mid-write. That is a fact worth a line wherever the store is opened, so
// it is carried on the store rather than thrown away.
func (s *Store) LastStagingSweep() SweepResult { return s.lastStagingSweep }
