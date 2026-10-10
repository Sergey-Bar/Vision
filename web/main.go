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
	log.Printf("Repo root: %s", repoRoot)

	securityctlPath := mustSecurityctl(repoRoot)

	// Static files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(repoRoot, "web", "static")))))

	// API routes
	http.HandleFunc("/api/poc", handlePOC(securityctlPath, repoRoot))
	http.HandleFunc("/api/acceptance", handleAcceptance(securityctlPath, repoRoot))
	http.HandleFunc("/api/rules/lint", handleRulesLint(securityctlPath, repoRoot))
	http.HandleFunc("/api/rules/simulate", handleRulesSimulate(securityctlPath, repoRoot))
	registerLiveRoutes(repoRoot)

	// Main page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(repoRoot, "web", "static", "index.html"))
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
	return "C:\\VS-Code-Projects\\Github\\AI AI CAPTAIN"
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
