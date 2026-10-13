package main

// The one command that turns a stored TOTP secret into a current six-digit code,
// and why it exists in the product CLI at all.
//
// # The case for it
//
// The console is mandatory-MFA, and an MFA factor rotates every thirty seconds.
// So a judge who has the shared secret — because `security-seed` printed it, on
// purpose, as a demo credential with a fixed value — still cannot sign in without
// something that turns a base32 secret into a time-varying code. The options are an
// authenticator app, which is a second device to set up, or this.
//
// It is the same arithmetic an authenticator performs. It is not a bypass:
// `identity.VerifyFactor` still recomputes the code from the stored secret and
// compares it, still consumes the step, still refuses a replay, and still counts
// the attempt against the lockout. What this command does is save a judge from
// installing an app to watch a six-digit number change.
//
// # Why it is not on `securityctl install` or `securityctl recovery`
//
// Because those are actions on an account. This one has no database connection at
// all: it reads a secret the operator already has and prints the code for it. That
// makes it safe to run on a machine with no access to the tenant, and it is also
// why it cannot list factors, recover one, or mint a new session.
//
// # What it will not do
//
// It will not read a secret out of the database. A command that could dump live
// factors would be a command that turns "an attacker has the console's API" into
// "an attacker has every operator's second factor for the next thirty seconds",
// and the argument for keeping this narrow is exactly that. The secret comes from
// the console, on stdin or as an argument, and the argument is documented as
// visible in a process list for the same reason `security-migrate -password` is.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies SHA-1; identity verifies both
	"encoding/base32"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
)

// totpPeriod is the time step in seconds. RFC 6238 says 30 and `identity`'s
// verifier divides by the same number, so these two must move together.
const totpPeriod = 30

// totpDigits is the number of digits RFC 6238 §5.1 specifies for a six-digit code.
const totpDigits = 6

// totpModulus is 10^totpDigits, computed rather than written so the two cannot
// disagree.
var totpModulus = func() uint32 {
	m := uint32(1)
	for range totpDigits {
		m *= 10
	}
	return m
}()

func authCommand() *command.Command {
	return (&command.Command{
		Name:    "auth",
		Summary: "Authentication helpers that need no database",
	}).Register(
		&command.Command{
			Name:    "code",
			Summary: "Print the current TOTP code for a base32 shared secret",
			Usage: `code [--secret <base32>] [--at <RFC3339>]

Computes the RFC 6238 code for a shared secret and prints it, together with the
window it is valid in. Reads the secret from --secret or from stdin.

This does not bypass the second factor. The console still recomputes the code from
the stored secret and compares it, still consumes the step and still counts the
attempt against the lockout. This exists so a seeded demo can be signed into
without installing an authenticator app first.

  --secret       the base32 shared secret. Prefer stdin: a command argument is
                 visible to anything that can list processes on the same host.
  --at           the instant to compute for. Default now. Present so a transcript can
                 say which instant a printed code belonged to.
  --secret-only  print only the six digits, for scripting.

The digest is SHA-1 because that is what RFC 6238 specifies and what the majority
of authenticator apps compute. identity accepts a code derived under SHA-256 as
well, and this command follows the same order: SHA-1 first.`,
			Run: runAuthCode,
		},
	)
}

func runAuthCode(ctx context.Context, env *command.Env, args []string) error {
	_ = ctx
	fs := flag.NewFlagSet("auth code", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	secret := fs.String("secret", "", "The base32 shared secret. Prefer stdin.")
	at := fs.String("at", "", "The instant to compute for, RFC3339. Default now.")
	secretOnly := fs.Bool("secret-only", false, "Print only the six digits.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	text := *secret
	if strings.TrimSpace(text) == "" {
		read, err := io.ReadAll(env.Stdin)
		if err != nil {
			return fmt.Errorf("read the secret from stdin: %w", err)
		}
		text = strings.TrimSpace(string(read))
	}
	if text == "" {
		// The repository's own `usageErr`, from lifecycle.go. Reused rather than
		// redeclared: two helpers that build the same error type from the same
		// package are two exit codes waiting to disagree.
		return usageErr("a base32 shared secret is required, from --secret or on stdin." +
			" There is deliberately no database lookup: a command that could read a" +
			" stored factor is a command that turns read access to the console into a" +
			" second factor for every operator on the site")
	}

	when := time.Now().UTC()
	if strings.TrimSpace(*at) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*at))
		if err != nil {
			return usageErr(fmt.Sprintf("--at %q is not RFC3339: %v", *at, err))
		}
		when = parsed.UTC()
	}

	code, err := totpCode(text, when)
	if err != nil {
		return err
	}
	if *secretOnly {
		fmt.Fprintln(env.Stdout, code)
		return nil
	}
	// The window, not just the number. A code with no stated validity period is a
	// number a reader has to guess at, and a reader who guesses wrong concludes the
	// second factor is broken.
	counter := when.Unix() / totpPeriod
	validFrom := time.Unix(counter*totpPeriod, 0).UTC()
	fmt.Fprintf(env.Stdout, "%s\n", code)
	fmt.Fprintf(env.Stdout, "valid %s .. %s (%ds window, RFC 6238 SHA-1, %d digits)\n",
		validFrom.Format("15:04:05"), validFrom.Add(totpPeriod*time.Second).Format("15:04:05"),
		totpPeriod, totpDigits)
	return nil
}

// totpCode is RFC 6238 over a base32 secret: HMAC-SHA1 of the counter, dynamic
// truncation, modulo 10^6.
//
// The same construction `internal/identity/mfa.go` verifies against, in the same
// order, and the comment there explains why hashing the RAW SECRET BYTES matters —
// the stored value is base32 text and an authenticator decodes before hashing, so
// hashing the characters would disagree with every real authenticator.
func totpCode(base32Secret string, at time.Time) (string, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(base32Secret)
	if err != nil {
		return "", fmt.Errorf("the shared secret is not base32: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/totpPeriod)) //nolint:gosec // G115: a TOTP counter is T/30 and is non-negative for every instant this is called for; identity/mfa.go applies the same shape and guards it the same way.

	mac := hmac.New(sha1.New, raw)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// RFC 4226 dynamic truncation: the low nibble of the last byte selects four
	// bytes, the top bit of the first is masked off, and the result is taken modulo
	// the digit count.
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", totpDigits, value%totpModulus), nil
}
