package vlm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Sergey-Bar/vision/shared/aistate"
)

// Observe asks one question about one clip and returns an observation.
//
// This is the whole of the call path, in this order, and the order is the point:
//
//  1. the prompt is resolved by INCIDENT CLASS, so the class is a file and never a
//     field, and the DETECTED class is substituted into it
//  2. the budget is reserved BEFORE anything leaves the site
//  3. the clip is uploaded
//  4. the asset is scheduled for deletion on a context that survives the caller going
//     away, so a clip is deleted on every path after the upload
//  5. the completion is asked for
//
// `promptID` is the incident class, which question is being asked, and
// `detectedClass` is what the local pipeline measured the object to be. They are
// SEPARATE arguments because they are separate facts and conflating them is the bug
// this signature exists to prevent: a detector that confidently said `bird` and a
// novelty run that measured a change and named nothing ask the same question about
// different evidence, and the prompt has to say which of the two produced the clip. A
// single argument carrying either would send the model a false statement about its own
// evidence.
//
// Every failure produces an observation with a non-answered state and [VerdictNone].
// There is no path through this function that returns a verdict without an answered ask,
// and there is no path that returns no observation at all, because a caller that gets
// nothing cannot record that it got nothing.
func (c *Client) Observe(ctx context.Context, detectionID, promptID, detectedClass string, clip []byte, mediaType string) Observation {
	prompt, err := c.Prompts().GetFor(promptID, detectedClass)
	if err != nil {
		return refused(detectionID, aistate.NotAsked, detectedClass, string(ReasonNoPrompt), oneLine(err))
	}
	if len(clip) == 0 {
		return refused(detectionID, aistate.NotAsked, detectedClass, string(ReasonNoClip),
			"there is no clip to send, so the model was not asked")
	}
	// The budget is consulted BEFORE the upload. A site that has reached its cap
	// must not ship footage to a third party for a call that will be refused.
	if err := c.quota.take(); err != nil {
		return refused(detectionID, aistate.NotAsked, detectedClass, string(ReasonFor(err)), oneLine(err))
	}

	asset, err := c.UploadAsset(ctx, mediaType, clip)
	if err != nil {
		return c.failed(detectionID, detectedClass, prompt, asset, err)
	}
	// The delete runs on a context detached from the caller's, with a deadline
	// of its own. A deferred delete on the caller's context does not run if the
	// run is cancelled — which is exactly when footage is most likely to be
	// abandoned — and the footage would stay on somebody else's disk with nobody
	// left looking for it.
	obs, err := c.ask(ctx, asset.ID, prompt)
	defer c.deleteDetached(asset.ID)
	if err != nil {
		return c.failed(detectionID, detectedClass, prompt, asset, err)
	}
	obs.DetectionID = detectionID
	obs.Finalise()
	return obs
}

// deleteDetached removes an asset on a context that is not the caller's.
func (c *Client) deleteDetached(id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), c.cfg.Timeout)
	defer cancel()
	if err := c.DeleteAsset(ctx, id); err != nil {
		// A delete that fails is NOT silent. The asset stays in the ledger, so
		// SweepAssets still knows about it, and the operator's next run sweeps
		// it. Swallowing the error would leave the ledger empty and the footage
		// on a third party's disk.
		c.logf("vlm: the asset %s could not be deleted and remains outstanding for the next sweep: %v", id, err)
	}
}

// logf is nil-tolerant: a client with no logger is a client in a test.
func (c *Client) logf(format string, args ...any) {
	if c.logFn != nil {
		c.logFn(format, args...)
	}
}

// failed maps a transport failure to its ask state.
//
// The mapping is one function because it is the whole of the fault matrix: 429,
// 500, 401, a malformed body, two choices, prose where JSON was required, empty
// content, an asset that went missing, a redirect and a timeout each produce a
// DISTINCT state, and none of them fabricates an answer. A test asserts the
// states differ — which is the assertion that will go red the day somebody
// collapses two of them into one.
func (c *Client) failed(detectionID, detectedClass string, prompt Prompt, asset Asset, err error) Observation {
	obs := refused(detectionID, stateForError(err), detectedClass, string(ReasonFor(err)), oneLine(err))
	obs.PromptID = prompt.ID
	obs.PromptVersion = prompt.Version
	obs.ContractVersion = ContractVersionFor(prompt, c.cfg.OutputSchemaVersion, c.cfg.APIModelName, c.cfg.Region)
	obs.Temperature = c.cfg.Temperature
	if asset.ID != "" {
		obs.AssetID = asset.ID
	}
	obs.Finalise()
	return obs
}

// refused builds a non-answered observation. It cannot produce a verdict: the
// normalisation is the only thing that ever clears one, and it is called here so
// that there is no path into an Observation struct that skips it.
func refused(detectionID, state, class, reason, message string) Observation {
	o := Observation{
		DetectionID: detectionID,
		State:       state,
		Reason:      reason,
		Message:     message,
	}
	o.SetState(state)
	return o
}

// stateForError is the fault matrix, and it is a table rather than an if-chain so
// that adding a failure mode is a new row and not a new branch somewhere.
func stateForError(err error) string {
	switch {
	case err == nil:
		return aistate.Asked
	case QuotaExhausted(err):
		// We did not ask. A capped site is not a degraded answer.
		return aistate.NotAsked
	case errors.Is(err, ErrCrossRegion):
		// We did not ask, and deliberately: the footage stays on site.
		return aistate.NotAsked
	case errors.Is(err, ErrRedirectRefused):
		// Something tried to move the call, and it did not happen. A question
		// nobody was asked is not an answer that came back wrong.
		return aistate.NotAsked
	case errors.Is(err, ErrAssetNotFound):
		// The ask happened and could not be read.
		return aistate.CannotTell
	case errors.Is(err, ErrMalformedAnswer):
		// The ask happened and the answer was not usable. This is the one that
		// most easily becomes a partial parse, and it is refused as a whole.
		return aistate.CannotTell
	case errors.Is(err, ErrRegionUnavailable):
		// The answer is unknown, not empty. `degraded` exists so that this is
		// never confused with a frame that genuinely had nothing in it.
		return aistate.Degraded
	default:
		return aistate.Degraded
	}
}

// oneLine is the first line of an error, for a field with a fixed shape.
func oneLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}

// ReasonCode is the machine-readable cause of a degradation.
//
// It exists because the aistate vocabulary is a closed set with five members and
// the five most important transport failures are not five distinct words. So the
// category is shared and the cause is not: every degraded result carries a
// reason code in a fixed key=value shape, so a consumer parses a field rather
// than matching prose.
type ReasonCode string

// The reason codes this client can produce.
const (
	ReasonNone              ReasonCode = "none"
	ReasonQuotaExhausted    ReasonCode = "cap_reached"
	ReasonRegionRefused     ReasonCode = "region_refused"
	ReasonRegionUnavailable ReasonCode = "region_unavailable"
	ReasonAnswerMalformed   ReasonCode = "answer_malformed"
	ReasonAssetMissing      ReasonCode = "asset_missing"
	ReasonRedirectRefused   ReasonCode = "redirect_refused"
	ReasonNoClip            ReasonCode = "no_clip"
	ReasonNoPrompt          ReasonCode = "no_prompt"
	ReasonError             ReasonCode = "error"
)

// ReasonFor maps a transport error to its code.
func ReasonFor(err error) ReasonCode {
	switch {
	case err == nil:
		return ReasonNone
	case QuotaExhausted(err):
		return ReasonQuotaExhausted
	case errors.Is(err, ErrCrossRegion):
		return ReasonRegionRefused
	case errors.Is(err, ErrRegionUnavailable):
		return ReasonRegionUnavailable
	case errors.Is(err, ErrMalformedAnswer):
		return ReasonAnswerMalformed
	case errors.Is(err, ErrAssetNotFound):
		return ReasonAssetMissing
	case errors.Is(err, ErrRedirectRefused):
		return ReasonRedirectRefused
	default:
		return ReasonError
	}
}

// Detail renders the machine-readable detail for a failure: `reason=<code>` and
// nothing else. The repository's own rule is that nothing matches on prose, and
// a sentence built with fmt.Sprintf is prose.
func Detail(err error) string {
	if err == nil {
		return "reason=" + string(ReasonNone)
	}
	return "reason=" + string(ReasonFor(err))
}

// ReadClip reads a clip from disk for [Client.Observe].
//
// It refuses a zero-byte file for the same reason the evidence store does:
// evidence that shows nothing is not evidence.
func ReadClip(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("vlm: cannot read the clip at %s: %w", path, err)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("vlm: the clip at %s is zero bytes; there is nothing to ask about", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vlm: cannot read the clip at %s: %w", path, err)
	}
	return data, nil
}

// CopyTo is used by callers that hold an open reader rather than a path.
func CopyTo(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes))
	if err != nil {
		return nil, fmt.Errorf("vlm: cannot read the clip: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("vlm: the clip is empty; there is nothing to ask about")
	}
	return data, nil
}
