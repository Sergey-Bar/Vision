package observations

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// The query idempotency key.
//
// The plan's P2.8: "The idempotency key is derived from camera, window and
// prompt version. The worker checks for an existing observation before querying."
//
// The cost that motivates it is concrete. An inference is about fifteen cents. A
// worker that crashes after paying and before recording, and then retries, pays
// twice for the same answer - and a supervised service that restarts on a timer
// can do it on a loop.
//
// # Why this needs no new column
//
// The obvious way to make a key is to store it, and storing it means a migration
// and a new indexed column. It is not necessary, because everything the key is
// derived from is *already* a column on the row: `event_id` identifies the
// camera and window, `profile_id` and `prompt_version` identify what was asked,
// and `input_evidence_hashes` identify the clip the question was about.
//
// So the key is a *derived* value and the check is an indexed lookup on columns
// that were always there. A key that is stored can drift from the fields it was
// derived from; a key that is computed cannot.
//
// # What it is not
//
// It is not the observation's identity. Observations deliberately carry a random
// id, because an observation is a *run* of a model over evidence and two runs are
// two observations - that is what supersession is for. The query key is narrower:
// it identifies the question, not the answer.

// QueryKey identifies one inference request: the same evidence, the same
// profile and the same prompt, asked again.
type QueryKey struct {
	TenantID      string
	EventID       string
	ProfileID     string
	PromptVersion string
	// InputEvidenceHashes identify the clip. A window whose pre-roll changed is a
	// different question even at the same timestamps, and re-asking it is correct.
	InputEvidenceHashes []string
}

// String renders the key as a stable digest.
//
// Sorted hashes, because the order in which evidence was attached is not a
// property of the question. Two workers that assemble the same window from the
// same stored facts in a different order asked the same thing, and a key that
// treated them as different would pay twice for one answer.
//
// Hashed rather than assembled into a readable string, because the inputs are
// already hashes and a second layer of escaping produces a value nobody can
// eyeball in a log and nobody can write down.
func (k QueryKey) String() string {
	h := sha256.New()
	for _, part := range []string{
		"query/1",
		k.TenantID,
		k.EventID,
		k.ProfileID,
		k.PromptVersion,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	hashes := append([]string(nil), k.InputEvidenceHashes...)
	sort.Strings(hashes)
	for _, s := range hashes {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AlreadyQueried reports whether this exact question has already been answered.
//
// It is a pure function over the observations a caller already has, not a query,
// and that is deliberate: the rule that decides whether to spend money must be
// inspectable without a database and without a network, because the day it is
// wrong the company pays for the mistake at fifteen cents a call.
//
// Two rows are excluded, and both exclusions are the difference between a
// working check and a harmful one:
//
//   - **Degraded rows.** A cap that was reached, a provider that was down, a
//     region that refused - none of those is an answer. If a failed query counted
//     as completed, a single outage would permanently mark every window in the
//     site as done and the product would never look at any of them again, with no
//     error and an operator staring at a site that looks healthy and has stopped
//     watching since Tuesday.
//   - **Superseded rows.** A row is superseded when some *other* row names it in
//     its SupersedesObservationID. The row that *carries* the flag is the
//     current answer and does count; the row it names is history and does not.
//     Getting that backwards makes a corrected prompt impossible to re-run,
//     because the superseded row looks like the answer.
//
// A row that supersedes an earlier one counts, so re-evaluating after a prompt fix
// produces a *new* observation and the next retry of the same new question is
// recognised.
func AlreadyQueried(key QueryKey, existing []Observation) bool {
	superseded := make(map[string]bool, len(existing))
	for _, o := range existing {
		if o.SupersedesObservationID != "" {
			superseded[o.SupersedesObservationID] = true
		}
	}
	for _, o := range existing {
		if superseded[o.ObservationID] {
			continue
		}
		if o.isDegraded() {
			continue
		}
		if sameQuery(o, key) {
			return true
		}
	}
	return false
}

// isDegraded reports whether this row records a run that produced no answer.
//
// The empty string is treated as *not* degraded, and it has to be: Validate
// defaults an empty Degraded to DegradationNone, so a row read straight out of
// the database with the column left at its default arrives here as "" and would
// otherwise look degraded. Getting that wrong is the worst possible direction -
// every observation would look like a failure, nothing would ever count as
// already asked, and the product would pay fifteen cents for every window on
// every event, forever, with no error anywhere.
func (o Observation) isDegraded() bool {
	return o.Degraded != "" && o.Degraded != DegradationNone
}

func sameQuery(o Observation, key QueryKey) bool {
	return o.TenantID == key.TenantID &&
		o.EventID == key.EventID &&
		o.ProfileID == key.ProfileID &&
		o.PromptVersion == key.PromptVersion &&
		sameHashes(o.InputEvidenceHashes, key.InputEvidenceHashes)
}

func sameHashes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// IdempotencyKey is the column-free lookup a worker performs before it pays.
//
// It is a function rather than a method because it needs no pool, no clock and no
// network, and a decision that requires a database is a decision that is not
// taken when the database is the thing that is down - which is the moment it
// matters most.
func IdempotencyKey(tenantID, cameraID string, windowStart, windowEnd time.Time, profileID, promptVersion string, evidenceHashes []string) QueryKey {
	return QueryKey{
		TenantID:            tenantID,
		EventID:             EventIDFor(cameraID, windowStart, windowEnd),
		ProfileID:           profileID,
		PromptVersion:       promptVersion,
		InputEvidenceHashes: evidenceHashes,
	}
}

// EventIDFor derives the event identity a window of one camera's footage has.
//
// It is the same derivation the canonical event path uses, and it is named here
// so the two cannot drift: a query key built on an event id that is not the one
// the event was stored under is a key that never matches anything, and a
// never-matching key is an idempotency check that always pays.
func EventIDFor(cameraID string, windowStart, windowEnd time.Time) string {
	sum := sha256.Sum256([]byte("event/1\x00" + cameraID + "\x00" +
		windowStart.UTC().Format(time.RFC3339Nano) + "\x00" +
		windowEnd.UTC().Format(time.RFC3339Nano)))
	return "evt_" + hex.EncodeToString(sum[:12])
}

// DescribeKey renders the key's inputs for a log line, so an operator can see
// *why* a retry was skipped rather than only that it was.
//
// It is a fixed key=value shape for the repository's own reason: nothing matches
// on prose, and a diagnostic a human reads is allowed to be a sentence.
func DescribeKey(k QueryKey) string {
	return strings.Join([]string{
		"event=" + k.EventID,
		"profile=" + k.ProfileID,
		"prompt=" + k.PromptVersion,
		"evidence=" + strings.Join(k.InputEvidenceHashes, ","),
	}, " ")
}
