// prompts/manifest.json and prompts/*.txt are embedded, not read from disk.
//
// A prompt is the largest single determinant of the answer, so the answer's
// provenance is only worth anything if the prompt cannot have drifted between
// the run and the report. Reading it from a path would make that a matter of
// what was on disk when somebody asked.
package vlm

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed prompts/manifest.json prompts/*.txt
var promptFS embed.FS

// Prompt is one incident class.
type Prompt struct {
	// ID is the incident class. It is the ONLY thing that distinguishes one
	// class from another anywhere in this module: same four output fields, same
	// clip path, same push, same bookmark. A third class is a file here and an
	// entry in the manifest, and it is not a code change.
	ID string
	// Version is the operator's label for this prompt revision. It is part of the
	// contract version, so changing the prompt changes the version.
	Version string
	// Text is the prompt sent verbatim. This package never paraphrases it: a
	// prompt that "merely resembles" the provider's documented one is not the
	// provider's prompt, and the answer would be attributed to something
	// inaccurate.
	Text string
	// Class is the human label for the report and the screen. It is never a
	// field on the wire and never a value the parser sees.
	Class string
}

// PromptSet is the embedded set, indexed by class.
type PromptSet struct {
	byID     map[string]Prompt
	manifest Manifest
}

// Manifest is prompts/manifest.json, as declared. It is a document rather than a
// directory listing so that adding a prompt file without declaring its class
// fails loudly at load time instead of being ignored.
type Manifest struct {
	Schema      int              `json:"schema"`
	OutputShape OutputShape      `json:"output_shape"`
	Prompts     []ManifestPrompt `json:"prompts"`
}

// OutputShape is the schema every prompt must demand, and it is asserted at load
// time against the Go constant. Two classes that asked for different fields
// would be two parsers, and the plan's claim is that there is one.
type OutputShape struct {
	Fields []string `json:"fields"`
}

// ManifestPrompt is one declared class.
type ManifestPrompt struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	File    string `json:"file"`
	// Class is the human label for the report and the screen. It is never a
	// field on the wire.
	Class string `json:"class"`
}

// EmbeddedPrompts loads the embedded prompt set.
//
// It refuses a manifest that does not match the Go schema: a manifest edited to
// add a field the parser does not know is a manifest whose answers would be
// validated against the wrong shape.
func EmbeddedPrompts() (*PromptSet, error) {
	raw, err := promptFS.ReadFile("prompts/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("vlm: the embedded prompt manifest is missing: %w", err)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("vlm: the embedded prompt manifest is not readable: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("vlm: the embedded prompt manifest declares schema %d; this build reads schema 1", m.Schema)
	}
	want := RequiredOutputFields()
	if len(m.OutputShape.Fields) != len(want) {
		return nil, fmt.Errorf("vlm: the manifest declares %d output fields and this build validates %d (%v); "+
			"a manifest and a parser that disagree validate nothing", len(m.OutputShape.Fields), len(want), want)
	}
	for i, f := range want {
		if m.OutputShape.Fields[i] != f {
			return nil, fmt.Errorf("vlm: the manifest's output field %d is %q and this build validates %q; "+
				"the order matters because the parser decodes a declared shape", i, m.OutputShape.Fields[i], f)
		}
	}
	set := &PromptSet{byID: map[string]Prompt{}, manifest: m}
	for _, p := range m.Prompts {
		if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.File) == "" {
			return nil, errors.New("vlm: a manifest entry has no id or no file; a prompt with no class cannot be asked for")
		}
		if _, dup := set.byID[p.ID]; dup {
			return nil, fmt.Errorf("vlm: the manifest declares the class %q twice", p.ID)
		}
		body, err := promptFS.ReadFile("prompts/" + p.File)
		if err != nil {
			return nil, fmt.Errorf("vlm: the manifest names prompts/%s and it is not embedded: %w", p.File, err)
		}
		set.byID[p.ID] = Prompt{
			ID: p.ID, Version: p.Version, Class: p.Class,
			Text: string(body) + shapeInstruction(want),
		}
	}
	if len(set.byID) == 0 {
		return nil, errors.New("vlm: the embedded manifest declares no prompts, so no class can be asked")
	}
	return set, nil
}

// RequiredOutputFields is the closed shape every prompt must demand, in the order
// the manifest declares them.
func RequiredOutputFields() []string {
	return []string{"verdict", "summary", "confidence", "insufficient_evidence"}
}

// shapeInstruction is appended to every prompt verbatim.
//
// It is appended rather than written into each file so that the required shape is
// one constant and a prompt file cannot quietly ask for something else. A file
// that wanted a different shape would have to change the parser too, which is the
// change that should get a human.
func shapeInstruction(fields []string) string {
	return "\n\nAnswer with a single JSON object and nothing else — no prose before it, no code fence, no " +
		"explanation after it. Exactly these keys, no others:\n" +
		`  {"verdict": string, "summary": string, "confidence": number, "insufficient_evidence": boolean}` + "\n" +
		"  - verdict is one of: confirmed, false_positive, needs_review, none.\n" +
		"  - summary is one or two sentences describing what is happening in the region.\n" +
		"  - confidence is a number in [0,1].\n" +
		"  - insufficient_evidence is true when the clip does not show enough to decide, and then verdict must be needs_review.\n" +
		"  If the clip does not answer the question, say so with insufficient_evidence and verdict needs_review. " +
		"Never guess, and never return an empty object.\n"
}

// Get returns one class's prompt, refusing an unknown one by name.
func (s *PromptSet) Get(id string) (Prompt, error) {
	p, ok := s.byID[id]
	if !ok {
		return Prompt{}, fmt.Errorf("vlm: no prompt for the incident class %q; this build carries %v", id, s.Classes())
	}
	return p, nil
}

// Classes lists the declared incident classes, sorted.
func (s *PromptSet) Classes() []string {
	out := make([]string, 0, len(s.byID))
	for id := range s.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Manifest is exposed for the report, which prints the classes it can ask about.
func (s *PromptSet) Declared() []ManifestPrompt { return s.manifest.Prompts }

// Ids is the alias the plan's tests use: two classes differ only in prompt id.
func (s *PromptSet) Ids() []string { return s.Classes() }
