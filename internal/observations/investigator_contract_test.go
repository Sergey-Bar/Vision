package observations_test

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// The investigator's contract is declared in three places: this registry, the
// POC's request schema (derived from this registry, so it cannot drift) and the
// plane's own INVESTIGATOR_PROFILE / INVESTIGATOR_SCHEMA in
// deploy/ai-runtime/runtime.py. A Go-side consolidation closed one of the three
// gaps; this closes the last one by asserting the plane agrees, which is the
// difference between a contract and three copies of one that can diverge.
//
// It reads the plane rather than importing it because the plane is Python and
// this is Go. The reading is a real brace-match, because what is being checked
// is the shape of a dict literal, and the previous version's regex encoded that
// shape a second time: it required "properties" to precede "required" and each
// property to be a flat `"name": {`, so a legitimate edit — moving the keys, or
// nesting a sub-schema under citations — failed the test without any contract
// change. A parser does not care about the order, and reads the nesting.

const runtimePath = "deploy/ai-runtime/runtime.py"

func TestTheInvestigatorContractAgreesWithThePlane(t *testing.T) {
	profile, ok := observations.M1Profiles[observations.ProfileInvestigate]
	if !ok {
		t.Fatalf("the registry has no %s profile; the POC cannot send it", observations.ProfileInvestigate)
	}

	raw, err := os.ReadFile(findRuntime())
	if err != nil {
		t.Fatalf("read %s: %v", runtimePath, err)
	}
	src := string(raw)

	// The plane's profile id.
	idMatch := pythonStringConst(src, "INVESTIGATOR_PROFILE")
	if idMatch == "" {
		t.Fatal("runtime.py does not declare INVESTIGATOR_PROFILE")
	}
	if idMatch != observations.ProfileInvestigate {
		t.Errorf("the plane's profile id is %q and the registry's is %q", idMatch, observations.ProfileInvestigate)
	}

	// The plane's schema, parsed as the dict literal it is.
	schema, ok := pythonDictLiteral(src, "INVESTIGATOR_SCHEMA")
	if !ok {
		t.Fatal("runtime.py does not declare INVESTIGATOR_SCHEMA as a dict literal")
	}
	props, ok := schema["properties"]
	if !ok {
		t.Fatal("the plane's INVESTIGATOR_SCHEMA declares no properties")
	}
	propsDict, ok := props.(map[string]any)
	if !ok {
		t.Fatalf("the plane's properties is %T, want a dict", props)
	}

	var planeFields []string
	for field := range propsDict {
		planeFields = append(planeFields, field)
	}
	sort.Strings(planeFields)

	var registryFields []string
	for field := range profile.Fields {
		registryFields = append(registryFields, field)
	}
	sort.Strings(registryFields)
	if strings.Join(planeFields, ",") != strings.Join(registryFields, ",") {
		t.Errorf("the plane's field set is [%s] and the registry's is [%s]; a field added on one "+
			"side is a plane-side refusal on the other",
			strings.Join(planeFields, ", "), strings.Join(registryFields, ", "))
	}

	// The required subset, which the plane's validator reads from the request.
	req, ok := schema["required"]
	if !ok {
		t.Fatal("the plane's INVESTIGATOR_SCHEMA declares no required list")
	}
	reqList, ok := req.([]any)
	if !ok {
		t.Fatalf("the plane's required is %T, want a list", req)
	}
	var planeRequired []string
	for _, f := range reqList {
		s, ok := f.(string)
		if !ok {
			t.Errorf("the plane's required contains %T, want strings", f)
			continue
		}
		planeRequired = append(planeRequired, s)
	}
	sort.Strings(planeRequired)
	registryRequired := append([]string(nil), profile.Required...)
	sort.Strings(registryRequired)
	if strings.Join(planeRequired, ",") != strings.Join(registryRequired, ",") {
		t.Errorf("the plane requires [%s] and the registry requires [%s]",
			strings.Join(planeRequired, ", "), strings.Join(registryRequired, ", "))
	}
}

// pythonStringConst reads `NAME = "value"` from Python source.
func pythonStringConst(src, name string) string {
	marker := name + ` = "`
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	i += len(marker)
	j := strings.Index(src[i:], `"`)
	if j < 0 {
		return ""
	}
	return src[i : i+j]
}

// pythonDictLiteral parses `NAME = { ... }` from Python source into a
// map[string]any, brace-matched and string-aware, with enough of the Python
// literal grammar for a configured schema: dicts, lists, strings, numbers,
// booleans and None.
func pythonDictLiteral(src, name string) (map[string]any, bool) {
	// The declaration may carry a type annotation ("X: dict = {") or not, so
	// the marker is the name and the first '{' after it.
	i := strings.Index(src, name)
	if i < 0 {
		return nil, false
	}
	rel := strings.Index(src[i:], "{")
	if rel < 0 {
		return nil, false
	}
	i += rel
	end := matchBrace(src, i)
	if end < 0 {
		return nil, false
	}
	v, ok := parsePythonLiteral(src[i : end+1])
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// matchBrace returns the index of the '}' that closes the '{' at open,
// ignoring braces inside string literals.
func matchBrace(src string, open int) int {
	depth := 0
	inStr := byte(0)
	for i := open; i < len(src); i++ {
		c := src[i]
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parsePythonLiteral parses the subset of a Python literal this schema uses:
// dict, list, string, int, float, bool and None. It returns the parsed value
// and whether the whole input was consumed.
func parsePythonLiteral(src string) (any, bool) {
	i := skipSpace(src, 0)
	if i >= len(src) {
		return nil, false
	}
	v, ni, ok := parseValue(src, i)
	if !ok {
		return nil, false
	}
	ni = skipSpace(src, ni)
	if ni != len(src) {
		return nil, false
	}
	return v, true
}

func skipSpace(src string, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
		i++
	}
	return i
}

func parseValue(src string, i int) (any, int, bool) {
	i = skipSpace(src, i)
	if i >= len(src) {
		return nil, i, false
	}
	switch src[i] {
	case '{':
		return parseDict(src, i)
	case '[':
		return parseList(src, i)
	case '"', '\'':
		return parseStr(src, i)
	case 't', 'f':
		return parseBool(src, i)
	case 'N':
		return parseNone(src, i)
	default:
		return parseNumber(src, i)
	}
}

func parseDict(src string, i int) (any, int, bool) {
	m := map[string]any{}
	i = skipSpace(src, i+1)
	if i < len(src) && src[i] == '}' {
		return m, i + 1, true
	}
	for {
		key, ni, ok := parseStr(src, skipSpace(src, i))
		if !ok {
			return nil, i, false
		}
		ni = skipSpace(src, ni)
		if ni >= len(src) || src[ni] != ':' {
			return nil, i, false
		}
		val, nj, ok := parseValue(src, ni+1)
		if !ok {
			return nil, i, false
		}
		m[key.(string)] = val
		nj = skipSpace(src, nj)
		if nj >= len(src) {
			return nil, i, false
		}
		switch src[nj] {
		case ',':
			// Python allows a trailing comma, so a ',' followed by the close
			// is legal: skip the comma and check whether the dict ended.
			nxt := skipSpace(src, nj+1)
			if nxt < len(src) && src[nxt] == '}' {
				return m, nxt + 1, true
			}
			i = nxt
		case '}':
			return m, nj + 1, true
		default:
			return nil, i, false
		}
	}
}

func parseList(src string, i int) (any, int, bool) {
	var l []any
	i = skipSpace(src, i+1)
	if i < len(src) && src[i] == ']' {
		return l, i + 1, true
	}
	for {
		val, ni, ok := parseValue(src, i)
		if !ok {
			return nil, i, false
		}
		l = append(l, val)
		ni = skipSpace(src, ni)
		if ni >= len(src) {
			return nil, i, false
		}
		switch src[ni] {
		case ',':
			nxt := skipSpace(src, ni+1)
			if nxt < len(src) && src[nxt] == ']' {
				return l, nxt + 1, true
			}
			i = nxt
		case ']':
			return l, ni + 1, true
		default:
			return nil, i, false
		}
	}
}

func parseStr(src string, i int) (any, int, bool) {
	q := src[i]
	var b strings.Builder
	i++
	for i < len(src) {
		c := src[i]
		if c == '\\' && i+1 < len(src) {
			b.WriteByte(src[i+1])
			i += 2
			continue
		}
		if c == q {
			return b.String(), i + 1, true
		}
		b.WriteByte(c)
		i++
	}
	return nil, i, false
}

func parseBool(src string, i int) (any, int, bool) {
	if strings.HasPrefix(src[i:], "True") {
		return true, i + 4, true
	}
	if strings.HasPrefix(src[i:], "False") {
		return false, i + 5, true
	}
	return nil, i, false
}

func parseNone(src string, i int) (any, int, bool) {
	if strings.HasPrefix(src[i:], "None") {
		return nil, i + 4, true
	}
	return nil, i, false
}

func parseNumber(src string, i int) (any, int, bool) {
	j := i
	for j < len(src) && (src[j] == '-' || src[j] == '+' || src[j] == '.' || src[j] == 'e' || src[j] == 'E' ||
		(src[j] >= '0' && src[j] <= '9')) {
		j++
	}
	if j == i {
		return nil, i, false
	}
	f, err := strconv.ParseFloat(src[i:j], 64)
	if err != nil {
		return nil, i, false
	}
	return f, j, true
}

// findRuntime resolves the plane's source from any package directory the tool
// is run in.
func findRuntime() string {
	for _, c := range []string{"deploy/ai-runtime/runtime.py", "../../deploy/ai-runtime/runtime.py"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return runtimePath
}
