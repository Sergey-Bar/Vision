package nlsearch

// The pinned host: where a search request may go, decided in the repository and
// not at deploy time.
//
// # The threat, stated plainly
//
// A search gateway sends an operator's question — which contains a tenant id, a
// site name, a window, and whatever an operator typed — to a model somewhere. If
// the destination is configuration, then whoever can change the configuration can
// redirect every question this product asks. That is a data-exfiltration path with
// a config key, and it is the reason this file exists rather than a constant.
//
// So: **one host, committed, with the prompt and its digest beside it.** A host
// that is not the committed one is refused before a request is built. Changing it is
// a diff on a committed file, which is reviewable, rather than an env var, which is
// not.
//
// # What "pinned" covers, and what it does not
//
// It covers the destination and the PROMPT. It does not make the model
// deterministic, and nothing here claims it does: a `not-run` answer this run and a
// different one next run are the same plan over the same corpus and they will say
// so. What the pin makes true is narrower and is the part that matters for a
// security claim: **the same question compiles to the same query, and that query is
// the one a reviewer read.**

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Manifest is the committed statement of where requests go.
//
// One file, at `contracts/nlsearch/host.json`, and it is a CONTRACT: changing the
// host is a breaking change under `contractcheck`'s rules, because the host is part
// of what the product promises about its own data.
type Manifest struct {
	// Schema is 1. A build that reads any other number refuses rather than guesses.
	Schema int `json:"schema"`
	// Host is the URL every request goes to. Scheme, host and port, and nothing
	// else: a manifest carrying a path would be a manifest naming an endpoint
	// inside somebody's deployment.
	Host string `json:"host"`
	// PromptSHA256 is the digest of Prompt, hex, lowercase.
	PromptSHA256 string `json:"promptSha256"`
	// Prompt is the only text this product sends. It is committed so a reviewer can
	// read exactly what goes out, which is the whole point.
	Prompt string `json:"prompt"`
	// Why is a sentence for whoever changes this next. A pin without a stated reason
	// is a pin somebody re-points during an incident.
	Why string `json:"why"`
	// NeverDo is the list of things this gateway will not ask a model to do, in the
	// operator's words. It is in the manifest because it is part of the promise.
	NeverDo []string `json:"neverDo"`
}

// Refusal cases for a host that is not the committed one.
const (
	ReasonPinnedHostMismatch = ReasonHostMismatch
)

// LoadManifest reads and verifies a manifest.
//
// Verification is not optional and is not "the hash matches". A manifest whose
// prompt does not match its own digest is refused, because the two fields are
// written by different hands at different times and the moment they disagree is the
// moment somebody edited a prompt and forgot to re-pin it.
func LoadManifest(path string) (*Manifest, *Refusal) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, Refuse(ReasonPromptMismatch,
			"the committed host manifest at "+path+" could not be read: "+err.Error()+
				". Without it this gateway does not know where it is allowed to send a "+
				"question, and a gateway that guesses is the path this whole file exists to close",
			Question{})
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, Refuse(ReasonPromptMismatch,
			"the host manifest at "+path+" is not a manifest this build understands: "+err.Error(),
			Question{})
	}
	if m.Schema != 1 {
		return nil, Refuse(ReasonPromptMismatch,
			fmt.Sprintf("the host manifest at %s declares schema %d and this build reads 1. "+
				"Refusing rather than guessing at a shape it has never seen", path, m.Schema),
			Question{})
	}
	u, err := url.Parse(m.Host)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, Refuse(ReasonUnpinnedHost,
			fmt.Sprintf("the committed host %q is not a URL with a scheme and a host. A host "+
				"without a scheme is resolved against whatever happens to be default at "+
				"runtime, which is the opposite of pinned", m.Host),
			Question{})
	}
	if u.Path != "" && u.Path != "/" {
		return nil, Refuse(ReasonUnpinnedHost,
			fmt.Sprintf("the committed host %q carries a path. This manifest pins an origin, "+
				"and a path would put the destination inside somebody's deployment routing",
				m.Host),
			Question{})
	}
	got := PromptDigest(m.Prompt)
	if !strings.EqualFold(got, m.PromptSHA256) {
		return nil, Refuse(ReasonPromptMismatch,
			fmt.Sprintf("the committed prompt hashes to %s and the manifest says %s. Somebody "+
				"edited the prompt without re-pinning it, and the next question this gateway "+
				"asks would be one nobody reviewed", got, m.PromptSHA256),
			Question{})
	}
	return &m, nil
}

// PromptDigest is the digest of a prompt.
//
// Hex lowercase, and the whole thing, because it is compared as a string and a
// truncated digest is a digest that fits in a log line and matches two prompts.
func PromptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// CheckHost refuses a destination that is not the committed one.
//
// The comparison is on the normalised origin — scheme, host and port — so
// `https://MODEL.example/v1` and `https://model.example` are the same destination
// and a manifest is not refused for a trailing slash. Everything ELSE about the URL
// is compared too, because `https://model.example?key=other` is a different
// endpoint wearing the same name.
func (m *Manifest) CheckHost(requested string) *Refusal {
	want := strings.TrimRight(m.Host, "/")
	got := strings.TrimRight(strings.TrimSpace(requested), "/")
	if got == want {
		return nil
	}
	return Refuse(ReasonUnpinnedHost,
		fmt.Sprintf("this gateway sends questions to %s and nothing else, and you asked for %s. "+
			"A search gateway whose destination is a parameter is a path from every "+
			"operator's question to wherever the parameter points, and the reason it is "+
			"committed rather than configured is %s", want, got, m.Why),
		Question{})
}

// CheckPrompt refuses a prompt whose digest is not the committed one.
//
// Same rule as the host and for the same reason: a prompt decides what a model is
// asked, which is as much a part of the destination's meaning as the address is.
func (m *Manifest) CheckPrompt(prompt string) *Refusal {
	got := PromptDigest(prompt)
	if strings.EqualFold(got, m.PromptSHA256) {
		return nil
	}
	return Refuse(ReasonPromptMismatch,
		fmt.Sprintf("the prompt hashes to %s and the committed manifest says %s. A gateway that "+
			"can be pointed at a different prompt can be made to ask for anything the model "+
			"will answer, and the query it builds from the answer is not one anybody read",
			got, m.PromptSHA256),
		Question{})
}

// Vowels is the manifest's `neverDo` list, sorted, for a console or a report.
//
// It is a list rather than prose because each entry is a separate promise and a
// paragraph makes three promises read as one.
func (m *Manifest) Vowels() []string {
	out := append([]string(nil), m.NeverDo...)
	sort.Strings(out)
	return out
}

// Request is one question on its way out.
//
// It carries the manifest it was checked against, so a caller cannot send without
// having been through [Manifest.CheckHost] and [Manifest.CheckPrompt]: there is no
// way to construct one that did not.
type Request struct {
	Manifest *Manifest
	Question Question
	Plan     Plan
	// SiteInScope is the principal's scope, re-checked at send time.
	//
	// Re-checked rather than trusted from the plan, because the plan was built
	// against a scope that can have changed — a role revoked, an assignment removed —
	// and a search is exactly the request where that matters.
	SiteInScope []string
}

// NewRequest checks the question and the destination, and returns a request that
// cannot be sent without both having passed.
func NewRequest(m *Manifest, q Question, inScope []string) (*Request, *Refusal) {
	if r := m.CheckHost(m.Host); r != nil {
		return nil, r
	}
	if r := m.CheckPrompt(m.Prompt); r != nil {
		return nil, r
	}
	if r := q.Validate(); r != nil {
		return nil, r
	}
	compiled, refusal := Compile(q, inScope)
	if refusal != nil {
		return nil, refusal
	}
	if refusal := compiled.Verify(q); refusal != nil {
		return nil, refusal
	}
	return &Request{
		Manifest:    m,
		Question:    q,
		Plan:        PlanFor(compiled, q).withBoundArgs(compiled.Args),
		SiteInScope: append([]string(nil), inScope...),
	}, nil
}

// Render is what the request would send, for an approval dialog or a transcript.
//
// The prompt is rendered IN FULL and the question is rendered as a bound
// parameter, because a transcript that shows the prompt with the question already
// spliced into it is a transcript of a different request than the one that is sent.
func (r *Request) Render() string {
	var b strings.Builder
	b.WriteString("POST " + r.Manifest.Host + "\n")
	b.WriteString("Content-Type: application/json\n")
	b.WriteString("X-Search-Prompt-SHA256: " + r.Manifest.PromptSHA256 + "\n\n")
	b.WriteString("--- prompt (committed, digest above) ---\n")
	b.WriteString(r.Manifest.Prompt + "\n")
	b.WriteString("--- bound parameters ---\n")
	b.WriteString("tenant_id       " + r.Question.TenantID + "\n")
	b.WriteString("sites           " + strings.Join(r.Question.Sites, ", ") + "\n")
	b.WriteString("wording         " + r.Question.Text + "\n")
	b.WriteString("since           " + r.windowString(r.Question.Since) + "\n")
	b.WriteString("until           " + r.windowString(r.Question.Until) + "\n")
	b.WriteString("\nThis gateway will not ask the model to:\n")
	for _, v := range r.Manifest.Vowels() {
		b.WriteString("  - " + v + "\n")
	}
	return b.String()
}

func (r *Request) windowString(t time.Time) string {
	if t.IsZero() {
		return "unbounded"
	}
	return t.UTC().Format(time.RFC3339)
}
