// Package pathguard is the one path guard.
//
// # Why it exists
//
// The product writes bytes to more than one root: the EVIDENCE store and the
// RECORDING index. Both take a reference that names a file inside a root, and
// both have to refuse a reference that names something outside it.
//
// Two roots, two copies of the same guard, is two implementations of one
// thing — and the two drift. The drift is invisible until the day the
// weaker copy lets a `..` through, and the weaker copy is the one nobody
// reads.
//
// So the guard lives HERE, and both roots use it.
//
// # What it refuses
//
// A reference is RELATIVE, FORWARD-SLASHED, and made of plain segments. Every
// one of these is refused, and each one is refused for a reason that is
// written down:
//
//   - an absolute path, and a Windows volume name: a reference that names a
//     root is a reference that names the whole disk.
//   - a backslash: references are forward-slashed, and a backslash is how a
//     Windows path is spelled.
//   - a NUL byte: it truncates the path in every syscall that takes a C
//     string.
//   - a `..` segment, and a segment that is only dots: both are a traversal,
//     and a segment that is only dots is one even on a filesystem that does
//     not treat it as one.
//   - an empty segment: `a//b` and `a/` both mean something different on
//     different systems.
//   - a UNC prefix: `\\host\share` is a path to another machine.
//
// And `ContainedIn` is the SECOND half: a reference that passes the shape
// check can still resolve outside the root — through a symlink, or through a
// junction on Windows. So the resolved path is compared against the
// resolved root, and the separator on the prefix is what makes it a
// containment test rather than a string test: `/var/evidence-other` does not
// start with `/var/evidence` as a path.
package pathguard

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrRejected is a reference that names something outside the root.
//
// It is a sentinel so that a caller can say "refused as a path" rather than
// matching on a message.
var ErrRejected = errors.New("pathguard: a storage reference was rejected")

// unsafeSegment matches a path segment that is a traversal, a drive, a UNC
// prefix, or anything with a NUL in it.
var unsafeSegment = regexp.MustCompile(`(?i)^(\.\.?|:|\\\\|//)`)

// maxRefLength is the longest reference the guard accepts.
//
// A reference reaches a DATABASE and an export tool as well as the
// filesystem, so it is bounded here where the reason is explicit rather than
// at every call site.
const maxRefLength = 512

// ValidateRef checks a storage reference without touching the filesystem.
//
// It is the SHAPE half of the guard: the reference is relative, forward-
// slashed and made of plain segments. `ContainedIn` is the other half, and a
// reference that passes this check can still resolve outside the root.
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: empty reference", ErrRejected)
	}
	// A NUL truncates the path in every syscall that takes a C string. Go's
	// own os package rejects it, but a reference reaches a database and an
	// export tool as well, so it is refused here where the reason is explicit.
	if strings.ContainsRune(ref, 0) {
		return fmt.Errorf("%w: reference contains a NUL byte", ErrRejected)
	}
	if strings.ContainsRune(ref, '\\') {
		return fmt.Errorf("%w: %q contains a backslash; references are forward-slashed and relative", ErrRejected, ref)
	}
	if strings.HasPrefix(ref, "/") {
		return fmt.Errorf("%w: %q is absolute", ErrRejected, ref)
	}
	if filepath.IsAbs(ref) || filepath.VolumeName(ref) != "" {
		return fmt.Errorf("%w: %q names a volume", ErrRejected, ref)
	}
	if len(ref) > maxRefLength {
		return fmt.Errorf("%w: reference is %d characters, at most %d", ErrRejected, len(ref), maxRefLength)
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == "" {
			return fmt.Errorf("%w: %q has an empty path segment", ErrRejected, ref)
		}
		if unsafeSegment.MatchString(segment) {
			return fmt.Errorf("%w: %q contains segment %q", ErrRejected, ref, segment)
		}
		// A segment that is only dots on some filesystem is a traversal even
		// where it is not on this one.
		if strings.Trim(segment, ".") == "" {
			return fmt.Errorf("%w: %q contains segment %q", ErrRejected, ref, segment)
		}
	}
	return nil
}

// ContainedIn reports whether path is inside root, or is root itself.
//
// The root is expected to have been resolved once, so this compares real
// paths rather than names that could be repointed afterwards. The separator
// on the prefix is what makes it a containment test rather than a string
// test: "/var/evidence-other" does not start with "/var/evidence" as a path.
func ContainedIn(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("%w: not under the root: %w", ErrRejected, err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: resolves to %q, which is outside %q", ErrRejected, path, root)
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("%w: resolves to the absolute path %q", ErrRejected, rel)
	}
	return nil
}
