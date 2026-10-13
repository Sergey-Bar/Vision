package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"fmt"

	"log"

	"os/exec"

	"strings"

	"sync"

	"sync/atomic"

	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"

	"github.com/Sergey-Bar/vision/internal/anomaly"

	"github.com/Sergey-Bar/vision/internal/correlation"

	"github.com/Sergey-Bar/vision/internal/evidence"

	"github.com/Sergey-Bar/vision/internal/mp4"

	"github.com/Sergey-Bar/vision/internal/sitemodel"

	"github.com/Sergey-Bar/vision/internal/telemetry"

	"github.com/Sergey-Bar/vision/internal/threat"

	"github.com/Sergey-Bar/vision/poc/categorization"

	"github.com/Sergey-Bar/vision/poc/search"

	"github.com/Sergey-Bar/vision/poc/spatial"

	"github.com/Sergey-Bar/vision/poc/ws"

	"github.com/Sergey-Bar/vision-poc/detectclient"

	"gopkg.in/yaml.v3"
)

const (
	pocVersion = "poc/youtube/0.1.0"
)

type Config struct {
	YouTube YouTubeConfig `yaml:"youtube"`

	Cameras []CameraConfig `yaml:"cameras"`

	Live LiveConfig `yaml:"live"`

	Detector DetectorConfig `yaml:"detector"`

	Categorization CategorizationConfig `yaml:"categorization"`

	Spatial SpatialConfig `yaml:"spatial"`

	Search SearchConfig `yaml:"search"`

	WebSocket WSConfig `yaml:"websocket"`

	AIRuntime AIRuntimeConfig `yaml:"ai_runtime"`

	Evidence EvidenceConfig `yaml:"evidence"`

	Web WebConfig `yaml:"web"`

	Logging LoggingConfig `yaml:"logging"`

	Site *SiteConfig `yaml:"site"`
}

// CameraConfig is one camera in the fleet's stream registry: the stream its
// frames come from and its display name.
//
// The camera-zone declaration does NOT live here: which zones a camera covers
// is declared once, in the site model (sitemodel.Camera.ZoneIDs), and two
// declarations of it would drift apart.
type CameraConfig struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	YouTubeURL string `yaml:"youtube_url"`
}

type YouTubeConfig struct {
	URL      string `yaml:"url"`
	Quality  string `yaml:"quality"`
	LiveEdge int    `yaml:"hlslive_edge"`
}

type LiveConfig struct {
	CaptureFPS       float64       `yaml:"capture_fps"`
	SidecarAddr      string        `yaml:"sidecar_addr"`
	SidecarScript    string        `yaml:"sidecar_script"`
	Model            string        `yaml:"model"`
	Anchors          string        `yaml:"anchors"`
	Python           string        `yaml:"python"`
	MinScore         float64       `yaml:"min_score"`
	Deadline         time.Duration `yaml:"deadline"`
	IndexEveryFrames int           `yaml:"index_every_frames"`
}

type DetectorConfig struct {
	Threshold float64       `yaml:"threshold"`
	PreRoll   time.Duration `yaml:"pre_roll"`
	PostRoll  time.Duration `yaml:"post_roll"`
	MaxTime   time.Duration `yaml:"max_clip_time"`
	MaxBytes  int64         `yaml:"max_clip_bytes"`
}

type CategorizationConfig struct {
	Enabled            bool    `yaml:"enabled"`
	ConfidenceFloor    float64 `yaml:"confidence_floor"`
	MaxObjectsPerFrame int     `yaml:"max_objects_per_frame"`
}

type SpatialConfig struct {
	Enabled bool                 `yaml:"enabled"`
	Rules   []RuleConfig         `yaml:"rules"`
	Zones   []spatial.ZoneConfig `yaml:"zones"`
}

// RuleConfig is one zone rule the POC's spatial engine watches. It is POC
// configuration, not the product's contract surface: the zone it applies to,
// the categories whose presence is a violation, and the naming and severity
// the violation is reported under.
type RuleConfig struct {
	ID            string        `yaml:"id"`
	ZoneID        string        `yaml:"zone_id"`
	Cooldown      time.Duration `yaml:"cooldown"`
	Categories    []string      `yaml:"categories"`
	ViolationType string        `yaml:"violation_type"`
	Severity      string        `yaml:"severity"`
}

type SearchConfig struct {
	Retention  time.Duration `yaml:"retention"`
	Enabled    bool          `yaml:"enabled"`
	MaxEntries int           `yaml:"max_entries"`
}

type WSConfig struct {
	Port        int           `yaml:"port"`
	Heartbeat   time.Duration `yaml:"heartbeat"`
	ReadBuffer  int           `yaml:"read_buffer"`
	WriteBuffer int           `yaml:"write_buffer"`
}

type AIRuntimeConfig struct {
	Endpoint    string        `yaml:"endpoint"`
	Timeout     time.Duration `yaml:"timeout"`
	MaxBytes    int64         `yaml:"max_response_bytes"`
	ProfileID   string        `yaml:"profile_id"`
	HealthEvery string        `yaml:"health_every"`
}

type EvidenceConfig struct {
	TempDir  string `yaml:"temp_dir"`
	ClipDir  string `yaml:"clip_dir"`
	MaxSize  int64  `yaml:"max_size_mb"`
	StoreDir string `yaml:"store_dir"`
}

type WebConfig struct {
	StaticDir string `yaml:"static_dir"`
	Port      int    `yaml:"port"`
	// Host is what the dashboard binds. Loopback is the default and the
	// reason is a firewall prompt: a listener on all interfaces is the one
	// shape of socket Windows asks a question about on every build of a
	// binary whose name it does not recognise, and asking repeatedly is how
	// an operator learns to click through the prompt. The POC is a
	// single-workstation demo, so loopback is the right default; set
	// "0.0.0.0" when the dashboard has to be reachable from another machine
	// and the prompt is the honest price of that decision.
	Host string `yaml:"host"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type SiteConfig struct {
	Enabled bool   `yaml:"enabled"`
	File    string `yaml:"file"`
	// DutyLogDir is where the shift hand-over artifact is written. One
	// append-only, hash-chained JSONL file per shift; a decision is never
	// broadcast before its line is fsynced.
	DutyLogDir string `yaml:"dutylog_dir"`
	// PatrolInterval is how often a route walk is due, in the form demos
	// read: 15s, 5m, 1h. Empty or unparseable means the patrol loop's
	// default interval applies.
	PatrolInterval string `yaml:"patrol_interval"`
}

// PatrolIntervalParsed is PatrolInterval as a duration, or zero when it is
// unset or will not parse — the caller's default then applies, and an
// unparseable interval is named in the log rather than silently becoming a
// different number.
func (s *SiteConfig) Interval() time.Duration {
	if s == nil {
		return 0
	}
	return patrolIntervalFrom(s.PatrolInterval)
}

func patrolIntervalFrom(spec string) time.Duration {
	if spec == "" {
		return 0
	}
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		log.Printf("site.patrol_interval %q will not parse as a duration; the 5m default applies", spec)
		return 0
	}
	return d
}

// main is the POC's entry point: it hands the whole run to run(), which returns
// the one reason the POC could not start, and reports it here.
//
// The fatal lives in main and nowhere else on purpose. run() holds the
// signal-notify context's stop() in a defer, and a log.Fatal anywhere in that
// call path exits the process without running it -- which is precisely the leak
// gocritic's exitAfterDefer names. Reporting the error upward and fataling at
// the top of the stack is what makes the deferred stop() actually run.
func main() {
	if err := run(); err != nil {
		log.Fatalf("poc: %v", err)
	}
}

// run parses flags, loads config, enters the source selection, and runs until a
// signal arrives. The POC always has a source running: in demo policy it is the
// simulator, in a live policy it is the fleet, and if the fleet cannot start it
// is the simulator again with the reason on the status line.
func run() error {
	var (
		configPath  = flag.String("config", "poc/config.yaml", "path to the POC config file")
		youtubeURL  = flag.String("youtube-url", "", "a YouTube stream URL to run instead of the configured fleet")
		autonomyTok = flag.String("autonomy-token", "", "bearer token for the autonomy API")
		runMode     = flag.String("mode", "auto", "source policy: auto | demo | live")
		tenantID    = flag.String("tenant", "local-tenant", "tenant id for the autonomy layer")
		siteID      = flag.String("site", "local-site", "site id for the autonomy layer")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if *youtubeURL != "" {
		config.Cameras = append(config.Cameras, CameraConfig{
			ID: "cam-cli", Name: "CLI stream", YouTubeURL: *youtubeURL,
		})
		config.YouTube.URL = *youtubeURL
	}
	if *autonomyTok != "" {
		// The autonomy API is bearer-authenticated; the token is passed
		// through to the server rather than logged.
		_ = *autonomyTok
	}

	ingest := NewStreamIngest(config, *tenantID, *siteID)
	ingest.mode = *runMode
	ingest.startedAt = time.Now().UTC()
	ingest.rootCtx = ctx
	srv := NewServer(config, ingest)

	// The hand-over artifact for this run: one shift, one duty log. A failure
	// to open it is named and the POC still runs — a dead POC proves nothing
	// about the site it was watching.
	shift := time.Now().UTC().Format("2006-01-02") + "-" + *runMode
	if err := ingest.initHandover(shift); err != nil {
		log.Printf("hand-over artifact unavailable (%v); the POC runs without it", err)
	}

	// Source selection. Everything is allowed to fail by name and fall back
	// to the demo source with the reason on the status line — a missing
	// yt-dlp or a model that will not load must not produce a silent
	// dashboard.
	switch *runMode {
	case "demo":
		for _, c := range ingest.fleet.liveCameras() {
			c.fail("not started: demo policy (-mode demo)")
		}
		ingest.StartDemoSource(ctx)
	default: // auto, live
		if err := ingest.startCameras(ctx); err != nil {
			log.Printf("%s policy: %v; the demo source runs", *runMode, err)
			ingest.StartDemoSource(ctx)
		}
	}

	ingest.StartLoops(ctx)
	if err := srv.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

// NewStreamIngest builds the POC's pipeline hub from its configuration: the
// autonomy layer when a site is declared, the autonomy layer's shared
// collectors, and the camera fleet built from the site model plus the
// config's camera registry.
func NewStreamIngest(config *Config, tenantID, siteID string) *StreamIngest {
	// An empty config is a legitimate construction (the no-log and no-site
	// paths in the tests build one), and every field it reads is defaulted
	// rather than dereferenced: a zero port, a zero heartbeat, an absent
	// evidence root and a nil rules slice all yield working zero values
	// instead of a nil pointer.
	if config == nil {
		config = &Config{}
	}
	wsCfg := config.WebSocket
	if wsCfg.Port == 0 {
		wsCfg.Port = 8081
	}
	if wsCfg.Heartbeat <= 0 {
		wsCfg.Heartbeat = 30 * time.Second
	}
	wsHub := ws.NewHub(wsCfg.Port, wsCfg.Heartbeat)

	recCfg := config.Detector
	if recCfg.MaxTime <= 0 {
		recCfg.MaxTime = 30 * time.Second
	}
	recorder := mp4.NewRecorder(mp4.Options{
		PreRoll:  recCfg.PreRoll,
		PostRoll: recCfg.PostRoll,
		MaxTime:  recCfg.MaxTime,
		MaxBytes: recCfg.MaxBytes,
	})
	evidenceStore, _ := evidence.New(evidence.Options{Root: config.Evidence.StoreDir})
	categorizer := categorization.NewEngine(nil, config.AIRuntime.ProfileID, config.Categorization.ConfidenceFloor)
	spatialEngine := spatial.NewEngine(config.Spatial.Zones)
	searchIndex := search.NewIndex(config.Search.Retention, config.Search.MaxEntries)

	var siteModel *sitemodel.Site
	if config.Site != nil && config.Site.Enabled && config.Site.File != "" {
		if site, err := sitemodel.LoadFromFile(config.Site.File); err != nil {
			log.Printf("site model: %v", err)
		} else {
			siteModel = site
		}
	}

	ingest := &StreamIngest{
		config:        config,
		wsHub:         wsHub,
		recorder:      recorder,
		evidenceStore: evidenceStore,
		categorizer:   categorizer,
		spatialEngine: spatialEngine,
		zone:          newZoneWatch(spatialEngine, config.Spatial.Rules),
		searchIndex:   searchIndex,
		// Autonomy layer
		siteModel:    siteModel,
		tracker:      correlation.NewTracker(correlation.DefaultTrackerConfig()),
		linker:       correlation.NewLinker(siteID, correlation.DefaultLinkerConfig()),
		anomalyRules: anomaly.DefaultRuleSet(siteModel),
		threatScorer: threat.NewScorer(threat.DefaultScorerConfig()),
		playbook:     threat.NewPlaybook(threat.DefaultPlaybookConfig(), threat.DefaultMatrix(), threat.NewScorer(threat.DefaultScorerConfig())),
		telemetry:    telemetry.NewMetricsCollector(),
		lastSight:    newSightCache(),
		lastDecision: make(map[string]string),
		tenantID:     tenantID,
		siteID:       siteID,
	}
	ingest.buildFleet()
	if ingest.anomalyRules != nil {
		ingest.anomalyRules.Add(anomaly.NewCoverageRule(ingest.cameraDegraded))
	}
	return ingest
}

// StartLoops starts the background loops shared by every mode: the periodic
// status broadcast with its per-incident janitor and zone coverage sweep, and
// the patrol reminder clock.
func (s *StreamIngest) StartLoops(ctx context.Context) {
	go s.runMetricsLoop(ctx)
	go s.patrolLoop(ctx)
}

// LoadConfig reads the POC's YAML configuration. A file that does not parse is
// a named error, because a silently half-loaded config produces a pipeline
// that looks like it is watching when it is not.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// StreamIngest is the POC's pipeline hub: the fleet's cameras, the autonomy
// layer, the hand-over artifact and the websocket hub, all publishing through
// one fan-out.
type StreamIngest struct {
	config        *Config
	wsHub         *ws.Hub
	recorder      *mp4.Recorder
	evidenceStore *evidence.Store
	aiClient      *airuntime.Client
	categorizer   *categorization.Engine
	spatialEngine *spatial.Engine
	zone          *zoneWatch
	searchIndex   *search.Index

	// Autonomy layer: the per-camera fleet, the shared correlation tracker
	// and linker, the behaviour rules, the threat scorer and playbook, the
	// telemetry collectors and the hand-over artifact.
	siteModel    *sitemodel.Site
	tracker      *correlation.Tracker
	linker       *correlation.Linker
	anomalyRules *anomaly.RuleSet
	threatScorer *threat.Scorer
	playbook     *threat.Playbook
	telemetry    *telemetry.MetricsCollector

	// Zone and route declarations, built once from the site model.
	zoneCameras map[string][]string

	// The hand-over label and artifact. One shift per run.
	shift    string
	handover *handover

	// lastSight remembers the most recent second-sight verdict per incident
	// for API clients that are not on the websocket.
	lastSight *sightCache

	// The per-incident memories of the duty log's decision lines: which
	// action and autonomy state each incident's last recorded line carried,
	// so a line is written when the decision changes and not on every pass.
	lastDecision map[string]string

	// Site and tenant identity, required on every audit row.
	siteID   string
	tenantID string

	// The per-camera fleet: one runtime per camera, started and stopped
	// together, each degrading by name.
	fleet *cameraFleet

	// The demo pseudo-camera's lifetime: a run once, stopped when a real
	// camera takes the dashboard over.
	rootCtx     context.Context
	demoRunning atomic.Bool
	demoCancel  context.CancelFunc

	// The shared sidecar's serialized round trip: the Go client is
	// concurrency-safe but the Python server's concurrency is unverified,
	// so one lock bounds the question. At capture_fps 1 it costs nothing.
	detectMu sync.Mutex

	// passMu serializes the autonomy pass across cameras. The tracker, the
	// linker and the playbook keep per-incident state in maps, and N camera
	// loops publishing at once is exactly the concurrency that would corrupt
	// them; serialization is also what makes the shared tracker's "all
	// cameras' tracks at decision time" true rather than racy.
	passMu sync.Mutex

	// The last hour of escalation moments, written by the autonomy pass
	// under escMu. The alarm budget band reads it; a copy is what the
	// snapshot sees, so a slow client cannot stall a pass.
	escMu           sync.Mutex
	escalationTimes []time.Time
	patrolEvery     time.Duration

	// The metrics loop's clock, stamped at Start.
	startedAt time.Time
	// mode is the run policy: demo (the simulator), auto (cameras then the
	// simulator) or live (cameras only).
	mode string

	mu sync.RWMutex

	// The frame-and-status counters every source shares.
	frameCount     atomic.Int64
	triggerCount   atomic.Int64
	clipCount      atomic.Int64
	objectCount    atomic.Int64
	alertCount     atomic.Int64
	violationCount atomic.Int64
	latestObjects  atomic.Value

	// The real-path evidence counters: how many frames the cameras captured
	// and what the sidecar answered for them.
	captureCount atomic.Int64
	aiAnswered   atomic.Int64
	aiNoObjects  atomic.Int64
	aiDegraded   atomic.Int64
	aiCannotTell atomic.Int64
	aiNotAsked   atomic.Int64
	lastAIState  atomic.Value
	inferLat     *detectclient.Latencies
}

// resolveHLSURL resolves a watch URL to a playable HLS manifest through
// yt-dlp, which is the only resolver on PATH that speaks YouTube's adaptive
// streams. A missing yt-dlp is a named refusal, not a silent empty stream.
func (s *StreamIngest) resolveHLSURL(ctx context.Context, watchURL string) (string, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", fmt.Errorf("yt-dlp is not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", "-f", "best[height<=720]/best", "-g", watchURL)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp could not resolve %s: %w", watchURL, err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("yt-dlp resolved no stream for %s", watchURL)
	}
	return url, nil
}

// statusSnapshot is the one shape of "how the pipeline is doing": the metrics
// loop broadcasts it and GET /api/status returns it, so the page and the API
// can never disagree. The cameras array is the fleet's own view of itself:
// one row per camera with its named state, so a dashboard that shows a dark
// tile also shows why it is dark.
func (s *StreamIngest) statusSnapshot() map[string]any {
	now := time.Now().UTC()
	out := map[string]any{
		"mode":              s.mode,
		"source":            "cameras",
		"source_reason":     "streaming",
		"cameras":           s.fleetStatus(now),
		"frames_processed":  s.frameCount.Load(),
		"triggers":          s.triggerCount.Load(),
		"clips_created":     s.clipCount.Load(),
		"objects_total":     s.objectCount.Load(),
		"alerts_total":      s.alertCount.Load(),
		"violations_total":  s.violationCount.Load(),
		"latest_objects":    s.latestObjects.Load(),
		"index_size":        s.searchIndex.Count(),
		"connected_clients": s.wsHub.ClientCount(),
		"uptime_seconds":    time.Since(s.startedAt).Seconds(),
		"version":           pocVersion,

		// Real-path evidence: how many frames the cameras captured and what
		// the sidecar answered for them, so a run that says "streaming"
		// carries the numbers that make it a claim.
		"frames_captured": s.captureCount.Load(),
		"ai_answered":     s.aiAnswered.Load(),
		"ai_no_objects":   s.aiNoObjects.Load(),
		"ai_degraded":     s.aiDegraded.Load(),
		"ai_cannot_tell":  s.aiCannotTell.Load(),
		"ai_not_asked":    s.aiNotAsked.Load(),
	}
	if v := s.lastAIState.Load(); v != nil {
		out["last_ai_state"] = v.(string)
	}
	if lat := s.inferLat; lat != nil {
		if p50, p95, p99, n, ok := lat.Percentiles(); ok {
			out["infer_p50_ms"] = float64(p50) / float64(time.Millisecond)
			out["infer_p95_ms"] = float64(p95) / float64(time.Millisecond)
			out["infer_p99_ms"] = float64(p99) / float64(time.Millisecond)
			out["infer_samples"] = n
		}
	}
	return out
}

// fleetSourceLine is the aggregate source line: which source drives the
// dashboard and why, derived from the fleet's own counters rather than
// recorded at source-start, so a status row can never claim a source the
// fleet is not running.
func (s *StreamIngest) fleetSourceLine() (string, string) {
	now := time.Now().UTC()
	w := healthWindowsFor(s.config.Live.CaptureFPS)
	var live int
	var degraded []string
	for _, c := range s.fleet.all() {
		if c.sim {
			continue
		}
		switch c.healthFor(now, w) {
		case camFresh, camStale:
			live++
		default:
			if r := c.failReason(); r != "" {
				degraded = append(degraded, c.id+": "+r)
			} else {
				degraded = append(degraded, c.id+": "+c.healthFor(now, w))
			}
		}
	}
	if live > 0 {
		reason := "streaming"
		if len(degraded) > 0 {
			reason = fmt.Sprintf("%d of %d cameras streaming; degraded: %s", live, live+len(degraded), strings.Join(degraded, "; "))
		}
		return "cameras", reason
	}
	if s.demoRunning.Load() {
		return "sim", "no real camera is producing frames; the demo source is running"
	}
	if len(degraded) > 0 {
		return "starting", strings.Join(degraded, "; ")
	}
	return "starting", "no camera is producing frames yet"
}

// runMetricsLoop drives the periodic status broadcast and the two sweeps it
// carries: the per-incident memory janitor and the zone coverage sweep. The
// sweeps live here rather than in the autonomy pass because the pass only
// sees incidents the linker still reports as live, and a resolved incident
// never reaches it.
func (s *StreamIngest) runMetricsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			s.takeCameraHealthSamples(now)
			s.forgetTerminalIncidents()
			s.recordCoverageSweep()
			s.wsHub.Broadcast(ws.Message{
				Type:      "status",
				Data:      s.statusSnapshot(),
				Timestamp: now,
			})
		}
	}
}

// recordAIState counts one sidecar answer into the state the status snapshot
// reports, so "what the detector actually said" is a number rather than a
// claim. An absent or degraded answer is recorded as one: the count of
// answered frames goes up, and so does the count of the ones that could not
// tell, which is the figure that has to be honest for the next incident.
func (s *StreamIngest) recordAIState(res detectclient.Result) {
	switch res.State {
	case detectclient.StateAnswered:
		s.aiAnswered.Add(1)
	case detectclient.StateNoObjects:
		s.aiNoObjects.Add(1)
	case detectclient.StateDegraded:
		s.aiDegraded.Add(1)
	case detectclient.StateCannotTell:
		s.aiCannotTell.Add(1)
	case detectclient.StateNotAsked:
		s.aiNotAsked.Add(1)
	default:
		return
	}
	s.lastAIState.Store(res.State)
}
