package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRundocFixture puts a file at a repo-relative path under a temp root.
func writeRundocFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestRundocServesTheDocumentsItNames: every name in the allowlist serves the
// bytes of the file it names, with the content type its key declares.
//
// The decoy is the point. A handler that spells the served path from the
// request - filepath.Join(root, name) instead of root plus the key's own rel -
// is one line away, and with that line the decoy is what a reader would be
// shown: a file that exists exactly where a request-spelled path looks. The
// assertion is that the allowlist's file wins.
func TestRundocServesTheDocumentsItNames(t *testing.T) {
	root := t.TempDir()
	for name, doc := range rundocDocs {
		body := "document " + name
		writeRundocFixture(t, root, doc.rel, body)
		writeRundocFixture(t, root, name, "the file a request-spelled path would have served")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/rundoc/"+name, nil)
		handleRundoc(root)(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 (body: %s)", name, rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got != body {
			t.Fatalf("%s: served %q, want %q - the document named by the key, not the file at the request's spelling", name, got, body)
		}
		if got := rec.Header().Get("Content-Type"); got != doc.contentType {
			t.Fatalf("%s: content type %q, want %q", name, got, doc.contentType)
		}
	}
}

// TestRundocCannotBePointedAtAnotherFile is the control the allowlist exists to
// be: the request name never reaches the filesystem. A secret is placed next to
// the served documents and every spelling of "go up one directory" is asked for
// it; all of them are refused, and the secret is not in any response body.
func TestRundocCannotBePointedAtAnotherFile(t *testing.T) {
	root := t.TempDir()
	const secret = "the file rundoc must never serve"
	writeRundocFixture(t, root, "dist/demo/report.html", "the report")
	writeRundocFixture(t, root, "dist/secret.txt", secret)

	for _, attempt := range []string{
		"../secret.txt",
		"..%2fsecret.txt",
		"..\\secret.txt",
		"dist/secret.txt",
		"/etc/passwd",
		"report.html\x00.txt",
		"..",
		".",
		"",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/rundoc/placeholder", nil)
		// The handler reads URL.Path directly and the mux in front of it cleans
		// traversal; this stands in for the cleaned form so the handler itself
		// is what is under test.
		req.URL.Path = "/rundoc/" + attempt
		handleRundoc(root)(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%q: status %d, want 404", attempt, rec.Code)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("%q: the refusal body leaked the file next to the documents", attempt)
		}
	}
}

// TestRundocSaysWhoWritesADocumentThatIsNotThereYet: a name the machine has no
// file for answers with the command that produces it, because a bare 404 sends
// its reader looking for a bug instead of for the demo they have not run.
func TestRundocSaysWhoWritesADocumentThatIsNotThereYet(t *testing.T) {
	root := t.TempDir() // nothing written
	name, doc := "live-report", rundocDocs["live-report"]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rundoc/"+name, nil)
	handleRundoc(root)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), doc.writtenBy) {
		t.Fatalf("the refusal does not name the command that writes %s: %q", doc.rel, rec.Body.String())
	}
}

// TestRundocIsReadOnly: the surface exists to show files, not to accept any.
func TestRundocIsReadOnly(t *testing.T) {
	root := t.TempDir()
	writeRundocFixture(t, root, rundocDocs["poc-report"].rel, "the report")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/rundoc/poc-report", nil)
		handleRundoc(root)(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: status %d, want 405", method, rec.Code)
		}
	}
}
