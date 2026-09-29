package rules

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// schemaFS carries the published bundle schema inside the binary.
//
// Embedded rather than read from disk: a bundle schema that can be swapped on
// disk is a bundle schema that can be swapped by whoever can write to the
// install directory, and the point of the schema gate is that a bundle cannot be
// loaded without being checked. A test asserts this copy matches the file in
// contracts/, so the two cannot drift silently.
//
//go:embed schema/rule-bundle.schema.json
var schemaFS embed.FS

// Problem is one compilation failure, with enough context to find it in a file.
//
// Severity distinguishes "this bundle cannot load" from "this bundle loads but
// should not be activated". Lint reports both; activation refuses on either,
// because a bundle that is merely suspicious is still a bundle nobody reviewed.
type Problem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	RuleID   string `json:"ruleId,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(p.Severity)
	b.WriteString(" ")
	b.WriteString(p.Code)
	if p.RuleID != "" {
		fmt.Fprintf(&b, " [%s]", p.RuleID)
	}
	if p.Path != "" {
		fmt.Fprintf(&b, " at %s", p.Path)
	}
	b.WriteString(": ")
	b.WriteString(p.Message)
	return b.String()
}

// Problem severities. Activation refuses on either, because a bundle that is
// merely suspicious is still a bundle nobody reviewed.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Compiler turns a bundle into a CompiledBundle.
type Compiler struct {
	// schema is the bundle JSON Schema. It is compiled once and reused: schema
	// compilation is not cheap and lint runs over a whole set.
	schema *jsonschema.Schema
	once   sync.Once
	// KnownZones and KnownSchedules are what the site actually has. A rule
	// referring to a zone that does not exist is a semantic error, and it is the
	// one that a JSON Schema cannot catch.
	KnownZones     map[string]bool
	KnownSchedules map[string]bool
	// KnownAIFields maps a profile id to the structured fields its output schema
	// defines. A rule naming a field the profile does not emit can never match,
	// and an operator who believes it is running is worse off than a load error.
	KnownAIFields map[string]map[string]bool
}

// NewCompiler builds a compiler with no site knowledge, which is what `lint`
// does when it runs without a database.
func NewCompiler() *Compiler {
	return &Compiler{
		KnownZones:     map[string]bool{},
		KnownSchedules: map[string]bool{},
		KnownAIFields:  map[string]map[string]bool{},
	}
}

// Compile parses, schema-checks, semantically validates and compiles a bundle.
//
// The stages are in that order because they fail differently and the operator
// needs to know which one fired. A schema error points at a line in a file. A
// semantic error points at a rule that is well-formed and cannot work.
func (c *Compiler) Compile(raw []byte) (*CompiledBundle, []Problem) {
	var problems []Problem

	// Stage 1: YAML. Decoded with KnownFields so a typo in a key name is an
	// error rather than a silently ignored setting - the failure mode being a
	// rule that does not do what its author believes.
	var doc Bundle
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "yaml_parse",
			Message:  err.Error(),
		})
		return nil, problems
	}
	// A second document in a bundle is either a mistake or an injection attempt.
	if err := dec.Decode(new(any)); err == nil {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Code:     "yaml_multiple_documents",
			Message:  "a bundle must contain exactly one YAML document",
		})
		return nil, problems
	}

	// Stage 2: JSON Schema. The typed decode above cannot express every
	// constraint the published schema states, and the published schema is what a
	// third party writing a bundle editor will read. Both run.
	problems = append(problems, c.checkSchema(raw)...)

	// Stage 3 and 4: semantic validation and compilation. These are one pass
	// because a compiled rule and the checks it failed are the same traversal.
	compiled := compileBundle(&doc)
	problems = append(problems, c.semantic(&doc)...)

	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].RuleID != problems[j].RuleID {
			return problems[i].RuleID < problems[j].RuleID
		}
		return problems[i].Code < problems[j].Code
	})

	if hasErrors(problems) {
		return nil, problems
	}
	return compiled, problems
}

func hasErrors(problems []Problem) bool {
	for _, p := range problems {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

// checkSchema validates the raw YAML against the published bundle schema,
// after converting it to JSON so one schema serves both formats.
func (c *Compiler) checkSchema(raw []byte) []Problem {
	c.once.Do(func() {
		schemaBytes, err := schemaFS.ReadFile("schema/rule-bundle.schema.json")
		if err != nil {
			c.schema = nil
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
		if err != nil {
			c.schema = nil
			return
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("rule-bundle.schema.json", doc); err != nil {
			c.schema = nil
			return
		}
		c.schema, err = compiler.Compile("rule-bundle.schema.json")
		if err != nil {
			c.schema = nil
		}
	})
	if c.schema == nil {
		// A compiler that cannot load its own schema is a build problem, and it
		// is reported rather than skipped: silently skipping the gate is how an
		// invalid bundle reaches a site.
		return []Problem{{
			Severity: SeverityError,
			Code:     "schema_unavailable",
			Message:  "the bundle JSON Schema is missing from this build; refusing to compile without it",
		}}
	}

	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []Problem{{Severity: SeverityError, Code: "yaml_parse", Message: err.Error()}}
	}
	normalized, err := normalizeYAML(doc)
	if err != nil {
		return []Problem{{Severity: SeverityError, Code: "yaml_to_json", Message: err.Error()}}
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(normalized))
	if err != nil {
		return []Problem{{Severity: SeverityError, Code: "yaml_to_json", Message: err.Error()}}
	}
	if err := c.schema.Validate(instance); err != nil {
		return []Problem{{
			Severity: SeverityError,
			Code:     "schema_violation",
			Message:  err.Error(),
		}}
	}
	return nil
}

// normalizeYAML converts a YAML document into the JSON shape the schema
// describes. YAML allows non-string map keys and typed scalars; JSON does not,
// and a bundle using one is a bundle the schema cannot describe.
func normalizeYAML(v any) ([]byte, error) {
	converted, err := convertYAML(v, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func convertYAML(v any, path string) (any, error) {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			c, err := convertYAML(child, joinPath(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("%s: map key %v is not a string; a bundle must be JSON-shaped", path, k)
			}
			c, err := convertYAML(child, joinPath(path, key))
			if err != nil {
				return nil, err
			}
			out[key] = c
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(node))
		for i, child := range node {
			c, err := convertYAML(child, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	case int, int64, uint64, float64, string, bool, nil:
		return node, nil
	case time.Time:
		// YAML auto-parses timestamps. A timestamp in a bundle is almost always
		// a string the author meant literally, and accepting one here would make
		// the canonical form depend on the parser.
		return node.Format(time.RFC3339), nil
	default:
		return nil, fmt.Errorf("%s: unsupported YAML node of type %T", path, v)
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// ContentHash is the identity of a bundle's bytes, in the canonical JSON form.
func ContentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
