package safename

import (
	"strings"
	"testing"
)

// TestSafeNeutralizesEveryEscape is the one place both artifact file names are
// proven to be safe. The two callers are internal/dutylog (a shift's JSONL) and
// internal/siteconfig (an object's YAML), and they carried a copy each of this
// guard before this package existed.
func TestSafeNeutralizesEveryEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"../../etc/passwd", ".._.._etc_passwd"},
		{"..\\..\\windows\\system32", ".._.._windows_system32"},
		{"C:\\evil", "C__evil"},
		{"a*b?c\"d<e>f|g", "a_b_c_d_e_f_g"},
		{"with space", "with_space"},
		{"", "_"},
		{"   ", "___"},
		{"\x00\x01control", "__control"},
		{"plain-id-123", "plain-id-123"},
		{"a/b\\c", "a_b_c"},
	}
	for _, c := range cases {
		got := Safe(c.in)
		if got != c.want {
			t.Errorf("Safe(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.ContainsAny(got, "/\\:*?\"<>| ") {
			t.Errorf("Safe(%q) = %q still contains a reserved character", c.in, got)
		}
		if got == "" {
			t.Errorf("Safe(%q) produced the empty name", c.in)
		}
	}
}

// TestSafeIsIdempotent: applying the mapping twice is applying it once, so a
// re-apply of the same plan finds the file an earlier apply wrote under the
// same name.
func TestSafeIsIdempotent(t *testing.T) {
	for _, id := range []string{"cam-01/02", "../../etc/passwd", `C:\x`, "plain"} {
		once := Safe(id)
		if twice := Safe(once); twice != once {
			t.Errorf("Safe is not idempotent for %q: %q then %q", id, once, twice)
		}
	}
}
