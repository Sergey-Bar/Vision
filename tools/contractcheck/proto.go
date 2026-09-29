package main

// Protobuf surface. A .proto file is the wire: an adapter, the AI runtime and a
// months-old stored row all depend on these field numbers. Removing a tag,
// reusing one for a different field, or retyping a field silently corrupts
// history, because a decision recorded last quarter must still replay.

import (
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

var (
	pkgRe   = regexp.MustCompile(`(?m)^package\s+([A-Za-z0-9_.]+)\s*;`)
	msgRe   = regexp.MustCompile(`(?m)^message\s+([A-Za-z0-9_]+)\s*\{`)
	enumRe  = regexp.MustCompile(`(?m)^enum\s+([A-Za-z0-9_]+)\s*\{`)
	fieldRe = regexp.MustCompile(`^\s*(repeated\s+|optional\s+)?((?:map\s*<[^>]+>)|(?:[A-Za-z0-9_.]+))\s+([A-Za-z0-9_]+)\s*=\s*(\d+)\s*(\[[^\]]*\])?\s*;`)
	// A map field is a synthesised repeated message with a key and a value, so
	// it needs its own token: `map<string, string>` has a `<` in the middle of
	// what would otherwise be the type, and `[A-Za-z0-9_.]+` stops there, which
	// made the whole field invisible rather than merely mistyped.
	enumValRe = regexp.MustCompile(`^\s*([A-Z0-9_]+)\s*=\s*(-?\d+)\s*(\[[^\]]*\])?\s*;`)
)

func parseProtoFile(path, rel string, c *Baseline) error {
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
					Type:     normalizeProtoType(m[2]),
					Repeated: strings.TrimSpace(m[1]) == "repeated" || strings.HasPrefix(m[2], "map<"),
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

// normalizeProtoType collapses the whitespace inside a `map<k, v>` token so that
// `map<string, string>` and `map<string,string>` are one type, not two, and a
// reformat of one of them is not mistaken for a retype of the other.
func normalizeProtoType(t string) string {
	if !strings.HasPrefix(t, "map<") {
		return t
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(t, "map<"), ">")
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return t
	}
	return "map<" + strings.TrimSpace(parts[0]) + "," + strings.TrimSpace(parts[1]) + ">"
}

func parseProtoTree(root string) (*Baseline, error) {
	c := newBaseline()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		return parseProtoFile(path, filepath.ToSlash(rel), c)
	})
	return c, err
}

func normalizeProto(c *Baseline) {
	for k, m := range c.Messages {
		sort.Slice(m.Fields, func(i, j int) bool { return m.Fields[i].Number < m.Fields[j].Number })
		c.Messages[k] = m
	}
	for k, e := range c.Enums {
		sort.Slice(e.Values, func(i, j int) bool { return e.Values[i].Number < e.Values[j].Number })
		c.Enums[k] = e
	}
}

func compareProto(base, cur *Baseline) []finding {
	var out []finding

	for name, oldMsg := range base.Messages {
		newMsg, ok := cur.Messages[name]
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

	for name, oldEnum := range base.Enums {
		newEnum, ok := cur.Enums[name]
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
