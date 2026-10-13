package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// TestConnectConfigRefusesRatherThanGuessing is the fix, asserted.
//
// `connectConfig` used to return `dbx.DefaultConfig()` when neither `--dsn` nor
// SP_DATABASE_URL was set. That is localhost:5432/security as user
// security_core, so `security-migrate up` typed in a shell without the variable
// exported connected to whatever was listening and began changing its schema —
// and said nothing, because there was no error to say.
//
// A migration runner that can pick its own target is the one component where a
// silent default is not a convenience.
func TestConnectConfigRefusesRatherThanGuessing(t *testing.T) {
	t.Setenv("SP_DATABASE_URL", "")

	cfg, err := connectConfig("", "")
	if err == nil {
		t.Fatalf("no database was named and the tool connected to %s anyway", cfg.DSN())
	}
	if !errors.Is(err, dbx.ErrNoTarget) {
		t.Errorf("the refusal is %v, which a caller cannot recognise as \"no target\"", err)
	}

	// The message has to carry the answer, because the operator's next move is
	// either to pass a DSN or to decide that the default was what they meant.
	msg := err.Error()
	for _, want := range []string{"-dsn", "SP_DATABASE_URL", "security"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so it does not say what to do:\n%s", want, msg)
		}
	}
	// And no password may appear in it, since DSN() omits one and this message
	// goes to stderr on a site console.
	if strings.Contains(msg, "password") {
		t.Errorf("the refusal mentions a password field:\n%s", msg)
	}
}

// Each of the two ways to name a database works, and a flag beats the environment
// — an operator who types a DSN means that one even if a stale variable is
// exported.
func TestConnectConfigUsesEitherNamedDatabase(t *testing.T) {
	t.Setenv("SP_DATABASE_URL", "")

	cfg, err := connectConfig("postgres://alice@db.example:6000/customer", "")
	if err != nil {
		t.Fatalf("a named DSN was refused: %v", err)
	}
	if cfg.Host != "db.example" || cfg.Port != 6000 || cfg.Database != "customer" ||
		cfg.User != "alice" {
		t.Errorf("the flag named a different database than it was given: %+v", cfg)
	}

	t.Setenv("SP_DATABASE_URL", "postgres://bob@env.example:7000/fromenv")
	cfg, err = connectConfig("", "")
	if err != nil {
		t.Fatalf("SP_DATABASE_URL was refused: %v", err)
	}
	if cfg.Host != "env.example" || cfg.Database != "fromenv" || cfg.User != "bob" {
		t.Errorf("the environment named a different database than it held: %+v", cfg)
	}

	// And the flag wins over a stale export.
	cfg, err = connectConfig("postgres://alice@flag.example:6000/wins", "")
	if err != nil {
		t.Fatalf("the flag did not take precedence: %v", err)
	}
	if cfg.Host != "flag.example" {
		t.Errorf("the flag lost to the environment: %+v", cfg)
	}
}

// A malformed DSN is still a malformed DSN, and the refusal for it must not be
// `ErrNoTarget` — the operator named a database and named it wrong, which is a
// different problem from naming none.
func TestAMalformedDSNIsNotRefusedAsNoTarget(t *testing.T) {
	t.Setenv("SP_DATABASE_URL", "")

	_, err := connectConfig("postgres://user@host:notaport/db", "")
	if err == nil {
		t.Fatal("a DSN with an unparseable port was accepted")
	}
	if errors.Is(err, dbx.ErrNoTarget) {
		t.Error("a malformed DSN is reported as no target; the operator named one and named it wrong")
	}
}

// The password is resolved separately and never through this path, because a flag
// argument is visible in the process list. Worth a test because it is the kind of
// thing that gets "simplified" into the config struct one day.
func TestThePasswordIsNotResolvedFromTheDSN(t *testing.T) {
	t.Setenv("SP_DATABASE_PASSWORD", "from-the-environment")

	if got := resolvePassword("from-the-flag"); got != "from-the-flag" {
		t.Errorf("the flag did not win: %q", got)
	}
	if got := resolvePassword(""); got != "from-the-environment" {
		t.Errorf("the dedicated variable was not used: %q", got)
	}
}
