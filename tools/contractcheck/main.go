// Command contractcheck enforces contract compatibility across every published
// contract surface: the protobuf wire, the JSON Schemas, and the OpenAPI
// document.
//
// These files are the product's public surface: adapters, the AI runtime, site
// config files, uploaded rule bundles and stored rows all depend on them. A
// change that breaks an existing field number, removes a message, drops a
// schema property, retypes one, removes an enum value, or deletes an endpoint
// silently corrupts or refuses data that is already in the field. This command
// turns that rule into a build failure.
//
// The surface is chosen by what is under -root, so the same baseline file holds
// all three sections and each is checked by pointing at its own tree:
//
//	contractcheck -check -root contracts/protobuf
//	contractcheck -check -root contracts/json-schema
//	contractcheck -check -root contracts/openapi
//	contractcheck -write -root contracts/protobuf        # only for a new version
//
// Risk R4: the tool had never diffed the JSON Schemas or the OpenAPI document,
// so the first run over them surfaces facts that were always true rather than
// changes that were just made. To keep a baseline refresh honest, -write is
// refused if it would drop or retype anything already in the baseline, unless
// -allow-breaking is passed. A refresh is therefore a pure addition by
// construction, not by review discipline alone.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Baseline struct {
	// Key is "package.Message" or "package.Enum".
	Messages map[string]Message `json:"messages"`
	Enums    map[string]Enum    `json:"enums"`
	// Schemas is keyed by the schema's path relative to -root.
	Schemas map[string]Schema `json:"schemas,omitempty"`
	// OpenAPI is keyed by the document's path relative to -root.
	OpenAPI map[string]APIDoc `json:"openapi,omitempty"`
}

func newBaseline() *Baseline {
	return &Baseline{
		Messages: map[string]Message{},
		Enums:    map[string]Enum{},
		Schemas:  map[string]Schema{},
		OpenAPI:  map[string]APIDoc{},
	}
}

type surface int

const (
	surfaceProto surface = iota
	surfaceSchema
	surfaceOpenAPI
)

func (s surface) String() string {
	switch s {
	case surfaceSchema:
		return "json-schema"
	case surfaceOpenAPI:
		return "openapi"
	default:
		return "protobuf"
	}
}

type finding struct {
	Symbol string
	Kind   string
	Detail string
}

// detectSurface decides which reader to use from the contents of -root. A wrong
// guess is a silent pass, so a root with nothing recognisable in it is an
// error rather than a default.
func detectSurface(root string) (surface, error) {
	stat, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !stat.IsDir() {
		return 0, fmt.Errorf("%s is not a directory", root)
	}
	counts := map[surface]int{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".proto"):
			counts[surfaceProto]++
		case strings.HasSuffix(path, ".schema.json"):
			counts[surfaceSchema]++
		case isYAML(path):
			counts[surfaceOpenAPI]++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, s := range []surface{surfaceProto, surfaceSchema, surfaceOpenAPI} {
		if counts[s] > 0 {
			return s, nil
		}
	}
	return 0, fmt.Errorf("%s contains no .proto, .schema.json or .yaml files; "+
		"refusing to guess which contract surface to check", root)
}

func isYAML(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

func readSurface(s surface, root string) (*Baseline, []string, error) {
	c := newBaseline()
	switch s {
	case surfaceSchema:
		m, err := parseSchemaTree(root)
		if err != nil {
			return nil, nil, err
		}
		normalizeSchemas(m)
		c.Schemas = m
	case surfaceOpenAPI:
		m, warnings, err := parseOpenAPITree(root)
		if err != nil {
			return nil, nil, err
		}
		normalizeOpenAPI(m)
		c.OpenAPI = m
		return c, warnings, nil
	default:
		cur, err := parseProtoTree(root)
		if err != nil {
			return nil, nil, err
		}
		normalizeProto(cur)
		c.Messages, c.Enums = cur.Messages, cur.Enums
	}
	return c, nil, nil
}

// merge copies the freshly read section of `cur` over `base`, leaving the other
// sections alone. A protobuf run must not delete the schema section just
// because the protobuf reader knows nothing about it.
func merge(base, cur *Baseline, s surface) {
	switch s {
	case surfaceSchema:
		base.Schemas = cur.Schemas
	case surfaceOpenAPI:
		base.OpenAPI = cur.OpenAPI
	default:
		base.Messages, base.Enums = cur.Messages, cur.Enums
	}
}

func compareSurface(s surface, base, cur *Baseline) []finding {
	switch s {
	case surfaceSchema:
		return compareSchemas(base.Schemas, cur.Schemas)
	case surfaceOpenAPI:
		return compareOpenAPI(base.OpenAPI, cur.OpenAPI)
	default:
		return compareProto(base, cur)
	}
}

func empty(s surface, c *Baseline) bool {
	switch s {
	case surfaceSchema:
		return len(c.Schemas) == 0
	case surfaceOpenAPI:
		return len(c.OpenAPI) == 0
	default:
		return len(c.Messages) == 0 && len(c.Enums) == 0
	}
}

func count(s surface, c *Baseline) string {
	switch s {
	case surfaceSchema:
		n := 0
		for _, sc := range c.Schemas {
			n += len(sc.Properties)
		}
		return fmt.Sprintf("%d schemas, %d properties", len(c.Schemas), n)
	case surfaceOpenAPI:
		n := 0
		for _, d := range c.OpenAPI {
			n += len(d.Paths)
		}
		return fmt.Sprintf("%d documents, %d paths", len(c.OpenAPI), n)
	default:
		return fmt.Sprintf("%d messages, %d enums", len(c.Messages), len(c.Enums))
	}
}

// writeRefusal is the R4 control. A baseline refresh may only add. The first
// run of the tool over a surface it had never diffed surfaces facts that were
// always true, and without this a human regenerating the file would bake a
// genuine regression into the baseline and call it housekeeping. Opting out is
// deliberate and named on the command line.
func writeRefusal(findings []finding, allowBreaking bool) error {
	if allowBreaking || len(findings) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d breaking change(s) against the current baseline.\n"+
		"A baseline refresh is a pure addition; anything below removes or retypes "+
		"something that is already published.\n", len(findings))
	report(findings, &b)
	b.WriteString("\nIf this is intentionally a new contract version, pass -allow-breaking and\n" +
		"have a human read the diff before it lands.\n")
	return errors.New(b.String())
}

func report(findings []finding, w io.Writer) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Symbol != findings[j].Symbol {
			return findings[i].Symbol < findings[j].Symbol
		}
		return findings[i].Kind < findings[j].Kind
	})
	fmt.Fprintf(w, "  %-56s %-26s %s\n", "SYMBOL", "KIND", "DETAIL")
	for _, f := range findings {
		fmt.Fprintf(w, "  %-56s %-26s %s\n", f.Symbol, f.Kind, f.Detail)
	}
}

func main() {
	var (
		mode          = flag.String("mode", "check", "check | write")
		baseline      = flag.String("baseline", "contracts/compat/baseline.json", "baseline contract snapshot")
		root          = flag.String("root", "contracts/protobuf", "directory containing the contract files")
		allowBreaking = flag.Bool("allow-breaking", false,
			"write even if it drops or retypes something already in the baseline; "+
				"this is how a new contract version is recorded, and it is the only way R4 can be broken on purpose")
	)
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "contractcheck: unexpected argument %q; the contract surface is -root\n",
			flag.Arg(0))
		os.Exit(2)
	}
	if *mode != "check" && *mode != "write" {
		fmt.Fprintf(os.Stderr, "contractcheck: -mode must be check or write, not %q\n", *mode)
		os.Exit(2)
	}

	s, err := detectSurface(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
		os.Exit(2)
	}
	current, warnings, err := readSurface(s, *root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
		os.Exit(2)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "contractcheck: warning: %s\n", w)
	}
	if empty(s, current) {
		fmt.Fprintf(os.Stderr, "contractcheck: %s holds nothing; the gate would pass vacuously\n", *root)
		os.Exit(2)
	}

	// The existing baseline is read even in write mode: a refresh has to be
	// checked against what it is replacing, or "regenerate" is just a way to
	// delete a contract.
	var base Baseline
	raw, err := os.ReadFile(*baseline)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &base); err != nil {
			fmt.Fprintf(os.Stderr, "contractcheck: parse baseline: %v\n", err)
			os.Exit(2)
		}
	case os.IsNotExist(err) && *mode == "check":
		fmt.Fprintf(os.Stderr, "contractcheck: no baseline at %s; run -mode write to record one\n", *baseline)
		os.Exit(2)
	}
	base.Messages, base.Enums = ensureMaps(base.Messages, base.Enums)

	findings := compareSurface(s, &base, current)

	if *mode == "write" {
		if err := writeRefusal(findings, *allowBreaking); err != nil {
			fmt.Fprintf(os.Stderr, "contractcheck: refusing to rewrite %s: %v\n", *baseline, err)
			os.Exit(1)
		}
		merge(&base, current, s)
		b, err := json.MarshalIndent(&base, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
			os.Exit(2)
		}
		if err := os.MkdirAll(filepath.Dir(*baseline), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*baseline, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s [%s: %s]\n", *baseline, s, count(s, &base))
		if len(findings) > 0 {
			fmt.Printf("note: %d breaking change(s) recorded, which means a contract version was retired\n", len(findings))
		}
		return
	}

	if empty(s, &base) {
		fmt.Fprintf(os.Stderr, "contractcheck: the baseline records no %s surface; the gate would pass "+
			"vacuously. Run -mode write -root %s\n", s, *root)
		os.Exit(2)
	}
	if len(findings) == 0 {
		fmt.Printf("contractcheck: OK — %s [%s: %s] compatible with baseline\n", *baseline, s, count(s, current))
		return
	}
	fmt.Fprintf(os.Stderr, "contractcheck: %d breaking contract change(s) in the %s surface:\n", len(findings), s)
	report(findings, os.Stderr)
	fmt.Fprintf(os.Stderr, "\nIf a change is intentionally breaking, it is a new contract version:\n"+
		"rewrite the contract files, bump the version, then\n"+
		"  contractcheck -mode write -allow-breaking -baseline %s -root %s\n", *baseline, *root)
	os.Exit(1)
}

func ensureMaps(m map[string]Message, e map[string]Enum) (map[string]Message, map[string]Enum) {
	if m == nil {
		m = map[string]Message{}
	}
	if e == nil {
		e = map[string]Enum{}
	}
	return m, e
}
