package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/export"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/recording"
)

// The recording index.
//
// # What this surface is for
//
// A VMS is judged on one question: is the recording CONTINUOUS? A camera that
// was never probed, a camera that was probed and found nothing, and a camera
// that was recording and went quiet for four minutes are three different
// states, and only the third one has a hole in it.
//
// So the routes here publish the MEASURED gap rather than a summary of it. A
// timeline that says "recording healthy" because no incident fired is a
// timeline that cannot tell the operator that the disk has been full since
// Tuesday.
//
// # The site scope
//
// The scope is the PRINCIPAL's, resolved from the configuration, and a
// `siteId` in the query is checked against it rather than believed. A value a
// caller sends is an instruction and not an authorisation.
//
// # What is NOT here
//
// The BYTES. A segment's bytes live on the recorder's disk, in the customer's
// own storage, and this API does not stream them: it publishes the index, and
// the digest that makes the bytes provable. A route that streamed them would
// turn an index read into a bandwidth bill.

// recordingWindow is the default window a route reads.
//
// One hour is an operator's "what happened in the last hour" question, which
// is what a person opening a VMS at a desk actually asks.
const recordingWindow = time.Hour

// recordingWindowCeiling is the LONGEST window a request may ask for.
//
// The index is one row per segment per device, so a year of one camera is
// 315 360 rows. A route that lets a request ask for that without saying so
// is a route that a browser can ask for by accident.
const recordingWindowCeiling = 24 * time.Hour

// defaultGapThreshold is the silence that counts as a HOLE.
//
// The boundary between two segments is about one frame wide, because the
// keyframe that ends one segment is the first frame of the next. A second of
// silence is not a frame boundary at any frame rate a camera ships, so a
// second is the floor — and the threshold is a PARAMETER, because a camera
// running at one frame a minute is a real thing and its boundary gaps are
// 60 seconds long.
const defaultGapThreshold = time.Second

// handleRecordings lists the recorded segments in a window.
//
// The device is optional: a site with many cameras asks for the whole site's
// recording at once, and the answer is the same rows in one place rather than
// a request per camera.
func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Recordings == nil {
		// An empty list and an unconfigured recorder look identical, and only
		// one of them is true.
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no recording index configured.",
			"items":       []any{},
		})
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	from, to, ok := s.recordingRange(w, r)
	if !ok {
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200, 2000)

	segments, err := d.Recordings.List(r.Context(), p.TenantID, site, device, from, to, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"siteId":    site,
		"deviceId":  device,
		"from":      from,
		"to":        to,
		"items":     recordingViews(segments),
	})
}

// handleRecordingTimeline answers: what did this camera record, and is the
// recording continuous?
//
// It is the route the console's recording page reads, and it answers both
// halves in one response because an operator asking "is it recording" is
// asking about the same seconds the timeline shows.
//
// The CONTINUITY verdict is computed from the MEASURED gaps, and it is a
// named degradation rather than a boolean when the answer is unknown — a
// camera that has never recorded has no verdict, and a verdict of "healthy"
// for a camera that has never recorded would be a lie.
func (s *Server) handleRecordingTimeline(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Recordings == nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no recording index configured.",
			"items":       []any{},
		})
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if device == "" {
		WriteError(w, http.StatusBadRequest, "device_required",
			"deviceId is required: a timeline is a camera's timeline.", requestID(r))
		return
	}
	from, to, ok := s.recordingRange(w, r)
	if !ok {
		return
	}
	threshold := s.recordingGapThreshold(r)

	segments, err := d.Recordings.Timeline(r.Context(), p.TenantID, site, device, from, to)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	gaps, err := d.Recordings.Gaps(r.Context(), p.TenantID, site, device, from, to, threshold)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}

	covered := time.Duration(0)
	for _, seg := range segments {
		covered += seg.EndedAt.Sub(seg.StartedAt)
	}
	window := to.Sub(from)

	out := map[string]any{
		"available":      true,
		"siteId":         site,
		"deviceId":       device,
		"from":           from,
		"to":             to,
		"segments":       recordingViews(segments),
		"gaps":           recordingGapViews(gaps),
		"gapThresholdMs": threshold.Milliseconds(),
		"continuity": map[string]any{
			// The three numbers that say whether the recording is whole.
			"segmentCount": len(segments),
			"coveredMs":    covered.Milliseconds(),
			"windowMs":     window.Milliseconds(),
			"gapCount":     len(gaps),
			"gapMs":        recordingTotalGap(gaps).Milliseconds(),
		},
	}
	// The verdict, and it is named rather than a boolean.
	out["verdict"] = recordingVerdict(len(segments), gaps)
	WriteJSON(w, http.StatusOK, out)
}

// handleRecordingGaps answers: where was the recording broken?
//
// It is a separate route because a gap is a question an operator asks about
// a SPECIFIC window — "was it recording over the weekend?" — and the answer
// is the list of holes with their sizes.
func (s *Server) handleRecordingGaps(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Recordings == nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no recording index configured.",
			"gaps":        []any{},
		})
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if device == "" {
		WriteError(w, http.StatusBadRequest, "device_required",
			"deviceId is required: a gap belongs to a camera.", requestID(r))
		return
	}
	from, to, ok := s.recordingRange(w, r)
	if !ok {
		return
	}
	threshold := s.recordingGapThreshold(r)
	gaps, err := d.Recordings.Gaps(r.Context(), p.TenantID, site, device, from, to, threshold)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"available":      true,
		"siteId":         site,
		"deviceId":       device,
		"from":           from,
		"to":             to,
		"gapThresholdMs": threshold.Milliseconds(),
		"gaps":           recordingGapViews(gaps),
		"totalGapMs":     recordingTotalGap(gaps).Milliseconds(),
	})
}

// handleRecordingContent streams a segment's bytes.
//
// # The bytes are the CUSTOMER'S, and the route serves them from the
// customer's own disk.
//
// The recording root is the recorder's, and the core reads the same root the
// recorder writes. An install whose recorder runs on another machine has no
// root to read, and the route says so by name rather than answering with an
// empty body that a player reads as "a clip of nothing".
//
// # The path is the SHARED guard's path
//
// The reference comes out of the index, and the index is a database, and a
// database is not a trusted source of a path. So the reference is checked for
// shape and the RESOLVED path is checked for containment, and the guard is
// the same one the evidence store uses.
func (s *Server) handleRecordingContent(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Recordings == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no recording index configured.", requestID(r))
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	segmentID := strings.TrimSpace(r.PathValue("segmentId"))
	if segmentID == "" {
		WriteError(w, http.StatusBadRequest, "segment_required",
			"segmentId is required: a segment is named by its own id.", requestID(r))
		return
	}
	content, err := d.Recordings.Open(r.Context(), p.TenantID, site, segmentID)
	if err != nil {
		switch {
		case errors.Is(err, recording.ErrNoSuchSegment):
			WriteError(w, http.StatusNotFound, "not_found",
				"No such segment: the recording index does not hold it.", requestID(r))
		case errors.Is(err, recording.ErrNoRoot):
			WriteError(w, http.StatusServiceUnavailable, "no_recording_root",
				"This install has no recording root: the recorder's bytes are on a disk this "+
					"process cannot read.", requestID(r))
		case errors.Is(err, recording.ErrPathRejected):
			WriteError(w, http.StatusBadRequest, "bad_reference",
				"The segment's storage reference was refused: it names something outside the "+
					"recording root.", requestID(r))
		case errors.Is(err, recording.ErrContentMismatch):
			// The bytes no longer match the row. The operator is told,
			// because footage that cannot be proven to be the footage the
			// index names is worse than no footage.
			WriteError(w, http.StatusConflict, "content_mismatch",
				"The segment's bytes do not match the digest the recording index recorded: "+
					"the file was changed after it was written.", requestID(r))
		default:
			WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		}
		return
	}
	defer func() { _ = content.Reader.Close() }()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	// The digest is published, so a client can verify what it received
	// against what the index says.
	w.Header().Set("X-Content-Digest", content.Digest)
	// And whether the bytes were CHECKED against it. A client that knows
	// the check was on can trust the digest; one that does not know is
	// trusting nothing.
	w.Header().Set("X-Content-Verified", strconv.FormatBool(d.Recordings.VerifyDigestOnRead()))
	// The bytes are not cached by a proxy: they are evidence.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content.Reader)
}

// handleRecordingClip answers: what covers this window, and what came before
// it?
//
// The clip is a LIST of segments and the pre-roll is the segment before the
// window, because the seconds before the moment are where the lead-in lives.
func (s *Server) handleRecordingClip(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Export == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"This install has no recording export configured.", requestID(r))
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	q := r.URL.Query()
	device := strings.TrimSpace(q.Get("deviceId"))
	if device == "" {
		WriteError(w, http.StatusBadRequest, "device_required",
			"deviceId is required: a clip belongs to a camera.", requestID(r))
		return
	}
	from, to, ok := s.recordingRange(w, r)
	if !ok {
		return
	}
	clip, err := d.Export.Clip(r.Context(), p.TenantID, site, device, from, to)
	if err != nil {
		if errors.Is(err, export.ErrNoWindow) {
			WriteError(w, http.StatusBadRequest, "bad_window", err.Error(), requestID(r))
			return
		}
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	segments := make([]map[string]any, 0, len(clip.Segments))
	for _, seg := range clip.Segments {
		segments = append(segments, map[string]any{
			"segmentId":  seg.SegmentID,
			"seq":        seg.Seq,
			"startedAt":  seg.StartedAt,
			"endedAt":    seg.EndedAt,
			"bytes":      seg.Bytes,
			"digest":     seg.Digest,
			"state":      seg.State,
			"contentUrl": "/v1/recordings/" + url.PathEscape(seg.SegmentID) + "/content",
		})
	}
	out := map[string]any{
		"available": true,
		"siteId":    site,
		"deviceId":  device,
		"from":      from,
		"to":        to,
		"segments":  segments,
		"gaps":      recordingGapViews(clip.Gaps),
		"coveredMs": clip.CoveredMs,
		"windowMs":  clip.WindowMs,
	}
	if clip.PreRoll != nil {
		out["preRoll"] = map[string]any{
			"segmentId":  clip.PreRoll.SegmentID,
			"seq":        clip.PreRoll.Seq,
			"startedAt":  clip.PreRoll.StartedAt,
			"endedAt":    clip.PreRoll.EndedAt,
			"bytes":      clip.PreRoll.Bytes,
			"digest":     clip.PreRoll.Digest,
			"contentUrl": "/v1/recordings/" + url.PathEscape(clip.PreRoll.SegmentID) + "/content",
		}
	} else {
		// No pre-roll is a FACT about the recording — the window starts
		// at the beginning of what was recorded — and not an absence the
		// console has to guess about.
		out["preRoll"] = nil
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleRecordingChain answers: can this recording be PROVED?
//
// The chain is recomputed from the rows rather than read from the chain row,
// because a reader that trusted the stored head would report a broken
// recording as sound. The recorded head is published alongside the
// recomputed one, so a mismatch is visible rather than inferred.
func (s *Server) handleRecordingChain(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.Recordings == nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no recording index configured.",
		})
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if device == "" {
		WriteError(w, http.StatusBadRequest, "device_required",
			"deviceId is required: a chain belongs to a camera.", requestID(r))
		return
	}
	day := s.recordingDay(r)
	verification, err := d.Recordings.VerifyChain(r.Context(), p.TenantID, site, device, day)
	if err != nil {
		if errors.Is(err, recording.ErrNoSuchChain) {
			// No chain for that device and day. That is a FACT about the
			// device — it recorded nothing that day — and it is not an
			// error, so it is a 200 with a named reason rather than a 404
			// that reads as "the route is broken".
			WriteJSON(w, http.StatusOK, map[string]any{
				"available": true,
				"siteId":    site,
				"deviceId":  device,
				"day":       day.Format("2006-01-02"),
				"verified":  false,
				"reason":    "This camera has no recording chain for that day: it recorded nothing then.",
			})
			return
		}
		WriteError(w, http.StatusInternalServerError, "verify_failed", err.Error(), requestID(r))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"available":    true,
		"siteId":       site,
		"deviceId":     device,
		"day":          day.Format("2006-01-02"),
		"verified":     verification.OK,
		"segmentCount": verification.Segments,
		"recordedHead": verification.Recorded,
		"expectedHead": verification.Expected,
		"brokenAtSeq":  verification.BrokenAt,
	})
}

// recordingSite resolves the site the request reads.
//
// The scope is the PRINCIPAL's. A `siteId` in the query is CHECKED against
// the scope rather than believed, and a site outside the scope is a 403
// rather than a 404 — the operator knows which sites they have.
func (s *Server) recordingSite(w http.ResponseWriter, r *http.Request, p identity.Principal) (string, bool) {
	if len(p.SiteScope) == 0 {
		WriteError(w, http.StatusForbidden, "site_not_assigned",
			"You are not assigned to any site.", requestID(r))
		return "", false
	}
	requested := strings.TrimSpace(r.URL.Query().Get("siteId"))
	if requested == "" {
		return firstSite(p), true
	}
	if !p.CanSeeSite(requested) {
		WriteError(w, http.StatusForbidden, "site_not_assigned",
			"You are not assigned to that site.", requestID(r))
		return "", false
	}
	return requested, true
}

// recordingRange reads the window.
//
// The default is the last hour. A window longer than the ceiling is refused
// with the reason rather than silently clamped, because a silently clamped
// window answers a different question from the one that was asked.
func (s *Server) recordingRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	now := s.clock()
	to := now
	from := now.Add(-recordingWindow)
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_range",
				"to is not an RFC3339 instant: "+raw, requestID(r))
			return time.Time{}, time.Time{}, false
		}
		to = t.UTC()
	}
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_range",
				"from is not an RFC3339 instant: "+raw, requestID(r))
			return time.Time{}, time.Time{}, false
		}
		from = t.UTC()
	}
	if !to.After(from) {
		WriteError(w, http.StatusBadRequest, "bad_range",
			"the window ends before it starts.", requestID(r))
		return time.Time{}, time.Time{}, false
	}
	if to.Sub(from) > recordingWindowCeiling {
		WriteError(w, http.StatusBadRequest, "window_too_wide",
			"The window is longer than the 24-hour ceiling. Ask for a shorter window, or for "+
				"one camera at a time.", requestID(r))
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// recordingDay reads the day whose chain is verified.
//
// It defaults to TODAY in UTC, because the chain is sealed per DAY and a
// chain that is sealed at midnight is a chain about one UTC day.
func (s *Server) recordingDay(r *http.Request) time.Time {
	raw := strings.TrimSpace(r.URL.Query().Get("day"))
	if raw == "" {
		return s.clock().UTC().Truncate(24 * time.Hour)
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		// A malformed day falls back to today rather than failing: the day
		// is a convenience parameter and the chain route's answer is about
		// a camera, not about the parameter's syntax.
		return s.clock().UTC().Truncate(24 * time.Hour)
	}
	return t.UTC()
}

// recordingGapThreshold reads the silence that counts as a hole.
func (s *Server) recordingGapThreshold(r *http.Request) time.Duration {
	raw := strings.TrimSpace(r.URL.Query().Get("minGapMs"))
	if raw == "" {
		return defaultGapThreshold
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return defaultGapThreshold
	}
	return time.Duration(ms) * time.Millisecond
}

// recordingVerdict names what the recording is.
//
// The three states are three DIFFERENT facts and the route says which one it
// is:
//
//   - NO RECORDING: the camera has no segment in the window at all. A camera
//     that has never been asked to record is not the same as a camera that
//     failed, and the operator needs to know which.
//   - BROKEN: there is at least one hole, and the holes are published.
//   - CONTINUOUS: there are segments and no hole larger than the threshold.
//
// There is no fourth state in which the answer is unknown, because the gaps
// are MEASURED from the stored rows and a read failure is a 500 rather than a
// verdict.
func recordingVerdict(segments int, gaps []recording.Gap) map[string]any {
	switch {
	case segments == 0:
		return map[string]any{
			"state":  "no_recording",
			"detail": "This camera recorded nothing in this window.",
		}
	case len(gaps) > 0:
		return map[string]any{
			"state":  "broken",
			"detail": "This camera's recording has " + itoa(int64(len(gaps))) + " hole(s) in this window.",
		}
	default:
		return map[string]any{
			"state":  "continuous",
			"detail": "This camera's recording is continuous across this window.",
		}
	}
}

// recordingViews renders the segments.
func recordingViews(segments []recording.Segment) []map[string]any {
	out := make([]map[string]any, 0, len(segments))
	for _, seg := range segments {
		out = append(out, map[string]any{
			"segmentId":   seg.SegmentID,
			"deviceId":    seg.DeviceID,
			"seq":         seg.Seq,
			"startedAt":   seg.StartedAt,
			"endedAt":     seg.EndedAt,
			"gapBeforeMs": seg.GapBefore.Milliseconds(),
			"bytes":       seg.Bytes,
			"digest":      seg.Digest,
			"codec":       seg.Codec,
			"width":       seg.Width,
			"height":      seg.Height,
			"storageRef":  seg.StorageRef,
			"state":       seg.State,
			"chainHash":   seg.ChainHash,
		})
	}
	return out
}

// recordingGapViews renders the measured holes.
func recordingGapViews(gaps []recording.Gap) []map[string]any {
	out := make([]map[string]any, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, map[string]any{
			"deviceId":   g.DeviceID,
			"startedAt":  g.StartedAt,
			"endedAt":    g.EndedAt,
			"durationMs": g.Duration.Milliseconds(),
		})
	}
	return out
}

// recordingTotalGap is the sum of the holes.
func recordingTotalGap(gaps []recording.Gap) time.Duration {
	var total time.Duration
	for _, g := range gaps {
		total += g.Duration
	}
	return total
}
