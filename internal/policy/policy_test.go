package policy_test

// The binding tests. The property the package exists for is that a change to
// ANY of the five inputs changes the digest, and that it changes it the same way
// every time — so the first test is the table-driven version of exactly that,
// with each input flipped one at a time and the digest compared against the
// original each time. A binding that survived one of those five would still pass
// a test that only compared whole bindings.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/policy"
)

// fixture is one complete binding. Every test that needs one starts here, so
// there is exactly one place where the parts are named.
func fixture() policy.Binding {
	return policy.Binding{
		RuleBundle: policy.Part{ID: "site-security", Version: "1", Digest: digest("bundle")},
		Playbook:   policy.Part{ID: "restricted-area", Version: "2", Digest: digest("playbook")},
		Model:      policy.Part{ID: "restricted-person", Version: "1", Digest: digest("model")},
		Policy:     policy.Part{ID: "guard-hours", Version: "3", Digest: digest("policy")},
		SiteConfig: policy.Part{ID: "hq-london", Version: "5", Digest: digest("siteconfig")},
	}
}

// digest is a real 64-hex-char digest derived from a short label, so a test can
// tell two digests apart by reading them, and so the package under test is
// handed the shape of thing it will really be given.
func digest(label string) string {
	sum := sha256.Sum256([]byte("policy-test-fixture:" + label))
	return hex.EncodeToString(sum[:])
}

// TestEveryPartIsCoveredByTheDigest is the package's whole claim, in five cases.
//
// It is the red-check for this package's control: the rule is "a change to any
// part changes the digest", the mutation is "the digest ignores one part", and
// the test goes red on exactly the part that was dropped. Delete a field from
// `writeField`'s loop in policy.go and one of these five fails.
func TestEveryPartIsCoveredByTheDigest(t *testing.T) {
	base := fixture()
	want := base.Digest()

	flips := map[string]func(b *policy.Binding){
		policy.PartRuleBundle: func(b *policy.Binding) { b.RuleBundle.Version = "2" },
		policy.PartPlaybook:   func(b *policy.Binding) { b.Playbook.Digest = digest("other") },
		policy.PartModel:      func(b *policy.Binding) { b.Model.ID = "some-other-model" },
		policy.PartPolicy:     func(b *policy.Binding) { b.Policy.Digest = digest("other") },
		policy.PartSiteConfig: func(b *policy.Binding) { b.SiteConfig.Version = "6" },
	}

	for name, flip := range flips {
		t.Run(name, func(t *testing.T) {
			mutated := fixture()
			flip(&mutated)
			if got := mutated.Digest(); got == want {
				t.Errorf("changing the %s part left the digest at %s, so the binding "+
					"cannot tell that world from this one", name, got)
			}
		})
	}

	// And the same flip, the other way: the original is still the original.
	if got := base.Digest(); got != want {
		t.Errorf("the digest moved: %s != %s", got, want)
	}
}

// TestTheDigestIsStableAcrossCalls is what makes the digest an identity rather
// than a checksum of a moment. A binding that produced a different digest each
// time would break every comparison this package exists to support.
func TestTheDigestIsStableAcrossCalls(t *testing.T) {
	b := fixture()
	first := b.Digest()
	for i := 0; i < 5; i++ {
		if got := b.Digest(); got != first {
			t.Fatalf("digest %d is %s, want %s", i, got, first)
		}
	}
}

// TestBindRefusesAPartWithNoDigest is the refusal that makes the digest mean
// something. A skipped part would produce a digest over the other four and pass
// every other test in this file.
func TestBindRefusesAPartWithNoDigest(t *testing.T) {
	complete := func() (policy.Binding, error) {
		return policy.Bind(
			policy.Part{ID: "b", Version: "1", Digest: digest("b")},
			policy.Part{ID: "p", Version: "1", Digest: digest("p")},
			policy.Part{ID: "m", Version: "1", Digest: digest("m")},
			policy.Part{ID: "d", Version: "1", Digest: digest("d")},
			policy.Part{ID: "s", Version: "1", Digest: digest("s")},
		)
	}
	if _, err := complete(); err != nil {
		t.Fatalf("a complete binding was refused: %v", err)
	}

	// Each of the five, missing its digest one at a time. The error must name
	// the part: "a digest is missing" tells an operator nothing about which
	// part they half-configured.
	cases := map[string]func(p *policy.Part){
		policy.PartRuleBundle: func(p *policy.Part) { p.Digest = "" },
		policy.PartPlaybook:   func(p *policy.Part) { p.Digest = "" },
		policy.PartModel:      func(p *policy.Part) { p.Digest = "" },
		policy.PartPolicy:     func(p *policy.Part) { p.Digest = "" },
		policy.PartSiteConfig: func(p *policy.Part) { p.Digest = "" },
	}
	for name, strip := range cases {
		t.Run(name, func(t *testing.T) {
			parts := []policy.Part{
				{ID: "b", Version: "1", Digest: digest("b")},
				{ID: "p", Version: "1", Digest: digest("p")},
				{ID: "m", Version: "1", Digest: digest("m")},
				{ID: "d", Version: "1", Digest: digest("d")},
				{ID: "s", Version: "1", Digest: digest("s")},
			}
			idx := map[string]int{
				policy.PartRuleBundle: 0, policy.PartPlaybook: 1, policy.PartModel: 2,
				policy.PartPolicy: 3, policy.PartSiteConfig: 4,
			}[name]
			strip(&parts[idx])

			_, err := policy.Bind(parts[0], parts[1], parts[2], parts[3], parts[4])
			if err == nil {
				t.Fatalf("a binding with no %s digest was accepted", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name the %s part: %v", name, err)
			}
		})
	}
}

// TestExplainShowsWhatTheDigestCovers is the operator-facing half.
//
// A digest on its own answers the question for somebody who already knows the
// answer. `Explain` exists for the one who does not, so it must print every part
// by name, with the version and the digest, or it has failed at its one job.
func TestExplainShowsWhatTheDigestCovers(t *testing.T) {
	b := fixture()
	out := b.Explain()

	if !strings.Contains(out, b.Digest()) {
		t.Error("the explanation does not carry the digest it explains")
	}
	for _, name := range policy.PartIdentifiers {
		p := b.Part(name)
		if !strings.Contains(out, name) || !strings.Contains(out, p.ID) ||
			!strings.Contains(out, p.Version) || !strings.Contains(out, p.Digest) {
			t.Errorf("the explanation omits the %s part (%s@%s)", name, p.ID, p.Version)
		}
	}
	// And the deterministic half: the same binding renders the same bytes.
	if again := b.Explain(); again != out {
		t.Error("the explanation is not stable across calls, so a golden test of it " +
			"would be flaky and an operator diffing two days would see a difference " +
			"where there is none")
	}
}

// TestJSONCarriesTheSameFacts is the machine-readable half, and it checks the
// part order is carried in the payload so a reader outside Go can reproduce the
// digest's ordering instead of guessing it.
func TestJSONCarriesTheSameFacts(t *testing.T) {
	b := fixture()
	raw, err := b.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var doc struct {
		Digest string                 `json:"digest"`
		Parts  map[string]policy.Part `json:"parts"`
		Order  []string               `json:"partOrder"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Digest != b.Digest() {
		t.Errorf("the JSON digest %s disagrees with the binding's %s", doc.Digest, b.Digest())
	}
	if len(doc.Parts) != len(policy.PartIdentifiers) {
		t.Errorf("the JSON carries %d parts, want %d", len(doc.Parts), len(policy.PartIdentifiers))
	}
	if len(doc.Order) != len(policy.PartIdentifiers) {
		t.Errorf("the JSON carries %d orders, want %d", len(doc.Order), len(policy.PartIdentifiers))
	}
	for i, name := range policy.PartIdentifiers {
		if doc.Order[i] != name {
			t.Errorf("part order [%d] is %q, want %q", i, doc.Order[i], name)
		}
		if doc.Parts[name] != b.Part(name) {
			t.Errorf("the JSON %s part disagrees with the binding", name)
		}
	}
}

// TestTheDigestEncodingIsInjective is the property behind the length prefixes.
//
// Without them, a binding of parts ("a", "bc") and ("ab", "c") encode the same
// bytes and produce the same digest — a collision in the one function whose job
// is not having them. This test builds exactly that pair and fails if they ever
// agree.
func TestTheDigestEncodingIsInjective(t *testing.T) {
	one := policy.Binding{
		RuleBundle: policy.Part{ID: "a", Version: "bc", Digest: digest("x")},
		Playbook:   policy.Part{ID: "ab", Version: "c", Digest: digest("y")},
	}
	two := policy.Binding{
		RuleBundle: policy.Part{ID: "ab", Version: "c", Digest: digest("x")},
		Playbook:   policy.Part{ID: "a", Version: "bc", Digest: digest("y")},
	}
	if one.Digest() == two.Digest() {
		t.Error("two different bindings share a digest; the encoding is not injective")
	}
}

// TestARecorderSeesExactlyWhatHappened is the ledger seam.
//
// A recorder is the one thing a caller cannot fake its way past, so it is tested
// with a recorder that fails: the binding must not be reported as recorded, and
// the error must travel. An interface this package accepts and then ignores
// would be worse than no interface.
type failingRecorder struct{}

func (failingRecorder) RecordBinding(_ context.Context, c policy.Change) error {
	return errSentinel
}

var errSentinel = &recorderError{}

type recorderError struct{}

func (*recorderError) Error() string { return "policy: the recorder refused" }

func TestARecorderSeesExactlyWhatHappened(t *testing.T) {
	b := fixture()
	_, err := policy.BindAndRecord(context.Background(), failingRecorder{},
		policy.ChangeActivated, "operator@example", b)
	if err == nil {
		t.Fatal("a recorder that refused was reported as recorded")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the recorder's error was replaced: %v", err)
	}
}

// TestThePartNamesAreTheOnesTheDigestUses guards the constant table. A part name
// that existed in one place and not the other would make `Part(name)` return
// the zero Part for a name the digest does cover, silently.
func TestThePartNamesAreTheOnesTheDigestUses(t *testing.T) {
	b := fixture()
	for _, name := range policy.PartIdentifiers {
		if got := b.Part(name); got == (policy.Part{}) {
			t.Errorf("%s is encoded in the digest but Part() returns nothing for it", name)
		}
	}
	if got := b.Part("not-a-part"); got != (policy.Part{}) {
		t.Errorf("an unknown part name returned %+v", got)
	}
}

// TestParseDigestRefusesAlmostEverything is the input half of `securityctl
// policy activate --digest`. A caller that cannot tell "not a digest" from "not
// this digest" will report a typo as amnesia.
func TestParseDigestRefusesAlmostEverything(t *testing.T) {
	good := strings.Repeat("a", 64)
	if err := policy.ParseDigest(good); err != nil {
		t.Errorf("a well-formed digest was refused: %v", err)
	}
	for _, bad := range []string{
		"", "a", strings.Repeat("a", 63), strings.Repeat("a", 65),
		strings.Repeat("A", 64), strings.Repeat("g", 64),
		strings.Repeat(" ", 64), strings.Repeat("a", 64) + "\n",
	} {
		if err := policy.ParseDigest(bad); err == nil {
			t.Errorf("%q was accepted as a digest", bad)
		}
	}
}
