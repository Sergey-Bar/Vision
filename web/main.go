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
	"time"
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
		log.Print("cannot find the repository: no securityctl.exe above the working directory. Start the console from the repository root, or build it with `go build -o securityctl.exe ./cmd/securityctl`")
		return
	}
	log.Printf("Repo root: %s", repoRoot)

	securityctlPath := mustSecurityctl(repoRoot)

	// Static files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(repoRoot, "web", "static")))))

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
	registerLiveRoutes(repoRoot)

	// Main page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(repoRoot, "web", "static", "index.html"))
	})

	// The operator surface. It is a page, not a tab on the console, because the
	// console answers "run this and show me the output" and this answers "where
	// does my system stay quiet" — different questions, different readers.
	http.HandleFunc("/operations", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(repoRoot, "web", "static", "operations.html"))
	})

	port := "8080"
	log.Printf("Starting web UI on http://localhost:%s", port)
	log.Printf("Using securityctl at: %s", securityctlPath)
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
	path := filepath.Join(repoRoot, "securityctl.exe")
	if _, err := os.Stat(path); err != nil {
		log.Fatalf("securityctl.exe not found at: %s", path)
	}
	return path
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "securityctl.exe")); err == nil {
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
