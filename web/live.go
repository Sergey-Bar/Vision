package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/youtube"
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
		frames, err := streamDetector(ctx, repoRoot, conn, canonicalURL, videoID, profile)
		if err != nil && frames == 0 {
			// No real detector frames were produced (missing venv, CUDA, or an
			// unreachable stream). Fall back to the built-in animated demo so the
			// dashboard never stays blank; the DEMO badge keeps it distinguishable
			// from a real camera feed.
			_ = demoStream(ctx, conn)
			return
		}
		if err != nil {
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

func streamDetector(ctx context.Context, repoRoot string, conn *websocket.Conn, rawURL, videoID, profile string) (int, error) {
	python := filepath.Join(repoRoot, ".venv-poc", "Scripts", "python.exe")
	args := []string{filepath.Join(repoRoot, "tools", "youtube_live_detector.py"), "--url", rawURL}
	if cookies := os.Getenv("YT_COOKIES"); cookies != "" {
		args = append(args, "--cookies", cookies)
	}
	if profile == "realtime" {
		args = append(args, "--imgsz", "1280", "--confidence", "0.25")
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("open detector output: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("open detector diagnostics: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start detector: %w", err)
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
			return frames, err
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return frames, err
		}
		if frames == 0 {
			return frames, demoStream(ctx, conn)
		}
		return frames, fmt.Errorf("read detector output: %w", err)
	}
	detail := <-diagnostics
	if detail != "" && frames == 0 {
		return frames, demoStream(ctx, conn)
	}
	if detail != "" {
		return frames, fmt.Errorf("detector stopped: %s", detail)
	}
	if frames == 0 {
		return frames, demoStream(ctx, conn)
	}
	return frames, errors.New("detector stream ended")
}

// demoStream is a built-in demonstration feed. It runs when the Python detector
// cannot start or produces no real frames (missing venv, missing CUDA, or an
// unreachable stream) so the dashboard shows analytics instead of a black screen.
// It renders an animated street scene with a DEMO watermark so operators can
// tell the difference between simulated demo analytics and a real camera feed.
func demoStream(ctx context.Context, conn *websocket.Conn) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	const width, height = 1280, 720
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	type movObj struct {
		label  string
		cat    string
		x, y   float64
		dx, dy float64
		w, h   float64
		zone   string
	}
	objs := []movObj{
		{"red car", "vehicle", 0.02, 0.55, 0.0018, 0, 0.14, 0.09, "public"},
		{"silver truck", "vehicle", 0.85, 0.60, -0.0014, 0, 0.16, 0.10, "public"},
		{"person", "biological", 0.10, 0.82, 0.0010, 0.0002, 0.04, 0.07, "crosswalk"},
		{"person", "biological", 0.78, 0.80, -0.0009, -0.0001, 0.04, 0.07, "crosswalk"},
		{"silver car", "vehicle", 0.45, 0.58, 0.0012, -0.0001, 0.12, 0.08, "public"},
		{"red car", "vehicle", 0.92, 0.63, -0.0011, 0.0002, 0.13, 0.09, "no-zone"},
	}
	zones := []liveZone{
		{ID: "crosswalk", Name: "Crosswalk", Type: "watch", Points: [][2]float64{{0.04, 0.72}, {0.82, 0.68}, {0.96, 0.92}, {0.06, 0.95}}},
		{ID: "no-zone", Name: "Restricted no-zone", Type: "restricted", Points: [][2]float64{{0.00, 0.48}, {0.24, 0.45}, {0.30, 0.74}, {0.00, 0.80}}},
	}
	glyphBitmap := func(ch rune) [][]bool {
		font := map[rune][][]bool{
			'D': {{true, true, true}, {true, false, true}, {true, false, true}, {true, false, true}, {true, true, true}},
			'E': {{true, true, true}, {true, false, false}, {true, true, false}, {true, false, false}, {true, true, true}},
			'M': {{true, false, true}, {true, true, true}, {true, false, true}, {true, false, true}, {true, false, true}},
			'O': {{true, true, true}, {true, false, true}, {true, false, true}, {true, false, true}, {true, true, true}},
		}
		if g, ok := font[ch]; ok {
			return g
		}
		return [][]bool{{true}}
	}
	drawScene := func(img *image.RGBA, frame int) {
		wf := float64(width)
		hf := float64(height)
		sky := color.RGBA{14, 18, 26, 255}
		draw.Draw(img, img.Bounds(), &image.Uniform{sky}, image.Point{}, draw.Src)
		// buildings
		buildings := []struct{ x, w, h float64 }{{0, 0.22, 0.30}, {0.28, 0.18, 0.22}, {0.52, 0.20, 0.28}, {0.78, 0.24, 0.20}}
		for _, b := range buildings {
			r := image.Rect(int(b.x*wf), int((0.70-b.h)*hf), int((b.x+b.w)*wf), int(0.70*hf))
			draw.Draw(img, r, &image.Uniform{color.RGBA{24, 28, 40, 255}}, image.Point{}, draw.Src)
			// windows
			for wx := 0.0; wx < b.w-0.03; wx += 0.05 {
				for wy := 0.0; wy < b.h-0.04; wy += 0.06 {
					if rng.Float64() > 0.7 {
						wr := image.Rect(int((b.x+wx+0.01)*wf), int((0.70-b.h+wy+0.01)*hf), int((b.x+wx+0.035)*wf), int((0.70-b.h+wy+0.04)*hf))
						draw.Draw(img, wr, &image.Uniform{color.RGBA{255, 220, 120, 255}}, image.Point{}, draw.Src)
					}
				}
			}
		}
		// road
		roadTop := int(0.55 * hf)
		draw.Draw(img, image.Rect(0, roadTop, width, height), &image.Uniform{color.RGBA{26, 26, 30, 255}}, image.Point{}, draw.Src)
		// lane lines
		for i := 0; i < width; i += 60 {
			dx := (i+frame*12)%(width+60) - 60
			draw.Draw(img, image.Rect(dx, int(0.62*hf), dx+30, int(0.63*hf)), &image.Uniform{color.RGBA{90, 90, 100, 255}}, image.Point{}, draw.Src)
		}
		// crosswalk stripes
		for i := 0.0; i < 1.0; i += 0.04 {
			x := int(i * wf)
			draw.Draw(img, image.Rect(x, int(0.86*hf), x+20, int(0.92*hf)), &image.Uniform{color.RGBA{200, 200, 205, 255}}, image.Point{}, draw.Src)
		}
		// sidewalk
		draw.Draw(img, image.Rect(0, int(0.92*hf), width, height), &image.Uniform{color.RGBA{34, 36, 44, 255}}, image.Point{}, draw.Src)
		// DEMO badge
		badge := "DEMO"
		bx, by := width-90, 24
		br := image.Rect(bx-6, by-10, bx+6+len(badge)*8, by+10)
		draw.Draw(img, br, &image.Uniform{color.RGBA{255, 69, 58, 230}}, image.Point{}, draw.Src)
		txtColor := color.RGBA{255, 255, 255, 255}
		for i, ch := range badge {
			tx := bx + i*10
			ty := by - 4
			glyph := glyphBitmap(ch)
			for gy, row := range glyph {
				for gx, on := range row {
					if on {
						px := tx + gx*2
						py := ty + gy*2
						if px >= 0 && px < width && py >= 0 && py < height {
							img.SetRGBA(px, py, txtColor)
						}
					}
				}
			}
		}
	}
	frame := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			frame++
			img := image.NewRGBA(image.Rect(0, 0, width, height))
			drawScene(img, frame)

			nDetections := 3 + rng.Intn(5)
			people := 0
			vehicles := 0
			restricted := 0
			dets := make([]liveDetection, 0, nDetections+len(objs))
			for i := range objs {
				o := &objs[i]
				o.x += o.dx
				o.y += o.dy
				if o.x < -0.2 {
					o.x = 1.02
				}
				if o.x > 1.02 {
					o.x = -0.2
				}
				if o.y < 0.5 {
					o.y = 0.5
					o.dy = -o.dy
				}
				if o.y > 0.88 {
					o.y = 0.88
					o.dy = -o.dy
				}
				z := zoneFor(o.x+o.w/2, o.y+o.h/2, zones)
				o.zone = z
				if z == "no-zone" {
					restricted++
				}
				if o.cat == "vehicle" {
					vehicles++
				} else if o.label == "person" {
					people++
				}
				dets = append(dets, liveDetection{
					ID:          fmt.Sprintf("demo-%d-%d", frame, i),
					Label:       o.label,
					Category:    o.cat,
					Color:       "",
					VehicleType: "",
					Confidence:  0.70 + rng.Float64()*0.25,
					BBox:        [4]float64{o.x, o.y, o.w, o.h},
					Zone:        z,
					Violation:   z == "no-zone",
				})
			}
			for i := 0; i < nDetections; i++ {
				x := float64(rng.Intn(800)) / 1000
				y := 0.52 + float64(rng.Intn(300))/1000
				w := 0.05 + rng.Float64()*0.14
				h := 0.07 + rng.Float64()*0.18
				if x+w > 0.98 {
					x = 0.98 - w
				}
				if y+h > 0.90 {
					y = 0.90 - h
				}
				z := zoneFor(x+w/2, y+h/2, zones)
				if z == "no-zone" {
					restricted++
				}
				cat := "other"
				label := "cone"
				if rng.Float64() < 0.5 {
					cat = "biological"
					label = "person"
					people++
				} else {
					cat = "vehicle"
					label = "car"
					vehicles++
				}
				dets = append(dets, liveDetection{
					ID:          fmt.Sprintf("demo-%d-%d", frame, len(objs)+i),
					Label:       label,
					Category:    cat,
					Color:       "",
					VehicleType: "",
					Confidence:  0.60 + rng.Float64()*0.30,
					BBox:        [4]float64{x, y, w, h},
					Zone:        z,
					Violation:   z == "no-zone",
				})
			}
			for _, d := range dets {
				r := image.Rect(int(d.BBox[0]*float64(width)), int(d.BBox[1]*float64(height)),
					int((d.BBox[0]+d.BBox[2])*float64(width)), int((d.BBox[1]+d.BBox[3])*float64(height)))
				var c color.RGBA
				switch d.Category {
				case "vehicle":
					c = color.RGBA{56, 189, 248, 255}
				case "biological":
					c = color.RGBA{52, 211, 153, 255}
				default:
					c = color.RGBA{251, 191, 36, 255}
				}
				draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Over)
			}
			buf := &bytes.Buffer{}
			if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 68}); err != nil {
				return err
			}
			avg := 0.0
			for _, d := range dets {
				avg += d.Confidence
			}
			if len(dets) > 0 {
				avg /= float64(len(dets))
			}
			f := liveFrame{
				VideoID:           "demo",
				Backend:           "demo",
				Frame:             frame,
				At:                time.Now(),
				People:            people,
				Vehicles:          vehicles,
				Animals:           0,
				Other:             0,
				Detections:        dets,
				Zones:             zones,
				ImageData:         "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
				InferenceMS:       6.0,
				EncodeMS:          3.0,
				AnalysisFPS:       4.0,
				SourceWidth:       width,
				SourceHeight:      height,
				TransportKB:       float64(buf.Len()) / 1024,
				Profile:           "yolo11x-demo",
				UniqueTracks:      len(dets),
				RestrictedCount:   restricted,
				AverageConfidence: avg,
				OccupancyPercent:  0,
			}
			if err := conn.WriteJSON(f); err != nil {
				return err
			}
		}
	}
}

func zoneFor(x, y float64, zones []liveZone) string {
	for _, z := range zones {
		if z.Type == "restricted" && pointInPolygon(x, y, z.Points) {
			return z.ID
		}
	}
	for _, z := range zones {
		if pointInPolygon(x, y, z.Points) {
			return z.ID
		}
	}
	return "public"
}

func pointInPolygon(x, y float64, points [][2]float64) bool {
	inside := false
	for i := 0; i < len(points); i++ {
		x1, y1 := points[i][0], points[i][1]
		x2, y2 := points[(i+1)%len(points)][0], points[(i+1)%len(points)][1]
		if (y1 > y) != (y2 > y) && x < (x2-x1)*(y-y1)/(y2-y1)+x1 {
			inside = !inside
		}
	}
	return inside
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
