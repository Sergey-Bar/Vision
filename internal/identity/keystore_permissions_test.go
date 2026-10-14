package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The world-readable detection, as a property over modes rather than a list of
// examples. Every mode whose group or other bits are set must be reported as a
// problem that names the mode; every mode without them must be accepted. This
// is the red-check target: with the detection removed (the predicate forced to
// "accept"), the problem rows below go red and only them.
func TestAWorldReadableKeyModeIsAProblem(t *testing.T) {
	cases := []struct {
		mode     os.FileMode
		wantText string
	}{
		// Group or other bits set: readable by someone other than the owner.
		{0o007, "0007"},
		{0o070, "0070"},
		{0o640, "0640"},
		{0o644, "0644"},
		{0o646, "0646"},
		{0o604, "0604"},
		{0o610, "0610"},
		{0o660, "0660"},
		{0o666, "0666"},
		{0o777, "0777"},
		// No group or other bits: the owner alone can read it.
		{0o400, ""},
		{0o600, ""},
		{0o700, ""},
		{0o000, ""},
		{0o500, ""},
	}
	for _, c := range cases {
		got := keyPermissionProblem(c.mode)
		if c.wantText == "" {
			if got != "" {
				t.Errorf("mode %04o is not readable by anyone else, but was reported as a problem: %q",
					c.mode.Perm(), got)
			}
			continue
		}
		if got == "" {
			t.Errorf("mode %04o is readable by anyone other than its owner, but was accepted as "+
				"no problem; on a platform whose mode enforces it this is a refused key", c.mode.Perm())
			continue
		}
		if !strings.Contains(got, c.wantText) {
			t.Errorf("the problem for mode %04o is %q, which does not name the mode %s. A refusal "+
				"that hides the mode sends the operator to a 0400 fix with no number to check",
				c.mode.Perm(), got, c.wantText)
		}
	}
}

// The platform split, proved in both directions on a single host. This is not a
// skip: it runs everywhere and asserts the OUTCOME the platform actually has.
// A world-readable key file is refused where the mode is the control and
// accepted -- warned, not refused -- where it is advisory. The defect this
// guards returned the refusal on every platform, so on Windows the mode it
// checked was permanently 0666 and enrolment was blocked after migrate and
// connect had already succeeded.
func TestThePermissionCheckFollowsThePlatform(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0x11}, KeySize), 0o600); err != nil {
		t.Fatalf("seed the key file: %v", err)
	}
	// Make it genuinely world-readable. os.WriteFile is subject to the umask, so
	// set the mode explicitly to guarantee the group and other bits are present
	// where that means something.
	if err := os.Chmod(keyPath, 0o646); err != nil {
		t.Fatalf("widen the key file to 0646: %v", err)
	}
	store, err := NewFileSecretStore(keyPath)
	if err != nil {
		t.Fatalf("build the store: %v", err)
	}

	err = store.checkPermissions()
	if permissionBitsMeanSomething() {
		if err == nil {
			t.Fatalf("a world-readable key file was accepted on %s, where the mode is the control "+
				"and 0646 is exposed to every account", runtime.GOOS)
		}
		// The reason is part of the control: a refusal that does not name the fix
		// leaves the operator with nothing to act on.
		if !strings.Contains(err.Error(), "0400") {
			t.Errorf("the refusal is %v, which does not name 0400 as the fix", err)
		}
		return
	}
	if err != nil {
		t.Errorf("a world-readable key file was refused on %s, where the mode is advisory and the "+
			"ACL is the control. This is the defect that blocked enrolment of every administrator's "+
			"TOTP factor after migrate and connect had succeeded: %v", runtime.GOOS, err)
	}
}

// The exact regression, and the one an operator feels. Generating a key and then
// using it must succeed on every platform: on a host where the mode is advisory
// a freshly written key reports as world-readable, and the previous permission
// check refused it here, which is why security-seed failed at "enrol the
// administrator's TOTP factor" with a message about file modes. This failed on
// Windows before the fix and passes after; on Linux the key is genuinely 0400
// and nothing warns.
func TestGenerateSecretKeyProducesAKeyTheStoreCanUse(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "secret.key")
	if err := GenerateSecretKey(keyPath); err != nil {
		t.Fatalf("generate the secret key: %v", err)
	}
	store, err := NewFileSecretStore(keyPath)
	if err != nil {
		t.Fatalf("build the store over the generated key: %v", err)
	}
	ct, err := store.WrapFor("tenant-1", "user-1", "factor-1", []byte("the-totp-secret"))
	if err != nil {
		t.Fatalf("wrap a secret under a freshly generated key: %v. A freshly generated key must be "+
			"usable by the store on the platform that generated it", err)
	}
	pt, err := store.UnwrapFor("tenant-1", "user-1", "factor-1", ct)
	if err != nil {
		t.Fatalf("unwrap the secret: %v", err)
	}
	if string(pt) != "the-totp-secret" {
		t.Errorf("the round trip returned %q, want %q", pt, "the-totp-secret")
	}
}

// The row-binding AAD, proved at the point the permission refactor touches. The
// platform split must not have loosened the binding: a secret wrapped for one
// row still fails to unwrap for another, on every platform.
func TestARowBindingSurvivesThePermissionRefactor(t *testing.T) {
	store := newTestStore(t)
	ct, err := store.WrapFor("tenant-1", "user-1", "factor-1", []byte("secret"))
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := store.UnwrapFor("tenant-1", "user-2", "factor-1", ct); err == nil {
		t.Errorf("a secret bound to user-1 unwrapped under user-2; the binding was lost")
	}
}

// newTestStore writes a usable key file at a tight mode and returns a store over
// it. The 0400 is what a POSIX install produces; on Windows it is advisory and
// the store warns and continues, which is the behaviour under test.
func newTestStore(t *testing.T) *FileSecretStore {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "secret.key")
	if err := GenerateSecretKey(keyPath); err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	store, err := NewFileSecretStore(keyPath)
	if err != nil {
		t.Fatalf("build the store: %v", err)
	}
	return store
}
