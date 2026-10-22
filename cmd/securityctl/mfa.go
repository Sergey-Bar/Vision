package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// The second factor, and the enrolment that creates it.
//
// # Why this is a command and not a route
//
// Enrolment is an install-time action on the console terminal, and it is
// deliberately not a web surface. A route that mints a second factor is a
// route that an attacker with a stolen session calls twice, and a factor that
// is created by whoever reached the page is not a factor anybody proved they
// hold. The enrolment happens once, at the terminal, with the database
// credentials and the wrapping key already on the machine.
//
// The secret is printed exactly once and is not recoverable afterwards. The
// database holds it wrapped, bound to the row it belongs to, under the
// install's own key: a dump of the database alone does not yield a working
// factor, and a secret copied from one row to another does not open.

// mfaCommand is the second factor's own command tree.
func mfaCommand() *command.Command {
	m := &command.Command{
		Name:    "mfa",
		Summary: "Enrol and confirm a second factor",
		Usage: `mfa — the second factor, and the enrolment that creates it.

enrol issues a TOTP secret for a principal and prints it ONCE. The secret is
never recoverable afterwards: the database holds a wrapped copy that only the
install's own key can open, and the point of that is that a database dump
alone does not yield a working factor.

The enrolment needs the MFA wrapping key, which the installer generated. It
is at SP_SECRET_KEY, or /etc/security-platform/secret.key.`,
	}
	m.Register(
		simpleFlag("enrol", "Enrol a TOTP factor for a principal", runMFAEnrol),
		simpleFlag("confirm", "Confirm an enrolment with a code from the authenticator", runMFAConfirm),
	)
	return m
}

// secretKeyPath is where the MFA wrapping key lives.
//
// It is the same path the core reads and the installer writes, so a site that
// has not overridden it still finds it.
func secretKeyPath(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if v := strings.TrimSpace(os.Getenv("SP_SECRET_KEY")); v != "" {
		return v
	}
	return "/etc/security-platform/secret.key"
}

// mfaIdentityService builds the identity service for the MFA commands.
//
// The secret store is the point of the service: without it there is no
// enrolment, because the enrolment seals the secret and the verification
// opens it. The Audit writer is not optional either -- an identity layer that
// can be made not to record is an identity layer nobody can investigate.
func mfaIdentityService(ctx context.Context, keyPath string) (*identity.Service, error) {
	pool, err := openPool(ctx)
	if err != nil {
		return nil, err
	}
	store, err := identity.NewFileSecretStore(keyPath)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &identity.Service{
		Pool:        pool,
		Audit:       &audit.Writer{Pool: pool},
		SecretStore: store,
	}, nil
}

// findPrincipal resolves what the operator typed to a user id.
//
// The operator says "operator" or "usr-operator" and the enrolment binds the
// factor to a user id, so the command has to know which one was meant. Both
// are accepted because both are what people have in front of them: the
// username is in the console's topbar and the id is in the audit ledger.
func findPrincipal(ctx context.Context, pool *dbx.Pool, tenant, principal string) (userID, username string, err error) {
	err = pool.For(tenant, "securityctl").QueryRow(ctx, `
		SELECT user_id, username
		  FROM users
		 WHERE tenant_id = $1 AND (user_id = $2 OR lower(username) = lower($2))
		 LIMIT 1`, tenant, principal).Scan(&userID, &username)
	if err != nil {
		return "", "", err
	}
	return userID, username, nil
}

func runMFAEnrol(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("mfa enrol", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	user := fs.String("user", "", "The principal: a user id or a username.")
	label := fs.String("label", "", "Label for the credential. Defaults to the principal's username.")
	keyPath := fs.String("secret-key", "", "Path to the MFA wrapping key. Defaults to SP_SECRET_KEY.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return &command.UsageError{Err: errors.New("--user is required")}
	}

	path := secretKeyPath(*keyPath)
	ident, err := mfaIdentityService(ctx, path)
	if err != nil {
		return command.Refuse("%v", err)
	}
	defer ident.Pool.Close()

	tenant := configuredTenant()
	userID, username, err := findPrincipal(ctx, ident.Pool, tenant, strings.TrimSpace(*user))
	if err != nil {
		return command.Refuse("no principal %q in tenant %q: %v", *user, tenant, err)
	}

	factorLabel := strings.TrimSpace(*label)
	if factorLabel == "" {
		factorLabel = username
	}

	secret, uri, err := ident.EnrollTOTP(ctx, tenant, userID, factorLabel)
	if err != nil {
		return command.Refuse("%v", err)
	}

	fmt.Fprintf(env.Stdout, "enrolled a TOTP factor for %s (%s)\n", username, userID)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "  secret:  "+secret)
	fmt.Fprintln(env.Stdout, "  uri:     "+uri)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "The secret is shown once and is not recoverable. The database holds it")
	fmt.Fprintln(env.Stdout, "wrapped under the install's own key, so a copy of the database is not a")
	fmt.Fprintln(env.Stdout, "working factor -- and neither is this terminal once you close it.")
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "Store it in the authenticator now, and confirm the enrolment with a code")
	fmt.Fprintln(env.Stdout, "from it:")
	fmt.Fprintf(env.Stdout, "  securityctl mfa confirm --user %s --code <code>\n", userID)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "Until it is confirmed, the factor is stored but unverified: the authenticator")
	fmt.Fprintln(env.Stdout, "may have imported the wrong secret, and nothing has said so yet.")
	return nil
}

func runMFAConfirm(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("mfa confirm", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	user := fs.String("user", "", "The principal: a user id or a username.")
	code := fs.String("code", "", "The six-digit code the authenticator is showing.")
	keyPath := fs.String("secret-key", "", "Path to the MFA wrapping key. Defaults to SP_SECRET_KEY.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return &command.UsageError{Err: errors.New("--user is required")}
	}
	if strings.TrimSpace(*code) == "" {
		return &command.UsageError{Err: errors.New("--code is required")}
	}

	ident, err := mfaIdentityService(ctx, secretKeyPath(*keyPath))
	if err != nil {
		return command.Refuse("%v", err)
	}
	defer ident.Pool.Close()

	tenant := configuredTenant()
	userID, username, err := findPrincipal(ctx, ident.Pool, tenant, strings.TrimSpace(*user))
	if err != nil {
		return command.Refuse("no principal %q in tenant %q: %v", *user, tenant, err)
	}

	if err := ident.VerifyTOTPEnrollment(ctx, tenant, userID, strings.TrimSpace(*code)); err != nil {
		return command.Refuse("%v", err)
	}
	fmt.Fprintf(env.Stdout, "confirmed the second factor for %s (%s)\n", username, userID)
	return nil
}
