package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sergey-Bar/vision/internal/mp4"
	"github.com/Sergey-Bar/vision/internal/rtsp"
)

// reportVersion is bumped when the JSON shape changes, so a dashboard can refuse
// a report it does not understand rather than read fields that moved.
const reportVersion = 1

// pocReport is what a reviewer reads. Everything absent is named.
type pocReport struct {
	ReportVersion int       `json:"reportVersion"`
	Build         string    `json:"build"`
	TenantID      string    `json:"tenantId"`
	SiteID        string    `json:"siteId"`
	Source        pocSource `json:"source"`
	Detector      string    `json:"detector"`
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt,omitempty"`
	// Limitations is the honest list. It is printed on the banner and carried in
	// the JSON, because a demo that hides its own limits is a demo that gets
	// quoted as a capability.
	Limitations []string `json:"limitations"`

	Outcome      string        `json:"outcome"`
	OutcomeWhy   string        `json:"outcomeWhy"`
	Pictures     int           `json:"picturesSeen"`
	Duration     time.Duration `json:"streamDuration,omitempty"`
	LastScore    float64       `json:"lastScore,omitempty"`
	Triggered    bool          `json:"detectorTriggered"`
	TriggerWhy   string        `json:"triggerReason,omitempty"`
	Degradations []string      `json:"degradations,omitempty"`

	Clip        *pocClip        `json:"clip,omitempty"`
	Evidence    *pocEvidence    `json:"evidence,omitempty"`
	Incident    *pocIncident    `json:"incident,omitempty"`
	Observation *pocObservation `json:"observation,omitempty"`
	// DevidenceFailures counts the checks that did not pass. It is a field
	// rather than a line in the degradations because a degradation is a fact
	// about the capture and this is a fact about the proof.
	DevidenceFailures int `json:"proofCheckFailures"`

	// Absences is the three-absences rendering, kept separate because it is the
	// part a reviewer has to be able to check at a glance.
	Absences pocAbsences `json:"absences"`
}

// pocAbsences renders the three absences as three distinct facts.
//
// A field that can hold any of the three, or that is simply absent, cannot be
// checked. Three named booleans and a sentence each can be.
type pocAbsences struct {
	NoObservation        bool   `json:"noObservation"`
	NoObservationWhy     string `json:"noObservationWhy,omitempty"`
	DegradedObservation  bool   `json:"degradedObservation"`
	DegradedWhy          string `json:"degradedWhy,omitempty"`
	InsufficientEvidence bool   `json:"insufficientEvidence"`
	InsufficientWhy      string `json:"insufficientWhy,omitempty"`
	// IsNegative says whether any of the three was treated as a negative answer.
	// It is false here, and a run that could not keep it false would be a bug in
	// the run rather than a finding about the camera.
	IsNegative bool `json:"treatedAsNegative"`
}

type pocSource struct {
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
	Transport  string `json:"transport"`
	Resolution string `json:"resolution,omitempty"`
	FrameRate  int    `json:"frameRate,omitempty"`
	ClockRate  int    `json:"clockRate,omitempty"`
	// Profile and Level are the stream's own coding configuration, read from its
	// SPS rather than asked of the camera. The camera's description is not
	// trusted for geometry and is not trusted for this either.
	Profile int `json:"profile,omitempty"`
	Level   int `json:"level,omitempty"`
	// DeclaredGeometry says whether resolution and frame rate came from the
	// camera's own description or from the nominal rate a file replay was told
	// to assume. Blanking a field is a gap; mislabelling one is a lie.
	DeclaredGeometry bool `json:"declaredGeometry"`
}

type pocClip struct {
	Bytes         int           `json:"bytes"`
	MediaType     string        `json:"mediaType"`
	Duration      time.Duration `json:"duration"`
	Width         int           `json:"width"`
	Height        int           `json:"height"`
	Profile       int           `json:"profile"`
	Level         int           `json:"level"`
	Pictures      int           `json:"pictures"`
	KeyFrames     int           `json:"keyFrames"`
	PreRollPics   int           `json:"preRollPictures"`
	PreRollSpan   time.Duration `json:"preRollSpan"`
	PreRollReason string        `json:"preRollReason,omitempty"`
	CapReason     string        `json:"capReason,omitempty"`
	StopReason    string        `json:"stopReason,omitempty"`
	DamagedReason string        `json:"damagedReason,omitempty"`
	WallStart     time.Time     `json:"wallStart,omitempty"`
	WallEnd       time.Time     `json:"wallEnd,omitempty"`
}

type pocEvidence struct {
	StorageRef string `json:"storageRef"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
	MediaType  string `json:"mediaType"`
	// HashVerified is the store's own statement that the bytes it handed back
	// were the bytes it hashed, not a promise by this command.
	HashVerified bool `json:"hashVerified"`
}

// pocIncident is the Stage 1 artefact. It is NOT a disposition.
//
// A disposition is an operator's answer, and there is no operator in this run.
// The incident is therefore left open, with a severity of "query" - which is
// what an unverified novelty score is - and the reason it was raised is named.
type pocIncident struct {
	IncidentID string      `json:"incidentId"`
	TenantID   string      `json:"tenantId"`
	SiteID     string      `json:"siteId"`
	State      string      `json:"state"`
	Severity   string      `json:"severity"`
	Title      string      `json:"title"`
	ReasonCode string      `json:"reasonCode"`
	Reason     string      `json:"reason"`
	RaisedAt   time.Time   `json:"raisedAt"`
	EventID    string      `json:"eventId"`
	Evidence   pocEvidence `json:"evidence"`
	// DecidedWithoutAIObservation is the single most important field in this
	// document and it is a boolean that is always true in this build.
	DecidedWithoutAIObservation bool      `json:"decidedWithoutAiObservation"`
	AwaitingDisposition         bool      `json:"awaitingOperatorDisposition"`
	ObservationID               string    `json:"observationId,omitempty"`
	ObservationDegraded         bool      `json:"observationDegraded"`
	ArtifactPath                string    `json:"artifactPath,omitempty"`
	WrittenAt                   time.Time `json:"writtenAt,omitempty"`
}

type pocObservation struct {
	ObservationID string `json:"observationId"`
	// IDMintedLocally records that the id was generated by this run rather than
	// returned by a database. It is a field rather than a comment because the
	// artefact outlives the run, and an identifier that looks assigned and is
	// not is the kind of thing a later reader cannot check.
	IDMintedLocally      bool      `json:"idMintedLocally"`
	Degraded             bool      `json:"degraded"`
	DegradedReason       string    `json:"degradedReason"`
	DegradedDetail       string    `json:"degradedDetail"`
	InsufficientEvidence bool      `json:"insufficientEvidence"`
	Model                string    `json:"model"`
	ModelHash            string    `json:"modelHash"`
	RuntimeVersion       string    `json:"runtimeVersion"`
	ProfileID            string    `json:"profileId"`
	InputEvidenceHashes  []string  `json:"inputEvidenceHashes"`
	CreatedAt            time.Time `json:"createdAt"`
	ArtifactPath         string    `json:"artifactPath,omitempty"`
}

// frameSource produces complete coded pictures.
//
// Both sources are pictures rather than NAL units because picture boundaries are
// the detector's unit of measurement: a score computed per NAL is a score
// computed per fragment, and a fragmented IDR is four or five of them.
type frameSource interface {
	source() pocSource
	// next returns the next complete picture, or an error at the end of the
	// stream.
	next() (*mp4.Picture, time.Time, error)
	close() error
}

func openSource(ctx context.Context, o pocOptions) (frameSource, error) {
	if o.stream != "" {
		return openFileSource(o)
	}
	return openRTSPSource(ctx, o)
}

// fileSource replays a recorded elementary stream.
//
// This is the path that makes the build reproducible. A capture path that only
// works against a live camera is one whose failures cannot be told apart from
// the camera's, and this build has to be demonstrable on a laptop.
type fileSource struct {
	reader *mp4.PictureReader
	file   *os.File
	step   time.Duration
	start  time.Time
	// emitted counts the pictures read, which is what the wall clock is
	// synthesised from.
	emitted int64
	src     pocSource
}

func openFileSource(o pocOptions) (*fileSource, error) {
	f, err := os.Open(o.stream)
	if err != nil {
		return nil, fmt.Errorf("poc: open --stream: %w", err)
	}
	fs := &fileSource{
		file:  f,
		step:  time.Duration(float64(time.Second) / o.fps),
		start: time.Now().UTC(),
	}
	fs.reader = mp4.ReadPictures(f, o.fps, mp4.DefaultTimescale)
	abs, _ := filepath.Abs(o.stream)
	fs.src = pocSource{
		Kind: "file", Endpoint: abs, Transport: "none",
		FrameRate: int(o.fps), ClockRate: mp4.DefaultTimescale,
		// A file carries no geometry and no timing. Both are supplied by flags,
		// and the field says so.
		DeclaredGeometry: false,
	}
	return fs, nil
}

func (s *fileSource) source() pocSource { return s.src }
func (s *fileSource) close() error {
	return s.file.Close()
}

func (s *fileSource) next() (*mp4.Picture, time.Time, error) {
	p, err := s.reader.Next()
	if p == nil {
		if err == nil {
			err = io.EOF
		}
		return nil, time.Time{}, err
	}
	// A recorded stream has no wall clock of its own. The capture session's own
	// clock is used, so a clip's timestamps are real times of a real run rather
	// than times invented for the replay.
	wall := s.start.Add(time.Duration(s.emitted) * s.step)
	return p, wall, nil
}

// rtspSource reads a live camera.
type rtspSource struct {
	client  *rtsp.Client
	builder mp4.PictureBuilder
	clock   rtsp.Clock
	src     pocSource
}

func openRTSPSource(ctx context.Context, o pocOptions) (*rtspSource, error) {
	c, err := rtsp.Dial(ctx, o.camera, rtsp.Options{
		Timeout: o.timeout, Transport: rtsp.TransportTCPInterleaved,
	})
	if err != nil {
		return nil, err
	}
	s := &rtspSource{client: c}
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	if _, err = c.Options(ctx); err != nil {
		return nil, err
	}
	if _, err = c.Describe(ctx); err != nil {
		return nil, err
	}
	desc := c.Description()
	s.src = pocSource{
		Kind: "rtsp", Endpoint: c.URL().String(), Transport: rtsp.TransportTCPInterleaved + " interleaved",
		FrameRate: desc.FPS, ClockRate: desc.ClockRate, DeclaredGeometry: desc.FPS > 0,
	}
	if desc.Width > 0 && desc.Height > 0 {
		s.src.Resolution = fmt.Sprintf("%dx%d", desc.Width, desc.Height)
	}
	return s, nil
}

func (s *rtspSource) source() pocSource { return s.src }

func (s *rtspSource) close() error {
	return s.client.Close()
}

func (s *rtspSource) next() (*mp4.Picture, time.Time, error) {
	for {
		nal, _, ts, err := s.client.ReadNAL()
		if err != nil {
			if done := s.builder.Flush(); done != nil {
				return done, time.Now().UTC(), nil
			}
			return nil, time.Time{}, err
		}
		wall := time.Now().UTC()
		// The RTP clock is 32 bits and wraps every 49.7 hours, which a camera
		// left running for three weeks hits several times. Unwrapping it is not
		// an optimisation: a clip whose timestamps jump backwards halfway
		// through is a clip a player cannot render.
		if done := s.builder.Push(nal, s.clock.Next(ts), wall); done != nil {
			return done, wall, nil
		}
	}
}
