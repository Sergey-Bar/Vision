// Package safename maps an id to a safe file-name stem.
//
// One function, one home, because two artifacts build file names from ids and
// were carrying two copies of this guard: `internal/dutylog` names a shift's
// JSONL, `internal/siteconfig` names an object's YAML. Hardening one copy and
// not the other is how a path-traversal guard quietly stops being a guard, and
// the zero-width test that proves it works now lives in exactly one place.
//
// The mapping is deliberately not an escape: every separator and reserved
// character becomes an underscore, so an id that tries to leave the directory
// (`../../etc/passwd`) becomes a file name inside it. It is a mapping, not a
// validation — a name a caller does not recognise is refused by the caller, not
// by this.
package safename

import "strings"

// Safe maps an id to a file-name stem: no separator, no reserved character, no
// control character, and nothing empty. An id that maps to the empty string is
// given a name that cannot collide with anything.
func Safe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|' || r == ' ':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			// A control character in a file name is a different operating
			// system's problem, and it is not a name.
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}
