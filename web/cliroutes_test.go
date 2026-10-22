package main

// The routes that run the CLI are behind the `-dev` flag.
//
// A console on plain HTTP with no authentication, serving routes that run a
// binary with a caller-supplied argument, is a surface anybody who can reach
// the port can use.
//
// The arguments are bounded and there is no shell, so this was never an
// injection. It was an EXPOSURE: a default server that runs a CLI on request.
// Behind `-dev` it is a deliberate choice by whoever starts the server, and
// the demo and the rehearsal both make it, because the judge's walk visits the
// operations page.
//
// This test asks the question directly, because "the route is behind the flag"
// is a claim about a registration and a registration is code.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cliRoutes are the routes that run the CLI.
var cliRoutes = []string{
	"/api/poc",
	"/api/acceptance",
	"/api/rules/lint",
	"/api/rules/simulate",
	"/api/efficiency",
	"/api/incident",
	"/api/explain",
}

// TestTheCLIRoutesAreBehindTheDevFlag is the exposure.
//
// A default console answers 404 for every one of them, and a console started
// with `-dev` answers something else.
func TestTheCLIRoutesAreBehindTheDevFlag(t *testing.T) {
	off := http.NewServeMux()
	registerAPIRoutes(off, "securityctl", ".", false)
	for _, path := range cliRoutes {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		off.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d on a console without -dev, want 404. A console on plain "+
				"HTTP with no authentication, running a binary with a caller-supplied argument, "+
				"is a surface anybody who can reach the port can use", path, rec.Code)
		}
	}
}

// TestTheCLIRoutesAreThereWithTheDevFlag is the other half.
//
// A gate that removed the routes and called it a day would pass the first test
// and break the judge's walk, and the judge's walk is the demo.
func TestTheCLIRoutesAreThereWithTheDevFlag(t *testing.T) {
	on := http.NewServeMux()
	registerAPIRoutes(on, "securityctl", ".", true)
	for _, path := range cliRoutes {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		on.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s answered 404 with -dev. The judge's walk visits the operations page, "+
				"and the operations page calls these three", path)
		}
	}
}
