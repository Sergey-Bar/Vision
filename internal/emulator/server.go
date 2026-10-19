package emulator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/classify"
	"github.com/Sergey-Bar/vision/internal/ortdetect"
)

// APIPrefix is the vendor's documented API prefix.
//
// It is written out here rather than imported, because this package is the
// OTHER SIDE of the wire: a VMS does not import the platform's adapter, and an
// emulator that shared a constant with the adapter would agree with it by
// construction, which is exactly the agreement the probe exists to check.
//
// The choice is pinned by a test in the ADAPTER's package that reads this
// constant and the adapter's own. A pin that lives on one side only is a
// comment; a pin that lives on both sides and fails when they disagree is a
// test.
const APIPrefix = "/api/rest/v1"

// controlPrefix is the emulator's own control surface.
//
// It is not a vendor route and it is not part of the wire the platform reads.
// It is how a console drives a VMS, and it lives outside the vendor's prefix so
// that nothing can mistake it for one: the platform polls the vendor's routes
// and never sees this.
const controlPrefix = "/__control"

// server is the emulator's HTTP surface.
type server struct {
	engine *Engine
	srv    *http.Server
	ln     net.Listener
}

// newServer builds the surface.
func newServer(e *Engine) (*server, error) {
	return &server{engine: e}, nil
}

// start binds the listener and serves.
func (s *server) start(ctx context.Context) error {
	listen := s.engine.opts.Listen
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("emulator: listen on %s: %w", listen, err)
	}
	s.ln = ln
	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = &http.Server{
		Handler:           s.withAuth(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// URL is the base URL a transport should be built with.
//
// It is the SITE ROOT, not a path that ends in the API prefix: the adapter
// appends the prefix to the base it is given, and a base that already carries
// it asks for `/api/api/rest/v1`. The route audit records this, and the
// product's default was wrong because of it.
func (s *server) URL() string { return "http://" + s.ln.Addr().String() }

// close stops the surface.
func (s *server) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}

// withAuth enforces the bearer credential.
//
// It is here rather than per route so a route added later is protected the
// moment it is registered.
func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.engine.opts.Token != "" {
			if r.Header.Get("Authorization") != "Bearer "+s.engine.opts.Token {
				w.Header().Set("WWW-Authenticate", `Bearer realm="milestone"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// routes registers every surface.
//
// The vendor's routes are registered one by one, because the probe checks
// seventeen of them and a route that answers 404 is a contradiction the probe
// reports. The control surface and the media surface are registered under
// their own prefixes.
func (s *server) routes(mux *http.ServeMux) {
	// The vendor's routes.
	mux.HandleFunc(APIPrefix+"/events", s.handleEvents)
	mux.HandleFunc(APIPrefix+"/events/", s.handleEventByID)
	mux.HandleFunc(APIPrefix+"/eventTypes", s.handleEventTypes)
	mux.HandleFunc(APIPrefix+"/Servlets", s.handleServlets)
	mux.HandleFunc(APIPrefix+"/System/Time", s.handleTime)
	mux.HandleFunc(APIPrefix+"/cameras", s.handleCameras)
	mux.HandleFunc(APIPrefix+"/cameras/", s.handleCameraByID)
	mux.HandleFunc(APIPrefix+"/Alarms", s.handleAlarms)
	mux.HandleFunc(APIPrefix+"/ResourceGroups", s.handleResourceGroups)
	mux.HandleFunc(APIPrefix+"/System/UserGroups", s.handleUserGroups)
	mux.HandleFunc(APIPrefix+"/recordingServers", s.handleRecordingServers)
	mux.HandleFunc(APIPrefix+"/rules", s.handleRules)
	mux.HandleFunc(APIPrefix+"/alarmDefinitions", s.handleAlarmDefinitions)
	mux.HandleFunc(APIPrefix+"/bookmarks", s.handleBookmarks)
	// The write routes the configurator drives. A scripted acknowledgement, the
	// same shape the adapter's own live server uses.
	for _, path := range []string{"/cameras/", "/recordingServers/", "/rules/"} {
		mux.HandleFunc("PUT "+APIPrefix+path, s.handleWrite)
	}
	mux.HandleFunc("POST "+APIPrefix+"/rules", s.handleWrite)

	// The media surface.
	mux.HandleFunc("/media/clip", s.handleMediaClip)
	mux.HandleFunc("/media/snapshot", s.handleMediaSnapshot)
	mux.HandleFunc("/media/probe", s.handleMediaProbe)

	// The control surface.
	mux.HandleFunc(controlPrefix+"/cameras", s.handleControlCameras)
	mux.HandleFunc(controlPrefix+"/cameras/", s.handleControlCamera)
	mux.HandleFunc(controlPrefix+"/state", s.handleControlState)

	mux.HandleFunc("/", s.handleUnknown)
}

// handleUnknown answers the way a device answers a path it does not have.
func (s *server) handleUnknown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": fmt.Sprintf("no such route: %s %s", r.Method, r.URL.Path),
	})
}

// writeJSON writes a JSON body.
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// handleEvents serves the analytics feed.
//
// The filter is the vendor's own dialect: `?time=gt:'<RFC3339>'`. It is
// parsed rather than ignored, because a feed that ignores the filter returns
// the same page every poll and the adapter's pagination never advances.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	since := time.Time{}
	if raw := r.URL.Query().Get("time"); raw != "" {
		parsed, err := parseVendorTime(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		since = parsed
	}
	limit := 200
	if raw := r.URL.Query().Get("size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 2000 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "size must be between 1 and 2000"})
			return
		}
		limit = n
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "page must be a positive integer"})
			return
		}
		page = n
	}
	events := s.engine.eventsSince(since)
	items := make([]map[string]any, 0, len(events))
	start := (page - 1) * limit
	if start > len(events) {
		start = len(events)
	}
	end := start + limit
	if end > len(events) {
		end = len(events)
	}
	for _, ev := range events[start:end] {
		items = append(items, ev.envelope())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"array":      items,
		"eventCount": len(events),
		"_links": map[string]any{
			"self": map[string]any{"href": r.URL.String()},
		},
	})
}

// parseVendorTime reads the vendor's `time=gt:'...'` filter.
//
// The dialect is `property=operator:value` with the value in single quotes, and
// only `gt` and `lt` are defined for `time`. A filter this emulator cannot
// read is refused by name rather than ignored: a feed that silently ignores a
// filter returns everything, and the adapter then re-reads the whole window
// every poll.
func parseVendorTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	for _, op := range []string{"gt:", "lt:"} {
		if strings.HasPrefix(value, op) {
			value = strings.TrimSpace(strings.TrimPrefix(value, op))
			value = strings.Trim(value, "'")
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return time.Time{}, fmt.Errorf("time=%q is not RFC3339", raw)
			}
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("time=%q is not a filter this emulator understands; the documented "+
		"dialect is time=gt:'<RFC3339>'", raw)
}

// handleEventByID serves one event.
//
// The literal placeholder `{id}` means "one of these". The probe asks for a
// route with its placeholder unsubstituted, because the probe cannot know an id
// the device has not shown it, and a device that answered 404 would be
// reporting a fact about the probe's request rather than about itself. So the
// placeholder is answered with the most recent event, and a request for a real
// id the emulator does not have is still a 404, because that IS a fact about
// the inventory.
func (s *server) handleEventByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, APIPrefix+"/events/")
	if id == "" {
		s.handleEvents(w, r)
		return
	}
	events := s.engine.eventsSince(time.Time{})
	if id == placeholderID {
		if len(events) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "this VMS has published no events yet"})
			return
		}
		writeJSON(w, http.StatusOK, events[len(events)-1].envelope())
		return
	}
	for _, ev := range events {
		if ev.ID == id {
			writeJSON(w, http.StatusOK, ev.envelope())
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such event: " + id})
}

// placeholderID is the unsubstituted `{id}` the probe sends.
const placeholderID = "{id}"

// handleEventTypes serves the analytics types the VMS publishes.
func (s *server) handleEventTypes(w http.ResponseWriter, r *http.Request) {
	types := []map[string]any{
		{"id": "emu-object-detected", "name": "Object Detected", "generatorType": "MIPDevice"},
		{"id": "emu-person-count", "name": "Person Count", "generatorType": "MIPDevice"},
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": types})
}

// handleServlets serves the camera inventory.
//
// "Servlets" is the vendor's name for the devices it serves. The console's
// camera list comes from here rather than from a mock, which is the point: the
// list of cameras is the VMS's fact.
func (s *server) handleServlets(w http.ResponseWriter, r *http.Request) {
	s.engine.mu.RLock()
	ids := make([]string, 0, len(s.engine.cameras))
	for id := range s.engine.cameras {
		ids = append(ids, id)
	}
	s.engine.mu.RUnlock()
	// A stable order, because a camera list that reorders between two polls
	// reads as cameras appearing and disappearing.
	sortStrings(ids)
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		st, ok := s.engine.state(id)
		if !ok {
			continue
		}
		items = append(items, map[string]any{
			"Id":      st.ID,
			"Name":    st.Name,
			"Type":    "Camera",
			"Enabled": true,
			"State":   "Online",
			"Uri":     fmt.Sprintf("%s/cameras/%s", s.URL(), st.ID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleCameras serves the camera list.
func (s *server) handleCameras(w http.ResponseWriter, r *http.Request) {
	s.handleServlets(w, r)
}

// handleCameraByID serves one camera.
//
// The literal placeholder `{id}` means "one of these", for the reason
// [handleEventByID] gives: the probe cannot name an id the device has not shown
// it. A real id the emulator does not have is a 404, because that is a fact
// about the inventory.
func (s *server) handleCameraByID(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		s.handleWrite(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, APIPrefix+"/cameras/")
	st, ok := s.engine.state(id)
	if !ok && id == placeholderID {
		s.engine.mu.RLock()
		ids := make([]string, 0, len(s.engine.cameras))
		for cameraID := range s.engine.cameras {
			ids = append(ids, cameraID)
		}
		s.engine.mu.RUnlock()
		sortStrings(ids)
		if len(ids) > 0 {
			st, ok = s.engine.state(ids[0])
		}
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera: " + id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": st.ID, "Name": st.Name, "Type": "Camera", "Enabled": true, "State": "Online",
	})
}

// handleTime serves the vendor's clock.
func (s *server) handleTime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"dateTime": s.engine.opts.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}

// handleEmptyArray answers a route the VMS has and nothing in it.
//
// An empty `array` is a CONTRADICTION for a route the vendor declares as an
// array, and it is the honest answer for a device that has the route and
// nothing to put in it.
//
// The routes below are NOT empty, though, because the emulator really does have
// these things: it IS a recording server, it DOES have analytics rules, and it
// DOES raise alarms. A VMS that answered an empty list for its own recording
// servers would be claiming it holds no recordings while serving clips out of
// them.
func (s *server) handleEmptyArray(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"array": []any{}, "eventCount": 0})
}

// handleRecordingServers serves the recording servers.
//
// The emulator is one. It holds the source files and it cuts clips out of them,
// and a device that served an empty list here would be denying the thing it
// does every time the platform asks for a clip.
func (s *server) handleRecordingServers(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{{
		"Id":          "emulator-recording-server",
		"Name":        "vms-emulator",
		"Type":        "RecordingServer",
		"State":       "Online",
		"Enabled":     true,
		"Description": "The VMS emulator's own store: the source files it plays and the clips it cuts.",
	}}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleRules serves the VMS's own analytics rules.
//
// They are the classes this VMS asserts and the rule that fires for each one.
// The platform never reads this route to decide what an incident is — its own
// bundle does that — but a VMS with no analytics rules is a VMS whose analytics
// come from nowhere, and the route is where a reader would look.
func (s *server) handleRules(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, len(emulatorRuleNames))
	for _, name := range emulatorRuleNames {
		items = append(items, map[string]any{
			"Id":          "emulator-rule-" + name,
			"Name":        name,
			"Type":        "Analytics",
			"Enabled":     true,
			"Description": "The VMS asserts this class; the rule that turns it into an incident is the platform's.",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleAlarmDefinitions serves the alarm types the VMS can raise.
func (s *server) handleAlarmDefinitions(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, len(alarmClassNames))
	for _, name := range alarmClassNames {
		items = append(items, map[string]any{
			"Id":       "emulator-alarm-" + name,
			"Name":     name,
			"Type":     "Analytics",
			"Enabled":  true,
			"Category": "ObjectDetected",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleAlarms serves the alarms the VMS has raised.
//
// An alarm here is the VMS's own assertion that something worth an operator's
// attention happened: a person with a bag, or a vehicle. The platform turns
// those into incidents with its own rules; this list is the vendor's side of
// the same fact.
func (s *server) handleAlarms(w http.ResponseWriter, r *http.Request) {
	events := s.engine.eventsSince(time.Time{})
	items := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		if !alarmClasses[ev.ClassName] {
			continue
		}
		items = append(items, map[string]any{
			"Id":          "emulator-alarm-" + ev.ID,
			"Type":        ev.ClassName,
			"Timestamp":   ev.At.UTC().Format(time.RFC3339),
			"DevicePath":  map[string]any{"type": "cameras", "id": ev.DeviceID},
			"Description": ev.ClassName + " asserted by " + ev.Vendor,
		})
		if len(items) >= 200 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleResourceGroups serves the camera groups.
//
// One group, holding every camera, because that is how the emulator is
// arranged: a site is a set of cameras and the emulator plays them all.
func (s *server) handleResourceGroups(w http.ResponseWriter, r *http.Request) {
	s.engine.mu.RLock()
	ids := make([]string, 0, len(s.engine.cameras))
	for id := range s.engine.cameras {
		ids = append(ids, id)
	}
	s.engine.mu.RUnlock()
	sortStrings(ids)
	members := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		members = append(members, map[string]any{"type": "cameras", "id": id})
	}
	items := []map[string]any{{
		"Id":      "emulator-all-cameras",
		"Name":    "All cameras",
		"Type":    "ResourceGroup",
		"Enabled": true,
		"Members": members,
	}}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleUserGroups serves the VMS's user groups.
//
// The emulator has one group and it grants everything, because the emulator has
// no user model: it is a device on a loopback socket with a bearer credential,
// and the credential is the whole of its access control. A group that claimed
// to restrict anything would be a fiction.
func (s *server) handleUserGroups(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{{
		"Id":      "emulator-administrators",
		"Name":    "Administrators",
		"Enabled": true,
	}}
	writeJSON(w, http.StatusOK, map[string]any{"array": items, "eventCount": len(items)})
}

// handleWrite acknowledges a configurator write.
func (s *server) handleWrite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"path": r.URL.Path, "method": r.Method, "accepted": true,
	})
}

// handleBookmarks serves the recording bookmarks.
//
// A bookmark is the vendor's record of "there is a recording over this window".
// The emulator answers it with the window it was asked about, because the
// emulator holds the source file for the whole run: the recording exists.
func (s *server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		var doc struct {
			Header        string `json:"header"`
			TimeTriggered string `json:"timeTriggered"`
			TimeBegin     string `json:"timeBegin"`
			TimeEnd       string `json:"timeEnd"`
			DevicePath    struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"devicePath"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body is not the documented shape"})
			return
		}
		reference := "emu-bookmark-" + sanitiseName(doc.DevicePath.ID)
		writeJSON(w, http.StatusOK, map[string]any{"array": []map[string]any{{
			"reference": reference, "header": doc.Header, "replayed": false,
		}}})
		return
	}
	reference := r.URL.Query().Get("reference")
	out := []map[string]any{}
	if reference != "" {
		out = append(out, map[string]any{"reference": reference})
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": out, "eventCount": len(out)})
}

// handleControlCameras lists the cameras for the console.
func (s *server) handleControlCameras(w http.ResponseWriter, r *http.Request) {
	s.engine.mu.RLock()
	ids := make([]string, 0, len(s.engine.cameras))
	for id := range s.engine.cameras {
		ids = append(ids, id)
	}
	s.engine.mu.RUnlock()
	sortStrings(ids)
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if st, ok := s.engine.state(id); ok {
			items = append(items, describeState(st))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"array": items})
}

// handleControlCamera serves one camera's live state, frame and detections.
//
// The sub-resource is named in the path: `/__control/cameras/{id}/frame.png`
// is the picture, `/__control/cameras/{id}/detections` is the boxes, and
// `/__control/cameras/{id}` is everything.
func (s *server) handleControlCamera(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, controlPrefix+"/cameras/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	what := ""
	if len(parts) > 1 {
		what = parts[1]
	}
	st, ok := s.engine.state(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera: " + id})
		return
	}
	switch what {
	case "frame.png":
		s.serveFrame(w, id)
	case "detections":
		out := make([]map[string]any, 0, len(st.Detections))
		for _, d := range st.Detections {
			out = append(out, describeDetection(d))
		}
		writeJSON(w, http.StatusOK, map[string]any{"array": out})
	case "":
		writeJSON(w, http.StatusOK, describeState(st))
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera resource: " + what})
	}
}

// serveFrame writes the camera's latest picture as a PNG.
//
// PNG rather than JPEG because the standard library writes it and the standard
// library has no JPEG writer, and because a lossless picture is the honest
// thing to draw boxes on.
func (s *server) serveFrame(w http.ResponseWriter, id string) {
	pix, width, height, ok := s.engine.frame(id)
	if !ok || len(pix) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "this camera has not produced a frame yet",
		})
		return
	}
	img, err := rgbToPNG(pix, width, height)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img)
}

// handleControlState serves the engine's own state: the cameras, the detector's
// identity, and what was measured.
func (s *server) handleControlState(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"vendor": s.engine.opts.Vendor,
		"now":    s.engine.opts.Now().UTC().Format(time.RFC3339Nano),
	}
	if s.engine.opts.Detector != nil {
		id := s.engine.opts.Detector.Identity()
		classes, declared := 0, false
		if counter, ok := s.engine.opts.Detector.(ClassCounter); ok {
			classes, declared = counter.ClassCount()
		}
		body["detector"] = map[string]any{
			"modelPath":       id.ModelPath,
			"sha256":          id.SHA256,
			"bytes":           id.Bytes,
			"licence":         id.Licence,
			"provider":        id.ExecutionProvider,
			"cudaDeviceID":    id.CUDADeviceID,
			"runtimeVersion":  id.RuntimeVersion,
			"classes":         classes,
			"classesDeclared": declared,
		}
		// The vocabulary: where the class names came from, the prompt that
		// produced them if a prompt produced them, and the families the
		// PRODUCT groups them into.
		//
		// It is reported because a detection whose class list cannot be named
		// cannot be checked: a reader who sees "class 7" has nothing to look up.
		body["vocabulary"] = describeVocabulary(s.engine.vocabulary)
	} else {
		body["detector"] = nil
	}
	writeJSON(w, http.StatusOK, body)
}

// describeVocabulary renders the class list for the control surface.
//
// It names the SOURCE the class names came from and the PROMPT that produced
// them, because a detection whose class list cannot be named cannot be
// checked: a reader who sees "class 7" has nothing to look up.
//
// The open classes are listed because they are the ones a PROMPT can add, and
// knowing which ones the taxonomy knows is the difference between "the model
// cannot say that" and "the model did not say that".
func describeVocabulary(v *ortdetect.Vocabulary) map[string]any {
	if v == nil {
		return nil
	}
	families := map[string]string{}
	for _, name := range v.Names {
		if family := v.FamilyOf(name); family != "" {
			families[name] = family
		}
	}
	return map[string]any{
		"source":      v.Source,
		"prompt":      v.Prompt,
		"size":        v.Size(),
		"families":    families,
		"openClasses": classify.OpenClasses,
	}
}

// describeState renders one camera for the control surface.
func describeState(st cameraState) map[string]any {
	out := map[string]any{
		"id":         st.ID,
		"name":       st.Name,
		"kind":       string(st.Kind),
		"location":   st.Location,
		"fps":        st.FPS,
		"frameIndex": st.FrameIndex,
		"frameCount": st.FrameCount,
		"measured":   st.Measured,
		"latencyMs":  st.LatencyMS,
		"paused":     st.Paused,
	}
	if st.Refusal != "" {
		out["refusal"] = st.Refusal
	}
	dets := make([]map[string]any, 0, len(st.Detections))
	for _, d := range st.Detections {
		dets = append(dets, describeDetection(d))
	}
	out["detections"] = dets
	return out
}

// sortStrings sorts in place. A tiny helper so the ordering rule is written
// once.
func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// clipDirFor is where the media surface writes.
func (s *server) clipDirFor() string { return s.engine.clip }

// ensureDir makes a directory exist.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o750)
}

// pathJoin is filepath.Join, named so the media handlers read as one line.
func pathJoin(parts ...string) string { return filepath.Join(parts...) }
