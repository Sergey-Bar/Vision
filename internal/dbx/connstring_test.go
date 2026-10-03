package dbx

import (
	"net/url"
	"strings"
	"testing"
)

// These live in package dbx rather than dbx_test because they assert on the string
// that is about to be handed to the driver, which is unexported and which pgx
// keeps out of reach once the pool exists: pgxpool.Pool does not export the config
// it was built from. Asserting on it after Open would mean opening sockets, and a
// connection-string defect asserted by a test that needs a database is a test that
// skips on the machines most likely to have the bug.

const (
	// A literal in a test file. It is not read from an environment variable, a
	// secret store or a file on disk, and it is not a credential.
	dsnPassword  = "not-a-real-credential"
	bandPassword = "also-not-a-real-credential"
)

// A DSN that names its own password must connect with it.
//
// This is the fix. ParseDSN read the password out of the URL and ApplyDSN threw it
// away, while Open only ever attached a role to the connection string on the branch
// that had a password to attach. So postgres://user:pass@host/db - the one form
// that carries a password when the dedicated variable is unset - reached pgx
// naming no role at all, and pgx filled the gap with the name of the OS account.
// `security-migrate up` was told to authenticate as `postgres` and authenticated as
// whoever happened to run it, which surfaced as a password authentication failure
// naming a user the operator never wrote down.
func TestADSNPasswordReachesTheConnectionString(t *testing.T) {
	cfg := configFor(t, "postgres://migrator:"+dsnPassword+"@db.internal:55432/security")

	u := parseConnString(t, cfg.connectionString(""))

	if got := u.User.Username(); got != "migrator" {
		t.Errorf("the connection string names user %q, want migrator. A DSN that names a role "+
			"and connects as somebody else is the failure this asserts against", got)
	}
	if got, set := u.User.Password(); !set || got != dsnPassword {
		t.Errorf("the connection string carries password %q (present: %t), want the one the DSN "+
			"carried. ParseDSN read it and ApplyDSN discarded it, so the driver was sent none", got, set)
	}
}

// The role has to survive on its own. The bug above had two halves and either one
// alone is enough to authenticate as the wrong identity: Open restored the role
// only when a password came with it, so a DSN naming a role and no password reached
// pgx naming no role at all and was silently the OS account.
func TestTheRoleReachesTheConnectionStringWithNoPasswordAtAll(t *testing.T) {
	cfg := configFor(t, "postgres://migrator@db.internal:55432/security")

	u := parseConnString(t, cfg.connectionString(""))

	if got := u.User.Username(); got != "migrator" {
		t.Errorf("the connection string names user %q, want migrator", got)
	}
	if got, set := u.User.Password(); set {
		t.Errorf("the connection string invented password %q from a DSN that carried none", got)
	}
}

// The dedicated password variable still wins over one embedded in the URL, or the
// fix would have turned the URL into a way to override a deliberate setting.
func TestAnOutOfBandPasswordBeatsOneCarriedInTheDSN(t *testing.T) {
	cfg := configFor(t, "postgres://migrator:"+dsnPassword+"@db.internal:55432/security")

	u := parseConnString(t, cfg.connectionString(bandPassword))

	if got, _ := u.User.Password(); got != bandPassword {
		t.Errorf("the connection string carries password %q, want the one supplied out of band. "+
			"The DSN's password winning would let a stale URL override SP_DATABASE_PASSWORD", got)
	}
}

// The password must not be re-read on a second call either: connectionString is
// value-receiver and ApplyDSN is the only writer, so a Config that has been through
// ApplyDSN once resolves the same way every time. Cheap, and it fails if someone
// later makes the resolution mutate the Config.
func TestTheResolvedCredentialIsStableAcrossCalls(t *testing.T) {
	cfg := configFor(t, "postgres://migrator:"+dsnPassword+"@db.internal:55432/security")

	first := cfg.connectionString("")
	second := cfg.connectionString("")
	if first != second {
		t.Errorf("two calls with no out-of-band password disagree:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "db.internal:55432/security") {
		t.Errorf("the connection string is %q and lost the host, port or database", first)
	}
}

func configFor(t *testing.T, dsn string) Config {
	t.Helper()
	cfg := DefaultConfig()
	if err := cfg.ApplyDSN(dsn); err != nil {
		t.Fatalf("ApplyDSN(%q): %v", dsn, err)
	}
	return cfg
}

func parseConnString(t *testing.T, connStr string) *url.URL {
	t.Helper()
	u, err := url.Parse(connStr)
	if err != nil {
		t.Fatalf("parse %q: %v", connStr, err)
	}
	if u.User == nil {
		t.Fatalf("the connection string is %q and carries no role at all, so the driver falls "+
			"back to the OS account", connStr)
	}
	return u
}
