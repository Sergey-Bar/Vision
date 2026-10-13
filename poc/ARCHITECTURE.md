# YouTube Live Stream Analytics POC - Architecture Specification

## Overview

This POC demonstrates a Minimum Viable Product (MVP) that integrates YouTube live streams with the existing VISION analytics engine. The system provides real-time video playback, live categorization, spatial analysis, and smart search capabilities.

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           YouTube Live Stream POC                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐    ┌─────────────────┐    ┌──────────────────────────┐   │
│  │   YouTube    │───▶│  Stream Ingest  │───▶│  Frame Extractor &       │   │
│  │   Live HLS   │    │  Service (Go)   │    │  Detector (CodedSize)    │   │
│  └──────────────┘    └────────┬────────┘    └───────────┬──────────────┘   │
│                               │                         │                   │
│                               ▼                         ▼                   │
│                    ┌─────────────────────┐   ┌───────────────────────┐      │
│                    │  Clip Recorder &    │   │  AI Runtime Client    │      │
│                    │  Evidence Store     │   │  (REST to Python)     │      │
│                    └──────────┬──────────┘   └───────────┬───────────┘      │
│                               │                         │                   │
│                               ▼                         ▼                   │
│                    ┌─────────────────────────────────────────────────┐     │
│                    │           Rules Engine (Deterministic)          │     │
│                    │  • Corroboration    • Verification States       │     │
│                    │  • Degradation      • Dispatch Actions          │     │
│                    └────────────────────┬────────────────────────────┘     │
│                                         │                                   │
│                    ┌────────────────────┴────────────────────────────┐     │
│                    │              WebSocket Hub                      │     │
│                    │  • Real-time analytics events                   │     │
│                    │  • Live categorization updates                  │     │
│                    │  • Spatial analysis alerts                      │     │
│                    │  • Search index updates                         │     │
│                    └────────────────────┬────────────────────────────┘     │
│                                         │                                   │
│                    ┌────────────────────┴────────────────────────────┐     │
│                    │              Web Frontend (React/Vanilla JS)    │     │
│                    │  • YouTube Player (iframe API)                  │     │
│                    │  • Real-time Dashboard                          │     │
│                    │  • Live Categorization Panel                    │     │
│                    │  • Spatial Analysis Overlay                     │     │
│                    │  • Smart Search Interface                       │     │
│                    └─────────────────────────────────────────────────┘     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Components

### 1. Single POC Server (`poc/stream_ingest.go` + `poc/server.go`)
- One binary serves the dashboard (`poc/static/index.html`), the JSON API and
  the WebSocket hub from a single origin (`web.port`, default `:8082`)
- `-mode auto|demo|live`: in any non-demo mode the **camera fleet**
  (`poc/cameras.go`) runs — one `yt-dlp`+`ffmpeg` pipe and one detection loop
  per camera in the `cameras:` list, over one shared sidecar client. `demo`
  (and any moment no real camera is producing frames) runs the simulator as
  the fleet's pseudo-camera `sim`, so the dashboard is never empty
- A camera that cannot resolve, capture or decode is recorded with a **named
  reason** on that camera and the rest of the fleet keeps running; the demo
  source only takes over when the *last* camera goes dark
- Frames from every camera pass through the shared `publishFrame` fan-out, so
  the autonomy layer sees all cameras' tracks through one correlation tracker

### 2. Real-Time Analytics WebSocket (`poc/ws/hub.go`)
- Registered on the main server's mux via `Hub.Attach(ctx, mux)` — no second port
- Event types:
  - `detection`: per-frame objects (category, sub-category, label, confidence, bbox)
  - `spatial`: zone entry / dwell alerts
  - `violation`: predefined no-zone rule violations
  - `status`: counters (frames, objects, alerts, violations, clients, uptime)
  - `search_index`: new frames added to the live index
  - `stream`: ingested stream metadata
  - `categorization` / `incident`: clip-time results in live mode

### 3. Live Categorization Engine (`poc/categorization/engine.go`)
- Integrates with AI Runtime for VLM-based classification of captured clips
- Categories:
  - **Vehicles**: car types (sedan, SUV, truck, motorcycle), colors
  - **Biological Entities**: Humans (adult/child), animals (dog, cat, bird)
  - **Other**: General objects (bags, packages, furniture)
- Uses profile schemas from `internal/observations/profiles.go`;
  degraded observations are recorded when the AI runtime is unavailable

### 4. Spatial Analysis (`poc/spatial/engine.go` + `poc/pipeline.go`)
- **No-Zone Detection**: configurable polygonal restricted areas
- **Rule Violation Alerts** from `spatial.rules` in `poc/config.yaml`
  (`no-person-in-restricted-1`, `no-vehicle-in-restricted-2`):
  - Entry into restricted zones (once per crossing, re-arms on exit)
  - Dwelling time violations (once per threshold, re-arms on exit)
  - Cooldown per rule + object so a demo does not flood the feed

### 5. Live Smart Search (`poc/search/index.go`)
- Real-time in-memory index; a frame's content is indexed every 5 s while the
  stream runs, and the stream's own title/author is indexed too
- Queries are token-AND across label, category, sub-category and zone names:
  "red car" matches a *red* label in an entry that also has a *car*
- Served by `POST /api/search`, re-run live in the UI as the index grows

### 6. Web Frontend (`poc/static/index.html`)
- **Camera Wall**: one tile per camera the fleet reports (`cameras` in
  `GET /api/status`), each with its own YouTube player, its own overlay canvas
  and its own health badge — `fresh`, `stale`, `silent` (no frames) or
  `blind` (the frame bytes say the camera cannot see: near-uniform, or the
  whole scene changed at once). A tile carries its named degradation reason;
  nothing about a camera's state is inferred in the browser
- **Player + metadata**: YouTube IFrame API per tile; the id is resolved and
  its metadata ingested by `POST /api/stream`, which registers the stream as
  the fleet's `manual` camera rather than taking over the process
- **Analytics Dashboard**: live counters (frames, objects per category, zone
  alerts, rule violations, WS clients) and the active rule list
- **Categorization Panel**: newest-first detection feed with category chips,
  each row naming the camera that saw the object
- **Spatial Overlay**: per-tile canvas drawing zone polygons and that
  camera's frame's bounding boxes in the 1280×720 frame coordinate space
- **Smart Search**: debounced query over the live index plus a "live matches
  now" count that highlights matching boxes on every tile's overlay

## Data Flow (demo mode — the verified vertical slice)

1. **Stream Start**: the UI POSTs the YouTube URL → the server normalizes the
   video id and ingests oEmbed metadata → the IFrame player loads the live stream
2. **Frame Pipeline**: the demo source advances a 1280×720 scene at 2 fps →
   objects (vehicles with type/color, humans, animals, other) move with
   lifetimes so the cast churns
3. **Spatial**: each frame goes through `spatial.Engine.Analyze` → entry/dwell
   alerts with transition tracking; `spatial.Engine.CheckRuleViolation` evaluates
   the configured no-zone rules with a per-rule/object cooldown
4. **Search**: every 10th frame's object set is indexed; the stream title is
   indexed when it is loaded
5. **Broadcast**: `detection` / `spatial` / `violation` / `status` /
   `search_index` → WebSocket → dashboard renders boxes, feeds and counters
6. **Query**: the search box debounces into `POST /api/search`; incoming
   `search_index` messages re-run the active query every 2 s

## Data Flow (live mode — the camera fleet, needs `yt-dlp` + `ffmpeg` + AI runtime)

1. **Fleet start**: `startCameras` ensures one sidecar (`ensureSidecar`), then
   starts one loop per configured camera (`poc/cameras.go`)
2. **Stream Start (per camera)**: the camera's `youtube_url` → `yt-dlp -g`
   resolves HLS → `ffmpeg` pipes h264. A failure here is recorded on that
   camera as its named reason and nothing else stops
3. **Frame Processing**: each NAL → `detector.CodedSize` → novelty score
   (`detection`); the frame bytes also feed a per-frame digest that decides
   the camera's `blind` verdict
4. **Detections**: sidecar boxes → categorised frame objects → the camera's
   own local tracker → published with that camera's id, so every detection
   carries the camera that saw it
5. **AI Inference / Rules / Broadcast**: as above, through the shared fan-out

## Integration Points

| Existing Component | POC Integration |
|-------------------|-----------------|
| `detector.CodedSize` | Frame novelty scoring (no decoder needed) |
| `internal/mp4.Recorder` | Clip assembly with pre/post roll |
| `internal/evidence.Store` | Clip storage by content hash |
| `internal/observations` | Degraded observation recording |
| `internal/rules.Engine` | Deterministic verification |
| `internal/airuntime.Client` | VLM inference for categorization |
| `internal/simulate` | Bundle testing against fixtures |

## Configuration

```yaml
# poc/config.yaml
youtube:
  url: ""  # Set at runtime
  quality: "720p"

# The camera fleet: one entry per camera. Which zones a camera covers is
# declared in the site model (site.file), not here.
cameras:
  - id: "cam-01"
    name: "City Square"
    youtube_url: "https://www.youtube.com/watch?v=…"
  - id: "cam-02"
    name: "Harbour Road"
    youtube_url: "https://www.youtube.com/watch?v=…"

detector:
  threshold: 0.5
  pre_roll: 5s
  post_roll: 5s
  max_clip_time: 30s
  
categorization:
  enabled: true
  profile: "general-monitoring"  # or restricted-person
  confidence_threshold: 0.6
  
spatial:
  zones:
    - id: "restricted-1"
      name: "Restricted Area 1"
      polygon: [[100,100], [500,100], [500,400], [100,400]]
      type: "no-entry"
    - id: "entrance"
      name: "Main Entrance"
      polygon: [[200,300], [400,300], [400,400], [200,400]]
      type: "monitoring"
      
search:
  index_retention: 24h
  max_results: 50
  
websocket:
  port: 8081
  heartbeat: 30s
  
ai_runtime:
  endpoint: "http://127.0.0.1:8800"
  timeout: 10s
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | The dashboard (`poc/static/index.html`) |
| `/api/healthz` | GET | Liveness |
| `/api/status` | GET | Counters: mode, frames, objects, alerts, violations, index size, and the camera fleet's own rows (state, frames, named reason) |
| `/api/zones` | GET | Zone polygons + configured rules + frame geometry |
| `/api/search` | POST | Smart search query `{"text":"red car","limit":25}` |
| `/api/stream` | GET/POST | Current stream metadata / ingest metadata for a URL |
| `/api/incidents/active` | GET | Active correlated incidents |
| `/api/incidents/{id}` | GET | One incident from stored facts only, with its second-sight verdict and its own duty-log lines |
| `/api/telemetry/live` | GET | The measures: HAM/1000, time-to-decision, correlation hit, zone context, override rate, false-escalation rate, camera honesty |
| `/api/briefs/latest` | GET | The shift hand-over: the rendered brief, in the same bytes the ws carries |
| `/api/dutylog` | GET | The artifact's own state: path, chain head, verification, lines and dump |
| `/api/site-config` | GET | The configurator's input page: the artifact this fleet generates, its digest, assumed entries, learned proposals, derived rules |
| `/api/investigate` | POST | One question over the pipeline's stored facts, answered with citations — or with the named reason the plane could not answer |
| `/api/patrol` | GET/POST | The site's routes and their cameras / record that an operator walked one |
| `/ws` | WS | Real-time analytics WebSocket |

## WebSocket Message Format

```json
{
  "type": "detection|spatial|violation|status|search_index|stream|categorization|incident|correlated|threat|autonomy|brief|patrol",
  "timestamp": "2026-10-06T10:00:00Z",
  "data": { ... }
}
```

The `detection`, `spatial` and `violation` messages carry a `camera_id`, so a
detection is always attributable to the camera that saw it; `autonomy` carries
the incident's `second_sight` verdict; `patrol` carries `kind: "due"` or `"checked"`;
`brief` carries the whole rendered brief.

## Deployment

Run it through the script runner. There is no binary to build and nothing to
install into the checkout: `go run` compiles into the toolchain's own cache and
runs the result, so what an operator keeps is source.

```bash
# Backend + frontend, one script (Linux/macOS). -mode demo|auto|live.
./scripts/run-poc.sh              # defaults to demo policy
./scripts/run-poc.sh -smoke       # asks one question, then stops the backend

# Windows
.\scripts\run-poc.ps1             # add -Mode auto -Open to open the dashboard
.\scripts\run-poc.ps1 -Smoke

# Then open the URL the script prints:
#   http://localhost:8082/        the dashboard: camera wall, alerts, search
#   http://localhost:8082/api/status   one row per camera, with its named state
```

Do **not** `go build -o poc.exe ./poc` in this tree. Two reasons, and the first
is the one that bites on a Windows development machine: a compiled exe dropped
in the checkout is a file Windows may block or quarantine, and the whole
point of the script form is that there is no such artefact. The second is that
a binary in the tree is a binary nobody rebuilds. `go vet ./...` and
`go test ./...` are the compile checks and neither writes a binary into the
checkout; `scripts/verify.ps1` is the gate and runs no `go build` of its own.

The equivalent, if you would rather not use a script:

```bash
go run ./poc --config poc/config.yaml --mode demo
```

**It binds loopback by default** (`web.host`, default `127.0.0.1`). That is
deliberate and it is a firewall matter: a listener bound to all interfaces is
the shape of socket Windows asks about for every newly built executable, and
an operator who sees the prompt for every rebuild learns to click through it —
which is how the prompt stops meaning anything. The dashboard is a
single-workstation demo, so loopback is the right default. Set
`web.host: "0.0.0.0"` when it has to be reachable from another machine, and
treat the prompt as the honest price of that decision rather than as a
nuisance.

The runner waits for `/api/healthz` rather than sleeping, refuses to start when
the port is already serving (a leftover server would make this run's output
another process'), and stops the compiled server behind `go run` as well as the
wrapper. Both scripts print the endpoints an operator needs next: the
hand-over artifact, the brief, the configurator's view, and the investigator.

The same script drives the demo: `scripts/demo.sh` starts the backend this way
for its fleet steps, so the transcript comes from the build on disk rather than
from an artifact somebody left behind.

```bash
# The equivalent, if you would rather not use a script:
go run ./poc --config poc/config.yaml --mode demo
```

Optional, for clip-time categorization in live mode:
`cd deploy/ai-runtime && python runtime.py`

## Success Criteria

1. ✅ YouTube live stream loads and plays in browser (verified: NYC 24/7 cam,
   Lofi Girl's stream blocks embedding — error 150 is surfaced in the UI)
2. ✅ Per-frame detections displayed live (category, sub-category, label,
   confidence, bbox) — demo source; live source scores novelty instead
3. ⚠️ Clips recorded and stored on trigger — live path only, not exercised here
4. ⚠️ AI Runtime called for categorization (degraded if no model) — live path
5. ⚠️ Rules engine evaluates and produces verification state — live path;
   the demo exercises the spatial rule set instead
6. ✅ Live categorization updates in dashboard
7. ✅ No-zone violations detected and alerted (entry, dwell, rule violations)
8. ✅ Smart search returns live results (token-AND over the growing index)
9. ✅ All events flow through WebSocket to frontend
10. ✅ Degraded observations properly recorded when AI unavailable (live path)