package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The vendor's media surface.
//
// The route audit does not cover it, and that is a fact rather than an omission:
// the documented route table is the vendor's EVENT and DEVICE API, and the
// recording surface -- the one that answers "what do you hold on this camera
// between these two instants" -- is not in it.
//
// These paths are therefore the ADAPTER's reading of where that surface lives,
// and they are declared in one place so that a second implementation of the
// vendor's shape can be checked against them. The emulator serves exactly
// these paths, and a test asks the emulator for a clip through the adapter, so
// a disagreement is a failing test rather than a 404 in production.
//
// They live under the API prefix because the media surface belongs to the same
// device as the events, and a recording fetched from a different host than the
// events that describe it is a recording from somebody else's site.

// The media surface's paths.
const (
	// MediaProbePath asks what the vendor holds over a window.
	MediaProbePath = APIPrefix + "/media/probe"
	// MediaClipPath fetches the bytes for a window.
	MediaClipPath = APIPrefix + "/media/clip"
	// MediaSnapshotPath fetches one still at one instant.
	MediaSnapshotPath = APIPrefix + "/media/snapshot"
)

// mediaProbeResponse is the probe's answer.
//
// The intervals are ISO-860 instants, because the caller compares them with
// the record's corrected time and a comparison of two differently-formatted
// timestamps is a comparison that silently succeeds.
type mediaProbeResponse struct {
	CameraRef string      `json:"cameraRef"`
	Available []mediaSpan `json:"available"`
	Source    string      `json:"source"`
	Asked     mediaWindow `json:"asked"`
}

type mediaSpan struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type mediaWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// ProbeRange asks the vendor what it holds on one camera over one window.
//
// An empty answer is a FACT and a failed probe is an ERROR, and the two are
// different answers to "what happened": the first says the recording does not
// exist and the second says nobody could ask.
func (t *httpTransport) ProbeRange(ctx context.Context, cameraRef string, from, to time.Time) (MediaRange, error) {
	if t.token == "" {
		return MediaRange{}, &UnauthenticatedError{Route: Route{Path: MediaProbePath}, Status: 0}
	}
	target := t.mediaURL(MediaProbePath, map[string]string{
		"camera": cameraRef,
		"from":   from.UTC().Format(time.RFC3339Nano),
		"to":     to.UTC().Format(time.RFC3339Nano),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return MediaRange{}, fmt.Errorf("milestone: build the media probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ai-captain-security-core/1")

	resp, err := t.client.Do(req)
	if err != nil {
		return MediaRange{}, fmt.Errorf("milestone: probe the media surface: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxBodyBytes))
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return MediaRange{}, fmt.Errorf("milestone: read the media probe answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return MediaRange{}, &UnauthenticatedError{Route: Route{Path: MediaProbePath}, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusNotFound:
		// The vendor has no media surface at all, which is a different thing
		// from holding nothing over the window.
		return MediaRange{}, &NotFoundError{Route: Route{Path: MediaProbePath}}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return MediaRange{}, &StatusError{Route: Route{Path: MediaProbePath}, Status: resp.StatusCode, Body: summarize(raw)}
	}
	var answer mediaProbeResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return MediaRange{}, &MalformedError{
			Route: Route{Path: MediaProbePath},
			Why: "the media probe's answer is not JSON, so a recording surface that answers " +
				"with an HTML error page would look like a camera that holds nothing",
		}
	}
	out := MediaRange{CameraRef: answer.CameraRef, Source: answer.Source}
	for _, span := range answer.Available {
		out.Available = append(out.Available, Interval(span))
	}
	return out, nil
}

// FetchClip fetches the bytes for a window.
//
// The range in the answer is what the bytes ACTUALLY cover, which is not
// necessarily what was asked for: a vendor may return what it holds rather
// than what was requested, and a receipt that recorded the ask would record a
// claim about coverage nobody made.
func (t *httpTransport) FetchClip(ctx context.Context, cameraRef string, from, to time.Time) (MediaClip, error) {
	return t.fetchMedia(ctx, MediaClipKind, MediaClipPath, map[string]string{
		"camera": cameraRef,
		"from":   from.UTC().Format(time.RFC3339Nano),
		"to":     to.UTC().Format(time.RFC3339Nano),
	}, Interval{From: from, To: to})
}

// FetchSnapshot fetches one still at one instant.
func (t *httpTransport) FetchSnapshot(ctx context.Context, cameraRef string, at time.Time) (MediaClip, error) {
	return t.fetchMedia(ctx, MediaSnapshotKind, MediaSnapshotPath, map[string]string{
		"camera": cameraRef,
		"at":     at.UTC().Format(time.RFC3339Nano),
	}, Interval{From: at, To: at})
}

// fetchMedia performs one media fetch and shapes the answer.
func (t *httpTransport) fetchMedia(ctx context.Context, kind, path string, params map[string]string, asked Interval) (MediaClip, error) {
	if t.token == "" {
		return MediaClip{}, &UnauthenticatedError{Route: Route{Path: path}, Status: 0}
	}
	target := t.mediaURL(path, params)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return MediaClip{}, fmt.Errorf("milestone: build the media fetch request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("User-Agent", "ai-captain-security-core/1")

	resp, err := t.client.Do(req)
	if err != nil {
		return MediaClip{}, fmt.Errorf("milestone: fetch from the media surface: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return MediaClip{}, &UnauthenticatedError{Route: Route{Path: path}, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusNotFound:
		return MediaClip{}, &NotFoundError{Route: Route{Path: path}}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return MediaClip{}, &StatusError{Route: Route{Path: path}, Status: resp.StatusCode}
	}
	// The bytes are NOT read here. They are handed to the caller as a stream,
	// because the evidence store writes them and the store is the single path to
	// storage: a fetch that read the whole clip into memory and then wrote it
	// would be a second copy of a video nobody asked to keep.
	rangeHeader := resp.Header.Get("X-Media-Range")
	covered := asked
	if rangeHeader != "" {
		if parsed, ok := parseMediaRange(rangeHeader); ok {
			covered = parsed
		}
	}
	return MediaClip{
		Kind:        kind,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        resp.ContentLength,
		Reader:      resp.Body,
		Range:       covered,
	}, nil
}

// mediaURL builds a media surface URL.
//
// It is the same builder the route table uses, with the media paths substituted
// in: one URL builder, so the trailing-slash bug has one home.
func (t *httpTransport) mediaURL(path string, params map[string]string) *url.URL {
	u := *t.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return &u
}

// parseMediaRange reads the range header the media surface answers with.
//
// The header is the vendor's own way of saying which span the bytes cover, and
// it is parsed rather than assumed: a receipt that recorded the ASK would
// record a claim about coverage nobody made.
func parseMediaRange(header string) (Interval, bool) {
	// The form is `start=...;end=...`, and the instants are ISO-860.
	start, end, found := strings.Cut(header, ";")
	if !found {
		return Interval{}, false
	}
	start = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(start), "start="))
	end = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(end), "end="))
	from, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return Interval{}, false
	}
	to, err := time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return Interval{}, false
	}
	return Interval{From: from, To: to}, true
}
