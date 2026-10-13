package main

// policy — the M3 unlock's operator surface.
//
// A decision in this product is made under five things: the rule bundle that was
// active, the playbook that fired, the model that produced the observation, the
// policy that scoped it, and the site it happened at. Until `internal/policy`,
// an operator asking "what was this decision made under" had to assemble the
// answer from four tables and a YAML file, and an auditor asking "is this the
// same binding as yesterday" had no way to ask at all.
//
// This command renders what `internal/policy` computes: `explain` prints the
// binding with each part's own digest, and `bind` prints the digest over those
// same facts. Both take the parts the same way, so the two answers cannot
// disagree about what they are describing.
//
// It deliberately writes nothing. Binding is a fact about the world; recording a
// binding is a separate act, owned by `cmd/security-core` where the decision
// ledger's writer already lives, and gated there by whoever is allowed to
// activate a policy. A CLI that could record a binding would be a CLI that could
// put a digest in the ledger without an actor saying so.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/policy"
)

func policyCommand() *command.Command {
	c := &command.Command{
		Name:    "policy",
		Summary: "Bind and explain what a decision is made under",
		Usage: `policy — the binding an operator or an auditor reads.

A binding is the five inputs a decision was made under, each identified by its
own content digest. The binding's digest is a SHA-256 over all of them, so one
hex string answers "what was this decision made under" and answers it the same
way on every run.

  policy bind    --parts <file>                 print the binding's digest
  policy explain --parts <file> [--json]        render the binding, and it says what it covers

Each part is id@version@digest, and all five are required: a part with no
digest is refused rather than skipped, because a binding that omitted one would
still produce a digest and two different worlds would share it.

The parts file is JSON:

  {
    "ruleBundle": "site-security@1@<64 hex>",
    "playbook":   "restricted-area@2@<64 hex>",
    "model":      "restricted-person@1@<64 hex>",
    "policy":     "guard-hours@3@<64 hex>",
    "siteConfig": "hq-london@5@<64 hex>"
  }

` + policyPartOrderLine(),
	}
	c.Register(
		simpleFlag("bind", "Print the digest of the binding described by --parts", runPolicyBind),
		simpleFlag("explain", "Render the binding, with every part's own digest", runPolicyExplain),
	)
	return c
}

func policyPartOrderLine() string {
	var sb strings.Builder
	sb.WriteString("The five parts, in the order the digest encodes them:\n")
	for i, name := range policy.PartIdentifiers {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, name)
	}
	return sb.String()
}

// partsFile is the JSON form the command reads. Field names are the ones
// `Binding.JSON` writes, so an operator can pipe one into the other.
type partsFile struct {
	RuleBundle string `json:"ruleBundle"`
	Playbook   string `json:"playbook"`
	Model      string `json:"model"`
	Policy     string `json:"policy"`
	SiteConfig string `json:"siteConfig"`
}

// partsFlag is the --parts value, read once so both subcommands agree.
func partsFlag(fs *flag.FlagSet) *string {
	return fs.String("parts", "", "Path to the JSON file describing the five parts. Required.")
}

// readParts reads and binds the parts file.
//
// The parse of "id@version@digest" is strict on the digest: a part whose digest
// is not 64 lower-case hex characters is refused here rather than producing a
// digest the operator cannot look up later.
func readParts(path string) (policy.Binding, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator named the file
	if err != nil {
		return policy.Binding{}, err
	}
	var pf partsFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return policy.Binding{}, err
	}
	// Named twice on purpose: `key` is what the operator wrote in their file and
	// what a refusal must quote back at them, `part` is what the package calls it.
	// A refusal that said "site_config is absent" against a file that says
	// "siteConfig" is a refusal for a different document.
	parts := []struct {
		key   string
		part  string
		value string
	}{
		{"ruleBundle", policy.PartRuleBundle, pf.RuleBundle},
		{"playbook", policy.PartPlaybook, pf.Playbook},
		{"model", policy.PartModel, pf.Model},
		{"policy", policy.PartPolicy, pf.Policy},
		{"siteConfig", policy.PartSiteConfig, pf.SiteConfig},
	}
	parsed := map[string]policy.Part{}
	for _, part := range parts {
		if strings.TrimSpace(part.value) == "" {
			return policy.Binding{}, fmt.Errorf("policy: the %s part is absent from %s",
				part.key, path)
		}
		p, err := parsePart(part.key, part.value)
		if err != nil {
			return policy.Binding{}, err
		}
		parsed[part.part] = p
	}
	return policy.Bind(
		parsed[policy.PartRuleBundle],
		parsed[policy.PartPlaybook],
		parsed[policy.PartModel],
		parsed[policy.PartPolicy],
		parsed[policy.PartSiteConfig],
	)
}

// parsePart splits "id@version@digest" and validates the digest.
//
// Three fields and exactly three: a version is not optional (an unversioned
// part is an unpinnable one) and a digest is not optional (see the package
// comment on `ErrUnboundPart`).
func parsePart(name, value string) (policy.Part, error) {
	fields := strings.Split(value, "@")
	if len(fields) != 3 {
		return policy.Part{}, fmt.Errorf("policy: the %s part is %q, want id@version@digest",
			name, value)
	}
	id, version, digest := fields[0], fields[1], fields[2]
	if id == "" {
		return policy.Part{}, fmt.Errorf("policy: the %s part names no id", name)
	}
	if version == "" {
		return policy.Part{}, fmt.Errorf("policy: the %s part names no version", name)
	}
	if err := policy.ParseDigest(digest); err != nil {
		return policy.Part{}, fmt.Errorf("policy: the %s part: %w", name, err)
	}
	return policy.Part{ID: id, Version: version, Digest: digest}, nil
}

func runPolicyBind(_ context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("policy bind", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	parts := partsFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *parts == "" {
		return &command.UsageError{Err: errors.New("pass the parts with --parts")}
	}
	b, err := readParts(*parts)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "%s\n", b.Digest())
	return nil
}

func runPolicyExplain(_ context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("policy explain", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	parts := partsFlag(fs)
	asJSON := fs.Bool("json", false, "Emit machine-readable output.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *parts == "" {
		return &command.UsageError{Err: errors.New("pass the parts with --parts")}
	}
	b, err := readParts(*parts)
	if err != nil {
		return err
	}
	if *asJSON {
		raw, err := b.JSON()
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s\n", raw)
		return nil
	}
	fmt.Fprint(env.Stdout, b.Explain())
	return nil
}
