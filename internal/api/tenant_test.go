package api_test

// The tenant is configuration. A request does not carry it, and a request
// that says it does is not believed.
//
// The product's tenant reaches the database through the pool's tenant
// binding, which is set from the principal, which is set from configuration.
// There is no path from a request header to a query. That is the invariant, and
// this file is what holds it.
//
// It exists because the server used to read `X-Tenant-Id` out of the request
// when the install had no configured tenant, and the comment above that code
// said a header was never promoted to the tenant. Which is exactly what the
// code did, on exactly the install where the promotion was least wanted.
//
// A header is attacker-controlled. This product has no session for a header to
// be matched against, so a header is an instruction rather than an
// authorisation, and the tenant is the one field a cross-tenant read would
// turn into a disclosure of everything.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheAccessLogNamesTheConfiguredTenantAndNotAHeader is the behavioural half.
//
// A forged header on the request must not change the tenant the log names.
// The log is where an operator looks to answer "whose request was this", so a
// log line whose tenant a caller chose is a log line that can be made to say
// anything.
func TestTheAccessLogNamesTheConfiguredTenantAndNotAHeader(t *testing.T) {
	s, tenant, _ := server(t)

	var buf bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&buf, nil))

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("X-Tenant-Id", "someone-else")
	req.Header.Set("X-Tenant-ID", "someone-else")
	req.Header.Set("X-Site-Id", "site-somebody-elses")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	if line == "" {
		t.Fatal("the request produced no log line")
	}
	if !strings.Contains(line, "tenant_id="+tenant) {
		t.Errorf("the log line does not name the configured tenant %q:\n%s", tenant, line)
	}
	if strings.Contains(line, "someone-else") {
		t.Errorf("the log line carries a value the caller sent:\n%s", line)
	}
	if strings.Contains(line, "site-somebody-elses") {
		t.Errorf("the log line carries the site header a caller sent:\n%s", line)
	}
}

// TestTheProductNeverReadsATenantFromARequest is the source-level half.
//
// The scan is over the SOURCE rather than over the behaviour, because the
// behaviour is "read somebody else's rows", and a test that asserts on that
// needs a second tenant in a database. The source scan catches the code path
// that would do it, including a path no test happens to exercise.
//
// A comment may NAME the header, in order to say that it is not read. A test
// file may name it for the same reason. Neither is a read.
func TestTheProductNeverReadsATenantFromARequest(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the api package: %v", err)
	}

	forbidden := []string{"X-Tenant-Id", "X-Tenant-ID", "x-tenant-id", "X-Site-Id", "X-Site-ID"}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, f := range forbidden {
			for _, line := range strings.Split(string(raw), "\n") {
				if !strings.Contains(line, f) {
					continue
				}
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
					continue
				}
				t.Errorf("%s reads %q. The tenant and the site are CONFIGURED, and a header "+
					"from a caller with no session to match it against is an instruction rather "+
					"than an authorisation", name, f)
			}
		}
	}
}
