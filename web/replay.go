package main

// The replay clip endpoint: /api/replay/clip
//
// GET /api/replay/clip?run=<run-id>&det=<detection-id>
//
// The handler resolves the clip FROM THE STORED REVIEW ROW: it re-reads the
// run's review sheet through the POC's own tool, finds the row whose detection
// id the request named, and serves the reference THAT ROW carries, through the
// root repository's evidence store, which verifies the bytes against the
// content-addressed filename on the way out. The request names a detection,
// never a file: there is no parameter that reaches a path, so `..`, an absolute
// path and a NUL byte are not filtered, they are unspellable. (RC-31: the clip a
// replay serves is the clip the stored row names, not the clip the request names.)

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Sergey-Bar/vision/internal/evidence"
)

var (
	replayRunIDRe = regexp.MustCompile(`^run-[0-9a-f]{16}$`)
	replayDetIDRe = regexp.MustCompile(`^det-[0-9a-f]{24}$`)
	replayClipRe  = regexp.MustCompile(`^[0-9a-f]{2}/[0-9a-f]{2}/([0-9a-f]{64})\.(mp4|jpg|jpeg|png|webm)$`)
)

func registerReplayRoutes(repoRoot string) {
	http.HandleFunc("/api/replay/clip", handleReplayClip(repoRoot))
}

func handleReplayClip(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleReplayClip: %v", rec)
				http.Error(w, "Internal error", http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		runID := r.URL.Query().Get("run")
		detID := r.URL.Query().Get("det")
		if !replayRunIDRe.MatchString(runID) || !replayDetIDRe.MatchString(detID) {
			replayRefuse(w, http.StatusBadRequest, "run and det must be a run id and a detection id as the store writes them (`run-` + 16 hex, `det-` + 24 hex). Any extra parameter - including one spelled like a path - is ignored.")
			return
		}

		root, reason := pocRootAndBinary(repoRoot)
		if reason != "" {
			replayRefuse(w, http.StatusNotFound, reason)
			return
		}

		review, err := loadReview(r.Context(), repoRoot, root, runID)
		if err != nil {
			replayRefuse(w, http.StatusNotFound, err.Error())
			return
		}

		var row *pocReviewRow
		for i := range review.Rows {
			if review.Rows[i].DetectionID == detID {
				row = &review.Rows[i]
				break
			}
		}
		if row == nil {
			replayRefuse(w, http.StatusNotFound, fmt.Sprintf("run %s holds no detection %s as the store reports it", runID, detID))
			return
		}
		if row.ClipRef == "" {
			replayRefuse(w, http.StatusNotFound, fmt.Sprintf("detection %s has no clip in the store; the review sheet lists it without one, so it cannot be played or judged", detID))
			return
		}

		m := replayClipRe.FindStringSubmatch(row.ClipRef)
		if m == nil {
			replayRefuse(w, http.StatusConflict, fmt.Sprintf("the stored reference %q is not content-addressed and is not served by this endpoint", row.ClipRef))
			return
		}
		sha := m[1]

		evidenceRoot := filepath.Join(repoRoot, filepath.FromSlash(root.Path), "evidence")
		fi, statErr := os.Stat(evidenceRoot)
		if statErr != nil || !fi.IsDir() {
			replayRefuse(w, http.StatusNotFound, "the chosen data root has no evidence directory beside its database")
			return
		}

		store, err := evidence.New(evidence.Options{
			Root:             evidenceRoot,
			Fsync:            evidence.Bool(false),
			VerifyHashOnRead: evidence.Bool(true),
		})
		if err != nil {
			replayRefuse(w, http.StatusInternalServerError, err.Error())
			return
		}

		rc, obj, err := store.Open(row.ClipRef, sha)
		if err != nil {
			status := http.StatusConflict
			if os.IsNotExist(err) {
				status = http.StatusNotFound
			}
			if errors.Is(err, evidence.ErrHashMismatch) {
				status = http.StatusConflict
			}
			replayRefuse(w, status, err.Error())
			return
		}
		defer func() { _ = rc.Close() }()

		w.Header().Set("Content-Type", clipContentType(row.ClipRef))
		w.Header().Set("X-Content-SHA256", obj.SHA256)
		w.Header().Set("Content-Length", strconv.FormatInt(obj.SizeBytes, 10))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	}
}

// replayRefuse writes a structured refusal in the same JSON shape every other
// endpoint uses, so the page renders a reason in every case.
func replayRefuse(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
}

func clipContentType(ref string) string {
	switch strings.ToLower(filepath.Ext(ref)) {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return "application/octet-stream"
}
