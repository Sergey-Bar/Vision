package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/detector"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/mp4"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// The profile the POC would have evaluated had an AI plane been configured.
//
// It is named rather than defaulted to nothing because an observation that names
// no profile is an observation no rule can cite. This is the one production
// profile in this build; the shadow profiles produce observations and no
// incidents, and a POC whose whole point is to raise an incident needs the
// production one.
const pocProfileID = "restricted-person"

// runCapture is the whole pipeline, and it is one function for both sources.
//
// The loop is deliberately linear and single-threaded: a detector reading, a
// recorder writing, and a clip finishing are three steps on one stream, and the
// ordering is the design. A detector that fires after the trigger is pushed
// loses the picture that fired it, and a recorder that finishes before the
// detector has reported loses the reason the clip exists.
func runCapture(
	ctx context.Context,
	o pocOptions,
	rec *clipRecorder,
	src frameSource,
	store *evidence.Store,
	rep *pocReport,
	onReading func(detector.Reading),
	onReady func(),
) (*pocRun, error) {
	// rcr is the muxer; rec is the recording of the session. Two different
	// things that once shared a name, which is how one of them ended up wired
	// into only one of the two sources.
	rcr := mp4.NewRecorder(mp4.Options{
		PreRoll:  o.preRoll,
		PostRoll: o.postRoll,
		MaxTime:  o.maxTime,
		MaxBytes: o.maxBytes,
		OnDamaged: func(reason string) {
			rep.Degradations = append(rep.Degradations, "elementary stream: "+reason)
		},
	})
	det := detector.NewCodedSize(detector.CodedSizeOptions{Threshold: o.threshold})

	run := &pocRun{report: rep}
	// The banner waits for the first picture; see below.
	bannerPending := onReady != nil
	var stopReason string
	var deadline time.Time
	if o.seconds > 0 {
		deadline = time.Now().Add(o.seconds)
	}

	for {
		if ctx.Err() != nil {
			stopReason = "interrupted"
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			stopReason = "reached the --seconds limit"
			break
		}
		p, wall, err := src.next()
		if err != nil {
			// The end of a stream is a normal ending, not a failure. Everything
			// else is: a camera that hung, a socket that died, a depacketiser
			// that refused. The distinction decides the exit code.
			if errors.Is(err, errStreamEnd) || isEndOfStream(err) {
				stopReason = streamEnded
			} else {
				stopReason = "the stream failed: " + err.Error()
				rep.Degradations = append(rep.Degradations, "capture: "+err.Error())
			}
			break
		}

		reading, derr := det.Observe(detector.Observed{
			CodedBytes: p.Bytes(),
			KeyFrame:   p.KeyFrame,
			Time:       wall,
		})
		if derr != nil {
			// A detector that cannot read a picture is a named degradation, and
			// the run continues: the clip is still worth having even when the
			// trigger that would have selected it did not happen.
			rep.Degradations = append(rep.Degradations, "detector: "+derr.Error())
		} else {
			rep.LastScore = reading.Score
			if onReading != nil {
				onReading(reading)
			}
			if reading.Triggered && !rcr.Triggered() {
				// The first trigger arms the recorder. Recording begins at the
				// next keyframe, by which point the pre-roll ring holds every
				// group since the last eviction - including this one - so the
				// clip contains the event rather than starting after it.
				rcr.Trigger(reading.Reason, p.DecodeTime)
				rep.Triggered = true
				rep.TriggerWhy = reading.Reason
			}
		}
		rep.Pictures++
		if o.pictures > 0 && rep.Pictures >= o.pictures {
			stopReason = "reached the --pictures limit"
			break
		}

		for _, nal := range p.NALs {
			// The session recording is written from the same loop that feeds the
			// muxer, so a capture taken from a recording and a capture taken from
			// a camera produce the same fixture. Wiring this into only one source
			// produced an empty file for a replay, which is the worst kind of
			// wrong: it looks like a flag that works.
			rec.write(nal)
			if err := rcr.PushNAL(nal, p.DecodeTime, wall); err != nil {
				rep.Degradations = append(rep.Degradations, "elementary stream: "+err.Error())
				stopReason = "the recorder refused the stream: " + err.Error()
				return run, run.finish(o, store, rcr, rep, stopReason, nil)
			}
		}

		// The banner is held back until the first picture has been read, because
		// the frame's own parameter sets are the only authority on its geometry.
		// Printing it earlier would either leave a blank where the resolution
		// belongs, or print the camera's declared frame size, which cameras get
		// wrong often enough that a banner claiming it would be a small lie.
		if bannerPending {
			bannerPending = false
			if w, h, profile, level, ok := rcr.Geometry(); ok {
				rep.Source.Resolution = fmt.Sprintf("%dx%d", w, h)
				rep.Source.DeclaredGeometry = true
				rep.Source.Profile = profile
				rep.Source.Level = level
			}
			if onReady != nil {
				onReady()
			}
		}
	}

	clip, clipErr := rcr.Finish()
	if clipErr != nil { //nolint:nilerr // an untriggered recorder is a completed run over a quiet scene, not a failure
		// A recorder that was never triggered is not an error: the detector did
		// its job and the scene was quiet. It is a distinct outcome, reported
		// as one, because "the capture worked and nothing happened" and "the
		// capture is broken" must never share a line of output.
		rep.Outcome = "no_clip"
		rep.OutcomeWhy = "the detector never fired within the run, so there was nothing to clip. " +
			"The capture path itself completed without error."
		rep.EndedAt = time.Now().UTC()
		//nolint:nilerr // an untriggered recorder is a completed run over a quiet
		// scene, not a failure, and the run's outcome says so
		return run, nil
	}
	if stopReason != "" && strings.HasPrefix(stopReason, "the recorder") {
		return run, fmt.Errorf("poc: %s", stopReason)
	}
	return run, run.finish(o, store, rcr, rep, stopReason, clip)
}

// errStreamEnd is the sentinel a frame source returns at the end of its input.
var errStreamEnd = errors.New("poc: end of stream")

func isEndOfStream(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, errStreamEnd) ||
		strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "use of closed network connection")
}

// finish stores the clip, records the degraded observation and the incident, and
// writes both to disk.
//
// The order is the point: the clip is stored and hashed before anything claims
// it exists, and the observation and the incident both name the stored hash, so
// there is no way to end up with an incident citing evidence that was never
// written.
func (r *pocRun) finish(
	o pocOptions,
	store *evidence.Store,
	rec *mp4.Recorder,
	rep *pocReport,
	stopReason string,
	clip *mp4.Clip,
) error {
	_ = rec
	if clip == nil {
		return nil
	}
	rep.EndedAt = time.Now().UTC()
	rep.Duration = clip.WallEnd.Sub(clip.WallStart)

	// The declared geometry is only used for the banner; the clip's own
	// dimensions come out of the SPS, and where the two disagree the SPS is
	// right and the camera's description was wrong.
	if !rep.Source.DeclaredGeometry {
		rep.Source.DeclaredGeometry = true
	}
	rep.Source.Resolution = fmt.Sprintf("%dx%d", clip.Width, clip.Height)

	obj, err := store.Put(o.tenant, bytes.NewReader(clip.Data))
	if err != nil {
		return fmt.Errorf("poc: store the clip: %w", err)
	}
	ev := &pocEvidence{
		StorageRef: obj.Ref, SHA256: obj.SHA256, SizeBytes: obj.SizeBytes,
		MediaType: clip.MediaType,
	}
	// Read it back and prove the bytes are the bytes.
	//
	// The POC's entire claim is "a stored, hashed clip", and a claim of that
	// shape is worth exactly as much as the check behind it. Put hashes on the
	// way in, which means the address is derived from what was written - but
	// nothing yet has established that reading the address back yields the same
	// clip. One Open and one comparison does, and it costs nothing on a
	// hundred-megabyte object.
	body, readBack, err := store.Open(obj.Ref, obj.SHA256)
	if err != nil {
		rep.DevidenceFailures++
		rep.Degradations = append(rep.Degradations,
			"evidence: the stored clip could not be read back: "+err.Error())
	} else {
		got, copyErr := io.ReadAll(body)
		_ = body.Close()
		switch {
		case copyErr != nil:
			rep.DevidenceFailures++
			rep.Degradations = append(rep.Degradations, "evidence: reading the clip back failed: "+copyErr.Error())
		case !bytes.Equal(got, clip.Data):
			rep.DevidenceFailures++
			rep.Degradations = append(rep.Degradations,
				"evidence: the clip read back from the store is not the clip that was written")
		default:
			ev.HashVerified = readBack.Verified
		}
	}
	rep.Evidence = ev
	rep.Clip = &pocClip{
		Bytes: clip.Size(), MediaType: clip.MediaType, Duration: clip.Duration,
		Width: clip.Width, Height: clip.Height, Profile: clip.Profile, Level: clip.Level,
		Pictures: clip.Pictures, KeyFrames: clip.KeyFrames,
		PreRollPics: clip.PreRollPics, PreRollSpan: clip.PreRollSpan,
		PreRollReason: clip.PreRollReason, CapReason: clip.CapReason,
		StopReason: clip.StopReason, DamagedReason: clip.DamagedReason,
		WallStart: clip.WallStart, WallEnd: clip.WallEnd,
	}
	if clip.DamagedReason != "" {
		rep.Degradations = append(rep.Degradations, "clip: "+clip.DamagedReason)
	}
	if clip.PreRollReason != "" {
		rep.Degradations = append(rep.Degradations, "pre-roll: "+clip.PreRollReason)
	}

	// An event id derived from the clip's content, not from a clock: the same
	// bytes are the same event, so re-running against the same fixture produces
	// the same id and a retry is recognisable as a retry.
	eventID := "evt_" + obj.SHA256[:24]
	incidentID, err := newID("inc_")
	if err != nil {
		return err
	}

	obs := observations.Observation{
		TenantID: o.tenant, SiteID: o.site,
		IncidentID: incidentID, EventID: eventID,
		ProfileID:           pocProfileID,
		InputEvidenceIDs:    []string{obj.Ref},
		InputEvidenceHashes: []string{obj.SHA256},
		CreatedAt:           time.Now().UTC(),
	}
	// MarkDegraded is the same call the writer makes, so the artefact below is
	// the row that would have been stored rather than a description of it. The
	// distinction is the whole point of the run: degraded is not a negative
	// answer, and insufficient_evidence stays true so that any consumer which
	// forgets to check the degradation fails closed.
	obs.MarkDegraded(observations.DegradationNoRuntime,
		"no AI plane is configured in "+pocBuildVersion+"; the model was never asked")
	observations.ApplyDegradedPlaceholders(&obs)
	if err := obs.Validate(); err != nil {
		return fmt.Errorf("poc: the degraded observation did not validate: %w", err)
	}
	// The observation id is minted here rather than taken from a database, and
	// the artefact says so. A stored document carrying the literal string
	// "obs_pending" is a document that claims an identity it does not have.
	obsID, err := newID("obs_")
	if err != nil {
		return err
	}
	rep.Observation = &pocObservation{
		ObservationID:        obsID,
		IDMintedLocally:      true,
		Degraded:             true,
		DegradedReason:       string(obs.Degraded),
		DegradedDetail:       obs.DegradedDetail,
		InsufficientEvidence: obs.InsufficientEvidence,
		Model:                obs.Model,
		ModelHash:            obs.ModelHash,
		RuntimeVersion:       obs.RuntimeVersion,
		ProfileID:            obs.ProfileID,
		InputEvidenceHashes:  obs.InputEvidenceHashes,
		CreatedAt:            obs.CreatedAt,
	}

	rep.Incident = &pocIncident{
		IncidentID: incidentID, TenantID: o.tenant, SiteID: o.site,
		State: "open", Severity: "query",
		Title:      fmt.Sprintf("unverified change on %s", o.site),
		ReasonCode: "ai_plane_absent",
		Reason: "The always-on detector reported a novelty score above the configured threshold. " +
			"The AI plane was never asked, so this incident is decided without an observation. " +
			"It is a query for an operator, not a claim that anything happened.",
		RaisedAt: time.Now().UTC(), EventID: eventID,
		Evidence:                    *rep.Evidence,
		DecidedWithoutAIObservation: true,
		AwaitingDisposition:         true,
		ObservationID:               rep.Observation.ObservationID,
		ObservationDegraded:         true,
	}

	rep.Absences = pocAbsences{
		// All three are true here, and they are true for different reasons. An
		// AI plane was not asked; a request would have been degraded because
		// there is nothing to answer it; and the model could not have said
		// "nothing happened" because no model ran at all.
		NoObservation:        true,
		NoObservationWhy:     "the AI plane is not configured in this build, so it was never asked",
		DegradedObservation:  true,
		DegradedWhy:          string(observations.DegradationNoRuntime),
		InsufficientEvidence: true,
		InsufficientWhy: "no model ran, so no model assessed sufficiency; the flag is set so that " +
			"any consumer which forgets to check the degradation fails closed",
		IsNegative: false,
	}

	rep.Outcome = "clip_stored"
	rep.OutcomeWhy = "the detector fired, a bounded clip with lead-in was stored by content hash, " +
		"and an incident was raised that is decided without an AI observation"

	if err := writeArtefacts(o.out, rep); err != nil {
		return err
	}
	// Only an abnormal ending is a degradation. "The stream ended" is what a
	// finished file does, and logging it as a degradation teaches an operator to
	// scroll past the degradations, which is how the real one gets missed.
	if stopReason != "" && stopReason != streamEnded {
		rep.Degradations = append(rep.Degradations, "capture: "+stopReason)
	}
	return nil
}

// streamEnded is the ordinary way a source finishes. It is named so that
// distinguishing it from a failure is a comparison rather than a judgement.
const streamEnded = "the stream ended"

func newID(prefix string) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("poc: generate an id: %w", err)
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// writeArtefacts puts the incident and the observation on disk as JSON.
//
// They are files rather than rows because this build has no database, and the
// difference matters: a file is a thing a person can open, read, and attach to a
// review. The milestone's incident ledger is where these become rows, and until
// then a JSON artefact is the honest form of "this is what happened".
func writeArtefacts(dir string, rep *pocReport) error {
	if rep.Incident == nil || rep.Observation == nil {
		return nil
	}
	incPath := filepath.Join(dir, rep.Incident.IncidentID+".incident.json")
	obsPath := filepath.Join(dir, rep.Incident.IncidentID+".observation.json")
	incPath, err := writeJSON(incPath, rep.Incident)
	if err != nil {
		return err
	}
	obsPath, err = writeJSON(obsPath, rep.Observation)
	if err != nil {
		return err
	}
	rep.Incident.ArtifactPath = incPath
	rep.Observation.ArtifactPath = obsPath
	// Rewritten with the paths in it, so the file and the report agree.
	rep.Incident.WrittenAt = time.Now().UTC()
	if _, err := writeJSON(incPath, rep.Incident); err != nil {
		return err
	}
	return nil
}

func writeJSON(path string, v any) (string, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("poc: encode %s: %w", filepath.Base(path), err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("poc: write %s: %w", path, err)
	}
	return path, nil
}
