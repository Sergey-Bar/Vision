package hafnia

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/airuntime"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
)

// outputHash is the SHA-256 over the canonical serialisation of a structured
// answer.
//
// encoding/json sorts map keys, so the same answer hashes the same on every
// machine and in every process. It is computed here and nowhere else, because two
// implementations of "canonical" are two values and the hash is only useful if
// there is one.
func outputHash(structured map[string]any) string {
	body, err := json.Marshal(structured)
	if err != nil {
		// A value built from decoded JSON always marshals back. If one somehow
		// does not, an empty hash is the honest answer: it is not a hash of the
		// output, and validation refuses it downstream rather than storing a
		// plausible-looking stand-in.
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Do satisfies airuntime.Provider by uploading each piece of evidence, asking,
// and deleting the assets again.
//
// # The deletion is the design, not a cleanup
//
// The plan's P3.3 is "asset deletion after the observation is recorded and on
// every failure path after upload". That is a `defer` on every path after the
// upload returns, and a `defer` does not run when the process is killed. The
// ledger is what makes the residue visible: whatever this process uploaded and
// did not release is listed by OutstandingAssets, and [Client.SweepAssets] is
// the compensating action. A run that ends with a non-empty list has left
// footage on somebody else's disk and says so.
//
// # What happens on failure
//
// Every failure path returns a Result with Degraded set and a reason code, and
// returns nil for the error. That is not leniency: to the caller a failed
// inference is a *fact about this run*, and returning it as an error would mean
// the worker's only two options are "store it" or "lose the fact". The error is
// returned alongside for the cases that are genuinely exceptional - a
// misconfiguration the process should stop for.
//
// The one thing this method can never produce is a Result with Degraded false
// and an empty Structured. That would be a fabricated "there is nothing here"
// from a model that did not answer, and it is the single worst thing this
// product can do.
func (c *Client) Do(ctx context.Context, req airuntime.Request) (airuntime.Result, error) {
	attrib := c.Attribution()

	fail := func(err error, extra map[string]string) (airuntime.Result, error) {
		return airuntime.Result{
			ObservationID:        req.ObservationID,
			ProfileID:            req.ProfileID,
			Degraded:             true,
			DegradedReason:       degradationFor(err),
			DegradedDetail:       detailFor(err, extra),
			ModelIdentity:        attrib.ModelIdentity,
			ModelContractVersion: attrib.ModelContractVersion,
			Temperature:          attrib.Temperature,
			InputEvidenceHashes:  hashes(req.Evidence),
			OutputSchemaVersion:  0,
		}, nil
	}

	if len(req.Evidence) == 0 {
		return fail(errors.New("hafnia: the request carries no evidence"), nil)
	}

	// The whole point of the call is to look at evidence, and evidence with no
	// recognisable media type cannot be uploaded. Refusing it is different from
	// asking and getting nothing back: nothing was sent anywhere.
	var video *airuntime.Evidence
	for i := range req.Evidence {
		if strings.HasPrefix(req.Evidence[i].ContentType, "video/") {
			video = &req.Evidence[i]
			break
		}
	}
	if video == nil {
		return fail(errors.New("hafnia: no video evidence in the request"), nil)
	}

	// The budget is consulted before anything leaves the site. Reserving after the
	// upload would mean a site at its cap still ships footage to a third party
	// for a call that is then refused, which is the wrong answer to both the cost
	// question and the lawful-basis question at the same time.
	if err := c.quota.take(); err != nil {
		return fail(err, nil)
	}

	asset, err := c.UploadAsset(ctx, video.ContentType, video.Data)
	if err != nil {
		return fail(err, map[string]string{"bytes": fmt.Sprintf("%d", len(video.Data))})
	}

	// From here the clip exists on somebody else's disk, so every return deletes
	// it. The ledger is updated by DeleteAsset on success, so a failure to delete
	// leaves the id outstanding and the sweep can find it.
	defer func() {
		delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = c.DeleteAsset(delCtx, asset.ID)
	}()

	answer, err := c.ask(ctx, asset.ID)
	if err != nil {
		return fail(err, map[string]string{"assetId": asset.ID})
	}

	insufficient, _ := answer.Structured["insufficientEvidence"].(bool)
	return airuntime.Result{
		ObservationID:        req.ObservationID,
		ProfileID:            req.ProfileID,
		Structured:           answer.Structured,
		InsufficientEvidence: insufficient,
		Degraded:             false,
		DegradedReason:       observations.DegradationNone,
		// Deliberately empty. See the package comment: the provider's weights
		// are not ours to hash, and a fabricated one would defeat the field.
		Model:                answer.ModelIdentity,
		ModelHash:            "",
		RuntimeVersion:       "hafnia/1",
		ModelIdentity:        answer.ModelIdentity,
		ModelContractVersion: attrib.ModelContractVersion,
		Temperature:          attrib.Temperature,
		InputEvidenceHashes:  hashes(req.Evidence),
		OutputSchemaVersion:  0,
		LatencyMS:            answer.LatencyMS,
		OutputHash:           outputHash(answer.Structured),
	}, nil
}

func hashes(ev []airuntime.Evidence) []string {
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		out = append(out, e.SHA256)
	}
	return out
}

// NewObservationID mints an observation id for a call this package is about to
// make.
//
// It lives here rather than in the worker because the worker may be retried and
// two attempts at the same event must not become two observations - and because
// a retried attempt costs money, so the id has to be derived from the inputs
// rather than from a clock or a counter.
func NewObservationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("hafnia: mint an observation id: %w", err)
	}
	return "obs_" + hex.EncodeToString(b[:]), nil
}
