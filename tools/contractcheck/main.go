// Command contractcheck enforces protobuf contract compatibility.
//
// The contracts are the product's public surface: adapters, the AI runtime and
// stored rows all depend on them. A change that breaks an existing field number,
// removes a message, reuses a tag for something else, or drops an enum value
// silently corrupts historical data, because a months-old decision must still
// replay. This command turns that rule into a build failure.
//
// Usage:
//
//	contractcheck -check -baseline contracts/compat/baseline.json contracts/protobuf
//	contractcheck -write -baseline contracts/compat/baseline.json contracts/protobuf
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Field struct {
	Number   int64  `json:"number"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Repeated bool   `json:"repeated,omitempty"`
	OneOf    string `json:"oneof,omitempty"`
}

type Message struct {
	Fields []Field `json:"fields"`
}

type EnumValue struct {
	Number int32  `json:"number"`
	Name   string `json:"name"`
}

type Enum struct {
	Values []EnumValue `json:"values"`
}

type Contract struct {
	// Key is "package.Message" or "package.Enum".
	Messages map[string]Message `json:"messages"`
	Enums    map[string]Enum    `json:"enums"`
}

func newContract() *Contract {
	return &Contract{Messages: map[string]Message{}, Enums: map[string]Enum{}}
}

var (
	pkgRe     = regexp.MustCompile(`(?m)^package\s+([A-Za-z0-9_.]+)\s*;`)
	msgRe     = regexp.MustCompile(`(?m)^message\s+([A-Za-z0-9_]+)\s*\{`)
	enumRe    = regexp.MustCompile(`(?m)^enum\s+([A-Za-z0-9_]+)\s*\{`)
	fieldRe   = regexp.MustCompile(`^\s*(repeated\s+|optional\s+)?([A-Za-z0-9_.]+)\s+([A-Za-z0-9_]+)\s*=\s*(\d+)\s*(\[[^\]]*\])?\s*;`)
	enumValRe = regexp.MustCompile(`^\s*([A-Z0-9_]+)\s*=\s*(-?\d+)\s*(\[[^\]]*\])?\s*;`)
)

func parseFile(path, rel string, c *Contract) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")

	pkg := ""
	for _, m := range pkgRe.FindAllStringSubmatch(string(src), 1) {
		pkg = m[1]
	}
	if pkg == "" {
		return fmt.Errorf("%s: no package declaration", rel)
	}

	var (
		curKind string
		curName string
		msg     Message
		enum    Enum
		oneof   string
	)
	flush := func() {
		if curName == "" {
			return
		}
		//nolint:staticcheck // QF1003: this is already a tagged switch on a
		// string; the linter is reading a type it inferred from
		// zero values.
		switch curKind {
		case "message":
			c.Messages[pkg+"."+curName] = msg
		case "enum":
			c.Enums[pkg+"."+curName] = enum
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if m := msgRe.FindStringSubmatch(line); m != nil {
			flush()
			curKind, curName, msg, enum, oneof = "message", m[1], Message{}, Enum{}, ""
			continue
		}
		if m := enumRe.FindStringSubmatch(line); m != nil {
			flush()
			curKind, curName, msg, enum, oneof = "enum", m[1], Message{}, Enum{}, ""
			continue
		}
		if strings.HasPrefix(line, "oneof ") {
			oneof = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "oneof ")), "{")
			continue
		}
		if line == "}" {
			if curKind == "message" && oneof != "" {
				oneof = ""
				continue
			}
			flush()
			curName = ""
			continue
		}
		if curName == "" {
			continue
		}
		switch curKind {
		case "message":
			if m := fieldRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.ParseInt(m[4], 10, 64)
				msg.Fields = append(msg.Fields, Field{
					Number:   n,
					Name:     m[3],
					Type:     m[2],
					Repeated: strings.TrimSpace(m[1]) == "repeated",
					OneOf:    oneof,
				})
			}
		case "enum":
			if m := enumValRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.ParseInt(m[2], 10, 32)
				enum.Values = append(enum.Values, EnumValue{Number: int32(n), Name: m[1]})
			}
		}
	}
	flush()
	return nil
}

func parseTree(root string) (*Contract, error) {
	c := newContract()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		return parseFile(path, rel, c)
	})
	return c, err
}

type finding struct {
	Symbol string
	Kind   string
	Detail string
}

func compare(baseline, current *Contract) []finding {
	var out []finding

	for name, oldMsg := range baseline.Messages {
		newMsg, ok := current.Messages[name]
		if !ok {
			out = append(out, finding{name, "message removed", "historical rows may no longer decode"})
			continue
		}
		byNum := map[int64]Field{}
		byName := map[string]Field{}
		for _, f := range newMsg.Fields {
			byNum[f.Number] = f
			byName[f.Name] = f
		}
		for _, of := range oldMsg.Fields {
			nf, ok := byNum[of.Number]
			if !ok {
				if _, renamed := byName[of.Name]; renamed {
					out = append(out, finding{fmt.Sprintf("%s.%s", name, of.Name), "field number reused",
						fmt.Sprintf("tag %d now belongs to another field; stored rows will mis-decode", of.Number)})
				} else {
					out = append(out, finding{fmt.Sprintf("%s.%s", name, of.Name), "field removed", "historical rows lose data on re-read"})
				}
				continue
			}
			if nf.Type != of.Type {
				out = append(out, finding{fmt.Sprintf("%s.%s", name, of.Name), "field type changed",
					fmt.Sprintf("tag %d: %s -> %s", of.Number, of.Type, nf.Type)})
			}
			if nf.Repeated != of.Repeated {
				out = append(out, finding{fmt.Sprintf("%s.%s", name, of.Name), "field cardinality changed",
					fmt.Sprintf("tag %d repeated %v -> %v", of.Number, of.Repeated, nf.Repeated)})
			}
			if nf.OneOf != of.OneOf {
				out = append(out, finding{fmt.Sprintf("%s.%s", name, of.Name), "field moved out of oneof",
					fmt.Sprintf("tag %d: oneof %q -> %q", of.Number, of.OneOf, nf.OneOf)})
			}
		}
	}

	for name, oldEnum := range baseline.Enums {
		newEnum, ok := current.Enums[name]
		if !ok {
			out = append(out, finding{name, "enum removed", "stored values lose their name"})
			continue
		}
		byNum := map[int32]EnumValue{}
		for _, v := range newEnum.Values {
			byNum[v.Number] = v
		}
		for _, ov := range oldEnum.Values {
			nv, ok := byNum[ov.Number]
			if !ok {
				out = append(out, finding{fmt.Sprintf("%s.%s", name, ov.Name), "enum value removed",
					fmt.Sprintf("value %d no longer decodes", ov.Number)})
				continue
			}
			if nv.Name != ov.Name {
				out = append(out, finding{fmt.Sprintf("%s.%s", name, ov.Name), "enum value renamed",
					fmt.Sprintf("value %d: %s -> %s", ov.Number, ov.Name, nv.Name)})
			}
		}
	}
	return out
}

func normalize(c *Contract) *Contract {
	for k, m := range c.Messages {
		sort.Slice(m.Fields, func(i, j int) bool { return m.Fields[i].Number < m.Fields[j].Number })
		c.Messages[k] = m
	}
	for k, e := range c.Enums {
		sort.Slice(e.Values, func(i, j int) bool { return e.Values[i].Number < e.Values[j].Number })
		c.Enums[k] = e
	}
	return c
}

func main() {
	var (
		mode     = flag.String("mode", "check", "check | write")
		baseline = flag.String("baseline", "contracts/compat/baseline.json", "baseline contract snapshot")
		root     = flag.String("root", "contracts/protobuf", "directory containing .proto files")
	)
	flag.Parse()

	current, err := parseTree(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contractcheck: %v\n", err)
		os.Exit(2)
	}
	normalize(current)

	if *mode == "write" {
		b, err := json.MarshalIndent(current, "", "  ")
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
		fmt.Printf("wrote %s (%d messages, %d enums)\n", *baseline, len(current.Messages), len(current.Enums))
		return
	}

	raw, err := os.ReadFile(*baseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contractcheck: read baseline: %v\n", err)
		os.Exit(2)
	}
	var base Contract
	if err := json.Unmarshal(raw, &base); err != nil {
		fmt.Fprintf(os.Stderr, "contractcheck: parse baseline: %v\n", err)
		os.Exit(2)
	}

	findings := compare(&base, current)
	if len(findings) == 0 {
		fmt.Printf("contractcheck: OK — %d messages, %d enums compatible with baseline\n", len(current.Messages), len(current.Enums))
		return
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Symbol < findings[j].Symbol })
	fmt.Fprintf(os.Stderr, "contractcheck: %d breaking contract change(s):\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "  %-45s %-26s %s\n", f.Symbol, f.Kind, f.Detail)
	}
	fmt.Fprintf(os.Stderr, "\nIf a change is intentionally breaking, it is a new contract version:\n"+
		"rewrite the .proto files, bump the package or the schema_version field, then\n"+
		"  contractcheck -mode write -baseline %s -root %s\n", *baseline, *root)
	os.Exit(1)
}
