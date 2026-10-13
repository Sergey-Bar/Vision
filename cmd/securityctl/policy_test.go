package main

// The `policy` command's tests.
//
// The command is thin by design — it parses, binds, and renders — so the tests
// that matter are the ones about what it refuses. A CLI that accepted a part
// with no digest, or a version that was absent, would produce a digest that
// looked just as valid as a real one, and the operator would have no way to
// tell.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/command"
)

// testEnv is a command environment whose streams are buffers, so a test can read
// what the operator would have seen. Both come back because `policy explain`'s
// `--json` form is asserted against its text form in one test and the refusals
// against stdout in another, and a helper that returned only one would make one
// of those reach into the command's env to get at it.
func testEnv() (*command.Env, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &command.Env{Stdout: &out, Stderr: &errOut}, &out
}

// writeParts writes a parts file to a temp dir and returns its path.
func writeParts(t *testing.T, doc map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "parts.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func goodParts() map[string]string {
	return map[string]string{
		"ruleBundle": "site-security@1@" + strings.Repeat("1", 64),
		"playbook":   "restricted-area@2@" + strings.Repeat("2", 64),
		"model":      "restricted-person@1@" + strings.Repeat("3", 64),
		"policy":     "guard-hours@3@" + strings.Repeat("4", 64),
		"siteConfig": "hq-london@5@" + strings.Repeat("5", 64),
	}
}

// TestPolicyBindPrintsTheDigestThePackageComputes is the whole command's happy
// path, and it asserts against the package rather than against a stored value:
// a CLI and a package that disagreed about the digest would each look fine on
// its own.
func TestPolicyBindPrintsTheDigestThePackageComputes(t *testing.T) {
	env, out := testEnv()
	if err := runPolicyBind(context.Background(), env,
		[]string{"--parts", writeParts(t, goodParts())}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	got := strings.TrimSpace(out.String())
	if len(got) != 64 {
		t.Fatalf("bind printed %q, want a 64-character digest", got)
	}

	// The same facts through the package, through the command's own reader.
	b, err := readParts(writeParts(t, goodParts()))
	if err != nil {
		t.Fatalf("readParts: %v", err)
	}
	if got != b.Digest() {
		t.Errorf("the command printed %s, the package computes %s", got, b.Digest())
	}
}

// TestPolicyExplainRendersEveryPart is the operator-facing half, tested against
// the package's own rendering rather than a golden string: a golden string would
// pass while the command rendered something stale.
func TestPolicyExplainRendersEveryPart(t *testing.T) {
	env, out := testEnv()
	if err := runPolicyExplain(context.Background(), env,
		[]string{"--parts", writeParts(t, goodParts())}); err != nil {
		t.Fatalf("explain: %v", err)
	}
	rendered := out.String()
	b, err := readParts(writeParts(t, goodParts()))
	if err != nil {
		t.Fatalf("readParts: %v", err)
	}
	if !strings.Contains(rendered, b.Digest()) {
		t.Errorf("the rendered binding omits its own digest %s", b.Digest())
	}
	for _, name := range []string{"rule_bundle", "playbook", "model", "policy", "site_config"} {
		if !strings.Contains(rendered, name) {
			t.Errorf("the rendered binding omits the %s part", name)
		}
	}
}

// TestPolicyExplainJSONCarriesTheSameDigest checks the machine form is not a
// second opinion: it must agree with the text form's digest.
func TestPolicyExplainJSONCarriesTheSameDigest(t *testing.T) {
	env, out := testEnv()
	if err := runPolicyExplain(context.Background(), env,
		[]string{"--json", "--parts", writeParts(t, goodParts())}); err != nil {
		t.Fatalf("explain --json: %v", err)
	}
	var doc struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Digest == "" {
		t.Fatal("the JSON form carries no digest")
	}

	textEnv, textOut := testEnv()
	if err := runPolicyExplain(context.Background(), textEnv,
		[]string{"--parts", writeParts(t, goodParts())}); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(textOut.String(), doc.Digest) {
		t.Errorf("the text form and the JSON form disagree: %s is in neither",
			doc.Digest)
	}
}

// TestAPartWithNoDigestIsRefused is the command's most important refusal.
//
// It is the CLI half of `ErrUnboundPart`: a part whose digest is missing would
// otherwise bind to four parts and produce a digest that looks exactly as valid
// as a real one — the failure the digest exists to prevent, arriving through a
// flag instead of a library call.
func TestAPartWithNoDigestIsRefused(t *testing.T) {
	parts := goodParts()
	parts["model"] = "restricted-person@1"

	env, _ := testEnv()
	err := runPolicyBind(context.Background(), env,
		[]string{"--parts", writeParts(t, parts)})
	if err == nil {
		t.Fatal("a part with no digest was bound")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("the refusal does not name the part at fault: %v", err)
	}
}

// TestAMalformedDigestIsRefusedBeforeItBinds covers the shape check. A digest
// that is not 64 lower-case hex characters is refused at the parse, so the
// operator learns their input was wrong here rather than looking up a digest
// nothing can match.
func TestAMalformedDigestIsRefusedBeforeItBinds(t *testing.T) {
	for _, bad := range []string{
		"restricted-person@1@nothex",
		"restricted-person@1@" + strings.Repeat("A", 64),
		"restricted-person@1@" + strings.Repeat("z", 64),
		"restricted-person@1@" + strings.Repeat("1", 63),
	} {
		parts := goodParts()
		parts["model"] = bad
		env, _ := testEnv()
		err := runPolicyBind(context.Background(), env,
			[]string{"--parts", writeParts(t, parts)})
		if err == nil {
			t.Errorf("the part %q was bound", bad)
		}
	}
}

// TestAnAbsentPartIsRefusedNamesWhichOne covers the missing-five case, and
// asserts the message names the part rather than saying "invalid input".
func TestAnAbsentPartIsRefusedNamedWhichOne(t *testing.T) {
	for name := range goodParts() {
		parts := goodParts()
		delete(parts, name)
		env, _ := testEnv()
		err := runPolicyBind(context.Background(), env,
			[]string{"--parts", writeParts(t, parts)})
		if err == nil {
			t.Errorf("a parts file with no %s was bound", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal for a missing %s does not name it: %v", name, err)
		}
	}
}

// TestTheMissingPartsFlagIsAUsageError checks the flag discipline: a command run
// without --parts reports a usage error, not a nil-pointer panic or a silent
// success with an empty binding.
func TestTheMissingPartsFlagIsAUsageError(t *testing.T) {
	env, _ := testEnv()
	err := runPolicyBind(context.Background(), env, nil)
	if err == nil {
		t.Fatal("bind without --parts succeeded")
	}
	var usage *command.UsageError
	if !errors.As(err, &usage) {
		t.Errorf("bind without --parts returned %T, want a UsageError", err)
	}
}

// TestAUsageErrorIsWrittenToStderrNotStdout is the stream discipline for the
// flag path. A command whose usage error went to stdout would let a script pipe
// it into a digest comparison, so the error path is asserted to write nothing a
// caller could mistake for output.
func TestAUsageErrorIsWrittenToStderrNotStdout(t *testing.T) {
	env, out := testEnv()
	_ = runPolicyBind(context.Background(), env, nil)
	if got := out.String(); got != "" {
		t.Errorf("a usage error wrote %q to stdout", got)
	}
}

// TestARefusalLeavesStdoutClean is the stream discipline.
//
// A command that printed its refusal to stdout would let a script pipe the
// refusal into a digest comparison and conclude the binding was fine. The
// command returns its error to the framework, which prints it to stderr, and
// this asserts the half it owns: stdout carries nothing a caller could mistake
// for a digest.
func TestARefusalLeavesStdoutClean(t *testing.T) {
	env, out := testEnv()
	err := runPolicyBind(context.Background(), env,
		[]string{"--parts", writeParts(t, func() map[string]string {
			p := goodParts()
			delete(p, "model")
			return p
		}())})
	if err == nil {
		t.Fatal("a binding with no model part was accepted")
	}
	if got := out.String(); got != "" {
		t.Errorf("a refused bind wrote %q to stdout", got)
	}
}

// TestThePartsFlagMustNameAFile covers the file-not-found path.
func TestThePartsFlagMustNameAFile(t *testing.T) {
	env, _ := testEnv()
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	err := runPolicyBind(context.Background(), env, []string{"--parts", missing})
	if err == nil {
		t.Fatal("bind against a missing file succeeded")
	}
}

// TestAPartsFileWithWrongFieldShapeIsRefused covers the JSON malformed path,
// which is what an operator gets when they hand the command a binding output
// instead of a parts file.
func TestAPartsFileWithWrongFieldShapeIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrong.json")
	body, err := json.Marshal(map[string]any{"digest": strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	env, _ := testEnv()
	if err := runPolicyBind(context.Background(), env,
		[]string{"--parts", path}); err == nil {
		t.Fatal("a parts file with no parts was bound")
	}
}
