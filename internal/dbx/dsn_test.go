package dbx_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// ApplyDSN's contract is that a field absent from the DSN keeps the Config's
// value. SSLMode was guarded; Port and User were not, and url.Parse leaves both
// at their zero value when the DSN omits them. So ApplyDSN("postgres://host/db")
// over a DefaultConfig() produced "postgres://:0/db" — a port no listener holds,
// with the configured user silently discarded.
//
// Every case starts from DefaultConfig(), because that is the call shape: a site
// configuration supplies defaults and a DSN overrides part of it.
func TestApplyDSNKeepsConfigValuesTheDSNDoesNotSupply(t *testing.T) {
	cases := []struct {
		name     string
		dsn      string
		host     string
		port     int
		database string
		user     string
		sslMode  string
	}{
		{
			name:     "no port and no user keeps both",
			dsn:      "postgres://db.internal/security",
			host:     "db.internal",
			port:     5432,
			database: "security",
			user:     "security_core",
			sslMode:  "require",
		},
		{
			name:     "a port but no user keeps the user",
			dsn:      "postgres://db.internal:55432/security",
			host:     "db.internal",
			port:     55432,
			database: "security",
			user:     "security_core",
			sslMode:  "require",
		},
		{
			name:     "a user but no port keeps the port",
			dsn:      "postgres://migrator@db.internal/security",
			host:     "db.internal",
			port:     5432,
			database: "security",
			user:     "migrator",
			sslMode:  "require",
		},
		{
			name:     "user and port both supplied replaces both",
			dsn:      "postgres://migrator@db.internal:55432/security",
			host:     "db.internal",
			port:     55432,
			database: "security",
			user:     "migrator",
			sslMode:  "require",
		},
		{
			name:     "sslmode is overridden when present",
			dsn:      "postgres://db.internal:55432/security?sslmode=disable",
			host:     "db.internal",
			port:     55432,
			database: "security",
			user:     "security_core",
			sslMode:  "disable",
		},
		{
			name:     "the postgresql scheme behaves identically",
			dsn:      "postgresql://db.internal/security",
			host:     "db.internal",
			port:     5432,
			database: "security",
			user:     "security_core",
			sslMode:  "require",
		},
		{
			name:     "an empty userinfo section is treated as absent",
			dsn:      "postgres://@db.internal:55432/security",
			host:     "db.internal",
			port:     55432,
			database: "security",
			user:     "security_core",
			sslMode:  "require",
		},
		{
			name:     "a non-default port is kept, not just the absence case",
			dsn:      "postgres://db.internal:1/security",
			host:     "db.internal",
			port:     1,
			database: "security",
			user:     "security_core",
			sslMode:  "require",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := dbx.DefaultConfig()
			if cfg.Port == 0 || cfg.User == "" {
				t.Fatal("DefaultConfig has no port or no user; this test would pass vacuously")
			}
			if err := cfg.ApplyDSN(tc.dsn); err != nil {
				t.Fatalf("ApplyDSN(%q): %v", tc.dsn, err)
			}
			got := []any{cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.SSLMode}
			want := []any{tc.host, tc.port, tc.database, tc.user, tc.sslMode}
			fields := []string{"Host", "Port", "Database", "User", "SSLMode"}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s is %v, want %v (from %q over DefaultConfig)",
						fields[i], got[i], want[i], tc.dsn)
				}
			}
		})
	}
}

// The failure this bug produced was visible in the rendered DSN, so the rendered
// DSN is asserted as well. This is the string that reaches pgx and every
// preflight report, and a zero port in it is a connection that cannot be made.
func TestApplyDSNWithoutAPortRendersTheConfiguredPort(t *testing.T) {
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN("postgres://db.internal/security"); err != nil {
		t.Fatalf("ApplyDSN: %v", err)
	}
	dsn := cfg.DSN()
	if want := ":5432/"; !strings.Contains(dsn, want) {
		t.Errorf("DSN is %q, want it to contain %q; a port of 0 or a dropped port "+
			"both produce a connection string that cannot be opened", dsn, want)
	}
	if strings.Contains(dsn, ":0/") {
		t.Errorf("DSN is %q, which names port 0", dsn)
	}
}

// The refusal the fix had to keep, and the reason Config needed a String() method
// to keep it: DSN() is the field the startup log and every preflight report use,
// so the password must not come back out of a Config by either route. fmt prints
// unexported struct fields under %v and %+v, so a Config that holds a password at
// all is only safe to print because String() redirects it to DSN().
//
// String() is not named here on purpose. Calling it directly would make its
// removal a build failure rather than a leak, and a build failure proves less: the
// %v row below routes through String() exactly as a caller's diagnostic would, so
// deleting it turns that row red on the password itself. %s is left out for the
// same reason - it routes identically, and vet rejects it on a type that has lost
// its String(), which would put the failure back behind a build error.
//
// The credential asserted on here is a literal in a test file. It is not read from
// an environment variable, a secret store, or a file on disk, and it is not one.
const dsnPassword = "not-a-real-credential"

func TestTheRenderedDSNCarriesTheRoleAndNeverThePassword(t *testing.T) {
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN("postgres://migrator:" + dsnPassword + "@db.internal:55432/security"); err != nil {
		t.Fatalf("ApplyDSN: %v", err)
	}

	if !strings.Contains(cfg.DSN(), "migrator@") {
		t.Errorf("DSN() is %q and names no role. Every rendered DSN used to drop it, including "+
			"the one handed to the driver", cfg.DSN())
	}

	for _, rendered := range []struct {
		what string
		got  string
	}{
		{"DSN()", cfg.DSN()},
		{"%+v", fmt.Sprintf("%+v", cfg)},
		{"%v", fmt.Sprintf("%v", cfg)},
	} {
		if strings.Contains(rendered.got, dsnPassword) {
			t.Errorf("%s printed the database password; it is the diagnostic every one of "+
				"these ends up in a ticket or a log line", rendered.what)
		}
	}
}

// A password reaching the driver must not have loosened anything else: the DSN is
// still refused for the same reasons, and still without mutating the config.
func TestADSNCarryingAPasswordStillRefusesEverythingItRefusedBefore(t *testing.T) {
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN("postgres://db.internal:55432/security?sslmode=disable"); err != nil {
		t.Fatalf("ApplyDSN: %v", err)
	}
	before := cfg
	if err := cfg.ApplyDSN("mysql://host/db?password=" + dsnPassword); err == nil {
		t.Error("a DSN that is not a postgres URL was accepted")
	}
	if cfg != before {
		t.Errorf("the refused DSN mutated the config: %s", cfg.DSN())
	}
}

// A DSN that is not a postgres URL, or that omits the host or the database, is
// still refused - and refusing it must leave the Config exactly as it was, since
// ApplyDSN is called on a live configuration during startup.
func TestApplyDSNRejectsABadDSNWithoutMutatingTheConfig(t *testing.T) {
	for _, dsn := range []string{
		"mysql://host/db",
		"host=localhost dbname=security",
		"postgres://db.internal",
		"postgres:///security",
		"postgres://db.internal:notaport/security",
	} {
		cfg := dbx.DefaultConfig()
		before := cfg
		if err := cfg.ApplyDSN(dsn); err == nil {
			t.Errorf("ApplyDSN(%q) was accepted", dsn)
			continue
		}
		if cfg != before {
			t.Errorf("ApplyDSN(%q) failed but left the config as %+v, want %+v",
				dsn, cfg, before)
		}
	}
}
