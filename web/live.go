package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/youtube"
	"github.com/gorilla/websocket"
)

type liveDetection struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Category    string     `json:"category"`
	Color       string     `json:"color,omitempty"`
	VehicleType string     `json:"vehicleType,omitempty"`
	Confidence  float64    `json:"confidence"`
	BBox        [4]float64 `json:"bbox"`
	Zone        string     `json:"zone"`
	Violation   bool       `json:"violation"`
}

type liveZone struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Type   string       `json:"type"`
	Points [][2]float64 `json:"points"`
}

type liveAlert struct {
	ID       string    `json:"id"`
	Severity string    `json:"severity"`
	Rule     string    `json:"rule"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

type liveFrame struct {
	VideoID           string          `json:"videoId"`
	Backend           string          `json:"backend"`
	Frame             int             `json:"frame"`
	At                time.Time       `json:"at"`
	People            int             `json:"people"`
	Vehicles          int             `json:"vehicles"`
	Animals           int             `json:"animals"`
	Other             int             `json:"other"`
	Detections        []liveDetection `json:"detections"`
	Zones             []liveZone      `json:"zones"`
	Alerts            []liveAlert     `json:"alerts"`
	SearchHits        []liveDetection `json:"searchHits"`
	ImageData         string          `json:"imageData,omitempty"`
	InferenceMS       float64         `json:"inferenceMs,omitempty"`
	EncodeMS          float64         `json:"encodeMs,omitempty"`
	AnalysisFPS       float64         `json:"analysisFps,omitempty"`
	SourceWidth       int             `json:"sourceWidth,omitempty"`
	SourceHeight      int             `json:"sourceHeight,omitempty"`
	TransportKB       float64         `json:"transportKB,omitempty"`
	Profile           string          `json:"profile,omitempty"`
	UniqueTracks      int             `json:"uniqueTracks,omitempty"`
	RestrictedCount   int             `json:"restrictedCount,omitempty"`
	AverageConfidence float64         `json:"averageConfidence,omitempty"`
	OccupancyPercent  float64         `json:"occupancyPercent,omitempty"`
}

var liveUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}}

var liveSession = struct {
	sync.Mutex
	cancel     context.CancelFunc
	conn       *websocket.Conn
	generation uint64
}{}

func registerLiveRoutes(repoRoot string) {
	http.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(repoRoot, "web", "static", "live.html"))
	})
	http.HandleFunc("/ws/live", handleLiveWS(repoRoot))
}

func handleLiveWS(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := liveUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("live websocket upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if r.URL.Query().Get("protocol") != "2" {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(4002, "dashboard refresh required"), time.Now().Add(time.Second))
			return
		}
		ctx, cancel, generation := beginLiveSession(r.Context(), conn)
		defer cancel()
		defer endLiveSession(generation)
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					cancel()
					return
				}
			}
		}()

		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			rawURL = "https://www.youtube.com/watch?v=JQ_jwk_7OVE"
		}
		videoID, err := youtube.ExtractVideoID(rawURL)
		if err != nil {
			_ = conn.WriteJSON(map[string]string{"error": err.Error()})
			return
		}
		canonicalURL := "https://www.youtube.com/watch?v=" + videoID
		profile := r.URL.Query().Get("profile")
		if profile != "realtime" {
			profile = "maximum"
		}
		if err := streamDetector(ctx, repoRoot, conn, canonicalURL, videoID, profile); err != nil && ctx.Err() == nil {
			_ = conn.WriteJSON(map[string]string{"error": err.Error()})
		}
	}
}

func beginLiveSession(parent context.Context, conn *websocket.Conn) (context.Context, context.CancelFunc, uint64) {
	liveSession.Lock()
	defer liveSession.Unlock()
	if liveSession.cancel != nil {
		liveSession.cancel()
	}
	if liveSession.conn != nil {
		_ = liveSession.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4001, "analysis moved to another tab"), time.Now().Add(time.Second))
	}
	liveSession.generation++
	ctx, cancel := context.WithCancel(parent)
	liveSession.cancel = cancel
	liveSession.conn = conn
	return ctx, cancel, liveSession.generation
}

func endLiveSession(generation uint64) {
	liveSession.Lock()
	defer liveSession.Unlock()
	if liveSession.generation == generation {
		liveSession.cancel = nil
		liveSession.conn = nil
	}
}

func streamDetector(ctx context.Context, repoRoot string, conn *websocket.Conn, rawURL, videoID, profile string) error {
	python := filepath.Join(repoRoot, ".venv-poc", "Scripts", "python.exe")
	args := []string{filepath.Join(repoRoot, "tools", "youtube_live_detector.py"), "--url", rawURL}
	if profile == "realtime" {
		args = append(args, "--imgsz", "1280", "--confidence", "0.25")
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open detector output: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open detector diagnostics: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detector: %w", err)
	}
	processDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stopProcessTree(ctx, cmd)
		case <-processDone:
		}
	}()
	defer close(processDone)
	defer func() { stopProcessTree(ctx, cmd); _ = cmd.Wait() }()

	diagnostics := make(chan string, 1)
	go func() {
		body, _ := io.ReadAll(io.LimitReader(stderr, 64<<10))
		diagnostics <- strings.TrimSpace(string(body))
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	frames := 0
	for scanner.Scan() {
		var frame liveFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			continue
		}
		frame.VideoID = videoID
		frame.Backend = "cuda-yolo11x"
		frames++
		if err := conn.WriteJSON(frame); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read detector output: %w", err)
	}
	detail := <-diagnostics
	if detail != "" {
		return fmt.Errorf("detector stopped: %s", detail)
	}
	if frames == 0 {
		return errors.New("detector stopped before producing a real frame")
	}
	return errors.New("detector stream ended")
}

// stopProcessTree kills the sidecar, and on Windows its whole process tree.
func stopProcessTree(ctx context.Context, cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// A Windows venv launcher creates a second python.exe. Killing only the
		// launcher leaks the CUDA worker after every reconnect or profile switch.
		// CommandContext rather than Command: taskkill on an already-dead pid
		// would otherwise hang this goroutine until it is killed.
		_ = exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = cmd.Process.Kill()
}
