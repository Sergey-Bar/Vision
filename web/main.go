// Package main provides the web UI server for the Vision POC.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// registerAPIRoutes registers the console's API routes.
//
// # The routes that run the CLI are behind `-dev`
//
// The development surfaces run a CLI: one of them runs a 200-iteration soak,
// and the three OPERATOR surfaces run the CLI with a request-supplied incident
// id.
//
// The operator three used to be registered unconditionally, on the grounds that
// they are "the product's claims, reachable by clicking rather than by knowing
// which command to run". That is true, and it is not the whole question: a
// console on plain HTTP with no authentication, serving routes that run a
// binary with a caller-supplied argument, is a surface anybody who can reach
// the port can use.
//
// The arguments are bounded and there is no shell, so this was never an
// injection. It was an EXPOSURE: a default server that runs a CLI on request.
// Behind `-dev` it is a deliberate choice by whoever starts the server, and the
// demo and the rehearsal both make it, because the judge's walk visits the
// operations page.
//
// It is a function and not inline in `main` so that a test can ask the
// question -- "is the route there when the flag is off?" -- without starting a
// server.
func registerAPIRoutes(mux *http.ServeMux, securityctlPath, repoRoot string, dev bool) {
	if !dev {
		log.Print("development surfaces are off; start with -dev to serve /dev, the CLI endpoints " +
			"and the three operator surfaces that run the CLI")
		return
	}
	mux.HandleFunc("/api/poc", handlePOC(securityctlPath, repoRoot))
	mux.HandleFunc("/api/acceptance", handleAcceptance(securityctlPath, repoRoot))
	mux.HandleFunc("/api/rules/lint", handleRulesLint(securityctlPath, repoRoot))
	mux.HandleFunc("/api/rules/simulate", handleRulesSimulate(securityctlPath, repoRoot))
	mux.HandleFunc("/api/efficiency", handleEfficiency(securityctlPath, repoRoot))
	mux.HandleFunc("/api/incident", handleIncident(securityctlPath, repoRoot))
	mux.HandleFunc("/api/explain", handleExplain(securityctlPath, repoRoot))
}

func main() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Fatalf("PANIC in main: %v", rec)
		}
	}()

	// The development-only surfaces. Off by default, because they run the CLI
	// and, in one case, a 200-iteration soak, from a request a browser made.
	dev := flag.Bool("dev", false,
		"serve the development-only surfaces: the capture-path page and the endpoints that run the CLI")
	// The port. It was hardcoded, which meant the rehearsal could not run
	// beside a demo server without the two fighting over 8080.
	portFlag := flag.String("port", "8080", "the port to listen on")
	flag.Parse()

	repoRoot := findRepoRoot()
	if repoRoot == "" {
		// Refused rather than guessed. This function used to return a hardcoded
		// absolute path — "C:\VS-Code-Projects\Github\AI AI CAPTAIN", which is
		// not where the repository is and not where it ever was — so a console
		// started from an unexpected directory went looking for its binary two
		// levels away under a name that does not exist, and the failure it then
		// printed was about a path nobody recognises.
		//
		// log.Print and a return, rather than log.Fatal: main has a deferred
		// recover at its head, and log.Fatal would exit past it. gocritic says
		// so, and it is right that a nil-root exit is not worth taking the
		// panic handler with it.
		log.Print("cannot find the repository: no pocctl.exe under poc/active-monitoring/bin/ and no securityctl.exe above the working directory. Start the console from the repository root, or build it with `go build -o pocctl.exe ./poc/active-monitoring/cmd/pocctl`")
		return
	}
	log.Printf("Repo root: %s", repoRoot)

	securityctlPath := mustSecurityctl(repoRoot)

	// The database pool, when a test/database URL is configured. The new
	// free-text search and chat surfaces read the canonical record directly
	// instead of shelling out; everything else still runs the CLI binary.
	pool := openDatabasePool()
	if pool != nil {
		defer pool.Close()
	}

	// Static files. The showcase's pages, stylesheets and scripts are
	// compiled into the binary, so the server serves the same bytes whether it
	// was started from a source tree or from a release archive.
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub()))))

	// API routes
	registerAPIRoutes(http.DefaultServeMux, securityctlPath, repoRoot, *dev)

	// Free-text search and grounded chat read the canonical record directly.
	// The scope the console server acts as: the tenant and the sites, both
	// configured, neither of them read from a request. See web/scope.go.
	consoleScope := configuredScope()
	consoleScope.describeScope()
	setPrincipal(consoleScope.tenant)

	registerSearchRoutes(repoRoot, pool, consoleScope)
	registerChatRoutes(repoRoot, pool, consoleScope)
	registerClipsAndCriticalRoutes(repoRoot, pool, consoleScope)
	registerCameraRoutes(repoRoot, pool, consoleScope)

	// The console is a third thing: it is the operator's window onto the
	// product and onto the VMS, so it proxies to both rather than asking the
	// browser to.
	registerProxies()

	// The pages. Two owners and no others: the showcase owns the root and its
	// acts, the console owns /console and everything under it. They are
	// registered on their own mux and mounted at "/", so the root space has
	// exactly one dispatcher and the API, the proxies and the static files -
	// which are more specific patterns - still win.
	pageMux := http.NewServeMux()
	registerPageRoutes(pageMux)
	if *dev {
		pageMux.HandleFunc("/dev", serveDevPage())
	}
	http.Handle("/", pageMux)

	registerLiveRoutes(repoRoot)
	registerShowcaseRoutes(repoRoot)
	registerRundocRoutes(repoRoot)
	registerAskRoutes(repoRoot)
	registerChiefRoutes(repoRoot)
	registerReplayRoutes(repoRoot)

	port := *portFlag
	log.Printf("Starting web UI on http://localhost:%s", port)
	log.Printf("Using binary at: %s", securityctlPath)
	// The middleware, in the order they run.
	//
	// The request id goes on first, because the access log carries it and a
	// log line without one is a line nobody can find. The security headers go
	// on last, because they must be on the response of every handler
	// including the ones that answer with an error.
	server := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: 5 * time.Second,
		Handler: securityHeaders(
			withRequestID(
				refuseCrossOrigin(
					accessLog(http.DefaultServeMux)))),
	}
	serveOrFatal(server)
}

func serveOrFatal(server *http.Server) {
	log.Fatal(server.ListenAndServe())
}

// serveDevPage answers /dev, the capture-path runner.
//
// It used to be the front door: web/static/index.html, a page with three tabs
// that ran the capture path, the acceptance criteria and the rule linter. Two
// front doors on one server is one too many, and the one that was in the way
// was a page of buttons rather than a claim.
//
// It is a development surface, so it is behind -dev, and the pages it drives
// are behind the same flag.
func serveDevPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		serveEmbedded(w, r, "static/dev.html")
	}
}

func mustSecurityctl(repoRoot string) string {
	if p := pocctlAt(repoRoot); p != "" {
		return p
	}
	if p := securityctlAt(repoRoot); p != "" {
		return p
	}
	log.Fatalf("neither pocctl.exe under poc/active-monitoring/bin/ nor securityctl.exe was found at: %s", repoRoot)
	return ""
}

func pocctlAt(dir string) string {
	name := "pocctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, "poc", "active-monitoring", "bin", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func securityctlAt(dir string) string {
	p := filepath.Join(dir, "securityctl.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if pocctlAt(dir) != "" || securityctlAt(dir) != "" {
			if _, err := os.Stat(filepath.Join(dir, "cmd")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func handlePOC(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handlePOC: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Source string `json:"source"`
			URL    string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var output []byte
		var err error

		switch req.Source {
		case "fixture":
			output, err = runCmd(r.Context(), securityctlPath, repoRoot, "poc", "--stream", filepath.Join(repoRoot, "testdata", "poc", "testsrc.h264"), "--json")
		case "youtube":
			if req.URL == "" {
				http.Error(w, "YouTube URL required", http.StatusBadRequest)
				return
			}
			output, err = runYouTubePOC(repoRoot, req.URL)
		default:
			http.Error(w, "Invalid source", http.StatusBadRequest)
			return
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(output)
	}
}

func runYouTubePOC(_, _ string) ([]byte, error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("PANIC in runYouTubePOC: %v", rec)
		}
	}()
	return nil, fmt.Errorf("youtube POC not yet implemented: HLS URLs expire too quickly; use the test fixture instead")
}

func runCmd(ctx context.Context, securityctlPath, repoRoot string, args ...string) ([]byte, error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("PANIC in runCmd: %v", rec)
		}
	}()
	cmd := exec.CommandContext(ctx, securityctlPath, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+os.Getenv("PATH")+";C:\\Program Files\\Go\\bin;C:\\msys64\\mingw64\\bin;"+os.Getenv("USERPROFILE")+"\\go\\bin",
	)
	return cmd.CombinedOutput()
}

func handleAcceptance(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleAcceptance: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		output, err := runCmd(r.Context(), securityctlPath, repoRoot, "acceptance", "--soak", "200", "--json")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(output)
	}
}

func handleRulesLint(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleRulesLint: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		output, err := runCmd(r.Context(), securityctlPath, repoRoot, "rules", "lint", "--bundle", filepath.Join(repoRoot, "rules", "builtin", "bundle.yaml"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"output": %q}`, string(output))
	}
}

func handleRulesSimulate(securityctlPath, repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleRulesSimulate: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		output, err := runCmd(r.Context(), securityctlPath, repoRoot,
			"rules", "simulate",
			"--bundle", filepath.Join(repoRoot, "rules", "builtin", "bundle.yaml"),
			"--fixtures", filepath.Join(repoRoot, "testdata", "fixtures"),
			"--json",
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(output)
	}
}

// openDatabasePool opens the canonical-record pool when SP_DATABASE_URL is
// configured, and returns nil otherwise. A fatal configuration error exits
// immediately, so no deferred close is pending in this function.
func openDatabasePool() *dbx.Pool {
	dsn := os.Getenv("SP_DATABASE_URL")
	if dsn == "" {
		return nil
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(dsn); err != nil {
		log.Fatalf("database: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_DATABASE_PASSWORD"))
	if err != nil {
		log.Fatalf("database: open pool: %v", err)
	}
	return pool
}
