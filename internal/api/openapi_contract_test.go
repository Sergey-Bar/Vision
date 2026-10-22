package api_test

// The published OpenAPI document, checked against the server that serves it.
//
// # Why this is a gate and not a report
//
// The document is the contract. A path in it that the server does not serve is
// a promise the product does not keep, and a security scheme in it that
// nothing enforces is a protection an auditor will believe in. Both are the
// kind of defect that reads as checked, because the document is the artefact
// people read to find out what the product does.
//
// # What it caught
//
// It was written against a document that had drifted, and it failed on that
// document on the first run: three authentication paths for a login the product
// removed, a `sessionCookie` scheme for a session it does not have, and an
// `X-CSRF-Token` header marked required on ten methods that never read it.
// Every one of them was a claim in a contract the code had stopped keeping.
//
// # How it reads the document
//
// From disk, through a YAML parser, because the assertions are about
// STRUCTURE -- which methods exist, which scheme each one names, which
// parameters are required -- and a line-oriented reader answers "the document
// has a path" to a document that has the path spelled wrong.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// ---------------------------------------------------------------------------
// The document
// ---------------------------------------------------------------------------

// openAPIDoc is the published document, parsed.
type openAPIDoc struct {
	raw map[string]any
}

// loadOpenAPIDoc reads the document from the repository.
//
// From the tree, not from a copy: a gate that reads a copy of the contract
// checks the copy, and a copy is a thing that can be edited without the
// contract being edited.
func loadOpenAPIDoc(t *testing.T) openAPIDoc {
	t.Helper()
	path := filepath.Join(contractRepoRoot(t), "contracts", "openapi", "v1", "openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the OpenAPI document: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the OpenAPI document %s: %v", path, err)
	}
	if doc == nil {
		t.Fatal("the OpenAPI document parsed to nothing")
	}
	return openAPIDoc{raw: doc}
}

// contractRepoRoot walks up to the module root.
func contractRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

// declaredOperation is one method the document declares.
type declaredOperation struct {
	Method string
	Path   string
	// Secured is true when the operation's effective security requirement is
	// not empty: the operation names a scheme itself, or it inherits the
	// document's.
	Secured bool
	// Schemes are the scheme names the effective requirement names.
	Schemes []string
	// RequiredHeaders are the header parameters the operation marks required.
	RequiredHeaders []string
}

// operations returns every declared operation, in a stable order.
//
// The order is stable so that a failure names the same operation twice in two
// runs, which is what makes a failure readable at all.
func (d openAPIDoc) operations(t *testing.T) []declaredOperation {
	t.Helper()
	paths, ok := d.raw["paths"].(map[string]any)
	if !ok {
		t.Fatal("the OpenAPI document declares no paths")
	}
	documentSecurity := d.securityList(d.raw["security"])

	var out []declaredOperation
	for _, path := range sortedKeys(paths) {
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("path %q is not a path item", path)
		}
		for _, method := range sortedKeys(item) {
			if !isHTTPMethod(method) {
				continue
			}
			op, ok := item[method].(map[string]any)
			if !ok {
				t.Fatalf("operation %s %q is not an object", method, path)
			}
			security := documentSecurity
			if v, present := op["security"]; present {
				// An operation that says `security: []` opts out of the
				// document's requirement, and an operation that says nothing
				// inherits it.
				security = d.securityList(v)
			}
			out = append(out, declaredOperation{
				Method:          strings.ToUpper(method),
				Path:            path,
				Secured:         len(security) > 0,
				Schemes:         schemeNames(security),
				RequiredHeaders: d.requiredHeaders(op),
			})
		}
	}
	return out
}

// securitySchemes returns the scheme names the document declares.
func (d openAPIDoc) securitySchemes(t *testing.T) []string {
	t.Helper()
	components, ok := d.raw["components"].(map[string]any)
	if !ok {
		return nil
	}
	schemes, ok := components["securitySchemes"].(map[string]any)
	if !ok {
		return nil
	}
	return sortedKeys(schemes)
}

// securityList reads a `security:` value into its requirements.
func (d openAPIDoc) securityList(v any) []map[string][]string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string][]string
	for _, entry := range list {
		req, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		converted := map[string][]string{}
		for name, scopes := range req {
			list, _ := scopes.([]any)
			names := make([]string, 0, len(list))
			for _, s := range list {
				if s, ok := s.(string); ok {
					names = append(names, s)
				}
			}
			converted[name] = names
		}
		out = append(out, converted)
	}
	return out
}

// requiredHeaders returns the header parameters an operation marks required.
//
// A parameter may be written inline or referenced from `components/parameters`,
// and both are resolved, because a required header that is only reachable
// through a reference is still a required header.
func (d openAPIDoc) requiredHeaders(op map[string]any) []string {
	params, ok := op["parameters"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, entry := range params {
		param, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := param["$ref"].(string); ok {
			resolved := d.resolveParameter(ref)
			if resolved == nil {
				continue
			}
			param = resolved
		}
		in, _ := param["in"].(string)
		name, _ := param["name"].(string)
		required, _ := param["required"].(bool)
		if in == "header" && required && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// resolveParameter looks a `$ref` up in `components/parameters`.
func (d openAPIDoc) resolveParameter(ref string) map[string]any {
	const prefix = "#/components/parameters/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	components, ok := d.raw["components"].(map[string]any)
	if !ok {
		return nil
	}
	params, ok := components["parameters"].(map[string]any)
	if !ok {
		return nil
	}
	param, _ := params[strings.TrimPrefix(ref, prefix)].(map[string]any)
	return param
}

// schemeNames flattens a security requirement into the scheme names it names.
func schemeNames(requirements []map[string][]string) []string {
	var out []string
	for _, req := range requirements {
		for name := range req {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// isHTTPMethod reports whether a path-item key is an operation.
func isHTTPMethod(key string) bool {
	switch strings.ToLower(key) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	}
	return false
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The server
// ---------------------------------------------------------------------------

// contractServer is the product API as it answers a caller that holds nothing.
//
// The service role is the shipped role that holds integration capabilities
// (`event.ingest`, `evidence.write`) and none of the product's own, so it is
// the closest thing this tree has to a caller with no authority at all. Every
// route on this surface is behind a capability check, so a principal like this
// is refused everywhere -- and a route that is NOT refused is a route with no
// check on it, which is what the security assertion is for.
//
// No database is involved: the capability check runs before any handler reaches
// the data, so this gate needs no database and no Milestone server, and it
// cannot be skipped by an environment that has neither.
func contractServer() *api.Server {
	s := &api.Server{
		Tenant:        "contract-tenant",
		PrincipalRole: identity.RoleService,
		PrincipalSite: "contract-site",
	}
	s.Routes = func(mux *http.ServeMux) { s.RegisterRoutes(api.Deps{})(mux) }
	return s
}

// pathParameterRe matches a path template's parameter.
var pathParameterRe = regexp.MustCompile(`\{[^/}]+\}`)

// requestPath turns a declared path into one that can be requested.
//
// The braces are the document's parameter names and the mux's are its own --
// the document says `{incidentId}` and the server says `{id}` -- and a request
// cannot carry braces, so every parameter becomes one segment.
func requestPath(path string) string {
	return pathParameterRe.ReplaceAllString(path, "contract-parameter")
}

// contractRequest issues a request against the contract server.
//
// The header argument names a header to carry; an empty name carries none.
func contractRequest(h http.Handler, method, path, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if header != "" {
		req.Header.Set(header, "contract-value")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// contractAnswer is what a response says, for two responses to be compared.
type contractAnswer struct {
	Status int
	Code   string
}

func answerOf(rec *httptest.ResponseRecorder) contractAnswer {
	out := contractAnswer{Status: rec.Code}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
		if code, ok := body["code"].(string); ok {
			out.Code = code
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// TestEveryDeclaredPathIsRegisteredOnTheMux is the first half of the gate.
//
// A path in the contract that the server does not serve is a promise the
// product does not keep, and it is the failure mode a document drifts into
// quietly: the route is removed from the code, the document keeps it, and
// every client written against the document waits for an answer that will
// never come.
func TestEveryDeclaredPathIsRegisteredOnTheMux(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	operations := doc.operations(t)
	if len(operations) == 0 {
		t.Fatal("the OpenAPI document declares no operations; the gate would pass vacuously")
	}
	h := contractServer().Handler()
	for _, op := range operations {
		rec := contractRequest(h, op.Method, requestPath(op.Path), "")
		switch rec.Code {
		case http.StatusNotFound:
			t.Errorf("%s %s is declared in the OpenAPI document and the server does not "+
				"serve it. A contract that promises an endpoint the product does not "+
				"have is a promise nobody can rely on.", op.Method, op.Path)
		case http.StatusMethodNotAllowed:
			t.Errorf("%s %s is declared in the OpenAPI document and the server serves "+
				"that path under another method. The contract and the mux disagree "+
				"about the method, and a client that trusts the contract will get a "+
				"405 it was not promised.", op.Method, op.Path)
		}
	}
}

// TestEveryDeclaredSecuritySchemeIsEnforcedOnTheMethodsThatNameIt is the
// second half.
//
// A method that names a security scheme must not answer a caller that holds
// nothing: the scheme is a published claim that the method is not open, and a
// method that answers anyway is a claim nobody enforces.
//
// The converse is checked too. A method that declares NO requirement is a
// method the document publishes as open, and a server that refuses it anyway
// is a document that lies in the other direction -- a client that reads the
// document will treat the refusal as a fault rather than as the rule.
func TestEveryDeclaredSecuritySchemeIsEnforcedOnTheMethodsThatNameIt(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	operations := doc.operations(t)
	if len(operations) == 0 {
		t.Fatal("the OpenAPI document declares no operations; the gate would pass vacuously")
	}
	h := contractServer().Handler()
	for _, op := range operations {
		rec := contractRequest(h, op.Method, requestPath(op.Path), "")
		answer := answerOf(rec)
		if op.Secured {
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s names the security scheme %v and the server answered %d "+
					"to a caller that holds nothing. The scheme is published and "+
					"nothing enforces it.", op.Method, op.Path, op.Schemes, rec.Code)
			}
			continue
		}
		if answer.Code == "capability_denied" {
			t.Errorf("%s %s declares no security requirement and the server refused it "+
				"with capability_denied. The document publishes the method as open "+
				"and the server gates it, so a client that trusts the document will "+
				"read the refusal as a fault.", op.Method, op.Path)
		}
	}
}

// TestTheDocumentAndTheServerNameTheSameSecuritySchemes is the third half.
//
// The comparison runs in both directions. A scheme the document declares and
// the server does not enforce is a protection an auditor will believe in. A
// scheme the server enforces and the document does not declare is a protection
// the document hides, which is the same defect with the paperwork filed.
//
// The server's own list is the authority here, so the list lives next to the
// code that does the enforcing rather than in this test.
func TestTheDocumentAndTheServerNameTheSameSecuritySchemes(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	declared := doc.securitySchemes(t)
	if len(declared) == 0 {
		t.Fatal("the OpenAPI document declares no security scheme; the gate would pass " +
			"vacuously on a document that publishes no protection at all")
	}
	enforced := api.EnforcedSecuritySchemes
	inDocument := map[string]bool{}
	for _, name := range declared {
		inDocument[name] = true
	}
	inServer := map[string]bool{}
	for _, name := range enforced {
		inServer[name] = true
	}
	for _, name := range declared {
		if !inServer[name] {
			t.Errorf("the OpenAPI document declares the security scheme %q and the server "+
				"does not enforce it. A published scheme that nothing provides is a "+
				"protection an auditor will believe in.", name)
		}
	}
	for _, name := range enforced {
		if !inDocument[name] {
			t.Errorf("the server enforces the security scheme %q and the OpenAPI document "+
				"does not declare it. A protection the document hides is a protection "+
				"nobody can check.", name)
		}
	}
	for _, op := range doc.operations(t) {
		for _, name := range op.Schemes {
			if !inDocument[name] {
				t.Errorf("%s %s names the security scheme %q, which the document does "+
					"not declare", op.Method, op.Path, name)
			}
		}
	}
}

// TestEveryRequiredHeaderParameterIsOneTheServerRequires is the fourth half.
//
// A parameter marked required is a promise the client makes to the server: it
// sends the header every time. If the server never reads it, the client is
// sending a secret to nobody, and the document is asking for a credential the
// product does not have.
//
// The check is behavioural -- the same request with and without the header must
// not be answered the same way -- because a header the server ignores is
// invisible to every other kind of check.
func TestEveryRequiredHeaderParameterIsOneTheServerRequires(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	operations := doc.operations(t)
	h := contractServer().Handler()
	for _, op := range operations {
		for _, header := range op.RequiredHeaders {
			with := answerOf(contractRequest(h, op.Method, requestPath(op.Path), header))
			without := answerOf(contractRequest(h, op.Method, requestPath(op.Path), ""))
			if with == without {
				t.Errorf("%s %s declares %s as a required header and the server answers "+
					"the same either way. The document asks a client for a token nobody "+
					"reads, and a token in a request body is a token in a log.",
					op.Method, op.Path, header)
			}
		}
	}
}
