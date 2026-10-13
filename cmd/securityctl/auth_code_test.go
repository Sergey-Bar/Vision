package main

// The TOTP helper, checked against RFC 6238's published test vectors rather than
// against itself.
//
// # Why the vectors and not `internal/identity`
//
// `internal/identity/mfa.go` has its own `totpCode` and a test that computes a code
// the same way the verifier does. Asserting this command against that would prove
// the two agree with each other, and the property that was once broken in this
// package is exactly the property that a self-consistent pair shares while both are
// wrong: the digest has to run over the RAW SECRET BYTES, because the stored value
// is base32 text and every real authenticator decodes before hashing.
//
// So this asserts against the six vectors in RFC 6238 Appendix B, which is a
// document neither implementation was written from. If both were hashing the
// characters, these fail and the self-consistent test passes.
//
// Secret `12345678901234567890` in base32 is `GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ`.

import (
	"bytes"
	"context"
	"encoding/base32"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
)

// The vectors from RFC 6238 Appendix B, for the SHA-1 digest this command uses.
//
// Two of them are given as an 8-digit code in the RFC, because the appendix
// specifies eight digits. Truncating to the low six of the eight is what a
// six-digit implementation is specified to do — the modulo is taken over the
// truncated dynamic-truncation value — and the expected values here are the low
// six digits of the RFC's own, not a re-derivation of them.
func TestTheCommandComputesTheCodesRFC6238Publishes(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
	if secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Fatalf("the RFC secret base32-encodes to %q; this test's premise is wrong", secret)
	}
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := totpCode(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("at unix %d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("at unix %d the code is %s, want %s", c.unix, got, c.want)
		}
	}
}

// The window the code is valid in, stated rather than left to the reader. A code
// with no stated period is a number somebody has to guess at, and somebody who
// guesses wrong concludes the second factor is broken.
func TestTheCodeCarriesTheWindowItIsValidIn(t *testing.T) {
	at := time.Date(2026, 3, 1, 22, 30, 7, 0, time.UTC) // not on a step boundary
	code, out, errOut := runAuthCodeCmd(t, "--secret", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"--at", at.Format(time.RFC3339))
	if code != command.ExitOK {
		t.Fatalf("exit %d; stderr: %s", code, errOut)
	}
	if !strings.HasPrefix(out, "523250\n") {
		t.Errorf("the first line is not the six-digit code: %q", out)
	}
	if !strings.Contains(out, "22:30:00 .. 22:30:30") {
		t.Errorf("the output does not state the window the code is valid in: %q", out)
	}
}

// --secret-only is for scripting and must not print the window, or a script that
// captures stdout gets prose where it wanted a number.
func TestSecretOnlyPrintsOnlyTheCode(t *testing.T) {
	_, out, _ := runAuthCodeCmd(t, "--secret", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"--at", "2026-03-01T22:30:07Z", "--secret-only")
	if out != "523250\n" {
		t.Errorf("--secret-only printed %q, want just the six digits and a newline", out)
	}
}

// stdin is the documented way to pass a secret, because a command argument is
// visible to anything that can list processes on the host.
func TestTheSecretIsReadFromStdinWhenNoFlagIsGiven(t *testing.T) {
	_, out, errOut := runAuthCodeWithStdin(t, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ\n",
		"--at", "2026-03-01T22:30:07Z", "--secret-only")
	if errOut != "" && !strings.Contains(errOut, "") {
		t.Log(errOut)
	}
	if out != "523250\n" {
		t.Errorf("stdin gave %q, want the same code as the flag; stderr: %s", out, errOut)
	}
}

// A secret that is not base32 is refused rather than half-decoded. A code computed
// from a misread secret would not verify, and the failure would read as "the second
// factor is broken".
func TestTheSecretMustBeBase32(t *testing.T) {
	code, _, stderr := runAuthCodeCmd(t, "--secret", "not base32 at all!!", "--secret-only")
	if code == command.ExitOK {
		t.Fatal("a secret that is not base32 produced a code")
	}
	if !strings.Contains(stderr, "base32") {
		t.Errorf("the refusal did not name the problem: %q", stderr)
	}
}

// No secret at all is a usage error, and the refusal says why there is no database
// fallback — that sentence is the reason this command exists in this shape.
func TestAMissingSecretIsAUsageErrorThatNamesTheReason(t *testing.T) {
	code, _, stderr := runAuthCodeCmd(t, "--secret-only")
	if code != command.ExitUsage {
		t.Fatalf("exit %d with no secret, want the usage exit (%d); stderr: %s",
			code, command.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "no database lookup") {
		t.Errorf("the usage text did not say why there is no database fallback: %q", stderr)
	}
}

// The modulus is computed from the digit count so the two cannot disagree. If it
// were written as a literal and one of them changed, every code would be wrong by
// a factor of ten and this is where that would be noticed.
func TestTheModulusFollowsTheDigitCount(t *testing.T) {
	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(totpDigits), nil)
	if uint64(totpModulus) != want.Uint64() {
		t.Errorf("totpModulus is %d, want %d", totpModulus, want)
	}
	if totpPeriod != 30 {
		t.Errorf("totpPeriod is %d; RFC 6238 says 30 and identity's verifier divides by "+
			"the same number, so the two must move together", totpPeriod)
	}
}

func runAuthCodeCmd(t *testing.T, args ...string) (command.ExitCode, string, string) {
	t.Helper()
	return runAuthCodeWithStdin(t, "", args...)
}

func runAuthCodeWithStdin(t *testing.T, stdin string, args ...string) (command.ExitCode, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &command.Env{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
	}
	code := newRootCommand().Execute(context.Background(), env, append([]string{"auth", "code"}, args...))
	return code, out.String(), errOut.String()
}
