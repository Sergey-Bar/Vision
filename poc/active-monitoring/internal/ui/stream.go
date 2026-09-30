package ui

import (
	"bytes"
	"fmt"
	"net/http"
	"time"
)

// boundary is the multipart separator for an MJPEG stream.
//
// This is the whole trick for a live view with NO JAVASCRIPT: a browser renders
// a `multipart/x-mixed-replace` response inside a plain `<img>` and updates it as
// parts arrive. It is how a security camera has shown live video in a browser
// for thirty years, and it means the live view is a real stream rather than a
// polling loop that a person has to refresh.
//
// The alternative — a page that reloaded itself on a timer, or JavaScript — is
// worse. A reload flashes the whole layout and loses scroll position; JavaScript
// would put a language runtime between the operator and the thing that is
// supposed to be inspectable when something is wrong.
const boundary = "pocframe"

// streamHandler serves the live frame as a multipart MJPEG stream.
//
// It polls the recorder's newest frame rather than subscribing to it. That is a
// deliberate trade: a subscription would deliver every frame the pipeline
// produces, and at 3 fps of inference with a CPU encoder in the path the stream
// would fall behind itself and show a stale picture. A poll at a fixed rate
// shows the newest thing available, which is what a viewer wants and what an
// operator needs.
func (s *Server) streamHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server cannot stream: the response writer is not a flusher, so a live view would have to be a full reload on a timer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The frame rate the STREAM shows, which is lower than the inference rate on
	// purpose. Ten frames a second is enough for a person to see movement, and a
	// higher number spends the CPU the pipeline needs for detection.
	const streamFPS = 6
	tick := time.NewTicker(time.Second / streamFPS)
	defer tick.Stop()

	var lastHash string
	sent := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}

		live := s.current()
		if len(live.Frame) == 0 {
			// Before the first frame there is nothing to show. A blank box that
			// says so is better than a black rectangle, which looks like a
			// camera pointing at nothing.
			if sent == 0 {
				fmt.Fprintf(w, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nX-Poc-Status: waiting-for-first-frame\r\n\r\nwaiting for the first frame from the recorder\n", boundary)
				flusher.Flush()
				sent++
			}
			continue
		}

		// Only send when the frame CHANGED. A static scene costs nothing, and the
		// CPU saved here is the CPU the detector gets.
		h := frameKey(live.Frame)
		if h == lastHash {
			continue
		}
		lastHash = h

		// The overlay is drawn HERE, on the server, on the bytes the recorder
		// already has. Doing it in the browser would need JavaScript and would
		// put the boxes on the client where nobody can audit them.
		drawn, err := DrawOverlay(live)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nX-Poc-Seq: %d\r\nX-Poc-At: %s\r\nContent-Length: %d\r\n\r\n",
			boundary, live.Seq, live.At.UTC().Format(time.RFC3339Nano), len(drawn)); err != nil {
			return
		}
		if _, err := w.Write(drawn); err != nil {
			return
		}
		flusher.Flush()
		sent++
	}
}

// frameKey is a cheap fingerprint of a frame, so the stream can skip sending a
// picture that has not changed. It is a hash of the length and a few sampled
// bytes, not of the whole frame: this runs per viewer per frame, and a full
// SHA-256 of every frame would cost more than the JPEG encode it is avoiding.
func frameKey(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var h uint64 = 1469598103934665603
	step := 1
	if len(b) > 1024 {
		step = len(b) / 1024
	}
	for i := 0; i < len(b); i += step {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%d:%d:%x", len(b), step, h)
}

// stillHandler serves ONE frame, for a client that cannot stream, and as the
// OpenGraph-ish preview that a screenshot tool and a link unfurl will pick up.
func (s *Server) stillHandler(w http.ResponseWriter, r *http.Request) {
	live := s.current()
	if len(live.Frame) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no frame has been read from the recorder yet. This is not a black frame: it is the absence of one, and the run's log says when the first frame arrived."))
		return
	}
	drawn, err := DrawOverlay(live)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "the overlay could not be drawn: %v", err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(drawn)
}

var _ = bytes.NewReader
