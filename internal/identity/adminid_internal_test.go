package identity

import "testing"

// The bootstrap administrator's id used to be the SHA-256 of nothing, appended to
// the tenant and the username: `sha256.New().Sum(x)` returns x plus the digest of
// zero bytes, so every administrator in every deployment was "admin-e3b0c442" —
// e3b0c442 being the first eight hex characters of the SHA-256 of no input at all.
//
// This is tested here rather than through BootstrapAdmin because the property is
// about two strings. Doing it through the service needed a tenant, a site, a
// password and a second run before it would even fail again, because the
// bootstrap refuses to create a second administrator in the same tenant.
func TestAdminUserIDIsNotTheDigestOfNothing(t *testing.T) {
	id := adminUserID("t-1", "admin")
	if id == "admin-e3b0c442" {
		t.Errorf("the id is %q, which is the tenant and username followed by the first "+
			"eight hex characters of the SHA-256 of no input at all", id)
	}
	if id == "t-1admin" {
		t.Errorf("the id is %q, which is the un-hashed concatenation of its inputs", id)
	}
}

// The separator matters for the same reason the digest did. Without one, the
// administrator of tenant "ab" named "c" and the administrator of tenant "a"
// named "bc" were the same account id in two different tenants.
func TestAdminUserIDSeparatesItsInputs(t *testing.T) {
	cases := [][2][2]string{
		{{"ab", "c"}, {"a", "bc"}},
		{{"t", "1"}, {"t1", ""}},
		{{"", "admin"}, {"a", "dmin"}},
	}
	for _, c := range cases {
		a := adminUserID(c[0][0], c[0][1])
		b := adminUserID(c[1][0], c[1][1])
		if a == b {
			t.Errorf("(%q,%q) and (%q,%q) both produce %q. The inputs are concatenated "+
				"without a separator, so they are not distinguishable",
				c[0][0], c[0][1], c[1][0], c[1][1], a)
		}
	}
}

// Derived, not generated: the same site following the same steps gets the same
// id, which is what lets an operator find the account without a lookup table.
//
// The value is pinned rather than compared to a second call. A function of its
// arguments with no randomness is equal to itself by construction, so
// "call it twice, compare" asserts nothing at all — the linter is right about
// that, and it was right when it said so. A pinned constant also catches the
// change nobody intended: an altered separator or a truncated digest moves it.
func TestAdminUserIDIsPinned(t *testing.T) {
	// admin- + the first eight hex characters of
	// sha256("t-1" + NUL + "admin").
	const want = "admin-3e12acaf"
	if got := adminUserID("t-1", "admin"); got != want {
		t.Errorf("adminUserID(%q, %q) = %q, want %q. The id is derived, not generated, so "+
			"this value is fixed for ever; if it moved, the derivation changed and every "+
			"operator following a runbook is now looking for an account under a different name",
			"t-1", "admin", got, want)
	}
	if adminUserID("t-2", "admin") == want {
		t.Error("two different tenants produced the same administrator id")
	}
	if adminUserID("t-1", "root") == want {
		t.Error("two different usernames in one tenant produced the same id")
	}
}
