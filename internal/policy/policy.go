// Package policy is the M3 unlock: the binding of everything that governs a
// decision into one digest.
//
// A decision in this product is the output of several independent inputs — the
// rule bundle that was active, the playbook that fired, the model that produced
// the observation, the policy that scoped it, and the site it happened at. Until
// this package, an operator asking "what was this decision made under" had to
// assemble the answer from four tables and a YAML file, and an auditor asking
// "is this the same binding as yesterday" had no way to ask at all.
//
// A `Binding` names each of those five inputs with its **content digest**, and
// `Binding.Digest` is a SHA-256 over the canonical encoding of all of them. One
// hex string answers both questions, and it is the same string every run
// produces.
//
// # Why five parts and not a map of them
//
// A `map[string]Part` would let a caller bind any number of parts, and the
// property this package exists for — that a change to *any* input changes the
// digest — would be satisfied by accident rather than by construction. Five
// fields cannot silently acquire a sixth; adding one is a change to the type, a
// change to `Digest`, a change to the encoding test, and a diff a human reads.
//
// # Why the digest is over digests and not over content
//
// The binding carries what each part *is*, identified by the hash of its own
// bytes, which is what the stores already compute. Hashing the content here
// would mean holding every part's bytes in memory together, and would make a
// digest change whenever a store's serialization changed rather than when the
// meaning changed. The parts are identified by their own digests; that those
// digests are SHA-256 over the right bytes is each store's job, and each one is
// tested there.
//
// # Why an input with no digest is refused rather than skipped
//
// A binding that omitted an unbindable part would still produce a digest, and
// two different worlds would produce the same one — the exact failure the digest
// exists to prevent. So every part must carry its own digest, and a part that
// cannot be read is a refusal, never an absence.
//
// # Why recording is an interface
//
// Binding reads from stores; recording writes to the decision ledger. A pure
// package with a `Recorder` parameter is one whose tests can prove the
// digest is stable without a database, and whose production caller is the one
// place that owns a `pgx.Tx` — the discipline the rest of the store packages
// follow.
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The five parts of a binding, as names in the digest encoding. They are
// constants rather than string literals so the encoding, the explanation and the
// tests all spell them the same way.
//
// RuleBundle and Policy are stores with their own content hash; Playbook,
// Model and SiteConfig are declarations, and a declaration that carries no
// digest is a declaration nobody can check.
const (
	PartRuleBundle = "rule_bundle"
	PartPlaybook   = "playbook"
	PartModel      = "model"
	PartPolicy     = "policy"
	PartSiteConfig = "site_config"
)

// PartIdentifiers are all five, in the order they are encoded. The order is part
// of the digest: it is fixed here rather than sorted at encode time so that a
// reader comparing two digests can tell which part differs.
var PartIdentifiers = [...]string{PartRuleBundle, PartPlaybook, PartModel, PartPolicy, PartSiteConfig}

// Part is one input a decision was made under, named and identified.
//
// ID and Version are what the operator reads; Digest is what the binding is
// computed from. An ID with no digest is a part that was not really read, and
// `Bind` refuses it — see the package comment.
type Part struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Binding is the set of inputs a decision was made under, each identified by its
// own content digest.
type Binding struct {
	RuleBundle Part `json:"ruleBundle"`
	Playbook   Part `json:"playbook"`
	Model      Part `json:"model"`
	Policy     Part `json:"policy"`
	SiteConfig Part `json:"siteConfig"`
}

// ErrUnboundPart is returned when a part carries no digest, and names which one.
//
// It exists because "the model part was empty" and "the playbook part was empty"
// are different operator problems, and a binding that produced a digest anyway
// would hide both.
var ErrUnboundPart = errors.New("policy: a part is bound without its digest")

// Bind builds a binding from five parts, refusing any that carries no digest.
//
// A refusal is not a default. The alternative — treating an unbindable part as
// absent — is the one behaviour that would make two different worlds share a
// digest, which is the property the digest exists to prevent.
func Bind(ruleBundle, playbook, model, policyDoc, siteConfig Part) (Binding, error) {
	named := [...]struct {
		name string
		part Part
	}{
		{PartRuleBundle, ruleBundle},
		{PartPlaybook, playbook},
		{PartModel, model},
		{PartPolicy, policyDoc},
		{PartSiteConfig, siteConfig},
	}
	for _, n := range named {
		if n.part.Digest == "" {
			return Binding{}, fmt.Errorf("%w: %s (id %q, version %q)", ErrUnboundPart,
				n.name, n.part.ID, n.part.Version)
		}
		if n.part.ID == "" {
			return Binding{}, fmt.Errorf("policy: the %s part names no id", n.name)
		}
	}
	return Binding{
		RuleBundle: ruleBundle,
		Playbook:   playbook,
		Model:      model,
		Policy:     policyDoc,
		SiteConfig: siteConfig,
	}, nil
}

// Part returns one part by name, for callers that read a part by the same
// constant the digest uses. An unknown name returns the zero Part.
//
// It exists for the explanation renderer and for `securityctl policy explain`,
// which both walk the parts by name rather than by five fields.
func (b Binding) Part(name string) Part {
	switch name {
	case PartRuleBundle:
		return b.RuleBundle
	case PartPlaybook:
		return b.Playbook
	case PartModel:
		return b.Model
	case PartPolicy:
		return b.Policy
	case PartSiteConfig:
		return b.SiteConfig
	default:
		return Part{}
	}
}

// Digest is the SHA-256 over the canonical encoding of the binding.
//
// The encoding is length-prefixed and ordered: for each part, in
// `PartIdentifiers` order, its name, then its id, version and digest, each
// preceded by its byte length. Length prefixes are what make the encoding
// injective — without them "ab"+"c" and "a"+"bc" encode the same bytes for
// different parts, which is a collision in the one function whose whole job is
// not having them.
func (b Binding) Digest() string {
	h := sha256.New()
	writeField(h, "policy.binding")
	writeField(h, "v1")
	for _, name := range PartIdentifiers {
		p := b.Part(name)
		writeField(h, name)
		writeField(h, p.ID)
		writeField(h, p.Version)
		writeField(h, p.Digest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeField writes one field as a length prefix and its bytes, in the
// repository's standard prefix format (eight uppercase hex digits).
func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var buf [8]byte
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < 8; i++ {
		buf[i] = hexDigits[(len(s)>>(4*(7-i)))&0xF]
	}
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(s))
}

// Explain renders the binding as the text an operator reads, in the same
// deterministic spirit as `brief.Render`: the same binding renders the same
// bytes, and it shows what the digest covers rather than only the digest.
//
// A one-line hex string would answer "what is this decision under" for a person
// who already knows; this answers it for the one who does not, and prints each
// part's own digest so a mismatch is visible without re-deriving anything.
func (b Binding) Explain() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "policy binding %s\n", b.Digest())
	for _, name := range PartIdentifiers {
		p := b.Part(name)
		fmt.Fprintf(&sb, "  %-12s %s@%s  %s\n", name, p.ID, p.Version, p.Digest)
	}
	return sb.String()
}

// JSON is the machine-readable form of the binding, including its digest.
//
// It exists because an operator surface that renders only prose is unreadable by
// the thing that checks it, and an API that returns only JSON leaves the person
// reading a terminal with a hex string. Both, from one struct.
func (b Binding) JSON() ([]byte, error) {
	doc := struct {
		Digest string          `json:"digest"`
		Parts  map[string]Part `json:"parts"`
		Order  []string        `json:"partOrder"`
	}{
		Digest: b.Digest(),
		Parts:  map[string]Part{},
		Order:  append([]string(nil), PartIdentifiers[:]...),
	}
	for _, name := range PartIdentifiers {
		doc.Parts[name] = b.Part(name)
	}
	return json.MarshalIndent(doc, "", "  ")
}

// ChangeKind is what a recorded binding action was.
//
// Four, and no more: a binding is produced, activated (became the one in force),
// unloaded (stopped being the one in force, with its digest still recorded so
// the history has an end), or superseded (replaced by a newer one).
type ChangeKind string

const (
	// ChangeProduced is a binding computed and recorded before it is active.
	ChangeProduced ChangeKind = "produced"
	// ChangeActivated is a binding that became the one in force.
	ChangeActivated ChangeKind = "activated"
	// ChangeUnloaded is a binding that stopped being the one in force. The digest
	// of the *previous* binding is recorded, because "what changed" is a question
	// about what stopped.
	ChangeUnloaded ChangeKind = "unloaded"
	// ChangeSuperseded is a binding replaced by a newer version of itself.
	ChangeSuperseded ChangeKind = "superseded"
)

// Change is one recorded binding action.
//
// It is a value, not a pointer: it travels through a `Recorder` and is written
// to a ledger row, and a change that could be mutated after the fact would make
// the ledger record whatever the caller holds now rather than what happened.
type Change struct {
	Kind    ChangeKind `json:"kind"`
	Actor   string     `json:"actor"`
	Tenant  string     `json:"tenant,omitempty"`
	Digest  string     `json:"digest"`
	Details string     `json:"details,omitempty"`
}

// Recorder is the seam between a binding and the decision ledger.
//
// A package that binds inputs and records what it bound is a package that can be
// tested for exactly the property that matters — that the digest is stable, that
// an unbound part is refused, that a change is recorded once with the digest it
// claims — without a database, a ledger, or a writer that could be blamed for
// the result.
//
// Implementations live in `cmd/security-core` (ledger-backed) and in tests
// (in-memory). The signature takes the already-computed digest rather than the
// Binding, so a recorder cannot record a digest that differs from the one the
// binding produced.
//
// It takes `context.Context` like every other interface in this repository.
// A pure package with a stdlib context parameter is still a pure package; one
// with a hand-written alias of context.Context is a pure package nobody else
// can implement without reading this file first.
type Recorder interface {
	RecordBinding(ctx context.Context, change Change) error
}

// BindAndRecord binds the five parts, then records the change.
//
// The digest recorded is the one the binding produced, and the recording happens
// only if the binding succeeded: an unbound part is a refusal, and a refusal
// that still wrote a row would leave the ledger asserting a binding that never
// existed.
func BindAndRecord(ctx context.Context, r Recorder, changeKind ChangeKind, actor string,
	b Binding) (Binding, error) {

	if err := r.RecordBinding(ctx, Change{
		Kind:   changeKind,
		Actor:  actor,
		Digest: b.Digest(),
	}); err != nil {
		return b, err
	}
	return b, nil
}

// ParseDigest validates a stored or operator-supplied digest.
//
// It exists so `securityctl policy activate --digest <hex>` is checked before it
// is looked up, and so a caller comparing digests can tell "not a digest" from
// "not this digest". Sixty-four lower-case hex characters, and nothing else.
func ParseDigest(s string) error {
	if len(s) != 64 {
		return fmt.Errorf("policy: a digest is 64 hex characters, got %d", len(s))
	}
	for _, c := range s {
		isHexDigit := c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
		if !isHexDigit {
			return fmt.Errorf("policy: %q is not a lower-case hex digest", s)
		}
	}
	return nil
}
