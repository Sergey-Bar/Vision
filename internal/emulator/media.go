package emulator

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The media surface is the vendor's recording.
//
// It is what makes INSTANT REPLAY work: the platform asks the vendor "what do
// you hold on this camera between these two instants", the vendor answers with
// a clip or with a gap, and the platform stores what it was given in its own
// evidence store. Without it every incident would have a bookmark and no
// picture, which is the shape of a system that records where something happened
// and never what happened.
//
// The surface is HTTP and it is the emulator's own, not the vendor's: the
// vendor's media API is not in the documented route table, so an emulator that
// pretended to serve it would be claiming a route it has no evidence for.

// MediaRange is what a probe found.
type MediaRange struct {
	CameraRef string
	Available []MediaInterval
	Source    string
}

// MediaInterval is one continuous span of recording.
type MediaInterval struct {
	From time.Time
	To   time.Time
}

// handleMediaProbe answers what the VMS holds over a window.
//
// It answers in the same shape the platform's own media reader expects, so the
// emulator can stand in for a vendor at the seam without the platform knowing
// that the shape came from the same author.
func (s *server) handleMediaProbe(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera")
	if cameraID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "camera is required"})
		return
	}
	cam, ok := s.engine.camera(cameraID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera: " + cameraID})
		return
	}
	from, to, err := windowOf(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	available := []MediaInterval{}
	if cam.source.Kind == KindFile {
		// A file source holds the whole file, so the window is the file's own
		// extent: from the moment playback started to now.
		available = append(available, MediaInterval{From: cam.startedAt, To: s.engine.opts.Now()})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cameraRef": cameraID,
		"available": available,
		"source":    "emulator",
		"asked":     map[string]any{"from": from, "to": to},
	})
}

// handleMediaClip cuts a clip and serves it.
func (s *server) handleMediaClip(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera")
	if cameraID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "camera is required"})
		return
	}
	cam, ok := s.engine.camera(cameraID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera: " + cameraID})
		return
	}
	if cam.source.Kind != KindFile {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "this camera's video is not seekable, so there is no recording to cut: " +
				"the emulator holds a file's worth of recording and nothing else",
		})
		return
	}
	from, to, err := windowOf(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out := filepath.Join(s.clipDirFor(), fmt.Sprintf("clip-%s-%d.mp4",
		sanitiseName(cameraID), from.UTC().UnixNano()))
	if err := cutClip(r.Context(), s.engine.opts.FFmpeg, clipRequest{
		Path:  cam.source.Location,
		Start: positionOf(cam, from, s.engine.opts.Now()),
		End:   positionOf(cam, to, s.engine.opts.Now()),
		Out:   out,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	serveFile(w, out, "video/mp4")
}

// handleMediaSnapshot writes one still and serves it.
func (s *server) handleMediaSnapshot(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera")
	if cameraID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "camera is required"})
		return
	}
	cam, ok := s.engine.camera(cameraID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such camera: " + cameraID})
		return
	}
	if cam.source.Kind != KindFile {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "this camera's video is not seekable, so there is no still to take",
		})
		return
	}
	at := s.engine.opts.Now()
	if raw := r.URL.Query().Get("at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "at must be RFC3339"})
			return
		}
		at = parsed
	}
	out := filepath.Join(s.clipDirFor(), fmt.Sprintf("snap-%s-%d.jpg",
		sanitiseName(cameraID), at.UTC().UnixNano()))
	if err := snapshotAt(r.Context(), s.engine.opts.FFmpeg, cam.source.Location, positionOf(cam, at, s.engine.opts.Now()), out); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	serveFile(w, out, "image/jpeg")
}

// windowOf reads the from/to window out of the query.
func windowOf(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-30 * time.Second)
	to := now
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be RFC3339")
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be RFC3339")
		}
		to = parsed
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("the window ends before it begins")
	}
	return from, to, nil
}

// positionOf maps an instant onto a position in the source file.
//
// The file's own clock starts when playback started, so the position of an
// instant is how long after that it was. It is approximate — the emulator knows
// when it started playing, not where in the file it was — and the
// approximation is named rather than hidden: a clip that is a few seconds out
// is a clip that is a few seconds out.
func positionOf(cam *camera, at time.Time, now time.Time) time.Duration {
	if at.Before(cam.startedAt) {
		return 0
	}
	offset := at.Sub(cam.startedAt)
	elapsed := now.Sub(cam.startedAt)
	if offset > elapsed {
		return elapsed
	}
	return offset
}

// serveFile writes a file with its content type.
func serveFile(w http.ResponseWriter, path, contentType string) {
	info, err := os.Stat(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such recording"})
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = copyTo(w, f)
}

// rgbToPNG encodes RGB24 pixels as a PNG.
func rgbToPNG(pix []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("the frame has no size")
	}
	if len(pix) < width*height*3 {
		return nil, fmt.Errorf("the frame carries %d bytes, and %dx%d RGB needs %d",
			len(pix), width, height, width*height*3)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			src := (y*width + x) * 3
			dst := img.PixOffset(x, y)
			img.Pix[dst] = pix[src]
			img.Pix[dst+1] = pix[src+1]
			img.Pix[dst+2] = pix[src+2]
			img.Pix[dst+3] = 0xff
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("emulator: encode a frame as PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// copyTo copies bytes, bounded by the caller's reader.
func copyTo(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}
