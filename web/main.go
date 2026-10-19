// Package main provides the web UI server for the Vision POC.
package main

import (
	"context"
	"encoding/json"
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

func main() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Fatalf("PANIC in main: %v", rec)
		}
	}()
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

	// Static files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(repoRoot, "web", "static")))))

	// The SPA builds its assets into web/static/spa/assets and index.html refers to
	// them at /assets/..., so serve that directory directly.
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(repoRoot, "web", "static", "spa", "assets")))))

	// API routes
	http.HandleFunc("/api/poc", handlePOC(securityctlPath, repoRoot))
	http.HandleFunc("/api/acceptance", handleAcceptance(securityctlPath, repoRoot))
	http.HandleFunc("/api/rules/lint", handleRulesLint(securityctlPath, repoRoot))
	http.HandleFunc("/api/rules/simulate", handleRulesSimulate(securityctlPath, repoRoot))

	// The operator surface. These three are the product's claims, reachable by
	// clicking rather than by knowing which command to run.
	http.HandleFunc("/api/efficiency", handleEfficiency(securityctlPath, repoRoot))
	http.HandleFunc("/api/incident", handleIncident(securityctlPath, repoRoot))
	http.HandleFunc("/api/explain", handleExplain(securityctlPath, repoRoot))

	// Free-text search and grounded chat read the canonical record directly.
	registerSearchRoutes(repoRoot, pool)
	registerChatRoutes(repoRoot, pool)
	registerClipsAndCriticalRoutes(repoRoot, pool)
	registerCameraRoutes(repoRoot, pool)

	// The console is a third thing: it is the operator's window onto the
	// product and onto the VMS, so it proxies to both rather than asking the
	// browser to.
	registerProxies()

	// The SPA lives under web/static/spa (built by `npm run build` in web/spa).
	// Its assets are served by the /static/ handler above; its index.html is the
	// single entry point for every non-API route. Old HTML pages (hub, console,
	// pipeline, trust, lab, census, operations, ask, live, fleet, chief) redirect
	// to / so there is one operator surface.
	spaIndex := filepath.Join(repoRoot, "web", "static", "spa", "index.html")
	spaHandler := func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, spaIndex)
	}
	for _, p := range []string{"/", "/cameras", "/clips", "/console", "/events", "/incidents", "/pipeline", "/trust", "/lab", "/census", "/operations", "/ask", "/live", "/fleet", "/chief", "/sites", "/users", "/audit", "/settings"} {
		http.HandleFunc(p, spaHandler)
	}

	registerLiveRoutes(repoRoot)
	registerShowcaseRoutes(repoRoot)
	registerRundocRoutes(repoRoot)
	registerAskRoutes(repoRoot)
	registerChiefRoutes(repoRoot)
	registerReplayRoutes(repoRoot)

	port := "8080"
	log.Printf("Starting web UI on http://localhost:%s", port)
	log.Printf("Using binary at: %s", securityctlPath)
	server := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveOrFatal(server)
}

func serveOrFatal(server *http.Server) {
	log.Fatal(server.ListenAndServe())
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
